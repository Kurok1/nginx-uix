/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestExecuteReleasePublishesValidatedDraftAndPersistsStages(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(_ context.Context, specification commandSpec) (commandResult, error) {
		fixture.commands = append(fixture.commands, slices.Clone(specification.arguments))
		return commandResult{exitCode: 0}, nil
	})

	result, err := service.ExecuteRelease(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecuteRelease() error = %v", err)
	}
	if result.State != config.ReleaseStateSucceeded || result.Stage != config.ReleaseStageCommitted || result.Backup.BackupID != request.BackupID || result.MasterPID != 101 || result.WorkerCount != 1 || result.HTTPStatus != 204 {
		t.Fatalf("result = %+v", result)
	}
	contents, err := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
	if err != nil || string(contents) != fixture.draftContent {
		t.Fatalf("published contents = %q, err = %v", contents, err)
	}
	assertReleaseStages(t, result.Stages, []config.ReleaseStageName{
		config.ReleaseStageRechecking, config.ReleaseStageBackupCreating, config.ReleaseStageBackupVerified,
		config.ReleaseStageCandidateValidated, config.ReleaseStageFilesApplying, config.ReleaseStageFilesApplied,
		config.ReleaseStageProductionValidated, config.ReleaseStageReloadRequested,
		config.ReleaseStageRuntimeConfirmed, config.ReleaseStageCommitted,
	})
	if len(fixture.commands) != 3 || !slices.Equal(fixture.commands[1], []string{"-t", "-c", filepath.Join(fixture.production, "nginx.conf")}) || !slices.Equal(fixture.commands[2], []string{"-s", "reload"}) {
		t.Fatalf("fixed commands = %#v", fixture.commands)
	}
	journal, err := os.ReadFile(filepath.Join(fixture.releases, string(request.ReleaseID), "control", "state.json"))
	if err != nil || !bytes.Contains(journal, []byte(`"stage":"committed"`)) {
		t.Fatalf("journal = %q, err = %v", journal, err)
	}
}

func TestExecuteReleaseCreatesNewFileWithDraftManifestMode(t *testing.T) {
	fixture := newReleaseFixture(t)
	newPath := config.RelativePath("nginx-uix-acme-22222222222222222222222222222222.conf")
	newContent := []byte("location = /.well-known/acme-challenge/token { return 200; }\n")
	mustWriteCandidate(t, filepath.Join(fixture.workspaces, string(fixture.workspaceID), "draft", string(newPath)), string(newContent), 0o600)
	entry := config.Entry{
		Path: newPath, Type: config.EntryRegular, Class: config.EntryManagedText,
		Mode: 0o600, Size: int64(len(newContent)), ContentDigest: config.Digest(sha256.Sum256(newContent)),
	}
	insertAt := slices.IndexFunc(fixture.manifest.Entries, func(existing config.Entry) bool {
		return existing.Path == "nginx.conf"
	})
	if insertAt < 0 {
		t.Fatal("fixture manifest has no nginx.conf entry")
	}
	fixture.manifest.Entries = slices.Insert(fixture.manifest.Entries, insertAt, entry)
	fixture.manifest.EntryCount++
	fixture.manifest.ManagedBytes += entry.Size
	workspaceRoot, err := config.OpenScopedRoot(filepath.Join(fixture.workspaces, string(fixture.workspaceID)))
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteControlManifest(context.Background(), workspaceRoot, fixture.manifest); err != nil {
		t.Fatal(err)
	}
	if err := workspaceRoot.Close(); err != nil {
		t.Fatal(err)
	}

	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	result, err := service.ExecuteRelease(context.Background(), request)
	if err != nil || result.State != config.ReleaseStateSucceeded {
		t.Fatalf("ExecuteRelease() = %+v, %v", result, err)
	}
	information, err := os.Stat(filepath.Join(fixture.production, string(newPath)))
	if err != nil || information.Mode().Perm() != entry.Mode {
		t.Fatalf("created file mode = %v, error = %v, want %v", information.Mode().Perm(), err, entry.Mode)
	}
}

