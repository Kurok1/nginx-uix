/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStartupStateWriteUsesAtomicDurableSequenceAndMode(t *testing.T) {
	root := t.TempDir()
	recorder := newRecordingStartupStateFS()
	store := newStartupStateStore(root)
	store.fileSystem = recorder
	checkedAt := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	state := StartupState{Validation: &StartupValidation{Valid: true, CheckedAt: checkedAt, Diagnostic: "syntax is ok"}}

	if err := store.Write(context.Background(), state); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	information, err := os.Lstat(filepath.Join(root, startupStateFilename))
	if err != nil {
		t.Fatalf("Lstat(target) error = %v", err)
	}
	if got, want := information.Mode().Perm(), fs.FileMode(0o600); got != want {
		t.Fatalf("target mode = %04o, want %04o", got, want)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(root) error = %v", err)
	}
	if recorder.tempDirectory != canonicalRoot {
		t.Fatalf("temp directory = %q, want canonical same directory %q", recorder.tempDirectory, canonicalRoot)
	}
	assertOperationOrder(t, recorder.operations,
		"create-temp", "chmod-0600", "file-sync", "file-close", "rename", "open-directory", "directory-sync", "directory-close",
	)
	assertNoStartupTemps(t, root)

	stored, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if stored.Validation == nil || !stored.Validation.Valid || stored.Validation.CheckedAt != checkedAt {
		t.Fatalf("stored state = %+v, want validation snapshot", stored)
	}
}

func TestStartupStateWriteRejectsSymlinksAndPathEscape(t *testing.T) {
	t.Run("target symlink", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.json")
		if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
			t.Fatalf("WriteFile(outside) error = %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(root, startupStateFilename)); err != nil {
			t.Fatalf("Symlink(target) error = %v", err)
		}
		store := newStartupStateStore(root)
		if err := store.Write(context.Background(), StartupState{}); err == nil {
			t.Fatal("Write() error = nil, want target symlink rejection")
		}
		contents, err := os.ReadFile(outside)
		if err != nil || string(contents) != "unchanged" {
			t.Fatalf("outside contents = %q, %v; want unchanged", contents, err)
		}
		assertNoStartupTemps(t, root)
	})

	t.Run("root symlink", func(t *testing.T) {
		realRoot := t.TempDir()
		linkedRoot := filepath.Join(t.TempDir(), "runtime")
		if err := os.Symlink(realRoot, linkedRoot); err != nil {
			t.Fatalf("Symlink(root) error = %v", err)
		}
		if err := newStartupStateStore(linkedRoot).Write(context.Background(), StartupState{}); err == nil {
			t.Fatal("Write() error = nil, want root symlink rejection")
		}
	})

	t.Run("escaped target", func(t *testing.T) {
		root := t.TempDir()
		store := newStartupStateStore(root)
		store.target = filepath.Join(root, "..", "escaped.json")
		if err := store.Write(context.Background(), StartupState{}); err == nil {
			t.Fatal("Write() error = nil, want path escape rejection")
		}
		if _, err := os.Lstat(filepath.Clean(store.target)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("escaped target exists or Lstat failed unexpectedly: %v", err)
		}
	})
}

func TestStartupStateWriteCleansTemporaryFileOnEncodeSyncRenameAndCancelErrors(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*startupStateStore, *recordingStartupStateFS, context.CancelFunc)
		wantOp    string
	}{
		{
			name: "encode error",
			configure: func(store *startupStateStore, _ *recordingStartupStateFS, _ context.CancelFunc) {
				store.encode = func(io.Writer, *StartupState) error { return errors.New("encode failed") }
			},
			wantOp: "remove-temp",
		},
		{
			name: "file sync error",
			configure: func(_ *startupStateStore, recorder *recordingStartupStateFS, _ context.CancelFunc) {
				recorder.fileSyncError = errors.New("sync failed")
			},
			wantOp: "remove-temp",
		},
		{
			name: "rename error",
			configure: func(_ *startupStateStore, recorder *recordingStartupStateFS, _ context.CancelFunc) {
				recorder.renameError = errors.New("rename failed")
			},
			wantOp: "remove-temp",
		},
		{
			name: "cancellation after encode",
			configure: func(store *startupStateStore, _ *recordingStartupStateFS, cancel context.CancelFunc) {
				store.encode = func(writer io.Writer, state *StartupState) error {
					err := json.NewEncoder(writer).Encode(state)
					cancel()
					return err
				}
			},
			wantOp: "remove-temp",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			recorder := newRecordingStartupStateFS()
			store := newStartupStateStore(root)
			store.fileSystem = recorder
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			test.configure(store, recorder, cancel)

			if err := store.Write(ctx, StartupState{}); err == nil {
				t.Fatal("Write() error = nil, want injected failure")
			}
			if !slices.Contains(recorder.operations, test.wantOp) {
				t.Fatalf("operations = %#v, want %q", recorder.operations, test.wantOp)
			}
			assertNoStartupTemps(t, root)
			if _, err := os.Lstat(filepath.Join(root, startupStateFilename)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("target exists after failed write or Lstat failed: %v", err)
			}
		})
	}
}

