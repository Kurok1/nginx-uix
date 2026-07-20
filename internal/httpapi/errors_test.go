/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kuroky/nginx-uix/internal/config"
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

func TestConfigErrorResponseMapsStableCodesAndStatuses(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "path", err: config.ErrPathInvalid, status: http.StatusUnprocessableEntity, code: "CONFIG_PATH_INVALID"},
		{name: "entry", err: config.ErrEntryNotManaged, status: http.StatusUnprocessableEntity, code: "CONFIG_ENTRY_NOT_MANAGED"},
		{name: "limit", err: config.ErrLimitExceeded, status: http.StatusRequestEntityTooLarge, code: "CONFIG_LIMIT_EXCEEDED"},
		{name: "missing", err: fs.ErrNotExist, status: http.StatusNotFound, code: "CONFIG_WORKSPACE_NOT_FOUND"},
		{name: "conflict", err: config.ErrConflict, status: http.StatusConflict, code: "CONFIG_WORKSPACE_CONFLICT"},
		{name: "workspace stale", err: config.ErrWorkspaceStale, status: http.StatusConflict, code: "CONFIG_WORKSPACE_STALE"},
		{name: "workspace needs attention", err: config.ErrWorkspaceNeedsAttention, status: http.StatusConflict, code: "CONFIG_WORKSPACE_NEEDS_ATTENTION"},
		{name: "production changed", err: config.ErrProductionChanged, status: http.StatusConflict, code: "CONFIG_PRODUCTION_CHANGED"},
		{name: "snapshot", err: config.ErrSnapshotChanged, status: http.StatusConflict, code: "CONFIG_SNAPSHOT_CHANGED"},
		{name: "candidate", err: config.ErrCandidateInvalid, status: http.StatusUnprocessableEntity, code: "CONFIG_CANDIDATE_INVALID"},
		{name: "expired check", err: config.ErrPublishCheckExpired, status: http.StatusConflict, code: "CONFIG_PUBLISH_CHECK_EXPIRED"},
		{name: "release active", err: config.ErrReleaseInProgress, status: http.StatusConflict, code: "CONFIG_PUBLISH_IN_PROGRESS"},
		{name: "timeout", err: context.DeadlineExceeded, status: http.StatusGatewayTimeout, code: "CONFIG_OPERATION_TIMEOUT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/config/workspaces", nil)
			request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, "request-1"))
			writeConfigAPIError(recorder, request, errors.Join(errors.New("private /var/lib/workspace detail"), test.err), nil)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			var envelope ErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != test.code {
				t.Fatalf("code = %q, want %q", envelope.Error.Code, test.code)
			}
		})
	}
}

func TestConfigErrorDetailsValidateValuesByMeaning(t *testing.T) {
	validETag := config.DraftETag(config.Digest{1})
	details := whitelistDetails("CONFIG_WORKSPACE_CONFLICT", map[string]any{
		"current_etag": validETag,
		"path":         "conf.d/site.conf",
		"field":        "path",
		"password":     "secret",
	})
	if details["current_etag"] != validETag || details["path"] != "conf.d/site.conf" || details["field"] != "path" {
		t.Fatalf("valid details = %#v", details)
	}
	for _, test := range []map[string]any{
		{"current_etag": `W/"draft-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`},
		{"path": "/etc/nginx/nginx.conf"},
		{"field": "password"},
	} {
		if got := whitelistDetails("CONFIG_WORKSPACE_CONFLICT", test); len(got) != 0 {
			t.Fatalf("unsafe details retained: %#v", got)
		}
	}
	if got := whitelistDetails("CONFIG_LIMIT_EXCEEDED", map[string]any{
		"limit_name": "request_body_bytes", "limit_value": int64(4096), "actual": int64(-1),
	}); got["limit_name"] != "request_body_bytes" || got["limit_value"] != int64(4096) || got["actual"] != nil {
		t.Fatalf("limit details = %#v", got)
	}
}
