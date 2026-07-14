/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	nginxSupervisorPIDPath = "/run/service/nginx/supervise/pid"
	pidFileLimit           = 64
	recoveryWindow         = 5 * time.Minute

	statusIssueBuildUnavailable   = "NGINX_BUILD_UNAVAILABLE"
	statusIssueBuildContradiction = "NGINX_BUILD_CONTRADICTION"
	statusIssuePIDUnreadable      = "NGINX_PID_SOURCE_UNREADABLE"
	statusIssueProcessUnreadable  = "NGINX_PROCESS_UNREADABLE"
	statusIssueSourceConflict     = "NGINX_PROCESS_SOURCES_CONFLICT"
	statusIssueWorkerMissing      = "NGINX_WORKER_MISSING"
	statusIssueWorkerInvalid      = "NGINX_WORKER_INVALID"
	statusIssueRecoveryActive     = "NGINX_RECOVERY_ACTIVE"
	statusIssueStartupUnreadable  = "NGINX_STARTUP_STATE_UNREADABLE"
)

// Status reads a fresh bounded process snapshot and cross-checks both fixed PID sources.
func (s *Service) Status(ctx context.Context) (Status, error) {
	status := Status{
		SampledAt: s.currentTime(),
		State:     StateUnknown,
		Workers:   make([]NginxProcess, 0),
		Issues:    make([]string, 0),
	}
	if err := ctx.Err(); err != nil {
		return status, fmt.Errorf("inspect nginx status: %w", err)
	}

	build, err := s.BuildInfo(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return status, fmt.Errorf("inspect nginx status: %w", ctx.Err())
		}
		status.Issues = append(status.Issues, statusIssueBuildUnavailable)
		return finishStatus(status), nil
	}
	status.Build = &build
	if build.SbinPath != nginxExecutable {
		status.Issues = append(status.Issues, statusIssueBuildContradiction)
		return finishStatus(status), nil
	}

	uncertain := false
	if s.readStartupState != nil {
		startup, startupErr := s.readStartupState(ctx)
		if startupErr != nil && !errors.Is(startupErr, fs.ErrNotExist) {
			if ctx.Err() != nil {
				return status, fmt.Errorf("inspect nginx status: %w", ctx.Err())
			}
			uncertain = true
			status.Issues = append(status.Issues, statusIssueStartupUnreadable)
		} else if startup != nil {
			startup = cloneStartupState(startup)
			status.StartupValidation = startup.Validation
			status.Recovery = startup.Recovery
		}
	}

	snapshot, err := s.processSource.Snapshot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return status, fmt.Errorf("inspect nginx status: %w", ctx.Err())
		}
		status.Issues = append(status.Issues, statusIssueProcessUnreadable)
		return finishStatus(status), nil
	}

	candidates := make(map[int]struct{}, 2)
	for _, path := range []string{build.PIDPath, nginxSupervisorPIDPath} {
		if err := ctx.Err(); err != nil {
			return status, fmt.Errorf("inspect nginx status: %w", err)
		}
		pid, readErr := s.readPIDFile(ctx, path)
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			if ctx.Err() != nil {
				return status, fmt.Errorf("inspect nginx status: %w", ctx.Err())
			}
			uncertain = true
			status.Issues = append(status.Issues, statusIssuePIDUnreadable)
			continue
		}
		if pid <= 0 {
			uncertain = true
			status.Issues = append(status.Issues, statusIssuePIDUnreadable)
			continue
		}
		candidates[pid] = struct{}{}
	}

	candidatePIDs := make([]int, 0, len(candidates))
	for pid := range candidates {
		candidatePIDs = append(candidatePIDs, pid)
	}
	sort.Ints(candidatePIDs)
	verifiedMasters := make([]processRecord, 0, len(candidatePIDs))
	for _, pid := range candidatePIDs {
		if err := ctx.Err(); err != nil {
			return status, fmt.Errorf("inspect nginx status: %w", err)
		}
		if _, exists := snapshot.Unreadable[pid]; exists {
			uncertain = true
			status.Issues = append(status.Issues, statusIssueProcessUnreadable)
			continue
		}
		process, exists := snapshot.Processes[pid]
		if !exists {
			continue
		}
		classification := classifyMasterCandidate(process)
		switch classification {
		case masterCandidateVerified:
			verifiedMasters = append(verifiedMasters, process)
		case masterCandidateContradictory:
			uncertain = true
			status.Issues = append(status.Issues, statusIssueSourceConflict)
		case masterCandidateUnreadable:
			uncertain = true
			status.Issues = append(status.Issues, statusIssueProcessUnreadable)
		case masterCandidateForeign:
			// A foreign executable is a stale, reused PID and is not Nginx evidence.
		}
	}

	if uncertain || len(verifiedMasters) > 1 {
		if len(verifiedMasters) > 1 {
			status.Issues = append(status.Issues, statusIssueSourceConflict)
		}
		return finishStatus(status), nil
	}
	if len(verifiedMasters) == 0 {
		status.State = StateStopped
		return finishStatus(status), nil
	}

	master := verifiedMasters[0]
	status.Master = &NginxProcess{PID: master.PID, Role: ProcessRoleMaster, StartedAt: master.StartedAt}
	degraded := false
	for _, child := range snapshot.Children(master.PID) {
		if err := ctx.Err(); err != nil {
			return status, fmt.Errorf("inspect nginx status: %w", err)
		}
		if child.ExecutableError != nil || child.CmdlineError != nil {
			degraded = true
			status.Issues = append(status.Issues, statusIssueWorkerInvalid)
			continue
		}
		if child.Executable != nginxExecutable {
			degraded = true
			status.Issues = append(status.Issues, statusIssueWorkerInvalid)
			continue
		}
		if isAuxiliaryNginxProcess(child.Arguments) {
			continue
		}
		if !hasProcessRole(child.Arguments, "nginx: worker process") || child.StartedAt.Before(master.StartedAt) {
			degraded = true
			status.Issues = append(status.Issues, statusIssueWorkerInvalid)
			continue
		}
		status.Workers = append(status.Workers, NginxProcess{
			PID:       child.PID,
			Role:      ProcessRoleWorker,
			StartedAt: child.StartedAt,
		})
	}
	if len(status.Workers) == 0 {
		degraded = true
		status.Issues = append(status.Issues, statusIssueWorkerMissing)
	}
	if status.StartupValidation != nil && !status.StartupValidation.Valid {
		status.Master = nil
		status.Workers = nil
		status.Issues = append(status.Issues, statusIssueSourceConflict)
		return finishStatus(status), nil
	}
	if status.Recovery != nil {
		if status.Recovery.Permanent {
			status.Master = nil
			status.Workers = nil
			status.Issues = append(status.Issues, statusIssueSourceConflict)
			return finishStatus(status), nil
		}
		for _, event := range status.Recovery.Events {
			if !event.OccurredAt.Before(status.SampledAt.Add(-recoveryWindow)) && !event.OccurredAt.After(status.SampledAt) {
				degraded = true
				status.Issues = append(status.Issues, statusIssueRecoveryActive)
				break
			}
		}
	}
	if degraded {
		status.State = StateDegraded
	} else {
		status.State = StateRunning
	}
	return finishStatus(status), nil
}

