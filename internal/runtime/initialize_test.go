/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInitializeContainerCopiesCompleteDefaultsTreeWhenTargetEmpty(t *testing.T) {
	root := t.TempDir()
	defaultsRoot := filepath.Join(root, "defaults")
	nginxRoot := filepath.Join(root, "nginx")
	mustMkdir(t, filepath.Join(defaultsRoot, "conf.d"), 0o750)
	mustMkdir(t, filepath.Join(defaultsRoot, "html"), 0o755)
	mustWriteFile(t, filepath.Join(defaultsRoot, "nginx.conf"), []byte("main configuration\n"), 0o640)
	mustWriteFile(t, filepath.Join(defaultsRoot, ".packaged"), []byte("hidden marker\n"), 0o600)
	mustWriteFile(t, filepath.Join(defaultsRoot, "conf.d", "default.conf"), []byte("server configuration\n"), 0o644)
	mustWriteFile(t, filepath.Join(defaultsRoot, "html", "index.html"), []byte("welcome page\n"), 0o644)
	mustMkdir(t, nginxRoot, 0o755)

	options := InitializeOptions{
		DefaultsRoot:  defaultsRoot,
		NginxRoot:     nginxRoot,
		DataRoot:      filepath.Join(root, "data"),
		WorkspaceRoot: filepath.Join(root, "data", "workspaces"),
		RunRoot:       filepath.Join(root, "run"),
		DataUID:       os.Getuid(),
		DataGID:       os.Getgid(),
	}
	if err := InitializeContainer(context.Background(), options); err != nil {
		t.Fatalf("InitializeContainer() error = %v", err)
	}

	got := snapshotTree(t, nginxRoot)
	want := snapshotTree(t, defaultsRoot)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copied tree = %#v, want %#v", got, want)
	}
}

func TestInitializeNginxRuntimeLeavesDataUntouched(t *testing.T) {
	root := t.TempDir()
	defaultsRoot := filepath.Join(root, "defaults")
	nginxRoot := filepath.Join(root, "nginx")
	dataRoot := filepath.Join(root, "data")
	runRoot := filepath.Join(root, "run")
	mustMkdir(t, defaultsRoot, 0o755)
	mustWriteFile(t, filepath.Join(defaultsRoot, "nginx.conf"), []byte("packaged default\n"), 0o644)
	mustMkdir(t, nginxRoot, 0o755)
	mustMkdir(t, dataRoot, 0o751)
	mustWriteFile(t, filepath.Join(dataRoot, "nginx-uix.db"), []byte("existing database\n"), 0o640)
	mustWriteFile(t, filepath.Join(dataRoot, ".keep"), []byte("preserve data metadata\n"), 0o600)
	mustMkdir(t, runRoot, 0o700)
	mustWriteFile(t, filepath.Join(runRoot, "stale.pid"), []byte("123\n"), 0o600)
	setTreeModTime(t, dataRoot, time.Unix(1_721_001_000, 111_000_000))
	dataBefore := snapshotExactTree(t, dataRoot)

	options := InitializeOptions{
		DefaultsRoot:  defaultsRoot,
		NginxRoot:     nginxRoot,
		DataRoot:      dataRoot,
		WorkspaceRoot: filepath.Join(dataRoot, "workspaces"),
		RunRoot:       runRoot,
		DataUID:       os.Getuid(),
		DataGID:       os.Getgid(),
	}
	if err := InitializeNginxRuntime(context.Background(), options); err != nil {
		t.Fatalf("InitializeNginxRuntime() error = %v", err)
	}

	if dataAfter := snapshotExactTree(t, dataRoot); !reflect.DeepEqual(dataAfter, dataBefore) {
		t.Fatalf("runtime initialization changed data tree\nbefore: %#v\nafter:  %#v", dataBefore, dataAfter)
	}
	if got, want := snapshotTree(t, nginxRoot), snapshotTree(t, defaultsRoot); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime initialization copied tree = %#v, want %#v", got, want)
	}
	if entries, err := os.ReadDir(runRoot); err != nil || len(entries) != 0 {
		t.Fatalf("runtime directory entries = %#v, %v; want empty", entries, err)
	}
	if information, err := os.Lstat(runRoot); err != nil || information.Mode().Perm() != 0o755 {
		t.Fatalf("runtime directory mode = %v, %v; want 0755", information, err)
	}
}

