/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

func TestEffectiveConfigRequiresAuthenticationBeforeCallingAgent(t *testing.T) {
	agent := &stubAgent{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nginx/effective-config", nil)
	NewHandler(Dependencies{Sessions: &recordingSessionService{}, Agent: agent}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", recorder.Code, recorder.Body.String())
	}
	if agent.effectiveCalls != 0 || agent.buildCalls != 0 {
		t.Fatalf("Agent effective/build calls = %d/%d, want 0/0", agent.effectiveCalls, agent.buildCalls)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestEffectiveConfigPreservesOrderedRepeatedOccurrencesAndContent(t *testing.T) {
	wantOccurrences := []nginxruntime.ConfigOccurrence{
		{ID: "occurrence-000001", LoadOrder: 1, Path: "/etc/nginx/nginx.conf", Content: "events {}\nhttp {\n  include /etc/nginx/conf.d/*.conf;\n}\n"},
		{ID: "occurrence-000002", LoadOrder: 2, Path: "/etc/nginx/conf.d/site.conf", Content: "server { listen 80; }\n"},
		{ID: "occurrence-000003", LoadOrder: 3, Path: "/etc/nginx/conf.d/site.conf", Content: "server { listen 8080; }\n"},
	}
	agent := &stubAgent{
		effective: nginxruntime.EffectiveConfig{Occurrences: wantOccurrences},
		build:     nginxruntime.BuildInfo{Version: "1.30.3", PIDPath: "/run/private.pid", SbinPath: "/usr/sbin/private"},
	}
	recorder := serveAuthenticatedBusinessGET(t, "/api/v1/nginx/effective-config", agent)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var response decodedEffectiveConfig
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal(config) error = %v; body = %s", err, recorder.Body.String())
	}
	if response.GeneratedAt.IsZero() {
		t.Fatal("generated_at is zero, want RFC3339 timestamp")
	}
	if response.NginxVersion != "1.30.3" || response.EntryConfigPath != "/etc/nginx/nginx.conf" {
		t.Fatalf("version/entry = %q/%q, want 1.30.3 and fixed entry", response.NginxVersion, response.EntryConfigPath)
	}
	if response.OccurrenceCount != len(wantOccurrences) {
		t.Fatalf("occurrence_count = %d, want %d", response.OccurrenceCount, len(wantOccurrences))
	}
	wantDecoded := make([]decodedConfigOccurrence, 0, len(wantOccurrences))
	for _, occurrence := range wantOccurrences {
		wantDecoded = append(wantDecoded, decodedConfigOccurrence(occurrence))
	}
	if !reflect.DeepEqual(response.Occurrences, wantDecoded) {
		t.Fatalf("occurrences = %#v, want ordered repeated %#v", response.Occurrences, wantDecoded)
	}
	for _, forbidden := range []string{"pid_path", "sbin_path", "/run/private.pid", "/usr/sbin/private"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("effective config exposes build-internal field %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestEffectiveConfigMapsErrorsWithoutReturningPartialConfiguration(t *testing.T) {
	partial := nginxruntime.EffectiveConfig{Occurrences: []nginxruntime.ConfigOccurrence{{
		ID: "occurrence-000001", LoadOrder: 1, Path: "/etc/nginx/partial-secret.conf", Content: "private_key secret-value;\n",
	}}}
	tests := []struct {
		name         string
		effectiveErr error
		buildErr     error
		wantStatus   int
		wantCode     string
	}{
		{name: "invalid", effectiveErr: nginxruntime.ErrConfigInvalid, wantStatus: http.StatusUnprocessableEntity, wantCode: "NGINX_CONFIG_INVALID"},
		{name: "timeout", effectiveErr: nginxruntime.ErrCommandTimeout, wantStatus: http.StatusGatewayTimeout, wantCode: "NGINX_COMMAND_TIMEOUT"},
		{name: "too large", effectiveErr: nginxruntime.ErrOutputTooLarge, wantStatus: http.StatusBadGateway, wantCode: "NGINX_OUTPUT_TOO_LARGE"},
		{name: "Agent unavailable", effectiveErr: errors.New("dial /run/private/agent.sock: permission denied"), wantStatus: http.StatusServiceUnavailable, wantCode: "AGENT_UNAVAILABLE"},
		{name: "build unavailable after config", buildErr: errors.New("agent disconnected after config secret-value"), wantStatus: http.StatusServiceUnavailable, wantCode: "AGENT_UNAVAILABLE"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agent := &stubAgent{
				effective: partial, effectiveErr: test.effectiveErr,
				build: nginxruntime.BuildInfo{Version: "1.30.3"}, buildErr: test.buildErr,
			}
			recorder := serveAuthenticatedBusinessGET(t, "/api/v1/nginx/effective-config", agent)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			var envelope ErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("json.Unmarshal(error) error = %v; body = %s", err, recorder.Body.String())
			}
			if envelope.Error.Code != test.wantCode || envelope.Error.Message == "" || envelope.Error.RequestID == "" {
				t.Fatalf("error = %+v, want code %s and safe complete envelope", envelope.Error, test.wantCode)
			}
			for _, forbidden := range []string{"occurrences", "partial-secret", "private_key", "secret-value", "permission denied", "/run/private"} {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Fatalf("error response exposes partial config or diagnostic %q: %s", forbidden, recorder.Body.String())
				}
			}
		})
	}
}

type decodedEffectiveConfig struct {
	GeneratedAt     time.Time                 `json:"generated_at"`
	NginxVersion    string                    `json:"nginx_version"`
	EntryConfigPath string                    `json:"entry_config_path"`
	OccurrenceCount int                       `json:"occurrence_count"`
	Occurrences     []decodedConfigOccurrence `json:"occurrences"`
}

type decodedConfigOccurrence struct {
	ID        string `json:"id"`
	LoadOrder int    `json:"load_order"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}
