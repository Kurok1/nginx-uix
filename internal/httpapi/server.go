/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"context"
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
	PublicURL       *url.URL
	Readiness       func(context.Context) error
	Logger          *slog.Logger
	RequestIDSource io.Reader
}

// NewHandler creates the public HTTP surface.
func NewHandler(dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", live)
	mux.HandleFunc("GET /health/ready", ready(dependencies.Readiness))
	sessions := &sessionHandler{service: dependencies.Sessions, publicURL: dependencies.PublicURL}
	mux.HandleFunc("POST /api/v1/auth/session", sessions.login)
	mux.HandleFunc("GET /api/v1/auth/session", sessions.current)
	mux.HandleFunc("DELETE /api/v1/auth/session", sessions.logout)
	mux.Handle("/", spaFallback(dependencies.Assets))
	return requestBoundary(mux, dependencies.Logger, newRequestIDGenerator(dependencies.RequestIDSource))
}

func spaFallback(assets fs.FS) http.Handler {
	if assets == nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(assets))
}
