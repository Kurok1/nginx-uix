/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package config

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRecoveryServiceQueuesAndRunsRestoreWithIndexedSafetyBackup(t *testing.T) {
	now := time.Date(2026, time.July, 19, 14, 0, 0, 0, time.UTC)
	target := recoveryServiceBackup(3, now.Add(-48*time.Hour), 512, false)
	target.OriginType = BackupOriginRelease
	target.OriginID = "33333333333333333333333333333333"
	target.ReleaseID = ReleaseID(target.OriginID)
	target.ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	restoreID := RestoreID("22222222222222222222222222222222")
	safetyID := BackupID("44444444444444444444444444444444")
	attentionID := AttentionCaseID("55555555555555555555555555555555")
	sourceDigest := recoveryDigest(90)
	safety := BackupEvidence{
		BackupID: safetyID, OriginType: BackupOriginRestore, OriginID: string(restoreID),
		ProductionDigest: sourceDigest, TreeDigest: recoveryDigest(91), EntryCount: 4,
		TotalBytes: 600, VerifiedAt: now.Add(time.Second),
	}
	preparationStages := []RestoreStage{
		{RestoreID: restoreID, Sequence: 1, Stage: RestoreStageTargetVerifying, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now.Add(time.Second)},
		{RestoreID: restoreID, Sequence: 2, Stage: RestoreStageTargetValidated, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(2 * time.Second)},
		{RestoreID: restoreID, Sequence: 3, Stage: RestoreStageSafetyBackupCreating, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now.Add(3 * time.Second)},
		{RestoreID: restoreID, Sequence: 4, Stage: RestoreStageSafetyBackupVerified, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(4 * time.Second)},
	}
	executionStages := append(append([]RestoreStage(nil), preparationStages...), RestoreStage{
		RestoreID: restoreID, Sequence: 5, Stage: RestoreStageSucceeded, Result: StageResultSuccess,
		PublicDetailsJSON: `{}`, OccurredAt: now.Add(5 * time.Second),
	})
	repository := &memoryRecoveryRepository{
		backups: []Backup{target}, attention: []AttentionCase{{
			ID: attentionID, SubjectType: AttentionSubjectRelease, SubjectID: string(target.ReleaseID),
			BackupID: target.ID, State: AttentionCaseOpen, ReasonCode: "release_uncertain", OpenedAt: now.Add(-time.Hour),
		}},
	}
	agent := &memoryRecoveryAgent{
		verification: BackupEvidence{
			BackupID: target.ID, OriginType: target.OriginType, OriginID: target.OriginID,
			ReleaseID: target.ReleaseID, ProductionDigest: target.ProductionDigest,
			TreeDigest: target.TreeDigest, EntryCount: target.EntryCount, TotalBytes: target.TotalBytes,
			VerifiedAt: target.VerifiedAt,
		},
		production: ProductionState{Digest: sourceDigest},
		preparation: RestorePreparationResult{
			RestoreID: restoreID, State: RestoreStateRunning, Stage: RestoreStageSafetyBackupVerified,
			SafetyBackup: safety, Stages: preparationStages,
		},
		restoreResult: RestoreExecutionResult{
			RestoreID: restoreID, State: RestoreStateSucceeded, Stage: RestoreStageSucceeded,
			SafetyBackup: safety, Stages: executionStages, MasterPID: 100, WorkerCount: 2,
			HTTPStatus: 204, FinishedAt: now.Add(5 * time.Second),
		},
	}
	random := append(bytes.Repeat([]byte{0x22}, 16), bytes.Repeat([]byte{0x44}, 16)...)
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: agent, Clock: &fixedClock{now: now}, Random: bytes.NewReader(random),
		Policy: DefaultRetentionPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 7, RequestID: "restore-request"}
	queued, err := service.QueueRestore(context.Background(), actor, QueueRestoreInput{
		TargetBackupID: target.ID, AttentionCaseID: attentionID,
		Reason: "recover the last known configuration", ConfirmBackupID: string(target.ID),
	})
	if err != nil {
		t.Fatalf("QueueRestore() error = %v", err)
	}
	if queued.ID != restoreID || queued.SafetyBackupID != safetyID || queued.SourceDigest != sourceDigest ||
		queued.TargetDigest != target.ProductionDigest || queued.State != RestoreStateQueued {
		t.Fatalf("queued restore = %#v", queued)
	}
	if agent.verifyCalls != 2 || agent.configDigestCalls != 2 {
		t.Fatalf("queue rechecks = verify %d, digest %d", agent.verifyCalls, agent.configDigestCalls)
	}
	if err := service.RunRestore(context.Background(), restoreID); err != nil {
		t.Fatalf("RunRestore() error = %v", err)
	}
	if repository.storedRestore.State != RestoreStateSucceeded ||
		repository.storedRestore.Stage != RestoreStageSucceeded || len(repository.restoreStages) != 6 {
		t.Fatalf("stored restore = %#v, stages = %#v", repository.storedRestore, repository.restoreStages)
	}
	if len(repository.putBackups) != 1 || repository.putBackups[0].ID != safetyID ||
		repository.putBackups[0].OriginID != string(restoreID) {
		t.Fatalf("indexed safety backups = %#v", repository.putBackups)
	}
	wantExecution := RestoreExecutionRequest{
		RestoreID: restoreID, TargetBackupID: target.ID, SafetyBackupID: safetyID,
		SourceDigest: sourceDigest, TargetDigest: target.ProductionDigest,
		TargetTreeDigest: target.TreeDigest, SafetyTreeDigest: safety.TreeDigest,
	}
	if !reflect.DeepEqual(agent.restoreRequest, wantExecution) {
		t.Fatalf("restore request = %#v, want %#v", agent.restoreRequest, wantExecution)
	}
	if repository.attention[0].State != AttentionCaseResolved ||
		repository.attention[0].ResolutionType != AttentionResolutionRestore ||
		repository.attention[0].ResolutionID != string(restoreID) {
		t.Fatalf("resolved attention = %#v", repository.attention[0])
	}
}

