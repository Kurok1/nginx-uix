/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestCreateBackupWritesCompleteImmutableTreeAndDetectsTampering(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	backups := filepath.Join(root, "backups")
	for _, directory := range []string{production, backups, filepath.Join(production, "conf.d"), filepath.Join(production, "ssl")} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\nhttp { include conf.d/*.conf; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "conf.d", "site.conf"), "server { listen 8080; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "ssl", "site.key"), "private-material\n", 0o600)
	if err := os.Symlink("site.conf", filepath.Join(production, "conf.d", "active.conf")); err != nil {
		t.Fatal(err)
	}
	productionDigest := mustProductionDigest(t, production)
	service := mustBackupService(t, backupOptions{NginxRoot: production, BackupRoot: backups, Limits: config.DefaultLimits()})
	request := config.BackupRequest{
		ReleaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ProductionDigest: productionDigest,
	}

	evidence, err := service.CreateBackup(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateBackup() error = %v", err)
	}
	t.Cleanup(func() {
		if err := thawBackupFixture(filepath.Join(backups, string(request.BackupID))); err != nil {
			t.Errorf("thaw backup fixture: %v", err)
		}
	})
	if evidence.BackupID != request.BackupID || evidence.ReleaseID != request.ReleaseID || evidence.TreeDigest == (config.Digest{}) || evidence.EntryCount != 6 || evidence.TotalBytes <= 0 || evidence.VerifiedAt.IsZero() {
		t.Fatalf("evidence = %+v", evidence)
	}
	backupPath := filepath.Join(backups, string(request.BackupID))
	for _, relative := range []string{"control/manifest.bin", "control/complete.json", "tree/ssl/site.key"} {
		if information, err := os.Stat(filepath.Join(backupPath, relative)); err != nil || information.Mode().Perm()&0o222 != 0 {
			t.Fatalf("protected %s mode = %v, err = %v", relative, information, err)
		}
	}
	verified, err := service.VerifyBackup(context.Background(), request.BackupID)
	if err != nil || verified.TreeDigest != evidence.TreeDigest {
		t.Fatalf("VerifyBackup() = %+v, %v", verified, err)
	}
	keyPath := filepath.Join(backupPath, "tree", "ssl", "site.key")
	if err := os.Chmod(keyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyBackup(context.Background(), request.BackupID); !errors.Is(err, config.ErrSnapshotChanged) {
		t.Fatalf("VerifyBackup(tampered) error = %v", err)
	}

	metadataRequest := config.BackupRequest{
		ReleaseID: "cccccccccccccccccccccccccccccccc", BackupID: "dddddddddddddddddddddddddddddddd", ProductionDigest: productionDigest,
	}
	if _, err := service.CreateBackup(context.Background(), metadataRequest); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(backups, string(metadataRequest.BackupID))
	t.Cleanup(func() {
		if err := thawBackupFixture(metadataPath); err != nil {
			t.Errorf("thaw metadata backup fixture: %v", err)
		}
	})
	controlPath := filepath.Join(metadataPath, "control")
	completePath := filepath.Join(controlPath, "complete.json")
	for path, mode := range map[string]fs.FileMode{metadataPath: 0o700, controlPath: 0o700, completePath: 0o600} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	complete, err := os.ReadFile(completePath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(complete, []byte(metadataRequest.ReleaseID), []byte("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"), 1)
	if bytes.Equal(tampered, complete) {
		t.Fatal("release identity was not present in complete metadata")
	}
	if err := os.WriteFile(completePath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		path string
		mode fs.FileMode
	}{{completePath, 0o400}, {controlPath, 0o500}, {metadataPath, 0o500}} {
		if err := os.Chmod(change.path, change.mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.verifyReleaseBackup(context.Background(), config.ReleaseExecutionRequest{
		ReleaseID: metadataRequest.ReleaseID, BackupID: metadataRequest.BackupID, ProductionDigest: metadataRequest.ProductionDigest,
	}); !errors.Is(err, config.ErrSnapshotChanged) {
		t.Fatalf("verifyReleaseBackup(tampered release identity) error = %v", err)
	}
}

func TestCreateBackupRejectsConcurrentProductionChangeAndCleansIncompleteDirectory(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	backups := filepath.Join(root, "backups")
	for _, directory := range []string{production, backups} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	productionDigest := mustProductionDigest(t, production)
	service := mustBackupService(t, backupOptions{
		NginxRoot: production, BackupRoot: backups, Limits: config.DefaultLimits(),
		afterCopy: func() error {
			return os.WriteFile(filepath.Join(production, "nginx.conf"), []byte("events { worker_connections 8; }\n"), 0o640)
		},
	})
	request := config.BackupRequest{
		ReleaseID: "cccccccccccccccccccccccccccccccc", BackupID: "dddddddddddddddddddddddddddddddd", ProductionDigest: productionDigest,
	}

	_, err := service.CreateBackup(context.Background(), request)
	if !errors.Is(err, config.ErrSnapshotChanged) {
		t.Fatalf("CreateBackup() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(backups, string(request.BackupID))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("incomplete backup survived: %v", err)
	}
}

func TestCreateBackupCleansIncompleteDirectoryOnInjectedStorageFailure(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		injected error
	}{
		{name: "disk full", injected: syscall.ENOSPC},
		{name: "read-only directory", injected: fs.ErrPermission},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			production := filepath.Join(root, "production")
			backups := filepath.Join(root, "backups")
			for _, directory := range []string{production, backups} {
				mustMkdirCandidate(t, directory)
			}
			mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
			productionDigest := mustProductionDigest(t, production)
			service := mustBackupService(t, backupOptions{
				NginxRoot: production, BackupRoot: backups, Limits: config.DefaultLimits(),
				afterCopy: func() error { return testCase.injected },
			})
			request := config.BackupRequest{
				ReleaseID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", BackupID: "ffffffffffffffffffffffffffffffff", ProductionDigest: productionDigest,
			}

			_, err := service.CreateBackup(context.Background(), request)
			if !errors.Is(err, testCase.injected) {
				t.Fatalf("CreateBackup() error = %v", err)
			}
			if _, err := os.Stat(filepath.Join(backups, string(request.BackupID))); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("incomplete backup survived injected storage failure: %v", err)
			}
		})
	}
}

func TestCreateBackupThawsAndCleansIncompleteDirectoryAfterTreeFreeze(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	backups := filepath.Join(root, "backups")
	for _, directory := range []string{production, backups} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	productionDigest := mustProductionDigest(t, production)
	service := mustBackupService(t, backupOptions{
		NginxRoot: production, BackupRoot: backups, Limits: config.DefaultLimits(),
		afterFreeze: func() error { return syscall.ENOSPC },
	})
	request := config.BackupRequest{
		ReleaseID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", BackupID: "ffffffffffffffffffffffffffffffff", ProductionDigest: productionDigest,
	}

	_, err := service.CreateBackup(context.Background(), request)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("CreateBackup() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(backups, string(request.BackupID))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("frozen incomplete backup survived cleanup: %v", err)
	}
}

func mustProductionDigest(t *testing.T, path string) config.Digest {
	t.Helper()
	root, err := config.OpenScopedRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	inventory, inventoryErr := config.BuildInventory(context.Background(), root, config.SnapshotOptions{
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Policy: config.NewPolicy(), FileMode: 0o400, DirectoryMode: 0o700,
	})
	if closeErr := root.Close(); inventoryErr != nil || closeErr != nil {
		t.Fatalf("BuildInventory() error = %v, close = %v", inventoryErr, closeErr)
	}
	return inventory.Digest
}

func mustBackupService(t *testing.T, options backupOptions) *Service {
	t.Helper()
	service, err := newBackupService(options)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func thawBackupFixture(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		mode := fs.FileMode(0o600)
		if entry.IsDir() {
			mode = 0o700
		}
		return os.Chmod(path, mode)
	})
}
