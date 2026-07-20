/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestReconcileReleaseArtifactsRemovesOnlySafeAbandonedState(t *testing.T) {
	releaseRoot := t.TempDir()
	backupRoot := t.TempDir()
	service := newServiceWithExecutor(executeCommand)
	service.release.ReleaseRoot = releaseRoot
	service.release.BackupRoot = backupRoot

	candidatePath := filepath.Join(releaseRoot, ".candidate-abandoned")
	mustMkdirCandidate(t, candidatePath)
	mustWriteCandidate(t, filepath.Join(candidatePath, "nginx.conf"), "candidate", 0o600)

	referencedID := config.BackupID("11111111111111111111111111111111")
	unreferencedID := config.BackupID("22222222222222222222222222222222")
	completeID := config.BackupID("33333333333333333333333333333333")
	for _, id := range []config.BackupID{referencedID, unreferencedID, completeID} {
		mustMkdirCandidate(t, filepath.Join(backupRoot, string(id)))
		mustMkdirCandidate(t, filepath.Join(backupRoot, string(id), "control"))
	}
	mustWriteCandidate(t, filepath.Join(backupRoot, string(completeID), "control", "complete.json"), "{}\n", 0o400)
	mustMkdirCandidate(t, filepath.Join(backupRoot, "operator-notes"))

	releaseID := config.ReleaseID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	controlPath := filepath.Join(releaseRoot, string(releaseID), "control")
	mustMkdirCandidate(t, filepath.Dir(controlPath))
	mustMkdirCandidate(t, controlPath)
	if err := writeReleaseJournal(controlPath, releaseJournal{
		SchemaVersion: 1,
		ReleaseID:     releaseID,
		BackupID:      referencedID,
		Stage:         config.ReleaseStageBackupCreating,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.ReconcileReleaseArtifacts(context.Background()); err != nil {
		t.Fatalf("ReconcileReleaseArtifacts() error = %v", err)
	}
	assertPathMissing(t, candidatePath)
	assertPathMissing(t, filepath.Join(backupRoot, string(unreferencedID)))
	assertPathExists(t, filepath.Join(backupRoot, string(referencedID)))
	assertPathExists(t, filepath.Join(backupRoot, string(completeID)))
	assertPathExists(t, filepath.Join(backupRoot, "operator-notes"))
}

func TestReconcileReleaseArtifactsPreservesIncompleteBackupsWhenJournalScanIsUncertain(t *testing.T) {
	releaseRoot := t.TempDir()
	backupRoot := t.TempDir()
	service := newServiceWithExecutor(executeCommand)
	service.release.ReleaseRoot = releaseRoot
	service.release.BackupRoot = backupRoot

	candidatePath := filepath.Join(releaseRoot, ".candidate-abandoned")
	mustMkdirCandidate(t, candidatePath)
	incompleteID := config.BackupID("44444444444444444444444444444444")
	mustMkdirCandidate(t, filepath.Join(backupRoot, string(incompleteID)))
	mustMkdirCandidate(t, filepath.Join(backupRoot, string(incompleteID), "control"))
	controlPath := filepath.Join(releaseRoot, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "control")
	mustMkdirCandidate(t, filepath.Dir(controlPath))
	mustMkdirCandidate(t, controlPath)
	mustWriteCandidate(t, filepath.Join(controlPath, "state.json"), "{broken", 0o600)

	if err := service.ReconcileReleaseArtifacts(context.Background()); err != nil {
		t.Fatalf("ReconcileReleaseArtifacts() error = %v", err)
	}
	assertPathMissing(t, candidatePath)
	assertPathExists(t, filepath.Join(backupRoot, string(incompleteID)))
}

func TestReconcileReleaseArtifactsDoesNotFollowArtifactSymlinks(t *testing.T) {
	releaseRoot := t.TempDir()
	backupRoot := t.TempDir()
	service := newServiceWithExecutor(executeCommand)
	service.release.ReleaseRoot = releaseRoot
	service.release.BackupRoot = backupRoot
	target := filepath.Join(t.TempDir(), "target")
	mustMkdirCandidate(t, target)
	mustWriteCandidate(t, filepath.Join(target, "keep"), "keep", 0o600)
	if err := os.Symlink(target, filepath.Join(releaseRoot, ".candidate-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(backupRoot, "55555555555555555555555555555555")); err != nil {
		t.Fatal(err)
	}

	if err := service.ReconcileReleaseArtifacts(context.Background()); err != nil {
		t.Fatalf("ReconcileReleaseArtifacts() error = %v", err)
	}
	assertPathExists(t, filepath.Join(target, "keep"))
	assertPathMissing(t, filepath.Join(releaseRoot, ".candidate-link"))
	assertPathMissing(t, filepath.Join(backupRoot, "55555555555555555555555555555555"))
}

func TestReconcileReleaseArtifactsHonorsCancellationBeforeMutation(t *testing.T) {
	releaseRoot := t.TempDir()
	backupRoot := t.TempDir()
	service := newServiceWithExecutor(executeCommand)
	service.release.ReleaseRoot = releaseRoot
	service.release.BackupRoot = backupRoot
	candidatePath := filepath.Join(releaseRoot, ".candidate-keep")
	mustMkdirCandidate(t, candidatePath)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := service.ReconcileReleaseArtifacts(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReconcileReleaseArtifacts() error = %v, want cancellation", err)
	}
	assertPathExists(t, candidatePath)
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("Lstat(%q) error = %v, want path", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%q) error = %v, want missing path", path, err)
	}
}
