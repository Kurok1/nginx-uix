/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
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
	"path/filepath"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	releaseJournalLimit = 64 << 10
	releaseEventsLimit  = 4 << 20
	releaseEventLimit   = 512
)

// RecoverRelease reconciles one interrupted fixed-root transaction from durable Agent evidence.
func (s *Service) RecoverRelease(ctx context.Context, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.ReleaseExecutionResult{}, errors.New("recover release: service is unavailable")
	}
	if err := validateReleaseExecutionRequest(request); err != nil {
		return config.ReleaseExecutionResult{}, err
	}
	select {
	case s.releaseLock <- struct{}{}:
		defer func() { <-s.releaseLock }()
	case <-ctx.Done():
		return config.ReleaseExecutionResult{}, fmt.Errorf("recover release: %w", ctx.Err())
	}
	options := s.release
	if options.NginxRoot == "" {
		options = defaultReleaseOptions()
	}
	if err := validateReleaseOptions(options); err != nil {
		return config.ReleaseExecutionResult{}, err
	}
	controlPath := filepath.Join(options.ReleaseRoot, string(request.ReleaseID), "control")
	journal, stages, err := readReleaseEvidence(ctx, controlPath, request)
	if err != nil {
		return uncertainRecoveryResult(request.ReleaseID, "release_journal_invalid"), errors.Join(config.ErrConflict, err)
	}
	result := config.ReleaseExecutionResult{
		ReleaseID: request.ReleaseID, State: config.ReleaseStateRunning, Stage: journal.Stage,
		Stages: stages, MasterPID: journal.MasterPID, FinishedAt: journal.UpdatedAt,
	}
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
	if len(stages) == 0 || stages[len(stages)-1].Stage != journal.Stage {
		if err := appendStage(journal.Stage, recoveredStageResult(journal.Stage), "recovered_event_gap"); err != nil {
			return uncertainRecoveryResult(request.ReleaseID, "release_event_recovery_failed"), errors.Join(config.ErrConflict, err)
		}
	}

	switch journal.Stage {
	case config.ReleaseStageQueued, config.ReleaseStageRechecking, config.ReleaseStageBackupCreating,
		config.ReleaseStageBackupVerified, config.ReleaseStageCandidateValidated, config.ReleaseStageFilesApplying,
		config.ReleaseStageFilesApplied, config.ReleaseStageProductionValidated, config.ReleaseStageReloadRequested,
		config.ReleaseStageRuntimeConfirmed, config.ReleaseStageRollbackApplying,
		config.ReleaseStageRollbackFilesRestored, config.ReleaseStageRollbackValidated,
		config.ReleaseStageRollbackReloadRequested:
		// Non-terminal stages continue through the digest-bound recovery decision below.
	case config.ReleaseStageCommitted:
		recovered, recoveryErr := s.recoverCommitted(ctx, options, request, journal, result)
		if recovered.State == config.ReleaseStateNeedsAttention {
			return s.needsAttention(&result, appendStage, recovered.ErrorCode, recoveryErr)
		}
		return recovered, recoveryErr
	case config.ReleaseStageRolledBack:
		recovered, recoveryErr := s.recoverRolledBack(ctx, options, request, journal, result)
		if recovered.State == config.ReleaseStateNeedsAttention {
			return s.needsAttention(&result, appendStage, recovered.ErrorCode, recoveryErr)
		}
		return recovered, recoveryErr
	case config.ReleaseStageFailed:
		result.State = config.ReleaseStateFailed
		result.ErrorCode = lastReleaseStageCode(result.Stages, "release_failed")
		return result, nil
	case config.ReleaseStageNeedsAttention:
		result.State = config.ReleaseStateNeedsAttention
		result.ErrorCode = lastReleaseStageCode(result.Stages, "release_needs_attention")
		return result, nil
	}

	production, digestErr := s.ConfigDigest(ctx)
	if digestErr != nil {
		return s.needsAttention(&result, appendStage, "recovery_digest_failed", digestErr)
	}
	if production.Digest != request.ProductionDigest && production.Digest != request.DraftDigest {
		return s.needsAttention(&result, appendStage, "recovery_digest_ambiguous", config.ErrSnapshotChanged)
	}
	if journal.DurableOperation == 0 && production.Digest == request.ProductionDigest &&
		(stageBeforeProductionWrite(journal.Stage) || journal.Stage == config.ReleaseStageFilesApplying) {
		if err := appendStage(config.ReleaseStageFailed, config.StageResultFailed, "interrupted_before_write"); err != nil {
			return s.needsAttention(&result, appendStage, "recovery_journal_failed", err)
		}
		result.State = config.ReleaseStateFailed
		result.ErrorCode = "interrupted_before_write"
		result.FinishedAt = s.currentTime()
		return result, errors.New("release interrupted before production write")
	}
	backup, backupErr := s.verifyReleaseBackup(ctx, request)
	if backupErr != nil {
		return s.needsAttention(&result, appendStage, "recovery_backup_invalid", backupErr)
	}
	result.Backup = backup
	if releaseStageOrder(journal.Stage) >= releaseStageOrder(config.ReleaseStageRollbackApplying) &&
		releaseStageOrder(journal.Stage) < releaseStageOrder(config.ReleaseStageRolledBack) {
		return s.rollbackRelease(
			ctx, options, request, &result, journal, appendStage,
			"interrupted_release", errors.New("release interrupted during rollback"),
			recoveryMayNeedReload(result.Stages),
		)
	}

	if (journal.Stage == config.ReleaseStageReloadRequested || journal.Stage == config.ReleaseStageRuntimeConfirmed) &&
		production.Digest == request.DraftDigest {
		confirmed, httpStatus, confirmErr := s.confirmReleaseRuntime(ctx, options, journal.MasterPID, journal.WorkerPIDs, true)
		if confirmErr == nil {
			if journal.Stage == config.ReleaseStageReloadRequested {
				if err := appendStage(config.ReleaseStageRuntimeConfirmed, config.StageResultSuccess, "recovered"); err != nil {
					return s.needsAttention(&result, appendStage, "recovery_journal_failed", err)
				}
			}
			if err := appendStage(config.ReleaseStageCommitted, config.StageResultSuccess, "recovered"); err != nil {
				return s.needsAttention(&result, appendStage, "recovery_journal_failed", err)
			}
			result.State = config.ReleaseStateSucceeded
			result.MasterPID = confirmed.Master.PID
			result.WorkerCount = len(confirmed.Workers)
			result.HTTPStatus = httpStatus
			result.FinishedAt = s.currentTime()
			return result, nil
		}
	}

	return s.rollbackRelease(
		ctx, options, request, &result, journal, appendStage,
		"interrupted_release", errors.New("release interrupted during production transaction"),
		stageMayHaveReloaded(journal.Stage),
	)
}

