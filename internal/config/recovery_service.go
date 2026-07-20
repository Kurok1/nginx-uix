/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"slices"
	"time"
)

const (
	retentionPlanLifetime = 10 * time.Minute
	attentionReadLimit    = 100
)

// RecoveryDependencies contains explicit recovery-and-history collaborators.
type RecoveryDependencies struct {
	Repository RecoveryRepository
	Agent      RecoveryAgent
	Clock      Clock
	Random     io.Reader
	Policy     RetentionPolicy
}

// QueueRetentionExecution acquires the production lease for one unexpired, exactly confirmed plan.
func (s *RecoveryService) QueueRetentionExecution(
	ctx context.Context,
	actor Actor,
	id RetentionRunID,
	confirmation string,
) (RetentionRun, error) {
	if ctx == nil || s == nil || s.repository == nil || s.clock == nil {
		return RetentionRun{}, errors.New("queue backup retention: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return RetentionRun{}, err
	}
	if _, err := ParseRetentionRunID(string(id)); err != nil {
		return RetentionRun{}, err
	}
	if confirmation != string(id) {
		return RetentionRun{}, ErrConflict
	}
	run, _, err := s.repository.RetentionRun(ctx, id)
	if err != nil {
		return RetentionRun{}, fmt.Errorf("queue backup retention: %w", err)
	}
	if run.State != RetentionRunPlanned {
		return RetentionRun{}, ErrConflict
	}
	now := s.clock.Now().UTC()
	if now.IsZero() {
		return RetentionRun{}, errors.New("queue backup retention: clock is invalid")
	}
	if !now.Before(run.ExpiresAt) {
		expired := run
		expired.State = RetentionRunExpired
		expired.LastErrorCode = "retention_plan_expired"
		expired.FinishedAt = now
		if transitionErr := s.repository.TransitionRetentionRun(ctx, RetentionRunPlanned, expired); transitionErr != nil {
			return RetentionRun{}, fmt.Errorf("expire backup retention: %w", transitionErr)
		}
		return expired, ErrRetentionPlanExpired
	}
	queued := run
	queued.State = RetentionRunExecuting
	queued.ExecutionRequestID = actor.RequestID
	queued.StartedAt = now
	if err := s.repository.TransitionRetentionRun(ctx, RetentionRunPlanned, queued); err != nil {
		return RetentionRun{}, fmt.Errorf("queue backup retention: %w", err)
	}
	return queued, nil
}

// RunRetention executes one persisted plan serially, preserving a tombstone for every completed deletion.
func (s *RecoveryService) RunRetention(ctx context.Context, id RetentionRunID) error {
	if ctx == nil || s == nil || s.repository == nil || s.agent == nil || s.clock == nil {
		return errors.New("run backup retention: service is unavailable")
	}
	run, items, err := s.repository.RetentionRun(ctx, id)
	if err != nil {
		return fmt.Errorf("run backup retention: %w", err)
	}
	if run.State != RetentionRunExecuting || run.ExecutionRequestID == "" || len(items) != run.BackupCount {
		return ErrConflict
	}
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("run backup retention: %w", err)
		}
		if item.RunID != id || item.Ordinal != index {
			return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
				"retention_plan_evidence_invalid", ErrConflict)
		}
		switch item.State {
		case RetentionItemKept:
			if item.Decision != RetentionDecisionKeep {
				return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
					"retention_plan_evidence_invalid", ErrConflict)
			}
			continue
		case RetentionItemDeleted, RetentionItemSkippedProtected:
			if item.Decision != RetentionDecisionDelete {
				return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
					"retention_plan_evidence_invalid", ErrConflict)
			}
			continue
		case RetentionItemDeleting:
			if item.Decision != RetentionDecisionDelete {
				return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
					"retention_plan_evidence_invalid", ErrConflict)
			}
			if err := s.resumeRetentionDeletion(ctx, run, item); err != nil {
				return err
			}
			continue
		case RetentionItemPlanned:
		case RetentionItemFailed, RetentionItemNeedsAttention:
			return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
				"retention_interrupted_terminal_item", ErrConflict)
		default:
			return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
				"retention_plan_evidence_invalid", ErrConflict)
		}
		now := s.clock.Now().UTC()
		if item.Decision == RetentionDecisionKeep {
			if err := s.repository.TransitionRetentionItem(
				ctx, id, item.Ordinal, RetentionItemPlanned, RetentionItemKept, now,
			); err != nil {
				return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
					"retention_item_persistence_failed", err)
			}
			continue
		}
		if item.Decision != RetentionDecisionDelete {
			return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
				"retention_plan_evidence_invalid", ErrConflict)
		}
		backup, err := s.repository.Backup(ctx, item.BackupID)
		if err != nil || !retentionBackupMatchesSnapshot(backup, item) {
			_ = s.repository.TransitionRetentionItem(
				ctx, id, item.Ordinal, RetentionItemPlanned, RetentionItemFailed, now,
			)
			return s.finishRetentionWithError(ctx, id, RetentionRunFailed,
				"retention_plan_changed", errors.Join(ErrRetentionPlanExpired, err))
		}
		evidence, err := s.agent.VerifyBackup(ctx, run.ExecutionRequestID, backup.ID)
		if err != nil || !retentionBackupEvidenceMatches(backup, evidence) {
			_ = s.repository.TransitionRetentionItem(
				ctx, id, item.Ordinal, RetentionItemPlanned, RetentionItemFailed, now,
			)
			return s.finishRetentionWithError(ctx, id, RetentionRunFailed,
				"backup_verification_failed", errors.Join(ErrRetentionPlanExpired, err))
		}
		err = s.repository.BeginRetentionDeletion(
			ctx, id, item.Ordinal, backup.ID, item.SnapshotCreatedAt, item.SnapshotTotalBytes, now,
		)
		if errors.Is(err, ErrBackupProtected) {
			if transitionErr := s.repository.TransitionRetentionItem(
				ctx, id, item.Ordinal, RetentionItemPlanned, RetentionItemSkippedProtected, now,
			); transitionErr != nil {
				return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
					"retention_item_persistence_failed", transitionErr)
			}
			continue
		}
		if err != nil {
			_ = s.repository.TransitionRetentionItem(
				ctx, id, item.Ordinal, RetentionItemPlanned, RetentionItemFailed, now,
			)
			return s.finishRetentionWithError(ctx, id, RetentionRunFailed, "retention_plan_changed", err)
		}
		deletion := BackupDeletionRequest{
			RunID: id, BackupID: backup.ID, ProductionDigest: backup.ProductionDigest,
			TreeDigest: backup.TreeDigest, SnapshotCreatedAt: item.SnapshotCreatedAt,
			SnapshotTotalBytes: item.SnapshotTotalBytes,
		}
		if err := s.agent.DeleteBackup(ctx, run.ExecutionRequestID, deletion); err != nil {
			if reconcileErr := s.reconcileFailedRetentionDeletion(ctx, run, item, backup, err); reconcileErr != nil {
				return reconcileErr
			}
			continue
		}
		if err := s.repository.CompleteRetentionDeletion(ctx, id, item.Ordinal, backup.ID, s.clock.Now().UTC()); err != nil {
			return s.finishRetentionWithError(ctx, id, RetentionRunNeedsAttention,
				"retention_tombstone_failed", err)
		}
	}
	return s.finishRetentionWithError(ctx, id, RetentionRunSucceeded, "", nil)
}

