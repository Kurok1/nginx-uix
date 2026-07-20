/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kuroky/nginx-uix/internal/config"
)

// HasOpenAttentionCases reports whether production consistency remains uncertain.
func (d *DB) HasOpenAttentionCases(ctx context.Context) (bool, error) {
	if ctx == nil {
		return false, errors.New("check open attention cases: context is required")
	}
	var exists int
	err := d.sql.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM config_attention_cases WHERE state = ? LIMIT 1
		)`, config.AttentionCaseOpen).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check open attention cases: %w", err)
	}
	return exists == 1, nil
}

const (
	recoveryPageLimit        = 100
	retentionBackupReadLimit = 4096
)

// ProductionLease returns the current durable production-operation owner, if any.
func (d *DB) ProductionLease(ctx context.Context) (config.ProductionLease, error) {
	var ownerType, ownerID, acquiredAt sql.NullString
	if err := d.sql.QueryRowContext(ctx, `SELECT owner_type, owner_id, acquired_at
		FROM config_production_lease WHERE singleton = 1`).Scan(&ownerType, &ownerID, &acquiredAt); err != nil {
		return config.ProductionLease{}, fmt.Errorf("read production lease: %w", err)
	}
	if !ownerType.Valid && !ownerID.Valid && !acquiredAt.Valid {
		return config.ProductionLease{}, nil
	}
	if !ownerType.Valid || !ownerID.Valid || !acquiredAt.Valid ||
		!validProductionOwner(config.ProductionOperationKind(ownerType.String), ownerID.String) {
		return config.ProductionLease{}, fmt.Errorf("read production lease: invalid metadata")
	}
	parsed, err := parseTime("production lease acquisition", acquiredAt.String)
	if err != nil {
		return config.ProductionLease{}, fmt.Errorf("read production lease: %w", err)
	}
	return config.ProductionLease{
		OwnerType: config.ProductionOperationKind(ownerType.String), OwnerID: ownerID.String, AcquiredAt: parsed,
	}, nil
}

// AcquireProductionLease grants the empty single-writer slot to one exact operation.
func (d *DB) AcquireProductionLease(
	ctx context.Context,
	ownerType config.ProductionOperationKind,
	ownerID string,
	acquiredAt time.Time,
) error {
	if !validProductionOwner(ownerType, ownerID) || acquiredAt.IsZero() {
		return fmt.Errorf("acquire production lease: invalid input")
	}
	result, err := d.sql.ExecContext(ctx, `UPDATE config_production_lease
		SET owner_type = ?, owner_id = ?, acquired_at = ?
		WHERE singleton = 1 AND owner_type IS NULL AND owner_id IS NULL AND acquired_at IS NULL`,
		ownerType, ownerID, formatTime(acquiredAt),
	)
	if err != nil {
		return fmt.Errorf("acquire production lease: %w", err)
	}
	matched, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("acquire production lease: read affected rows: %w", err)
	}
	if matched != 1 {
		return fmt.Errorf("acquire production lease: %w", config.ErrOperationInProgress)
	}
	return nil
}

// ReleaseProductionLease clears the slot only for its exact current owner.
func (d *DB) ReleaseProductionLease(ctx context.Context, ownerType config.ProductionOperationKind, ownerID string) error {
	if !validProductionOwner(ownerType, ownerID) {
		return fmt.Errorf("release production lease: invalid input")
	}
	result, err := d.sql.ExecContext(ctx, `UPDATE config_production_lease
		SET owner_type = NULL, owner_id = NULL, acquired_at = NULL
		WHERE singleton = 1 AND owner_type = ? AND owner_id = ?`, ownerType, ownerID)
	if err != nil {
		return fmt.Errorf("release production lease: %w", err)
	}
	matched, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("release production lease: read affected rows: %w", err)
	}
	if matched != 1 {
		return fmt.Errorf("release production lease: %w", config.ErrConflict)
	}
	return nil
}

func validProductionOwner(ownerType config.ProductionOperationKind, ownerID string) bool {
	switch ownerType {
	case config.ProductionOperationRelease:
		_, err := config.ParseReleaseID(ownerID)
		return err == nil
	case config.ProductionOperationRestore:
		_, err := config.ParseRestoreID(ownerID)
		return err == nil
	case config.ProductionOperationRestart:
		_, err := config.ParseRestartID(ownerID)
		return err == nil
	case config.ProductionOperationRetention:
		_, err := config.ParseRetentionRunID(ownerID)
		return err == nil
	default:
		return false
	}
}

// ListBackups returns one bounded keyset page ordered newest first.
func (d *DB) ListBackups(ctx context.Context, query config.BackupQuery) (backups []config.Backup, returnErr error) {
	if query.Limit <= 0 || query.Limit > recoveryPageLimit ||
		(query.BeforeCreatedAt.IsZero() != (query.BeforeID == "")) {
		return nil, fmt.Errorf("list backups: invalid query")
	}
	if query.BeforeID != "" {
		if _, err := config.ParseBackupID(string(query.BeforeID)); err != nil {
			return nil, fmt.Errorf("list backups: %w", err)
		}
	}
	statement := backupSelect + " WHERE 1 = 1"
	arguments := make([]any, 0, 4)
	if !query.IncludeDeleted {
		statement += " AND state <> 'deleted'"
	}
	if query.BeforeID != "" {
		statement += " AND (created_at < ? OR (created_at = ? AND id < ?))"
		formatted := formatTime(query.BeforeCreatedAt)
		arguments = append(arguments, formatted, formatted, query.BeforeID)
	}
	statement += " ORDER BY created_at DESC, id DESC LIMIT ?"
	arguments = append(arguments, query.Limit)
	rows, err := d.sql.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	backups = make([]config.Backup, 0, query.Limit)
	for rows.Next() {
		backup, err := scanBackup(rows)
		if err != nil {
			return nil, fmt.Errorf("list backups: %w", err)
		}
		backups = append(backups, backup)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	return backups, nil
}

// RetentionBackups returns all bounded complete, present backups in deterministic oldest-first order.
func (d *DB) RetentionBackups(ctx context.Context) (backups []config.Backup, returnErr error) {
	rows, err := d.sql.QueryContext(ctx, backupSelect+` WHERE state = 'complete' AND body_present = 1
		ORDER BY created_at ASC, id ASC LIMIT ?`, retentionBackupReadLimit+1)
	if err != nil {
		return nil, fmt.Errorf("list retention backups: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	backups = make([]config.Backup, 0)
	for rows.Next() {
		backup, err := scanBackup(rows)
		if err != nil {
			return nil, fmt.Errorf("list retention backups: %w", err)
		}
		backups = append(backups, backup)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list retention backups: %w", err)
	}
	if len(backups) > retentionBackupReadLimit {
		return nil, fmt.Errorf("list retention backups: %w", config.ErrLimitExceeded)
	}
	return backups, nil
}

// ChangeBackupProtection applies an exact manual-protection CAS with linked audit evidence.
func (d *DB) ChangeBackupProtection(ctx context.Context, change config.BackupProtectionChange) (config.Backup, error) {
	if _, err := config.ParseBackupID(string(change.BackupID)); err != nil ||
		change.ExpectedProtected == change.NextProtected ||
		change.Actor.UserID <= 0 || change.Actor.RequestID == "" ||
		change.Actor.UserID != change.Audit.ActorUserID || change.Actor.RequestID != change.Operation.RequestID ||
		change.Actor.RequestID != change.Audit.RequestID ||
		(change.NextProtected && !validRecoveryReason(change.Reason)) || (!change.NextProtected && change.Reason != "") {
		return config.Backup{}, fmt.Errorf("change backup protection: invalid input")
	}
	if err := validateOperationAudit(change.Operation, change.Audit); err != nil ||
		change.Operation.ObjectType != "config_backup" || change.Operation.ObjectID != string(change.BackupID) {
		return config.Backup{}, fmt.Errorf("change backup protection: invalid audit evidence")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		current, err := scanBackup(connection.QueryRowContext(ctx, backupSelect+" WHERE id = ?", change.BackupID))
		if err != nil {
			return err
		}
		if current.ManuallyProtected != change.ExpectedProtected || current.State != config.BackupStateComplete || !current.BodyPresent {
			return config.ErrConflict
		}
		var reason any
		var protectedBy any
		var protectedAt any
		if change.NextProtected {
			reason = change.Reason
			protectedBy = change.Actor.UserID
			protectedAt = formatTime(change.Operation.OccurredAt)
		} else {
			reason = ""
			protectedBy = nil
			protectedAt = nil
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_backups SET
			manually_protected = ?, protection_reason = ?, protected_by = ?, protected_at = ?
			WHERE id = ? AND manually_protected = ? AND state = 'complete' AND body_present = 1`,
			boolInteger(change.NextProtected), reason, protectedBy, protectedAt,
			change.BackupID, boolInteger(change.ExpectedProtected),
		)
		if err != nil {
			return mapConfigConstraint("change backup protection", err)
		}
		matched, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if matched != 1 {
			return config.ErrConflict
		}
		return insertOperationAndAudit(ctx, connection, change.Operation, change.Audit)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return config.Backup{}, fmt.Errorf("change backup protection: %w", fs.ErrNotExist)
		}
		return config.Backup{}, fmt.Errorf("change backup protection: %w", err)
	}
	return d.Backup(ctx, change.BackupID)
}

