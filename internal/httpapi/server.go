/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
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
	mux.Handle("GET /", spaFallback(dependencies.Assets))
	return requestBoundary(mux, dependencies.Logger, newRequestIDGenerator(dependencies.RequestIDSource))
}

func spaFallback(assets fs.FS) http.Handler {
	if assets == nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assetPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if assetPath != "" && assetPath != "." {
			info, err := fs.Stat(assets, assetPath)
			switch {
			case err == nil && !info.IsDir():
				if strings.HasSuffix(strings.ToLower(assetPath), ".html") {
					writer.Header().Set("Cache-Control", "no-store")
				}
				files.ServeHTTP(writer, request)
				return
			case err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrInvalid):
				http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}

		if reservedSPAPath(request.URL.Path) {
			http.NotFound(writer, request)
			return
		}

		writer.Header().Set("Cache-Control", "no-store")
		indexRequest := request.Clone(request.Context())
		indexURL := *request.URL
		indexURL.Path = "/"
		indexURL.RawPath = ""
		indexRequest.URL = &indexURL
		files.ServeHTTP(writer, indexRequest)
	})
}

func reservedSPAPath(requestPath string) bool {
	cleaned := path.Clean(requestPath)
	for _, prefix := range []string{"/api", "/health", "/assets"} {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return true
		}
	}
	return false
}
