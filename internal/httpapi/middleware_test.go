/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"bytes"
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
		"Content-Security-Policy": "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'",
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

func TestRequestBoundaryPreservesValidRequestID(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("X-Request-ID", "request_123.test-ok")
	NewHandler(Dependencies{}).ServeHTTP(recorder, request)
	if got, want := recorder.Header().Get("X-Request-ID"), "request_123.test-ok"; got != want {
		t.Fatalf("X-Request-ID = %q, want %q", got, want)
	}
}