type masterCandidateClassification uint8

const (
	masterCandidateForeign masterCandidateClassification = iota
	masterCandidateVerified
	masterCandidateContradictory
	masterCandidateUnreadable
)

func classifyMasterCandidate(process processRecord) masterCandidateClassification {
	if process.ExecutableError != nil || process.CmdlineError != nil {
		return masterCandidateUnreadable
	}
	if isTerminatedProcess(process.State) {
		return masterCandidateForeign
	}
	if process.Executable != nginxExecutable {
		return masterCandidateForeign
	}
	if process.Name != "nginx" {
		return masterCandidateContradictory
	}
	if hasProcessRole(process.Arguments, "nginx: master process") {
		return masterCandidateVerified
	}
	return masterCandidateContradictory
}

func isTerminatedProcess(state byte) bool {
	return state == 'Z' || state == 'X' || state == 'x'
}

func hasProcessRole(arguments []string, prefix string) bool {
	return len(arguments) > 0 && (arguments[0] == prefix || strings.HasPrefix(arguments[0], prefix+" "))
}

func isAuxiliaryNginxProcess(arguments []string) bool {
	return hasProcessRole(arguments, "nginx: cache manager process") ||
		hasProcessRole(arguments, "nginx: cache loader process")
}

func readPIDFile(ctx context.Context, path string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("read nginx pid: %w", err)
	}
	information, err := os.Lstat(path)
	if err != nil {
		return 0, fmt.Errorf("read nginx pid: %w", err)
	}
	if information.Mode()&os.ModeSymlink != 0 || !information.Mode().IsRegular() {
		return 0, fmt.Errorf("read nginx pid: regular file without symlinks is required")
	}
	contents, err := readBoundedFile(ctx, path, pidFileLimit)
	if err != nil {
		return 0, fmt.Errorf("read nginx pid: %w", err)
	}
	value := strings.TrimSpace(string(contents))
	if value == "" || strings.IndexFunc(value, func(character rune) bool {
		return character < '0' || character > '9'
	}) >= 0 {
		return 0, fmt.Errorf("read nginx pid: invalid decimal pid")
	}
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("read nginx pid: invalid decimal pid")
	}
	return pid, nil
}

func (s *Service) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func finishStatus(status Status) Status {
	sort.Strings(status.Issues)
	status.Issues = slices.Compact(status.Issues)
	if status.Workers == nil {
		status.Workers = make([]NginxProcess, 0)
	}
	return status
}

func cloneStartupState(state *StartupState) *StartupState {
	if state == nil {
		return nil
	}
	clone := &StartupState{}
	if state.Validation != nil {
		validation := *state.Validation
		if state.Validation.ExitCode != nil {
			exitCode := *state.Validation.ExitCode
			validation.ExitCode = &exitCode
		}
		clone.Validation = &validation
	}
	if state.Recovery != nil {
		recovery := *state.Recovery
		recovery.Events = append([]ExitEvent(nil), state.Recovery.Events...)
		clone.Recovery = &recovery
	}
	return clone
}
