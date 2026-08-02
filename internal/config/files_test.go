/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTreeIsStableAndReturnsCurrentWorkspaceETag(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)

	first, err := fixture.service.Tree(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	second, err := fixture.service.Tree(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("Tree(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) || first.WorkspaceETag != workspace.ETag() {
		t.Fatalf("Tree() = %#v then %#v", first, second)
	}
	if !slices.IsSortedFunc(first.Entries, compareEntries) || !slices.IsSortedFunc(first.Dependencies, compareDependencies) {
		t.Fatalf("Tree() is not canonically ordered: %#v", first)
	}
}

func TestTreeReadsVerifiedPublishedWorkspace(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	if _, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-published-tree"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/published.conf"), Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	}); err != nil {
		t.Fatal(err)
	}
	setReviewWorkspaceState(t, fixture, workspace.ID, StatePublished)

	published, err := fixture.repository.Workspace(context.Background(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fixture.service.Tree(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("Tree(published) error = %v", err)
	}
	if tree.WorkspaceETag != published.ETag() || tree.DiffStatuses["conf.d/published.conf"] != "created" ||
		!slices.ContainsFunc(tree.Entries, func(entry Entry) bool {
			return entry.Path == "conf.d/published.conf" && entry.Type == EntryRegular && entry.Class == EntryManagedText
		}) {
		t.Fatalf("Tree(published) = %#v", tree)
	}
}

func TestTreeReadsPublishedWorkspaceWithoutDraftChanges(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	setReviewWorkspaceState(t, fixture, workspace.ID, StatePublished)

	tree, err := fixture.service.Tree(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("Tree(unchanged published) error = %v", err)
	}
	if tree.WorkspaceETag != workspace.ETag() || tree.DiffStatuses["conf.d/site.conf"] != "unchanged" {
		t.Fatalf("Tree(unchanged published) = %#v", tree)
	}
}

func TestTreeProjectsManagedDiffStatusWithoutUsingUnifiedDiff(t *testing.T) {
	fixture := newServiceFixture(t)
	writeFixtureFile(t, filepath.Join(fixture.production.productionRoot, "private.key"), "secret\n", 0o600)
	workspace := fixture.mustCreate(t)

	created, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-tree-create"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/new.conf"), Content: []byte("server { listen 8082; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	modified, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-tree-modify"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "nginx.conf"), Content: []byte("events { worker_connections 512; }\nhttp { include conf.d/*.conf; }\n"), IfMatch: created.Workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}

	fixture.service.limits.MaxDiffResponseBytes = 1
	tree, err := fixture.service.Tree(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	want := map[RelativePath]string{
		"conf.d/new.conf":  "created",
		"conf.d/site.conf": "unchanged",
		"nginx.conf":       "modified",
	}
	if !reflect.DeepEqual(tree.DiffStatuses, want) {
		t.Fatalf("Tree().DiffStatuses = %#v, want %#v", tree.DiffStatuses, want)
	}
	for _, unmanaged := range []RelativePath{"conf.d", "private.key"} {
		if status, exists := tree.DiffStatuses[unmanaged]; exists {
			t.Fatalf("unmanaged %q diff status = %q", unmanaged, status)
		}
	}
	if _, err := fixture.service.Diff(context.Background(), workspace.ID, nil); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Diff() with one-byte response limit error = %v, want ErrLimitExceeded", err)
	}

	deleted, err := fixture.service.DeleteFile(context.Background(), Actor{UserID: 7, RequestID: "req-tree-delete"}, workspace.ID, DeleteFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), ConfirmPath: "conf.d/site.conf", IfMatch: modified.Workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err = fixture.service.Tree(context.Background(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := tree.DiffStatuses["conf.d/site.conf"]; exists {
		t.Fatalf("deleted draft path remains in tree projection: %#v", tree.DiffStatuses)
	}
	fixture.service.limits.MaxDiffResponseBytes = DefaultLimits().MaxDiffResponseBytes
	diff, err := fixture.service.Diff(context.Background(), deleted.Workspace.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(diff.Files, func(summary FileDiffSummary) bool {
		return summary.Path == "conf.d/site.conf" && summary.Status == "deleted"
	}) {
		t.Fatalf("Diff() does not represent deleted tree path: %#v", diff.Files)
	}
}

func TestReadFileReturnsManagedContentAndRejectsOtherClasses(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)

	view, err := fixture.service.ReadFile(context.Background(), workspace.ID, mustRelativePath(t, "conf.d/site.conf"))
	if err != nil {
		t.Fatalf("ReadFile(managed) error = %v", err)
	}
	if view.Content != "server { listen 8080; }\n" || view.LineEnding != "lf" || view.WorkspaceETag != workspace.ETag() ||
		view.Entry.Path != "conf.d/site.conf" {
		t.Fatalf("ReadFile(managed) = %#v", view)
	}

	tests := []struct {
		name string
		path string
		make func(string) error
	}{
		{name: "directory", path: "directory", make: func(path string) error { return os.Mkdir(path, 0o700) }},
		{name: "symlink", path: "linked.conf", make: func(path string) error { return os.Symlink("site.conf", path) }},
		{name: "sensitive", path: "secret.key", make: func(path string) error { return os.WriteFile(path, []byte("secret"), 0o600) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(fixture.path(workspace.ID, "draft"), test.path)
			if err := test.make(path); err != nil {
				t.Fatal(err)
			}
			_, err := fixture.service.ReadFile(context.Background(), workspace.ID, RelativePath(test.path))
			if !errors.Is(err, ErrEntryNotManaged) && !errors.Is(err, ErrConflict) {
				t.Fatalf("ReadFile(%s) error = %v, want managed rejection", test.name, err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReadFileReturnsHistoricalContentFromPublishedWorkspace(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	updated, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-published-file"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server { listen 8443 ssl; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	setReviewWorkspaceState(t, fixture, workspace.ID, StatePublished)

	view, err := fixture.service.ReadFile(context.Background(), workspace.ID, mustRelativePath(t, "conf.d/site.conf"))
	if err != nil {
		t.Fatalf("ReadFile(published) error = %v", err)
	}
	if view.Content != "server { listen 8443 ssl; }\n" || view.WorkspaceETag != updated.Workspace.ETag() {
		t.Fatalf("ReadFile(published) = %#v", view)
	}
}

func TestPublishedWorkspaceReadFailsClosedOnControlMetadataMismatch(t *testing.T) {
	tests := map[string]func(*ControlState){
		"state":       func(control *ControlState) { control.State = StateReady },
		"reason code": func(control *ControlState) { control.StateReasonCode = "UNEXPECTED" },
		"updated at":  func(control *ControlState) { control.UpdatedAt = control.UpdatedAt.Add(time.Second) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			setReviewWorkspaceState(t, fixture, workspace.ID, StatePublished)
			published, err := fixture.repository.Workspace(context.Background(), workspace.ID)
			if err != nil {
				t.Fatal(err)
			}
			root := fixture.openWorkspace(t, workspace.ID)
			control := controlStateFromWorkspace(published)
			mutate(&control)
			if err := WriteControlState(context.Background(), root, control); err != nil {
				root.Close()
				t.Fatal(err)
			}
			if err := root.Close(); err != nil {
				t.Fatal(err)
			}

			if _, err := fixture.service.Tree(context.Background(), workspace.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("Tree(control %s mismatch) error = %v, want ErrConflict", name, err)
			}
		})
	}
}

func TestPublishedWorkspaceReadRequiresBaseManifestWhenDraftChanged(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	if _, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-published-base"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/published.conf"), Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	}); err != nil {
		t.Fatal(err)
	}
	setReviewWorkspaceState(t, fixture, workspace.ID, StatePublished)
	root := fixture.openWorkspace(t, workspace.ID)
	if err := root.RemoveRegular(context.Background(), controlBaseManifestPath); err != nil {
		root.Close()
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Tree(context.Background(), workspace.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("Tree(missing changed base manifest) error = %v, want ErrConflict", err)
	}
}

func TestPublishedWorkspaceReadRequiresValidLastReleaseID(t *testing.T) {
	for name, releaseID := range map[string]ReleaseID{
		"missing":   "",
		"malformed": "not-a-release-id",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			setReviewWorkspaceState(t, fixture, workspace.ID, StatePublished)
			fixture.repository.mu.Lock()
			published := fixture.repository.workspaces[workspace.ID]
			published.LastReleaseID = releaseID
			fixture.repository.workspaces[workspace.ID] = published
			fixture.repository.mu.Unlock()

			if _, err := fixture.service.Tree(context.Background(), workspace.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("Tree(last_release_id=%q) error = %v, want ErrConflict", releaseID, err)
			}
		})
	}
}

func TestReplaceFileRequiresCurrentWorkspaceETag(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	path := mustRelativePath(t, "conf.d/site.conf")
	_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-replace"}, workspace.ID, ReplaceFileInput{
		Path: path, Content: []byte("server { listen 8081; }\n"),
		IfMatch: `"draft-v1:0000000000000000000000000000000000000000000000000000000000000000"`,
	})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.CurrentETag != workspace.ETag() {
		t.Fatalf("ReplaceFile() error = %#v", err)
	}
	assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "server { listen 8080; }\n")
	assertNoFileAudit(t, fixture.repository, "config.file.replace")
}

