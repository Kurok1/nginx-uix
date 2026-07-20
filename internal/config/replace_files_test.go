/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package config

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	testStructuredPreviewID = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	testStructuredTargetID  = "2122232425262728292a2b2c2d2e2f30"
)

func TestReplaceFilesAtomicallyUpdatesStablePathsAndSafeAudit(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	input := testReplaceFilesInput(workspace)

	result, err := fixture.service.ReplaceFiles(
		context.Background(),
		Actor{UserID: 7, RequestID: "req-structured-rename"},
		workspace.ID,
		input,
	)
	if err != nil {
		t.Fatalf("ReplaceFiles() error = %v", err)
	}
	wantPaths := []RelativePath{"conf.d/site.conf", "nginx.conf"}
	if result.Workspace.Revision != workspace.Revision+1 ||
		!reflect.DeepEqual(result.ChangedPaths, wantPaths) ||
		result.Workspace.ETag() == workspace.ETag() {
		t.Fatalf("ReplaceFiles() = %#v", result)
	}
	assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "server { listen 8081; }\n")
	assertFileContent(
		t,
		fixture.path(workspace.ID, "draft/nginx.conf"),
		"events { worker_connections 512; }\nhttp { include conf.d/*.conf; }\n",
	)

	fixture.repository.mu.Lock()
	defer fixture.repository.mu.Unlock()
	for _, audit := range fixture.repository.audits {
		if audit.Action != "config.structured.upstream.rename" {
			continue
		}
		if audit.RequestID != "req-structured-rename" ||
			!strings.Contains(audit.DetailsJSON, testStructuredPreviewID) ||
			!strings.Contains(audit.DetailsJSON, testStructuredTargetID) ||
			!strings.Contains(audit.DetailsJSON, "\"path_count\":2") ||
			strings.Contains(audit.DetailsJSON, "nginx.conf") ||
			strings.Contains(audit.DetailsJSON, "listen") {
			t.Fatalf("unsafe structured audit = %#v", audit)
		}
		return
	}
	t.Fatalf("structured audit not found in %#v", fixture.repository.audits)
}

func TestReplaceFilesRejectsDuplicatePathsBeforeWriting(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	input := testReplaceFilesInput(workspace)
	input.Replacements[1].Path = input.Replacements[0].Path

	_, err := fixture.service.ReplaceFiles(
		context.Background(),
		Actor{UserID: 7, RequestID: "req-structured-duplicate"},
		workspace.ID,
		input,
	)
	if !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("ReplaceFiles() error = %v, want ErrPathInvalid", err)
	}
	assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "server { listen 8080; }\n")
	assertFileContent(t, fixture.path(workspace.ID, "draft/nginx.conf"), "events {}\nhttp { include conf.d/*.conf; }\n")
	assertNoFileAudit(t, fixture.repository, "config.structured.upstream.rename")
}

func TestReconcileReplaceFilesRestoresWholeBatchAfterPartialFilesystemApply(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	fixture.service.mutationHook = func(checkpoint string) error {
		if checkpoint == "after_filesystem_replace_000000" {
			return errors.New("injected batch crash")
		}
		return nil
	}

	_, err := fixture.service.ReplaceFiles(
		context.Background(),
		Actor{UserID: 7, RequestID: "req-structured-crash"},
		workspace.ID,
		testReplaceFilesInput(workspace),
	)
	if err == nil || !strings.Contains(err.Error(), "injected batch crash") {
		t.Fatalf("ReplaceFiles() error = %v, want injected crash", err)
	}
	fixture.service.mutationHook = nil
	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	got := fixture.mustWorkspace(t, workspace.ID)
	if got.State != StateReady || got.Revision != workspace.Revision ||
		got.DraftDigest != workspace.DraftDigest {
		t.Fatalf("workspace after rollback = %#v, want %#v", got, workspace)
	}
	assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "server { listen 8080; }\n")
	assertFileContent(t, fixture.path(workspace.ID, "draft/nginx.conf"), "events {}\nhttp { include conf.d/*.conf; }\n")
}

func TestReconcileReplaceFilesFinalizesCommittedBatch(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	fixture.service.mutationHook = func(checkpoint string) error {
		if checkpoint == "after_database" {
			return errors.New("injected batch crash")
		}
		return nil
	}

	_, err := fixture.service.ReplaceFiles(
		context.Background(),
		Actor{UserID: 7, RequestID: "req-structured-committed"},
		workspace.ID,
		testReplaceFilesInput(workspace),
	)
	if err == nil || !strings.Contains(err.Error(), "injected batch crash") {
		t.Fatalf("ReplaceFiles() error = %v, want injected crash", err)
	}
	fixture.service.mutationHook = nil
	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	got := fixture.mustWorkspace(t, workspace.ID)
	if got.State != StateReady || got.Revision != workspace.Revision+1 ||
		got.DraftDigest == workspace.DraftDigest {
		t.Fatalf("workspace after finalize = %#v", got)
	}
	assertFileContent(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "server { listen 8081; }\n")
	assertFileContent(
		t,
		fixture.path(workspace.ID, "draft/nginx.conf"),
		"events { worker_connections 512; }\nhttp { include conf.d/*.conf; }\n",
	)
	if _, err := os.Lstat(fixture.path(workspace.ID, "control/journal.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains after reconciliation: %v", err)
	}
}

func testReplaceFilesInput(workspace Workspace) ReplaceFilesInput {
	return ReplaceFilesInput{
		Replacements: []FileReplacement{
			{
				Path: "nginx.conf",
				Content: []byte(
					"events { worker_connections 512; }\nhttp { include conf.d/*.conf; }\n",
				),
			},
			{Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n")},
		},
		IfMatch:       workspace.ETag(),
		OperationKind: "upstream.rename",
		PreviewID:     testStructuredPreviewID,
		TargetID:      testStructuredTargetID,
	}
}
