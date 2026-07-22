/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.6.0
 */
package store

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kuroky/nginx-uix/internal/auth"
	"github.com/kuroky/nginx-uix/internal/certificate"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

func TestColdBackupRestorePreservesV05State(t *testing.T) {
	ctx := context.Background()
	sourceRoot := secureTempDir(t)
	configRoot := filepath.Join(sourceRoot, "etc", "nginx")
	dataRoot := filepath.Join(sourceRoot, "var", "lib", "nginx-uix")
	for _, directory := range []string{configRoot, dataRoot, filepath.Join(dataRoot, "certs")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", directory, err)
		}
	}
	configuration := []byte("events {}\nhttp { server { listen 8080; } }\n")
	vaultMaterial := []byte("encrypted-test-certificate-material")
	for path, contents := range map[string][]byte{
		filepath.Join(configRoot, "nginx.conf"):        configuration,
		filepath.Join(dataRoot, "certs", "vault-item"): vaultMaterial,
	} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	if err := os.Symlink("nginx.conf", filepath.Join(configRoot, "active.conf")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	databasePath := filepath.Join(dataRoot, "nginx-uix.db")
	database, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(source) error = %v", err)
	}
	databaseOpen := true
	t.Cleanup(func() {
		if databaseOpen {
			if err := database.Close(); err != nil {
				t.Errorf("Close(source) error = %v", err)
			}
		}
	})
	if _, err := database.CreateInitialUser(ctx, auth.NewUser{
		Username: "operator", NormalizedName: "operator", PasswordHash: "argon2id-hash", CreatedAt: testTime(0),
	}); err != nil {
		t.Fatalf("CreateInitialUser() error = %v", err)
	}
	workspace := testWorkspace(1, "Recovered workspace", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.recovery-fixture")
	routeRun := testRouteRun(8, testTime(3))
	routeRun.WorkspaceID = workspace.ID
	if err := database.CreateRouteRun(ctx, routeRun, routelab.RunStage{
		RunID: routeRun.ID, Sequence: 1, Stage: routeRun.Stage,
		Result: routelab.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: routeRun.CreatedAt,
	}); err != nil {
		t.Fatalf("CreateRouteRun() error = %v", err)
	}
	account := testCertificateAccount(testTime(4).UTC())
	if err := database.CreateCertificateAccount(ctx, account); err != nil {
		t.Fatalf("CreateCertificateAccount() error = %v", err)
	}
	item := testCertificate(testTime(5).UTC(), account.ID, 6)
	version := testCertificateVersion(testTime(5).UTC(), item.ID)
	binding := testCertificateBinding(
		testTime(5).UTC(), item.ID, version.ID, certificate.BindingID("77777777777777777777777777777777"),
	)
	if err := database.CreateIssuedCertificate(ctx, item, version, []certificate.Binding{binding}); err != nil {
		t.Fatalf("CreateIssuedCertificate() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close(source before backup) error = %v", err)
	}
	databaseOpen = false

	snapshotRoot := secureTempDir(t)
	if err := copyColdStateTree(sourceRoot, snapshotRoot); err != nil {
		t.Fatalf("copy cold backup snapshot: %v", err)
	}
	restoredRoot := secureTempDir(t)
	if err := copyColdStateTree(snapshotRoot, restoredRoot); err != nil {
		t.Fatalf("restore cold backup snapshot: %v", err)
	}

	restoredDatabase, err := Open(ctx, filepath.Join(restoredRoot, "var", "lib", "nginx-uix", "nginx-uix.db"))
	if err != nil {
		t.Fatalf("Open(restored) error = %v", err)
	}
	t.Cleanup(func() {
		if err := restoredDatabase.Close(); err != nil {
			t.Errorf("Close(restored) error = %v", err)
		}
	})
	if got := migrationVersions(t, restoredDatabase); !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5, 6, 7}) {
		t.Fatalf("restored migration versions = %v, want [1 2 3 4 5 6 7]", got)
	}
	if count, err := restoredDatabase.UserCount(ctx); err != nil || count != 1 {
		t.Fatalf("restored UserCount() = %d, %v, want 1", count, err)
	}
	storedWorkspace, err := restoredDatabase.Workspace(ctx, workspace.ID)
	if err != nil || storedWorkspace.Name != workspace.Name || storedWorkspace.Revision != workspace.Revision {
		t.Fatalf("restored Workspace() = %#v, %v", storedWorkspace, err)
	}
	storedRun, err := restoredDatabase.RouteRun(ctx, routeRun.ID)
	if err != nil || storedRun.WorkspaceID != workspace.ID || storedRun.State != routeRun.State {
		t.Fatalf("restored RouteRun() = %#v, %v", storedRun, err)
	}
	storedAccount, err := restoredDatabase.CertificateAccount(ctx, account.ID)
	if err != nil || storedAccount.URI != account.URI {
		t.Fatalf("restored CertificateAccount() = %#v, %v", storedAccount, err)
	}
	storedCertificate, err := restoredDatabase.Certificate(ctx, item.ID)
	if err != nil || storedCertificate.ActiveVersionID != version.ID {
		t.Fatalf("restored Certificate() = %#v, %v", storedCertificate, err)
	}
	assertSQLiteIntegrity(t, restoredDatabase)

	for path, want := range map[string][]byte{
		filepath.Join(restoredRoot, "etc", "nginx", "nginx.conf"):                     configuration,
		filepath.Join(restoredRoot, "var", "lib", "nginx-uix", "certs", "vault-item"): vaultMaterial,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("restored file %q = %q, want %q", path, got, want)
		}
		information, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Lstat(%q) error = %v", path, err)
		}
		if information.Mode().Perm() != 0o600 {
			t.Fatalf("restored file %q mode = %v, want 0600", path, information.Mode().Perm())
		}
	}
	restoredLink := filepath.Join(restoredRoot, "etc", "nginx", "active.conf")
	linkTarget, err := os.Readlink(restoredLink)
	if err != nil {
		t.Fatalf("Readlink(%q) error = %v", restoredLink, err)
	}
	if linkTarget != "nginx.conf" {
		t.Fatalf("restored symlink target = %q, want nginx.conf", linkTarget)
	}
	linkInformation, err := os.Lstat(restoredLink)
	if err != nil {
		t.Fatalf("Lstat(%q) error = %v", restoredLink, err)
	}
	if linkInformation.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("restored path %q mode = %v, want symlink", restoredLink, linkInformation.Mode())
	}
}

