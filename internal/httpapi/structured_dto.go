/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package httpapi

import (
	"github.com/kuroky/nginx-uix/internal/location"
	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/structuredconfig"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

type structuredCatalogResponse struct {
	WorkspaceID         string                         `json:"workspace_id"`
	DraftETag           string                         `json:"draft_etag"`
	Complete            bool                           `json:"complete"`
	ProjectDiagnostics  []projectDiagnosticResponse    `json:"project_diagnostics"`
	HTTPBlocks          []structuredHTTPBlockResponse  `json:"http_blocks"`
	Upstreams           []structuredUpstreamResponse   `json:"upstreams"`
	ProxyPassReferences []structuredReferenceResponse  `json:"proxy_pass_references"`
	Servers             []structuredHTTPServerResponse `json:"servers"`
	Diagnostics         []structuredDiagnosticResponse `json:"diagnostics"`
}

type structuredSourceResponse struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

type projectDiagnosticResponse struct {
	Code        nginxast.DiagnosticCode `json:"code"`
	Path        string                  `json:"path"`
	Line        int                     `json:"line"`
	Column      int                     `json:"column"`
	RelatedPath string                  `json:"related_path,omitempty"`
}

type preservedSyntaxResponse struct {
	Name     string `json:"name"`
	Editable bool   `json:"editable"`
}

type structuredHTTPBlockResponse struct {
	ID             string                   `json:"id"`
	Source         structuredSourceResponse `json:"source"`
	Editable       bool                     `json:"editable"`
	ReadOnlyReason string                   `json:"read_only_reason,omitempty"`
	Instances      int                      `json:"instances"`
}

type structuredEndpointResponse struct {
	Address string  `json:"address"`
	Port    *uint16 `json:"port"`
	Unix    bool    `json:"unix"`
}

type structuredUpstreamServerResponse struct {
	ID                  string                     `json:"id"`
	Source              structuredSourceResponse   `json:"source"`
	Endpoint            structuredEndpointResponse `json:"endpoint"`
	Weight              *int                       `json:"weight"`
	Backup              bool                       `json:"backup"`
	Down                bool                       `json:"down"`
	MaxFails            *int                       `json:"max_fails"`
	FailTimeout         *string                    `json:"fail_timeout"`
	PreservedParameters []preservedSyntaxResponse  `json:"preserved_parameters"`
	Editable            bool                       `json:"editable"`
	ReadOnlyReason      string                     `json:"read_only_reason,omitempty"`
}

type structuredReferenceResponse struct {
	ID           string                   `json:"id"`
	Source       structuredSourceResponse `json:"source"`
	State        upstream.ReferenceState  `json:"state"`
	Scheme       string                   `json:"scheme,omitempty"`
	Host         string                   `json:"host,omitempty"`
	Port         *uint16                  `json:"port"`
	URI          string                   `json:"uri,omitempty"`
	UpstreamID   string                   `json:"upstream_id,omitempty"`
	UpstreamName string                   `json:"upstream_name,omitempty"`
}

type structuredUpstreamResponse struct {
	ID                  string                             `json:"id"`
	Name                string                             `json:"name"`
	Source              structuredSourceResponse           `json:"source"`
	Servers             []structuredUpstreamServerResponse `json:"servers"`
	PreservedDirectives []preservedSyntaxResponse          `json:"preserved_directives"`
	References          []structuredReferenceResponse      `json:"references"`
	Editable            bool                               `json:"editable"`
	ReadOnlyReason      string                             `json:"read_only_reason,omitempty"`
	Instances           int                                `json:"instances"`
}

type structuredHTTPServerResponse struct {
	ID               string                       `json:"id"`
	Source           structuredSourceResponse     `json:"source"`
	Listens          []string                     `json:"listens"`
	ServerNames      []string                     `json:"server_names"`
	SummaryTruncated bool                         `json:"summary_truncated"`
	Locations        []structuredLocationResponse `json:"locations"`
	Editable         bool                         `json:"editable"`
	ReadOnlyReason   string                       `json:"read_only_reason,omitempty"`
	Instances        int                          `json:"instances"`
}

type structuredLocationResponse struct {
	ID                      string                        `json:"id"`
	Type                    location.MatcherType          `json:"type"`
	Matcher                 string                        `json:"matcher"`
	Source                  structuredSourceResponse      `json:"source"`
	Children                []structuredLocationResponse  `json:"children"`
	ProxyPasses             []structuredReferenceResponse `json:"proxy_passes"`
	UnknownDirectiveCount   int                           `json:"unknown_directive_count"`
	Editable                bool                          `json:"editable"`
	ReadOnlyReason          string                        `json:"read_only_reason,omitempty"`
	ProxyPassEditable       bool                          `json:"proxy_pass_editable"`
	ProxyPassReadOnlyReason string                        `json:"proxy_pass_read_only_reason,omitempty"`
	Instances               int                           `json:"instances"`
}

type structuredDiagnosticResponse struct {
	Domain    string                      `json:"domain"`
	Code      string                      `json:"code"`
	Severity  location.DiagnosticSeverity `json:"severity"`
	Source    structuredSourceResponse    `json:"source"`
	RelatedID string                      `json:"related_id,omitempty"`
	ParentID  string                      `json:"parent_id,omitempty"`
}

func newStructuredCatalogResponse(projection structuredconfig.Projection) structuredCatalogResponse {
	response := structuredCatalogResponse{
		WorkspaceID: string(projection.WorkspaceID), DraftETag: projection.DraftETag,
		Complete:            projection.Complete,
		ProjectDiagnostics:  make([]projectDiagnosticResponse, len(projection.ProjectDiagnostics)),
		HTTPBlocks:          make([]structuredHTTPBlockResponse, len(projection.HTTPBlocks)),
		Upstreams:           make([]structuredUpstreamResponse, len(projection.Upstreams.Upstreams)),
		ProxyPassReferences: make([]structuredReferenceResponse, len(projection.Upstreams.References)),
		Servers:             make([]structuredHTTPServerResponse, len(projection.Locations.Servers)),
		Diagnostics: make(
			[]structuredDiagnosticResponse,
			0,
			len(projection.Upstreams.Diagnostics)+len(projection.Locations.Diagnostics),
		),
	}
	for index, diagnostic := range projection.ProjectDiagnostics {
		response.ProjectDiagnostics[index] = projectDiagnosticResponse{
			Code: diagnostic.Code, Path: diagnostic.Path, Line: diagnostic.Line,
			Column: diagnostic.Column, RelatedPath: diagnostic.RelatedPath,
		}
	}
	for index, block := range projection.HTTPBlocks {
		response.HTTPBlocks[index] = structuredHTTPBlockResponse{
			ID: block.ID, Source: structuredConfigSourceResponse(block.Source),
			Editable: block.Editable, ReadOnlyReason: block.ReadOnlyReason, Instances: block.Instances,
		}
	}
	for index, group := range projection.Upstreams.Upstreams {
		response.Upstreams[index] = newStructuredUpstream(group)
	}
	for index, reference := range projection.Upstreams.References {
		response.ProxyPassReferences[index] = newStructuredReference(reference)
	}
	for _, diagnostic := range projection.Upstreams.Diagnostics {
		response.Diagnostics = append(response.Diagnostics, structuredDiagnosticResponse{
			Domain: "upstream", Code: string(diagnostic.Code),
			Severity: upstreamDiagnosticSeverity(diagnostic.Code),
			Source:   upstreamSourceResponse(diagnostic.Source), RelatedID: diagnostic.RelatedID,
		})
	}
	for index, server := range projection.Locations.Servers {
		response.Servers[index] = newStructuredHTTPServer(server)
	}
	for _, diagnostic := range projection.Locations.Diagnostics {
		response.Diagnostics = append(response.Diagnostics, structuredDiagnosticResponse{
			Domain: "location", Code: string(diagnostic.Code), Severity: diagnostic.Severity,
			Source: locationSourceResponse(diagnostic.Source), RelatedID: diagnostic.RelatedID,
			ParentID: diagnostic.ParentID,
		})
	}
	return response
}

func newStructuredUpstream(group upstream.Upstream) structuredUpstreamResponse {
	response := structuredUpstreamResponse{
		ID: group.ID, Name: group.Name, Source: upstreamSourceResponse(group.Source),
		Servers:             make([]structuredUpstreamServerResponse, len(group.Servers)),
		PreservedDirectives: make([]preservedSyntaxResponse, len(group.PreservedDirectives)),
		References:          make([]structuredReferenceResponse, len(group.References)),
		Editable:            group.Editable, ReadOnlyReason: group.ReadOnlyReason, Instances: group.Instances,
	}
	for index, server := range group.Servers {
		response.Servers[index] = structuredUpstreamServerResponse{
			ID: server.ID, Source: upstreamSourceResponse(server.Source),
			Endpoint: structuredEndpointResponse{
				Address: server.Endpoint.Address, Port: server.Endpoint.Port, Unix: server.Endpoint.Unix,
			},
			Weight: server.Weight, Backup: server.Backup, Down: server.Down,
			MaxFails: server.MaxFails, FailTimeout: server.FailTimeout,
			PreservedParameters: make([]preservedSyntaxResponse, len(server.PreservedParameters)),
			Editable:            server.Editable, ReadOnlyReason: server.ReadOnlyReason,
		}
		for parameterIndex, parameter := range server.PreservedParameters {
			response.Servers[index].PreservedParameters[parameterIndex] = preservedSyntaxResponse{
				Name: parameter.Name, Editable: false,
			}
		}
	}
	for index, directive := range group.PreservedDirectives {
		response.PreservedDirectives[index] = preservedSyntaxResponse{Name: directive.Name, Editable: false}
	}
	for index, reference := range group.References {
		response.References[index] = newStructuredReference(reference)
	}
	return response
}

func newStructuredReference(reference upstream.Reference) structuredReferenceResponse {
	return structuredReferenceResponse{
		ID: reference.ID, Source: upstreamSourceResponse(reference.Source), State: reference.State,
		Scheme: reference.Scheme, Host: reference.Host, Port: reference.Port, URI: reference.URI,
		UpstreamID: reference.UpstreamID, UpstreamName: reference.UpstreamName,
	}
}

func newStructuredHTTPServer(server location.Server) structuredHTTPServerResponse {
	response := structuredHTTPServerResponse{
		ID: server.ID, Source: locationSourceResponse(server.Source),
		Listens:          append([]string(nil), server.Listens...),
		ServerNames:      append([]string(nil), server.ServerNames...),
		SummaryTruncated: server.SummaryTruncated,
		Locations:        make([]structuredLocationResponse, len(server.Locations)),
		Editable:         server.Editable, ReadOnlyReason: server.ReadOnlyReason, Instances: server.Instances,
	}
	if response.Listens == nil {
		response.Listens = make([]string, 0)
	}
	if response.ServerNames == nil {
		response.ServerNames = make([]string, 0)
	}
	for index, candidate := range server.Locations {
		response.Locations[index] = newStructuredLocation(candidate)
	}
	return response
}

func newStructuredLocation(candidate location.Location) structuredLocationResponse {
	response := structuredLocationResponse{
		ID: candidate.ID, Type: candidate.Type, Matcher: candidate.Matcher,
		Source:                locationSourceResponse(candidate.Source),
		Children:              make([]structuredLocationResponse, len(candidate.Children)),
		ProxyPasses:           make([]structuredReferenceResponse, len(candidate.ProxyPasses)),
		UnknownDirectiveCount: candidate.UnknownDirectiveCount,
		Editable:              candidate.Editable, ReadOnlyReason: candidate.ReadOnlyReason,
		ProxyPassEditable:       candidate.ProxyPassEditable,
		ProxyPassReadOnlyReason: candidate.ProxyPassReadOnlyReason,
		Instances:               candidate.Instances,
	}
	for index, child := range candidate.Children {
		response.Children[index] = newStructuredLocation(child)
	}
	for index, reference := range candidate.ProxyPasses {
		response.ProxyPasses[index] = newStructuredReference(reference)
	}
	return response
}

func upstreamSourceResponse(source upstream.SourceLocation) structuredSourceResponse {
	return structuredSourceResponse{
		Path: source.Path, StartLine: source.StartLine, StartColumn: source.StartColumn,
		EndLine: source.EndLine, EndColumn: source.EndColumn,
	}
}

func locationSourceResponse(source location.SourceLocation) structuredSourceResponse {
	return structuredSourceResponse{
		Path: source.Path, StartLine: source.StartLine, StartColumn: source.StartColumn,
		EndLine: source.EndLine, EndColumn: source.EndColumn,
	}
}

func structuredConfigSourceResponse(source structuredconfig.SourceLocation) structuredSourceResponse {
	return structuredSourceResponse{
		Path: source.Path, StartLine: source.StartLine, StartColumn: source.StartColumn,
		EndLine: source.EndLine, EndColumn: source.EndColumn,
	}
}

func upstreamDiagnosticSeverity(code upstream.DiagnosticCode) location.DiagnosticSeverity {
	switch code {
	case upstream.DiagnosticDuplicateName, upstream.DiagnosticServerRawOnly,
		upstream.DiagnosticReferenceAmbiguous, upstream.DiagnosticContextReadOnly:
		return location.SeverityBlocking
	case upstream.DiagnosticReferenceDangling, upstream.DiagnosticReferenceDynamic,
		upstream.DiagnosticReferenceUnknown:
		return location.SeverityWarning
	default:
		return location.SeverityWarning
	}
}
