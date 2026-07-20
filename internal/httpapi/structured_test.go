/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/location"
	"github.com/kuroky/nginx-uix/internal/structuredconfig"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

func TestStructuredCatalogReturnsExplicitNonNullSafeDTO(t *testing.T) {
	t.Parallel()

	workspace := configWorkspaceFixture()
	api := &structuredAPIStub{projection: structuredconfig.Projection{
		WorkspaceID: workspace.ID, DraftETag: workspace.ETag(), Complete: true,
		HTTPBlocks: []structuredconfig.HTTPBlock{{
			ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Source: structuredconfig.SourceLocation{
				Path: "nginx.conf", StartLine: 2, StartColumn: 1, EndLine: 5, EndColumn: 2,
			},
			Editable: true, Instances: 1,
		}},
		Upstreams: upstream.Catalog{
			Upstreams: []upstream.Upstream{{
				ID: "11111111111111111111111111111111", Name: "backend",
				Source: upstream.SourceLocation{
					Path: "conf.d/upstreams.conf", StartLine: 1, StartColumn: 1, EndLine: 3, EndColumn: 2,
				},
				Servers: []upstream.Server{{
					ID:                  "22222222222222222222222222222222",
					Endpoint:            upstream.Endpoint{Address: "127.0.0.1"},
					PreservedParameters: []upstream.PreservedParameter{{Name: "resolve", Raw: "resolve"}},
					Editable:            true,
				}},
				PreservedDirectives: []upstream.PreservedSyntax{{
					ID: "33333333333333333333333333333333", Name: "zone",
				}},
				Editable: true, Instances: 1,
			}},
			References: make([]upstream.Reference, 0), Diagnostics: make([]upstream.Diagnostic, 0),
			ReferenceAnalysisComplete: true,
		},
		Locations: location.Catalog{
			Servers: []location.Server{{
				ID: "44444444444444444444444444444444",
				Source: location.SourceLocation{
					Path: "conf.d/site.conf", StartLine: 1, StartColumn: 1, EndLine: 3, EndColumn: 2,
				},
				Listens: []string{"443 ssl"}, ServerNames: []string{"example.test"},
				Locations: make([]location.Location, 0), Editable: true, Instances: 1,
			}},
			Diagnostics: make([]location.Diagnostic, 0), Complete: true,
		},
	}}

	recorder := serveStructured(t, http.MethodGet,
		"/api/v1/config/workspaces/"+string(workspace.ID)+"/structured-config",
		"", "", api, &workspaceAPIStub{workspace: workspace})
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != workspace.ETag() {
		t.Fatalf("status/etag = %d/%q; body = %s", recorder.Code, recorder.Header().Get("ETag"), recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"http_blocks", "upstreams", "proxy_pass_references", "servers", "diagnostics", "project_diagnostics"} {
		if response[field] == nil {
			t.Fatalf("response field %q is null: %#v", field, response)
		}
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"\"raw\":\"resolve\"", "zone backend", "byte_offset", "/var/lib/nginx-uix"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("catalog exposes %q: %s", forbidden, body)
		}
	}
}

func TestStructuredPreviewDecodesIndependentTaggedOperationWithMutationProtection(t *testing.T) {
	t.Parallel()

	workspace := configWorkspaceFixture()
	api := &structuredAPIStub{preview: structuredconfig.Preview{
		PreviewID: strings.Repeat("1", 64), WorkspaceID: workspace.ID,
		DraftETag: workspace.ETag(), OperationKind: structuredconfig.OperationUpstreamRename,
		TargetID: "11111111111111111111111111111111", ChangedFiles: make([]structuredconfig.ChangedFile, 0),
		Complete: true,
	}}
	body := "{\"kind\":\"upstream.rename\",\"input\":{\"upstream_id\":\"11111111111111111111111111111111\",\"new_name\":\"backend\"}}"
	recorder := serveStructured(t, http.MethodPost,
		"/api/v1/config/workspaces/"+string(workspace.ID)+"/structured-change-previews",
		body, "", api, &workspaceAPIStub{workspace: workspace})
	if recorder.Code != http.StatusOK || api.previewCalls != 1 ||
		api.operation.UpstreamRename == nil || api.operation.UpstreamRename.NewName != "backend" {
		t.Fatalf("status/calls/operation = %d/%d/%#v; body = %s", recorder.Code, api.previewCalls, api.operation, recorder.Body.String())
	}

	for _, invalid := range []string{
		"{\"kind\":\"unknown\",\"input\":{}}",
		"{\"kind\":\"upstream.rename\",\"input\":{\"upstream_id\":\"x\",\"new_name\":\"a\",\"extra\":true}}",
		"{\"kind\":\"upstream.rename\",\"kind\":\"upstream.delete\",\"input\":{}}",
	} {
		recorder = serveStructured(t, http.MethodPost,
			"/api/v1/config/workspaces/"+string(workspace.ID)+"/structured-change-previews",
			invalid, "", api, &workspaceAPIStub{workspace: workspace})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %s status = %d; response = %s", invalid, recorder.Code, recorder.Body.String())
		}
	}
}

