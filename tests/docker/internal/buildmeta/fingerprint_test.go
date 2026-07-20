/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
package buildmeta

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSourceFingerprintTracksCanonicalFilesystemInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "regular file contents",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "internal/app.go", "package changed\n", 0o644)
			},
		},
		{
			name: "relative path",
			mutate: func(t *testing.T, root string) {
				if err := os.Rename(filepath.Join(root, "internal/app.go"), filepath.Join(root, "internal/renamed.go")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "executable bit",
			mutate: func(t *testing.T, root string) {
				if err := os.Chmod(filepath.Join(root, "internal/app.go"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "regular to symlink type",
			mutate: func(t *testing.T, root string) {
				path := filepath.Join(root, "internal/app.go")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("other.go", path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink target",
			mutate: func(t *testing.T, root string) {
				path := filepath.Join(root, "current")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("internal/other.go", path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "empty directory",
			mutate: func(t *testing.T, root string) {
				if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newContextFixture(t)
			first := sourceDigest(t, root)
			test.mutate(t, root)
			second := sourceDigest(t, root)
			if first == second {
				t.Fatalf("%s change did not change source fingerprint", test.name)
			}
		})
	}
}

func TestSourceFingerprintUsesLstatAndDoesNotFollowSymlinkDirectories(t *testing.T) {
	root := newContextFixture(t)
	external := t.TempDir()
	writeFixtureFile(t, external, "secret", "first", 0o644)
	if err := os.Symlink(external, filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	first := sourceDigest(t, root)

	writeFixtureFile(t, external, "secret", "second", 0o644)
	second := sourceDigest(t, root)
	if first != second {
		t.Fatal("content below a symlink directory changed the source fingerprint")
	}
}

func TestSourceFingerprintIgnoresMtime(t *testing.T) {
	root := newContextFixture(t)
	first := sourceDigest(t, root)
	path := filepath.Join(root, "internal/app.go")
	changed := time.Unix(2_000_000_000, 0)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	second := sourceDigest(t, root)
	if first != second {
		t.Fatal("mtime changed the source fingerprint")
	}
}

func TestSourceFingerprintSortsSlashPathsByRawUTF8Bytes(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".dockerignore", "", 0o644)
	writeFixtureFile(t, root, "é", "accent", 0o644)
	writeFixtureFile(t, root, "z", "zee", 0o644)
	writeFixtureFile(t, root, "a", "aye", 0o644)

	got := sourceDigest(t, root)
	want := canonicalSourceDigest([]canonicalEntry{
		{path: ".dockerignore", kind: 'f', payload: []byte{}},
		{path: "a", kind: 'f', payload: []byte("aye")},
		{path: "z", kind: 'f', payload: []byte("zee")},
		{path: "é", kind: 'f', payload: []byte("accent")},
	})
	if got != want {
		t.Fatalf("source fingerprint did not use canonical raw-byte order: got %s want %s", got, want)
	}
}

func TestSourceFingerprintPrunesLiteralIgnoredPrefixes(t *testing.T) {
	root := newContextFixture(t)
	writeFixtureFile(t, root, ".tmp/excluded", "first", 0o644)
	writeFixtureFile(t, root, ".superpowers/excluded", "first", 0o644)
	writeFixtureFile(t, root, "node_modules/excluded", "first", 0o644)
	writeFixtureFile(t, root, ".tmp2/included", "first", 0o644)
	baseline := sourceDigest(t, root)

	writeFixtureFile(t, root, ".tmp/excluded", "second", 0o755)
	writeFixtureFile(t, root, ".superpowers/excluded", "second", 0o755)
	writeFixtureFile(t, root, "node_modules/excluded", "second", 0o755)
	if got := sourceDigest(t, root); got != baseline {
		t.Fatal("an ignored literal prefix changed the source fingerprint")
	}

	writeFixtureFile(t, root, ".tmp2/included", "second", 0o644)
	if got := sourceDigest(t, root); got == baseline {
		t.Fatal("literal prefix matching incorrectly ignored .tmp2")
	}
}

func TestSourceFingerprintAlwaysIncludesIgnoreFileBytes(t *testing.T) {
	root := newContextFixture(t)
	first := sourceDigest(t, root)
	writeFixtureFile(t, root, ".dockerignore", ".tmp\n.superpowers\nnode_modules\n# changed\n", 0o644)
	second := sourceDigest(t, root)
	if first == second {
		t.Fatal(".dockerignore bytes did not change the source fingerprint")
	}
}

func TestSourceFingerprintRejectsUnknownIgnoreSyntax(t *testing.T) {
	for _, rule := range []string{"!keep", "*.tmp", "file?", "[abc]", "foo/**", "/absolute"} {
		t.Run(rule, func(t *testing.T) {
			root := newContextFixture(t)
			writeFixtureFile(t, root, ".dockerignore", rule+"\n", 0o644)
			_, err := SourceFingerprint(context.Background(), SourceOptions{Root: root, IgnoreFile: ".dockerignore"})
			if err == nil || !strings.Contains(err.Error(), "unsupported ignore rule") {
				t.Fatalf("SourceFingerprint() error = %v, want unsupported ignore rule", err)
			}
			if strings.Contains(err.Error(), root) {
				t.Fatalf("error leaked absolute root: %v", err)
			}
		})
	}
}

func TestSourceFingerprintRejectsInvalidUTF8Path(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("this filesystem cannot represent an invalid UTF-8 fixture")
	}
	root := newContextFixture(t)
	invalid := string([]byte{'b', 'a', 'd', 0xff})
	if err := os.WriteFile(filepath.Join(root, invalid), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := SourceFingerprint(context.Background(), SourceOptions{Root: root, IgnoreFile: ".dockerignore"})
	if err == nil || !strings.Contains(err.Error(), "invalid UTF-8 path") {
		t.Fatalf("SourceFingerprint() error = %v, want invalid UTF-8 path", err)
	}
}

func TestValidateSourceRelativePathRejectsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{'b', 'a', 'd', 0xff})
	if err := validateSourceRelativePath(invalid); err == nil || !strings.Contains(err.Error(), "invalid UTF-8 path") {
		t.Fatalf("validateSourceRelativePath() error = %v, want invalid UTF-8 path", err)
	}
}

func TestSourceFingerprintHonorsCancellation(t *testing.T) {
	root := newContextFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := SourceFingerprint(ctx, SourceOptions{Root: root, IgnoreFile: ".dockerignore"})
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("SourceFingerprint() error = %v, want context canceled", err)
	}
}

func TestVerifyDockerfileCoverageAcceptsCurrentDockerfiles(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	options := SourceOptions{Root: root, IgnoreFile: ".dockerignore"}
	for _, dockerfile := range []string{"deploy/docker/Dockerfile", "deploy/docker/Playwright.Dockerfile"} {
		if err := VerifyDockerfileCoverage(context.Background(), options, dockerfile); err != nil {
			t.Fatalf("VerifyDockerfileCoverage(%q): %v", dockerfile, err)
		}
	}
}

func TestVerifyDockerfileCoverageRejectsIgnoredAbsentAndUnrecognizedSources(t *testing.T) {
	tests := []struct {
		name       string
		ignore     string
		dockerfile string
		files      map[string]string
	}{
		{
			name:       "ignored source",
			ignore:     "ignored\n",
			dockerfile: "FROM scratch\nCOPY ignored /target\n",
			files:      map[string]string{"ignored/file": "content"},
		},
		{
			name:       "absent source",
			dockerfile: "FROM scratch\nCOPY absent /target\n",
		},
		{
			name:       "JSON copy",
			dockerfile: "FROM scratch\nCOPY [\"file\", \"/target\"]\n",
			files:      map[string]string{"file": "content"},
		},
		{
			name:       "variable source",
			dockerfile: "FROM scratch\nCOPY $SOURCE /target\n",
			files:      map[string]string{"file": "content"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, ".dockerignore", test.ignore, 0o644)
			writeFixtureFile(t, root, "Dockerfile", test.dockerfile, 0o644)
			for path, content := range test.files {
				writeFixtureFile(t, root, path, content, 0o644)
			}
			err := VerifyDockerfileCoverage(
				context.Background(),
				SourceOptions{Root: root, IgnoreFile: ".dockerignore"},
				"Dockerfile",
			)
			if err == nil {
				t.Fatal("VerifyDockerfileCoverage() succeeded, want failure")
			}
			if strings.Contains(err.Error(), root) {
				t.Fatalf("error leaked absolute root: %v", err)
			}
		})
	}
}

func TestVerifyDockerfileCoverageSkipsRemoteAddAndStageCopy(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".dockerignore", "", 0o644)
	writeFixtureFile(t, root, "Dockerfile", `FROM scratch AS build
ADD https://example.invalid/archive.tar /tmp/archive.tar
COPY --from=build /out/binary /binary
`, 0o644)
	if err := VerifyDockerfileCoverage(
		context.Background(),
		SourceOptions{Root: root, IgnoreFile: ".dockerignore"},
		"Dockerfile",
	); err != nil {
		t.Fatal(err)
	}
}

func TestBuildIdentityIsCanonicalAndTracksOutputInputs(t *testing.T) {
	input := validIdentityInput()
	first := identityDigest(t, input)

	reordered := validIdentityInput()
	reordered.BaseDigests = map[string]string{
		"nginx": digestText('3'),
		"go":    digestText('1'),
		"node":  digestText('2'),
	}
	reordered.BuildArgs = map[string]string{
		"SOURCE_DATE_EPOCH": "1770000000",
		"BUILD_TIME":        "2026-07-17T00:00:00Z",
		"COMMIT":            "0123456789abcdef",
		"VERSION":           "0.2.1",
	}
	if got := identityDigest(t, reordered); got != first {
		t.Fatal("map insertion order changed build identity")
	}

	arm := validIdentityInput()
	arm.Platform = "linux/arm64"
	if got := identityDigest(t, arm); got == first {
		t.Fatal("platform did not change build identity")
	}

	version := validIdentityInput()
	version.BuildArgs["VERSION"] = "0.2.2"
	if got := identityDigest(t, version); got == first {
		t.Fatal("VERSION did not change build identity")
	}

	playwright := validIdentityInput()
	playwright.BaseDigests["playwright"] = digestText('4')
	if got := identityDigest(t, playwright); got == first {
		t.Fatal("Playwright base digest did not change build identity")
	}
}

func TestBuildIdentityExcludesProxyAndBuildEnvironmentValues(t *testing.T) {
	baseline := identityDigest(t, validIdentityInput())
	withEnvironment := validIdentityInput()
	for key, value := range map[string]string{
		"HTTP_PROXY":        "http://secret.invalid",
		"https_proxy":       "https://other.invalid",
		"NO_PROXY":          "localhost",
		"ALL_PROXY":         "socks5://proxy.invalid",
		"BUILDKIT_PROGRESS": "plain",
		"BUILDX_BUILDER":    "developer-machine",
		"DOCKER_CONFIG":     "/home/developer/.docker",
	} {
		withEnvironment.BuildArgs[key] = value
	}
	if got := identityDigest(t, withEnvironment); got != baseline {
		t.Fatal("proxy or build-environment value changed build identity")
	}

	outputArg := validIdentityInput()
	outputArg.BuildArgs["FEATURE_SET"] = "strict"
	if got := identityDigest(t, outputArg); got == baseline {
		t.Fatal("proxy-independent build argument did not change build identity")
	}
}

func TestBuildIdentityRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*IdentityInput)
	}{
		{name: "zero source", mutate: func(input *IdentityInput) { input.SourceFingerprint = Digest{} }},
		{name: "platform", mutate: func(input *IdentityInput) { input.Platform = "linux/ppc64le" }},
		{name: "missing base", mutate: func(input *IdentityInput) { delete(input.BaseDigests, "go") }},
		{name: "unknown base", mutate: func(input *IdentityInput) { input.BaseDigests["unknown"] = digestText('4') }},
		{name: "uppercase base digest", mutate: func(input *IdentityInput) { input.BaseDigests["go"] = strings.Repeat("A", 64) }},
		{name: "missing version", mutate: func(input *IdentityInput) { delete(input.BuildArgs, "VERSION") }},
		{name: "empty commit", mutate: func(input *IdentityInput) { input.BuildArgs["COMMIT"] = "" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validIdentityInput()
			test.mutate(&input)
			if _, err := BuildIdentity(input); err == nil {
				t.Fatal("BuildIdentity() succeeded, want failure")
			}
		})
	}
}