func (s *Service) recoverCommitted(
	ctx context.Context,
	options releaseOptions,
	request config.ReleaseExecutionRequest,
	journal releaseJournal,
	result config.ReleaseExecutionResult,
) (config.ReleaseExecutionResult, error) {
	production, err := s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.DraftDigest {
		result.State = config.ReleaseStateNeedsAttention
		result.Stage = config.ReleaseStageNeedsAttention
		result.ErrorCode = "committed_digest_mismatch"
		return result, errors.Join(config.ErrSnapshotChanged, err)
	}
	backup, err := s.verifyReleaseBackup(ctx, request)
	if err != nil {
		result.State = config.ReleaseStateNeedsAttention
		result.Stage = config.ReleaseStageNeedsAttention
		result.ErrorCode = "committed_backup_invalid"
		return result, err
	}
	result.State = config.ReleaseStateSucceeded
	result.Stage = config.ReleaseStageCommitted
	result.Backup = backup
	result.MasterPID = journal.MasterPID
	result.FinishedAt = journal.UpdatedAt
	if status, statusErr := s.releaseStatus(ctx, options); statusErr == nil && status.Master != nil {
		result.MasterPID = status.Master.PID
		result.WorkerCount = len(status.Workers)
	}
	return result, nil
}

func (s *Service) recoverRolledBack(
	ctx context.Context,
	options releaseOptions,
	request config.ReleaseExecutionRequest,
	journal releaseJournal,
	result config.ReleaseExecutionResult,
) (config.ReleaseExecutionResult, error) {
	backup, err := s.verifyReleaseBackup(ctx, request)
	if err != nil {
		result.State = config.ReleaseStateNeedsAttention
		result.Stage = config.ReleaseStageNeedsAttention
		result.ErrorCode = "rolled_back_backup_invalid"
		return result, err
	}
	production, err := s.ConfigDigest(ctx)
	if err != nil || production.Digest != request.ProductionDigest {
		result.State = config.ReleaseStateNeedsAttention
		result.Stage = config.ReleaseStageNeedsAttention
		result.ErrorCode = "rolled_back_digest_mismatch"
		return result, errors.Join(config.ErrSnapshotChanged, err)
	}
	if err := s.validateProductionConfig(ctx, options); err != nil {
		result.State = config.ReleaseStateNeedsAttention
		result.Stage = config.ReleaseStageNeedsAttention
		result.ErrorCode = "rolled_back_validation_failed"
		return result, err
	}
	confirmed, httpStatus, err := s.confirmReleaseRuntime(
		ctx, options, journal.MasterPID, journal.WorkerPIDs, recoveryMayNeedReload(result.Stages),
	)
	if err != nil {
		result.State = config.ReleaseStateNeedsAttention
		result.Stage = config.ReleaseStageNeedsAttention
		result.ErrorCode = "rolled_back_health_failed"
		return result, err
	}
	result.State = config.ReleaseStateRolledBack
	result.Stage = config.ReleaseStageRolledBack
	result.Backup = backup
	result.MasterPID = confirmed.Master.PID
	result.WorkerCount = len(confirmed.Workers)
	result.HTTPStatus = httpStatus
	result.ErrorCode = lastReleaseStageCode(result.Stages, "interrupted_release")
	result.FinishedAt = journal.UpdatedAt
	return result, nil
}

