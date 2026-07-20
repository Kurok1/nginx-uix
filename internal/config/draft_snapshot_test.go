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
	"testing"
)

func TestDraftSnapshotReturnsOneVerifiedCanonicalView(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)

	snapshot, err := fixture.service.DraftSnapshot(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("DraftSnapshot() error = %v", err)
	}
	if snapshot.Workspace != workspace || snapshot.WorkspaceETag != workspace.ETag() {
		t.Fatalf("snapshot identity = %#v, want workspace %#v", snapshot, workspace)
	}
	wantFiles := []DraftFile{
		{
			Path: "conf.d/site.conf", Content: []byte("server { listen 8080; }\n"),
			ContentDigest: mustEntry(t, snapshot.Entries, "conf.d/site.conf").ContentDigest, LineEnding: "lf",
		},
		{
			Path: "nginx.conf", Content: []byte("events {}\nhttp { include conf.d/*.conf; }\n"),
			ContentDigest: mustEntry(t, snapshot.Entries, "nginx.conf").ContentDigest, LineEnding: "lf",
		},
	}
	if !reflect.DeepEqual(snapshot.Files, wantFiles) {
		t.Fatalf("snapshot files = %#v, want %#v", snapshot.Files, wantFiles)
	}
	if len(snapshot.Dependencies) != 1 || snapshot.Dependencies[0].Source != "nginx.conf" ||
		snapshot.Dependencies[0].Target != "conf.d/site.conf" {
		t.Fatalf("snapshot dependencies = %#v", snapshot.Dependencies)
	}

	snapshot.Files[0].Content[0] = 'X'
	second, err := fixture.service.DraftSnapshot(context.Background(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(second.Files[0].Content) != "server { listen 8080; }\n" {
		t.Fatalf("snapshot content aliases caller memory: %q", second.Files[0].Content)
	}
}

func TestDraftSnapshotRejectsDraftTampering(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	if err := os.WriteFile(fixture.path(workspace.ID, "draft/conf.d/site.conf"), []byte("server { listen 9000; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.DraftSnapshot(context.Background(), workspace.ID)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("DraftSnapshot() error = %v, want ErrConflict", err)
	}
}

func mustEntry(t *testing.T, entries []Entry, path RelativePath) Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Path == path {
			return entry
		}
	}
	t.Fatalf("entry %q not found in %#v", path, entries)
	return Entry{}
}
