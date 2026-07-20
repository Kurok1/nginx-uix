/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	recoveryStageLimit             = 512
	recoveryProgressPollInterval   = 200 * time.Millisecond
	recoveryProgressRequestTimeout = 2 * time.Second
)

// Reconcile resolves the exact durable production-lease owner before new mutations are accepted.
func (s *RecoveryService) Reconcile(ctx context.Context) error {
	if ctx == nil || s == nil || s.repository == nil || s.agent == nil || s.clock == nil {
		return errors.New("reconcile recovery operations: service is unavailable")
	}
	lease, err := s.repository.ProductionLease(ctx)
	if err != nil {
		return fmt.Errorf("reconcile recovery operations: %w", err)
	}
	if lease.OwnerID == "" {
		if _, err := s.repository.ActiveRestore(ctx); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return errors.Join(ErrConflict, err)
		}
		if _, err := s.repository.ActiveRestart(ctx); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return errors.Join(ErrConflict, err)
		}
		return nil
	}
	switch lease.OwnerType {
	case ProductionOperationRestore:
		id, err := ParseRestoreID(lease.OwnerID)
		if err != nil {
			return err
		}
		return s.reconcileRestore(ctx, id)
	case ProductionOperationRestart:
		id, err := ParseRestartID(lease.OwnerID)
		if err != nil {
			return err
		}
		return s.reconcileRestart(ctx, id)
	case ProductionOperationRetention:
		id, err := ParseRetentionRunID(lease.OwnerID)
		if err != nil {
			return err
		}
		return s.RunRetention(ctx, id)
	case ProductionOperationRelease:
		return ErrOperationInProgress
	default:
		return ErrConflict
	}
}

