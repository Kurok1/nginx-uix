/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const cspNoncePlaceholder = "__NGINX_UIX_CSP_NONCE__"

// Dependencies contains explicit HTTP boundary dependencies.
type Dependencies struct {
	Assets                    fs.FS
	Sessions                  SessionService
	Workspaces                WorkspaceAPI
	Structured                StructuredAPI
	Groups                    GroupAPI
	Releases                  ReleaseAPI
	ReleaseTasks              ReleaseTaskStarter
	Recovery                  RecoveryAPI
	RecoveryTasks             RecoveryTaskStarter
	RouteLab                  RouteLabAPI
	RouteTasks                RouteTaskController
	CertificateAccounts       CertificateAccountAPI
	CertificateCredentials    CertificateCredentialAPI
	CertificatePlans          CertificatePlanAPI
	CertificatePlanReader     CertificatePlanReader
	CertificateQueue          CertificateQueueAPI
	CertificateTasks          CertificateTaskAPI
	CertificateTaskController CertificateTaskController
	Certificates              CertificateInventoryAPI
	CertificateRenewals       CertificateRenewalAPI
	CertificateBindings       CertificateBindingAPI
	CertificateLifecycle      CertificateLifecycleAPI
	Agent                     Agent
	Database                  DatabaseProbe
	PublicURL                 *url.URL
	Logger                    *slog.Logger
	RequestIDSource           io.Reader
	CSPNonceSource            io.Reader
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
		workspaces: dependencies.Workspaces, structured: dependencies.Structured, groups: dependencies.Groups,
		sessions: dependencies.Sessions, publicURL: dependencies.PublicURL,
	}
	releases := &releaseHandler{
		service: dependencies.Releases, workspaces: dependencies.Workspaces, tasks: dependencies.ReleaseTasks,
		sessions: dependencies.Sessions, publicURL: dependencies.PublicURL,
	}
	recovery := &recoveryHandler{
		service: dependencies.Recovery, tasks: dependencies.RecoveryTasks,
		sessions: dependencies.Sessions, publicURL: dependencies.PublicURL,
	}
	routeLab := &routeLabHandler{
		service: dependencies.RouteLab, workspaces: dependencies.Workspaces, tasks: dependencies.RouteTasks,
		sessions: dependencies.Sessions, publicURL: dependencies.PublicURL,
	}
	certificates := &certificateHandler{
		accounts: dependencies.CertificateAccounts, credentials: dependencies.CertificateCredentials,
		plans: dependencies.CertificatePlans, planReader: dependencies.CertificatePlanReader,
		queue: dependencies.CertificateQueue, tasks: dependencies.CertificateTasks,
		taskOwner: dependencies.CertificateTaskController, inventory: dependencies.Certificates,
		renewals: dependencies.CertificateRenewals, bindingPlans: dependencies.CertificateBindings,
		lifecycle: dependencies.CertificateLifecycle,
		sessions:  dependencies.Sessions,
		publicURL: dependencies.PublicURL,
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
	mux.HandleFunc("GET /api/v1/config/workspaces/{workspace_id}/structured-config", configuration.structuredCatalog)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/structured-change-previews", configuration.structuredPreview)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/structured-changes", configuration.structuredApply)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/publish-checks", releases.checkWorkspace)
	mux.HandleFunc("GET /api/v1/config/publish-checks/{check_id}", releases.check)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/releases", releases.queue)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/route-analyses", routeLab.analyze)
	mux.HandleFunc("POST /api/v1/config/workspaces/{workspace_id}/route-tests", routeLab.queue)
	mux.HandleFunc("GET /api/v1/route-tests", routeLab.history)
	mux.HandleFunc("GET /api/v1/route-tests/{run_id}", routeLab.run)
	mux.HandleFunc("GET /api/v1/route-tests/{run_id}/events", routeLab.events)
	mux.HandleFunc("POST /api/v1/route-tests/{run_id}/cancellations", routeLab.cancel)
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
	mux.HandleFunc("GET /api/v1/acme/directories", certificates.directories)
	mux.HandleFunc("GET /api/v1/acme/accounts", certificates.accountsCollection)
	mux.HandleFunc("POST /api/v1/acme/accounts", certificates.accountsCollection)
	mux.HandleFunc("POST /api/v1/acme/account-imports", certificates.accountImports)
	mux.HandleFunc("POST /api/v1/acme/accounts/{account_id}/deactivations", certificates.deactivateAccount)
	mux.HandleFunc("GET /api/v1/certificate-dns-credentials", certificates.credentialsCollection)
	mux.HandleFunc("POST /api/v1/certificate-dns-credentials", certificates.credentialsCollection)
	mux.HandleFunc("DELETE /api/v1/certificate-dns-credentials/{credential_id}", certificates.credential)
	mux.HandleFunc("GET /api/v1/certificate-server-candidates", certificates.serverCandidates)
	mux.HandleFunc("POST /api/v1/certificate-order-plans", certificates.plansCollection)
	mux.HandleFunc("GET /api/v1/certificate-order-plans/{plan_id}", certificates.plan)
	mux.HandleFunc("POST /api/v1/certificate-order-plans/{plan_id}/executions", certificates.executePlan)
	mux.HandleFunc("GET /api/v1/certificate-tasks", certificates.tasksCollection)
	mux.HandleFunc("GET /api/v1/certificate-tasks/{task_id}", certificates.task)
	mux.HandleFunc("GET /api/v1/certificate-tasks/{task_id}/events", certificates.taskEvents)
	mux.HandleFunc("POST /api/v1/certificate-tasks/{task_id}/cancellations", certificates.cancelTask)
	mux.HandleFunc("GET /api/v1/certificates", certificates.certificatesCollection)
	mux.HandleFunc("GET /api/v1/certificates/{certificate_id}", certificates.certificate)
	mux.HandleFunc("POST /api/v1/certificates/{certificate_id}/renewals", certificates.renewCertificate)
	mux.HandleFunc("PUT /api/v1/certificates/{certificate_id}/renewal-policy", certificates.updateRenewalPolicy)
	mux.HandleFunc("POST /api/v1/certificates/{certificate_id}/binding-plans", certificates.createBindingPlan)
	mux.HandleFunc("POST /api/v1/certificate-binding-plans/{plan_id}/executions", certificates.executeBindingPlan)
	mux.HandleFunc("POST /api/v1/certificates/{certificate_id}/unbindings", certificates.unbindCertificate)
	mux.HandleFunc("POST /api/v1/certificates/{certificate_id}/exports", certificates.exportCertificate)
	mux.HandleFunc("DELETE /api/v1/certificates/{certificate_id}", certificates.deleteCertificate)
	mux.Handle("GET /", spaFallback(dependencies.Assets, newCSPNonceGenerator(dependencies.CSPNonceSource)))
	return requestBoundary(configNoStore(mux), dependencies.Logger, newRequestIDGenerator(dependencies.RequestIDSource))
}

