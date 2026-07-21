/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package location

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

const (
	maxServerSummaryItems = 20
	maxServerSummaryRunes = 256
)

// BuildCatalog derives HTTP servers and their location trees without becoming a configuration source.
func BuildCatalog(project *nginxast.Project, upstreams upstream.Catalog) Catalog {
	catalog := Catalog{
		Complete: project != nil && project.Complete && upstreams.ReferenceAnalysisComplete,
	}
	if project == nil {
		return catalog
	}

	references := make(map[string]upstream.Reference, len(upstreams.References))
	for _, reference := range upstreams.References {
		references[reference.ID] = reference
	}

	for _, reference := range project.Nodes {
		block, ok := reference.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "server" || !hasPlacementContext(reference, nginxast.ContextHTTP) {
			continue
		}
		catalog.Servers = append(catalog.Servers, buildServer(project, reference, block, references, &catalog))
	}
	return catalog
}

func buildServer(
	project *nginxast.Project,
	reference *nginxast.NodeRef,
	block *nginxast.Block,
	references map[string]upstream.Reference,
	catalog *Catalog,
) Server {
	server := Server{
		ID: reference.ID, Source: sourceLocation(reference.Path, block.Span),
		Editable:  !reference.Ambiguous && reference.Instances == 1 && len(block.Arguments) == 0,
		Instances: reference.Instances,
	}
	if !server.Editable {
		if len(block.Arguments) != 0 {
			server.ReadOnlyReason = "invalid_header"
		} else {
			server.ReadOnlyReason = "ambiguous_context"
		}
		catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
			Code: DiagnosticContextReadOnly, Severity: SeverityBlocking,
			Source: server.Source, RelatedID: server.ID,
		})
	}

	for _, child := range directChildren(project, reference.ID, nginxast.ContextServer) {
		directive, ok := child.Node.(*nginxast.Directive)
		if !ok {
			continue
		}
		switch directive.Name.Value {
		case "listen":
			appendSummary(&server.Listens, directive.Arguments, &server.SummaryTruncated)
		case "server_name":
			for _, argument := range directive.Arguments {
				appendSummaryValue(&server.ServerNames, argument.Value, &server.SummaryTruncated)
			}
		}
	}
	server.Locations = buildChildren(project, reference.ID, nginxast.ContextServer, references, catalog)
	validateSiblings(server.Locations, reference.ID, catalog)
	return server
}

func buildChildren(
	project *nginxast.Project,
	parentID string,
	context nginxast.ContextKind,
	references map[string]upstream.Reference,
	catalog *Catalog,
) []Location {
	children := make([]Location, 0)
	for _, reference := range directChildren(project, parentID, context) {
		block, ok := reference.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "location" {
			continue
		}
		child := buildLocation(project, reference, block, references, catalog)
		children = append(children, child)
	}
	return children
}

func buildLocation(
	project *nginxast.Project,
	reference *nginxast.NodeRef,
	block *nginxast.Block,
	references map[string]upstream.Reference,
	catalog *Catalog,
) Location {
	location := Location{
		ID: reference.ID, Type: MatcherUnknown, Source: sourceLocation(reference.Path, block.Span),
		Editable:  !reference.Ambiguous && reference.Instances == 1,
		Instances: reference.Instances,
	}
	if !location.Editable {
		location.ReadOnlyReason = "ambiguous_context"
		addDiagnostic(catalog, DiagnosticContextReadOnly, SeverityBlocking, location.Source, location.ID, "")
	}
	if location.Editable && spanContainsComment(project, reference.Path, block.HeaderSpan) {
		location.Editable = false
		location.ReadOnlyReason = "inline_comment"
		addDiagnostic(catalog, DiagnosticInvalidHeader, SeverityBlocking, location.Source, location.ID, "")
	}

	matcherType, matcher, matcherRaw, headerOK, matcherOK := parseMatcher(block.Arguments)
	location.Type = matcherType
	location.Matcher = matcher
	location.MatcherRaw = matcherRaw
	switch {
	case !headerOK:
		location.Editable = false
		location.ReadOnlyReason = "invalid_header"
		addDiagnostic(catalog, DiagnosticInvalidHeader, SeverityBlocking, location.Source, location.ID, "")
	case !matcherOK:
		location.Editable = false
		location.ReadOnlyReason = "invalid_matcher"
		addDiagnostic(catalog, DiagnosticInvalidMatcher, SeverityBlocking, location.Source, location.ID, "")
	}

	proxyPassHasInlineComment := false
	for _, child := range directChildren(project, reference.ID, nginxast.ContextLocation) {
		switch node := child.Node.(type) {
		case *nginxast.Directive:
			if node.Name.Value != "proxy_pass" {
				location.UnknownDirectiveCount++
				continue
			}
			proxyPassHasInlineComment = proxyPassHasInlineComment ||
				spanContainsComment(project, child.Path, node.Span)
			proxyReference, exists := references[child.ID]
			if !exists {
				proxyReference = upstream.Reference{
					ID: child.ID, Source: upstreamSourceLocation(child.Path, node.Span),
					State: upstream.ReferenceUnknown,
				}
			}
			location.ProxyPasses = append(location.ProxyPasses, proxyReference)
		case *nginxast.Block:
			if node.Name.Value != "location" {
				location.UnknownDirectiveCount++
			}
		}
	}

	location.Children = buildChildren(project, reference.ID, nginxast.ContextLocation, references, catalog)
	validateSiblings(location.Children, location.ID, catalog)
	validateParent(&location, catalog)
	validateDirectProxyPass(&location, catalog)
	if proxyPassHasInlineComment {
		location.ProxyPassEditable = false
		location.ProxyPassReadOnlyReason = "inline_comment"
	}
	return location
}

