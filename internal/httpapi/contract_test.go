/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIV1FrozenContractFingerprint(t *testing.T) {
	contents, err := os.ReadFile("../../api/v1/openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi) error = %v", err)
	}
	got, err := openAPIContractFingerprint(contents)
	if err != nil {
		t.Fatalf("openAPIContractFingerprint() error = %v", err)
	}
	const want = "cbec829304bf5e1b00a9d79c8aae79505ebbfe6fa8402e87866822c23730d857"
	if got != want {
		t.Fatalf("OpenAPI v1 contract fingerprint = %q, want %q; contract changes require an explicit compatibility review", got, want)
	}
}

func openAPIContractFingerprint(contents []byte) (string, error) {
	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return "", fmt.Errorf("decode OpenAPI contract: %w", err)
	}
	info, ok := document["info"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("decode OpenAPI contract: info object is required")
	}
	// Application patch versions may change without creating a new REST API major.
	info["version"] = "v1"
	normalized, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("normalize OpenAPI contract: %w", err)
	}
	checksum := sha256.Sum256(normalized)
	return fmt.Sprintf("%x", checksum), nil
}

type openAPIContractDocument struct {
	OpenAPI    string                                 `yaml:"openapi"`
	Paths      map[string]map[string]openAPIOperation `yaml:"paths"`
	Components openAPIComponents                      `yaml:"components"`
}

type openAPIComponents struct {
	Parameters map[string]openAPIParameter `yaml:"parameters"`
	Schemas    map[string]openAPISchema    `yaml:"schemas"`
}

type openAPIOperation struct {
	OperationID string                     `yaml:"operationId"`
	Description string                     `yaml:"description"`
	Security    []map[string][]string      `yaml:"security"`
	Parameters  []openAPIParameter         `yaml:"parameters"`
	RequestBody *openAPIRequestBody        `yaml:"requestBody"`
	Responses   map[string]openAPIResponse `yaml:"responses"`
}

type openAPIParameter struct {
	Ref         string        `yaml:"$ref"`
	Name        string        `yaml:"name"`
	In          string        `yaml:"in"`
	Required    bool          `yaml:"required"`
	Description string        `yaml:"description"`
	Schema      openAPISchema `yaml:"schema"`
}

type openAPIRequestBody struct {
	Required    bool                    `yaml:"required"`
	Description string                  `yaml:"description"`
	Content     map[string]openAPIMedia `yaml:"content"`
}

type openAPIMedia struct {
	Schema openAPISchema `yaml:"schema"`
}

type openAPIResponse struct {
	Ref     string                   `yaml:"$ref"`
	Headers map[string]openAPIHeader `yaml:"headers"`
	Content map[string]openAPIMedia  `yaml:"content"`
}

type openAPIHeader struct {
	Ref    string        `yaml:"$ref"`
	Schema openAPISchema `yaml:"schema"`
}

type openAPISchema struct {
	Ref                  string                   `yaml:"$ref"`
	Type                 any                      `yaml:"type"`
	Format               string                   `yaml:"format"`
	Pattern              string                   `yaml:"pattern"`
	Description          string                   `yaml:"description"`
	Enum                 []string                 `yaml:"enum"`
	Required             []string                 `yaml:"required"`
	Properties           map[string]openAPISchema `yaml:"properties"`
	Items                *openAPISchema           `yaml:"items"`
	OneOf                []openAPISchema          `yaml:"oneOf"`
	AdditionalProperties any                      `yaml:"additionalProperties"`
	MinItems             *int                     `yaml:"minItems"`
	MaxItems             *int                     `yaml:"maxItems"`
	WriteOnly            bool                     `yaml:"writeOnly"`
}

type contractOperation struct {
	method         string
	path           string
	operationID    string
	successStatus  string
	successSchemas []string
	query          string
	queryRequired  bool
	ifMatch        bool
	etag           bool
	bodyLimitBytes int
}

type recoveryContractOperation struct {
	method        string
	path          string
	operationID   string
	successStatus string
	successSchema string
	mediaType     string
	queries       []string
	bodyLimit     int
	location      bool
	lastEventID   bool
}

type routeContractOperation struct {
	method        string
	path          string
	operationID   string
	successStatus string
	successSchema string
	mediaType     string
	queries       []string
	bodyLimit     int
	location      bool
	lastEventID   bool
	ifMatch       bool
}

var configContractOperations = []contractOperation{
	{http.MethodGet, "/api/v1/config/workspaces", "listConfigWorkspaces", "200", []string{"WorkspaceList"}, "", false, false, false, 0},
	{http.MethodPost, "/api/v1/config/workspaces", "createConfigWorkspace", "201", []string{"WorkspaceDetail"}, "", false, false, true, 4 << 10},
	{http.MethodGet, "/api/v1/config/workspaces/{workspace_id}", "getConfigWorkspace", "200", []string{"WorkspaceDetail"}, "", false, false, true, 0},
	{http.MethodDelete, "/api/v1/config/workspaces/{workspace_id}", "deleteConfigWorkspace", "204", nil, "", false, true, false, 256 << 10},
	{http.MethodGet, "/api/v1/config/workspaces/{workspace_id}/files", "getConfigWorkspaceFiles", "200", []string{"ConfigTree", "ConfigFile"}, "path", false, false, true, 0},
	{http.MethodPost, "/api/v1/config/workspaces/{workspace_id}/files", "createConfigFile", "201", []string{"FileMutationResponse"}, "", false, true, true, (2 << 20) + (64 << 10)},
	{http.MethodPut, "/api/v1/config/workspaces/{workspace_id}/files", "replaceConfigFile", "200", []string{"FileMutationResponse"}, "path", true, true, true, (2 << 20) + (64 << 10)},
	{http.MethodPatch, "/api/v1/config/workspaces/{workspace_id}/files", "renameConfigFile", "200", []string{"FileMutationResponse"}, "path", true, true, true, 256 << 10},
	{http.MethodDelete, "/api/v1/config/workspaces/{workspace_id}/files", "deleteConfigFile", "200", []string{"FileMutationResponse"}, "path", true, true, true, 256 << 10},
	{http.MethodPost, "/api/v1/config/workspaces/{workspace_id}/files/copies", "copyConfigFile", "201", []string{"FileMutationResponse"}, "", false, true, true, 256 << 10},
	{http.MethodGet, "/api/v1/config/workspaces/{workspace_id}/files/search", "searchConfigFiles", "200", []string{"SearchResponse"}, "query", true, false, false, 0},
	{http.MethodGet, "/api/v1/config/workspaces/{workspace_id}/diff", "getConfigDiff", "200", []string{"DiffResponse"}, "path", false, false, false, 0},
	{http.MethodGet, "/api/v1/config/workspaces/{workspace_id}/structured-config", "getStructuredConfig", "200", []string{"StructuredConfig"}, "", false, false, true, 0},
	{http.MethodPost, "/api/v1/config/workspaces/{workspace_id}/structured-change-previews", "createStructuredChangePreview", "200", []string{"StructuredChangePreview"}, "", false, false, true, 256 << 10},
	{http.MethodPost, "/api/v1/config/workspaces/{workspace_id}/structured-changes", "applyStructuredChange", "200", []string{"StructuredChangeResult"}, "", false, true, true, 256 << 10},
	{http.MethodPost, "/api/v1/config/workspaces/{workspace_id}/publish-checks", "createConfigPublishCheck", "201", []string{"PublishCheck"}, "", false, true, false, 4 << 10},
	{http.MethodGet, "/api/v1/config/publish-checks/{check_id}", "getConfigPublishCheck", "200", []string{"PublishCheck"}, "", false, false, false, 0},
	{http.MethodPost, "/api/v1/config/workspaces/{workspace_id}/releases", "createConfigRelease", "202", []string{"Release"}, "", false, true, false, 4 << 10},
	{http.MethodGet, "/api/v1/config/releases/{release_id}", "getConfigRelease", "200", []string{"Release"}, "", false, false, false, 0},
	{http.MethodGet, "/api/v1/config/groups", "listConfigGroups", "200", []string{"GroupCollection"}, "workspace_id", false, false, true, 0},
	{http.MethodPost, "/api/v1/config/groups", "createConfigGroup", "201", []string{"GroupCollection"}, "", false, true, true, 256 << 10},
	{http.MethodPut, "/api/v1/config/groups/{group_id}", "replaceConfigGroup", "200", []string{"GroupCollection"}, "", false, true, true, 256 << 10},
	{http.MethodDelete, "/api/v1/config/groups/{group_id}", "deleteConfigGroup", "200", []string{"GroupCollection"}, "", false, true, true, 256 << 10},
}

