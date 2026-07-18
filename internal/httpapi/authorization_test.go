/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kuroky/nginx-uix/internal/auth"
)

func TestAuthorizeBusinessMutationRequiresSessionOriginAndCSRF(t *testing.T) {
	issued := testIssuedSession()
	sessions := &authorizationSessionStub{issued: issued}
	request := mutationAuthorizationRequest(issued)
	recorder := httptest.NewRecorder()
	actor, ok := authorizeBusinessMutation(recorder, request, sessions, nil)
	if !ok || actor.UserID != issued.User.ID || actor.RequestID != "request-1" {
		t.Fatalf("actor = %#v, ok = %v", actor, ok)
	}
	if sessions.currentCalls != 1 || sessions.verifyCalls != 1 {
		t.Fatalf("current/verify calls = %d/%d, want 1/1", sessions.currentCalls, sessions.verifyCalls)
	}
}

func TestAuthorizeBusinessMutationRejectsEachBoundaryInOrder(t *testing.T) {
	issued := testIssuedSession()
	publicURL, err := url.Parse("https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		edit         func(*http.Request, *authorizationSessionStub)
		code         string
		currentCalls int
		verifyCalls  int
	}{
		{name: "missing cookie", edit: func(request *http.Request, _ *authorizationSessionStub) { request.Header.Del("Cookie") }, code: "unauthenticated"},
		{name: "expired session", edit: func(_ *http.Request, sessions *authorizationSessionStub) {
			sessions.currentErr = auth.ErrUnauthenticated
		}, code: "unauthenticated", currentCalls: 1},
		{name: "missing origin", edit: func(request *http.Request, _ *authorizationSessionStub) { request.Header.Del("Origin") }, code: "origin_rejected", currentCalls: 1},
		{name: "duplicate origin", edit: func(request *http.Request, _ *authorizationSessionStub) {
			request.Header.Add("Origin", "http://example.test")
		}, code: "origin_rejected", currentCalls: 1},
		{name: "public URL mismatch", edit: func(_ *http.Request, _ *authorizationSessionStub) {}, code: "origin_rejected", currentCalls: 1},
		{name: "missing csrf", edit: func(request *http.Request, _ *authorizationSessionStub) { request.Header.Del(csrfHeaderName) }, code: "csrf_rejected", currentCalls: 1},
		{name: "invalid csrf", edit: func(_ *http.Request, sessions *authorizationSessionStub) { sessions.verifyErr = auth.ErrInvalidCSRF }, code: "csrf_rejected", currentCalls: 1, verifyCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessions := &authorizationSessionStub{issued: issued}
			request := mutationAuthorizationRequest(issued)
			test.edit(request, sessions)
			trusted := (*url.URL)(nil)
			if test.name == "public URL mismatch" {
				trusted = publicURL
			}
			recorder := httptest.NewRecorder()
			if _, ok := authorizeBusinessMutation(recorder, request, sessions, trusted); ok {
				t.Fatal("authorizeBusinessMutation() ok = true")
			}
			var envelope ErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != test.code {
				t.Fatalf("code = %q, want %q", envelope.Error.Code, test.code)
			}
			if sessions.currentCalls != test.currentCalls || sessions.verifyCalls != test.verifyCalls {
				t.Fatalf("current/verify = %d/%d, want %d/%d", sessions.currentCalls, sessions.verifyCalls, test.currentCalls, test.verifyCalls)
			}
		})
	}
}

func mutationAuthorizationRequest(issued auth.IssuedSession) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://example.test/api/v1/config/workspaces", strings.NewReader(`{"name":"review"}`))
	request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, "request-1"))
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set(csrfHeaderName, issued.CSRFToken)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	return request
}

type authorizationSessionStub struct {
	issued       auth.IssuedSession
	currentErr   error
	verifyErr    error
	currentCalls int
	verifyCalls  int
}

func (*authorizationSessionStub) Login(context.Context, auth.LoginInput) (auth.IssuedSession, error) {
	return auth.IssuedSession{}, errors.New("unexpected login")
}

func (s *authorizationSessionStub) Current(context.Context, string) (auth.IssuedSession, error) {
	s.currentCalls++
	return s.issued, s.currentErr
}

func (s *authorizationSessionStub) VerifyCSRF(context.Context, string, string) error {
	s.verifyCalls++
	return s.verifyErr
}

func (*authorizationSessionStub) Logout(context.Context, string) error {
	return errors.New("unexpected logout")
}
