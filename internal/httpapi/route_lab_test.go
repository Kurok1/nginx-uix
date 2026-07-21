/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

func TestRouteLabAnalysisAndQueueUseStrictTypedWorkspaceInput(t *testing.T) {
	api := &routeAPIStub{
		analysis: routelab.Analysis{
			Complete: true, NormalizedURI: "/api", PredictedServerRouteID: "srv_1",
			PredictedLocationRouteID: "loc_1", Servers: []routelab.ServerCandidate{},
			Locations: []routelab.LocationCandidate{},
		},
		queued: routelab.QueuedRun{Run: testHTTPRouteRun(routelab.RunStateQueued)},
	}
	tasks := &routeTaskStub{}
	body := `{"scheme":"http","host":"example.test","port":8080,"method":"GET","uri":"/api","query":"source=test","headers":[],"body":"","timeout_ms":1000,"assertions":{"status_code":200,"contains_text":"","forbidden_text":""},"confirmation":""}`
	analysis := serveRouteMutation(t, "/api/v1/config/workspaces/11111111111111111111111111111111/route-analyses", body, api, tasks)
	if analysis.Code != http.StatusOK || !strings.Contains(analysis.Body.String(), `"predicted_location_route_id":"loc_1"`) {
		t.Fatalf("analysis status = %d, body = %s", analysis.Code, analysis.Body.String())
	}
	if api.ifMatch != `"draft-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"` ||
		api.request.Host != "example.test" || api.request.Query != "source=test" {
		t.Fatalf("analysis input = etag %q request %+v", api.ifMatch, api.request)
	}

	queue := serveRouteMutation(t, "/api/v1/config/workspaces/11111111111111111111111111111111/route-tests", body, api, tasks)
	if queue.Code != http.StatusAccepted || queue.Header().Get("Location") != "/api/v1/route-tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ||
		!tasks.started || !strings.Contains(queue.Body.String(), `"state":"queued"`) {
		t.Fatalf("queue status = %d, headers = %+v, body = %s", queue.Code, queue.Header(), queue.Body.String())
	}
}

func TestRouteLabGetCancelAndTerminalSSE(t *testing.T) {
	run := testHTTPRouteRun(routelab.RunStateSucceeded)
	api := &routeAPIStub{
		run: run,
		stages: []routelab.RunStage{
			{RunID: run.ID, Sequence: 1, Stage: routelab.RunStageQueued, Result: routelab.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: run.CreatedAt},
			{RunID: run.ID, Sequence: 2, Stage: routelab.RunStageCompleted, Result: routelab.StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: run.FinishedAt},
		},
	}
	tasks := &routeTaskStub{}
	get := serveRouteGET(t, "/api/v1/route-tests/"+string(run.ID), api, tasks, "")
	if get.Code != http.StatusOK || get.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(get.Body.String(), `"safe_request"`) ||
		!strings.Contains(get.Body.String(), `"stages"`) {
		t.Fatalf("GET status = %d, body = %s", get.Code, get.Body.String())
	}

	events := serveRouteGET(t, "/api/v1/route-tests/"+string(run.ID)+"/events", api, tasks, "1")
	if events.Code != http.StatusOK || events.Header().Get("Content-Type") != "text/event-stream" ||
		events.Header().Get("Cache-Control") != "no-store" || events.Header().Get("X-Accel-Buffering") != "no" ||
		!strings.Contains(events.Body.String(), "id: 2\nevent: terminal") || strings.Contains(events.Body.String(), "event: snapshot") {
		t.Fatalf("events status = %d, body = %s", events.Code, events.Body.String())
	}

	api.run = testHTTPRouteRun(routelab.RunStateRunning)
	cancel := serveRouteMutation(t, "/api/v1/route-tests/"+string(run.ID)+"/cancellations", `{}`, api, tasks)
	if cancel.Code != http.StatusAccepted || !tasks.cancelled || api.cancelID != run.ID {
		t.Fatalf("cancel status = %d, body = %s", cancel.Code, cancel.Body.String())
	}
}

