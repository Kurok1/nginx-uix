/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
)

func TestSessionLoginRejectsHostileRequestsBeforeService(t *testing.T) {
	tests := []struct {
		name        string
		origin      string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "missing origin", contentType: "application/json", body: `{"username":"operator","password":"password-value"}`, wantStatus: http.StatusForbidden},
		{name: "mismatched origin", origin: "https://evil.example", contentType: "application/json", body: `{"username":"operator","password":"password-value"}`, wantStatus: http.StatusForbidden},
		{name: "wrong content type", origin: "https://admin.example.test", contentType: "text/plain", body: `{"username":"operator","password":"password-value"}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "unknown field", origin: "https://admin.example.test", contentType: "application/json", body: `{"username":"operator","password":"password-value","admin":true}`, wantStatus: http.StatusBadRequest},
		{name: "duplicate field", origin: "https://admin.example.test", contentType: "application/json", body: `{"username":"operator","username":"other","password":"password-value"}`, wantStatus: http.StatusBadRequest},
		{name: "oversized body", origin: "https://admin.example.test", contentType: "application/json", body: strings.Repeat("x", 4097), wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &recordingSessionService{}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", strings.NewReader(test.body))
			request.RemoteAddr = "192.0.2.10:32100"
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}

			newTestHandler(t, service).ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if service.loginCalls != 0 {
				t.Fatalf("Login() calls = %d, want 0", service.loginCalls)
			}
			assertErrorEnvelope(t, recorder.Body.Bytes())
		})
	}
}

func TestSessionLoginUsesRemoteAddressAndIgnoresForwardedHeaders(t *testing.T) {
	service := &recordingSessionService{loginResult: testIssuedSession()}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", strings.NewReader(`{"username":"operator","password":"password-value"}`))
	request.RemoteAddr = "192.0.2.44:32100"
	request.Header.Set("Origin", "https://admin.example.test")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("X-Forwarded-For", "203.0.113.99")
	request.Header.Set("X-Real-IP", "203.0.113.98")

	newTestHandler(t, service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if got, want := service.loginInput.SourceIP, netip.MustParseAddr("192.0.2.44"); got != want {
		t.Fatalf("Login() SourceIP = %s, want direct peer %s", got, want)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", recorder.Header().Get("Cache-Control"))
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || cookie.Value != service.loginResult.Token || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Errorf("session cookie = %+v, want secure strict HttpOnly cookie", cookie)
	}
	if !cookie.Expires.IsZero() || cookie.MaxAge != 0 {
		t.Errorf("issued cookie has persistence fields: Expires=%s MaxAge=%d", cookie.Expires, cookie.MaxAge)
	}
	if strings.Contains(recorder.Body.String(), service.loginResult.Token) {
		t.Fatal("login response exposes raw session token")
	}
}

func TestSessionReadAndDeleteRequireCookieAndCSRF(t *testing.T) {
	t.Run("GET missing cookie", func(t *testing.T) {
		service := &recordingSessionService{}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
		newTestHandler(t, service).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized || service.currentCalls != 0 {
			t.Fatalf("status/current calls = %d/%d, want 401/0", recorder.Code, service.currentCalls)
		}
	})

	t.Run("DELETE wrong CSRF", func(t *testing.T) {
		service := &recordingSessionService{verifyError: auth.ErrInvalidCSRF}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/session", nil)
		request.Header.Set("Origin", "https://admin.example.test")
		request.Header.Set(csrfHeaderName, "wrong-token")
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testIssuedSession().Token})
		newTestHandler(t, service).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden || service.logoutCalls != 0 {
			t.Fatalf("status/logout calls = %d/%d, want 403/0", recorder.Code, service.logoutCalls)
		}
	})

	t.Run("DELETE success clears browser cookie", func(t *testing.T) {
		service := &recordingSessionService{}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/session", nil)
		request.Header.Set("Origin", "https://admin.example.test")
		request.Header.Set(csrfHeaderName, "csrf-token")
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testIssuedSession().Token})
		newTestHandler(t, service).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent || service.verifyCalls != 1 || service.logoutCalls != 1 {
			t.Fatalf("status/verify/logout = %d/%d/%d, want 204/1/1", recorder.Code, service.verifyCalls, service.logoutCalls)
		}
		cookies := recorder.Result().Cookies()
		if len(cookies) != 1 || cookies[0].MaxAge >= 0 || !cookies[0].Expires.Before(time.Now()) {
			t.Fatalf("cleared cookie = %+v, want expired deletion cookie", cookies)
		}
	})
}

func TestSessionRateLimitReturnsIntegerRetryAfter(t *testing.T) {
	service := &recordingSessionService{loginError: &auth.RateLimitError{RetryAfter: 90*time.Second + 250*time.Millisecond}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", strings.NewReader(`{"username":"operator","password":"password-value"}`))
	request.RemoteAddr = "192.0.2.10:32100"
	request.Header.Set("Origin", "https://admin.example.test")
	request.Header.Set("Content-Type", "application/json")

	newTestHandler(t, service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if got, want := recorder.Header().Get("Retry-After"), "91"; got != want {
		t.Fatalf("Retry-After = %q, want %q", got, want)
	}
}

func TestRequestLoggingExcludesCredentialsCookiesAndBody(t *testing.T) {
	service := &recordingSessionService{loginResult: testIssuedSession()}
	publicURL, err := url.Parse("https://admin.example.test")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	var logs bytes.Buffer
	handler := NewHandler(Dependencies{
		Sessions: service, PublicURL: publicURL,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{0x2a}, 64)),
		Logger:          slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	password := "log-secret-password"
	rawBody := `{"username":"operator","password":"` + password + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", strings.NewReader(rawBody))
	request.RemoteAddr = "192.0.2.77:32100"
	request.Header.Set("Origin", "https://admin.example.test")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer authorization-secret")
	request.Header.Set("Cookie", "nginx_uix_session=cookie-secret")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	for _, secret := range []string{password, rawBody, "authorization-secret", "cookie-secret", service.loginResult.Token} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("request log exposes secret %q: %s", secret, logs.String())
		}
	}
}

