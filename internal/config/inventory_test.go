/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBuildInventoryClassifiesClosureAndSafeMetadata(t *testing.T) {
	rootPath, err := os.MkdirTemp("/tmp", "nginx-uix-build-inventory-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(rootPath); err != nil {
			t.Error(err)
		}
	})
	writeFixture(t, rootPath, "nginx.conf", "events {}\nhttp {\n include conf.d/*.conf;\n include snippets/transitive.inc;\n include missing.conf;\n include /opt/external.conf;\n ssl_certificate private/server.key;\n}\n", 0o644)
	writeFixture(t, rootPath, "conf.d/b.conf", "include conf.d/a.conf;\n", 0o640)
	writeFixture(t, rootPath, "conf.d/a.conf", "include conf.d/b.conf;\n", 0o640)
	writeFixture(t, rootPath, "snippets/transitive.inc", "server { listen 8080; }\n", 0o600)
	writeFixture(t, rootPath, "mime.types", "types {}\n", 0o644)
	writeFixture(t, rootPath, "fastcgi_params", "fastcgi_param A B;\n", 0o644)
	writeFixture(t, rootPath, "private/server.key", "-----BEGIN PRIVATE KEY-----\n", 0o600)
	writeFixture(t, rootPath, "notes.txt", "not managed\n", 0o644)
	writeFixture(t, rootPath, "invalid.conf", string([]byte{'a', 0, 'b'}), 0o644)
	if err := os.Symlink("nginx.conf", filepath.Join(rootPath, "internal-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "secret"), filepath.Join(rootPath, "external-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", filepath.Join(rootPath, "broken-link")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(rootPath, "pipe"), 0o600); err != nil {
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
	inventory, err := BuildInventory(context.Background(), root, testSnapshotOptions())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Digest == (Digest{}) || inventory.Digest != inventory.Manifest.Digest() {
		t.Fatal("inventory digest is empty or inconsistent")
	}
	wantClasses := map[RelativePath]EntryClass{
		"nginx.conf":              EntryManagedText,
		"conf.d/a.conf":           EntryManagedText,
		"conf.d/b.conf":           EntryManagedText,
		"snippets/transitive.inc": EntryManagedText,
		"mime.types":              EntryManagedText,
		"fastcgi_params":          EntryManagedText,
		"private/server.key":      EntrySensitiveMaterial,
		"notes.txt":               EntryNotCandidate,
		"invalid.conf":            EntryInvalidText,
		"internal-link":           EntrySymlinkInternal,
		"external-link":           EntrySymlinkExternal,
		"broken-link":             EntrySymlinkUnavailable,
		"pipe":                    EntrySpecialReadOnly,
		"socket":                  EntrySpecialReadOnly,
	}
	for path, want := range wantClasses {
		entry, ok := inventory.Manifest.Entry(path)
		if !ok || entry.Class != want {
			t.Fatalf("entry %q = %#v, %t, want class %q", path, entry, ok, want)
		}
		if want != EntryManagedText && entry.ContentDigest != (Digest{}) {
			t.Fatalf("entry %q exposed content digest", path)
		}
	}
	internal, _ := inventory.Manifest.Entry("internal-link")
	if internal.SafeLinkTarget != "nginx.conf" {
		t.Fatalf("internal link target = %q", internal.SafeLinkTarget)
	}
	statuses := make([]DependencyStatus, 0, len(inventory.Manifest.Dependencies))
	for _, dependency := range inventory.Manifest.Dependencies {
		statuses = append(statuses, dependency.Status)
		if filepath.IsAbs(dependency.DisplayValue) || strings.Contains(dependency.DisplayValue, "/opt/") {
			t.Fatalf("dependency leaked external path: %#v", dependency)
		}
	}
	for _, want := range []DependencyStatus{DependencyResolved, DependencyMissing, DependencyExternal, DependencyCycle} {
		if !slices.Contains(statuses, want) {
			t.Fatalf("dependency statuses %q do not contain %q", statuses, want)
		}
	}
}

func TestBuildInventoryRecognizesAllKnownHelperBasenames(t *testing.T) {
	rootPath := t.TempDir()
	writeFixture(t, rootPath, "nginx.conf", "events {}\n", 0o600)
	for _, name := range []string{"mime.types", "fastcgi_params", "fastcgi.conf", "uwsgi_params", "scgi_params", "koi-win", "koi-utf", "win-utf"} {
		writeFixture(t, rootPath, name, "directive value;\n", 0o640)
	}
	inventory, err := BuildInventory(context.Background(), openTestScopedRoot(t, rootPath), testSnapshotOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range inventory.Manifest.Entries {
		if entry.Type == EntryRegular && entry.Class != EntryManagedText {
			t.Fatalf("helper %q class = %q", entry.Path, entry.Class)
		}
	}
}

func TestBuildInventoryDoesNotScanSensitiveIncludedContent(t *testing.T) {
	rootPath := t.TempDir()
	writeFixture(t, rootPath, "nginx.conf", "include secrets/credential.conf;\n", 0o600)
	writeFixture(t, rootPath, "secrets/credential.conf", "-----BEGIN PRIVATE KEY-----\nsecret bytes\n", 0o600)
	inventory, err := BuildInventory(context.Background(), openTestScopedRoot(t, rootPath), testSnapshotOptions())
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := inventory.Manifest.Entry("secrets/credential.conf")
	if !ok || entry.Class != EntrySensitiveMaterial || entry.ContentDigest != (Digest{}) {
		t.Fatalf("sensitive include = %#v, %t", entry, ok)
	}
}

func TestBuildInventoryExpandsFixtureIncludesInByteOrder(t *testing.T) {
	rootPath := t.TempDir()
	writeFixtureFromFile(t, rootPath, "nginx.conf", "testdata/inventory/nginx.conf", 0o644)
	writeFixtureFromFile(t, rootPath, "conf.d/z.conf", "testdata/inventory/site.conf", 0o640)
	writeFixtureFromFile(t, rootPath, "conf.d/a.conf", "testdata/inventory/site.conf", 0o640)
	writeFixtureFromFile(t, rootPath, "snippets/transitive.inc", "testdata/inventory/transitive.conf", 0o600)
	inventory, err := BuildInventory(context.Background(), openTestScopedRoot(t, rootPath), testSnapshotOptions())
	if err != nil {
		t.Fatal(err)
	}
	var targets []RelativePath
	for _, dependency := range inventory.Manifest.Dependencies {
		if dependency.Source == "nginx.conf" {
			targets = append(targets, dependency.Target)
		}
	}
	if want := []RelativePath{"conf.d/a.conf", "conf.d/z.conf"}; !reflect.DeepEqual(targets, want) {
		t.Fatalf("glob targets = %q, want %q", targets, want)
	}
	entry, ok := inventory.Manifest.Entry("snippets/transitive.inc")
	if !ok || entry.Class != EntryManagedText {
		t.Fatalf("transitive include = %#v, %t", entry, ok)
	}
}

func TestBuildInventoryRejectsInvalidPathUTF8(t *testing.T) {
	rootPath := t.TempDir()
	writeFixture(t, rootPath, "nginx.conf", "events {}\n", 0o600)
	invalidName := string([]byte{'b', 'a', 'd', 0xff})
	if err := os.WriteFile(filepath.Join(rootPath, invalidName), []byte("value"), 0o600); err != nil {
		if errors.Is(err, unix.EILSEQ) {
			t.Skip("filesystem rejects invalid UTF-8 names before inventory")
		}
		t.Fatal(err)
	}
	_, err := BuildInventory(context.Background(), openTestScopedRoot(t, rootPath), testSnapshotOptions())
	if !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("BuildInventory() error = %v, want ErrPathInvalid", err)
	}
}

func TestBuildInventoryRejects4097Entries(t *testing.T) {
	rootPath := t.TempDir()
	for index := range DefaultLimits().MaxEntries + 1 {
		if err := os.WriteFile(filepath.Join(rootPath, inventoryName(index)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := BuildInventory(context.Background(), openTestScopedRoot(t, rootPath), testSnapshotOptions())
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("BuildInventory() error = %v, want ErrLimitExceeded", err)
	}
}

func TestBuildInventoryRejectsManagedBytesOver32MiB(t *testing.T) {
	rootPath := t.TempDir()
	block := bytesOf('a', int(DefaultLimits().MaxFileBytes))
	for index := range 16 {
		writeFixtureBytes(t, rootPath, "managed-"+inventoryName(index)+".conf", block, 0o600)
	}
	writeFixture(t, rootPath, "nginx.conf", "events {}\n", 0o600)
	_, err := BuildInventory(context.Background(), openTestScopedRoot(t, rootPath), testSnapshotOptions())
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("BuildInventory() error = %v, want ErrLimitExceeded", err)
	}
}

func TestDigestRootMatchesSingleInventory(t *testing.T) {
	rootPath := t.TempDir()
	writeFixture(t, rootPath, "nginx.conf", "events {}\n", 0o640)
	root := openTestScopedRoot(t, rootPath)
	options := testSnapshotOptions()
	inventory, err := BuildInventory(context.Background(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	state, err := DigestRoot(context.Background(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	want := ProductionState{Digest: inventory.Digest, ManifestVersion: ManifestSchemaVersion, EntryCount: inventory.Manifest.EntryCount, ManagedBytes: inventory.Manifest.ManagedBytes}
	if state != want {
		t.Fatalf("DigestRoot() = %#v, want %#v", state, want)
	}
}

func TestSnapshotToCopiesOnlyManagedTextWithoutHardlinks(t *testing.T) {
	sourcePath := t.TempDir()
	targetPath := t.TempDir()
	writeFixture(t, sourcePath, "nginx.conf", "events {}\nhttp { include conf.d/*.conf; }\n", 0o644)
	writeFixture(t, sourcePath, "conf.d/site.conf", "server { listen 8080; }\n", 0o640)
	writeFixture(t, sourcePath, "private/server.key", "-----BEGIN PRIVATE KEY-----\n", 0o600)
	source := openTestScopedRoot(t, sourcePath)
	target := openTestScopedRoot(t, targetPath)

	snapshot, err := SnapshotTo(context.Background(), source, target, testSnapshotOptions())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProductionDigest == (Digest{}) || snapshot.BaseDigest == (Digest{}) {
		t.Fatal("snapshot digests are empty")
	}
	if _, err := os.Stat(filepath.Join(targetPath, "private/server.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sensitive content was copied")
	}
	assertDifferentInode(t, filepath.Join(sourcePath, "nginx.conf"), filepath.Join(targetPath, "nginx.conf"))
	info, err := os.Stat(filepath.Join(targetPath, "conf.d/site.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("snapshot mode = %o, want 0400", info.Mode().Perm())
	}
}

func TestSnapshotToCopiesTransitiveIncludeAndIndependentlyDigestsTarget(t *testing.T) {
	sourcePath := t.TempDir()
	targetPath := t.TempDir()
	writeFixture(t, sourcePath, "nginx.conf", "include snippets/site.inc;\n", 0o600)
	writeFixture(t, sourcePath, "snippets/site.inc", "events {}\n", 0o600)
	source := openTestScopedRoot(t, sourcePath)
	target := openTestScopedRoot(t, targetPath)
	snapshot, err := SnapshotTo(context.Background(), source, target, testSnapshotOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(targetPath, "snippets/site.inc")); err != nil {
		t.Fatal(err)
	}
	if snapshot.BaseDigest != snapshot.ProductionDigest {
		t.Fatalf("base digest = %s, production digest = %s", snapshot.BaseDigest, snapshot.ProductionDigest)
	}
}

func TestSnapshotToRejectsSourceChangeAndCleansStage(t *testing.T) {
	sourcePath := t.TempDir()
	targetPath := t.TempDir()
	writeFixture(t, sourcePath, "nginx.conf", "events {}\n", 0o600)
	source := openTestScopedRoot(t, sourcePath)
	target := openTestScopedRoot(t, targetPath)
	mutated := false
	target.fsync = func(int) error {
		if !mutated {
			mutated = true
			return os.WriteFile(filepath.Join(sourcePath, "nginx.conf"), []byte("events { worker_connections 8; }\n"), 0o600)
		}
		return nil
	}
	_, err := SnapshotTo(context.Background(), source, target, testSnapshotOptions())
	if !errors.Is(err, ErrSnapshotChanged) {
		t.Fatalf("SnapshotTo() error = %v, want ErrSnapshotChanged", err)
	}
	assertDirectoryEmpty(t, targetPath)
}

func TestSnapshotToHonorsCancellationAndCleansStage(t *testing.T) {
	sourcePath := t.TempDir()
	targetPath := t.TempDir()
	writeFixture(t, sourcePath, "nginx.conf", "include site.conf;\n", 0o600)
	writeFixture(t, sourcePath, "site.conf", "server {}\n", 0o600)
	source := openTestScopedRoot(t, sourcePath)
	target := openTestScopedRoot(t, targetPath)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	target.fsync = func(int) error {
		calls++
		if calls == 1 {
			cancel()
		}
		return nil
	}
	_, err := SnapshotTo(ctx, source, target, testSnapshotOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SnapshotTo() error = %v, want context.Canceled", err)
	}
	assertDirectoryEmpty(t, targetPath)
}

func TestSnapshotToRejectsExistingTargetWithoutDeletingIt(t *testing.T) {
	sourcePath := t.TempDir()
	targetPath := t.TempDir()
	writeFixture(t, sourcePath, "nginx.conf", "events {}\n", 0o600)
	writeFixture(t, targetPath, "sentinel", "keep", 0o600)
	_, err := SnapshotTo(context.Background(), openTestScopedRoot(t, sourcePath), openTestScopedRoot(t, targetPath), testSnapshotOptions())
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("SnapshotTo() error = %v, want fs.ErrExist", err)
	}
	content, readErr := os.ReadFile(filepath.Join(targetPath, "sentinel"))
	if readErr != nil || string(content) != "keep" {
		t.Fatalf("sentinel = %q, %v", content, readErr)
	}
}

func TestSnapshotToReportsFsyncFailureAndCleansStage(t *testing.T) {
	sourcePath := t.TempDir()
	targetPath := t.TempDir()
	writeFixture(t, sourcePath, "nginx.conf", "include conf.d/site.conf;\n", 0o600)
	writeFixture(t, sourcePath, "conf.d/site.conf", "server {}\n", 0o600)
	target := openTestScopedRoot(t, targetPath)
	injected := errors.New("injected fsync failure")
	failed := false
	target.fsync = func(int) error {
		if !failed {
			failed = true
			return injected
		}
		return nil
	}
	_, err := SnapshotTo(context.Background(), openTestScopedRoot(t, sourcePath), target, testSnapshotOptions())
	if !errors.Is(err, injected) {
		t.Fatalf("SnapshotTo() error = %v, want injected error", err)
	}
	assertDirectoryEmpty(t, targetPath)
}

func TestSnapshotToAppliesOwnershipToCreatedDescriptor(t *testing.T) {
	sourcePath := t.TempDir()
	targetPath := t.TempDir()
	writeFixture(t, sourcePath, "nginx.conf", "events {}\n", 0o600)
	options := testSnapshotOptions()
	options.Owner = &Owner{UID: os.Getuid(), GID: os.Getgid()}
	if _, err := SnapshotTo(context.Background(), openTestScopedRoot(t, sourcePath), openTestScopedRoot(t, targetPath), options); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(targetPath, "nginx.conf"))
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != options.Owner.UID || int(stat.Gid) != options.Owner.GID {
		t.Fatalf("snapshot owner = %#v", info.Sys())
	}
}

func TestSnapshotToAppliesExactModesUnderRestrictiveUmask(t *testing.T) {
	sourcePath := t.TempDir()
	targetPath := t.TempDir()
	writeFixture(t, sourcePath, "nginx.conf", "include conf.d/site.conf;\n", 0o600)
	writeFixture(t, sourcePath, "conf.d/site.conf", "server {}\n", 0o600)
	source := openTestScopedRoot(t, sourcePath)
	target := openTestScopedRoot(t, targetPath)
	previous := unix.Umask(0o777)
	t.Cleanup(func() { unix.Umask(previous) })
	if _, err := SnapshotTo(context.Background(), source, target, testSnapshotOptions()); err != nil {
		t.Fatal(err)
	}
	for relative, want := range map[string]fs.FileMode{"conf.d": 0o700, "nginx.conf": 0o400, "conf.d/site.conf": 0o400} {
		info, err := os.Stat(filepath.Join(targetPath, relative))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("mode %q = %o, want %o", relative, info.Mode().Perm(), want)
		}
	}
}

func testSnapshotOptions() SnapshotOptions {
	return SnapshotOptions{
		Entry:         "nginx.conf",
		Limits:        DefaultLimits(),
		Policy:        NewPolicy(),
		FileMode:      0o400,
		DirectoryMode: 0o700,
	}
}

func writeFixture(t *testing.T, rootPath, relative, content string, mode fs.FileMode) {
	t.Helper()
	writeFixtureBytes(t, rootPath, relative, []byte(content), mode)
}

func writeFixtureBytes(t *testing.T, rootPath, relative string, content []byte, mode fs.FileMode) {
	t.Helper()
	path := filepath.Join(rootPath, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func writeFixtureFromFile(t *testing.T, rootPath, relative, fixture string, mode fs.FileMode) {
	t.Helper()
	content, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureBytes(t, rootPath, relative, content, mode)
}

func assertDifferentInode(t *testing.T, left, right string) {
	t.Helper()
	leftInfo, err := os.Stat(left)
	if err != nil {
		t.Fatal(err)
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(leftInfo, rightInfo) {
		t.Fatal("snapshot file is a hardlink")
	}
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging directory contains %v", entries)
	}
}

func inventoryName(index int) string {
	const digits = "0123456789abcdef"
	name := []byte("entry-0000")
	for position := len(name) - 1; position >= len("entry-"); position-- {
		name[position] = digits[index&15]
		index >>= 4
	}
	return string(name)
}

func bytesOf(value byte, count int) []byte {
	content := make([]byte, count)
	for index := range content {
		content[index] = value
	}
	return content
}
