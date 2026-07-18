/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestValidateCandidateBuildsCompleteOverlayAndUsesFixedCommand(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "stages")
	for _, directory := range []string{production, workspaces, stages, filepath.Join(production, "conf.d")} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\nhttp { include "+production+"/conf.d/*.conf; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "conf.d", "site.conf"), "server { listen 8080; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "mime.types"), "types { text/plain txt; }\n", 0o644)

	id := config.WorkspaceID("11111111111111111111111111111111")
	draftContent := []byte("server { listen 8081; }\n")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, id, "conf.d/site.conf", draftContent)
	draftDigest := manifest.Digest()

	var gotCandidate string
	executor := func(_ context.Context, specification commandSpec) (commandResult, error) {
		if specification.executable != nginxExecutable || specification.timeout != candidateValidationTimeout || specification.maxOutputBytes != candidateDiagnosticLimit {
			t.Fatalf("unexpected fixed command specification: %+v", specification)
		}
		if len(specification.arguments) != 5 || !slices.Equal(specification.arguments[:2], []string{"-t", "-p"}) || specification.arguments[3] != "-c" {
			t.Fatalf("arguments = %#v", specification.arguments)
		}
		gotCandidate = strings.TrimSuffix(specification.arguments[2], string(filepath.Separator))
		if specification.arguments[4] != filepath.Join(gotCandidate, "nginx.conf") {
			t.Fatalf("config argument = %q", specification.arguments[4])
		}
		contents, err := os.ReadFile(filepath.Join(gotCandidate, "conf.d", "site.conf"))
		if err != nil || string(contents) != string(draftContent) {
			t.Fatalf("draft overlay = %q, err = %v", contents, err)
		}
		unmanaged, err := os.ReadFile(filepath.Join(gotCandidate, "mime.types"))
		if err != nil || string(unmanaged) != "types { text/plain txt; }\n" {
			t.Fatalf("unmanaged copy = %q, err = %v", unmanaged, err)
		}
		entry, err := os.ReadFile(filepath.Join(gotCandidate, "nginx.conf"))
		if err != nil || strings.Contains(string(entry), production+"/conf.d") || !strings.Contains(string(entry), gotCandidate+"/conf.d") {
			t.Fatalf("absolute include was not isolated: %q, err = %v", entry, err)
		}
		return commandResult{exitCode: 0, stderr: []byte("nginx: configuration file test is successful\n")}, nil
	}
	service := mustCandidateService(t, candidateOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Executor: executor,
	})
	service.cachedBuild = &BuildInfo{Version: "1.27.5", PIDPath: "/run/nginx.pid", SbinPath: nginxExecutable, ConfigureArguments: []string{"--pid-path=/run/nginx.pid", "--sbin-path=" + nginxExecutable}}

	result, err := service.ValidateCandidate(context.Background(), config.CandidateValidationRequest{
		WorkspaceID: id, ProductionDigest: productionDigest, DraftDigest: draftDigest,
	})
	if err != nil {
		t.Fatalf("ValidateCandidate() error = %v", err)
	}
	if !result.Valid || result.CandidateDigest == (config.Digest{}) || result.ValidatorBuildID == "" || result.CheckedAt.IsZero() {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(gotCandidate); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("candidate stage survived validation: %v", err)
	}
	assertCandidateStageEmpty(t, stages)
}

func TestValidateCandidateRejectsUnsupportedProductionEntryBeforeCommand(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "stages")
	for _, directory := range []string{production, workspaces, stages} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	id := config.WorkspaceID("22222222222222222222222222222222")
	if err := os.Symlink("missing.conf", filepath.Join(production, "broken.conf")); err != nil {
		t.Fatal(err)
	}
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, id, "", nil)

	called := false
	service := mustCandidateService(t, candidateOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Executor: func(context.Context, commandSpec) (commandResult, error) {
			called = true
			return commandResult{}, nil
		},
	})
	service.cachedBuild = &BuildInfo{Version: "1.27.5", PIDPath: "/run/nginx.pid", SbinPath: nginxExecutable, ConfigureArguments: []string{"--pid-path=/run/nginx.pid", "--sbin-path=" + nginxExecutable}}

	_, err := service.ValidateCandidate(context.Background(), config.CandidateValidationRequest{
		WorkspaceID: id, ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
	})
	if !errors.Is(err, config.ErrPathInvalid) {
		t.Fatalf("ValidateCandidate() error = %v, want path invalid", err)
	}
	if called {
		t.Fatal("nginx command ran for unsafe candidate")
	}
	assertCandidateStageEmpty(t, stages)
}

func TestValidateCandidateReturnsRelativeBoundedDiagnostic(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "stages")
	for _, directory := range []string{production, workspaces, stages} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	id := config.WorkspaceID("33333333333333333333333333333333")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, id, "nginx.conf", []byte("invalid directive;\n"))
	service := mustCandidateService(t, candidateOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Executor: func(_ context.Context, specification commandSpec) (commandResult, error) {
			candidate := strings.TrimSuffix(specification.arguments[2], string(filepath.Separator))
			stderr := []byte("nginx: [emerg] unknown directive in " + filepath.Join(candidate, "nginx.conf") + ":7\nsecret\x00tail")
			return commandResult{exitCode: 1, stderr: stderr}, &commandExitError{Code: 1, Diagnostic: sanitizeDiagnostic(stderr)}
		},
	})
	service.cachedBuild = &BuildInfo{Version: "1.27.5", PIDPath: "/run/nginx.pid", SbinPath: nginxExecutable, ConfigureArguments: []string{"--pid-path=/run/nginx.pid", "--sbin-path=" + nginxExecutable}}

	result, err := service.ValidateCandidate(context.Background(), config.CandidateValidationRequest{
		WorkspaceID: id, ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
	})
	if !errors.Is(err, ErrConfigInvalid) || result.Valid || len(result.Diagnostics) != 1 {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Path != "nginx.conf" || diagnostic.Line != 7 || diagnostic.Code != "nginx_config_invalid" || strings.Contains(diagnostic.Summary, root) || strings.ContainsRune(diagnostic.Summary, '\x00') {
		t.Fatalf("diagnostic = %+v", diagnostic)
	}
	assertCandidateStageEmpty(t, stages)
}

func TestValidateCandidateLimitsConcurrentMaterialization(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "stages")
	for _, directory := range []string{production, workspaces, stages} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\n", 0o640)
	id := config.WorkspaceID("44444444444444444444444444444444")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, id, "nginx.conf", []byte("events { worker_connections 128; }\n"))
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	service := mustCandidateService(t, candidateOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Executor: func(ctx context.Context, _ commandSpec) (commandResult, error) {
			started <- struct{}{}
			select {
			case <-release:
				return commandResult{exitCode: 0}, nil
			case <-ctx.Done():
				return commandResult{}, ctx.Err()
			}
		},
	})
	service.cachedBuild = &BuildInfo{Version: "1.27.5", PIDPath: "/run/nginx.pid", SbinPath: nginxExecutable, ConfigureArguments: []string{"--pid-path=/run/nginx.pid", "--sbin-path=" + nginxExecutable}}
	request := config.CandidateValidationRequest{
		WorkspaceID: id, ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
	}
	errorsByCall := make(chan error, 3)
	for range 2 {
		go func() {
			_, err := service.ValidateCandidate(context.Background(), request)
			errorsByCall <- err
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("candidate validation did not occupy both slots")
		}
	}
	waitingContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	go func() {
		_, err := service.ValidateCandidate(waitingContext, request)
		errorsByCall <- err
	}()
	select {
	case <-started:
		t.Fatal("third candidate validation exceeded the global limit")
	case <-waitingContext.Done():
	}
	close(release)

	deadlineErrors := 0
	for range 3 {
		select {
		case err := <-errorsByCall:
			if errors.Is(err, context.DeadlineExceeded) {
				deadlineErrors++
			} else if err != nil {
				t.Fatalf("ValidateCandidate() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("candidate validation did not finish")
		}
	}
	if deadlineErrors != 1 {
		t.Fatalf("deadline errors = %d, want 1", deadlineErrors)
	}
	assertCandidateStageEmpty(t, stages)
}

func mustCandidateWorkspace(t *testing.T, production, workspaces string, id config.WorkspaceID, draftPath config.RelativePath, draftEntry []byte) (config.Manifest, config.Digest) {
	t.Helper()
	productionRoot, err := config.OpenScopedRoot(production)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := config.BuildInventory(context.Background(), productionRoot, config.SnapshotOptions{
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Policy: config.NewPolicy(), FileMode: 0o400, DirectoryMode: 0o700,
	})
	if closeErr := productionRoot.Close(); err != nil || closeErr != nil {
		t.Fatalf("inventory error = %v, close = %v", err, closeErr)
	}
	manifest := inventory.Manifest
	workspace := filepath.Join(workspaces, string(id))
	for _, directory := range []string{workspace, filepath.Join(workspace, "control"), filepath.Join(workspace, "draft")} {
		mustMkdirCandidate(t, directory)
	}
	for index, entry := range manifest.Entries {
		if entry.Type == config.EntryDirectory {
			mustMkdirCandidate(t, filepath.Join(workspace, "draft", filepath.FromSlash(string(entry.Path))))
		}
		if entry.Class != config.EntryManagedText {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(production, filepath.FromSlash(string(entry.Path))))
		if err != nil {
			t.Fatal(err)
		}
		if draftEntry != nil && entry.Path == draftPath {
			contents = draftEntry
		}
		manifest.Entries[index].Size = int64(len(contents))
		manifest.Entries[index].ContentDigest = config.Digest(sha256.Sum256(contents))
		mustWriteCandidate(t, filepath.Join(workspace, "draft", filepath.FromSlash(string(entry.Path))), string(contents), 0o600)
	}
	var managedBytes int64
	for _, entry := range manifest.Entries {
		if entry.Class == config.EntryManagedText {
			managedBytes += entry.Size
		}
	}
	manifest.ManagedBytes = managedBytes
	workspaceRoot, err := config.OpenScopedRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteControlManifest(context.Background(), workspaceRoot, manifest); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteControlState(context.Background(), workspaceRoot, config.ControlState{
		SchemaVersion: config.ControlSchemaVersion, WorkspaceID: id, State: config.StateReady, Revision: 1, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := workspaceRoot.Close(); err != nil {
		t.Fatal(err)
	}
	return manifest, inventory.Digest
}

func mustCandidateService(t *testing.T, options candidateOptions) *Service {
	t.Helper()
	service, err := newCandidateService(options)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func mustMkdirCandidate(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWriteCandidate(t *testing.T, path, contents string, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertCandidateStageEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("candidate stages remain: %v", entries)
	}
}
