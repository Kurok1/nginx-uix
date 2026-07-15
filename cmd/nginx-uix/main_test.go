/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestRunRejectsUnknownModesAndArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing mode"},
		{name: "unknown mode", args: []string{"unknown"}},
		{name: "flag as mode", args: []string{"--version"}},
		{name: "serve flag", args: []string{"serve", "--listen", "127.0.0.1:9001"}},
		{name: "healthcheck URL", args: []string{"healthcheck", "http://example.test/health/ready"}},
		{name: "healthcheck host", args: []string{"healthcheck", "example.test"}},
		{name: "healthcheck path", args: []string{"healthcheck", "/health/live"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := run(test.args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", test.args, got, exitUsage)
			}
		})
	}
}

func TestHealthcheckUsesValidatedListenAddressAndOnlyReadyPath(t *testing.T) {
	tests := []struct {
		name         string
		listenHost   string
		expectedHost string
	}{
		{name: "IPv4 wildcard", listenHost: "0.0.0.0", expectedHost: "127.0.0.1"},
		{name: "IPv6 wildcard", listenHost: "::", expectedHost: "127.0.0.1"},
		{name: "empty wildcard", listenHost: "", expectedHost: "127.0.0.1"},
		{name: "loopback", listenHost: "127.0.0.1", expectedHost: "127.0.0.1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestDetails := make(chan *http.Request, 1)
			server := newIPv4TestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requestDetails <- request.Clone(request.Context())
				writer.WriteHeader(http.StatusOK)
			}))
			port := server.Listener.Addr().(*net.TCPAddr).Port
			t.Setenv("NGINX_UIX_LISTEN_ADDR", net.JoinHostPort(test.listenHost, strconv.Itoa(port)))

			if got := run([]string{"healthcheck"}); got != exitOK {
				t.Fatalf("run(healthcheck) = %d, want %d", got, exitOK)
			}
			select {
			case request := <-requestDetails:
				if got, want := request.Method, http.MethodGet; got != want {
					t.Errorf("method = %q, want %q", got, want)
				}
				if got, want := request.URL.Path, "/health/ready"; got != want {
					t.Errorf("path = %q, want %q", got, want)
				}
				if request.URL.RawQuery != "" {
					t.Errorf("query = %q, want empty", request.URL.RawQuery)
				}
				host, _, err := net.SplitHostPort(request.Host)
				if err != nil {
					t.Fatalf("SplitHostPort(request.Host) error = %v", err)
				}
				if host != test.expectedHost {
					t.Errorf("request host = %q, want %q", host, test.expectedHost)
				}
			default:
				t.Fatal("healthcheck made no request")
			}
		})
	}
}

func TestHealthcheckReturnsSuccessOnlyForStatusOK(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusFound, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprintf("status %d", status), func(t *testing.T) {
			server := newIPv4TestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(status)
			}))
			t.Setenv("NGINX_UIX_LISTEN_ADDR", server.Listener.Addr().String())

			if got := run([]string{"healthcheck"}); got != exitFailure {
				t.Fatalf("run(healthcheck) = %d for status %d, want %d", got, status, exitFailure)
			}
		})
	}
}

func TestHealthcheckDoesNotFollowRedirects(t *testing.T) {
	redirectFollowed := make(chan struct{}, 1)
	server := newIPv4TestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health/ready" {
			http.Redirect(writer, request, "/unexpected", http.StatusFound)
			return
		}
		redirectFollowed <- struct{}{}
		writer.WriteHeader(http.StatusOK)
	}))
	t.Setenv("NGINX_UIX_LISTEN_ADDR", server.Listener.Addr().String())

	if got := run([]string{"healthcheck"}); got != exitFailure {
		t.Fatalf("run(healthcheck) = %d, want %d", got, exitFailure)
	}
	select {
	case <-redirectFollowed:
		t.Fatal("healthcheck followed a redirect")
	default:
	}
}

func TestHealthcheckRejectsInvalidListenAddress(t *testing.T) {
	t.Setenv("NGINX_UIX_LISTEN_ADDR", "127.0.0.1/not-a-port")

	if got := run([]string{"healthcheck"}); got != exitUsage {
		t.Fatalf("run(healthcheck) = %d, want %d", got, exitUsage)
	}
}

func TestHealthcheckClientHasThreeSecondTimeout(t *testing.T) {
	client := newHealthcheckClient()
	if got, want := client.Timeout, 3*time.Second; got != want {
		t.Fatalf("healthcheck client timeout = %s, want %s", got, want)
	}
}

func TestHealthcheckClientDoesNotUseEnvironmentProxy(t *testing.T) {
	client := newHealthcheckClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("healthcheck transport = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("healthcheck transport uses a proxy")
	}
}

func newIPv4TestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	if err := server.Listener.Close(); err != nil {
		t.Fatalf("close default test listener: %v", err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server
}
