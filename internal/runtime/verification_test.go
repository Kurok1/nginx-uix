/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestVerifyRuntimeRequiresConfigIdentityRunningWorkersAndLoopbackHealth(t *testing.T) {
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
			}
			return commandResult{exitCode: 0}, nil
		},
		Status: func(context.Context) (Status, error) { return runningRestartStatus(100, 101), nil },
		Probe:  func(context.Context) (int, error) { return 204, nil }, ConfirmTimeout: time.Second,
	})
	request := config.RuntimeVerificationRequest{
		VerificationID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProductionDigest: digest,
	}
	result, err := service.VerifyRuntime(context.Background(), request)
	if err != nil || result.State != config.VerificationStateSucceeded || result.MasterPID != 100 ||
		result.WorkerCount != 1 || result.HTTPStatus != 204 || result.ProductionDigest != digest ||
		result.CheckedAt.IsZero() || supervisorCalled {
		t.Fatalf("VerifyRuntime() = %#v, %v, supervisor = %v", result, err, supervisorCalled)
	}

	service.restart.Status = func(context.Context) (Status, error) {
		return Status{State: StateUnknown}, errors.New("runtime identity unavailable")
	}
	failed, err := service.VerifyRuntime(context.Background(), request)
	if err == nil || failed.State != config.VerificationStateFailed || failed.ErrorCode != "runtime_identity_invalid" {
		t.Fatalf("failed VerifyRuntime() = %#v, %v", failed, err)
	}
}
