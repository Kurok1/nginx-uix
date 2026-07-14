/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"crypto/tls"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestOriginMatchesConfiguredPublicURL(t *testing.T) {
	publicURL, err := url.Parse("https://Admin.Example.Test/")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "same origin", origin: "https://admin.example.test", want: true},
		{name: "default HTTPS port", origin: "https://admin.example.test:443", want: true},
		{name: "missing", origin: "", want: false},
		{name: "wrong scheme", origin: "http://admin.example.test", want: false},
		{name: "wrong host", origin: "https://evil.example.test", want: false},
		{name: "userinfo", origin: "https://user@admin.example.test", want: false},
		{name: "path", origin: "https://admin.example.test/path", want: false},
		{name: "opaque null", origin: "null", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "http://internal/api/v1/auth/session", nil)
			request.Header.Set("Origin", test.origin)
			if got := originMatches(request, publicURL); got != test.want {
				t.Fatalf("originMatches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestOriginMatchesActualRequestWithoutForwardedScheme(t *testing.T) {
	request := httptest.NewRequest("DELETE", "https://admin.example.test/api/v1/auth/session", nil)
	request.TLS = &tls.ConnectionState{}
	request.Host = "admin.example.test"
	request.Header.Set("Origin", "https://admin.example.test")
	request.Header.Set("X-Forwarded-Proto", "http")
	if !originMatches(request, nil) {
		t.Fatal("originMatches() = false, want direct TLS and Host origin")
	}

	request.Header.Set("Origin", "http://admin.example.test")
	if originMatches(request, nil) {
		t.Fatal("originMatches() trusted X-Forwarded-Proto")
	}
}