var recoveryContractOperations = []recoveryContractOperation{
	{http.MethodGet, "/api/v1/config/history/releases", "listConfigReleaseHistory", "200", "ReleaseHistoryPage", "application/json", []string{"cursor", "limit"}, 0, false, false},
	{http.MethodGet, "/api/v1/config/history/restores", "listConfigRestoreHistory", "200", "RestoreHistoryPage", "application/json", []string{"cursor", "limit"}, 0, false, false},
	{http.MethodGet, "/api/v1/config/history/restarts", "listNginxRestartHistory", "200", "RestartHistoryPage", "application/json", []string{"cursor", "limit"}, 0, false, false},
	{http.MethodGet, "/api/v1/config/backups", "listConfigBackups", "200", "BackupPage", "application/json", []string{"cursor", "limit", "include_deleted"}, 0, false, false},
	{http.MethodGet, "/api/v1/config/backups/{backup_id}", "getConfigBackup", "200", "ConfigBackup", "application/json", nil, 0, false, false},
	{http.MethodPut, "/api/v1/config/backups/{backup_id}/protection", "changeConfigBackupProtection", "200", "ConfigBackup", "application/json", nil, 4 << 10, false, false},
	{http.MethodPost, "/api/v1/config/backup-retention-runs", "createConfigBackupRetentionPlan", "201", "RetentionRun", "application/json", nil, 4 << 10, true, false},
	{http.MethodGet, "/api/v1/config/backup-retention-runs/{retention_id}", "getConfigBackupRetentionRun", "200", "RetentionRun", "application/json", nil, 0, false, false},
	{http.MethodPost, "/api/v1/config/backup-retention-runs/{retention_id}/executions", "executeConfigBackupRetentionRun", "202", "RetentionRun", "application/json", nil, 4 << 10, true, false},
	{http.MethodPost, "/api/v1/config/backups/{backup_id}/restores", "createConfigRestore", "202", "ConfigRestore", "application/json", nil, 4 << 10, true, false},
	{http.MethodGet, "/api/v1/config/restores/{restore_id}", "getConfigRestore", "200", "ConfigRestore", "application/json", nil, 0, false, false},
	{http.MethodGet, "/api/v1/config/restores/{restore_id}/events", "streamConfigRestoreEvents", "200", "", "text/event-stream", nil, 0, false, true},
	{http.MethodPost, "/api/v1/nginx/restarts", "createNginxRestart", "202", "NginxRestart", "application/json", nil, 4 << 10, true, false},
	{http.MethodGet, "/api/v1/nginx/restarts/{restart_id}", "getNginxRestart", "200", "NginxRestart", "application/json", nil, 0, false, false},
	{http.MethodGet, "/api/v1/nginx/restarts/{restart_id}/events", "streamNginxRestartEvents", "200", "", "text/event-stream", nil, 0, false, true},
	{http.MethodGet, "/api/v1/config/audit-events", "listConfigAuditEvents", "200", "AuditEventPage", "application/json", []string{"cursor", "limit"}, 0, false, false},
	{http.MethodGet, "/api/v1/config/attention-cases", "listConfigAttentionCases", "200", "AttentionCasePage", "application/json", []string{"state", "cursor", "limit"}, 0, false, false},
	{http.MethodGet, "/api/v1/config/attention-cases/{attention_id}", "getConfigAttentionCase", "200", "AttentionCase", "application/json", nil, 0, false, false},
	{http.MethodPost, "/api/v1/config/attention-cases/{attention_id}/verifications", "createConfigAttentionVerification", "201", "RuntimeVerification", "application/json", nil, 4 << 10, false, false},
}

var routeContractOperations = []routeContractOperation{
	{http.MethodPost, "/api/v1/config/workspaces/{workspace_id}/route-analyses", "createRouteAnalysis", "200", "RouteAnalysis", "application/json", nil, 128 << 10, false, false, true},
	{http.MethodPost, "/api/v1/config/workspaces/{workspace_id}/route-tests", "createRouteTest", "202", "RouteTestRun", "application/json", nil, 128 << 10, true, false, true},
	{http.MethodGet, "/api/v1/route-tests", "listRouteTests", "200", "RouteTestHistoryPage", "application/json", []string{"cursor", "limit", "state", "workspace_id"}, 0, false, false, false},
	{http.MethodGet, "/api/v1/route-tests/{run_id}", "getRouteTest", "200", "RouteTestRun", "application/json", nil, 0, false, false, false},
	{http.MethodGet, "/api/v1/route-tests/{run_id}/events", "streamRouteTestEvents", "200", "", "text/event-stream", nil, 0, false, true, false},
	{http.MethodPost, "/api/v1/route-tests/{run_id}/cancellations", "createRouteTestCancellation", "202", "RouteTestRun", "application/json", nil, 4 << 10, false, false, false},
}

var certificateContractOperations = []routeContractOperation{
	{http.MethodGet, "/api/v1/acme/directories", "listACMEDirectories", "200", "ACMEDirectoryCollection", "application/json", nil, 0, false, false, false},
	{http.MethodGet, "/api/v1/acme/accounts", "listACMEAccounts", "200", "ACMEAccountCollection", "application/json", nil, 0, false, false, false},
	{http.MethodPost, "/api/v1/acme/accounts", "createACMEAccount", "201", "ACMEAccount", "application/json", nil, 128 << 10, false, false, false},
	{http.MethodPost, "/api/v1/acme/account-imports", "importACMEAccount", "201", "ACMEAccount", "application/json", nil, 256 << 10, false, false, false},
	{http.MethodPost, "/api/v1/acme/accounts/{account_id}/deactivations", "deactivateACMEAccount", "200", "ACMEAccount", "application/json", nil, 128 << 10, false, false, false},
	{http.MethodGet, "/api/v1/certificate-dns-credentials", "listCertificateDNSCredentials", "200", "CertificateDNSCredentialCollection", "application/json", nil, 0, false, false, false},
	{http.MethodPost, "/api/v1/certificate-dns-credentials", "createCertificateDNSCredential", "201", "CertificateDNSCredential", "application/json", nil, 256 << 10, false, false, false},
	{http.MethodDelete, "/api/v1/certificate-dns-credentials/{credential_id}", "deleteCertificateDNSCredential", "204", "", "", nil, 128 << 10, false, false, false},
	{http.MethodGet, "/api/v1/certificate-server-candidates", "listCertificateServerCandidates", "200", "CertificateServerCandidateCollection", "application/json", nil, 0, false, false, false},
	{http.MethodPost, "/api/v1/certificate-order-plans", "createCertificateOrderPlan", "201", "CertificateOrderPlan", "application/json", nil, 128 << 10, false, false, false},
	{http.MethodGet, "/api/v1/certificate-order-plans/{plan_id}", "getCertificateOrderPlan", "200", "CertificateOrderPlan", "application/json", nil, 0, false, false, false},
	{http.MethodPost, "/api/v1/certificate-order-plans/{plan_id}/executions", "executeCertificateOrderPlan", "202", "CertificateTask", "application/json", nil, 128 << 10, true, false, false},
	{http.MethodGet, "/api/v1/certificate-tasks", "listCertificateTasks", "200", "CertificateTaskCollection", "application/json", []string{"limit"}, 0, false, false, false},
	{http.MethodGet, "/api/v1/certificate-tasks/{task_id}", "getCertificateTask", "200", "CertificateTask", "application/json", nil, 0, false, false, false},
	{http.MethodGet, "/api/v1/certificate-tasks/{task_id}/events", "streamCertificateTaskEvents", "200", "", "text/event-stream", nil, 0, false, true, false},
	{http.MethodPost, "/api/v1/certificate-tasks/{task_id}/cancellations", "cancelCertificateTask", "202", "CertificateTask", "application/json", nil, 128 << 10, false, false, false},
	{http.MethodGet, "/api/v1/certificates", "listCertificates", "200", "CertificateCollection", "application/json", []string{"limit"}, 0, false, false, false},
	{http.MethodGet, "/api/v1/certificates/{certificate_id}", "getCertificate", "200", "Certificate", "application/json", nil, 0, false, false, false},
	{http.MethodPost, "/api/v1/certificates/{certificate_id}/renewals", "renewCertificate", "202", "CertificateTask", "application/json", nil, 128 << 10, true, false, false},
	{http.MethodPut, "/api/v1/certificates/{certificate_id}/renewal-policy", "updateCertificateRenewalPolicy", "200", "Certificate", "application/json", nil, 128 << 10, false, false, false},
	{http.MethodPost, "/api/v1/certificates/{certificate_id}/binding-plans", "createCertificateBindingPlan", "201", "CertificateBindingPlan", "application/json", nil, 128 << 10, false, false, false},
	{http.MethodPost, "/api/v1/certificate-binding-plans/{plan_id}/executions", "executeCertificateBindingPlan", "202", "CertificateTask", "application/json", nil, 128 << 10, true, false, false},
	{http.MethodPost, "/api/v1/certificates/{certificate_id}/unbindings", "unbindCertificate", "200", "Certificate", "application/json", nil, 128 << 10, false, false, false},
	{http.MethodPost, "/api/v1/certificates/{certificate_id}/exports", "exportCertificate", "200", "CertificatePEM", "application/x-pem-file", nil, 128 << 10, false, false, false},
	{http.MethodDelete, "/api/v1/certificates/{certificate_id}", "deleteCertificate", "204", "", "", nil, 128 << 10, false, false, false},
}

