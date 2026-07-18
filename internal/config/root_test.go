/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestScopedRootRejectsRootSymlink(t *testing.T) {
	rootPath := t.TempDir()
	linkPath := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(rootPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenScopedRoot(linkPath); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("OpenScopedRoot() error = %v, want ErrPathInvalid", err)
	}
}

func TestScopedRootRejectsEverySymlinkComponent(t *testing.T) {
	rootPath := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.conf"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "inside.conf"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.conf", filepath.Join(rootPath, "inside-link.conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing.conf", filepath.Join(rootPath, "broken.conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop.conf", filepath.Join(rootPath, "loop.conf")); err != nil {
		t.Fatal(err)
	}

	root := openTestScopedRoot(t, rootPath)
	tests := []string{"linked/secret.conf", "inside-link.conf", "broken.conf", "loop.conf"}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			path := mustRelativePath(t, raw)
			if _, _, err := root.ReadRegular(context.Background(), path, 2<<20); !errors.Is(err, ErrPathInvalid) {
				t.Fatalf("ReadRegular() error = %v, want ErrPathInvalid", err)
			}
		})
	}
}

func TestScopedRootRejectsSpecialAndDirectoryFiles(t *testing.T) {
	rootPath, err := os.MkdirTemp("/tmp", "nginx-uix-root-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(rootPath); err != nil {
			t.Error(err)
		}
	})
	if err := os.Mkdir(filepath.Join(rootPath, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(rootPath, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(rootPath, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})

	root := openTestScopedRoot(t, rootPath)
	for _, raw := range []string{"directory", "fifo", "socket"} {
		t.Run(raw, func(t *testing.T) {
			if _, _, err := root.ReadRegular(context.Background(), mustRelativePath(t, raw), 2<<20); !errors.Is(err, ErrPathInvalid) {
				t.Fatalf("ReadRegular() error = %v, want ErrPathInvalid", err)
			}
		})
	}
}

func TestScopedRootDoesNotTrustPriorLstat(t *testing.T) {
	rootPath := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "victim.conf"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.conf"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := openTestScopedRoot(t, rootPath)
	path := mustRelativePath(t, "victim.conf")
	if _, err := root.Lstat(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(rootPath, "victim.conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.conf"), filepath.Join(rootPath, "victim.conf")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := root.ReadRegular(context.Background(), path, 2<<20); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("ReadRegular() error = %v, want ErrPathInvalid", err)
	}
}

func TestScopedRootHonorsContextCancellation(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "nginx.conf"), []byte("events {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := openTestScopedRoot(t, rootPath)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := root.Lstat(ctx, mustRelativePath(t, "nginx.conf")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Lstat() error = %v, want context.Canceled", err)
	}
}

