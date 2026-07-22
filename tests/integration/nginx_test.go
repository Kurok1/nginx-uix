/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/certificate"
)

func TestEffectiveConfigWithRealIsolatedNginx(t *testing.T) {
	binary := requireIntegrationNginx(t)
	versionContext, cancelVersion := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelVersion()
	version, err := nginxVersion(versionContext, binary)
	if err != nil {
		t.Fatalf("read Nginx version: %v", err)
	}
	t.Logf("real Nginx: %s", version)

	t.Run("valid configuration preserves nested repeated includes", func(t *testing.T) {
		harness := newNginxHarness(t, binary, "effective")
		commandContext, cancelCommands := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCommands()

		validation, err := harness.run(commandContext, "-t")
		if err != nil {
			t.Fatalf("nginx -t error = %v, stderr = %q", err, validation.stderr)
		}
		dump, err := harness.run(commandContext, "-T")
		if err != nil {
			t.Fatalf("nginx -T error = %v, stderr = %q", err, dump.stderr)
		}
		paths, err := configurationPaths(dump.stdout, harness.configPath)
		if err != nil {
			t.Fatalf("configurationPaths() error = %v", err)
		}
		wantPaths := []string{
			harness.configPath,
			filepath.Join(harness.prefix, "conf.d", "root.conf"),
			filepath.Join(harness.prefix, "conf.d", "repeated.conf"),
			filepath.Join(harness.prefix, "conf.d", "repeated.conf"),
		}
		if !slices.Equal(paths, wantPaths) {
			t.Fatalf("configuration paths = %#v, want %#v", paths, wantPaths)
		}

		startContext, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelStart()
		if err := harness.start(startContext); err != nil {
			t.Fatalf("start isolated Nginx: %v", err)
		}
		if err := harness.captureWorkerProcessIDs(commandContext); err != nil {
			t.Fatalf("capture worker PIDs: %v", err)
		}
		processIDs := harness.processIDs()
		if len(processIDs) < 2 {
			t.Fatalf("captured process IDs = %#v, want master and worker", processIDs)
		}

		closeContext, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelClose()
		if err := harness.close(closeContext); err != nil {
			t.Fatalf("close isolated Nginx: %v", err)
		}
		assertProcessIDsGone(t, processIDs)
		assertPathsAbsent(t, harness.runtimeArtifactPaths())
	})

	t.Run("invalid configuration starts no surviving master", func(t *testing.T) {
		harness := newNginxHarness(t, binary, "invalid")
		commandContext, cancelCommands := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCommands()

		validation, err := harness.run(commandContext, "-t")
		if err == nil {
			t.Fatalf("nginx -t error = nil, stdout = %q, stderr = %q", validation.stdout, validation.stderr)
		}
		if _, statErr := os.Stat(harness.pidPath); !os.IsNotExist(statErr) {
			t.Fatalf("invalid validation PID file error = %v, want not exist", statErr)
		}

		startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelStart()
		if err := harness.start(startContext); err == nil {
			t.Fatal("start invalid isolated Nginx error = nil")
		}
		processIDs := harness.processIDs()
		assertProcessIDsGone(t, processIDs)
		if _, statErr := os.Stat(harness.pidPath); !os.IsNotExist(statErr) {
			t.Fatalf("invalid start PID file error = %v, want not exist", statErr)
		}

		closeContext, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelClose()
		if err := harness.close(closeContext); err != nil {
			t.Fatalf("close invalid harness: %v", err)
		}
		assertPathsAbsent(t, harness.runtimeArtifactPaths())
	})

	t.Run("cancellation reaps captured processes and removes runtime files", func(t *testing.T) {
		harness := newNginxHarness(t, binary, "effective")
		runContext, cancelRun := context.WithCancel(context.Background())
		if err := harness.start(runContext); err != nil {
			cancelRun()
			t.Fatalf("start isolated Nginx: %v", err)
		}
		captureContext, cancelCapture := context.WithTimeout(context.Background(), 3*time.Second)
		if err := harness.captureWorkerProcessIDs(captureContext); err != nil {
			cancelCapture()
			cancelRun()
			t.Fatalf("capture worker PIDs: %v", err)
		}
		cancelCapture()
		processIDs := harness.processIDs()

		cancelRun()
		waitContext, cancelWait := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelWait()
		if err := harness.waitForExit(waitContext); err != nil {
			t.Fatalf("wait for canceled isolated Nginx: %v", err)
		}
		if err := harness.close(waitContext); err != nil {
			t.Fatalf("clean canceled isolated Nginx: %v", err)
		}
		assertProcessIDsGone(t, processIDs)
		assertPathsAbsent(t, harness.runtimeArtifactPaths())
	})
}

