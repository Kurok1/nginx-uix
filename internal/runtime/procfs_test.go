/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseProcStatHandlesProcessNamesWithSpacesAndParentheses(t *testing.T) {
	stat, err := parseProcStat([]byte("42 (nginx: worker (cache)) S 7 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1 0 1250\n"))
	if err != nil {
		t.Fatalf("parseProcStat() error = %v", err)
	}
	if got, want := stat.PID, 42; got != want {
		t.Fatalf("PID = %d, want %d", got, want)
	}
	if got, want := stat.Name, "nginx: worker (cache)"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got, want := stat.ParentPID, 7; got != want {
		t.Fatalf("ParentPID = %d, want %d", got, want)
	}
	if got, want := stat.StartTicks, uint64(1250); got != want {
		t.Fatalf("StartTicks = %d, want %d", got, want)
	}
}

func TestParseProcStatRejectsTruncatedInput(t *testing.T) {
	tests := []string{
		"",
		"42 nginx S 1",
		"42 (nginx) S 1",
		"42 (nginx S 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1 0 1250",
	}
	for _, input := range tests {
		if _, err := parseProcStat([]byte(input)); err == nil {
			t.Fatalf("parseProcStat(%q) error = nil, want rejection", input)
		}
	}
}

func TestParseProcCmdlinePreservesNULSeparatedArguments(t *testing.T) {
	arguments, err := parseProcCmdline([]byte("nginx: master process nginx\x00-g\x00daemon off;\x00"))
	if err != nil {
		t.Fatalf("parseProcCmdline() error = %v", err)
	}
	want := []string{"nginx: master process nginx", "-g", "daemon off;"}
	if !slices.Equal(arguments, want) {
		t.Fatalf("parseProcCmdline() = %#v, want %#v", arguments, want)
	}
}

func TestParseProcCmdlineRejectsEmptyAndInteriorEmptyArguments(t *testing.T) {
	for _, input := range [][]byte{nil, {}, []byte("\x00"), []byte("nginx\x00\x00worker\x00")} {
		if _, err := parseProcCmdline(input); err == nil {
			t.Fatalf("parseProcCmdline(%q) error = nil, want rejection", input)
		}
	}
}

func TestProcessSourceBuildsBoundedSnapshotAndSortsChildren(t *testing.T) {
	source := fixtureProcessSource(t)

	snapshot, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	master, ok := snapshot.Processes[100]
	if !ok {
		t.Fatal("master PID 100 missing from snapshot")
	}
	if got, want := master.Executable, nginxExecutable; got != want {
		t.Fatalf("master executable = %q, want %q", got, want)
	}
	if got, want := master.StartedAt, time.Unix(1_700_000_000, 0).UTC().Add(10*time.Second); !got.Equal(want) {
		t.Fatalf("master StartedAt = %s, want %s", got, want)
	}

	children := snapshot.Children(100)
	gotPIDs := make([]int, 0, len(children))
	for _, child := range children {
		gotPIDs = append(gotPIDs, child.PID)
	}
	if want := []int{101, 103}; !slices.Equal(gotPIDs, want) {
		t.Fatalf("child PIDs = %#v, want %#v", gotPIDs, want)
	}
	if _, exists := snapshot.Processes[999]; exists {
		t.Fatal("stale PID 999 retained after stat disappeared")
	}
	if _, exists := snapshot.Processes[0]; exists {
		t.Fatal("nonnumeric proc entry was read as a process")
	}
}

func TestProcessSourceRetainsUnreadableExecutableAsPartialEvidence(t *testing.T) {
	source := fixtureProcessSource(t)
	source.readlink = func(path string) (string, error) {
		if strings.HasSuffix(path, filepath.Join("103", "exe")) {
			return "", fs.ErrPermission
		}
		return readFixtureExecutable(path)
	}

	snapshot, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	worker := snapshot.Processes[103]
	if !errors.Is(worker.ExecutableError, fs.ErrPermission) {
		t.Fatalf("worker ExecutableError = %v, want permission error", worker.ExecutableError)
	}
	if worker.ParentPID != 100 {
		t.Fatalf("worker ParentPID = %d, want 100", worker.ParentPID)
	}
}

func TestProcessSourceHonorsCancellationBeforeAndDuringEnumeration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixtureProcessSource(t).Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot(canceled) error = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	source := fixtureProcessSource(t)
	reads := 0
	source.readFile = func(ctx context.Context, path string, limit int64) ([]byte, error) {
		reads++
		if reads == 2 {
			cancel()
		}
		return readBoundedFile(ctx, path, limit)
	}
	if _, err := source.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot(canceled during loop) error = %v, want context.Canceled", err)
	}
}

func fixtureProcessSource(t *testing.T) *procFS {
	t.Helper()
	source := &procFS{
		root:     filepath.Join("testdata", "proc", "running"),
		readlink: readFixtureExecutable,
	}
	defaultRead := source.readFile
	source.readFile = func(ctx context.Context, path string, limit int64) ([]byte, error) {
		if strings.HasSuffix(path, filepath.Join("999", "stat")) {
			return nil, fs.ErrNotExist
		}
		if defaultRead != nil {
			return defaultRead(ctx, path, limit)
		}
		return readBoundedFile(ctx, path, limit)
	}
	return source
}

func readFixtureExecutable(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(contents)), nil
}
