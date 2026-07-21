/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

const (
	routeRequestBodyLimit = int64(128 << 10)
	routeStageReadLimit   = 512
)

// RouteLabAPI is the durable Route Lab behavior consumed by the HTTP boundary.
type RouteLabAPI interface {
	Analyze(context.Context, config.WorkspaceID, string, routelab.Request) (routelab.Analysis, error)
	Queue(context.Context, config.WorkspaceID, string, routelab.Request, config.Actor) (routelab.QueuedRun, error)
	Run(context.Context, routelab.RunID) (routelab.Run, error)
	Stages(context.Context, routelab.RunID, uint64, int) ([]routelab.RunStage, error)
	List(context.Context, routelab.HistoryQuery) ([]routelab.Run, error)
	Cancel(context.Context, routelab.RunID) (routelab.Run, error)
}

// RouteTaskController starts request-independent work and cancels one owned run context.
type RouteTaskController interface {
	Start(routelab.QueuedRun) bool
	Cancel(routelab.RunID) bool
}

type routeLabHandler struct {
	service   RouteLabAPI
	tasks     RouteTaskController
	sessions  SessionService
	publicURL *url.URL
}

type routeHeaderRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type routeAssertionsRequest struct {
	StatusCode    int    `json:"status_code"`
	ContainsText  string `json:"contains_text"`
	ForbiddenText string `json:"forbidden_text"`
}

type routeTestRequest struct {
	Scheme       routelab.Scheme        `json:"scheme"`
	Host         string                 `json:"host"`
	Port         int                    `json:"port"`
	SNI          string                 `json:"sni"`
	Method       string                 `json:"method"`
	URI          string                 `json:"uri"`
	Query        string                 `json:"query"`
	Headers      []routeHeaderRequest   `json:"headers"`
	Body         string                 `json:"body"`
	TimeoutMS    int64                  `json:"timeout_ms"`
	Assertions   routeAssertionsRequest `json:"assertions"`
	Confirmation string                 `json:"confirmation"`
}

type routeSourceResponse struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

type routeListenerResponse struct {
	Address       string `json:"address"`
	Port          int    `json:"port"`
	SSL           bool   `json:"ssl"`
	DefaultServer bool   `json:"default_server"`
	Derived       bool   `json:"derived"`
	Supported     bool   `json:"supported"`
}

type routeServerCandidateResponse struct {
	RouteID     string                        `json:"route_id"`
	Source      routeSourceResponse           `json:"source"`
	Listeners   []routeListenerResponse       `json:"listeners"`
	ServerNames []string                      `json:"server_names"`
	Disposition routelab.CandidateDisposition `json:"disposition"`
	Reason      routelab.CandidateReason      `json:"reason"`
}

type routeLocationCandidateResponse struct {
	RouteID       string                        `json:"route_id"`
	ParentRouteID string                        `json:"parent_route_id"`
	Source        routeSourceResponse           `json:"source"`
	MatcherType   routelab.MatcherType          `json:"matcher_type"`
	Matcher       string                        `json:"matcher"`
	Depth         int                           `json:"depth"`
	Disposition   routelab.CandidateDisposition `json:"disposition"`
	Reason        routelab.CandidateReason      `json:"reason"`
}

type routeAnalysisResponse struct {
	Complete                  bool                             `json:"complete"`
	NormalizedURI             string                           `json:"normalized_uri"`
	PredictedTLSServerRouteID string                           `json:"predicted_tls_server_route_id,omitempty"`
	PredictedServerRouteID    string                           `json:"predicted_server_route_id,omitempty"`
	PredictedLocationRouteID  string                           `json:"predicted_location_route_id,omitempty"`
	RuntimeRedirectPossible   bool                             `json:"runtime_redirect_possible"`
	Servers                   []routeServerCandidateResponse   `json:"servers"`
	Locations                 []routeLocationCandidateResponse `json:"locations"`
}

