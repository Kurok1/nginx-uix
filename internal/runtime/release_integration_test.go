/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestReleaseWithRealIsolatedNginx(t *testing.T) {
	if os.Getenv("NGINX_UIX_INTEGRATION") != "1" {
		t.Skip("real Nginx integration disabled; set NGINX_UIX_INTEGRATION=1")
	}
	binary := os.Getenv("NGINX_BIN")
	if binary == "" {
		binary = "nginx"
	}
	resolvedBinary, err := exec.LookPath(binary)
	if err != nil {
		t.Fatalf("resolve real Nginx binary: %v", err)
	}
	port := reserveReleaseIntegrationPort(t)
	original := fmt.Sprintf("server { listen 127.0.0.1:%d; return 200; }\n", port)
	draft := fmt.Sprintf("server { listen 127.0.0.1:%d; return 204; }\n", port)
	fixture := newReleaseFixtureWithConfig(
		t,
		"error_log stderr notice;\nevents {}\nhttp { access_log off; include conf.d/*.conf; }\n",
		original,
		draft,
	)
	prefix := fixture.production + string(os.PathSeparator)
	configuration := filepath.Join(fixture.production, "nginx.conf")
	errorLog := filepath.Join(fixture.root, "launcher-error.log")
	pidPath := filepath.Join(fixture.root, "nginx.pid")
	command := exec.Command(resolvedBinary, "-p", prefix, "-c", configuration, "-e", errorLog, "-g", "pid "+pidPath+"; daemon off;")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start isolated Nginx: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGQUIT)
		}
		select {
		case <-waitDone:
		case <-time.After(3 * time.Second):
			if command.Process != nil {
				_ = command.Process.Kill()
			}
			<-waitDone
		}
	})

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := waitForReleaseHTTP(context.Background(), address, http.StatusOK); err != nil {
		t.Fatalf("wait for isolated Nginx: %v; stderr = %q", err, stderr.String())
	}
	masterPID := command.Process.Pid
	workersBefore, err := releaseIntegrationWorkerPIDs(context.Background(), masterPID)
	if err != nil || len(workersBefore) == 0 {
		t.Fatalf("read initial workers: %v, workers = %v", err, workersBefore)
	}

	executor := func(ctx context.Context, specification commandSpec) (commandResult, error) {
		specification.executable = resolvedBinary
		switch {
		case slices.Equal(specification.arguments, []string{"-t", "-c", configuration}):
			specification.arguments = append(specification.arguments, "-p", prefix, "-e", errorLog)
		case slices.Equal(specification.arguments, []string{"-s", "reload"}):
			specification.arguments = append(specification.arguments, "-p", prefix, "-c", configuration, "-e", errorLog, "-g", "pid "+pidPath+";")
		case len(specification.arguments) >= 3 && specification.arguments[0] == "-t" && specification.arguments[1] == "-p":
			specification.arguments = append(specification.arguments, "-e", filepath.Join(specification.arguments[2], "logs", "validator-error.log"))
		}
		return executeCommand(ctx, specification)
	}
	service := fixture.service(t, executor)
	service.release.Status = func(ctx context.Context) (Status, error) {
		if err := command.Process.Signal(syscall.Signal(0)); err != nil {
			return Status{}, err
		}
		workers, err := releaseIntegrationWorkerPIDs(ctx, masterPID)
		if err != nil || len(workers) == 0 {
			return Status{}, fmt.Errorf("read isolated Nginx workers: %w", err)
		}
		processes := make([]NginxProcess, 0, len(workers))
		for _, pid := range workers {
			processes = append(processes, NginxProcess{PID: pid, Role: ProcessRoleWorker})
		}
		return Status{
			SampledAt: time.Now().UTC(), State: StateRunning,
			Master: &NginxProcess{PID: masterPID, Role: ProcessRoleMaster}, Workers: processes,
		}, nil
	}
	service.release.Probe = func(ctx context.Context) (int, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/", nil)
		if err != nil {
			return 0, err
		}
		client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		response, err := client.Do(request)
		if err != nil {
			return 0, err
		}
		return response.StatusCode, response.Body.Close()
	}

	request := fixture.request(t)
	result, err := service.ExecuteRelease(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecuteRelease() error = %v; stderr = %q", err, stderr.String())
	}
	if result.State != config.ReleaseStateSucceeded || result.Stage != config.ReleaseStageCommitted ||
		result.MasterPID != masterPID || result.HTTPStatus != http.StatusNoContent {
		t.Fatalf("ExecuteRelease() = %+v", result)
	}
	if err := waitForReleaseHTTP(context.Background(), address, http.StatusNoContent); err != nil {
		t.Fatalf("wait for released configuration: %v", err)
	}
	workersAfter, err := waitForReplacementWorkers(context.Background(), masterPID, workersBefore)
	if err != nil {
		t.Fatalf("wait for replacement workers: %v", err)
	}
	if slices.Equal(workersBefore, workersAfter) {
		t.Fatalf("workers were not replaced: before = %v, after = %v", workersBefore, workersAfter)
	}
}

func reserveReleaseIntegrationPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback port: %v", err)
	}
	return port
}

func waitForReleaseHTTP(parent context.Context, address string, want int) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/", nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode == want {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForReplacementWorkers(parent context.Context, masterPID int, before []int) ([]int, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		workers, err := releaseIntegrationWorkerPIDs(ctx, masterPID)
		if err == nil && len(workers) > 0 && !slices.Equal(workers, before) {
			return workers, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func releaseIntegrationWorkerPIDs(ctx context.Context, masterPID int) ([]int, error) {
	command := exec.CommandContext(ctx, "ps", "-ax", "-o", "pid=", "-o", "ppid=")
	payload, err := command.Output()
	if err != nil {
		return nil, err
	}
	workers := make([]int, 0)
	for _, line := range strings.Split(string(payload), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr == nil && parentErr == nil && parent == masterPID {
			workers = append(workers, pid)
		}
	}
	slices.Sort(workers)
	return workers, nil
}