func TestPrepareContainerDataLeavesNginxAndRuntimeUntouched(t *testing.T) {
	root := t.TempDir()
	defaultsRoot := filepath.Join(root, "defaults")
	nginxRoot := filepath.Join(root, "nginx")
	dataRoot := filepath.Join(root, "data")
	runRoot := filepath.Join(root, "run")
	mustMkdir(t, defaultsRoot, 0o755)
	mustMkdir(t, nginxRoot, 0o711)
	mustWriteFile(t, filepath.Join(nginxRoot, "user.conf"), []byte("user configuration\n"), 0o640)
	mustMkdir(t, dataRoot, 0o755)
	mustWriteFile(t, filepath.Join(dataRoot, "nginx-uix.db"), []byte("existing database\n"), 0o644)
	mustMkdir(t, runRoot, 0o700)
	mustWriteFile(t, filepath.Join(runRoot, "active.sock"), []byte("runtime state\n"), 0o600)
	setTreeModTime(t, nginxRoot, time.Unix(1_721_001_100, 222_000_000))
	setTreeModTime(t, runRoot, time.Unix(1_721_001_200, 333_000_000))
	nginxBefore := snapshotExactTree(t, nginxRoot)
	runBefore := snapshotExactTree(t, runRoot)

	options := InitializeOptions{
		DefaultsRoot:  defaultsRoot,
		NginxRoot:     nginxRoot,
		DataRoot:      dataRoot,
		WorkspaceRoot: filepath.Join(dataRoot, "workspaces"),
		RunRoot:       runRoot,
		DataUID:       os.Getuid(),
		DataGID:       os.Getgid(),
	}
	if err := PrepareContainerData(context.Background(), options); err != nil {
		t.Fatalf("PrepareContainerData() error = %v", err)
	}

	if nginxAfter := snapshotExactTree(t, nginxRoot); !reflect.DeepEqual(nginxAfter, nginxBefore) {
		t.Fatalf("data preparation changed nginx tree\nbefore: %#v\nafter:  %#v", nginxBefore, nginxAfter)
	}
	if runAfter := snapshotExactTree(t, runRoot); !reflect.DeepEqual(runAfter, runBefore) {
		t.Fatalf("data preparation changed runtime tree\nbefore: %#v\nafter:  %#v", runBefore, runAfter)
	}
	assertPathModeAndOwner(t, dataRoot, options.DataUID, options.DataGID)
	assertPathModeAndOwner(t, options.WorkspaceRoot, options.DataUID, options.DataGID)
	databaseInformation, err := os.Lstat(filepath.Join(dataRoot, "nginx-uix.db"))
	if err != nil {
		t.Fatalf("Lstat(database) error = %v", err)
	}
	if got := databaseInformation.Mode().Perm(); got&^fs.FileMode(0o600) != 0 {
		t.Fatalf("database mode = %04o, want no broader than 0600", got)
	}
}

func TestPrepareContainerDataCreatesAndSecuresPersistentWorkspaceRoot(t *testing.T) {
	t.Run("upgrades an existing database without a workspace directory", func(t *testing.T) {
		root := t.TempDir()
		defaultsRoot := filepath.Join(root, "defaults")
		nginxRoot := filepath.Join(root, "nginx")
		dataRoot := filepath.Join(root, "data")
		runRoot := filepath.Join(root, "run")
		for _, path := range []string{defaultsRoot, nginxRoot, dataRoot, runRoot} {
			mustMkdir(t, path, 0o700)
		}
		mustWriteFile(t, filepath.Join(dataRoot, "nginx-uix.db"), []byte("existing"), 0o600)
		options := InitializeOptions{
			DefaultsRoot: defaultsRoot, NginxRoot: nginxRoot, DataRoot: dataRoot,
			WorkspaceRoot: filepath.Join(dataRoot, "workspaces"), RunRoot: runRoot,
			DataUID: os.Getuid(), DataGID: os.Getgid(),
		}
		if err := PrepareContainerData(context.Background(), options); err != nil {
			t.Fatal(err)
		}
		assertPathModeAndOwner(t, options.WorkspaceRoot, options.DataUID, options.DataGID)
	})

	t.Run("repairs wrong mode and owner", func(t *testing.T) {
		root := t.TempDir()
		defaultsRoot := filepath.Join(root, "defaults")
		nginxRoot := filepath.Join(root, "nginx")
		dataRoot := filepath.Join(root, "data")
		runRoot := filepath.Join(root, "run")
		for _, path := range []string{defaultsRoot, nginxRoot, dataRoot, runRoot} {
			mustMkdir(t, path, 0o700)
		}
		workspaceRoot := filepath.Join(dataRoot, "workspaces")
		mustMkdir(t, workspaceRoot, 0o755)
		groups, err := os.Getgroups()
		if err != nil {
			t.Fatal(err)
		}
		wantGID := os.Getgid()
		for _, group := range groups {
			if group != fileOwner(t, workspaceRoot).gid {
				wantGID = group
				break
			}
		}
		options := InitializeOptions{
			DefaultsRoot: defaultsRoot, NginxRoot: nginxRoot, DataRoot: dataRoot,
			WorkspaceRoot: workspaceRoot, RunRoot: runRoot, DataUID: os.Getuid(), DataGID: wantGID,
		}
		if err := PrepareContainerData(context.Background(), options); err != nil {
			t.Fatal(err)
		}
		assertPathModeAndOwner(t, workspaceRoot, options.DataUID, options.DataGID)
	})
}

