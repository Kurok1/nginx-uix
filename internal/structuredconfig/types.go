/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

// Package structuredconfig coordinates lossless domain plans with verified configuration workspaces.
package structuredconfig

import (
	"errors"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/location"
	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

var (
	// ErrInvalidOperation indicates a missing, mismatched, or multiply populated operation payload.
	ErrInvalidOperation = errors.New("structured operation invalid")
	// ErrPreviewStale indicates that preview identity, operation, or draft ETag no longer matches.
	ErrPreviewStale = errors.New("structured preview stale")
	// ErrPreviewIncomplete indicates that bounded review data cannot safely be applied.
	ErrPreviewIncomplete = errors.New("structured preview incomplete")
	// ErrPostcondition indicates that rendered sources did not rebuild into the requested structure.
	ErrPostcondition = errors.New("structured edit postcondition failed")
	// ErrParseFailed indicates that the bounded project is incomplete or contains invalid syntax.
	ErrParseFailed = errors.New("structured project parse failed")
	// ErrLimitExceeded indicates that parser, project, or preview bounds prevent a safe operation.
	ErrLimitExceeded = errors.New("structured project limit exceeded")
)

// OperationKind is a stable structured mutation discriminator.
type OperationKind string

const (
	// OperationUpstreamCreate creates one upstream block.
	OperationUpstreamCreate OperationKind = "upstream.create"
	// OperationUpstreamRename renames one upstream and direct references.
	OperationUpstreamRename OperationKind = "upstream.rename"
	// OperationUpstreamDelete deletes one unreferenced upstream.
	OperationUpstreamDelete OperationKind = "upstream.delete"
	// OperationUpstreamServerCreate appends one upstream server.
	OperationUpstreamServerCreate OperationKind = "upstream_server.create"
	// OperationUpstreamServerUpdate updates one upstream server.
	OperationUpstreamServerUpdate OperationKind = "upstream_server.update"
	// OperationUpstreamServerDelete deletes one upstream server.
	OperationUpstreamServerDelete OperationKind = "upstream_server.delete"
	// OperationLocationCreate creates one location.
	OperationLocationCreate OperationKind = "location.create"
	// OperationLocationUpdate updates one location and direct proxy_pass intent.
	OperationLocationUpdate OperationKind = "location.update"
	// OperationLocationDelete deletes one location.
	OperationLocationDelete OperationKind = "location.delete"
)

// Operation is a strict tagged union; exactly the field named by Kind must be populated.
type Operation struct {
	Kind OperationKind

	UpstreamCreate       *upstream.CreateInput
	UpstreamRename       *upstream.RenameInput
	UpstreamDelete       *upstream.DeleteInput
	UpstreamServerCreate *upstream.CreateServerInput
	UpstreamServerUpdate *upstream.UpdateServerInput
	UpstreamServerDelete *upstream.DeleteServerInput
	LocationCreate       *location.CreateInput
	LocationUpdate       *location.UpdateInput
	LocationDelete       *location.DeleteInput
}

// SourceLocation is a safe configuration-root-relative source range.
type SourceLocation struct {
	Path        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// HTTPBlock is one top-level HTTP context that can own a new upstream.
type HTTPBlock struct {
	ID             string
	Source         SourceLocation
	Editable       bool
	ReadOnlyReason string
	Instances      int
}

// Projection is one immutable, ETag-bound structured catalog.
type Projection struct {
	WorkspaceID        config.WorkspaceID
	DraftETag          string
	Complete           bool
	ProjectDiagnostics []nginxast.ProjectDiagnostic
	HTTPBlocks         []HTTPBlock
	Upstreams          upstream.Catalog
	Locations          location.Catalog
}

// ChangedFile contains bounded review evidence for one generated replacement.
type ChangedFile struct {
	Path         config.RelativePath
	BeforeDigest string
	AfterDigest  string
	AddedLines   int
	RemovedLines int
	Patch        string
}

// Preview is a pure, deterministic structured change review.
type Preview struct {
	PreviewID     string
	WorkspaceID   config.WorkspaceID
	DraftETag     string
	OperationKind OperationKind
	TargetID      string
	ChangedFiles  []ChangedFile
	Complete      bool

	replacements []config.FileReplacement
}
