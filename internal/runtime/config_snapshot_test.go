/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
package runtime

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const testConfigWorkspaceID = config.WorkspaceID("0123456789abcdef0123456789abcdef")

func TestConfigSnapshotUsesOnlyConfiguredFixedRoots(t *testing.T) {
	production := t.TempDir()
	workspaces := newConfigSnapshotWorkspaceRoot(t)
	writeNginxFixture(t, production)
	service, err := newConfigSnapshotService(configSnapshotOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, DataUID: os.Getuid(), DataGID: os.Getgid(),
		Limits: config.DefaultLimits(), Entry: "nginx.conf",
	})
	if err != nil {
		t.Fatal(err)
	}
	createPreparingWorkspace(t, workspaces, testConfigWorkspaceID)

	snapshot, err := service.ConfigSnapshot(context.Background(), testConfigWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProductionDigest == (config.Digest{}) || snapshot.BaseDigest != snapshot.ProductionDigest {
		t.Fatalf("snapshot digests = (%s, %s)", snapshot.ProductionDigest, snapshot.BaseDigest)
	}
	baseFile := filepath.Join(workspaces, string(testConfigWorkspaceID), "base", "nginx.conf")
	information, err := os.Stat(baseFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := information.Mode().Perm(), fs.FileMode(0o400); got != want {
		t.Fatalf("base file mode = %04o, want %04o", got, want)
	}
	assertConfigSnapshotOwner(t, information, os.Getuid(), os.Getgid())
	assertNoConfigSnapshotStages(t, workspaces, testConfigWorkspaceID)
}

func TestConfigSnapshotRejectsUnsafeWorkspaceState(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string, config.WorkspaceID)
		want  error
	}{
		{name: "missing workspace", want: fs.ErrNotExist},
		{name: "pre-existing base", build: func(t *testing.T, root string, id config.WorkspaceID) {
			createPreparingWorkspace(t, root, id)
			mustMkdirConfigSnapshot(t, filepath.Join(root, string(id), "base"))
		}, want: fs.ErrExist},
		{name: "workspace symlink", build: func(t *testing.T, root string, id config.WorkspaceID) {
			target := t.TempDir()
			if err := os.Symlink(target, filepath.Join(root, string(id))); err != nil {
				t.Fatal(err)
			}
		}, want: config.ErrPathInvalid},
		{name: "control symlink", build: func(t *testing.T, root string, id config.WorkspaceID) {
			workspace := filepath.Join(root, string(id))
			mustMkdirConfigSnapshot(t, workspace)
			if err := os.Symlink(t.TempDir(), filepath.Join(workspace, "control")); err != nil {
				t.Fatal(err)
			}
		}, want: config.ErrPathInvalid},
		{name: "base symlink", build: func(t *testing.T, root string, id config.WorkspaceID) {
			createPreparingWorkspace(t, root, id)
			if err := os.Symlink(t.TempDir(), filepath.Join(root, string(id), "base")); err != nil {
				t.Fatal(err)
			}
		}, want: config.ErrPathInvalid},
		{name: "workspace mode", build: func(t *testing.T, root string, id config.WorkspaceID) {
			createPreparingWorkspace(t, root, id)
			if err := os.Chmod(filepath.Join(root, string(id)), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: config.ErrPathInvalid},
		{name: "control mode", build: func(t *testing.T, root string, id config.WorkspaceID) {
			createPreparingWorkspace(t, root, id)
			if err := os.Chmod(filepath.Join(root, string(id), "control"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: config.ErrPathInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			production := t.TempDir()
			workspaces := newConfigSnapshotWorkspaceRoot(t)
			writeNginxFixture(t, production)
			if test.build != nil {
				test.build(t, workspaces, testConfigWorkspaceID)
			}
			service := mustConfigSnapshotService(t, production, workspaces)
			_, err := service.ConfigSnapshot(context.Background(), testConfigWorkspaceID)
			if !errors.Is(err, test.want) {
				t.Fatalf("ConfigSnapshot() error = %v, want %v", err, test.want)
			}
		})
	}

	service := mustConfigSnapshotService(t, t.TempDir(), newConfigSnapshotWorkspaceRoot(t))
	if _, err := service.ConfigSnapshot(context.Background(), config.WorkspaceID("/etc/nginx")); !errors.Is(err, config.ErrIdentifierInvalid) {
		t.Fatalf("ConfigSnapshot(invalid ID) error = %v, want ErrIdentifierInvalid", err)
	}

	production := t.TempDir()
	workspaces := newConfigSnapshotWorkspaceRoot(t)
	writeNginxFixture(t, production)
	createPreparingWorkspace(t, workspaces, testConfigWorkspaceID)
	wrongOwnerService, err := newConfigSnapshotService(configSnapshotOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, DataUID: os.Getuid(), DataGID: os.Getgid() + 1,
		Entry: "nginx.conf", Limits: config.DefaultLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongOwnerService.ConfigSnapshot(context.Background(), testConfigWorkspaceID); !errors.Is(err, config.ErrPathInvalid) {
		t.Fatalf("ConfigSnapshot(wrong owner) error = %v, want ErrPathInvalid", err)
	}
}

func TestConfigSnapshotBoundsContextToSixtySeconds(t *testing.T) {
	production := t.TempDir()
	workspaces := newConfigSnapshotWorkspaceRoot(t)
	writeNginxFixture(t, production)
	createPreparingWorkspace(t, workspaces, testConfigWorkspaceID)
	service := mustConfigSnapshotService(t, production, workspaces)
	service.configSnapshot.operations.snapshotTo = func(ctx context.Context, _, _ *config.ScopedRoot, _ config.SnapshotOptions) (config.Snapshot, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("snapshot context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > 60*time.Second {
			t.Fatalf("snapshot deadline remaining = %v, want (0, 60s]", remaining)
		}
		return config.Snapshot{}, context.DeadlineExceeded
	}

	_, err := service.ConfigSnapshot(context.Background(), testConfigWorkspaceID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ConfigSnapshot() error = %v, want context deadline", err)
	}
	assertNoConfigSnapshotStages(t, workspaces, testConfigWorkspaceID)
}

func TestConfigSnapshotCleansOnlyGeneratedStageAfterFaults(t *testing.T) {
	injected := errors.New("injected snapshot fault")
	tests := []struct {
		name   string
		mutate func(*configSnapshotOperations)
	}{
		{name: "chown", mutate: func(operations *configSnapshotOperations) {
			operations.fchown = func(int, int, int) error { return injected }
		}},
		{name: "fsync", mutate: func(operations *configSnapshotOperations) {
			operations.fsync = func(int) error { return injected }
		}},
		{name: "rename", mutate: func(operations *configSnapshotOperations) {
			operations.rename = func(int, string, int, string) error { return injected }
		}},
		{name: "snapshot changed", mutate: func(operations *configSnapshotOperations) {
			operations.snapshotTo = func(context.Context, *config.ScopedRoot, *config.ScopedRoot, config.SnapshotOptions) (config.Snapshot, error) {
				return config.Snapshot{}, errors.Join(config.ErrSnapshotChanged, injected)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			production := t.TempDir()
			workspaces := newConfigSnapshotWorkspaceRoot(t)
			writeNginxFixture(t, production)
			createPreparingWorkspace(t, workspaces, testConfigWorkspaceID)
			workspace := filepath.Join(workspaces, string(testConfigWorkspaceID))
			mustMkdirConfigSnapshot(t, filepath.Join(workspace, "base.stage-keep"))
			service := mustConfigSnapshotService(t, production, workspaces)
			test.mutate(&service.configSnapshot.operations)

			_, err := service.ConfigSnapshot(context.Background(), testConfigWorkspaceID)
			if !errors.Is(err, injected) {
				t.Fatalf("ConfigSnapshot() error = %v, want injected fault", err)
			}
			if _, statErr := os.Stat(filepath.Join(workspace, "base.stage-keep")); statErr != nil {
				t.Fatalf("unowned stage changed: %v", statErr)
			}
			entries, readErr := os.ReadDir(workspace)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "base.stage-") && entry.Name() != "base.stage-keep" {
					t.Fatalf("generated stage %q was not cleaned", entry.Name())
				}
			}
		})
	}
}

func TestConfigDigestUsesOnlyConfiguredNginxRootWithoutCommand(t *testing.T) {
	production := t.TempDir()
	workspaces := newConfigSnapshotWorkspaceRoot(t)
	writeNginxFixture(t, production)
	service := mustConfigSnapshotService(t, production, workspaces)
	service.executor = func(context.Context, commandSpec) (commandResult, error) {
		t.Fatal("ConfigDigest executed an Nginx command")
		return commandResult{}, nil
	}

	state, err := service.ConfigDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Digest == (config.Digest{}) || state.ManifestVersion != config.ManifestSchemaVersion || state.EntryCount == 0 {
		t.Fatalf("ConfigDigest() = %#v", state)
	}
}

func TestConfigDigestBoundsContextToFifteenSeconds(t *testing.T) {
	production := t.TempDir()
	writeNginxFixture(t, production)
	service := mustConfigSnapshotService(t, production, newConfigSnapshotWorkspaceRoot(t))
	service.configSnapshot.operations.digestRoot = func(ctx context.Context, _ *config.ScopedRoot, _ config.SnapshotOptions) (config.ProductionState, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("digest context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > 15*time.Second {
			t.Fatalf("digest deadline remaining = %v, want (0, 15s]", remaining)
		}
		return config.ProductionState{}, context.DeadlineExceeded
	}

	_, err := service.ConfigDigest(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ConfigDigest() error = %v, want context deadline", err)
	}
}

func mustConfigSnapshotService(t *testing.T, production, workspaces string) *Service {
	t.Helper()
	service, err := newConfigSnapshotService(configSnapshotOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, DataUID: os.Getuid(), DataGID: os.Getgid(),
		Entry: "nginx.conf", Limits: config.DefaultLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func writeNginxFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("events {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func createPreparingWorkspace(t *testing.T, root string, id config.WorkspaceID) {
	t.Helper()
	workspace := filepath.Join(root, string(id))
	mustMkdirConfigSnapshot(t, workspace)
	mustMkdirConfigSnapshot(t, filepath.Join(workspace, "control"))
}

func mustMkdirConfigSnapshot(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
}

func newConfigSnapshotWorkspaceRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chown(root, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, configWorkspaceMode); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertConfigSnapshotOwner(t *testing.T, information fs.FileInfo, uid, gid int) {
	t.Helper()
	stat, ok := information.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid || int(stat.Gid) != gid {
		t.Fatalf("owner = %#v, want uid=%d gid=%d", information.Sys(), uid, gid)
	}
}

func assertNoConfigSnapshotStages(t *testing.T, root string, id config.WorkspaceID) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, string(id)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "base.stage-") {
			t.Fatalf("unexpected snapshot stage %q", entry.Name())
		}
	}
}