func readReleaseEvidence(
	ctx context.Context,
	controlPath string,
	request config.ReleaseExecutionRequest,
) (journal releaseJournal, stages []config.ReleaseStage, returnErr error) {
	root, err := config.OpenScopedRoot(controlPath)
	if err != nil {
		return releaseJournal{}, nil, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	journalPayload, _, err := root.ReadRegular(ctx, "state.json", releaseJournalLimit)
	if err != nil {
		return releaseJournal{}, nil, err
	}
	if err := decodeReleaseJSON(journalPayload, &journal); err != nil {
		return releaseJournal{}, nil, fmt.Errorf("decode release journal: %w", err)
	}
	if journal.SchemaVersion != 1 || journal.ReleaseID != request.ReleaseID || journal.BackupID != request.BackupID ||
		journal.WorkspaceID != request.WorkspaceID || journal.ProductionDigest != request.ProductionDigest ||
		journal.DraftDigest != request.DraftDigest || journal.CandidateDigest != request.CandidateDigest ||
		!knownReleaseStage(journal.Stage) || !validReleaseRuntimeIdentity(journal) || journal.UpdatedAt.IsZero() {
		return releaseJournal{}, nil, errors.New("release journal identity is invalid")
	}
	eventsPayload, _, err := root.ReadRegular(ctx, "events.ndjson", releaseEventsLimit)
	if errors.Is(err, fs.ErrNotExist) && journal.Stage == config.ReleaseStageRechecking {
		return journal, []config.ReleaseStage{}, nil
	}
	if err != nil {
		return releaseJournal{}, nil, err
	}
	stages, err = decodeReleaseEvents(eventsPayload, request.ReleaseID)
	if err != nil {
		return releaseJournal{}, nil, err
	}
	if len(stages) == 0 || releaseStageOrder(journal.Stage) < releaseStageOrder(stages[len(stages)-1].Stage) {
		return releaseJournal{}, nil, errors.New("release journal stage regressed")
	}
	return journal, stages, nil
}

func validReleaseWorkerPIDs(workers []int) bool {
	if len(workers) == 0 || !slices.IsSorted(workers) {
		return false
	}
	for index, pid := range workers {
		if pid <= 0 || index > 0 && workers[index-1] == pid {
			return false
		}
	}
	return true
}

func validReleaseRuntimeIdentity(journal releaseJournal) bool {
	if journal.MasterPID > 0 && validReleaseWorkerPIDs(journal.WorkerPIDs) {
		return true
	}
	if journal.MasterPID != 0 || len(journal.WorkerPIDs) != 0 {
		return false
	}
	return journal.Stage == config.ReleaseStageRechecking || journal.Stage == config.ReleaseStageFailed
}

func recoveredStageResult(stage config.ReleaseStageName) config.StageResult {
	switch stage {
	case config.ReleaseStageQueued:
		return config.StageResultPending
	case config.ReleaseStageRechecking, config.ReleaseStageBackupCreating, config.ReleaseStageBackupVerified,
		config.ReleaseStageCandidateValidated, config.ReleaseStageFilesApplying, config.ReleaseStageFilesApplied,
		config.ReleaseStageProductionValidated, config.ReleaseStageReloadRequested, config.ReleaseStageRuntimeConfirmed,
		config.ReleaseStageRollbackApplying, config.ReleaseStageRollbackFilesRestored,
		config.ReleaseStageRollbackValidated, config.ReleaseStageRollbackReloadRequested:
		return config.StageResultRunning
	case config.ReleaseStageCommitted:
		return config.StageResultSuccess
	case config.ReleaseStageRolledBack:
		return config.StageResultWarning
	case config.ReleaseStageFailed, config.ReleaseStageNeedsAttention:
		return config.StageResultFailed
	}
	return config.StageResultFailed
}

func recoveryMayNeedReload(stages []config.ReleaseStage) bool {
	for _, stage := range stages {
		if stage.Stage == config.ReleaseStageReloadRequested || stage.Stage == config.ReleaseStageRollbackReloadRequested {
			return true
		}
	}
	return false
}

func decodeReleaseEvents(payload []byte, id config.ReleaseID) ([]config.ReleaseStage, error) {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 4<<10), releaseJournalLimit)
	stages := make([]config.ReleaseStage, 0, 16)
	lastOrder := -1
	for scanner.Scan() {
		if len(stages) >= releaseEventLimit {
			return nil, config.ErrLimitExceeded
		}
		var stage config.ReleaseStage
		if err := decodeReleaseJSON(scanner.Bytes(), &stage); err != nil {
			return nil, fmt.Errorf("decode release event: %w", err)
		}
		order := releaseStageOrder(stage.Stage)
		if stage.ReleaseID != id || stage.Sequence != uint64(len(stages)+1) || order < 0 || order < lastOrder ||
			stage.OccurredAt.IsZero() || !json.Valid([]byte(stage.PublicDetailsJSON)) {
			return nil, errors.New("release event sequence is invalid")
		}
		lastOrder = order
		stages = append(stages, stage)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return stages, nil
}

