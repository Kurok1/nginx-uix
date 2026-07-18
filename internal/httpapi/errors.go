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
	"strings"

	"github.com/kuroky/nginx-uix/internal/config"
	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

const (
	errorCodeAgentUnavailable = "AGENT_UNAVAILABLE"
	errorCodeConfigInvalid    = "NGINX_CONFIG_INVALID"
	errorCodeCommandTimeout   = "NGINX_COMMAND_TIMEOUT"
	errorCodeOutputTooLarge   = "NGINX_OUTPUT_TOO_LARGE"
)

var errAgentUnavailable = errors.New("agent unavailable")

var configErrorDetails = map[string]map[string]struct{}{
	"CONFIG_PATH_INVALID":       {"path": {}, "field": {}},
	"CONFIG_ENTRY_NOT_MANAGED":  {"path": {}},
	"CONFIG_LIMIT_EXCEEDED":     {"limit_name": {}, "limit_value": {}, "actual": {}},
	"CONFIG_WORKSPACE_CONFLICT": {"current_etag": {}, "field": {}, "path": {}},
}

var safeDetailFields = map[string]struct{}{
	"body": {}, "confirm_name": {}, "confirm_path": {}, "content": {}, "destination_path": {},
	"group_id": {}, "members": {}, "name": {}, "path": {}, "query": {}, "source_path": {}, "workspace_id": {},
	"username": {},
}

var safeLimitNames = map[string]struct{}{
	"request_body_bytes": {}, "file_bytes": {}, "entries": {}, "managed_bytes": {}, "workspaces": {},
	"workspace_bytes": {}, "groups": {}, "group_members": {}, "total_group_members": {},
	"diff_response_bytes": {}, "search_matches": {}, "search_query_bytes": {}, "include_token_bytes": {},
	"include_directive_bytes": {}, "include_edges": {}, "include_depth": {},
}

// ErrorEnvelope is the stable public API error shape.
type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

// APIError contains a stable code, safe message and request correlation ID.
type APIError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Details   map[string]any `json:"details,omitempty"`
}

func writeAPIError(
	writer http.ResponseWriter,
	requestID string,
	status int,
	code string,
	message string,
	details map[string]any,
) {
	envelope := ErrorEnvelope{Error: APIError{
		Code: code, Message: message, RequestID: requestID, Details: whitelistDetails(code, details),
	}}
	writeJSON(writer, status, envelope)
}

func whitelistDetails(code string, details map[string]any) map[string]any {
	allowed := map[string]map[string]struct{}{
		"invalid_request": {"field": {}},
		"rate_limited":    {"retry_after_seconds": {}},
	}
	for configCode, keys := range configErrorDetails {
		allowed[configCode] = keys
	}
	keys, ok := allowed[code]
	if !ok || len(details) == 0 {
		return nil
	}
	result := make(map[string]any, len(keys))
	for key := range keys {
		value, exists := details[key]
		if !exists {
			continue
		}
		if safe, ok := safeDetailValue(key, value); ok {
			result[key] = safe
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func safeDetailValue(key string, value any) (any, bool) {
	switch key {
	case "path":
		raw, ok := value.(string)
		if !ok {
			return nil, false
		}
		parsed, err := config.ParseRelativePath(raw, config.DefaultLimits())
		return raw, err == nil && string(parsed) == raw
	case "current_etag":
		raw, ok := value.(string)
		if !ok || strings.Contains(raw, ",") {
			return nil, false
		}
		if _, err := config.ParseStrongETag(raw, "draft-v1:"); err == nil {
			return raw, true
		}
		if _, err := config.ParseStrongETag(raw, "groups-v1:"); err == nil {
			return raw, true
		}
		return nil, false
	case "field":
		raw, ok := value.(string)
		_, allowed := safeDetailFields[raw]
		return raw, ok && allowed
	case "limit_name":
		raw, ok := value.(string)
		_, allowed := safeLimitNames[raw]
		return raw, ok && allowed
	case "retry_after_seconds", "limit_value", "actual":
		switch number := value.(type) {
		case int:
			return number, number >= 0
		case int64:
			return number, number >= 0
		case float64:
			return number, number >= 0
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func writeConfigAPIError(writer http.ResponseWriter, request *http.Request, err error, details map[string]any) {
	requestID := requestIDFromContext(request.Context())
	var conflict *config.ConflictError
	var agentProtocol *nginxruntime.AgentProtocolError
	switch {
	case errors.Is(err, config.ErrPathInvalid), errors.Is(err, config.ErrIdentifierInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CONFIG_PATH_INVALID", "配置路径无效", details)
	case errors.Is(err, config.ErrEntryNotManaged):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CONFIG_ENTRY_NOT_MANAGED", "该配置条目不可管理", details)
	case errors.Is(err, config.ErrLimitExceeded), errors.Is(err, nginxruntime.ErrOutputTooLarge):
		writeAPIError(writer, requestID, http.StatusRequestEntityTooLarge, "CONFIG_LIMIT_EXCEEDED", "配置操作超过安全限制", details)
	case errors.Is(err, fs.ErrNotExist):
		writeAPIError(writer, requestID, http.StatusNotFound, "CONFIG_WORKSPACE_NOT_FOUND", "配置工作区不存在", nil)
	case errors.As(err, &conflict):
		writeAPIError(writer, requestID, http.StatusConflict, "CONFIG_WORKSPACE_CONFLICT", "配置工作区已变化", map[string]any{"current_etag": conflict.CurrentETag})
	case errors.Is(err, config.ErrSnapshotChanged):
		writeAPIError(writer, requestID, http.StatusConflict, "CONFIG_SNAPSHOT_CHANGED", "生产配置在快照期间发生变化", nil)
	case errors.Is(err, config.ErrConflict), errors.Is(err, fs.ErrExist):
		writeAPIError(writer, requestID, http.StatusConflict, "CONFIG_WORKSPACE_CONFLICT", "配置工作区已变化", details)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, nginxruntime.ErrCommandTimeout):
		writeAPIError(writer, requestID, http.StatusGatewayTimeout, "CONFIG_OPERATION_TIMEOUT", "配置操作超时", nil)
	case errors.As(err, &agentProtocol):
		writeAPIError(writer, requestID, http.StatusServiceUnavailable, "AGENT_UNAVAILABLE", "本地 Agent 暂时不可用", nil)
	default:
		writeAPIError(writer, requestID, http.StatusInternalServerError, "internal_error", "服务暂时不可用", nil)
	}
}

func writeAgentAPIError(writer http.ResponseWriter, requestID string, err error) {
	status, code, message := classifyAgentAPIError(err)
	writeAPIError(writer, requestID, status, code, message, nil)
}

func classifyAgentAPIError(err error) (int, string, string) {
	switch {
	case errors.Is(err, nginxruntime.ErrConfigInvalid):
		return http.StatusUnprocessableEntity, errorCodeConfigInvalid, "Nginx 配置无法通过检查"
	case errors.Is(err, nginxruntime.ErrCommandTimeout), errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, errorCodeCommandTimeout, "Nginx 命令执行超时"
	case errors.Is(err, nginxruntime.ErrOutputTooLarge):
		return http.StatusBadGateway, errorCodeOutputTooLarge, "Nginx 输出超过安全限制"
	default:
		return http.StatusServiceUnavailable, errorCodeAgentUnavailable, "本地 Agent 暂时不可用"
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		payload = []byte(`{"error":{"code":"internal_error","message":"服务暂时不可用","request_id":"unknown"}}`)
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	// The response may already be disconnected; the server records transport errors.
	_, _ = writer.Write(append(payload, '\n'))
}