func TestRecoveryServiceClassifiesDamagedRestoreTargetBeforeCreatingTask(t *testing.T) {
	now := time.Date(2026, time.July, 19, 14, 30, 0, 0, time.UTC)
	target := recoveryServiceBackup(3, now.Add(-48*time.Hour), 512, false)
	target.ID = "abababababababababababababababab"
	repository := &memoryRecoveryRepository{backups: []Backup{target}}
	agent := &memoryRecoveryAgent{verifyErr: fs.ErrNotExist}
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: agent, Clock: &fixedClock{now: now},
		Random: bytes.NewReader(bytes.Repeat([]byte{0x22}, 32)), Policy: DefaultRetentionPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.QueueRestore(context.Background(), Actor{UserID: 7, RequestID: "invalid-target"}, QueueRestoreInput{
		TargetBackupID: target.ID, Reason: "recover known configuration", ConfirmBackupID: string(target.ID),
	})
	if !errors.Is(err, ErrBackupTargetInvalid) || repository.storedRestore.ID != "" {
		t.Fatalf("QueueRestore() error/task = %v/%#v", err, repository.storedRestore)
	}
}

func TestRecoveryServiceQueuesAndRunsFixedRestart(t *testing.T) {
	now := time.Date(2026, time.July, 19, 15, 0, 0, 0, time.UTC)
	restartID := RestartID("66666666666666666666666666666666")
	production := ProductionState{Digest: recoveryDigest(66)}
	repository := &memoryRecoveryRepository{}
	agent := &memoryRecoveryAgent{
		production: production,
		restartResult: RestartExecutionResult{
			RestartID: restartID, State: RestartStateSucceeded, Stage: RestartStageSucceeded,
			Stages: []RestartStage{
				{RestartID: restartID, Sequence: 1, Stage: RestartStageProductionValidating, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now.Add(time.Second)},
				{RestartID: restartID, Sequence: 2, Stage: RestartStageSucceeded, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(2 * time.Second)},
			},
			BeforeMasterPID: 100, AfterMasterPID: 200, WorkerCount: 2, HTTPStatus: 204,
			FinishedAt: now.Add(2 * time.Second),
		},
	}
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: agent, Clock: &fixedClock{now: now},
		Random: bytes.NewReader(bytes.Repeat([]byte{0x66}, 16)), Policy: DefaultRetentionPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 7, RequestID: "restart-request"}
	if _, err := service.QueueRestart(context.Background(), actor, QueueRestartInput{
		Reason: "restart after runtime degradation", Confirmation: "restart nginx",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unsafe QueueRestart() error = %v", err)
	}
	queued, err := service.QueueRestart(context.Background(), actor, QueueRestartInput{
		Reason: "restart after runtime degradation", Confirmation: "RESTART NGINX",
	})
	if err != nil {
		t.Fatalf("QueueRestart() error = %v", err)
	}
	if queued.ID != restartID || queued.ProductionDigest != production.Digest || queued.State != RestartStateQueued {
		t.Fatalf("queued restart = %#v", queued)
	}
	if err := service.RunRestart(context.Background(), restartID); err != nil {
		t.Fatalf("RunRestart() error = %v", err)
	}
	if repository.storedRestart.State != RestartStateSucceeded || repository.storedRestart.AfterMasterPID != 200 ||
		len(repository.restartStages) != 3 || agent.restartRequest.ProductionDigest != production.Digest {
		t.Fatalf("stored restart = %#v, stages = %#v, request = %#v",
			repository.storedRestart, repository.restartStages, agent.restartRequest)
	}
}