func copyColdStateTree(sourceRoot, destinationRoot string) error {
	return filepath.WalkDir(sourceRoot, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return fmt.Errorf("resolve relative backup path: %w", err)
		}
		if relativePath == "." {
			return nil
		}
		destinationPath := filepath.Join(destinationRoot, relativePath)
		information, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect backup source %q: %w", sourcePath, err)
		}
		if information.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(sourcePath)
			if err != nil {
				return fmt.Errorf("read backup symlink %q: %w", sourcePath, err)
			}
			if err := os.Symlink(target, destinationPath); err != nil {
				return fmt.Errorf("write backup symlink %q: %w", destinationPath, err)
			}
			return nil
		}
		if entry.IsDir() {
			if err := os.MkdirAll(destinationPath, information.Mode().Perm()); err != nil {
				return fmt.Errorf("create backup directory %q: %w", destinationPath, err)
			}
			return os.Chmod(destinationPath, information.Mode().Perm())
		}
		if !information.Mode().IsRegular() {
			return fmt.Errorf("copy backup source %q: regular file required", sourcePath)
		}
		contents, err := os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("read backup source %q: %w", sourcePath, err)
		}
		if err := os.WriteFile(destinationPath, contents, information.Mode().Perm()); err != nil {
			return fmt.Errorf("write backup file %q: %w", destinationPath, err)
		}
		return os.Chmod(destinationPath, information.Mode().Perm())
	})
}

func assertSQLiteIntegrity(t *testing.T, database *DB) {
	t.Helper()
	var integrity string
	if err := database.sql.QueryRowContext(context.Background(), "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("PRAGMA integrity_check error = %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("PRAGMA integrity_check = %q, want ok", integrity)
	}
	rows, err := database.sql.QueryContext(context.Background(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_check error = %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("Close(foreign_key_check) error = %v", err)
		}
	}()
	if rows.Next() {
		t.Fatal("PRAGMA foreign_key_check reported a restored-data violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate PRAGMA foreign_key_check: %v", err)
	}
}