func TestOpenAPIContract(t *testing.T) {
	contents, err := os.ReadFile("../../api/v1/openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi) error = %v", err)
	}
	var document openAPIContractDocument
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("yaml.Unmarshal(openapi) error = %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("openapi version = %q, want 3.1.0", document.OpenAPI)
	}
	type publicOperation struct {
		method string
		path   string
	}
	healthOperations := []publicOperation{
		{method: http.MethodGet, path: "/health/live"},
		{method: http.MethodGet, path: "/health/ready"},
	}
	businessOperations := []publicOperation{
		{method: http.MethodPost, path: "/api/v1/auth/session"},
		{method: http.MethodGet, path: "/api/v1/auth/session"},
		{method: http.MethodDelete, path: "/api/v1/auth/session"},
		{method: http.MethodGet, path: "/api/v1/system/status"},
		{method: http.MethodGet, path: "/api/v1/nginx/effective-config"},
	}
	for _, operation := range configContractOperations {
		businessOperations = append(businessOperations, publicOperation{method: operation.method, path: operation.path})
	}
	for _, operation := range recoveryContractOperations {
		businessOperations = append(businessOperations, publicOperation{method: operation.method, path: operation.path})
	}
	for _, operation := range routeContractOperations {
		businessOperations = append(businessOperations, publicOperation{method: operation.method, path: operation.path})
	}
	for _, operation := range certificateContractOperations {
		businessOperations = append(businessOperations, publicOperation{method: operation.method, path: operation.path})
	}
	businessOperations = append(businessOperations, publicOperation{method: http.MethodGet, path: "/api/v1/config/releases/{release_id}/events"})
	operations := make([]publicOperation, 0, len(healthOperations)+len(businessOperations))
	operations = append(operations, healthOperations...)
	operations = append(operations, businessOperations...)
	for _, operation := range operations {
		methods, exists := document.Paths[operation.path]
		if !exists {
			t.Errorf("OpenAPI missing path %s", operation.path)
			continue
		}
		if _, exists := methods[strings.ToLower(operation.method)]; !exists {
			t.Errorf("OpenAPI missing operation %s %s", operation.method, operation.path)
		}

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(operation.method, operation.path, nil)
		NewHandler(Dependencies{}).ServeHTTP(recorder, request)
		if recorder.Code == http.StatusNotFound || recorder.Code == http.StatusMethodNotAllowed {
			t.Errorf("registered handler missing %s %s: status %d", operation.method, operation.path, recorder.Code)
		}
	}

	openAPIBusinessCount := 0
	for path, methods := range document.Paths {
		if !strings.HasPrefix(path, "/api/v1/") {
			continue
		}
		for method := range methods {
			switch strings.ToUpper(method) {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				openAPIBusinessCount++
			}
		}
	}
	if got, want := openAPIBusinessCount, len(businessOperations); got != want {
		t.Fatalf("OpenAPI business operation count = %d, want registered contract count %d", got, want)
	}

	assertConfigOpenAPIContract(t, contents, document)
	assertRecoveryOpenAPIContract(t, document)
	assertRouteOpenAPIContract(t, document)
	assertCertificateOpenAPIContract(t, document)
}

func assertConfigOpenAPIContract(t *testing.T, contents []byte, document openAPIContractDocument) {
	t.Helper()
	for _, expected := range configContractOperations {
		operation, exists := document.Paths[expected.path][strings.ToLower(expected.method)]
		if !exists {
			t.Errorf("config contract missing %s %s", expected.method, expected.path)
			continue
		}
		if operation.OperationID != expected.operationID {
			t.Errorf("%s %s operationId = %q, want %q", expected.method, expected.path, operation.OperationID, expected.operationID)
		}
		if !hasSessionSecurity(operation.Security) {
			t.Errorf("%s %s missing sessionCookie security", expected.method, expected.path)
		}
		parameters := resolveParameters(document.Components.Parameters, operation.Parameters)
		assertRequestIDParameter(t, expected, parameters)
		if expected.method != http.MethodGet {
			assertHeaderParameter(t, expected, parameters, "Origin", true)
			assertHeaderParameter(t, expected, parameters, "X-CSRF-Token", true)
		}
		if expected.ifMatch {
			assertHeaderParameter(t, expected, parameters, "If-Match", true)
		}
		assertQueryContract(t, expected, parameters)
		assertRequestBodyContract(t, expected, operation.RequestBody)
		assertSuccessContract(t, expected, operation.Responses[expected.successStatus])
		assertErrorContract(t, expected, operation.Responses)
	}

	assertConfigSchemas(t, document.Components.Schemas)
	var pathsOnly struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(contents, &pathsOnly); err != nil {
		t.Fatalf("yaml.Unmarshal(config paths) error = %v", err)
	}
	for path, operations := range pathsOnly.Paths {
		if !strings.HasPrefix(path, "/api/v1/config/") {
			continue
		}
		for method, operation := range operations {
			serialized, err := yaml.Marshal(operation)
			if err != nil {
				t.Fatalf("yaml.Marshal(%s %s) error = %v", method, path, err)
			}
			lower := strings.ToLower(string(serialized))
			for _, forbidden := range []string{"shell", "absolute_path", "nginx_args", "arbitrary_command"} {
				if strings.Contains(lower, forbidden) {
					t.Errorf("config operation %s %s exposes forbidden term %q", method, path, forbidden)
				}
			}
		}
	}
}

func assertRecoveryOpenAPIContract(t *testing.T, document openAPIContractDocument) {
	t.Helper()
	for _, expected := range recoveryContractOperations {
		operation, exists := document.Paths[expected.path][strings.ToLower(expected.method)]
		if !exists {
			t.Errorf("recovery contract missing %s %s", expected.method, expected.path)
			continue
		}
		if operation.OperationID != expected.operationID {
			t.Errorf("%s %s operationId = %q, want %q", expected.method, expected.path, operation.OperationID, expected.operationID)
		}
		if !hasSessionSecurity(operation.Security) {
			t.Errorf("%s %s missing sessionCookie security", expected.method, expected.path)
		}
		parameters := resolveParameters(document.Components.Parameters, operation.Parameters)
		assertRecoveryHeader(t, expected, parameters, "X-Request-ID", false)
		if expected.method != http.MethodGet {
			assertRecoveryHeader(t, expected, parameters, "Origin", true)
			assertRecoveryHeader(t, expected, parameters, "X-CSRF-Token", true)
		}
		if expected.lastEventID {
			assertRecoveryHeader(t, expected, parameters, "Last-Event-ID", false)
		}
		assertRecoveryQueries(t, expected, parameters)
		assertRecoveryBody(t, expected, operation.RequestBody)
		assertRecoverySuccess(t, expected, operation.Responses[expected.successStatus])
		assertRecoveryErrors(t, expected, operation.Responses)
	}
	assertRecoverySchemas(t, document.Components.Schemas)
}

