/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewAgentClientUsesOnlyFixedProductionSocket(t *testing.T) {
	client := NewAgentClient()
	if got, want := client.socketPath, agentSocketPath; got != want {
		t.Fatalf("socket path = %q, want fixed production path %q", got, want)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.httpClient.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("transport Proxy is configured, want direct Unix socket only")
	}
	if transport.DialContext == nil {
		t.Fatal("transport DialContext is nil, want fixed Unix socket dialer")
	}
	if got := transport.MaxResponseHeaderBytes; got <= 0 || got > agentMaxHeaderBytes {
		t.Fatalf("MaxResponseHeaderBytes = %d, want a positive limit no greater than %d", got, agentMaxHeaderBytes)
	}
}

func TestAgentClientCallsFiveFixedGETEndpoints(t *testing.T) {
	checkedAt := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	exitCode := 0
	wantBuild := BuildInfo{
		Version:            "1.30.3",
		ConfigureArguments: []string{"--with-http_ssl_module", "--pid-path=/run/nginx.pid"},
		PIDPath:            "/run/nginx.pid",
		SbinPath:           nginxExecutable,
	}
	wantStartup := StartupState{
		Validation: &StartupValidation{
			Valid: true, CheckedAt: checkedAt, ExitCode: &exitCode, Diagnostic: "configuration is valid",
		},
		Recovery: &RecoveryState{Count: 1, LastResult: RecoveryResultRestarting},
	}
	wantStatus := Status{
		SampledAt: checkedAt,
		State:     StateRunning,
		Master: &NginxProcess{
			PID: 100, Role: ProcessRoleMaster, StartedAt: checkedAt.Add(-time.Hour),
		},
		Workers:           []NginxProcess{{PID: 101, Role: ProcessRoleWorker, StartedAt: checkedAt.Add(-time.Hour)}},
		Build:             &wantBuild,
		StartupValidation: wantStartup.Validation,
		Recovery:          wantStartup.Recovery,
		Issues:            []string{},
	}
	wantConfig := EffectiveConfig{
		DisplayMode: EffectiveConfigDisplayModeRaw,
		Occurrences: []ConfigOccurrence{},
		RawContent:  "# configuration file /etc/nginx/nginx.conf:\nevents {}\n",
		Warnings:    []EffectiveConfigWarning{EffectiveConfigWarningPathOutsideAllowedRoots},
	}

	requests := make(chan agentClientRequest, 5)
	responses := map[string]any{
		agentProtocolHealthPath:            agentHealthResponse{Status: "healthy"},
		agentProtocolStatusPath:            newAgentStatusResponse(wantStatus),
		agentProtocolBuildInfoPath:         newAgentBuildInfoResponse(wantBuild),
		agentProtocolStartupValidationPath: newAgentStartupStateResponse(wantStartup),
		agentProtocolEffectiveConfigPath:   newAgentEffectiveConfigResponse(wantConfig),
	}
	path := startAgentClientUnixServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		requests <- agentClientRequest{
			method: request.Method, path: request.URL.Path, rawQuery: request.URL.RawQuery,
			body: body, readErr: err, accept: request.Header.Get("Accept"),
		}
		response, found := responses[request.URL.Path]
		if !found {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", agentProtocolContentType)
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			return
		}
	}))
	client := newAgentClient(path)

	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	gotStatus, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !reflect.DeepEqual(gotStatus, wantStatus) {
		t.Fatalf("Status() = %#v, want %#v", gotStatus, wantStatus)
	}
	gotBuild, err := client.BuildInfo(context.Background())
	if err != nil {
		t.Fatalf("BuildInfo() error = %v", err)
	}
	if !reflect.DeepEqual(gotBuild, wantBuild) {
		t.Fatalf("BuildInfo() = %#v, want %#v", gotBuild, wantBuild)
	}
	gotStartup, err := client.StartupValidation(context.Background())
	if err != nil {
		t.Fatalf("StartupValidation() error = %v", err)
	}
	if !reflect.DeepEqual(gotStartup, wantStartup) {
		t.Fatalf("StartupValidation() = %#v, want %#v", gotStartup, wantStartup)
	}
	gotConfig, err := client.EffectiveConfig(context.Background())
	if err != nil {
		t.Fatalf("EffectiveConfig() error = %v", err)
	}
	if !reflect.DeepEqual(gotConfig, wantConfig) {
		t.Fatalf("EffectiveConfig() = %#v, want %#v", gotConfig, wantConfig)
	}

	wantPaths := []string{
		agentProtocolHealthPath,
		agentProtocolStatusPath,
		agentProtocolBuildInfoPath,
		agentProtocolStartupValidationPath,
		agentProtocolEffectiveConfigPath,
	}
	for _, wantPath := range wantPaths {
		request := <-requests
		if request.readErr != nil {
			t.Fatalf("read request body for %q: %v", wantPath, request.readErr)
		}
		if request.method != http.MethodGet || request.path != wantPath {
			t.Fatalf("request = %s %s, want GET %s", request.method, request.path, wantPath)
		}
		if request.rawQuery != "" {
			t.Fatalf("request %q query = %q, want none", wantPath, request.rawQuery)
		}
		if len(request.body) != 0 {
			t.Fatalf("request %q body = %q, want none", wantPath, request.body)
		}
		if got, want := request.accept, agentProtocolContentType; got != want {
			t.Fatalf("request %q Accept = %q, want %q", wantPath, got, want)
		}
	}
}

