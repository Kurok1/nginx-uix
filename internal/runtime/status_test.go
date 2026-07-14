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

func TestStatusClassifiesVerifiedNginxProcessStates(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	master := statusProcess(100, 1, "nginx: master process nginx", now.Add(-time.Hour))
	worker := statusProcess(101, 100, "nginx: worker process", now.Add(-59*time.Minute))
	unreadableWorker := statusProcess(102, 100, "nginx: worker process", now.Add(-58*time.Minute))
	unreadableWorker.Executable = ""
	unreadableWorker.ExecutableError = fs.ErrPermission
	secondMaster := statusProcess(200, 1, "nginx: master process nginx", now.Add(-30*time.Minute))
	foreignReuse := statusProcess(100, 1, "unrelated", now.Add(-time.Minute))
	foreignReuse.Executable = "/usr/bin/unrelated"
	wrongRole := statusProcess(200, 100, "nginx: worker process", now.Add(-time.Minute))

	tests := []struct {
		name        string
		processes   map[int]processRecord
		unreadable  map[int]error
		pidFiles    map[string]pidResult
		snapshotErr error
		wantState   State
		wantMaster  *int
		wantWorkers []int
	}{
		{
			name:        "verified master and worker are running",
			processes:   processMap(master, worker),
			pidFiles:    statusPIDFiles(100, 100),
			wantState:   StateRunning,
			wantMaster:  intPointer(100),
			wantWorkers: []int{101},
		},
		{
			name:       "verified master without worker is degraded",
			processes:  processMap(master),
			pidFiles:   statusPIDFiles(100, 100),
			wantState:  StateDegraded,
			wantMaster: intPointer(100),
		},
		{
			name:        "one unreadable direct worker is degraded",
			processes:   processMap(master, worker, unreadableWorker),
			pidFiles:    statusPIDFiles(100, 100),
			wantState:   StateDegraded,
			wantMaster:  intPointer(100),
			wantWorkers: []int{101},
		},
		{
			name:      "completed checks without candidates are stopped",
			processes: processMap(),
			pidFiles:  statusPIDFiles(0, 0),
			wantState: StateStopped,
		},
		{
			name:      "pid file alone never proves running",
			processes: processMap(),
			pidFiles:  statusPIDFiles(100, 100),
			wantState: StateStopped,
		},
		{
			name:      "stale reused pid belonging to foreign executable is stopped",
			processes: processMap(foreignReuse),
			pidFiles:  statusPIDFiles(100, 100),
			wantState: StateStopped,
		},
		{
			name:       "permission failure is unknown",
			processes:  processMap(),
			unreadable: map[int]error{100: fs.ErrPermission},
			pidFiles:   statusPIDFiles(100, 100),
			wantState:  StateUnknown,
		},
		{
			name:        "process source timeout is unknown",
			snapshotErr: context.DeadlineExceeded,
			pidFiles:    statusPIDFiles(100, 100),
			wantState:   StateUnknown,
		},
		{
			name:      "two conflicting verified masters are unknown",
			processes: processMap(master, secondMaster),
			pidFiles:  statusPIDFiles(100, 200),
			wantState: StateUnknown,
		},
		{
			name:      "supervisor pointing at an nginx worker contradicts master source",
			processes: processMap(master, wrongRole),
			pidFiles:  statusPIDFiles(100, 200),
			wantState: StateUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := statusService(t, now, processSnapshot{
				Processes:  test.processes,
				Unreadable: test.unreadable,
			}, test.snapshotErr, test.pidFiles, nil)

			status, err := service.Status(context.Background())
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if status.State != test.wantState {
				t.Fatalf("State = %q, want %q (issues: %#v)", status.State, test.wantState, status.Issues)
			}
			if test.wantMaster == nil {
				if status.Master != nil {
					t.Fatalf("Master = %+v, want nil", status.Master)
				}
			} else if status.Master == nil || status.Master.PID != *test.wantMaster {
				t.Fatalf("Master = %+v, want PID %d", status.Master, *test.wantMaster)
			}
			workerPIDs := make([]int, 0, len(status.Workers))
			for _, gotWorker := range status.Workers {
				workerPIDs = append(workerPIDs, gotWorker.PID)
			}
			if !slices.Equal(workerPIDs, test.wantWorkers) {
				t.Fatalf("worker PIDs = %#v, want %#v", workerPIDs, test.wantWorkers)
			}
			if status.SampledAt != now {
				t.Fatalf("SampledAt = %s, want %s", status.SampledAt, now)
			}
		})
	}
}