func spaFallback(assets fs.FS, nonceGenerator *randomTokenGenerator) http.Handler {
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
					serveNonceHTML(writer, request, assets, assetPath, nonceGenerator)
					return
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

		serveNonceHTML(writer, request, assets, "index.html", nonceGenerator)
	})
}

func serveNonceHTML(
	writer http.ResponseWriter,
	request *http.Request,
	assets fs.FS,
	assetPath string,
	nonceGenerator *randomTokenGenerator,
) {
	payload, err := fs.ReadFile(assets, assetPath)
	if err != nil {
		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	placeholder := []byte(cspNoncePlaceholder)
	if bytes.Count(payload, placeholder) != 1 {
		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	nonce, err := nonceGenerator.Generate()
	if err != nil {
		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	rendered := bytes.Replace(payload, placeholder, []byte(nonce), 1)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Content-Length", strconv.Itoa(len(rendered)))
	writer.Header().Set(
		"Content-Security-Policy",
		"default-src 'self'; style-src 'self' 'nonce-"+nonce+"'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'",
	)
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	// A client disconnect is terminal for this response; there is no safe secondary response to write.
	_, _ = writer.Write(rendered)
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
	case "/login", "/configuration", "/config/workspaces", "/config/operations", "/config/route-lab", "/certificates":
		return true
	}
	if certificateID, found := strings.CutPrefix(path, "/certificates/"); found {
		return isOpaqueNavigationID(certificateID)
	}
	const workspacePrefix = "/config/workspaces/"
	workspaceRoute, found := strings.CutPrefix(path, workspacePrefix)
	if !found {
		return false
	}
	workspaceID, child, hasChild := strings.Cut(workspaceRoute, "/")
	if !isOpaqueNavigationID(workspaceID) {
		return false
	}
	return !hasChild || child == "upstreams" || child == "servers"
}

func isOpaqueNavigationID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
