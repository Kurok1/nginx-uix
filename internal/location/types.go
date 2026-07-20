/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

// Package location projects HTTP servers and locations from the lossless Nginx syntax tree.
package location

import "github.com/kuroky/nginx-uix/internal/upstream"

// SourceLocation is a safe relative source range.
type SourceLocation struct {
	Path        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// MatcherType identifies one supported Nginx location header form.
type MatcherType string

const (
	// MatcherUnknown identifies a header that remains available only through raw editing.
	MatcherUnknown MatcherType = "unknown"
	// MatcherExact is an "=" literal matcher.
	MatcherExact MatcherType = "exact"
	// MatcherPrefix is an ordinary literal prefix matcher.
	MatcherPrefix MatcherType = "prefix"
	// MatcherPrefixPriority is a "^~" literal prefix matcher.
	MatcherPrefixPriority MatcherType = "prefix_priority"
	// MatcherRegex is a case-sensitive regular expression matcher.
	MatcherRegex MatcherType = "regex"
	// MatcherRegexInsensitive is a case-insensitive regular expression matcher.
	MatcherRegexInsensitive MatcherType = "regex_insensitive"
	// MatcherNamed is an internal named location.
	MatcherNamed MatcherType = "named"
)

// Location is one location block and its direct structured children.
type Location struct {
	ID                      string
	Type                    MatcherType
	Matcher                 string
	MatcherRaw              string
	Source                  SourceLocation
	Children                []Location
	ProxyPasses             []upstream.Reference
	UnknownDirectiveCount   int
	Editable                bool
	ReadOnlyReason          string
	ProxyPassEditable       bool
	ProxyPassReadOnlyReason string
	Instances               int
}

// Server is one HTTP server block with bounded display summaries.
type Server struct {
	ID               string
	Source           SourceLocation
	Listens          []string
	ServerNames      []string
	SummaryTruncated bool
	Locations        []Location
	Editable         bool
	ReadOnlyReason   string
	Instances        int
}

// DiagnosticCode is a stable server/location analysis result.
type DiagnosticCode string

const (
	// DiagnosticContextReadOnly identifies ambiguous or multiply loaded syntax.
	DiagnosticContextReadOnly DiagnosticCode = "location_context_read_only"
	// DiagnosticInvalidHeader identifies an unsupported location argument shape.
	DiagnosticInvalidHeader DiagnosticCode = "location_invalid_header"
	// DiagnosticInvalidMatcher identifies an invalid literal, named, or regular expression matcher.
	DiagnosticInvalidMatcher DiagnosticCode = "location_invalid_matcher"
	// DiagnosticNestedUnderExact identifies children under an exact matcher.
	DiagnosticNestedUnderExact DiagnosticCode = "location_nested_under_exact"
	// DiagnosticNestedUnderNamed identifies children under a named matcher.
	DiagnosticNestedUnderNamed DiagnosticCode = "location_nested_under_named"
	// DiagnosticNestedNamed identifies a named matcher below another location.
	DiagnosticNestedNamed DiagnosticCode = "location_nested_named"
	// DiagnosticLiteralOutsideParent identifies a literal child outside a known parent prefix.
	DiagnosticLiteralOutsideParent DiagnosticCode = "location_literal_outside_parent"
	// DiagnosticParentUnprovable identifies a literal child below a non-literal parent.
	DiagnosticParentUnprovable DiagnosticCode = "location_parent_unprovable"
	// DiagnosticDuplicate identifies two identical sibling matcher rules.
	DiagnosticDuplicate DiagnosticCode = "location_duplicate"
	// DiagnosticRegexOrderSensitive identifies sibling regex matchers whose order is meaningful.
	DiagnosticRegexOrderSensitive DiagnosticCode = "location_regex_order_sensitive"
	// DiagnosticMultipleProxyPass identifies repeated direct proxy_pass directives.
	DiagnosticMultipleProxyPass DiagnosticCode = "location_multiple_proxy_pass"
)

// DiagnosticSeverity controls whether a diagnostic blocks a related structured edit.
type DiagnosticSeverity string

const (
	// SeverityBlocking prevents edits that touch the related syntax.
	SeverityBlocking DiagnosticSeverity = "blocking"
	// SeverityWarning communicates a known risk without rewriting user syntax.
	SeverityWarning DiagnosticSeverity = "warning"
)

// Diagnostic contains a safe source fact and optional related object.
type Diagnostic struct {
	Code      DiagnosticCode
	Severity  DiagnosticSeverity
	Source    SourceLocation
	RelatedID string
	ParentID  string
}

// Catalog is the complete HTTP server/location projection for one draft ETag.
type Catalog struct {
	Servers     []Server
	Diagnostics []Diagnostic
	Complete    bool
}