func TestStartupStateWriteReportsDirectorySyncAndLeavesNoTemporaryFile(t *testing.T) {
	root := t.TempDir()
	recorder := newRecordingStartupStateFS()
	recorder.directorySyncError = errors.New("directory sync failed")
	store := newStartupStateStore(root)
	store.fileSystem = recorder

	if err := store.Write(context.Background(), StartupState{}); err == nil {
		t.Fatal("Write() error = nil, want directory sync failure")
	}
	if !slices.Contains(recorder.operations, "directory-sync") {
		t.Fatalf("operations = %#v, want directory sync attempt", recorder.operations)
	}
	assertNoStartupTemps(t, root)
}

func TestStartupStateWriteCapsAndSanitizesDiagnosticWithoutMutatingCaller(t *testing.T) {
	root := t.TempDir()
	diagnostic := strings.Repeat("界", startupDiagnosticLimit/3+10) + "\x00secret"
	state := StartupState{Validation: &StartupValidation{Valid: true, Diagnostic: diagnostic}}
	store := newStartupStateStore(root)

	if err := store.Write(context.Background(), state); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	stored, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if stored.Validation == nil {
		t.Fatal("stored validation = nil")
	}
	if got := len([]byte(stored.Validation.Diagnostic)); got > startupDiagnosticLimit {
		t.Fatalf("stored diagnostic bytes = %d, want <= %d", got, startupDiagnosticLimit)
	}
	if !utf8.ValidString(stored.Validation.Diagnostic) || strings.ContainsRune(stored.Validation.Diagnostic, '\x00') {
		t.Fatalf("stored diagnostic is unsafe: valid=%v containsNUL=%v", utf8.ValidString(stored.Validation.Diagnostic), strings.ContainsRune(stored.Validation.Diagnostic, '\x00'))
	}
	if state.Validation.Diagnostic != diagnostic {
		t.Fatal("Write() mutated caller-owned diagnostic")
	}
}