func parseMatcher(arguments []nginxast.Argument) (MatcherType, string, string, bool, bool) {
	if len(arguments) == 1 {
		value := arguments[0].Value
		if value == "=" || value == "^~" || value == "~" || value == "~*" {
			return MatcherUnknown, "", "", false, false
		}
		if strings.HasPrefix(value, "@") {
			return MatcherNamed, value, arguments[0].Raw, true, validNamedMatcher(value)
		}
		return MatcherPrefix, value, arguments[0].Raw, true, validLiteralMatcher(value)
	}
	if len(arguments) != 2 {
		return MatcherUnknown, "", "", false, false
	}

	matcher := arguments[1].Value
	raw := arguments[1].Raw
	switch arguments[0].Value {
	case "=":
		return MatcherExact, matcher, raw, true, validLiteralMatcher(matcher)
	case "^~":
		return MatcherPrefixPriority, matcher, raw, true, validLiteralMatcher(matcher)
	case "~":
		return MatcherRegex, matcher, raw, true, validRegexMatcher(matcher)
	case "~*":
		return MatcherRegexInsensitive, matcher, raw, true, validRegexMatcher(matcher)
	default:
		return MatcherUnknown, "", "", false, false
	}
}

func validateParent(location *Location, catalog *Catalog) {
	if location == nil {
		return
	}
	for index := range location.Children {
		child := &location.Children[index]
		switch location.Type {
		case MatcherExact:
			markReadOnly(location, "nested_under_exact")
			markSubtreeReadOnly(child, "nested_under_exact")
			addDiagnostic(catalog, DiagnosticNestedUnderExact, SeverityBlocking, child.Source, child.ID, location.ID)
		case MatcherNamed:
			markReadOnly(location, "nested_under_named")
			markSubtreeReadOnly(child, "nested_under_named")
			addDiagnostic(catalog, DiagnosticNestedUnderNamed, SeverityBlocking, child.Source, child.ID, location.ID)
		case MatcherUnknown, MatcherPrefix, MatcherPrefixPriority, MatcherRegex, MatcherRegexInsensitive:
		}

		if child.Type == MatcherNamed {
			markSubtreeReadOnly(child, "nested_named")
			addDiagnostic(catalog, DiagnosticNestedNamed, SeverityBlocking, child.Source, child.ID, location.ID)
			continue
		}
		if !isLiteral(child.Type) || location.Type == MatcherExact || location.Type == MatcherNamed {
			continue
		}
		switch location.Type {
		case MatcherPrefix, MatcherPrefixPriority:
			if !strings.HasPrefix(child.Matcher, location.Matcher) {
				markSubtreeReadOnly(child, "literal_outside_parent")
				addDiagnostic(
					catalog, DiagnosticLiteralOutsideParent, SeverityBlocking,
					child.Source, child.ID, location.ID,
				)
			}
		case MatcherRegex, MatcherRegexInsensitive, MatcherUnknown:
			markSubtreeReadOnly(child, "parent_unprovable")
			addDiagnostic(catalog, DiagnosticParentUnprovable, SeverityBlocking, child.Source, child.ID, location.ID)
		case MatcherExact, MatcherNamed:
			continue
		}
	}
	location.ProxyPassEditable = location.ProxyPassEditable && location.Editable
}

func validateSiblings(locations []Location, parentID string, catalog *Catalog) {
	seen := make(map[string]int, len(locations))
	regexIndexes := make([]int, 0)
	for index := range locations {
		location := &locations[index]
		if location.Type != MatcherUnknown {
			key := string(location.Type) + "\x00" + location.Matcher
			if previous, exists := seen[key]; exists {
				markReadOnly(&locations[previous], "duplicate")
				markReadOnly(location, "duplicate")
				addDiagnostic(catalog, DiagnosticDuplicate, SeverityBlocking, location.Source, location.ID, parentID)
			} else {
				seen[key] = index
			}
		}
		if location.Type == MatcherRegex || location.Type == MatcherRegexInsensitive {
			regexIndexes = append(regexIndexes, index)
		}
	}
	if len(regexIndexes) > 1 {
		for _, index := range regexIndexes {
			location := &locations[index]
			addDiagnostic(
				catalog, DiagnosticRegexOrderSensitive, SeverityWarning,
				location.Source, location.ID, parentID,
			)
		}
	}
	for index := range locations {
		locations[index].ProxyPassEditable = locations[index].ProxyPassEditable && locations[index].Editable
	}
}