func TestPrepareContainerDataRejectsUnsafePersistentWorkspaceRoot(t *testing.T) {
	for _, kind := range []string{"symlink", "regular file"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			defaultsRoot := filepath.Join(root, "defaults")
			nginxRoot := filepath.Join(root, "nginx")
			dataRoot := filepath.Join(root, "data")
			runRoot := filepath.Join(root, "run")
			for _, path := range []string{defaultsRoot, nginxRoot, dataRoot, runRoot} {
				mustMkdir(t, path, 0o700)
			}
			workspaceRoot := filepath.Join(dataRoot, "workspaces")
			outside := filepath.Join(root, "outside")
			mustMkdir(t, outside, 0o700)
			if kind == "symlink" {
				if err := os.Symlink(outside, workspaceRoot); err != nil {
					t.Fatal(err)
				}
			} else {
				mustWriteFile(t, workspaceRoot, []byte("keep"), 0o600)
			}
			options := InitializeOptions{
				DefaultsRoot: defaultsRoot, NginxRoot: nginxRoot, DataRoot: dataRoot,
				WorkspaceRoot: workspaceRoot, RunRoot: runRoot, DataUID: os.Getuid(), DataGID: os.Getgid(),
			}
			if err := PrepareContainerData(context.Background(), options); err == nil {
				t.Fatal("PrepareContainerData() error = nil, want unsafe workspace root rejection")
			}
			if kind == "symlink" {
				if information, err := os.Lstat(workspaceRoot); err != nil || information.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("workspace symlink changed: %v, %v", information, err)
				}
			} else if contents, err := os.ReadFile(workspaceRoot); err != nil || string(contents) != "keep" {
				t.Fatalf("workspace file = %q, %v", contents, err)
			}
		})
	}
}

