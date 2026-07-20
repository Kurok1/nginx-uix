/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"testing"
	"time"
)

func TestRecoveryServicePlansDeterministicProtectedRetention(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	repository := &memoryRecoveryRepository{
		backups: []Backup{
			recoveryServiceBackup(1, now.Add(-6*24*time.Hour), 100, true),
			recoveryServiceBackup(2, now.Add(-5*24*time.Hour), 100, false),
			recoveryServiceBackup(3, now.Add(-4*24*time.Hour), 100, false),
			recoveryServiceBackup(4, now.Add(-3*24*time.Hour), 100, false),
			recoveryServiceBackup(5, now.Add(-2*24*time.Hour), 100, false),
		},
		attention: []AttentionCase{{
			ID: AttentionCaseID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), SubjectType: AttentionSubjectRelease,
			SubjectID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", BackupID: BackupID("00000000000000000000000000000002"),
			State: AttentionCaseOpen, ReasonCode: "runtime_unknown", PublicEvidenceJSON: `{}`, OpenedAt: now.Add(-time.Hour),
		}},
	}
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: &memoryRecoveryAgent{}, Clock: &fixedClock{now: now}, Random: bytes.NewReader(bytes.Repeat([]byte{0xaa}, 16)),
		Policy: RetentionPolicy{MinimumComplete: 2, MaximumComplete: 3, MaximumTotalBytes: 1 << 20, MinimumAge: 24 * time.Hour},
	})
	if err != nil {
		t.Fatalf("NewRecoveryService() error = %v", err)
	}

	run, items, err := service.PlanRetention(context.Background(), Actor{UserID: 7, RequestID: "retention-plan"})
	if err != nil {
		t.Fatalf("PlanRetention() error = %v", err)
	}
	if run.ID != RetentionRunID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") || run.State != RetentionRunPlanned ||
		run.BackupCount != 5 || run.ProtectedCount != 4 || run.DeleteCount != 1 || run.DeleteBytes != 100 {
		t.Fatalf("retention run = %#v", run)
	}
	if len(items) != 5 {
		t.Fatalf("retention item count = %d, want 5", len(items))
	}
	wantDecisions := []RetentionDecision{
		RetentionDecisionKeep,
		RetentionDecisionKeep,
		RetentionDecisionDelete,
		RetentionDecisionKeep,
		RetentionDecisionKeep,
	}
	wantReasons := []string{
		"manual_protection",
		"attention_case",
		"maximum_complete",
		"minimum_complete",
		"minimum_complete",
	}
	for index := range items {
		if items[index].Ordinal != index || items[index].Decision != wantDecisions[index] ||
			items[index].ReasonCode != wantReasons[index] || items[index].State != RetentionItemPlanned {
			t.Fatalf("retention item %d = %#v", index, items[index])
		}
	}
	if !reflect.DeepEqual(repository.createdRun, run) || !reflect.DeepEqual(repository.createdItems, items) {
		t.Fatalf("persisted plan = %#v/%#v", repository.createdRun, repository.createdItems)
	}
}