// QueueRestore validates one immutable recovery point twice around durable lease acquisition.
func (s *RecoveryService) QueueRestore(
	ctx context.Context,
	actor Actor,
	input QueueRestoreInput,
) (Restore, error) {
	if ctx == nil || s == nil || s.repository == nil || s.agent == nil || s.clock == nil || s.random == nil {
		return Restore{}, errors.New("queue restore: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return Restore{}, err
	}
	if _, err := ParseBackupID(string(input.TargetBackupID)); err != nil {
		return Restore{}, err
	}
	if input.ConfirmBackupID != string(input.TargetBackupID) || !validRecoveryInputReason(input.Reason) {
		return Restore{}, ErrConflict
	}
	if err := s.requireOpenAttention(ctx, input.AttentionCaseID); err != nil {
		return Restore{}, err
	}
	target, err := s.verifiedRestoreTarget(ctx, actor.RequestID, input.TargetBackupID)
	if err != nil {
		return Restore{}, fmt.Errorf("verify restore target: %w", err)
	}
	production, err := s.agent.ConfigDigest(ctx, actor.RequestID)
	if err != nil || production.Digest == (Digest{}) {
		return Restore{}, errors.Join(errors.New("read production digest for restore"), err)
	}
	restoreID, err := NewRestoreID(s.random)
	if err != nil {
		return Restore{}, fmt.Errorf("generate restore id: %w", err)
	}
	safetyID, err := NewBackupID(s.random)
	if err != nil {
		return Restore{}, fmt.Errorf("generate restore safety backup id: %w", err)
	}
	now := s.clock.Now().UTC()
	if now.IsZero() {
		return Restore{}, errors.New("queue restore: clock is invalid")
	}
	restore := Restore{
		ID: restoreID, TargetBackupID: target.ID, SafetyBackupID: safetyID,
		AttentionCaseID: input.AttentionCaseID, State: RestoreStateQueued, Stage: RestoreStageQueued,
		SourceDigest: production.Digest, TargetDigest: target.ProductionDigest,
		CreatedBy: actor.UserID, Reason: input.Reason, RequestID: actor.RequestID,
		CreatedAt: now, UpdatedAt: now,
	}
	stage := RestoreStage{
		RestoreID: restore.ID, Sequence: 1, Stage: RestoreStageQueued, Result: StageResultPending,
		PublicDetailsJSON: "{}", OccurredAt: now,
	}
	if err := s.repository.CreateRestore(ctx, restore, stage); err != nil {
		return Restore{}, fmt.Errorf("queue restore: %w", err)
	}

	secondTarget, targetErr := s.verifiedRestoreTarget(ctx, actor.RequestID, input.TargetBackupID)
	secondProduction, productionErr := s.agent.ConfigDigest(ctx, actor.RequestID)
	attentionErr := s.requireOpenAttention(ctx, input.AttentionCaseID)
	if targetErr != nil || productionErr != nil || attentionErr != nil ||
		!sameRestoreTargetIndex(target, secondTarget) || secondProduction.Digest != production.Digest {
		primary := errors.Join(ErrSnapshotChanged, targetErr, productionErr, attentionErr)
		transitionErr := s.failQueuedRestore(ctx, restore, "restore_precondition_changed")
		return Restore{}, errors.Join(primary, transitionErr)
	}
	return restore, nil
}

// RunRestore mirrors one Agent restore journal into SQLite and indexes its safety backup before execution.
func (s *RecoveryService) RunRestore(ctx context.Context, id RestoreID) error {
	if ctx == nil || s == nil || s.repository == nil || s.agent == nil || s.clock == nil {
		return errors.New("run restore: service is unavailable")
	}
	restore, err := s.repository.Restore(ctx, id)
	if err != nil {
		return fmt.Errorf("run restore: %w", err)
	}
	if restore.State != RestoreStateQueued || restore.Stage != RestoreStageQueued {
		return ErrConflict
	}
	target, err := s.repository.Backup(ctx, restore.TargetBackupID)
	if err != nil || !usableRestoreTargetIndex(target) || target.ProductionDigest != restore.TargetDigest {
		return errors.Join(s.failQueuedRestore(ctx, restore, "restore_target_index_invalid"), err)
	}
	now := s.clock.Now().UTC()
	running := restore
	running.State = RestoreStateRunning
	running.Stage = RestoreStageTargetVerifying
	running.UpdatedAt = now
	if err := s.repository.TransitionRestore(ctx, restore.State, restore.Stage, running, RestoreStage{
		RestoreID: id, Sequence: 2, Stage: RestoreStageTargetVerifying, Result: StageResultRunning,
		PublicDetailsJSON: "{}", OccurredAt: now,
	}); err != nil {
		return fmt.Errorf("start restore: %w", err)
	}
	restore = running
	request := RestoreExecutionRequest{
		RestoreID: restore.ID, TargetBackupID: restore.TargetBackupID, SafetyBackupID: restore.SafetyBackupID,
		SourceDigest: restore.SourceDigest, TargetDigest: restore.TargetDigest, TargetTreeDigest: target.TreeDigest,
	}
	preparation, preparationErr := s.agent.PrepareRestore(ctx, restore.RequestID, request)
	if !validRestorePreparation(preparation, restore, request) {
		return s.markRestoreUncertain(ctx, restore, 3, "restore_preparation_evidence_invalid", preparationErr)
	}
	if preparation.SafetyBackup.BackupID != "" {
		if err := s.persistRestoreSafetyBackup(ctx, restore, preparation.SafetyBackup); err != nil {
			return s.markRestoreUncertain(ctx, restore, 3, "restore_safety_index_failed", errors.Join(err, preparationErr))
		}
	}
	restore, sequence, err := s.mirrorRestoreStages(
		ctx, restore, preparation.State, preparation.Stage, preparation.ErrorCode,
		preparation.FinishedAt, preparation.Stages[1:], 3,
	)
	if err != nil {
		return err
	}
	if terminalRecoveryRestoreState(preparation.State) {
		return preparationErr
	}
	if restore.State != RestoreStateRunning || restore.Stage != RestoreStageSafetyBackupVerified ||
		preparation.SafetyBackup.BackupID != restore.SafetyBackupID {
		return s.markRestoreUncertain(ctx, restore, sequence, "restore_preparation_incomplete", preparationErr)
	}
	request.SafetyTreeDigest = preparation.SafetyBackup.TreeDigest
	type executionOutcome struct {
		result RestoreExecutionResult
		err    error
	}
	completed := make(chan executionOutcome, 1)
	go func() {
		result, executionErr := s.agent.ExecuteRestore(ctx, restore.RequestID, request)
		completed <- executionOutcome{result: result, err: executionErr}
	}()
	ticker := time.NewTicker(recoveryProgressPollInterval)
	defer ticker.Stop()
	mirroredAgentStages := len(preparation.Stages)
	agentPrefix := append([]RestoreStage(nil), preparation.Stages...)
	var persistenceErr error
	var result RestoreExecutionResult
	var executionErr error
	executionFinished := false
	for !executionFinished {
		select {
		case outcome := <-completed:
			result = outcome.result
			executionErr = outcome.err
			executionFinished = true
		case <-ticker.C:
			if persistenceErr != nil {
				continue
			}
			progressCtx, cancel := context.WithTimeout(ctx, recoveryProgressRequestTimeout)
			progress, progressErr := s.agent.RestoreProgress(
				progressCtx, restore.RequestID, request,
			)
			cancel()
			if progressErr != nil || !validRestoreExecutionProgress(
				progress, restore, request, mirroredAgentStages, agentPrefix,
			) {
				continue
			}
			available := nonTerminalRestoreStageCount(progress)
			if available <= mirroredAgentStages {
				continue
			}
			var mirrorErr error
			restore, sequence, mirrorErr = s.mirrorRestoreStages(
				ctx, restore, progress.State, progress.Stage, progress.ErrorCode, progress.FinishedAt,
				progress.Stages[mirroredAgentStages:available], sequence,
			)
			if mirrorErr != nil {
				persistenceErr = mirrorErr
				continue
			}
			agentPrefix = append(agentPrefix[:0], progress.Stages[:available]...)
			mirroredAgentStages = available
		case <-ctx.Done():
			return fmt.Errorf("run restore: %w", ctx.Err())
		}
	}
	if !validRestoreExecutionResult(result, restore, request, preparation.Stages) ||
		!sameRestoreStagePrefix(agentPrefix, result.Stages) || mirroredAgentStages >= len(result.Stages) {
		return s.markRestoreUncertain(ctx, restore, sequence, "restore_execution_evidence_invalid", executionErr)
	}
	if persistenceErr != nil {
		return s.markRestoreUncertain(ctx, restore, sequence,
			"restore_stage_persistence_failed", errors.Join(persistenceErr, executionErr))
	}
	restore, sequence, err = s.mirrorRestoreStages(
		ctx, restore, result.State, result.Stage, result.ErrorCode, result.FinishedAt,
		result.Stages[mirroredAgentStages:], sequence,
	)
	if err != nil {
		return err
	}
	if restore.State != result.State || restore.Stage != result.Stage || !terminalRecoveryRestoreState(restore.State) {
		return s.markRestoreUncertain(ctx, restore, sequence, "restore_terminal_mismatch", executionErr)
	}
	if restore.State == RestoreStateSucceeded && restore.AttentionCaseID != "" {
		if err := s.repository.ResolveAttentionCase(
			ctx, restore.AttentionCaseID, AttentionResolutionRestore, string(restore.ID),
			Actor{UserID: restore.CreatedBy, RequestID: restore.RequestID}, restore.FinishedAt,
		); err != nil {
			return fmt.Errorf("resolve restore attention case: %w", err)
		}
	}
	return executionErr
}

// QueueRestart persists the sole fixed restart operation after exact named confirmation.
func (s *RecoveryService) QueueRestart(
	ctx context.Context,
	actor Actor,
	input QueueRestartInput,
) (Restart, error) {
	if ctx == nil || s == nil || s.repository == nil || s.agent == nil || s.clock == nil || s.random == nil {
		return Restart{}, errors.New("queue restart: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return Restart{}, err
	}
	if input.Confirmation != "RESTART NGINX" || !validRecoveryInputReason(input.Reason) {
		return Restart{}, ErrConflict
	}
	if err := s.requireOpenAttention(ctx, input.AttentionCaseID); err != nil {
		return Restart{}, err
	}
	production, err := s.agent.ConfigDigest(ctx, actor.RequestID)
	if err != nil || production.Digest == (Digest{}) {
		return Restart{}, errors.Join(errors.New("read production digest for restart"), err)
	}
	id, err := NewRestartID(s.random)
	if err != nil {
		return Restart{}, fmt.Errorf("generate restart id: %w", err)
	}
	now := s.clock.Now().UTC()
	restart := Restart{
		ID: id, AttentionCaseID: input.AttentionCaseID, State: RestartStateQueued,
		Stage: RestartStageQueued, ProductionDigest: production.Digest, CreatedBy: actor.UserID,
		Reason: input.Reason, RequestID: actor.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	stage := RestartStage{
		RestartID: id, Sequence: 1, Stage: RestartStageQueued, Result: StageResultPending,
		PublicDetailsJSON: "{}", OccurredAt: now,
	}
	if err := s.repository.CreateRestart(ctx, restart, stage); err != nil {
		return Restart{}, fmt.Errorf("queue restart: %w", err)
	}
	return restart, nil
}

// VerifyAttentionCase records a fixed health proof and resolves the case only after persisted success.
func (s *RecoveryService) VerifyAttentionCase(
	ctx context.Context,
	actor Actor,
	id AttentionCaseID,
) (Verification, error) {
	if ctx == nil || s == nil || s.repository == nil || s.agent == nil || s.clock == nil || s.random == nil {
		return Verification{}, errors.New("verify attention case: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return Verification{}, err
	}
	if err := s.requireOpenAttention(ctx, id); err != nil {
		return Verification{}, err
	}
	production, err := s.agent.ConfigDigest(ctx, actor.RequestID)
	if err != nil || production.Digest == (Digest{}) {
		return Verification{}, errors.Join(errors.New("read production digest for verification"), err)
	}
	verificationID, err := NewVerificationID(s.random)
	if err != nil {
		return Verification{}, fmt.Errorf("generate verification id: %w", err)
	}
	createdAt := s.clock.Now().UTC()
	result, verificationErr := s.agent.VerifyRuntime(ctx, actor.RequestID, RuntimeVerificationRequest{
		VerificationID: verificationID, ProductionDigest: production.Digest,
	})
	if !validRuntimeVerificationResult(result, verificationID, production.Digest) {
		return Verification{}, errors.Join(ErrConflict, verificationErr)
	}
	verification := Verification{
		ID: verificationID, AttentionCaseID: id, State: result.State,
		ProductionDigest: result.ProductionDigest, MasterPID: result.MasterPID,
		WorkerCount: result.WorkerCount, HTTPStatus: result.HTTPStatus,
		LastErrorCode: result.ErrorCode, CreatedBy: actor.UserID, RequestID: actor.RequestID,
		CreatedAt: createdAt, FinishedAt: result.CheckedAt,
	}
	if err := s.repository.CreateVerification(ctx, verification); err != nil {
		return Verification{}, fmt.Errorf("persist runtime verification: %w", err)
	}
	if verification.State != VerificationStateSucceeded || verificationErr != nil {
		return verification, errors.Join(ErrAttentionUnresolved, verificationErr)
	}
	if err := s.repository.ResolveAttentionCase(
		ctx, id, AttentionResolutionVerification, string(verification.ID), actor, verification.FinishedAt,
	); err != nil {
		return verification, fmt.Errorf("resolve verified attention case: %w", err)
	}
	return verification, nil
}

// RunRestart executes only the Agent's fixed restart and mirrors its durable evidence.
func (s *RecoveryService) RunRestart(ctx context.Context, id RestartID) error {
	if ctx == nil || s == nil || s.repository == nil || s.agent == nil || s.clock == nil {
		return errors.New("run restart: service is unavailable")
	}
	restart, err := s.repository.Restart(ctx, id)
	if err != nil {
		return fmt.Errorf("run restart: %w", err)
	}
	if restart.State != RestartStateQueued || restart.Stage != RestartStageQueued {
		return ErrConflict
	}
	now := s.clock.Now().UTC()
	running := restart
	running.State = RestartStateRunning
	running.Stage = RestartStageProductionValidating
	running.UpdatedAt = now
	if err := s.repository.TransitionRestart(ctx, restart.State, restart.Stage, running, RestartStage{
		RestartID: id, Sequence: 2, Stage: RestartStageProductionValidating, Result: StageResultRunning,
		PublicDetailsJSON: "{}", OccurredAt: now,
	}); err != nil {
		return fmt.Errorf("start restart: %w", err)
	}
	restart = running
	request := RestartExecutionRequest{RestartID: id, ProductionDigest: restart.ProductionDigest}
	type executionOutcome struct {
		result RestartExecutionResult
		err    error
	}
	completed := make(chan executionOutcome, 1)
	go func() {
		result, executionErr := s.agent.ExecuteRestart(ctx, restart.RequestID, request)
		completed <- executionOutcome{result: result, err: executionErr}
	}()
	ticker := time.NewTicker(recoveryProgressPollInterval)
	defer ticker.Stop()
	mirroredAgentStages := 1
	sequence := uint64(3)
	var agentPrefix []RestartStage
	var persistenceErr error
	var result RestartExecutionResult
	var executionErr error
	executionFinished := false
	for !executionFinished {
		select {
		case outcome := <-completed:
			result = outcome.result
			executionErr = outcome.err
			executionFinished = true
		case <-ticker.C:
			if persistenceErr != nil {
				continue
			}
			progressCtx, cancel := context.WithTimeout(ctx, recoveryProgressRequestTimeout)
			progress, progressErr := s.agent.RestartProgress(progressCtx, restart.RequestID, request)
			cancel()
			if progressErr != nil || !validRestartExecutionProgress(progress, restart, mirroredAgentStages, agentPrefix) {
				continue
			}
			available := nonTerminalRestartStageCount(progress)
			if available <= mirroredAgentStages {
				continue
			}
			var mirrorErr error
			restart, sequence, mirrorErr = s.mirrorRestartStages(
				ctx, restart, progress, progress.Stages[mirroredAgentStages:available], sequence,
			)
			if mirrorErr != nil {
				persistenceErr = mirrorErr
				continue
			}
			agentPrefix = append(agentPrefix[:0], progress.Stages[:available]...)
			mirroredAgentStages = available
		case <-ctx.Done():
			return fmt.Errorf("run restart: %w", ctx.Err())
		}
	}
	if !validRestartExecutionResult(result, restart) ||
		(len(agentPrefix) > 0 && !sameRestartStagePrefix(agentPrefix, result.Stages)) ||
		mirroredAgentStages >= len(result.Stages) {
		return s.markRestartUncertain(ctx, restart, sequence, "restart_execution_evidence_invalid", executionErr)
	}
	if persistenceErr != nil {
		return s.markRestartUncertain(ctx, restart, sequence,
			"restart_stage_persistence_failed", errors.Join(persistenceErr, executionErr))
	}
	restart, sequence, err = s.mirrorRestartStages(
		ctx, restart, result, result.Stages[mirroredAgentStages:], sequence,
	)
	if err != nil {
		return err
	}
	if restart.State != result.State || restart.Stage != result.Stage || !terminalRecoveryRestartState(restart.State) {
		return s.markRestartUncertain(ctx, restart, sequence, "restart_terminal_mismatch", executionErr)
	}
	if restart.State == RestartStateSucceeded && restart.AttentionCaseID != "" {
		if err := s.repository.ResolveAttentionCase(
			ctx, restart.AttentionCaseID, AttentionResolutionRestart, string(restart.ID),
			Actor{UserID: restart.CreatedBy, RequestID: restart.RequestID}, restart.FinishedAt,
		); err != nil {
			return fmt.Errorf("resolve restart attention case: %w", err)
		}
	}
	return executionErr
}

func (s *RecoveryService) reconcileRestore(ctx context.Context, id RestoreID) error {
	restore, err := s.repository.Restore(ctx, id)
	if err != nil {
		return fmt.Errorf("reconcile restore: %w", err)
	}
	stages, err := s.repository.RestoreStages(ctx, id, 0, recoveryStageLimit)
	if err != nil || len(stages) == 0 || stages[len(stages)-1].Stage != restore.Stage {
		return errors.Join(errors.New("reconcile restore stages"), ErrConflict, err)
	}
	if restore.State == RestoreStateQueued && restore.Stage == RestoreStageQueued {
		return s.failQueuedRestore(ctx, restore, "interrupted_before_agent")
	}
	if restore.State != RestoreStateRunning && restore.State != RestoreStateRollingBack {
		return ErrConflict
	}
	target, err := s.repository.Backup(ctx, restore.TargetBackupID)
	if err != nil || !usableRestoreTargetIndex(target) || target.TreeDigest == (Digest{}) {
		return s.markRestoreUncertain(ctx, restore, uint64(len(stages)+1),
			"restore_target_index_invalid", err)
	}
	safetyEvidence, verifyErr := s.agent.VerifyBackup(ctx, restore.RequestID, restore.SafetyBackupID)
	if verifyErr != nil {
		return s.markRestoreUncertain(ctx, restore, uint64(len(stages)+1),
			"restore_safety_recovery_evidence_missing", verifyErr)
	}
	if err := s.persistRestoreSafetyBackup(ctx, restore, safetyEvidence); err != nil {
		return s.markRestoreUncertain(ctx, restore, uint64(len(stages)+1),
			"restore_safety_index_failed", err)
	}
	request := RestoreExecutionRequest{
		RestoreID: restore.ID, TargetBackupID: restore.TargetBackupID, SafetyBackupID: restore.SafetyBackupID,
		SourceDigest: restore.SourceDigest, TargetDigest: restore.TargetDigest,
		TargetTreeDigest: target.TreeDigest, SafetyTreeDigest: safetyEvidence.TreeDigest,
	}
	result, recoveryErr := s.agent.RecoverRestore(ctx, restore.RequestID, request)
	agentMirrored := len(stages) - 1
	if !validRecoveredRestoreResult(result, restore, request, stages[1:]) || agentMirrored >= len(result.Stages) {
		return s.markRestoreUncertain(ctx, restore, uint64(len(stages)+1),
			"restore_recovery_evidence_invalid", recoveryErr)
	}
	restore, sequence, err := s.mirrorRestoreStages(
		ctx, restore, result.State, result.Stage, result.ErrorCode, result.FinishedAt,
		result.Stages[agentMirrored:], uint64(len(stages)+1),
	)
	if err != nil {
		return err
	}
	if restore.State != result.State || restore.Stage != result.Stage || !terminalRecoveryRestoreState(restore.State) {
		return s.markRestoreUncertain(ctx, restore, sequence, "restore_recovery_terminal_mismatch", recoveryErr)
	}
	if restore.State == RestoreStateSucceeded && restore.AttentionCaseID != "" {
		if err := s.repository.ResolveAttentionCase(ctx, restore.AttentionCaseID,
			AttentionResolutionRestore, string(restore.ID),
			Actor{UserID: restore.CreatedBy, RequestID: restore.RequestID}, restore.FinishedAt); err != nil {
			return err
		}
	}
	return recoveryErr
}

func (s *RecoveryService) reconcileRestart(ctx context.Context, id RestartID) error {
	restart, err := s.repository.Restart(ctx, id)
	if err != nil {
		return fmt.Errorf("reconcile restart: %w", err)
	}
	stages, err := s.repository.RestartStages(ctx, id, 0, recoveryStageLimit)
	if err != nil || len(stages) == 0 || stages[len(stages)-1].Stage != restart.Stage {
		return errors.Join(errors.New("reconcile restart stages"), ErrConflict, err)
	}
	if restart.State == RestartStateQueued && restart.Stage == RestartStageQueued {
		now := s.clock.Now().UTC()
		next := restart
		next.State = RestartStateFailed
		next.Stage = RestartStageFailed
		next.LastErrorCode = "interrupted_before_agent"
		next.UpdatedAt = now
		next.FinishedAt = now
		return s.repository.TransitionRestart(ctx, restart.State, restart.Stage, next, RestartStage{
			RestartID: id, Sequence: uint64(len(stages) + 1), Stage: RestartStageFailed,
			Result: StageResultFailed, Code: next.LastErrorCode, PublicDetailsJSON: "{}", OccurredAt: now,
		})
	}
	if restart.State != RestartStateRunning {
		return ErrConflict
	}
	result, recoveryErr := s.agent.RecoverRestart(ctx, restart.RequestID, RestartExecutionRequest{
		RestartID: id, ProductionDigest: restart.ProductionDigest,
	})
	agentMirrored := len(stages) - 1
	if !validRestartExecutionResult(result, restart) || agentMirrored >= len(result.Stages) ||
		!sameRestartStagePrefix(stages[1:], result.Stages) {
		return s.markRestartUncertain(ctx, restart, uint64(len(stages)+1),
			"restart_recovery_evidence_invalid", recoveryErr)
	}
	restart, sequence, err := s.mirrorRestartStages(
		ctx, restart, result, result.Stages[agentMirrored:], uint64(len(stages)+1),
	)
	if err != nil {
		return err
	}
	if restart.State != result.State || restart.Stage != result.Stage || !terminalRecoveryRestartState(restart.State) {
		return s.markRestartUncertain(ctx, restart, sequence, "restart_recovery_terminal_mismatch", recoveryErr)
	}
	if restart.State == RestartStateSucceeded && restart.AttentionCaseID != "" {
		if err := s.repository.ResolveAttentionCase(ctx, restart.AttentionCaseID,
			AttentionResolutionRestart, string(restart.ID),
			Actor{UserID: restart.CreatedBy, RequestID: restart.RequestID}, restart.FinishedAt); err != nil {
			return err
		}
	}
	return recoveryErr
}

func (s *RecoveryService) verifiedRestoreTarget(
	ctx context.Context,
	requestID string,
	id BackupID,
) (Backup, error) {
	backup, err := s.repository.Backup(ctx, id)
	if err != nil {
		return Backup{}, errors.Join(ErrBackupTargetInvalid, err)
	}
	if !usableRestoreTargetIndex(backup) {
		return Backup{}, ErrBackupTargetInvalid
	}
	evidence, err := s.agent.VerifyBackup(ctx, requestID, id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Backup{}, errors.Join(ErrBackupTargetInvalid, err)
		}
		return Backup{}, fmt.Errorf("verify restore backup: %w", err)
	}
	if !restoreTargetEvidenceMatchesIndex(backup, evidence, backup.TreeDigest == (Digest{})) {
		return Backup{}, ErrBackupTargetInvalid
	}
	if backup.TreeDigest == (Digest{}) {
		backup.TreeDigest = evidence.TreeDigest
		backup.EntryCount = evidence.EntryCount
		backup.TotalBytes = evidence.TotalBytes
		backup.VerifiedAt = evidence.VerifiedAt
		if err := s.repository.PutBackup(ctx, backup); err != nil {
			return Backup{}, fmt.Errorf("upgrade backup integrity index: %w", err)
		}
	}
	return backup, nil
}

func (s *RecoveryService) persistRestoreSafetyBackup(
	ctx context.Context,
	restore Restore,
	evidence BackupEvidence,
) error {
	if evidence.BackupID != restore.SafetyBackupID || evidence.OriginType != BackupOriginRestore ||
		evidence.OriginID != string(restore.ID) || evidence.ReleaseID != "" ||
		evidence.ProductionDigest != restore.SourceDigest || evidence.TreeDigest == (Digest{}) ||
		evidence.EntryCount <= 0 || evidence.TotalBytes < 0 || evidence.VerifiedAt.IsZero() {
		return ErrConflict
	}
	return s.repository.PutBackup(ctx, Backup{
		ID: evidence.BackupID, OriginType: BackupOriginRestore, OriginID: string(restore.ID),
		ProductionDigest: evidence.ProductionDigest, TreeDigest: evidence.TreeDigest,
		State: BackupStateComplete, EntryCount: evidence.EntryCount, TotalBytes: evidence.TotalBytes,
		BodyPresent: true, CreatedAt: restore.CreatedAt, VerifiedAt: evidence.VerifiedAt,
	})
}

func (s *RecoveryService) mirrorRestoreStages(
	ctx context.Context,
	restore Restore,
	resultState RestoreState,
	resultStage RestoreStageName,
	errorCode string,
	finishedAt time.Time,
	stages []RestoreStage,
	sequence uint64,
) (Restore, uint64, error) {
	for index, agentStage := range stages {
		if sequence > recoveryStageLimit {
			return restore, sequence, s.markRestoreUncertain(ctx, restore, sequence, "restore_stage_limit", ErrLimitExceeded)
		}
		next := restore
		next.Stage = agentStage.Stage
		next.State = restoreStateForRecoveryStage(agentStage.Stage)
		if index == len(stages)-1 && agentStage.Stage == resultStage && terminalRecoveryRestoreState(resultState) {
			next.State = resultState
		}
		next.UpdatedAt = agentStage.OccurredAt.UTC()
		next.LastErrorCode = agentStage.Code
		if terminalRecoveryRestoreState(next.State) {
			next.FinishedAt = finishedAt.UTC()
			next.LastErrorCode = errorCode
		}
		stage := agentStage
		stage.RestoreID = restore.ID
		stage.Sequence = sequence
		stage.OccurredAt = stage.OccurredAt.UTC()
		if err := s.repository.TransitionRestore(ctx, restore.State, restore.Stage, next, stage); err != nil {
			return restore, sequence, fmt.Errorf("persist restore stage: %w", err)
		}
		restore = next
		sequence++
	}
	return restore, sequence, nil
}

func (s *RecoveryService) mirrorRestartStages(
	ctx context.Context,
	restart Restart,
	result RestartExecutionResult,
	stages []RestartStage,
	sequence uint64,
) (Restart, uint64, error) {
	for index, agentStage := range stages {
		if sequence > recoveryStageLimit {
			return restart, sequence, s.markRestartUncertain(ctx, restart, sequence, "restart_stage_limit", ErrLimitExceeded)
		}
		next := restart
		next.Stage = agentStage.Stage
		next.State = restartStateForRecoveryStage(agentStage.Stage)
		if index == len(stages)-1 && agentStage.Stage == result.Stage && terminalRecoveryRestartState(result.State) {
			next.State = result.State
		}
		next.UpdatedAt = agentStage.OccurredAt.UTC()
		next.LastErrorCode = agentStage.Code
		if terminalRecoveryRestartState(next.State) {
			next.FinishedAt = result.FinishedAt.UTC()
			next.LastErrorCode = result.ErrorCode
			next.BeforeMasterPID = result.BeforeMasterPID
			next.AfterMasterPID = result.AfterMasterPID
			next.WorkerCount = result.WorkerCount
			next.HTTPStatus = result.HTTPStatus
		}
		stage := agentStage
		stage.RestartID = restart.ID
		stage.Sequence = sequence
		stage.OccurredAt = stage.OccurredAt.UTC()
		if err := s.repository.TransitionRestart(ctx, restart.State, restart.Stage, next, stage); err != nil {
			return restart, sequence, fmt.Errorf("persist restart stage: %w", err)
		}
		restart = next
		sequence++
	}
	return restart, sequence, nil
}

func (s *RecoveryService) failQueuedRestore(ctx context.Context, restore Restore, code string) error {
	now := s.clock.Now().UTC()
	next := restore
	next.State = RestoreStateFailed
	next.Stage = RestoreStageFailed
	next.LastErrorCode = code
	next.UpdatedAt = now
	next.FinishedAt = now
	return s.repository.TransitionRestore(ctx, restore.State, restore.Stage, next, RestoreStage{
		RestoreID: restore.ID, Sequence: 2, Stage: RestoreStageFailed, Result: StageResultFailed,
		Code: code, PublicDetailsJSON: "{}", OccurredAt: now,
	})
}

func (s *RecoveryService) markRestoreUncertain(
	ctx context.Context,
	restore Restore,
	sequence uint64,
	code string,
	primary error,
) error {
	now := s.clock.Now().UTC()
	next := restore
	next.State = RestoreStateNeedsAttention
	next.Stage = RestoreStageNeedsAttention
	next.LastErrorCode = code
	next.UpdatedAt = now
	next.FinishedAt = now
	err := s.repository.TransitionRestore(ctx, restore.State, restore.Stage, next, RestoreStage{
		RestoreID: restore.ID, Sequence: sequence, Stage: RestoreStageNeedsAttention,
		Result: StageResultFailed, Code: code, PublicDetailsJSON: "{}", OccurredAt: now,
	})
	return errors.Join(primary, err)
}

func (s *RecoveryService) markRestartUncertain(
	ctx context.Context,
	restart Restart,
	sequence uint64,
	code string,
	primary error,
) error {
	now := s.clock.Now().UTC()
	next := restart
	next.State = RestartStateNeedsAttention
	next.Stage = RestartStageNeedsAttention
	next.LastErrorCode = code
	next.UpdatedAt = now
	next.FinishedAt = now
	err := s.repository.TransitionRestart(ctx, restart.State, restart.Stage, next, RestartStage{
		RestartID: restart.ID, Sequence: sequence, Stage: RestartStageNeedsAttention,
		Result: StageResultFailed, Code: code, PublicDetailsJSON: "{}", OccurredAt: now,
	})
	return errors.Join(primary, err)
}

func (s *RecoveryService) requireOpenAttention(ctx context.Context, id AttentionCaseID) error {
	if id == "" {
		return nil
	}
	if _, err := ParseAttentionCaseID(string(id)); err != nil {
		return err
	}
	attention, err := s.repository.AttentionCase(ctx, id)
	if err != nil {
		return fmt.Errorf("read attention case: %w", err)
	}
	if attention.ID != id || attention.State != AttentionCaseOpen {
		return ErrAttentionUnresolved
	}
	return nil
}

func usableRestoreTargetIndex(backup Backup) bool {
	return backup.ID != "" && backup.State == BackupStateComplete && backup.BodyPresent &&
		backup.ProductionDigest != (Digest{}) && backup.EntryCount >= 0 && backup.TotalBytes >= 0 &&
		!backup.CreatedAt.IsZero() && !backup.VerifiedAt.IsZero()
}

func restoreTargetEvidenceMatchesIndex(backup Backup, evidence BackupEvidence, allowMissingTree bool) bool {
	if evidence.BackupID != backup.ID || evidence.OriginType != backup.OriginType ||
		evidence.OriginID != backup.OriginID || evidence.ReleaseID != backup.ReleaseID ||
		evidence.ProductionDigest != backup.ProductionDigest || evidence.TreeDigest == (Digest{}) ||
		evidence.EntryCount <= 0 || evidence.TotalBytes < 0 || evidence.VerifiedAt.IsZero() {
		return false
	}
	if allowMissingTree {
		return true
	}
	return evidence.TreeDigest == backup.TreeDigest && evidence.EntryCount == backup.EntryCount &&
		evidence.TotalBytes == backup.TotalBytes && evidence.VerifiedAt.Equal(backup.VerifiedAt)
}

func sameRestoreTargetIndex(left, right Backup) bool {
	return left.ID == right.ID && left.OriginType == right.OriginType && left.OriginID == right.OriginID &&
		left.ReleaseID == right.ReleaseID && left.ProductionDigest == right.ProductionDigest &&
		left.TreeDigest == right.TreeDigest && left.EntryCount == right.EntryCount &&
		left.TotalBytes == right.TotalBytes && left.VerifiedAt.Equal(right.VerifiedAt)
}

func validRestorePreparation(
	result RestorePreparationResult,
	restore Restore,
	request RestoreExecutionRequest,
) bool {
	if result.RestoreID != restore.ID || len(result.Stages) == 0 ||
		result.Stages[0].Stage != RestoreStageTargetVerifying ||
		result.Stage != result.Stages[len(result.Stages)-1].Stage ||
		restoreStateForRecoveryStage(result.Stage) != result.State ||
		!validRestoreAgentStages(result.Stages, restore.ID) {
		return false
	}
	if terminalRecoveryRestoreState(result.State) {
		return !result.FinishedAt.IsZero()
	}
	return result.State == RestoreStateRunning && result.Stage == RestoreStageSafetyBackupVerified &&
		result.FinishedAt.IsZero() && result.SafetyBackup.BackupID == request.SafetyBackupID &&
		result.SafetyBackup.ProductionDigest == request.SourceDigest
}

func validRestoreExecutionResult(
	result RestoreExecutionResult,
	restore Restore,
	request RestoreExecutionRequest,
	preparation []RestoreStage,
) bool {
	if result.RestoreID != restore.ID || !terminalRecoveryRestoreState(result.State) ||
		result.FinishedAt.IsZero() || len(result.Stages) <= len(preparation) ||
		result.Stage != result.Stages[len(result.Stages)-1].Stage ||
		restoreStateForRecoveryStage(result.Stage) != result.State ||
		!validRestoreAgentStages(result.Stages, restore.ID) {
		return false
	}
	for index := range preparation {
		if !sameRestoreAgentStage(preparation[index], result.Stages[index]) {
			return false
		}
	}
	return result.SafetyBackup.BackupID == request.SafetyBackupID &&
		result.SafetyBackup.TreeDigest == request.SafetyTreeDigest
}

func validRestoreExecutionProgress(
	result RestoreExecutionResult,
	restore Restore,
	request RestoreExecutionRequest,
	mirrored int,
	prefix []RestoreStage,
) bool {
	if result.RestoreID != restore.ID || mirrored < 1 || len(result.Stages) < mirrored ||
		len(result.Stages) >= recoveryStageLimit || len(result.Stages) == 0 ||
		result.Stages[0].Stage != RestoreStageTargetVerifying ||
		result.Stage != result.Stages[len(result.Stages)-1].Stage ||
		restoreStateForRecoveryStage(result.Stage) != result.State ||
		!validRestoreAgentStages(result.Stages, restore.ID) ||
		result.SafetyBackup.BackupID != request.SafetyBackupID ||
		result.SafetyBackup.TreeDigest != request.SafetyTreeDigest {
		return false
	}
	if result.State != RestoreStateRunning && result.State != RestoreStateRollingBack &&
		!terminalRecoveryRestoreState(result.State) {
		return false
	}
	if terminalRecoveryRestoreState(result.State) != !result.FinishedAt.IsZero() {
		return false
	}
	if mirrored > 1 && result.Stages[mirrored-1].Stage != restore.Stage {
		return false
	}
	return sameRestoreStagePrefix(prefix, result.Stages)
}

func sameRestoreStagePrefix(prefix, stages []RestoreStage) bool {
	if len(prefix) > len(stages) {
		return false
	}
	for index := range prefix {
		if !sameRestoreAgentStage(prefix[index], stages[index]) {
			return false
		}
	}
	return true
}

func nonTerminalRestoreStageCount(result RestoreExecutionResult) int {
	if terminalRecoveryRestoreState(result.State) && len(result.Stages) > 0 {
		return len(result.Stages) - 1
	}
	return len(result.Stages)
}

func validRecoveredRestoreResult(
	result RestoreExecutionResult,
	restore Restore,
	request RestoreExecutionRequest,
	persisted []RestoreStage,
) bool {
	if result.RestoreID != restore.ID || !terminalRecoveryRestoreState(result.State) ||
		result.FinishedAt.IsZero() || len(result.Stages) <= len(persisted) ||
		result.Stage != result.Stages[len(result.Stages)-1].Stage ||
		restoreStateForRecoveryStage(result.Stage) != result.State ||
		!validRestoreAgentStages(result.Stages, restore.ID) ||
		result.SafetyBackup.BackupID != request.SafetyBackupID ||
		result.SafetyBackup.TreeDigest != request.SafetyTreeDigest {
		return false
	}
	for index := range persisted {
		if !sameRecoveryRestoreStage(persisted[index], result.Stages[index]) {
			return false
		}
	}
	return true
}

func validRestoreAgentStages(stages []RestoreStage, id RestoreID) bool {
	lastOrder := -1
	for index, stage := range stages {
		order := recoveryRestoreStageOrder(stage.Stage)
		if stage.RestoreID != id || stage.Sequence != uint64(index+1) || order < 0 || order < lastOrder ||
			!knownRecoveryStageResult(stage.Result) || stage.OccurredAt.IsZero() ||
			stage.OccurredAt.Location() != time.UTC || !json.Valid([]byte(stage.PublicDetailsJSON)) {
			return false
		}
		lastOrder = order
	}
	return true
}

func sameRestoreAgentStage(left, right RestoreStage) bool {
	return left.RestoreID == right.RestoreID && left.Sequence == right.Sequence && left.Stage == right.Stage &&
		left.Result == right.Result && left.Code == right.Code && left.PublicDetailsJSON == right.PublicDetailsJSON &&
		left.OccurredAt.Equal(right.OccurredAt)
}

func sameRecoveryRestoreStage(left, right RestoreStage) bool {
	return left.RestoreID == right.RestoreID && left.Stage == right.Stage && left.Result == right.Result &&
		left.Code == right.Code && left.PublicDetailsJSON == right.PublicDetailsJSON &&
		left.OccurredAt.Equal(right.OccurredAt)
}

func validRestartExecutionResult(result RestartExecutionResult, restart Restart) bool {
	if result.RestartID != restart.ID || !terminalRecoveryRestartState(result.State) ||
		result.FinishedAt.IsZero() || len(result.Stages) < 2 ||
		result.Stages[0].Stage != RestartStageProductionValidating ||
		result.Stage != result.Stages[len(result.Stages)-1].Stage ||
		restartStateForRecoveryStage(result.Stage) != result.State {
		return false
	}
	lastOrder := -1
	for index, stage := range result.Stages {
		order := recoveryRestartStageOrder(stage.Stage)
		if stage.RestartID != restart.ID || stage.Sequence != uint64(index+1) || order < 0 || order < lastOrder ||
			!knownRecoveryStageResult(stage.Result) || stage.OccurredAt.IsZero() ||
			stage.OccurredAt.Location() != time.UTC || !json.Valid([]byte(stage.PublicDetailsJSON)) {
			return false
		}
		lastOrder = order
	}
	return true
}

func validRestartExecutionProgress(
	result RestartExecutionResult,
	restart Restart,
	mirrored int,
	prefix []RestartStage,
) bool {
	if result.RestartID != restart.ID || mirrored < 1 || len(result.Stages) < mirrored ||
		len(result.Stages) >= recoveryStageLimit || len(result.Stages) == 0 ||
		result.Stages[0].Stage != RestartStageProductionValidating ||
		result.Stage != result.Stages[len(result.Stages)-1].Stage ||
		restartStateForRecoveryStage(result.Stage) != result.State {
		return false
	}
	if result.State != RestartStateRunning && !terminalRecoveryRestartState(result.State) {
		return false
	}
	if terminalRecoveryRestartState(result.State) != !result.FinishedAt.IsZero() {
		return false
	}
	lastOrder := -1
	for index, stage := range result.Stages {
		order := recoveryRestartStageOrder(stage.Stage)
		if stage.RestartID != restart.ID || stage.Sequence != uint64(index+1) ||
			order < 0 || order < lastOrder || !knownRecoveryStageResult(stage.Result) ||
			stage.OccurredAt.IsZero() || stage.OccurredAt.Location() != time.UTC ||
			!json.Valid([]byte(stage.PublicDetailsJSON)) {
			return false
		}
		lastOrder = order
	}
	if mirrored > 1 && result.Stages[mirrored-1].Stage != restart.Stage {
		return false
	}
	return len(prefix) == 0 || sameRestartStagePrefix(prefix, result.Stages)
}

func nonTerminalRestartStageCount(result RestartExecutionResult) int {
	if terminalRecoveryRestartState(result.State) && len(result.Stages) > 0 {
		return len(result.Stages) - 1
	}
	return len(result.Stages)
}

func validRuntimeVerificationResult(
	result RuntimeVerificationResult,
	id VerificationID,
	digest Digest,
) bool {
	if result.VerificationID != id || result.ProductionDigest != digest ||
		result.CheckedAt.IsZero() || result.CheckedAt.Location() != time.UTC ||
		result.MasterPID < 0 || result.WorkerCount < 0 || result.HTTPStatus < 0 || result.HTTPStatus > 599 {
		return false
	}
	switch result.State {
	case VerificationStateSucceeded:
		return result.ErrorCode == "" && result.MasterPID > 0 && result.WorkerCount > 0 &&
			result.HTTPStatus >= 200 && result.HTTPStatus < 300
	case VerificationStateFailed:
		return result.ErrorCode != ""
	default:
		return false
	}
}

func sameRestartStagePrefix(persisted, agent []RestartStage) bool {
	if len(persisted) > len(agent) {
		return false
	}
	for index := range persisted {
		if persisted[index].RestartID != agent[index].RestartID || persisted[index].Stage != agent[index].Stage ||
			persisted[index].Result != agent[index].Result || persisted[index].Code != agent[index].Code ||
			persisted[index].PublicDetailsJSON != agent[index].PublicDetailsJSON ||
			!persisted[index].OccurredAt.Equal(agent[index].OccurredAt) {
			return false
		}
	}
	return true
}

func knownRecoveryStageResult(result StageResult) bool {
	switch result {
	case StageResultPending, StageResultRunning, StageResultSuccess, StageResultFailed, StageResultWarning:
		return true
	default:
		return false
	}
}

func restoreStateForRecoveryStage(stage RestoreStageName) RestoreState {
	switch stage {
	case RestoreStageQueued:
		return RestoreStateQueued
	case RestoreStageTargetVerifying, RestoreStageTargetValidated, RestoreStageSafetyBackupCreating,
		RestoreStageSafetyBackupVerified, RestoreStageFilesRestoring, RestoreStageFilesRestored,
		RestoreStageProductionValidated, RestoreStageReloadRequested, RestoreStageRuntimeConfirmed:
		return RestoreStateRunning
	case RestoreStageRollbackApplying, RestoreStageRollbackFilesRestored,
		RestoreStageRollbackValidated, RestoreStageRollbackReloadRequested:
		return RestoreStateRollingBack
	case RestoreStageSucceeded:
		return RestoreStateSucceeded
	case RestoreStageFailed:
		return RestoreStateFailed
	case RestoreStageRolledBack:
		return RestoreStateRolledBack
	case RestoreStageNeedsAttention:
		return RestoreStateNeedsAttention
	default:
		return ""
	}
}

func restartStateForRecoveryStage(stage RestartStageName) RestartState {
	switch stage {
	case RestartStageQueued:
		return RestartStateQueued
	case RestartStageProductionValidating, RestartStageRuntimeSampling,
		RestartStageRestartRequested, RestartStageRuntimeConfirming:
		return RestartStateRunning
	case RestartStageSucceeded:
		return RestartStateSucceeded
	case RestartStageFailed:
		return RestartStateFailed
	case RestartStageNeedsAttention:
		return RestartStateNeedsAttention
	default:
		return ""
	}
}

func terminalRecoveryRestoreState(state RestoreState) bool {
	switch state {
	case RestoreStateSucceeded, RestoreStateFailed, RestoreStateRolledBack,
		RestoreStateNeedsAttention, RestoreStateCancelled:
		return true
	case RestoreStateQueued, RestoreStateRunning, RestoreStateRollingBack:
		return false
	default:
		return false
	}
}

func terminalRecoveryRestartState(state RestartState) bool {
	switch state {
	case RestartStateSucceeded, RestartStateFailed, RestartStateNeedsAttention, RestartStateCancelled:
		return true
	case RestartStateQueued, RestartStateRunning:
		return false
	default:
		return false
	}
}

func recoveryRestoreStageOrder(stage RestoreStageName) int {
	switch stage {
	case RestoreStageQueued:
		return 0
	case RestoreStageTargetVerifying:
		return 1
	case RestoreStageTargetValidated:
		return 2
	case RestoreStageSafetyBackupCreating:
		return 3
	case RestoreStageSafetyBackupVerified:
		return 4
	case RestoreStageFilesRestoring:
		return 5
	case RestoreStageFilesRestored:
		return 6
	case RestoreStageProductionValidated:
		return 7
	case RestoreStageReloadRequested:
		return 8
	case RestoreStageRuntimeConfirmed:
		return 9
	case RestoreStageSucceeded:
		return 10
	case RestoreStageRollbackApplying:
		return 20
	case RestoreStageRollbackFilesRestored:
		return 21
	case RestoreStageRollbackValidated:
		return 22
	case RestoreStageRollbackReloadRequested:
		return 23
	case RestoreStageRolledBack:
		return 24
	case RestoreStageFailed:
		return 30
	case RestoreStageNeedsAttention:
		return 31
	default:
		return -1
	}
}

func recoveryRestartStageOrder(stage RestartStageName) int {
	switch stage {
	case RestartStageQueued:
		return 0
	case RestartStageProductionValidating:
		return 1
	case RestartStageRuntimeSampling:
		return 2
	case RestartStageRestartRequested:
		return 3
	case RestartStageRuntimeConfirming:
		return 4
	case RestartStageSucceeded:
		return 5
	case RestartStageFailed:
		return 10
	case RestartStageNeedsAttention:
		return 11
	default:
		return -1
	}
}

func validRecoveryInputReason(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
