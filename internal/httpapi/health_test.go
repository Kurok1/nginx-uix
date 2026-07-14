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
)

func TestReadyReportsOnlyAggregateAvailability(t *testing.T) {
	tests := []struct {
		name      string
		readiness func(context.Context) error
		want      int
	}{
		{name: "unconfigured", readiness: nil, want: http.StatusServiceUnavailable},
		{name: "ready", readiness: func(context.Context) error { return nil }, want: http.StatusOK},
		{name: "not ready", readiness: func(context.Context) error { return errors.New("database path is secret") }, want: http.StatusServiceUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
			NewHandler(Dependencies{Readiness: test.readiness}).ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if recorder.Body.String() != "{\"status\":\"ok\"}\n" && recorder.Body.String() != "{\"status\":\"unavailable\"}\n" {
				t.Fatalf("body = %q, want aggregate status only", recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "database") || strings.Contains(recorder.Body.String(), "path") || strings.Contains(recorder.Body.String(), "secret") {
				t.Fatalf("readiness body exposes diagnostic: %s", recorder.Body.String())
			}
		})
	}
}