type routeStageResponse struct {
	Sequence   uint64                `json:"sequence"`
	Stage      routelab.RunStageName `json:"stage"`
	Result     routelab.StageResult  `json:"result"`
	Code       string                `json:"code,omitempty"`
	Details    json.RawMessage       `json:"details"`
	OccurredAt time.Time             `json:"occurred_at"`
}

type routeRunResponse struct {
	ID                   string                `json:"id"`
	WorkspaceID          string                `json:"workspace_id"`
	WorkspaceRevision    uint64                `json:"workspace_revision"`
	WorkspaceETag        string                `json:"workspace_etag"`
	ProductionDigest     string                `json:"production_digest"`
	DraftDigest          string                `json:"draft_digest"`
	CandidateDigest      string                `json:"candidate_digest,omitempty"`
	State                routelab.RunState     `json:"state"`
	Stage                routelab.RunStageName `json:"stage"`
	SafeRequest          json.RawMessage       `json:"safe_request"`
	StaticAnalysis       json.RawMessage       `json:"static_analysis"`
	TerminalResult       json.RawMessage       `json:"terminal_result,omitempty"`
	Replayable           bool                  `json:"replayable"`
	SideEffecting        bool                  `json:"side_effecting"`
	BodyBytes            int64                 `json:"body_bytes"`
	BodyDigest           string                `json:"body_digest"`
	SensitiveHeaderNames []string              `json:"sensitive_header_names"`
	LastErrorCode        string                `json:"last_error_code,omitempty"`
	CancelRequestedAt    *time.Time            `json:"cancel_requested_at,omitempty"`
	CreatedAt            time.Time             `json:"created_at"`
	UpdatedAt            time.Time             `json:"updated_at"`
	StartedAt            *time.Time            `json:"started_at,omitempty"`
	FinishedAt           *time.Time            `json:"finished_at,omitempty"`
	Stages               []routeStageResponse  `json:"stages"`
}