func decodeReleaseJSON(payload []byte, target any) error {
	if err := rejectDuplicateAgentJSONFields(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func stageBeforeProductionWrite(stage config.ReleaseStageName) bool {
	return releaseStageOrder(stage) < releaseStageOrder(config.ReleaseStageFilesApplying)
}

func stageMayHaveReloaded(stage config.ReleaseStageName) bool {
	return releaseStageOrder(stage) >= releaseStageOrder(config.ReleaseStageReloadRequested)
}

func knownReleaseStage(stage config.ReleaseStageName) bool {
	return releaseStageOrder(stage) >= 0
}

func releaseStageOrder(stage config.ReleaseStageName) int {
	switch stage {
	case config.ReleaseStageQueued:
		return 0
	case config.ReleaseStageRechecking:
		return 1
	case config.ReleaseStageBackupCreating:
		return 2
	case config.ReleaseStageBackupVerified:
		return 3
	case config.ReleaseStageCandidateValidated:
		return 4
	case config.ReleaseStageFilesApplying:
		return 5
	case config.ReleaseStageFilesApplied:
		return 6
	case config.ReleaseStageProductionValidated:
		return 7
	case config.ReleaseStageReloadRequested:
		return 8
	case config.ReleaseStageRuntimeConfirmed:
		return 9
	case config.ReleaseStageCommitted:
		return 10
	case config.ReleaseStageRollbackApplying:
		return 20
	case config.ReleaseStageRollbackFilesRestored:
		return 21
	case config.ReleaseStageRollbackValidated:
		return 22
	case config.ReleaseStageRollbackReloadRequested:
		return 23
	case config.ReleaseStageRolledBack:
		return 24
	case config.ReleaseStageFailed:
		return 30
	case config.ReleaseStageNeedsAttention:
		return 31
	default:
		return -1
	}
}

func uncertainRecoveryResult(id config.ReleaseID, code string) config.ReleaseExecutionResult {
	now := time.Now().UTC()
	return config.ReleaseExecutionResult{
		ReleaseID: id, State: config.ReleaseStateNeedsAttention, Stage: config.ReleaseStageNeedsAttention,
		ErrorCode: code, FinishedAt: now,
		Stages: []config.ReleaseStage{{
			ReleaseID: id, Sequence: 1, Stage: config.ReleaseStageNeedsAttention, Result: config.StageResultFailed,
			Code: code, PublicDetailsJSON: "{}", OccurredAt: now,
		}},
	}
}

func lastReleaseStageCode(stages []config.ReleaseStage, fallback string) string {
	for index := len(stages) - 1; index >= 0; index-- {
		if stages[index].Code != "" {
			return stages[index].Code
		}
	}
	return fallback
}
