/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestControlStateRoundTripUsesStrictVersionedAtomicRecord(t *testing.T) {
	root := newControlTestRoot(t)
	want := ControlState{
		SchemaVersion:   ControlSchemaVersion,
		WorkspaceID:     WorkspaceID("11111111111111111111111111111111"),
		State:           StatePreparing,
		StateReasonCode: "",
		Revision:        1,
		UpdatedAt:       time.Date(2026, time.July, 16, 1, 2, 3, 456789, time.FixedZone("offset", 8*60*60)),
	}
	if err := WriteControlState(context.Background(), root, want); err != nil {
		t.Fatalf("WriteControlState() error = %v", err)
	}
	got, err := ReadControlState(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadControlState() error = %v", err)
	}
	want.UpdatedAt = want.UpdatedAt.UTC()
	if got != want {
		t.Fatalf("ReadControlState() = %#v, want %#v", got, want)
	}
	assertControlFileMode(t, root.path, "control/state.json", 0o600)

	unknown := []byte(`{"schema_version":1,"workspace_id":"11111111111111111111111111111111","state":"ready","state_reason_code":"","revision":1,"updated_at":"2026-07-16T00:00:00Z","extra":true}`)
	if err := root.AtomicReplace(context.Background(), "control/state.json", unknown, 0o600); err != nil {
		t.Fatalf("replace state fixture: %v", err)
	}
	if _, err := ReadControlState(context.Background(), root); err == nil {
		t.Fatal("ReadControlState(unknown field) error = nil")
	}

	duplicate := []byte(`{"schema_version":1,"schema_version":1,"workspace_id":"11111111111111111111111111111111","state":"ready","state_reason_code":"","revision":1,"updated_at":"2026-07-16T00:00:00Z"}`)
	if err := root.AtomicReplace(context.Background(), "control/state.json", duplicate, 0o600); err != nil {
		t.Fatalf("replace duplicate state fixture: %v", err)
	}
	if _, err := ReadControlState(context.Background(), root); err == nil {
		t.Fatal("ReadControlState(duplicate field) error = nil")
	}
}

func TestControlManifestRoundTripUsesBoundedAtomicRecord(t *testing.T) {
	root := newControlTestRoot(t)
	want := testManifest([]Entry{{
		Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText,
		Mode: 0o644, Size: 1, ContentDigest: digestOf("n"),
	}})
	if err := WriteControlManifest(context.Background(), root, want); err != nil {
		t.Fatalf("WriteControlManifest() error = %v", err)
	}
	got, err := ReadControlManifest(context.Background(), root, DefaultLimits())
	if err != nil {
		t.Fatalf("ReadControlManifest() error = %v", err)
	}
	if got.Digest() != want.Digest() {
		t.Fatalf("manifest digest = %s, want %s", got.Digest(), want.Digest())
	}
	assertControlFileMode(t, root.path, "control/manifest.bin", 0o600)
}

func newControlTestRoot(t *testing.T) *ScopedRoot {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(path, "control"), 0o700); err != nil {
		t.Fatalf("Mkdir(control) error = %v", err)
	}
	root, err := OpenScopedRoot(path)
	if err != nil {
		t.Fatalf("OpenScopedRoot() error = %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return root
}

func assertControlFileMode(t *testing.T, rootPath, relative string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(rootPath, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("Lstat(%s) error = %v", relative, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != want {
		t.Fatalf("%s mode = %v, want regular %04o", relative, info.Mode(), want)
	}
}