func (s *RecoveryService) resumeRetentionDeletion(
	ctx context.Context,
	run RetentionRun,
	item RetentionItem,
) error {
	backup, err := s.repository.Backup(ctx, item.BackupID)
	if err != nil || backup.State != BackupStateDeleting || !backup.BodyPresent ||
		!backup.CreatedAt.Equal(item.SnapshotCreatedAt) || backup.TotalBytes != item.SnapshotTotalBytes ||
		backup.ProductionDigest == (Digest{}) || backup.TreeDigest == (Digest{}) {
		return s.finishRetentionWithError(ctx, run.ID, RetentionRunNeedsAttention,
			"retention_deletion_evidence_invalid", errors.Join(ErrConflict, err))
	}
	evidence, verifyErr := s.agent.VerifyBackup(ctx, run.ExecutionRequestID, backup.ID)
	if errors.Is(verifyErr, fs.ErrNotExist) {
		if err := s.repository.CompleteRetentionDeletion(
			ctx, run.ID, item.Ordinal, backup.ID, s.clock.Now().UTC(),
		); err != nil {
			return s.finishRetentionWithError(ctx, run.ID, RetentionRunNeedsAttention,
				"retention_tombstone_failed", err)
		}
		return nil
	}
	if verifyErr != nil || !retentionBackupEvidenceMatches(backup, evidence) {
		uncertainErr := s.repository.MarkRetentionDeletionUncertain(
			ctx, run.ID, item.Ordinal, backup.ID, s.clock.Now().UTC(),
		)
		return s.finishRetentionWithError(ctx, run.ID, RetentionRunNeedsAttention,
			"backup_delete_uncertain", errors.Join(verifyErr, uncertainErr))
	}
	request := BackupDeletionRequest{
		RunID: run.ID, BackupID: backup.ID, ProductionDigest: backup.ProductionDigest,
		TreeDigest: backup.TreeDigest, SnapshotCreatedAt: item.SnapshotCreatedAt,
		SnapshotTotalBytes: item.SnapshotTotalBytes,
	}
	if err := s.agent.DeleteBackup(ctx, run.ExecutionRequestID, request); err != nil {
		return s.reconcileFailedRetentionDeletion(ctx, run, item, backup, err)
	}
	if err := s.repository.CompleteRetentionDeletion(
		ctx, run.ID, item.Ordinal, backup.ID, s.clock.Now().UTC(),
	); err != nil {
		return s.finishRetentionWithError(ctx, run.ID, RetentionRunNeedsAttention,
			"retention_tombstone_failed", err)
	}
	return nil
}

