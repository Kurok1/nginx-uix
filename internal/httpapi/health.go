/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"context"
	"net/http"
)

type healthStatus struct {
	Status string `json:"status"`
}

func live(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, healthStatus{Status: "ok"})
}

func ready(readiness func(context.Context) error) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if readiness == nil || readiness(request.Context()) != nil {
			writeJSON(writer, http.StatusServiceUnavailable, healthStatus{Status: "unavailable"})
			return
		}
		writeJSON(writer, http.StatusOK, healthStatus{Status: "ok"})
	}
}
