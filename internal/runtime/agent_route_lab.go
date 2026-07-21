/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package runtime

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

const agentRouteTestRequestLimit = int64(128 << 10)

type agentRouteHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type agentRouteAssertions struct {
	StatusCode    int    `json:"status_code"`
	ContainsText  string `json:"contains_text"`
	ForbiddenText string `json:"forbidden_text"`
}

type agentRouteRequest struct {
	Scheme               routelab.Scheme      `json:"scheme"`
	Host                 string               `json:"host"`
	Port                 int                  `json:"port"`
	SNI                  string               `json:"sni"`
	Method               string               `json:"method"`
	URI                  string               `json:"uri"`
	Query                string               `json:"query"`
	Headers              []agentRouteHeader   `json:"headers"`
	Body                 []byte               `json:"body"`
	TimeoutMS            int64                `json:"timeout_ms"`
	Assertions           agentRouteAssertions `json:"assertions"`
	SideEffecting        bool                 `json:"side_effecting"`
	UpstreamSideEffect   bool                 `json:"upstream_side_effect"`
	Replayable           bool                 `json:"replayable"`
	SensitiveHeaderNames []string             `json:"sensitive_header_names"`
}

type agentRouteTestRequest struct {
	ProtocolVersion  uint16            `json:"protocol_version"`
	RunID            string            `json:"run_id"`
	WorkspaceID      string            `json:"workspace_id"`
	ProductionDigest string            `json:"production_digest"`
	DraftDigest      string            `json:"draft_digest"`
	Request          agentRouteRequest `json:"request"`
	RequestID        string            `json:"request_id"`
}

type agentRouteSource struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

type agentRouteDefinition struct {
	RouteID       string               `json:"route_id"`
	NodeID        string               `json:"node_id"`
	ParentRouteID string               `json:"parent_route_id"`
	Kind          routelab.RouteKind   `json:"kind"`
	MatcherType   routelab.MatcherType `json:"matcher_type"`
	Matcher       string               `json:"matcher"`
	Source        agentRouteSource     `json:"source"`
}

type agentRouteResponse struct {
	StatusCode     int                        `json:"status_code"`
	Headers        []agentRouteHeader         `json:"headers"`
	BodySnippet    string                     `json:"body_snippet"`
	BodyBytes      int64                      `json:"body_bytes"`
	BodyDigest     string                     `json:"body_digest"`
	BodyTruncated  bool                       `json:"body_truncated"`
	SnippetOmitted bool                       `json:"snippet_omitted"`
	DurationMS     int64                      `json:"duration_ms"`
	Assertions     agentRouteAssertionOutcome `json:"assertions"`
}

type agentRouteAssertionResult struct {
	Kind     routelab.AssertionKind `json:"kind"`
	Passed   bool                   `json:"passed"`
	Complete bool                   `json:"complete"`
}

type agentRouteAssertionOutcome struct {
	Passed   bool                        `json:"passed"`
	Complete bool                        `json:"complete"`
	Results  []agentRouteAssertionResult `json:"results"`
}

type agentRouteEvidence struct {
	ServerRouteID  string `json:"server_route_id"`
	RouteID        string `json:"route_id"`
	FinalURI       string `json:"final_uri"`
	Upstream       string `json:"upstream"`
	UpstreamStatus string `json:"upstream_status"`
	StatusCode     int    `json:"status_code"`
	RequestTimeMS  int64  `json:"request_time_ms"`
}

type agentRouteCleanup struct {
	MasterReaped bool `json:"master_reaped"`
	PortClosed   bool `json:"port_closed"`
	StageRemoved bool `json:"stage_removed"`
}

type agentRouteDiagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Summary string `json:"summary"`
}

type agentRouteTestResponse struct {
	CandidateDigest string                 `json:"candidate_digest"`
	Routes          []agentRouteDefinition `json:"routes"`
	Response        agentRouteResponse     `json:"response"`
	Evidence        agentRouteEvidence     `json:"evidence"`
	Cleanup         agentRouteCleanup      `json:"cleanup"`
	Diagnostics     []agentRouteDiagnostic `json:"diagnostics"`
}

