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
	Workspaces      WorkspaceAPI
	Groups          GroupAPI
	Releases        ReleaseAPI
	ReleaseTasks    ReleaseTaskStarter
	Recovery        RecoveryAPI
	RecoveryTasks   RecoveryTaskStarter
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
	recovery := &recoveryHandler{
		service: dependencies.Recovery, tasks: dependencies.RecoveryTasks,
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
	mux.HandleFunc("GET /api/v1/config/history/releases", recovery.historyReleases)
	mux.HandleFunc("GET /api/v1/config/history/restores", recovery.historyRestores)
	mux.HandleFunc("GET /api/v1/config/history/restarts", recovery.historyRestarts)
	mux.HandleFunc("GET /api/v1/config/backups", recovery.backups)
	mux.HandleFunc("GET /api/v1/config/backups/{backup_id}", recovery.backup)
	mux.HandleFunc("PUT /api/v1/config/backups/{backup_id}/protection", recovery.protection)
	mux.HandleFunc("POST /api/v1/config/backup-retention-runs", recovery.planRetention)
	mux.HandleFunc("GET /api/v1/config/backup-retention-runs/{retention_id}", recovery.retention)
	mux.HandleFunc("POST /api/v1/config/backup-retention-runs/{retention_id}/executions", recovery.executeRetention)
	mux.HandleFunc("POST /api/v1/config/backups/{backup_id}/restores", recovery.queueRestore)
	mux.HandleFunc("GET /api/v1/config/restores/{restore_id}", recovery.restore)
	mux.HandleFunc("GET /api/v1/config/restores/{restore_id}/events", recovery.restoreEvents)
	mux.HandleFunc("POST /api/v1/nginx/restarts", recovery.restartCollection)
	mux.HandleFunc("GET /api/v1/nginx/restarts/{restart_id}", recovery.restart)
	mux.HandleFunc("GET /api/v1/nginx/restarts/{restart_id}/events", recovery.restartEvents)
	mux.HandleFunc("GET /api/v1/config/audit-events", recovery.audit)
	mux.HandleFunc("GET /api/v1/config/attention-cases", recovery.attentionCollection)
	mux.HandleFunc("GET /api/v1/config/attention-cases/{attention_id}", recovery.attention)
	mux.HandleFunc("POST /api/v1/config/attention-cases/{attention_id}/verifications", recovery.verifyAttention)
	mux.HandleFunc("GET /api/v1/config/groups", configuration.groupsCollection)
	mux.HandleFunc("POST /api/v1/config/groups", configuration.groupsCollection)
	mux.HandleFunc("PUT /api/v1/config/groups/{group_id}", configuration.group)
	mux.HandleFunc("DELETE /api/v1/config/groups/{group_id}", configuration.group)
	mux.Handle("GET /", spaFallback(dependencies.Assets))
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
		cleanedPath := path.Clean(request.URL.Path)
		assetPath := strings.TrimPrefix(cleanedPath, "/")
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

		if reservedSPAPath(cleanedPath) || (cleanedPath != "/" && !isKnownSPANavigation(cleanedPath)) {
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

func isKnownSPANavigation(path string) bool {
	switch path {
	case "/login", "/configuration", "/config/workspaces", "/config/operations":
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