func TestReplaceFileRejectsOpenAttentionBeforeOpeningWorkspaceDraft(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	path := fixture.path(workspace.ID, "draft/nginx.conf")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.attention = fixedAttentionReader{open: true}
	_, err = fixture.service.ReplaceFile(
		context.Background(), Actor{UserID: 7, RequestID: "req-attention-edit"}, workspace.ID,
		ReplaceFileInput{Path: "nginx.conf", Content: []byte("events { worker_connections 128; }\n"), IfMatch: workspace.ETag()},
	)
	if !errors.Is(err, ErrAttentionUnresolved) {
		t.Fatalf("ReplaceFile() error = %v, want ErrAttentionUnresolved", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("draft changed while attention open: read error = %v", readErr)
	}
}

func TestReplaceFileUpdatesDraftManifestWorkspaceAndAudit(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	productionBefore, err := os.ReadFile(filepath.Join(fixture.production.productionRoot, "conf.d", "site.conf"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-replace"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatalf("ReplaceFile() error = %v", err)
	}
	if result.Workspace.Revision != workspace.Revision+1 || result.Workspace.DraftDigest == workspace.DraftDigest ||
		result.Entry == nil || result.Entry.Path != "conf.d/site.conf" || result.Workspace.ETag() == workspace.ETag() {
		t.Fatalf("ReplaceFile() = %#v", result)
	}
	assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "server { listen 8081; }\n")
	productionAfter, err := os.ReadFile(filepath.Join(fixture.production.productionRoot, "conf.d", "site.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(productionAfter, productionBefore) {
		t.Fatal("ReplaceFile() changed production")
	}
	assertFileAudit(t, fixture.repository, "config.file.replace", "req-replace", "conf.d/site.conf")
	if _, err := os.Lstat(fixture.path(workspace.ID, "control/journal.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("journal remains after success: %v", err)
	}
}

func TestCreateFileCreatesManagedTextOnlyInExistingDirectory(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	result, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-create-file"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/new.conf"), Content: []byte("server { listen 8082; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatalf("CreateFile() error = %v", err)
	}
	if result.Entry == nil || result.Entry.Path != "conf.d/new.conf" || result.Workspace.Revision != workspace.Revision+1 {
		t.Fatalf("CreateFile() = %#v", result)
	}
	assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/new.conf"), "server { listen 8082; }\n")
	assertFileAudit(t, fixture.repository, "config.file.create", "req-create-file", "conf.d/new.conf")

	_, err = fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-create-existing"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server {}\n"), IfMatch: result.Workspace.ETag(),
	})
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("CreateFile(existing) error = %v, want exists", err)
	}
	_, err = fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-create-missing-dir"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "missing/site.conf"), Content: []byte("server {}\n"), IfMatch: result.Workspace.ETag(),
	})
	if err == nil {
		t.Fatal("CreateFile(missing directory) error = nil")
	}
}