func newAgentRouteTestRequest(request routelab.AgentRequest) (agentRouteTestRequest, error) {
	validated, err := routelab.ValidateAgentRequest(request.Request)
	if err != nil || !validRouteRunID(request.RunID) || !validAgentRequestID(request.RequestID) ||
		request.ProductionDigest == (config.Digest{}) || request.DraftDigest == (config.Digest{}) {
		return agentRouteTestRequest{}, routelab.ErrInvalidRequest
	}
	workspaceID, err := config.ParseWorkspaceID(string(request.WorkspaceID))
	if err != nil || workspaceID != request.WorkspaceID {
		return agentRouteTestRequest{}, routelab.ErrInvalidRequest
	}
	return agentRouteTestRequest{
		ProtocolVersion: agentProtocolVersion,
		RunID:           request.RunID, WorkspaceID: string(request.WorkspaceID),
		ProductionDigest: request.ProductionDigest.String(), DraftDigest: request.DraftDigest.String(),
		Request: agentRouteRequestFromValidated(validated), RequestID: request.RequestID,
	}, nil
}

func agentRouteRequestFromValidated(request routelab.ValidatedRequest) agentRouteRequest {
	headers := make([]agentRouteHeader, 0, len(request.Headers))
	for _, header := range request.Headers {
		headers = append(headers, agentRouteHeader{Name: header.Name, Value: header.Value})
	}
	return agentRouteRequest{
		Scheme: request.Scheme, Host: request.Host, Port: request.Port, SNI: request.SNI,
		Method: request.Method, URI: request.URI, Query: request.Query,
		Headers: headers, Body: slices.Clone(request.Body), TimeoutMS: request.Timeout.Milliseconds(),
		Assertions: agentRouteAssertions{
			StatusCode: request.Assertions.StatusCode, ContainsText: request.Assertions.ContainsText,
			ForbiddenText: request.Assertions.ForbiddenText,
		},
		SideEffecting: request.SideEffecting, UpstreamSideEffect: request.UpstreamSideEffect,
		Replayable:           request.Replayable,
		SensitiveHeaderNames: slices.Clone(request.SensitiveHeaderNames),
	}
}

func decodeAgentRouteTestRequest(request *http.Request) (routelab.AgentRequest, error) {
	var decoded agentRouteTestRequest
	if err := decodeAgentTypedRequestLimit(request, &decoded, agentRouteTestRequestLimit); err != nil ||
		decoded.ProtocolVersion != agentProtocolVersion || !validRouteRunID(decoded.RunID) ||
		!validAgentRequestID(decoded.RequestID) {
		return routelab.AgentRequest{}, routelab.ErrInvalidRequest
	}
	workspaceID, workspaceErr := config.ParseWorkspaceID(decoded.WorkspaceID)
	productionDigest, productionErr := config.ParseDigest(decoded.ProductionDigest)
	draftDigest, draftErr := config.ParseDigest(decoded.DraftDigest)
	if workspaceErr != nil || productionErr != nil || draftErr != nil ||
		productionDigest == (config.Digest{}) || draftDigest == (config.Digest{}) {
		return routelab.AgentRequest{}, routelab.ErrInvalidRequest
	}
	validated, err := decoded.Request.validated()
	if err != nil {
		return routelab.AgentRequest{}, err
	}
	return routelab.AgentRequest{
		RunID: decoded.RunID, WorkspaceID: workspaceID,
		ProductionDigest: productionDigest, DraftDigest: draftDigest,
		Request: validated, RequestID: decoded.RequestID,
	}, nil
}

func (request agentRouteRequest) validated() (routelab.ValidatedRequest, error) {
	if request.TimeoutMS <= 0 || request.TimeoutMS > int64((30*time.Second)/time.Millisecond) {
		return routelab.ValidatedRequest{}, routelab.ErrInvalidRequest
	}
	headers := make([]routelab.Header, 0, len(request.Headers))
	for _, header := range request.Headers {
		headers = append(headers, routelab.Header{Name: header.Name, Value: header.Value})
	}
	input := routelab.ValidatedRequest{
		Request: routelab.Request{
			StaticRequest: routelab.StaticRequest{
				Scheme: request.Scheme, Host: request.Host, Port: request.Port, SNI: request.SNI, URI: request.URI,
			},
			Method: request.Method, Query: request.Query, Headers: headers, Body: slices.Clone(request.Body),
			Timeout: time.Duration(request.TimeoutMS) * time.Millisecond,
			Assertions: routelab.Assertions{
				StatusCode: request.Assertions.StatusCode, ContainsText: request.Assertions.ContainsText,
				ForbiddenText: request.Assertions.ForbiddenText,
			},
		},
		SideEffecting: request.SideEffecting, UpstreamSideEffect: request.UpstreamSideEffect,
		Replayable:           request.Replayable,
		SensitiveHeaderNames: slices.Clone(request.SensitiveHeaderNames),
	}
	return routelab.ValidateAgentRequest(input)
}