func assertRouteOpenAPIContract(t *testing.T, document openAPIContractDocument) {
	t.Helper()
	for _, expected := range routeContractOperations {
		operation, exists := document.Paths[expected.path][strings.ToLower(expected.method)]
		if !exists {
			t.Errorf("route contract missing %s %s", expected.method, expected.path)
			continue
		}
		if operation.OperationID != expected.operationID {
			t.Errorf("%s %s operationId = %q, want %q", expected.method, expected.path, operation.OperationID, expected.operationID)
		}
		if !hasSessionSecurity(operation.Security) {
			t.Errorf("%s %s missing sessionCookie security", expected.method, expected.path)
		}
		parameters := resolveParameters(document.Components.Parameters, operation.Parameters)
		assertRouteHeader(t, expected, parameters, "X-Request-ID", false)
		if expected.method != http.MethodGet {
			assertRouteHeader(t, expected, parameters, "Origin", true)
			assertRouteHeader(t, expected, parameters, "X-CSRF-Token", true)
		}
		if expected.ifMatch {
			assertRouteHeader(t, expected, parameters, "If-Match", true)
		}
		if expected.lastEventID {
			assertRouteHeader(t, expected, parameters, "Last-Event-ID", false)
		}
		assertRouteQueries(t, expected, parameters)
		assertRouteBody(t, expected, operation.RequestBody)
		assertRouteSuccess(t, expected, operation.Responses[expected.successStatus])
		assertRouteErrors(t, expected, operation.Responses)
	}
	assertRouteSchemas(t, document.Components.Schemas)
}

func assertCertificateOpenAPIContract(t *testing.T, document openAPIContractDocument) {
	t.Helper()
	for _, expected := range certificateContractOperations {
		operation, exists := document.Paths[expected.path][strings.ToLower(expected.method)]
		if !exists {
			t.Errorf("certificate contract missing %s %s", expected.method, expected.path)
			continue
		}
		if operation.OperationID != expected.operationID {
			t.Errorf("%s %s operationId = %q, want %q", expected.method, expected.path, operation.OperationID, expected.operationID)
		}
		if !hasSessionSecurity(operation.Security) {
			t.Errorf("%s %s missing sessionCookie security", expected.method, expected.path)
		}
		parameters := resolveParameters(document.Components.Parameters, operation.Parameters)
		assertRouteHeader(t, expected, parameters, "X-Request-ID", false)
		if expected.method != http.MethodGet {
			assertRouteHeader(t, expected, parameters, "Origin", true)
			assertRouteHeader(t, expected, parameters, "X-CSRF-Token", true)
		}
		if expected.lastEventID {
			assertRouteHeader(t, expected, parameters, "Last-Event-ID", false)
		}
		assertRouteQueries(t, expected, parameters)
		assertRouteBody(t, expected, operation.RequestBody)
		response, exists := operation.Responses[expected.successStatus]
		switch {
		case !exists:
			t.Errorf("%s %s missing success status %s", expected.method, expected.path, expected.successStatus)
		case expected.successStatus == "204":
			if len(response.Content) != 0 {
				t.Errorf("%s %s 204 response must not document a body", expected.method, expected.path)
			}
			assertCertificateResponseHeaders(t, expected, response)
		default:
			assertRouteSuccess(t, expected, response)
		}
		assertRouteErrors(t, expected, operation.Responses)
	}
	assertCertificateSchemas(t, document.Components.Schemas)
}

func assertCertificateResponseHeaders(
	t *testing.T,
	expected routeContractOperation,
	response openAPIResponse,
) {
	t.Helper()
	cache := response.Headers["Cache-Control"].Schema
	if schemaType(cache) != "string" || !slices.Contains(cache.Enum, "no-store") {
		t.Errorf("%s %s success response does not guarantee Cache-Control no-store", expected.method, expected.path)
	}
	if _, exists := response.Headers["X-Request-ID"]; !exists {
		t.Errorf("%s %s success response does not document X-Request-ID", expected.method, expected.path)
	}
}

func assertCertificateSchemas(t *testing.T, schemas map[string]openAPISchema) {
	t.Helper()
	for _, name := range []string{
		"ACMEDirectory", "ACMEDirectoryCollection", "ACMEAccount", "ACMEAccountCollection",
		"CreateACMEAccountRequest", "ImportACMEAccountRequest", "CertificateDNSCredential",
		"CertificateDNSCredentialCollection", "CreateCertificateDNSCredentialRequest", "CertificateServerRef",
		"CertificateServerCandidate", "CertificateServerCandidateCollection", "CertificateBindingDiff",
		"CreateCertificateOrderPlanRequest", "CertificateOrderPlan", "ExecuteCertificateOrderPlanRequest",
		"CertificateTaskStage", "CertificateTask", "CertificateTaskCollection", "CertificateVersion",
		"CertificateBinding", "Certificate", "CertificateCollection", "CertificateRenewalRequest",
		"CertificateRenewalPolicyRequest", "CreateCertificateBindingPlanRequest", "CertificateBindingPlan",
		"CertificateConfirmationRequest", "CertificateExportRequest", "CertificatePEM",
	} {
		if _, exists := schemas[name]; !exists {
			t.Errorf("OpenAPI missing certificate schema %s", name)
		}
	}
	for name, propertyAndMaximum := range map[string]struct {
		property string
		maximum  int
	}{
		"ACMEDirectoryCollection":              {property: "directories", maximum: 2},
		"ACMEAccountCollection":                {property: "accounts", maximum: 100},
		"CertificateDNSCredentialCollection":   {property: "credentials", maximum: 100},
		"CertificateServerCandidateCollection": {property: "candidates", maximum: 100},
		"CertificateTaskCollection":            {property: "tasks", maximum: 100},
		"CertificateCollection":                {property: "certificates", maximum: 100},
		"CertificateTask":                      {property: "stages", maximum: 512},
	} {
		property := schemas[name].Properties[propertyAndMaximum.property]
		if schemaType(property) != "array" || property.MinItems == nil || property.MaxItems == nil ||
			*property.MinItems != 0 || *property.MaxItems != propertyAndMaximum.maximum {
			t.Errorf("%s.%s must be bounded 0..%d", name, propertyAndMaximum.property, propertyAndMaximum.maximum)
		}
	}
	if token := schemas["CreateCertificateDNSCredentialRequest"].Properties["api_token"]; !token.WriteOnly {
		t.Error("CreateCertificateDNSCredentialRequest.api_token must be writeOnly")
	}
	if _, exists := schemas["CertificateDNSCredential"].Properties["api_token"]; exists {
		t.Error("CertificateDNSCredential must never expose api_token")
	}
	if key := schemas["ImportACMEAccountRequest"].Properties["private_key_pem"]; !key.WriteOnly {
		t.Error("ImportACMEAccountRequest.private_key_pem must be writeOnly")
	}
	for _, forbidden := range []string{"private_key_pem", "fullchain_pem", "api_token", "challenge_value"} {
		if _, exists := schemas["Certificate"].Properties[forbidden]; exists {
			t.Errorf("Certificate exposes forbidden secret field %s", forbidden)
		}
	}
	pem := schemas["CertificatePEM"]
	if schemaType(pem) != "string" || pem.Format != "binary" {
		t.Errorf("CertificatePEM = type %q format %q", schemaType(pem), pem.Format)
	}
	for _, code := range []string{
		"ACME_TERMS_REQUIRED", "ACME_ACCOUNT_INVALID", "ACME_ACCOUNT_DEACTIVATED",
		"ACME_STAGING_PREFLIGHT_REQUIRED", "ACME_RATE_LIMITED", "ACME_ORDER_FAILED",
		"CERTIFICATE_IDENTIFIER_INVALID", "CERTIFICATE_WILDCARD_REQUIRES_DNS",
		"CLOUDFLARE_TOKEN_INVALID", "CLOUDFLARE_PERMISSION_DENIED", "CLOUDFLARE_ZONE_NOT_FOUND",
		"CLOUDFLARE_UNAVAILABLE", "DNS_PROPAGATION_TIMEOUT", "CHALLENGE_CLEANUP_FAILED",
		"CERTIFICATE_KEY_MISMATCH", "CERTIFICATE_SAN_MISMATCH", "CERTIFICATE_FILE_INVALID",
		"CERTIFICATE_SERVER_NOT_FOUND", "CERTIFICATE_SERVER_AMBIGUOUS", "CERTIFICATE_BINDING_CONFLICT",
		"CERTIFICATE_REFERENCED", "CERTIFICATE_TASK_ACTIVE", "CERTIFICATE_PLAN_EXPIRED",
		"CERTIFICATE_NEEDS_ATTENTION", "CERTIFICATE_PRIVATE_KEY_CONFIRMATION_REQUIRED",
		"CERTIFICATE_RENEWAL_POLICY_INVALID", "CERTIFICATE_OPERATION_TIMEOUT",
		"CERTIFICATE_RESOURCE_NOT_FOUND", "CERTIFICATE_SERVICE_UNAVAILABLE", "CERTIFICATE_LIMIT_EXCEEDED",
		"CERTIFICATE_REQUEST_INVALID",
	} {
		if !slices.Contains(schemas["ConfigErrorCode"].Enum, code) {
			t.Errorf("ConfigErrorCode missing %s", code)
		}
	}
}