func TestCompatibilityMatrixWithRealIsolatedNginx(t *testing.T) {
	binary := requireIntegrationNginx(t)
	harness := newNginxHarness(t, binary, "compatibility")
	commandContext, cancelCommands := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCommands()

	validation, err := harness.run(commandContext, "-t")
	if err != nil {
		t.Fatalf("nginx -t error = %v, stderr = %q", err, validation.stderr)
	}
	dump, err := harness.run(commandContext, "-T")
	if err != nil {
		t.Fatalf("nginx -T error = %v, stderr = %q", err, dump.stderr)
	}
	for name, snippet := range map[string]string{
		"IPv4 upstream":               "server 127.0.0.1:18081",
		"IPv6 upstream":               "server [::1]:18082 backup",
		"Unix upstream":               "server unix:/tmp/nginx-uix-compatibility.sock down",
		"wildcard name":               "*.example.test",
		"regular expression location": "location ~* \\.(?:css|js)$",
		"named location":              "location @named_fallback",
		"map":                         "map $http_upgrade $connection_upgrade",
	} {
		if !strings.Contains(dump.stdout, snippet) {
			t.Errorf("nginx -T output does not contain %s %q", name, snippet)
		}
	}
}

func TestCertificateAutomationWithRealIsolatedNginx(t *testing.T) {
	binary := requireIntegrationNginx(t)

	t.Run("exact HTTP challenge is live only until transactional cleanup", func(t *testing.T) {
		const (
			challengeToken   = "e2e_HTTP-01_token"
			keyAuthorization = "e2e_HTTP-01_token.thumbprint_value"
		)
		fragment, err := certificate.RenderHTTPChallengeFragment([]certificate.HTTPChallengeResponse{{
			Identifier: "example.test", Token: challengeToken, KeyAuthorization: keyAuthorization,
		}})
		if err != nil {
			t.Fatalf("render HTTP challenge fragment: %v", err)
		}
		port := reserveLoopbackPort(t)
		harness := newGeneratedNginxHarness(t, binary, port, httpChallengeConfiguration(port, fragment))
		commandContext, cancelCommands := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCommands()
		if validation, err := harness.run(commandContext, "-t"); err != nil {
			t.Fatalf("validate HTTP challenge Nginx: %v, stderr = %q", err, validation.stderr)
		}
		startContext, cancelStart := context.WithCancel(context.Background())
		defer cancelStart()
		if err := harness.start(startContext); err != nil {
			t.Fatalf("start HTTP challenge Nginx: %v", err)
		}
		client := &http.Client{
			Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
			Timeout:   2 * time.Second,
		}
		challengeURL := fmt.Sprintf("http://127.0.0.1:%d/.well-known/acme-challenge/%s", port, challengeToken)
		status, body, err := requestIntegrationHTTP(commandContext, client, challengeURL, "example.test")
		if err != nil || status != http.StatusOK || body != keyAuthorization {
			t.Fatalf("exact challenge response = (%d, %q, %v), want (200, %q, nil)", status, body, err, keyAuthorization)
		}
		status, _, err = requestIntegrationHTTP(commandContext, client, challengeURL+"-near-match", "example.test")
		if err != nil || status != http.StatusNotFound {
			t.Fatalf("near challenge response = (%d, %v), want (404, nil)", status, err)
		}

		masterProcessID := harness.masterProcessID
		writeGeneratedNginxConfiguration(t, harness.configPath, httpChallengeConfiguration(port, ""))
		if validation, err := harness.run(commandContext, "-t"); err != nil {
			t.Fatalf("validate HTTP challenge cleanup: %v, stderr = %q", err, validation.stderr)
		}
		if err := harness.reload(commandContext); err != nil {
			t.Fatalf("reload HTTP challenge cleanup: %v", err)
		}
		if err := waitForIntegrationHTTPStatus(commandContext, client, challengeURL, "example.test", http.StatusNotFound); err != nil {
			t.Fatal(err)
		}
		if current := readNginxMasterProcessID(t, harness.pidPath); current != masterProcessID {
			t.Fatalf("master PID after challenge cleanup = %d, want %d", current, masterProcessID)
		}
	})

	t.Run("HTTPS SNI selects the validated certificate and invalid candidate keeps it active", func(t *testing.T) {
		now := time.Now().UTC()
		first := issueIntegrationCertificate(t, "first.example.test", 1, now)
		second := issueIntegrationCertificate(t, "second.example.test", 2, now)
		port := reserveLoopbackPort(t)
		prefix := filepath.Join(t.TempDir(), "certificate-prefix")
		if err := os.MkdirAll(filepath.Join(prefix, "material"), 0o700); err != nil {
			t.Fatal(err)
		}
		firstCertificatePath, firstKeyPath := writeIntegrationCertificate(t, prefix, "first", first)
		secondCertificatePath, secondKeyPath := writeIntegrationCertificate(t, prefix, "second", second)
		configuration := httpsSNIConfiguration(
			port,
			firstCertificatePath, firstKeyPath,
			secondCertificatePath, secondKeyPath,
		)
		harness := newGeneratedNginxHarnessAtPrefix(t, binary, prefix, port, configuration)
		commandContext, cancelCommands := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCommands()
		if validation, err := harness.run(commandContext, "-t"); err != nil {
			t.Fatalf("validate HTTPS Nginx: %v, stderr = %q", err, validation.stderr)
		}
		startContext, cancelStart := context.WithCancel(context.Background())
		defer cancelStart()
		if err := harness.start(startContext); err != nil {
			t.Fatalf("start HTTPS Nginx: %v", err)
		}

		roots := x509.NewCertPool()
		roots.AddCert(first.parsed)
		roots.AddCert(second.parsed)
		client := &http.Client{
			Transport: &http.Transport{
				Proxy:             nil,
				DisableKeepAlives: true,
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
					RootCAs:    roots,
					ServerName: "second.example.test",
				},
			},
			Timeout: 2 * time.Second,
		}
		requestURL := fmt.Sprintf("https://127.0.0.1:%d/", port)
		assertIntegrationHTTPSResponse(t, client, requestURL, "second.example.test", "second", second.parsed.Raw)

		writeGeneratedNginxConfiguration(t, harness.configPath, "unknown_certificate_candidate_directive;\n")
		if validation, err := harness.run(commandContext, "-t"); err == nil {
			t.Fatalf("invalid replacement nginx -t error = nil, stderr = %q", validation.stderr)
		}
		assertIntegrationHTTPSResponse(t, client, requestURL, "second.example.test", "second", second.parsed.Raw)
	})
}