func TestRecoveryServiceMirrorsRestartProgressBeforeExecutionCompletes(t *testing.T) {
	now := time.Date(2026, time.July, 19, 15, 30, 0, 0, time.UTC)
	restartID := RestartID("77777777777777777777777777777777")
	production := ProductionState{Digest: recoveryDigest(77)}
	stages := []RestartStage{
		{RestartID: restartID, Sequence: 1, Stage: RestartStageProductionValidating, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now.Add(time.Second)},
		{RestartID: restartID, Sequence: 2, Stage: RestartStageRuntimeSampling, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(2 * time.Second)},
		{RestartID: restartID, Sequence: 3, Stage: RestartStageRestartRequested, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(3 * time.Second)},
		{RestartID: restartID, Sequence: 4, Stage: RestartStageRuntimeConfirming, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now.Add(4 * time.Second)},
		{RestartID: restartID, Sequence: 5, Stage: RestartStageSucceeded, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(5 * time.Second)},
	}
	base := &memoryRecoveryAgent{
		production: production,
		restartResult: RestartExecutionResult{
			RestartID: restartID, State: RestartStateSucceeded, Stage: RestartStageSucceeded,
			Stages: stages, BeforeMasterPID: 100, AfterMasterPID: 200,
			WorkerCount: 2, HTTPStatus: 204, FinishedAt: now.Add(5 * time.Second),
		},
	}
	agent := &progressiveRestartRecoveryAgent{
		memoryRecoveryAgent: base, started: make(chan struct{}), finish: make(chan struct{}),
		polled: make(chan struct{}), progress: RestartExecutionResult{
			RestartID: restartID, State: RestartStateRunning, Stage: RestartStageRuntimeSampling,
			Stages: stages[:2],
		},
	}
	if !validRestartExecutionProgress(agent.progress, Restart{
		ID: restartID, State: RestartStateRunning, Stage: RestartStageProductionValidating,
	}, 1, nil) {
		t.Fatalf("test restart progress evidence is invalid: %#v", agent.progress)
	}
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(agent.finish) }) }
	t.Cleanup(finish)
	repository := &memoryRecoveryRepository{
		restartTransition: make(chan RestartStageName), restartNotifyStage: RestartStageRuntimeSampling,
	}
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: agent, Clock: &fixedClock{now: now},
		Random: bytes.NewReader(bytes.Repeat([]byte{0x77}, 16)), Policy: DefaultRetentionPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := service.QueueRestart(context.Background(), Actor{UserID: 7, RequestID: "restart-progress"}, QueueRestartInput{
		Reason: "restart after runtime degradation", Confirmation: "RESTART NGINX",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { completed <- service.RunRestart(context.Background(), queued.ID) }()
	select {
	case <-agent.started:
	case <-time.After(time.Second):
		t.Fatal("restart execution did not start")
	}
	select {
	case <-agent.polled:
	case <-time.After(2 * time.Second):
		t.Fatal("restart progress was not polled")
	}
	select {
	case stage := <-repository.restartTransition:
		if stage != RestartStageRuntimeSampling {
			t.Fatalf("persisted stage = %s, want %s", stage, RestartStageRuntimeSampling)
		}
	case <-time.After(time.Second):
		t.Fatal("restart progress was not persisted")
	}
	finish()
	if err := <-completed; err != nil {
		t.Fatalf("RunRestart() error = %v", err)
	}
}

func TestRecoveryServiceMirrorsRestoreProgressBeforeExecutionCompletes(t *testing.T) {
	now := time.Date(2026, time.July, 19, 15, 45, 0, 0, time.UTC)
	restoreID := RestoreID("88888888888888888888888888888888")
	target := recoveryServiceBackup(8, now.Add(-48*time.Hour), 512, false)
	target.ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	target.OriginType = BackupOriginRelease
	target.OriginID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	target.ReleaseID = ReleaseID(target.OriginID)
	safetyID := BackupID("cccccccccccccccccccccccccccccccc")
	sourceDigest := recoveryDigest(80)
	safety := BackupEvidence{
		BackupID: safetyID, OriginType: BackupOriginRestore, OriginID: string(restoreID),
		ProductionDigest: sourceDigest, TreeDigest: recoveryDigest(81), EntryCount: 4,
		TotalBytes: 600, VerifiedAt: now.Add(time.Second),
	}
	preparation := []RestoreStage{
		{RestoreID: restoreID, Sequence: 1, Stage: RestoreStageTargetVerifying, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now.Add(time.Second)},
		{RestoreID: restoreID, Sequence: 2, Stage: RestoreStageTargetValidated, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(2 * time.Second)},
		{RestoreID: restoreID, Sequence: 3, Stage: RestoreStageSafetyBackupCreating, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now.Add(3 * time.Second)},
		{RestoreID: restoreID, Sequence: 4, Stage: RestoreStageSafetyBackupVerified, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(4 * time.Second)},
	}
	stages := append(append([]RestoreStage(nil), preparation...),
		RestoreStage{RestoreID: restoreID, Sequence: 5, Stage: RestoreStageFilesRestoring, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now.Add(5 * time.Second)},
		RestoreStage{RestoreID: restoreID, Sequence: 6, Stage: RestoreStageFilesRestored, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(6 * time.Second)},
		RestoreStage{RestoreID: restoreID, Sequence: 7, Stage: RestoreStageSucceeded, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now.Add(7 * time.Second)},
	)
	restore := Restore{
		ID: restoreID, TargetBackupID: target.ID, SafetyBackupID: safetyID,
		State: RestoreStateQueued, Stage: RestoreStageQueued, SourceDigest: sourceDigest,
		TargetDigest: target.ProductionDigest, CreatedBy: 7, Reason: "restore known configuration",
		RequestID: "restore-progress", CreatedAt: now, UpdatedAt: now,
	}
	base := &memoryRecoveryAgent{
		preparation: RestorePreparationResult{
			RestoreID: restoreID, State: RestoreStateRunning, Stage: RestoreStageSafetyBackupVerified,
			SafetyBackup: safety, Stages: preparation,
		},
		restoreResult: RestoreExecutionResult{
			RestoreID: restoreID, State: RestoreStateSucceeded, Stage: RestoreStageSucceeded,
			SafetyBackup: safety, Stages: stages, MasterPID: 100, WorkerCount: 2,
			HTTPStatus: 204, FinishedAt: now.Add(7 * time.Second),
		},
	}
	agent := &progressiveRestoreRecoveryAgent{
		memoryRecoveryAgent: base, started: make(chan struct{}), finish: make(chan struct{}),
		polled: make(chan struct{}), progress: RestoreExecutionResult{
			RestoreID: restoreID, State: RestoreStateRunning, Stage: RestoreStageFilesRestoring,
			SafetyBackup: safety, Stages: stages[:5],
		},
	}
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(agent.finish) }) }
	t.Cleanup(finish)
	repository := &memoryRecoveryRepository{
		backups: []Backup{target}, storedRestore: restore,
		restoreStages: []RestoreStage{{
			RestoreID: restoreID, Sequence: 1, Stage: RestoreStageQueued,
			Result: StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now,
		}},
		lease:             ProductionLease{OwnerType: ProductionOperationRestore, OwnerID: string(restoreID), AcquiredAt: now},
		restoreTransition: make(chan RestoreStageName), restoreNotifyStage: RestoreStageFilesRestoring,
	}
	service, err := NewRecoveryService(RecoveryDependencies{
		Repository: repository, Agent: agent, Clock: &fixedClock{now: now},
		Random: bytes.NewReader(bytes.Repeat([]byte{0x88}, 16)), Policy: DefaultRetentionPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { completed <- service.RunRestore(context.Background(), restoreID) }()
	select {
	case <-agent.started:
	case <-time.After(time.Second):
		t.Fatal("restore execution did not start")
	}
	select {
	case <-agent.polled:
	case <-time.After(2 * time.Second):
		t.Fatal("restore progress was not polled")
	}
	select {
	case stage := <-repository.restoreTransition:
		if stage != RestoreStageFilesRestoring {
			t.Fatalf("persisted stage = %s, want %s", stage, RestoreStageFilesRestoring)
		}
	case <-time.After(time.Second):
		t.Fatal("restore progress was not persisted")
	}
	finish()
	if err := <-completed; err != nil {
		t.Fatalf("RunRestore() error = %v", err)
	}
}

type progressiveRestoreRecoveryAgent struct {
	*memoryRecoveryAgent
	started  chan struct{}
	finish   chan struct{}
	polled   chan struct{}
	pollOnce sync.Once
	progress RestoreExecutionResult
}

func (a *progressiveRestoreRecoveryAgent) ExecuteRestore(
	_ context.Context,
	_ string,
	request RestoreExecutionRequest,
) (RestoreExecutionResult, error) {
	a.restoreRequest = request
	close(a.started)
	<-a.finish
	return a.restoreResult, a.restoreErr
}

func (a *progressiveRestoreRecoveryAgent) RestoreProgress(
	context.Context,
	string,
	RestoreExecutionRequest,
) (RestoreExecutionResult, error) {
	a.pollOnce.Do(func() { close(a.polled) })
	return a.progress, nil
}

type progressiveRestartRecoveryAgent struct {
	*memoryRecoveryAgent
	started  chan struct{}
	finish   chan struct{}
	polled   chan struct{}
	pollOnce sync.Once
	progress RestartExecutionResult
}

func (a *progressiveRestartRecoveryAgent) ExecuteRestart(
	_ context.Context,
	_ string,
	request RestartExecutionRequest,
) (RestartExecutionResult, error) {
	a.restartRequest = request
	close(a.started)
	<-a.finish
	return a.restartResult, a.restartErr
}

func (a *progressiveRestartRecoveryAgent) RestartProgress(
	context.Context,
	string,
	RestartExecutionRequest,
) (RestartExecutionResult, error) {
	a.pollOnce.Do(func() { close(a.polled) })
	return a.progress, nil
}

func TestRecoveryServiceReconcilesInterruptedRecoveryTasksBeforeServing(t *testing.T) {
	now := time.Date(2026, time.July, 19, 16, 0, 0, 0, time.UTC)
	t.Run("queued restore becomes failed", func(t *testing.T) {
		restore := Restore{
			ID: "11111111111111111111111111111111", TargetBackupID: "22222222222222222222222222222222",
			SafetyBackupID: "33333333333333333333333333333333", State: RestoreStateQueued,
			Stage: RestoreStageQueued, SourceDigest: recoveryDigest(1), TargetDigest: recoveryDigest(2),
			CreatedBy: 7, Reason: "recover interrupted task", RequestID: "restore-reconcile",
			CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
		}
		repository := &memoryRecoveryRepository{
			storedRestore: restore,
			restoreStages: []RestoreStage{{
				RestoreID: restore.ID, Sequence: 1, Stage: RestoreStageQueued, Result: StageResultPending,
				PublicDetailsJSON: `{}`, OccurredAt: restore.CreatedAt,
			}},
			lease: ProductionLease{OwnerType: ProductionOperationRestore, OwnerID: string(restore.ID), AcquiredAt: restore.CreatedAt},
		}
		service, err := NewRecoveryService(validRecoveryDependencies(repository))
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if repository.storedRestore.State != RestoreStateFailed || repository.lease.OwnerID != "" {
			t.Fatalf("reconciled restore = %#v, lease = %#v", repository.storedRestore, repository.lease)
		}
	})

	t.Run("running restart uses agent recovery evidence", func(t *testing.T) {
		restart := Restart{
			ID: "44444444444444444444444444444444", State: RestartStateRunning,
			Stage: RestartStageProductionValidating, ProductionDigest: recoveryDigest(4),
			CreatedBy: 7, Reason: "recover interrupted restart", RequestID: "restart-reconcile",
			CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-30 * time.Second),
		}
		repository := &memoryRecoveryRepository{
			storedRestart: restart,
			restartStages: []RestartStage{
				{RestartID: restart.ID, Sequence: 1, Stage: RestartStageQueued, Result: StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: restart.CreatedAt},
				{RestartID: restart.ID, Sequence: 2, Stage: RestartStageProductionValidating, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: restart.UpdatedAt},
			},
			lease: ProductionLease{OwnerType: ProductionOperationRestart, OwnerID: string(restart.ID), AcquiredAt: restart.CreatedAt},
		}
		agent := &memoryRecoveryAgent{restartResult: RestartExecutionResult{
			RestartID: restart.ID, State: RestartStateSucceeded, Stage: RestartStageSucceeded,
			Stages: []RestartStage{
				{RestartID: restart.ID, Sequence: 1, Stage: RestartStageProductionValidating, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: restart.UpdatedAt},
				{RestartID: restart.ID, Sequence: 2, Stage: RestartStageSucceeded, Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now},
			},
			BeforeMasterPID: 100, AfterMasterPID: 200, WorkerCount: 2, HTTPStatus: 204, FinishedAt: now,
		}}
		dependencies := validRecoveryDependencies(repository)
		dependencies.Agent = agent
		service, err := NewRecoveryService(dependencies)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if repository.storedRestart.State != RestartStateSucceeded || repository.lease.OwnerID != "" ||
			len(repository.restartStages) != 3 {
			t.Fatalf("reconciled restart = %#v, lease = %#v, stages = %#v",
				repository.storedRestart, repository.lease, repository.restartStages)
		}
	})
}

