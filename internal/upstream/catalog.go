/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package upstream

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

// BuildCatalog derives upstreams and proxy_pass references without retaining a second source of truth.
func BuildCatalog(project *nginxast.Project) Catalog {
	catalog := Catalog{ReferenceAnalysisComplete: project != nil && project.Complete}
	if project == nil {
		return catalog
	}

	groupsByName := make(map[string][]int)
	for _, reference := range project.Nodes {
		block, ok := reference.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "upstream" || !hasContext(reference, nginxast.ContextHTTP) {
			continue
		}
		group := buildUpstream(project, reference, block, &catalog)
		index := len(catalog.Upstreams)
		catalog.Upstreams = append(catalog.Upstreams, group)
		if group.Name != "" {
			groupsByName[group.Name] = append(groupsByName[group.Name], index)
		}
	}

	for name, indexes := range groupsByName {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			group := &catalog.Upstreams[index]
			group.Editable = false
			group.ReadOnlyReason = "duplicate_name"
			catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
				Code: DiagnosticDuplicateName, Source: group.Source, RelatedID: group.ID,
			})
		}
		_ = name
	}

	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok || directive.Name.Value != "proxy_pass" {
			continue
		}
		proxyReference := parseReference(reference, directive)
		switch proxyReference.State {
		case ReferenceDynamic:
			catalog.ReferenceAnalysisComplete = false
			catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
				Code: DiagnosticReferenceDynamic, Source: proxyReference.Source, RelatedID: proxyReference.ID,
			})
		case ReferenceUnknown:
			catalog.ReferenceAnalysisComplete = false
			catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
				Code: DiagnosticReferenceUnknown, Source: proxyReference.Source, RelatedID: proxyReference.ID,
			})
		case ReferenceResolved, ReferenceDangling, ReferenceExternal, ReferenceAmbiguous:
			indexes := groupsByName[proxyReference.Host]
			switch len(indexes) {
			case 0:
				if likelyUpstreamName(proxyReference.Host) {
					proxyReference.State = ReferenceDangling
					proxyReference.UpstreamName = proxyReference.Host
					catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
						Code: DiagnosticReferenceDangling, Source: proxyReference.Source, RelatedID: proxyReference.ID,
					})
				} else {
					proxyReference.State = ReferenceExternal
				}
			case 1:
				proxyReference.State = ReferenceResolved
				proxyReference.UpstreamID = catalog.Upstreams[indexes[0]].ID
				proxyReference.UpstreamName = catalog.Upstreams[indexes[0]].Name
				catalog.Upstreams[indexes[0]].References = append(
					catalog.Upstreams[indexes[0]].References, proxyReference,
				)
			default:
				proxyReference.State = ReferenceAmbiguous
				proxyReference.UpstreamName = proxyReference.Host
				catalog.ReferenceAnalysisComplete = false
				catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
					Code: DiagnosticReferenceAmbiguous, Source: proxyReference.Source, RelatedID: proxyReference.ID,
				})
			}
		default:
			catalog.ReferenceAnalysisComplete = false
			proxyReference.State = ReferenceUnknown
			catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
				Code: DiagnosticReferenceUnknown, Source: proxyReference.Source, RelatedID: proxyReference.ID,
			})
		}
		catalog.References = append(catalog.References, proxyReference)
	}
	return catalog
}

