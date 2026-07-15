/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
)

// Dependencies contains explicit HTTP boundary dependencies.
type Dependencies struct {
	Assets          fs.FS
	Sessions        SessionService
	Agent           Agent
	Database        DatabaseProbe
	PublicURL       *url.URL
	Logger          *slog.Logger
	RequestIDSource io.Reader
}

// NewHandler creates the public HTTP surface.
func NewHandler(dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", live)
	mux.HandleFunc("GET /health/ready", ready(dependencies.Database, dependencies.Agent))
	sessions := &sessionHandler{service: dependencies.Sessions, publicURL: dependencies.PublicURL}
	mux.HandleFunc("POST /api/v1/auth/session", sessions.login)
	mux.HandleFunc("GET /api/v1/auth/session", sessions.current)
	mux.HandleFunc("DELETE /api/v1/auth/session", sessions.logout)
	mux.HandleFunc("GET /api/v1/system/status", systemStatus(dependencies.Sessions, dependencies.Agent))
	mux.HandleFunc("GET /api/v1/nginx/effective-config", effectiveConfig(dependencies.Sessions, dependencies.Agent))
	mux.Handle("/", spaFallback(dependencies.Assets))
	return requestBoundary(mux, dependencies.Logger, newRequestIDGenerator(dependencies.RequestIDSource))
}

func spaFallback(assets fs.FS) http.Handler {
	if assets == nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(assets))
}
