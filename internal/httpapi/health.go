/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"context"
	"net/http"
	"time"

	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

const readinessTimeout = 3 * time.Second

// DatabaseProbe is the bounded SQLite operation consumed by readiness.
type DatabaseProbe interface {
	PingContext(ctx context.Context) error
}

type healthStatus struct {
	Status string `json:"status"`
}

func live(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, healthStatus{Status: "ok"})
}

func ready(database DatabaseProbe, agent Agent) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if database == nil || agent == nil {
			writeJSON(writer, http.StatusServiceUnavailable, healthStatus{Status: "not_ready"})
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), readinessTimeout)
		defer cancel()
		if database.PingContext(ctx) != nil || agent.Health(ctx) != nil {
			writeJSON(writer, http.StatusServiceUnavailable, healthStatus{Status: "not_ready"})
			return
		}
		status, err := agent.Status(ctx)
		if err != nil || status.State != nginxruntime.StateRunning || (status.Recovery != nil && status.Recovery.Permanent) {
			writeJSON(writer, http.StatusServiceUnavailable, healthStatus{Status: "not_ready"})
			return
		}
		writeJSON(writer, http.StatusOK, healthStatus{Status: "ready"})
	}
}
