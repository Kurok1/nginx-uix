/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
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