func TestRecoveryServiceListsAndChangesBackupProtectionWithAuditEvidence(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 30, 0, 0, time.UTC)
	backups := []Backup{
		recoveryServiceBackup(4, now.Add(-time.Hour), 100, false),
		recoveryServiceBackup(3, now.Add(-2*time.Hour), 100, false),
		recoveryServiceBackup(2, now.Add(-3*time.Hour), 100, false),
		recoveryServiceBackup(1, now.Add(-4*time.Hour), 100, false),
	}
	repository := &memoryRecoveryRepository{backups: backups}
	service, err := NewRecoveryService(validRecoveryDependencies(repository))
	if err != nil {
		t.Fatal(err)
	}
	views, err := service.ListBackups(context.Background(), BackupQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 4 || !views[0].Protected || !views[1].Protected || !views[2].Protected ||
		views[3].Protected || views[0].Protections[0].Code != "minimum_complete" {
		t.Fatalf("backup views = %#v", views)
	}
	actor := Actor{UserID: 7, RequestID: "protect-backup"}
	protected, err := service.ChangeBackupProtection(context.Background(), actor, backups[3].ID, ChangeBackupProtectionInput{
		ExpectedProtected: false, Protected: true, Reason: "known stable recovery point",
	})
	if err != nil || !protected.ManuallyProtected || protected.ProtectionReason != "known stable recovery point" {
		t.Fatalf("ChangeBackupProtection(protect) = %#v, %v", protected, err)
	}
	if repository.protectionChange.Operation.Action != "config.backup.protect" ||
		repository.protectionChange.Audit.ActorUserID != actor.UserID {
		t.Fatalf("protection evidence = %#v", repository.protectionChange)
	}
	if _, err := service.ChangeBackupProtection(context.Background(), actor, backups[3].ID, ChangeBackupProtectionInput{
		ExpectedProtected: true, Protected: false, Confirmation: "short",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unsafe unprotect error = %v", err)
	}
	unprotected, err := service.ChangeBackupProtection(context.Background(), actor, backups[3].ID, ChangeBackupProtectionInput{
		ExpectedProtected: true, Protected: false, Confirmation: string(backups[3].ID),
	})
	if err != nil || unprotected.ManuallyProtected {
		t.Fatalf("ChangeBackupProtection(unprotect) = %#v, %v", unprotected, err)
	}
}

func TestRecoveryServiceRetentionUsesCountThenBytesAndMinimumAge(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	repository := &memoryRecoveryRepository{backups: []Backup{
		recoveryServiceBackup(1, now.Add(-72*time.Hour), 400, false),
		recoveryServiceBackup(2, now.Add(-48*time.Hour), 400, false),
		recoveryServiceBackup(3, now.Add(-12*time.Hour), 400, false),
		recoveryServiceBackup(4, now.Add(-6*time.Hour), 400, false),
	}}
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: &memoryRecoveryAgent{}, Clock: &fixedClock{now: now}, Random: bytes.NewReader(bytes.Repeat([]byte{0xbb}, 16)),
		Policy: RetentionPolicy{MinimumComplete: 1, MaximumComplete: 3, MaximumTotalBytes: 700, MinimumAge: 24 * time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, items, err := service.PlanRetention(context.Background(), Actor{UserID: 7, RequestID: "retention-space"})
	if err != nil {
		t.Fatal(err)
	}
	if run.DeleteCount != 2 || run.DeleteBytes != 800 || run.LastErrorCode != "retention_target_unreachable" {
		t.Fatalf("retention run = %#v", run)
	}
	if items[0].Decision != RetentionDecisionDelete || items[0].ReasonCode != "maximum_complete" ||
		items[1].Decision != RetentionDecisionDelete || items[1].ReasonCode != "maximum_total_bytes" ||
		items[2].Decision != RetentionDecisionKeep || items[2].ReasonCode != "minimum_age" ||
		items[3].Decision != RetentionDecisionKeep || items[3].ReasonCode != "minimum_complete" {
		t.Fatalf("retention items = %#v", items)
	}
}

func TestRecoveryServiceRejectsUnsafePolicyAndRepositoryOverflow(t *testing.T) {
	valid := RecoveryDependencies{
		Repository: &memoryRecoveryRepository{}, Agent: &memoryRecoveryAgent{}, Clock: &fixedClock{now: time.Now().UTC()},
		Random: bytes.NewReader(bytes.Repeat([]byte{1}, 16)),
		Policy: RetentionPolicy{MinimumComplete: 3, MaximumComplete: 20, MaximumTotalBytes: 4 << 30, MinimumAge: 24 * time.Hour},
	}
	for name, mutate := range map[string]func(*RecoveryDependencies){
		"missing repository": func(input *RecoveryDependencies) { input.Repository = nil },
		"minimum zero":       func(input *RecoveryDependencies) { input.Policy.MinimumComplete = 0 },
		"maximum below minimum": func(input *RecoveryDependencies) {
			input.Policy.MaximumComplete = 2
		},
		"fractional age": func(input *RecoveryDependencies) { input.Policy.MinimumAge += time.Nanosecond },
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			if _, err := NewRecoveryService(input); err == nil {
				t.Fatal("NewRecoveryService() error = nil")
			}
		})
	}

	repository := &memoryRecoveryRepository{retentionErr: ErrLimitExceeded}
	service, err := NewRecoveryService(validRecoveryDependencies(repository))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.PlanRetention(context.Background(), Actor{UserID: 1, RequestID: "overflow"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("PlanRetention() error = %v, want ErrLimitExceeded", err)
	}
}

func TestRecoveryServiceQueuesAndExecutesRetentionWithExactEvidence(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	backup := recoveryServiceBackup(1, now.Add(-72*time.Hour), 400, false)
	run := recoveryServiceRetentionRun(now, backup)
	items := []RetentionItem{
		{
			RunID: run.ID, Ordinal: 0, BackupID: backup.ID, Decision: RetentionDecisionDelete,
			ReasonCode: "maximum_complete", State: RetentionItemPlanned,
			SnapshotCreatedAt: backup.CreatedAt, SnapshotTotalBytes: backup.TotalBytes, UpdatedAt: run.CreatedAt,
		},
		{
			RunID: run.ID, Ordinal: 1, BackupID: BackupID("00000000000000000000000000000002"),
			Decision: RetentionDecisionKeep, ReasonCode: "minimum_complete", State: RetentionItemPlanned,
			SnapshotCreatedAt: now.Add(-48 * time.Hour), SnapshotTotalBytes: 200, UpdatedAt: run.CreatedAt,
		},
	}
	repository := &memoryRecoveryRepository{backups: []Backup{backup}, storedRun: run, storedItems: items}
	agent := &memoryRecoveryAgent{verification: BackupEvidence{
		BackupID: backup.ID, ProductionDigest: backup.ProductionDigest, TreeDigest: backup.TreeDigest,
		EntryCount: backup.EntryCount, TotalBytes: backup.TotalBytes, VerifiedAt: backup.VerifiedAt,
	}}
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: agent, Clock: &fixedClock{now: now},
		Random: bytes.NewReader(bytes.Repeat([]byte{1}, 16)), Policy: run.Policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 1, RequestID: "retention-execution"}
	queued, err := service.QueueRetentionExecution(context.Background(), actor, run.ID, string(run.ID))
	if err != nil {
		t.Fatalf("QueueRetentionExecution() error = %v", err)
	}
	if queued.State != RetentionRunExecuting || queued.ExecutionRequestID != actor.RequestID || !queued.StartedAt.Equal(now) {
		t.Fatalf("queued retention = %#v", queued)
	}
	if err := service.RunRetention(context.Background(), run.ID); err != nil {
		t.Fatalf("RunRetention() error = %v", err)
	}
	if repository.storedRun.State != RetentionRunSucceeded || repository.storedRun.DeletedCount != 1 ||
		repository.storedRun.DeletedBytes != backup.TotalBytes || repository.storedItems[0].State != RetentionItemDeleted ||
		repository.storedItems[1].State != RetentionItemKept {
		t.Fatalf("executed retention = %#v/%#v", repository.storedRun, repository.storedItems)
	}
	if len(agent.deleted) != 1 || agent.deleted[0].RunID != run.ID || agent.deleted[0].BackupID != backup.ID ||
		agent.deleted[0].ProductionDigest != backup.ProductionDigest || agent.deleted[0].TreeDigest != backup.TreeDigest {
		t.Fatalf("agent deletions = %#v", agent.deleted)
	}
}