func newAgentRouteTestResponse(result routelab.AgentResult) agentRouteTestResponse {
	routes := make([]agentRouteDefinition, 0, len(result.Routes))
	for _, route := range result.Routes {
		routes = append(routes, agentRouteDefinition{
			RouteID: route.RouteID, NodeID: route.NodeID, ParentRouteID: route.ParentRouteID,
			Kind: route.Kind, MatcherType: route.MatcherType, Matcher: route.Matcher,
			Source: agentRouteSource{
				Path: route.Source.Path, StartLine: route.Source.StartLine, StartColumn: route.Source.StartColumn,
				EndLine: route.Source.EndLine, EndColumn: route.Source.EndColumn,
			},
		})
	}
	headers := make([]agentRouteHeader, 0, len(result.Response.Headers))
	for _, header := range result.Response.Headers {
		headers = append(headers, agentRouteHeader{Name: header.Name, Value: header.Value})
	}
	diagnostics := make([]agentRouteDiagnostic, 0, len(result.Diagnostics))
	for _, diagnostic := range result.Diagnostics {
		diagnostics = append(diagnostics, agentRouteDiagnostic{
			Code: diagnostic.Code, Path: diagnostic.Path, Line: diagnostic.Line, Summary: diagnostic.Summary,
		})
	}
	assertionResults := make([]agentRouteAssertionResult, 0, len(result.Response.Assertions.Results))
	for _, assertion := range result.Response.Assertions.Results {
		assertionResults = append(assertionResults, agentRouteAssertionResult{
			Kind: assertion.Kind, Passed: assertion.Passed, Complete: assertion.Complete,
		})
	}
	return agentRouteTestResponse{
		CandidateDigest: result.CandidateDigest.String(), Routes: routes,
		Response: agentRouteResponse{
			StatusCode: result.Response.StatusCode, Headers: headers, BodySnippet: result.Response.BodySnippet,
			BodyBytes: result.Response.BodyBytes, BodyDigest: result.Response.BodyDigest,
			BodyTruncated: result.Response.BodyTruncated, SnippetOmitted: result.Response.SnippetOmitted,
			DurationMS: result.Response.Duration.Milliseconds(),
			Assertions: agentRouteAssertionOutcome{
				Passed: result.Response.Assertions.Passed, Complete: result.Response.Assertions.Complete,
				Results: assertionResults,
			},
		},
		Evidence: agentRouteEvidence{
			ServerRouteID: result.Evidence.ServerRouteID, RouteID: result.Evidence.RouteID,
			FinalURI: result.Evidence.FinalURI, Upstream: result.Evidence.Upstream,
			UpstreamStatus: result.Evidence.UpstreamStatus, StatusCode: result.Evidence.StatusCode,
			RequestTimeMS: result.Evidence.RequestTime.Milliseconds(),
		},
		Cleanup: agentRouteCleanup{
			MasterReaped: result.Cleanup.MasterReaped, PortClosed: result.Cleanup.PortClosed,
			StageRemoved: result.Cleanup.StageRemoved,
		},
		Diagnostics: diagnostics,
	}
}

