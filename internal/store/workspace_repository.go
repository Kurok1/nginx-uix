/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/kuroky/nginx-uix/internal/config"
)

var (
	_ config.WorkspaceReader = (*DB)(nil)
	_ config.WorkspaceWriter = (*DB)(nil)
)

// WorkspaceUsage returns the current workspace count and total durable workspace bytes.
func (d *DB) WorkspaceUsage(ctx context.Context) (int, int64, error) {
	var count int64
	var bytes int64
	if err := d.sql.QueryRowContext(
		ctx,
		"SELECT COUNT(*), COALESCE(SUM(workspace_bytes), 0) FROM config_workspaces",
	).Scan(&count, &bytes); err != nil {
		return 0, 0, fmt.Errorf("read workspace usage: %w", err)
	}
	if count > int64(math.MaxInt) {
		return 0, 0, fmt.Errorf("read workspace usage: count exceeds platform integer")
	}
	return int(count), bytes, nil
}

// ListWorkspaces returns workspaces ordered by most recent update and stable ID.
func (d *DB) ListWorkspaces(ctx context.Context) (workspaces []config.Workspace, returnErr error) {
	rows, err := d.sql.QueryContext(ctx, workspaceSelect+" ORDER BY updated_at DESC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close workspace list: %w", err)
		}
	}()

	workspaces = make([]config.Workspace, 0)
	for rows.Next() {
		workspace, err := scanWorkspace(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace list: %w", err)
		}
		workspaces = append(workspaces, workspace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace list: %w", err)
	}
	return workspaces, nil
}

// Workspace returns one exact workspace identifier.
func (d *DB) Workspace(ctx context.Context, id config.WorkspaceID) (config.Workspace, error) {
	workspace, err := scanWorkspace(d.sql.QueryRowContext(ctx, workspaceSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Workspace{}, fmt.Errorf("find workspace: %w", sql.ErrNoRows)
	}
	if err != nil {
		return config.Workspace{}, fmt.Errorf("find workspace: %w", err)
	}
	return workspace, nil
}

// CreateWorkspace atomically inserts workspace metadata, operation metadata and audit metadata.
func (d *DB) CreateWorkspace(ctx context.Context, creation config.WorkspaceCreation) error {
	if err := validateWorkspaceCreation(creation.Workspace); err != nil {
		return err
	}
	if creation.Operation.ObjectID != string(creation.Workspace.ID) {
		return fmt.Errorf("create workspace: operation object id mismatch")
	}

	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		replayed, err := operationAuditReplay(ctx, connection, creation.Operation, creation.Audit)
		if err != nil {
			return fmt.Errorf("check create replay: %w", err)
		}
		if replayed {
			return nil
		}
		workspace := creation.Workspace
		if _, err := connection.ExecContext(
			ctx,
			`INSERT INTO config_workspaces(
				id, name, state, state_reason_code, production_digest, base_digest, draft_digest,
				manifest_version, policy_version, entry_count, managed_bytes, workspace_bytes,
				revision, last_release_id, created_by, created_at, updated_at
			 ) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?, ?, ?, ?, ?, ?, ?)`,
			workspace.ID,
			workspace.Name,
			workspace.State,
			workspace.StateReasonCode,
			workspace.ProductionDigest[:],
			workspace.BaseDigest[:],
			workspace.DraftDigest[:],
			workspace.EntryCount,
			workspace.ManagedBytes,
			workspace.WorkspaceBytes,
			workspace.Revision,
			nullableReleaseID(workspace.LastReleaseID),
			workspace.CreatedBy,
			formatTime(workspace.CreatedAt),
			formatTime(workspace.UpdatedAt),
		); err != nil {
			return mapConfigConstraint("insert workspace", err)
		}
		return insertOperationAndAudit(ctx, connection, creation.Operation, creation.Audit)
	})
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	return nil
}

