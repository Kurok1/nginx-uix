/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package config

import (
	"context"
	"fmt"
	"io"
	"time"
)

// RestoreID identifies one durable manual recovery attempt.
type RestoreID string

// RestartID identifies one fixed Nginx restart attempt.
type RestartID string

// RetentionRunID identifies one persisted backup-retention plan and execution.
type RetentionRunID string

// AttentionCaseID identifies one evidence-bound manual-attention case.
type AttentionCaseID string

// VerificationID identifies one fixed current-production health verification.
type VerificationID string

// VerificationState describes one fixed current-production health verification.
type VerificationState string

const (
	VerificationStateSucceeded VerificationState = "succeeded"
	VerificationStateFailed    VerificationState = "failed"
)

// ProductionOperationKind identifies the sole owner of the production mutation lease.
type ProductionOperationKind string

const (
	ProductionOperationRelease   ProductionOperationKind = "release"
	ProductionOperationRestore   ProductionOperationKind = "restore"
	ProductionOperationRestart   ProductionOperationKind = "restart"
	ProductionOperationRetention ProductionOperationKind = "retention"
)

// ProductionLease is the durable single-writer ownership record.
type ProductionLease struct {
	OwnerType  ProductionOperationKind
	OwnerID    string
	AcquiredAt time.Time
}

// BackupQuery is a bounded keyset query ordered by creation time and ID descending.
type BackupQuery struct {
	BeforeCreatedAt time.Time
	BeforeID        BackupID
	Limit           int
	IncludeDeleted  bool
}

// BackupProtectionReason is one public, content-free reason a backup cannot be deleted.
type BackupProtectionReason struct {
	Kind string
	Code string
}

// BackupView combines an immutable index with dynamically derived protection evidence.
type BackupView struct {
	Backup      Backup
	Protected   bool
	Protections []BackupProtectionReason
}

// BackupProtectionChange is one exact manual protection mutation and its audit evidence.
type BackupProtectionChange struct {
	BackupID          BackupID
	ExpectedProtected bool
	NextProtected     bool
	Reason            string
	Actor             Actor
	Operation         OperationRecord
	Audit             AuditEvent
}

// ChangeBackupProtectionInput is one exact CAS-style manual protection request.
type ChangeBackupProtectionInput struct {
	ExpectedProtected bool
	Protected         bool
	Reason            string
	Confirmation      string
}

// RetentionPolicy contains fixed, trusted backup retention limits.
type RetentionPolicy struct {
	MinimumComplete   int
	MaximumComplete   int
	MaximumTotalBytes int64
	MinimumAge        time.Duration
}

// RetentionRunState describes one retention plan and optional execution.
type RetentionRunState string

const (
	RetentionRunPlanned        RetentionRunState = "planned"
	RetentionRunExecuting      RetentionRunState = "executing"
	RetentionRunSucceeded      RetentionRunState = "succeeded"
	RetentionRunFailed         RetentionRunState = "failed"
	RetentionRunNeedsAttention RetentionRunState = "needs_attention"
	RetentionRunExpired        RetentionRunState = "expired"
)

// RetentionDecision describes whether one snapshot entry is kept or selected for deletion.
type RetentionDecision string

const (
	RetentionDecisionKeep   RetentionDecision = "keep"
	RetentionDecisionDelete RetentionDecision = "delete"
)

// RetentionItemState describes one persisted retention decision outcome.
type RetentionItemState string

const (
	RetentionItemPlanned          RetentionItemState = "planned"
	RetentionItemKept             RetentionItemState = "kept"
	RetentionItemDeleting         RetentionItemState = "deleting"
	RetentionItemDeleted          RetentionItemState = "deleted"
	RetentionItemSkippedProtected RetentionItemState = "skipped_protected"
	RetentionItemFailed           RetentionItemState = "failed"
	RetentionItemNeedsAttention   RetentionItemState = "needs_attention"
)

// RetentionItem is one stable, immutable plan row plus its execution state.
type RetentionItem struct {
	RunID              RetentionRunID
	Ordinal            int
	BackupID           BackupID
	Decision           RetentionDecision
	ReasonCode         string
	State              RetentionItemState
	SnapshotCreatedAt  time.Time
	SnapshotTotalBytes int64
	UpdatedAt          time.Time
}