func TestRecoveryServiceRetentionExpiresOrSkipsNewProtection(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	backup := recoveryServiceBackup(1, now.Add(-72*time.Hour), 400, false)
	run := recoveryServiceRetentionRun(now, backup)
	run.ExpiresAt = now
	repository := &memoryRecoveryRepository{storedRun: run, storedItems: []RetentionItem{}}
	service, err := NewRecoveryService(validRecoveryDependencies(repository))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.QueueRetentionExecution(
		context.Background(), Actor{UserID: 1, RequestID: "expired"}, run.ID, string(run.ID),
	); !errors.Is(err, ErrRetentionPlanExpired) {
		t.Fatalf("expired QueueRetentionExecution() error = %v", err)
	}
	if repository.storedRun.State != RetentionRunExpired {
		t.Fatalf("expired retention state = %s", repository.storedRun.State)
	}

	run = recoveryServiceRetentionRun(now, backup)
	run.BackupCount = 1
	run.TotalBytes = backup.TotalBytes
	item := RetentionItem{
		RunID: run.ID, Ordinal: 0, BackupID: backup.ID, Decision: RetentionDecisionDelete,
		ReasonCode: "maximum_complete", State: RetentionItemPlanned,
		SnapshotCreatedAt: backup.CreatedAt, SnapshotTotalBytes: backup.TotalBytes, UpdatedAt: run.CreatedAt,
	}
	repository = &memoryRecoveryRepository{
		backups: []Backup{backup}, storedRun: run, storedItems: []RetentionItem{item},
		beginRetentionErr: ErrBackupProtected,
	}
	agent := &memoryRecoveryAgent{verification: BackupEvidence{
		BackupID: backup.ID, ProductionDigest: backup.ProductionDigest, TreeDigest: backup.TreeDigest,
		EntryCount: backup.EntryCount, TotalBytes: backup.TotalBytes, VerifiedAt: backup.VerifiedAt,
	}}
	service, err = NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: agent, Clock: &fixedClock{now: now},
		Random: bytes.NewReader(bytes.Repeat([]byte{1}, 16)), Policy: run.Policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.QueueRetentionExecution(
		context.Background(), Actor{UserID: 1, RequestID: "skip-protected"}, run.ID, string(run.ID),
	); err != nil {
		t.Fatal(err)
	}
	if err := service.RunRetention(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	if repository.storedRun.State != RetentionRunSucceeded ||
		repository.storedItems[0].State != RetentionItemSkippedProtected || len(agent.deleted) != 0 {
		t.Fatalf("protected retention = %#v/%#v/%#v", repository.storedRun, repository.storedItems, agent.deleted)
	}
}

func TestRecoveryServiceRetentionDeletionUncertaintyStopsRun(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	backup := recoveryServiceBackup(1, now.Add(-72*time.Hour), 400, false)
	run := recoveryServiceRetentionRun(now, backup)
	run.BackupCount = 1
	run.TotalBytes = backup.TotalBytes
	item := RetentionItem{
		RunID: run.ID, Ordinal: 0, BackupID: backup.ID, Decision: RetentionDecisionDelete,
		ReasonCode: "maximum_complete", State: RetentionItemPlanned,
		SnapshotCreatedAt: backup.CreatedAt, SnapshotTotalBytes: backup.TotalBytes, UpdatedAt: run.CreatedAt,
	}
	repository := &memoryRecoveryRepository{backups: []Backup{backup}, storedRun: run, storedItems: []RetentionItem{item}}
	agent := &memoryRecoveryAgent{
		verification: BackupEvidence{
			BackupID: backup.ID, ProductionDigest: backup.ProductionDigest, TreeDigest: backup.TreeDigest,
			EntryCount: backup.EntryCount, TotalBytes: backup.TotalBytes, VerifiedAt: backup.VerifiedAt,
		},
		deleteErr: errors.New("delete interrupted"), verifyAfterDeleteErr: configTestCorruptionError{},
	}
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: agent, Clock: &fixedClock{now: now},
		Random: bytes.NewReader(bytes.Repeat([]byte{1}, 16)), Policy: run.Policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.QueueRetentionExecution(
		context.Background(), Actor{UserID: 1, RequestID: "uncertain"}, run.ID, string(run.ID),
	); err != nil {
		t.Fatal(err)
	}
	if err := service.RunRetention(context.Background(), run.ID); err == nil {
		t.Fatal("RunRetention() error = nil")
	}
	if repository.storedRun.State != RetentionRunNeedsAttention ||
		repository.storedItems[0].State != RetentionItemNeedsAttention {
		t.Fatalf("uncertain retention = %#v/%#v", repository.storedRun, repository.storedItems)
	}
}

func recoveryServiceRetentionRun(now time.Time, backup Backup) RetentionRun {
	return RetentionRun{
		ID: RetentionRunID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), State: RetentionRunPlanned,
		Policy:      RetentionPolicy{MinimumComplete: 1, MaximumComplete: 3, MaximumTotalBytes: 700, MinimumAge: 24 * time.Hour},
		BackupCount: 2, TotalBytes: backup.TotalBytes + 200, DeleteCount: 1, DeleteBytes: backup.TotalBytes,
		CreatedBy: 1, RequestID: "retention-plan", CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(10 * time.Minute),
	}
}

