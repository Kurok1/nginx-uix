/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
package buildmeta

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestV1ReleaseMetadataIsSynchronized(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	const want = "1.1.0"
	const wantLicense = "Apache-2.0"
	const wantLicenseDigest = "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4"

	versionPayload, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatalf("ReadFile(VERSION) error = %v", err)
	}
	if got := strings.TrimSpace(string(versionPayload)); got != want {
		t.Errorf("VERSION = %q, want %q", got, want)
	}

	for _, relative := range []string{"web/package.json", "web/package-lock.json"} {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Errorf("ReadFile(%s) error = %v", relative, err)
			continue
		}
		var metadata struct {
			Version  string `json:"version"`
			License  string `json:"license"`
			Packages map[string]struct {
				Version string `json:"version"`
				License string `json:"license"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(payload, &metadata); err != nil {
			t.Errorf("json.Unmarshal(%s) error = %v", relative, err)
			continue
		}
		if metadata.Version != want {
			t.Errorf("%s version = %q, want %q", relative, metadata.Version, want)
		}
		if len(metadata.Packages) == 0 && metadata.License != wantLicense {
			t.Errorf("%s license = %q, want %q", relative, metadata.License, wantLicense)
		}
		if rootPackage, ok := metadata.Packages[""]; ok {
			if rootPackage.Version != want {
				t.Errorf("%s root package version = %q, want %q", relative, rootPackage.Version, want)
			}
			if rootPackage.License != wantLicense {
				t.Errorf("%s root package license = %q, want %q", relative, rootPackage.License, wantLicense)
			}
		}
	}

	licensePayload, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Fatalf("ReadFile(LICENSE) error = %v", err)
	}
	licenseDigest := sha256.Sum256(licensePayload)
	if got := hex.EncodeToString(licenseDigest[:]); got != wantLicenseDigest {
		t.Errorf("LICENSE digest = %q, want Apache-2.0 digest %q", got, wantLicenseDigest)
	}
	dockerfilePayload, err := os.ReadFile(filepath.Join(root, "deploy/docker/Dockerfile"))
	if err != nil {
		t.Fatalf("ReadFile(deploy/docker/Dockerfile) error = %v", err)
	}
	const licenseCopy = "COPY --chown=0:0 --chmod=0644 LICENSE /usr/share/licenses/nginx-uix/LICENSE"
	if !strings.Contains(string(dockerfilePayload), licenseCopy) {
		t.Errorf("deploy/docker/Dockerfile does not install the Apache-2.0 license")
	}
	if !strings.Contains(string(dockerfilePayload), `org.opencontainers.image.licenses="Apache-2.0"`) {
		t.Errorf("deploy/docker/Dockerfile does not label the image as Apache-2.0")
	}
	if !strings.Contains(string(dockerfilePayload), `org.opencontainers.image.source="https://github.com/Kurok1/nginx-uix"`) {
		t.Errorf("deploy/docker/Dockerfile does not link the GHCR image to the source repository")
	}

	for relative, marker := range map[string]string{
		"api/v1/openapi.yaml":      "\n  version: " + want + "\n",
		"deploy/docker/Dockerfile": "\nARG VERSION=" + want + "\n",
	} {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Errorf("ReadFile(%s) error = %v", relative, err)
			continue
		}
		if !strings.Contains(string(payload), marker) {
			t.Errorf("%s does not contain synchronized release marker %q", relative, strings.TrimSpace(marker))
		}
	}
}

func TestV1DockerSourceFingerprintIgnoresLocalWorktrees(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatalf("ReadFile(.dockerignore) error = %v", err)
	}
	rules, err := parseIgnoreRules(payload)
	if err != nil {
		t.Fatalf("parseIgnoreRules(.dockerignore) error = %v", err)
	}
	if !ignoredLiteral(".worktrees/release/VERSION", rules) {
		t.Error(".dockerignore must exclude local Git worktrees from the release source fingerprint")
	}
}

func TestV1UpgradeHarnessPinsDirectAndLongChainBaselines(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}

	upgradePayload, err := os.ReadFile(filepath.Join(root, "tests/docker/upgrade.sh"))
	if err != nil {
		t.Fatalf("ReadFile(upgrade.sh) error = %v", err)
	}
	upgrade := string(upgradePayload)
	for _, marker := range []string{
		"SOURCE_VERSION=${SOURCE_VERSION:-0.7.0}",
		"0.6.0) DEFAULT_SOURCE_REF=97036da",
		"0.7.0) DEFAULT_SOURCE_REF=e46d34d",
		"EXPECTED_SOURCE_COMMIT=97036da9efbb3a614080565ef64785e75a7584fd",
		"EXPECTED_SOURCE_COMMIT=e46d34d6b12e8e0fdf31b606d73cd224fcc0d61e",
		`git show "${SOURCE_REF}:VERSION"`,
		`[ "${source_version}" = "${SOURCE_VERSION}" ]`,
		`[ "${source_commit}" = "${EXPECTED_SOURCE_COMMIT}" ]`,
		`v${PROJECT_VERSION}`,
	} {
		if !strings.Contains(upgrade, marker) {
			t.Errorf("upgrade.sh does not pin source baseline marker %q", marker)
		}
	}
	if strings.Contains(upgrade, "v1.0.0") {
		t.Error("upgrade.sh must identify the current release from PROJECT_VERSION instead of v1.0.0")
	}

	matrixPayload, err := os.ReadFile(filepath.Join(root, "tests/docker/upgrade_compatibility.sh"))
	if err != nil {
		t.Fatalf("ReadFile(upgrade_compatibility.sh) error = %v", err)
		return
	}
	matrix := string(matrixPayload)
	for _, marker := range []string{
		"SOURCE_VERSION=0.6.0",
		"SOURCE_VERSION=0.7.0",
		`"${SCRIPT_DIR}/upgrade.sh"`,
	} {
		if !strings.Contains(matrix, marker) {
			t.Errorf("upgrade_compatibility.sh does not exercise marker %q", marker)
		}
	}
}

func TestV1RepeatedRecoveryHarnessPinsTenPublicAPIRounds(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}

	workspacePayload, err := os.ReadFile(filepath.Join(root, "tests/docker/workspace.sh"))
	if err != nil {
		t.Fatalf("ReadFile(workspace.sh) error = %v", err)
	}
	workspace := string(workspacePayload)
	for _, marker := range []string{
		"REPEAT_BATCH_ROUNDS=5",
		`REPEAT_BATCH=${REPEAT_BATCH:-}`,
		"full|closure|repeat",
		"exercise_repeated_release_restore()",
		`queue_workspace_release "${repeat_prefix}-release"`,
		`"${BASE_URL}/api/v1/config/backups/${repeat_backup_id}/restores"`,
		`"${BASE_URL}/api/v1/config/releases/${repeat_release_id}"`,
		`"${BASE_URL}/api/v1/config/restores/${repeat_restore_id}"`,
		"verify_repeated_recovery_resources()",
		"verify_repeated_recovery_database()",
		"SELECT COUNT(*) FROM config_workspaces;",
		"PRAGMA integrity_check;",
		"PRAGMA foreign_key_check;",
	} {
		if !strings.Contains(workspace, marker) {
			t.Errorf("workspace.sh does not preserve repeated recovery marker %q", marker)
		}
	}

	entryPayload, err := os.ReadFile(filepath.Join(root, "tests/docker/repeated_recovery.sh"))
	if err != nil {
		t.Fatalf("ReadFile(repeated_recovery.sh) error = %v", err)
		return
	}
	entry := string(entryPayload)
	for _, marker := range []string{
		"REPEAT_TOTAL_ROUNDS=10",
		"REPEAT_BATCH=1",
		"REPEAT_BATCH=2",
		"WORKSPACE_PROFILE=repeat",
		`"${SCRIPT_DIR}/workspace.sh"`,
	} {
		if !strings.Contains(entry, marker) {
			t.Errorf("repeated_recovery.sh does not preserve entrypoint marker %q", marker)
		}
	}
}

func TestV1StabilityHarnessPinsTenMinuteWindow(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}

	workspacePayload, err := os.ReadFile(filepath.Join(root, "tests/docker/workspace.sh"))
	if err != nil {
		t.Fatalf("ReadFile(workspace.sh) error = %v", err)
	}
	workspace := string(workspacePayload)
	for _, marker := range []string{
		"SOAK_DURATION_SECONDS=600",
		"SOAK_INTERVAL_SECONDS=10",
		"SOAK_EXPECTED_SAMPLES=60",
		"SOAK_MAX_MEMORY_BYTES=268435456",
		"SOAK_MAX_PIDS=64",
		"full|closure|repeat|stability",
		"exercise_stability_soak()",
		`"${BASE_URL}/health/live"`,
		`"${BASE_URL}/health/ready"`,
		`"${BASE_URL}/api/v1/system/status"`,
		`"${BASE_URL}/api/v1/certificate-tasks?limit=100"`,
		`"${BASE_URL}/api/v1/route-tests/${CLOSURE_ROUTE_RUN_ID}"`,
		"/sys/fs/cgroup/memory.current",
		"/sys/fs/cgroup/pids.current",
		"/var/lib/nginx-uix/route-lab",
		"SELECT COUNT(*) FROM route_lab_runs",
		"SELECT COUNT(*) FROM config_production_lease",
		"PRAGMA integrity_check;",
		"PRAGMA foreign_key_check;",
	} {
		if !strings.Contains(workspace, marker) {
			t.Errorf("workspace.sh does not preserve stability marker %q", marker)
		}
	}

	entryPayload, err := os.ReadFile(filepath.Join(root, "tests/docker/stability.sh"))
	if err != nil {
		t.Fatalf("ReadFile(stability.sh) error = %v", err)
		return
	}
	entry := string(entryPayload)
	for _, marker := range []string{
		"WORKSPACE_PROFILE=stability",
		`"${SCRIPT_DIR}/workspace.sh"`,
	} {
		if !strings.Contains(entry, marker) {
			t.Errorf("stability.sh does not preserve entrypoint marker %q", marker)
		}
	}
}

func TestV1MultiarchBuildsBothPlatformsWithoutRuntimeSuites(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}

	payload, err := os.ReadFile(filepath.Join(root, "tests/docker/multiarch.sh"))
	if err != nil {
		t.Fatalf("ReadFile(multiarch.sh) error = %v", err)
	}
	matrix := string(payload)
	for _, marker := range []string{
		"build_binary_pair amd64",
		"build_binary_pair arm64",
		"--target binary-export",
		`--output "type=local,dest=${binary_output}"`,
		`--output "type=oci,dest=${image_archive},name=nginx-uix:${VERSION}-${image_arch}"`,
		`--output "type=registry,name=${MULTIARCH_IMAGE_REPOSITORY}:${VERSION}-${image_arch}"`,
		`build_image_archive amd64 "${AMD64_BUILD_IDENTITY}"`,
		`build_image_archive arm64 "${ARM64_BUILD_IDENTITY}"`,
		"linux_amd64_binary=%s agent=%s build_identity=%s image_archive_sha256=%s",
		"linux_arm64_binary=%s agent=%s build_identity=%s image_archive_sha256=%s",
	} {
		if !strings.Contains(matrix, marker) {
			t.Errorf("multiarch.sh does not preserve build marker %q", marker)
		}
	}
	for _, unwanted := range []string{
		"run_smoke_suite",
		"run_fault_suite",
		"run_workspace_suite",
		"run_upgrade_suite",
		"run_repeated_recovery_suite",
		"run_stability_suite",
		"run_security_suite",
		"run_acme_suite",
		"playwright",
		"SBOM",
		"sbom",
		"grype",
	} {
		if strings.Contains(matrix, unwanted) {
			t.Errorf("multiarch.sh must not include runtime or third-party validation marker %q", unwanted)
		}
	}
	if strings.Contains(matrix, `--tag "nginx-uix:${VERSION}-${image_arch}"`) {
		t.Error("multiarch.sh must name exporters directly instead of falling back to Docker Hub")
	}
	if strings.Contains(matrix, "go build") {
		t.Error("multiarch.sh must export binaries from the image build so they include the compiled web UI")
	}

	dockerfilePayload, err := os.ReadFile(filepath.Join(root, "deploy/docker/Dockerfile"))
	if err != nil {
		t.Fatalf("ReadFile(deploy/docker/Dockerfile) error = %v", err)
	}
	dockerfile := string(dockerfilePayload)
	for _, marker := range []string{
		"FROM --platform=$BUILDPLATFORM node:",
		"FROM --platform=$BUILDPLATFORM golang:",
		"FROM --platform=$BUILDPLATFORM nginx:",
		"FROM scratch AS binary-export",
		"COPY --from=go-builder /out/nginx-uix /nginx-uix",
		"COPY --from=go-builder /out/nginx-uix-agent /nginx-uix-agent",
	} {
		if !strings.Contains(dockerfile, marker) {
			t.Errorf("deploy/docker/Dockerfile does not preserve binary export marker %q", marker)
		}
	}
}

func TestV1GitHubActionsKeepsUnitSmokeAndMultiPlatformBuildGates(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}

	payload, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatalf("ReadFile(.github/workflows/ci.yml) error = %v", err)
		return
	}
	workflow := string(payload)
	for _, marker := range []string{
		"permissions:\n  contents: read",
		"pull_request:",
		"push:",
		"workflow_dispatch:",
		"runs-on: ubuntu-24.04",
		"timeout-minutes: 60",
		"actions/checkout@11d5960a326750d5838078e36cf38b85af677262",
		"actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
		"docker/setup-qemu-action@c7c53464625b32c7a7e944ae62b3e17d2b600130",
		"docker/setup-buildx-action@8d2750c68a42422c14e847fe6c8ac0403b4cbd6f",
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
		"version: v0.36.0",
		"GO_VERSION: 1.26.5",
		"NODE_VERSION: 24.17.0",
		"NPM_VERSION: 11.13.0",
		"NVM_COMMIT: 977563e97ddc66facf3a8e31c6cff01d236f09bd",
		"go mod verify",
		"go test ./...",
		"npm run lint",
		"npm run typecheck",
		"npm run test",
		"npm run build",
		"SMOKE_PROFILE=basic",
		`MULTIARCH_OUTPUT_DIR="${RUNNER_TEMP}/nginx-uix-multiarch"`,
		`"${REPOSITORY_ROOT}/tests/docker/multiarch.sh"`,
		"CANDIDATE_IMAGE: nginx-uix:1.1.0-ci-${{ github.run_id }}-${{ github.run_attempt }}",
		"name: nginx-uix-1.1.0-${{ github.sha }}",
		"path: ${{ runner.temp }}/nginx-uix-multiarch/",
		"if-no-files-found: error",
		"compression-level: 0",
	} {
		if !strings.Contains(workflow, marker) {
			t.Errorf("ci.yml does not preserve v1 quality marker %q", marker)
		}
	}
	if strings.Contains(workflow, "pull_request_target:") {
		t.Error("ci.yml must not execute repository code through pull_request_target")
	}
	for _, unwanted := range []string{
		"go test -race",
		"golangci-lint",
		"npm audit",
		"playwright install",
		"npm run test:e2e",
		"playwright_summary_test.sh",
	} {
		if strings.Contains(workflow, unwanted) {
			t.Errorf("ci.yml must not include extended validation marker %q", unwanted)
		}
	}

	pinnedAction := regexp.MustCompile(`^[0-9a-f]{40}(?:\s+#.*)?$`)
	for lineNumber, line := range strings.Split(workflow, "\n") {
		_, action, found := strings.Cut(strings.TrimSpace(line), "uses:")
		if !found {
			continue
		}
		_, revision, found := strings.Cut(strings.TrimSpace(action), "@")
		if !found || !pinnedAction.MatchString(revision) {
			t.Errorf("ci.yml line %d action is not pinned to a full commit: %q", lineNumber+1, line)
		}
	}
}

func TestV1ReleaseWorkflowPublishesGitHubReleaseAndGHCR(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}

	payload, err := os.ReadFile(filepath.Join(root, ".github/workflows/release.yml"))
	if err != nil {
		t.Fatalf("ReadFile(.github/workflows/release.yml) error = %v", err)
	}
	workflow := string(payload)
	for _, marker := range []string{
		"# @author hanchao <hanchao@66yunlian.com>",
		"# @since 1.0.0",
		"name: release",
		`- "v*.*.*"`,
		"contents: write",
		"packages: write",
		`expected_tag="v$(tr -d '\r\n' < VERSION)"`,
		`test "${GITHUB_REF_NAME}" = "${expected_tag}"`,
		"actions/checkout@11d5960a326750d5838078e36cf38b85af677262",
		"actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
		"docker/setup-qemu-action@c7c53464625b32c7a7e944ae62b3e17d2b600130",
		"docker/setup-buildx-action@8d2750c68a42422c14e847fe6c8ac0403b4cbd6f",
		"go mod verify",
		"go test ./...",
		"npm run lint",
		"npm run typecheck",
		"npm run test",
		"npm run build",
		"SMOKE_PROFILE=basic",
		`"${REPOSITORY_ROOT}/tests/docker/multiarch.sh"`,
		`MULTIARCH_IMAGE_REPOSITORY="${IMAGE_REPOSITORY}"`,
		`docker login ghcr.io`,
		"docker buildx imagetools create",
		`"${IMAGE_REPOSITORY}:${VERSION}"`,
		`"${IMAGE_REPOSITORY}:latest"`,
		"SHA256SUMS",
		`release_notes="docs/release/${GITHUB_REF_NAME}-release-notes.md"`,
		`test -f "${release_notes}"`,
		"gh release create",
		"gh release edit",
		"gh release upload",
		`--notes-file "${release_notes}"`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Errorf("release.yml does not preserve release marker %q", marker)
		}
	}
	for _, unwanted := range []string{
		"go test -race",
		"golangci-lint",
		"npm audit",
		"playwright",
		"security.sh",
		"grype",
		"sbom",
		"docker load --input",
		"--generate-notes",
	} {
		if strings.Contains(strings.ToLower(workflow), strings.ToLower(unwanted)) {
			t.Errorf("release.yml must not include extended validation marker %q", unwanted)
		}
	}
	loginStep := strings.Index(workflow, "- name: Log in to GHCR")
	buildStep := strings.Index(workflow, "- name: Build release binaries and OCI images")
	if loginStep < 0 || buildStep < 0 || loginStep > buildStep {
		t.Error("release.yml must log in to GHCR before the multi-platform build pushes images")
	}

	pinnedAction := regexp.MustCompile(`^[0-9a-f]{40}(?:\s+#.*)?$`)
	for lineNumber, line := range strings.Split(workflow, "\n") {
		_, action, found := strings.Cut(strings.TrimSpace(line), "uses:")
		if !found {
			continue
		}
		_, revision, found := strings.Cut(strings.TrimSpace(action), "@")
		if !found || !pinnedAction.MatchString(revision) {
			t.Errorf("release.yml line %d action is not pinned to a full commit: %q", lineNumber+1, line)
		}
	}
}

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