func buildUpstream(
	project *nginxast.Project,
	reference *nginxast.NodeRef,
	block *nginxast.Block,
	catalog *Catalog,
) Upstream {
	group := Upstream{
		ID: reference.ID, Source: sourceLocation(reference.Path, block.Span),
		Editable:  !reference.Ambiguous && reference.Instances == 1 && len(block.Arguments) == 1,
		Instances: reference.Instances,
	}
	if len(block.Arguments) == 1 {
		group.Name = block.Arguments[0].Value
	}
	if !group.Editable {
		switch {
		case reference.Ambiguous:
			group.ReadOnlyReason = "ambiguous_context"
		case reference.Instances != 1:
			group.ReadOnlyReason = "multiple_include_instances"
		default:
			group.ReadOnlyReason = "invalid_header"
		}
		catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
			Code: DiagnosticContextReadOnly, Source: group.Source, RelatedID: group.ID,
		})
	}

	for _, child := range project.Nodes {
		placement, ok := singlePlacement(child)
		if !ok || placement.ParentID != reference.ID || placement.Context != nginxast.ContextUpstream {
			continue
		}
		directive, isDirective := child.Node.(*nginxast.Directive)
		if isDirective && directive.Name.Value == "server" {
			server := parseServer(project, child, directive)
			if !server.Editable {
				catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
					Code: DiagnosticServerRawOnly, Source: server.Source, RelatedID: server.ID,
				})
			}
			group.Servers = append(group.Servers, server)
			continue
		}
		name := nodeName(child.Node)
		group.PreservedDirectives = append(group.PreservedDirectives, PreservedSyntax{ID: child.ID, Name: name})
	}
	return group
}

func parseServer(
	project *nginxast.Project,
	reference *nginxast.NodeRef,
	directive *nginxast.Directive,
) Server {
	server := Server{
		ID: reference.ID, Source: sourceLocation(reference.Path, directive.Span),
		Editable: !reference.Ambiguous && reference.Instances == 1,
	}
	if len(directive.Arguments) == 0 {
		server.Editable = false
		server.ReadOnlyReason = "missing_address"
		return server
	}
	endpoint, ok := parseEndpoint(directive.Arguments[0].Value)
	if !ok {
		server.Editable = false
		server.ReadOnlyReason = "invalid_address"
		return server
	}
	server.Endpoint = endpoint

	seen := make(map[string]bool)
	ambiguous := false
	for _, argument := range directive.Arguments[1:] {
		value := argument.Value
		name, rawValue, hasValue := strings.Cut(value, "=")
		switch name {
		case "weight":
			parsed, valid := parseIntegerParameter(rawValue, hasValue, 1, int(^uint(0)>>1))
			if seen[name] || !valid {
				ambiguous = true
				continue
			}
			seen[name] = true
			server.Weight = &parsed
		case "max_fails":
			parsed, valid := parseIntegerParameter(rawValue, hasValue, 0, int(^uint(0)>>1))
			if seen[name] || !valid {
				ambiguous = true
				continue
			}
			seen[name] = true
			server.MaxFails = &parsed
		case "fail_timeout":
			if seen[name] || !hasValue || !validTimeLiteral(rawValue) {
				ambiguous = true
				continue
			}
			seen[name] = true
			copied := rawValue
			server.FailTimeout = &copied
		case "backup":
			if seen[name] || hasValue {
				ambiguous = true
				continue
			}
			seen[name] = true
			server.Backup = true
		case "down":
			if seen[name] || hasValue {
				ambiguous = true
				continue
			}
			seen[name] = true
			server.Down = true
		default:
			server.PreservedParameters = append(server.PreservedParameters, PreservedParameter{
				Name: name, Raw: argument.Raw,
			})
		}
	}
	if server.Backup && server.Down {
		ambiguous = true
	}
	if ambiguous {
		server.Editable = false
		server.ReadOnlyReason = "ambiguous_parameters"
	}
	if server.Editable && spanContainsComment(project, reference.Path, directive.Span) {
		server.Editable = false
		server.ReadOnlyReason = "inline_comment"
	}
	return server
}