type configTestCorruptionError struct{}

func (configTestCorruptionError) Error() string { return "backup integrity uncertain" }

func recoveryServiceBackup(number int, createdAt time.Time, bytes int64, manuallyProtected bool) Backup {
	id := BackupID(fmt.Sprintf("%032x", number))
	backup := Backup{
		ID: id, OriginType: BackupOriginRestore, OriginID: string(id),
		ProductionDigest: recoveryDigest(byte(number)), TreeDigest: recoveryDigest(byte(number + 20)),
		State: BackupStateComplete, EntryCount: number, TotalBytes: bytes, BodyPresent: true,
		CreatedAt: createdAt, VerifiedAt: createdAt.Add(time.Minute), ManuallyProtected: manuallyProtected,
	}
	if manuallyProtected {
		backup.ProtectionReason = "operator hold"
		backup.ProtectedBy = 7
		backup.ProtectedAt = createdAt.Add(2 * time.Minute)
	}
	return backup
}

func recoveryDigest(value byte) Digest {
	var digest Digest
	for index := range digest {
		digest[index] = value
	}
	return digest
}

func validRecoveryDependencies(repository RecoveryRepository) RecoveryDependencies {
	return RecoveryDependencies{
		Repository: repository, Agent: &memoryRecoveryAgent{}, Clock: &fixedClock{now: time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)},
		Random: bytes.NewReader(bytes.Repeat([]byte{1}, 16)),
		Policy: RetentionPolicy{MinimumComplete: 3, MaximumComplete: 20, MaximumTotalBytes: 4 << 30, MinimumAge: 24 * time.Hour},
	}
}