func (s *RecoveryService) reconcileFailedRetentionDeletion(
	ctx context.Context,
	run RetentionRun,
	item RetentionItem,
	backup Backup,
	deleteErr error,
) error {
	now := s.clock.Now().UTC()
	evidence, verifyErr := s.agent.VerifyBackup(ctx, run.ExecutionRequestID, backup.ID)
	switch {
	case verifyErr == nil && retentionBackupEvidenceMatches(backup, evidence):
		abortErr := s.repository.AbortRetentionDeletion(
			ctx, run.ID, item.Ordinal, backup.ID, RetentionItemFailed, now,
		)
		return s.finishRetentionWithError(ctx, run.ID, RetentionRunFailed,
			"backup_delete_failed", errors.Join(deleteErr, abortErr))
	case errors.Is(verifyErr, fs.ErrNotExist):
		if err := s.repository.CompleteRetentionDeletion(ctx, run.ID, item.Ordinal, backup.ID, now); err != nil {
			return s.finishRetentionWithError(ctx, run.ID, RetentionRunNeedsAttention,
				"retention_tombstone_failed", errors.Join(deleteErr, err))
		}
		return nil
	default:
		uncertainErr := s.repository.MarkRetentionDeletionUncertain(ctx, run.ID, item.Ordinal, backup.ID, now)
		return s.finishRetentionWithError(ctx, run.ID, RetentionRunNeedsAttention,
			"backup_delete_uncertain", errors.Join(deleteErr, verifyErr, uncertainErr))
	}
}

func (s *RecoveryService) finishRetentionWithError(
	ctx context.Context,
	id RetentionRunID,
	state RetentionRunState,
	code string,
	primary error,
) error {
	run, _, err := s.repository.RetentionRun(ctx, id)
	if err != nil {
		return errors.Join(primary, err)
	}
	if run.State != RetentionRunExecuting {
		return errors.Join(primary, ErrConflict)
	}
	run.State = state
	run.LastErrorCode = code
	run.FinishedAt = s.clock.Now().UTC()
	transitionErr := s.repository.TransitionRetentionRun(ctx, RetentionRunExecuting, run)
	return errors.Join(primary, transitionErr)
}