func TestRouteLabRejectsUnsupportedMediaType(t *testing.T) {
	issued := testIssuedSession()
	request := httptest.NewRequest(http.MethodPost,
		"http://example.test/api/v1/config/workspaces/11111111111111111111111111111111/route-tests",
		strings.NewReader(`{}`),
	)
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set(csrfHeaderName, issued.CSRFToken)
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("If-Match", `"draft-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{
		Sessions: &authorizationSessionStub{issued: issued}, RouteLab: &routeAPIStub{}, RouteTasks: &routeTaskStub{},
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
	}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType || !strings.Contains(recorder.Body.String(), `"code":"unsupported_media_type"`) {
		t.Fatalf("unsupported media response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func serveRouteMutation(
	t *testing.T,
	target string,
	body string,
	api RouteLabAPI,
	tasks RouteTaskController,
) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	request := httptest.NewRequest(http.MethodPost, "http://example.test"+target, strings.NewReader(body))
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set(csrfHeaderName, issued.CSRFToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"draft-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{
		Sessions: &authorizationSessionStub{issued: issued}, RouteLab: api, RouteTasks: tasks,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
	}).ServeHTTP(recorder, request)
	return recorder
}

func serveRouteGET(
	t *testing.T,
	target string,
	api RouteLabAPI,
	tasks RouteTaskController,
	lastEventID string,
) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{
		Sessions: &authorizationSessionStub{issued: issued}, RouteLab: api, RouteTasks: tasks,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
	}).ServeHTTP(recorder, request)
	return recorder
}

func testHTTPRouteRun(state routelab.RunState) routelab.Run {
	created := time.Date(2026, time.July, 20, 1, 2, 3, 0, time.UTC)
	run := routelab.Run{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WorkspaceID: "11111111111111111111111111111111",
		WorkspaceRevision: 3, WorkspaceETag: `"draft-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		ProductionDigest: config.Digest{1}, DraftDigest: config.Digest{2},
		State: state, Stage: routelab.RunStageQueued,
		SafeRequestJSON:    `{"scheme":"http","host":"example.test","headers":[]}`,
		StaticAnalysisJSON: `{"complete":true}`, Replayable: true,
		BodyDigest: config.Digest{3}, SensitiveHeaderNamesJSON: `[]`,
		CreatedBy: 1, RequestID: "request-route", CreatedAt: created, UpdatedAt: created,
	}
	if state == routelab.RunStateRunning {
		run.Stage = routelab.RunStagePreparing
		run.StartedAt = created.Add(time.Second)
		run.UpdatedAt = run.StartedAt
	}
	if state == routelab.RunStateSucceeded {
		run.Stage = routelab.RunStageCompleted
		run.CandidateDigest = config.Digest{4}
		run.TerminalResultJSON = `{"agent_result":{"response":{"status_code":200}}}`
		run.StartedAt = created.Add(time.Second)
		run.FinishedAt = created.Add(2 * time.Second)
		run.UpdatedAt = run.FinishedAt
	}
	return run
}

type routeAPIStub struct {
	analysis AnalysisResponseAlias
	queued   routelab.QueuedRun
	run      routelab.Run
	stages   []routelab.RunStage
	ifMatch  string
	request  routelab.Request
	cancelID routelab.RunID
}

type AnalysisResponseAlias = routelab.Analysis

func (api *routeAPIStub) Analyze(_ context.Context, _ config.WorkspaceID, ifMatch string, request routelab.Request) (routelab.Analysis, error) {
	api.ifMatch = ifMatch
	api.request = request
	return api.analysis, nil
}

func (api *routeAPIStub) Queue(_ context.Context, _ config.WorkspaceID, ifMatch string, request routelab.Request, _ config.Actor) (routelab.QueuedRun, error) {
	api.ifMatch = ifMatch
	api.request = request
	validated, err := routelab.ValidateRequest(request)
	if err != nil {
		return routelab.QueuedRun{}, err
	}
	api.queued.Request = validated
	return api.queued, nil
}

func (api *routeAPIStub) Run(context.Context, routelab.RunID) (routelab.Run, error) {
	return api.run, nil
}

func (api *routeAPIStub) Stages(_ context.Context, _ routelab.RunID, after uint64, _ int) ([]routelab.RunStage, error) {
	result := make([]routelab.RunStage, 0)
	for _, stage := range api.stages {
		if stage.Sequence > after {
			result = append(result, stage)
		}
	}
	return result, nil
}

func (api *routeAPIStub) List(context.Context, routelab.HistoryQuery) ([]routelab.Run, error) {
	return []routelab.Run{api.run}, nil
}

func (api *routeAPIStub) Cancel(_ context.Context, id routelab.RunID) (routelab.Run, error) {
	api.cancelID = id
	return api.run, nil
}

type routeTaskStub struct {
	started   bool
	cancelled bool
}

func (tasks *routeTaskStub) Start(routelab.QueuedRun) bool {
	tasks.started = true
	return true
}

func (tasks *routeTaskStub) Cancel(routelab.RunID) bool {
	tasks.cancelled = true
	return true
}