// RetentionRun is the persisted summary of one deterministic policy evaluation.
type RetentionRun struct {
	ID                 RetentionRunID
	State              RetentionRunState
	Policy             RetentionPolicy
	BackupCount        int
	TotalBytes         int64
	ProtectedCount     int
	DeleteCount        int
	DeleteBytes        int64
	DeletedCount       int
	DeletedBytes       int64
	LastErrorCode      string
	CreatedBy          int64
	RequestID          string
	ExecutionRequestID string
	CreatedAt          time.Time
	ExpiresAt          time.Time
	StartedAt          time.Time
	FinishedAt         time.Time
}

// BackupDeletionRequest binds one fixed-root deletion to a persisted retention decision and exact evidence.
type BackupDeletionRequest struct {
	RunID              RetentionRunID
	BackupID           BackupID
	ProductionDigest   Digest
	TreeDigest         Digest
	SnapshotCreatedAt  time.Time
	SnapshotTotalBytes int64
}

// RestoreBackupRequest authorizes one fixed-root safety backup for a manual restore.
type RestoreBackupRequest struct {
	RestoreID        RestoreID
	BackupID         BackupID
	ProductionDigest Digest
}

// RestoreState describes one manual restore task lifecycle.
type RestoreState string

const (
	RestoreStateQueued         RestoreState = "queued"
	RestoreStateRunning        RestoreState = "running"
	RestoreStateRollingBack    RestoreState = "rolling_back"
	RestoreStateSucceeded      RestoreState = "succeeded"
	RestoreStateFailed         RestoreState = "failed"
	RestoreStateRolledBack     RestoreState = "rolled_back"
	RestoreStateNeedsAttention RestoreState = "needs_attention"
	RestoreStateCancelled      RestoreState = "cancelled"
)

// RestoreStageName identifies a persisted manual-restore boundary.
type RestoreStageName string

const (
	RestoreStageQueued                  RestoreStageName = "queued"
	RestoreStageTargetVerifying         RestoreStageName = "target_verifying"
	RestoreStageTargetValidated         RestoreStageName = "target_validated"
	RestoreStageSafetyBackupCreating    RestoreStageName = "safety_backup_creating"
	RestoreStageSafetyBackupVerified    RestoreStageName = "safety_backup_verified"
	RestoreStageFilesRestoring          RestoreStageName = "files_restoring"
	RestoreStageFilesRestored           RestoreStageName = "files_restored"
	RestoreStageProductionValidated     RestoreStageName = "production_validated"
	RestoreStageReloadRequested         RestoreStageName = "reload_requested"
	RestoreStageRuntimeConfirmed        RestoreStageName = "runtime_confirmed"
	RestoreStageSucceeded               RestoreStageName = "succeeded"
	RestoreStageRollbackApplying        RestoreStageName = "rollback_applying"
	RestoreStageRollbackFilesRestored   RestoreStageName = "rollback_files_restored"
	RestoreStageRollbackValidated       RestoreStageName = "rollback_validated"
	RestoreStageRollbackReloadRequested RestoreStageName = "rollback_reload_requested"
	RestoreStageRolledBack              RestoreStageName = "rolled_back"
	RestoreStageFailed                  RestoreStageName = "failed"
	RestoreStageNeedsAttention          RestoreStageName = "needs_attention"
)

// Restore is the persisted projection of one manual restore task.
type Restore struct {
	ID              RestoreID
	TargetBackupID  BackupID
	SafetyBackupID  BackupID
	AttentionCaseID AttentionCaseID
	State           RestoreState
	Stage           RestoreStageName
	SourceDigest    Digest
	TargetDigest    Digest
	LastErrorCode   string
	CreatedBy       int64
	Reason          string
	RequestID       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	FinishedAt      time.Time
}

// RestoreStage is one immutable public manual-restore stage.
type RestoreStage struct {
	RestoreID         RestoreID
	Sequence          uint64
	Stage             RestoreStageName
	Result            StageResult
	Code              string
	PublicDetailsJSON string
	OccurredAt        time.Time
}

// RestoreExecutionRequest is the complete typed Agent authorization for one restore.
type RestoreExecutionRequest struct {
	RestoreID        RestoreID
	TargetBackupID   BackupID
	SafetyBackupID   BackupID
	SourceDigest     Digest
	TargetDigest     Digest
	TargetTreeDigest Digest
	SafetyTreeDigest Digest
}

