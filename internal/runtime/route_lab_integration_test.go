/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package runtime

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

func TestRouteLabRealNginxLocationAndInternalRedirectMatrix(t *testing.T) {
	if os.Getenv("NGINX_UIX_INTEGRATION") != "1" {
		t.Skip("set NGINX_UIX_INTEGRATION=1 to run real Nginx Route Lab tests")
	}
	executable, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx executable is unavailable")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "route-lab")
	for _, directory := range []string{production, workspaces, stages, filepath.Join(production, "conf.d")} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events { worker_connections 64; }\nhttp { include "+production+"/conf.d/*.conf; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "conf.d", "routes.conf"), `server {
    listen 8080 default_server;
    server_name example.test;
    error_page 418 = /teapot;
    location = /exact { return 200 "exact"; }
    location /prefix/ { return 201 "prefix"; }
    location ^~ /assets/ { return 202 "asset"; }
    location ~ \.php$ { return 203 "php"; }
    location ~ ^/regex { return 204 "regex-first"; }
    location ~ ^/regex/specific { return 205 "regex-second"; }
    location /nested {
        return 206 "nested";
        location /nested/inner { return 207 "inner"; }
    }
	location /nested-order/ {
		location /nested-order/prefix/ { return 212 "nested-prefix"; }
		location = /nested-order/exact { return 213 "nested-exact"; }
	}
	location ~ ^/nested-order/prefix/ { return 214 "outer-regex"; }
    location = /rewrite { rewrite ^ /final last; }
    location = /final { return 208 "rewritten"; }
    location = /try { try_files $uri /fallback; }
    location = /fallback { return 209 "fallback"; }
    location = /error { return 418; }
    location = /teapot { return 210 "teapot"; }
}
`, 0o640)
	workspaceID := config.WorkspaceID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, workspaceID, "", nil)
	service := mustRouteLabService(t, routeLabOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), NginxExecutable: executable,
	})

	tests := []struct {
		uri         string
		status      int
		finalURI    string
		matcher     string
		matcherType routelab.MatcherType
	}{
		{uri: "/exact", status: 200, finalURI: "/exact", matcher: "/exact", matcherType: routelab.MatcherExact},
		{uri: "/prefix/value", status: 201, finalURI: "/prefix/value", matcher: "/prefix/", matcherType: routelab.MatcherPrefix},
		{uri: "/assets/test.php", status: 202, finalURI: "/assets/test.php", matcher: "/assets/", matcherType: routelab.MatcherPrefixPriority},
		{uri: "/other.php", status: 203, finalURI: "/other.php", matcher: `.php$`, matcherType: routelab.MatcherRegex},
		{uri: "/regex/specific", status: 204, finalURI: "/regex/specific", matcher: "^/regex", matcherType: routelab.MatcherRegex},
		{uri: "/nested/inner/value", status: 207, finalURI: "/nested/inner/value", matcher: "/nested/inner", matcherType: routelab.MatcherPrefix},
		{uri: "/nested-order/prefix/value", status: 214, finalURI: "/nested-order/prefix/value", matcher: "^/nested-order/prefix/", matcherType: routelab.MatcherRegex},
		{uri: "/nested-order/exact", status: 213, finalURI: "/nested-order/exact", matcher: "/nested-order/exact", matcherType: routelab.MatcherExact},
		{uri: "/rewrite", status: 208, finalURI: "/final", matcher: "/final", matcherType: routelab.MatcherExact},
		{uri: "/try", status: 209, finalURI: "/fallback", matcher: "/fallback", matcherType: routelab.MatcherExact},
		{uri: "/error", status: 210, finalURI: "/teapot", matcher: "/teapot", matcherType: routelab.MatcherExact},
	}
	for index, test := range tests {
		t.Run(test.uri, func(t *testing.T) {
			validated, err := routelab.ValidateRequest(routelab.Request{
				StaticRequest: routelab.StaticRequest{
					Scheme: routelab.SchemeHTTP, Host: "example.test", Port: 8080, URI: test.uri,
				},
				Method: "GET", Timeout: 5 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.ExecuteRouteTest(context.Background(), routelab.AgentRequest{
				RunID: fmt.Sprintf("%032x", index+1), WorkspaceID: workspaceID,
				ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
				Request: validated, RequestID: fmt.Sprintf("route-int-%d", index+1),
			})
			if err != nil {
				t.Fatalf("ExecuteRouteTest() error = %v, diagnostics = %+v", err, result.Diagnostics)
			}
			if result.Response.StatusCode != test.status || result.Evidence.StatusCode != test.status ||
				result.Evidence.FinalURI != test.finalURI {
				t.Fatalf("result = %+v", result)
			}
			matched := false
			for _, route := range result.Routes {
				if route.RouteID == result.Evidence.RouteID && route.Matcher == test.matcher && route.MatcherType == test.matcherType {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("final route %q did not match %s %q: %+v", result.Evidence.RouteID, test.matcherType, test.matcher, result.Routes)
			}
			assertCandidateStageEmpty(t, stages)
		})
	}
}

func TestRouteLabRealNginxHTTPSPreservesSNIAndIsolatesCertificates(t *testing.T) {
	if os.Getenv("NGINX_UIX_INTEGRATION") != "1" {
		t.Skip("set NGINX_UIX_INTEGRATION=1 to run real Nginx Route Lab tests")
	}
	executable, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx executable is unavailable")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "route-lab")
	for _, directory := range []string{
		production, workspaces, stages, filepath.Join(production, "conf.d"), filepath.Join(production, "ssl"),
	} {
		mustMkdirCandidate(t, directory)
	}
	certificatePath := filepath.Join(production, "ssl", "cert.pem")
	keyPath := filepath.Join(production, "ssl", "key.pem")
	mustWriteRouteLabCertificate(t, certificatePath, keyPath, "sni.example.test")
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events { worker_connections 64; }\nhttp { include "+production+"/conf.d/*.conf; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "conf.d", "tls.conf"), `server {
    listen 8443 ssl default_server;
    ssl_reject_handshake on;
}
server {
    listen 8443 ssl;
    server_name sni.example.test;
    ssl_certificate `+certificatePath+`;
    ssl_certificate_key `+keyPath+`;
    location = /tls { return 211 "tls"; }
}
`, 0o640)
	workspaceID := config.WorkspaceID("dddddddddddddddddddddddddddddddd")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, workspaceID, "", nil)
	service := mustRouteLabService(t, routeLabOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), NginxExecutable: executable,
	})
	validated, err := routelab.ValidateRequest(routelab.Request{
		StaticRequest: routelab.StaticRequest{
			Scheme: routelab.SchemeHTTPS, Host: "sni.example.test", SNI: "sni.example.test", Port: 8443, URI: "/tls",
		},
		Method: "GET", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteRouteTest(context.Background(), routelab.AgentRequest{
		RunID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", WorkspaceID: workspaceID,
		ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
		Request: validated, RequestID: "route-int-tls",
	})
	if err != nil {
		t.Fatalf("ExecuteRouteTest() error = %v, diagnostics = %+v", err, result.Diagnostics)
	}
	if result.Response.StatusCode != 211 || result.Evidence.StatusCode != 211 ||
		result.Evidence.FinalURI != "/tls" || result.Evidence.ServerRouteID == "" || result.Evidence.RouteID == "" {
		t.Fatalf("result = %+v", result)
	}
	assertCandidateStageEmpty(t, stages)
	productionAfter, err := config.OpenScopedRoot(production)
	if err != nil {
		t.Fatal(err)
	}
	inventoryAfter, inventoryErr := config.BuildInventory(context.Background(), productionAfter, config.SnapshotOptions{
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Policy: config.NewPolicy(), FileMode: 0o400, DirectoryMode: 0o700,
	})
	closeErr := productionAfter.Close()
	if inventoryErr != nil || closeErr != nil {
		t.Fatalf("read production after route test: inventory=%v close=%v", inventoryErr, closeErr)
	}
	if inventoryAfter.Digest != productionDigest {
		t.Fatalf("production digest changed: got %s, want %s", inventoryAfter.Digest.String(), productionDigest.String())
	}
}

func TestRouteLabRealNginxPreservesProductionMasterListenerAndDigest(t *testing.T) {
	if os.Getenv("NGINX_UIX_INTEGRATION") != "1" {
		t.Skip("set NGINX_UIX_INTEGRATION=1 to run real Nginx Route Lab tests")
	}
	executable, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx executable is unavailable")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "route-lab")
	runtimeRoot := filepath.Join(root, "production-runtime")
	for _, directory := range []string{production, workspaces, stages, runtimeRoot} {
		mustMkdirCandidate(t, directory)
	}
	productionPort := reserveFixturePort(t)
	pidPath := filepath.Join(runtimeRoot, "nginx.pid")
	errorLogPath := filepath.Join(runtimeRoot, "error.log")
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), fmt.Sprintf(`daemon off;
pid %s;
error_log %s notice;
events { worker_connections 64; }
http {
    access_log off;
    server {
        listen 127.0.0.1:%d;
        server_name production.example.test;
        location / { return 200 "production"; }
    }
}
`, pidPath, errorLogPath, productionPort), 0o640)
	workspaceID := config.WorkspaceID("ffffffffffffffffffffffffffffffff")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, workspaceID, "", nil)
	productionProcess := startProductionNginxFixture(t, executable, production, pidPath, productionPort)
	masterPID := productionProcess.command.Process.Pid

	service := mustRouteLabService(t, routeLabOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), NginxExecutable: executable,
	})
	validated, err := routelab.ValidateRequest(routelab.Request{
		StaticRequest: routelab.StaticRequest{
			Scheme: routelab.SchemeHTTP, Host: "production.example.test", Port: productionPort, URI: "/",
		},
		Method: "GET", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteRouteTest(context.Background(), routelab.AgentRequest{
		RunID: "abababababababababababababababab", WorkspaceID: workspaceID,
		ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
		Request: validated, RequestID: "route-int-production-isolation",
	})
	if err != nil {
		t.Fatalf("ExecuteRouteTest() error = %v, diagnostics = %+v", err, result.Diagnostics)
	}
	if result.Response.StatusCode != 200 || result.Evidence.RouteID == "" {
		t.Fatalf("route result = %+v", result)
	}
	assertProductionNginxFixture(t, productionProcess, pidPath, productionPort, masterPID)
	assertCandidateStageEmpty(t, stages)
	productionAfter, err := config.OpenScopedRoot(production)
	if err != nil {
		t.Fatal(err)
	}
	inventoryAfter, inventoryErr := config.BuildInventory(context.Background(), productionAfter, config.SnapshotOptions{
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Policy: config.NewPolicy(), FileMode: 0o400, DirectoryMode: 0o700,
	})
	closeErr := productionAfter.Close()
	if inventoryErr != nil || closeErr != nil || inventoryAfter.Digest != productionDigest {
		t.Fatalf("production changed: digest=%s inventory=%v close=%v", inventoryAfter.Digest.String(), inventoryErr, closeErr)
	}
}

func reserveFixturePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func startProductionNginxFixture(
	t *testing.T,
	executable string,
	productionRoot string,
	pidPath string,
	port int,
) *sandboxProcess {
	t.Helper()
	commandContext, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(commandContext, executable, "-p", productionRoot+string(filepath.Separator), "-c", filepath.Join(productionRoot, "nginx.conf")) // #nosec G204 -- integration fixture executable and arguments are test-owned.
	command.Dir = productionRoot
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "TZ=UTC"}
	command.Stdout = &boundedRouteWriter{limit: routeLabProcessOutputLimit}
	command.Stderr = &boundedRouteWriter{limit: routeLabProcessOutputLimit}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	process := &sandboxProcess{command: command, done: make(chan struct{}), cancel: cancel}
	go func() {
		process.err = command.Wait()
		close(process.done)
	}()
	t.Cleanup(func() {
		cleanup, err := stopRouteSandboxProcess(process, port)
		if err != nil || !cleanup.MasterReaped || !cleanup.PortClosed {
			t.Errorf("stop production Nginx fixture: cleanup=%+v error=%v", cleanup, err)
		}
	})
	deadline := time.Now().Add(routeLabStartupTimeout)
	for time.Now().Before(deadline) {
		pid, pidErr := readRouteSandboxPID(pidPath)
		connection, dialErr := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
		}
		if pidErr == nil && pid == command.Process.Pid && dialErr == nil {
			return process
		}
		select {
		case <-process.done:
			t.Fatalf("production Nginx fixture exited: %v", process.err)
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("production Nginx fixture did not become ready")
	return nil
}

func assertProductionNginxFixture(t *testing.T, process *sandboxProcess, pidPath string, port, wantPID int) {
	t.Helper()
	select {
	case <-process.done:
		t.Fatalf("production Nginx exited during Route Lab: %v", process.err)
	default:
	}
	pid, err := readRouteSandboxPID(pidPath)
	if err != nil || pid != wantPID {
		t.Fatalf("production master PID = %d, %v; want %d", pid, err, wantPID)
	}
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("production listener changed: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close production listener probe: %v", err)
	}
}

func mustWriteRouteLabCertificate(t *testing.T, certificatePath, keyPath, dnsName string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: dnsName}, DNSNames: []string{dnsName},
		NotBefore: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:  time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC),
		IsCA:      true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: dnsName}, DNSNames: []string{dnsName},
		NotBefore: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:  time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:  x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, issuer, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteCandidate(t, certificatePath, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})), 0o600)
	mustWriteCandidate(t, keyPath, string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER})), 0o600)
}
