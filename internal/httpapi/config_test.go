/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestConfigWorkspaceCreateReturnsStrongETagAndNoStore(t *testing.T) {
	workspace := configWorkspaceFixture()
	api := &workspaceAPIStub{created: workspace}
	recorder := serveConfigMutation(t, http.MethodPost, "/api/v1/config/workspaces", `{"name":"review"}`, "", api, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("ETag"); got != workspace.ETag() {
		t.Fatalf("ETag = %q, want %q", got, workspace.ETag())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if api.createName != "review" || api.actor.UserID != testIssuedSession().User.ID || api.actor.RequestID == "" {
		t.Fatalf("create input = %q, actor = %#v", api.createName, api.actor)
	}
	var response workspaceResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != string(workspace.ID) || response.DraftETag != workspace.ETag() || response.ProductionDigest != workspace.ProductionDigest.String() {
		t.Fatalf("response = %#v", response)
	}
}

func TestConfigSystemWorkspacesAreHiddenAndCannotBeCreated(t *testing.T) {
	workspace := configWorkspaceFixture()
	system := workspace
	system.ID = "fedcba9876543210fedcba9876543210"
	system.Name = "ACME HTTP challenge deadbeef"
	api := &workspaceAPIStub{workspace: system, listed: []config.Workspace{workspace, system}}

	list := serveConfigGET(t, "/api/v1/config/workspaces", api, nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), string(system.ID)) {
		t.Fatalf("system workspace leaked from list: status=%d body=%s", list.Code, list.Body.String())
	}
	detail := serveConfigGET(t, "/api/v1/config/workspaces/"+string(system.ID), api, nil)
	if detail.Code != http.StatusNotFound || !strings.Contains(detail.Body.String(), `"code":"CONFIG_WORKSPACE_NOT_FOUND"`) {
		t.Fatalf("system workspace detail status/body = %d/%s", detail.Code, detail.Body.String())
	}
	create := serveConfigMutation(
		t, http.MethodPost, "/api/v1/config/workspaces", `{"name":"Certificate deploy deadbeef"}`, "", api, nil,
	)
	if create.Code != http.StatusUnprocessableEntity || api.createCalls != 0 {
		t.Fatalf("system workspace create status/calls = %d/%d; body=%s", create.Code, api.createCalls, create.Body.String())
	}
}

func TestConfigWorkspaceRequestLimitMapsStable413(t *testing.T) {
	api := &workspaceAPIStub{}
	recorder := serveConfigMutation(t, http.MethodPost, "/api/v1/config/workspaces", strings.Repeat("x", int(configSmallBodyLimit+1)), "", api, nil)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "CONFIG_LIMIT_EXCEEDED" || envelope.Error.Details["limit_name"] != "request_body_bytes" {
		t.Fatalf("error = %#v", envelope.Error)
	}
	if api.createCalls != 0 {
		t.Fatal("oversized request reached service")
	}
}

func TestConfigFileReadUsesExactlyOnePathQuery(t *testing.T) {
	workspace := configWorkspaceFixture()
	api := &workspaceAPIStub{workspace: workspace, file: config.FileView{
		Entry:   config.Entry{Path: "conf.d/site.conf", Type: config.EntryRegular, Class: config.EntryManagedText, Size: 10, ContentDigest: workspace.DraftDigest},
		Content: "server {}\n", LineEnding: "lf", WorkspaceETag: workspace.ETag(),
	}}
	recorder := serveConfigGET(t, "/api/v1/config/workspaces/"+string(workspace.ID)+"/files?path=conf.d%2Fsite.conf", api, nil)
	if recorder.Code != http.StatusOK || api.readPath != "conf.d/site.conf" {
		t.Fatalf("status/path = %d/%q; body = %s", recorder.Code, api.readPath, recorder.Body.String())
	}
	for _, rawQuery := range []string{"unknown=value", "path=a&path=b", "path="} {
		recorder = serveConfigGET(t, "/api/v1/config/workspaces/"+string(workspace.ID)+"/files?"+rawQuery, api, nil)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q status = %d, want 400", rawQuery, recorder.Code)
		}
	}
}

