/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestServiceQueuesSecretFreeRunAndExecutesAgentResult(t *testing.T) {
	snapshot := testDraftSnapshot(t)
	repository := newMemoryRouteRepository()
	agent := &recordingRouteAgent{}
	service := mustRouteService(t, ServiceOptions{
		Workspaces: staticWorkspaceStore{snapshot: snapshot}, Repository: repository, Agent: agent,
		TokenSource: bytes.NewReader(bytes.Repeat([]byte{0xab}, 64)), Now: monotonicRouteClock(testRouteTime(1)),
	})
	request := Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 8080, URI: "/api"},
		Method:        "POST", Headers: []Header{{Name: "Authorization", Value: "Bearer secret"}},
		Body: []byte("private-body"), Timeout: time.Second,
		Assertions:   Assertions{StatusCode: 201, ContainsText: "created"},
		Confirmation: SideEffectConfirmation,
	}
	queued, err := service.Queue(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request, config.Actor{
		UserID: 1, RequestID: "request-route-1",
	})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}
	if queued.Run.State != RunStateQueued || queued.Run.Stage != RunStageQueued || queued.Request.Body == nil ||
		queued.Run.ID == "" || !queued.Run.SideEffecting || queued.Run.Replayable {
		t.Fatalf("queued = %+v", queued)
	}
	if strings.Contains(queued.Run.SafeRequestJSON, "Bearer secret") || strings.Contains(queued.Run.SafeRequestJSON, "private-body") {
		t.Fatalf("safe request persisted a secret: %s", queued.Run.SafeRequestJSON)
	}
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := BuildRouteDefinitions(project)
	if err != nil {
		t.Fatal(err)
	}
	agent.result = AgentResult{
		CandidateDigest: config.Digest{9}, Routes: routes,
		Response: Response{
			StatusCode: 201, BodySnippet: "created", BodyBytes: 7, BodyDigest: strings.Repeat("0", 64),
			Assertions: EvaluateAssertions(request.Assertions, 201, []byte("created"), false),
		},
		Evidence: RuntimeEvidence{
			ServerRouteID: routes[0].RouteID, RouteID: routes[1].RouteID,
			FinalURI: "/api", StatusCode: 201,
		},
		Cleanup: CleanupEvidence{MasterReaped: true, PortClosed: true, StageRemoved: true},
	}
	completed, err := service.Execute(context.Background(), queued.Run.ID, queued.Request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if completed.State != RunStateSucceeded || completed.Stage != RunStageCompleted ||
		completed.TerminalResultJSON == "" || completed.CandidateDigest != agent.result.CandidateDigest ||
		agent.request.RunID != string(queued.Run.ID) || agent.request.WorkspaceID != snapshot.Workspace.ID {
		t.Fatalf("completed = %+v, agent request = %+v", completed, agent.request)
	}
	if strings.Contains(completed.TerminalResultJSON, "Bearer secret") || strings.Contains(completed.TerminalResultJSON, "private-body") {
		t.Fatalf("terminal result persisted a request secret: %s", completed.TerminalResultJSON)
	}
	for _, required := range []string{`"normalized_uri"`, `"candidate_digest"`, `"duration_ms"`, `"request_time_ms"`, `"diagnostics":[]`} {
		if !strings.Contains(queued.Run.StaticAnalysisJSON+completed.TerminalResultJSON, required) {
			t.Fatalf("persisted route JSON does not contain %s: analysis=%s terminal=%s", required, queued.Run.StaticAnalysisJSON, completed.TerminalResultJSON)
		}
	}
	for _, forbidden := range []string{`"NormalizedURI"`, `"CandidateDigest"`, `"Duration"`, `"RequestTime"`} {
		if strings.Contains(queued.Run.StaticAnalysisJSON+completed.TerminalResultJSON, forbidden) {
			t.Fatalf("persisted route JSON contains Go field %s: analysis=%s terminal=%s", forbidden, queued.Run.StaticAnalysisJSON, completed.TerminalResultJSON)
		}
	}
	if got := repository.stages[queued.Run.ID]; len(got) != 4 || got[0].Stage != RunStageQueued ||
		got[1].Stage != RunStagePreparing || got[2].Stage != RunStageCollecting || got[3].Stage != RunStageCompleted {
		t.Fatalf("stages = %+v", got)
	}
}

