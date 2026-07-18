/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestAgentProtocolAcceptsOnlyFiveExactGETEndpoints(t *testing.T) {
	checkedAt := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	operations := &recordingAgentOperations{
		status: Status{SampledAt: checkedAt, State: StateRunning, Workers: []NginxProcess{}, Issues: []string{}},
		build: BuildInfo{
			Version: "1.30.3", ConfigureArguments: []string{"--with-http_ssl_module"},
			PIDPath: "/run/nginx.pid", SbinPath: nginxExecutable,
		},
		startup: StartupState{Validation: &StartupValidation{Valid: true, CheckedAt: checkedAt}},
		effective: EffectiveConfig{Occurrences: []ConfigOccurrence{{
			ID: "occurrence-000001", LoadOrder: 1, Path: nginxConfigPath, Content: "events {}\n",
		}}},
	}
	handler := newAgentProtocolHandler(operations, nil)

	tests := []struct {
		name        string
		path        string
		wantCall    string
		wantPayload string
	}{
		{name: "health", path: "/v1/health", wantCall: "health", wantPayload: `"status":"healthy"`},
		{name: "status", path: "/v1/status", wantCall: "status", wantPayload: `"sampled_at":"2026-07-14T12:00:00Z"`},
		{name: "build info", path: "/v1/build-info", wantCall: "build-info", wantPayload: `"configure_arguments":["--with-http_ssl_module"]`},
		{name: "startup validation", path: "/v1/startup-validation", wantCall: "startup-validation", wantPayload: `"checked_at":"2026-07-14T12:00:00Z"`},
		{name: "effective config", path: "/v1/effective-config", wantCall: "effective-config", wantPayload: `"load_order":1`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations.resetCalls()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if got, want := response.Code, http.StatusOK; got != want {
				t.Fatalf("status = %d, want %d; body = %s", got, want, response.Body.String())
			}
			if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
				t.Fatalf("Content-Type = %q, want %q", got, want)
			}
			if got := response.Body.String(); !strings.Contains(got, test.wantPayload) {
				t.Fatalf("body = %q, want payload %q", got, test.wantPayload)
			}
			if got, want := operations.calls, []string{test.wantCall}; !equalStrings(got, want) {
				t.Fatalf("runtime calls = %#v, want %#v", got, want)
			}
		})
	}
}

func TestAgentProtocolConfigSnapshotAcceptsOnlyWorkspaceID(t *testing.T) {
	operations := &recordingAgentOperations{snapshot: testAgentConfigSnapshot(t)}
	handler := newAgentProtocolHandler(operations, nil)
	request := httptest.NewRequest(http.MethodPost, agentProtocolConfigSnapshotPath, strings.NewReader(
		`{"protocol_version":1,"workspace_id":"0123456789abcdef0123456789abcdef"}`,
	))
	request.Header.Set("Content-Type", agentProtocolContentType)
	request.Header.Set("X-Request-ID", "request-snapshot-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if operations.snapshotID != testConfigWorkspaceID {
		t.Fatalf("snapshot id = %q, want %q", operations.snapshotID, testConfigWorkspaceID)
	}
	var response agentConfigSnapshotResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	manifest, err := config.ParseManifest(response.Manifest, config.DefaultLimits())
	if err != nil {
		t.Fatalf("ParseManifest(response) error = %v", err)
	}
	if !response.BaseComplete || response.ManifestVersion != manifest.SchemaVersion || response.EntryCount != manifest.EntryCount || response.ManagedBytes != manifest.ManagedBytes {
		t.Fatalf("snapshot response = %#v, manifest = %#v", response, manifest)
	}
}