func TestDigestStringIsStableLowerHex(t *testing.T) {
	digest := sha256.Sum256([]byte("nginx-uix"))
	if got, want := Digest(digest).String(), "fe2b63093d9a4b60146c6a1c674acc9a98ef5dbea3d4946304cdc1bb92bd0eee"; got != want {
		t.Fatalf("Digest.String() = %q, want %q", got, want)
	}
}

type canonicalEntry struct {
	path       string
	kind       byte
	executable bool
	payload    []byte
}

func canonicalSourceDigest(entries []canonicalEntry) Digest {
	hash := sha256.New()
	hash.Write([]byte(SourceSchema))
	var size [8]byte
	for _, entry := range entries {
		binary.BigEndian.PutUint64(size[:], uint64(len(entry.path)))
		hash.Write(size[:])
		hash.Write([]byte(entry.path))
		hash.Write([]byte{entry.kind})
		if entry.kind == 'f' {
			if entry.executable {
				hash.Write([]byte{1})
			} else {
				hash.Write([]byte{0})
			}
		}
		binary.BigEndian.PutUint64(size[:], uint64(len(entry.payload)))
		hash.Write(size[:])
		hash.Write(entry.payload)
	}
	var result Digest
	copy(result[:], hash.Sum(nil))
	return result
}

func newContextFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, ".dockerignore", ".tmp\n.superpowers\nnode_modules\n", 0o644)
	writeFixtureFile(t, root, "internal/app.go", "package internal\n", 0o644)
	writeFixtureFile(t, root, "internal/other.go", "package internal\n", 0o644)
	if err := os.Symlink("internal/app.go", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFixtureFile(t *testing.T, root, relative, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func sourceDigest(t *testing.T, root string) Digest {
	t.Helper()
	digest, err := SourceFingerprint(context.Background(), SourceOptions{Root: root, IgnoreFile: ".dockerignore"})
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func identityDigest(t *testing.T, input IdentityInput) Digest {
	t.Helper()
	digest, err := BuildIdentity(input)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func validIdentityInput() IdentityInput {
	source := sha256.Sum256([]byte("source"))
	return IdentityInput{
		SourceFingerprint: Digest(source),
		Platform:          "linux/amd64",
		BaseDigests: map[string]string{
			"go":    digestText('1'),
			"node":  digestText('2'),
			"nginx": digestText('3'),
		},
		BuildArgs: map[string]string{
			"VERSION":           "0.2.1",
			"COMMIT":            "0123456789abcdef",
			"BUILD_TIME":        "2026-07-17T00:00:00Z",
			"SOURCE_DATE_EPOCH": "1770000000",
		},
	}
}

func digestText(value byte) string {
	return strings.Repeat(string(value), 64)
}
