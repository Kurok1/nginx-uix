/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	reloadTimeout       = 15 * time.Second
	reloadOutputLimit   = 1 << 20
	runtimeConfirmLimit = 30 * time.Second
	healthProbeTimeout  = 2 * time.Second
	healthBodyLimit     = 64 << 10
)

type releaseOptions struct {
	NginxRoot      string
	WorkspaceRoot  string
	CandidateRoot  string
	BackupRoot     string
	ReleaseRoot    string
	Entry          config.RelativePath
	Limits         config.Limits
	Executor       commandExecutor
	Status         func(context.Context) (Status, error)
	Probe          func(context.Context) (int, error)
	ConfirmTimeout time.Duration
	operationHook  func(config.ReleaseStageName, int) error
}

type releaseJournal struct {
	SchemaVersion    uint16                  `json:"schema_version"`
	ReleaseID        config.ReleaseID        `json:"release_id"`
	BackupID         config.BackupID         `json:"backup_id"`
	WorkspaceID      config.WorkspaceID      `json:"workspace_id"`
	ProductionDigest config.Digest           `json:"production_digest"`
	DraftDigest      config.Digest           `json:"draft_digest"`
	CandidateDigest  config.Digest           `json:"candidate_digest"`
	Stage            config.ReleaseStageName `json:"stage"`
	DurableOperation int                     `json:"durable_operation"`
	MasterPID        int                     `json:"master_pid"`
	WorkerPIDs       []int                   `json:"worker_pids"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

func defaultReleaseOptions() releaseOptions {
	return releaseOptions{
		NginxRoot: defaultConfigNginxRoot, WorkspaceRoot: defaultConfigWorkspaceRoot,
		CandidateRoot: "/var/lib/nginx-uix/releases", BackupRoot: "/var/lib/nginx-uix/backups",
		ReleaseRoot: "/var/lib/nginx-uix/releases", Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: executeCommand, ConfirmTimeout: runtimeConfirmLimit,
	}
}

func newReleaseService(options releaseOptions) (*Service, error) {
	if err := validateReleaseOptions(options); err != nil {
		return nil, err
	}
	service := newServiceWithExecutor(options.Executor)
	service.release = options
	service.candidate = candidateOptions{
		NginxRoot: options.NginxRoot, WorkspaceRoot: options.WorkspaceRoot, StageRoot: options.CandidateRoot,
		Entry: options.Entry, Limits: options.Limits, Executor: options.Executor,
	}
	service.backup = backupOptions{NginxRoot: options.NginxRoot, BackupRoot: options.BackupRoot, Limits: options.Limits}
	service.configSnapshot.NginxRoot = options.NginxRoot
	service.configSnapshot.WorkspaceRoot = options.WorkspaceRoot
	service.configSnapshot.Entry = options.Entry
	service.configSnapshot.Limits = options.Limits
	return service, nil
}

func validateReleaseOptions(options releaseOptions) error {
	if options.Executor == nil {
		return errors.New("configure release: executor is required")
	}
	for _, root := range []string{options.NginxRoot, options.WorkspaceRoot, options.CandidateRoot, options.BackupRoot, options.ReleaseRoot} {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("configure release root: %w", config.ErrPathInvalid)
		}
		information, err := os.Lstat(root)
		if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
			return errors.Join(fmt.Errorf("configure release root: %w", config.ErrPathInvalid), err)
		}
	}
	if options.NginxRoot == options.WorkspaceRoot || options.NginxRoot == options.BackupRoot || options.NginxRoot == options.ReleaseRoot {
		return fmt.Errorf("configure release roots: %w", config.ErrPathInvalid)
	}
	if _, err := config.ParseRelativePath(string(options.Entry), options.Limits); err != nil {
		return err
	}
	return nil
}

// ExecuteRelease performs one globally serialized, durable publish or confirmed rollback transaction.
func (s *Service) ExecuteRelease(ctx context.Context, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.ReleaseExecutionResult{}, errors.New("execute release: service is unavailable")
	}
	if err := validateReleaseExecutionRequest(request); err != nil {
		return config.ReleaseExecutionResult{}, err
	}
	select {
	case s.releaseLock <- struct{}{}:
		defer func() { <-s.releaseLock }()
	default:
		return config.ReleaseExecutionResult{}, config.ErrReleaseInProgress
	}
	options := s.release
	if options.NginxRoot == "" {
		options = defaultReleaseOptions()
	}
	if err := validateReleaseOptions(options); err != nil {
		return config.ReleaseExecutionResult{}, err
	}
	releasePath := filepath.Join(options.ReleaseRoot, string(request.ReleaseID))
	controlPath := filepath.Join(releasePath, "control")
	if err := os.Mkdir(releasePath, 0o700); err != nil {
		return config.ReleaseExecutionResult{}, fmt.Errorf("create release journal: %w", err)
	}
	if err := os.Mkdir(controlPath, 0o700); err != nil {
		return config.ReleaseExecutionResult{}, fmt.Errorf("create release journal control: %w", err)
	}
	journal := releaseJournal{
		SchemaVersion: 1, ReleaseID: request.ReleaseID, BackupID: request.BackupID, WorkspaceID: request.WorkspaceID,
		ProductionDigest: request.ProductionDigest, DraftDigest: request.DraftDigest, CandidateDigest: request.CandidateDigest,
	}
	result := config.ReleaseExecutionResult{ReleaseID: request.ReleaseID, State: config.ReleaseStateRunning}
	appendStage := func(stage config.ReleaseStageName, stageResult config.StageResult, code string) error {
		journal.Stage = stage
		journal.UpdatedAt = s.currentTime()
		if err := writeReleaseJournal(controlPath, journal); err != nil {
			return err
		}
		event := config.ReleaseStage{
			ReleaseID: request.ReleaseID, Sequence: uint64(len(result.Stages) + 1), Stage: stage,
			Result: stageResult, Code: code, PublicDetailsJSON: "{}", OccurredAt: journal.UpdatedAt,
		}
		if err := appendReleaseEvent(controlPath, event); err != nil {
			return err
		}
		result.Stage = stage
		result.Stages = append(result.Stages, event)
		return nil
	}
	failBeforeWrite := func(stage config.ReleaseStageName, code string, primary error) (config.ReleaseExecutionResult, error) {
		_ = appendStage(stage, config.StageResultFailed, code)
		result.State = config.ReleaseStateFailed
		result.ErrorCode = code
		result.FinishedAt = s.currentTime()
		return result, primary
	}

	if err := appendStage(config.ReleaseStageRechecking, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	status, err := s.releaseStatus(ctx, options)
	if err != nil || status.State != StateRunning || status.Master == nil || len(status.Workers) == 0 {
		return failBeforeWrite(config.ReleaseStageFailed, "nginx_runtime_unavailable", errors.Join(err, ErrConfigInvalid))
	}
	baselineHTTP, err := s.releaseProbe(ctx, options)
	if err != nil {
		return failBeforeWrite(config.ReleaseStageFailed, "nginx_health_unavailable", err)
	}
	_ = baselineHTTP
	journal.MasterPID = status.Master.PID
	journal.WorkerPIDs = releaseWorkerPIDs(status.Workers)
	production, err := s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.ProductionDigest {
		return failBeforeWrite(config.ReleaseStageFailed, "production_changed", errors.Join(config.ErrSnapshotChanged, err))
	}
	if err := appendStage(config.ReleaseStageBackupCreating, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	backup, err := s.CreateBackup(ctx, config.BackupRequest{ReleaseID: request.ReleaseID, BackupID: request.BackupID, ProductionDigest: request.ProductionDigest})
	if err != nil {
		return failBeforeWrite(config.ReleaseStageFailed, "backup_failed", err)
	}
	result.Backup = backup
	if err := appendStage(config.ReleaseStageBackupVerified, config.StageResultSuccess, ""); err != nil {
		return result, err
	}
	validation, err := s.ValidateCandidate(ctx, config.CandidateValidationRequest{
		WorkspaceID: request.WorkspaceID, ProductionDigest: request.ProductionDigest, DraftDigest: request.DraftDigest,
	})
	if err != nil || !validation.Valid || validation.CandidateDigest != request.CandidateDigest {
		return failBeforeWrite(config.ReleaseStageFailed, "candidate_changed", errors.Join(config.ErrSnapshotChanged, err))
	}
	if err := appendStage(config.ReleaseStageCandidateValidated, config.StageResultSuccess, ""); err != nil {
		return result, err
	}
	if err := appendStage(config.ReleaseStageFilesApplying, config.StageResultRunning, ""); err != nil {
		return result, err
	}
	if err := s.applyReleaseFiles(ctx, options, request, controlPath, &journal); err != nil {
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "files_apply_failed", err, false)
	}
	if err := appendStage(config.ReleaseStageFilesApplied, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "journal_failed", err, false)
	}
	if err := s.validateProductionConfig(ctx, options); err != nil {
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "production_invalid", err, false)
	}
	if err := appendStage(config.ReleaseStageProductionValidated, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "journal_failed", err, false)
	}
	reloadErr := s.reload(ctx, options)
	if reloadErr != nil {
		_ = appendStage(config.ReleaseStageReloadRequested, config.StageResultFailed, "reload_failed")
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "reload_failed", reloadErr, true)
	}
	if err := appendStage(config.ReleaseStageReloadRequested, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "journal_failed", err, true)
	}
	confirmed, httpStatus, err := s.confirmReleaseRuntime(ctx, options, journal.MasterPID, journal.WorkerPIDs, true)
	if err != nil {
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "runtime_unhealthy", err, true)
	}
	if err := appendStage(config.ReleaseStageRuntimeConfirmed, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "journal_failed", err, true)
	}
	if err := appendStage(config.ReleaseStageCommitted, config.StageResultSuccess, ""); err != nil {
		return s.rollbackRelease(ctx, options, request, &result, journal, appendStage, "journal_failed", err, true)
	}
	result.State = config.ReleaseStateSucceeded
	result.MasterPID = confirmed.Master.PID
	result.WorkerCount = len(confirmed.Workers)
	result.HTTPStatus = httpStatus
	result.FinishedAt = s.currentTime()
	return result, nil
}

func validateReleaseExecutionRequest(request config.ReleaseExecutionRequest) error {
	if _, err := config.ParseReleaseID(string(request.ReleaseID)); err != nil {
		return err
	}
	if _, err := config.ParseBackupID(string(request.BackupID)); err != nil {
		return err
	}
	if _, err := config.ParseWorkspaceID(string(request.WorkspaceID)); err != nil {
		return err
	}
	if request.ProductionDigest == (config.Digest{}) || request.DraftDigest == (config.Digest{}) || request.CandidateDigest == (config.Digest{}) {
		return config.ErrDigestInvalid
	}
	return nil
}

func (s *Service) applyReleaseFiles(ctx context.Context, options releaseOptions, request config.ReleaseExecutionRequest, controlPath string, journal *releaseJournal) (returnErr error) {
	productionRoot, err := config.OpenScopedRoot(options.NginxRoot)
	if err != nil {
		return err
	}
	productionInventory, err := config.BuildInventory(ctx, productionRoot, config.SnapshotOptions{
		Entry: options.Entry, Limits: options.Limits, Policy: config.NewPolicy(), FileMode: 0o400, DirectoryMode: 0o700,
	})
	if err != nil || productionInventory.Digest != request.ProductionDigest {
		return errors.Join(config.ErrSnapshotChanged, err, productionRoot.Close())
	}
	workspace, err := config.OpenScopedRoot(filepath.Join(options.WorkspaceRoot, string(request.WorkspaceID)))
	if err != nil {
		return errors.Join(err, productionRoot.Close())
	}
	draftManifest, err := config.ReadControlManifest(ctx, workspace, options.Limits)
	workspaceCloseErr := workspace.Close()
	if err != nil || workspaceCloseErr != nil || draftManifest.Digest() != request.DraftDigest {
		return errors.Join(config.ErrConflict, err, workspaceCloseErr, productionRoot.Close())
	}
	draftRoot, err := config.OpenScopedRoot(filepath.Join(options.WorkspaceRoot, string(request.WorkspaceID), "draft"))
	if err != nil {
		return errors.Join(err, productionRoot.Close())
	}
	defer func() {
		returnErr = errors.Join(returnErr, draftRoot.Close(), productionRoot.Close())
	}()
	productionManaged := make(map[config.RelativePath]config.Entry)
	for _, entry := range productionInventory.Manifest.Entries {
		if entry.Class == config.EntryManagedText {
			productionManaged[entry.Path] = entry
		}
	}
	draftManaged := make(map[config.RelativePath]config.Entry)
	paths := make([]config.RelativePath, 0)
	for _, entry := range draftManifest.Entries {
		if entry.Class == config.EntryManagedText {
			draftManaged[entry.Path] = entry
			paths = append(paths, entry.Path)
		}
	}
	for path := range productionManaged {
		if _, retained := draftManaged[path]; !retained {
			paths = append(paths, path)
		}
	}
	sort.Slice(paths, func(left, right int) bool { return paths[left] < paths[right] })
	paths = slices.Compact(paths)
	for index, path := range paths {
		if options.operationHook != nil {
			if err := options.operationHook(config.ReleaseStageFilesApplying, index); err != nil {
				return err
			}
		}
		entry, retained := draftManaged[path]
		if !retained {
			if err := productionRoot.RemoveRegular(ctx, path); err != nil {
				return err
			}
		} else {
			contents, _, err := draftRoot.ReadRegular(ctx, path, options.Limits.MaxFileBytes)
			if err != nil || int64(len(contents)) != entry.Size {
				return errors.Join(config.ErrConflict, err)
			}
			if current, exists := productionManaged[path]; exists {
				if err := productionRoot.AtomicReplace(ctx, path, contents, current.Mode.Perm()); err != nil {
					return err
				}
			} else if err := productionRoot.CreateRegular(ctx, path, contents, entry.Mode.Perm()); err != nil {
				return err
			}
		}
		journal.DurableOperation = index + 1
		journal.UpdatedAt = s.currentTime()
		if err := writeReleaseJournal(controlPath, *journal); err != nil {
			return err
		}
	}
	production, err := s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.DraftDigest {
		return errors.Join(config.ErrSnapshotChanged, err)
	}
	return nil
}

func (s *Service) rollbackRelease(
	ctx context.Context,
	options releaseOptions,
	request config.ReleaseExecutionRequest,
	result *config.ReleaseExecutionResult,
	journal releaseJournal,
	appendStage func(config.ReleaseStageName, config.StageResult, string) error,
	code string,
	primary error,
	reloadPossiblyRequested bool,
) (config.ReleaseExecutionResult, error) {
	result.State = config.ReleaseStateRollingBack
	currentOrder := releaseStageOrder(result.Stage)
	if currentOrder < releaseStageOrder(config.ReleaseStageRollbackApplying) {
		if err := appendStage(config.ReleaseStageRollbackApplying, config.StageResultRunning, code); err != nil {
			return s.needsAttention(result, appendStage, "rollback_journal_failed", errors.Join(primary, err))
		}
		currentOrder = releaseStageOrder(config.ReleaseStageRollbackApplying)
	}
	if err := s.restoreBackup(ctx, options, request); err != nil {
		return s.needsAttention(result, appendStage, "rollback_restore_failed", errors.Join(primary, err))
	}
	if currentOrder < releaseStageOrder(config.ReleaseStageRollbackFilesRestored) {
		if err := appendStage(config.ReleaseStageRollbackFilesRestored, config.StageResultSuccess, ""); err != nil {
			return s.needsAttention(result, appendStage, "rollback_journal_failed", errors.Join(primary, err))
		}
		currentOrder = releaseStageOrder(config.ReleaseStageRollbackFilesRestored)
	}
	production, err := s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.ProductionDigest {
		return s.needsAttention(result, appendStage, "rollback_digest_failed", errors.Join(primary, err, config.ErrSnapshotChanged))
	}
	if err := s.validateProductionConfig(ctx, options); err != nil {
		return s.needsAttention(result, appendStage, "rollback_validation_failed", errors.Join(primary, err))
	}
	if currentOrder < releaseStageOrder(config.ReleaseStageRollbackValidated) {
		if err := appendStage(config.ReleaseStageRollbackValidated, config.StageResultSuccess, ""); err != nil {
			return s.needsAttention(result, appendStage, "rollback_journal_failed", errors.Join(primary, err))
		}
		currentOrder = releaseStageOrder(config.ReleaseStageRollbackValidated)
	}
	if reloadPossiblyRequested && currentOrder < releaseStageOrder(config.ReleaseStageRollbackReloadRequested) {
		if err := s.reload(ctx, options); err != nil {
			_ = appendStage(config.ReleaseStageRollbackReloadRequested, config.StageResultFailed, "rollback_reload_failed")
			return s.needsAttention(result, appendStage, "rollback_reload_failed", errors.Join(primary, err))
		}
		if err := appendStage(config.ReleaseStageRollbackReloadRequested, config.StageResultSuccess, ""); err != nil {
			return s.needsAttention(result, appendStage, "rollback_journal_failed", errors.Join(primary, err))
		}
		currentOrder = releaseStageOrder(config.ReleaseStageRollbackReloadRequested)
	}
	confirmed, httpStatus, err := s.confirmReleaseRuntime(ctx, options, journal.MasterPID, journal.WorkerPIDs, reloadPossiblyRequested)
	if err != nil {
		return s.needsAttention(result, appendStage, "rollback_health_failed", errors.Join(primary, err))
	}
	if currentOrder < releaseStageOrder(config.ReleaseStageRolledBack) {
		if err := appendStage(config.ReleaseStageRolledBack, config.StageResultWarning, code); err != nil {
			return s.needsAttention(result, appendStage, "rollback_journal_failed", errors.Join(primary, err))
		}
	}
	result.State = config.ReleaseStateRolledBack
	result.MasterPID = confirmed.Master.PID
	result.WorkerCount = len(confirmed.Workers)
	result.HTTPStatus = httpStatus
	result.ErrorCode = code
	result.FinishedAt = s.currentTime()
	return *result, primary
}

func (s *Service) needsAttention(result *config.ReleaseExecutionResult, appendStage func(config.ReleaseStageName, config.StageResult, string) error, code string, primary error) (config.ReleaseExecutionResult, error) {
	_ = appendStage(config.ReleaseStageNeedsAttention, config.StageResultFailed, code)
	result.State = config.ReleaseStateNeedsAttention
	result.ErrorCode = code
	result.FinishedAt = s.currentTime()
	return *result, primary
}

func (s *Service) restoreBackup(ctx context.Context, options releaseOptions, request config.ReleaseExecutionRequest) (returnErr error) {
	if _, err := s.verifyReleaseBackup(ctx, request); err != nil {
		return err
	}
	id := request.BackupID
	backupPath := filepath.Join(options.BackupRoot, string(id))
	manifest, _, err := readBackupManifest(ctx, filepath.Join(backupPath, "control", "manifest.bin"), options.Limits)
	if err != nil {
		return err
	}
	production, err := config.OpenScopedRoot(options.NginxRoot)
	if err != nil {
		return err
	}
	backup, err := config.OpenScopedRoot(filepath.Join(backupPath, "tree"))
	if err != nil {
		return errors.Join(err, production.Close())
	}
	defer func() {
		returnErr = errors.Join(returnErr, backup.Close(), production.Close())
	}()
	expected := make(map[config.RelativePath]backupManifestEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		expected[entry.Path] = entry
	}
	current, err := production.Walk(ctx, options.Limits.MaxEntries)
	if err != nil {
		return err
	}
	for index := len(current) - 1; index >= 0; index-- {
		entry := current[index]
		if _, retained := expected[entry.Path]; retained {
			continue
		}
		path := filepath.Join(options.NginxRoot, filepath.FromSlash(string(entry.Path)))
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	for _, entry := range manifest.Entries {
		if entry.Type == config.EntryDirectory {
			if err := production.EnsureDirectory(ctx, entry.Path, fs.FileMode(entry.Mode)); err != nil {
				return err
			}
			if err := os.Chmod(filepath.Join(options.NginxRoot, filepath.FromSlash(string(entry.Path))), fs.FileMode(entry.Mode)); err != nil {
				return err
			}
		}
	}
	for _, entry := range manifest.Entries {
		destination := filepath.Join(options.NginxRoot, filepath.FromSlash(string(entry.Path)))
		switch entry.Type {
		case config.EntryRegular:
			contents, _, err := backup.ReadRegular(ctx, entry.Path, min(candidateFileLimit, options.Limits.MaxWorkspaceBytes))
			if err != nil {
				return err
			}
			if information, statErr := os.Lstat(destination); statErr == nil && information.Mode().IsRegular() {
				if err := production.AtomicReplace(ctx, entry.Path, contents, fs.FileMode(entry.Mode)); err != nil {
					return err
				}
			} else if errors.Is(statErr, fs.ErrNotExist) {
				if err := production.CreateRegular(ctx, entry.Path, contents, fs.FileMode(entry.Mode)); err != nil {
					return err
				}
			} else {
				return errors.Join(config.ErrPathInvalid, statErr)
			}
		case config.EntrySymlink:
			information, statErr := os.Lstat(destination)
			if statErr == nil && information.Mode()&fs.ModeSymlink != 0 {
				continue
			}
			if statErr == nil {
				return config.ErrPathInvalid
			}
			relative, err := filepath.Rel(filepath.Dir(destination), filepath.Join(options.NginxRoot, filepath.FromSlash(string(entry.LinkTarget))))
			if err != nil {
				return err
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

func (s *Service) verifyReleaseBackup(ctx context.Context, request config.ReleaseExecutionRequest) (config.BackupEvidence, error) {
	evidence, err := s.VerifyBackup(ctx, request.BackupID)
	if err != nil {
		return config.BackupEvidence{}, err
	}
	if evidence.ReleaseID != request.ReleaseID || evidence.ProductionDigest != request.ProductionDigest {
		return config.BackupEvidence{}, config.ErrSnapshotChanged
	}
	return evidence, nil
}

func (s *Service) validateProductionConfig(ctx context.Context, options releaseOptions) error {
	result, err := options.Executor(ctx, commandSpec{
		executable: nginxExecutable, arguments: []string{"-t", "-c", filepath.Join(options.NginxRoot, filepath.FromSlash(string(options.Entry)))},
		timeout: startupValidationTimeout, maxOutputBytes: startupDiagnosticLimit, allowedExitCodes: map[int]struct{}{0: {}},
	})
	if err != nil {
		var exitErr *commandExitError
		if errors.As(err, &exitErr) {
			return errors.Join(ErrConfigInvalid, fmt.Errorf("production validation failed: %s", sanitizeDiagnostic(result.stderr)))
		}
		return err
	}
	return nil
}

func (s *Service) reload(ctx context.Context, options releaseOptions) error {
	_, err := options.Executor(ctx, commandSpec{
		executable: nginxExecutable, arguments: []string{"-s", "reload"}, timeout: reloadTimeout,
		maxOutputBytes: reloadOutputLimit, allowedExitCodes: map[int]struct{}{0: {}},
	})
	return err
}

func (s *Service) releaseStatus(ctx context.Context, options releaseOptions) (Status, error) {
	if options.Status != nil {
		return options.Status(ctx)
	}
	return s.Status(ctx)
}

func (s *Service) releaseProbe(ctx context.Context, options releaseOptions) (int, error) {
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
		return status, fmt.Errorf("loopback health returned non-success status")
	}
	return status, nil
}

func (s *Service) confirmReleaseRuntime(
	ctx context.Context,
	options releaseOptions,
	masterPID int,
	baselineWorkers []int,
	requireReplacement bool,
) (Status, int, error) {
	timeout := options.ConfirmTimeout
	if timeout <= 0 || timeout > runtimeConfirmLimit {
		timeout = runtimeConfirmLimit
	}
	confirmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	var stableWorkers []int
	stableSamples := 0
	for {
		status, err := s.releaseStatus(confirmCtx, options)
		if err == nil && status.State == StateRunning && status.Master != nil && status.Master.PID == masterPID && len(status.Workers) > 0 {
			workers := releaseWorkerPIDs(status.Workers)
			if !requireReplacement || workersReplaced(baselineWorkers, workers) {
				if slices.Equal(stableWorkers, workers) {
					stableSamples++
				} else {
					stableWorkers = workers
					stableSamples = 1
				}
				if stableSamples >= 2 {
					httpStatus, probeErr := s.releaseProbe(confirmCtx, options)
					if probeErr == nil {
						return status, httpStatus, nil
					}
					lastErr = probeErr
				}
			} else {
				stableWorkers = nil
				stableSamples = 0
				lastErr = errors.New("nginx workers have not been replaced")
			}
		} else {
			stableWorkers = nil
			stableSamples = 0
			lastErr = errors.Join(err, errors.New("nginx runtime evidence is not stable"))
		}
		select {
		case <-confirmCtx.Done():
			return Status{}, 0, errors.Join(confirmCtx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func releaseWorkerPIDs(workers []NginxProcess) []int {
	result := make([]int, 0, len(workers))
	for _, worker := range workers {
		if worker.PID > 0 {
			result = append(result, worker.PID)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func workersReplaced(baseline, current []int) bool {
	if len(baseline) == 0 || len(current) == 0 {
		return false
	}
	for _, pid := range current {
		if _, found := slices.BinarySearch(baseline, pid); found {
			return false
		}
	}
	return true
}

func fixedLoopbackProbe(ctx context.Context) (int, error) {
	probeCtx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	dialer := &net.Dialer{Timeout: healthProbeTimeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:80")
		},
		DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: healthProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://127.0.0.1/", nil)
	if err != nil {
		return 0, err
	}
	request.Host = "localhost"
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	readBytes, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, healthBodyLimit+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return 0, errors.Join(readErr, closeErr)
	}
	if readBytes > healthBodyLimit {
		return 0, config.ErrLimitExceeded
	}
	return response.StatusCode, nil
}

func writeReleaseJournal(controlPath string, journal releaseJournal) error {
	payload, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return writeAtomicSyncedFile(controlPath, "state.json", append(payload, '\n'), 0o600)
}

func appendReleaseEvent(controlPath string, event config.ReleaseStage) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	// #nosec G304 -- controlPath is the fixed release root plus a validated opaque release ID.
	file, err := os.OpenFile(filepath.Join(controlPath, "events.ndjson"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, bytes.NewReader(append(payload, '\n'))); err != nil {
		return errors.Join(err, file.Close())
	}
	return errors.Join(file.Sync(), file.Close())
}