func TestStatusSortsWorkersAndRejectsInvalidWorkerIdentity(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	master := statusProcess(100, 1, "nginx: master process nginx", now.Add(-time.Hour))
	worker103 := statusProcess(103, 100, "nginx: worker process", now.Add(-50*time.Minute))
	worker101 := statusProcess(101, 100, "nginx: worker process", now.Add(-55*time.Minute))
	foreignChild := statusProcess(104, 100, "nginx: worker process", now.Add(-40*time.Minute))
	foreignChild.Executable = "/usr/bin/not-nginx"
	olderChild := statusProcess(105, 100, "nginx: worker process", now.Add(-2*time.Hour))
	indirectWorker := statusProcess(106, 999, "nginx: worker process", now.Add(-30*time.Minute))

	service := statusService(t, now, processSnapshot{
		Processes: processMap(master, worker103, worker101, foreignChild, olderChild, indirectWorker),
	}, nil, statusPIDFiles(100, 100), nil)

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != StateDegraded {
		t.Fatalf("State = %q, want degraded for invalid direct children", status.State)
	}
	gotPIDs := []int{status.Workers[0].PID, status.Workers[1].PID}
	if want := []int{101, 103}; !slices.Equal(gotPIDs, want) {
		t.Fatalf("worker PIDs = %#v, want %#v", gotPIDs, want)
	}
	for _, worker := range status.Workers {
		if worker.Role != ProcessRoleWorker || !worker.StartedAt.After(status.Master.StartedAt) {
			t.Fatalf("invalid worker DTO = %+v for master %+v", worker, status.Master)
		}
	}
}

func TestStatusRejectsNearMatchMasterRole(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	nearMatch := statusProcess(100, 1, "nginx: master process-hijack", now.Add(-time.Hour))
	service := statusService(t, now, processSnapshot{
		Processes: processMap(nearMatch),
	}, nil, statusPIDFiles(100, 100), nil)

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != StateUnknown || status.Master != nil {
		t.Fatalf("Status() = %+v, want contradictory near-match role to be unknown", status)
	}
}

func TestStatusAcceptsWorkerStartedInSameClockTick(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-time.Hour)
	master := statusProcess(100, 1, "nginx: master process nginx", startedAt)
	worker := statusProcess(101, 100, "nginx: worker process", startedAt)
	service := statusService(t, now, processSnapshot{
		Processes: processMap(master, worker),
	}, nil, statusPIDFiles(100, 100), nil)

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != StateRunning || len(status.Workers) != 1 {
		t.Fatalf("Status() = %+v, want same-tick worker to be running", status)
	}
}

func TestStatusDoesNotTreatZombieMasterAsRunning(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	master := statusProcess(100, 1, "nginx: master process nginx", now.Add(-time.Hour))
	master.State = 'Z'
	service := statusService(t, now, processSnapshot{
		Processes: processMap(master),
	}, nil, statusPIDFiles(100, 100), nil)

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != StateStopped || status.Master != nil {
		t.Fatalf("Status() = %+v, want zombie candidate to be stopped", status)
	}
}

func TestStatusReturnsNullableEvidenceAndRecentRecoveryDegradesRunningProcess(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	exitCode := 1
	startup := &StartupState{
		Validation: &StartupValidation{
			Valid:      true,
			CheckedAt:  now.Add(-2 * time.Minute),
			ExitCode:   &exitCode,
			Diagnostic: "syntax is ok",
		},
		Recovery: &RecoveryState{
			Count:      1,
			LastResult: RecoveryResultRestarting,
			Events: []ExitEvent{{
				ExitCode:   1,
				OccurredAt: now.Add(-time.Minute),
			}},
		},
	}
	master := statusProcess(100, 1, "nginx: master process nginx", now.Add(-time.Hour))
	worker := statusProcess(101, 100, "nginx: worker process", now.Add(-59*time.Minute))
	service := statusService(t, now, processSnapshot{Processes: processMap(master, worker)}, nil, statusPIDFiles(100, 100), startup)

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != StateDegraded {
		t.Fatalf("State = %q, want degraded during recovery window", status.State)
	}
	if status.Build == nil || status.Build.Version != "1.30.3" {
		t.Fatalf("Build = %+v, want cached build information", status.Build)
	}
	if status.StartupValidation == nil || status.StartupValidation.ExitCode == nil {
		t.Fatalf("StartupValidation = %+v, want populated nullable evidence", status.StartupValidation)
	}
	if status.Recovery == nil || status.Recovery.Count != 1 {
		t.Fatalf("Recovery = %+v, want one recent event", status.Recovery)
	}
}

func TestStatusBuildFailureReturnsUnknownWithNilEvidence(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	service := newServiceWithExecutor(func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{}, fs.ErrPermission
	})
	service.now = func() time.Time { return now }

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != StateUnknown || status.Build != nil || status.Master != nil || status.StartupValidation != nil {
		t.Fatalf("Status() = %+v, want unknown with nullable unavailable evidence", status)
	}
}

