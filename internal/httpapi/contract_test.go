/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
	{http.MethodGet, "/api/v1/config/groups", "listConfigGroups", "200", []string{"GroupCollection"}, "workspace_id", false, false, true, 0},
	{http.MethodPost, "/api/v1/config/groups", "createConfigGroup", "201", []string{"GroupCollection"}, "", false, true, true, 256 << 10},
	{http.MethodPut, "/api/v1/config/groups/{group_id}", "replaceConfigGroup", "200", []string{"GroupCollection"}, "", false, true, true, 256 << 10},
	{http.MethodDelete, "/api/v1/config/groups/{group_id}", "deleteConfigGroup", "200", []string{"GroupCollection"}, "", false, true, true, 256 << 10},
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
			for _, forbidden := range []string{"publish", "reload", "restart", "restore", "shell", "absolute_path", "nginx_args"} {
				if strings.Contains(lower, forbidden) {
					t.Errorf("config operation %s %s exposes forbidden term %q", method, path, forbidden)
				}
			}
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
		"APIErrorEnvelope", "ConfigErrorCode",
	}
	for _, name := range requiredSchemas {
		if _, exists := schemas[name]; !exists {
			t.Errorf("OpenAPI missing config schema %s", name)
		}
	}
	for _, code := range []string{
		"CONFIG_PATH_INVALID", "CONFIG_ENTRY_NOT_MANAGED", "CONFIG_LIMIT_EXCEEDED", "CONFIG_WORKSPACE_NOT_FOUND",
		"CONFIG_WORKSPACE_CONFLICT", "CONFIG_WORKSPACE_STALE", "CONFIG_WORKSPACE_NEEDS_ATTENTION", "CONFIG_SNAPSHOT_CHANGED",
		"AGENT_UNAVAILABLE", "CONFIG_OPERATION_TIMEOUT",
	} {
		if !slices.Contains(schemas["ConfigErrorCode"].Enum, code) {
			t.Errorf("ConfigErrorCode missing %s", code)
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
