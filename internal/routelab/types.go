/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

// Package routelab provides static route analysis and isolated route-test orchestration.
package routelab

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

var (
	// ErrInvalidRequest indicates that route request input is outside the bounded protocol.
	ErrInvalidRequest = errors.New("route request invalid")
	// ErrConfirmationRequired indicates that a potentially side-effecting request was not confirmed.
	ErrConfirmationRequired = errors.New("route request confirmation required")
	// ErrProjectIncomplete indicates that a complete instrumentable HTTP project cannot be proven.
	ErrProjectIncomplete = errors.New("route project incomplete")
	// ErrListenerAmbiguous indicates that port-only input cannot choose a production address group safely.
	ErrListenerAmbiguous = errors.New("route listener ambiguous")
	// ErrLimitExceeded indicates that a Route Lab work or response bound was reached.
	ErrLimitExceeded = errors.New("route lab limit exceeded")
	// ErrInvalidInstrumentation indicates that a trusted sandbox rewrite plan is incomplete.
	ErrInvalidInstrumentation = errors.New("route instrumentation invalid")
	// ErrCandidateInvalid indicates that the instrumented complete configuration failed nginx -t.
	ErrCandidateInvalid = errors.New("route candidate invalid")
	// ErrSandboxStart indicates that the isolated Nginx master did not become ready.
	ErrSandboxStart = errors.New("route sandbox start failed")
	// ErrRequestTimeout indicates that the bounded loopback request exceeded its deadline.
	ErrRequestTimeout = errors.New("route request timeout")
	// ErrEvidenceIncomplete indicates that runtime final-route evidence was absent or contradictory.
	ErrEvidenceIncomplete = errors.New("route evidence incomplete")
	// ErrCleanupFailed indicates that owned sandbox process or filesystem cleanup was not proven.
	ErrCleanupFailed = errors.New("route cleanup failed")
)

const (
	// SideEffectConfirmation is the exact explicit confirmation for a body or non-idempotent method.
	SideEffectConfirmation = "RUN SIDE-EFFECTING REQUEST"
)

// Scheme identifies the supported HTTP transport semantics.
type Scheme string

const (
	// SchemeHTTP runs a cleartext HTTP request against a loopback sandbox listener.
	SchemeHTTP Scheme = "http"
	// SchemeHTTPS runs a TLS HTTP request while preserving explicit SNI semantics.
	SchemeHTTPS Scheme = "https"
)