// QueueRestoreInput contains the named confirmation and bounded operator context for one manual restore.
type QueueRestoreInput struct {
	TargetBackupID  BackupID
	AttentionCaseID AttentionCaseID
	Reason          string
	ConfirmBackupID string
}

// RestorePreparationResult proves target validation and the current-production safety backup before any write.
type RestorePreparationResult struct {
	RestoreID    RestoreID
	State        RestoreState
	Stage        RestoreStageName
	SafetyBackup BackupEvidence
	Stages       []RestoreStage
	ErrorCode    string
	FinishedAt   time.Time
}

// RestoreExecutionResult is the content-free durable Agent restore outcome.
type RestoreExecutionResult struct {
	RestoreID    RestoreID
	State        RestoreState
	Stage        RestoreStageName
	SafetyBackup BackupEvidence
	Stages       []RestoreStage
	ErrorCode    string
	MasterPID    int
	WorkerCount  int
	HTTPStatus   int
	FinishedAt   time.Time
}

// RestartState describes one fixed runtime restart task.
type RestartState string

const (
	RestartStateQueued         RestartState = "queued"
	RestartStateRunning        RestartState = "running"
	RestartStateSucceeded      RestartState = "succeeded"
	RestartStateFailed         RestartState = "failed"
	RestartStateNeedsAttention RestartState = "needs_attention"
	RestartStateCancelled      RestartState = "cancelled"
)

// RestartStageName identifies a persisted fixed-restart boundary.
type RestartStageName string

const (
	RestartStageQueued               RestartStageName = "queued"
	RestartStageProductionValidating RestartStageName = "production_validating"
	RestartStageRuntimeSampling      RestartStageName = "runtime_sampling"
	RestartStageRestartRequested     RestartStageName = "restart_requested"
	RestartStageRuntimeConfirming    RestartStageName = "runtime_confirming"
	RestartStageSucceeded            RestartStageName = "succeeded"
	RestartStageFailed               RestartStageName = "failed"
	RestartStageNeedsAttention       RestartStageName = "needs_attention"
)

// Restart is the persisted projection of one fixed Nginx restart.
type Restart struct {
	ID               RestartID
	AttentionCaseID  AttentionCaseID
	State            RestartState
	Stage            RestartStageName
	ProductionDigest Digest
	BeforeMasterPID  int
	AfterMasterPID   int
	WorkerCount      int
	HTTPStatus       int
	LastErrorCode    string
	CreatedBy        int64
	Reason           string
	RequestID        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	FinishedAt       time.Time
}

// RestartStage is one immutable public fixed-restart stage.
type RestartStage struct {
	RestartID         RestartID
	Sequence          uint64
	Stage             RestartStageName
	Result            StageResult
	Code              string
	PublicDetailsJSON string
	OccurredAt        time.Time
}

// RestartExecutionRequest binds a restart to the exact production identity observed by the Web service.
type RestartExecutionRequest struct {
	RestartID        RestartID
	ProductionDigest Digest
}

// QueueRestartInput contains the fixed confirmation and bounded operator context for one restart.
type QueueRestartInput struct {
	AttentionCaseID AttentionCaseID
	Reason          string
	Confirmation    string
}

// RestartExecutionResult is the content-free durable Agent restart outcome.
type RestartExecutionResult struct {
	RestartID       RestartID
	State           RestartState
	Stage           RestartStageName
	Stages          []RestartStage
	ErrorCode       string
	BeforeMasterPID int
	AfterMasterPID  int
	WorkerCount     int
	HTTPStatus      int
	FinishedAt      time.Time
}

// AttentionCaseState describes whether a consistency incident still blocks normal mutation.
type AttentionCaseState string

const (
	AttentionCaseOpen     AttentionCaseState = "open"
	AttentionCaseResolved AttentionCaseState = "resolved"
)

// AttentionSubjectType identifies the evidence source that opened a case.
type AttentionSubjectType string

const (
	AttentionSubjectWorkspace AttentionSubjectType = "workspace"
	AttentionSubjectRelease   AttentionSubjectType = "release"
	AttentionSubjectRestore   AttentionSubjectType = "restore"
	AttentionSubjectRestart   AttentionSubjectType = "restart"
)

// AttentionResolutionType identifies one evidence-bearing case disposition.
type AttentionResolutionType string

