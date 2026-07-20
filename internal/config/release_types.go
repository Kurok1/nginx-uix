/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */

package config

import (
	"context"
	"fmt"
	"io"
	"time"
)

// PublishCheckID identifies one digest-bound candidate validation.
type PublishCheckID string

// ReleaseID identifies one durable production publication attempt.
type ReleaseID string

// BackupID identifies one immutable production backup.
type BackupID string

// PublishCheckState describes a candidate validation result.
type PublishCheckState string

const (
	PublishCheckStateRunning PublishCheckState = "running"
	PublishCheckStateValid   PublishCheckState = "valid"
	PublishCheckStateInvalid PublishCheckState = "invalid"
	PublishCheckStateFailed  PublishCheckState = "failed"
)

// CandidateValidationRequest binds an Agent validation to exact filesystem identities.
type CandidateValidationRequest struct {
	WorkspaceID      WorkspaceID
	ProductionDigest Digest
	DraftDigest      Digest
}

// CandidateDiagnostic is a bounded, production-path-free validation finding.
type CandidateDiagnostic struct {
	Code    string
	Path    RelativePath
	Line    int
	Summary string
}

// CandidateValidation is the public evidence returned by the fixed Agent validator.
type CandidateValidation struct {
	Valid            bool
	CandidateDigest  Digest
	ValidatorVersion uint16
	ValidatorBuildID string
	CheckedAt        time.Time
	Diagnostics      []CandidateDiagnostic
}

// PublishCheck is the persisted, expiring evidence for one candidate validation.
type PublishCheck struct {
	ID                PublishCheckID
	WorkspaceID       WorkspaceID
	WorkspaceRevision uint64
	ProductionDigest  Digest
	BaseDigest        Digest
	DraftDigest       Digest
	CandidateDigest   Digest
	ManifestVersion   uint16
	PolicyVersion     uint16
	ValidatorVersion  uint16
	ValidatorBuildID  string
	State             PublishCheckState
	DiagnosticCount   int
	PublicDetailsJSON string
	CreatedBy         int64
	RequestID         string
	StartedAt         time.Time
	FinishedAt        time.Time
	ExpiresAt         time.Time
}

// ReleaseState describes the durable publication task lifecycle.
type ReleaseState string

const (
	ReleaseStateQueued         ReleaseState = "queued"
	ReleaseStateRunning        ReleaseState = "running"
	ReleaseStateRollingBack    ReleaseState = "rolling_back"
	ReleaseStateSucceeded      ReleaseState = "succeeded"
	ReleaseStateFailed         ReleaseState = "failed"
	ReleaseStateRolledBack     ReleaseState = "rolled_back"
	ReleaseStateNeedsAttention ReleaseState = "needs_attention"
	ReleaseStateCancelled      ReleaseState = "cancelled"
)

// ReleaseStageName identifies a persisted publication boundary.
type ReleaseStageName string

const (
	ReleaseStageQueued                  ReleaseStageName = "queued"
	ReleaseStageRechecking              ReleaseStageName = "rechecking"
	ReleaseStageBackupCreating          ReleaseStageName = "backup_creating"
	ReleaseStageBackupVerified          ReleaseStageName = "backup_verified"
	ReleaseStageCandidateValidated      ReleaseStageName = "candidate_validated"
	ReleaseStageFilesApplying           ReleaseStageName = "files_applying"
	ReleaseStageFilesApplied            ReleaseStageName = "files_applied"
	ReleaseStageProductionValidated     ReleaseStageName = "production_validated"
	ReleaseStageReloadRequested         ReleaseStageName = "reload_requested"
	ReleaseStageRuntimeConfirmed        ReleaseStageName = "runtime_confirmed"
	ReleaseStageCommitted               ReleaseStageName = "committed"
	ReleaseStageRollbackApplying        ReleaseStageName = "rollback_applying"
	ReleaseStageRollbackFilesRestored   ReleaseStageName = "rollback_files_restored"
	ReleaseStageRollbackValidated       ReleaseStageName = "rollback_validated"
	ReleaseStageRollbackReloadRequested ReleaseStageName = "rollback_reload_requested"
	ReleaseStageRolledBack              ReleaseStageName = "rolled_back"
	ReleaseStageFailed                  ReleaseStageName = "failed"
	ReleaseStageNeedsAttention          ReleaseStageName = "needs_attention"
)

// StageResult describes one release stage outcome.
type StageResult string

const (
	StageResultPending StageResult = "pending"
	StageResultRunning StageResult = "running"
	StageResultSuccess StageResult = "success"
	StageResultFailed  StageResult = "failed"
	StageResultWarning StageResult = "warning"
)

// Release is the persisted projection of one publication task.
type Release struct {
	ID               ReleaseID
	WorkspaceID      WorkspaceID
	CheckID          PublishCheckID
	BackupID         BackupID
	State            ReleaseState
	Stage            ReleaseStageName
	ProductionDigest Digest
	DraftDigest      Digest
	CandidateDigest  Digest
	LastErrorCode    string
	CreatedBy        int64
	RequestID        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	FinishedAt       time.Time
}

// ReleaseStage is one immutable public stage event.
type ReleaseStage struct {
	ReleaseID         ReleaseID
	Sequence          uint64
	Stage             ReleaseStageName
	Result            StageResult
	Code              string
	PublicDetailsJSON string
	OccurredAt        time.Time
}

// BackupState describes whether a root-owned backup can be used for recovery.
type BackupState string