func TestRecoveryServiceReconcilesRetentionDeletionTombstone(t *testing.T) {
	now := time.Date(2026, time.July, 19, 17, 0, 0, 0, time.UTC)
	backup := recoveryServiceBackup(8, now.Add(-72*time.Hour), 400, false)
	backup.State = BackupStateDeleting
	run := recoveryServiceRetentionRun(now, backup)
	run.State = RetentionRunExecuting
	run.BackupCount = 1
	run.TotalBytes = backup.TotalBytes
	run.ExecutionRequestID = "retention-execute"
	run.StartedAt = now.Add(-30 * time.Second)
	item := RetentionItem{
		RunID: run.ID, Ordinal: 0, BackupID: backup.ID, Decision: RetentionDecisionDelete,
		ReasonCode: "maximum_complete", State: RetentionItemDeleting,
		SnapshotCreatedAt: backup.CreatedAt, SnapshotTotalBytes: backup.TotalBytes, UpdatedAt: run.StartedAt,
	}
	repository := &memoryRecoveryRepository{
		backups: []Backup{backup}, storedRun: run, storedItems: []RetentionItem{item},
		lease: ProductionLease{OwnerType: ProductionOperationRetention, OwnerID: string(run.ID), AcquiredAt: run.StartedAt},
	}
	agent := &memoryRecoveryAgent{verifyErr: fs.ErrNotExist}
	dependencies := validRecoveryDependencies(repository)
	dependencies.Agent = agent
	service, err := NewRecoveryService(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if repository.storedItems[0].State != RetentionItemDeleted ||
		repository.storedRun.State != RetentionRunSucceeded || repository.lease.OwnerID != "" {
		t.Fatalf("reconciled retention = %#v/%#v/%#v",
			repository.storedRun, repository.storedItems, repository.lease)
	}
}

func TestRecoveryServiceResolvesAttentionOnlyWithSuccessfulFixedVerification(t *testing.T) {
	now := time.Date(2026, time.July, 19, 18, 0, 0, 0, time.UTC)
	caseID := AttentionCaseID("11111111111111111111111111111111")
	verificationID := VerificationID("77777777777777777777777777777777")
	production := ProductionState{Digest: recoveryDigest(7)}
	repository := &memoryRecoveryRepository{attention: []AttentionCase{{
		ID: caseID, SubjectType: AttentionSubjectRestart,
		SubjectID: "22222222222222222222222222222222", State: AttentionCaseOpen,
		ReasonCode: "runtime_unknown", PublicEvidenceJSON: `{}`, OpenedAt: now.Add(-time.Hour),
	}}}
	agent := &memoryRecoveryAgent{
		production: production,
		runtimeVerification: RuntimeVerificationResult{
			VerificationID: verificationID, State: VerificationStateSucceeded,
			ProductionDigest: production.Digest, MasterPID: 100, WorkerCount: 2,
			HTTPStatus: 204, CheckedAt: now.Add(time.Second),
		},
	}
	dependencies := validRecoveryDependencies(repository)
	dependencies.Agent = agent
	dependencies.Random = bytes.NewReader(bytes.Repeat([]byte{0x77}, 16))
	service, err := NewRecoveryService(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 7, RequestID: "verify-attention"}
	verification, err := service.VerifyAttentionCase(context.Background(), actor, caseID)
	if err != nil {
		t.Fatalf("VerifyAttentionCase() error = %v", err)
	}
	if verification.ID != verificationID || verification.State != VerificationStateSucceeded ||
		repository.storedVerification.ID != verificationID ||
		repository.attention[0].ResolutionType != AttentionResolutionVerification {
		t.Fatalf("verification = %#v, stored = %#v, attention = %#v",
			verification, repository.storedVerification, repository.attention[0])
	}
	if agent.runtimeVerifyRequest.ProductionDigest != production.Digest ||
		agent.runtimeVerifyRequest.VerificationID != verificationID {
		t.Fatalf("runtime verification request = %#v", agent.runtimeVerifyRequest)
	}
}