// StaticRequest contains only fields that affect static server and location selection.
type StaticRequest struct {
	Scheme Scheme `json:"scheme"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	SNI    string `json:"sni"`
	URI    string `json:"uri"`
}

// Header is one ordered outbound request header.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Assertions are evaluated against a successfully collected HTTP response.
type Assertions struct {
	StatusCode    int    `json:"status_code"`
	ContainsText  string `json:"contains_text"`
	ForbiddenText string `json:"forbidden_text"`
}

// Request is one bounded request to be executed only against a fixed sandbox loopback address.
type Request struct {
	StaticRequest
	Method       string
	Query        string
	Headers      []Header
	Body         []byte
	Timeout      time.Duration
	Assertions   Assertions
	Confirmation string
}

// ValidatedRequest retains normalized execution input and safe history metadata.
type ValidatedRequest struct {
	Request
	SideEffecting        bool
	UpstreamSideEffect   bool
	Replayable           bool
	SensitiveHeaderNames []string
}

// SourceLocation is a safe configuration-root-relative source range.
type SourceLocation struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

// CandidateDisposition separates a final prediction from considered or excluded facts.
type CandidateDisposition string

const (
	// DispositionSelected is the static final prediction at its selection layer.
	DispositionSelected CandidateDisposition = "selected"
	// DispositionMatched is a matching intermediate candidate that did not become the final route.
	DispositionMatched CandidateDisposition = "matched"
	// DispositionExcluded is a candidate with a proven exclusion reason.
	DispositionExcluded CandidateDisposition = "excluded"
	// DispositionIndeterminate is a candidate whose result cannot be proven statically.
	DispositionIndeterminate CandidateDisposition = "indeterminate"
)

// CandidateReason is a stable explanation for candidate disposition.
type CandidateReason string

const (
	ReasonListenerMismatch           CandidateReason = "listener_mismatch"
	ReasonListenerUnsupported        CandidateReason = "listener_unsupported"
	ReasonListenerDefault            CandidateReason = "listener_default"
	ReasonServerNameExact            CandidateReason = "server_name_exact"
	ReasonServerNameLeadingWildcard  CandidateReason = "server_name_leading_wildcard"
	ReasonServerNameTrailingWildcard CandidateReason = "server_name_trailing_wildcard"
	ReasonServerNameRegex            CandidateReason = "server_name_regex"
	ReasonServerNameLowerPriority    CandidateReason = "server_name_lower_priority"
	ReasonServerNameIndeterminate    CandidateReason = "server_name_indeterminate"
	ReasonLocationExact              CandidateReason = "location_exact"
	ReasonLocationLongestPrefix      CandidateReason = "location_longest_prefix"
	ReasonLocationPrefixPriority     CandidateReason = "location_prefix_priority"
	ReasonLocationRegex              CandidateReason = "location_regex"
	ReasonLocationShorterPrefix      CandidateReason = "location_shorter_prefix"
	ReasonLocationPrefixNoMatch      CandidateReason = "location_prefix_no_match"
	ReasonLocationRegexNoMatch       CandidateReason = "location_regex_no_match"
	ReasonLocationEarlierRegex       CandidateReason = "location_earlier_regex_selected"
	ReasonLocationNamedInitial       CandidateReason = "location_named_not_initial"
	ReasonLocationParentMatched      CandidateReason = "location_parent_matched"
	ReasonLocationParentNotSelected  CandidateReason = "location_parent_not_selected"
	ReasonLocationRegexIndeterminate CandidateReason = "location_regex_indeterminate"
	ReasonLocationURIIndeterminate   CandidateReason = "location_uri_normalization_indeterminate"
)

// ListenerFact is one parsed direct listen directive or derived default.
type ListenerFact struct {
	Address       string `json:"address"`
	Port          int    `json:"port"`
	SSL           bool   `json:"ssl"`
	DefaultServer bool   `json:"default_server"`
	Derived       bool   `json:"derived"`
	Supported     bool   `json:"supported"`
}

// ServerCandidate is one HTTP virtual server considered for a static request.
type ServerCandidate struct {
	RouteID     string               `json:"route_id"`
	Source      SourceLocation       `json:"source"`
	Listeners   []ListenerFact       `json:"listeners"`
	ServerNames []string             `json:"server_names"`
	Disposition CandidateDisposition `json:"disposition"`
	Reason      CandidateReason      `json:"reason"`
}

// MatcherType identifies a supported static location header shape.
type MatcherType string

const (
	MatcherUnknown          MatcherType = "unknown"
	MatcherExact            MatcherType = "exact"
	MatcherPrefix           MatcherType = "prefix"
	MatcherPrefixPriority   MatcherType = "prefix_priority"
	MatcherRegex            MatcherType = "regex"
	MatcherRegexInsensitive MatcherType = "regex_insensitive"
	MatcherNamed            MatcherType = "named"
)

// LocationCandidate is one location considered beneath the selected server.
type LocationCandidate struct {
	RouteID       string               `json:"route_id"`
	ParentRouteID string               `json:"parent_route_id"`
	Source        SourceLocation       `json:"source"`
	Type          MatcherType          `json:"matcher_type"`
	Matcher       string               `json:"matcher"`
	Depth         int                  `json:"depth"`
	Disposition   CandidateDisposition `json:"disposition"`
	Reason        CandidateReason      `json:"reason"`
}

// Analysis is a bounded static explanation; runtime evidence remains authoritative.
type Analysis struct {
	Complete                  bool                `json:"complete"`
	NormalizedURI             string              `json:"normalized_uri"`
	PredictedTLSServerRouteID string              `json:"predicted_tls_server_route_id,omitempty"`
	PredictedServerRouteID    string              `json:"predicted_server_route_id,omitempty"`
	PredictedLocationRouteID  string              `json:"predicted_location_route_id,omitempty"`
	RuntimeRedirectPossible   bool                `json:"runtime_redirect_possible"`
	Servers                   []ServerCandidate   `json:"servers"`
	Locations                 []LocationCandidate `json:"locations"`
}

// RouteKind distinguishes server and location instrumentation identities.
type RouteKind string

const (
	RouteServer   RouteKind = "server"
	RouteLocation RouteKind = "location"
)

// RouteDefinition maps one stable public identity back to safe source evidence.
type RouteDefinition struct {
	RouteID       string         `json:"route_id"`
	NodeID        string         `json:"node_id"`
	ParentRouteID string         `json:"parent_route_id"`
	Kind          RouteKind      `json:"kind"`
	MatcherType   MatcherType    `json:"matcher_type"`
	Matcher       string         `json:"matcher"`
	Source        SourceLocation `json:"source"`
}

// AgentRequest is the complete typed authorization for one fixed-root sandbox execution.
type AgentRequest struct {
	RunID            string
	WorkspaceID      config.WorkspaceID
	ProductionDigest config.Digest
	DraftDigest      config.Digest
	Request          ValidatedRequest
	RequestID        string
}

// Response captures bounded HTTP evidence without authentication or cookie headers.
type Response struct {
	StatusCode     int              `json:"status_code"`
	Headers        []Header         `json:"headers"`
	BodySnippet    string           `json:"body_snippet"`
	BodyBytes      int64            `json:"body_bytes"`
	BodyDigest     string           `json:"body_digest"`
	BodyTruncated  bool             `json:"body_truncated"`
	SnippetOmitted bool             `json:"snippet_omitted"`
	Duration       time.Duration    `json:"duration"`
	Assertions     AssertionOutcome `json:"assertions"`
}

// RuntimeEvidence is the final server/location fact emitted by the finishing Nginx context.
type RuntimeEvidence struct {
	ServerRouteID  string
	RouteID        string
	FinalURI       string
	Upstream       string
	UpstreamStatus string
	StatusCode     int
	RequestTime    time.Duration
}

// CleanupEvidence proves the owned master, listener and stage were removed.
type CleanupEvidence struct {
	MasterReaped bool `json:"master_reaped"`
	PortClosed   bool `json:"port_closed"`
	StageRemoved bool `json:"stage_removed"`
}

// AgentDiagnostic is one bounded configuration-root-relative execution diagnostic.
type AgentDiagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Summary string `json:"summary"`
}

// AgentResult is the content-bounded terminal output of one fixed Route Lab execution.
type AgentResult struct {
	CandidateDigest config.Digest
	Routes          []RouteDefinition
	Response        Response
	Evidence        RuntimeEvidence
	Cleanup         CleanupEvidence
	Diagnostics     []AgentDiagnostic
}

// MarshalJSON emits the persisted/API projection with explicit millisecond units and non-null lists.
func (response Response) MarshalJSON() ([]byte, error) {
	headers := response.Headers
	if headers == nil {
		headers = []Header{}
	}
	assertions := response.Assertions
	if assertions.Results == nil {
		assertions.Results = []AssertionResult{}
	}
	return json.Marshal(struct {
		StatusCode     int              `json:"status_code"`
		Headers        []Header         `json:"headers"`
		BodySnippet    string           `json:"body_snippet"`
		BodyBytes      int64            `json:"body_bytes"`
		BodyDigest     string           `json:"body_digest"`
		BodyTruncated  bool             `json:"body_truncated"`
		SnippetOmitted bool             `json:"snippet_omitted"`
		DurationMS     int64            `json:"duration_ms"`
		Assertions     AssertionOutcome `json:"assertions"`
	}{
		StatusCode: response.StatusCode, Headers: headers, BodySnippet: response.BodySnippet,
		BodyBytes: response.BodyBytes, BodyDigest: response.BodyDigest, BodyTruncated: response.BodyTruncated,
		SnippetOmitted: response.SnippetOmitted, DurationMS: response.Duration.Milliseconds(), Assertions: assertions,
	})
}

// MarshalJSON emits runtime evidence with an explicit millisecond duration.
func (evidence RuntimeEvidence) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ServerRouteID  string `json:"server_route_id"`
		RouteID        string `json:"route_id"`
		FinalURI       string `json:"final_uri"`
		Upstream       string `json:"upstream"`
		UpstreamStatus string `json:"upstream_status"`
		StatusCode     int    `json:"status_code"`
		RequestTimeMS  int64  `json:"request_time_ms"`
	}{
		ServerRouteID: evidence.ServerRouteID, RouteID: evidence.RouteID, FinalURI: evidence.FinalURI,
		Upstream: evidence.Upstream, UpstreamStatus: evidence.UpstreamStatus, StatusCode: evidence.StatusCode,
		RequestTimeMS: evidence.RequestTime.Milliseconds(),
	})
}

// MarshalJSON emits a bounded public result and represents its digest as lowercase hexadecimal text.
func (result AgentResult) MarshalJSON() ([]byte, error) {
	routes := result.Routes
	if routes == nil {
		routes = []RouteDefinition{}
	}
	diagnostics := result.Diagnostics
	if diagnostics == nil {
		diagnostics = []AgentDiagnostic{}
	}
	return json.Marshal(struct {
		CandidateDigest string            `json:"candidate_digest"`
		Routes          []RouteDefinition `json:"routes"`
		Response        Response          `json:"response"`
		Evidence        RuntimeEvidence   `json:"evidence"`
		Cleanup         CleanupEvidence   `json:"cleanup"`
		Diagnostics     []AgentDiagnostic `json:"diagnostics"`
	}{
		CandidateDigest: result.CandidateDigest.String(), Routes: routes, Response: result.Response,
		Evidence: result.Evidence, Cleanup: result.Cleanup, Diagnostics: diagnostics,
	})
}
