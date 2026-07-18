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
	"strings"
)

// Dependencies contains explicit HTTP boundary dependencies.
type Dependencies struct {
	Assets          fs.FS
	Sessions        SessionService
	Workspaces      WorkspaceAPI
	Groups          GroupAPI
	Releases        ReleaseAPI
	ReleaseTasks    ReleaseTaskStarter
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
	configuration := &configHandler{
		workspaces: dependencies.Workspaces, groups: dependencies.Groups,
		sessions: dependencies.Sessions, publicURL: dependencies.PublicURL,
	}
	releases := &releaseHandler{
		service: dependencies.Releases, tasks: dependencies.ReleaseTasks,
		sessions: dependencies.Sessions, publicURL: dependencies.PublicURL,
	}
	mux.HandleFunc("GET /api/v1/config/workspaces", configuration.workspacesCollection)
	mux.HandleFunc("POST /api/v1/config/workspaces", configuration.workspacesCollection)
	mux.HandleFunc("GET /api/v1/config/workspaces/{workspace_id}", configuration.workspace)
	mux.HandleFunc("DELETE /api/v1/config/workspaces/{workspace_id}", configuration.workspace)
	mux.HandleFunc("GET /api/v1/config/workspaces/{workspace_id}/files", configuration.files)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/files", configuration.files)
	mux.HandleFunc("PUT /api/v1/config/workspaces/{workspace_id}/files", configuration.files)
	mux.HandleFunc("PATCH /api/v1/config/workspaces/{workspace_id}/files", configuration.files)
	mux.HandleFunc("DELETE /api/v1/config/workspaces/{workspace_id}/files", configuration.files)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/files/copies", configuration.copies)
	mux.HandleFunc("GET /api/v1/config/workspaces/{workspace_id}/files/search", configuration.search)
	mux.HandleFunc("GET /api/v1/config/workspaces/{workspace_id}/diff", configuration.diff)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/publish-checks", releases.checkWorkspace)
	mux.HandleFunc("GET /api/v1/config/publish-checks/{check_id}", releases.check)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/releases", releases.queue)
	mux.HandleFunc("GET /api/v1/config/releases/{release_id}", releases.release)
	mux.HandleFunc("GET /api/v1/config/releases/{release_id}/events", releases.events)
	mux.HandleFunc("GET /api/v1/config/groups", configuration.groupsCollection)
	mux.HandleFunc("POST /api/v1/config/groups", configuration.groupsCollection)
	mux.HandleFunc("PUT /api/v1/config/groups/{group_id}", configuration.group)
	mux.HandleFunc("DELETE /api/v1/config/groups/{group_id}", configuration.group)
	mux.Handle("/", spaFallback(dependencies.Assets))
	return requestBoundary(configNoStore(mux), dependencies.Logger, newRequestIDGenerator(dependencies.RequestIDSource))
}

func spaFallback(assets fs.FS) http.Handler {
	if assets == nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(writer, request)
			return
		}
		assetPath := strings.TrimPrefix(request.URL.Path, "/")
		if assetPath == "" {
			files.ServeHTTP(writer, request)
			return
		}
		if information, err := fs.Stat(assets, assetPath); err == nil && !information.IsDir() {
			files.ServeHTTP(writer, request)
			return
		}
		if !isKnownSPANavigation(request.URL.Path) {
			http.NotFound(writer, request)
			return
		}
		indexRequest := request.Clone(request.Context())
		indexURL := *request.URL
		indexURL.Path = "/"
		indexURL.RawPath = ""
		indexRequest.URL = &indexURL
		files.ServeHTTP(writer, indexRequest)
	})
}

func isKnownSPANavigation(path string) bool {
	switch path {
	case "/login", "/configuration", "/config/workspaces":
		return true
	}
	const workspacePrefix = "/config/workspaces/"
	workspaceID, found := strings.CutPrefix(path, workspacePrefix)
	if !found || len(workspaceID) != 32 {
		return false
	}
	for _, character := range workspaceID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
