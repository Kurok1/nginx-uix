/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestExecuteRestartUsesOnlyFixedSupervisorCommandAndConfirmsNewRuntime(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	restarts := filepath.Join(root, "restarts")
	expectedExitMarker := filepath.Join(root, "run", "nginx-restart-expected")
	mustMkdirCandidate(t, production)
	mustMkdirCandidate(t, restarts)
	mustMkdirCandidate(t, filepath.Dir(expectedExitMarker))
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	digest := mustProductionDigest(t, production)
	var commands []commandSpec
	var commandLock sync.Mutex
	statuses := []Status{
		runningRestartStatus(100, 101),
		runningRestartStatus(200, 201),
		runningRestartStatus(200, 201),
	}
	statusIndex := 0
	service := mustRestartService(t, restartOptions{
		NginxRoot: production, RestartRoot: restarts, Entry: "nginx.conf", Limits: config.DefaultLimits(),
		ExpectedExitMarker: expectedExitMarker,
		Executor: func(_ context.Context, specification commandSpec) (commandResult, error) {
			if specification.executable == fixedSupervisorExecutable {
				payload, err := os.ReadFile(expectedExitMarker)
				if err != nil || string(payload) != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" {
					t.Fatalf("expected restart marker = %q, error = %v", payload, err)
				}
				information, err := os.Lstat(expectedExitMarker)
				if err != nil || !information.Mode().IsRegular() || information.Mode().Perm() != 0o600 {
					t.Fatalf("expected restart marker mode = %v, error = %v", information, err)
				}
				if err := os.Remove(expectedExitMarker); err != nil {
					t.Fatalf("consume expected restart marker: %v", err)
				}
			}
			commandLock.Lock()
			commands = append(commands, specification)
			commandLock.Unlock()
			return commandResult{exitCode: 0}, nil
		},
		Status: func(context.Context) (Status, error) {
			if statusIndex >= len(statuses) {
				return statuses[len(statuses)-1], nil
			}
			status := statuses[statusIndex]
			statusIndex++
			return status, nil
		},
		Probe: func(context.Context) (int, error) { return 204, nil }, ConfirmTimeout: time.Second,
	})
	request := config.RestartExecutionRequest{
		RestartID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProductionDigest: digest,
	}
	result, err := service.ExecuteRestart(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecuteRestart() error = %v", err)
	}
	if result.State != config.RestartStateSucceeded || result.Stage != config.RestartStageSucceeded ||
		result.BeforeMasterPID != 100 || result.AfterMasterPID != 200 || result.WorkerCount != 1 || result.HTTPStatus != 204 {
		t.Fatalf("restart result = %#v", result)
	}
	commandLock.Lock()
	defer commandLock.Unlock()
	var supervisor []commandSpec
	for _, specification := range commands {
		if specification.executable == fixedSupervisorExecutable {
			supervisor = append(supervisor, specification)
		}
	}
	if len(supervisor) != 1 || !slices.Equal(supervisor[0].arguments, []string{"-r", fixedNginxServiceDirectory}) ||
		supervisor[0].timeout != restartCommandTimeout || supervisor[0].maxOutputBytes != restartOutputLimit {
		t.Fatalf("supervisor commands = %#v", supervisor)
	}
	if _, err := os.Lstat(expectedExitMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected restart marker remains after success: %v", err)
	}
}

func TestExecuteRestartRejectsInvalidProductionBeforeSupervisor(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	restarts := filepath.Join(root, "restarts")
	mustMkdirCandidate(t, production)
	mustMkdirCandidate(t, restarts)
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	digest := mustProductionDigest(t, production)
	supervisorCalled := false
	service := mustRestartService(t, restartOptions{
		NginxRoot: production, RestartRoot: restarts, Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: func(_ context.Context, specification commandSpec) (commandResult, error) {
			if specification.executable == fixedSupervisorExecutable {
				supervisorCalled = true
				return commandResult{}, nil
			}
			return commandResult{exitCode: 1, stderr: []byte("invalid")}, &commandExitError{Code: 1}
		},
		Status: func(context.Context) (Status, error) { return runningRestartStatus(100, 101), nil },
		Probe:  func(context.Context) (int, error) { return 204, nil }, ConfirmTimeout: 100 * time.Millisecond,
	})
	result, err := service.ExecuteRestart(context.Background(), config.RestartExecutionRequest{
		RestartID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProductionDigest: digest,
	})
	if !errors.Is(err, ErrConfigInvalid) || result.State != config.RestartStateFailed ||
		result.ErrorCode != "restart_config_invalid" || supervisorCalled {
		t.Fatalf("invalid restart = %#v, error = %v, supervisor = %v", result, err, supervisorCalled)
	}
}

