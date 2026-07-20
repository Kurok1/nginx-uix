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
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const recoveryStageReadLimit = 512

// CreateRestore atomically persists one queued manual restore and acquires the shared production lease.
func (d *DB) CreateRestore(ctx context.Context, restore config.Restore, stage config.RestoreStage) error {
	if err := validateRestore(restore); err != nil || restore.State != config.RestoreStateQueued ||
		restore.Stage != config.RestoreStageQueued || stage.RestoreID != restore.ID || stage.Sequence != 1 ||
		stage.Stage != restore.Stage || validateRestoreStage(stage) != nil {
		return fmt.Errorf("create restore: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if err := requireUsableRestoreTarget(ctx, connection, restore.TargetBackupID); err != nil {
			return err
		}
		if err := requireOpenAttentionCase(ctx, connection, restore.AttentionCaseID); err != nil {
			return err
		}
		if err := acquireProductionLeaseTx(ctx, connection, config.ProductionOperationRestore,
			string(restore.ID), restore.CreatedAt); err != nil {
			return err
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO config_restores(
			id, target_backup_id, safety_backup_id, attention_case_id, state, stage,
			source_digest, target_digest, last_error_code, created_by, reason, request_id,
			created_at, updated_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, restore.ID,
			restore.TargetBackupID, restore.SafetyBackupID, nullableAttentionCaseID(restore.AttentionCaseID),
			restore.State, restore.Stage, restore.SourceDigest[:], restore.TargetDigest[:],
			restore.LastErrorCode, restore.CreatedBy, restore.Reason, restore.RequestID,
			formatTime(restore.CreatedAt), formatTime(restore.UpdatedAt), nullableTime(restore.FinishedAt))
		if err != nil {
			return mapConfigConstraint("insert restore", err)
		}
		if err := insertRestoreStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertRestoreStageAudit(ctx, connection, restore, stage)
	})
	if err != nil {
		return fmt.Errorf("create restore: %w", err)
	}
	return nil
}