// UpdateWorkspace atomically advances one exact workspace revision with its audit records.
func (d *DB) UpdateWorkspace(ctx context.Context, change config.WorkspaceChange) error {
	if err := validateWorkspaceChange(change); err != nil {
		return err
	}
	if change.Operation.ObjectID != string(change.Next.ID) {
		return fmt.Errorf("update workspace: operation object id mismatch")
	}

	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		replayed, err := operationAuditReplay(ctx, connection, change.Operation, change.Audit)
		if err != nil {
			return fmt.Errorf("check update replay: %w", err)
		}
		if replayed {
			return nil
		}
		next := change.Next
		result, err := connection.ExecContext(
			ctx,
			`UPDATE config_workspaces SET
				name = ?, state = ?, state_reason_code = ?, production_digest = ?, base_digest = ?,
				draft_digest = ?, entry_count = ?, managed_bytes = ?, workspace_bytes = ?, revision = ?,
				last_release_id = ?, updated_at = ?
			 WHERE id = ? AND revision = ?`,
			next.Name,
			next.State,
			next.StateReasonCode,
			next.ProductionDigest[:],
			next.BaseDigest[:],
			next.DraftDigest[:],
			next.EntryCount,
			next.ManagedBytes,
			next.WorkspaceBytes,
			next.Revision,
			nullableReleaseID(next.LastReleaseID),
			formatTime(next.UpdatedAt),
			next.ID,
			change.ExpectedRevision,
		)
		if err != nil {
			return mapConfigConstraint("update workspace", err)
		}
		matched, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read updated workspace count: %w", err)
		}
		if matched == 0 {
			return workspaceCASMiss(ctx, connection, next.ID)
		}
		return insertOperationAndAudit(ctx, connection, change.Operation, change.Audit)
	})
	if err != nil {
		return fmt.Errorf("update workspace: %w", err)
	}
	return nil
}

// DeleteWorkspace atomically removes one exact workspace revision and records its audit metadata.
func (d *DB) DeleteWorkspace(ctx context.Context, deletion config.WorkspaceDeletion) error {
	if _, err := config.ParseWorkspaceID(string(deletion.ID)); err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	if !validRevision(deletion.ExpectedRevision) {
		return fmt.Errorf("delete workspace: %w", config.ErrConflict)
	}
	if deletion.Operation.ObjectID != string(deletion.ID) {
		return fmt.Errorf("delete workspace: operation object id mismatch")
	}

	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		replayed, err := operationAuditReplay(ctx, connection, deletion.Operation, deletion.Audit)
		if err != nil {
			return fmt.Errorf("check delete replay: %w", err)
		}
		if replayed {
			return nil
		}
		result, err := connection.ExecContext(
			ctx,
			"DELETE FROM config_workspaces WHERE id = ? AND revision = ?",
			deletion.ID,
			deletion.ExpectedRevision,
		)
		if err != nil {
			return mapConfigConstraint("delete workspace", err)
		}
		matched, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read deleted workspace count: %w", err)
		}
		if matched == 0 {
			return workspaceCASMiss(ctx, connection, deletion.ID)
		}
		return insertOperationAndAudit(ctx, connection, deletion.Operation, deletion.Audit)
	})
	if err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	return nil
}

const workspaceSelect = `SELECT
	id, name, state, state_reason_code, production_digest, base_digest, draft_digest,
	entry_count, managed_bytes, workspace_bytes, revision, last_release_id, created_by, created_at, updated_at
FROM config_workspaces`