func retentionBackupMatchesSnapshot(backup Backup, item RetentionItem) bool {
	return backup.ID == item.BackupID && backup.State == BackupStateComplete && backup.BodyPresent &&
		backup.CreatedAt.Equal(item.SnapshotCreatedAt) && backup.TotalBytes == item.SnapshotTotalBytes &&
		backup.ProductionDigest != (Digest{}) && backup.TreeDigest != (Digest{}) &&
		backup.EntryCount >= 0 && !backup.VerifiedAt.IsZero()
}

func retentionBackupEvidenceMatches(backup Backup, evidence BackupEvidence) bool {
	return evidence.BackupID == backup.ID && evidence.ProductionDigest == backup.ProductionDigest &&
		evidence.TreeDigest == backup.TreeDigest && evidence.EntryCount == backup.EntryCount &&
		evidence.TotalBytes == backup.TotalBytes && evidence.VerifiedAt.Equal(backup.VerifiedAt)
}

// RecoveryService owns backup retention, manual recovery, restart, and attention orchestration.
type RecoveryService struct {
	repository RecoveryRepository
	agent      RecoveryAgent
	clock      Clock
	random     io.Reader
	policy     RetentionPolicy
}

// DefaultRetentionPolicy returns the fixed v0.2.3 recovery-point policy.
func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		MinimumComplete: 3, MaximumComplete: 20,
		MaximumTotalBytes: 4 << 30, MinimumAge: 24 * time.Hour,
	}
}

// NewRecoveryService validates and assembles the recovery domain service.
func NewRecoveryService(dependencies RecoveryDependencies) (*RecoveryService, error) {
	if dependencies.Repository == nil || dependencies.Agent == nil || dependencies.Clock == nil ||
		dependencies.Random == nil || !validRetentionPolicy(dependencies.Policy) {
		return nil, fmt.Errorf("create recovery service: invalid dependencies")
	}
	return &RecoveryService{
		repository: dependencies.Repository, agent: dependencies.Agent,
		clock: dependencies.Clock, random: dependencies.Random, policy: dependencies.Policy,
	}, nil
}

