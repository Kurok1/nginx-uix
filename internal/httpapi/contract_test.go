/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIParsesAndContainsEveryPublicOperation(t *testing.T) {
	contents, err := os.ReadFile("../../api/v1/openapi.yaml")
	if err != nil {
		t.Fatalf("ReadFile(openapi) error = %v", err)
	}
	var document struct {
		OpenAPI string                          `yaml:"openapi"`
		Paths   map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("yaml.Unmarshal(openapi) error = %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("openapi version = %q, want 3.1.0", document.OpenAPI)
	}
	type operation struct {
		method string
		path   string
	}
	healthOperations := []operation{
		{method: http.MethodGet, path: "/health/live"},
		{method: http.MethodGet, path: "/health/ready"},
	}
	businessOperations := []operation{
		{method: http.MethodPost, path: "/api/v1/auth/session"},
		{method: http.MethodGet, path: "/api/v1/auth/session"},
		{method: http.MethodDelete, path: "/api/v1/auth/session"},
		{method: http.MethodGet, path: "/api/v1/system/status"},
		{method: http.MethodGet, path: "/api/v1/nginx/effective-config"},
	}
	if got, want := len(businessOperations), 5; got != want {
		t.Fatalf("business route contract count = %d, want %d", got, want)
	}
	operations := make([]operation, 0, len(healthOperations)+len(businessOperations))
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
}
