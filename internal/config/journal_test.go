/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestJournalRoundTripPhasesAndRemoval(t *testing.T) {
	root := newJournalTestRoot(t)
	journal := testJournal(t)
	for _, phase := range []JournalPhase{
		JournalPrepared,
		JournalFilesApplied,
		JournalSQLCommitted,
		JournalControlCommitted,
	} {
		journal.Phase = phase
		if err := WriteJournal(context.Background(), root, journal); err != nil {
			t.Fatalf("WriteJournal(%s) error = %v", phase, err)
		}
		got, err := ReadJournal(context.Background(), root)
		if err != nil {
			t.Fatalf("ReadJournal(%s) error = %v", phase, err)
		}
		if !reflect.DeepEqual(got, journal) {
			t.Fatalf("ReadJournal(%s) = %#v, want %#v", phase, got, journal)
		}
	}
	if err := RemoveJournal(context.Background(), root); err != nil {
		t.Fatalf("RemoveJournal() error = %v", err)
	}
	if _, err := ReadJournal(context.Background(), root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadJournal(removed) error = %v, want not exist", err)
	}
	if err := RemoveJournal(context.Background(), root); err != nil {
		t.Fatalf("RemoveJournal(absent) error = %v", err)
	}
}

func TestJournalRejectsMalformedOrUnsafeRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Journal)
	}{
		{name: "schema", mutate: func(journal *Journal) { journal.SchemaVersion++ }},
		{name: "operation", mutate: func(journal *Journal) { journal.OperationID = "not-opaque" }},
		{name: "phase", mutate: func(journal *Journal) { journal.Phase = "unknown" }},
		{name: "revision", mutate: func(journal *Journal) { journal.NextRevision++ }},
		{name: "actor", mutate: func(journal *Journal) { journal.Actor.RequestID = "bad request" }},
		{name: "paths", mutate: func(journal *Journal) { journal.Paths = append(journal.Paths, "third.conf") }},
		{name: "time", mutate: func(journal *Journal) { journal.StartedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newJournalTestRoot(t)
			journal := testJournal(t)
			test.mutate(&journal)
			if err := WriteJournal(context.Background(), root, journal); err == nil {
				t.Fatal("WriteJournal() error = nil")
			}
			if _, err := os.Lstat(filepath.Join(root.path, "control", "journal.json")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("journal was written after rejection: %v", err)
			}
		})
	}
}

func TestJournalReadRejectsDuplicateFieldsAndWrongMode(t *testing.T) {
	root := newJournalTestRoot(t)
	journal := testJournal(t)
	if err := WriteJournal(context.Background(), root, journal); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root.path, "control", "journal.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := strings.Replace(string(payload), `{"schema_version":1`, `{"schema_version":1,"schema_version":1`, 1)
	if err := os.WriteFile(path, []byte(duplicate), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadJournal(context.Background(), root); err == nil {
		t.Fatal("ReadJournal(duplicate) error = nil")
	}
	if err := WriteJournal(context.Background(), root, journal); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadJournal(context.Background(), root); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("ReadJournal(wrong mode) error = %v, want ErrPathInvalid", err)
	}
}

func TestJournalWorkspaceDeleteRequiresUnchangedDigest(t *testing.T) {
	root := newJournalTestRoot(t)
	journal := testJournal(t)
	journal.Kind = "workspace_delete"
	journal.Paths = nil
	if err := WriteJournal(context.Background(), root, journal); err == nil {
		t.Fatal("WriteJournal(workspace delete with changed digest) error = nil")
	}
	journal.AfterDigest = journal.BeforeDigest
	if err := WriteJournal(context.Background(), root, journal); err != nil {
		t.Fatalf("WriteJournal(workspace delete) error = %v", err)
	}
}

func newJournalTestRoot(t *testing.T) *ScopedRoot {
	t.Helper()
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "control"), 0o700); err != nil {
		t.Fatal(err)
	}
	return openTestScopedRoot(t, path)
}

func testJournal(t *testing.T) Journal {
	t.Helper()
	workspaceID, err := ParseWorkspaceID("0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatal(err)
	}
	first := testDigestByte(0x11)
	second := testDigestByte(0x22)
	return Journal{
		SchemaVersion:    JournalSchemaVersion,
		OperationID:      "1112131415161718191a1b1c1d1e1f20",
		Kind:             "rename",
		Phase:            JournalPrepared,
		WorkspaceID:      workspaceID,
		ExpectedRevision: 4,
		NextRevision:     5,
		BeforeDigest:     first,
		AfterDigest:      second,
		Actor:            Actor{UserID: 7, RequestID: "req-journal"},
		Paths:            []RelativePath{mustRelativePath(t, "conf.d/old.conf"), mustRelativePath(t, "conf.d/new.conf")},
		StartedAt:        time.Date(2026, time.July, 16, 4, 5, 6, 7, time.UTC),
	}
}

func testDigestByte(value byte) Digest {
	var digest Digest
	for index := range digest {
		digest[index] = value
	}
	return digest
}