func TestCopyFileValidatesSourceAndDestinationPolicy(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	result, err := fixture.service.CopyFile(context.Background(), Actor{UserID: 7, RequestID: "req-copy"}, workspace.ID, CopyFileInput{
		SourcePath: mustRelativePath(t, "conf.d/site.conf"), DestinationPath: mustRelativePath(t, "conf.d/copy.conf"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatalf("CopyFile() error = %v", err)
	}
	if result.Entry == nil || result.Entry.Path != "conf.d/copy.conf" {
		t.Fatalf("CopyFile() = %#v", result)
	}
	assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/copy.conf"), "server { listen 8080; }\n")
	assertFileAudit(t, fixture.repository, "config.file.copy", "req-copy", "conf.d/copy.conf")

	_, err = fixture.service.CopyFile(context.Background(), Actor{UserID: 7, RequestID: "req-copy-policy"}, workspace.ID, CopyFileInput{
		SourcePath: mustRelativePath(t, "conf.d/site.conf"), DestinationPath: mustRelativePath(t, "conf.d/not-managed.txt"), IfMatch: result.Workspace.ETag(),
	})
	if !errors.Is(err, ErrEntryNotManaged) {
		t.Fatalf("CopyFile(unmanaged destination) error = %v, want ErrEntryNotManaged", err)
	}
}

func TestRenameFileIsCopyThenDeleteWithoutIncludeRewrite(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	result, err := fixture.service.RenameFile(context.Background(), Actor{UserID: 7, RequestID: "req-rename"}, workspace.ID, RenameFileInput{
		SourcePath: mustRelativePath(t, "conf.d/site.conf"), DestinationPath: mustRelativePath(t, "conf.d/renamed.conf"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatalf("RenameFile() error = %v", err)
	}
	if result.Entry == nil || result.Entry.Path != "conf.d/renamed.conf" {
		t.Fatalf("RenameFile() = %#v", result)
	}
	if _, err := os.Lstat(fixture.path(workspace.ID, "draft/conf.d/site.conf")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("renamed source remains: %v", err)
	}
	if len(result.Workspace.ETag()) == 0 {
		t.Fatal("RenameFile() returned empty ETag")
	}
	assertFileAudit(t, fixture.repository, "config.file.rename", "req-rename", "conf.d/renamed.conf")
}

func TestDeleteFileRequiresExactConfirmPathAndReturnsNilEntry(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	created, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-create-delete"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/delete.conf"), Content: []byte("server { listen 8083; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.DeleteFile(context.Background(), Actor{UserID: 7, RequestID: "req-delete-wrong"}, workspace.ID, DeleteFileInput{
		Path: mustRelativePath(t, "conf.d/delete.conf"), ConfirmPath: "conf.d/other.conf", IfMatch: created.Workspace.ETag(),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteFile(wrong confirmation) error = %v, want ErrConflict", err)
	}
	result, err := fixture.service.DeleteFile(context.Background(), Actor{UserID: 7, RequestID: "req-delete"}, workspace.ID, DeleteFileInput{
		Path: mustRelativePath(t, "conf.d/delete.conf"), ConfirmPath: "conf.d/delete.conf", IfMatch: created.Workspace.ETag(),
	})
	if err != nil {
		t.Fatalf("DeleteFile() error = %v", err)
	}
	if result.Entry != nil || result.Workspace.Revision != created.Workspace.Revision+1 {
		t.Fatalf("DeleteFile() = %#v", result)
	}
	if _, err := os.Lstat(fixture.path(workspace.ID, "draft/conf.d/delete.conf")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted file remains: %v", err)
	}
	assertFileAudit(t, fixture.repository, "config.file.delete", "req-delete", "conf.d/delete.conf")
}

func TestReplaceFileRejectsEveryNonCurrentETagBeforeJournal(t *testing.T) {
	tests := []string{
		"",
		`W/"draft-v1:0000000000000000000000000000000000000000000000000000000000000000"`,
		`"draft-v1:0000000000000000000000000000000000000000000000000000000000000000", "other"`,
		`"draft-v1:0000000000000000000000000000000000000000000000000000000000000000"`,
	}
	for _, ifMatch := range tests {
		t.Run(ifMatch, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-etag"}, workspace.ID, ReplaceFileInput{
				Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server { listen 8081; }\n"), IfMatch: ifMatch,
			})
			var conflict *ConflictError
			if !errors.As(err, &conflict) || conflict.CurrentETag != workspace.ETag() {
				t.Fatalf("ReplaceFile() error = %#v", err)
			}
			if _, err := os.Lstat(fixture.path(workspace.ID, "control/journal.json")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("journal written after ETag mismatch: %v", err)
			}
			assertNoFileAudit(t, fixture.repository, "config.file.replace")
		})
	}
}

