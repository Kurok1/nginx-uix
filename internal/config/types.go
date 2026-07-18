/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	draftETagPrefix = "draft-v1:"
	groupETagPrefix = "groups-v1:"
)

// Digest identifies configuration content by its SHA-256 digest.
type Digest [32]byte

// WorkspaceID is an opaque workspace identifier.
type WorkspaceID string

// GroupID is an opaque configuration group identifier.
type GroupID string

// RelativePath is a validated slash-separated path beneath a scoped root.
type RelativePath string

// WorkspaceState describes the lifecycle state of a configuration workspace.
type WorkspaceState string

// EntryType identifies a filesystem entry kind.
type EntryType string

// EntryClass describes how a filesystem entry participates in management.
type EntryClass string

// DependencyStatus describes the resolution state of a configuration dependency.
type DependencyStatus string

const (
	// StatePreparing indicates that workspace preparation has not completed.
	StatePreparing WorkspaceState = "preparing"
	// StateReady indicates that a workspace can accept operations.
	StateReady WorkspaceState = "ready"
	// StateStale indicates that production changed after workspace creation.
	StateStale WorkspaceState = "stale"
	// StatePublished indicates that the immutable draft was successfully released.
	StatePublished WorkspaceState = "published"
	// StateNeedsAttention indicates that automatic recovery cannot establish safety.
	StateNeedsAttention WorkspaceState = "needs_attention"
)

// RawEntry describes an entry discovered beneath a scoped configuration root.
type RawEntry struct {
	Path           RelativePath
	Type           EntryType
	Mode           fs.FileMode
	Size           int64
	SafeLinkTarget RelativePath
	LinkClass      EntryClass
}

// Actor identifies the authenticated user and request responsible for a change.
type Actor struct {
	UserID    int64
	RequestID string
}

// ProductionState summarizes the currently managed production configuration.
type ProductionState struct {
	Digest          Digest
	ManifestVersion uint16
	EntryCount      int
	ManagedBytes    int64
}

// Workspace is a persisted configuration workspace record.
type Workspace struct {
	ID               WorkspaceID
	Name             string
	State            WorkspaceState
	StateReasonCode  string
	ProductionDigest Digest
	BaseDigest       Digest
	DraftDigest      Digest
	EntryCount       int
	ManagedBytes     int64
	WorkspaceBytes   int64
	Revision         uint64
	LastReleaseID    ReleaseID
	CreatedBy        int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Group is an ordered named collection of configuration paths.
type Group struct {
	ID             GroupID
	Name           string
	NormalizedName string
	SortOrder      int
	Members        []RelativePath
	CreatedBy      int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// GroupCollection is the revisioned complete set of configuration groups.
type GroupCollection struct {
	Revision uint64
	Groups   []Group
}

// OperationRecord captures the durable summary of a configuration mutation.
type OperationRecord struct {
	ID           string
	ObjectType   string
	ObjectID     string
	Action       string
	BeforeDigest *Digest
	AfterDigest  *Digest
	Result       string
	RequestID    string
	OccurredAt   time.Time
}

// AuditEvent captures the user-visible audit details for a mutation.
type AuditEvent struct {
	OperationID string
	OccurredAt  time.Time
	ActorUserID int64
	Action      string
	ObjectType  string
	ObjectID    string
	Result      string
	RequestID   string
	DetailsJSON string
}

// WorkspaceCreation atomically creates a workspace and its audit records.
type WorkspaceCreation struct {
	Workspace Workspace
	Operation OperationRecord
	Audit     AuditEvent
}

// WorkspaceChange atomically updates a workspace and its audit records.
type WorkspaceChange struct {
	ExpectedRevision uint64
	Next             Workspace
	Operation        OperationRecord
	Audit            AuditEvent
}

// WorkspaceDeletion atomically deletes a workspace and records the mutation.
type WorkspaceDeletion struct {
	ID               WorkspaceID
	ExpectedRevision uint64
	Operation        OperationRecord
	Audit            AuditEvent
}

// FileView is one explicitly requested managed text file and its workspace identity.
type FileView struct {
	Entry         Entry
	Content       string
	LineEnding    string
	WorkspaceETag string
}

// TreeView is the complete canonical workspace inventory without file content.
type TreeView struct {
	Entries       []Entry
	Dependencies  []Dependency
	DiffStatuses  map[RelativePath]string
	WorkspaceETag string
}

// CreateFileInput describes one exclusive managed-text creation.
type CreateFileInput struct {
	Path    RelativePath
	Content []byte
	IfMatch string
}

// ReplaceFileInput describes one atomic managed-text replacement.
type ReplaceFileInput struct {
	Path    RelativePath
	Content []byte
	IfMatch string
}

// CopyFileInput describes one managed-text copy without include rewriting.
type CopyFileInput struct {
	SourcePath      RelativePath
	DestinationPath RelativePath
	IfMatch         string
}

// RenameFileInput describes one copy-then-delete managed-text rename.
type RenameFileInput struct {
	SourcePath      RelativePath
	DestinationPath RelativePath
	IfMatch         string
}

// DeleteFileInput describes one confirmed managed-text deletion.
type DeleteFileInput struct {
	Path        RelativePath
	ConfirmPath string
	IfMatch     string
}

// MutationResult returns the persisted workspace and optional resulting entry.
type MutationResult struct {
	Workspace Workspace
	Entry     *Entry
}

// GroupChange atomically replaces the group collection and records the mutation.
type GroupChange struct {
	ExpectedRevision uint64
	Groups           []Group
	Operation        OperationRecord
	Audit            AuditEvent
}

// WorkspaceReader provides persisted workspace queries to the configuration service.
type WorkspaceReader interface {
	WorkspaceUsage(context.Context) (count int, bytes int64, err error)
	ListWorkspaces(context.Context) ([]Workspace, error)
	Workspace(context.Context, WorkspaceID) (Workspace, error)
	OperationAudit(context.Context, string) (OperationRecord, AuditEvent, bool, error)
}

// WorkspaceWriter persists atomic workspace mutations.
type WorkspaceWriter interface {
	CreateWorkspace(context.Context, WorkspaceCreation) error
	UpdateWorkspace(context.Context, WorkspaceChange) error
	DeleteWorkspace(context.Context, WorkspaceDeletion) error
}

// GroupRepository provides revisioned group collection storage.
type GroupRepository interface {
	GroupCollection(context.Context) (GroupCollection, error)
	ChangeGroupCollection(context.Context, GroupChange) (GroupCollection, error)
}

// ProductionReader provides immutable snapshots and current production digests.
type ProductionReader interface {
	ConfigSnapshot(context.Context, string, WorkspaceID) (Snapshot, error)
	ConfigDigest(context.Context, string) (ProductionState, error)
}

// ParseWorkspaceID validates an opaque lowercase hexadecimal workspace identifier.
func ParseWorkspaceID(raw string) (WorkspaceID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse workspace id: %w", ErrIdentifierInvalid)
	}
	return WorkspaceID(raw), nil
}

// NewWorkspaceID reads exactly 16 random bytes and returns their lowercase hexadecimal form.
func NewWorkspaceID(random io.Reader) (WorkspaceID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate workspace id: %w", err)
	}
	return WorkspaceID(raw), nil
}