func TestStartupStateReadAcceptsMaxDiagnosticAfterJSONEscaping(t *testing.T) {
	root := t.TempDir()
	diagnostic := strings.Repeat("<", startupDiagnosticLimit)
	store := newStartupStateStore(root)

	if err := store.Write(context.Background(), StartupState{
		Validation: &StartupValidation{Valid: true, Diagnostic: diagnostic},
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	stored, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if stored.Validation == nil || stored.Validation.Diagnostic != diagnostic {
		t.Fatalf("stored diagnostic length = %d, want %d", len(stored.Validation.Diagnostic), len(diagnostic))
	}
}

func TestStartupStateInvalidConfigurationIsImmediatelyPermanent(t *testing.T) {
	root := t.TempDir()
	exitCode := 100
	store := newStartupStateStore(root)
	state := StartupState{Validation: &StartupValidation{Valid: false, ExitCode: &exitCode, Diagnostic: "invalid"}}

	if err := store.Write(context.Background(), state); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	stored, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if stored.Recovery == nil || !stored.Recovery.Permanent || stored.Recovery.LastResult != RecoveryResultInvalidConfig {
		t.Fatalf("Recovery = %+v, want immediate invalid-config permanent state", stored.Recovery)
	}
}

func TestRecordNginxExitMaintainsFiveMinuteRollingWindow(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := newStartupStateStore(root)
	store.now = func() time.Time { return now }
	initial := StartupState{Recovery: &RecoveryState{
		Count:      3,
		LastResult: RecoveryResultRestarting,
		Events: []ExitEvent{
			{OccurredAt: now.Add(-5*time.Minute - time.Nanosecond), ExitCode: 1},
			{OccurredAt: now.Add(-5 * time.Minute), ExitCode: 2},
			{OccurredAt: now.Add(-time.Minute), ExitCode: 3},
		},
	}}
	if err := store.Write(context.Background(), initial); err != nil {
		t.Fatalf("Write(initial) error = %v", err)
	}

	recovery, err := store.RecordNginxExit(context.Background(), ExitEvent{ExitCode: 4, Signal: 9, OccurredAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatalf("RecordNginxExit() error = %v", err)
	}
	if recovery.Count != 3 || recovery.Permanent || recovery.LastResult != RecoveryResultRestarting {
		t.Fatalf("Recovery = %+v, want three current-window deaths", recovery)
	}
	if len(recovery.Events) != 3 || recovery.Events[0].ExitCode != 2 || recovery.Events[2].OccurredAt != now {
		t.Fatalf("Events = %+v, want boundary event, recent event and internally timestamped exit", recovery.Events)
	}
}

func TestRecordNginxExitMakesFifthRuntimeDeathPermanent(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := newStartupStateStore(root)
	store.now = func() time.Time { return now }
	events := make([]ExitEvent, 0, 4)
	for index := range 4 {
		events = append(events, ExitEvent{OccurredAt: now.Add(-time.Duration(4-index) * time.Second), ExitCode: index + 1})
	}
	if err := store.Write(context.Background(), StartupState{Recovery: &RecoveryState{
		Count: 4, Events: events, LastResult: RecoveryResultRestarting,
	}}); err != nil {
		t.Fatalf("Write(initial) error = %v", err)
	}

	recovery, err := store.RecordNginxExit(context.Background(), ExitEvent{ExitCode: 5})
	if err != nil {
		t.Fatalf("RecordNginxExit() error = %v", err)
	}
	if recovery.Count != 5 || !recovery.Permanent || recovery.LastResult != RecoveryResultPermanent || len(recovery.Events) != 5 {
		t.Fatalf("Recovery = %+v, want fifth death permanent state", recovery)
	}
	stored, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if stored.Recovery == nil || !stored.Recovery.Permanent {
		t.Fatalf("stored Recovery = %+v, want permanent", stored.Recovery)
	}
}

func assertNoStartupTemps(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, startupStateTempPattern))
	if err != nil {
		t.Fatalf("Glob(temp) error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary state files remain: %#v", matches)
	}
}

func assertOperationOrder(t *testing.T, operations []string, expected ...string) {
	t.Helper()
	position := 0
	for _, operation := range operations {
		if position < len(expected) && operation == expected[position] {
			position++
		}
	}
	if position != len(expected) {
		t.Fatalf("operations = %#v, want ordered subsequence %#v", operations, expected)
	}
}

type recordingStartupStateFS struct {
	operations         []string
	tempDirectory      string
	fileSyncError      error
	directorySyncError error
	renameError        error
}

func newRecordingStartupStateFS() *recordingStartupStateFS {
	return &recordingStartupStateFS{operations: make([]string, 0, 16)}
}

func (f *recordingStartupStateFS) Lstat(path string) (fs.FileInfo, error) {
	return os.Lstat(path)
}

func (f *recordingStartupStateFS) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func (f *recordingStartupStateFS) CreateTemp(directory, pattern string) (startupStateFile, error) {
	f.operations = append(f.operations, "create-temp")
	f.tempDirectory = directory
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return nil, err
	}
	return &recordingStartupStateFile{File: file, owner: f}, nil
}

func (f *recordingStartupStateFS) Open(path string) (startupStateFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	information, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	isDirectory := information.IsDir()
	if isDirectory {
		f.operations = append(f.operations, "open-directory")
	}
	return &recordingStartupStateFile{File: file, owner: f, directory: isDirectory}, nil
}

func (f *recordingStartupStateFS) Rename(oldPath, newPath string) error {
	f.operations = append(f.operations, "rename")
	if f.renameError != nil {
		return f.renameError
	}
	return os.Rename(oldPath, newPath)
}

func (f *recordingStartupStateFS) Remove(path string) error {
	f.operations = append(f.operations, "remove-temp")
	return os.Remove(path)
}

type recordingStartupStateFile struct {
	*os.File
	owner     *recordingStartupStateFS
	directory bool
}

func (f *recordingStartupStateFile) Chmod(mode fs.FileMode) error {
	if mode == 0o600 {
		f.owner.operations = append(f.owner.operations, "chmod-0600")
	}
	return f.File.Chmod(mode)
}

func (f *recordingStartupStateFile) Sync() error {
	if f.directory {
		f.owner.operations = append(f.owner.operations, "directory-sync")
		if f.owner.directorySyncError != nil {
			return f.owner.directorySyncError
		}
		return f.File.Sync()
	}
	f.owner.operations = append(f.owner.operations, "file-sync")
	if f.owner.fileSyncError != nil {
		return f.owner.fileSyncError
	}
	return f.File.Sync()
}

func (f *recordingStartupStateFile) Close() error {
	if f.directory {
		f.owner.operations = append(f.owner.operations, "directory-close")
	} else {
		f.owner.operations = append(f.owner.operations, "file-close")
	}
	return f.File.Close()
}