type integrationCertificate struct {
	issued certificate.IssuedCertificate
	key    *ecdsa.PrivateKey
	parsed *x509.Certificate
}

func issueIntegrationCertificate(t *testing.T, serverName string, serial int64, now time.Time) integrationCertificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate integration certificate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: serverName},
		Issuer:                pkix.Name{CommonName: "Nginx UIX local integration issuer"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{serverName},
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("issue integration certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("parse integration certificate: %v", err)
	}
	issued, err := certificate.ValidateIssuedCertificate([][]byte{raw}, key, []string{serverName}, now)
	if err != nil {
		t.Fatalf("validate integration certificate through production policy: %v", err)
	}
	return integrationCertificate{issued: issued, key: key, parsed: parsed}
}

func writeIntegrationCertificate(
	t *testing.T,
	prefix string,
	name string,
	material integrationCertificate,
) (string, string) {
	t.Helper()
	certificatePath := filepath.Join(prefix, "material", name+"-fullchain.pem")
	privateKeyPath := filepath.Join(prefix, "material", name+"-privkey.pem")
	privateKey, err := certificate.MarshalPrivateKeyPEM(material.key)
	if err != nil {
		t.Fatalf("marshal integration private key: %v", err)
	}
	for path, contents := range map[string][]byte{
		certificatePath: material.issued.FullChainPEM,
		privateKeyPath:  privateKey,
	} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatalf("write integration certificate material %q: %v", path, err)
		}
		information, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("inspect integration certificate material %q: %v", path, err)
		}
		if !information.Mode().IsRegular() || information.Mode().Perm() != 0o600 {
			t.Fatalf("integration certificate material %q mode = %v, want regular 0600", path, information.Mode())
		}
	}
	return certificatePath, privateKeyPath
}