func TestAgentProtocolConfigDigestRequiresCorrelatedBodylessGET(t *testing.T) {
	want := config.ProductionState{
		Digest: config.Digest(sha256.Sum256([]byte("production"))), ManifestVersion: config.ManifestSchemaVersion,
		EntryCount: 2, ManagedBytes: 64,
	}
	operations := &recordingAgentOperations{digest: want}
	request := httptest.NewRequest(http.MethodGet, agentProtocolConfigDigestPath, nil)
	request.Header.Set("X-Request-ID", "request-digest-1")
	recorder := httptest.NewRecorder()

	newAgentProtocolHandler(operations, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	var response agentConfigDigestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ProductionDigest != want.Digest.String() || response.ManifestVersion != want.ManifestVersion || response.EntryCount != want.EntryCount || response.ManagedBytes != want.ManagedBytes {
		t.Fatalf("digest response = %#v, want %#v", response, want)
	}
}

func TestAgentProtocolConfigOperationsRejectMalformedBoundariesBeforeRuntimeCall(t *testing.T) {
	validBody := `{"protocol_version":1,"workspace_id":"0123456789abcdef0123456789abcdef"}`
	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		contentType string
		requestIDs  []string
		wantStatus  int
		wantAllow   string
	}{
		{name: "snapshot GET", method: http.MethodGet, target: agentProtocolConfigSnapshotPath, requestIDs: []string{"request-1"}, wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost},
		{name: "digest POST", method: http.MethodPost, target: agentProtocolConfigDigestPath, requestIDs: []string{"request-1"}, wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodGet},
		{name: "missing version", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: `{"workspace_id":"0123456789abcdef0123456789abcdef"}`, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "wrong version", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: `{"protocol_version":2,"workspace_id":"0123456789abcdef0123456789abcdef"}`, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "missing workspace", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: `{"protocol_version":1}`, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "duplicate field", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: `{"protocol_version":1,"workspace_id":"0123456789abcdef0123456789abcdef","workspace_id":"0123456789abcdef0123456789abcdef"}`, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: validBody[:len(validBody)-1] + `,"extra":true}`, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "absolute path field", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: validBody[:len(validBody)-1] + `,"nginx_root":"/tmp/attacker"}`, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "trailing JSON", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: validBody + `{}`, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "oversized body", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: strings.Repeat(" ", agentSnapshotRequestLimit+1), contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "missing content type", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: validBody, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "wrong content type", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: validBody, contentType: "text/plain", requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "missing request ID", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: validBody, contentType: agentProtocolContentType, wantStatus: http.StatusBadRequest},
		{name: "invalid request ID", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: validBody, contentType: agentProtocolContentType, requestIDs: []string{"request id"}, wantStatus: http.StatusBadRequest},
		{name: "multiple request ID", method: http.MethodPost, target: agentProtocolConfigSnapshotPath, body: validBody, contentType: agentProtocolContentType, requestIDs: []string{"request-1", "request-2"}, wantStatus: http.StatusBadRequest},
		{name: "snapshot query", method: http.MethodPost, target: agentProtocolConfigSnapshotPath + "?root=/tmp", body: validBody, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "snapshot force query", method: http.MethodPost, target: agentProtocolConfigSnapshotPath + "?", body: validBody, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "snapshot suffix", method: http.MethodPost, target: agentProtocolConfigSnapshotPath + "/etc/nginx", body: validBody, contentType: agentProtocolContentType, requestIDs: []string{"request-1"}, wantStatus: http.StatusNotFound},
		{name: "digest body", method: http.MethodGet, target: agentProtocolConfigDigestPath, body: `{}`, requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "digest query", method: http.MethodGet, target: agentProtocolConfigDigestPath + "?root=/tmp", requestIDs: []string{"request-1"}, wantStatus: http.StatusBadRequest},
		{name: "digest missing request ID", method: http.MethodGet, target: agentProtocolConfigDigestPath, wantStatus: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &recordingAgentOperations{}
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.requestIDs != nil {
				request.Header["X-Request-Id"] = test.requestIDs
			}
			recorder := httptest.NewRecorder()
			newAgentProtocolHandler(operations, nil).ServeHTTP(recorder, request)

			wantCode := map[int]string{
				http.StatusBadRequest:       agentErrorCodeInvalidRequest,
				http.StatusMethodNotAllowed: agentErrorCodeMethodNotAllowed,
				http.StatusNotFound:         agentErrorCodeNotFound,
			}[test.wantStatus]
			assertAgentProtocolError(t, recorder, test.wantStatus, wantCode)
			if test.wantAllow != "" && recorder.Header().Get("Allow") != test.wantAllow {
				t.Fatalf("Allow = %q, want %q", recorder.Header().Get("Allow"), test.wantAllow)
			}
			if len(operations.calls) != 0 {
				t.Fatalf("runtime calls = %#v, want none", operations.calls)
			}
		})
	}
}