const (
	AttentionResolutionRestore      AttentionResolutionType = "restore"
	AttentionResolutionRestart      AttentionResolutionType = "restart"
	AttentionResolutionVerification AttentionResolutionType = "verification"
)

// AttentionCase is one persistent consistency incident and optional evidence-bound resolution.
type AttentionCase struct {
	ID                 AttentionCaseID
	SubjectType        AttentionSubjectType
	SubjectID          string
	WorkspaceID        WorkspaceID
	BackupID           BackupID
	State              AttentionCaseState
	ReasonCode         string
	PublicEvidenceJSON string
	OpenedAt           time.Time
	ResolvedBy         int64
	ResolvedAt         time.Time
	ResolutionType     AttentionResolutionType
	ResolutionID       string
}

// Verification is one persisted, content-free current-production health result.
type Verification struct {
	ID               VerificationID
	AttentionCaseID  AttentionCaseID
	State            VerificationState
	ProductionDigest Digest
	MasterPID        int
	WorkerCount      int
	HTTPStatus       int
	LastErrorCode    string
	CreatedBy        int64
	RequestID        string
	CreatedAt        time.Time
	FinishedAt       time.Time
}

// RuntimeVerificationRequest binds Agent health evidence to one exact production identity.
type RuntimeVerificationRequest struct {
	VerificationID   VerificationID
	ProductionDigest Digest
}

// RuntimeVerificationResult contains only fixed, content-free runtime health evidence.
type RuntimeVerificationResult struct {
	VerificationID   VerificationID
	State            VerificationState
	ProductionDigest Digest
	MasterPID        int
	WorkerCount      int
	HTTPStatus       int
	ErrorCode        string
	CheckedAt        time.Time
}

// AuditQuery is a bounded keyset query ordered by occurrence time and numeric ID descending.
type AuditQuery struct {
	BeforeOccurredAt time.Time
	BeforeID         int64
	Limit            int
}

// HistoryQuery is a bounded newest-first task query using a stable time/ID keyset.
type HistoryQuery struct {
	BeforeCreatedAt time.Time
	BeforeID        string
	Limit           int
}

// AttentionQuery is a bounded newest-first case query using an opened-at/ID keyset.
type AttentionQuery struct {
	State          AttentionCaseState
	BeforeOpenedAt time.Time
	BeforeID       AttentionCaseID
	Limit          int
}

// AuditRecord is one user-visible audit row with actor display metadata.
type AuditRecord struct {
	ID          int64
	OccurredAt  time.Time
	ActorUserID int64
	ActorName   string
	Action      string
	ObjectType  string
	ObjectID    string
	Result      string
	RequestID   string
	DetailsJSON string
}

// RecoveryRepository persists history, leases, recovery tasks, retention, and attention evidence.
type RecoveryRepository interface {
	ProductionLease(context.Context) (ProductionLease, error)
	AcquireProductionLease(context.Context, ProductionOperationKind, string, time.Time) error
	ReleaseProductionLease(context.Context, ProductionOperationKind, string) error
	ListBackups(context.Context, BackupQuery) ([]Backup, error)
	RetentionBackups(context.Context) ([]Backup, error)
	Backup(context.Context, BackupID) (Backup, error)
	PutBackup(context.Context, Backup) error
	ChangeBackupProtection(context.Context, BackupProtectionChange) (Backup, error)
	CreateRetentionRun(context.Context, RetentionRun, []RetentionItem) error
	RetentionRun(context.Context, RetentionRunID) (RetentionRun, []RetentionItem, error)
	TransitionRetentionItem(context.Context, RetentionRunID, int, RetentionItemState, RetentionItemState, time.Time) error
	BeginRetentionDeletion(context.Context, RetentionRunID, int, BackupID, time.Time, int64, time.Time) error
	CompleteRetentionDeletion(context.Context, RetentionRunID, int, BackupID, time.Time) error
	AbortRetentionDeletion(context.Context, RetentionRunID, int, BackupID, RetentionItemState, time.Time) error
	MarkRetentionDeletionUncertain(context.Context, RetentionRunID, int, BackupID, time.Time) error
	TransitionRetentionRun(context.Context, RetentionRunState, RetentionRun) error
	CreateRestore(context.Context, Restore, RestoreStage) error
	TransitionRestore(context.Context, RestoreState, RestoreStageName, Restore, RestoreStage) error
	Restore(context.Context, RestoreID) (Restore, error)
	ActiveRestore(context.Context) (Restore, error)
	RestoreStages(context.Context, RestoreID, uint64, int) ([]RestoreStage, error)
	ListRestores(context.Context, HistoryQuery) ([]Restore, error)
	CreateRestart(context.Context, Restart, RestartStage) error
	TransitionRestart(context.Context, RestartState, RestartStageName, Restart, RestartStage) error
	Restart(context.Context, RestartID) (Restart, error)
	ActiveRestart(context.Context) (Restart, error)
	RestartStages(context.Context, RestartID, uint64, int) ([]RestartStage, error)
	ListRestarts(context.Context, HistoryQuery) ([]Restart, error)
	ListReleases(context.Context, HistoryQuery) ([]Release, error)
	ListAuditEvents(context.Context, AuditQuery) ([]AuditRecord, error)
	ListAttentionCases(context.Context, AttentionQuery) ([]AttentionCase, error)
	AttentionCase(context.Context, AttentionCaseID) (AttentionCase, error)
	CreateVerification(context.Context, Verification) error
	Verification(context.Context, VerificationID) (Verification, error)
	ResolveAttentionCase(context.Context, AttentionCaseID, AttentionResolutionType, string, Actor, time.Time) error
}