func newGeneratedNginxHarness(
	t *testing.T,
	binary string,
	port int,
	configuration string,
) *nginxHarness {
	t.Helper()
	return newGeneratedNginxHarnessAtPrefix(
		t,
		binary,
		filepath.Join(t.TempDir(), "generated-prefix"),
		port,
		configuration,
	)
}

func newGeneratedNginxHarnessAtPrefix(
	t *testing.T,
	binary string,
	prefix string,
	port int,
	configuration string,
) *nginxHarness {
	t.Helper()
	for _, directory := range []string{prefix, filepath.Join(prefix, "logs"), filepath.Join(prefix, "runtime")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create generated Nginx directory %q: %v", directory, err)
		}
	}
	configPath := filepath.Join(prefix, "nginx.conf")
	writeGeneratedNginxConfiguration(t, configPath, configuration)
	harness := &nginxHarness{
		binary:     binary,
		prefix:     prefix,
		configPath: configPath,
		pidPath:    filepath.Join(prefix, "logs", "nginx.pid"),
		errorPath:  filepath.Join(prefix, "logs", "error.log"),
		port:       port,
	}
	t.Cleanup(func() {
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelCleanup()
		if err := harness.close(cleanupContext); err != nil {
			t.Errorf("cleanup generated isolated Nginx: %v", err)
		}
	})
	return harness
}

func writeGeneratedNginxConfiguration(t *testing.T, path, configuration string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(configuration), 0o600); err != nil {
		t.Fatalf("write generated Nginx configuration: %v", err)
	}
}

func httpChallengeConfiguration(port int, fragment string) string {
	return fmt.Sprintf(`worker_processes 1;
pid logs/nginx.pid;
error_log logs/error.log info;
events { worker_connections 32; }
http {
    access_log off;
    server {
        listen 127.0.0.1:%d;
        server_name example.test;
%s
        location / { return 404; }
    }
}
`, port, fragment)
}

func httpsSNIConfiguration(
	port int,
	firstCertificatePath, firstPrivateKeyPath string,
	secondCertificatePath, secondPrivateKeyPath string,
) string {
	return fmt.Sprintf(`worker_processes 1;
pid logs/nginx.pid;
error_log logs/error.log info;
events { worker_connections 32; }
http {
    access_log off;
    ssl_protocols TLSv1.2 TLSv1.3;
    server {
        listen 127.0.0.1:%d ssl;
        server_name first.example.test;
        ssl_certificate %q;
        ssl_certificate_key %q;
        return 200 "first";
    }
    server {
        listen 127.0.0.1:%d ssl;
        server_name second.example.test;
        ssl_certificate %q;
        ssl_certificate_key %q;
        return 200 "second";
    }
}
`, port, firstCertificatePath, firstPrivateKeyPath, port, secondCertificatePath, secondPrivateKeyPath)
}