func parseEndpoint(raw string) (Endpoint, bool) {
	if raw == "" || strings.ContainsAny(raw, " \t\r\n;{}\"'$") {
		return Endpoint{}, false
	}
	if value, found := strings.CutPrefix(raw, "unix:"); found {
		if !strings.HasPrefix(value, "/") || !safeEndpointText(value, 1024) {
			return Endpoint{}, false
		}
		return Endpoint{Address: value, Unix: true}, true
	}
	if strings.HasPrefix(raw, "[") {
		closing := strings.IndexByte(raw, ']')
		if closing <= 1 || net.ParseIP(raw[1:closing]) == nil {
			return Endpoint{}, false
		}
		endpoint := Endpoint{Address: raw[1:closing]}
		remainder := raw[closing+1:]
		if remainder == "" {
			return endpoint, true
		}
		if !strings.HasPrefix(remainder, ":") {
			return Endpoint{}, false
		}
		port, ok := parsePort(remainder[1:])
		if !ok {
			return Endpoint{}, false
		}
		endpoint.Port = &port
		return endpoint, true
	}
	if net.ParseIP(raw) != nil {
		return Endpoint{Address: raw}, true
	}
	if strings.Count(raw, ":") == 1 {
		host, portText, _ := strings.Cut(raw, ":")
		port, ok := parsePort(portText)
		if !validHostname(host) || !ok {
			return Endpoint{}, false
		}
		return Endpoint{Address: host, Port: &port}, true
	}
	if strings.Contains(raw, ":") {
		return Endpoint{}, false
	}
	if !safeEndpointText(raw, 255) || !validHostname(raw) {
		return Endpoint{}, false
	}
	return Endpoint{Address: raw}, true
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

func parseReference(reference *nginxast.NodeRef, directive *nginxast.Directive) Reference {
	result := Reference{
		ID: reference.ID, Source: sourceLocation(reference.Path, directive.Span), State: ReferenceUnknown,
	}
	if reference.Ambiguous || reference.Instances != 1 || len(directive.Arguments) != 1 {
		return result
	}
	raw := directive.Arguments[0].Value
	if strings.Contains(raw, "$") {
		result.State = ReferenceDynamic
		return result
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil ||
		parsed.Host == "" || parsed.Hostname() == "" || strings.HasPrefix(parsed.Host, "unix:") {
		return result
	}
	result.Scheme = parsed.Scheme
	result.Host = parsed.Hostname()
	if parsed.Port() != "" {
		port, ok := parsePort(parsed.Port())
		if !ok {
			return result
		}
		result.Port = &port
	}
	result.URI = parsed.EscapedPath()
	if parsed.RawQuery != "" {
		result.URI += "?" + parsed.RawQuery
	}
	result.State = ReferenceExternal
	return result
}

func parseIntegerParameter(raw string, present bool, minimum, maximum int) (int, bool) {
	if !present || raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, false
	}
	return value, true
}

func parsePort(raw string) (uint16, bool) {
	value, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || value == 0 {
		return 0, false
	}
	return uint16(value), true
}

func validTimeLiteral(raw string) bool {
	if raw == "" {
		return false
	}
	for offset := 0; offset < len(raw); {
		start := offset
		for offset < len(raw) && raw[offset] >= '0' && raw[offset] <= '9' {
			offset++
		}
		if start == offset {
			return false
		}
		if offset == len(raw) {
			return true
		}
		if raw[offset] == 'm' && offset+1 < len(raw) && raw[offset+1] == 's' {
			offset += 2
			continue
		}
		if !strings.ContainsRune("smhdwMy", rune(raw[offset])) {
			return false
		}
		offset++
	}
	return true
}

func likelyUpstreamName(host string) bool {
	if host == "" || host == "localhost" || net.ParseIP(host) != nil || strings.Contains(host, ".") {
		return false
	}
	for _, value := range host {
		if unicode.IsLetter(value) || unicode.IsDigit(value) || value == '_' || value == '-' {
			continue
		}
		return false
	}
	return true
}

func hasContext(reference *nginxast.NodeRef, context nginxast.ContextKind) bool {
	for _, placement := range reference.Placements {
		if placement.Context == context {
			return true
		}
	}
	return false
}

func singlePlacement(reference *nginxast.NodeRef) (nginxast.Placement, bool) {
	if reference == nil || reference.Ambiguous || len(reference.Placements) != 1 {
		return nginxast.Placement{}, false
	}
	return reference.Placements[0], true
}

func sourceLocation(sourcePath string, span nginxast.Span) SourceLocation {
	return SourceLocation{
		Path: sourcePath, StartLine: span.Start.Line, StartColumn: span.Start.Column,
		EndLine: span.End.Line, EndColumn: span.End.Column,
	}
}

func nodeName(node nginxast.Node) string {
	switch value := node.(type) {
	case *nginxast.Directive:
		return value.Name.Value
	case *nginxast.Block:
		return value.Name.Value
	default:
		return ""
	}
}