type memoryRecoveryAgent struct {
	verification         BackupEvidence
	verifyErr            error
	verifyAfterDeleteErr error
	deleteErr            error
	deleted              []BackupDeletionRequest
	verifyCalls          int
	production           ProductionState
	configDigestErr      error
	configDigestCalls    int
	preparation          RestorePreparationResult
	prepareErr           error
	prepareRequest       RestoreExecutionRequest
	restoreResult        RestoreExecutionResult
	restoreErr           error
	restoreRequest       RestoreExecutionRequest
	restartResult        RestartExecutionResult
	restartErr           error
	restartRequest       RestartExecutionRequest
	runtimeVerification  RuntimeVerificationResult
	runtimeVerifyErr     error
	runtimeVerifyRequest RuntimeVerificationRequest
}

func (a *memoryRecoveryAgent) ConfigDigest(context.Context, string) (ProductionState, error) {
	a.configDigestCalls++
	return a.production, a.configDigestErr
}

func (a *memoryRecoveryAgent) VerifyBackup(context.Context, string, BackupID) (BackupEvidence, error) {
	a.verifyCalls++
	if a.verifyCalls > 1 && a.verifyAfterDeleteErr != nil {
		return BackupEvidence{}, a.verifyAfterDeleteErr
	}
	if a.verifyErr != nil {
		return BackupEvidence{}, a.verifyErr
	}
	if a.verification.BackupID == "" {
		return BackupEvidence{}, fs.ErrNotExist
	}
	return a.verification, nil
}

func (a *memoryRecoveryAgent) DeleteBackup(_ context.Context, _ string, request BackupDeletionRequest) error {
	a.deleted = append(a.deleted, request)
	return a.deleteErr
}

func (a *memoryRecoveryAgent) PrepareRestore(_ context.Context, _ string, request RestoreExecutionRequest) (RestorePreparationResult, error) {
	a.prepareRequest = request
	if a.preparation.RestoreID == "" && a.prepareErr == nil {
		return RestorePreparationResult{}, fs.ErrNotExist
	}
	return a.preparation, a.prepareErr
}

func (a *memoryRecoveryAgent) ExecuteRestore(_ context.Context, _ string, request RestoreExecutionRequest) (RestoreExecutionResult, error) {
	a.restoreRequest = request
	if a.restoreResult.RestoreID == "" && a.restoreErr == nil {
		return RestoreExecutionResult{}, fs.ErrNotExist
	}
	return a.restoreResult, a.restoreErr
}

func (a *memoryRecoveryAgent) RestoreProgress(context.Context, string, RestoreExecutionRequest) (RestoreExecutionResult, error) {
	return a.restoreResult, a.restoreErr
}

func (a *memoryRecoveryAgent) RecoverRestore(context.Context, string, RestoreExecutionRequest) (RestoreExecutionResult, error) {
	return a.restoreResult, a.restoreErr
}

func (a *memoryRecoveryAgent) ExecuteRestart(_ context.Context, _ string, request RestartExecutionRequest) (RestartExecutionResult, error) {
	a.restartRequest = request
	if a.restartResult.RestartID == "" && a.restartErr == nil {
		return RestartExecutionResult{}, fs.ErrNotExist
	}
	return a.restartResult, a.restartErr
}

func (*memoryRecoveryAgent) RestartProgress(context.Context, string, RestartExecutionRequest) (RestartExecutionResult, error) {
	return RestartExecutionResult{}, fs.ErrNotExist
}

func (a *memoryRecoveryAgent) RecoverRestart(context.Context, string, RestartExecutionRequest) (RestartExecutionResult, error) {
	return a.restartResult, a.restartErr
}

func (a *memoryRecoveryAgent) VerifyRuntime(_ context.Context, _ string, request RuntimeVerificationRequest) (RuntimeVerificationResult, error) {
	a.runtimeVerifyRequest = request
	return a.runtimeVerification, a.runtimeVerifyErr
}

type memoryRecoveryRepository struct {
	lease              ProductionLease
	backups            []Backup
	attention          []AttentionCase
	retentionErr       error
	createdRun         RetentionRun
	createdItems       []RetentionItem
	storedRun          RetentionRun
	storedItems        []RetentionItem
	beginRetentionErr  error
	putBackups         []Backup
	storedRestore      Restore
	restoreStages      []RestoreStage
	restoreTransition  chan RestoreStageName
	restoreNotifyStage RestoreStageName
	storedRestart      Restart
	restartStages      []RestartStage
	restartTransition  chan RestartStageName
	restartNotifyStage RestartStageName
	protectionChange   BackupProtectionChange
	storedVerification Verification
}