func requestIntegrationHTTP(ctx context.Context, client *http.Client, requestURL, host string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("create integration request: %w", err)
	}
	request.Host = host
	response, err := client.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("perform integration request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return 0, "", fmt.Errorf("read integration response: %w", err)
	}
	if len(body) > 4096 {
		return 0, "", fmt.Errorf("integration response exceeds limit")
	}
	return response.StatusCode, string(body), nil
}

func waitForIntegrationHTTPStatus(
	ctx context.Context,
	client *http.Client,
	requestURL, host string,
	expected int,
) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, _, err := requestIntegrationHTTP(ctx, client, requestURL, host)
		if err == nil && status == expected {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for integration HTTP status %d: last status %d: %w", expected, status, ctx.Err())
		case <-ticker.C:
		}
	}
}

func readNginxMasterProcessID(t *testing.T, path string) int {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Nginx master PID: %v", err)
	}
	processID, err := strconv.Atoi(strings.TrimSpace(string(payload)))
	if err != nil {
		t.Fatalf("parse Nginx master PID: %v", err)
	}
	return processID
}

func assertIntegrationHTTPSResponse(
	t *testing.T,
	client *http.Client,
	requestURL, host, expectedBody string,
	expectedCertificate []byte,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, requestURL, nil)
	if err != nil {
		t.Fatalf("create integration HTTPS request: %v", err)
	}
	request.Host = host
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform integration HTTPS request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		t.Fatalf("read integration HTTPS response: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != expectedBody {
		t.Fatalf("integration HTTPS response = (%d, %q), want (200, %q)", response.StatusCode, body, expectedBody)
	}
	if response.TLS == nil || len(response.TLS.PeerCertificates) == 0 ||
		!bytes.Equal(response.TLS.PeerCertificates[0].Raw, expectedCertificate) {
		t.Fatal("integration HTTPS response did not present the SNI-selected certificate")
	}
}

const (
	integrationOptIn = "NGINX_UIX_INTEGRATION"
	nginxBinaryEnv   = "NGINX_BIN"
)

var workerProcessPattern = regexp.MustCompile(`start worker process ([0-9]+)`)

type nginxCommandResult struct {
	stdout string
	stderr string
}

type nginxHarness struct {
	binary     string
	prefix     string
	configPath string
	pidPath    string
	errorPath  string
	port       int

	mu               sync.Mutex
	command          *exec.Cmd
	masterProcessID  int
	workerProcessIDs []int
	waitDone         chan struct{}
	waitErr          error
	stdout           bytes.Buffer
	stderr           bytes.Buffer
}

func requireIntegrationNginx(t *testing.T) string {
	t.Helper()
	if os.Getenv(integrationOptIn) != "1" {
		t.Skip("real Nginx integration disabled; set NGINX_UIX_INTEGRATION=1")
	}

	binary := os.Getenv(nginxBinaryEnv)
	if binary == "" {
		binary = "nginx"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		t.Fatalf("resolve real Nginx binary %q: %v", binary, err)
	}
	return resolved
}

func nginxVersion(ctx context.Context, binary string) (string, error) {
	command := exec.CommandContext(ctx, binary, "-V")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("run nginx -V: %w", err)
	}
	for _, line := range strings.Split(stderr.String()+"\n"+stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nginx version:") {
			return line, nil
		}
	}
	return "", fmt.Errorf("parse nginx -V: version line is missing")
}