// RecoveryAgent exposes only fixed-root restore, restart, and backup-lifecycle operations.
type RecoveryAgent interface {
	ConfigDigest(context.Context, string) (ProductionState, error)
	VerifyBackup(context.Context, string, BackupID) (BackupEvidence, error)
	DeleteBackup(context.Context, string, BackupDeletionRequest) error
	PrepareRestore(context.Context, string, RestoreExecutionRequest) (RestorePreparationResult, error)
	ExecuteRestore(context.Context, string, RestoreExecutionRequest) (RestoreExecutionResult, error)
	RestoreProgress(context.Context, string, RestoreExecutionRequest) (RestoreExecutionResult, error)
	RecoverRestore(context.Context, string, RestoreExecutionRequest) (RestoreExecutionResult, error)
	ExecuteRestart(context.Context, string, RestartExecutionRequest) (RestartExecutionResult, error)
	RestartProgress(context.Context, string, RestartExecutionRequest) (RestartExecutionResult, error)
	RecoverRestart(context.Context, string, RestartExecutionRequest) (RestartExecutionResult, error)
	VerifyRuntime(context.Context, string, RuntimeVerificationRequest) (RuntimeVerificationResult, error)
}

// ParseRestoreID validates one opaque restore identifier.
func ParseRestoreID(raw string) (RestoreID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse restore id: %w", ErrIdentifierInvalid)
	}
	return RestoreID(raw), nil
}

// NewRestoreID returns a new opaque restore identifier.
func NewRestoreID(random io.Reader) (RestoreID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate restore id: %w", err)
	}
	return RestoreID(raw), nil
}

// ParseRestartID validates one opaque restart identifier.
func ParseRestartID(raw string) (RestartID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse restart id: %w", ErrIdentifierInvalid)
	}
	return RestartID(raw), nil
}

// NewRestartID returns a new opaque restart identifier.
func NewRestartID(random io.Reader) (RestartID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate restart id: %w", err)
	}
	return RestartID(raw), nil
}

// ParseRetentionRunID validates one opaque retention-run identifier.
func ParseRetentionRunID(raw string) (RetentionRunID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse retention run id: %w", ErrIdentifierInvalid)
	}
	return RetentionRunID(raw), nil
}

// NewRetentionRunID returns a new opaque retention-run identifier.
func NewRetentionRunID(random io.Reader) (RetentionRunID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate retention run id: %w", err)
	}
	return RetentionRunID(raw), nil
}

// ParseAttentionCaseID validates one opaque attention-case identifier.
func ParseAttentionCaseID(raw string) (AttentionCaseID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse attention case id: %w", ErrIdentifierInvalid)
	}
	return AttentionCaseID(raw), nil
}

// NewVerificationID returns a new opaque health-verification identifier.
func NewVerificationID(random io.Reader) (VerificationID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate verification id: %w", err)
	}
	return VerificationID(raw), nil
}

// ParseVerificationID validates one opaque current-health verification identifier.
func ParseVerificationID(raw string) (VerificationID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse verification id: %w", ErrIdentifierInvalid)
	}
	return VerificationID(raw), nil
}