// CreateRetentionRun atomically stores one deterministic plan and its immutable decisions.
func (d *DB) CreateRetentionRun(ctx context.Context, run config.RetentionRun, items []config.RetentionItem) error {
	if err := validateRetentionRun(run); err != nil || run.State != config.RetentionRunPlanned || len(items) > retentionBackupReadLimit {
		return fmt.Errorf("create retention run: invalid plan")
	}
	for index, item := range items {
		if err := validateRetentionItem(item); err != nil || item.RunID != run.ID || item.Ordinal != index {
			return fmt.Errorf("create retention run: invalid item")
		}
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if _, err := connection.ExecContext(ctx, `INSERT INTO config_retention_runs(
			id, state, minimum_complete, maximum_complete, maximum_total_bytes, minimum_age_seconds,
			backup_count, total_bytes, protected_count, delete_count, delete_bytes, deleted_count,
			deleted_bytes, last_error_code, created_by, request_id, execution_request_id,
			created_at, expires_at, started_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, run.State, run.Policy.MinimumComplete, run.Policy.MaximumComplete,
			run.Policy.MaximumTotalBytes, int64(run.Policy.MinimumAge/time.Second), run.BackupCount,
			run.TotalBytes, run.ProtectedCount, run.DeleteCount, run.DeleteBytes, run.DeletedCount,
			run.DeletedBytes, run.LastErrorCode, run.CreatedBy, run.RequestID, run.ExecutionRequestID, formatTime(run.CreatedAt),
			formatTime(run.ExpiresAt), nullableTime(run.StartedAt), nullableTime(run.FinishedAt),
		); err != nil {
			return mapConfigConstraint("insert retention run", err)
		}
		for _, item := range items {
			if _, err := connection.ExecContext(ctx, `INSERT INTO config_retention_items(
				run_id, ordinal, backup_id, decision, reason_code, state, snapshot_created_at,
				snapshot_total_bytes, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.RunID, item.Ordinal, item.BackupID,
				item.Decision, item.ReasonCode, item.State, formatTime(item.SnapshotCreatedAt),
				item.SnapshotTotalBytes, formatTime(item.UpdatedAt)); err != nil {
				return mapConfigConstraint("insert retention item", err)
			}
		}
		details, err := json.Marshal(struct {
			BackupCount int   `json:"backup_count"`
			DeleteCount int   `json:"delete_count"`
			DeleteBytes int64 `json:"delete_bytes"`
		}{run.BackupCount, run.DeleteCount, run.DeleteBytes})
		if err != nil {
			return err
		}
		operation := config.OperationRecord{
			ID: string(run.ID) + ":plan", ObjectType: "config_retention_run", ObjectID: string(run.ID),
			Action: "config.retention.plan", Result: "planned", RequestID: run.RequestID, OccurredAt: run.CreatedAt,
		}
		return insertOperationAndAudit(ctx, connection, operation, config.AuditEvent{
			OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: run.CreatedBy,
			Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		})
	})
	if err != nil {
		return fmt.Errorf("create retention run: %w", err)
	}
	return nil
}

