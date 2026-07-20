/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestRestoreWithRealIsolatedNginx(t *testing.T) {
	binary := recoveryIntegrationNginx(t)
	root := t.TempDir()
	production := filepath.Join(root, "production")
	backups := filepath.Join(root, "backups")
	restores := filepath.Join(root, "restores")
	for _, directory := range []string{production, backups, restores, filepath.Join(production, "conf.d")} {
		mustMkdirCandidate(t, directory)
	}
	t.Cleanup(func() {
		if err := thawBackupFixture(backups); err != nil {
			t.Errorf("thaw recovery backups: %v", err)
		}
	})
	port := reserveReleaseIntegrationPort(t)
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"),
		"error_log stderr notice;\nevents {}\nhttp { access_log off; include conf.d/*.conf; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "conf.d", "site.conf"),
		fmt.Sprintf("server { listen 127.0.0.1:%d; return 204; }\n", port), 0o640)
	targetDigest := mustProductionDigest(t, production)
	backupService := mustBackupService(t, backupOptions{
		NginxRoot: production, BackupRoot: backups, Limits: config.DefaultLimits(),
	})
	target, err := backupService.CreateBackup(context.Background(), config.BackupRequest{
		ReleaseID: "11111111111111111111111111111111", BackupID: "22222222222222222222222222222222",
		ProductionDigest: targetDigest,
	})
	if err != nil {
		t.Fatalf("create restore target: %v", err)
	}
	mustWriteCandidate(t, filepath.Join(production, "conf.d", "site.conf"),
		fmt.Sprintf("server { listen 127.0.0.1:%d; return 200; }\n", port), 0o640)
	sourceDigest := mustProductionDigest(t, production)

	harness := newRecoveryNginxHarness(t, binary, production, port)
	harness.start(http.StatusOK)
	masterBefore := harness.masterPID()
	workersBefore, err := releaseIntegrationWorkerPIDs(context.Background(), masterBefore)
	if err != nil || len(workersBefore) == 0 {
		t.Fatalf("read restore baseline workers: %v, workers = %v", err, workersBefore)
	}
	service := mustRestoreService(t, restoreOptions{
		NginxRoot: production, BackupRoot: backups, RestoreRoot: restores,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Executor: harness.execute,
		Status: harness.status, Probe: harness.probe, ConfirmTimeout: 5 * time.Second,
	})
	request := config.RestoreExecutionRequest{
		RestoreID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TargetBackupID: target.BackupID,
		SafetyBackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceDigest: sourceDigest,
		TargetDigest: targetDigest, TargetTreeDigest: target.TreeDigest,
	}
	prepared, err := service.PrepareRestore(context.Background(), request)
	if err != nil {
		t.Fatalf("prepare real restore: %v; stderr = %q", err, harness.stderr.String())
	}
	request.SafetyTreeDigest = prepared.SafetyBackup.TreeDigest
	result, err := service.ExecuteRestore(context.Background(), request)
	if err != nil {
		t.Fatalf("execute real restore: %v; result = %#v; stderr = %q", err, result, harness.stderr.String())
	}
	if result.State != config.RestoreStateSucceeded || result.Stage != config.RestoreStageSucceeded ||
		result.MasterPID != masterBefore || result.WorkerCount == 0 || result.HTTPStatus != http.StatusNoContent {
		t.Fatalf("real restore result = %#v", result)
	}
	if err := waitForReleaseHTTP(context.Background(), harness.address, http.StatusNoContent); err != nil {
		t.Fatalf("wait for restored HTTP: %v", err)
	}
	workersAfter, err := waitForReplacementWorkers(context.Background(), masterBefore, workersBefore)
	if err != nil || slices.Equal(workersBefore, workersAfter) {
		t.Fatalf("restore workers were not replaced: before = %v, after = %v, error = %v", workersBefore, workersAfter, err)
	}
	if digest := mustProductionDigest(t, production); digest != targetDigest {
		t.Fatalf("real restored production digest = %s, want %s", digest, targetDigest)
	}
}

func TestRestartWithRealIsolatedNginx(t *testing.T) {
	binary := recoveryIntegrationNginx(t)
	root := t.TempDir()
	production := filepath.Join(root, "production")
	restarts := filepath.Join(root, "restarts")
	for _, directory := range []string{production, restarts, filepath.Join(production, "conf.d")} {
		mustMkdirCandidate(t, directory)
	}
	port := reserveReleaseIntegrationPort(t)
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"),
		"error_log stderr notice;\nevents {}\nhttp { access_log off; include conf.d/*.conf; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "conf.d", "site.conf"),
		fmt.Sprintf("server { listen 127.0.0.1:%d; return 200; }\n", port), 0o640)
	productionDigest := mustProductionDigest(t, production)

	harness := newRecoveryNginxHarness(t, binary, production, port)
	harness.start(http.StatusOK)
	masterBefore := harness.masterPID()
	service := mustRestartService(t, restartOptions{
		NginxRoot: production, RestartRoot: restarts, Entry: "nginx.conf",
		Limits: config.DefaultLimits(), Executor: harness.execute,
		Status: harness.status, Probe: harness.probe, ConfirmTimeout: 5 * time.Second,
	})
	result, err := service.ExecuteRestart(context.Background(), config.RestartExecutionRequest{
		RestartID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProductionDigest: productionDigest,
	})
	if err != nil {
		t.Fatalf("execute real restart: %v; result = %#v; stderr = %q", err, result, harness.stderr.String())
	}
	if result.State != config.RestartStateSucceeded || result.Stage != config.RestartStageSucceeded ||
		result.BeforeMasterPID != masterBefore || result.AfterMasterPID == masterBefore ||
		result.WorkerCount == 0 || result.HTTPStatus != http.StatusOK || harness.supervisorCalls != 1 {
		t.Fatalf("real restart result = %#v, supervisor calls = %d", result, harness.supervisorCalls)
	}
	if err := waitForReleaseHTTP(context.Background(), harness.address, http.StatusOK); err != nil {
		t.Fatalf("wait for restarted HTTP: %v", err)
	}
}