const (
	BackupStateCreating BackupState = "creating"
	BackupStateComplete BackupState = "complete"
	BackupStateInvalid  BackupState = "invalid"
	BackupStateDeleting BackupState = "deleting"
	BackupStateDeleted  BackupState = "deleted"
)

// BackupOriginType identifies the operation that created an immutable recovery point.
type BackupOriginType string

const (
	BackupOriginRelease BackupOriginType = "release"
	BackupOriginRestore BackupOriginType = "restore"
)

// Backup is the content-free persisted index for one immutable backup.
type Backup struct {
	ID                BackupID
	OriginType        BackupOriginType
	OriginID          string
	ReleaseID         ReleaseID
	ProductionDigest  Digest
	TreeDigest        Digest
	State             BackupState
	EntryCount        int
	TotalBytes        int64
	ManuallyProtected bool
	ProtectionReason  string
	ProtectedBy       int64
	ProtectedAt       time.Time
	BodyPresent       bool
	DeleteRunID       string
	DeleteReason      string
	CreatedAt         time.Time
	VerifiedAt        time.Time
	DeletedAt         time.Time
}

// BackupRequest authorizes one fixed-root immutable backup before production writes.
type BackupRequest struct {
	ReleaseID        ReleaseID
	BackupID         BackupID
	ProductionDigest Digest
}

// BackupEvidence exposes only content-free integrity facts about a protected backup.
type BackupEvidence struct {
	BackupID         BackupID
	OriginType       BackupOriginType
	OriginID         string
	ReleaseID        ReleaseID
	ProductionDigest Digest
	TreeDigest       Digest
	EntryCount       int
	TotalBytes       int64
	VerifiedAt       time.Time
}

// ReleaseExecutionRequest is the complete typed Agent authorization for one fixed-root transaction.
type ReleaseExecutionRequest struct {
	ReleaseID        ReleaseID
	BackupID         BackupID
	WorkspaceID      WorkspaceID
	ProductionDigest Digest
	DraftDigest      Digest
	CandidateDigest  Digest
}

// ReleaseExecutionResult is the content-free durable Agent outcome.
type ReleaseExecutionResult struct {
	ReleaseID   ReleaseID
	State       ReleaseState
	Stage       ReleaseStageName
	Backup      BackupEvidence
	Stages      []ReleaseStage
	ErrorCode   string
	MasterPID   int
	WorkerCount int
	HTTPStatus  int
	FinishedAt  time.Time
}

// ReleaseRepository persists publish checks, tasks, stages, and backup indexes.
type ReleaseRepository interface {
	CreatePublishCheck(context.Context, PublishCheck) error
	PublishCheck(context.Context, PublishCheckID) (PublishCheck, error)
	CreateRelease(context.Context, Release, ReleaseStage) error
	TransitionRelease(context.Context, ReleaseState, ReleaseStageName, Release, ReleaseStage) error
	Release(context.Context, ReleaseID) (Release, error)
	ActiveRelease(context.Context) (Release, error)
	ReleaseStages(context.Context, ReleaseID, uint64, int) ([]ReleaseStage, error)
	HasOpenAttentionCases(context.Context) (bool, error)
	PutBackup(context.Context, Backup) error
	Backup(context.Context, BackupID) (Backup, error)
}

// ReleaseAgent exposes only typed fixed-root candidate and transaction operations.
type ReleaseAgent interface {
	ValidateCandidate(context.Context, string, CandidateValidationRequest) (CandidateValidation, error)
	ExecuteRelease(context.Context, string, ReleaseExecutionRequest) (ReleaseExecutionResult, error)
	ReleaseProgress(context.Context, string, ReleaseExecutionRequest) (ReleaseExecutionResult, error)
	RecoverRelease(context.Context, string, ReleaseExecutionRequest) (ReleaseExecutionResult, error)
}

// PublishCheckInput binds a check to the current strong workspace ETag.
type PublishCheckInput struct {
	WorkspaceID WorkspaceID
	IfMatch     string
}

// QueueReleaseInput contains the explicit named confirmation for a validated check.
type QueueReleaseInput struct {
	WorkspaceID WorkspaceID
	CheckID     PublishCheckID
	IfMatch     string
	ConfirmName string
}

// ParsePublishCheckID validates one opaque publish-check identifier.
func ParsePublishCheckID(raw string) (PublishCheckID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse publish check id: %w", ErrIdentifierInvalid)
	}
	return PublishCheckID(raw), nil
}

// NewPublishCheckID returns a new opaque publish-check identifier.
func NewPublishCheckID(random io.Reader) (PublishCheckID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate publish check id: %w", err)
	}
	return PublishCheckID(raw), nil
}

// ParseReleaseID validates one opaque release identifier.
func ParseReleaseID(raw string) (ReleaseID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse release id: %w", ErrIdentifierInvalid)
	}
	return ReleaseID(raw), nil
}

// NewReleaseID returns a new opaque release identifier.
func NewReleaseID(random io.Reader) (ReleaseID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate release id: %w", err)
	}
	return ReleaseID(raw), nil
}

// ParseBackupID validates one opaque backup identifier.
func ParseBackupID(raw string) (BackupID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse backup id: %w", ErrIdentifierInvalid)
	}
	return BackupID(raw), nil
}

// NewBackupID returns a new opaque backup identifier.
func NewBackupID(random io.Reader) (BackupID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate backup id: %w", err)
	}
	return BackupID(raw), nil
}
