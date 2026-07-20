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
	"slices"
)

// ListBackups returns a bounded page with dynamically derived protection evidence.
func (s *RecoveryService) ListBackups(ctx context.Context, query BackupQuery) ([]BackupView, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return nil, errors.New("list recovery backups: service is unavailable")
	}
	backups, err := s.repository.ListBackups(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list recovery backups: %w", err)
	}
	protections, err := s.derivedBackupProtections(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]BackupView, 0, len(backups))
	for _, backup := range backups {
		reasons := slices.Clone(protections[backup.ID])
		if backup.ManuallyProtected {
			reasons = append([]BackupProtectionReason{{Kind: "manual", Code: "manual_protection"}}, reasons...)
		}
		views = append(views, BackupView{Backup: backup, Protected: len(reasons) > 0, Protections: reasons})
	}
	return views, nil
}

// Backup returns one content-free backup projection with current protection reasons.
func (s *RecoveryService) Backup(ctx context.Context, id BackupID) (BackupView, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return BackupView{}, errors.New("read recovery backup: service is unavailable")
	}
	backup, err := s.repository.Backup(ctx, id)
	if err != nil {
		return BackupView{}, fmt.Errorf("read recovery backup: %w", err)
	}
	protections, err := s.derivedBackupProtections(ctx)
	if err != nil {
		return BackupView{}, err
	}
	reasons := slices.Clone(protections[id])
	if backup.ManuallyProtected {
		reasons = append([]BackupProtectionReason{{Kind: "manual", Code: "manual_protection"}}, reasons...)
	}
	return BackupView{Backup: backup, Protected: len(reasons) > 0, Protections: reasons}, nil
}

// ChangeBackupProtection applies one audited exact-state manual protection mutation.
func (s *RecoveryService) ChangeBackupProtection(
	ctx context.Context,
	actor Actor,
	id BackupID,
	input ChangeBackupProtectionInput,
) (Backup, error) {
	if ctx == nil || s == nil || s.repository == nil || s.clock == nil {
		return Backup{}, errors.New("change backup protection: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return Backup{}, err
	}
	if _, err := ParseBackupID(string(id)); err != nil {
		return Backup{}, err
	}
	if input.ExpectedProtected == input.Protected {
		return Backup{}, ErrConflict
	}
	if input.Protected {
		if !validRecoveryInputReason(input.Reason) || input.Confirmation != "" {
			return Backup{}, ErrConflict
		}
	} else if input.Reason != "" || input.Confirmation != string(id) {
		return Backup{}, ErrConflict
	}
	now := s.clock.Now().UTC()
	action := "config.backup.unprotect"
	if input.Protected {
		action = "config.backup.protect"
	}
	details, err := json.Marshal(struct {
		Protected bool `json:"protected"`
	}{Protected: input.Protected})
	if err != nil {
		return Backup{}, fmt.Errorf("encode backup protection audit: %w", err)
	}
	operation := OperationRecord{
		ID: string(id) + ":protection:" + actor.RequestID, ObjectType: "config_backup",
		ObjectID: string(id), Action: action, Result: "succeeded", RequestID: actor.RequestID,
		OccurredAt: now,
	}
	backup, err := s.repository.ChangeBackupProtection(ctx, BackupProtectionChange{
		BackupID: id, ExpectedProtected: input.ExpectedProtected, NextProtected: input.Protected,
		Reason: input.Reason, Actor: actor, Operation: operation,
		Audit: AuditEvent{
			OperationID: operation.ID, OccurredAt: now, ActorUserID: actor.UserID,
			Action: action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: actor.RequestID, DetailsJSON: string(details),
		},
	})
	if err != nil {
		return Backup{}, fmt.Errorf("change backup protection: %w", err)
	}
	return backup, nil
}

// RetentionRun returns one persisted plan and its immutable item order.
func (s *RecoveryService) RetentionRun(
	ctx context.Context,
	id RetentionRunID,
) (RetentionRun, []RetentionItem, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return RetentionRun{}, nil, errors.New("read retention run: service is unavailable")
	}
	return s.repository.RetentionRun(ctx, id)
}

// Restore returns one persisted restore projection.
func (s *RecoveryService) Restore(ctx context.Context, id RestoreID) (Restore, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return Restore{}, errors.New("read restore: service is unavailable")
	}
	return s.repository.Restore(ctx, id)
}

