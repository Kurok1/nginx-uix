/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"encoding/json"
	"net/http"
)

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
		switch value.(type) {
		case string, int, int64, float64:
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
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