func TestAgentClientCancelsEveryCallContext(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *AgentClient) error
	}{
		{name: "health", call: func(ctx context.Context, client *AgentClient) error { return client.Health(ctx) }},
		{name: "status", call: func(ctx context.Context, client *AgentClient) error {
			_, err := client.Status(ctx)
			return err
		}},
		{name: "build info", call: func(ctx context.Context, client *AgentClient) error {
			_, err := client.BuildInfo(ctx)
			return err
		}},
		{name: "startup validation", call: func(ctx context.Context, client *AgentClient) error {
			_, err := client.StartupValidation(ctx)
			return err
		}},
		{name: "effective config", call: func(ctx context.Context, client *AgentClient) error {
			_, err := client.EffectiveConfig(ctx)
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			serverCanceled := make(chan struct{})
			path := startAgentClientUnixServer(t, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				close(started)
				<-request.Context().Done()
				close(serverCanceled)
			}))
			client := newAgentClient(path)
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				result <- test.call(ctx, client)
			}()

			waitAgentClientSignal(t, started, "request start")
			cancel()
			err := waitAgentClientError(t, result)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("call error = %v, want context.Canceled", err)
			}
			waitAgentClientSignal(t, serverCanceled, "server context cancellation")
		})
	}
}

func TestAgentClientBoundsResponseBodyWithoutReturningPartialData(t *testing.T) {
	path := startAgentClientUnixServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", agentProtocolContentType)
		writer.WriteHeader(http.StatusOK)
		chunk := bytes.Repeat([]byte{'x'}, 32*1024)
		remaining := agentProtocolResponseLimit + 1
		for remaining > 0 {
			length := min(remaining, len(chunk))
			written, err := writer.Write(chunk[:length])
			remaining -= written
			if err != nil {
				return
			}
		}
	}))

	configuration, err := newAgentClient(path).EffectiveConfig(context.Background())
	if !reflect.DeepEqual(configuration, EffectiveConfig{}) {
		t.Fatalf("EffectiveConfig() = %#v, want zero value on oversized response", configuration)
	}
	assertAgentClientError(t, err, agentErrorCodeResponseTooLarge, errAgentResponseTooLarge)
}

func TestAgentClientRejectsInvalidResponseBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
	}{
		{
			name: "oversized headers",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", agentProtocolContentType)
				writer.Header().Set("X-Oversized", strings.Repeat("x", agentMaxHeaderBytes*2))
				_, _ = io.WriteString(writer, `{"status":"healthy"}`)
			}),
		},
		{
			name: "non JSON content type",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(writer, `{"status":"healthy"}`)
			}),
		},
		{
			name: "multiple JSON values",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", agentProtocolContentType)
				_, _ = io.WriteString(writer, `{"status":"healthy"}{"diagnostic":"secret"}`)
			}),
		},
		{
			name: "unexpected health value",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", agentProtocolContentType)
				_, _ = io.WriteString(writer, `{"status":"unhealthy","diagnostic":"secret"}`)
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := startAgentClientUnixServer(t, test.handler)
			err := newAgentClient(path).Health(context.Background())
			assertAgentClientError(t, err, agentErrorCodeInternal, nil)
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "diagnostic") {
				t.Fatalf("Health() error = %q, want sanitized error", err)
			}
		})
	}
}

func TestAgentClientRejectsMalformedJSONWithoutPartialData(t *testing.T) {
	path := startAgentClientUnixServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", agentProtocolContentType)
		_, _ = io.WriteString(writer, `{"version":"1.30.3","configure_arguments":["--secret"],"pid_path":"/run/nginx.pid","sbin_path":"/usr/sbin/nginx"}{"diagnostic":"private.key"}`)
	}))

	build, err := newAgentClient(path).BuildInfo(context.Background())
	if !reflect.DeepEqual(build, BuildInfo{}) {
		t.Fatalf("BuildInfo() = %#v, want zero value on malformed JSON", build)
	}
	assertAgentClientError(t, err, agentErrorCodeInternal, nil)
	for _, sensitive := range []string{"secret", "private.key", "diagnostic"} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("BuildInfo() error = %q, want no %q", err, sensitive)
		}
	}
}

func TestAgentClientRejectsInconsistentEffectiveConfigModesWithoutPartialData(t *testing.T) {
	tests := []string{
		`{"display_mode":"structured","occurrences":[],"raw_content":"events {}","warnings":[]}`,
		`{"display_mode":"structured","occurrences":[],"raw_content":null,"warnings":["NGINX_CONFIG_STRUCTURE_UNVERIFIED"]}`,
		`{"display_mode":"raw","occurrences":[{"id":"occurrence-000001","load_order":1,"path":"/etc/nginx/nginx.conf","content":"events {}"}],"raw_content":"events {}","warnings":["NGINX_CONFIG_STRUCTURE_UNVERIFIED"]}`,
		`{"display_mode":"raw","occurrences":[],"raw_content":null,"warnings":["NGINX_CONFIG_STRUCTURE_UNVERIFIED"]}`,
		`{"display_mode":"raw","occurrences":[],"raw_content":"events {}","warnings":[]}`,
		`{"display_mode":"raw","occurrences":[],"raw_content":"events {}","warnings":["UNKNOWN"]}`,
	}
	for _, payload := range tests {
		path := startAgentClientUnixServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", agentProtocolContentType)
			_, _ = io.WriteString(writer, payload)
		}))

		configuration, err := newAgentClient(path).EffectiveConfig(context.Background())
		if !reflect.DeepEqual(configuration, EffectiveConfig{}) {
			t.Fatalf("EffectiveConfig() = %#v, want zero value for payload %s", configuration, payload)
		}
		assertAgentClientError(t, err, agentErrorCodeInternal, nil)
	}
}