func assertRouteHeader(
	t *testing.T,
	expected routeContractOperation,
	parameters []openAPIParameter,
	name string,
	required bool,
) {
	t.Helper()
	for _, parameter := range parameters {
		if parameter.In == "header" && parameter.Name == name {
			if parameter.Required != required {
				t.Errorf("%s %s header %s required = %v", expected.method, expected.path, name, parameter.Required)
			}
			return
		}
	}
	t.Errorf("%s %s missing header %s", expected.method, expected.path, name)
}

func assertRouteQueries(t *testing.T, expected routeContractOperation, parameters []openAPIParameter) {
	t.Helper()
	actual := make([]string, 0, len(expected.queries))
	for _, parameter := range parameters {
		if parameter.In != "query" {
			continue
		}
		actual = append(actual, parameter.Name)
		if parameter.Required || !strings.Contains(strings.ToLower(parameter.Description), "exactly once") {
			t.Errorf("%s %s query %s must be optional and document exactly-once cardinality", expected.method, expected.path, parameter.Name)
		}
	}
	slices.Sort(actual)
	wanted := slices.Clone(expected.queries)
	slices.Sort(wanted)
	if !slices.Equal(actual, wanted) {
		t.Errorf("%s %s query parameters = %v, want %v", expected.method, expected.path, actual, wanted)
	}
}

func assertRouteBody(t *testing.T, expected routeContractOperation, body *openAPIRequestBody) {
	t.Helper()
	if expected.bodyLimit == 0 {
		if body != nil {
			t.Errorf("%s %s documents unexpected request body", expected.method, expected.path)
		}
		return
	}
	if body == nil || !body.Required {
		t.Errorf("%s %s missing required strict request body", expected.method, expected.path)
		return
	}
	if !strings.Contains(body.Description, fmt.Sprintf("%d bytes", expected.bodyLimit)) {
		t.Errorf("%s %s body does not document %d-byte limit", expected.method, expected.path, expected.bodyLimit)
	}
	if body.Content["application/json"].Schema.Ref == "" {
		t.Errorf("%s %s request body does not use a reusable schema", expected.method, expected.path)
	}
}

func assertRouteSuccess(t *testing.T, expected routeContractOperation, response openAPIResponse) {
	t.Helper()
	if response.Ref != "" || len(response.Content) == 0 {
		t.Errorf("%s %s missing explicit success response", expected.method, expected.path)
		return
	}
	cache := response.Headers["Cache-Control"].Schema
	if schemaType(cache) != "string" || !slices.Contains(cache.Enum, "no-store") {
		t.Errorf("%s %s success response does not guarantee Cache-Control no-store", expected.method, expected.path)
	}
	if _, exists := response.Headers["X-Request-ID"]; !exists {
		t.Errorf("%s %s success response does not document X-Request-ID", expected.method, expected.path)
	}
	_, hasLocation := response.Headers["Location"]
	if hasLocation != expected.location {
		t.Errorf("%s %s Location present = %v, want %v", expected.method, expected.path, hasLocation, expected.location)
	}
	media, exists := response.Content[expected.mediaType]
	if !exists {
		t.Errorf("%s %s success response missing %s", expected.method, expected.path, expected.mediaType)
		return
	}
	if expected.successSchema != "" && media.Schema.Ref != "#/components/schemas/"+expected.successSchema {
		t.Errorf("%s %s success schema = %q, want %s", expected.method, expected.path, media.Schema.Ref, expected.successSchema)
	}
}

func assertRouteErrors(t *testing.T, expected routeContractOperation, responses map[string]openAPIResponse) {
	t.Helper()
	found := false
	for status, response := range responses {
		if strings.HasPrefix(status, "2") {
			continue
		}
		found = true
		if response.Ref != "#/components/responses/ConfigAPIError" {
			t.Errorf("%s %s response %s is not ConfigAPIError", expected.method, expected.path, status)
		}
	}
	if !found {
		t.Errorf("%s %s documents no stable error response", expected.method, expected.path)
	}
}

func assertRouteSchemas(t *testing.T, schemas map[string]openAPISchema) {
	t.Helper()
	for _, name := range []string{
		"RouteTestRequest", "RouteAnalysis", "RouteServerCandidate", "RouteLocationCandidate", "RouteSafeRequest",
		"RouteTestRun", "RouteTestHistoryPage", "RouteRunStage", "RouteTerminalResult", "RouteAgentResult",
		"RouteHTTPResponse", "RouteRuntimeEvidence", "RouteCleanupEvidence", "RouteAssertionOutcome",
	} {
		if _, exists := schemas[name]; !exists {
			t.Errorf("OpenAPI missing Route Lab schema %s", name)
		}
	}
	for name, maximum := range map[string]int{"RouteAnalysis.servers": 1000, "RouteAnalysis.locations": 5000, "RouteTestHistoryPage.runs": 100, "RouteTestRun.stages": 512} {
		parts := strings.Split(name, ".")
		property := schemas[parts[0]].Properties[parts[1]]
		if schemaType(property) != "array" || property.MinItems == nil || property.MaxItems == nil || *property.MinItems != 0 || *property.MaxItems != maximum {
			t.Errorf("%s must be bounded 0..%d", name, maximum)
		}
	}
	for _, code := range []string{
		"ROUTE_REQUEST_INVALID", "ROUTE_WORKSPACE_CONFLICT", "ROUTE_CONFIRMATION_REQUIRED", "ROUTE_PROJECT_INCOMPLETE",
		"ROUTE_LISTENER_AMBIGUOUS", "ROUTE_LAB_BUSY", "ROUTE_CANDIDATE_INVALID", "ROUTE_SANDBOX_START_FAILED",
		"ROUTE_REQUEST_TIMEOUT", "ROUTE_EVIDENCE_INCOMPLETE", "ROUTE_CLEANUP_FAILED", "ROUTE_ALREADY_TERMINAL",
		"ROUTE_LIMIT_EXCEEDED", "ROUTE_TEST_NOT_FOUND", "ROUTE_LAB_UNAVAILABLE",
	} {
		if !slices.Contains(schemas["ConfigErrorCode"].Enum, code) {
			t.Errorf("ConfigErrorCode missing %s", code)
		}
	}
}