func TestCreateFileAcceptsDomainContentThroughTwoMiBAndRejectsInvalidText(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	content := bytes.Repeat([]byte("# comment\n"), (256<<10)/len("# comment\n"))
	result, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-large"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/large.conf"), Content: content, IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatalf("CreateFile(256 KiB) error = %v", err)
	}
	for name, invalid := range map[string][]byte{
		"invalid utf8": {0xff},
		"nul":          {'s', 'e', 'r', 'v', 'e', 'r', 0},
		"over limit":   bytes.Repeat([]byte{'#'}, (2<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-invalid"}, workspace.ID, CreateFileInput{
				Path: mustRelativePath(t, "conf.d/invalid.conf"), Content: invalid, IfMatch: result.Workspace.ETag(),
			})
			if err == nil {
				t.Fatal("CreateFile(invalid) error = nil")
			}
		})
	}
}

func TestFileMutationRequiresReadyWorkspace(t *testing.T) {
	for _, state := range []WorkspaceState{StateStale, StatePublished} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			setReviewWorkspaceState(t, fixture, workspace.ID, state)
			_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-not-ready"}, workspace.ID, ReplaceFileInput{
				Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server {}\n"), IfMatch: workspace.ETag(),
			})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("ReplaceFile(%s) error = %v, want ErrConflict", state, err)
			}
			assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "server { listen 8080; }\n")
		})
	}
}