func TestStructuredApplyRequiresCurrentStrongETagAndReturnsDraftOnlyResult(t *testing.T) {
	t.Parallel()

	workspace := configWorkspaceFixture()
	next := workspace
	next.Revision++
	api := &structuredAPIStub{applyResult: config.ReplaceFilesResult{
		Workspace: next, ChangedPaths: []config.RelativePath{"sites.conf", "upstreams.conf"},
	}}
	body := "{\"preview_id\":\"" + strings.Repeat("1", 64) +
		"\",\"kind\":\"upstream.rename\",\"input\":{\"upstream_id\":\"11111111111111111111111111111111\",\"new_name\":\"backend\"}}"
	path := "/api/v1/config/workspaces/" + string(workspace.ID) + "/structured-changes"
	recorder := serveStructured(t, http.MethodPost, path, body, "", api, &workspaceAPIStub{workspace: workspace})
	if recorder.Code != http.StatusConflict || api.applyCalls != 0 {
		t.Fatalf("missing If-Match status/calls = %d/%d", recorder.Code, api.applyCalls)
	}

	recorder = serveStructured(t, http.MethodPost, path, body, workspace.ETag(), api, &workspaceAPIStub{workspace: workspace})
	if recorder.Code != http.StatusOK || api.applyCalls != 1 ||
		api.ifMatch != workspace.ETag() || api.actor.UserID != testIssuedSession().User.ID ||
		recorder.Header().Get("ETag") != next.ETag() {
		t.Fatalf("status/calls/etag/input = %d/%d/%q/%#v; body = %s",
			recorder.Code, api.applyCalls, recorder.Header().Get("ETag"), api, recorder.Body.String())
	}
	var response struct {
		DraftETag    string   `json:"draft_etag"`
		ChangedPaths []string `json:"changed_paths"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.DraftETag != next.ETag() ||
		!stringSlicesEqual(response.ChangedPaths, []string{"sites.conf", "upstreams.conf"}) {
		t.Fatalf("response = %#v", response)
	}
	if strings.Contains(recorder.Body.String(), "\"valid\"") || strings.Contains(recorder.Body.String(), "\"published\"") {
		t.Fatalf("apply response overclaims validation or publication: %s", recorder.Body.String())
	}
}

func TestStructuredDomainErrorsMapToStableCodes(t *testing.T) {
	t.Parallel()

	workspace := configWorkspaceFixture()
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{upstream.ErrInvalid, http.StatusUnprocessableEntity, "UPSTREAM_INVALID"},
		{upstream.ErrDuplicate, http.StatusConflict, "UPSTREAM_DUPLICATE"},
		{upstream.ErrReferenced, http.StatusConflict, "UPSTREAM_REFERENCED"},
		{upstream.ErrReferenceIncomplete, http.StatusConflict, "UPSTREAM_REFERENCE_INCOMPLETE"},
		{location.ErrInvalid, http.StatusUnprocessableEntity, "LOCATION_INVALID"},
		{location.ErrDuplicate, http.StatusConflict, "LOCATION_DUPLICATE"},
		{location.ErrProxyPassInvalid, http.StatusUnprocessableEntity, "PROXY_PASS_INVALID"},
		{structuredconfig.ErrPreviewStale, http.StatusConflict, "STRUCTURED_PREVIEW_STALE"},
		{structuredconfig.ErrParseFailed, http.StatusUnprocessableEntity, "STRUCTURED_PARSE_FAILED"},
		{structuredconfig.ErrLimitExceeded, http.StatusRequestEntityTooLarge, "STRUCTURED_LIMIT_EXCEEDED"},
	}
	for _, test := range tests {
		api := &structuredAPIStub{err: test.err}
		body := "{\"kind\":\"upstream.rename\",\"input\":{\"upstream_id\":\"11111111111111111111111111111111\",\"new_name\":\"backend\"}}"
		recorder := serveStructured(t, http.MethodPost,
			"/api/v1/config/workspaces/"+string(workspace.ID)+"/structured-change-previews",
			body, "", api, &workspaceAPIStub{workspace: workspace})
		var envelope ErrorEnvelope
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != test.status || envelope.Error.Code != test.code {
			t.Errorf("error %v status/code = %d/%q, want %d/%q",
				test.err, recorder.Code, envelope.Error.Code, test.status, test.code)
		}
	}
}

type structuredAPIStub struct {
	projection   structuredconfig.Projection
	preview      structuredconfig.Preview
	applyResult  config.ReplaceFilesResult
	err          error
	operation    structuredconfig.Operation
	actor        config.Actor
	ifMatch      string
	previewCalls int
	applyCalls   int
}

func (s *structuredAPIStub) Catalog(
	context.Context,
	config.WorkspaceID,
) (structuredconfig.Projection, error) {
	return s.projection, s.err
}

func (s *structuredAPIStub) Preview(
	_ context.Context,
	_ config.WorkspaceID,
	operation structuredconfig.Operation,
) (structuredconfig.Preview, error) {
	s.previewCalls++
	s.operation = operation
	return s.preview, s.err
}

func (s *structuredAPIStub) Apply(
	_ context.Context,
	actor config.Actor,
	_ config.WorkspaceID,
	operation structuredconfig.Operation,
	_ string,
	ifMatch string,
) (config.ReplaceFilesResult, error) {
	s.applyCalls++
	s.actor = actor
	s.operation = operation
	s.ifMatch = ifMatch
	return s.applyResult, s.err
}

func serveStructured(
	t *testing.T,
	method string,
	target string,
	body string,
	etag string,
	structured StructuredAPI,
	workspaces WorkspaceAPI,
) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	sessions := &authorizationSessionStub{issued: issued}
	request := httptest.NewRequest(method, "http://example.test"+target, strings.NewReader(body))
	request.Host = "example.test"
	if method != http.MethodGet {
		request.Header.Set("Origin", "http://example.test")
		request.Header.Set(csrfHeaderName, issued.CSRFToken)
		request.Header.Set("Content-Type", "application/json")
	}
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{
		Sessions: sessions, Workspaces: workspaces, Structured: structured,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
	}).ServeHTTP(recorder, request)
	return recorder
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var _ StructuredAPI = (*structuredAPIStub)(nil)