func TestAgentClientMapsStableErrorsWithoutServerDiagnostics(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		code        string
		want        error
		wantMessage string
	}{
		{name: "invalid config", status: http.StatusUnprocessableEntity, code: agentErrorCodeConfigInvalid, want: ErrConfigInvalid, wantMessage: "nginx configuration is invalid"},
		{name: "command timeout", status: http.StatusGatewayTimeout, code: agentErrorCodeCommandTimeout, want: ErrCommandTimeout, wantMessage: "nginx operation timed out"},
		{name: "command output", status: http.StatusBadGateway, code: agentErrorCodeCommandOutputLarge, want: ErrOutputTooLarge, wantMessage: "nginx output exceeded limit"},
		{name: "encoded response", status: http.StatusInternalServerError, code: agentErrorCodeResponseTooLarge, want: errAgentResponseTooLarge, wantMessage: "agent response exceeded limit"},
		{name: "internal", status: http.StatusInternalServerError, code: agentErrorCodeInternal, wantMessage: "agent operation failed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := startAgentClientUnixServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", agentProtocolContentType)
				writer.WriteHeader(test.status)
				_ = json.NewEncoder(writer).Encode(agentErrorEnvelope{Error: AgentProtocolError{
					Code: test.code, Message: "secret /etc/nginx/private.key",
				}})
			}))

			configuration, err := newAgentClient(path).EffectiveConfig(context.Background())
			if !reflect.DeepEqual(configuration, EffectiveConfig{}) {
				t.Fatalf("EffectiveConfig() = %#v, want zero value on Agent error", configuration)
			}
			assertAgentClientError(t, err, test.code, test.want)
			if !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("EffectiveConfig() error = %q, want stable message %q", err, test.wantMessage)
			}
			for _, sensitive := range []string{"secret", "/etc/nginx", "private.key"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("EffectiveConfig() error = %q, want no %q", err, sensitive)
				}
			}
		})
	}
}

func TestAgentClientDoesNotUseProxyOrTCPFallback(t *testing.T) {
	var tcpHits atomic.Int32
	tcpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		tcpHits.Add(1)
		writer.Header().Set("Content-Type", agentProtocolContentType)
		_, _ = io.WriteString(writer, `{"status":"healthy"}`)
	}))
	defer tcpServer.Close()
	t.Setenv("HTTP_PROXY", tcpServer.URL)
	t.Setenv("HTTPS_PROXY", tcpServer.URL)
	t.Setenv("NO_PROXY", "")

	missingSocket := filepath.Join(shortAgentClientSocketDir(t), "missing.sock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := newAgentClient(missingSocket).Health(ctx); err == nil {
		t.Fatal("Health() error = nil, want unavailable Unix socket error")
	}
	if got := tcpHits.Load(); got != 0 {
		t.Fatalf("TCP request count = %d, want no proxy or TCP fallback", got)
	}
}

type agentClientRequest struct {
	method   string
	path     string
	rawQuery string
	body     []byte
	readErr  error
	accept   string
}

func assertAgentClientError(t *testing.T, err error, wantCode string, wantCause error) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want Agent error code %q", wantCode)
	}
	var protocolError *AgentProtocolError
	if !errors.As(err, &protocolError) {
		t.Fatalf("error type = %T, want *AgentProtocolError; error = %v", err, err)
	}
	if got := protocolError.Code; got != wantCode {
		t.Fatalf("error code = %q, want %q", got, wantCode)
	}
	if wantCause != nil && !errors.Is(err, wantCause) {
		t.Fatalf("error = %v, want cause %v", err, wantCause)
	}
}

func startAgentClientUnixServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	directory := shortAgentClientSocketDir(t)
	path := filepath.Join(directory, "agent.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		done <- serveErr
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		shutdownErr := server.Shutdown(ctx)
		cancel()
		if shutdownErr != nil {
			t.Errorf("Shutdown(test Agent server) error = %v", shutdownErr)
		}
		select {
		case serveErr := <-done:
			if serveErr != nil {
				t.Errorf("Serve(test Agent server) error = %v", serveErr)
			}
		case <-time.After(2 * time.Second):
			t.Error("test Agent server did not stop")
		}
	})
	return path
}

func shortAgentClientSocketDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "nginx-uix-client-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("RemoveAll(%q) error = %v", directory, err)
		}
	})
	return directory
}

func waitAgentClientSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitAgentClientError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Agent client call")
		return nil
	}
}