func validateDirectProxyPass(location *Location, catalog *Catalog) {
	switch len(location.ProxyPasses) {
	case 0:
		location.ProxyPassEditable = location.Editable
	case 1:
		state := location.ProxyPasses[0].State
		location.ProxyPassEditable = location.Editable &&
			state != upstream.ReferenceDynamic && state != upstream.ReferenceUnknown
		if !location.ProxyPassEditable {
			location.ProxyPassReadOnlyReason = "unsupported_direct_proxy_pass"
		}
	default:
		location.ProxyPassEditable = false
		location.ProxyPassReadOnlyReason = "multiple_direct_proxy_pass"
		addDiagnostic(
			catalog, DiagnosticMultipleProxyPass, SeverityBlocking,
			location.Source, location.ID, "",
		)
	}
}

func directChildren(project *nginxast.Project, parentID string, context nginxast.ContextKind) []*nginxast.NodeRef {
	children := make([]*nginxast.NodeRef, 0)
	for _, reference := range project.Nodes {
		for _, placement := range reference.Placements {
			if placement.ParentID == parentID && placement.Context == context {
				children = append(children, reference)
				break
			}
		}
	}
	return children
}

func spanContainsComment(project *nginxast.Project, path string, span nginxast.Span) bool {
	if project == nil {
		return false
	}
	parsed, exists := project.Documents[path]
	if !exists || parsed.Document == nil {
		return false
	}
	for _, token := range parsed.Document.Tokens {
		if token.Span.Start.Offset >= span.End.Offset {
			break
		}
		if token.Kind == nginxast.TokenComment && token.Span.Start.Offset >= span.Start.Offset {
			return true
		}
	}
	return false
}

func appendSummary(target *[]string, arguments []nginxast.Argument, truncated *bool) {
	values := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		values = append(values, argument.Value)
	}
	appendSummaryValue(target, strings.Join(values, " "), truncated)
}

func appendSummaryValue(target *[]string, value string, truncated *bool) {
	if len(*target) >= maxServerSummaryItems {
		*truncated = true
		return
	}
	runes := []rune(value)
	if len(runes) > maxServerSummaryRunes {
		value = string(runes[:maxServerSummaryRunes])
		*truncated = true
	}
	*target = append(*target, value)
}

func validLiteralMatcher(value string) bool {
	if value == "" || !utf8.ValidString(value) || !strings.HasPrefix(value, "/") ||
		strings.ContainsAny(value, "$;{}#\"'\\") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func validNamedMatcher(value string) bool {
	if len(value) < 2 || value[0] != '@' || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value[1:] {
		if unicode.IsLetter(character) || unicode.IsDigit(character) ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validRegexMatcher(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	_, err := regexp.Compile(value)
	return err == nil
}

func isLiteral(matcherType MatcherType) bool {
	return matcherType == MatcherExact || matcherType == MatcherPrefix || matcherType == MatcherPrefixPriority
}

func markReadOnly(location *Location, reason string) {
	if location == nil {
		return
	}
	location.Editable = false
	location.ProxyPassEditable = false
	if location.ReadOnlyReason == "" {
		location.ReadOnlyReason = reason
	}
}

func markSubtreeReadOnly(location *Location, reason string) {
	if location == nil {
		return
	}
	markReadOnly(location, reason)
	for index := range location.Children {
		markSubtreeReadOnly(&location.Children[index], reason)
	}
}

func addDiagnostic(
	catalog *Catalog,
	code DiagnosticCode,
	severity DiagnosticSeverity,
	source SourceLocation,
	relatedID string,
	parentID string,
) {
	catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
		Code: code, Severity: severity, Source: source, RelatedID: relatedID, ParentID: parentID,
	})
}

func hasPlacementContext(reference *nginxast.NodeRef, context nginxast.ContextKind) bool {
	for _, placement := range reference.Placements {
		if placement.Context == context {
			return true
		}
	}
	return false
}

func sourceLocation(sourcePath string, span nginxast.Span) SourceLocation {
	return SourceLocation{
		Path: sourcePath, StartLine: span.Start.Line, StartColumn: span.Start.Column,
		EndLine: span.End.Line, EndColumn: span.End.Column,
	}
}

func upstreamSourceLocation(sourcePath string, span nginxast.Span) upstream.SourceLocation {
	return upstream.SourceLocation{
		Path: sourcePath, StartLine: span.Start.Line, StartColumn: span.Start.Column,
		EndLine: span.End.Line, EndColumn: span.End.Column,
	}
}