func TestReleaseProgressReadsDurableStagesWhileProductionTransactionIsLocked(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	reachedWrite := make(chan struct{})
	continueWrite := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(continueWrite) }) }
	t.Cleanup(unblock)
	service.release.operationHook = func(stage config.ReleaseStageName, index int) error {
		if stage == config.ReleaseStageFilesApplying && index == 0 {
			close(reachedWrite)
			<-continueWrite
		}
		return nil
	}
	type outcome struct {
		result config.ReleaseExecutionResult
		err    error
	}
	completed := make(chan outcome, 1)
	go func() {
		result, err := service.ExecuteRelease(context.Background(), request)
		completed <- outcome{result: result, err: err}
	}()
	select {
	case <-reachedWrite:
	case <-time.After(time.Second):
		t.Fatal("release did not reach the durable files-applying stage")
	}
	if _, err := service.ExecuteRelease(context.Background(), request); !errors.Is(err, config.ErrReleaseInProgress) {
		t.Fatalf("concurrent ExecuteRelease() error = %v, want ErrReleaseInProgress", err)
	}

	progress, err := service.ReleaseProgress(context.Background(), request)
	if err != nil {
		t.Fatalf("ReleaseProgress() error = %v", err)
	}
	if progress.State != config.ReleaseStateRunning || progress.Stage != config.ReleaseStageFilesApplying ||
		len(progress.Stages) == 0 || progress.Stages[len(progress.Stages)-1].Stage != config.ReleaseStageFilesApplying ||
		!progress.FinishedAt.IsZero() {
		t.Fatalf("ReleaseProgress() = %+v", progress)
	}
	unblock()
	select {
	case finished := <-completed:
		if finished.err != nil || finished.result.State != config.ReleaseStateSucceeded {
			t.Fatalf("ExecuteRelease() = %+v, %v", finished.result, finished.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("release did not finish after the write was unblocked")
	}
}

func TestExecuteReleaseRejectsProductionChangeBeforeBackupOrNginxCommand(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	mustWriteCandidate(t, filepath.Join(fixture.production, "conf.d", "site.conf"), "server { listen 9090; }\n", 0o640)
	service := fixture.service(t, func(_ context.Context, specification commandSpec) (commandResult, error) {
		fixture.commands = append(fixture.commands, slices.Clone(specification.arguments))
		return commandResult{exitCode: 0}, nil
	})

	result, err := service.ExecuteRelease(context.Background(), request)
	if !errors.Is(err, config.ErrSnapshotChanged) || result.State != config.ReleaseStateFailed ||
		result.ErrorCode != "production_changed" || len(fixture.commands) != 0 {
		t.Fatalf("ExecuteRelease() = %+v, %v, commands = %v", result, err, fixture.commands)
	}
	if _, statErr := os.Stat(filepath.Join(fixture.backups, string(request.BackupID))); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("backup exists after production changed: %v", statErr)
	}
}

func TestExecuteReleaseRejectsChangedCandidateBeforeProductionWriteOrReload(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	request.CandidateDigest = config.Digest{99}
	service := fixture.service(t, func(_ context.Context, specification commandSpec) (commandResult, error) {
		fixture.commands = append(fixture.commands, slices.Clone(specification.arguments))
		return commandResult{exitCode: 0}, nil
	})

	result, err := service.ExecuteRelease(context.Background(), request)
	if !errors.Is(err, config.ErrSnapshotChanged) || result.State != config.ReleaseStateFailed || result.ErrorCode != "candidate_changed" {
		t.Fatalf("ExecuteRelease() = %+v, %v", result, err)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
	if readErr != nil || string(contents) != fixture.originalContent {
		t.Fatalf("production contents = %q, %v", contents, readErr)
	}
	for _, command := range fixture.commands {
		if slices.Equal(command, []string{"-s", "reload"}) {
			t.Fatalf("reload ran for changed candidate: %v", fixture.commands)
		}
	}
}

func TestExecuteReleaseRollsBackInjectedStorageFailureBeforeFirstFileMutation(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		injected error
	}{
		{name: "disk full", injected: syscall.ENOSPC},
		{name: "read-only directory", injected: fs.ErrPermission},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newReleaseFixture(t)
			request := fixture.request(t)
			service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
				return commandResult{exitCode: 0}, nil
			})
			service.release.operationHook = func(stage config.ReleaseStageName, index int) error {
				if stage == config.ReleaseStageFilesApplying && index == 0 {
					return testCase.injected
				}
				return nil
			}

			result, err := service.ExecuteRelease(context.Background(), request)
			if !errors.Is(err, testCase.injected) || result.State != config.ReleaseStateRolledBack || result.Stage != config.ReleaseStageRolledBack {
				t.Fatalf("ExecuteRelease() = %+v, %v", result, err)
			}
			contents, readErr := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
			if readErr != nil || string(contents) != fixture.originalContent {
				t.Fatalf("production contents = %q, %v", contents, readErr)
			}
		})
	}
}

