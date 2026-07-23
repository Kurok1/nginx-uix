/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	fixedSupervisorExecutable  = "/command/s6-svc"
	fixedNginxServiceDirectory = "/run/service/nginx"
	fixedExpectedExitMarker    = "/run/nginx-uix/nginx-restart-expected"
	restartCommandTimeout      = 15 * time.Second
	restartConfirmLimit        = 45 * time.Second
	restartOutputLimit         = 1 << 20
	restartJournalLimit        = 64 << 10
	restartEventsLimit         = 2 << 20
	restartEventLimit          = 128
)

type restartOptions struct {
	NginxRoot          string
	RestartRoot        string
	Entry              config.RelativePath
	Limits             config.Limits
	Executor           commandExecutor
	Status             func(context.Context) (Status, error)
	Probe              func(context.Context) (int, error)
	ConfirmTimeout     time.Duration
	ExpectedExitMarker string
}

type restartJournal struct {
	SchemaVersion    uint16                  `json:"schema_version"`
	RestartID        config.RestartID        `json:"restart_id"`
	ProductionDigest config.Digest           `json:"production_digest"`
	Stage            config.RestartStageName `json:"stage"`
	BeforeMasterPID  int                     `json:"before_master_pid"`
	BeforeWorkerPIDs []int                   `json:"before_worker_pids"`
	AfterMasterPID   int                     `json:"after_master_pid"`
	WorkerCount      int                     `json:"worker_count"`
	HTTPStatus       int                     `json:"http_status"`
	ErrorCode        string                  `json:"error_code"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

func defaultRestartOptions() restartOptions {
	return restartOptions{
		NginxRoot: defaultConfigNginxRoot, RestartRoot: "/var/lib/nginx-uix/restarts",
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Executor: executeCommand,
		ConfirmTimeout: restartConfirmLimit, ExpectedExitMarker: fixedExpectedExitMarker,
	}
}

func newRestartService(options restartOptions) (*Service, error) {
	options = normalizeRestartOptions(options)
	if err := validateRestartOptions(options); err != nil {
		return nil, err
	}
	service := newServiceWithExecutor(options.Executor)
	service.restart = options
	service.configSnapshot.NginxRoot = options.NginxRoot
	service.configSnapshot.Entry = options.Entry
	service.configSnapshot.Limits = options.Limits
	return service, nil
}

func validateRestartOptions(options restartOptions) error {
	if options.Executor == nil || options.ConfirmTimeout <= 0 || options.ConfirmTimeout > restartConfirmLimit {
		return errors.New("configure restart: invalid dependencies")
	}
	for _, root := range []string{options.NginxRoot, options.RestartRoot} {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("configure restart root: %w", config.ErrPathInvalid)
		}
		information, err := os.Lstat(root)
		if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
			return errors.Join(fmt.Errorf("configure restart root: %w", config.ErrPathInvalid), err)
		}
	}
	if options.NginxRoot == options.RestartRoot {
		return fmt.Errorf("configure restart roots: %w", config.ErrPathInvalid)
	}
	if options.ExpectedExitMarker == "" || !filepath.IsAbs(options.ExpectedExitMarker) ||
		filepath.Clean(options.ExpectedExitMarker) != options.ExpectedExitMarker {
		return fmt.Errorf("configure restart marker: %w", config.ErrPathInvalid)
	}
	markerRoot := filepath.Dir(options.ExpectedExitMarker)
	information, err := os.Lstat(markerRoot)
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
		return errors.Join(fmt.Errorf("configure restart marker: %w", config.ErrPathInvalid), err)
	}
	canonicalMarkerRoot, err := filepath.EvalSymlinks(markerRoot)
	if err != nil || filepath.Clean(canonicalMarkerRoot) != markerRoot {
		return errors.Join(fmt.Errorf("configure restart marker: %w", config.ErrPathInvalid), err)
	}
	if _, err := config.ParseRelativePath(string(options.Entry), options.Limits); err != nil {
		return err
	}
	return nil
}

// ExecuteRestart performs only the fixed s6 restart and proves the replacement runtime healthy.
func (s *Service) ExecuteRestart(
	ctx context.Context,
	request config.RestartExecutionRequest,
) (config.RestartExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.RestartExecutionResult{}, errors.New("execute restart: service is unavailable")
	}
	if err := validateRestartExecutionRequest(request); err != nil {
		return config.RestartExecutionResult{}, err
	}
	select {
	case s.releaseLock <- struct{}{}:
		defer func() { <-s.releaseLock }()
	default:
		return config.RestartExecutionResult{}, config.ErrReleaseInProgress
	}
	options := s.restart
	if options.NginxRoot == "" {
		options = defaultRestartOptions()
	}
	if err := validateRestartOptions(options); err != nil {
		return config.RestartExecutionResult{}, err
	}
	controlPath, err := createRestartControlPath(options.RestartRoot, request.RestartID)
	if err != nil {
		return config.RestartExecutionResult{}, err
	}
	journal := restartJournal{SchemaVersion: 1, RestartID: request.RestartID, ProductionDigest: request.ProductionDigest}
	result := config.RestartExecutionResult{RestartID: request.RestartID, State: config.RestartStateRunning}
	appendStage := restartStageAppender(s, controlPath, &journal, &result)
	fail := func(state config.RestartState, code string, primary error) (config.RestartExecutionResult, error) {
		stage := config.RestartStageFailed
		if state == config.RestartStateNeedsAttention {
			stage = config.RestartStageNeedsAttention
		}
		journal.ErrorCode = code
		_ = appendStage(stage, config.StageResultFailed, code)
		result.State = state
		result.ErrorCode = code
		result.FinishedAt = s.currentTime()
		return result, primary
	}

	if err := appendStage(config.RestartStageProductionValidating, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	production, err := s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.ProductionDigest {
		return fail(config.RestartStateFailed, "production_changed", errors.Join(config.ErrSnapshotChanged, err))
	}
	if err := validateRestartProduction(ctx, options); err != nil {
		return fail(config.RestartStateFailed, "restart_config_invalid", err)
	}
	if err := appendStage(config.RestartStageProductionValidating, config.StageResultSuccess, ""); err != nil {
		return result, err
	}
	if err := appendStage(config.RestartStageRuntimeSampling, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	baseline, err := restartStatus(ctx, s, options)
	if err != nil || !validRestartBaseline(baseline) {
		return fail(config.RestartStateFailed, "restart_runtime_invalid", errors.Join(err, ErrConfigInvalid))
	}
	if baseline.Master != nil {
		journal.BeforeMasterPID = baseline.Master.PID
	}
	journal.BeforeWorkerPIDs = releaseWorkerPIDs(baseline.Workers)
	result.BeforeMasterPID = journal.BeforeMasterPID
	if err := appendStage(config.RestartStageRuntimeSampling, config.StageResultSuccess, ""); err != nil {
		return result, err
	}
	if err := appendStage(config.RestartStageRestartRequested, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	if err := createExpectedRestartMarker(ctx, options.ExpectedExitMarker, request.RestartID); err != nil {
		return fail(config.RestartStateFailed, "restart_marker_failed", err)
	}
	_, commandErr := options.Executor(ctx, commandSpec{
		executable: fixedSupervisorExecutable, arguments: []string{"-r", fixedNginxServiceDirectory},
		timeout: restartCommandTimeout, maxOutputBytes: restartOutputLimit,
		allowedExitCodes: map[int]struct{}{0: {}},
	})
	if commandErr != nil {
		commandErr = errors.Join(commandErr, removeExpectedRestartMarker(options.ExpectedExitMarker))
		var exitErr *commandExitError
		if errors.As(commandErr, &exitErr) {
			return fail(config.RestartStateFailed, "restart_supervisor_failed", commandErr)
		}
		return fail(config.RestartStateNeedsAttention, "restart_supervisor_uncertain", commandErr)
	}
	if err := appendStage(config.RestartStageRestartRequested, config.StageResultSuccess, ""); err != nil {
		return fail(config.RestartStateNeedsAttention, "restart_journal_failed", err)
	}
	if err := appendStage(config.RestartStageRuntimeConfirming, config.StageResultRunning, ""); err != nil {
		return fail(config.RestartStateNeedsAttention, "restart_journal_failed", err)
	}
	confirmed, httpStatus, err := s.confirmRestartRuntime(ctx, options, request, journal.BeforeMasterPID)
	if err != nil {
		return fail(config.RestartStateNeedsAttention, "restart_runtime_unconfirmed",
			errors.Join(err, removeExpectedRestartMarker(options.ExpectedExitMarker)))
	}
	if err := removeExpectedRestartMarker(options.ExpectedExitMarker); err != nil {
		return fail(config.RestartStateNeedsAttention, "restart_marker_cleanup_failed", err)
	}
	result.AfterMasterPID = confirmed.Master.PID
	result.WorkerCount = len(confirmed.Workers)
	result.HTTPStatus = httpStatus
	journal.AfterMasterPID = result.AfterMasterPID
	journal.WorkerCount = result.WorkerCount
	journal.HTTPStatus = result.HTTPStatus
	if err := appendStage(config.RestartStageSucceeded, config.StageResultSuccess, ""); err != nil {
		return fail(config.RestartStateNeedsAttention, "restart_journal_failed", err)
	}
	result.State = config.RestartStateSucceeded
	result.FinishedAt = s.currentTime()
	return result, nil
}

// RestartProgress returns only durable fixed-restart evidence.
func (s *Service) RestartProgress(
	ctx context.Context,
	request config.RestartExecutionRequest,
) (config.RestartExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.RestartExecutionResult{}, errors.New("read restart progress: service is unavailable")
	}
	if err := validateRestartExecutionRequest(request); err != nil {
		return config.RestartExecutionResult{}, err
	}
	options := s.restart
	if options.RestartRoot == "" {
		options = defaultRestartOptions()
	}
	journal, stages, err := readRestartEvidence(ctx,
		filepath.Join(options.RestartRoot, string(request.RestartID), "control"), request)
	if err != nil {
		return config.RestartExecutionResult{}, err
	}
	return restartResultFromEvidence(journal, stages), nil
}

// RecoverRestart reconciles an interrupted accepted restart from durable runtime evidence.
func (s *Service) RecoverRestart(
	ctx context.Context,
	request config.RestartExecutionRequest,
) (config.RestartExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.RestartExecutionResult{}, errors.New("recover restart: service is unavailable")
	}
	if err := validateRestartExecutionRequest(request); err != nil {
		return config.RestartExecutionResult{}, err
	}
	select {
	case s.releaseLock <- struct{}{}:
		defer func() { <-s.releaseLock }()
	case <-ctx.Done():
		return config.RestartExecutionResult{}, ctx.Err()
	}
	options := s.restart
	if options.NginxRoot == "" {
		options = defaultRestartOptions()
	}
	if err := validateRestartOptions(options); err != nil {
		return config.RestartExecutionResult{}, err
	}
	controlPath := filepath.Join(options.RestartRoot, string(request.RestartID), "control")
	journal, stages, err := readRestartEvidence(ctx, controlPath, request)
	if err != nil {
		return uncertainRestartResult(request.RestartID, "restart_journal_invalid"), err
	}
	result := restartResultFromEvidence(journal, stages)
	if terminalRuntimeRestartState(result.State) {
		return result, nil
	}
	appendStage := restartStageAppender(s, controlPath, &journal, &result)
	if restartStageOrder(journal.Stage) < restartStageOrder(config.RestartStageRestartRequested) {
		if err := appendStage(config.RestartStageFailed, config.StageResultFailed, "interrupted_before_restart"); err != nil {
			return uncertainRestartResult(request.RestartID, "restart_recovery_journal_failed"), err
		}
		result.State = config.RestartStateFailed
		result.ErrorCode = "interrupted_before_restart"
		result.FinishedAt = s.currentTime()
		return result, errors.New("restart interrupted before supervisor request")
	}
	confirmed, httpStatus, confirmErr := s.confirmRestartRuntime(ctx, options, request, journal.BeforeMasterPID)
	if confirmErr == nil {
		result.BeforeMasterPID = journal.BeforeMasterPID
		result.AfterMasterPID = confirmed.Master.PID
		result.WorkerCount = len(confirmed.Workers)
		result.HTTPStatus = httpStatus
		journal.AfterMasterPID = result.AfterMasterPID
		journal.WorkerCount = result.WorkerCount
		journal.HTTPStatus = result.HTTPStatus
		if err := appendStage(config.RestartStageSucceeded, config.StageResultSuccess, "recovered"); err != nil {
			return uncertainRestartResult(request.RestartID, "restart_recovery_journal_failed"), err
		}
		result.State = config.RestartStateSucceeded
		result.FinishedAt = s.currentTime()
		return result, nil
	}
	journal.ErrorCode = "restart_runtime_unconfirmed"
	_ = appendStage(config.RestartStageNeedsAttention, config.StageResultFailed, journal.ErrorCode)
	result.State = config.RestartStateNeedsAttention
	result.ErrorCode = "restart_runtime_unconfirmed"
	result.FinishedAt = s.currentTime()
	return result, confirmErr
}

func validateRestartExecutionRequest(request config.RestartExecutionRequest) error {
	if _, err := config.ParseRestartID(string(request.RestartID)); err != nil {
		return err
	}
	if request.ProductionDigest == (config.Digest{}) {
		return config.ErrDigestInvalid
	}
	return nil
}

func normalizeRestartOptions(options restartOptions) restartOptions {
	if options.ExpectedExitMarker == "" && options.RestartRoot != "" {
		options.ExpectedExitMarker = filepath.Join(options.RestartRoot, ".nginx-restart-expected")
	}
	if options.ExpectedExitMarker != "" {
		markerRoot := filepath.Dir(options.ExpectedExitMarker)
		if canonicalRoot, err := filepath.EvalSymlinks(markerRoot); err == nil {
			options.ExpectedExitMarker = filepath.Join(canonicalRoot, filepath.Base(options.ExpectedExitMarker))
		}
	}
	return options
}

func createExpectedRestartMarker(ctx context.Context, path string, id config.RestartID) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	// #nosec G304 -- path is the fixed marker in a canonical, non-symlink process root validated by validateRestartOptions.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create expected nginx exit marker: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			returnErr = errors.Join(returnErr, file.Close(), removeExpectedRestartMarker(path))
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure expected nginx exit marker: %w", err)
	}
	if _, err := file.WriteString(string(id) + "\n"); err != nil {
		return fmt.Errorf("write expected nginx exit marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync expected nginx exit marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close expected nginx exit marker: %w", err)
	}
	owned = false
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync expected nginx exit marker directory: %w", err)
	}
	return nil
}

func removeExpectedRestartMarker(path string) error {
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("remove expected nginx exit marker: %w", err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync expected nginx exit marker directory: %w", err)
	}
	return nil
}

func validateRestartProduction(ctx context.Context, options restartOptions) error {
	result, err := options.Executor(ctx, commandSpec{
		executable: nginxExecutable,
		arguments:  []string{"-t", "-c", filepath.Join(options.NginxRoot, filepath.FromSlash(string(options.Entry)))},
		timeout:    startupValidationTimeout, maxOutputBytes: startupDiagnosticLimit,
		allowedExitCodes: map[int]struct{}{0: {}},
	})
	if err == nil {
		return nil
	}
	var exitErr *commandExitError
	if errors.As(err, &exitErr) {
		return errors.Join(ErrConfigInvalid,
			fmt.Errorf("restart production validation failed: %s", sanitizeDiagnostic(result.stderr)))
	}
	return err
}

func restartStatus(ctx context.Context, service *Service, options restartOptions) (Status, error) {
	if options.Status != nil {
		return options.Status(ctx)
	}
	return service.Status(ctx)
}

func validRestartBaseline(status Status) bool {
	switch status.State {
	case StateRunning, StateDegraded:
		return status.Master != nil && status.Master.PID > 0
	case StateStopped:
		return status.Master == nil && len(status.Workers) == 0
	case StateUnknown:
		return false
	default:
		return false
	}
}

func (s *Service) confirmRestartRuntime(
	ctx context.Context,
	options restartOptions,
	request config.RestartExecutionRequest,
	beforeMasterPID int,
) (Status, int, error) {
	timeout := options.ConfirmTimeout
	if timeout <= 0 || timeout > restartConfirmLimit {
		timeout = restartConfirmLimit
	}
	confirmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	stableSamples := 0
	stableWorkers := []int(nil)
	var lastErr error
	for {
		status, err := restartStatus(confirmCtx, s, options)
		if err == nil && status.State == StateRunning && status.Master != nil &&
			status.Master.PID > 0 && status.Master.PID != beforeMasterPID && len(status.Workers) > 0 {
			workers := releaseWorkerPIDs(status.Workers)
			if slices.Equal(workers, stableWorkers) {
				stableSamples++
			} else {
				stableWorkers = workers
				stableSamples = 1
			}
			if stableSamples >= 2 {
				production, digestErr := s.ConfigDigest(confirmCtx)
				validationErr := validateRestartProduction(confirmCtx, options)
				httpStatus, probeErr := restartProbe(confirmCtx, options)
				if digestErr == nil && production.Digest == request.ProductionDigest &&
					validationErr == nil && probeErr == nil {
					return status, httpStatus, nil
				}
				lastErr = errors.Join(digestErr, validationErr, probeErr, config.ErrSnapshotChanged)
			}
		} else {
			lastErr = errors.Join(err, ErrConfigInvalid)
		}
		select {
		case <-confirmCtx.Done():
			return Status{}, 0, errors.Join(lastErr, confirmCtx.Err())
		case <-ticker.C:
		}
	}
}

func restartProbe(ctx context.Context, options restartOptions) (int, error) {
	var status int
	var err error
	if options.Probe != nil {
		status, err = options.Probe(ctx)
	} else {
		status, err = fixedLoopbackProbe(ctx)
	}
	if err != nil {
		return 0, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return status, errors.New("restart loopback health returned non-success status")
	}
	return status, nil
}

func createRestartControlPath(root string, id config.RestartID) (string, error) {
	taskPath := filepath.Join(root, string(id))
	if err := os.Mkdir(taskPath, 0o700); err != nil {
		return "", fmt.Errorf("create restart journal: %w", err)
	}
	controlPath := filepath.Join(taskPath, "control")
	if err := os.Mkdir(controlPath, 0o700); err != nil {
		return "", fmt.Errorf("create restart journal control: %w", err)
	}
	return controlPath, nil
}

func restartStageAppender(
	service *Service,
	controlPath string,
	journal *restartJournal,
	result *config.RestartExecutionResult,
) func(config.RestartStageName, config.StageResult, string) error {
	return func(stage config.RestartStageName, stageResult config.StageResult, code string) error {
		journal.Stage = stage
		journal.UpdatedAt = service.currentTime()
		if err := writeRestartJournal(controlPath, *journal); err != nil {
			return err
		}
		event := config.RestartStage{
			RestartID: journal.RestartID, Sequence: uint64(len(result.Stages) + 1), Stage: stage,
			Result: stageResult, Code: code, PublicDetailsJSON: "{}", OccurredAt: journal.UpdatedAt,
		}
		if err := appendRestartEvent(controlPath, event); err != nil {
			return err
		}
		result.Stage = stage
		result.Stages = append(result.Stages, event)
		return nil
	}
}

func writeRestartJournal(controlPath string, journal restartJournal) error {
	payload, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return writeAtomicSyncedFile(controlPath, "state.json", append(payload, '\n'), 0o600)
}

func appendRestartEvent(controlPath string, stage config.RestartStage) error {
	payload, err := json.Marshal(stage)
	if err != nil {
		return err
	}
	path := filepath.Join(controlPath, "events.ndjson")
	// #nosec G304 -- controlPath is an Agent-owned 0700 task directory derived from a parsed restart ID.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return errors.Join(err, file.Close())
	}
	return errors.Join(file.Sync(), file.Close(), syncDirectory(controlPath))
}

func readRestartEvidence(
	ctx context.Context,
	controlPath string,
	request config.RestartExecutionRequest,
) (_ restartJournal, _ []config.RestartStage, returnErr error) {
	root, err := config.OpenScopedRoot(controlPath)
	if err != nil {
		return restartJournal{}, nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	journalPayload, _, err := root.ReadRegular(ctx, "state.json", restartJournalLimit)
	if err != nil {
		return restartJournal{}, nil, err
	}
	var journal restartJournal
	if err := decodeReleaseJSON(journalPayload, &journal); err != nil || journal.SchemaVersion != 1 ||
		journal.RestartID != request.RestartID || journal.ProductionDigest != request.ProductionDigest ||
		restartStageOrder(journal.Stage) < 0 || journal.UpdatedAt.IsZero() {
		return restartJournal{}, nil, errors.New("restart journal identity is invalid")
	}
	eventsPayload, _, err := root.ReadRegular(ctx, "events.ndjson", restartEventsLimit)
	if err != nil {
		return restartJournal{}, nil, err
	}
	stages, err := decodeRestartEvents(eventsPayload, request.RestartID)
	if err != nil || len(stages) == 0 || stages[len(stages)-1].Stage != journal.Stage {
		return restartJournal{}, nil, errors.Join(errors.New("restart events are invalid"), err)
	}
	return journal, stages, nil
}

func decodeRestartEvents(payload []byte, id config.RestartID) ([]config.RestartStage, error) {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 4<<10), restartJournalLimit)
	stages := make([]config.RestartStage, 0, 12)
	lastOrder := -1
	for scanner.Scan() {
		if len(stages) >= restartEventLimit {
			return nil, config.ErrLimitExceeded
		}
		var stage config.RestartStage
		if err := decodeReleaseJSON(scanner.Bytes(), &stage); err != nil {
			return nil, err
		}
		order := restartStageOrder(stage.Stage)
		if stage.RestartID != id || stage.Sequence != uint64(len(stages)+1) || order < 0 ||
			order < lastOrder || !json.Valid([]byte(stage.PublicDetailsJSON)) || stage.OccurredAt.IsZero() {
			return nil, errors.New("restart event sequence is invalid")
		}
		stages = append(stages, stage)
		lastOrder = order
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return stages, nil
}

func restartResultFromEvidence(journal restartJournal, stages []config.RestartStage) config.RestartExecutionResult {
	result := config.RestartExecutionResult{
		RestartID: journal.RestartID, State: restartStateForStage(journal.Stage), Stage: journal.Stage,
		Stages: stages, BeforeMasterPID: journal.BeforeMasterPID, AfterMasterPID: journal.AfterMasterPID,
		WorkerCount: journal.WorkerCount, HTTPStatus: journal.HTTPStatus, ErrorCode: journal.ErrorCode,
	}
	if terminalRuntimeRestartState(result.State) {
		result.FinishedAt = journal.UpdatedAt
		if result.ErrorCode == "" && len(stages) > 0 {
			result.ErrorCode = stages[len(stages)-1].Code
		}
	}
	return result
}

func restartStateForStage(stage config.RestartStageName) config.RestartState {
	switch stage {
	case config.RestartStageQueued:
		return config.RestartStateQueued
	case config.RestartStageProductionValidating, config.RestartStageRuntimeSampling,
		config.RestartStageRestartRequested, config.RestartStageRuntimeConfirming:
		return config.RestartStateRunning
	case config.RestartStageSucceeded:
		return config.RestartStateSucceeded
	case config.RestartStageFailed:
		return config.RestartStateFailed
	case config.RestartStageNeedsAttention:
		return config.RestartStateNeedsAttention
	default:
		return config.RestartStateNeedsAttention
	}
}

func restartStageOrder(stage config.RestartStageName) int {
	switch stage {
	case config.RestartStageQueued:
		return 0
	case config.RestartStageProductionValidating:
		return 1
	case config.RestartStageRuntimeSampling:
		return 2
	case config.RestartStageRestartRequested:
		return 3
	case config.RestartStageRuntimeConfirming:
		return 4
	case config.RestartStageSucceeded:
		return 5
	case config.RestartStageFailed:
		return 10
	case config.RestartStageNeedsAttention:
		return 11
	default:
		return -1
	}
}

func terminalRuntimeRestartState(state config.RestartState) bool {
	switch state {
	case config.RestartStateSucceeded, config.RestartStateFailed,
		config.RestartStateNeedsAttention, config.RestartStateCancelled:
		return true
	case config.RestartStateQueued, config.RestartStateRunning:
		return false
	default:
		return false
	}
}

func uncertainRestartResult(id config.RestartID, code string) config.RestartExecutionResult {
	return config.RestartExecutionResult{
		RestartID: id, State: config.RestartStateNeedsAttention,
		Stage: config.RestartStageNeedsAttention, ErrorCode: code,
	}
}