// RetentionRun returns one plan and all decisions in stable ordinal order.
func (d *DB) RetentionRun(ctx context.Context, id config.RetentionRunID) (_ config.RetentionRun, _ []config.RetentionItem, returnErr error) {
	if _, err := config.ParseRetentionRunID(string(id)); err != nil {
		return config.RetentionRun{}, nil, fmt.Errorf("read retention run: %w", err)
	}
	run, err := scanRetentionRun(d.sql.QueryRowContext(ctx, retentionRunSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.RetentionRun{}, nil, fmt.Errorf("read retention run: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.RetentionRun{}, nil, fmt.Errorf("read retention run: %w", err)
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT run_id, ordinal, backup_id, decision, reason_code,
		state, snapshot_created_at, snapshot_total_bytes, updated_at
		FROM config_retention_items WHERE run_id = ? ORDER BY ordinal ASC`, id)
	if err != nil {
		return config.RetentionRun{}, nil, fmt.Errorf("read retention items: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	items := make([]config.RetentionItem, 0)
	for rows.Next() {
		item, err := scanRetentionItem(rows)
		if err != nil {
			return config.RetentionRun{}, nil, fmt.Errorf("read retention items: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return config.RetentionRun{}, nil, fmt.Errorf("read retention items: %w", err)
	}
	return run, items, nil
}

// TransitionRetentionItem applies one exact per-plan item state CAS.
func (d *DB) TransitionRetentionItem(
	ctx context.Context,
	runID config.RetentionRunID,
	ordinal int,
	expected config.RetentionItemState,
	next config.RetentionItemState,
	updatedAt time.Time,
) error {
	if _, err := config.ParseRetentionRunID(string(runID)); err != nil || ordinal < 0 || expected == next || updatedAt.IsZero() ||
		!knownRetentionItemState(expected) || !knownRetentionItemState(next) {
		return fmt.Errorf("transition retention item: invalid input")
	}
	result, err := d.sql.ExecContext(ctx, `UPDATE config_retention_items SET state = ?, updated_at = ?
		WHERE run_id = ? AND ordinal = ? AND state = ?`, next, formatTime(updatedAt), runID, ordinal, expected)
	if err != nil {
		return fmt.Errorf("transition retention item: %w", err)
	}
	matched, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("transition retention item: read affected rows: %w", err)
	}
	if matched != 1 {
		return fmt.Errorf("transition retention item: %w", config.ErrConflict)
	}
	return nil
}

// BeginRetentionDeletion atomically records one exact tombstone intent after rechecking all durable protections.
func (d *DB) BeginRetentionDeletion(
	ctx context.Context,
	runID config.RetentionRunID,
	ordinal int,
	backupID config.BackupID,
	snapshotCreatedAt time.Time,
	snapshotTotalBytes int64,
	updatedAt time.Time,
) error {
	if !validRetentionDeletionIdentity(runID, backupID) || ordinal < 0 ||
		snapshotCreatedAt.IsZero() || snapshotTotalBytes < 0 || updatedAt.IsZero() {
		return fmt.Errorf("begin retention deletion: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var runState config.RetentionRunState
		var actorID int64
		var requestID string
		var minimumComplete int
		if err := connection.QueryRowContext(ctx, `SELECT state, created_by, request_id, minimum_complete
			FROM config_retention_runs WHERE id = ?`, runID).Scan(
			&runState, &actorID, &requestID, &minimumComplete,
		); err != nil {
			return err
		}
		if runState != config.RetentionRunExecuting {
			return config.ErrConflict
		}
		var itemBackupID config.BackupID
		var decision config.RetentionDecision
		var itemState config.RetentionItemState
		var itemCreatedAt string
		var itemBytes int64
		if err := connection.QueryRowContext(ctx, `SELECT backup_id, decision, state,
			snapshot_created_at, snapshot_total_bytes FROM config_retention_items
			WHERE run_id = ? AND ordinal = ?`, runID, ordinal).Scan(
			&itemBackupID, &decision, &itemState, &itemCreatedAt, &itemBytes,
		); err != nil {
			return err
		}
		if itemBackupID != backupID || decision != config.RetentionDecisionDelete ||
			itemState != config.RetentionItemPlanned || itemCreatedAt != formatTime(snapshotCreatedAt) ||
			itemBytes != snapshotTotalBytes {
			return config.ErrRetentionPlanExpired
		}
		backup, err := scanBackup(connection.QueryRowContext(ctx, backupSelect+" WHERE id = ?", backupID))
		if err != nil {
			return err
		}
		if backup.State != config.BackupStateComplete || !backup.BodyPresent ||
			!backup.CreatedAt.Equal(snapshotCreatedAt.UTC()) || backup.TotalBytes != snapshotTotalBytes {
			return config.ErrRetentionPlanExpired
		}
		protected, err := retentionBackupProtected(ctx, connection, backup, minimumComplete)
		if err != nil {
			return err
		}
		if protected {
			return config.ErrBackupProtected
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_backups SET
			state = 'deleting', delete_run_id = ?, delete_reason = 'retention'
			WHERE id = ? AND state = 'complete' AND body_present = 1
			AND created_at = ? AND total_bytes = ?`, runID, backupID,
			formatTime(snapshotCreatedAt), snapshotTotalBytes)
		if err != nil {
			return mapConfigConstraint("begin retention backup deletion", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrRetentionPlanExpired, err)
		}
		result, err = connection.ExecContext(ctx, `UPDATE config_retention_items
			SET state = 'deleting', updated_at = ?
			WHERE run_id = ? AND ordinal = ? AND state = 'planned'`,
			formatTime(updatedAt), runID, ordinal)
		if err != nil {
			return err
		}
		matched, err = result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		return insertRetentionItemAudit(ctx, connection, runID, ordinal, backupID, actorID,
			requestID, "config.retention.delete.start", "deleting", snapshotTotalBytes, updatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("begin retention deletion: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("begin retention deletion: %w", err)
	}
	return nil
}

// CompleteRetentionDeletion atomically advances one authorized deleting backup to a durable tombstone.
func (d *DB) CompleteRetentionDeletion(
	ctx context.Context,
	runID config.RetentionRunID,
	ordinal int,
	backupID config.BackupID,
	deletedAt time.Time,
) error {
	if !validRetentionDeletionIdentity(runID, backupID) || ordinal < 0 || deletedAt.IsZero() {
		return fmt.Errorf("complete retention deletion: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		actorID, requestID, snapshotBytes, err := retentionDeletionEvidence(
			ctx, connection, runID, ordinal, backupID, config.RetentionItemDeleting,
		)
		if err != nil {
			return err
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_backups SET
			state = 'deleted', body_present = 0, deleted_at = ?
			WHERE id = ? AND state = 'deleting' AND body_present = 1 AND delete_run_id = ?`,
			formatTime(deletedAt), backupID, runID)
		if err != nil {
			return mapConfigConstraint("complete retention backup deletion", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		result, err = connection.ExecContext(ctx, `UPDATE config_retention_items
			SET state = 'deleted', updated_at = ?
			WHERE run_id = ? AND ordinal = ? AND backup_id = ? AND state = 'deleting'`,
			formatTime(deletedAt), runID, ordinal, backupID)
		if err != nil {
			return err
		}
		matched, err = result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		result, err = connection.ExecContext(ctx, `UPDATE config_retention_runs SET
			deleted_count = deleted_count + 1, deleted_bytes = deleted_bytes + ?
			WHERE id = ? AND state = 'executing'`, snapshotBytes, runID)
		if err != nil {
			return err
		}
		matched, err = result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		return insertRetentionItemAudit(ctx, connection, runID, ordinal, backupID, actorID,
			requestID, "config.retention.delete.result", "deleted", snapshotBytes, deletedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("complete retention deletion: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("complete retention deletion: %w", err)
	}
	return nil
}

// AbortRetentionDeletion returns an intact backup to complete while preserving the item outcome.
func (d *DB) AbortRetentionDeletion(
	ctx context.Context,
	runID config.RetentionRunID,
	ordinal int,
	backupID config.BackupID,
	next config.RetentionItemState,
	updatedAt time.Time,
) error {
	if !validRetentionDeletionIdentity(runID, backupID) || ordinal < 0 || updatedAt.IsZero() ||
		(next != config.RetentionItemSkippedProtected && next != config.RetentionItemFailed) {
		return fmt.Errorf("abort retention deletion: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		actorID, requestID, snapshotBytes, err := retentionDeletionEvidence(
			ctx, connection, runID, ordinal, backupID, config.RetentionItemDeleting,
		)
		if err != nil {
			return err
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_backups SET
			state = 'complete', delete_run_id = NULL, delete_reason = ''
			WHERE id = ? AND state = 'deleting' AND body_present = 1 AND delete_run_id = ?`, backupID, runID)
		if err != nil {
			return mapConfigConstraint("abort retention backup deletion", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		result, err = connection.ExecContext(ctx, `UPDATE config_retention_items SET state = ?, updated_at = ?
			WHERE run_id = ? AND ordinal = ? AND backup_id = ? AND state = 'deleting'`,
			next, formatTime(updatedAt), runID, ordinal, backupID)
		if err != nil {
			return err
		}
		matched, err = result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		return insertRetentionItemAudit(ctx, connection, runID, ordinal, backupID, actorID,
			requestID, "config.retention.delete.result", string(next), snapshotBytes, updatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("abort retention deletion: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("abort retention deletion: %w", err)
	}
	return nil
}

// MarkRetentionDeletionUncertain preserves the deleting intent when filesystem outcome is not provable.
func (d *DB) MarkRetentionDeletionUncertain(
	ctx context.Context,
	runID config.RetentionRunID,
	ordinal int,
	backupID config.BackupID,
	updatedAt time.Time,
) error {
	if !validRetentionDeletionIdentity(runID, backupID) || ordinal < 0 || updatedAt.IsZero() {
		return fmt.Errorf("mark retention deletion uncertain: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		actorID, requestID, snapshotBytes, err := retentionDeletionEvidence(
			ctx, connection, runID, ordinal, backupID, config.RetentionItemDeleting,
		)
		if err != nil {
			return err
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_retention_items
			SET state = 'needs_attention', updated_at = ?
			WHERE run_id = ? AND ordinal = ? AND backup_id = ? AND state = 'deleting'`,
			formatTime(updatedAt), runID, ordinal, backupID)
		if err != nil {
			return err
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		return insertRetentionItemAudit(ctx, connection, runID, ordinal, backupID, actorID,
			requestID, "config.retention.delete.result", "needs_attention", snapshotBytes, updatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("mark retention deletion uncertain: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("mark retention deletion uncertain: %w", err)
	}
	return nil
}

func validRetentionDeletionIdentity(runID config.RetentionRunID, backupID config.BackupID) bool {
	if _, err := config.ParseRetentionRunID(string(runID)); err != nil {
		return false
	}
	_, err := config.ParseBackupID(string(backupID))
	return err == nil
}

func retentionBackupProtected(
	ctx context.Context,
	connection *sql.Conn,
	backup config.Backup,
	minimumComplete int,
) (bool, error) {
	if backup.ManuallyProtected || minimumComplete < 1 {
		return true, nil
	}
	var references int
	if err := connection.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM config_attention_cases WHERE state = 'open' AND backup_id = ?) +
		(SELECT COUNT(*) FROM config_releases WHERE state IN ('queued', 'running', 'rolling_back') AND backup_id = ?) +
		(SELECT COUNT(*) FROM config_restores WHERE state IN ('queued', 'running', 'rolling_back')
			AND (target_backup_id = ? OR safety_backup_id = ?))`,
		backup.ID, backup.ID, backup.ID, backup.ID).Scan(&references); err != nil {
		return false, err
	}
	if references > 0 {
		return true, nil
	}
	var newer int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM config_backups
		WHERE state = 'complete' AND body_present = 1
		AND (created_at > ? OR (created_at = ? AND id > ?))`,
		formatTime(backup.CreatedAt), formatTime(backup.CreatedAt), backup.ID).Scan(&newer); err != nil {
		return false, err
	}
	return newer < minimumComplete, nil
}

func retentionDeletionEvidence(
	ctx context.Context,
	connection *sql.Conn,
	runID config.RetentionRunID,
	ordinal int,
	backupID config.BackupID,
	wantState config.RetentionItemState,
) (int64, string, int64, error) {
	var actorID int64
	var requestID string
	var snapshotBytes int64
	var runState config.RetentionRunState
	var itemState config.RetentionItemState
	err := connection.QueryRowContext(ctx, `SELECT r.state, r.created_by, r.request_id,
		i.snapshot_total_bytes, i.state FROM config_retention_runs AS r
		JOIN config_retention_items AS i ON i.run_id = r.id
		WHERE r.id = ? AND i.ordinal = ? AND i.backup_id = ?`, runID, ordinal, backupID).Scan(
		&runState, &actorID, &requestID, &snapshotBytes, &itemState,
	)
	if err != nil {
		return 0, "", 0, err
	}
	if runState != config.RetentionRunExecuting || itemState != wantState {
		return 0, "", 0, config.ErrConflict
	}
	return actorID, requestID, snapshotBytes, nil
}

func insertRetentionItemAudit(
	ctx context.Context,
	connection *sql.Conn,
	runID config.RetentionRunID,
	ordinal int,
	backupID config.BackupID,
	actorID int64,
	requestID string,
	action string,
	result string,
	bytes int64,
	occurredAt time.Time,
) error {
	details, err := json.Marshal(struct {
		Ordinal  int             `json:"ordinal"`
		BackupID config.BackupID `json:"backup_id"`
		Bytes    int64           `json:"bytes"`
	}{Ordinal: ordinal, BackupID: backupID, Bytes: bytes})
	if err != nil {
		return err
	}
	operation := config.OperationRecord{
		ID: fmt.Sprintf("%s:%d:%s", runID, ordinal, result), ObjectType: "config_retention_run",
		ObjectID: string(runID), Action: action, Result: result, RequestID: requestID, OccurredAt: occurredAt,
	}
	return insertOperationAndAudit(ctx, connection, operation, config.AuditEvent{
		OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: actorID,
		Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
		Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
	})
}

// TransitionRetentionRun applies one exact run state CAS and owns/releases the production lease atomically.
func (d *DB) TransitionRetentionRun(ctx context.Context, expected config.RetentionRunState, next config.RetentionRun) error {
	if err := validateRetentionRun(next); err != nil || expected == next.State {
		return fmt.Errorf("transition retention run: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if expected == config.RetentionRunPlanned && next.State == config.RetentionRunExecuting {
			result, err := connection.ExecContext(ctx, `UPDATE config_production_lease
				SET owner_type = 'retention', owner_id = ?, acquired_at = ?
				WHERE singleton = 1 AND owner_type IS NULL`, next.ID, formatTime(next.StartedAt))
			if err != nil {
				return err
			}
			matched, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if matched != 1 {
				return config.ErrOperationInProgress
			}
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_retention_runs SET
			state = ?, deleted_count = ?, deleted_bytes = ?, last_error_code = ?,
			execution_request_id = ?, started_at = ?, finished_at = ? WHERE id = ? AND state = ?`, next.State,
			next.DeletedCount, next.DeletedBytes, next.LastErrorCode, next.ExecutionRequestID,
			nullableTime(next.StartedAt), nullableTime(next.FinishedAt), next.ID, expected)
		if err != nil {
			return mapConfigConstraint("transition retention run", err)
		}
		matched, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if matched != 1 {
			return config.ErrConflict
		}
		if expected == config.RetentionRunExecuting && terminalRetentionState(next.State) {
			result, err := connection.ExecContext(ctx, `UPDATE config_production_lease
				SET owner_type = NULL, owner_id = NULL, acquired_at = NULL
				WHERE singleton = 1 AND owner_type = 'retention' AND owner_id = ?`, next.ID)
			if err != nil {
				return err
			}
			matched, err := result.RowsAffected()
			if err != nil || matched != 1 {
				return errors.Join(config.ErrConflict, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("transition retention run: %w", err)
	}
	return nil
}

// ListAuditEvents returns a bounded newest-first keyset page with actor display names.
func (d *DB) ListAuditEvents(ctx context.Context, query config.AuditQuery) (records []config.AuditRecord, returnErr error) {
	if query.Limit <= 0 || query.Limit > recoveryPageLimit ||
		(query.BeforeOccurredAt.IsZero() != (query.BeforeID == 0)) || query.BeforeID < 0 {
		return nil, fmt.Errorf("list audit events: invalid query")
	}
	statement := `SELECT a.id, a.occurred_at, a.actor_user_id, COALESCE(u.username, ''),
		a.action, a.object_type, a.object_id, a.result, a.request_id, a.details_json
		FROM audit_events AS a LEFT JOIN users AS u ON u.id = a.actor_user_id WHERE 1 = 1`
	arguments := make([]any, 0, 4)
	if query.BeforeID != 0 {
		statement += " AND (a.occurred_at < ? OR (a.occurred_at = ? AND a.id < ?))"
		formatted := formatTime(query.BeforeOccurredAt)
		arguments = append(arguments, formatted, formatted, query.BeforeID)
	}
	statement += " ORDER BY a.occurred_at DESC, a.id DESC LIMIT ?"
	arguments = append(arguments, query.Limit)
	rows, err := d.sql.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	records = make([]config.AuditRecord, 0, query.Limit)
	for rows.Next() {
		var record config.AuditRecord
		var actorID sql.NullInt64
		var occurredAt string
		if err := rows.Scan(&record.ID, &occurredAt, &actorID, &record.ActorName, &record.Action,
			&record.ObjectType, &record.ObjectID, &record.Result, &record.RequestID, &record.DetailsJSON); err != nil {
			return nil, fmt.Errorf("list audit events: %w", err)
		}
		if actorID.Valid {
			record.ActorUserID = actorID.Int64
		}
		parsed, err := parseTime("audit occurrence", occurredAt)
		if err != nil || record.ID <= 0 || !json.Valid([]byte(record.DetailsJSON)) {
			return nil, fmt.Errorf("list audit events: invalid row")
		}
		record.OccurredAt = parsed
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	return records, nil
}

// ListAttentionCases returns a bounded newest-first set, optionally filtered by state.
func (d *DB) ListAttentionCases(ctx context.Context, query config.AttentionQuery) (cases []config.AttentionCase, returnErr error) {
	if query.Limit <= 0 || query.Limit > recoveryPageLimit ||
		(query.State != "" && query.State != config.AttentionCaseOpen && query.State != config.AttentionCaseResolved) ||
		(query.BeforeOpenedAt.IsZero() != (query.BeforeID == "")) {
		return nil, fmt.Errorf("list attention cases: invalid query")
	}
	if query.BeforeID != "" {
		if _, err := config.ParseAttentionCaseID(string(query.BeforeID)); err != nil {
			return nil, fmt.Errorf("list attention cases: invalid query")
		}
	}
	statement := attentionCaseSelect
	conditions := make([]string, 0, 2)
	arguments := make([]any, 0, 5)
	if query.State != "" {
		conditions = append(conditions, "state = ?")
		arguments = append(arguments, query.State)
	}
	if query.BeforeID != "" {
		conditions = append(conditions, "(opened_at < ? OR (opened_at = ? AND id < ?))")
		formatted := formatTime(query.BeforeOpenedAt)
		arguments = append(arguments, formatted, formatted, query.BeforeID)
	}
	if len(conditions) > 0 {
		statement += " WHERE " + strings.Join(conditions, " AND ")
	}
	statement += " ORDER BY opened_at DESC, id DESC LIMIT ?"
	arguments = append(arguments, query.Limit)
	rows, err := d.sql.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list attention cases: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	cases = make([]config.AttentionCase, 0, query.Limit)
	for rows.Next() {
		attention, err := scanAttentionCase(rows)
		if err != nil {
			return nil, fmt.Errorf("list attention cases: %w", err)
		}
		cases = append(cases, attention)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attention cases: %w", err)
	}
	return cases, nil
}

// AttentionCase returns one exact evidence-bound incident.
func (d *DB) AttentionCase(ctx context.Context, id config.AttentionCaseID) (config.AttentionCase, error) {
	if _, err := config.ParseAttentionCaseID(string(id)); err != nil {
		return config.AttentionCase{}, fmt.Errorf("read attention case: %w", err)
	}
	attention, err := scanAttentionCase(d.sql.QueryRowContext(ctx, attentionCaseSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.AttentionCase{}, fmt.Errorf("read attention case: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.AttentionCase{}, fmt.Errorf("read attention case: %w", err)
	}
	return attention, nil
}

const retentionRunSelect = `SELECT id, state, minimum_complete, maximum_complete, maximum_total_bytes,
	minimum_age_seconds, backup_count, total_bytes, protected_count, delete_count, delete_bytes,
	deleted_count, deleted_bytes, last_error_code, created_by, request_id, execution_request_id, created_at, expires_at,
	started_at, finished_at FROM config_retention_runs`

const attentionCaseSelect = `SELECT id, subject_type, subject_id, workspace_id, backup_id, state,
	reason_code, public_evidence_json, opened_at, resolved_by, resolved_at, resolution_type,
	resolution_id FROM config_attention_cases`

func scanRetentionRun(row rowScanner) (config.RetentionRun, error) {
	var run config.RetentionRun
	var minimumAgeSeconds int64
	var createdAt, expiresAt string
	var startedAt, finishedAt sql.NullString
	if err := row.Scan(&run.ID, &run.State, &run.Policy.MinimumComplete, &run.Policy.MaximumComplete,
		&run.Policy.MaximumTotalBytes, &minimumAgeSeconds, &run.BackupCount, &run.TotalBytes,
		&run.ProtectedCount, &run.DeleteCount, &run.DeleteBytes, &run.DeletedCount, &run.DeletedBytes,
		&run.LastErrorCode, &run.CreatedBy, &run.RequestID, &run.ExecutionRequestID,
		&createdAt, &expiresAt, &startedAt, &finishedAt); err != nil {
		return config.RetentionRun{}, err
	}
	if minimumAgeSeconds < 0 || minimumAgeSeconds > int64(math.MaxInt64/time.Second) {
		return config.RetentionRun{}, fmt.Errorf("decode retention run: invalid minimum age")
	}
	run.Policy.MinimumAge = time.Duration(minimumAgeSeconds) * time.Second
	var err error
	if run.CreatedAt, err = parseTime("retention run creation", createdAt); err != nil {
		return config.RetentionRun{}, err
	}
	if run.ExpiresAt, err = parseTime("retention run expiration", expiresAt); err != nil {
		return config.RetentionRun{}, err
	}
	if startedAt.Valid {
		if run.StartedAt, err = parseTime("retention run start", startedAt.String); err != nil {
			return config.RetentionRun{}, err
		}
	}
	if finishedAt.Valid {
		if run.FinishedAt, err = parseTime("retention run finish", finishedAt.String); err != nil {
			return config.RetentionRun{}, err
		}
	}
	return run, nil
}

func scanRetentionItem(row rowScanner) (config.RetentionItem, error) {
	var item config.RetentionItem
	var snapshotCreatedAt, updatedAt string
	if err := row.Scan(&item.RunID, &item.Ordinal, &item.BackupID, &item.Decision, &item.ReasonCode,
		&item.State, &snapshotCreatedAt, &item.SnapshotTotalBytes, &updatedAt); err != nil {
		return config.RetentionItem{}, err
	}
	var err error
	if item.SnapshotCreatedAt, err = parseTime("retention item snapshot", snapshotCreatedAt); err != nil {
		return config.RetentionItem{}, err
	}
	if item.UpdatedAt, err = parseTime("retention item update", updatedAt); err != nil {
		return config.RetentionItem{}, err
	}
	return item, nil
}

func scanAttentionCase(row rowScanner) (config.AttentionCase, error) {
	var attention config.AttentionCase
	var workspaceID, backupID, resolutionType, resolutionID sql.NullString
	var resolvedBy sql.NullInt64
	var openedAt string
	var resolvedAt sql.NullString
	if err := row.Scan(&attention.ID, &attention.SubjectType, &attention.SubjectID, &workspaceID,
		&backupID, &attention.State, &attention.ReasonCode, &attention.PublicEvidenceJSON,
		&openedAt, &resolvedBy, &resolvedAt, &resolutionType, &resolutionID); err != nil {
		return config.AttentionCase{}, err
	}
	if workspaceID.Valid {
		attention.WorkspaceID = config.WorkspaceID(workspaceID.String)
	}
	if backupID.Valid {
		attention.BackupID = config.BackupID(backupID.String)
	}
	if resolvedBy.Valid {
		attention.ResolvedBy = resolvedBy.Int64
	}
	if resolutionType.Valid {
		attention.ResolutionType = config.AttentionResolutionType(resolutionType.String)
	}
	if resolutionID.Valid {
		attention.ResolutionID = resolutionID.String
	}
	var err error
	if attention.OpenedAt, err = parseTime("attention case opening", openedAt); err != nil {
		return config.AttentionCase{}, err
	}
	if resolvedAt.Valid {
		if attention.ResolvedAt, err = parseTime("attention case resolution", resolvedAt.String); err != nil {
			return config.AttentionCase{}, err
		}
	}
	if !json.Valid([]byte(attention.PublicEvidenceJSON)) {
		return config.AttentionCase{}, fmt.Errorf("decode attention case: invalid evidence")
	}
	return attention, nil
}

func validateRetentionRun(run config.RetentionRun) error {
	if _, err := config.ParseRetentionRunID(string(run.ID)); err != nil {
		return err
	}
	if run.Policy.MinimumComplete < 1 || run.Policy.MaximumComplete < run.Policy.MinimumComplete ||
		run.Policy.MaximumTotalBytes <= 0 || run.Policy.MinimumAge < 0 ||
		run.Policy.MinimumAge%time.Second != 0 || run.BackupCount < 0 || run.TotalBytes < 0 ||
		run.ProtectedCount < 0 || run.DeleteCount < 0 || run.DeleteBytes < 0 || run.DeletedCount < 0 ||
		run.DeletedBytes < 0 || run.CreatedBy <= 0 || run.RequestID == "" || run.CreatedAt.IsZero() ||
		run.ExpiresAt.IsZero() || !run.ExpiresAt.After(run.CreatedAt) {
		return errors.New("invalid retention run metadata")
	}
	switch run.State {
	case config.RetentionRunPlanned:
		if run.ExecutionRequestID != "" || !run.StartedAt.IsZero() || !run.FinishedAt.IsZero() {
			return errors.New("planned retention run has execution times")
		}
	case config.RetentionRunExecuting:
		if run.ExecutionRequestID == "" || run.StartedAt.IsZero() || !run.FinishedAt.IsZero() {
			return errors.New("executing retention run has invalid times")
		}
	case config.RetentionRunSucceeded, config.RetentionRunFailed, config.RetentionRunNeedsAttention:
		if run.ExecutionRequestID == "" || run.StartedAt.IsZero() || run.FinishedAt.IsZero() {
			return errors.New("terminal retention run lacks times")
		}
	case config.RetentionRunExpired:
		if run.ExecutionRequestID != "" || !run.StartedAt.IsZero() || run.FinishedAt.IsZero() {
			return errors.New("expired retention plan has invalid times")
		}
	default:
		return errors.New("invalid retention run state")
	}
	return nil
}

func validateRetentionItem(item config.RetentionItem) error {
	if _, err := config.ParseRetentionRunID(string(item.RunID)); err != nil {
		return err
	}
	if _, err := config.ParseBackupID(string(item.BackupID)); err != nil {
		return err
	}
	if item.Ordinal < 0 || item.ReasonCode == "" || item.SnapshotCreatedAt.IsZero() ||
		item.SnapshotTotalBytes < 0 || item.UpdatedAt.IsZero() {
		return errors.New("invalid retention item metadata")
	}
	if item.Decision != config.RetentionDecisionKeep && item.Decision != config.RetentionDecisionDelete {
		return errors.New("invalid retention decision")
	}
	if !knownRetentionItemState(item.State) {
		return errors.New("invalid retention item state")
	}
	return nil
}

func knownRetentionItemState(state config.RetentionItemState) bool {
	switch state {
	case config.RetentionItemPlanned, config.RetentionItemKept, config.RetentionItemDeleting,
		config.RetentionItemDeleted, config.RetentionItemSkippedProtected, config.RetentionItemFailed,
		config.RetentionItemNeedsAttention:
		return true
	default:
		return false
	}
}

func terminalRetentionState(state config.RetentionRunState) bool {
	switch state {
	case config.RetentionRunSucceeded, config.RetentionRunFailed, config.RetentionRunNeedsAttention,
		config.RetentionRunExpired:
		return true
	case config.RetentionRunPlanned, config.RetentionRunExecuting:
		return false
	default:
		return false
	}
}

func validRecoveryReason(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}
