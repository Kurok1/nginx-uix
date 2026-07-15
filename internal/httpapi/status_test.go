/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"context"
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

func TestSystemStatusMapsVerifiedStatesAndSortsPublicEvidence(t *testing.T) {
	sampledAt := time.Date(2026, time.July, 15, 8, 30, 0, 0, time.UTC)
	masterStartedAt := sampledAt.Add(-time.Hour)
	workerStartedAt := sampledAt.Add(-59 * time.Minute)
	exitCode := 0

	tests := []struct {
		name  string
		state nginxruntime.State
	}{
		{name: "healthy", state: nginxruntime.StateRunning},
		{name: "degraded", state: nginxruntime.StateDegraded},
		{name: "stopped", state: nginxruntime.StateStopped},
		{name: "unknown", state: nginxruntime.StateUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := nginxruntime.Status{
				SampledAt: sampledAt,
				State:     test.state,
				Workers:   make([]nginxruntime.NginxProcess, 0),
				Issues:    []string{"Z_ISSUE", "A_ISSUE", "A_ISSUE"},
			}
			if test.state == nginxruntime.StateRunning || test.state == nginxruntime.StateDegraded {
				status.Master = &nginxruntime.NginxProcess{
					PID: 100, Role: nginxruntime.ProcessRoleMaster, StartedAt: masterStartedAt,
				}
				status.Workers = []nginxruntime.NginxProcess{
					{PID: 103, Role: nginxruntime.ProcessRoleWorker, StartedAt: workerStartedAt},
					{PID: 101, Role: nginxruntime.ProcessRoleWorker, StartedAt: workerStartedAt},
				}
				status.Build = &nginxruntime.BuildInfo{
					Version: "1.30.3", ConfigureArguments: []string{"--with-http_ssl_module", "--with-http_v2_module"},
					PIDPath: "/run/private-nginx.pid", SbinPath: "/usr/sbin/private-nginx",
				}
				status.StartupValidation = &nginxruntime.StartupValidation{
					Valid: true, CheckedAt: sampledAt.Add(-2 * time.Minute), ExitCode: &exitCode,
					Diagnostic: "syntax\x00is\x1bok\r\nsecond line",
				}
				status.Recovery = &nginxruntime.RecoveryState{
					Count: 2, LastResult: nginxruntime.RecoveryResultRestarting,
					Events: []nginxruntime.ExitEvent{{OccurredAt: sampledAt.Add(-time.Minute), ExitCode: 1}},
				}
			}

			agent := &stubAgent{status: status}
			recorder := serveAuthenticatedBusinessGET(t, "/api/v1/system/status", agent)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			var response decodedSystemStatus
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("json.Unmarshal(status) error = %v; body = %s", err, recorder.Body.String())
			}
			if response.SampledAt != sampledAt {
				t.Fatalf("sampled_at = %s, want %s", response.SampledAt, sampledAt)
			}
			if response.Components != (decodedStatusComponents{UI: "healthy", Agent: "healthy", Nginx: string(test.state)}) {
				t.Fatalf("components = %+v, want healthy/healthy/%s", response.Components, test.state)
			}
			if want := []string{"A_ISSUE", "Z_ISSUE"}; !reflect.DeepEqual(response.Issues, want) {
				t.Fatalf("issues = %#v, want %#v", response.Issues, want)
			}
			if test.state != nginxruntime.StateRunning && test.state != nginxruntime.StateDegraded {
				if response.Master != nil || response.Build != nil || response.StartupValidation != nil || response.Recovery != nil {
					t.Fatalf("nullable evidence = master:%+v build:%+v startup:%+v recovery:%+v, want nil", response.Master, response.Build, response.StartupValidation, response.Recovery)
				}
				if response.Workers == nil || len(response.Workers) != 0 {
					t.Fatalf("workers = %#v, want non-nil empty array", response.Workers)
				}
				return
			}

			if response.Master == nil || response.Master.PID != 100 || response.Master.Role != "master" || response.Master.StartedAt != masterStartedAt {
				t.Fatalf("master = %+v, want explicit public master evidence", response.Master)
			}
			if got, want := workerPIDs(response.Workers), []int{101, 103}; !reflect.DeepEqual(got, want) {
				t.Fatalf("worker PIDs = %#v, want %#v", got, want)
			}
			if response.Build == nil || response.Build.Version != "1.30.3" || !reflect.DeepEqual(response.Build.ConfigureArguments, []string{"--with-http_ssl_module", "--with-http_v2_module"}) {
				t.Fatalf("build = %+v, want public version and ordered arguments", response.Build)
			}
			if response.StartupValidation == nil || response.StartupValidation.Diagnostic != "syntax?is?ok\nsecond line" {
				t.Fatalf("startup_validation = %+v, want boundary-sanitized diagnostic", response.StartupValidation)
			}
			if response.Recovery == nil || response.Recovery.Count != 2 || response.Recovery.LastResult != "restarting" || response.Recovery.Permanent {
				t.Fatalf("recovery = %+v, want bounded public recovery evidence", response.Recovery)
			}
			for _, forbidden := range []string{"pid_path", "sbin_path", "private-nginx", "events", "occurred_at", "exit_code\":1"} {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Fatalf("public status exposes runtime-only field %q: %s", forbidden, recorder.Body.String())
				}
			}
		})
	}
}