func newTestHandler(t *testing.T, service SessionService) http.Handler {
	t.Helper()
	publicURL, err := url.Parse("https://admin.example.test")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return NewHandler(Dependencies{
		Sessions:        service,
		PublicURL:       publicURL,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{0x2a}, 1024)),
	})
}

func testIssuedSession() auth.IssuedSession {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	return auth.IssuedSession{
		User:      auth.User{ID: 7, Username: "Operator", NormalizedName: "operator", CreatedAt: now},
		Token:     "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI",
		CSRFToken: "csrf-token",
		CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(8 * time.Hour), AbsoluteExpiresAt: now.Add(24 * time.Hour),
	}
}

func assertErrorEnvelope(t *testing.T, body []byte) {
	t.Helper()
	var envelope ErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; body = %s", err, body)
	}
	if envelope.Error.Code == "" || envelope.Error.Message == "" || envelope.Error.RequestID == "" {
		t.Fatalf("incomplete error envelope: %+v", envelope)
	}
}

type recordingSessionService struct {
	loginCalls    int
	currentCalls  int
	verifyCalls   int
	logoutCalls   int
	loginInput    auth.LoginInput
	loginResult   auth.IssuedSession
	currentResult auth.IssuedSession
	loginError    error
	currentError  error
	verifyError   error
	logoutError   error
}

func (s *recordingSessionService) Login(_ context.Context, input auth.LoginInput) (auth.IssuedSession, error) {
	s.loginCalls++
	s.loginInput = input
	return s.loginResult, s.loginError
}

func (s *recordingSessionService) Current(context.Context, string) (auth.IssuedSession, error) {
	s.currentCalls++
	return s.currentResult, s.currentError
}

func (s *recordingSessionService) VerifyCSRF(context.Context, string, string) error {
	s.verifyCalls++
	return s.verifyError
}

func (s *recordingSessionService) Logout(context.Context, string) error {
	s.logoutCalls++
	return s.logoutError
}

var _ SessionService = (*recordingSessionService)(nil)