func assertRecoveryHeader(
	t *testing.T,
	expected recoveryContractOperation,
	parameters []openAPIParameter,
	name string,
	required bool,
) {
	t.Helper()
	for _, parameter := range parameters {
		if parameter.In == "header" && parameter.Name == name {
			if parameter.Required != required {
				t.Errorf("%s %s header %s required = %v", expected.method, expected.path, name, parameter.Required)
			}
			return
		}
	}
	t.Errorf("%s %s missing header %s", expected.method, expected.path, name)
}

func assertRecoveryQueries(t *testing.T, expected recoveryContractOperation, parameters []openAPIParameter) {
	t.Helper()
	actual := make([]string, 0, len(expected.queries))
	for _, parameter := range parameters {
		if parameter.In != "query" {
			continue
		}
		actual = append(actual, parameter.Name)
		if parameter.Required || !strings.Contains(strings.ToLower(parameter.Description), "exactly once") {
			t.Errorf("%s %s query %s must be optional and document exactly-once cardinality", expected.method, expected.path, parameter.Name)
		}
	}
	slices.Sort(actual)
	wanted := slices.Clone(expected.queries)
	slices.Sort(wanted)
	if !slices.Equal(actual, wanted) {
		t.Errorf("%s %s query parameters = %v, want %v", expected.method, expected.path, actual, wanted)
	}
}

func assertRecoveryBody(t *testing.T, expected recoveryContractOperation, body *openAPIRequestBody) {
	t.Helper()
	if expected.bodyLimit == 0 {
		if body != nil {
			t.Errorf("%s %s documents unexpected request body", expected.method, expected.path)
		}
		return
	}
	if body == nil || !body.Required {
		t.Errorf("%s %s missing required strict request body", expected.method, expected.path)
		return
	}
	if !strings.Contains(body.Description, fmt.Sprintf("%d bytes", expected.bodyLimit)) {
		t.Errorf("%s %s body does not document %d-byte limit", expected.method, expected.path, expected.bodyLimit)
	}
	if body.Content["application/json"].Schema.Ref == "" {
		t.Errorf("%s %s request body does not use a reusable schema", expected.method, expected.path)
	}
}

func assertRecoverySuccess(t *testing.T, expected recoveryContractOperation, response openAPIResponse) {
	t.Helper()
	if response.Ref != "" || len(response.Content) == 0 {
		t.Errorf("%s %s missing explicit success response", expected.method, expected.path)
		return
	}
	cache := response.Headers["Cache-Control"].Schema
	if schemaType(cache) != "string" || !slices.Contains(cache.Enum, "no-store") {
		t.Errorf("%s %s success response does not guarantee Cache-Control no-store", expected.method, expected.path)
	}
	if _, exists := response.Headers["X-Request-ID"]; !exists {
		t.Errorf("%s %s success response does not document X-Request-ID", expected.method, expected.path)
	}
	_, hasLocation := response.Headers["Location"]
	if hasLocation != expected.location {
		t.Errorf("%s %s Location present = %v, want %v", expected.method, expected.path, hasLocation, expected.location)
	}
	media, exists := response.Content[expected.mediaType]
	if !exists {
		t.Errorf("%s %s success response missing %s", expected.method, expected.path, expected.mediaType)
		return
	}
	if expected.successSchema != "" && media.Schema.Ref != "#/components/schemas/"+expected.successSchema {
		t.Errorf("%s %s success schema = %q, want %s", expected.method, expected.path, media.Schema.Ref, expected.successSchema)
	}
}

func assertRecoveryErrors(t *testing.T, expected recoveryContractOperation, responses map[string]openAPIResponse) {
	t.Helper()
	found := false
	for status, response := range responses {
		if strings.HasPrefix(status, "2") {
			continue
		}
		found = true
		if response.Ref != "#/components/responses/ConfigAPIError" {
			t.Errorf("%s %s response %s is not ConfigAPIError", expected.method, expected.path, status)
		}
	}
	if !found {
		t.Errorf("%s %s documents no stable error response", expected.method, expected.path)
	}
}

func assertRecoverySchemas(t *testing.T, schemas map[string]openAPISchema) {
	t.Helper()
	for _, name := range []string{
		"ReleaseHistoryPage", "RestoreHistoryPage", "RestartHistoryPage", "BackupPage", "ConfigBackup",
		"BackupProtectionRequest", "RetentionExecutionRequest", "RetentionRun", "RetentionItem", "RetentionPolicy",
		"RestoreRequest", "ConfigRestore", "RestoreStage", "RestartRequest", "NginxRestart", "RestartStage",
		"AuditEventPage", "AuditEvent", "AttentionCasePage", "AttentionCase", "RuntimeVerification",
	} {
		if _, exists := schemas[name]; !exists {
			t.Errorf("OpenAPI missing recovery schema %s", name)
		}
	}
	for _, name := range []string{
		"ReleaseHistoryPage", "RestoreHistoryPage", "RestartHistoryPage", "BackupPage", "AuditEventPage", "AttentionCasePage",
	} {
		items := schemas[name].Properties["items"]
		if schemaType(items) != "array" || items.MinItems == nil || items.MaxItems == nil ||
			*items.MinItems != 0 || *items.MaxItems != recoveryMaximumPageLimit {
			t.Errorf("%s.items must be bounded 0..%d", name, recoveryMaximumPageLimit)
		}
	}
	for _, code := range []string{
		"CONFIG_OPERATION_IN_PROGRESS", "CONFIG_BACKUP_PROTECTED", "CONFIG_RETENTION_PLAN_EXPIRED",
		"CONFIG_ATTENTION_UNRESOLVED", "CONFIG_BACKUP_TARGET_INVALID", "CONFIG_RESTORE_NEEDS_ATTENTION",
		"NGINX_RESTART_CONFIG_INVALID", "NGINX_RESTART_FAILED", "NGINX_RESTART_NEEDS_ATTENTION",
		"CONFIG_BACKUP_NOT_FOUND", "CONFIG_RETENTION_RUN_NOT_FOUND", "CONFIG_RESTORE_NOT_FOUND",
		"NGINX_RESTART_NOT_FOUND", "CONFIG_ATTENTION_CASE_NOT_FOUND",
	} {
		if !slices.Contains(schemas["ConfigErrorCode"].Enum, code) {
			t.Errorf("ConfigErrorCode missing %s", code)
		}
	}
	wantRestoreStages := []string{
		"queued", "target_verifying", "target_validated", "safety_backup_creating", "safety_backup_verified",
		"files_restoring", "files_restored", "production_validated", "reload_requested", "runtime_confirmed",
		"succeeded", "rollback_applying", "rollback_files_restored", "rollback_validated",
		"rollback_reload_requested", "rolled_back", "failed", "needs_attention",
	}
	for _, name := range []string{"ConfigRestore", "RestoreStage"} {
		if stage := schemas[name].Properties["stage"]; !slices.Equal(stage.Enum, wantRestoreStages) {
			t.Errorf("%s.stage enum = %v", name, stage.Enum)
		}
	}
	wantRestartStages := []string{
		"queued", "production_validating", "runtime_sampling", "restart_requested", "runtime_confirming",
		"succeeded", "failed", "needs_attention",
	}
	for _, name := range []string{"NginxRestart", "RestartStage"} {
		if stage := schemas[name].Properties["stage"]; !slices.Equal(stage.Enum, wantRestartStages) {
			t.Errorf("%s.stage enum = %v", name, stage.Enum)
		}
	}
}

func resolveParameters(components map[string]openAPIParameter, raw []openAPIParameter) []openAPIParameter {
	resolved := make([]openAPIParameter, 0, len(raw))
	for _, parameter := range raw {
		if parameter.Ref != "" {
			const prefix = "#/components/parameters/"
			if strings.HasPrefix(parameter.Ref, prefix) {
				parameter = components[strings.TrimPrefix(parameter.Ref, prefix)]
			}
		}
		resolved = append(resolved, parameter)
	}
	return resolved
}

func hasSessionSecurity(security []map[string][]string) bool {
	for _, requirement := range security {
		if scopes, exists := requirement["sessionCookie"]; exists && len(scopes) == 0 {
			return true
		}
	}
	return false
}