func newNginxHarness(t *testing.T, binary, fixtureName string) *nginxHarness {
	t.Helper()
	port := reserveLoopbackPort(t)
	prefix := filepath.Join(t.TempDir(), "prefix")
	fixtureRoot := filepath.Join("..", "fixtures", "nginx", fixtureName)
	if err := os.CopyFS(prefix, os.DirFS(fixtureRoot)); err != nil {
		t.Fatalf("copy Nginx fixture %q: %v", fixtureName, err)
	}

	configPath := filepath.Join(prefix, "nginx.conf")
	if err := replacePortPlaceholder(prefix, port); err != nil {
		t.Fatalf("assign isolated Nginx port: %v", err)
	}

	for _, directory := range []string{filepath.Join(prefix, "logs"), filepath.Join(prefix, "runtime")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create isolated Nginx runtime directory: %v", err)
		}
	}

	harness := &nginxHarness{
		binary:     binary,
		prefix:     prefix,
		configPath: configPath,
		pidPath:    filepath.Join(prefix, "logs", "nginx.pid"),
		errorPath:  filepath.Join(prefix, "logs", "error.log"),
		port:       port,
	}
	t.Cleanup(func() {
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelCleanup()
		if err := harness.close(cleanupContext); err != nil {
			t.Errorf("cleanup isolated Nginx: %v", err)
		}
	})
	return harness
}

func replacePortPlaceholder(root string, port int) error {
	placeholder := []byte("{{PORT}}")
	replacement := []byte(strconv.Itoa(port))
	replacements := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".conf" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read copied configuration %q: %w", path, err)
		}
		count := bytes.Count(contents, placeholder)
		if count == 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect copied configuration %q: %w", path, err)
		}
		if err := os.WriteFile(path, bytes.ReplaceAll(contents, placeholder, replacement), info.Mode().Perm()); err != nil {
			return fmt.Errorf("write copied configuration %q: %w", path, err)
		}
		replacements += count
		return nil
	})
	if err != nil {
		return err
	}
	if replacements == 0 {
		return fmt.Errorf("fixture contains no port placeholder")
	}
	return nil
}

func reserveLoopbackPort(t *testing.T) int {
	t.Helper()
	listenContext, cancelListen := context.WithTimeout(context.Background(), time.Second)
	defer cancelListen()
	listener, err := (&net.ListenConfig{}).Listen(listenContext, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		if closeErr := listener.Close(); closeErr != nil {
			t.Errorf("close unexpected listener: %v", closeErr)
		}
		t.Fatalf("reserved address type = %T, want *net.TCPAddr", listener.Addr())
	}
	port := address.Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved loopback port: %v", err)
	}
	return port
}

