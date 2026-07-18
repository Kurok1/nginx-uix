/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/kuroky/nginx-uix/internal/config"
)

// ReleaseProgress reads one transaction's durable Agent evidence without taking the production lock.
func (s *Service) ReleaseProgress(ctx context.Context, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	if ctx == nil || s == nil {
		return config.ReleaseExecutionResult{}, errors.New("read release progress: service is unavailable")
	}
	if err := validateReleaseExecutionRequest(request); err != nil {
		return config.ReleaseExecutionResult{}, err
	}
	options := s.release
	if options.NginxRoot == "" {
		options = defaultReleaseOptions()
	}
	if err := validateReleaseOptions(options); err != nil {
		return config.ReleaseExecutionResult{}, err
	}
	journal, stages, err := readReleaseEvidence(
		ctx,
		filepath.Join(options.ReleaseRoot, string(request.ReleaseID), "control"),
		request,
	)
	if err != nil {
		return config.ReleaseExecutionResult{}, err
	}
	result := config.ReleaseExecutionResult{
		ReleaseID: request.ReleaseID,
		State:     releaseStateForJournalStage(journal.Stage),
		Stage:     journal.Stage,
		Stages:    stages,
		MasterPID: journal.MasterPID,
	}
	if journal.WorkerPIDs != nil {
		result.WorkerCount = len(journal.WorkerPIDs)
	}
	if terminalAgentReleaseState(result.State) {
		result.ErrorCode = lastReleaseStageCode(stages, "")
		result.FinishedAt = journal.UpdatedAt
	}
	return result, nil
}

func releaseStateForJournalStage(stage config.ReleaseStageName) config.ReleaseState {
	switch stage {
	case config.ReleaseStageQueued:
		return config.ReleaseStateQueued
	case config.ReleaseStageRollbackApplying, config.ReleaseStageRollbackFilesRestored,
		config.ReleaseStageRollbackValidated, config.ReleaseStageRollbackReloadRequested:
		return config.ReleaseStateRollingBack
	case config.ReleaseStageCommitted:
		return config.ReleaseStateSucceeded
	case config.ReleaseStageRolledBack:
		return config.ReleaseStateRolledBack
	case config.ReleaseStageFailed:
		return config.ReleaseStateFailed
	case config.ReleaseStageNeedsAttention:
		return config.ReleaseStateNeedsAttention
	case config.ReleaseStageRechecking, config.ReleaseStageBackupCreating, config.ReleaseStageBackupVerified,
		config.ReleaseStageCandidateValidated, config.ReleaseStageFilesApplying, config.ReleaseStageFilesApplied,
		config.ReleaseStageProductionValidated, config.ReleaseStageReloadRequested, config.ReleaseStageRuntimeConfirmed:
		return config.ReleaseStateRunning
	}
	return config.ReleaseStateNeedsAttention
}

func terminalAgentReleaseState(state config.ReleaseState) bool {
	switch state {
	case config.ReleaseStateSucceeded, config.ReleaseStateFailed, config.ReleaseStateRolledBack,
		config.ReleaseStateNeedsAttention, config.ReleaseStateCancelled:
		return true
	case config.ReleaseStateQueued, config.ReleaseStateRunning, config.ReleaseStateRollingBack:
		return false
	}
	return false
}