func assertRequestIDParameter(t *testing.T, expected contractOperation, parameters []openAPIParameter) {
	t.Helper()
	assertHeaderParameter(t, expected, parameters, "X-Request-ID", false)
}

func assertHeaderParameter(t *testing.T, expected contractOperation, parameters []openAPIParameter, name string, required bool) {
	t.Helper()
	for _, parameter := range parameters {
		if parameter.Name == name && parameter.In == "header" {
			if parameter.Required != required {
				t.Errorf("%s %s parameter %s required = %v, want %v", expected.method, expected.path, name, parameter.Required, required)
			}
			return
		}
	}
	t.Errorf("%s %s missing header %s parameter", expected.method, expected.path, name)
}

func assertQueryContract(t *testing.T, expected contractOperation, parameters []openAPIParameter) {
	t.Helper()
	queries := make([]openAPIParameter, 0, 1)
	for _, parameter := range parameters {
		if parameter.In == "query" {
			queries = append(queries, parameter)
		}
	}
	if expected.query == "" {
		if len(queries) != 0 {
			t.Errorf("%s %s documents unexpected query parameters", expected.method, expected.path)
		}
		return
	}
	if len(queries) != 1 || queries[0].Name != expected.query || queries[0].Required != expected.queryRequired {
		t.Errorf("%s %s query contract = %#v, want exactly %q required=%v", expected.method, expected.path, queries, expected.query, expected.queryRequired)
		return
	}
	if !strings.Contains(strings.ToLower(queries[0].Description), "exactly once") {
		t.Errorf("%s %s query %s does not document exactly-once cardinality", expected.method, expected.path, expected.query)
	}
}

func assertRequestBodyContract(t *testing.T, expected contractOperation, body *openAPIRequestBody) {
	t.Helper()
	if expected.bodyLimitBytes == 0 {
		if body != nil {
			t.Errorf("%s %s documents unexpected request body", expected.method, expected.path)
		}
		return
	}
	if body == nil || !body.Required {
		t.Errorf("%s %s missing required request body", expected.method, expected.path)
		return
	}
	want := fmt.Sprintf("%d bytes", expected.bodyLimitBytes)
	if !strings.Contains(body.Description, want) {
		t.Errorf("%s %s body description = %q, want limit %q", expected.method, expected.path, body.Description, want)
	}
	if schema := body.Content["application/json"].Schema; schema.Ref == "" {
		t.Errorf("%s %s request body is not a reusable schema", expected.method, expected.path)
	}
}

func assertSuccessContract(t *testing.T, expected contractOperation, response openAPIResponse) {
	t.Helper()
	if response.Ref != "" || (len(response.Content) == 0 && expected.successStatus != "204") {
		t.Errorf("%s %s missing explicit %s success response", expected.method, expected.path, expected.successStatus)
		return
	}
	cache := response.Headers["Cache-Control"].Schema
	if schemaType(cache) != "string" || !slices.Contains(cache.Enum, "no-store") {
		t.Errorf("%s %s success response does not guarantee Cache-Control no-store", expected.method, expected.path)
	}
	if _, exists := response.Headers["X-Request-ID"]; !exists {
		t.Errorf("%s %s success response does not document X-Request-ID", expected.method, expected.path)
	}
	_, hasETag := response.Headers["ETag"]
	if hasETag != expected.etag {
		t.Errorf("%s %s ETag response header present = %v, want %v", expected.method, expected.path, hasETag, expected.etag)
	}
	if expected.successStatus == "204" {
		if len(response.Content) != 0 {
			t.Errorf("%s %s 204 response documents a body", expected.method, expected.path)
		}
		return
	}
	schema := response.Content["application/json"].Schema
	refs := schemaRefs(schema)
	for _, wanted := range expected.successSchemas {
		if !slices.Contains(refs, "#/components/schemas/"+wanted) {
			t.Errorf("%s %s success schema refs = %v, missing %s", expected.method, expected.path, refs, wanted)
		}
	}
}

func schemaRefs(schema openAPISchema) []string {
	refs := make([]string, 0, len(schema.OneOf)+1)
	if schema.Ref != "" {
		refs = append(refs, schema.Ref)
	}
	for _, item := range schema.OneOf {
		if item.Ref != "" {
			refs = append(refs, item.Ref)
		}
	}
	return refs
}

func assertErrorContract(t *testing.T, expected contractOperation, responses map[string]openAPIResponse) {
	t.Helper()
	seen := false
	for status, response := range responses {
		if strings.HasPrefix(status, "2") {
			continue
		}
		seen = true
		if expected.operationID == "createConfigPublishCheck" && status == "422" {
			if !slices.Contains(schemaRefs(response.Content["application/json"].Schema), "#/components/schemas/PublishCheck") {
				t.Errorf("%s %s response 422 does not expose persisted publish-check evidence", expected.method, expected.path)
			}
			cache := response.Headers["Cache-Control"].Schema
			if schemaType(cache) != "string" || !slices.Contains(cache.Enum, "no-store") {
				t.Errorf("%s %s response 422 does not guarantee Cache-Control no-store", expected.method, expected.path)
			}
			continue
		}
		if response.Ref != "#/components/responses/ConfigAPIError" {
			t.Errorf("%s %s response %s is not the stable config error envelope", expected.method, expected.path, status)
		}
	}
	if !seen {
		t.Errorf("%s %s documents no stable error response", expected.method, expected.path)
	}
}

