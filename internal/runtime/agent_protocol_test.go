/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestAgentProtocolPreservesRawEffectiveConfigFallback(t *testing.T) {
	operations := &recordingAgentOperations{effective: EffectiveConfig{
		DisplayMode: EffectiveConfigDisplayModeRaw,
		Occurrences: []ConfigOccurrence{},
		RawContent:  "# configuration file /etc/nginx/nginx.conf:\nevents {}\n",
		Warnings:    []EffectiveConfigWarning{EffectiveConfigWarningPathOutsideAllowedRoots},
	}}
	response := httptest.NewRecorder()

	newAgentProtocolHandler(operations, nil).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, agentProtocolEffectiveConfigPath, nil),
	)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d; body = %s", got, want, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`"display_mode":"raw"`,
		`"raw_content":"# configuration file /etc/nginx/nginx.conf:\nevents {}\n"`,
		`"warnings":["NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS"]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want %q", body, want)
		}
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