func scanWorkspace(row rowScanner) (config.Workspace, error) {
	var workspace config.Workspace
	var productionDigest, baseDigest, draftDigest []byte
	var revision int64
	var lastReleaseID sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(
		&workspace.ID,
		&workspace.Name,
		&workspace.State,
		&workspace.StateReasonCode,
		&productionDigest,
		&baseDigest,
		&draftDigest,
		&workspace.EntryCount,
		&workspace.ManagedBytes,
		&workspace.WorkspaceBytes,
		&revision,
		&lastReleaseID,
		&workspace.CreatedBy,
		&createdAt,
		&updatedAt,
	); err != nil {
		return config.Workspace{}, err
	}
	if len(productionDigest) != len(workspace.ProductionDigest) ||
		len(baseDigest) != len(workspace.BaseDigest) || len(draftDigest) != len(workspace.DraftDigest) {
		return config.Workspace{}, fmt.Errorf("decode workspace: invalid digest length")
	}
	if revision < 1 {
		return config.Workspace{}, fmt.Errorf("decode workspace: invalid revision")
	}
	copy(workspace.ProductionDigest[:], productionDigest)
	copy(workspace.BaseDigest[:], baseDigest)
	copy(workspace.DraftDigest[:], draftDigest)
	workspace.Revision = uint64(revision)
	if lastReleaseID.Valid {
		parsed, err := config.ParseReleaseID(lastReleaseID.String)
		if err != nil {
			return config.Workspace{}, fmt.Errorf("decode workspace: invalid release id")
		}
		workspace.LastReleaseID = parsed
	}

	parsedCreatedAt, err := parseTime("workspace creation", createdAt)
	if err != nil {
		return config.Workspace{}, err
	}
	workspace.CreatedAt = parsedCreatedAt
	parsedUpdatedAt, err := parseTime("workspace update", updatedAt)
	if err != nil {
		return config.Workspace{}, err
	}
	workspace.UpdatedAt = parsedUpdatedAt
	return workspace, nil
}

func validateWorkspaceCreation(workspace config.Workspace) error {
	if workspace.Revision != 1 {
		return fmt.Errorf("create workspace: %w", config.ErrConflict)
	}
	return validateWorkspace(workspace, "create workspace")
}

func validateWorkspaceChange(change config.WorkspaceChange) error {
	if !validRevision(change.ExpectedRevision) || change.ExpectedRevision == math.MaxInt64 ||
		change.Next.Revision != change.ExpectedRevision+1 {
		return fmt.Errorf("update workspace: %w", config.ErrConflict)
	}
	return validateWorkspace(change.Next, "update workspace")
}

func validateWorkspace(workspace config.Workspace, action string) error {
	if _, err := config.ParseWorkspaceID(string(workspace.ID)); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	display, err := config.ValidateDisplayName(workspace.Name)
	if err != nil || display != workspace.Name {
		return fmt.Errorf("%s: %w", action, config.ErrDisplayNameInvalid)
	}
	switch workspace.State {
	case config.StatePreparing, config.StateReady, config.StateStale, config.StatePublished, config.StateNeedsAttention:
	default:
		return fmt.Errorf("%s: invalid workspace state", action)
	}
	if workspace.EntryCount < 0 || workspace.ManagedBytes < 0 || workspace.WorkspaceBytes < 0 {
		return fmt.Errorf("%s: negative workspace usage", action)
	}
	if workspace.CreatedBy <= 0 || !validRevision(workspace.Revision) {
		return fmt.Errorf("%s: invalid workspace metadata", action)
	}
	if workspace.State == config.StatePublished && workspace.LastReleaseID == "" {
		return fmt.Errorf("%s: published workspace lacks release id", action)
	}
	if workspace.LastReleaseID != "" {
		if _, err := config.ParseReleaseID(string(workspace.LastReleaseID)); err != nil {
			return fmt.Errorf("%s: invalid release id", action)
		}
	}
	return nil
}

func nullableReleaseID(id config.ReleaseID) any {
	if id == "" {
		return nil
	}
	return id
}

func validRevision(revision uint64) bool {
	return revision >= 1 && revision <= math.MaxInt64
}

func workspaceCASMiss(ctx context.Context, connection *sql.Conn, id config.WorkspaceID) error {
	var exists int
	err := connection.QueryRowContext(
		ctx,
		"SELECT 1 FROM config_workspaces WHERE id = ? LIMIT 1",
		id,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return fmt.Errorf("check workspace after revision miss: %w", err)
	}
	return config.ErrConflict
}