// RestoreStages returns bounded durable restore stages after one sequence.
func (s *RecoveryService) RestoreStages(ctx context.Context, id RestoreID, after uint64) ([]RestoreStage, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return nil, errors.New("read restore stages: service is unavailable")
	}
	return s.repository.RestoreStages(ctx, id, after, recoveryStageLimit)
}

// Restart returns one persisted restart projection.
func (s *RecoveryService) Restart(ctx context.Context, id RestartID) (Restart, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return Restart{}, errors.New("read restart: service is unavailable")
	}
	return s.repository.Restart(ctx, id)
}

// RestartStages returns bounded durable restart stages after one sequence.
func (s *RecoveryService) RestartStages(ctx context.Context, id RestartID, after uint64) ([]RestartStage, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return nil, errors.New("read restart stages: service is unavailable")
	}
	return s.repository.RestartStages(ctx, id, after, recoveryStageLimit)
}

// ListReleases returns one stable release-history page.
func (s *RecoveryService) ListReleases(ctx context.Context, query HistoryQuery) ([]Release, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return nil, errors.New("list release history: service is unavailable")
	}
	return s.repository.ListReleases(ctx, query)
}

// ListRestores returns one stable restore-history page.
func (s *RecoveryService) ListRestores(ctx context.Context, query HistoryQuery) ([]Restore, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return nil, errors.New("list restore history: service is unavailable")
	}
	return s.repository.ListRestores(ctx, query)
}

// ListRestarts returns one stable restart-history page.
func (s *RecoveryService) ListRestarts(ctx context.Context, query HistoryQuery) ([]Restart, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return nil, errors.New("list restart history: service is unavailable")
	}
	return s.repository.ListRestarts(ctx, query)
}

// ListAuditEvents returns one stable, bounded audit page.
func (s *RecoveryService) ListAuditEvents(ctx context.Context, query AuditQuery) ([]AuditRecord, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return nil, errors.New("list recovery audit events: service is unavailable")
	}
	return s.repository.ListAuditEvents(ctx, query)
}

// ListAttentionCases returns bounded evidence-bearing attention cases.
func (s *RecoveryService) ListAttentionCases(
	ctx context.Context,
	query AttentionQuery,
) ([]AttentionCase, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return nil, errors.New("list attention cases: service is unavailable")
	}
	return s.repository.ListAttentionCases(ctx, query)
}

// AttentionCase returns one exact attention case.
func (s *RecoveryService) AttentionCase(ctx context.Context, id AttentionCaseID) (AttentionCase, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return AttentionCase{}, errors.New("read attention case: service is unavailable")
	}
	return s.repository.AttentionCase(ctx, id)
}

func (s *RecoveryService) derivedBackupProtections(
	ctx context.Context,
) (map[BackupID][]BackupProtectionReason, error) {
	result := make(map[BackupID][]BackupProtectionReason)
	complete, err := s.repository.RetentionBackups(ctx)
	if err != nil {
		return nil, fmt.Errorf("derive backup protections: %w", err)
	}
	slices.SortFunc(complete, func(left, right Backup) int {
		if comparison := left.CreatedAt.Compare(right.CreatedAt); comparison != 0 {
			return comparison
		}
		return stringCompare(string(left.ID), string(right.ID))
	})
	minimum := min(s.policy.MinimumComplete, len(complete))
	for index := len(complete) - minimum; index < len(complete); index++ {
		if index >= 0 {
			result[complete[index].ID] = append(result[complete[index].ID],
				BackupProtectionReason{Kind: "system", Code: "minimum_complete"})
		}
	}
	attention, err := s.repository.ListAttentionCases(ctx, AttentionQuery{
		State: AttentionCaseOpen, Limit: attentionReadLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("derive backup protections: %w", err)
	}
	if len(attention) == attentionReadLimit {
		return nil, fmt.Errorf("derive backup protections: %w", ErrLimitExceeded)
	}
	for _, item := range attention {
		if item.BackupID != "" {
			result[item.BackupID] = append(result[item.BackupID],
				BackupProtectionReason{Kind: "attention", Code: "attention_case"})
		}
	}
	activeRestore, err := s.repository.ActiveRestore(ctx)
	if err == nil {
		for _, id := range []BackupID{activeRestore.TargetBackupID, activeRestore.SafetyBackupID} {
			if id != "" {
				result[id] = append(result[id], BackupProtectionReason{Kind: "active", Code: "active_restore"})
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("derive backup protections: %w", err)
	}
	return result, nil
}
