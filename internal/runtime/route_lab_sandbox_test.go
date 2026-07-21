/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package runtime

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/routelab"
)

func TestExecuteRouteRequestPinsLoopbackAndSanitizesResponse(t *testing.T) {
	received := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.Body.Close(); err != nil {
			t.Errorf("close received request body: %v", err)
		}
		received <- request.Clone(request.Context())
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.Header().Set("Location", "/next")
		writer.Header().Set("Set-Cookie", "session=secret")
		writer.Header().Set("Www-Authenticate", "Basic realm=secret")
		writer.Header().Set("X-Route-Trace", "trace-safe")
		writer.Header().Set("X-Api-Key", "response-secret")
		writer.Header().Set("X-Auth-Token", "response-token")
		writer.Header().Set("X-Nginx-UIX-Internal", "internal-secret")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("created"))
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	validated, err := routelab.ValidateRequest(routelab.Request{
		StaticRequest: routelab.StaticRequest{
			Scheme: routelab.SchemeHTTP, Host: "unresolvable.invalid", Port: 8080, URI: "/submit",
		},
		Method: "POST", Query: "source=route-lab",
		Headers: []routelab.Header{{Name: "X-Trace-ID", Value: "trace-1"}},
		Body:    []byte("payload"), Timeout: time.Second,
		Confirmation: routelab.SideEffectConfirmation,
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := executeRouteRequest(context.Background(), sandboxRun{
		TargetPort: port, Request: validated, TestToken: "test-token",
	})
	if err != nil {
		t.Fatalf("executeRouteRequest() error = %v", err)
	}
	if response.StatusCode != http.StatusCreated || response.BodySnippet != "created" || response.BodyTruncated ||
		response.SnippetOmitted || response.BodyBytes != 7 || len(response.BodyDigest) != 64 || response.Duration <= 0 {
		t.Fatalf("response = %+v", response)
	}
	headers := routeHeaderMap(response.Headers)
	if headers["Content-Type"] != "text/plain; charset=utf-8" || headers["Location"] != "/next" ||
		headers["X-Route-Trace"] != "trace-safe" || headers["Set-Cookie"] != "" ||
		headers["Www-Authenticate"] != "" || headers["X-Api-Key"] != "" ||
		headers["X-Auth-Token"] != "" || headers["X-Nginx-Uix-Internal"] != "" {
		t.Fatalf("response headers = %+v", response.Headers)
	}

	select {
	case request := <-received:
		if request.Host != "unresolvable.invalid" || request.URL.Path != "/submit" ||
			request.URL.RawQuery != "source=route-lab" || request.Header.Get("X-Trace-ID") != "trace-1" ||
			request.Header.Get("X-Nginx-Uix-Test-Id") != "test-token" {
			t.Fatalf("received request = %+v, headers = %+v", request, request.Header)
		}
	case <-time.After(time.Second):
		t.Fatal("loopback server did not receive route request")
	}
}

func TestExecuteRouteRequestBoundsBinaryBody(t *testing.T) {
	body := bytes.Repeat([]byte{0xff}, routeLabResponseBodyLimit+128)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/octet-stream")
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	validated, err := routelab.ValidateRequest(routelab.Request{
		StaticRequest: routelab.StaticRequest{
			Scheme: routelab.SchemeHTTP, Host: "binary.invalid", Port: 80, URI: "/",
		},
		Method: "GET", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := executeRouteRequest(context.Background(), sandboxRun{
		TargetPort: port, Request: validated, TestToken: "binary-token",
	})
	if err != nil {
		t.Fatalf("executeRouteRequest() error = %v", err)
	}
	if !response.BodyTruncated || !response.SnippetOmitted || response.BodySnippet != "" ||
		response.BodyBytes != int64(routeLabResponseBodyLimit+1) || len(response.BodyDigest) != 64 {
		t.Fatalf("response = %+v", response)
	}
}

func TestParseRouteEvidenceRequiresOneMatchingRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	routes := []routelab.RouteDefinition{
		{RouteID: "srv_1", Kind: routelab.RouteServer},
		{RouteID: "loc_1", Kind: routelab.RouteLocation},
	}
	record := []byte(`{"test":"token-1","server":"srv_1","route":"loc_1","uri":"/final","status":"201","upstream":"127.0.0.1:9000","upstream_status":"201","request_time":"0.012"}` + "\n")
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatal(err)
	}
	evidence, err := parseRouteEvidence(path, "token-1", routes)
	if err != nil {
		t.Fatalf("parseRouteEvidence() error = %v", err)
	}
	if evidence.ServerRouteID != "srv_1" || evidence.RouteID != "loc_1" || evidence.FinalURI != "/final" ||
		evidence.StatusCode != 201 || evidence.Upstream != "127.0.0.1:9000" ||
		evidence.UpstreamStatus != "201" || evidence.RequestTime != 12*time.Millisecond {
		t.Fatalf("evidence = %+v", evidence)
	}

	if err := os.WriteFile(path, append(record, record...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseRouteEvidence(path, "token-1", routes); !errors.Is(err, routelab.ErrEvidenceIncomplete) {
		t.Fatalf("parseRouteEvidence() error = %v, want ErrEvidenceIncomplete", err)
	}
	if _, err := parseRouteEvidence(path, "other-token", routes); !errors.Is(err, routelab.ErrEvidenceIncomplete) {
		t.Fatalf("parseRouteEvidence() error = %v, want ErrEvidenceIncomplete", err)
	}
}

func routeHeaderMap(headers []routelab.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for _, header := range headers {
		result[header.Name] = header.Value
	}
	return result
}