func (r *memoryRecoveryRepository) ProductionLease(context.Context) (ProductionLease, error) {
	return r.lease, nil
}

func (r *memoryRecoveryRepository) AcquireProductionLease(context.Context, ProductionOperationKind, string, time.Time) error {
	return nil
}

func (r *memoryRecoveryRepository) ReleaseProductionLease(context.Context, ProductionOperationKind, string) error {
	return nil
}

func (r *memoryRecoveryRepository) ListBackups(_ context.Context, query BackupQuery) ([]Backup, error) {
	if query.Limit <= 0 {
		return nil, errors.New("invalid query")
	}
	return append([]Backup(nil), r.backups...), nil
}

func (r *memoryRecoveryRepository) RetentionBackups(context.Context) ([]Backup, error) {
	if r.retentionErr != nil {
		return nil, r.retentionErr
	}
	return append([]Backup(nil), r.backups...), nil
}

func (r *memoryRecoveryRepository) Backup(_ context.Context, id BackupID) (Backup, error) {
	for _, backup := range r.backups {
		if backup.ID == id {
			return backup, nil
		}
	}
	return Backup{}, fs.ErrNotExist
}

func (r *memoryRecoveryRepository) PutBackup(_ context.Context, backup Backup) error {
	r.putBackups = append(r.putBackups, backup)
	for index := range r.backups {
		if r.backups[index].ID == backup.ID {
			r.backups[index] = backup
			return nil
		}
	}
	r.backups = append(r.backups, backup)
	return nil
}

func (r *memoryRecoveryRepository) ChangeBackupProtection(_ context.Context, change BackupProtectionChange) (Backup, error) {
	for index := range r.backups {
		if r.backups[index].ID != change.BackupID {
			continue
		}
		if r.backups[index].ManuallyProtected != change.ExpectedProtected {
			return Backup{}, ErrConflict
		}
		r.protectionChange = change
		r.backups[index].ManuallyProtected = change.NextProtected
		if change.NextProtected {
			r.backups[index].ProtectionReason = change.Reason
			r.backups[index].ProtectedBy = change.Actor.UserID
			r.backups[index].ProtectedAt = change.Operation.OccurredAt
		} else {
			r.backups[index].ProtectionReason = ""
			r.backups[index].ProtectedBy = 0
			r.backups[index].ProtectedAt = time.Time{}
		}
		return r.backups[index], nil
	}
	return Backup{}, fs.ErrNotExist
}

func (r *memoryRecoveryRepository) CreateRetentionRun(_ context.Context, run RetentionRun, items []RetentionItem) error {
	r.createdRun = run
	r.createdItems = append([]RetentionItem(nil), items...)
	r.storedRun = run
	r.storedItems = append([]RetentionItem(nil), items...)
	return nil
}

func (r *memoryRecoveryRepository) RetentionRun(context.Context, RetentionRunID) (RetentionRun, []RetentionItem, error) {
	if r.storedRun.ID == "" {
		return RetentionRun{}, nil, fs.ErrNotExist
	}
	return r.storedRun, append([]RetentionItem(nil), r.storedItems...), nil
}

func (r *memoryRecoveryRepository) TransitionRetentionItem(_ context.Context, _ RetentionRunID, ordinal int, expected, next RetentionItemState, updatedAt time.Time) error {
	if ordinal < 0 || ordinal >= len(r.storedItems) || r.storedItems[ordinal].State != expected {
		return ErrConflict
	}
	r.storedItems[ordinal].State = next
	r.storedItems[ordinal].UpdatedAt = updatedAt
	return nil
}

func (r *memoryRecoveryRepository) BeginRetentionDeletion(ctx context.Context, _ RetentionRunID, ordinal int, _ BackupID, _ time.Time, _ int64, updatedAt time.Time) error {
	if r.beginRetentionErr != nil {
		return r.beginRetentionErr
	}
	return r.TransitionRetentionItem(ctx, r.storedRun.ID, ordinal, RetentionItemPlanned, RetentionItemDeleting, updatedAt)
}