func TestServiceRejectsWorkspaceConflictAndPersistsTimeoutTerminal(t *testing.T) {
	snapshot := testDraftSnapshot(t)
	repository := newMemoryRouteRepository()
	agent := &recordingRouteAgent{err: ErrRequestTimeout}
	service := mustRouteService(t, ServiceOptions{
		Workspaces: staticWorkspaceStore{snapshot: snapshot}, Repository: repository, Agent: agent,
		TokenSource: bytes.NewReader(bytes.Repeat([]byte{0xcd}, 64)), Now: monotonicRouteClock(testRouteTime(1)),
	})
	request := Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 8080, URI: "/api"},
		Method:        "GET", Timeout: time.Second,
	}
	if _, err := service.Analyze(context.Background(), snapshot.Workspace.ID, `"draft:wrong"`, request); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("Analyze() error = %v, want conflict", err)
	}
	queued, err := service.Queue(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request, config.Actor{
		UserID: 1, RequestID: "request-route-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	timedOut, err := service.Execute(context.Background(), queued.Run.ID, queued.Request)
	if !errors.Is(err, ErrRequestTimeout) || timedOut.State != RunStateTimedOut ||
		timedOut.Stage != RunStageTimedOut || timedOut.LastErrorCode != "route_request_timeout" {
		t.Fatalf("Execute() = %+v, %v", timedOut, err)
	}
}

func TestServiceRequiresExecutionConfirmationForPotentialUpstreamGET(t *testing.T) {
	snapshot := testDraftSnapshotWithSource(t, []byte(`events {}
http {
    server {
        listen 8080;
        server_name example.test;
	        location /api { proxy_pass http://127.0.0.1:9000; }
	    }
	}
`))
	service := mustRouteService(t, ServiceOptions{
		Workspaces: staticWorkspaceStore{snapshot: snapshot}, Repository: newMemoryRouteRepository(),
		Agent: &recordingRouteAgent{}, TokenSource: bytes.NewReader(bytes.Repeat([]byte{0xef}, 64)),
		Now: monotonicRouteClock(testRouteTime(1)),
	})
	request := Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 8080, URI: "/api"},
		Method:        "GET", Timeout: time.Second,
	}
	if _, err := service.Analyze(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	actor := config.Actor{UserID: 1, RequestID: "request-route-upstream"}
	if _, err := service.Queue(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request, actor); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("Queue() error = %v, want ErrConfirmationRequired", err)
	}
	request.Confirmation = SideEffectConfirmation
	queued, err := service.Queue(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request, actor)
	if err != nil {
		t.Fatalf("Queue(confirmed) error = %v", err)
	}
	if !queued.Run.SideEffecting || !queued.Request.SideEffecting || !queued.Request.UpstreamSideEffect {
		t.Fatalf("confirmed upstream run = %+v", queued)
	}
}

func TestServiceQueuesInstrumentablePCREWhenStaticPredictionIsIncomplete(t *testing.T) {
	snapshot := testDraftSnapshotWithSource(t, []byte(`events {}
http {
    server {
        listen 8080;
        server_name example.test;
        location ~ ^/api(?=/) { return 200; }
        location ~ ^/api/ { return 201; }
    }
}
`))
	repository := newMemoryRouteRepository()
	service := mustRouteService(t, ServiceOptions{
		Workspaces: staticWorkspaceStore{snapshot: snapshot}, Repository: repository,
		Agent: &recordingRouteAgent{}, TokenSource: bytes.NewReader(bytes.Repeat([]byte{0xee}, 64)),
		Now: monotonicRouteClock(testRouteTime(1)),
	})
	request := Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 8080, URI: "/api/value"},
		Method:        "GET", Timeout: time.Second,
	}
	analysis, err := service.Analyze(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.Complete || analysis.PredictedLocationRouteID != "" {
		t.Fatalf("analysis = %+v, want an incomplete static prediction", analysis)
	}
	queued, err := service.Queue(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request, config.Actor{
		UserID: 1, RequestID: "request-route-pcre",
	})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}
	if queued.Run.State != RunStateQueued || !strings.Contains(queued.Run.StaticAnalysisJSON, `"complete":false`) {
		t.Fatalf("queued run = %+v", queued.Run)
	}
}