func TestScopedRootCreateRegularIsExclusive(t *testing.T) {
	rootPath := t.TempDir()
	root := openTestScopedRoot(t, rootPath)
	if err := root.EnsureDirectory(context.Background(), mustRelativePath(t, "conf.d"), 0o750); err != nil {
		t.Fatal(err)
	}
	path := mustRelativePath(t, "conf.d/site.conf")
	if err := root.CreateRegular(context.Background(), path, []byte("server {}"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := root.CreateRegular(context.Background(), path, []byte("replace"), 0o600); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("CreateRegular(existing) error = %v, want fs.ErrExist", err)
	}
	content, info, err := root.ReadRegular(context.Background(), path, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "server {}" || info.Mode().Perm() != 0o640 {
		t.Fatalf("ReadRegular() = %q, mode %o", content, info.Mode().Perm())
	}
}

func TestScopedRootEnsureDirectoryDoesNotLeakDescriptors(t *testing.T) {
	root := openTestScopedRoot(t, t.TempDir())
	before := countOpenDescriptors(t)
	for index := range 32 {
		path := mustRelativePath(t, fmt.Sprintf("directory-%02d", index))
		if err := root.EnsureDirectory(context.Background(), path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	after := countOpenDescriptors(t)
	if after > before+2 {
		t.Fatalf("EnsureDirectory() leaked descriptors: before = %d, after = %d", before, after)
	}
}

func TestScopedRootAtomicReplace(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "nginx.conf"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := openTestScopedRoot(t, rootPath)
	path := mustRelativePath(t, "nginx.conf")
	if err := root.AtomicReplace(context.Background(), path, []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}
	content, info, err := root.ReadRegular(context.Background(), path, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" || info.Mode().Perm() != 0o640 {
		t.Fatalf("ReadRegular() = %q, mode %o", content, info.Mode().Perm())
	}
	assertNoTemporaryFiles(t, rootPath)
}

func TestScopedRootRemoveRegular(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "obsolete.conf"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(rootPath, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := openTestScopedRoot(t, rootPath)
	if err := root.RemoveRegular(context.Background(), mustRelativePath(t, "obsolete.conf")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "obsolete.conf")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(removed) error = %v, want fs.ErrNotExist", err)
	}
	if err := root.RemoveRegular(context.Background(), mustRelativePath(t, "directory")); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("RemoveRegular(directory) error = %v, want ErrPathInvalid", err)
	}
}

func TestScopedRootAtomicReplaceReportsFsyncErrors(t *testing.T) {
	tests := []struct {
		name           string
		failCall       int
		wantContent    string
		wantTargetMode fs.FileMode
	}{
		{name: "temporary file sync", failCall: 1, wantContent: "old", wantTargetMode: 0o600},
		{name: "parent directory sync", failCall: 2, wantContent: "new", wantTargetMode: 0o640},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootPath := t.TempDir()
			if err := os.WriteFile(filepath.Join(rootPath, "nginx.conf"), []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			root := openTestScopedRoot(t, rootPath)
			injected := errors.New("injected fsync failure")
			calls := 0
			root.fsync = func(int) error {
				calls++
				if calls == test.failCall {
					return injected
				}
				return nil
			}
			err := root.AtomicReplace(context.Background(), mustRelativePath(t, "nginx.conf"), []byte("new"), 0o640)
			if !errors.Is(err, injected) {
				t.Fatalf("AtomicReplace() error = %v, want injected error", err)
			}
			content, info, readErr := root.ReadRegular(context.Background(), mustRelativePath(t, "nginx.conf"), 2<<20)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(content) != test.wantContent || info.Mode().Perm() != test.wantTargetMode {
				t.Fatalf("ReadRegular() = %q, mode %o", content, info.Mode().Perm())
			}
			assertNoTemporaryFiles(t, rootPath)
		})
	}
}

func TestScopedRootWalkIsBoundedAndSorted(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "conf.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"nginx.conf":    "events {}",
		"conf.d/b.conf": "server {}",
		"conf.d/a.conf": "server {}",
	} {
		if err := os.WriteFile(filepath.Join(rootPath, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := openTestScopedRoot(t, rootPath)
	entries, err := root.Walk(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, string(entry.Path))
	}
	if want := []string{"conf.d", "conf.d/a.conf", "conf.d/b.conf", "nginx.conf"}; !slices.Equal(paths, want) {
		t.Fatalf("Walk() paths = %q, want %q", paths, want)
	}
	if _, err := root.Walk(context.Background(), 3); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Walk(limit) error = %v, want ErrLimitExceeded", err)
	}
}

func TestScopedRootWalkClassifiesSymlinksAndSpecialEntries(t *testing.T) {
	rootPath, err := os.MkdirTemp("/tmp", "nginx-uix-inventory-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(rootPath); err != nil {
			t.Error(err)
		}
	})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "inside.conf"), []byte("events {}"), 0o640); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"internal": "inside.conf",
		"external": filepath.Join(outside, "secret.conf"),
		"broken":   "missing.conf",
		"loop":     "loop",
	} {
		if err := os.Symlink(target, filepath.Join(rootPath, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mkfifo(filepath.Join(rootPath, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(rootPath, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})

	root := openTestScopedRoot(t, rootPath)
	entries, err := root.Walk(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[RelativePath]RawEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	for _, path := range []RelativePath{"fifo", "socket"} {
		if entry := byPath[path]; entry.Type != EntrySpecial || entry.LinkClass != EntrySpecialReadOnly {
			t.Fatalf("entry %q = %#v, want special", path, entry)
		}
	}
	wantLinks := map[RelativePath]RawEntry{
		"internal": {Path: "internal", Type: EntrySymlink, SafeLinkTarget: "inside.conf", LinkClass: EntrySymlinkInternal},
		"external": {Path: "external", Type: EntrySymlink, LinkClass: EntrySymlinkExternal},
		"broken":   {Path: "broken", Type: EntrySymlink, LinkClass: EntrySymlinkUnavailable},
		"loop":     {Path: "loop", Type: EntrySymlink, LinkClass: EntrySymlinkUnavailable},
	}
	for path, want := range wantLinks {
		got := byPath[path]
		if got.Type != want.Type || got.SafeLinkTarget != want.SafeLinkTarget || got.LinkClass != want.LinkClass {
			t.Fatalf("entry %q = %#v, want %#v", path, got, want)
		}
	}
}

func TestScopedRootRechecksFilesystemNameMax(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "abcde"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := openTestScopedRoot(t, rootPath)
	root.nameMax = 4
	if _, _, err := root.ReadRegular(context.Background(), mustRelativePath(t, "abcde"), 2<<20); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("ReadRegular() error = %v, want ErrPathInvalid", err)
	}
}

func TestDescriptorHelpersRejectInvalidValues(t *testing.T) {
	if _, err := fileFromDescriptor(-1, "invalid"); err == nil {
		t.Fatal("fileFromDescriptor(-1) error = nil")
	}
	if _, err := descriptorFromFile(nil); err == nil {
		t.Fatal("descriptorFromFile(nil) error = nil")
	}
	if err := closeDescriptor("close invalid descriptor", -1); err == nil {
		t.Fatal("closeDescriptor(-1) error = nil")
	}
}

func openTestScopedRoot(t *testing.T, path string) *ScopedRoot {
	t.Helper()
	root, err := OpenScopedRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root
}

func mustRelativePath(t *testing.T, raw string) RelativePath {
	t.Helper()
	path, err := ParseRelativePath(raw, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertNoTemporaryFiles(t *testing.T, rootPath string) {
	t.Helper()
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".nginx-uix-") {
			t.Errorf("temporary file %q was not cleaned up", entry.Name())
		}
	}
}

func countOpenDescriptors(t *testing.T) int {
	t.Helper()
	directory, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	entries, readErr := directory.Readdirnames(-1)
	closeErr := directory.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return len(entries)
}