func assertConfigSchemas(t *testing.T, schemas map[string]openAPISchema) {
	t.Helper()
	requiredSchemas := []string{
		"WorkspaceSummary", "WorkspaceDetail", "WorkspaceList", "ConfigTree", "ConfigTreeNode", "ConfigDependency", "ConfigFile",
		"CreateWorkspaceRequest", "CreateFileRequest", "ReplaceFileRequest", "RenameFileRequest", "CopyFileRequest",
		"DeleteWorkspaceRequest", "DeleteFileRequest", "FileMutationResponse", "DiffResponse", "FileDiffSummary",
		"SearchResponse", "SearchMatch", "GroupCollection", "ConfigGroup", "GroupMutationRequest", "DeleteGroupRequest",
		"PublishCheck", "CandidateDiagnostic", "CreateReleaseRequest", "Release", "ReleaseStage",
		"StructuredConfig", "StructuredSource", "StructuredProjectDiagnostic", "StructuredDiagnostic",
		"StructuredHTTPBlock", "StructuredUpstream", "StructuredUpstreamServer", "StructuredEndpoint", "StructuredReference",
		"StructuredHTTPServer", "StructuredLocation", "PreservedSyntax", "StructuredOperationRequest",
		"StructuredApplyRequest", "StructuredChangePreview", "StructuredChangedFile", "StructuredChangeResult",
		"APIErrorEnvelope", "ConfigErrorCode",
	}
	for _, name := range requiredSchemas {
		if _, exists := schemas[name]; !exists {
			t.Errorf("OpenAPI missing config schema %s", name)
		}
	}
	for _, code := range []string{
		"CONFIG_PATH_INVALID", "CONFIG_ENTRY_NOT_MANAGED", "CONFIG_LIMIT_EXCEEDED", "CONFIG_WORKSPACE_NOT_FOUND",
		"CONFIG_PUBLISH_CHECK_NOT_FOUND", "CONFIG_RELEASE_NOT_FOUND",
		"CONFIG_WORKSPACE_CONFLICT", "CONFIG_WORKSPACE_STALE", "CONFIG_WORKSPACE_NEEDS_ATTENTION", "CONFIG_SNAPSHOT_CHANGED",
		"CONFIG_PRODUCTION_CHANGED", "CONFIG_BACKUP_INVALID", "NGINX_HEALTH_UNAVAILABLE", "CONFIG_RELEASE_NEEDS_ATTENTION",
		"AGENT_UNAVAILABLE", "CONFIG_OPERATION_TIMEOUT",
		"CONFIG_CANDIDATE_INVALID", "CONFIG_NO_CHANGES", "CONFIG_PUBLISH_CHECK_EXPIRED", "CONFIG_PUBLISH_IN_PROGRESS",
		"STRUCTURED_PARSE_FAILED", "STRUCTURED_LIMIT_EXCEEDED", "STRUCTURED_PREVIEW_STALE",
		"STRUCTURED_CONTEXT_AMBIGUOUS", "STRUCTURED_EDIT_CONFLICT", "UPSTREAM_INVALID",
		"UPSTREAM_DUPLICATE", "UPSTREAM_REFERENCED", "UPSTREAM_REFERENCE_INCOMPLETE",
		"LOCATION_INVALID", "LOCATION_DUPLICATE", "PROXY_PASS_INVALID",
	} {
		if !slices.Contains(schemas["ConfigErrorCode"].Enum, code) {
			t.Errorf("ConfigErrorCode missing %s", code)
		}
	}
	wantReleaseStages := []string{
		"queued", "rechecking", "backup_creating", "backup_verified", "candidate_validated", "files_applying",
		"files_applied", "production_validated", "reload_requested", "runtime_confirmed", "committed",
		"rollback_applying", "rollback_files_restored", "rollback_validated", "rollback_reload_requested",
		"rolled_back", "failed", "needs_attention",
	}
	for _, name := range []string{"Release", "ReleaseStage"} {
		stage := schemas[name].Properties["stage"]
		if schemaType(stage) != "string" || !slices.Equal(stage.Enum, wantReleaseStages) {
			t.Errorf("%s.stage enum = %v", name, stage.Enum)
		}
	}
	for _, name := range []string{"WorkspaceList", "ConfigTree", "DiffResponse", "SearchResponse", "GroupCollection", "ConfigGroup", "GroupMutationRequest"} {
		assertRequiredArraysBounded(t, name, schemas[name])
	}
	for _, name := range []string{"WorkspaceSummary", "WorkspaceDetail"} {
		assertStringSchemaProperty(t, name, schemas[name], "id", "^[0-9a-f]{32}$", "")
		assertStringSchemaProperty(t, name, schemas[name], "production_digest", "^[0-9a-f]{64}$", "")
		assertStringSchemaProperty(t, name, schemas[name], "base_digest", "^[0-9a-f]{64}$", "")
		assertStringSchemaProperty(t, name, schemas[name], "draft_etag", `^"draft-v1:[0-9a-f]{64}"$`, "")
		assertStringSchemaProperty(t, name, schemas[name], "created_at", "", "date-time")
		assertStringSchemaProperty(t, name, schemas[name], "updated_at", "", "date-time")
	}
	for _, name := range []string{"ConfigTreeNode", "ConfigFile", "FileDiffSummary", "SearchMatch"} {
		assertRelativePathSchema(t, name, schemas[name].Properties["path"])
	}
	assertRelativePathSchema(t, "ConfigDependency.source", schemas["ConfigDependency"].Properties["source"])
	assertRelativePathSchema(t, "ConfigDependency.target", schemas["ConfigDependency"].Properties["target"])
	for _, name := range []string{"CreateFileRequest", "RenameFileRequest", "CopyFileRequest", "DeleteFileRequest"} {
		for property, schema := range schemas[name].Properties {
			if strings.Contains(property, "path") {
				assertRelativePathSchema(t, name+"."+property, schema)
			}
		}
	}
	for _, name := range []string{"ConfigGroup", "GroupMutationRequest"} {
		assertRelativePathSchema(t, name+".members[]", *schemas[name].Properties["members"].Items)
	}
	assertRelativePathSchema(t, "ConfigGroup.missing[]", *schemas["ConfigGroup"].Properties["missing"].Items)
	assertStringSchemaProperty(t, "ConfigFile", schemas["ConfigFile"], "content_digest", "^[0-9a-f]{64}$", "")
	treeNode := schemas["ConfigTreeNode"]
	diffStatus, exists := treeNode.Properties["diff_status"]
	if !exists {
		t.Error("ConfigTreeNode missing optional diff_status")
	} else {
		if schemaType(diffStatus) != "string" || !slices.Equal(diffStatus.Enum, []string{"unchanged", "created", "modified", "deleted"}) {
			t.Errorf("ConfigTreeNode.diff_status = type %q enum %v", schemaType(diffStatus), diffStatus.Enum)
		}
		if slices.Contains(treeNode.Required, "diff_status") {
			t.Error("ConfigTreeNode.diff_status must be optional")
		}
	}
	if _, exists := schemas["ConfigTreeNode"].Properties["content"]; exists {
		t.Error("ConfigTreeNode must not expose file content")
	}
	if slices.Contains(treeNode.Required, "content_digest") {
		t.Error("ConfigTreeNode content_digest must be optional because sensitive nodes omit it")
	}
	if !strings.Contains(strings.ToLower(treeNode.Properties["content_digest"].Description), "omitted") {
		t.Error("ConfigTreeNode content_digest must document omission for sensitive nodes")
	}
	if _, exists := schemas["ConfigFile"].Properties["content"]; !exists {
		t.Error("ConfigFile must expose content for explicit file GET")
	}
}

func assertRequiredArraysBounded(t *testing.T, name string, schema openAPISchema) {
	t.Helper()
	for property, value := range schema.Properties {
		if schemaType(value) != "array" {
			continue
		}
		if !slices.Contains(schema.Required, property) {
			t.Errorf("%s.%s array is not required", name, property)
		}
		if value.MinItems == nil || value.MaxItems == nil || *value.MinItems != 0 || *value.MaxItems < 1 {
			t.Errorf("%s.%s array bounds = %v..%v, want 0..positive", name, property, value.MinItems, value.MaxItems)
		}
	}
}

func assertStringSchemaProperty(t *testing.T, name string, schema openAPISchema, property, pattern, format string) {
	t.Helper()
	value, exists := schema.Properties[property]
	if !exists {
		t.Errorf("%s missing property %s", name, property)
		return
	}
	if actualType := schemaTypeOf(value); actualType != "string" || value.Pattern != pattern || value.Format != format {
		t.Errorf("%s.%s = type %q pattern %q format %q", name, property, value.Type, value.Pattern, value.Format)
	}
}

func assertRelativePathSchema(t *testing.T, name string, schema openAPISchema) {
	t.Helper()
	if schemaType(schema) != "string" || !strings.Contains(schema.Description, "1024 UTF-8 bytes") {
		t.Errorf("%s does not document the 1024-byte relative path limit", name)
	}
}

func schemaType(schema openAPISchema) string {
	return schemaTypeOf(schema)
}

func schemaTypeOf(schema openAPISchema) string {
	value, _ := schema.Type.(string)
	return value
}

func TestBusinessAPIRouteContract(t *testing.T) {
	type operation struct {
		method string
		path   string
	}
	operations := []operation{
		{http.MethodGet, "/api/v1/config/workspaces"},
		{http.MethodPost, "/api/v1/config/workspaces"},
		{http.MethodGet, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef"},
		{http.MethodDelete, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef"},
		{http.MethodGet, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/files"},
		{http.MethodPost, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/files"},
		{http.MethodPut, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/files"},
		{http.MethodPatch, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/files"},
		{http.MethodDelete, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/files"},
		{http.MethodPost, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/files/copies"},
		{http.MethodGet, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/files/search"},
		{http.MethodGet, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/diff"},
		{http.MethodGet, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/structured-config"},
		{http.MethodPost, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/structured-change-previews"},
		{http.MethodPost, "/api/v1/config/workspaces/0123456789abcdef0123456789abcdef/structured-changes"},
		{http.MethodGet, "/api/v1/config/groups"},
		{http.MethodPost, "/api/v1/config/groups"},
		{http.MethodPut, "/api/v1/config/groups/0123456789abcdef0123456789abcdef"},
		{http.MethodDelete, "/api/v1/config/groups/0123456789abcdef0123456789abcdef"},
	}
	handler := NewHandler(Dependencies{})
	for _, operation := range operations {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(operation.method, operation.path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusNotFound || recorder.Code == http.StatusMethodNotAllowed {
			t.Errorf("registered handler missing %s %s: status %d", operation.method, operation.path, recorder.Code)
		}
	}
}