func TestServiceCancellationWinsBeforeSuccessfulAgentSettlement(t *testing.T) {
	snapshot := testDraftSnapshot(t)
	repository := newMemoryRouteRepository()
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := BuildRouteDefinitions(project)
	if err != nil {
		t.Fatal(err)
	}
	agent := &cancellingRouteAgent{repository: repository, result: AgentResult{
		CandidateDigest: config.Digest{9}, Routes: routes,
		Response: Response{StatusCode: 200, BodyDigest: strings.Repeat("0", 64)},
		Evidence: RuntimeEvidence{
			ServerRouteID: routes[0].RouteID, RouteID: routes[1].RouteID, FinalURI: "/api", StatusCode: 200,
		},
		Cleanup: CleanupEvidence{MasterReaped: true, PortClosed: true, StageRemoved: true},
	}}
	service := mustRouteService(t, ServiceOptions{
		Workspaces: staticWorkspaceStore{snapshot: snapshot}, Repository: repository, Agent: agent,
		TokenSource: bytes.NewReader(bytes.Repeat([]byte{0xfa}, 64)), Now: monotonicRouteClock(testRouteTime(1)),
	})
	request := Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 8080, URI: "/api"},
		Method:        "GET", Timeout: time.Second,
	}
	queued, err := service.Queue(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request, config.Actor{
		UserID: 1, RequestID: "request-route-cancel-race",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.Execute(context.Background(), queued.Run.ID, queued.Request)
	if !errors.Is(err, context.Canceled) || completed.State != RunStateCancelled || completed.CancelRequestedAt.IsZero() {
		t.Fatalf("Execute() = %+v, %v", completed, err)
	}
	persisted, readErr := repository.RouteRun(context.Background(), queued.Run.ID)
	if readErr != nil || persisted.State != RunStateCancelled || persisted.CancelRequestedAt.IsZero() {
		t.Fatalf("persisted cancellation = %+v, %v", persisted, readErr)
	}
}

func TestServiceCleanupFailureOverridesCancellation(t *testing.T) {
	snapshot := testDraftSnapshot(t)
	repository := newMemoryRouteRepository()
	service := mustRouteService(t, ServiceOptions{
		Workspaces: staticWorkspaceStore{snapshot: snapshot}, Repository: repository,
		Agent:       &recordingRouteAgent{err: errors.Join(context.Canceled, ErrCleanupFailed)},
		TokenSource: bytes.NewReader(bytes.Repeat([]byte{0xfb}, 64)), Now: monotonicRouteClock(testRouteTime(1)),
	})
	request := Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 8080, URI: "/api"},
		Method:        "GET", Timeout: time.Second,
	}
	queued, err := service.Queue(context.Background(), snapshot.Workspace.ID, snapshot.WorkspaceETag, request, config.Actor{
		UserID: 1, RequestID: "request-route-cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := service.Execute(context.Background(), queued.Run.ID, queued.Request)
	if !errors.Is(err, ErrCleanupFailed) || failed.State != RunStateFailed ||
		failed.Stage != RunStageFailed || failed.LastErrorCode != "route_cleanup_failed" {
		t.Fatalf("Execute() = %+v, %v", failed, err)
	}
}

func testDraftSnapshot(t *testing.T) config.DraftSnapshot {
	t.Helper()
	return testDraftSnapshotWithSource(t, []byte(`events {}
http {
    server {
        listen 8080 default_server;
        server_name example.test;
        location /api { return 201 "created"; }
    }
}
`))
}

func testDraftSnapshotWithSource(t *testing.T, content []byte) config.DraftSnapshot {
	t.Helper()
	digest := config.Digest{2}
	return config.DraftSnapshot{
		Workspace: config.Workspace{
			ID: "11111111111111111111111111111111", State: config.StateReady,
			ProductionDigest: config.Digest{1}, DraftDigest: digest, Revision: 3,
		},
		WorkspaceETag: `"draft:test"`,
		Files:         []config.DraftFile{{Path: "nginx.conf", Content: content, ContentDigest: digest}},
	}
}

func testRouteTime(second int) time.Time {
	return time.Date(2026, time.July, 20, 1, 2, second, 0, time.UTC)
}

func monotonicRouteClock(start time.Time) func() time.Time {
	current := start.Add(-time.Second)
	return func() time.Time {
		current = current.Add(time.Second)
		return current
	}
}

func mustRouteService(t *testing.T, options ServiceOptions) *Service {
	t.Helper()
	service, err := NewService(options)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type staticWorkspaceStore struct {
	snapshot config.DraftSnapshot
	err      error
}

func (store staticWorkspaceStore) DraftSnapshot(context.Context, config.WorkspaceID) (config.DraftSnapshot, error) {
	return store.snapshot, store.err
}

type recordingRouteAgent struct {
	request AgentRequest
	result  AgentResult
	err     error
}

type cancellingRouteAgent struct {
	repository *memoryRouteRepository
	result     AgentResult
}

func (agent *cancellingRouteAgent) ExecuteRouteTest(ctx context.Context, request AgentRequest) (AgentResult, error) {
	_, err := agent.repository.RequestRouteRunCancellation(context.WithoutCancel(ctx), RunID(request.RunID), testRouteTime(20))
	return agent.result, err
}

func (agent *recordingRouteAgent) ExecuteRouteTest(_ context.Context, request AgentRequest) (AgentResult, error) {
	agent.request = request
	return agent.result, agent.err
}

type memoryRouteRepository struct {
	runs   map[RunID]Run
	stages map[RunID][]RunStage
	active int
}

func newMemoryRouteRepository() *memoryRouteRepository {
	return &memoryRouteRepository{runs: make(map[RunID]Run), stages: make(map[RunID][]RunStage)}
}

func (repository *memoryRouteRepository) CreateRouteRun(_ context.Context, run Run, stage RunStage) error {
	if repository.active >= 8 {
		return ErrBusy
	}
	repository.runs[run.ID] = run
	repository.stages[run.ID] = []RunStage{stage}
	repository.active++
	return nil
}

func (repository *memoryRouteRepository) TransitionRouteRun(
	_ context.Context,
	expectedState RunState,
	expectedStage RunStageName,
	next Run,
	stage RunStage,
) error {
	current, exists := repository.runs[next.ID]
	if !exists {
		return fs.ErrNotExist
	}
	if current.State != expectedState || current.Stage != expectedStage ||
		stage.Sequence != uint64(len(repository.stages[next.ID])+1) {
		return config.ErrConflict
	}
	if !current.State.Terminal() && next.State.Terminal() {
		repository.active--
	}
	repository.runs[next.ID] = next
	repository.stages[next.ID] = append(repository.stages[next.ID], stage)
	return nil
}

func (repository *memoryRouteRepository) RouteRun(_ context.Context, id RunID) (Run, error) {
	run, exists := repository.runs[id]
	if !exists {
		return Run{}, fs.ErrNotExist
	}
	return run, nil
}

func (repository *memoryRouteRepository) RouteRunStages(_ context.Context, id RunID, after uint64, limit int) ([]RunStage, error) {
	stages := repository.stages[id]
	if int(after) >= len(stages) {
		return []RunStage{}, nil
	}
	end := min(len(stages), int(after)+limit)
	return append([]RunStage(nil), stages[after:end]...), nil
}

func (repository *memoryRouteRepository) ListRouteRuns(context.Context, HistoryQuery) ([]Run, error) {
	result := make([]Run, 0, len(repository.runs))
	for _, run := range repository.runs {
		result = append(result, run)
	}
	return result, nil
}

func (repository *memoryRouteRepository) ActiveRouteRunCount(context.Context) (int, error) {
	return repository.active, nil
}

func (repository *memoryRouteRepository) RequestRouteRunCancellation(_ context.Context, id RunID, at time.Time) (Run, error) {
	run, exists := repository.runs[id]
	if !exists {
		return Run{}, fs.ErrNotExist
	}
	if run.State.Terminal() {
		return Run{}, ErrAlreadyTerminal
	}
	run.CancelRequestedAt = at
	repository.runs[id] = run
	return run, nil
}

func (repository *memoryRouteRepository) FailInterruptedRouteRuns(context.Context, time.Time, string) (int, error) {
	return 0, nil
}