type recoveryNginxHarness struct {
	t               *testing.T
	binary          string
	prefix          string
	configuration   string
	errorLog        string
	pidPath         string
	address         string
	current         *exec.Cmd
	waitDone        chan error
	stderr          bytes.Buffer
	supervisorCalls int
}

func newRecoveryNginxHarness(
	t *testing.T,
	binary string,
	production string,
	port int,
) *recoveryNginxHarness {
	t.Helper()
	root := filepath.Dir(production)
	return &recoveryNginxHarness{
		t: t, binary: binary, prefix: production + string(os.PathSeparator),
		configuration: filepath.Join(production, "nginx.conf"),
		errorLog:      filepath.Join(root, "recovery-nginx-error.log"),
		pidPath:       filepath.Join(root, "recovery-nginx.pid"),
		address:       fmt.Sprintf("127.0.0.1:%d", port),
	}
}

func (h *recoveryNginxHarness) start(wantStatus int) {
	h.t.Helper()
	if err := h.launch(); err != nil {
		h.t.Fatalf("start isolated recovery Nginx: %v", err)
	}
	h.t.Cleanup(h.stop)
	if err := waitForReleaseHTTP(context.Background(), h.address, wantStatus); err != nil {
		h.t.Fatalf("wait for isolated recovery Nginx: %v; stderr = %q", err, h.stderr.String())
	}
}

func (h *recoveryNginxHarness) launch() error {
	command := exec.Command(h.binary, "-p", h.prefix, "-c", h.configuration, "-e", h.errorLog,
		"-g", "pid "+h.pidPath+"; daemon off;")
	command.Stderr = &h.stderr
	if err := command.Start(); err != nil {
		return err
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	h.current = command
	h.waitDone = waitDone
	return nil
}

func (h *recoveryNginxHarness) stop() {
	if h.current == nil || h.current.Process == nil {
		return
	}
	_ = h.current.Process.Signal(syscall.SIGQUIT)
	select {
	case <-h.waitDone:
	case <-time.After(3 * time.Second):
		_ = h.current.Process.Kill()
		<-h.waitDone
	}
	h.current = nil
}

func (h *recoveryNginxHarness) restart(ctx context.Context) error {
	if h.current == nil || h.current.Process == nil {
		return errors.New("isolated recovery Nginx is unavailable")
	}
	if err := h.current.Process.Signal(syscall.SIGQUIT); err != nil {
		return err
	}
	select {
	case err := <-h.waitDone:
		if err != nil {
			return fmt.Errorf("stop isolated recovery Nginx: %w", err)
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
		return errors.New("stop isolated recovery Nginx: timeout")
	}
	h.current = nil
	return h.launch()
}

func (h *recoveryNginxHarness) masterPID() int {
	if h.current == nil || h.current.Process == nil {
		return 0
	}
	return h.current.Process.Pid
}

func (h *recoveryNginxHarness) status(ctx context.Context) (Status, error) {
	masterPID := h.masterPID()
	if masterPID == 0 || h.current.Process.Signal(syscall.Signal(0)) != nil {
		return Status{}, errors.New("isolated recovery Nginx is not running")
	}
	workers, err := releaseIntegrationWorkerPIDs(ctx, masterPID)
	if err != nil || len(workers) == 0 {
		return Status{}, errors.Join(errors.New("read isolated recovery Nginx workers"), err)
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

func (h *recoveryNginxHarness) probe(ctx context.Context) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+h.address+"/", nil)
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

func (h *recoveryNginxHarness) execute(ctx context.Context, specification commandSpec) (commandResult, error) {
	switch {
	case specification.executable == fixedSupervisorExecutable &&
		slices.Equal(specification.arguments, []string{"-r", fixedNginxServiceDirectory}):
		h.supervisorCalls++
		if err := h.restart(ctx); err != nil {
			return commandResult{}, err
		}
		return commandResult{exitCode: 0}, nil
	case specification.executable == nginxExecutable:
		specification.executable = h.binary
		switch {
		case slices.Equal(specification.arguments, []string{"-t", "-c", h.configuration}):
			specification.arguments = append(specification.arguments, "-p", h.prefix, "-e", h.errorLog)
		case slices.Equal(specification.arguments, []string{"-s", "reload"}):
			specification.arguments = append(specification.arguments,
				"-p", h.prefix, "-c", h.configuration, "-e", h.errorLog, "-g", "pid "+h.pidPath+";")
		case len(specification.arguments) >= 3 && specification.arguments[0] == "-t" &&
			specification.arguments[1] == "-p":
			specification.arguments = append(specification.arguments,
				"-e", filepath.Join(specification.arguments[2], "validator-error.log"))
		default:
			return commandResult{}, fmt.Errorf("unsupported recovery Nginx operation: %v", specification.arguments)
		}
		return executeCommand(ctx, specification)
	default:
		return commandResult{}, fmt.Errorf("unsupported recovery executable: %s", specification.executable)
	}
}

func recoveryIntegrationNginx(t *testing.T) string {
	t.Helper()
	if os.Getenv("NGINX_UIX_INTEGRATION") != "1" {
		t.Skip("real Nginx integration disabled; set NGINX_UIX_INTEGRATION=1")
	}
	binary := os.Getenv("NGINX_BIN")
	if binary == "" {
		binary = "nginx"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		t.Fatalf("resolve real Nginx binary: %v", err)
	}
	return resolved
}
