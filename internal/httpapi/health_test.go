/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

func TestReadinessRequiresDatabaseAgentAndVerifiedRunningNginx(t *testing.T) {
	tests := []struct {
		name       string
		database   DatabaseProbe
		agent      *stubAgent
		wantStatus int
		wantBody   string
	}{
		{name: "unconfigured", wantStatus: http.StatusServiceUnavailable, wantBody: "{\"status\":\"not_ready\"}\n"},
		{
			name:       "database unavailable",
			database:   &stubDatabaseProbe{err: errors.New("database path is secret")},
			agent:      &stubAgent{status: nginxruntime.Status{State: nginxruntime.StateRunning}},
			wantStatus: http.StatusServiceUnavailable, wantBody: "{\"status\":\"not_ready\"}\n",
		},
		{
			name:       "Agent unavailable",
			database:   &stubDatabaseProbe{},
			agent:      &stubAgent{healthErr: errors.New("private socket unavailable")},
			wantStatus: http.StatusServiceUnavailable, wantBody: "{\"status\":\"not_ready\"}\n",
		},
		{
			name:       "status unavailable",
			database:   &stubDatabaseProbe{},
			agent:      &stubAgent{statusErr: errors.New("private process path unavailable")},
			wantStatus: http.StatusServiceUnavailable, wantBody: "{\"status\":\"not_ready\"}\n",
		},
		{
			name:       "running",
			database:   &stubDatabaseProbe{},
			agent:      &stubAgent{status: nginxruntime.Status{State: nginxruntime.StateRunning}},
			wantStatus: http.StatusOK, wantBody: "{\"status\":\"ready\"}\n",
		},
	}
	for _, state := range []nginxruntime.State{nginxruntime.StateDegraded, nginxruntime.StateStopped, nginxruntime.StateUnknown} {
		tests = append(tests, struct {
			name       string
			database   DatabaseProbe
			agent      *stubAgent
			wantStatus int
			wantBody   string
		}{
			name: string(state), database: &stubDatabaseProbe{}, agent: &stubAgent{status: nginxruntime.Status{State: state}},
			wantStatus: http.StatusServiceUnavailable, wantBody: "{\"status\":\"not_ready\"}\n",
		})
	}
	tests = append(tests, struct {
		name       string
		database   DatabaseProbe
		agent      *stubAgent
		wantStatus int
		wantBody   string
	}{
		name:     "permanent recovery failure",
		database: &stubDatabaseProbe{},
		agent: &stubAgent{status: nginxruntime.Status{
			State: nginxruntime.StateRunning, Recovery: &nginxruntime.RecoveryState{Permanent: true},
		}},
		wantStatus: http.StatusServiceUnavailable, wantBody: "{\"status\":\"not_ready\"}\n",
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
			NewHandler(Dependencies{Database: test.database, Agent: test.agent}).ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if recorder.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", recorder.Body.String(), test.wantBody)
			}
			for _, forbidden := range []string{"database", "Agent", "nginx", "path", "socket", "private", "secret", "running", "permanent"} {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Fatalf("readiness body exposes component detail %q: %s", forbidden, recorder.Body.String())
				}
			}
		})
	}
}

func TestReadinessUsesOneThreeSecondParentDeadlineForEveryProbe(t *testing.T) {
	database := &stubDatabaseProbe{ping: func(ctx context.Context) error {
		assertReadinessDeadline(t, ctx)
		return nil
	}}
	agent := &stubAgent{
		health: func(ctx context.Context) error {
			assertReadinessDeadline(t, ctx)
			return nil
		},
		statusFunc: func(ctx context.Context) (nginxruntime.Status, error) {
			assertReadinessDeadline(t, ctx)
			return nginxruntime.Status{State: nginxruntime.StateRunning}, nil
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	NewHandler(Dependencies{Database: database, Agent: agent}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if database.calls != 1 || agent.healthCalls != 1 || agent.statusCalls != 1 {
		t.Fatalf("database/health/status calls = %d/%d/%d, want 1/1/1", database.calls, agent.healthCalls, agent.statusCalls)
	}
}

func assertReadinessDeadline(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("readiness probe context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 2*time.Second || remaining > 3*time.Second {
		t.Fatalf("readiness probe deadline remaining = %s, want one shared deadline within three seconds", remaining)
	}
}

type stubDatabaseProbe struct {
	ping  func(context.Context) error
	err   error
	calls int
}

func (p *stubDatabaseProbe) PingContext(ctx context.Context) error {
	p.calls++
	if p.ping != nil {
		return p.ping(ctx)
	}
	return p.err
}

var _ DatabaseProbe = (*stubDatabaseProbe)(nil)
