/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import "errors"

var (
	// ErrIdentifierInvalid indicates that an opaque domain identifier is malformed.
	ErrIdentifierInvalid = errors.New("identifier invalid")
	// ErrDigestInvalid indicates that a configuration digest is malformed.
	ErrDigestInvalid = errors.New("digest invalid")
	// ErrETagInvalid indicates that an entity tag is not the required strong form.
	ErrETagInvalid = errors.New("etag invalid")
	// ErrDisplayNameInvalid indicates that a user-facing name violates its bounds.
	ErrDisplayNameInvalid = errors.New("display name invalid")
	// ErrPathInvalid indicates that a path cannot be safely used beneath a scoped root.
	ErrPathInvalid = errors.New("path invalid")
	// ErrLimitExceeded indicates that an operation exceeded a fixed resource bound.
	ErrLimitExceeded = errors.New("limit exceeded")
	// ErrEntryNotManaged indicates that an operation targets an unmanaged entry.
	ErrEntryNotManaged = errors.New("entry not managed")
	// ErrSnapshotChanged indicates that source content changed during snapshot construction.
	ErrSnapshotChanged = errors.New("snapshot changed")
	// ErrProductionChanged indicates that the live production digest no longer matches release evidence.
	ErrProductionChanged = errors.New("production changed")
	// ErrWorkspaceStale indicates that production changed after the workspace snapshot was created.
	ErrWorkspaceStale = errors.New("workspace stale")
	// ErrWorkspaceNeedsAttention indicates that a workspace cannot be changed automatically with confidence.
	ErrWorkspaceNeedsAttention = errors.New("workspace needs attention")
	// ErrConflict indicates that concurrent state or uniqueness prevents a mutation.
	ErrConflict = errors.New("conflict")
	// ErrReleaseInProgress indicates that the single global publication slot is occupied.
	ErrReleaseInProgress = errors.New("release in progress")
	// ErrOperationInProgress indicates that another production-changing task owns the global lease.
	ErrOperationInProgress = errors.New("configuration operation in progress")
	// ErrBackupProtected indicates that a recovery point cannot be removed under current evidence.
	ErrBackupProtected = errors.New("configuration backup protected")
	// ErrBackupTargetInvalid indicates that a requested restore point is absent, damaged, or fails integrity evidence.
	ErrBackupTargetInvalid = errors.New("configuration backup target invalid")
	// ErrRetentionPlanExpired indicates that a persisted deletion plan can no longer be executed.
	ErrRetentionPlanExpired = errors.New("configuration retention plan expired")
	// ErrAttentionUnresolved indicates that an open consistency case still blocks ordinary mutation.
	ErrAttentionUnresolved = errors.New("configuration attention unresolved")
	// ErrCandidateInvalid indicates that the complete isolated candidate was rejected.
	ErrCandidateInvalid = errors.New("candidate invalid")
	// ErrPublishCheckExpired indicates that digest-bound validation evidence is no longer usable.
	ErrPublishCheckExpired = errors.New("publish check expired")
	// ErrNoChanges indicates that a publication candidate is identical to its immutable base.
	ErrNoChanges = errors.New("no configuration changes")
)

// ConflictError returns the current strong workspace ETag without retaining request data.
type ConflictError struct {
	CurrentETag string
}

// Error returns a stable conflict message.
func (e *ConflictError) Error() string {
	return "workspace draft conflict"
}

// Unwrap supports errors.Is with ErrConflict.
func (e *ConflictError) Unwrap() error {
	return ErrConflict
}

// PathError classifies a relative-path validation failure without retaining input.
type PathError struct {
	Reason string
}

// Error describes why path validation failed.
func (e *PathError) Error() string {
	return "validate relative path: " + e.Reason
}

// Unwrap supports errors.Is with ErrPathInvalid.
func (e *PathError) Unwrap() error {
	return ErrPathInvalid
}