func (r *memoryRecoveryRepository) CompleteRetentionDeletion(ctx context.Context, _ RetentionRunID, ordinal int, _ BackupID, updatedAt time.Time) error {
	if err := r.TransitionRetentionItem(ctx, r.storedRun.ID, ordinal, RetentionItemDeleting, RetentionItemDeleted, updatedAt); err != nil {
		return err
	}
	r.storedRun.DeletedCount++
	r.storedRun.DeletedBytes += r.storedItems[ordinal].SnapshotTotalBytes
	return nil
}

func (r *memoryRecoveryRepository) AbortRetentionDeletion(ctx context.Context, _ RetentionRunID, ordinal int, _ BackupID, next RetentionItemState, updatedAt time.Time) error {
	return r.TransitionRetentionItem(ctx, r.storedRun.ID, ordinal, RetentionItemDeleting, next, updatedAt)
}

func (r *memoryRecoveryRepository) MarkRetentionDeletionUncertain(ctx context.Context, _ RetentionRunID, ordinal int, _ BackupID, updatedAt time.Time) error {
	return r.TransitionRetentionItem(ctx, r.storedRun.ID, ordinal, RetentionItemDeleting, RetentionItemNeedsAttention, updatedAt)
}

func (r *memoryRecoveryRepository) TransitionRetentionRun(_ context.Context, expected RetentionRunState, next RetentionRun) error {
	if r.storedRun.State != expected {
		return ErrConflict
	}
	r.storedRun = next
	if expected == RetentionRunPlanned && next.State == RetentionRunExecuting {
		if r.lease.OwnerID != "" {
			return ErrOperationInProgress
		}
		r.lease = ProductionLease{OwnerType: ProductionOperationRetention, OwnerID: string(next.ID), AcquiredAt: next.StartedAt}
	}
	if expected == RetentionRunExecuting && terminalRetentionRunStateForTest(next.State) {
		r.lease = ProductionLease{}
	}
	return nil
}

func terminalRetentionRunStateForTest(state RetentionRunState) bool {
	return state == RetentionRunSucceeded || state == RetentionRunFailed ||
		state == RetentionRunNeedsAttention || state == RetentionRunExpired
}

func (r *memoryRecoveryRepository) CreateRestore(_ context.Context, restore Restore, stage RestoreStage) error {
	if r.storedRestore.ID != "" || r.lease.OwnerID != "" {
		return ErrOperationInProgress
	}
	r.storedRestore = restore
	r.restoreStages = []RestoreStage{stage}
	r.lease = ProductionLease{OwnerType: ProductionOperationRestore, OwnerID: string(restore.ID), AcquiredAt: restore.CreatedAt}
	return nil
}

func (r *memoryRecoveryRepository) TransitionRestore(_ context.Context, expectedState RestoreState, expectedStage RestoreStageName, restore Restore, stage RestoreStage) error {
	if r.storedRestore.ID == "" {
		return fs.ErrNotExist
	}
	if r.storedRestore.State != expectedState || r.storedRestore.Stage != expectedStage ||
		stage.Sequence != uint64(len(r.restoreStages)+1) {
		return ErrConflict
	}
	r.storedRestore = restore
	r.restoreStages = append(r.restoreStages, stage)
	if r.restoreTransition != nil && restore.Stage == r.restoreNotifyStage {
		r.restoreTransition <- restore.Stage
	}
	if terminalRecoveryRestoreState(restore.State) {
		r.lease = ProductionLease{}
	}
	return nil
}

func (r *memoryRecoveryRepository) Restore(_ context.Context, id RestoreID) (Restore, error) {
	if r.storedRestore.ID != id {
		return Restore{}, fs.ErrNotExist
	}
	return r.storedRestore, nil
}

func (r *memoryRecoveryRepository) ActiveRestore(context.Context) (Restore, error) {
	if r.storedRestore.ID == "" || terminalRecoveryRestoreState(r.storedRestore.State) {
		return Restore{}, fs.ErrNotExist
	}
	return r.storedRestore, nil
}

func (r *memoryRecoveryRepository) RestoreStages(_ context.Context, id RestoreID, after uint64, limit int) ([]RestoreStage, error) {
	if r.storedRestore.ID != id {
		return nil, fs.ErrNotExist
	}
	result := make([]RestoreStage, 0, len(r.restoreStages))
	for _, stage := range r.restoreStages {
		if stage.Sequence > after && len(result) < limit {
			result = append(result, stage)
		}
	}
	return result, nil
}

func (r *memoryRecoveryRepository) ListRestores(context.Context, HistoryQuery) ([]Restore, error) {
	return nil, nil
}

