/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

// Package upstream projects and safely edits HTTP upstream configuration.
package upstream

// SourceLocation is a safe relative source range.
type SourceLocation struct {
	Path        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// PreservedSyntax identifies syntax retained outside the structured field set.
type PreservedSyntax struct {
	ID   string
	Name string
}

// PreservedParameter retains one unrecognized upstream server parameter.
type PreservedParameter struct {
	Name string
	Raw  string
}

// Endpoint is one parsed upstream server address.
type Endpoint struct {
	Address string
	Port    *uint16
	Unix    bool
}

// Server is one upstream server directive.
type Server struct {
	ID                  string
	Source              SourceLocation
	Endpoint            Endpoint
	Weight              *int
	Backup              bool
	Down                bool
	MaxFails            *int
	FailTimeout         *string
	PreservedParameters []PreservedParameter
	Editable            bool
	ReadOnlyReason      string
}

// ReferenceState describes a direct or non-static proxy_pass target.
type ReferenceState string

const (
	// ReferenceResolved points to exactly one declared upstream.
	ReferenceResolved ReferenceState = "resolved"
	// ReferenceDangling looks like an upstream identifier without a declaration.
	ReferenceDangling ReferenceState = "dangling"
	// ReferenceExternal is a static network target outside declared upstreams.
	ReferenceExternal ReferenceState = "external"
	// ReferenceDynamic contains a variable and cannot be resolved statically.
	ReferenceDynamic ReferenceState = "dynamic"
	// ReferenceAmbiguous matches duplicate upstream names.
	ReferenceAmbiguous ReferenceState = "ambiguous"
	// ReferenceUnknown cannot be split into a supported proxy URL.
	ReferenceUnknown ReferenceState = "unknown"
)

// Reference is one proxy_pass analysis result.
type Reference struct {
	ID           string
	Source       SourceLocation
	State        ReferenceState
	Scheme       string
	Host         string
	Port         *uint16
	URI          string
	UpstreamID   string
	UpstreamName string
}

// Upstream is one HTTP upstream block and its structured projection.
type Upstream struct {
	ID                  string
	Name                string
	Source              SourceLocation
	Servers             []Server
	PreservedDirectives []PreservedSyntax
	References          []Reference
	Editable            bool
	ReadOnlyReason      string
	Instances           int
}

// DiagnosticCode is a stable upstream analysis result.
type DiagnosticCode string

const (
	// DiagnosticDuplicateName identifies non-unique upstream names.
	DiagnosticDuplicateName DiagnosticCode = "upstream_duplicate_name"
	// DiagnosticServerRawOnly identifies a server directive that cannot be safely structured.
	DiagnosticServerRawOnly DiagnosticCode = "upstream_server_raw_only"
	// DiagnosticReferenceDangling identifies a likely missing upstream declaration.
	DiagnosticReferenceDangling DiagnosticCode = "upstream_reference_dangling"
	// DiagnosticReferenceDynamic identifies a variable proxy_pass target.
	DiagnosticReferenceDynamic DiagnosticCode = "upstream_reference_dynamic"
	// DiagnosticReferenceUnknown identifies unsupported proxy_pass syntax.
	DiagnosticReferenceUnknown DiagnosticCode = "upstream_reference_unknown"
	// DiagnosticReferenceAmbiguous identifies a reference to duplicate upstream names.
	DiagnosticReferenceAmbiguous DiagnosticCode = "upstream_reference_ambiguous"
	// DiagnosticContextReadOnly identifies an ambiguous or multiply loaded block.
	DiagnosticContextReadOnly DiagnosticCode = "upstream_context_read_only"
)

// Diagnostic contains a safe source fact and optional related object.
type Diagnostic struct {
	Code      DiagnosticCode
	Source    SourceLocation
	RelatedID string
}

// Catalog is the complete upstream and proxy_pass projection for one draft ETag.
type Catalog struct {
	Upstreams                 []Upstream
	References                []Reference
	Diagnostics               []Diagnostic
	ReferenceAnalysisComplete bool
}
