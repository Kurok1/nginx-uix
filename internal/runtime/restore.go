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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	restoreOperationTimeout = 5 * time.Minute
	restoreJournalLimit     = 64 << 10
	restoreEventsLimit      = 4 << 20
	restoreEventLimit       = 512
)

type restoreOptions struct {
	NginxRoot      string
	BackupRoot     string
	RestoreRoot    string
	Entry          config.RelativePath
	Limits         config.Limits
	Executor       commandExecutor
	Status         func(context.Context) (Status, error)
	Probe          func(context.Context) (int, error)
	ConfirmTimeout time.Duration
}

type restoreJournal struct {
	SchemaVersion    uint16                  `json:"schema_version"`
	RestoreID        config.RestoreID        `json:"restore_id"`
	TargetBackupID   config.BackupID         `json:"target_backup_id"`
	SafetyBackupID   config.BackupID         `json:"safety_backup_id"`
	SourceDigest     config.Digest           `json:"source_digest"`
	TargetDigest     config.Digest           `json:"target_digest"`
	TargetTreeDigest config.Digest           `json:"target_tree_digest"`
	SafetyTreeDigest config.Digest           `json:"safety_tree_digest"`
	Stage            config.RestoreStageName `json:"stage"`
	DurableOperation int                     `json:"durable_operation"`
	MasterPID        int                     `json:"master_pid"`
	WorkerPIDs       []int                   `json:"worker_pids"`
	SafetyBackup     config.BackupEvidence   `json:"safety_backup"`
	ErrorCode        string                  `json:"error_code"`
	MasterAfter      int                     `json:"master_after"`
	WorkerCount      int                     `json:"worker_count"`
	HTTPStatus       int                     `json:"http_status"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

func defaultRestoreOptions() restoreOptions {
	return restoreOptions{
		NginxRoot: defaultConfigNginxRoot, BackupRoot: "/var/lib/nginx-uix/backups",
		RestoreRoot: "/var/lib/nginx-uix/restores", Entry: "nginx.conf",
		Limits: config.DefaultLimits(), Executor: executeCommand, ConfirmTimeout: runtimeConfirmLimit,
	}
}

func newRestoreService(options restoreOptions) (*Service, error) {
	if err := validateRestoreOptions(options); err != nil {
		return nil, err
	}
	service := newServiceWithExecutor(options.Executor)
	service.restore = options
	service.backup = backupOptions{NginxRoot: options.NginxRoot, BackupRoot: options.BackupRoot, Limits: options.Limits}
	service.configSnapshot.NginxRoot = options.NginxRoot
	service.configSnapshot.Entry = options.Entry
	service.configSnapshot.Limits = options.Limits
	return service, nil
}

func validateRestoreOptions(options restoreOptions) error {
	if options.Executor == nil || options.ConfirmTimeout <= 0 || options.ConfirmTimeout > runtimeConfirmLimit {
		return errors.New("configure restore: invalid dependencies")
	}
	seen := make(map[string]struct{}, 3)
	for _, root := range []string{options.NginxRoot, options.BackupRoot, options.RestoreRoot} {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("configure restore root: %w", config.ErrPathInvalid)
		}
		if _, duplicate := seen[root]; duplicate {
			return fmt.Errorf("configure restore roots: %w", config.ErrPathInvalid)
		}
		seen[root] = struct{}{}
		information, err := os.Lstat(root)
		if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
			return errors.Join(fmt.Errorf("configure restore root: %w", config.ErrPathInvalid), err)
		}
	}
	if _, err := config.ParseRelativePath(string(options.Entry), options.Limits); err != nil {
		return err
	}
	return nil
}

// PrepareRestore verifies and isolates the target, samples runtime, and creates a verified safety backup without writing production.
func (s *Service) PrepareRestore(
	ctx context.Context,
	request config.RestoreExecutionRequest,
) (config.RestorePreparationResult, error) {
	if ctx == nil || s == nil {
		return config.RestorePreparationResult{}, errors.New("prepare restore: service is unavailable")
	}
	if err := validateRestoreExecutionRequest(request, false); err != nil {
		return config.RestorePreparationResult{}, err
	}
	select {
	case s.releaseLock <- struct{}{}:
		defer func() { <-s.releaseLock }()
	default:
		return config.RestorePreparationResult{}, config.ErrReleaseInProgress
	}
	options := s.restore
	if options.NginxRoot == "" {
		options = defaultRestoreOptions()
	}
	if err := validateRestoreOptions(options); err != nil {
		return config.RestorePreparationResult{}, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, restoreOperationTimeout)
	defer cancel()
	controlPath, validationPath, err := createRestorePaths(options.RestoreRoot, request.RestoreID)
	if err != nil {
		return config.RestorePreparationResult{}, err
	}
	defer func() { _ = os.RemoveAll(validationPath) }()
	journal := restoreJournal{
		SchemaVersion: 1, RestoreID: request.RestoreID, TargetBackupID: request.TargetBackupID,
		SafetyBackupID: request.SafetyBackupID, SourceDigest: request.SourceDigest,
		TargetDigest: request.TargetDigest, TargetTreeDigest: request.TargetTreeDigest,
	}
	result := config.RestorePreparationResult{RestoreID: request.RestoreID, State: config.RestoreStateRunning}
	appendStage := restorePreparationAppender(s, controlPath, &journal, &result)
	fail := func(code string, primary error) (config.RestorePreparationResult, error) {
		journal.ErrorCode = code
		_ = appendStage(config.RestoreStageFailed, config.StageResultFailed, code)
		result.State = config.RestoreStateFailed
		result.ErrorCode = code
		result.FinishedAt = s.currentTime()
		return result, primary
	}
	if err := appendStage(config.RestoreStageTargetVerifying, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	target, err := s.VerifyBackup(operationCtx, request.TargetBackupID)
	if err != nil || !restoreTargetEvidenceMatches(request, target) {
		return fail("restore_target_invalid", errors.Join(config.ErrSnapshotChanged, err))
	}
	if err := s.validateRestoreTarget(operationCtx, options, target, validationPath); err != nil {
		return fail("restore_target_invalid", err)
	}
	if err := appendStage(config.RestoreStageTargetValidated, config.StageResultSuccess, ""); err != nil {
		return result, err
	}
	production, err := s.ConfigDigest(operationCtx)
	if err != nil || production.Digest != request.SourceDigest {
		return fail("restore_source_changed", errors.Join(config.ErrSnapshotChanged, err))
	}
	status, err := restoreStatus(operationCtx, s, options)
	if err != nil || status.State != StateRunning || status.Master == nil || len(status.Workers) == 0 {
		return fail("restore_runtime_unavailable", errors.Join(err, ErrConfigInvalid))
	}
	if _, err := restoreProbe(operationCtx, options); err != nil {
		return fail("restore_runtime_unavailable", err)
	}
	journal.MasterPID = status.Master.PID
	journal.WorkerPIDs = releaseWorkerPIDs(status.Workers)
	if err := appendStage(config.RestoreStageSafetyBackupCreating, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	safety, err := s.CreateRestoreBackup(operationCtx, config.RestoreBackupRequest{
		RestoreID: request.RestoreID, BackupID: request.SafetyBackupID, ProductionDigest: request.SourceDigest,
	})
	if err != nil {
		return fail("restore_safety_backup_failed", err)
	}
	journal.SafetyBackup = safety
	journal.SafetyTreeDigest = safety.TreeDigest
	result.SafetyBackup = safety
	if err := appendStage(config.RestoreStageSafetyBackupVerified, config.StageResultSuccess, ""); err != nil {
		return fail("restore_journal_failed", err)
	}
	return result, nil
}

// ExecuteRestore applies a previously prepared target and rolls back to the indexed safety backup on failure.
func (s *Service) ExecuteRestore(
	ctx context.Context,
	request config.RestoreExecutionRequest,
) (config.RestoreExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.RestoreExecutionResult{}, errors.New("execute restore: service is unavailable")
	}
	if err := validateRestoreExecutionRequest(request, true); err != nil {
		return config.RestoreExecutionResult{}, err
	}
	select {
	case s.releaseLock <- struct{}{}:
		defer func() { <-s.releaseLock }()
	default:
		return config.RestoreExecutionResult{}, config.ErrReleaseInProgress
	}
	options := s.restore
	if options.NginxRoot == "" {
		options = defaultRestoreOptions()
	}
	if err := validateRestoreOptions(options); err != nil {
		return config.RestoreExecutionResult{}, err
	}
	controlPath := filepath.Join(options.RestoreRoot, string(request.RestoreID), "control")
	journal, stages, err := readRestoreEvidence(ctx, controlPath, request)
	if err != nil || journal.Stage != config.RestoreStageSafetyBackupVerified {
		return uncertainRestoreResult(request.RestoreID, "restore_journal_invalid"), errors.Join(err, config.ErrConflict)
	}
	result := restoreExecutionFromEvidence(journal, stages)
	appendStage := restoreExecutionAppender(s, controlPath, &journal, &result)
	failBeforeWrite := func(code string, primary error) (config.RestoreExecutionResult, error) {
		journal.ErrorCode = code
		_ = appendStage(config.RestoreStageFailed, config.StageResultFailed, code)
		result.State = config.RestoreStateFailed
		result.ErrorCode = code
		result.FinishedAt = s.currentTime()
		return result, primary
	}
	target, targetErr := s.VerifyBackup(ctx, request.TargetBackupID)
	safety, safetyErr := s.VerifyBackup(ctx, request.SafetyBackupID)
	if targetErr != nil || !restoreTargetEvidenceMatches(request, target) {
		return failBeforeWrite("restore_target_changed", errors.Join(config.ErrSnapshotChanged, targetErr))
	}
	if safetyErr != nil || !restoreSafetyEvidenceMatches(request, safety) ||
		!sameBackupEvidence(journal.SafetyBackup, safety) {
		return failBeforeWrite("restore_safety_backup_invalid", errors.Join(config.ErrSnapshotChanged, safetyErr))
	}
	result.SafetyBackup = safety
	production, err := s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.SourceDigest {
		return failBeforeWrite("restore_source_changed", errors.Join(config.ErrSnapshotChanged, err))
	}
	if err := appendStage(config.RestoreStageFilesRestoring, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	if err := s.applyVerifiedBackup(ctx, options, target); err != nil {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_apply_failed", err, false)
	}
	journal.DurableOperation = 1
	if err := appendStage(config.RestoreStageFilesRestored, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_journal_failed", err, false)
	}
	production, err = s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.TargetDigest {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_digest_failed", errors.Join(config.ErrSnapshotChanged, err), false)
	}
	if err := validateRestoreProduction(ctx, options); err != nil {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_production_invalid", err, false)
	}
	if err := appendStage(config.RestoreStageProductionValidated, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_journal_failed", err, false)
	}
	if err := restoreReload(ctx, options); err != nil {
		_ = appendStage(config.RestoreStageReloadRequested, config.StageResultFailed, "restore_reload_failed")
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_reload_failed", err, true)
	}
	if err := appendStage(config.RestoreStageReloadRequested, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_journal_failed", err, true)
	}
	confirmed, httpStatus, err := s.confirmRestoreRuntime(ctx, options, journal.MasterPID, journal.WorkerPIDs, true)
	if err != nil {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_runtime_unhealthy", err, true)
	}
	journal.MasterAfter = confirmed.Master.PID
	journal.WorkerCount = len(confirmed.Workers)
	journal.HTTPStatus = httpStatus
	result.MasterPID = journal.MasterAfter
	result.WorkerCount = journal.WorkerCount
	result.HTTPStatus = journal.HTTPStatus
	if err := appendStage(config.RestoreStageRuntimeConfirmed, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_journal_failed", err, true)
	}
	if err := appendStage(config.RestoreStageSucceeded, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
			"restore_journal_failed", err, true)
	}
	result.State = config.RestoreStateSucceeded
	result.FinishedAt = s.currentTime()
	return result, nil
}

// RestoreProgress returns the durable Agent projection without performing side effects.
func (s *Service) RestoreProgress(
	ctx context.Context,
	request config.RestoreExecutionRequest,
) (config.RestoreExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.RestoreExecutionResult{}, errors.New("read restore progress: service is unavailable")
	}
	if err := validateRestoreExecutionRequest(request, true); err != nil {
		return config.RestoreExecutionResult{}, err
	}
	options := s.restore
	if options.RestoreRoot == "" {
		options = defaultRestoreOptions()
	}
	journal, stages, err := readRestoreEvidence(ctx,
		filepath.Join(options.RestoreRoot, string(request.RestoreID), "control"), request)
	if err != nil {
		return config.RestoreExecutionResult{}, err
	}
	return restoreExecutionFromEvidence(journal, stages), nil
}

// RecoverRestore reconciles an interrupted restore to target, safety rollback, or needs_attention.
func (s *Service) RecoverRestore(
	ctx context.Context,
	request config.RestoreExecutionRequest,
) (config.RestoreExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.RestoreExecutionResult{}, errors.New("recover restore: service is unavailable")
	}
	if err := validateRestoreExecutionRequest(request, true); err != nil {
		return config.RestoreExecutionResult{}, err
	}
	select {
	case s.releaseLock <- struct{}{}:
		defer func() { <-s.releaseLock }()
	case <-ctx.Done():
		return config.RestoreExecutionResult{}, ctx.Err()
	}
	options := s.restore
	if options.NginxRoot == "" {
		options = defaultRestoreOptions()
	}
	if err := validateRestoreOptions(options); err != nil {
		return config.RestoreExecutionResult{}, err
	}
	controlPath := filepath.Join(options.RestoreRoot, string(request.RestoreID), "control")
	journal, stages, err := readRestoreEvidence(ctx, controlPath, request)
	if err != nil {
		return uncertainRestoreResult(request.RestoreID, "restore_journal_invalid"), err
	}
	result := restoreExecutionFromEvidence(journal, stages)
	if terminalRuntimeRestoreState(result.State) {
		return result, nil
	}
	appendStage := restoreExecutionAppender(s, controlPath, &journal, &result)
	rollbackStarted := restoreStageOrder(journal.Stage) >= restoreStageOrder(config.RestoreStageRollbackApplying)
	production, digestErr := s.ConfigDigest(ctx)
	if !rollbackStarted && digestErr == nil && production.Digest == request.TargetDigest {
		if validationErr := validateRestoreProduction(ctx, options); validationErr == nil {
			if reloadErr := restoreReload(ctx, options); reloadErr == nil {
				confirmed, httpStatus, confirmErr := s.confirmRestoreRuntime(
					ctx, options, journal.MasterPID, journal.WorkerPIDs, true,
				)
				if confirmErr == nil {
					journal.MasterAfter = confirmed.Master.PID
					journal.WorkerCount = len(confirmed.Workers)
					journal.HTTPStatus = httpStatus
					result.MasterPID = journal.MasterAfter
					result.WorkerCount = journal.WorkerCount
					result.HTTPStatus = journal.HTTPStatus
					_ = appendStage(config.RestoreStageRuntimeConfirmed, config.StageResultSuccess, "recovered")
					if err := appendStage(config.RestoreStageSucceeded, config.StageResultSuccess, "recovered"); err == nil {
						result.State = config.RestoreStateSucceeded
						result.FinishedAt = s.currentTime()
						return result, nil
					}
				}
			}
		}
	}
	if !rollbackStarted && digestErr == nil && production.Digest == request.SourceDigest &&
		journal.DurableOperation == 0 {
		journal.ErrorCode = "interrupted_before_write"
		_ = appendStage(config.RestoreStageFailed, config.StageResultFailed, journal.ErrorCode)
		result.State = config.RestoreStateFailed
		result.ErrorCode = journal.ErrorCode
		result.FinishedAt = s.currentTime()
		return result, errors.New("restore interrupted before production write")
	}
	return s.rollbackRestore(ctx, options, request, &journal, &result, appendStage,
		"interrupted_restore", errors.Join(errors.New("restore interrupted"), digestErr), true)
}

func (s *Service) rollbackRestore(
	ctx context.Context,
	options restoreOptions,
	request config.RestoreExecutionRequest,
	journal *restoreJournal,
	result *config.RestoreExecutionResult,
	appendStage func(config.RestoreStageName, config.StageResult, string) error,
	code string,
	primary error,
	reloadPossiblyRequested bool,
) (config.RestoreExecutionResult, error) {
	result.State = config.RestoreStateRollingBack
	if restoreStageOrder(result.Stage) < restoreStageOrder(config.RestoreStageRollbackApplying) {
		if err := appendStage(config.RestoreStageRollbackApplying, config.StageResultRunning, code); err != nil {
			return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_journal_failed", errors.Join(primary, err))
		}
	}
	safety, err := s.VerifyBackup(ctx, request.SafetyBackupID)
	if err != nil || !restoreSafetyEvidenceMatches(request, safety) {
		return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_backup_invalid", errors.Join(primary, err))
	}
	result.SafetyBackup = safety
	if restoreStageOrder(result.Stage) < restoreStageOrder(config.RestoreStageRollbackFilesRestored) {
		if err := s.applyVerifiedBackup(ctx, options, safety); err != nil {
			return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_apply_failed", errors.Join(primary, err))
		}
		if err := appendStage(config.RestoreStageRollbackFilesRestored, config.StageResultSuccess, ""); err != nil {
			return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_journal_failed", errors.Join(primary, err))
		}
	}
	production, err := s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.SourceDigest {
		return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_digest_failed", errors.Join(primary, err))
	}
	if err := validateRestoreProduction(ctx, options); err != nil {
		return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_validation_failed", errors.Join(primary, err))
	}
	if restoreStageOrder(result.Stage) < restoreStageOrder(config.RestoreStageRollbackValidated) {
		if err := appendStage(config.RestoreStageRollbackValidated, config.StageResultSuccess, ""); err != nil {
			return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_journal_failed", errors.Join(primary, err))
		}
	}
	if reloadPossiblyRequested &&
		restoreStageOrder(result.Stage) < restoreStageOrder(config.RestoreStageRollbackReloadRequested) {
		if err := restoreReload(ctx, options); err != nil {
			return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_reload_failed", errors.Join(primary, err))
		}
		if err := appendStage(config.RestoreStageRollbackReloadRequested, config.StageResultSuccess, ""); err != nil {
			return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_journal_failed", errors.Join(primary, err))
		}
	}
	confirmed, httpStatus, err := s.confirmRestoreRuntime(
		ctx, options, journal.MasterPID, journal.WorkerPIDs, reloadPossiblyRequested,
	)
	if err != nil {
		return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_health_failed", errors.Join(primary, err))
	}
	journal.MasterAfter = confirmed.Master.PID
	journal.WorkerCount = len(confirmed.Workers)
	journal.HTTPStatus = httpStatus
	result.MasterPID = journal.MasterAfter
	result.WorkerCount = journal.WorkerCount
	result.HTTPStatus = journal.HTTPStatus
	journal.ErrorCode = code
	if restoreStageOrder(result.Stage) < restoreStageOrder(config.RestoreStageRolledBack) {
		if err := appendStage(config.RestoreStageRolledBack, config.StageResultWarning, code); err != nil {
			return restoreNeedsAttention(result, journal, appendStage, "restore_rollback_journal_failed", errors.Join(primary, err))
		}
	}
	result.State = config.RestoreStateRolledBack
	result.ErrorCode = code
	result.FinishedAt = s.currentTime()
	return *result, primary
}

func restoreNeedsAttention(
	result *config.RestoreExecutionResult,
	journal *restoreJournal,
	appendStage func(config.RestoreStageName, config.StageResult, string) error,
	code string,
	primary error,
) (config.RestoreExecutionResult, error) {
	journal.ErrorCode = code
	_ = appendStage(config.RestoreStageNeedsAttention, config.StageResultFailed, code)
	result.State = config.RestoreStateNeedsAttention
	result.ErrorCode = code
	result.FinishedAt = journal.UpdatedAt
	return *result, primary
}

func validateRestoreExecutionRequest(request config.RestoreExecutionRequest, requireSafety bool) error {
	if _, err := config.ParseRestoreID(string(request.RestoreID)); err != nil {
		return err
	}
	if _, err := config.ParseBackupID(string(request.TargetBackupID)); err != nil {
		return err
	}
	if _, err := config.ParseBackupID(string(request.SafetyBackupID)); err != nil {
		return err
	}
	if request.SourceDigest == (config.Digest{}) || request.TargetDigest == (config.Digest{}) ||
		request.TargetTreeDigest == (config.Digest{}) ||
		(requireSafety && request.SafetyTreeDigest == (config.Digest{})) {
		return config.ErrDigestInvalid
	}
	return nil
}

func restoreTargetEvidenceMatches(request config.RestoreExecutionRequest, evidence config.BackupEvidence) bool {
	return evidence.BackupID == request.TargetBackupID && evidence.ProductionDigest == request.TargetDigest &&
		evidence.TreeDigest == request.TargetTreeDigest && evidence.EntryCount > 0 && evidence.TotalBytes >= 0 &&
		!evidence.VerifiedAt.IsZero()
}

func restoreSafetyEvidenceMatches(request config.RestoreExecutionRequest, evidence config.BackupEvidence) bool {
	return evidence.BackupID == request.SafetyBackupID && evidence.OriginType == config.BackupOriginRestore &&
		evidence.OriginID == string(request.RestoreID) && evidence.ProductionDigest == request.SourceDigest &&
		evidence.TreeDigest == request.SafetyTreeDigest && evidence.EntryCount > 0 && evidence.TotalBytes >= 0 &&
		!evidence.VerifiedAt.IsZero()
}

func sameBackupEvidence(left, right config.BackupEvidence) bool {
	return left.BackupID == right.BackupID && left.OriginType == right.OriginType && left.OriginID == right.OriginID &&
		left.ReleaseID == right.ReleaseID && left.ProductionDigest == right.ProductionDigest &&
		left.TreeDigest == right.TreeDigest && left.EntryCount == right.EntryCount &&
		left.TotalBytes == right.TotalBytes && left.VerifiedAt.Equal(right.VerifiedAt)
}

func (s *Service) validateRestoreTarget(
	ctx context.Context,
	options restoreOptions,
	target config.BackupEvidence,
	validationPath string,
) error {
	if err := materializeBackupForValidation(ctx, options, target.BackupID, validationPath); err != nil {
		return err
	}
	if err := isolateRestoreIncludes(ctx, validationPath, options); err != nil {
		return err
	}
	result, err := options.Executor(ctx, commandSpec{
		executable: nginxExecutable,
		arguments: []string{"-t", "-p", validationPath + string(filepath.Separator), "-c",
			filepath.Join(validationPath, filepath.FromSlash(string(options.Entry)))},
		timeout: candidateValidationTimeout, maxOutputBytes: candidateDiagnosticLimit,
		allowedExitCodes: map[int]struct{}{0: {}},
	})
	if err == nil {
		return nil
	}
	var exitErr *commandExitError
	if errors.As(err, &exitErr) {
		return errors.Join(ErrConfigInvalid,
			fmt.Errorf("restore target validation failed: %s", sanitizeDiagnostic(result.stderr)))
	}
	return err
}

func materializeBackupForValidation(
	ctx context.Context,
	options restoreOptions,
	id config.BackupID,
	target string,
) (returnErr error) {
	manifest, _, err := readBackupManifest(ctx,
		filepath.Join(options.BackupRoot, string(id), "control", "manifest.bin"), options.Limits)
	if err != nil {
		return err
	}
	source, err := config.OpenScopedRoot(filepath.Join(options.BackupRoot, string(id), "tree"))
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, source.Close()) }()
	for _, entry := range manifest.Entries {
		destination := filepath.Join(target, filepath.FromSlash(string(entry.Path)))
		switch entry.Type {
		case config.EntryDirectory:
			if err := os.Mkdir(destination, 0o700); err != nil {
				return err
			}
		case config.EntryRegular:
			contents, _, err := source.ReadRegular(ctx, entry.Path, options.Limits.MaxFileBytes)
			if err != nil {
				return err
			}
			if err := os.WriteFile(destination, contents, 0o600); err != nil {
				return err
			}
		case config.EntrySymlink:
			relative, err := filepath.Rel(filepath.Dir(destination),
				filepath.Join(target, filepath.FromSlash(string(entry.LinkTarget))))
			if err != nil || filepath.IsAbs(relative) || relative == ".." ||
				strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return config.ErrPathInvalid
			}
			if err := os.Symlink(relative, destination); err != nil {
				return err
			}
		case config.EntrySpecial:
			return config.ErrPathInvalid
		default:
			return config.ErrPathInvalid
		}
	}
	return nil
}

func isolateRestoreIncludes(ctx context.Context, validationPath string, options restoreOptions) (returnErr error) {
	root, err := config.OpenScopedRoot(validationPath)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	entries, err := root.Walk(ctx, options.Limits.MaxEntries)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type != config.EntryRegular {
			continue
		}
		contents, information, err := root.ReadRegular(ctx, entry.Path, options.Limits.MaxFileBytes)
		if err != nil {
			return err
		}
		directives, scanErr := config.ScanDirectives(contents, options.Limits)
		if scanErr != nil {
			if entry.Path == options.Entry {
				return scanErr
			}
			continue
		}
		rewritten := string(contents)
		for _, directive := range directives {
			if directive.Name != "include" || len(directive.Arguments) != 1 {
				continue
			}
			argument := directive.Arguments[0]
			if strings.Contains(argument, "$") {
				return config.ErrPathInvalid
			}
			if !filepath.IsAbs(argument) {
				continue
			}
			clean := filepath.Clean(argument)
			if clean != options.NginxRoot && !strings.HasPrefix(clean, options.NginxRoot+string(filepath.Separator)) {
				return config.ErrPathInvalid
			}
			relative, err := filepath.Rel(options.NginxRoot, clean)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return config.ErrPathInvalid
			}
			rewritten = strings.ReplaceAll(rewritten, argument, filepath.Join(validationPath, relative))
		}
		if rewritten != string(contents) {
			if err := root.AtomicReplace(ctx, entry.Path, []byte(rewritten), information.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) applyVerifiedBackup(
	ctx context.Context,
	options restoreOptions,
	evidence config.BackupEvidence,
) (returnErr error) {
	verified, err := s.VerifyBackup(ctx, evidence.BackupID)
	if err != nil || !sameBackupEvidence(verified, evidence) {
		return errors.Join(config.ErrSnapshotChanged, err)
	}
	manifest, _, err := readBackupManifest(ctx,
		filepath.Join(options.BackupRoot, string(evidence.BackupID), "control", "manifest.bin"), options.Limits)
	if err != nil {
		return err
	}
	production, err := config.OpenScopedRoot(options.NginxRoot)
	if err != nil {
		return err
	}
	backup, err := config.OpenScopedRoot(filepath.Join(options.BackupRoot, string(evidence.BackupID), "tree"))
	if err != nil {
		return errors.Join(err, production.Close())
	}
	defer func() { returnErr = errors.Join(returnErr, backup.Close(), production.Close()) }()
	expected := make(map[config.RelativePath]backupManifestEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		expected[entry.Path] = entry
	}
	current, err := production.Walk(ctx, options.Limits.MaxEntries)
	if err != nil {
		return err
	}
	for index := len(current) - 1; index >= 0; index-- {
		if _, retained := expected[current[index].Path]; retained {
			continue
		}
		if err := os.Remove(filepath.Join(options.NginxRoot,
			filepath.FromSlash(string(current[index].Path)))); err != nil {
			return err
		}
	}
	for _, entry := range manifest.Entries {
		if entry.Type != config.EntryDirectory {
			continue
		}
		if err := production.EnsureDirectory(ctx, entry.Path, fs.FileMode(entry.Mode)); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(options.NginxRoot, filepath.FromSlash(string(entry.Path))),
			fs.FileMode(entry.Mode)); err != nil {
			return err
		}
	}
	for _, entry := range manifest.Entries {
		destination := filepath.Join(options.NginxRoot, filepath.FromSlash(string(entry.Path)))
		switch entry.Type {
		case config.EntryRegular:
			contents, _, err := backup.ReadRegular(ctx, entry.Path, options.Limits.MaxFileBytes)
			if err != nil {
				return err
			}
			information, statErr := os.Lstat(destination)
			switch {
			case statErr == nil && information.Mode().IsRegular():
				if err := production.AtomicReplace(ctx, entry.Path, contents, fs.FileMode(entry.Mode)); err != nil {
					return err
				}
			case errors.Is(statErr, fs.ErrNotExist):
				if err := production.CreateRegular(ctx, entry.Path, contents, fs.FileMode(entry.Mode)); err != nil {
					return err
				}
			default:
				return errors.Join(config.ErrPathInvalid, statErr)
			}
		case config.EntrySymlink:
			relative, err := filepath.Rel(filepath.Dir(destination),
				filepath.Join(options.NginxRoot, filepath.FromSlash(string(entry.LinkTarget))))
			if err != nil {
				return err
			}
			if existing, readErr := os.Readlink(destination); readErr == nil && existing == relative {
				continue
			} else if readErr == nil {
				return config.ErrPathInvalid
			} else if !errors.Is(readErr, fs.ErrNotExist) {
				return readErr
			}
			if err := os.Symlink(relative, destination); err != nil {
				return err
			}
		case config.EntryDirectory:
			continue
		case config.EntrySpecial:
			return config.ErrPathInvalid
		default:
			return config.ErrPathInvalid
		}
	}
	restored, err := buildBackupManifest(ctx, production, options.Limits)
	if err != nil || !equalBackupManifests(manifest, restored) {
		return errors.Join(config.ErrSnapshotChanged, err)
	}
	return syncFilesystemTree(options.NginxRoot)
}

func validateRestoreProduction(ctx context.Context, options restoreOptions) error {
	return validateRestartProduction(ctx, restartOptions{
		NginxRoot: options.NginxRoot, Entry: options.Entry, Limits: options.Limits, Executor: options.Executor,
	})
}

func restoreReload(ctx context.Context, options restoreOptions) error {
	_, err := options.Executor(ctx, commandSpec{
		executable: nginxExecutable, arguments: []string{"-s", "reload"}, timeout: reloadTimeout,
		maxOutputBytes: reloadOutputLimit, allowedExitCodes: map[int]struct{}{0: {}},
	})
	return err
}

func restoreStatus(ctx context.Context, service *Service, options restoreOptions) (Status, error) {
	if options.Status != nil {
		return options.Status(ctx)
	}
	return service.Status(ctx)
}

func restoreProbe(ctx context.Context, options restoreOptions) (int, error) {
	return restartProbe(ctx, restartOptions{Probe: options.Probe})
}

func (s *Service) confirmRestoreRuntime(
	ctx context.Context,
	options restoreOptions,
	masterPID int,
	baselineWorkers []int,
	requireReplacement bool,
) (Status, int, error) {
	releaseOptions := releaseOptions{
		Status: options.Status, Probe: options.Probe, ConfirmTimeout: options.ConfirmTimeout,
	}
	return s.confirmReleaseRuntime(ctx, releaseOptions, masterPID, baselineWorkers, requireReplacement)
}

func createRestorePaths(root string, id config.RestoreID) (string, string, error) {
	taskPath := filepath.Join(root, string(id))
	if err := os.Mkdir(taskPath, 0o700); err != nil {
		return "", "", fmt.Errorf("create restore journal: %w", err)
	}
	controlPath := filepath.Join(taskPath, "control")
	validationPath := filepath.Join(taskPath, "validation")
	if err := os.Mkdir(controlPath, 0o700); err != nil {
		return "", "", err
	}
	if err := os.Mkdir(validationPath, 0o700); err != nil {
		return "", "", err
	}
	return controlPath, validationPath, nil
}

func restorePreparationAppender(
	service *Service,
	controlPath string,
	journal *restoreJournal,
	result *config.RestorePreparationResult,
) func(config.RestoreStageName, config.StageResult, string) error {
	return func(stage config.RestoreStageName, stageResult config.StageResult, code string) error {
		journal.Stage = stage
		journal.UpdatedAt = service.currentTime()
		if err := writeRestoreJournal(controlPath, *journal); err != nil {
			return err
		}
		event := config.RestoreStage{
			RestoreID: journal.RestoreID, Sequence: uint64(len(result.Stages) + 1), Stage: stage,
			Result: stageResult, Code: code, PublicDetailsJSON: "{}", OccurredAt: journal.UpdatedAt,
		}
		if err := appendRestoreEvent(controlPath, event); err != nil {
			return err
		}
		result.Stage = stage
		result.Stages = append(result.Stages, event)
		return nil
	}
}

func restoreExecutionAppender(
	service *Service,
	controlPath string,
	journal *restoreJournal,
	result *config.RestoreExecutionResult,
) func(config.RestoreStageName, config.StageResult, string) error {
	return func(stage config.RestoreStageName, stageResult config.StageResult, code string) error {
		journal.Stage = stage
		journal.UpdatedAt = service.currentTime()
		if err := writeRestoreJournal(controlPath, *journal); err != nil {
			return err
		}
		event := config.RestoreStage{
			RestoreID: journal.RestoreID, Sequence: uint64(len(result.Stages) + 1), Stage: stage,
			Result: stageResult, Code: code, PublicDetailsJSON: "{}", OccurredAt: journal.UpdatedAt,
		}
		if err := appendRestoreEvent(controlPath, event); err != nil {
			return err
		}
		result.Stage = stage
		result.Stages = append(result.Stages, event)
		return nil
	}
}

func writeRestoreJournal(controlPath string, journal restoreJournal) error {
	payload, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return writeAtomicSyncedFile(controlPath, "state.json", append(payload, '\n'), 0o600)
}

func appendRestoreEvent(controlPath string, stage config.RestoreStage) error {
	payload, err := json.Marshal(stage)
	if err != nil {
		return err
	}
	// #nosec G304 -- controlPath is an Agent-owned 0700 task directory derived from a parsed restore ID.
	file, err := os.OpenFile(filepath.Join(controlPath, "events.ndjson"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return errors.Join(err, file.Close())
	}
	return errors.Join(file.Sync(), file.Close(), syncDirectory(controlPath))
}

func readRestoreEvidence(
	ctx context.Context,
	controlPath string,
	request config.RestoreExecutionRequest,
) (_ restoreJournal, _ []config.RestoreStage, returnErr error) {
	root, err := config.OpenScopedRoot(controlPath)
	if err != nil {
		return restoreJournal{}, nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	journalPayload, _, err := root.ReadRegular(ctx, "state.json", restoreJournalLimit)
	if err != nil {
		return restoreJournal{}, nil, err
	}
	var journal restoreJournal
	if err := decodeReleaseJSON(journalPayload, &journal); err != nil || journal.SchemaVersion != 1 ||
		journal.RestoreID != request.RestoreID || journal.TargetBackupID != request.TargetBackupID ||
		journal.SafetyBackupID != request.SafetyBackupID || journal.SourceDigest != request.SourceDigest ||
		journal.TargetDigest != request.TargetDigest || journal.TargetTreeDigest != request.TargetTreeDigest ||
		journal.SafetyTreeDigest != request.SafetyTreeDigest ||
		restoreStageOrder(journal.Stage) < 0 || journal.UpdatedAt.IsZero() {
		return restoreJournal{}, nil, errors.New("restore journal identity is invalid")
	}
	eventsPayload, _, err := root.ReadRegular(ctx, "events.ndjson", restoreEventsLimit)
	if err != nil {
		return restoreJournal{}, nil, err
	}
	stages, err := decodeRestoreEvents(eventsPayload, request.RestoreID)
	if err != nil || len(stages) == 0 || stages[len(stages)-1].Stage != journal.Stage {
		return restoreJournal{}, nil, errors.Join(errors.New("restore events are invalid"), err)
	}
	return journal, stages, nil
}

func decodeRestoreEvents(payload []byte, id config.RestoreID) ([]config.RestoreStage, error) {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 4<<10), restoreJournalLimit)
	stages := make([]config.RestoreStage, 0, 24)
	lastOrder := -1
	for scanner.Scan() {
		if len(stages) >= restoreEventLimit {
			return nil, config.ErrLimitExceeded
		}
		var stage config.RestoreStage
		if err := decodeReleaseJSON(scanner.Bytes(), &stage); err != nil {
			return nil, err
		}
		order := restoreStageOrder(stage.Stage)
		if stage.RestoreID != id || stage.Sequence != uint64(len(stages)+1) || order < 0 ||
			order < lastOrder || stage.OccurredAt.IsZero() || !json.Valid([]byte(stage.PublicDetailsJSON)) {
			return nil, errors.New("restore event sequence is invalid")
		}
		stages = append(stages, stage)
		lastOrder = order
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return stages, nil
}

func restoreExecutionFromEvidence(
	journal restoreJournal,
	stages []config.RestoreStage,
) config.RestoreExecutionResult {
	result := config.RestoreExecutionResult{
		RestoreID: journal.RestoreID, State: restoreStateForStage(journal.Stage), Stage: journal.Stage,
		SafetyBackup: journal.SafetyBackup, Stages: stages, ErrorCode: journal.ErrorCode,
		MasterPID: journal.MasterAfter, WorkerCount: journal.WorkerCount, HTTPStatus: journal.HTTPStatus,
	}
	if terminalRuntimeRestoreState(result.State) {
		result.FinishedAt = journal.UpdatedAt
	}
	return result
}

func restoreStateForStage(stage config.RestoreStageName) config.RestoreState {
	switch stage {
	case config.RestoreStageQueued:
		return config.RestoreStateQueued
	case config.RestoreStageTargetVerifying, config.RestoreStageTargetValidated,
		config.RestoreStageSafetyBackupCreating, config.RestoreStageSafetyBackupVerified,
		config.RestoreStageFilesRestoring, config.RestoreStageFilesRestored,
		config.RestoreStageProductionValidated, config.RestoreStageReloadRequested,
		config.RestoreStageRuntimeConfirmed:
		return config.RestoreStateRunning
	case config.RestoreStageRollbackApplying, config.RestoreStageRollbackFilesRestored,
		config.RestoreStageRollbackValidated, config.RestoreStageRollbackReloadRequested:
		return config.RestoreStateRollingBack
	case config.RestoreStageSucceeded:
		return config.RestoreStateSucceeded
	case config.RestoreStageRolledBack:
		return config.RestoreStateRolledBack
	case config.RestoreStageFailed:
		return config.RestoreStateFailed
	case config.RestoreStageNeedsAttention:
		return config.RestoreStateNeedsAttention
	default:
		return config.RestoreStateNeedsAttention
	}
}

func restoreStageOrder(stage config.RestoreStageName) int {
	switch stage {
	case config.RestoreStageQueued:
		return 0
	case config.RestoreStageTargetVerifying:
		return 1
	case config.RestoreStageTargetValidated:
		return 2
	case config.RestoreStageSafetyBackupCreating:
		return 3
	case config.RestoreStageSafetyBackupVerified:
		return 4
	case config.RestoreStageFilesRestoring:
		return 5
	case config.RestoreStageFilesRestored:
		return 6
	case config.RestoreStageProductionValidated:
		return 7
	case config.RestoreStageReloadRequested:
		return 8
	case config.RestoreStageRuntimeConfirmed:
		return 9
	case config.RestoreStageSucceeded:
		return 10
	case config.RestoreStageRollbackApplying:
		return 20
	case config.RestoreStageRollbackFilesRestored:
		return 21
	case config.RestoreStageRollbackValidated:
		return 22
	case config.RestoreStageRollbackReloadRequested:
		return 23
	case config.RestoreStageRolledBack:
		return 24
	case config.RestoreStageFailed:
		return 30
	case config.RestoreStageNeedsAttention:
		return 31
	default:
		return -1
	}
}

func terminalRuntimeRestoreState(state config.RestoreState) bool {
	switch state {
	case config.RestoreStateSucceeded, config.RestoreStateFailed, config.RestoreStateRolledBack,
		config.RestoreStateNeedsAttention, config.RestoreStateCancelled:
		return true
	case config.RestoreStateQueued, config.RestoreStateRunning, config.RestoreStateRollingBack:
		return false
	default:
		return false
	}
}

func uncertainRestoreResult(id config.RestoreID, code string) config.RestoreExecutionResult {
	return config.RestoreExecutionResult{
		RestoreID: id, State: config.RestoreStateNeedsAttention,
		Stage: config.RestoreStageNeedsAttention, ErrorCode: code,
	}
}