func TestAgentProtocolMapsConfigErrorsToStableSanitizedCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "path", err: errors.Join(errors.New("secret /etc/nginx"), config.ErrPathInvalid), wantStatus: http.StatusBadRequest, wantCode: agentErrorCodeConfigPathInvalid},
		{name: "limit", err: errors.Join(errors.New("secret content"), config.ErrLimitExceeded), wantStatus: http.StatusRequestEntityTooLarge, wantCode: agentErrorCodeConfigLimitExceeded},
		{name: "changed", err: errors.Join(errors.New("secret content"), config.ErrSnapshotChanged), wantStatus: http.StatusConflict, wantCode: agentErrorCodeConfigSnapshotChanged},
		{name: "timeout", err: context.DeadlineExceeded, wantStatus: http.StatusGatewayTimeout, wantCode: agentErrorCodeConfigOperationTimeout},
		{name: "canceled", err: context.Canceled, wantStatus: http.StatusGatewayTimeout, wantCode: agentErrorCodeConfigOperationTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &recordingAgentOperations{snapshotErr: test.err}
			request := httptest.NewRequest(http.MethodPost, agentProtocolConfigSnapshotPath, strings.NewReader(
				`{"protocol_version":1,"workspace_id":"0123456789abcdef0123456789abcdef"}`,
			))
			request.Header.Set("Content-Type", agentProtocolContentType)
			request.Header.Set("X-Request-ID", "request-1")
			recorder := httptest.NewRecorder()
			newAgentProtocolHandler(operations, nil).ServeHTTP(recorder, request)
			assertAgentProtocolError(t, recorder, test.wantStatus, test.wantCode)
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/etc/nginx") {
				t.Fatalf("error body leaked diagnostics: %s", recorder.Body.String())
			}
		})
	}
}

func TestAgentProtocolConfigOperationCarriesDeadlineAndLogsOnlyRequestID(t *testing.T) {
	var output bytes.Buffer
	operations := &recordingAgentOperations{snapshotCall: func(ctx context.Context, id config.WorkspaceID) (config.Snapshot, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > agentSnapshotTimeout {
			t.Fatalf("snapshot deadline = %v, %v", deadline, ok)
		}
		return testAgentConfigSnapshot(t), nil
	}}
	request := httptest.NewRequest(http.MethodPost, agentProtocolConfigSnapshotPath, strings.NewReader(
		`{"protocol_version":1,"workspace_id":"0123456789abcdef0123456789abcdef"}`,
	))
	request.Header.Set("Content-Type", agentProtocolContentType)
	request.Header.Set("X-Request-ID", "request-snapshot-log")
	recorder := httptest.NewRecorder()
	newAgentProtocolHandler(operations, slog.New(slog.NewJSONHandler(&output, nil))).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	logOutput := output.String()
	if !strings.Contains(logOutput, `"request_id":"request-snapshot-log"`) {
		t.Fatalf("log missing request ID: %s", logOutput)
	}
	for _, forbidden := range []string{"workspace_id", string(testConfigWorkspaceID), "/v1/config", "protocol_version"} {
		if strings.Contains(logOutput, forbidden) {
			t.Fatalf("log contains forbidden value %q: %s", forbidden, logOutput)
		}
	}
}

func TestAgentProtocolRejectsRequestsOutsideTheFixedSurfaceBeforeRuntimeCall(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "alternate method", method: http.MethodPost, target: "/v1/status", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "query", method: http.MethodGet, target: "/v1/status?details=true", wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "empty force query", method: http.MethodGet, target: "/v1/status?", wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "body", method: http.MethodGet, target: "/v1/status", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "unknown route", method: http.MethodGet, target: "/v1/unknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &recordingAgentOperations{}
			handler := newAgentProtocolHandler(operations, nil)
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assertAgentProtocolError(t, response, test.wantStatus, test.wantCode)
			if len(operations.calls) != 0 {
				t.Fatalf("runtime calls = %#v, want none", operations.calls)
			}
		})
	}
}

