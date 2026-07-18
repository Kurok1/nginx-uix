/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package httpapi

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestReleaseHTTPCheckQueueQueryAndTerminalSSE(t *testing.T) {
	workspace := configWorkspaceFixture()
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	check := config.PublishCheck{
		ID: "11111111111111111111111111111111", WorkspaceID: workspace.ID, WorkspaceRevision: workspace.Revision,
		ProductionDigest: workspace.ProductionDigest, BaseDigest: workspace.BaseDigest, DraftDigest: workspace.DraftDigest,
		CandidateDigest: config.Digest{9}, ManifestVersion: 1, PolicyVersion: 1, ValidatorVersion: 1,
		ValidatorBuildID: "build-id", State: config.PublishCheckStateValid, PublicDetailsJSON: `{"diagnostics":[]}`,
		CreatedBy: 1, RequestID: "request", StartedAt: now, FinishedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	release := config.Release{
		ID: "22222222222222222222222222222222", WorkspaceID: workspace.ID, CheckID: check.ID,
		BackupID: "33333333333333333333333333333333", State: config.ReleaseStateSucceeded,
		Stage: config.ReleaseStageCommitted, ProductionDigest: workspace.ProductionDigest,
		DraftDigest: workspace.DraftDigest, CandidateDigest: check.CandidateDigest,
		CreatedBy: 1, RequestID: "request", CreatedAt: now, UpdatedAt: now, FinishedAt: now,
	}
	api := &releaseAPIStub{check: check, release: release, stages: []config.ReleaseStage{{
		ReleaseID: release.ID, Sequence: 1, Stage: config.ReleaseStageQueued, Result: config.StageResultPending, PublicDetailsJSON: "{}", OccurredAt: now,
	}, {
		ReleaseID: release.ID, Sequence: 2, Stage: config.ReleaseStageCommitted, Result: config.StageResultSuccess, PublicDetailsJSON: "{}", OccurredAt: now,
	}}, ran: make(chan config.ReleaseID, 1)}

	checkPath := "/api/v1/config/workspaces/" + string(workspace.ID) + "/publish-checks"
	recorder := serveReleaseMutation(t, http.MethodPost, checkPath, `{}`, workspace.ETag(), api)
	if recorder.Code != http.StatusCreated || api.checkInput.WorkspaceID != workspace.ID || api.checkInput.IfMatch != workspace.ETag() || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("check status/input/cache = %d/%+v/%q; body = %s", recorder.Code, api.checkInput, recorder.Header().Get("Cache-Control"), recorder.Body.String())
	}
	releasePath := "/api/v1/config/workspaces/" + string(workspace.ID) + "/releases"
	recorder = serveReleaseMutation(t, http.MethodPost, releasePath, `{"check_id":"`+string(check.ID)+`","confirm_name":"review"}`, workspace.ETag(), api)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Location") != "/api/v1/config/releases/"+string(release.ID) {
		t.Fatalf("release status/location = %d/%q; body = %s", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String())
	}
	select {
	case ran := <-api.ran:
		if ran != release.ID {
			t.Fatalf("run id = %s", ran)
		}
	case <-time.After(time.Second):
		t.Fatal("queued release was not started")
	}
	recorder = serveReleaseGET(t, "/api/v1/config/releases/"+string(release.ID), api, "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"state":"succeeded"`) {
		t.Fatalf("release GET status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	recorder = serveReleaseGET(t, "/api/v1/config/releases/"+string(release.ID)+"/events", api, "1")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(recorder.Body.String(), "id: 2\nevent: terminal\n") {
		t.Fatalf("SSE status/headers/body = %d/%v/%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestReleaseHTTPReturnsPersistedInvalidCheckDiagnostics(t *testing.T) {
	workspace := configWorkspaceFixture()
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	api := &releaseAPIStub{check: config.PublishCheck{
		ID: "11111111111111111111111111111111", WorkspaceID: workspace.ID, WorkspaceRevision: workspace.Revision,
		ProductionDigest: workspace.ProductionDigest, BaseDigest: workspace.BaseDigest, DraftDigest: workspace.DraftDigest,
		ManifestVersion: 1, PolicyVersion: 1, ValidatorVersion: 1, ValidatorBuildID: "build-id",
		State: config.PublishCheckStateInvalid, DiagnosticCount: 1,
		PublicDetailsJSON: `{"diagnostics":[{"code":"syntax_error","path":"conf.d/site.conf","line":4,"summary":"配置语法无效"}]}`,
		StartedAt:         now, FinishedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}, checkErr: config.ErrCandidateInvalid}

	recorder := serveReleaseMutation(t, http.MethodPost,
		"/api/v1/config/workspaces/"+string(workspace.ID)+"/publish-checks", `{}`, workspace.ETag(), api)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"state":"invalid"`) ||
		!strings.Contains(recorder.Body.String(), `"path":"conf.d/site.conf"`) {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestReleaseHTTPReturnsSpecificNotFoundErrors(t *testing.T) {
	api := &releaseAPIStub{getCheckErr: fs.ErrNotExist, getReleaseErr: fs.ErrNotExist}

	recorder := serveReleaseGET(t, "/api/v1/config/publish-checks/11111111111111111111111111111111", api, "")
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"CONFIG_PUBLISH_CHECK_NOT_FOUND"`) {
		t.Fatalf("check status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	recorder = serveReleaseGET(t, "/api/v1/config/releases/22222222222222222222222222222222", api, "")
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"CONFIG_RELEASE_NOT_FOUND"`) {
		t.Fatalf("release status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	recorder = serveReleaseGET(t, "/api/v1/config/releases/22222222222222222222222222222222/events", api, "")
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"CONFIG_RELEASE_NOT_FOUND"`) {
		t.Fatalf("events status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestReleaseHTTPQueueRequiresApplicationOwnedTaskRunner(t *testing.T) {
	workspace := configWorkspaceFixture()
	checkID := config.PublishCheckID("11111111111111111111111111111111")
	api := &releaseAPIStub{release: config.Release{
		ID: "22222222222222222222222222222222", WorkspaceID: workspace.ID, CheckID: checkID,
		State: config.ReleaseStateQueued, Stage: config.ReleaseStageQueued,
	}}
	issued := testIssuedSession()
	request := httptest.NewRequest(
		http.MethodPost,
		"http://example.test/api/v1/config/workspaces/"+string(workspace.ID)+"/releases",
		strings.NewReader(`{"check_id":"`+string(checkID)+`","confirm_name":"review"}`),
	)
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set(csrfHeaderName, issued.CSRFToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", workspace.ETag())
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{
		Sessions: &authorizationSessionStub{issued: issued}, Releases: api,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
	}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"service_unavailable"`) || api.queueInput.WorkspaceID != "" {
		t.Fatalf("status/body/queue input = %d/%s/%+v", recorder.Code, recorder.Body.String(), api.queueInput)
	}
}

func TestReleaseHTTPSSERefreshesTerminalProjectionBeforeClassifyingNewStage(t *testing.T) {
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	running := config.Release{
		ID: "22222222222222222222222222222222", WorkspaceID: "11111111111111111111111111111111",
		CheckID: "33333333333333333333333333333333", BackupID: "44444444444444444444444444444444",
		State: config.ReleaseStateRunning, Stage: config.ReleaseStageRuntimeConfirmed,
		ProductionDigest: config.Digest{1}, DraftDigest: config.Digest{2}, CandidateDigest: config.Digest{3},
		CreatedBy: 1, RequestID: "request", CreatedAt: now, UpdatedAt: now,
	}
	terminal := running
	terminal.State = config.ReleaseStateSucceeded
	terminal.Stage = config.ReleaseStageCommitted
	terminal.FinishedAt = now.Add(time.Second)
	terminal.UpdatedAt = terminal.FinishedAt
	api := &releaseAPIStub{
		release:         terminal,
		releaseSequence: []config.Release{running, terminal},
		stages: []config.ReleaseStage{{
			ReleaseID: terminal.ID, Sequence: 2, Stage: config.ReleaseStageCommitted,
			Result: config.StageResultSuccess, PublicDetailsJSON: "{}", OccurredAt: terminal.FinishedAt,
		}},
	}

	recorder := serveReleaseGET(t, "/api/v1/config/releases/"+string(terminal.ID)+"/events", api, "1")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "id: 2\nevent: terminal\n") ||
		strings.Contains(recorder.Body.String(), "id: 2\nevent: stage\n") {
		t.Fatalf("SSE status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestReleaseHTTPSSERejectsOutOfRangeLastEventIDBeforeStartingStream(t *testing.T) {
	api := &releaseAPIStub{release: config.Release{
		ID: "22222222222222222222222222222222", State: config.ReleaseStateRunning,
	}}
	recorder := serveReleaseGET(t, "/api/v1/config/releases/22222222222222222222222222222222/events", api, "513")
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Content-Type") == "text/event-stream" ||
		!strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("status/headers/body = %d/%v/%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

type releaseAPIStub struct {
	check           config.PublishCheck
	release         config.Release
	stages          []config.ReleaseStage
	checkInput      config.PublishCheckInput
	queueInput      config.QueueReleaseInput
	ran             chan config.ReleaseID
	checkErr        error
	getCheckErr     error
	getReleaseErr   error
	releaseSequence []config.Release
	releaseCalls    int
}

func (s *releaseAPIStub) Check(_ context.Context, _ config.Actor, input config.PublishCheckInput) (config.PublishCheck, error) {
	s.checkInput = input
	return s.check, s.checkErr
}
func (s *releaseAPIStub) Queue(_ context.Context, _ config.Actor, input config.QueueReleaseInput) (config.Release, error) {
	s.queueInput = input
	return s.release, nil
}
func (s *releaseAPIStub) Start(id config.ReleaseID) bool {
	if s.ran != nil {
		s.ran <- id
	}
	return true
}
func (s *releaseAPIStub) PublishCheck(_ context.Context, _ config.PublishCheckID) (config.PublishCheck, error) {
	return s.check, s.getCheckErr
}
func (s *releaseAPIStub) Release(_ context.Context, _ config.ReleaseID) (config.Release, error) {
	if len(s.releaseSequence) != 0 {
		index := min(s.releaseCalls, len(s.releaseSequence)-1)
		s.releaseCalls++
		return s.releaseSequence[index], s.getReleaseErr
	}
	return s.release, s.getReleaseErr
}
func (s *releaseAPIStub) Stages(_ context.Context, _ config.ReleaseID, after uint64) ([]config.ReleaseStage, error) {
	result := make([]config.ReleaseStage, 0)
	for _, stage := range s.stages {
		if stage.Sequence > after {
			result = append(result, stage)
		}
	}
	return result, nil
}

func serveReleaseMutation(t *testing.T, method, target, body, etag string, releases ReleaseAPI) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	request := httptest.NewRequest(method, "http://example.test"+target, strings.NewReader(body))
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set(csrfHeaderName, issued.CSRFToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", etag)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	tasks, _ := releases.(ReleaseTaskStarter)
	NewHandler(Dependencies{Sessions: &authorizationSessionStub{issued: issued}, Releases: releases, ReleaseTasks: tasks, RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64))}).ServeHTTP(recorder, request)
	return recorder
}

func serveReleaseGET(t *testing.T, target string, releases ReleaseAPI, lastEventID string) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{Sessions: &authorizationSessionStub{issued: issued}, Releases: releases, RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64))}).ServeHTTP(recorder, request)
	return recorder
}