func TestConfigFileMutationForwardsValidatedIfMatch(t *testing.T) {
	workspace := configWorkspaceFixture()
	api := &workspaceAPIStub{workspace: workspace, mutation: config.MutationResult{Workspace: workspace}}
	path := "/api/v1/config/workspaces/" + string(workspace.ID) + "/files?path=conf.d%2Fsite.conf"
	recorder := serveConfigMutation(t, http.MethodPut, path, `{"content":"server {}\n"}`, workspace.ETag(), api, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if api.replaceInput.Path != "conf.d/site.conf" || api.replaceInput.IfMatch != workspace.ETag() || string(api.replaceInput.Content) != "server {}\n" {
		t.Fatalf("replace input = %#v", api.replaceInput)
	}
	if recorder.Header().Get("ETag") != workspace.ETag() {
		t.Fatalf("ETag = %q", recorder.Header().Get("ETag"))
	}
}

func TestConfigMutationRejectsMissingWeakAndMultipleIfMatchWithoutWriting(t *testing.T) {
	workspace := configWorkspaceFixture()
	for _, etag := range []string{"", `W/"draft-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"draft-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "other"`} {
		api := &workspaceAPIStub{workspace: workspace}
		path := "/api/v1/config/workspaces/" + string(workspace.ID) + "/files?path=conf.d%2Fsite.conf"
		recorder := serveConfigMutation(t, http.MethodPut, path, `{"content":"server {}\n"}`, etag, api, nil)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("If-Match %q status = %d, want 409; body = %s", etag, recorder.Code, recorder.Body.String())
		}
		if api.replaceCalls != 0 {
			t.Fatalf("If-Match %q reached mutation service", etag)
		}
	}
}

func TestConfigRoutesAuthenticateBeforeDependencyOrInputDisclosure(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/config/workspaces"},
		{http.MethodPost, "/api/v1/config/workspaces"},
		{http.MethodGet, "/api/v1/config/workspaces/not-an-id"},
		{http.MethodDelete, "/api/v1/config/workspaces/not-an-id"},
		{http.MethodGet, "/api/v1/config/workspaces/not-an-id/files?unknown=x"},
		{http.MethodPost, "/api/v1/config/workspaces/not-an-id/files"},
		{http.MethodPost, "/api/v1/config/workspaces/not-an-id/files/copies"},
		{http.MethodGet, "/api/v1/config/workspaces/not-an-id/files/search"},
		{http.MethodGet, "/api/v1/config/workspaces/not-an-id/diff"},
		{http.MethodGet, "/api/v1/config/groups?unknown=x"},
		{http.MethodPost, "/api/v1/config/groups"},
		{http.MethodPut, "/api/v1/config/groups/not-an-id"},
	}
	handler := NewHandler(Dependencies{RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 1024))})
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", test.method, test.path, recorder.Code)
		}
	}
}