func TestAgentProtocolRejectsEncodedResponseOverLimit(t *testing.T) {
	operations := &recordingAgentOperations{effective: EffectiveConfig{Occurrences: []ConfigOccurrence{{
		ID: "occurrence-000001", LoadOrder: 1, Path: nginxConfigPath,
		Content: strings.Repeat("x", agentProtocolResponseLimit),
	}}}}
	handler := newAgentProtocolHandler(operations, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/effective-config", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertAgentProtocolError(t, response, http.StatusInternalServerError, "response_too_large")
	if strings.Contains(response.Body.String(), strings.Repeat("x", 64)) {
		t.Fatal("oversized response content leaked into error body")
	}
}

func TestAgentProtocolMapsRuntimeErrorsToStableSanitizedCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid config", err: fmt.Errorf("secret config body: %w", ErrConfigInvalid), wantStatus: http.StatusUnprocessableEntity, wantCode: "nginx_config_invalid"},
		{name: "timeout", err: fmt.Errorf("secret command output: %w", ErrCommandTimeout), wantStatus: http.StatusGatewayTimeout, wantCode: "nginx_command_timeout"},
		{name: "output too large", err: fmt.Errorf("secret command output: %w", ErrOutputTooLarge), wantStatus: http.StatusBadGateway, wantCode: "nginx_output_too_large"},
		{name: "internal", err: errors.New("secret /etc/nginx/private.key"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &recordingAgentOperations{statusErr: test.err}
			handler := newAgentProtocolHandler(operations, nil)
			request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assertAgentProtocolError(t, response, test.wantStatus, test.wantCode)
			body := response.Body.String()
			for _, sensitive := range []string{"secret", "/etc/nginx", "private.key"} {
				if strings.Contains(body, sensitive) {
					t.Fatalf("error body %q contains sensitive value %q", body, sensitive)
				}
			}
		})
	}
}

func assertAgentProtocolError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if got := response.Code; got != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", got, wantStatus, response.Body.String())
	}
	if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	var envelope agentErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("Unmarshal(error) error = %v; body = %s", err, response.Body.String())
	}
	if got := envelope.Error.Code; got != wantCode {
		t.Fatalf("error code = %q, want %q; body = %s", got, wantCode, response.Body.String())
	}
	if envelope.Error.Message == "" {
		t.Fatal("error message is empty")
	}
}

type recordingAgentOperations struct {
	calls        []string
	status       Status
	statusErr    error
	build        BuildInfo
	buildErr     error
	startup      StartupState
	startupErr   error
	effective    EffectiveConfig
	effectiveErr error
	healthErr    error
	snapshot     config.Snapshot
	snapshotErr  error
	snapshotID   config.WorkspaceID
	snapshotCall func(context.Context, config.WorkspaceID) (config.Snapshot, error)
	digest       config.ProductionState
	digestErr    error
}

func (o *recordingAgentOperations) Health(context.Context) error {
	o.calls = append(o.calls, "health")
	return o.healthErr
}

func (o *recordingAgentOperations) Status(context.Context) (Status, error) {
	o.calls = append(o.calls, "status")
	return o.status, o.statusErr
}

func (o *recordingAgentOperations) BuildInfo(context.Context) (BuildInfo, error) {
	o.calls = append(o.calls, "build-info")
	return o.build, o.buildErr
}

func (o *recordingAgentOperations) StartupValidation(context.Context) (StartupState, error) {
	o.calls = append(o.calls, "startup-validation")
	return o.startup, o.startupErr
}

func (o *recordingAgentOperations) EffectiveConfig(context.Context) (EffectiveConfig, error) {
	o.calls = append(o.calls, "effective-config")
	return o.effective, o.effectiveErr
}

func (o *recordingAgentOperations) ConfigSnapshot(ctx context.Context, id config.WorkspaceID) (config.Snapshot, error) {
	o.calls = append(o.calls, "config-snapshot")
	o.snapshotID = id
	if o.snapshotCall != nil {
		return o.snapshotCall(ctx, id)
	}
	return o.snapshot, o.snapshotErr
}

func (o *recordingAgentOperations) ConfigDigest(context.Context) (config.ProductionState, error) {
	o.calls = append(o.calls, "config-digest")
	return o.digest, o.digestErr
}

func (o *recordingAgentOperations) resetCalls() {
	o.calls = nil
}

func equalStrings(left, right []string) bool {
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

func testAgentConfigSnapshot(t *testing.T) config.Snapshot {
	t.Helper()
	content := []byte("events {}\n")
	manifest := config.Manifest{
		SchemaVersion: config.ManifestSchemaVersion,
		PolicyVersion: config.NewPolicy().Version(),
		Entries: []config.Entry{{
			Path: "nginx.conf", Type: config.EntryRegular, Class: config.EntryManagedText,
			Mode: 0o600, Size: int64(len(content)), ContentDigest: config.Digest(sha256.Sum256(content)),
		}},
		Dependencies: []config.Dependency{}, EntryCount: 1, ManagedBytes: int64(len(content)),
	}
	if err := manifest.Validate(config.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	digest := manifest.Digest()
	return config.Snapshot{Manifest: manifest, ProductionDigest: digest, BaseDigest: digest}
}