func TestInitializeContainerLeavesEveryNonemptyTargetUntouched(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string)
	}{
		{
			name: "regular file",
			build: func(t *testing.T, root string) {
				mustWriteFile(t, filepath.Join(root, "existing.conf"), []byte("user configuration\n"), 0o640)
			},
		},
		{
			name: "directory",
			build: func(t *testing.T, root string) {
				mustMkdir(t, filepath.Join(root, "existing"), 0o750)
				mustWriteFile(t, filepath.Join(root, "existing", "nested.conf"), []byte("nested user configuration\n"), 0o600)
			},
		},
		{
			name: "symlink",
			build: func(t *testing.T, root string) {
				outside := filepath.Join(t.TempDir(), "outside.conf")
				mustWriteFile(t, outside, []byte("outside user configuration\n"), 0o600)
				if err := os.Symlink(outside, filepath.Join(root, "existing-link")); err != nil {
					t.Fatalf("Symlink(existing-link) error = %v", err)
				}
			},
		},
		{
			name: "hidden entry",
			build: func(t *testing.T, root string) {
				mustWriteFile(t, filepath.Join(root, ".keep"), []byte("volume marker\n"), 0o600)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			defaultsRoot := filepath.Join(root, "defaults")
			nginxRoot := filepath.Join(root, "nginx")
			mustMkdir(t, defaultsRoot, 0o755)
			mustWriteFile(t, filepath.Join(defaultsRoot, "new.conf"), []byte("packaged default\n"), 0o644)
			mustMkdir(t, nginxRoot, 0o711)
			test.build(t, nginxRoot)
			setTreeModTime(t, nginxRoot, time.Unix(1_721_000_000, 123_000_000))
			before := snapshotExactTree(t, nginxRoot)

			options := testInitializeOptions(root, defaultsRoot, nginxRoot)
			if err := InitializeContainer(context.Background(), options); err != nil {
				t.Fatalf("InitializeContainer() error = %v", err)
			}

			after := snapshotExactTree(t, nginxRoot)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("nonempty target changed\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestInitializeContainerRejectsSourceAndTargetRootSymlinks(t *testing.T) {
	t.Run("target root symlink", func(t *testing.T) {
		root := t.TempDir()
		defaultsRoot := filepath.Join(root, "defaults")
		mustMkdir(t, defaultsRoot, 0o755)
		mustWriteFile(t, filepath.Join(defaultsRoot, "nginx.conf"), []byte("packaged default\n"), 0o644)
		outside := filepath.Join(root, "outside")
		mustMkdir(t, outside, 0o755)
		nginxRoot := filepath.Join(root, "nginx")
		if err := os.Symlink(outside, nginxRoot); err != nil {
			t.Fatalf("Symlink(target root) error = %v", err)
		}

		before := snapshotExactTree(t, outside)
		err := InitializeContainer(context.Background(), testInitializeOptions(root, defaultsRoot, nginxRoot))
		if err == nil {
			t.Fatal("InitializeContainer() error = nil, want target root symlink rejection")
		}
		after := snapshotExactTree(t, outside)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("target symlink destination changed\nbefore: %#v\nafter:  %#v", before, after)
		}
		linkTarget, linkErr := os.Readlink(nginxRoot)
		if linkErr != nil || linkTarget != outside {
			t.Fatalf("target symlink = %q, %v; want %q unchanged", linkTarget, linkErr, outside)
		}
	})

	t.Run("source root symlink", func(t *testing.T) {
		root := t.TempDir()
		realDefaults := filepath.Join(root, "real-defaults")
		mustMkdir(t, realDefaults, 0o755)
		mustWriteFile(t, filepath.Join(realDefaults, "nginx.conf"), []byte("packaged default\n"), 0o644)
		defaultsRoot := filepath.Join(root, "defaults")
		if err := os.Symlink(realDefaults, defaultsRoot); err != nil {
			t.Fatalf("Symlink(source root) error = %v", err)
		}
		nginxRoot := filepath.Join(root, "nginx")
		mustMkdir(t, nginxRoot, 0o755)

		err := InitializeContainer(context.Background(), testInitializeOptions(root, defaultsRoot, nginxRoot))
		if err == nil {
			t.Fatal("InitializeContainer() error = nil, want source root symlink rejection")
		}
		if got := snapshotExactTree(t, nginxRoot); len(got) != 1 {
			t.Fatalf("empty target changed after source rejection: %#v", got)
		}
	})
}

func TestInitializeContainerRejectsNestedSourceSymlinkAndCleansPartialCopy(t *testing.T) {
	root := t.TempDir()
	defaultsRoot := filepath.Join(root, "defaults")
	nginxRoot := filepath.Join(root, "nginx")
	mustMkdir(t, defaultsRoot, 0o755)
	mustWriteFile(t, filepath.Join(defaultsRoot, "a-first.conf"), []byte("copied before failure\n"), 0o644)
	outside := filepath.Join(root, "outside.conf")
	mustWriteFile(t, outside, []byte("outside must remain unchanged\n"), 0o600)
	if err := os.Symlink(outside, filepath.Join(defaultsRoot, "z-unsafe-link")); err != nil {
		t.Fatalf("Symlink(nested source) error = %v", err)
	}
	mustMkdir(t, nginxRoot, 0o755)
	setTreeModTime(t, nginxRoot, time.Unix(1_721_000_100, 456_000_000))
	targetBefore := snapshotExactTree(t, nginxRoot)
	outsideBefore := snapshotExactTree(t, root)["outside.conf"]

	err := InitializeContainer(context.Background(), testInitializeOptions(root, defaultsRoot, nginxRoot))
	if err == nil {
		t.Fatal("InitializeContainer() error = nil, want nested source symlink rejection")
	}
	if targetAfter := snapshotExactTree(t, nginxRoot); !reflect.DeepEqual(targetAfter, targetBefore) {
		t.Fatalf("partial copy was not cleaned\nbefore: %#v\nafter:  %#v", targetBefore, targetAfter)
	}
	if outsideAfter := snapshotExactTree(t, root)["outside.conf"]; !reflect.DeepEqual(outsideAfter, outsideBefore) {
		t.Fatalf("source symlink destination changed\nbefore: %#v\nafter:  %#v", outsideBefore, outsideAfter)
	}
}

func TestInitializeContainerCleansPartialCopyWhenFileCopyFails(t *testing.T) {
	root := t.TempDir()
	defaultsRoot := filepath.Join(root, "defaults")
	nginxRoot := filepath.Join(root, "nginx")
	mustMkdir(t, defaultsRoot, 0o755)
	mustWriteFile(t, filepath.Join(defaultsRoot, "a-first.conf"), []byte("first\n"), 0o644)
	mustWriteFile(t, filepath.Join(defaultsRoot, "b-second.conf"), []byte("second\n"), 0o644)
	mustMkdir(t, nginxRoot, 0o755)
	setTreeModTime(t, nginxRoot, time.Unix(1_721_000_200, 789_000_000))
	before := snapshotExactTree(t, nginxRoot)

	injected := errors.New("injected file copy failure")
	initializer := newContainerInitializer()
	copyFile := initializer.copyFile
	calls := 0
	initializer.copyFile = func(
		ctx context.Context,
		sourcePath string,
		targetPath string,
		mode fs.FileMode,
		transaction *initializeCopyTransaction,
	) error {
		calls++
		if calls == 2 {
			return injected
		}
		return copyFile(ctx, sourcePath, targetPath, mode, transaction)
	}

	err := initializer.initialize(context.Background(), testInitializeOptions(root, defaultsRoot, nginxRoot))
	if !errors.Is(err, injected) {
		t.Fatalf("initialize() error = %v, want injected copy failure", err)
	}
	if after := snapshotExactTree(t, nginxRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed copy changed target\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInitializeContainerCancellationAfterLastCopyRollsBack(t *testing.T) {
	root := t.TempDir()
	defaultsRoot := filepath.Join(root, "defaults")
	nginxRoot := filepath.Join(root, "nginx")
	mustMkdir(t, defaultsRoot, 0o755)
	mustWriteFile(t, filepath.Join(defaultsRoot, "only.conf"), []byte("last copied file\n"), 0o644)
	mustMkdir(t, nginxRoot, 0o755)
	setTreeModTime(t, nginxRoot, time.Unix(1_721_000_300, 111_000_000))
	before := snapshotExactTree(t, nginxRoot)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initializer := newContainerInitializer()
	copyFile := initializer.copyFile
	initializer.copyFile = func(
		ctx context.Context,
		sourcePath string,
		targetPath string,
		mode fs.FileMode,
		transaction *initializeCopyTransaction,
	) error {
		if err := copyFile(ctx, sourcePath, targetPath, mode, transaction); err != nil {
			return err
		}
		cancel()
		return nil
	}

	err := initializer.initialize(ctx, testInitializeOptions(root, defaultsRoot, nginxRoot))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("initialize() error = %v, want context cancellation", err)
	}
	if after := snapshotExactTree(t, nginxRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("canceled copy changed target\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInitializeContainerAbortsConcurrentMergeAndPreservesNewUserEntry(t *testing.T) {
	root := t.TempDir()
	defaultsRoot := filepath.Join(root, "defaults")
	nginxRoot := filepath.Join(root, "nginx")
	mustMkdir(t, defaultsRoot, 0o755)
	mustWriteFile(t, filepath.Join(defaultsRoot, "default.conf"), []byte("packaged default\n"), 0o644)
	mustMkdir(t, nginxRoot, 0o755)

	injectedUserPath := filepath.Join(nginxRoot, "user.conf")
	initializer := newContainerInitializer()
	copyFile := initializer.copyFile
	initializer.copyFile = func(
		ctx context.Context,
		sourcePath string,
		targetPath string,
		mode fs.FileMode,
		transaction *initializeCopyTransaction,
	) error {
		if err := copyFile(ctx, sourcePath, targetPath, mode, transaction); err != nil {
			return err
		}
		mustWriteFile(t, injectedUserPath, []byte("concurrent user content\n"), 0o600)
		return nil
	}

	err := initializer.initialize(context.Background(), testInitializeOptions(root, defaultsRoot, nginxRoot))
	if err == nil {
		t.Fatal("initialize() error = nil, want concurrent target merge rejection")
	}
	if _, statErr := os.Lstat(filepath.Join(nginxRoot, "default.conf")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("packaged default remains after rejected merge: %v", statErr)
	}
	contents, readErr := os.ReadFile(injectedUserPath)
	if readErr != nil || string(contents) != "concurrent user content\n" {
		t.Fatalf("concurrent user entry = %q, %v; want unchanged", contents, readErr)
	}
	information, statErr := os.Lstat(injectedUserPath)
	if statErr != nil || information.Mode().Perm() != 0o600 {
		t.Fatalf("concurrent user entry mode = %v, %v; want 0600", information, statErr)
	}
}

func TestPrepareDirectoriesSecuresPersistentDataAndRecreatesRuntime(t *testing.T) {
	root := t.TempDir()
	nginxRoot := filepath.Join(root, "nginx")
	mustMkdir(t, nginxRoot, 0o755)
	mustWriteFile(t, filepath.Join(nginxRoot, ".keep"), []byte("existing configuration volume\n"), 0o640)
	setTreeModTime(t, nginxRoot, time.Unix(1_721_000_400, 222_000_000))
	nginxBefore := snapshotExactTree(t, nginxRoot)

	dataRoot := filepath.Join(root, "data")
	mustMkdir(t, dataRoot, 0o755)
	databasePath := filepath.Join(dataRoot, "nginx-uix.db")
	mustWriteFile(t, databasePath, []byte("existing sqlite bytes"), 0o644)
	databaseTime := time.Unix(1_721_000_500, 333_000_000)
	if err := os.Chtimes(databasePath, databaseTime, databaseTime); err != nil {
		t.Fatalf("Chtimes(database) error = %v", err)
	}

	runRoot := filepath.Join(root, "run")
	mustMkdir(t, runRoot, 0o700)
	mustWriteFile(t, filepath.Join(runRoot, "stale.sock"), []byte("stale runtime state"), 0o600)
	oldRunInformation, err := os.Lstat(runRoot)
	if err != nil {
		t.Fatalf("Lstat(old run root) error = %v", err)
	}

	options := InitializeOptions{
		DefaultsRoot:  filepath.Join(root, "must-not-be-inspected"),
		NginxRoot:     nginxRoot,
		DataRoot:      dataRoot,
		WorkspaceRoot: filepath.Join(dataRoot, "workspaces"),
		RunRoot:       runRoot,
		DataUID:       os.Getuid(),
		DataGID:       os.Getgid(),
	}
	if err := InitializeContainer(context.Background(), options); err != nil {
		t.Fatalf("InitializeContainer() error = %v", err)
	}

	if nginxAfter := snapshotExactTree(t, nginxRoot); !reflect.DeepEqual(nginxAfter, nginxBefore) {
		t.Fatalf("nonempty nginx root changed\nbefore: %#v\nafter:  %#v", nginxBefore, nginxAfter)
	}
	assertPathModeAndOwner(t, dataRoot, options.DataUID, options.DataGID)
	databaseInformation, err := os.Lstat(databasePath)
	if err != nil {
		t.Fatalf("Lstat(database) error = %v", err)
	}
	if got := databaseInformation.Mode().Perm(); got&^fs.FileMode(0o600) != 0 {
		t.Fatalf("database mode = %04o, want no broader than 0600", got)
	}
	assertPathOwner(t, databasePath, databaseInformation, options.DataUID, options.DataGID)
	contents, err := os.ReadFile(databasePath)
	if err != nil || string(contents) != "existing sqlite bytes" {
		t.Fatalf("database contents = %q, %v; want unchanged", contents, err)
	}
	if got := databaseInformation.ModTime(); !got.Equal(databaseTime) {
		t.Fatalf("database mtime = %v, want %v", got, databaseTime)
	}

	newRunInformation, err := os.Lstat(runRoot)
	if err != nil {
		t.Fatalf("Lstat(new run root) error = %v", err)
	}
	if os.SameFile(oldRunInformation, newRunInformation) {
		t.Fatal("runtime directory inode was reused, want full recreation")
	}
	if got, want := newRunInformation.Mode().Perm(), fs.FileMode(0o755); got != want {
		t.Fatalf("runtime directory mode = %04o, want %04o", got, want)
	}
	if entries, err := os.ReadDir(runRoot); err != nil || len(entries) != 0 {
		t.Fatalf("runtime directory entries = %#v, %v; want empty", entries, err)
	}
}

func TestInitializeContainerRejectsOverlappingTrustedRootsBeforeMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*InitializeOptions)
	}{
		{
			name: "defaults and nginx",
			mutate: func(options *InitializeOptions) {
				options.DefaultsRoot = options.NginxRoot
			},
		},
		{
			name: "data and nginx",
			mutate: func(options *InitializeOptions) {
				options.DataRoot = options.NginxRoot
			},
		},
		{
			name: "runtime and nginx",
			mutate: func(options *InitializeOptions) {
				options.RunRoot = options.NginxRoot
			},
		},
		{
			name: "data and runtime",
			mutate: func(options *InitializeOptions) {
				options.RunRoot = options.DataRoot
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			defaultsRoot := filepath.Join(root, "defaults")
			nginxRoot := filepath.Join(root, "nginx")
			mustMkdir(t, defaultsRoot, 0o755)
			mustWriteFile(t, filepath.Join(defaultsRoot, "nginx.conf"), []byte("packaged default\n"), 0o644)
			mustMkdir(t, nginxRoot, 0o755)
			mustWriteFile(t, filepath.Join(nginxRoot, ".keep"), []byte("existing configuration\n"), 0o640)
			setTreeModTime(t, nginxRoot, time.Unix(1_721_000_600, 444_000_000))
			before := snapshotExactTree(t, nginxRoot)
			options := testInitializeOptions(root, defaultsRoot, nginxRoot)
			test.mutate(&options)

			if err := InitializeContainer(context.Background(), options); err == nil {
				t.Fatal("InitializeContainer() error = nil, want overlapping root rejection")
			}
			if after := snapshotExactTree(t, nginxRoot); !reflect.DeepEqual(after, before) {
				t.Fatalf("overlap rejection changed nginx tree\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestPrepareDirectoriesRejectsSymlinksBeforeAnyMutation(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string, *InitializeOptions)
	}{
		{
			name: "data root",
			build: func(t *testing.T, root string, options *InitializeOptions) {
				outside := filepath.Join(root, "outside-data")
				mustMkdir(t, outside, 0o755)
				if err := os.Symlink(outside, options.DataRoot); err != nil {
					t.Fatalf("Symlink(data root) error = %v", err)
				}
			},
		},
		{
			name: "sqlite database",
			build: func(t *testing.T, root string, options *InitializeOptions) {
				mustMkdir(t, options.DataRoot, 0o755)
				outside := filepath.Join(root, "outside.db")
				mustWriteFile(t, outside, []byte("outside database\n"), 0o644)
				if err := os.Symlink(outside, filepath.Join(options.DataRoot, "nginx-uix.db")); err != nil {
					t.Fatalf("Symlink(database) error = %v", err)
				}
			},
		},
		{
			name: "runtime root",
			build: func(t *testing.T, root string, options *InitializeOptions) {
				mustMkdir(t, options.DataRoot, 0o755)
				outside := filepath.Join(root, "outside-run")
				mustMkdir(t, outside, 0o755)
				mustWriteFile(t, filepath.Join(outside, "keep"), []byte("outside runtime\n"), 0o600)
				if err := os.Symlink(outside, options.RunRoot); err != nil {
					t.Fatalf("Symlink(runtime root) error = %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			defaultsRoot := filepath.Join(root, "defaults")
			nginxRoot := filepath.Join(root, "nginx")
			mustMkdir(t, defaultsRoot, 0o755)
			mustMkdir(t, nginxRoot, 0o755)
			mustWriteFile(t, filepath.Join(nginxRoot, ".keep"), []byte("existing configuration\n"), 0o640)
			options := testInitializeOptions(root, defaultsRoot, nginxRoot)
			test.build(t, root, &options)
			setTreeModTime(t, root, time.Unix(1_721_000_700, 555_000_000))
			before := snapshotExactTree(t, root)

			if err := InitializeContainer(context.Background(), options); err == nil {
				t.Fatal("InitializeContainer() error = nil, want symlink rejection")
			}
			if after := snapshotExactTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("symlink rejection changed filesystem\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestInitializeContainerPackagedDefaultsAreMinimalAndSafe(t *testing.T) {
	defaultsRoot, err := filepath.Abs(filepath.Join("..", "..", "deploy", "docker", "defaults"))
	if err != nil {
		t.Fatalf("Abs(packaged defaults) error = %v", err)
	}
	nginxConfiguration := mustReadFile(t, filepath.Join(defaultsRoot, "nginx.conf"))
	for _, required := range []string{
		"worker_processes auto;",
		"pid /run/nginx-uix/nginx.pid;",
		"error_log stderr warn;",
		"worker_connections 1024;",
		"access_log /dev/stdout combined;",
		"include conf.d/*.conf;",
	} {
		if !strings.Contains(nginxConfiguration, required) {
			t.Errorf("nginx.conf missing %q", required)
		}
	}

	serverConfiguration := mustReadFile(t, filepath.Join(defaultsRoot, "conf.d", "default.conf"))
	for _, required := range []string{"listen 80 default_server;", "root html;", "index index.html;"} {
		if !strings.Contains(serverConfiguration, required) {
			t.Errorf("default.conf missing %q", required)
		}
	}
	for _, forbidden := range []string{"proxy_pass", "ssl_certificate", "ssl_certificate_key", "listen 443"} {
		if strings.Contains(nginxConfiguration, forbidden) || strings.Contains(serverConfiguration, forbidden) {
			t.Errorf("packaged defaults contain forbidden directive %q", forbidden)
		}
	}

	welcomePage := mustReadFile(t, filepath.Join(defaultsRoot, "html", "index.html"))
	if !strings.Contains(welcomePage, "<!doctype html>") || !strings.Contains(welcomePage, "Nginx UIX") {
		t.Errorf("welcome page is not the minimal Nginx UIX document: %q", welcomePage)
	}

	root := t.TempDir()
	nginxRoot := filepath.Join(root, "nginx")
	mustMkdir(t, nginxRoot, 0o755)
	if err := InitializeContainer(context.Background(), testInitializeOptions(root, defaultsRoot, nginxRoot)); err != nil {
		t.Fatalf("InitializeContainer(packaged defaults) error = %v", err)
	}
	if got, want := snapshotTree(t, nginxRoot), snapshotTree(t, defaultsRoot); !reflect.DeepEqual(got, want) {
		t.Fatalf("copied packaged defaults = %#v, want %#v", got, want)
	}

	nonemptyRoot := filepath.Join(root, "nonempty-nginx")
	mustMkdir(t, nonemptyRoot, 0o755)
	mustWriteFile(t, filepath.Join(nonemptyRoot, ".mounted-volume"), []byte("keep\n"), 0o600)
	setTreeModTime(t, nonemptyRoot, time.Unix(1_721_000_800, 666_000_000))
	before := snapshotExactTree(t, nonemptyRoot)
	options := testInitializeOptions(filepath.Join(root, "second"), defaultsRoot, nonemptyRoot)
	mustMkdir(t, filepath.Dir(options.DataRoot), 0o755)
	if err := InitializeContainer(context.Background(), options); err != nil {
		t.Fatalf("InitializeContainer(packaged defaults, hidden target) error = %v", err)
	}
	if after := snapshotExactTree(t, nonemptyRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("packaged defaults changed hidden-entry target\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInitializeContainerPackagedDefaultsValidateWithRealIsolatedNginx(t *testing.T) {
	if os.Getenv("NGINX_UIX_INTEGRATION") != "1" {
		t.Skip("real Nginx integration disabled; set NGINX_UIX_INTEGRATION=1")
	}
	binary := os.Getenv("NGINX_BIN")
	if binary == "" {
		binary = "nginx"
	}
	resolvedBinary, err := exec.LookPath(binary)
	if err != nil {
		t.Fatalf("resolve real Nginx binary %q: %v", binary, err)
	}

	defaultsRoot, err := filepath.Abs(filepath.Join("..", "..", "deploy", "docker", "defaults"))
	if err != nil {
		t.Fatalf("Abs(packaged defaults) error = %v", err)
	}
	root := t.TempDir()
	nginxRoot := filepath.Join(root, "prefix")
	mustMkdir(t, nginxRoot, 0o755)
	options := testInitializeOptions(root, defaultsRoot, nginxRoot)
	if err := InitializeContainer(context.Background(), options); err != nil {
		t.Fatalf("InitializeContainer(packaged defaults) error = %v", err)
	}

	logsRoot := filepath.Join(root, "logs")
	mustMkdir(t, logsRoot, 0o700)
	configurationPath := filepath.Join(nginxRoot, "nginx.conf")
	configuration := mustReadFile(t, configurationPath)
	configuration = strings.Replace(configuration, "user nginx;", "# local integration uses the invoking user", 1)
	configuration = strings.Replace(
		configuration,
		"pid /run/nginx-uix/nginx.pid;",
		"pid "+filepath.Join(root, "nginx.pid")+";",
		1,
	)
	configuration = strings.Replace(
		configuration,
		"listen 80 default_server;",
		"listen 127.0.0.1:"+strconv.Itoa(reserveInitializeLoopbackPort(t))+" default_server;",
		1,
	)
	if err := os.WriteFile(configurationPath, []byte(configuration), 0o644); err != nil {
		t.Fatalf("write isolated Nginx configuration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	arguments := []string{
		"-t",
		"-c", configurationPath,
		"-p", nginxRoot + string(os.PathSeparator),
		"-e", filepath.Join(logsRoot, "error.log"),
	}
	runValidation := func() (string, string, error) {
		command := exec.CommandContext(ctx, resolvedBinary, arguments...)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		return stdout.String(), stderr.String(), err
	}
	stdout, stderr, err := runValidation()
	if err != nil {
		t.Fatalf("valid isolated nginx -t error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	pidPath := filepath.Join(root, "nginx.pid")
	if err := os.Remove(pidPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("remove valid nginx -t PID artifact: %v", err)
	}

	invalidPath := filepath.Join(nginxRoot, "conf.d", "default.conf")
	invalidConfiguration := mustReadFile(t, invalidPath) + "\ninvalid_directive_for_negative_control;\n"
	if err := os.WriteFile(invalidPath, []byte(invalidConfiguration), 0o644); err != nil {
		t.Fatalf("write invalid isolated Nginx configuration: %v", err)
	}
	stdout, stderr, err = runValidation()
	if err == nil {
		t.Fatalf("invalid isolated nginx -t error = nil, stdout = %q, stderr = %q", stdout, stderr)
	}
	if _, statErr := os.Lstat(pidPath); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("isolated nginx -t created PID file or stat failed: %v", statErr)
	}
}

type treeEntrySnapshot struct {
	Mode     fs.FileMode
	Contents string
}

type exactTreeEntrySnapshot struct {
	Mode       fs.FileMode
	Contents   string
	LinkTarget string
	ModTime    time.Time
}

func snapshotTree(t *testing.T, root string) map[string]treeEntrySnapshot {
	t.Helper()
	snapshot := make(map[string]treeEntrySnapshot)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		information, err := os.Lstat(path)
		if err != nil {
			return err
		}
		item := treeEntrySnapshot{Mode: information.Mode()}
		if information.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Contents = string(contents)
		}
		snapshot[relative] = item
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot tree %q: %v", root, err)
	}
	return snapshot
}

func snapshotExactTree(t *testing.T, root string) map[string]exactTreeEntrySnapshot {
	t.Helper()
	snapshot := make(map[string]exactTreeEntrySnapshot)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		information, err := os.Lstat(path)
		if err != nil {
			return err
		}
		item := exactTreeEntrySnapshot{Mode: information.Mode(), ModTime: information.ModTime()}
		switch {
		case information.Mode().IsRegular():
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Contents = string(contents)
		case information.Mode()&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			item.LinkTarget = linkTarget
		}
		snapshot[relative] = item
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot exact tree %q: %v", root, err)
	}
	return snapshot
}

func setTreeModTime(t *testing.T, root string, modTime time.Time) {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink == 0 {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("collect tree paths for Chtimes(%q): %v", root, err)
	}
	for index := len(paths) - 1; index >= 0; index-- {
		if err := os.Chtimes(paths[index], modTime, modTime); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", paths[index], err)
		}
	}
}

func testInitializeOptions(root, defaultsRoot, nginxRoot string) InitializeOptions {
	return InitializeOptions{
		DefaultsRoot:  defaultsRoot,
		NginxRoot:     nginxRoot,
		DataRoot:      filepath.Join(root, "data"),
		WorkspaceRoot: filepath.Join(root, "data", "workspaces"),
		RunRoot:       filepath.Join(root, "run"),
		DataUID:       os.Getuid(),
		DataGID:       os.Getgid(),
	}
}

type testFileOwner struct {
	uid int
	gid int
}

func fileOwner(t *testing.T, path string) testFileOwner {
	t.Helper()
	information, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := information.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat type = %T", information.Sys())
	}
	return testFileOwner{uid: int(stat.Uid), gid: int(stat.Gid)}
}

func assertPathModeAndOwner(t *testing.T, path string, uid, gid int) {
	t.Helper()
	information, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%q) error = %v", path, err)
	}
	if got := information.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode(%q) = %04o, want 0700", path, got)
	}
	assertPathOwner(t, path, information, uid, gid)
}

func assertPathOwner(t *testing.T, path string, information fs.FileInfo, uid, gid int) {
	t.Helper()
	stat, ok := information.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat(%q) type = %T, want *syscall.Stat_t", path, information.Sys())
	}
	if gotUID, gotGID := int(stat.Uid), int(stat.Gid); gotUID != uid || gotGID != gid {
		t.Fatalf("owner(%q) = %d:%d, want %d:%d", path, gotUID, gotGID, uid, gid)
	}
}

func mustMkdir(t *testing.T, path string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%q) error = %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, contents []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%q) error = %v", path, err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(contents)
}

func reserveInitializeLoopbackPort(t *testing.T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		if closeErr := listener.Close(); closeErr != nil {
			t.Errorf("close unexpected listener: %v", closeErr)
		}
		t.Fatalf("reserved address type = %T, want *net.TCPAddr", listener.Addr())
	}
	port := address.Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved loopback port: %v", err)
	}
	return port
}
