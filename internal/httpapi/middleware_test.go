/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestBoundaryReplacesMalformedRequestIDAndAddsSecurityHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("X-Request-ID", "bad request id\n")
	handler := NewHandler(Dependencies{RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{0x2a}, 64))})

	handler.ServeHTTP(recorder, request)

	if got, want := recorder.Header().Get("X-Request-ID"), strings.Repeat("2a", 16); got != want {
		t.Fatalf("X-Request-ID = %q, want generated %q", got, want)
	}
	for header, want := range map[string]string{
		"Content-Security-Policy": "default-src 'self'; style-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Permissions-Policy":      "camera=(), microphone=(), geolocation=()",
	} {
		if got := recorder.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestRequestBoundaryKeepsSecurityHeadersWhenRequestIDGenerationFails(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	NewHandler(Dependencies{RequestIDSource: failingReader{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	for _, header := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy"} {
		if recorder.Header().Get(header) == "" {
			t.Errorf("%s is absent on request-ID failure", header)
		}
	}
}

func TestRequestBoundaryPreservesValidRequestID(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("X-Request-ID", "request_123.test-ok")
	NewHandler(Dependencies{}).ServeHTTP(recorder, request)
	if got, want := recorder.Header().Get("X-Request-ID"), "request_123.test-ok"; got != want {
		t.Fatalf("X-Request-ID = %q, want %q", got, want)
	}
}

func TestRequestBoundaryLogsOnlyRedactedRequestMetadata(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := requestBoundary(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}), logger, newRequestIDGenerator(nil))

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/session?token=query-secret",
		strings.NewReader(`{"password":"body-secret"}`),
	)
	request.Header.Set("X-Request-ID", "request-redaction")
	request.Header.Set("Authorization", "Bearer authorization-secret")
	request.Header.Set("Cookie", "nginx_uix_session=cookie-secret")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	logged := logs.String()
	for _, secret := range []string{
		"query-secret",
		"body-secret",
		"authorization-secret",
		"cookie-secret",
	} {
		if strings.Contains(logged, secret) {
			t.Errorf("request log contains secret %q: %s", secret, logged)
		}
	}
	for _, metadata := range []string{
		"POST /api/v1/auth/session",
		"request-redaction",
		"result=204",
	} {
		if !strings.Contains(logged, metadata) {
			t.Errorf("request log %q does not contain metadata %q", logged, metadata)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("random source unavailable")
}