func agentRouteTestResult(response agentRouteTestResponse) (routelab.AgentResult, error) {
	digest, err := config.ParseDigest(response.CandidateDigest)
	if err != nil || digest == (config.Digest{}) || len(response.Routes) == 0 || len(response.Routes) > 6000 ||
		len(response.Response.Headers) > 64 || len(response.Response.BodySnippet) > 16<<10 ||
		response.Response.BodyBytes < 0 || response.Response.BodyBytes > (64<<10)+1 ||
		response.Response.DurationMS < 0 || response.Evidence.RequestTimeMS < 0 ||
		!response.Cleanup.MasterReaped || !response.Cleanup.PortClosed || !response.Cleanup.StageRemoved {
		return routelab.AgentResult{}, errAgentInvalidResponse
	}
	routes := make([]routelab.RouteDefinition, 0, len(response.Routes))
	for _, route := range response.Routes {
		routes = append(routes, routelab.RouteDefinition{
			RouteID: route.RouteID, NodeID: route.NodeID, ParentRouteID: route.ParentRouteID,
			Kind: route.Kind, MatcherType: route.MatcherType, Matcher: route.Matcher,
			Source: routelab.SourceLocation{
				Path: route.Source.Path, StartLine: route.Source.StartLine, StartColumn: route.Source.StartColumn,
				EndLine: route.Source.EndLine, EndColumn: route.Source.EndColumn,
			},
		})
	}
	headers := make([]routelab.Header, 0, len(response.Response.Headers))
	for _, header := range response.Response.Headers {
		headers = append(headers, routelab.Header{Name: header.Name, Value: header.Value})
	}
	diagnostics := make([]routelab.AgentDiagnostic, 0, len(response.Diagnostics))
	for _, diagnostic := range response.Diagnostics {
		diagnostics = append(diagnostics, routelab.AgentDiagnostic{
			Code: diagnostic.Code, Path: diagnostic.Path, Line: diagnostic.Line, Summary: diagnostic.Summary,
		})
	}
	assertionResults := make([]routelab.AssertionResult, 0, len(response.Response.Assertions.Results))
	for _, assertion := range response.Response.Assertions.Results {
		assertionResults = append(assertionResults, routelab.AssertionResult{
			Kind: assertion.Kind, Passed: assertion.Passed, Complete: assertion.Complete,
		})
	}
	result := routelab.AgentResult{
		CandidateDigest: digest, Routes: routes,
		Response: routelab.Response{
			StatusCode: response.Response.StatusCode, Headers: headers, BodySnippet: response.Response.BodySnippet,
			BodyBytes: response.Response.BodyBytes, BodyDigest: response.Response.BodyDigest,
			BodyTruncated: response.Response.BodyTruncated, SnippetOmitted: response.Response.SnippetOmitted,
			Duration: time.Duration(response.Response.DurationMS) * time.Millisecond,
			Assertions: routelab.AssertionOutcome{
				Passed: response.Response.Assertions.Passed, Complete: response.Response.Assertions.Complete,
				Results: assertionResults,
			},
		},
		Evidence: routelab.RuntimeEvidence{
			ServerRouteID: response.Evidence.ServerRouteID, RouteID: response.Evidence.RouteID,
			FinalURI: response.Evidence.FinalURI, Upstream: response.Evidence.Upstream,
			UpstreamStatus: response.Evidence.UpstreamStatus, StatusCode: response.Evidence.StatusCode,
			RequestTime: time.Duration(response.Evidence.RequestTimeMS) * time.Millisecond,
		},
		Cleanup: routelab.CleanupEvidence{
			MasterReaped: response.Cleanup.MasterReaped, PortClosed: response.Cleanup.PortClosed,
			StageRemoved: response.Cleanup.StageRemoved,
		},
		Diagnostics: diagnostics,
	}
	if err := validateRouteEvidence(result); err != nil {
		return routelab.AgentResult{}, errAgentInvalidResponse
	}
	return result, nil
}

// ExecuteRouteTest asks the fixed local Agent to execute one typed isolated route request.
func (client *AgentClient) ExecuteRouteTest(
	ctx context.Context,
	request routelab.AgentRequest,
) (routelab.AgentResult, error) {
	payloadRequest, err := newAgentRouteTestRequest(request)
	if err != nil {
		return routelab.AgentResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := encodeAgentProtocolResponse(payloadRequest)
	if err != nil || int64(len(payload)) > agentRouteTestRequestLimit {
		return routelab.AgentResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	var response agentRouteTestResponse
	if err := client.doJSON(
		ctx,
		request.RequestID,
		http.MethodPost,
		agentProtocolRouteTestPath,
		payload,
		routeLabExecutionTimeout+5*time.Second,
		&response,
	); err != nil {
		return routelab.AgentResult{}, fmt.Errorf("execute agent route test: %w", err)
	}
	result, err := agentRouteTestResult(response)
	if err != nil {
		return routelab.AgentResult{}, fmt.Errorf(
			"execute agent route test: %w",
			newAgentClientProtocolError(agentErrorCodeInternal),
		)
	}
	return result, nil
}

func routeAgentError(code string) error {
	switch code {
	case agentErrorCodeRouteProjectIncomplete:
		return routelab.ErrProjectIncomplete
	case agentErrorCodeRouteListenerAmbiguous:
		return routelab.ErrListenerAmbiguous
	case agentErrorCodeRouteRequestInvalid:
		return routelab.ErrInvalidRequest
	case agentErrorCodeRouteCandidateInvalid:
		return routelab.ErrCandidateInvalid
	case agentErrorCodeRouteSandboxStart:
		return routelab.ErrSandboxStart
	case agentErrorCodeRouteRequestTimeout:
		return routelab.ErrRequestTimeout
	case agentErrorCodeRouteEvidence:
		return routelab.ErrEvidenceIncomplete
	case agentErrorCodeRouteCleanup:
		return routelab.ErrCleanupFailed
	case agentErrorCodeRouteLimit:
		return routelab.ErrLimitExceeded
	default:
		return nil
	}
}

func agentRouteProtocolError(code string) *AgentProtocolError {
	cause := routeAgentError(code)
	if cause == nil {
		return nil
	}
	return &AgentProtocolError{Code: code, Message: cause.Error(), cause: cause}
}
