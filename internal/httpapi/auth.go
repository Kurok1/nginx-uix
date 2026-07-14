/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
)

const (
	sessionCookieName = "nginx_uix_session"
	csrfHeaderName    = "X-CSRF-Token"
	loginBodyLimit    = 4096
)

// SessionService is the authentication behavior consumed by the HTTP boundary.
type SessionService interface {
	Login(ctx context.Context, input auth.LoginInput) (auth.IssuedSession, error)
	Current(ctx context.Context, rawToken string) (auth.IssuedSession, error)
	VerifyCSRF(ctx context.Context, rawToken, csrfToken string) error
	Logout(ctx context.Context, rawToken string) error
}

type sessionHandler struct {
	service   SessionService
	publicURL *url.URL
}

type loginRequest struct {
	Username string
	Password string
}

type sessionResponse struct {
	User              sessionUser `json:"user"`
	CSRFToken         string      `json:"csrf_token"`
	CreatedAt         time.Time   `json:"created_at"`
	LastSeenAt        time.Time   `json:"last_seen_at"`
	IdleExpiresAt     time.Time   `json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time   `json:"absolute_expires_at"`
}

type sessionUser struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *sessionHandler) login(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	requestID := requestIDFromContext(request.Context())
	if h.service == nil {
		writeAPIError(writer, requestID, http.StatusServiceUnavailable, "service_unavailable", "服务暂时不可用", nil)
		return
	}
	if !originMatches(request, h.publicURL) {
		writeAPIError(writer, requestID, http.StatusForbidden, "origin_rejected", "请求来源不受信任", nil)
		return
	}
	input, status, err := decodeLoginRequest(request)
	if err != nil {
		code := "invalid_request"
		message := "请求格式无效"
		switch status {
		case http.StatusUnsupportedMediaType:
			code, message = "unsupported_media_type", "仅接受 application/json"
		case http.StatusRequestEntityTooLarge:
			code, message = "request_too_large", "请求体过大"
		}
		writeAPIError(writer, requestID, status, code, message, nil)
		return
	}
	peer, err := directPeerAddress(request.RemoteAddr)
	if err != nil {
		writeAPIError(writer, requestID, http.StatusBadRequest, "invalid_request", "无法识别请求来源", nil)
		return
	}
	issued, err := h.service.Login(request.Context(), auth.LoginInput{
		Username: input.Username, Password: input.Password, SourceIP: peer,
	})
	if err != nil {
		h.writeAuthError(writer, requestID, err)
		return
	}
	h.setSessionCookie(writer, issued.Token)
	writeJSON(writer, http.StatusOK, toSessionResponse(issued))
}

func (h *sessionHandler) current(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	requestID := requestIDFromContext(request.Context())
	rawToken, ok := readSessionCookie(request)
	if !ok || h.service == nil {
		writeAPIError(writer, requestID, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
		return
	}
	issued, err := h.service.Current(request.Context(), rawToken)
	if err != nil {
		h.writeAuthError(writer, requestID, err)
		return
	}
	writeJSON(writer, http.StatusOK, toSessionResponse(issued))
}

func (h *sessionHandler) logout(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	requestID := requestIDFromContext(request.Context())
	if h.service == nil {
		writeAPIError(writer, requestID, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
		return
	}
	if !originMatches(request, h.publicURL) {
		writeAPIError(writer, requestID, http.StatusForbidden, "origin_rejected", "请求来源不受信任", nil)
		return
	}
	rawToken, ok := readSessionCookie(request)
	if !ok {
		writeAPIError(writer, requestID, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
		return
	}
	csrfToken := request.Header.Get(csrfHeaderName)
	if csrfToken == "" {
		writeAPIError(writer, requestID, http.StatusForbidden, "csrf_rejected", "CSRF 校验失败", nil)
		return
	}
	if err := h.service.VerifyCSRF(request.Context(), rawToken, csrfToken); err != nil {
		h.writeAuthError(writer, requestID, err)
		return
	}
	if err := h.service.Logout(request.Context(), rawToken); err != nil {
		h.writeAuthError(writer, requestID, err)
		return
	}
	h.clearSessionCookie(writer)
	writer.WriteHeader(http.StatusNoContent)
}

func (h *sessionHandler) writeAuthError(writer http.ResponseWriter, requestID string, err error) {
	var rateLimit *auth.RateLimitError
	switch {
	case errors.As(err, &rateLimit):
		seconds := int64((rateLimit.RetryAfter + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		writeAPIError(writer, requestID, http.StatusTooManyRequests, "rate_limited", "登录尝试过于频繁", map[string]any{"retry_after_seconds": seconds})
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeAPIError(writer, requestID, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误", nil)
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrNotFound):
		writeAPIError(writer, requestID, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
	case errors.Is(err, auth.ErrInvalidCSRF):
		writeAPIError(writer, requestID, http.StatusForbidden, "csrf_rejected", "CSRF 校验失败", nil)
	default:
		writeAPIError(writer, requestID, http.StatusInternalServerError, "internal_error", "服务暂时不可用", nil)
	}
}

func (h *sessionHandler) setSessionCookie(writer http.ResponseWriter, rawToken string) {
	secure := h.publicURL != nil && h.publicURL.Scheme == "https"
	http.SetCookie(writer, &http.Cookie{
		Name: sessionCookieName, Value: rawToken, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

func (h *sessionHandler) clearSessionCookie(writer http.ResponseWriter) {
	secure := h.publicURL != nil && h.publicURL.Scheme == "https"
	http.SetCookie(writer, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
		MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
}

func decodeLoginRequest(request *http.Request) (loginRequest, int, error) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return loginRequest{}, http.StatusUnsupportedMediaType, fmt.Errorf("validate login content type")
	}
	payload, err := io.ReadAll(io.LimitReader(request.Body, loginBodyLimit+1))
	if err != nil {
		return loginRequest{}, http.StatusBadRequest, fmt.Errorf("read login request: %w", err)
	}
	if len(payload) > loginBodyLimit {
		return loginRequest{}, http.StatusRequestEntityTooLarge, fmt.Errorf("read login request: body limit exceeded")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request: object required")
	}
	seen := make(map[string]struct{}, 2)
	var input loginRequest
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request: string key required")
		}
		if _, exists := seen[key]; exists {
			return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request: duplicate field")
		}
		seen[key] = struct{}{}
		switch key {
		case "username":
			err = decoder.Decode(&input.Username)
		case "password":
			err = decoder.Decode(&input.Password)
		default:
			return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request: unknown field")
		}
		if err != nil {
			return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request value: %w", err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request object: %w", err)
	}
	if decoder.More() {
		return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request: trailing value")
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request: trailing data")
	}
	if input.Username == "" || input.Password == "" {
		return loginRequest{}, http.StatusBadRequest, fmt.Errorf("decode login request: required fields missing")
	}
	return input, http.StatusOK, nil
}

func directPeerAddress(remoteAddress string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse remote address: %w", err)
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse remote IP: %w", err)
	}
	return address.Unmap(), nil
}

func readSessionCookie(request *http.Request) (string, bool) {
	var value string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == sessionCookieName {
			value = cookie.Value
			count++
		}
	}
	return value, count == 1 && value != ""
}

func toSessionResponse(session auth.IssuedSession) sessionResponse {
	return sessionResponse{
		User:      sessionUser{ID: session.User.ID, Username: session.User.Username, CreatedAt: session.User.CreatedAt},
		CSRFToken: session.CSRFToken, CreatedAt: session.CreatedAt, LastSeenAt: session.LastSeenAt,
		IdleExpiresAt: session.IdleExpiresAt, AbsoluteExpiresAt: session.AbsoluteExpiresAt,
	}
}