// ParseGroupID validates an opaque lowercase hexadecimal group identifier.
func ParseGroupID(raw string) (GroupID, error) {
	if !validOpaqueID(raw) {
		return "", fmt.Errorf("parse group id: %w", ErrIdentifierInvalid)
	}
	return GroupID(raw), nil
}

// NewGroupID reads exactly 16 random bytes and returns their lowercase hexadecimal form.
func NewGroupID(random io.Reader) (GroupID, error) {
	raw, err := newOpaqueID(random)
	if err != nil {
		return "", fmt.Errorf("generate group id: %w", err)
	}
	return GroupID(raw), nil
}

// ParseDigest validates a lowercase hexadecimal SHA-256 digest.
func ParseDigest(raw string) (Digest, error) {
	var digest Digest
	if len(raw) != hex.EncodedLen(len(digest)) {
		return Digest{}, fmt.Errorf("parse digest: %w", ErrDigestInvalid)
	}
	if _, err := hex.Decode(digest[:], []byte(raw)); err != nil || hex.EncodeToString(digest[:]) != raw {
		return Digest{}, fmt.Errorf("parse digest: %w", ErrDigestInvalid)
	}
	return digest, nil
}

// String returns the digest in canonical lowercase hexadecimal form.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// DraftETag returns the strong entity tag for a workspace draft digest.
func DraftETag(d Digest) string {
	return strongETag(draftETagPrefix, d)
}

// GroupETag returns the strong entity tag for a group collection digest.
func GroupETag(d Digest) string {
	return strongETag(groupETagPrefix, d)
}

// ParseStrongETag validates one exact strong entity tag with the required prefix.
func ParseStrongETag(raw, prefix string) (Digest, error) {
	expectedLength := 2 + len(prefix) + hex.EncodedLen(len(Digest{}))
	if len(raw) != expectedLength || raw[0] != '"' || raw[len(raw)-1] != '"' || !strings.HasPrefix(raw[1:], prefix) {
		return Digest{}, fmt.Errorf("parse strong etag: %w", ErrETagInvalid)
	}
	digest, err := ParseDigest(raw[1+len(prefix) : len(raw)-1])
	if err != nil {
		return Digest{}, fmt.Errorf("parse strong etag: %w", ErrETagInvalid)
	}
	return digest, nil
}

// ValidateDisplayName trims and validates a bounded user-facing display name.
func ValidateDisplayName(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("validate display name: %w", ErrDisplayNameInvalid)
	}
	display := strings.TrimSpace(raw)
	runeCount := utf8.RuneCountInString(display)
	if runeCount == 0 || runeCount > 128 {
		return "", fmt.Errorf("validate display name: %w", ErrDisplayNameInvalid)
	}
	for _, value := range display {
		if unicode.IsControl(value) {
			return "", fmt.Errorf("validate display name: %w", ErrDisplayNameInvalid)
		}
	}
	return display, nil
}

// NormalizeGroupName returns a validated display name and its uniqueness key.
func NormalizeGroupName(raw string) (display string, normalized string, err error) {
	display, err = ValidateDisplayName(raw)
	if err != nil {
		return "", "", err
	}
	return display, strings.ToLower(display), nil
}

func newOpaqueID(random io.Reader) (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func validOpaqueID(raw string) bool {
	if len(raw) != 32 {
		return false
	}
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, []byte(raw)); err != nil {
		return false
	}
	return hex.EncodeToString(decoded) == raw
}

func strongETag(prefix string, digest Digest) string {
	return `"` + prefix + digest.String() + `"`
}