func TestFileMutationMovesChangedProductionToStale(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	writeExistingFixtureFile(t, filepath.Join(fixture.production.productionRoot, "conf.d", "site.conf"), "server { listen 9090; }\n", 0o640)
	_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-production-change"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server {}\n"), IfMatch: workspace.ETag(),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ReplaceFile(changed production) error = %v, want ErrConflict", err)
	}
	got, readErr := fixture.repository.Workspace(context.Background(), workspace.ID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got.State != StateStale || got.StateReasonCode != "PRODUCTION_CHANGED" {
		t.Fatalf("workspace = %#v, want stale", got)
	}
	if _, err := os.Lstat(fixture.path(workspace.ID, "control/journal.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("journal written for stale transition: %v", err)
	}
}

func TestFileMutationMarksBaseOrDraftTamperNeedsAttention(t *testing.T) {
	tests := []struct {
		name       string
		relative   string
		wantReason string
	}{
		{name: "base", relative: "base/conf.d/site.conf", wantReason: reasonBaseDigestMismatch},
		{name: "draft", relative: "draft/conf.d/site.conf", wantReason: reasonDraftDigestMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			writeExistingFixtureFile(t, fixture.path(workspace.ID, test.relative), "tampered\n", map[string]fs.FileMode{"base": 0o400, "draft": 0o600}[test.name])
			_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-tamper"}, workspace.ID, ReplaceFileInput{
				Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server {}\n"), IfMatch: workspace.ETag(),
			})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("ReplaceFile(tamper) error = %v, want ErrConflict", err)
			}
			got, readErr := fixture.repository.Workspace(context.Background(), workspace.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if got.State != StateNeedsAttention || got.StateReasonCode != test.wantReason {
				t.Fatalf("workspace = %#v, want needs_attention/%s", got, test.wantReason)
			}
		})
	}
}

func TestCreateFileEnforcesTotalWorkspaceCapacity(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	fixture.service.limits.MaxWorkspaceBytes = workspace.WorkspaceBytes + 1024
	_, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-capacity"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/capacity.conf"), Content: bytes.Repeat([]byte{'#'}, 2048), IfMatch: workspace.ETag(),
	})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("CreateFile(capacity) error = %v, want ErrLimitExceeded", err)
	}
}

func TestFileMutationHonorsCancellationAndLockContention(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	root := fixture.openWorkspace(t, workspace.ID)
	lock, err := AcquireWorkspaceLock(context.Background(), root, LockExclusive)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err = fixture.service.ReplaceFile(ctx, Actor{UserID: 7, RequestID: "req-contended"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server {}\n"), IfMatch: workspace.ETag(),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReplaceFile(contended) error = %v, want deadline", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	_, err = fixture.service.ReplaceFile(canceled, Actor{UserID: 7, RequestID: "req-canceled"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server {}\n"), IfMatch: workspace.ETag(),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReplaceFile(canceled) error = %v, want canceled", err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("ReadFile(%s) = %q, want %q", path, content, want)
	}
}

func assertNoFileAudit(t *testing.T, repository *memoryWorkspaceRepository, action string) {
	t.Helper()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, audit := range repository.audits {
		if audit.Action == action {
			t.Fatalf("unexpected audit: %#v", audit)
		}
	}
}

func assertFileAudit(t *testing.T, repository *memoryWorkspaceRepository, action, requestID, path string) {
	t.Helper()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, audit := range repository.audits {
		if audit.Action == action && audit.RequestID == requestID && strings.Contains(audit.DetailsJSON, `"`+path+`"`) &&
			!strings.Contains(audit.DetailsJSON, "listen 8081") {
			return
		}
	}
	t.Fatalf("safe audit %q/%q not found in %#v", action, requestID, repository.audits)
}