// PlanRetention snapshots complete recovery points and persists one deterministic dry-run.
func (s *RecoveryService) PlanRetention(
	ctx context.Context,
	actor Actor,
) (RetentionRun, []RetentionItem, error) {
	if ctx == nil || s == nil || s.repository == nil || s.clock == nil || s.random == nil ||
		actor.UserID <= 0 || actor.RequestID == "" {
		return RetentionRun{}, nil, fmt.Errorf("plan backup retention: invalid input")
	}
	backups, err := s.repository.RetentionBackups(ctx)
	if err != nil {
		return RetentionRun{}, nil, fmt.Errorf("plan backup retention: %w", err)
	}
	backups = append([]Backup(nil), backups...)
	slices.SortFunc(backups, func(left, right Backup) int {
		if comparison := left.CreatedAt.Compare(right.CreatedAt); comparison != 0 {
			return comparison
		}
		return stringCompare(string(left.ID), string(right.ID))
	})
	if err := validateRetentionBackups(backups); err != nil {
		return RetentionRun{}, nil, fmt.Errorf("plan backup retention: %w", err)
	}
	attention, err := s.repository.ListAttentionCases(ctx, AttentionQuery{
		State: AttentionCaseOpen, Limit: attentionReadLimit,
	})
	if err != nil {
		return RetentionRun{}, nil, fmt.Errorf("plan backup retention: %w", err)
	}
	protected := make(map[BackupID]string, len(backups))
	for _, backup := range backups {
		if backup.ManuallyProtected {
			protected[backup.ID] = "manual_protection"
		}
	}
	for _, attentionCase := range attention {
		if attentionCase.State != AttentionCaseOpen {
			return RetentionRun{}, nil, fmt.Errorf("plan backup retention: invalid attention state")
		}
		if attentionCase.BackupID != "" {
			if _, exists := protected[attentionCase.BackupID]; !exists {
				protected[attentionCase.BackupID] = "attention_case"
			}
		}
	}
	minimum := min(s.policy.MinimumComplete, len(backups))
	for index := len(backups) - minimum; index < len(backups); index++ {
		if index < 0 {
			continue
		}
		if _, exists := protected[backups[index].ID]; !exists {
			protected[backups[index].ID] = "minimum_complete"
		}
	}

	now := s.clock.Now().UTC()
	if now.IsZero() {
		return RetentionRun{}, nil, fmt.Errorf("plan backup retention: clock is invalid")
	}
	id, err := NewRetentionRunID(s.random)
	if err != nil {
		return RetentionRun{}, nil, fmt.Errorf("plan backup retention: %w", err)
	}
	totalBytes := int64(0)
	for _, backup := range backups {
		if backup.TotalBytes > math.MaxInt64-totalBytes {
			return RetentionRun{}, nil, fmt.Errorf("plan backup retention: %w", ErrLimitExceeded)
		}
		totalBytes += backup.TotalBytes
	}
	remainingCount := len(backups)
	remainingBytes := totalBytes
	items := make([]RetentionItem, 0, len(backups))
	deleteCount := 0
	deleteBytes := int64(0)
	for index, backup := range backups {
		decision := RetentionDecisionKeep
		reason := protected[backup.ID]
		if reason == "" {
			switch {
			case backup.CreatedAt.After(now.Add(-s.policy.MinimumAge)):
				reason = "minimum_age"
			case remainingCount > s.policy.MaximumComplete:
				decision = RetentionDecisionDelete
				reason = "maximum_complete"
			case remainingBytes > s.policy.MaximumTotalBytes:
				decision = RetentionDecisionDelete
				reason = "maximum_total_bytes"
			default:
				reason = "policy_satisfied"
			}
		}
		if decision == RetentionDecisionDelete {
			remainingCount--
			remainingBytes -= backup.TotalBytes
			deleteCount++
			deleteBytes += backup.TotalBytes
		}
		items = append(items, RetentionItem{
			RunID: id, Ordinal: index, BackupID: backup.ID, Decision: decision,
			ReasonCode: reason, State: RetentionItemPlanned,
			SnapshotCreatedAt: backup.CreatedAt.UTC(), SnapshotTotalBytes: backup.TotalBytes, UpdatedAt: now,
		})
	}
	run := RetentionRun{
		ID: id, State: RetentionRunPlanned, Policy: s.policy,
		BackupCount: len(backups), TotalBytes: totalBytes, ProtectedCount: len(protected),
		DeleteCount: deleteCount, DeleteBytes: deleteBytes,
		CreatedBy: actor.UserID, RequestID: actor.RequestID,
		CreatedAt: now, ExpiresAt: now.Add(retentionPlanLifetime),
	}
	if remainingCount > s.policy.MaximumComplete || remainingBytes > s.policy.MaximumTotalBytes {
		run.LastErrorCode = "retention_target_unreachable"
	}
	if err := s.repository.CreateRetentionRun(ctx, run, items); err != nil {
		return RetentionRun{}, nil, fmt.Errorf("plan backup retention: %w", err)
	}
	return run, items, nil
}

func validRetentionPolicy(policy RetentionPolicy) bool {
	return policy.MinimumComplete >= 1 && policy.MaximumComplete >= policy.MinimumComplete &&
		policy.MaximumTotalBytes > 0 && policy.MinimumAge >= 0 && policy.MinimumAge%time.Second == 0
}

func validateRetentionBackups(backups []Backup) error {
	seen := make(map[BackupID]struct{}, len(backups))
	for _, backup := range backups {
		if _, err := ParseBackupID(string(backup.ID)); err != nil || backup.State != BackupStateComplete ||
			!backup.BodyPresent || backup.ProductionDigest == (Digest{}) || backup.EntryCount < 0 ||
			backup.TotalBytes < 0 || backup.CreatedAt.IsZero() || backup.VerifiedAt.IsZero() {
			return ErrConflict
		}
		if _, duplicate := seen[backup.ID]; duplicate {
			return ErrConflict
		}
		seen[backup.ID] = struct{}{}
	}
	return nil
}

func stringCompare(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
