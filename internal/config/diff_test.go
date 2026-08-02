/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestDiffReportsCreatedModifiedDeletedAndUnchangedFiles(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)

	created, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-diff-create"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/created.conf"), Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	modified, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-diff-modify"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "nginx.conf"), Content: []byte("events { worker_connections 512; }\nhttp { include conf.d/*.conf; }\n"), IfMatch: created.Workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := fixture.service.DeleteFile(context.Background(), Actor{UserID: 7, RequestID: "req-diff-delete"}, workspace.ID, DeleteFileInput{
		Path: mustRelativePath(t, "conf.d/created.conf"), ConfirmPath: "conf.d/created.conf", IfMatch: modified.Workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err = fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-diff-create-again"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/new.conf"), Content: []byte("server { listen 8082; }\n"), IfMatch: deleted.Workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err = fixture.service.DeleteFile(context.Background(), Actor{UserID: 7, RequestID: "req-diff-delete-base"}, workspace.ID, DeleteFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), ConfirmPath: "conf.d/site.conf", IfMatch: created.Workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.Diff(context.Background(), workspace.ID, nil)
	if err != nil {
		t.Fatalf("Diff() error = %v", err)
	}
	want := []FileDiffSummary{
		{Path: "conf.d/new.conf", Status: "created", AddedLines: 1},
		{Path: "conf.d/site.conf", Status: "deleted", RemovedLines: 1},
		{Path: "nginx.conf", Status: "modified", AddedLines: 1, RemovedLines: 1},
	}
	if !reflect.DeepEqual(result.Files, want) || !result.Complete || result.Reason != "" {
		t.Fatalf("Diff() = %#v, want files %#v", result, want)
	}
	if !strings.Contains(result.Patch, "--- /dev/null\n+++ b/conf.d/new.conf\n") ||
		!strings.Contains(result.Patch, "--- a/conf.d/site.conf\n+++ /dev/null\n") ||
		!strings.Contains(result.Patch, "--- a/nginx.conf\n+++ b/nginx.conf\n") {
		t.Fatalf("Diff() patch = %q", result.Patch)
	}
}

func TestDiffIncludesUnchangedFiles(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	result, err := fixture.service.Diff(context.Background(), workspace.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("Diff() files = %#v", result.Files)
	}
	for _, summary := range result.Files {
		if summary.Status != "unchanged" || summary.AddedLines != 0 || summary.RemovedLines != 0 {
			t.Fatalf("unchanged summary = %#v", summary)
		}
	}
	if result.Patch != "" || !result.Complete {
		t.Fatalf("Diff() = %#v", result)
	}
}

func TestDiffRepresentsRenameAsDeleteAndCreate(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	_, err := fixture.service.RenameFile(context.Background(), Actor{UserID: 7, RequestID: "req-diff-rename"}, workspace.ID, RenameFileInput{
		SourcePath: mustRelativePath(t, "conf.d/site.conf"), DestinationPath: mustRelativePath(t, "conf.d/new.conf"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Diff(context.Background(), workspace.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []FileDiffSummary{{Path: "conf.d/new.conf", Status: "created", AddedLines: 1}, {Path: "conf.d/site.conf", Status: "deleted", RemovedLines: 1}, {Path: "nginx.conf", Status: "unchanged"}}
	if !reflect.DeepEqual(result.Files, want) {
		t.Fatalf("files = %#v, want %#v", result.Files, want)
	}
	if !strings.Contains(result.Patch, "+++ b/conf.d/new.conf") || !strings.Contains(result.Patch, "--- a/conf.d/site.conf") {
		t.Fatalf("patch = %q", result.Patch)
	}
}

func TestDiffPreservesCRLFNoFinalNewlineAndUnicode(t *testing.T) {
	patch, summary, err := UnifiedDiff(context.Background(), "conf.d/unicode.conf", []byte("alpha\r\n旧值"), []byte("alpha\r\n新值"), 8_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != "modified" || summary.AddedLines != 1 || summary.RemovedLines != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	for _, want := range []string{" alpha\r\n", "-旧值\n\\ No newline at end of file\n", "+新值\n\\ No newline at end of file\n"} {
		if !strings.Contains(patch, want) {
			t.Fatalf("patch = %q, missing %q", patch, want)
		}
	}
}

func TestDiffPathFilterRejectsInvalidAndUnmanagedPaths(t *testing.T) {
	fixture := newServiceFixture(t)
	writeFixtureFile(t, filepath.Join(fixture.production.productionRoot, "private.key"), "secret\n", 0o600)
	workspace := fixture.mustCreate(t)
	filtered := mustRelativePath(t, "conf.d/site.conf")
	result, err := fixture.service.Diff(context.Background(), workspace.ID, &filtered)
	if err != nil || len(result.Files) != 1 || result.Files[0].Path != filtered {
		t.Fatalf("Diff(filtered) = %#v, %v", result, err)
	}
	invalid := RelativePath("../site.conf")
	if _, err := fixture.service.Diff(context.Background(), workspace.ID, &invalid); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("Diff(invalid) error = %v, want ErrPathInvalid", err)
	}
	unmanaged := mustRelativePath(t, "private.key")
	if _, err := fixture.service.Diff(context.Background(), workspace.ID, &unmanaged); !errors.Is(err, ErrEntryNotManaged) {
		t.Fatalf("Diff(unmanaged) error = %v, want ErrEntryNotManaged", err)
	}
}

func TestDiffStableOrder(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	first, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-diff-z"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/z.conf"), Content: []byte("# z\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-diff-a"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/a.conf"), Content: []byte("# a\n"), IfMatch: first.Workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Diff(context.Background(), workspace.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.IsSortedFunc(result.Files, func(left, right FileDiffSummary) int { return bytes.Compare([]byte(left.Path), []byte(right.Path)) }) {
		t.Fatalf("files are not byte-sorted: %#v", result.Files)
	}
	second, err := fixture.service.Diff(context.Background(), workspace.ID, nil)
	if err != nil || !reflect.DeepEqual(result, second) {
		t.Fatalf("Diff() unstable: %#v then %#v, %v", result, second, err)
	}
}

func TestDiffReadsStaleAndNeedsAttentionWorkspaces(t *testing.T) {
	for _, state := range []WorkspaceState{StateStale, StateNeedsAttention} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			setReviewWorkspaceState(t, fixture, workspace.ID, state)
			if _, err := fixture.service.Diff(context.Background(), workspace.ID, nil); err != nil {
				t.Fatalf("Diff(%s) error = %v", state, err)
			}
		})
	}
}

func TestDiffFailsClosedOnBaseOrDraftTamper(t *testing.T) {
	for _, tree := range []string{"base", "draft"} {
		t.Run(tree, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			mode := os.FileMode(0o400)
			if tree == "draft" {
				mode = 0o600
			}
			writeExistingFixtureFile(t, fixture.path(workspace.ID, tree+"/conf.d/site.conf"), "tampered\n", mode)
			if _, err := fixture.service.Diff(context.Background(), workspace.ID, nil); !errors.Is(err, ErrConflict) {
				t.Fatalf("Diff(%s tamper) error = %v, want ErrConflict", tree, err)
			}
		})
	}
}

func TestDiffHonorsCancellation(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.Diff(ctx, workspace.ID, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Diff(canceled) error = %v", err)
	}
	if _, _, err := UnifiedDiff(ctx, "nginx.conf", []byte("a\n"), []byte("b\n"), 8_000_000); !errors.Is(err, context.Canceled) {
		t.Fatalf("UnifiedDiff(canceled) error = %v", err)
	}
}

func TestUnifiedDiffFallsBackToSingleCompleteReplacementHunk(t *testing.T) {
	patch, summary, err := UnifiedDiff(context.Background(), "nginx.conf", []byte("same\nbefore\ntail\n"), []byte("same\nafter\ntail\n"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(patch, "@@") != 2 || !strings.Contains(patch, "-before\n+after\n") {
		t.Fatalf("fallback patch = %q", patch)
	}
	if summary.AddedLines != 1 || summary.RemovedLines != 1 {
		t.Fatalf("fallback summary = %#v", summary)
	}
}

func TestUnifiedDiffTreatsTwoAbsentSidesAsUnchanged(t *testing.T) {
	patch, summary, err := UnifiedDiff(context.Background(), "nginx.conf", nil, nil, 8_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if patch != "" || summary.Status != "unchanged" {
		t.Fatalf("UnifiedDiff(absent) = %q, %#v", patch, summary)
	}
}

func TestDiffResponseBudgetAcceptsExactFourMiB(t *testing.T) {
	result := diffResultWithJSONSize(t, DiffResult{Complete: true}, 4<<20)
	bounded, err := boundDiffResult(result, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bounded, result) {
		t.Fatalf("boundDiffResult() changed exact-limit result")
	}
}

func TestDiffResponseBudgetDropsPatchAtFourMiBPlusOne(t *testing.T) {
	result := diffResultWithJSONSize(t, DiffResult{Files: []FileDiffSummary{{Path: "nginx.conf", Status: "modified", AddedLines: 1, RemovedLines: 1}}, Complete: true}, (4<<20)+1)
	bounded, err := boundDiffResult(result, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	if bounded.Patch != "" || bounded.Complete || bounded.Reason != "response_limit" || !reflect.DeepEqual(bounded.Files, result.Files) {
		t.Fatalf("boundDiffResult() = %#v", bounded)
	}
	payload, err := json.Marshal(bounded)
	if err != nil || len(payload) > 4<<20 {
		t.Fatalf("fallback JSON bytes = %d, %v", len(payload), err)
	}
}

func TestDiffResponseBudgetRejectsSummariesAtFourMiBPlusOne(t *testing.T) {
	result := summariesOnlyDiffResultAtSize(t, (4<<20)+1)
	if _, err := boundDiffResult(result, 4<<20); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("boundDiffResult() error = %v, want ErrLimitExceeded", err)
	}
}

func diffResultWithJSONSize(t *testing.T, result DiffResult, target int) DiffResult {
	t.Helper()
	result.Patch = ""
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > target {
		t.Fatalf("fixed result size = %d, target %d", len(payload), target)
	}
	result.Patch = strings.Repeat("x", target-len(payload))
	payload, err = json.Marshal(result)
	if err != nil || len(payload) != target {
		t.Fatalf("result JSON bytes = %d, %v, want %d", len(payload), err, target)
	}
	return result
}

func summariesOnlyDiffResultAtSize(t *testing.T, target int) DiffResult {
	t.Helper()
	fullPath := strings.Repeat("a", 1024)
	full := make([]FileDiffSummary, DefaultLimits().MaxEntries)
	for index := range full {
		full[index] = FileDiffSummary{Path: RelativePath(fullPath), Status: "unchanged"}
	}
	firstOver := sort.Search(len(full)+1, func(count int) bool {
		payload, err := json.Marshal(DiffResult{Files: full[:count], Complete: false, Reason: "response_limit"})
		if err != nil {
			t.Fatal(err)
		}
		return len(payload) > target
	})
	if firstOver == 0 || firstOver > len(full) {
		t.Fatalf("summary bounds do not cross %d bytes", target)
	}
	result := DiffResult{Files: slices.Clone(full[:firstOver-1]), Complete: false, Reason: "response_limit"}
	candidate := result
	candidate.Files = append(slices.Clone(result.Files), FileDiffSummary{Path: "a", Status: "unchanged"})
	payload, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	pathLength := 1 + target - len(payload)
	if pathLength < 1 || pathLength > len(fullPath) {
		t.Fatalf("required final path length = %d", pathLength)
	}
	candidate.Files[len(candidate.Files)-1].Path = RelativePath(fullPath[:pathLength])
	payload, err = json.Marshal(candidate)
	if err != nil || len(payload) != target {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("summaries-only JSON bytes = %d, want %d", len(payload), target)
	}
	return candidate
}

func setReviewWorkspaceState(t *testing.T, fixture *serviceFixture, id WorkspaceID, state WorkspaceState) {
	t.Helper()
	reason := ""
	switch state {
	case StateStale:
		reason = "PRODUCTION_CHANGED"
	case StateNeedsAttention:
		reason = reasonRecoveryInvalid
	}
	fixture.repository.forceState(id, state, reason)
	workspace, err := fixture.repository.Workspace(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	root := fixture.openWorkspace(t, id)
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close(workspace) error = %v", err)
		}
	}()
	if err := WriteControlState(context.Background(), root, controlStateFromWorkspace(workspace)); err != nil {
		t.Fatalf("WriteControlState(%s) error = %v", state, err)
	}
}