// TransitionRestore applies one exact state/stage CAS, audit event, attention evidence, and lease release.
func (d *DB) TransitionRestore(
	ctx context.Context,
	expectedState config.RestoreState,
	expectedStage config.RestoreStageName,
	next config.Restore,
	stage config.RestoreStage,
) error {
	if err := validateRestore(next); err != nil || validateRestoreStage(stage) != nil ||
		stage.RestoreID != next.ID || stage.Stage != next.Stage {
		return fmt.Errorf("transition restore: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState config.RestoreState
		var currentStage config.RestoreStageName
		var lastSequence int64
		if err := connection.QueryRowContext(ctx, `SELECT r.state, r.stage,
			COALESCE((SELECT MAX(sequence) FROM config_restore_stages WHERE restore_id = r.id), 0)
			FROM config_restores AS r WHERE r.id = ?`, next.ID).Scan(
			&currentState, &currentStage, &lastSequence,
		); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage ||
			lastSequence < 0 || uint64(lastSequence)+1 != stage.Sequence {
			return config.ErrConflict
		}
		attentionID := next.AttentionCaseID
		if next.State == config.RestoreStateNeedsAttention && attentionID == "" {
			attentionID = config.AttentionCaseID(next.ID)
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_restores SET
			safety_backup_id = ?, attention_case_id = ?, state = ?, stage = ?, last_error_code = ?,
			updated_at = ?, finished_at = ? WHERE id = ? AND state = ? AND stage = ?`,
			next.SafetyBackupID, nullableAttentionCaseID(attentionID), next.State, next.Stage,
			next.LastErrorCode, formatTime(next.UpdatedAt), nullableTime(next.FinishedAt),
			next.ID, expectedState, expectedStage)
		if err != nil {
			return mapConfigConstraint("update restore", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertRestoreStage(ctx, connection, stage); err != nil {
			return err
		}
		if err := insertRestoreStageAudit(ctx, connection, next, stage); err != nil {
			return err
		}
		if next.State == config.RestoreStateNeedsAttention {
			if err := insertTaskAttentionCase(ctx, connection, config.AttentionCaseID(next.ID),
				config.AttentionSubjectRestore, string(next.ID), next.TargetBackupID,
				next.LastErrorCode, next.UpdatedAt); err != nil {
				return err
			}
		}
		if terminalStoredRestoreState(next.State) {
			return releaseProductionLeaseTx(ctx, connection, config.ProductionOperationRestore, string(next.ID))
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("transition restore: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("transition restore: %w", err)
	}
	return nil
}

// Restore returns one exact persisted manual recovery task.
func (d *DB) Restore(ctx context.Context, id config.RestoreID) (config.Restore, error) {
	if _, err := config.ParseRestoreID(string(id)); err != nil {
		return config.Restore{}, fmt.Errorf("read restore: %w", err)
	}
	restore, err := scanRestore(d.sql.QueryRowContext(ctx, restoreSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Restore{}, fmt.Errorf("read restore: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.Restore{}, fmt.Errorf("read restore: %w", err)
	}
	return restore, nil
}

// ActiveRestore returns the sole queued, running, or rolling-back manual restore.
func (d *DB) ActiveRestore(ctx context.Context) (config.Restore, error) {
	restore, err := scanRestore(d.sql.QueryRowContext(ctx, restoreSelect+
		" WHERE state IN ('queued', 'running', 'rolling_back') LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Restore{}, fmt.Errorf("read active restore: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.Restore{}, fmt.Errorf("read active restore: %w", err)
	}
	return restore, nil
}

// RestoreStages returns immutable restore stages after one sequence.
func (d *DB) RestoreStages(
	ctx context.Context,
	id config.RestoreID,
	after uint64,
	limit int,
) (stages []config.RestoreStage, returnErr error) {
	if _, err := config.ParseRestoreID(string(id)); err != nil || after > math.MaxInt64 ||
		limit <= 0 || limit > recoveryStageReadLimit {
		return nil, fmt.Errorf("list restore stages: invalid input")
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT restore_id, sequence, stage, result, code,
		public_details_json, occurred_at FROM config_restore_stages
		WHERE restore_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list restore stages: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	for rows.Next() {
		stage, err := scanRestoreStage(rows)
		if err != nil {
			return nil, fmt.Errorf("list restore stages: %w", err)
		}
		stages = append(stages, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list restore stages: %w", err)
	}
	return stages, nil
}

// ListRestores returns one stable newest-first keyset page.
func (d *DB) ListRestores(ctx context.Context, query config.HistoryQuery) (_ []config.Restore, returnErr error) {
	if !validHistoryQuery(query, func(raw string) error {
		_, err := config.ParseRestoreID(raw)
		return err
	}) {
		return nil, fmt.Errorf("list restores: invalid query")
	}
	statement := restoreSelect + " WHERE 1 = 1"
	arguments := make([]any, 0, 4)
	if query.BeforeID != "" {
		statement += " AND (created_at < ? OR (created_at = ? AND id < ?))"
		formatted := formatTime(query.BeforeCreatedAt)
		arguments = append(arguments, formatted, formatted, query.BeforeID)
	}
	statement += " ORDER BY created_at DESC, id DESC LIMIT ?"
	arguments = append(arguments, query.Limit)
	rows, err := d.sql.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list restores: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result := make([]config.Restore, 0, query.Limit)
	for rows.Next() {
		restore, err := scanRestore(rows)
		if err != nil {
			return nil, fmt.Errorf("list restores: %w", err)
		}
		result = append(result, restore)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list restores: %w", err)
	}
	return result, nil
}

// CreateRestart atomically persists one fixed restart and acquires the shared production lease.
func (d *DB) CreateRestart(ctx context.Context, restart config.Restart, stage config.RestartStage) error {
	if err := validateRestart(restart); err != nil || restart.State != config.RestartStateQueued ||
		restart.Stage != config.RestartStageQueued || stage.RestartID != restart.ID || stage.Sequence != 1 ||
		stage.Stage != restart.Stage || validateRestartStage(stage) != nil {
		return fmt.Errorf("create restart: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if err := requireOpenAttentionCase(ctx, connection, restart.AttentionCaseID); err != nil {
			return err
		}
		if err := acquireProductionLeaseTx(ctx, connection, config.ProductionOperationRestart,
			string(restart.ID), restart.CreatedAt); err != nil {
			return err
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO config_restarts(
			id, attention_case_id, state, stage, production_digest, before_master_pid,
			after_master_pid, worker_count, http_status, last_error_code, created_by,
			reason, request_id, created_at, updated_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, restart.ID,
			nullableAttentionCaseID(restart.AttentionCaseID), restart.State, restart.Stage,
			restart.ProductionDigest[:], nullablePositiveInt(restart.BeforeMasterPID),
			nullablePositiveInt(restart.AfterMasterPID), restart.WorkerCount,
			nullablePositiveInt(restart.HTTPStatus), restart.LastErrorCode, restart.CreatedBy,
			restart.Reason, restart.RequestID, formatTime(restart.CreatedAt),
			formatTime(restart.UpdatedAt), nullableTime(restart.FinishedAt))
		if err != nil {
			return mapConfigConstraint("insert restart", err)
		}
		if err := insertRestartStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertRestartStageAudit(ctx, connection, restart, stage)
	})
	if err != nil {
		return fmt.Errorf("create restart: %w", err)
	}
	return nil
}

// TransitionRestart applies one exact fixed-restart state/stage CAS and terminal evidence.
func (d *DB) TransitionRestart(
	ctx context.Context,
	expectedState config.RestartState,
	expectedStage config.RestartStageName,
	next config.Restart,
	stage config.RestartStage,
) error {
	if err := validateRestart(next); err != nil || validateRestartStage(stage) != nil ||
		stage.RestartID != next.ID || stage.Stage != next.Stage {
		return fmt.Errorf("transition restart: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState config.RestartState
		var currentStage config.RestartStageName
		var lastSequence int64
		if err := connection.QueryRowContext(ctx, `SELECT r.state, r.stage,
			COALESCE((SELECT MAX(sequence) FROM config_restart_stages WHERE restart_id = r.id), 0)
			FROM config_restarts AS r WHERE r.id = ?`, next.ID).Scan(
			&currentState, &currentStage, &lastSequence,
		); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage ||
			lastSequence < 0 || uint64(lastSequence)+1 != stage.Sequence {
			return config.ErrConflict
		}
		attentionID := next.AttentionCaseID
		if next.State == config.RestartStateNeedsAttention && attentionID == "" {
			attentionID = config.AttentionCaseID(next.ID)
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_restarts SET
			attention_case_id = ?, state = ?, stage = ?, before_master_pid = ?, after_master_pid = ?,
			worker_count = ?, http_status = ?, last_error_code = ?, updated_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND stage = ?`, nullableAttentionCaseID(attentionID),
			next.State, next.Stage, nullablePositiveInt(next.BeforeMasterPID), nullablePositiveInt(next.AfterMasterPID),
			next.WorkerCount, nullablePositiveInt(next.HTTPStatus), next.LastErrorCode,
			formatTime(next.UpdatedAt), nullableTime(next.FinishedAt), next.ID, expectedState, expectedStage)
		if err != nil {
			return mapConfigConstraint("update restart", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertRestartStage(ctx, connection, stage); err != nil {
			return err
		}
		if err := insertRestartStageAudit(ctx, connection, next, stage); err != nil {
			return err
		}
		if next.State == config.RestartStateNeedsAttention {
			if err := insertTaskAttentionCase(ctx, connection, config.AttentionCaseID(next.ID),
				config.AttentionSubjectRestart, string(next.ID), "", next.LastErrorCode, next.UpdatedAt); err != nil {
				return err
			}
		}
		if terminalStoredRestartState(next.State) {
			return releaseProductionLeaseTx(ctx, connection, config.ProductionOperationRestart, string(next.ID))
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("transition restart: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("transition restart: %w", err)
	}
	return nil
}

// Restart returns one exact fixed runtime-control task.
func (d *DB) Restart(ctx context.Context, id config.RestartID) (config.Restart, error) {
	if _, err := config.ParseRestartID(string(id)); err != nil {
		return config.Restart{}, fmt.Errorf("read restart: %w", err)
	}
	restart, err := scanRestart(d.sql.QueryRowContext(ctx, restartSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Restart{}, fmt.Errorf("read restart: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.Restart{}, fmt.Errorf("read restart: %w", err)
	}
	return restart, nil
}

// ActiveRestart returns the sole queued or running fixed restart.
func (d *DB) ActiveRestart(ctx context.Context) (config.Restart, error) {
	restart, err := scanRestart(d.sql.QueryRowContext(ctx, restartSelect+
		" WHERE state IN ('queued', 'running') LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Restart{}, fmt.Errorf("read active restart: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.Restart{}, fmt.Errorf("read active restart: %w", err)
	}
	return restart, nil
}

// RestartStages returns immutable fixed-restart stages after one sequence.
func (d *DB) RestartStages(
	ctx context.Context,
	id config.RestartID,
	after uint64,
	limit int,
) (stages []config.RestartStage, returnErr error) {
	if _, err := config.ParseRestartID(string(id)); err != nil || after > math.MaxInt64 ||
		limit <= 0 || limit > recoveryStageReadLimit {
		return nil, fmt.Errorf("list restart stages: invalid input")
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT restart_id, sequence, stage, result, code,
		public_details_json, occurred_at FROM config_restart_stages
		WHERE restart_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list restart stages: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	for rows.Next() {
		stage, err := scanRestartStage(rows)
		if err != nil {
			return nil, fmt.Errorf("list restart stages: %w", err)
		}
		stages = append(stages, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list restart stages: %w", err)
	}
	return stages, nil
}

// ListRestarts returns one stable newest-first keyset page.
func (d *DB) ListRestarts(ctx context.Context, query config.HistoryQuery) (_ []config.Restart, returnErr error) {
	if !validHistoryQuery(query, func(raw string) error {
		_, err := config.ParseRestartID(raw)
		return err
	}) {
		return nil, fmt.Errorf("list restarts: invalid query")
	}
	statement := restartSelect + " WHERE 1 = 1"
	arguments := make([]any, 0, 4)
	if query.BeforeID != "" {
		statement += " AND (created_at < ? OR (created_at = ? AND id < ?))"
		formatted := formatTime(query.BeforeCreatedAt)
		arguments = append(arguments, formatted, formatted, query.BeforeID)
	}
	statement += " ORDER BY created_at DESC, id DESC LIMIT ?"
	arguments = append(arguments, query.Limit)
	rows, err := d.sql.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list restarts: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result := make([]config.Restart, 0, query.Limit)
	for rows.Next() {
		restart, err := scanRestart(rows)
		if err != nil {
			return nil, fmt.Errorf("list restarts: %w", err)
		}
		result = append(result, restart)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list restarts: %w", err)
	}
	return result, nil
}

// ListReleases returns one stable newest-first publication-history page.
func (d *DB) ListReleases(ctx context.Context, query config.HistoryQuery) (_ []config.Release, returnErr error) {
	if !validHistoryQuery(query, func(raw string) error {
		_, err := config.ParseReleaseID(raw)
		return err
	}) {
		return nil, fmt.Errorf("list releases: invalid query")
	}
	statement := releaseSelect + " WHERE 1 = 1"
	arguments := make([]any, 0, 4)
	if query.BeforeID != "" {
		statement += " AND (created_at < ? OR (created_at = ? AND id < ?))"
		formatted := formatTime(query.BeforeCreatedAt)
		arguments = append(arguments, formatted, formatted, query.BeforeID)
	}
	statement += " ORDER BY created_at DESC, id DESC LIMIT ?"
	arguments = append(arguments, query.Limit)
	rows, err := d.sql.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result := make([]config.Release, 0, query.Limit)
	for rows.Next() {
		release, err := scanRelease(rows)
		if err != nil {
			return nil, fmt.Errorf("list releases: %w", err)
		}
		result = append(result, release)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	return result, nil
}

// CreateVerification persists one fixed runtime-health result before it can resolve an attention case.
func (d *DB) CreateVerification(ctx context.Context, verification config.Verification) error {
	if err := validateVerification(verification); err != nil {
		return fmt.Errorf("create runtime verification: %w", err)
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if err := requireOpenAttentionCase(ctx, connection, verification.AttentionCaseID); err != nil {
			return err
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO config_verifications(
			id, attention_case_id, state, production_digest, master_pid, worker_count,
			http_status, last_error_code, created_by, request_id, created_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, verification.ID,
			verification.AttentionCaseID, verification.State, verification.ProductionDigest[:],
			nullablePositiveInt(verification.MasterPID), verification.WorkerCount,
			nullablePositiveInt(verification.HTTPStatus), verification.LastErrorCode,
			verification.CreatedBy, verification.RequestID, formatTime(verification.CreatedAt),
			formatTime(verification.FinishedAt))
		if err != nil {
			return mapConfigConstraint("insert runtime verification", err)
		}
		details, err := json.Marshal(struct {
			State      config.VerificationState `json:"state"`
			HTTPStatus int                      `json:"http_status,omitempty"`
		}{verification.State, verification.HTTPStatus})
		if err != nil {
			return err
		}
		operation := config.OperationRecord{
			ID: string(verification.ID), ObjectType: "config_verification",
			ObjectID: string(verification.ID), Action: "config.attention.verify",
			Result: string(verification.State), RequestID: verification.RequestID,
			OccurredAt: verification.FinishedAt,
		}
		return insertOperationAndAudit(ctx, connection, operation, config.AuditEvent{
			OperationID: operation.ID, OccurredAt: operation.OccurredAt,
			ActorUserID: verification.CreatedBy, Action: operation.Action,
			ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		})
	})
	if err != nil {
		return fmt.Errorf("create runtime verification: %w", err)
	}
	return nil
}

// Verification returns one exact persisted runtime-health result.
func (d *DB) Verification(ctx context.Context, id config.VerificationID) (config.Verification, error) {
	if _, err := config.ParseVerificationID(string(id)); err != nil {
		return config.Verification{}, fmt.Errorf("read runtime verification: %w", err)
	}
	verification, err := scanVerification(d.sql.QueryRowContext(ctx, verificationSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Verification{}, fmt.Errorf("read runtime verification: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.Verification{}, fmt.Errorf("read runtime verification: %w", err)
	}
	return verification, nil
}

// ResolveAttentionCase closes one open case only with a successful evidence-bearing operation.
func (d *DB) ResolveAttentionCase(
	ctx context.Context,
	id config.AttentionCaseID,
	resolutionType config.AttentionResolutionType,
	resolutionID string,
	actor config.Actor,
	resolvedAt time.Time,
) error {
	if _, err := config.ParseAttentionCaseID(string(id)); err != nil || actor.UserID <= 0 ||
		actor.RequestID == "" || resolvedAt.IsZero() || !validAttentionResolution(resolutionType, resolutionID) {
		return fmt.Errorf("resolve attention case: invalid input")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if err := requireSuccessfulResolution(ctx, connection, resolutionType, resolutionID); err != nil {
			return err
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_attention_cases SET
			state = 'resolved', resolved_by = ?, resolved_at = ?, resolution_type = ?, resolution_id = ?
			WHERE id = ? AND state = 'open'`, actor.UserID, formatTime(resolvedAt),
			resolutionType, resolutionID, id)
		if err != nil {
			return mapConfigConstraint("resolve attention case", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		details, err := json.Marshal(struct {
			ResolutionType config.AttentionResolutionType `json:"resolution_type"`
			ResolutionID   string                         `json:"resolution_id"`
		}{resolutionType, resolutionID})
		if err != nil {
			return err
		}
		operation := config.OperationRecord{
			ID: string(id) + ":resolved", ObjectType: "config_attention_case", ObjectID: string(id),
			Action: "config.attention.resolve", Result: "resolved", RequestID: actor.RequestID,
			OccurredAt: resolvedAt,
		}
		return insertOperationAndAudit(ctx, connection, operation, config.AuditEvent{
			OperationID: operation.ID, OccurredAt: resolvedAt, ActorUserID: actor.UserID,
			Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("resolve attention case: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("resolve attention case: %w", err)
	}
	return nil
}

const restoreSelect = `SELECT id, target_backup_id, safety_backup_id, attention_case_id,
	state, stage, source_digest, target_digest, last_error_code, created_by, reason, request_id,
	created_at, updated_at, finished_at FROM config_restores`

const restartSelect = `SELECT id, attention_case_id, state, stage, production_digest,
	before_master_pid, after_master_pid, worker_count, http_status, last_error_code,
	created_by, reason, request_id, created_at, updated_at, finished_at FROM config_restarts`

const verificationSelect = `SELECT id, attention_case_id, state, production_digest, master_pid,
	worker_count, http_status, last_error_code, created_by, request_id, created_at, finished_at
	FROM config_verifications`

func scanVerification(row rowScanner) (config.Verification, error) {
	var verification config.Verification
	var digest []byte
	var masterPID, httpStatus sql.NullInt64
	var createdAt, finishedAt string
	if err := row.Scan(&verification.ID, &verification.AttentionCaseID, &verification.State,
		&digest, &masterPID, &verification.WorkerCount, &httpStatus, &verification.LastErrorCode,
		&verification.CreatedBy, &verification.RequestID, &createdAt, &finishedAt); err != nil {
		return config.Verification{}, err
	}
	if !copyDigest(&verification.ProductionDigest, digest) {
		return config.Verification{}, errors.New("decode runtime verification: invalid digest")
	}
	if masterPID.Valid {
		verification.MasterPID = int(masterPID.Int64)
	}
	if httpStatus.Valid {
		verification.HTTPStatus = int(httpStatus.Int64)
	}
	var err error
	if verification.CreatedAt, err = parseTime("runtime verification creation", createdAt); err != nil {
		return config.Verification{}, err
	}
	if verification.FinishedAt, err = parseTime("runtime verification finish", finishedAt); err != nil {
		return config.Verification{}, err
	}
	if err := validateVerification(verification); err != nil {
		return config.Verification{}, err
	}
	return verification, nil
}

func scanRestore(row rowScanner) (config.Restore, error) {
	var restore config.Restore
	var attentionID sql.NullString
	var sourceDigest, targetDigest []byte
	var createdAt, updatedAt string
	var finishedAt sql.NullString
	if err := row.Scan(&restore.ID, &restore.TargetBackupID, &restore.SafetyBackupID, &attentionID,
		&restore.State, &restore.Stage, &sourceDigest, &targetDigest, &restore.LastErrorCode,
		&restore.CreatedBy, &restore.Reason, &restore.RequestID, &createdAt, &updatedAt, &finishedAt); err != nil {
		return config.Restore{}, err
	}
	if attentionID.Valid {
		restore.AttentionCaseID = config.AttentionCaseID(attentionID.String)
	}
	if !copyDigest(&restore.SourceDigest, sourceDigest) || !copyDigest(&restore.TargetDigest, targetDigest) {
		return config.Restore{}, errors.New("decode restore: invalid digest")
	}
	var err error
	if restore.CreatedAt, err = parseTime("restore creation", createdAt); err != nil {
		return config.Restore{}, err
	}
	if restore.UpdatedAt, err = parseTime("restore update", updatedAt); err != nil {
		return config.Restore{}, err
	}
	if finishedAt.Valid {
		if restore.FinishedAt, err = parseTime("restore finish", finishedAt.String); err != nil {
			return config.Restore{}, err
		}
	}
	return restore, nil
}

func scanRestoreStage(row rowScanner) (config.RestoreStage, error) {
	var stage config.RestoreStage
	var sequence int64
	var occurredAt string
	if err := row.Scan(&stage.RestoreID, &sequence, &stage.Stage, &stage.Result, &stage.Code,
		&stage.PublicDetailsJSON, &occurredAt); err != nil {
		return config.RestoreStage{}, err
	}
	if sequence < 1 {
		return config.RestoreStage{}, errors.New("decode restore stage: invalid sequence")
	}
	stage.Sequence = uint64(sequence)
	parsed, err := parseTime("restore stage", occurredAt)
	if err != nil {
		return config.RestoreStage{}, err
	}
	stage.OccurredAt = parsed
	return stage, nil
}

func scanRestart(row rowScanner) (config.Restart, error) {
	var restart config.Restart
	var attentionID sql.NullString
	var productionDigest []byte
	var beforeMaster, afterMaster, httpStatus sql.NullInt64
	var createdAt, updatedAt string
	var finishedAt sql.NullString
	if err := row.Scan(&restart.ID, &attentionID, &restart.State, &restart.Stage, &productionDigest,
		&beforeMaster, &afterMaster, &restart.WorkerCount, &httpStatus, &restart.LastErrorCode,
		&restart.CreatedBy, &restart.Reason, &restart.RequestID, &createdAt, &updatedAt, &finishedAt); err != nil {
		return config.Restart{}, err
	}
	if attentionID.Valid {
		restart.AttentionCaseID = config.AttentionCaseID(attentionID.String)
	}
	if !copyDigest(&restart.ProductionDigest, productionDigest) {
		return config.Restart{}, errors.New("decode restart: invalid digest")
	}
	if beforeMaster.Valid {
		restart.BeforeMasterPID = int(beforeMaster.Int64)
	}
	if afterMaster.Valid {
		restart.AfterMasterPID = int(afterMaster.Int64)
	}
	if httpStatus.Valid {
		restart.HTTPStatus = int(httpStatus.Int64)
	}
	var err error
	if restart.CreatedAt, err = parseTime("restart creation", createdAt); err != nil {
		return config.Restart{}, err
	}
	if restart.UpdatedAt, err = parseTime("restart update", updatedAt); err != nil {
		return config.Restart{}, err
	}
	if finishedAt.Valid {
		if restart.FinishedAt, err = parseTime("restart finish", finishedAt.String); err != nil {
			return config.Restart{}, err
		}
	}
	return restart, nil
}

func scanRestartStage(row rowScanner) (config.RestartStage, error) {
	var stage config.RestartStage
	var sequence int64
	var occurredAt string
	if err := row.Scan(&stage.RestartID, &sequence, &stage.Stage, &stage.Result, &stage.Code,
		&stage.PublicDetailsJSON, &occurredAt); err != nil {
		return config.RestartStage{}, err
	}
	if sequence < 1 {
		return config.RestartStage{}, errors.New("decode restart stage: invalid sequence")
	}
	stage.Sequence = uint64(sequence)
	parsed, err := parseTime("restart stage", occurredAt)
	if err != nil {
		return config.RestartStage{}, err
	}
	stage.OccurredAt = parsed
	return stage, nil
}

func validateRestore(restore config.Restore) error {
	if _, err := config.ParseRestoreID(string(restore.ID)); err != nil {
		return err
	}
	if _, err := config.ParseBackupID(string(restore.TargetBackupID)); err != nil {
		return err
	}
	if _, err := config.ParseBackupID(string(restore.SafetyBackupID)); err != nil {
		return err
	}
	if restore.AttentionCaseID != "" {
		if _, err := config.ParseAttentionCaseID(string(restore.AttentionCaseID)); err != nil {
			return err
		}
	}
	if restore.SourceDigest == (config.Digest{}) || restore.TargetDigest == (config.Digest{}) ||
		restore.Stage == "" || restore.CreatedBy <= 0 || !validRecoveryReason(restore.Reason) ||
		restore.RequestID == "" || restore.CreatedAt.IsZero() || restore.UpdatedAt.IsZero() {
		return errors.New("invalid restore metadata")
	}
	switch restore.State {
	case config.RestoreStateQueued, config.RestoreStateRunning, config.RestoreStateRollingBack:
		if !restore.FinishedAt.IsZero() {
			return errors.New("active restore has finish time")
		}
	case config.RestoreStateSucceeded, config.RestoreStateFailed, config.RestoreStateRolledBack,
		config.RestoreStateNeedsAttention, config.RestoreStateCancelled:
		if restore.FinishedAt.IsZero() {
			return errors.New("terminal restore lacks finish time")
		}
	default:
		return errors.New("invalid restore state")
	}
	return nil
}

func validateRestoreStage(stage config.RestoreStage) error {
	if _, err := config.ParseRestoreID(string(stage.RestoreID)); err != nil {
		return err
	}
	if stage.Sequence == 0 || stage.Stage == "" || stage.OccurredAt.IsZero() ||
		!json.Valid([]byte(stage.PublicDetailsJSON)) || !knownStageResult(stage.Result) {
		return errors.New("invalid restore stage")
	}
	return nil
}

func validateRestart(restart config.Restart) error {
	if _, err := config.ParseRestartID(string(restart.ID)); err != nil {
		return err
	}
	if restart.AttentionCaseID != "" {
		if _, err := config.ParseAttentionCaseID(string(restart.AttentionCaseID)); err != nil {
			return err
		}
	}
	if restart.ProductionDigest == (config.Digest{}) || restart.Stage == "" || restart.CreatedBy <= 0 ||
		!validRecoveryReason(restart.Reason) || restart.RequestID == "" || restart.CreatedAt.IsZero() ||
		restart.UpdatedAt.IsZero() || restart.BeforeMasterPID < 0 || restart.AfterMasterPID < 0 ||
		restart.WorkerCount < 0 || restart.HTTPStatus < 0 || restart.HTTPStatus > 599 {
		return errors.New("invalid restart metadata")
	}
	switch restart.State {
	case config.RestartStateQueued, config.RestartStateRunning:
		if !restart.FinishedAt.IsZero() {
			return errors.New("active restart has finish time")
		}
	case config.RestartStateSucceeded, config.RestartStateFailed,
		config.RestartStateNeedsAttention, config.RestartStateCancelled:
		if restart.FinishedAt.IsZero() {
			return errors.New("terminal restart lacks finish time")
		}
	default:
		return errors.New("invalid restart state")
	}
	return nil
}

func validateRestartStage(stage config.RestartStage) error {
	if _, err := config.ParseRestartID(string(stage.RestartID)); err != nil {
		return err
	}
	if stage.Sequence == 0 || stage.Stage == "" || stage.OccurredAt.IsZero() ||
		!json.Valid([]byte(stage.PublicDetailsJSON)) || !knownStageResult(stage.Result) {
		return errors.New("invalid restart stage")
	}
	return nil
}

func validateVerification(verification config.Verification) error {
	if _, err := config.ParseVerificationID(string(verification.ID)); err != nil {
		return err
	}
	if _, err := config.ParseAttentionCaseID(string(verification.AttentionCaseID)); err != nil {
		return err
	}
	if verification.ProductionDigest == (config.Digest{}) || verification.MasterPID < 0 ||
		verification.WorkerCount < 0 || verification.HTTPStatus < 0 || verification.HTTPStatus > 599 ||
		verification.CreatedBy <= 0 || verification.RequestID == "" || verification.CreatedAt.IsZero() ||
		verification.FinishedAt.IsZero() || verification.FinishedAt.Before(verification.CreatedAt) {
		return errors.New("invalid runtime verification metadata")
	}
	switch verification.State {
	case config.VerificationStateSucceeded:
		if verification.MasterPID <= 0 || verification.WorkerCount <= 0 ||
			verification.HTTPStatus < 200 || verification.HTTPStatus >= 300 || verification.LastErrorCode != "" {
			return errors.New("invalid successful runtime verification")
		}
	case config.VerificationStateFailed:
		if verification.LastErrorCode == "" {
			return errors.New("invalid failed runtime verification")
		}
	default:
		return errors.New("invalid runtime verification state")
	}
	return nil
}

func knownStageResult(result config.StageResult) bool {
	switch result {
	case config.StageResultPending, config.StageResultRunning, config.StageResultSuccess,
		config.StageResultFailed, config.StageResultWarning:
		return true
	default:
		return false
	}
}

func terminalStoredRestoreState(state config.RestoreState) bool {
	switch state {
	case config.RestoreStateSucceeded, config.RestoreStateFailed, config.RestoreStateRolledBack,
		config.RestoreStateNeedsAttention, config.RestoreStateCancelled:
		return true
	case config.RestoreStateQueued, config.RestoreStateRunning, config.RestoreStateRollingBack:
		return false
	default:
		return false
	}
}

func terminalStoredRestartState(state config.RestartState) bool {
	switch state {
	case config.RestartStateSucceeded, config.RestartStateFailed,
		config.RestartStateNeedsAttention, config.RestartStateCancelled:
		return true
	case config.RestartStateQueued, config.RestartStateRunning:
		return false
	default:
		return false
	}
}

func insertRestoreStage(ctx context.Context, connection *sql.Conn, stage config.RestoreStage) error {
	_, err := connection.ExecContext(ctx, `INSERT INTO config_restore_stages(
		restore_id, sequence, stage, result, code, public_details_json, occurred_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, stage.RestoreID, stage.Sequence, stage.Stage,
		stage.Result, stage.Code, stage.PublicDetailsJSON, formatTime(stage.OccurredAt))
	if err != nil {
		return mapConfigConstraint("insert restore stage", err)
	}
	return nil
}

func insertRestartStage(ctx context.Context, connection *sql.Conn, stage config.RestartStage) error {
	_, err := connection.ExecContext(ctx, `INSERT INTO config_restart_stages(
		restart_id, sequence, stage, result, code, public_details_json, occurred_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, stage.RestartID, stage.Sequence, stage.Stage,
		stage.Result, stage.Code, stage.PublicDetailsJSON, formatTime(stage.OccurredAt))
	if err != nil {
		return mapConfigConstraint("insert restart stage", err)
	}
	return nil
}

func insertRestoreStageAudit(
	ctx context.Context,
	connection *sql.Conn,
	restore config.Restore,
	stage config.RestoreStage,
) error {
	return insertRecoveryStageAudit(ctx, connection, string(restore.ID), "config_restore",
		"config.restore.stage", restore.CreatedBy, restore.RequestID, stage.Sequence,
		string(stage.Stage), stage.Result, stage.OccurredAt)
}

func insertRestartStageAudit(
	ctx context.Context,
	connection *sql.Conn,
	restart config.Restart,
	stage config.RestartStage,
) error {
	return insertRecoveryStageAudit(ctx, connection, string(restart.ID), "nginx_restart",
		"nginx.restart.stage", restart.CreatedBy, restart.RequestID, stage.Sequence,
		string(stage.Stage), stage.Result, stage.OccurredAt)
}

func insertRecoveryStageAudit(
	ctx context.Context,
	connection *sql.Conn,
	id string,
	objectType string,
	action string,
	actorID int64,
	requestID string,
	sequence uint64,
	stage string,
	result config.StageResult,
	occurredAt time.Time,
) error {
	details, err := json.Marshal(struct {
		Sequence uint64             `json:"sequence"`
		Stage    string             `json:"stage"`
		Result   config.StageResult `json:"result"`
	}{sequence, stage, result})
	if err != nil {
		return err
	}
	operation := config.OperationRecord{
		ID: fmt.Sprintf("%s:%d", id, sequence), ObjectType: objectType, ObjectID: id,
		Action: action, Result: string(result), RequestID: requestID, OccurredAt: occurredAt,
	}
	return insertOperationAndAudit(ctx, connection, operation, config.AuditEvent{
		OperationID: operation.ID, OccurredAt: occurredAt, ActorUserID: actorID,
		Action: action, ObjectType: objectType, ObjectID: id, Result: string(result),
		RequestID: requestID, DetailsJSON: string(details),
	})
}

func insertTaskAttentionCase(
	ctx context.Context,
	connection *sql.Conn,
	id config.AttentionCaseID,
	subjectType config.AttentionSubjectType,
	subjectID string,
	backupID config.BackupID,
	reason string,
	openedAt time.Time,
) error {
	if reason == "" {
		reason = "operation_needs_attention"
	}
	_, err := connection.ExecContext(ctx, `INSERT INTO config_attention_cases(
		id, subject_type, subject_id, workspace_id, backup_id, state, reason_code,
		public_evidence_json, opened_at
	) VALUES (?, ?, ?, NULL, ?, 'open', ?, '{}', ?)
	ON CONFLICT(subject_type, subject_id) DO NOTHING`, id, subjectType, subjectID,
		nullableBackupID(backupID), reason, formatTime(openedAt))
	if err != nil {
		return mapConfigConstraint("insert task attention case", err)
	}
	return nil
}

func acquireProductionLeaseTx(
	ctx context.Context,
	connection *sql.Conn,
	ownerType config.ProductionOperationKind,
	ownerID string,
	acquiredAt time.Time,
) error {
	result, err := connection.ExecContext(ctx, `UPDATE config_production_lease SET
		owner_type = ?, owner_id = ?, acquired_at = ?
		WHERE singleton = 1 AND owner_type IS NULL AND owner_id IS NULL AND acquired_at IS NULL`,
		ownerType, ownerID, formatTime(acquiredAt))
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
	return nil
}

func releaseProductionLeaseTx(
	ctx context.Context,
	connection *sql.Conn,
	ownerType config.ProductionOperationKind,
	ownerID string,
) error {
	result, err := connection.ExecContext(ctx, `UPDATE config_production_lease SET
		owner_type = NULL, owner_id = NULL, acquired_at = NULL
		WHERE singleton = 1 AND owner_type = ? AND owner_id = ?`, ownerType, ownerID)
	if err != nil {
		return err
	}
	matched, err := result.RowsAffected()
	if err != nil || matched != 1 {
		return errors.Join(config.ErrConflict, err)
	}
	return nil
}

func requireUsableRestoreTarget(ctx context.Context, connection *sql.Conn, id config.BackupID) error {
	var state config.BackupState
	var bodyPresent int
	if err := connection.QueryRowContext(ctx, `SELECT state, body_present FROM config_backups WHERE id = ?`, id).Scan(
		&state, &bodyPresent,
	); err != nil {
		return err
	}
	if state != config.BackupStateComplete || bodyPresent != 1 {
		return config.ErrConflict
	}
	return nil
}

func requireOpenAttentionCase(ctx context.Context, connection *sql.Conn, id config.AttentionCaseID) error {
	if id == "" {
		return nil
	}
	var state config.AttentionCaseState
	if err := connection.QueryRowContext(ctx, `SELECT state FROM config_attention_cases WHERE id = ?`, id).Scan(&state); err != nil {
		return err
	}
	if state != config.AttentionCaseOpen {
		return config.ErrConflict
	}
	return nil
}

func validHistoryQuery(query config.HistoryQuery, parseID func(string) error) bool {
	if query.Limit <= 0 || query.Limit > recoveryPageLimit ||
		(query.BeforeCreatedAt.IsZero() != (query.BeforeID == "")) {
		return false
	}
	return query.BeforeID == "" || parseID(query.BeforeID) == nil
}

func validAttentionResolution(resolutionType config.AttentionResolutionType, id string) bool {
	switch resolutionType {
	case config.AttentionResolutionRestore:
		_, err := config.ParseRestoreID(id)
		return err == nil
	case config.AttentionResolutionRestart:
		_, err := config.ParseRestartID(id)
		return err == nil
	case config.AttentionResolutionVerification:
		_, err := config.ParseVerificationID(id)
		return err == nil
	default:
		return false
	}
}

func requireSuccessfulResolution(
	ctx context.Context,
	connection *sql.Conn,
	resolutionType config.AttentionResolutionType,
	id string,
) error {
	var state string
	var statement string
	switch resolutionType {
	case config.AttentionResolutionRestore:
		statement = "SELECT state FROM config_restores WHERE id = ?"
	case config.AttentionResolutionRestart:
		statement = "SELECT state FROM config_restarts WHERE id = ?"
	case config.AttentionResolutionVerification:
		statement = "SELECT state FROM config_verifications WHERE id = ?"
	default:
		return config.ErrConflict
	}
	if err := connection.QueryRowContext(ctx, statement, id).Scan(&state); err != nil {
		return err
	}
	if state != "succeeded" {
		return config.ErrConflict
	}
	return nil
}

func nullableAttentionCaseID(id config.AttentionCaseID) any {
	if id == "" {
		return nil
	}
	return id
}

func nullablePositiveInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}