func (h *nginxHarness) run(ctx context.Context, operation string) (nginxCommandResult, error) {
	if operation != "-t" && operation != "-T" {
		return nginxCommandResult{}, fmt.Errorf("run isolated Nginx: unsupported operation %q", operation)
	}
	arguments := []string{operation, "-c", h.configPath, "-p", h.prefix + string(os.PathSeparator), "-e", h.errorPath}
	command := exec.CommandContext(ctx, h.binary, arguments...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return nginxCommandResult{stdout: stdout.String(), stderr: stderr.String()}, err
}

func (h *nginxHarness) reload(ctx context.Context) error {
	arguments := []string{
		"-c", h.configPath,
		"-p", h.prefix + string(os.PathSeparator),
		"-e", h.errorPath,
		"-s", "reload",
	}
	command := exec.CommandContext(ctx, h.binary, arguments...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("reload isolated Nginx: %w: stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	return nil
}

func (h *nginxHarness) start(ctx context.Context) error {
	h.mu.Lock()
	if h.command != nil {
		h.mu.Unlock()
		return fmt.Errorf("start isolated Nginx: already started")
	}
	arguments := []string{"-c", h.configPath, "-p", h.prefix + string(os.PathSeparator), "-e", h.errorPath, "-g", "daemon off;"}
	command := exec.CommandContext(ctx, h.binary, arguments...)
	command.Stdout = &h.stdout
	command.Stderr = &h.stderr
	command.WaitDelay = time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return command.Process.Signal(syscall.SIGQUIT)
	}
	if err := command.Start(); err != nil {
		h.mu.Unlock()
		return fmt.Errorf("start isolated Nginx command: %w", err)
	}
	h.command = command
	h.masterProcessID = command.Process.Pid
	h.waitDone = make(chan struct{})
	waitDone := h.waitDone
	h.mu.Unlock()

	go func() {
		err := command.Wait()
		h.mu.Lock()
		h.waitErr = err
		h.mu.Unlock()
		close(waitDone)
	}()

	readyContext, cancelReady := context.WithTimeout(ctx, 3*time.Second)
	defer cancelReady()
	if err := h.waitForReady(readyContext); err != nil {
		return fmt.Errorf("start isolated Nginx: %w", err)
	}
	return nil
}

func (h *nginxHarness) waitForReady(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(h.port))

	for {
		h.mu.Lock()
		waitDone := h.waitDone
		h.mu.Unlock()
		select {
		case <-waitDone:
			h.mu.Lock()
			waitErr := h.waitErr
			h.mu.Unlock()
			return fmt.Errorf("Nginx exited before readiness: %w", waitErr)
		default:
		}

		dialContext, cancelDial := context.WithTimeout(ctx, 100*time.Millisecond)
		connection, err := (&net.Dialer{}).DialContext(dialContext, "tcp", address)
		cancelDial()
		if err == nil {
			if closeErr := connection.Close(); closeErr != nil {
				return fmt.Errorf("close readiness connection: %w", closeErr)
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for isolated Nginx readiness: %w", ctx.Err())
		case <-waitDone:
			h.mu.Lock()
			waitErr := h.waitErr
			h.mu.Unlock()
			return fmt.Errorf("Nginx exited before readiness: %w", waitErr)
		case <-ticker.C:
		}
	}
}

func (h *nginxHarness) captureWorkerProcessIDs(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		contents, err := os.ReadFile(h.errorPath)
		if err == nil {
			matches := workerProcessPattern.FindAllSubmatch(contents, -1)
			workerProcessIDs := make([]int, 0, len(matches))
			for _, match := range matches {
				processID, conversionErr := strconv.Atoi(string(match[1]))
				if conversionErr != nil {
					return fmt.Errorf("parse worker process ID: %w", conversionErr)
				}
				if !slices.Contains(workerProcessIDs, processID) {
					workerProcessIDs = append(workerProcessIDs, processID)
				}
			}
			if len(workerProcessIDs) > 0 {
				h.mu.Lock()
				h.workerProcessIDs = workerProcessIDs
				h.mu.Unlock()
				return nil
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("read isolated Nginx error log: %w", err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("capture worker process IDs: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (h *nginxHarness) processIDs() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	processIDs := make([]int, 0, 1+len(h.workerProcessIDs))
	if h.masterProcessID > 0 {
		processIDs = append(processIDs, h.masterProcessID)
	}
	processIDs = append(processIDs, h.workerProcessIDs...)
	return processIDs
}

func (h *nginxHarness) waitForExit(ctx context.Context) error {
	h.mu.Lock()
	waitDone := h.waitDone
	h.mu.Unlock()
	if waitDone == nil {
		return nil
	}
	select {
	case <-waitDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for isolated Nginx exit: %w", ctx.Err())
	}
}

func (h *nginxHarness) close(ctx context.Context) error {
	h.mu.Lock()
	waitDone := h.waitDone
	masterProcessID := h.masterProcessID
	h.mu.Unlock()

	var closeErrors []error
	if waitDone != nil && !channelClosed(waitDone) {
		if err := syscall.Kill(masterProcessID, syscall.SIGQUIT); err != nil && !errors.Is(err, syscall.ESRCH) {
			closeErrors = append(closeErrors, fmt.Errorf("signal isolated Nginx master %d: %w", masterProcessID, err))
		}
		if err := h.waitForExit(ctx); err != nil {
			closeErrors = append(closeErrors, err)
			if killErr := syscall.Kill(masterProcessID, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
				closeErrors = append(closeErrors, fmt.Errorf("kill isolated Nginx master %d: %w", masterProcessID, killErr))
			}
			fallbackContext, cancelFallback := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			if fallbackErr := h.waitForExit(fallbackContext); fallbackErr != nil {
				closeErrors = append(closeErrors, fallbackErr)
			}
			cancelFallback()
		}
	}

	processIDs := h.processIDs()
	for _, processID := range processIDs {
		if err := waitForProcessGone(ctx, processID); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	for _, path := range h.runtimeArtifactPaths() {
		if err := os.RemoveAll(path); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("remove runtime artifact %q: %w", path, err))
		}
	}
	return errors.Join(closeErrors...)
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func waitForProcessGone(ctx context.Context, processID int) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := syscall.Kill(processID, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		} else if err != nil && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("check captured process %d: %w", processID, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("captured process %d still exists: %w", processID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (h *nginxHarness) runtimeArtifactPaths() []string {
	return []string{filepath.Join(h.prefix, "logs"), filepath.Join(h.prefix, "runtime")}
}

func configurationPaths(output, entryPath string) ([]string, error) {
	const markerPrefix = "# configuration file "
	normalized := []byte(strings.ReplaceAll(output, "\r\n", "\n"))
	entryMarker := []byte(markerPrefix + entryPath + ":\n")
	markerStart := bytes.Index(normalized, entryMarker)
	if markerStart < 0 || (markerStart > 0 && normalized[markerStart-1] != '\n') {
		return nil, fmt.Errorf("fixed Nginx entry marker is missing")
	}

	paths := make([]string, 0, 4)
	contentsByPath := make(map[string][]byte)
	rootPath := filepath.Dir(entryPath)
	for {
		lineEnd := bytes.IndexByte(normalized[markerStart:], '\n')
		if lineEnd < 0 {
			return nil, fmt.Errorf("Nginx configuration marker has no line ending")
		}
		lineEnd += markerStart
		line := string(normalized[markerStart:lineEnd])
		if !strings.HasPrefix(line, markerPrefix) || !strings.HasSuffix(line, ":") {
			return nil, fmt.Errorf("malformed Nginx configuration marker %q", line)
		}
		pathWithColon := strings.TrimPrefix(line, markerPrefix)
		configPath := strings.TrimSuffix(pathWithColon, ":")
		if !filepath.IsAbs(configPath) {
			return nil, fmt.Errorf("invalid Nginx configuration marker path %q", configPath)
		}
		configPath = filepath.Clean(configPath)
		relativePath, err := filepath.Rel(rootPath, configPath)
		if err != nil || relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("Nginx configuration marker escapes fixture root: %q", configPath)
		}

		contents, found := contentsByPath[configPath]
		if !found {
			contents, err = os.ReadFile(configPath)
			if err != nil {
				return nil, fmt.Errorf("read dumped Nginx configuration %q: %w", configPath, err)
			}
			contents = bytes.ReplaceAll(contents, []byte("\r\n"), []byte("\n"))
			contentsByPath[configPath] = contents
		}
		bodyStart := lineEnd + 1
		if len(contents) > len(normalized)-bodyStart {
			return nil, fmt.Errorf("dumped Nginx configuration %q is truncated", configPath)
		}
		bodyEnd := bodyStart + len(contents)
		if !bytes.Equal(normalized[bodyStart:bodyEnd], contents) {
			return nil, fmt.Errorf("dumped Nginx configuration %q differs from fixture", configPath)
		}
		if bodyEnd >= len(normalized) || normalized[bodyEnd] != '\n' {
			return nil, fmt.Errorf("dumped Nginx configuration %q has no separator", configPath)
		}
		paths = append(paths, configPath)
		markerStart = bodyEnd + 1
		if markerStart == len(normalized) {
			return paths, nil
		}
		if !bytes.HasPrefix(normalized[markerStart:], []byte(markerPrefix)) {
			return nil, fmt.Errorf("unexpected data after dumped Nginx configuration %q", configPath)
		}
	}
}

func assertProcessIDsGone(t *testing.T, processIDs []int) {
	t.Helper()
	for _, processID := range processIDs {
		if err := syscall.Kill(processID, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("captured process %d still exists: %v", processID, err)
		}
	}
}

func assertPathsAbsent(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("runtime artifact %q stat error = %v, want not exist", path, err)
		}
	}
}
