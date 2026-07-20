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
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestPrepareAndExecuteRestoreValidatesTargetCreatesSafetyBackupAndConfirmsRuntime(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	backups := filepath.Join(root, "backups")
	restores := filepath.Join(root, "restores")
	for _, directory := range []string{production, backups, restores} {
		mustMkdirCandidate(t, directory)
	}
	t.Cleanup(func() {
		if err := thawBackupFixture(backups); err != nil {
			t.Errorf("thaw restore backups: %v", err)
		}
	})
	targetContents := "events { worker_connections 64; }\n"
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), targetContents, 0o640)
	targetDigest := mustProductionDigest(t, production)
	backupService := mustBackupService(t, backupOptions{
		NginxRoot: production, BackupRoot: backups, Limits: config.DefaultLimits(),
	})
	targetEvidence, err := backupService.CreateBackup(context.Background(), config.BackupRequest{
		ReleaseID: "11111111111111111111111111111111", BackupID: "22222222222222222222222222222222",
		ProductionDigest: targetDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceContents := "events { worker_connections 32; }\n"
	if err := os.WriteFile(filepath.Join(production, "nginx.conf"), []byte(sourceContents), 0o640); err != nil {
		t.Fatal(err)
	}
	sourceDigest := mustProductionDigest(t, production)
	statusCalls := 0
	reloadCalls := 0
	service := mustRestoreService(t, restoreOptions{
		NginxRoot: production, BackupRoot: backups, RestoreRoot: restores,
		Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: func(_ context.Context, specification commandSpec) (commandResult, error) {
			if len(specification.arguments) >= 2 && specification.arguments[0] == "-s" && specification.arguments[1] == "reload" {
				reloadCalls++
			}
			return commandResult{exitCode: 0}, nil
		},
		Status: func(context.Context) (Status, error) {
			statusCalls++
			if statusCalls <= 1 {
				return runningRestartStatus(100, 101), nil
			}
			return runningRestartStatus(100, 201), nil
		},
		Probe: func(context.Context) (int, error) { return 204, nil }, ConfirmTimeout: time.Second,
	})
	request := config.RestoreExecutionRequest{
		RestoreID:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TargetBackupID: targetEvidence.BackupID, SafetyBackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		SourceDigest: sourceDigest, TargetDigest: targetDigest, TargetTreeDigest: targetEvidence.TreeDigest,
	}
	prepared, err := service.PrepareRestore(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareRestore() error = %v", err)
	}
	if prepared.State != config.RestoreStateRunning || prepared.Stage != config.RestoreStageSafetyBackupVerified ||
		prepared.SafetyBackup.BackupID != request.SafetyBackupID ||
		prepared.SafetyBackup.OriginType != config.BackupOriginRestore ||
		prepared.SafetyBackup.OriginID != string(request.RestoreID) ||
		prepared.SafetyBackup.ProductionDigest != sourceDigest {
		t.Fatalf("prepared restore = %#v", prepared)
	}
	if digest := mustProductionDigest(t, production); digest != sourceDigest {
		t.Fatalf("production changed during preparation: %s", digest)
	}
	request.SafetyTreeDigest = prepared.SafetyBackup.TreeDigest
	result, err := service.ExecuteRestore(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecuteRestore() error = %v", err)
	}
	if result.State != config.RestoreStateSucceeded || result.Stage != config.RestoreStageSucceeded ||
		result.SafetyBackup.TreeDigest != request.SafetyTreeDigest || result.MasterPID != 100 ||
		result.WorkerCount != 1 || result.HTTPStatus != 204 || reloadCalls != 1 {
		t.Fatalf("restore result = %#v, reloads = %d", result, reloadCalls)
	}
	if digest := mustProductionDigest(t, production); digest != targetDigest {
		t.Fatalf("restored production digest = %s, want %s", digest, targetDigest)
	}
}

func TestPrepareRestoreRejectsCorruptTargetWithoutChangingProduction(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	backups := filepath.Join(root, "backups")
	restores := filepath.Join(root, "restores")
	for _, directory := range []string{production, backups, restores} {
		mustMkdirCandidate(t, directory)
	}
	t.Cleanup(func() {
		if err := thawBackupFixture(backups); err != nil {
			t.Errorf("thaw restore backups: %v", err)
		}
	})
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events { worker_connections 64; }\n", 0o640)
	targetDigest := mustProductionDigest(t, production)
	backupService := mustBackupService(t, backupOptions{NginxRoot: production, BackupRoot: backups, Limits: config.DefaultLimits()})
	evidence, err := backupService.CreateBackup(context.Background(), config.BackupRequest{
		ReleaseID: "11111111111111111111111111111111", BackupID: "22222222222222222222222222222222",
		ProductionDigest: targetDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(backups, string(evidence.BackupID))
	if err := thawBackupFixture(backupPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "tree", "nginx.conf"), []byte("tampered\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backupPath, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(backupPath, "tree"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(production, "nginx.conf"), []byte("events { worker_connections 32; }\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	sourceDigest := mustProductionDigest(t, production)
	service := mustRestoreService(t, restoreOptions{
		NginxRoot: production, BackupRoot: backups, RestoreRoot: restores,
		Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: func(context.Context, commandSpec) (commandResult, error) { return commandResult{exitCode: 0}, nil },
		Status:   func(context.Context) (Status, error) { return runningRestartStatus(100, 101), nil },
		Probe:    func(context.Context) (int, error) { return 204, nil }, ConfirmTimeout: time.Second,
	})
	result, err := service.PrepareRestore(context.Background(), config.RestoreExecutionRequest{
		RestoreID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TargetBackupID: evidence.BackupID,
		SafetyBackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceDigest: sourceDigest,
		TargetDigest: targetDigest, TargetTreeDigest: evidence.TreeDigest,
	})
	if !errors.Is(err, config.ErrSnapshotChanged) || result.State != config.RestoreStateFailed ||
		result.ErrorCode != "restore_target_invalid" {
		t.Fatalf("corrupt preparation = %#v, error = %v", result, err)
	}
	if digest := mustProductionDigest(t, production); digest != sourceDigest {
		t.Fatalf("production changed after corrupt target: %s", digest)
	}
}

func TestExecuteRestoreRollsBackToSafetyBackupWhenReloadFails(t *testing.T) {
	service, request, production, sourceDigest, reloadCalls, injected := preparedRestoreFailureFixture(t, 1)

	result, err := service.ExecuteRestore(context.Background(), request)
	if !errors.Is(err, injected) || result.State != config.RestoreStateRolledBack ||
		result.Stage != config.RestoreStageRolledBack || result.ErrorCode != "restore_reload_failed" ||
		*reloadCalls != 2 {
		t.Fatalf("rolled back restore = %#v, reloads = %d, error = %v", result, *reloadCalls, err)
	}
	if digest := mustProductionDigest(t, production); digest != sourceDigest {
		t.Fatalf("rolled back production digest = %s, want %s", digest, sourceDigest)
	}
	for _, stage := range []config.RestoreStageName{
		config.RestoreStageRollbackApplying,
		config.RestoreStageRollbackFilesRestored,
		config.RestoreStageRollbackValidated,
		config.RestoreStageRollbackReloadRequested,
		config.RestoreStageRolledBack,
	} {
		if !restoreResultHasStage(result, stage) {
			t.Fatalf("restore stages do not contain %s: %#v", stage, result.Stages)
		}
	}
}

func TestExecuteRestoreNeedsAttentionWhenRollbackReloadCannotBeConfirmed(t *testing.T) {
	service, request, production, sourceDigest, reloadCalls, injected := preparedRestoreFailureFixture(t, 2)

	result, err := service.ExecuteRestore(context.Background(), request)
	if !errors.Is(err, injected) || result.State != config.RestoreStateNeedsAttention ||
		result.Stage != config.RestoreStageNeedsAttention ||
		result.ErrorCode != "restore_rollback_reload_failed" || *reloadCalls != 2 {
		t.Fatalf("uncertain restore = %#v, reloads = %d, error = %v", result, *reloadCalls, err)
	}
	if digest := mustProductionDigest(t, production); digest != sourceDigest {
		t.Fatalf("needs-attention production digest = %s, want restored safety digest %s", digest, sourceDigest)
	}
	if !restoreResultHasStage(result, config.RestoreStageNeedsAttention) {
		t.Fatalf("restore stages do not contain needs_attention: %#v", result.Stages)
	}
}

func TestRecoverRestoreResumesEveryRollbackStageWithoutRegression(t *testing.T) {
	stages := []config.RestoreStageName{
		config.RestoreStageRollbackApplying,
		config.RestoreStageRollbackFilesRestored,
		config.RestoreStageRollbackValidated,
		config.RestoreStageRollbackReloadRequested,
	}
	for _, interruptedStage := range stages {
		t.Run(string(interruptedStage), func(t *testing.T) {
			service, request, production, sourceDigest, _, _ := preparedRestoreFailureFixture(t, 0)
			advanceRestoreToRollbackStage(t, service, request, interruptedStage)

			result, err := service.RecoverRestore(context.Background(), request)
			if err == nil || result.State != config.RestoreStateRolledBack ||
				result.Stage != config.RestoreStageRolledBack || result.ErrorCode != "interrupted_restore" {
				t.Fatalf("RecoverRestore() = %#v, error = %v", result, err)
			}
			if digest := mustProductionDigest(t, production); digest != sourceDigest {
				t.Fatalf("recovered production digest = %s, want %s", digest, sourceDigest)
			}
			progress, progressErr := service.RestoreProgress(context.Background(), request)
			if progressErr != nil || progress.State != config.RestoreStateRolledBack ||
				progress.Stage != config.RestoreStageRolledBack {
				t.Fatalf("RestoreProgress() = %#v, error = %v", progress, progressErr)
			}
		})
	}
}

func TestRecoverRestoreClassifiesEveryForwardDurableStage(t *testing.T) {
	testCases := []struct {
		name      string
		stage     config.RestoreStageName
		content   string
		wantState config.RestoreState
	}{
		{name: "before first file mutation", stage: config.RestoreStageFilesRestoring,
			content: "source", wantState: config.RestoreStateFailed},
		{name: "partial file mutation", stage: config.RestoreStageFilesRestoring,
			content: "partial", wantState: config.RestoreStateRolledBack},
		{name: "files restored", stage: config.RestoreStageFilesRestored,
			content: "target", wantState: config.RestoreStateSucceeded},
		{name: "production validated", stage: config.RestoreStageProductionValidated,
			content: "target", wantState: config.RestoreStateSucceeded},
		{name: "reload requested", stage: config.RestoreStageReloadRequested,
			content: "target", wantState: config.RestoreStateSucceeded},
		{name: "runtime confirmed", stage: config.RestoreStageRuntimeConfirmed,
			content: "target", wantState: config.RestoreStateSucceeded},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service, request, production, sourceDigest, _, _ := preparedRestoreFailureFixture(t, 0)
			advanceRestoreToForwardStage(t, service, request, testCase.stage, testCase.content)

			result, err := service.RecoverRestore(context.Background(), request)
			if result.State != testCase.wantState ||
				(testCase.wantState == config.RestoreStateSucceeded && err != nil) ||
				(testCase.wantState != config.RestoreStateSucceeded && err == nil) {
				t.Fatalf("RecoverRestore() = %#v, error = %v, want state %s", result, err, testCase.wantState)
			}
			wantDigest := sourceDigest
			if testCase.wantState == config.RestoreStateSucceeded {
				wantDigest = request.TargetDigest
			}
			if digest := mustProductionDigest(t, production); digest != wantDigest {
				t.Fatalf("recovered production digest = %s, want %s", digest, wantDigest)
			}
			progress, progressErr := service.RestoreProgress(context.Background(), request)
			if progressErr != nil || progress.State != testCase.wantState {
				t.Fatalf("RestoreProgress() = %#v, error = %v", progress, progressErr)
			}
		})
	}
}

func advanceRestoreToForwardStage(
	t *testing.T,
	service *Service,
	request config.RestoreExecutionRequest,
	interruptedStage config.RestoreStageName,
	content string,
) {
	t.Helper()
	controlPath := filepath.Join(service.restore.RestoreRoot, string(request.RestoreID), "control")
	journal, stages, err := readRestoreEvidence(context.Background(), controlPath, request)
	if err != nil {
		t.Fatalf("read prepared restore evidence: %v", err)
	}
	result := restoreExecutionFromEvidence(journal, stages)
	appendStage := restoreExecutionAppender(service, controlPath, &journal, &result)
	if err := appendStage(config.RestoreStageFilesRestoring, config.StageResultRunning, ""); err != nil {
		t.Fatalf("append files_restoring: %v", err)
	}
	if content == "source" {
		return
	}
	journal.DurableOperation = 1
	if content == "partial" {
		mustWriteCandidate(t, filepath.Join(service.restore.NginxRoot, "nginx.conf"),
			"events { worker_connections 16; }\n", 0o640)
		if err := writeRestoreJournal(controlPath, journal); err != nil {
			t.Fatalf("persist partial restore journal: %v", err)
		}
		return
	}
	target, err := service.VerifyBackup(context.Background(), request.TargetBackupID)
	if err != nil {
		t.Fatalf("verify restore target: %v", err)
	}
	if err := service.applyVerifiedBackup(context.Background(), service.restore, target); err != nil {
		t.Fatalf("apply restore target: %v", err)
	}
	forwardStages := []config.RestoreStageName{
		config.RestoreStageFilesRestored,
		config.RestoreStageProductionValidated,
		config.RestoreStageReloadRequested,
		config.RestoreStageRuntimeConfirmed,
	}
	for _, stage := range forwardStages {
		if err := appendStage(stage, config.StageResultSuccess, ""); err != nil {
			t.Fatalf("append %s: %v", stage, err)
		}
		if stage == interruptedStage {
			return
		}
	}
	t.Fatalf("unsupported forward restore stage %s", interruptedStage)
}

func advanceRestoreToRollbackStage(
	t *testing.T,
	service *Service,
	request config.RestoreExecutionRequest,
	interruptedStage config.RestoreStageName,
) {
	t.Helper()
	controlPath := filepath.Join(service.restore.RestoreRoot, string(request.RestoreID), "control")
	journal, stages, err := readRestoreEvidence(context.Background(), controlPath, request)
	if err != nil {
		t.Fatalf("read prepared restore evidence: %v", err)
	}
	result := restoreExecutionFromEvidence(journal, stages)
	appendStage := restoreExecutionAppender(service, controlPath, &journal, &result)
	target, err := service.VerifyBackup(context.Background(), request.TargetBackupID)
	if err != nil {
		t.Fatalf("verify restore target: %v", err)
	}
	if err := service.applyVerifiedBackup(context.Background(), service.restore, target); err != nil {
		t.Fatalf("apply restore target: %v", err)
	}
	if err := appendStage(config.RestoreStageFilesRestoring, config.StageResultRunning, ""); err != nil {
		t.Fatalf("append files_restoring: %v", err)
	}
	journal.DurableOperation = 1
	if err := appendStage(config.RestoreStageFilesRestored, config.StageResultSuccess, ""); err != nil {
		t.Fatalf("append files_restored: %v", err)
	}
	if err := appendStage(config.RestoreStageRollbackApplying, config.StageResultRunning, "interrupted_restore"); err != nil {
		t.Fatalf("append rollback_applying: %v", err)
	}
	if interruptedStage == config.RestoreStageRollbackApplying {
		return
	}
	safety, err := service.VerifyBackup(context.Background(), request.SafetyBackupID)
	if err != nil {
		t.Fatalf("verify restore safety backup: %v", err)
	}
	if err := service.applyVerifiedBackup(context.Background(), service.restore, safety); err != nil {
		t.Fatalf("apply restore safety backup: %v", err)
	}
	if err := appendStage(config.RestoreStageRollbackFilesRestored, config.StageResultSuccess, ""); err != nil {
		t.Fatalf("append rollback_files_restored: %v", err)
	}
	if interruptedStage == config.RestoreStageRollbackFilesRestored {
		return
	}
	if err := appendStage(config.RestoreStageRollbackValidated, config.StageResultSuccess, ""); err != nil {
		t.Fatalf("append rollback_validated: %v", err)
	}
	if interruptedStage == config.RestoreStageRollbackValidated {
		return
	}
	if err := appendStage(config.RestoreStageRollbackReloadRequested, config.StageResultSuccess, ""); err != nil {
		t.Fatalf("append rollback_reload_requested: %v", err)
	}
}

func preparedRestoreFailureFixture(
	t *testing.T,
	failingReloads int,
) (*Service, config.RestoreExecutionRequest, string, config.Digest, *int, error) {
	t.Helper()
	root := t.TempDir()
	production := filepath.Join(root, "production")
	backups := filepath.Join(root, "backups")
	restores := filepath.Join(root, "restores")
	for _, directory := range []string{production, backups, restores} {
		mustMkdirCandidate(t, directory)
	}
	t.Cleanup(func() {
		if err := thawBackupFixture(backups); err != nil {
			t.Errorf("thaw restore backups: %v", err)
		}
	})
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"),
		"events { worker_connections 64; }\n", 0o640)
	targetDigest := mustProductionDigest(t, production)
	backupService := mustBackupService(t, backupOptions{
		NginxRoot: production, BackupRoot: backups, Limits: config.DefaultLimits(),
	})
	target, err := backupService.CreateBackup(context.Background(), config.BackupRequest{
		ReleaseID: "11111111111111111111111111111111", BackupID: "22222222222222222222222222222222",
		ProductionDigest: targetDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"),
		"events { worker_connections 32; }\n", 0o640)
	sourceDigest := mustProductionDigest(t, production)
	injected := errors.New("injected restore reload failure")
	reloadCalls := 0
	statusCalls := 0
	service := mustRestoreService(t, restoreOptions{
		NginxRoot: production, BackupRoot: backups, RestoreRoot: restores,
		Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: func(_ context.Context, specification commandSpec) (commandResult, error) {
			if len(specification.arguments) >= 2 && specification.arguments[0] == "-s" &&
				specification.arguments[1] == "reload" {
				reloadCalls++
				if reloadCalls <= failingReloads {
					return commandResult{exitCode: 1}, injected
				}
			}
			return commandResult{exitCode: 0}, nil
		},
		Status: func(context.Context) (Status, error) {
			statusCalls++
			if statusCalls == 1 {
				return runningRestartStatus(100, 101), nil
			}
			return runningRestartStatus(100, 201), nil
		},
		Probe: func(context.Context) (int, error) { return 204, nil }, ConfirmTimeout: time.Second,
	})
	request := config.RestoreExecutionRequest{
		RestoreID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TargetBackupID: target.BackupID,
		SafetyBackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceDigest: sourceDigest,
		TargetDigest: targetDigest, TargetTreeDigest: target.TreeDigest,
	}
	prepared, err := service.PrepareRestore(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareRestore() error = %v", err)
	}
	request.SafetyTreeDigest = prepared.SafetyBackup.TreeDigest
	return service, request, production, sourceDigest, &reloadCalls, injected
}

func restoreResultHasStage(result config.RestoreExecutionResult, stage config.RestoreStageName) bool {
	for _, candidate := range result.Stages {
		if candidate.Stage == stage {
			return true
		}
	}
	return false
}

func mustRestoreService(t *testing.T, options restoreOptions) *Service {
	t.Helper()
	service, err := newRestoreService(options)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