func TestExecuteReleaseRollsBackWhenPublishedFilesFailFullValidation(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	productionValidations := 0
	service := fixture.service(t, func(_ context.Context, specification commandSpec) (commandResult, error) {
		if slices.Equal(specification.arguments, []string{"-t", "-c", filepath.Join(fixture.production, "nginx.conf")}) {
			productionValidations++
			if productionValidations == 1 {
				return commandResult{exitCode: 1}, &commandExitError{Code: 1, Diagnostic: "published config rejected"}
			}
		}
		return commandResult{exitCode: 0}, nil
	})

	result, err := service.ExecuteRelease(context.Background(), request)
	if err == nil || result.State != config.ReleaseStateRolledBack || result.ErrorCode != "production_invalid" || productionValidations != 2 {
		t.Fatalf("ExecuteRelease() = %+v, %v, validations = %d", result, err, productionValidations)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
	if readErr != nil || string(contents) != fixture.originalContent {
		t.Fatalf("production contents = %q, %v", contents, readErr)
	}
}

func TestReleaseProgressAcceptsEarliestJournalBeforeRuntimeIdentityIsRecorded(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	controlPath := filepath.Join(fixture.releases, string(request.ReleaseID), "control")
	mustMkdirCandidate(t, filepath.Dir(controlPath))
	mustMkdirCandidate(t, controlPath)
	now := time.Now().UTC()
	if err := writeReleaseJournal(controlPath, releaseJournal{
		SchemaVersion: 1, ReleaseID: request.ReleaseID, BackupID: request.BackupID, WorkspaceID: request.WorkspaceID,
		ProductionDigest: request.ProductionDigest, DraftDigest: request.DraftDigest, CandidateDigest: request.CandidateDigest,
		Stage: config.ReleaseStageRechecking, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := appendReleaseEvent(controlPath, config.ReleaseStage{
		ReleaseID: request.ReleaseID, Sequence: 1, Stage: config.ReleaseStageRechecking,
		Result: config.StageResultRunning, PublicDetailsJSON: "{}", OccurredAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	progress, err := service.ReleaseProgress(context.Background(), request)
	if err != nil || progress.Stage != config.ReleaseStageRechecking || progress.MasterPID != 0 || progress.WorkerCount != 0 {
		t.Fatalf("ReleaseProgress() = %+v, %v", progress, err)
	}
}

func TestExecuteReleaseRollsBackWhenReloadFails(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	reloads := 0
	service := fixture.service(t, func(_ context.Context, specification commandSpec) (commandResult, error) {
		if slices.Equal(specification.arguments, []string{"-s", "reload"}) {
			reloads++
			if reloads == 1 {
				return commandResult{exitCode: 1}, &commandExitError{Code: 1, Diagnostic: "reload rejected"}
			}
		}
		return commandResult{exitCode: 0}, nil
	})

	result, err := service.ExecuteRelease(context.Background(), request)
	if err == nil || result.State != config.ReleaseStateRolledBack || result.Stage != config.ReleaseStageRolledBack || reloads != 2 {
		t.Fatalf("result = %+v, error = %v, reloads = %d", result, err, reloads)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
	if readErr != nil || string(contents) != fixture.originalContent {
		t.Fatalf("rolled back contents = %q, err = %v", contents, readErr)
	}
	assertReleaseStagesSuffix(t, result.Stages, []config.ReleaseStageName{
		config.ReleaseStageRollbackApplying, config.ReleaseStageRollbackFilesRestored,
		config.ReleaseStageRollbackValidated, config.ReleaseStageRollbackReloadRequested, config.ReleaseStageRolledBack,
	})
}

func TestExecuteReleaseNeedsAttentionWhenRollbackCannotBeConfirmed(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(_ context.Context, specification commandSpec) (commandResult, error) {
		if slices.Equal(specification.arguments, []string{"-s", "reload"}) {
			return commandResult{exitCode: 1}, &commandExitError{Code: 1, Diagnostic: "reload rejected"}
		}
		return commandResult{exitCode: 0}, nil
	})

	result, err := service.ExecuteRelease(context.Background(), request)
	if err == nil || result.State != config.ReleaseStateNeedsAttention || result.Stage != config.ReleaseStageNeedsAttention || result.ErrorCode != "rollback_reload_failed" {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestExecuteReleaseRollsBackWhenHTTPHealthIsNotSuccessful(t *testing.T) {
	fixture := newReleaseFixture(t)
	fixture.confirmTimeout = 250 * time.Millisecond
	request := fixture.request(t)
	reloads := 0
	service := fixture.service(t, func(_ context.Context, specification commandSpec) (commandResult, error) {
		if slices.Equal(specification.arguments, []string{"-s", "reload"}) {
			reloads++
		}
		return commandResult{exitCode: 0}, nil
	})
	service.release.Probe = func(context.Context) (int, error) {
		if reloads == 1 {
			return 503, nil
		}
		return 204, nil
	}

	result, err := service.ExecuteRelease(context.Background(), request)
	if err == nil || result.State != config.ReleaseStateRolledBack || result.ErrorCode != "runtime_unhealthy" || reloads != 2 {
		t.Fatalf("ExecuteRelease() = %+v, error = %v, reloads = %d", result, err, reloads)
	}
}

func TestRecoverReleaseRollsBackInterruptedProductionWrite(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	if _, err := service.CreateBackup(context.Background(), config.BackupRequest{
		ReleaseID: request.ReleaseID, BackupID: request.BackupID, ProductionDigest: request.ProductionDigest,
	}); err != nil {
		t.Fatal(err)
	}
	mustWriteCandidate(t, filepath.Join(fixture.production, "conf.d", "site.conf"), fixture.draftContent, 0o640)
	controlPath := filepath.Join(fixture.releases, string(request.ReleaseID), "control")
	mustMkdirCandidate(t, filepath.Dir(controlPath))
	mustMkdirCandidate(t, controlPath)
	now := time.Now().UTC()
	journal := releaseJournal{
		SchemaVersion: 1, ReleaseID: request.ReleaseID, BackupID: request.BackupID, WorkspaceID: request.WorkspaceID,
		ProductionDigest: request.ProductionDigest, DraftDigest: request.DraftDigest, CandidateDigest: request.CandidateDigest,
		Stage: config.ReleaseStageFilesApplied, DurableOperation: 1, MasterPID: 101, WorkerPIDs: []int{102}, UpdatedAt: now,
	}
	if err := writeReleaseJournal(controlPath, journal); err != nil {
		t.Fatal(err)
	}
	for index, stage := range []config.ReleaseStageName{config.ReleaseStageRechecking, config.ReleaseStageFilesApplying, config.ReleaseStageFilesApplied} {
		if err := appendReleaseEvent(controlPath, config.ReleaseStage{
			ReleaseID: request.ReleaseID, Sequence: uint64(index + 1), Stage: stage,
			Result: config.StageResultRunning, PublicDetailsJSON: "{}", OccurredAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := service.RecoverRelease(context.Background(), request)
	if err == nil || result.State != config.ReleaseStateRolledBack || result.Stage != config.ReleaseStageRolledBack {
		t.Fatalf("RecoverRelease() = %+v, %v", result, err)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
	if readErr != nil || string(contents) != fixture.originalContent {
		t.Fatalf("recovered contents = %q, %v", contents, readErr)
	}
	assertReleaseStagesSuffix(t, result.Stages, []config.ReleaseStageName{
		config.ReleaseStageRollbackApplying, config.ReleaseStageRollbackFilesRestored,
		config.ReleaseStageRollbackValidated, config.ReleaseStageRolledBack,
	})
}

func TestRecoverReleaseReplaysCommittedJournalWithoutChangingProduction(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	published, err := service.ExecuteRelease(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
	if err != nil {
		t.Fatal(err)
	}

	recovered, err := service.RecoverRelease(context.Background(), request)
	if err != nil || recovered.State != config.ReleaseStateSucceeded || recovered.Stage != config.ReleaseStageCommitted ||
		len(recovered.Stages) != len(published.Stages) {
		t.Fatalf("RecoverRelease() = %+v, %v", recovered, err)
	}
	after, err := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("production changed during committed replay: %q, %v", after, err)
	}
}

func TestRecoverReleaseFailsClosedOnCorruptJournal(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	controlPath := filepath.Join(fixture.releases, string(request.ReleaseID), "control")
	mustMkdirCandidate(t, filepath.Dir(controlPath))
	mustMkdirCandidate(t, controlPath)
	mustWriteCandidate(t, filepath.Join(controlPath, "state.json"), "{broken", 0o600)

	result, err := service.RecoverRelease(context.Background(), request)
	if err == nil || !errors.Is(err, config.ErrConflict) || result.State != config.ReleaseStateNeedsAttention || result.Stage != config.ReleaseStageNeedsAttention {
		t.Fatalf("RecoverRelease() = %+v, %v", result, err)
	}
}

func TestRecoverReleaseClassifiesEveryPreWriteJournalStageAsFailed(t *testing.T) {
	for _, stage := range []config.ReleaseStageName{
		config.ReleaseStageRechecking,
		config.ReleaseStageBackupCreating,
		config.ReleaseStageBackupVerified,
		config.ReleaseStageCandidateValidated,
		config.ReleaseStageFilesApplying,
	} {
		t.Run(string(stage), func(t *testing.T) {
			fixture := newReleaseFixture(t)
			request := fixture.request(t)
			service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
				return commandResult{exitCode: 0}, nil
			})
			writeReleaseRecoveryEvidence(t, fixture, request, releaseJournal{
				SchemaVersion: 1, ReleaseID: request.ReleaseID, BackupID: request.BackupID, WorkspaceID: request.WorkspaceID,
				ProductionDigest: request.ProductionDigest, DraftDigest: request.DraftDigest, CandidateDigest: request.CandidateDigest,
				Stage: stage, MasterPID: 101, UpdatedAt: time.Now().UTC(),
			}, []config.ReleaseStageName{stage})

			result, err := service.RecoverRelease(context.Background(), request)
			if err == nil || result.State != config.ReleaseStateFailed || result.Stage != config.ReleaseStageFailed ||
				result.ErrorCode != "interrupted_before_write" {
				t.Fatalf("RecoverRelease() = %+v, %v", result, err)
			}
			contents, readErr := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
			if readErr != nil || string(contents) != fixture.originalContent {
				t.Fatalf("production contents = %q, error = %v", contents, readErr)
			}
		})
	}
}

func TestRecoverReleasePreservesAmbiguousProductionContent(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	if _, err := service.CreateBackup(context.Background(), config.BackupRequest{
		ReleaseID: request.ReleaseID, BackupID: request.BackupID, ProductionDigest: request.ProductionDigest,
	}); err != nil {
		t.Fatal(err)
	}
	unknown := "server { listen 9099; }\n"
	mustWriteCandidate(t, filepath.Join(fixture.production, "conf.d", "site.conf"), unknown, 0o640)
	writeReleaseRecoveryEvidence(t, fixture, request, releaseJournal{
		SchemaVersion: 1, ReleaseID: request.ReleaseID, BackupID: request.BackupID, WorkspaceID: request.WorkspaceID,
		ProductionDigest: request.ProductionDigest, DraftDigest: request.DraftDigest, CandidateDigest: request.CandidateDigest,
		Stage: config.ReleaseStageFilesApplied, DurableOperation: 1, MasterPID: 101, UpdatedAt: time.Now().UTC(),
	}, []config.ReleaseStageName{config.ReleaseStageRechecking, config.ReleaseStageFilesApplying, config.ReleaseStageFilesApplied})

	result, err := service.RecoverRelease(context.Background(), request)
	if err == nil || result.State != config.ReleaseStateNeedsAttention || result.ErrorCode != "recovery_digest_ambiguous" {
		t.Fatalf("RecoverRelease() = %+v, %v", result, err)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.production, "conf.d", "site.conf"))
	if readErr != nil || string(contents) != unknown {
		t.Fatalf("ambiguous production contents = %q, error = %v", contents, readErr)
	}
}

func TestRecoverReleaseResumesAnInterruptedRollbackWithoutStageRegression(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	if _, err := service.CreateBackup(context.Background(), config.BackupRequest{
		ReleaseID: request.ReleaseID, BackupID: request.BackupID, ProductionDigest: request.ProductionDigest,
	}); err != nil {
		t.Fatal(err)
	}
	writeReleaseRecoveryEvidence(t, fixture, request, releaseJournal{
		SchemaVersion: 1, ReleaseID: request.ReleaseID, BackupID: request.BackupID, WorkspaceID: request.WorkspaceID,
		ProductionDigest: request.ProductionDigest, DraftDigest: request.DraftDigest, CandidateDigest: request.CandidateDigest,
		Stage: config.ReleaseStageRollbackFilesRestored, DurableOperation: 1, MasterPID: 101, UpdatedAt: time.Now().UTC(),
	}, []config.ReleaseStageName{
		config.ReleaseStageRechecking, config.ReleaseStageFilesApplying,
		config.ReleaseStageRollbackApplying, config.ReleaseStageRollbackFilesRestored,
	})

	result, err := service.RecoverRelease(context.Background(), request)
	if err == nil || result.State != config.ReleaseStateRolledBack || result.Stage != config.ReleaseStageRolledBack {
		t.Fatalf("RecoverRelease() = %+v, %v", result, err)
	}
	assertReleaseStagesSuffix(t, result.Stages, []config.ReleaseStageName{
		config.ReleaseStageRollbackFilesRestored, config.ReleaseStageRollbackValidated, config.ReleaseStageRolledBack,
	})
	if _, err := service.RecoverRelease(context.Background(), request); err != nil {
		t.Fatalf("second RecoverRelease() error = %v", err)
	}
}

func TestRecoverReleaseRepairsTheFirstJournalEventGap(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request(t)
	service := fixture.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	controlPath := filepath.Join(fixture.releases, string(request.ReleaseID), "control")
	mustMkdirCandidate(t, filepath.Dir(controlPath))
	mustMkdirCandidate(t, controlPath)
	if err := writeReleaseJournal(controlPath, releaseJournal{
		SchemaVersion: 1, ReleaseID: request.ReleaseID, BackupID: request.BackupID, WorkspaceID: request.WorkspaceID,
		ProductionDigest: request.ProductionDigest, DraftDigest: request.DraftDigest, CandidateDigest: request.CandidateDigest,
		Stage: config.ReleaseStageRechecking, MasterPID: 101, WorkerPIDs: []int{102}, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := service.RecoverRelease(context.Background(), request)
	if err == nil || result.State != config.ReleaseStateFailed || len(result.Stages) != 2 ||
		result.Stages[0].Code != "recovered_event_gap" || result.Stages[1].Stage != config.ReleaseStageFailed {
		t.Fatalf("RecoverRelease() = %+v, %v", result, err)
	}
}

func writeReleaseRecoveryEvidence(
	t *testing.T,
	fixture *releaseFixture,
	request config.ReleaseExecutionRequest,
	journal releaseJournal,
	stages []config.ReleaseStageName,
) {
	t.Helper()
	if len(journal.WorkerPIDs) == 0 {
		journal.WorkerPIDs = []int{102}
	}
	controlPath := filepath.Join(fixture.releases, string(request.ReleaseID), "control")
	mustMkdirCandidate(t, filepath.Dir(controlPath))
	mustMkdirCandidate(t, controlPath)
	if err := writeReleaseJournal(controlPath, journal); err != nil {
		t.Fatal(err)
	}
	for index, stage := range stages {
		if err := appendReleaseEvent(controlPath, config.ReleaseStage{
			ReleaseID: request.ReleaseID, Sequence: uint64(index + 1), Stage: stage,
			Result: config.StageResultRunning, PublicDetailsJSON: "{}",
			OccurredAt: journal.UpdatedAt.Add(time.Duration(index) * time.Millisecond),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

type releaseFixture struct {
	root             string
	production       string
	workspaces       string
	candidates       string
	backups          string
	releases         string
	workspaceID      config.WorkspaceID
	manifest         config.Manifest
	productionDigest config.Digest
	draftContent     string
	originalContent  string
	commands         [][]string
	confirmTimeout   time.Duration
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	return newReleaseFixtureWithConfig(
		t,
		"events {}\nhttp { include conf.d/*.conf; }\n",
		"server { listen 8080; }\n",
		"server { listen 8081; }\n",
	)
}

func newReleaseFixtureWithConfig(t *testing.T, nginxConfiguration, originalContent, draftContent string) *releaseFixture {
	t.Helper()
	root := t.TempDir()
	fixture := &releaseFixture{
		root: root, production: filepath.Join(root, "production"), workspaces: filepath.Join(root, "workspaces"),
		candidates: filepath.Join(root, "candidates"), backups: filepath.Join(root, "backups"), releases: filepath.Join(root, "releases"),
		workspaceID:     "11111111111111111111111111111111",
		originalContent: originalContent, draftContent: draftContent,
	}
	for _, directory := range []string{
		fixture.production, fixture.workspaces, fixture.candidates, fixture.backups, fixture.releases,
		filepath.Join(fixture.production, "conf.d"), filepath.Join(fixture.production, "logs"), filepath.Join(fixture.production, "runtime"),
	} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(fixture.production, "nginx.conf"), nginxConfiguration, 0o640)
	mustWriteCandidate(t, filepath.Join(fixture.production, "conf.d", "site.conf"), fixture.originalContent, 0o640)
	fixture.manifest, fixture.productionDigest = mustCandidateWorkspace(t, fixture.production, fixture.workspaces, fixture.workspaceID, "conf.d/site.conf", []byte(fixture.draftContent))
	return fixture
}

func (f *releaseFixture) request(t *testing.T) config.ReleaseExecutionRequest {
	t.Helper()
	candidateService := f.service(t, func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 0}, nil
	})
	validation, err := candidateService.ValidateCandidate(context.Background(), config.CandidateValidationRequest{
		WorkspaceID: f.workspaceID, ProductionDigest: f.productionDigest, DraftDigest: f.manifest.Digest(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return config.ReleaseExecutionRequest{
		ReleaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", WorkspaceID: f.workspaceID,
		ProductionDigest: f.productionDigest, DraftDigest: f.manifest.Digest(), CandidateDigest: validation.CandidateDigest,
	}
}

func (f *releaseFixture) service(t *testing.T, executor commandExecutor) *Service {
	t.Helper()
	reloadGeneration := 0
	wrappedExecutor := func(ctx context.Context, specification commandSpec) (commandResult, error) {
		isReload := slices.Equal(specification.arguments, []string{"-s", "reload"})
		result, err := executor(ctx, specification)
		if isReload && err == nil {
			reloadGeneration++
		}
		return result, err
	}
	service, err := newReleaseService(releaseOptions{
		NginxRoot: f.production, WorkspaceRoot: f.workspaces, CandidateRoot: f.candidates,
		BackupRoot: f.backups, ReleaseRoot: f.releases, Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: wrappedExecutor,
		Status: func(context.Context) (Status, error) {
			return Status{SampledAt: time.Now().UTC(), State: StateRunning,
				Master:  &NginxProcess{PID: 101, Role: ProcessRoleMaster, StartedAt: time.Now().Add(-time.Hour)},
				Workers: []NginxProcess{{PID: 102 + reloadGeneration, Role: ProcessRoleWorker, StartedAt: time.Now().Add(-time.Minute)}},
			}, nil
		},
		Probe:          func(context.Context) (int, error) { return 204, nil },
		ConfirmTimeout: f.confirmTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.cachedBuild = &BuildInfo{Version: "1.27.5", PIDPath: "/run/nginx.pid", SbinPath: nginxExecutable, ConfigureArguments: []string{"--pid-path=/run/nginx.pid", "--sbin-path=" + nginxExecutable}}
	t.Cleanup(func() {
		entries, err := os.ReadDir(f.backups)
		if err != nil {
			t.Errorf("read backup fixtures: %v", err)
			return
		}
		for _, entry := range entries {
			if err := thawBackupFixture(filepath.Join(f.backups, entry.Name())); err != nil {
				t.Errorf("thaw backup fixture %s: %v", entry.Name(), err)
			}
		}
	})
	return service
}

func assertReleaseStages(t *testing.T, stages []config.ReleaseStage, want []config.ReleaseStageName) {
	t.Helper()
	got := make([]config.ReleaseStageName, len(stages))
	for index, stage := range stages {
		got[index] = stage.Stage
		if stage.Sequence != uint64(index+1) {
			t.Fatalf("stage sequence = %d at %d", stage.Sequence, index)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
}

func assertReleaseStagesSuffix(t *testing.T, stages []config.ReleaseStage, want []config.ReleaseStageName) {
	t.Helper()
	if len(stages) < len(want) {
		t.Fatalf("stages = %v", stages)
	}
	got := make([]config.ReleaseStageName, 0, len(want))
	for _, stage := range stages[len(stages)-len(want):] {
		got = append(got, stage.Stage)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("stage suffix = %v, want %v", got, want)
	}
}