type routeHistoryResponse struct {
	Runs       []routeRunResponse `json:"runs"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type routeHistoryCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func (handler *routeLabHandler) analyze(writer http.ResponseWriter, request *http.Request) {
	if _, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL); !ok {
		return
	}
	if handler.service == nil {
		writeRouteUnavailable(writer, request)
		return
	}
	workspaceID, ok := parseRouteWorkspaceID(writer, request)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	ifMatch, ok := requireRouteIfMatch(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[routeTestRequest](request, routeRequestBodyLimit)
	if err != nil {
		writeRouteDecodeError(writer, request, err)
		return
	}
	domainRequest, err := input.domain()
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	analysis, err := handler.service.Analyze(request.Context(), workspaceID, ifMatch, domainRequest)
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, newRouteAnalysisResponse(analysis))
}

func (handler *routeLabHandler) queue(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok {
		return
	}
	if handler.service == nil || handler.tasks == nil {
		writeRouteUnavailable(writer, request)
		return
	}
	workspaceID, ok := parseRouteWorkspaceID(writer, request)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	ifMatch, ok := requireRouteIfMatch(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[routeTestRequest](request, routeRequestBodyLimit)
	if err != nil {
		writeRouteDecodeError(writer, request, err)
		return
	}
	domainRequest, err := input.domain()
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	queued, err := handler.service.Queue(request.Context(), workspaceID, ifMatch, domainRequest, actor)
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	if !handler.tasks.Start(queued) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 2*time.Second)
		_, _ = handler.service.Cancel(cleanupCtx, queued.Run.ID)
		cancel()
		writeRouteUnavailable(writer, request)
		return
	}
	writer.Header().Set("Location", "/api/v1/route-tests/"+string(queued.Run.ID))
	writeJSON(writer, http.StatusAccepted, newRouteRunResponse(queued.Run, []routelab.RunStage{}))
}

func (handler *routeLabHandler) run(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) {
		return
	}
	if handler.service == nil {
		writeRouteUnavailable(writer, request)
		return
	}
	if !requireNoQuery(writer, request) {
		return
	}
	id, ok := parseRouteRunID(writer, request)
	if !ok {
		return
	}
	run, err := handler.service.Run(request.Context(), id)
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	stages, err := handler.service.Stages(request.Context(), id, 0, routeStageReadLimit)
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, newRouteRunResponse(run, stages))
}

func (handler *routeLabHandler) history(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) {
		return
	}
	if handler.service == nil {
		writeRouteUnavailable(writer, request)
		return
	}
	query, ok := parseRouteHistoryQuery(writer, request)
	if !ok {
		return
	}
	runs, err := handler.service.List(request.Context(), query)
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	response := routeHistoryResponse{Runs: make([]routeRunResponse, 0, len(runs))}
	for _, run := range runs {
		response.Runs = append(response.Runs, newRouteRunResponse(run, []routelab.RunStage{}))
	}
	if len(runs) == query.Limit && len(runs) > 0 {
		response.NextCursor = encodeRouteCursor(runs[len(runs)-1])
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *routeLabHandler) cancel(writer http.ResponseWriter, request *http.Request) {
	if _, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL); !ok {
		return
	}
	if handler.service == nil || handler.tasks == nil {
		writeRouteUnavailable(writer, request)
		return
	}
	if !requireNoQuery(writer, request) {
		return
	}
	id, ok := parseRouteRunID(writer, request)
	if !ok {
		return
	}
	if _, err := decodeStrictJSON[struct{}](request, 4<<10); err != nil {
		writeRouteDecodeError(writer, request, err)
		return
	}
	run, err := handler.service.Cancel(request.Context(), id)
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	_ = handler.tasks.Cancel(id)
	writeJSON(writer, http.StatusAccepted, newRouteRunResponse(run, []routelab.RunStage{}))
}

func (handler *routeLabHandler) events(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) {
		return
	}
	if handler.service == nil {
		writeRouteUnavailable(writer, request)
		return
	}
	if !requireNoQuery(writer, request) {
		return
	}
	id, ok := parseRouteRunID(writer, request)
	if !ok {
		return
	}
	after, ok := parseLastEventID(writer, request)
	if !ok {
		return
	}
	run, err := handler.service.Run(request.Context(), id)
	if err != nil {
		writeRouteAPIError(writer, request, err)
		return
	}
	controller := http.NewResponseController(writer)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	if after == 0 {
		if err := writeReleaseSSEEvent(writer, controller, "0", "snapshot", newRouteRunResponse(run, []routelab.RunStage{})); err != nil {
			return
		}
	}
	for {
		stages, stageErr := handler.service.Stages(request.Context(), id, after, routeStageReadLimit)
		if stageErr != nil {
			return
		}
		run, err = handler.service.Run(request.Context(), id)
		if err != nil {
			return
		}
		if run.State.Terminal() {
			stages, stageErr = handler.service.Stages(request.Context(), id, after, routeStageReadLimit)
			if stageErr != nil {
				return
			}
		}
		for _, stage := range stages {
			eventName := "stage"
			if run.State.Terminal() && stage.Stage == run.Stage {
				eventName = "terminal"
			}
			if err := writeReleaseSSEEvent(
				writer, controller, strconv.FormatUint(stage.Sequence, 10), eventName, newRouteStageResponse(stage),
			); err != nil {
				return
			}
			after = stage.Sequence
		}
		if run.State.Terminal() {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(2 * time.Second):
			if err := writeReleaseSSEEvent(writer, controller, "", "heartbeat", struct{}{}); err != nil {
				return
			}
		}
	}
}

func (input routeTestRequest) domain() (routelab.Request, error) {
	if input.TimeoutMS < 0 || input.TimeoutMS > 30_000 {
		return routelab.Request{}, routelab.ErrInvalidRequest
	}
	headers := make([]routelab.Header, 0, len(input.Headers))
	for _, header := range input.Headers {
		headers = append(headers, routelab.Header{Name: header.Name, Value: header.Value})
	}
	return routelab.Request{
		StaticRequest: routelab.StaticRequest{
			Scheme: input.Scheme, Host: input.Host, Port: input.Port, SNI: input.SNI, URI: input.URI,
		},
		Method: input.Method, Query: input.Query, Headers: headers, Body: []byte(input.Body),
		Timeout: time.Duration(input.TimeoutMS) * time.Millisecond,
		Assertions: routelab.Assertions{
			StatusCode: input.Assertions.StatusCode, ContainsText: input.Assertions.ContainsText,
			ForbiddenText: input.Assertions.ForbiddenText,
		},
		Confirmation: input.Confirmation,
	}, nil
}

func newRouteAnalysisResponse(analysis routelab.Analysis) routeAnalysisResponse {
	response := routeAnalysisResponse{
		Complete: analysis.Complete, NormalizedURI: analysis.NormalizedURI,
		PredictedTLSServerRouteID: analysis.PredictedTLSServerRouteID,
		PredictedServerRouteID:    analysis.PredictedServerRouteID,
		PredictedLocationRouteID:  analysis.PredictedLocationRouteID,
		RuntimeRedirectPossible:   analysis.RuntimeRedirectPossible,
		Servers:                   make([]routeServerCandidateResponse, 0, len(analysis.Servers)),
		Locations:                 make([]routeLocationCandidateResponse, 0, len(analysis.Locations)),
	}
	for _, candidate := range analysis.Servers {
		listeners := make([]routeListenerResponse, 0, len(candidate.Listeners))
		for _, listener := range candidate.Listeners {
			listeners = append(listeners, routeListenerResponse{
				Address: listener.Address, Port: listener.Port, SSL: listener.SSL,
				DefaultServer: listener.DefaultServer, Derived: listener.Derived, Supported: listener.Supported,
			})
		}
		response.Servers = append(response.Servers, routeServerCandidateResponse{
			RouteID: candidate.RouteID, Source: newRouteSourceResponse(candidate.Source),
			Listeners: listeners, ServerNames: append([]string(nil), candidate.ServerNames...),
			Disposition: candidate.Disposition, Reason: candidate.Reason,
		})
	}
	for _, candidate := range analysis.Locations {
		response.Locations = append(response.Locations, routeLocationCandidateResponse{
			RouteID: candidate.RouteID, ParentRouteID: candidate.ParentRouteID,
			Source: newRouteSourceResponse(candidate.Source), MatcherType: candidate.Type,
			Matcher: candidate.Matcher, Depth: candidate.Depth,
			Disposition: candidate.Disposition, Reason: candidate.Reason,
		})
	}
	return response
}

func newRouteSourceResponse(source routelab.SourceLocation) routeSourceResponse {
	return routeSourceResponse{
		Path: source.Path, StartLine: source.StartLine, StartColumn: source.StartColumn,
		EndLine: source.EndLine, EndColumn: source.EndColumn,
	}
}

func newRouteRunResponse(run routelab.Run, stages []routelab.RunStage) routeRunResponse {
	sensitiveNames := make([]string, 0)
	if err := json.Unmarshal([]byte(run.SensitiveHeaderNamesJSON), &sensitiveNames); err != nil || sensitiveNames == nil {
		sensitiveNames = []string{}
	}
	response := routeRunResponse{
		ID: string(run.ID), WorkspaceID: string(run.WorkspaceID), WorkspaceRevision: run.WorkspaceRevision,
		WorkspaceETag: run.WorkspaceETag, ProductionDigest: run.ProductionDigest.String(),
		DraftDigest: run.DraftDigest.String(), State: run.State, Stage: run.Stage,
		SafeRequest:    validRouteRawJSON(run.SafeRequestJSON),
		StaticAnalysis: validRouteRawJSON(run.StaticAnalysisJSON),
		Replayable:     run.Replayable, SideEffecting: run.SideEffecting,
		BodyBytes: run.BodyBytes, BodyDigest: run.BodyDigest.String(), SensitiveHeaderNames: sensitiveNames,
		LastErrorCode: run.LastErrorCode, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
		Stages: make([]routeStageResponse, 0, len(stages)),
	}
	if run.CandidateDigest != (config.Digest{}) {
		response.CandidateDigest = run.CandidateDigest.String()
	}
	if run.TerminalResultJSON != "" {
		response.TerminalResult = validRouteRawJSON(run.TerminalResultJSON)
	}
	response.CancelRequestedAt = routeTimePointer(run.CancelRequestedAt)
	response.StartedAt = routeTimePointer(run.StartedAt)
	response.FinishedAt = routeTimePointer(run.FinishedAt)
	for _, stage := range stages {
		response.Stages = append(response.Stages, newRouteStageResponse(stage))
	}
	return response
}

func newRouteStageResponse(stage routelab.RunStage) routeStageResponse {
	return routeStageResponse{
		Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result, Code: stage.Code,
		Details: validRouteRawJSON(stage.PublicDetailsJSON), OccurredAt: stage.OccurredAt,
	}
}

func validRouteRawJSON(value string) json.RawMessage {
	payload := json.RawMessage(value)
	if !json.Valid(payload) {
		return json.RawMessage(`{}`)
	}
	return payload
}

func routeTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	result := value.UTC()
	return &result
}

func parseRouteWorkspaceID(writer http.ResponseWriter, request *http.Request) (config.WorkspaceID, bool) {
	id, err := config.ParseWorkspaceID(request.PathValue("workspace_id"))
	if err != nil {
		writeRouteInvalidField(writer, request, "workspace_id")
		return "", false
	}
	return id, true
}

func parseRouteRunID(writer http.ResponseWriter, request *http.Request) (routelab.RunID, bool) {
	id, err := routelab.ParseRunID(request.PathValue("run_id"))
	if err != nil {
		writeRouteInvalidField(writer, request, "run_id")
		return "", false
	}
	return id, true
}

func requireRouteIfMatch(writer http.ResponseWriter, request *http.Request) (string, bool) {
	values := request.Header.Values("If-Match")
	if len(values) != 1 {
		writeRouteAPIError(writer, request, config.ErrConflict)
		return "", false
	}
	if _, err := config.ParseStrongETag(values[0], "draft-v1:"); err != nil {
		writeRouteAPIError(writer, request, config.ErrConflict)
		return "", false
	}
	return values[0], true
}

func parseRouteHistoryQuery(writer http.ResponseWriter, request *http.Request) (routelab.HistoryQuery, bool) {
	values := request.URL.Query()
	for key, entries := range values {
		if key != "workspace_id" && key != "state" && key != "cursor" && key != "limit" || len(entries) != 1 {
			writeRouteInvalidField(writer, request, "query")
			return routelab.HistoryQuery{}, false
		}
	}
	query := routelab.HistoryQuery{Limit: 20}
	if raw := values.Get("workspace_id"); raw != "" {
		id, err := config.ParseWorkspaceID(raw)
		if err != nil {
			writeRouteInvalidField(writer, request, "workspace_id")
			return routelab.HistoryQuery{}, false
		}
		query.WorkspaceID = id
	}
	if raw := values.Get("state"); raw != "" {
		query.State = routelab.RunState(raw)
		if !routelab.ValidRunState(query.State) {
			writeRouteInvalidField(writer, request, "state")
			return routelab.HistoryQuery{}, false
		}
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > 100 {
			writeRouteInvalidField(writer, request, "limit")
			return routelab.HistoryQuery{}, false
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeRouteCursor(raw)
		if err != nil {
			writeRouteInvalidField(writer, request, "cursor")
			return routelab.HistoryQuery{}, false
		}
		query.BeforeCreatedAt = cursor.CreatedAt
		query.BeforeID = routelab.RunID(cursor.ID)
	}
	return query, true
}

func encodeRouteCursor(run routelab.Run) string {
	payload, _ := json.Marshal(routeHistoryCursor{CreatedAt: run.CreatedAt.UTC(), ID: string(run.ID)})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeRouteCursor(raw string) (routeHistoryCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(payload) > 512 {
		return routeHistoryCursor{}, routelab.ErrInvalidRequest
	}
	var cursor routeHistoryCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.CreatedAt.IsZero() {
		return routeHistoryCursor{}, routelab.ErrInvalidRequest
	}
	if _, err := routelab.ParseRunID(cursor.ID); err != nil {
		return routeHistoryCursor{}, err
	}
	return cursor, nil
}

func writeRouteDecodeError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, errRequestBodyTooLarge) {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusRequestEntityTooLarge,
			"ROUTE_REQUEST_TOO_LARGE", "路径测试请求超过安全限制", nil)
		return
	}
	if errors.Is(err, errUnsupportedJSONMediaType) {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusUnsupportedMediaType,
			"unsupported_media_type", "仅接受 application/json", nil)
		return
	}
	writeRouteInvalidField(writer, request, "body")
}

func writeRouteInvalidField(writer http.ResponseWriter, request *http.Request, field string) {
	writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusUnprocessableEntity,
		"ROUTE_REQUEST_INVALID", "路径测试请求无效", map[string]any{"field": field})
}

func writeRouteUnavailable(writer http.ResponseWriter, request *http.Request) {
	writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusServiceUnavailable,
		"ROUTE_LAB_UNAVAILABLE", "路径实验室暂时不可用", nil)
}

func writeRouteAPIError(writer http.ResponseWriter, request *http.Request, err error) {
	requestID := requestIDFromContext(request.Context())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeAPIError(writer, requestID, http.StatusNotFound, "ROUTE_TEST_NOT_FOUND", "路径测试不存在", nil)
	case errors.Is(err, config.ErrConflict):
		writeAPIError(writer, requestID, http.StatusConflict, "ROUTE_WORKSPACE_CONFLICT", "配置工作区已变化", nil)
	case errors.Is(err, routelab.ErrConfirmationRequired):
		writeAPIError(writer, requestID, http.StatusConflict, "ROUTE_CONFIRMATION_REQUIRED", "该真实请求需要明确确认", nil)
	case errors.Is(err, routelab.ErrProjectIncomplete):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "ROUTE_PROJECT_INCOMPLETE", "无法完整分析或插桩该配置", nil)
	case errors.Is(err, routelab.ErrListenerAmbiguous):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "ROUTE_LISTENER_AMBIGUOUS", "监听地址组无法唯一选择", nil)
	case errors.Is(err, routelab.ErrInvalidRequest):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "ROUTE_REQUEST_INVALID", "路径测试请求无效", nil)
	case errors.Is(err, routelab.ErrBusy):
		writeAPIError(writer, requestID, http.StatusTooManyRequests, "ROUTE_LAB_BUSY", "路径实验室并发任务已满", nil)
	case errors.Is(err, routelab.ErrCandidateInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "ROUTE_CANDIDATE_INVALID", "沙箱候选配置无法通过检查", nil)
	case errors.Is(err, routelab.ErrSandboxStart):
		writeAPIError(writer, requestID, http.StatusServiceUnavailable, "ROUTE_SANDBOX_START_FAILED", "沙箱 Nginx 无法启动", nil)
	case errors.Is(err, routelab.ErrCleanupFailed):
		writeAPIError(writer, requestID, http.StatusInternalServerError, "ROUTE_CLEANUP_FAILED", "沙箱清理无法确认", nil)
	case errors.Is(err, routelab.ErrRequestTimeout), errors.Is(err, context.DeadlineExceeded):
		writeAPIError(writer, requestID, http.StatusGatewayTimeout, "ROUTE_REQUEST_TIMEOUT", "路径测试请求超时", nil)
	case errors.Is(err, routelab.ErrEvidenceIncomplete):
		writeAPIError(writer, requestID, http.StatusConflict, "ROUTE_EVIDENCE_INCOMPLETE", "运行时命中证据不完整", nil)
	case errors.Is(err, routelab.ErrAlreadyTerminal):
		writeAPIError(writer, requestID, http.StatusConflict, "ROUTE_ALREADY_TERMINAL", "路径测试已经结束", nil)
	case errors.Is(err, routelab.ErrLimitExceeded):
		writeAPIError(writer, requestID, http.StatusRequestEntityTooLarge, "ROUTE_LIMIT_EXCEEDED", "路径测试超过安全限制", nil)
	default:
		writeAPIError(writer, requestID, http.StatusInternalServerError, "internal_error", "服务暂时不可用", nil)
	}
}