func (r *memoryRecoveryRepository) CreateRestart(_ context.Context, restart Restart, stage RestartStage) error {
	if r.storedRestart.ID != "" || r.lease.OwnerID != "" {
		return ErrOperationInProgress
	}
	r.storedRestart = restart
	r.restartStages = []RestartStage{stage}
	r.lease = ProductionLease{OwnerType: ProductionOperationRestart, OwnerID: string(restart.ID), AcquiredAt: restart.CreatedAt}
	return nil
}

func (r *memoryRecoveryRepository) TransitionRestart(_ context.Context, expectedState RestartState, expectedStage RestartStageName, restart Restart, stage RestartStage) error {
	if r.storedRestart.ID == "" {
		return fs.ErrNotExist
	}
	if r.storedRestart.State != expectedState || r.storedRestart.Stage != expectedStage ||
		stage.Sequence != uint64(len(r.restartStages)+1) {
		return ErrConflict
	}
	r.storedRestart = restart
	r.restartStages = append(r.restartStages, stage)
	if r.restartTransition != nil && restart.Stage == r.restartNotifyStage {
		r.restartTransition <- restart.Stage
	}
	if terminalRecoveryRestartState(restart.State) {
		r.lease = ProductionLease{}
	}
	return nil
}

func (r *memoryRecoveryRepository) Restart(_ context.Context, id RestartID) (Restart, error) {
	if r.storedRestart.ID != id {
		return Restart{}, fs.ErrNotExist
	}
	return r.storedRestart, nil
}

func (r *memoryRecoveryRepository) ActiveRestart(context.Context) (Restart, error) {
	if r.storedRestart.ID == "" || terminalRecoveryRestartState(r.storedRestart.State) {
		return Restart{}, fs.ErrNotExist
	}
	return r.storedRestart, nil
}

func (r *memoryRecoveryRepository) RestartStages(_ context.Context, id RestartID, after uint64, limit int) ([]RestartStage, error) {
	if r.storedRestart.ID != id {
		return nil, fs.ErrNotExist
	}
	result := make([]RestartStage, 0, len(r.restartStages))
	for _, stage := range r.restartStages {
		if stage.Sequence > after && len(result) < limit {
			result = append(result, stage)
		}
	}
	return result, nil
}

func (r *memoryRecoveryRepository) ListRestarts(context.Context, HistoryQuery) ([]Restart, error) {
	return nil, nil
}

func (r *memoryRecoveryRepository) ListReleases(context.Context, HistoryQuery) ([]Release, error) {
	return nil, nil
}

func (r *memoryRecoveryRepository) ResolveAttentionCase(_ context.Context, id AttentionCaseID, resolutionType AttentionResolutionType, resolutionID string, actor Actor, resolvedAt time.Time) error {
	for index := range r.attention {
		if r.attention[index].ID != id {
			continue
		}
		if r.attention[index].State != AttentionCaseOpen {
			return ErrConflict
		}
		r.attention[index].State = AttentionCaseResolved
		r.attention[index].ResolvedBy = actor.UserID
		r.attention[index].ResolvedAt = resolvedAt
		r.attention[index].ResolutionType = resolutionType
		r.attention[index].ResolutionID = resolutionID
		return nil
	}
	return fs.ErrNotExist
}

func (r *memoryRecoveryRepository) ListAuditEvents(context.Context, AuditQuery) ([]AuditRecord, error) {
	return nil, nil
}

func (r *memoryRecoveryRepository) ListAttentionCases(_ context.Context, query AttentionQuery) ([]AttentionCase, error) {
	result := make([]AttentionCase, 0, len(r.attention))
	for _, attention := range r.attention {
		if (query.State == "" || attention.State == query.State) &&
			(query.BeforeID == "" || attention.OpenedAt.Before(query.BeforeOpenedAt) ||
				(attention.OpenedAt.Equal(query.BeforeOpenedAt) && attention.ID < query.BeforeID)) {
			result = append(result, attention)
		}
	}
	if len(result) > query.Limit {
		result = result[:query.Limit]
	}
	return result, nil
}

func (r *memoryRecoveryRepository) AttentionCase(_ context.Context, id AttentionCaseID) (AttentionCase, error) {
	for _, attention := range r.attention {
		if attention.ID == id {
			return attention, nil
		}
	}
	return AttentionCase{}, fs.ErrNotExist
}

func (r *memoryRecoveryRepository) CreateVerification(_ context.Context, verification Verification) error {
	r.storedVerification = verification
	return nil
}

func (r *memoryRecoveryRepository) Verification(_ context.Context, id VerificationID) (Verification, error) {
	if r.storedVerification.ID != id {
		return Verification{}, fs.ErrNotExist
	}
	return r.storedVerification, nil
}
