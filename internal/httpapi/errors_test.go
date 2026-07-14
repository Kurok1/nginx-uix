/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorResponseWhitelistsDetails(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeAPIError(recorder, "request-id", http.StatusBadRequest, "invalid_request", "请求无效", map[string]any{
		"field": "username", "internal_path": "/var/lib/private", "password": "secret",
	})

	var envelope ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got, want := envelope.Error.Details["field"], "username"; got != want {
		t.Fatalf("details.field = %v, want %v", got, want)
	}
	if _, exists := envelope.Error.Details["internal_path"]; exists {
		t.Fatal("details expose internal_path")
	}
	if _, exists := envelope.Error.Details["password"]; exists {
		t.Fatal("details expose password")
	}
}