func TestExecuteRestartMarksAcceptedButUnconfirmedRuntimeNeedsAttention(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	restarts := filepath.Join(root, "restarts")
	mustMkdirCandidate(t, production)
	mustMkdirCandidate(t, restarts)
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	digest := mustProductionDigest(t, production)
	statusCalls := 0
	service := mustRestartService(t, restartOptions{
		NginxRoot: production, RestartRoot: restarts, Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: func(context.Context, commandSpec) (commandResult, error) { return commandResult{exitCode: 0}, nil },
		Status: func(context.Context) (Status, error) {
			statusCalls++
			if statusCalls == 1 {
				return runningRestartStatus(100, 101), nil
			}
			return Status{State: StateUnknown, Workers: []NginxProcess{}}, nil
		},
		Probe:          func(context.Context) (int, error) { return 0, errors.New("unavailable") },
		ConfirmTimeout: 30 * time.Millisecond,
	})
	result, err := service.ExecuteRestart(context.Background(), config.RestartExecutionRequest{
		RestartID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProductionDigest: digest,
	})
	if err == nil || result.State != config.RestartStateNeedsAttention ||
		result.ErrorCode != "restart_runtime_unconfirmed" {
		t.Fatalf("unconfirmed restart = %#v, error = %v", result, err)
	}
}

func TestExecuteRestartSupervisorTimeoutAndCancellationNeedAttention(t *testing.T) {
	for _, supervisorErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(supervisorErr.Error(), func(t *testing.T) {
			root := t.TempDir()
			production := filepath.Join(root, "production")
			restarts := filepath.Join(root, "restarts")
			expectedExitMarker := filepath.Join(restarts, ".nginx-restart-expected")
			mustMkdirCandidate(t, production)
			mustMkdirCandidate(t, restarts)
			mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
			digest := mustProductionDigest(t, production)
			service := mustRestartService(t, restartOptions{
				NginxRoot: production, RestartRoot: restarts, Entry: "nginx.conf", Limits: config.DefaultLimits(),
				Executor: func(_ context.Context, specification commandSpec) (commandResult, error) {
					if specification.executable == fixedSupervisorExecutable {
						return commandResult{}, supervisorErr
					}
					return commandResult{exitCode: 0}, nil
				},
				Status: func(context.Context) (Status, error) { return runningRestartStatus(100, 101), nil },
				Probe:  func(context.Context) (int, error) { return 204, nil }, ConfirmTimeout: time.Second,
			})
			result, err := service.ExecuteRestart(context.Background(), config.RestartExecutionRequest{
				RestartID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProductionDigest: digest,
			})
			if !errors.Is(err, supervisorErr) || result.State != config.RestartStateNeedsAttention ||
				result.ErrorCode != "restart_supervisor_uncertain" {
				t.Fatalf("uncertain supervisor result = %#v, error = %v", result, err)
			}
			if _, err := os.Lstat(expectedExitMarker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected restart marker remains after supervisor uncertainty: %v", err)
			}
		})
	}
}

func TestExecuteRestartPortConflictAfterAcceptedRequestNeedsAttention(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	restarts := filepath.Join(root, "restarts")
	mustMkdirCandidate(t, production)
	mustMkdirCandidate(t, restarts)
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	digest := mustProductionDigest(t, production)
	statusCalls := 0
	service := mustRestartService(t, restartOptions{
		NginxRoot: production, RestartRoot: restarts, Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: func(context.Context, commandSpec) (commandResult, error) {
			return commandResult{exitCode: 0}, nil
		},
		Status: func(context.Context) (Status, error) {
			statusCalls++
			if statusCalls == 1 {
				return runningRestartStatus(100, 101), nil
			}
			return Status{State: StateStopped}, nil
		},
		Probe: func(context.Context) (int, error) {
			return 0, errors.New("listen port is already in use")
		},
		ConfirmTimeout: 30 * time.Millisecond,
	})
	result, err := service.ExecuteRestart(context.Background(), config.RestartExecutionRequest{
		RestartID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProductionDigest: digest,
	})
	if err == nil || result.State != config.RestartStateNeedsAttention ||
		result.ErrorCode != "restart_runtime_unconfirmed" {
		t.Fatalf("port-conflict restart = %#v, error = %v", result, err)
	}
}

func runningRestartStatus(masterPID, workerPID int) Status {
	return Status{
		State: StateRunning, Master: &NginxProcess{PID: masterPID},
		Workers: []NginxProcess{{PID: workerPID, Role: ProcessRoleWorker}}, Issues: []string{},
	}
}

func mustRestartService(t *testing.T, options restartOptions) *Service {
	t.Helper()
	service, err := newRestartService(options)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