func TestSystemStatusReturnsPartialResponseWhenAgentUnavailable(t *testing.T) {
	agent := &stubAgent{statusErr: errors.New("dial /run/private/agent.sock: permission denied")}
	recorder := serveAuthenticatedBusinessGET(t, "/api/v1/system/status", agent)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	var response decodedSystemStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal(status) error = %v", err)
	}
	if response.SampledAt.IsZero() {
		t.Fatal("sampled_at is zero, want an RFC3339 timestamp")
	}
	if response.Components != (decodedStatusComponents{UI: "healthy", Agent: "unavailable", Nginx: "unknown"}) {
		t.Fatalf("components = %+v, want healthy/unavailable/unknown", response.Components)
	}
	if response.Master != nil || response.Build != nil || response.StartupValidation != nil || response.Recovery != nil {
		t.Fatalf("unavailable evidence is not nullable: %+v", response)
	}
	if response.Workers == nil || len(response.Workers) != 0 {
		t.Fatalf("workers = %#v, want non-nil empty array", response.Workers)
	}
	if want := []string{"AGENT_UNAVAILABLE"}; !reflect.DeepEqual(response.Issues, want) {
		t.Fatalf("issues = %#v, want %#v", response.Issues, want)
	}
	for _, forbidden := range []string{"permission denied", "/run/private", "dial"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("partial response exposes Agent error %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestSystemStatusRequiresAuthenticationBeforeCallingAgent(t *testing.T) {
	agent := &stubAgent{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	NewHandler(Dependencies{Sessions: &recordingSessionService{}, Agent: agent}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", recorder.Code, recorder.Body.String())
	}
	if agent.statusCalls != 0 {
		t.Fatalf("Agent.Status() calls = %d, want 0", agent.statusCalls)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

type decodedSystemStatus struct {
	SampledAt         time.Time                 `json:"sampled_at"`
	Components        decodedStatusComponents   `json:"components"`
	Master            *decodedProcess           `json:"master"`
	Workers           []decodedProcess          `json:"workers"`
	Build             *decodedBuild             `json:"build"`
	StartupValidation *decodedStartupValidation `json:"startup_validation"`
	Recovery          *decodedRecovery          `json:"recovery"`
	Issues            []string                  `json:"issues"`
}

type decodedStatusComponents struct {
	UI    string `json:"ui"`
	Agent string `json:"agent"`
	Nginx string `json:"nginx"`
}

type decodedProcess struct {
	PID       int       `json:"pid"`
	Role      string    `json:"role"`
	StartedAt time.Time `json:"started_at"`
}

type decodedBuild struct {
	Version            string   `json:"version"`
	ConfigureArguments []string `json:"configure_arguments"`
}

type decodedStartupValidation struct {
	Valid      bool      `json:"valid"`
	CheckedAt  time.Time `json:"checked_at"`
	ExitCode   *int      `json:"exit_code"`
	Diagnostic string    `json:"diagnostic"`
}

type decodedRecovery struct {
	Count      int    `json:"count"`
	LastResult string `json:"last_result"`
	Permanent  bool   `json:"permanent"`
}

func workerPIDs(workers []decodedProcess) []int {
	result := make([]int, 0, len(workers))
	for _, worker := range workers {
		result = append(result, worker.PID)
	}
	return result
}

func serveAuthenticatedBusinessGET(t *testing.T, path string, agent Agent) *httptest.ResponseRecorder {
	t.Helper()
	sessions := &recordingSessionService{currentResult: testIssuedSession()}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testIssuedSession().Token})
	NewHandler(Dependencies{Sessions: sessions, Agent: agent}).ServeHTTP(recorder, request)
	return recorder
}

type stubAgent struct {
	health       func(context.Context) error
	statusFunc   func(context.Context) (nginxruntime.Status, error)
	healthErr    error
	status       nginxruntime.Status
	statusErr    error
	build        nginxruntime.BuildInfo
	buildErr     error
	startup      nginxruntime.StartupState
	startupErr   error
	effective    nginxruntime.EffectiveConfig
	effectiveErr error

	healthCalls    int
	statusCalls    int
	buildCalls     int
	startupCalls   int
	effectiveCalls int
}

func (a *stubAgent) Health(ctx context.Context) error {
	a.healthCalls++
	if a.health != nil {
		return a.health(ctx)
	}
	return a.healthErr
}

func (a *stubAgent) Status(ctx context.Context) (nginxruntime.Status, error) {
	a.statusCalls++
	if a.statusFunc != nil {
		return a.statusFunc(ctx)
	}
	return a.status, a.statusErr
}

func (a *stubAgent) BuildInfo(context.Context) (nginxruntime.BuildInfo, error) {
	a.buildCalls++
	return a.build, a.buildErr
}

func (a *stubAgent) StartupValidation(context.Context) (nginxruntime.StartupState, error) {
	a.startupCalls++
	return a.startup, a.startupErr
}

func (a *stubAgent) EffectiveConfig(context.Context) (nginxruntime.EffectiveConfig, error) {
	a.effectiveCalls++
	return a.effective, a.effectiveErr
}

var _ Agent = (*stubAgent)(nil)