func TestConfigCreateFileAllowsEmptyManagedText(t *testing.T) {
	workspace := configWorkspaceFixture()
	api := &workspaceAPIStub{workspace: workspace, mutation: config.MutationResult{Workspace: workspace}}
	path := "/api/v1/config/workspaces/" + string(workspace.ID) + "/files"
	recorder := serveConfigMutation(t, http.MethodPost, path, `{"path":"conf.d/empty.conf","content":""}`, workspace.ETag(), api, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestConfigApprovedMethodSurfaceReturnsExpectedSuccessStatuses(t *testing.T) {
	workspace := configWorkspaceFixture()
	workspaceAPI := &workspaceAPIStub{
		created: workspace, workspace: workspace, listed: []config.Workspace{workspace},
		tree:     config.TreeView{Entries: make([]config.Entry, 0), Dependencies: make([]config.Dependency, 0), WorkspaceETag: workspace.ETag()},
		mutation: config.MutationResult{Workspace: workspace},
		diff:     config.DiffResult{Files: make([]config.FileDiffSummary, 0), Complete: true},
		search:   config.SearchResult{Matches: make([]config.SearchMatch, 0), Complete: true},
	}
	groups := &groupAPIStub{view: groupCollectionFixture()}
	id := string(workspace.ID)
	groupID := "0123456789abcdef0123456789abcdef"
	tests := []struct {
		method string
		path   string
		body   string
		etag   string
		status int
	}{
		{http.MethodPost, "/api/v1/config/workspaces", `{"name":"review"}`, "", http.StatusCreated},
		{http.MethodDelete, "/api/v1/config/workspaces/" + id, `{"confirm_name":"review"}`, workspace.ETag(), http.StatusNoContent},
		{http.MethodPost, "/api/v1/config/workspaces/" + id + "/files", `{"path":"conf.d/new.conf","content":"server {}\n"}`, workspace.ETag(), http.StatusCreated},
		{http.MethodPut, "/api/v1/config/workspaces/" + id + "/files?path=conf.d%2Fsite.conf", `{"content":"server {}\n"}`, workspace.ETag(), http.StatusOK},
		{http.MethodPatch, "/api/v1/config/workspaces/" + id + "/files?path=conf.d%2Fsite.conf", `{"destination_path":"conf.d/new.conf"}`, workspace.ETag(), http.StatusOK},
		{http.MethodDelete, "/api/v1/config/workspaces/" + id + "/files?path=conf.d%2Fsite.conf", `{"confirm_path":"conf.d/site.conf"}`, workspace.ETag(), http.StatusOK},
		{http.MethodPost, "/api/v1/config/workspaces/" + id + "/files/copies", `{"source_path":"conf.d/site.conf","destination_path":"conf.d/copy.conf"}`, workspace.ETag(), http.StatusCreated},
		{http.MethodPost, "/api/v1/config/groups", `{"name":"sites","sort_order":1,"members":[]}`, groups.view.ETag, http.StatusCreated},
		{http.MethodPut, "/api/v1/config/groups/" + groupID, `{"name":"sites","sort_order":1,"members":[]}`, groups.view.ETag, http.StatusOK},
		{http.MethodDelete, "/api/v1/config/groups/" + groupID, `{"confirm_name":"sites"}`, groups.view.ETag, http.StatusOK},
	}
	for _, test := range tests {
		recorder := serveConfigMutation(t, test.method, test.path, test.body, test.etag, workspaceAPI, groups)
		if recorder.Code != test.status {
			t.Errorf("%s %s status = %d, want %d; body = %s", test.method, test.path, recorder.Code, test.status, recorder.Body.String())
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s missing no-store", test.method, test.path)
		}
	}
	for _, target := range []string{
		"/api/v1/config/workspaces", "/api/v1/config/workspaces/" + id,
		"/api/v1/config/workspaces/" + id + "/files",
		"/api/v1/config/workspaces/" + id + "/files/search?query=server",
		"/api/v1/config/workspaces/" + id + "/diff",
		"/api/v1/config/groups",
	} {
		recorder := serveConfigGET(t, target, workspaceAPI, groups)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s status/cache = %d/%q; body = %s", target, recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.String())
		}
	}
}

func TestConfigRequestLoggingExcludesQueryBodyAndContent(t *testing.T) {
	workspace := configWorkspaceFixture()
	api := &workspaceAPIStub{workspace: workspace, mutation: config.MutationResult{Workspace: workspace}}
	issued := testIssuedSession()
	sessions := &authorizationSessionStub{issued: issued}
	var logs bytes.Buffer
	handler := NewHandler(Dependencies{
		Sessions: sessions, Workspaces: api,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
		Logger:          slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	secret := "private-config-content"
	target := "/api/v1/config/workspaces/" + string(workspace.ID) + "/files?path=conf.d%2Fsecret.conf"
	request := httptest.NewRequest(http.MethodPut, "http://example.test"+target, strings.NewReader(`{"content":"`+secret+`"}`))
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set(csrfHeaderName, issued.CSRFToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", workspace.ETag())
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{secret, "secret.conf", "path=", "content", "patch", "snippet"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("logs expose %q: %s", forbidden, logs.String())
		}
	}
}

func TestConfigWorkspaceNamedDeleteAllowsReadOnlyLifecycleStates(t *testing.T) {
	for _, state := range []config.WorkspaceState{config.StateStale, config.StateNeedsAttention} {
		workspace := configWorkspaceFixture()
		workspace.State = state
		api := &workspaceAPIStub{workspace: workspace}
		target := "/api/v1/config/workspaces/" + string(workspace.ID)
		recorder := serveConfigMutation(t, http.MethodDelete, target, `{"confirm_name":"review"}`, workspace.ETag(), api, nil)
		if recorder.Code != http.StatusNoContent || api.deleteCalls != 1 {
			t.Errorf("state %s status/delete calls = %d/%d, want 204/1; body = %s", state, recorder.Code, api.deleteCalls, recorder.Body.String())
		}
	}
}

func TestConfigTreeNodesIncludeBoundedDependencySummary(t *testing.T) {
	workspace := configWorkspaceFixture()
	api := &workspaceAPIStub{tree: config.TreeView{
		Entries: []config.Entry{{Path: "nginx.conf", Type: config.EntryRegular, Class: config.EntryManagedText, Size: 10, ContentDigest: workspace.DraftDigest}},
		Dependencies: []config.Dependency{
			{Source: "nginx.conf", Target: "conf.d/a.conf", Status: config.DependencyResolved},
			{Source: "nginx.conf", Target: "conf.d/b.conf", Status: config.DependencyCycle, Cycle: true},
		},
		WorkspaceETag: workspace.ETag(),
	}}
	target := "/api/v1/config/workspaces/" + string(workspace.ID) + "/files"
	recorder := serveConfigGET(t, target, api, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Entries []struct {
			DependencyStatus      string `json:"dependency_status"`
			DependencyTargetCount int    `json:"dependency_target_count"`
			DependencyCycle       bool   `json:"dependency_cycle"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 1 || response.Entries[0].DependencyStatus != "cycle" ||
		response.Entries[0].DependencyTargetCount != 2 || !response.Entries[0].DependencyCycle {
		t.Fatalf("dependency summary = %#v", response.Entries)
	}
}

func TestConfigTreeNodesExposeOnlySafeManagedDiffStatus(t *testing.T) {
	workspace := configWorkspaceFixture()
	api := &workspaceAPIStub{tree: config.TreeView{
		Entries: []config.Entry{
			{Path: "nginx.conf", Type: config.EntryRegular, Class: config.EntryManagedText, Size: 10, ContentDigest: workspace.DraftDigest},
			{Path: "private.key", Type: config.EntryRegular, Class: config.EntrySensitiveMaterial, Size: 10},
		},
		DiffStatuses:  map[config.RelativePath]string{"nginx.conf": "modified"},
		WorkspaceETag: workspace.ETag(),
	}}
	target := "/api/v1/config/workspaces/" + string(workspace.ID) + "/files"
	recorder := serveConfigGET(t, target, api, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if got := response.Entries[0]["diff_status"]; got != "modified" {
		t.Fatalf("managed diff_status = %#v, want modified", got)
	}
	if _, exists := response.Entries[1]["diff_status"]; exists {
		t.Fatalf("sensitive node exposes diff_status: %#v", response.Entries[1])
	}
}

func serveConfigMutation(t *testing.T, method, target, body, etag string, workspaces WorkspaceAPI, groups GroupAPI) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	sessions := &authorizationSessionStub{issued: issued}
	request := httptest.NewRequest(method, "http://example.test"+target, strings.NewReader(body))
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set(csrfHeaderName, issued.CSRFToken)
	request.Header.Set("Content-Type", "application/json")
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{Sessions: sessions, Workspaces: workspaces, Groups: groups, RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64))}).ServeHTTP(recorder, request)
	return recorder
}

func serveConfigGET(t *testing.T, target string, workspaces WorkspaceAPI, groups GroupAPI) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	sessions := &authorizationSessionStub{issued: issued}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{Sessions: sessions, Workspaces: workspaces, Groups: groups, RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64))}).ServeHTTP(recorder, request)
	return recorder
}

func configWorkspaceFixture() config.Workspace {
	now := time.Date(2026, time.July, 16, 9, 0, 0, 0, time.UTC)
	var digest config.Digest
	for index := range digest {
		digest[index] = 0xaa
	}
	return config.Workspace{
		ID: "0123456789abcdef0123456789abcdef", Name: "review", State: config.StateReady,
		ProductionDigest: digest, BaseDigest: digest, DraftDigest: digest, EntryCount: 1,
		ManagedBytes: 10, WorkspaceBytes: 100, Revision: 1, CreatedBy: 7, CreatedAt: now, UpdatedAt: now,
	}
}

type workspaceAPIStub struct {
	created      config.Workspace
	workspace    config.Workspace
	listed       []config.Workspace
	tree         config.TreeView
	file         config.FileView
	mutation     config.MutationResult
	diff         config.DiffResult
	search       config.SearchResult
	err          error
	actor        config.Actor
	createName   string
	readPath     config.RelativePath
	replaceInput config.ReplaceFileInput
	createCalls  int
	replaceCalls int
	deleteCalls  int
}

func (s *workspaceAPIStub) Create(_ context.Context, actor config.Actor, name string) (config.Workspace, error) {
	s.createCalls++
	s.actor = actor
	s.createName = name
	return s.created, s.err
}
func (s *workspaceAPIStub) List(context.Context) ([]config.Workspace, error) { return s.listed, s.err }
func (s *workspaceAPIStub) Get(context.Context, config.WorkspaceID) (config.Workspace, error) {
	return s.workspace, s.err
}

func (s *workspaceAPIStub) Delete(context.Context, config.Actor, config.WorkspaceID, string, string) error {
	s.deleteCalls++
	return s.err
}
func (s *workspaceAPIStub) Tree(context.Context, config.WorkspaceID) (config.TreeView, error) {
	return s.tree, s.err
}
func (s *workspaceAPIStub) ReadFile(_ context.Context, _ config.WorkspaceID, path config.RelativePath) (config.FileView, error) {
	s.readPath = path
	return s.file, s.err
}
func (s *workspaceAPIStub) CreateFile(context.Context, config.Actor, config.WorkspaceID, config.CreateFileInput) (config.MutationResult, error) {
	return s.mutation, s.err
}
func (s *workspaceAPIStub) ReplaceFile(_ context.Context, _ config.Actor, _ config.WorkspaceID, input config.ReplaceFileInput) (config.MutationResult, error) {
	s.replaceCalls++
	s.replaceInput = input
	return s.mutation, s.err
}
func (s *workspaceAPIStub) CopyFile(context.Context, config.Actor, config.WorkspaceID, config.CopyFileInput) (config.MutationResult, error) {
	return s.mutation, s.err
}
func (s *workspaceAPIStub) RenameFile(context.Context, config.Actor, config.WorkspaceID, config.RenameFileInput) (config.MutationResult, error) {
	return s.mutation, s.err
}
func (s *workspaceAPIStub) DeleteFile(context.Context, config.Actor, config.WorkspaceID, config.DeleteFileInput) (config.MutationResult, error) {
	return s.mutation, s.err
}
func (s *workspaceAPIStub) Diff(context.Context, config.WorkspaceID, *config.RelativePath) (config.DiffResult, error) {
	return s.diff, s.err
}
func (s *workspaceAPIStub) Search(context.Context, config.WorkspaceID, string) (config.SearchResult, error) {
	return s.search, s.err
}

var _ WorkspaceAPI = (*workspaceAPIStub)(nil)