func TestStatusPropagatesCallerCancellation(t *testing.T) {
	service := statusService(t, time.Now().UTC(), processSnapshot{}, nil, statusPIDFiles(0, 0), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status, err := service.Status(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Status() error = %v, want context.Canceled", err)
	}
	if status.State != StateUnknown {
		t.Fatalf("State = %q, want unknown", status.State)
	}
}

func TestStatusReadsOnlyBuildAndSupervisorPIDCandidates(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	paths := make([]string, 0, 2)
	service := statusService(t, now, processSnapshot{}, nil, statusPIDFiles(0, 0), nil)
	service.readPIDFile = func(_ context.Context, path string) (int, error) {
		paths = append(paths, path)
		return 0, fs.ErrNotExist
	}

	if _, err := service.Status(context.Background()); err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	want := []string{"/run/nginx.pid", nginxSupervisorPIDPath}
	if !slices.Equal(paths, want) {
		t.Fatalf("PID paths = %#v, want only %#v", paths, want)
	}
}

func TestReadPIDFileIsBoundedAndRejectsSymlinks(t *testing.T) {
	directory := t.TempDir()
	validPath := filepath.Join(directory, "valid.pid")
	if err := os.WriteFile(validPath, []byte("123\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(valid) error = %v", err)
	}
	pid, err := readPIDFile(context.Background(), validPath)
	if err != nil || pid != 123 {
		t.Fatalf("readPIDFile(valid) = %d, %v; want 123, nil", pid, err)
	}

	invalidPath := filepath.Join(directory, "invalid.pid")
	if err := os.WriteFile(invalidPath, []byte("123 trailing"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}
	if _, err := readPIDFile(context.Background(), invalidPath); err == nil {
		t.Fatal("readPIDFile(invalid) error = nil, want rejection")
	}

	largePath := filepath.Join(directory, "large.pid")
	if err := os.WriteFile(largePath, []byte(strings.Repeat("1", 65)), 0o600); err != nil {
		t.Fatalf("WriteFile(large) error = %v", err)
	}
	if _, err := readPIDFile(context.Background(), largePath); err == nil {
		t.Fatal("readPIDFile(large) error = nil, want size rejection")
	}

	symlinkPath := filepath.Join(directory, "linked.pid")
	if err := os.Symlink(validPath, symlinkPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if _, err := readPIDFile(context.Background(), symlinkPath); err == nil {
		t.Fatal("readPIDFile(symlink) error = nil, want rejection")
	}
}

type staticProcessSource struct {
	snapshot processSnapshot
	err      error
}

func (s staticProcessSource) Snapshot(context.Context) (processSnapshot, error) {
	return s.snapshot, s.err
}

type pidResult struct {
	pid int
	err error
}

func statusService(
	t *testing.T,
	now time.Time,
	snapshot processSnapshot,
	snapshotErr error,
	pidFiles map[string]pidResult,
	startup *StartupState,
) *Service {
	t.Helper()
	service := newServiceWithExecutor(func(context.Context, commandSpec) (commandResult, error) {
		t.Fatal("unexpected executor call with cached build info")
		return commandResult{}, errors.New("unexpected executor call")
	})
	service.cachedBuild = &BuildInfo{
		Version:            "1.30.3",
		ConfigureArguments: []string{"--sbin-path=/usr/sbin/nginx", "--pid-path=/run/nginx.pid"},
		PIDPath:            "/run/nginx.pid",
		SbinPath:           nginxExecutable,
	}
	service.processSource = staticProcessSource{snapshot: snapshot, err: snapshotErr}
	service.readPIDFile = func(_ context.Context, path string) (int, error) {
		result, ok := pidFiles[path]
		if !ok {
			t.Fatalf("unexpected PID path %q", path)
		}
		return result.pid, result.err
	}
	service.readStartupState = func(context.Context) (*StartupState, error) {
		return cloneStartupState(startup), nil
	}
	service.now = func() time.Time { return now }
	return service
}

func statusPIDFiles(buildPID, supervisorPID int) map[string]pidResult {
	results := map[string]pidResult{
		"/run/nginx.pid":       {pid: buildPID},
		nginxSupervisorPIDPath: {pid: supervisorPID},
	}
	if buildPID == 0 {
		results["/run/nginx.pid"] = pidResult{err: fs.ErrNotExist}
	}
	if supervisorPID == 0 {
		results[nginxSupervisorPIDPath] = pidResult{err: fs.ErrNotExist}
	}
	return results
}

func statusProcess(pid, parentPID int, firstArgument string, startedAt time.Time) processRecord {
	return processRecord{
		PID:        pid,
		Name:       "nginx",
		State:      'S',
		ParentPID:  parentPID,
		StartedAt:  startedAt,
		Executable: nginxExecutable,
		Arguments:  []string{firstArgument},
	}
}

func processMap(processes ...processRecord) map[int]processRecord {
	result := make(map[int]processRecord, len(processes))
	for _, process := range processes {
		result[process.PID] = process
	}
	return result
}

func intPointer(value int) *int {
	return &value
}
