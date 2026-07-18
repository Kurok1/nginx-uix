/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
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

const releaseStageReadLimit = 512

// CreatePublishCheck persists one complete candidate-validation result.
func (d *DB) CreatePublishCheck(ctx context.Context, check config.PublishCheck) error {
	if err := validatePublishCheck(check); err != nil {
		return err
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if _, err := connection.ExecContext(ctx, `INSERT INTO config_publish_checks(
			id, workspace_id, workspace_revision, production_digest, base_digest, draft_digest,
			candidate_digest, manifest_version, policy_version, validator_version, validator_build_id,
			state, diagnostic_count, public_details_json, created_by, request_id,
			started_at, finished_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			check.ID, check.WorkspaceID, check.WorkspaceRevision, check.ProductionDigest[:],
			check.BaseDigest[:], check.DraftDigest[:], nullableReleaseDigest(check.CandidateDigest),
			check.ManifestVersion, check.PolicyVersion, check.ValidatorVersion, check.ValidatorBuildID,
			check.State, check.DiagnosticCount, check.PublicDetailsJSON, check.CreatedBy, check.RequestID,
			formatTime(check.StartedAt), nullableTime(check.FinishedAt), formatTime(check.ExpiresAt),
		); err != nil {
			return mapConfigConstraint("create publish check", err)
		}
		return insertPublishCheckAudits(ctx, connection, check)
	})
	if err != nil {
		return fmt.Errorf("create publish check: %w", err)
	}
	return nil
}

// PublishCheck returns one exact candidate-validation result.
func (d *DB) PublishCheck(ctx context.Context, id config.PublishCheckID) (config.PublishCheck, error) {
	if _, err := config.ParsePublishCheckID(string(id)); err != nil {
		return config.PublishCheck{}, fmt.Errorf("read publish check: %w", err)
	}
	check, err := scanPublishCheck(d.sql.QueryRowContext(ctx, publishCheckSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.PublishCheck{}, fmt.Errorf("read publish check: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.PublishCheck{}, fmt.Errorf("read publish check: %w", err)
	}
	return check, nil
}

// CreateRelease atomically creates a queued release and its first immutable stage.
func (d *DB) CreateRelease(ctx context.Context, release config.Release, stage config.ReleaseStage) error {
	if err := validateRelease(release); err != nil {
		return err
	}
	if err := validateReleaseStage(stage); err != nil || stage.ReleaseID != release.ID ||
		stage.Sequence != 1 || stage.Stage != release.Stage {
		return fmt.Errorf("create release: invalid initial stage")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var active string
		err := connection.QueryRowContext(ctx, `SELECT id FROM config_releases
			WHERE state IN ('queued', 'running', 'rolling_back') LIMIT 1`).Scan(&active)
		if err == nil {
			return config.ErrReleaseInProgress
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("inspect active release: %w", err)
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO config_releases(
			id, workspace_id, check_id, backup_id, state, stage, production_digest, draft_digest,
			candidate_digest, last_error_code, created_by, request_id, created_at, updated_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			release.ID, release.WorkspaceID, release.CheckID, nullableBackupID(release.BackupID),
			release.State, release.Stage, release.ProductionDigest[:], release.DraftDigest[:],
			release.CandidateDigest[:], release.LastErrorCode, release.CreatedBy, release.RequestID,
			formatTime(release.CreatedAt), formatTime(release.UpdatedAt), nullableTime(release.FinishedAt),
		); err != nil {
			return mapConfigConstraint("insert release", err)
		}
		if err := insertReleaseStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertReleaseStageAudit(ctx, connection, release, stage)
	})
	if err != nil {
		if errors.Is(err, config.ErrReleaseInProgress) {
			return fmt.Errorf("create release: %w", config.ErrReleaseInProgress)
		}
		return fmt.Errorf("create release: %w", err)
	}
	return nil
}

// TransitionRelease atomically applies one exact state/stage CAS and appends the next stage.
func (d *DB) TransitionRelease(
	ctx context.Context,
	expectedState config.ReleaseState,
	expectedStage config.ReleaseStageName,
	next config.Release,
	stage config.ReleaseStage,
) error {
	if err := validateRelease(next); err != nil {
		return err
	}
	if err := validateReleaseStage(stage); err != nil || stage.ReleaseID != next.ID || stage.Stage != next.Stage {
		return fmt.Errorf("transition release: invalid next stage")
	}
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState config.ReleaseState
		var currentStage config.ReleaseStageName
		var lastSequence int64
		if err := connection.QueryRowContext(ctx, `SELECT r.state, r.stage,
			COALESCE((SELECT MAX(sequence) FROM config_release_stages WHERE release_id = r.id), 0)
			FROM config_releases AS r WHERE r.id = ?`, next.ID).Scan(
			&currentState, &currentStage, &lastSequence,
		); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage ||
			lastSequence < 0 || uint64(lastSequence)+1 != stage.Sequence {
			return config.ErrConflict
		}
		result, err := connection.ExecContext(ctx, `UPDATE config_releases SET
			backup_id = ?, state = ?, stage = ?, last_error_code = ?, updated_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND stage = ?`,
			nullableBackupID(next.BackupID), next.State, next.Stage, next.LastErrorCode,
			formatTime(next.UpdatedAt), nullableTime(next.FinishedAt), next.ID, expectedState, expectedStage,
		)
		if err != nil {
			return mapConfigConstraint("update release", err)
		}
		matched, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read transitioned release count: %w", err)
		}
		if matched != 1 {
			return config.ErrConflict
		}
		if err := insertReleaseStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertReleaseStageAudit(ctx, connection, next, stage)
	})
	if err != nil {
		return fmt.Errorf("transition release: %w", err)
	}
	return nil
}

// Release returns one exact publication attempt.
func (d *DB) Release(ctx context.Context, id config.ReleaseID) (config.Release, error) {
	if _, err := config.ParseReleaseID(string(id)); err != nil {
		return config.Release{}, fmt.Errorf("read release: %w", err)
	}
	release, err := scanRelease(d.sql.QueryRowContext(ctx, releaseSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Release{}, fmt.Errorf("read release: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.Release{}, fmt.Errorf("read release: %w", err)
	}
	return release, nil
}

// ActiveRelease returns the sole queued, running or rolling-back release.
func (d *DB) ActiveRelease(ctx context.Context) (config.Release, error) {
	release, err := scanRelease(d.sql.QueryRowContext(ctx, releaseSelect+
		" WHERE state IN ('queued', 'running', 'rolling_back') LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Release{}, fmt.Errorf("read active release: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.Release{}, fmt.Errorf("read active release: %w", err)
	}
	return release, nil
}

// ReleaseStages returns immutable stages after one sequence in ascending order.
func (d *DB) ReleaseStages(ctx context.Context, id config.ReleaseID, after uint64, limit int) (stages []config.ReleaseStage, returnErr error) {
	if _, err := config.ParseReleaseID(string(id)); err != nil || after > math.MaxInt64 || limit <= 0 || limit > releaseStageReadLimit {
		return nil, fmt.Errorf("list release stages: invalid input")
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT release_id, sequence, stage, result, code,
		public_details_json, occurred_at FROM config_release_stages
		WHERE release_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list release stages: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, rows.Close())
	}()
	stages = make([]config.ReleaseStage, 0)
	for rows.Next() {
		stage, err := scanReleaseStage(rows)
		if err != nil {
			return nil, fmt.Errorf("list release stages: %w", err)
		}
		stages = append(stages, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list release stages: %w", err)
	}
	return stages, nil
}

// PutBackup inserts or advances one immutable backup index.
func (d *DB) PutBackup(ctx context.Context, backup config.Backup) error {
	if err := validateBackup(backup); err != nil {
		return err
	}
	_, err := d.sql.ExecContext(ctx, `INSERT INTO config_backups(
		id, release_id, state, entry_count, total_bytes, created_at, verified_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		state = excluded.state, entry_count = excluded.entry_count, total_bytes = excluded.total_bytes,
		verified_at = excluded.verified_at
	WHERE config_backups.release_id = excluded.release_id
		AND config_backups.state = 'creating'
		AND excluded.state IN ('complete', 'invalid')`,
		backup.ID, backup.ReleaseID, backup.State, backup.EntryCount, backup.TotalBytes,
		formatTime(backup.CreatedAt), nullableTime(backup.VerifiedAt),
	)
	if err != nil {
		return mapConfigConstraint("put backup", err)
	}
	return nil
}

// Backup returns one exact immutable-backup index.
func (d *DB) Backup(ctx context.Context, id config.BackupID) (config.Backup, error) {
	if _, err := config.ParseBackupID(string(id)); err != nil {
		return config.Backup{}, fmt.Errorf("read backup: %w", err)
	}
	backup, err := scanBackup(d.sql.QueryRowContext(ctx, `SELECT id, release_id, state,
		entry_count, total_bytes, created_at, verified_at FROM config_backups WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return config.Backup{}, fmt.Errorf("read backup: %w", fs.ErrNotExist)
	}
	if err != nil {
		return config.Backup{}, fmt.Errorf("read backup: %w", err)
	}
	return backup, nil
}

const publishCheckSelect = `SELECT id, workspace_id, workspace_revision, production_digest,
	base_digest, draft_digest, candidate_digest, manifest_version, policy_version, validator_version,
	validator_build_id, state, diagnostic_count, public_details_json, created_by, request_id,
	started_at, finished_at, expires_at FROM config_publish_checks`

const releaseSelect = `SELECT id, workspace_id, check_id, backup_id, state, stage,
	production_digest, draft_digest, candidate_digest, last_error_code, created_by, request_id,
	created_at, updated_at, finished_at FROM config_releases`

func insertReleaseStage(ctx context.Context, connection *sql.Conn, stage config.ReleaseStage) error {
	_, err := connection.ExecContext(ctx, `INSERT INTO config_release_stages(
		release_id, sequence, stage, result, code, public_details_json, occurred_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, stage.ReleaseID, stage.Sequence, stage.Stage, stage.Result,
		stage.Code, stage.PublicDetailsJSON, formatTime(stage.OccurredAt))
	if err != nil {
		return mapConfigConstraint("insert release stage", err)
	}
	return nil
}

func insertPublishCheckAudits(ctx context.Context, connection *sql.Conn, check config.PublishCheck) error {
	startDetails, err := json.Marshal(struct {
		WorkspaceRevision uint64 `json:"workspace_revision"`
		ManifestVersion   uint16 `json:"manifest_version"`
		PolicyVersion     uint16 `json:"policy_version"`
	}{
		WorkspaceRevision: check.WorkspaceRevision,
		ManifestVersion:   check.ManifestVersion,
		PolicyVersion:     check.PolicyVersion,
	})
	if err != nil {
		return fmt.Errorf("encode publish check start audit: %w", err)
	}
	start := config.OperationRecord{
		ID: string(check.ID) + ":start", ObjectType: "config_publish_check", ObjectID: string(check.ID),
		Action: "config.publish_check.start", BeforeDigest: &check.ProductionDigest, AfterDigest: &check.DraftDigest,
		Result: "started", RequestID: check.RequestID, OccurredAt: check.StartedAt,
	}
	if err := insertOperationAndAudit(ctx, connection, start, config.AuditEvent{
		OperationID: start.ID, OccurredAt: start.OccurredAt, ActorUserID: check.CreatedBy,
		Action: start.Action, ObjectType: start.ObjectType, ObjectID: start.ObjectID,
		Result: start.Result, RequestID: start.RequestID, DetailsJSON: string(startDetails),
	}); err != nil {
		return err
	}

	resultDetails, err := json.Marshal(struct {
		State            config.PublishCheckState `json:"state"`
		DiagnosticCount  int                      `json:"diagnostic_count"`
		ValidatorVersion uint16                   `json:"validator_version"`
		ValidatorBuildID string                   `json:"validator_build_id"`
	}{
		State: check.State, DiagnosticCount: check.DiagnosticCount,
		ValidatorVersion: check.ValidatorVersion, ValidatorBuildID: check.ValidatorBuildID,
	})
	if err != nil {
		return fmt.Errorf("encode publish check result audit: %w", err)
	}
	result := config.OperationRecord{
		ID: string(check.ID) + ":result", ObjectType: "config_publish_check", ObjectID: string(check.ID),
		Action: "config.publish_check.result", BeforeDigest: &check.DraftDigest,
		Result: string(check.State), RequestID: check.RequestID, OccurredAt: check.FinishedAt,
	}
	if check.CandidateDigest != (config.Digest{}) {
		result.AfterDigest = &check.CandidateDigest
	}
	return insertOperationAndAudit(ctx, connection, result, config.AuditEvent{
		OperationID: result.ID, OccurredAt: result.OccurredAt, ActorUserID: check.CreatedBy,
		Action: result.Action, ObjectType: result.ObjectType, ObjectID: result.ObjectID,
		Result: result.Result, RequestID: result.RequestID, DetailsJSON: string(resultDetails),
	})
}

func insertReleaseStageAudit(ctx context.Context, connection *sql.Conn, release config.Release, stage config.ReleaseStage) error {
	details, err := json.Marshal(struct {
		Sequence uint64                  `json:"sequence"`
		Stage    config.ReleaseStageName `json:"stage"`
		Result   config.StageResult      `json:"result"`
	}{Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result})
	if err != nil {
		return fmt.Errorf("encode release stage audit: %w", err)
	}
	operation := config.OperationRecord{
		ID: fmt.Sprintf("%s:%d", release.ID, stage.Sequence), ObjectType: "config_release", ObjectID: string(release.ID),
		Action: "config.release.stage", BeforeDigest: &release.ProductionDigest, AfterDigest: &release.CandidateDigest,
		Result: string(stage.Result), RequestID: release.RequestID, OccurredAt: stage.OccurredAt,
	}
	return insertOperationAndAudit(ctx, connection, operation, config.AuditEvent{
		OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: release.CreatedBy,
		Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
		Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
	})
}

func scanPublishCheck(row rowScanner) (config.PublishCheck, error) {
	var check config.PublishCheck
	var production, base, draft, candidate []byte
	var revision int64
	var manifestVersion, policyVersion, validatorVersion int64
	var startedAt, expiresAt string
	var finishedAt sql.NullString
	if err := row.Scan(&check.ID, &check.WorkspaceID, &revision, &production, &base, &draft, &candidate,
		&manifestVersion, &policyVersion, &validatorVersion, &check.ValidatorBuildID, &check.State,
		&check.DiagnosticCount, &check.PublicDetailsJSON, &check.CreatedBy, &check.RequestID,
		&startedAt, &finishedAt, &expiresAt); err != nil {
		return config.PublishCheck{}, err
	}
	if revision < 1 || manifestVersion < 1 || manifestVersion > math.MaxUint16 ||
		policyVersion < 1 || policyVersion > math.MaxUint16 || validatorVersion < 1 || validatorVersion > math.MaxUint16 ||
		!copyDigest(&check.ProductionDigest, production) || !copyDigest(&check.BaseDigest, base) ||
		!copyDigest(&check.DraftDigest, draft) || (candidate != nil && !copyDigest(&check.CandidateDigest, candidate)) {
		return config.PublishCheck{}, fmt.Errorf("decode publish check: invalid metadata")
	}
	check.WorkspaceRevision = uint64(revision)
	check.ManifestVersion = uint16(manifestVersion)
	check.PolicyVersion = uint16(policyVersion)
	check.ValidatorVersion = uint16(validatorVersion)
	var err error
	if check.StartedAt, err = parseTime("publish check start", startedAt); err != nil {
		return config.PublishCheck{}, err
	}
	if finishedAt.Valid {
		if check.FinishedAt, err = parseTime("publish check finish", finishedAt.String); err != nil {
			return config.PublishCheck{}, err
		}
	}
	if check.ExpiresAt, err = parseTime("publish check expiration", expiresAt); err != nil {
		return config.PublishCheck{}, err
	}
	return check, nil
}

func scanRelease(row rowScanner) (config.Release, error) {
	var release config.Release
	var backupID sql.NullString
	var production, draft, candidate []byte
	var createdAt, updatedAt string
	var finishedAt sql.NullString
	if err := row.Scan(&release.ID, &release.WorkspaceID, &release.CheckID, &backupID,
		&release.State, &release.Stage, &production, &draft, &candidate, &release.LastErrorCode,
		&release.CreatedBy, &release.RequestID, &createdAt, &updatedAt, &finishedAt); err != nil {
		return config.Release{}, err
	}
	if backupID.Valid {
		release.BackupID = config.BackupID(backupID.String)
	}
	if !copyDigest(&release.ProductionDigest, production) || !copyDigest(&release.DraftDigest, draft) ||
		!copyDigest(&release.CandidateDigest, candidate) {
		return config.Release{}, fmt.Errorf("decode release: invalid digest")
	}
	var err error
	if release.CreatedAt, err = parseTime("release creation", createdAt); err != nil {
		return config.Release{}, err
	}
	if release.UpdatedAt, err = parseTime("release update", updatedAt); err != nil {
		return config.Release{}, err
	}
	if finishedAt.Valid {
		if release.FinishedAt, err = parseTime("release finish", finishedAt.String); err != nil {
			return config.Release{}, err
		}
	}
	return release, nil
}

func scanReleaseStage(row rowScanner) (config.ReleaseStage, error) {
	var stage config.ReleaseStage
	var sequence int64
	var occurredAt string
	if err := row.Scan(&stage.ReleaseID, &sequence, &stage.Stage, &stage.Result, &stage.Code,
		&stage.PublicDetailsJSON, &occurredAt); err != nil {
		return config.ReleaseStage{}, err
	}
	if sequence < 1 {
		return config.ReleaseStage{}, fmt.Errorf("decode release stage: invalid sequence")
	}
	stage.Sequence = uint64(sequence)
	parsed, err := parseTime("release stage", occurredAt)
	if err != nil {
		return config.ReleaseStage{}, err
	}
	stage.OccurredAt = parsed
	return stage, nil
}

func scanBackup(row rowScanner) (config.Backup, error) {
	var backup config.Backup
	var createdAt string
	var verifiedAt sql.NullString
	if err := row.Scan(&backup.ID, &backup.ReleaseID, &backup.State, &backup.EntryCount,
		&backup.TotalBytes, &createdAt, &verifiedAt); err != nil {
		return config.Backup{}, err
	}
	var err error
	if backup.CreatedAt, err = parseTime("backup creation", createdAt); err != nil {
		return config.Backup{}, err
	}
	if verifiedAt.Valid {
		if backup.VerifiedAt, err = parseTime("backup verification", verifiedAt.String); err != nil {
			return config.Backup{}, err
		}
	}
	return backup, nil
}

func validatePublishCheck(check config.PublishCheck) error {
	if _, err := config.ParsePublishCheckID(string(check.ID)); err != nil {
		return fmt.Errorf("validate publish check: %w", err)
	}
	if _, err := config.ParseWorkspaceID(string(check.WorkspaceID)); err != nil {
		return fmt.Errorf("validate publish check: %w", err)
	}
	if check.WorkspaceRevision == 0 || check.ProductionDigest == (config.Digest{}) ||
		check.BaseDigest == (config.Digest{}) || check.DraftDigest == (config.Digest{}) ||
		check.ManifestVersion == 0 || check.PolicyVersion == 0 || check.ValidatorVersion == 0 ||
		check.ValidatorBuildID == "" || check.DiagnosticCount < 0 || check.CreatedBy <= 0 ||
		check.RequestID == "" || check.StartedAt.IsZero() || check.ExpiresAt.IsZero() ||
		!json.Valid([]byte(check.PublicDetailsJSON)) {
		return fmt.Errorf("validate publish check: invalid metadata")
	}
	switch check.State {
	case config.PublishCheckStateRunning:
		if !check.FinishedAt.IsZero() || check.CandidateDigest != (config.Digest{}) {
			return fmt.Errorf("validate publish check: invalid running result")
		}
	case config.PublishCheckStateValid:
		if check.FinishedAt.IsZero() || check.CandidateDigest == (config.Digest{}) {
			return fmt.Errorf("validate publish check: incomplete valid result")
		}
	case config.PublishCheckStateInvalid, config.PublishCheckStateFailed:
		if check.FinishedAt.IsZero() {
			return fmt.Errorf("validate publish check: incomplete terminal result")
		}
	default:
		return fmt.Errorf("validate publish check: invalid state")
	}
	return nil
}

func validateRelease(release config.Release) error {
	if _, err := config.ParseReleaseID(string(release.ID)); err != nil {
		return fmt.Errorf("validate release: %w", err)
	}
	if _, err := config.ParseWorkspaceID(string(release.WorkspaceID)); err != nil {
		return fmt.Errorf("validate release: %w", err)
	}
	if _, err := config.ParsePublishCheckID(string(release.CheckID)); err != nil {
		return fmt.Errorf("validate release: %w", err)
	}
	if release.BackupID != "" {
		if _, err := config.ParseBackupID(string(release.BackupID)); err != nil {
			return fmt.Errorf("validate release: %w", err)
		}
	}
	if release.ProductionDigest == (config.Digest{}) || release.DraftDigest == (config.Digest{}) ||
		release.CandidateDigest == (config.Digest{}) || release.CreatedBy <= 0 || release.RequestID == "" ||
		release.CreatedAt.IsZero() || release.UpdatedAt.IsZero() {
		return fmt.Errorf("validate release: invalid metadata")
	}
	switch release.State {
	case config.ReleaseStateQueued, config.ReleaseStateRunning, config.ReleaseStateRollingBack:
		if !release.FinishedAt.IsZero() {
			return fmt.Errorf("validate release: active release has finish time")
		}
	case config.ReleaseStateSucceeded, config.ReleaseStateFailed, config.ReleaseStateRolledBack,
		config.ReleaseStateNeedsAttention, config.ReleaseStateCancelled:
		if release.FinishedAt.IsZero() {
			return fmt.Errorf("validate release: terminal release lacks finish time")
		}
	default:
		return fmt.Errorf("validate release: invalid state")
	}
	return nil
}

func validateReleaseStage(stage config.ReleaseStage) error {
	if _, err := config.ParseReleaseID(string(stage.ReleaseID)); err != nil {
		return fmt.Errorf("validate release stage: %w", err)
	}
	if stage.Sequence == 0 || stage.Stage == "" || stage.OccurredAt.IsZero() ||
		!json.Valid([]byte(stage.PublicDetailsJSON)) {
		return fmt.Errorf("validate release stage: invalid metadata")
	}
	switch stage.Result {
	case config.StageResultPending, config.StageResultRunning, config.StageResultSuccess,
		config.StageResultFailed, config.StageResultWarning:
	default:
		return fmt.Errorf("validate release stage: invalid result")
	}
	return nil
}

func validateBackup(backup config.Backup) error {
	if _, err := config.ParseBackupID(string(backup.ID)); err != nil {
		return fmt.Errorf("validate backup: %w", err)
	}
	if _, err := config.ParseReleaseID(string(backup.ReleaseID)); err != nil {
		return fmt.Errorf("validate backup: %w", err)
	}
	if backup.EntryCount < 0 || backup.TotalBytes < 0 || backup.CreatedAt.IsZero() {
		return fmt.Errorf("validate backup: invalid metadata")
	}
	switch backup.State {
	case config.BackupStateCreating:
		if !backup.VerifiedAt.IsZero() {
			return fmt.Errorf("validate backup: creating backup is verified")
		}
	case config.BackupStateComplete, config.BackupStateInvalid:
		if backup.VerifiedAt.IsZero() {
			return fmt.Errorf("validate backup: terminal backup lacks verification time")
		}
	default:
		return fmt.Errorf("validate backup: invalid state")
	}
	return nil
}

func copyDigest(target *config.Digest, payload []byte) bool {
	if len(payload) != len(*target) {
		return false
	}
	copy(target[:], payload)
	return true
}

func nullableReleaseDigest(digest config.Digest) any {
	if digest == (config.Digest{}) {
		return nil
	}
	return digest[:]
}

func nullableBackupID(id config.BackupID) any {
	if id == "" {
		return nil
	}
	return id
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}
