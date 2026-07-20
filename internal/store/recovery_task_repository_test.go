/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestRecoveryTaskRepositoryPersistsRestoreStagesLeaseAndAttentionResolution(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	target := testRecoveryBackup(1, testTime(1), config.BackupStateComplete)
	insertRecoveryBackup(t, database, target)
	attentionID := config.AttentionCaseID("dddddddddddddddddddddddddddddddd")
	if _, err := database.sql.ExecContext(ctx, `INSERT INTO config_attention_cases(
		id, subject_type, subject_id, backup_id, state, reason_code, public_evidence_json, opened_at
	) VALUES (?, 'release', ?, ?, 'open', 'runtime_unknown', '{}', ?)`, attentionID,
		"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", target.ID, formatTime(testTime(2))); err != nil {
		t.Fatal(err)
	}
	restore := config.Restore{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TargetBackupID: target.ID,
		SafetyBackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", AttentionCaseID: attentionID,
		State: config.RestoreStateQueued, Stage: config.RestoreStageQueued,
		SourceDigest: testDigest(9), TargetDigest: target.ProductionDigest,
		CreatedBy: 1, Reason: "recover known-good configuration", RequestID: "restore-request",
		CreatedAt: testTime(3), UpdatedAt: testTime(3),
	}
	stage := config.RestoreStage{
		RestoreID: restore.ID, Sequence: 1, Stage: config.RestoreStageQueued,
		Result: config.StageResultPending, PublicDetailsJSON: "{}", OccurredAt: restore.CreatedAt,
	}
	if err := database.CreateRestore(ctx, restore, stage); err != nil {
		t.Fatalf("CreateRestore() error = %v", err)
	}
	lease, err := database.ProductionLease(ctx)
	if err != nil || lease.OwnerType != config.ProductionOperationRestore || lease.OwnerID != string(restore.ID) {
		t.Fatalf("restore lease = %#v, error = %v", lease, err)
	}
	active, err := database.ActiveRestore(ctx)
	if err != nil || !reflect.DeepEqual(active, normalizeRestoreTimes(restore)) {
		t.Fatalf("ActiveRestore() = %#v, error = %v", active, err)
	}
	running := restore
	running.State = config.RestoreStateRunning
	running.Stage = config.RestoreStageTargetVerifying
	running.UpdatedAt = testTime(4)
	if err := database.TransitionRestore(ctx, restore.State, restore.Stage, running, config.RestoreStage{
		RestoreID: restore.ID, Sequence: 2, Stage: running.Stage, Result: config.StageResultRunning,
		PublicDetailsJSON: "{}", OccurredAt: running.UpdatedAt,
	}); err != nil {
		t.Fatalf("TransitionRestore(running) error = %v", err)
	}
	safety := testRecoveryBackup(2, testTime(4), config.BackupStateComplete)
	safety.ID = restore.SafetyBackupID
	safety.OriginID = string(restore.ID)
	if err := database.PutBackup(ctx, safety); err != nil {
		t.Fatalf("PutBackup(safety) error = %v", err)
	}
	succeeded := running
	succeeded.State = config.RestoreStateSucceeded
	succeeded.Stage = config.RestoreStageSucceeded
	succeeded.UpdatedAt = testTime(5)
	succeeded.FinishedAt = testTime(5)
	if err := database.TransitionRestore(ctx, running.State, running.Stage, succeeded, config.RestoreStage{
		RestoreID: restore.ID, Sequence: 3, Stage: succeeded.Stage, Result: config.StageResultSuccess,
		PublicDetailsJSON: "{}", OccurredAt: succeeded.UpdatedAt,
	}); err != nil {
		t.Fatalf("TransitionRestore(succeeded) error = %v", err)
	}
	if err := database.ResolveAttentionCase(
		ctx, attentionID, config.AttentionResolutionRestore, string(restore.ID),
		config.Actor{UserID: 1, RequestID: "restore-request"}, testTime(5),
	); err != nil {
		t.Fatalf("ResolveAttentionCase() error = %v", err)
	}
	attention, err := database.AttentionCase(ctx, attentionID)
	if err != nil || attention.State != config.AttentionCaseResolved ||
		attention.ResolutionType != config.AttentionResolutionRestore || attention.ResolutionID != string(restore.ID) {
		t.Fatalf("resolved attention = %#v, error = %v", attention, err)
	}
	stored, err := database.Restore(ctx, restore.ID)
	if err != nil || stored.State != config.RestoreStateSucceeded {
		t.Fatalf("Restore() = %#v, error = %v", stored, err)
	}
	stages, err := database.RestoreStages(ctx, restore.ID, 1, 10)
	if err != nil || len(stages) != 2 || stages[0].Sequence != 2 || stages[1].Sequence != 3 {
		t.Fatalf("RestoreStages() = %#v, error = %v", stages, err)
	}
	history, err := database.ListRestores(ctx, config.HistoryQuery{Limit: 10})
	if err != nil || len(history) != 1 || history[0].ID != restore.ID {
		t.Fatalf("ListRestores() = %#v, error = %v", history, err)
	}
	lease, err = database.ProductionLease(ctx)
	if err != nil || lease != (config.ProductionLease{}) {
		t.Fatalf("terminal restore lease = %#v, error = %v", lease, err)
	}
}

func TestRecoveryTaskRepositoryRestartNeedsAttentionOpensCaseAndReleasesLease(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	restart := config.Restart{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: config.RestartStateQueued,
		Stage: config.RestartStageQueued, ProductionDigest: testDigest(7), CreatedBy: 1,
		Reason: "recover supervised nginx", RequestID: "restart-request",
		CreatedAt: testTime(1), UpdatedAt: testTime(1),
	}
	if err := database.CreateRestart(ctx, restart, config.RestartStage{
		RestartID: restart.ID, Sequence: 1, Stage: restart.Stage, Result: config.StageResultPending,
		PublicDetailsJSON: "{}", OccurredAt: restart.CreatedAt,
	}); err != nil {
		t.Fatalf("CreateRestart() error = %v", err)
	}
	if err := database.CreateRestart(ctx, config.Restart{
		ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", State: config.RestartStateQueued,
		Stage: config.RestartStageQueued, ProductionDigest: testDigest(8), CreatedBy: 1,
		Reason: "second restart", RequestID: "restart-conflict", CreatedAt: testTime(2), UpdatedAt: testTime(2),
	}, config.RestartStage{
		RestartID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Sequence: 1, Stage: config.RestartStageQueued,
		Result: config.StageResultPending, PublicDetailsJSON: "{}", OccurredAt: testTime(2),
	}); !errors.Is(err, config.ErrOperationInProgress) {
		t.Fatalf("concurrent CreateRestart() error = %v, want ErrOperationInProgress", err)
	}
	running := restart
	running.State = config.RestartStateRunning
	running.Stage = config.RestartStageRestartRequested
	running.BeforeMasterPID = 101
	running.UpdatedAt = testTime(2)
	if err := database.TransitionRestart(ctx, restart.State, restart.Stage, running, config.RestartStage{
		RestartID: restart.ID, Sequence: 2, Stage: running.Stage, Result: config.StageResultRunning,
		PublicDetailsJSON: "{}", OccurredAt: running.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	uncertain := running
	uncertain.State = config.RestartStateNeedsAttention
	uncertain.Stage = config.RestartStageNeedsAttention
	uncertain.LastErrorCode = "restart_runtime_unknown"
	uncertain.UpdatedAt = testTime(3)
	uncertain.FinishedAt = testTime(3)
	if err := database.TransitionRestart(ctx, running.State, running.Stage, uncertain, config.RestartStage{
		RestartID: restart.ID, Sequence: 3, Stage: uncertain.Stage, Result: config.StageResultFailed,
		Code: uncertain.LastErrorCode, PublicDetailsJSON: "{}", OccurredAt: uncertain.UpdatedAt,
	}); err != nil {
		t.Fatalf("TransitionRestart(needs attention) error = %v", err)
	}
	attention, err := database.AttentionCase(ctx, config.AttentionCaseID(restart.ID))
	if err != nil || attention.SubjectType != config.AttentionSubjectRestart ||
		attention.SubjectID != string(restart.ID) || attention.State != config.AttentionCaseOpen {
		t.Fatalf("restart attention = %#v, error = %v", attention, err)
	}
	stored, err := database.Restart(ctx, restart.ID)
	if err != nil || stored.State != config.RestartStateNeedsAttention {
		t.Fatalf("Restart() = %#v, error = %v", stored, err)
	}
	history, err := database.ListRestarts(ctx, config.HistoryQuery{Limit: 1})
	if err != nil || len(history) != 1 || history[0].ID != restart.ID {
		t.Fatalf("ListRestarts() = %#v, error = %v", history, err)
	}
	lease, err := database.ProductionLease(ctx)
	if err != nil || lease != (config.ProductionLease{}) {
		t.Fatalf("terminal restart lease = %#v, error = %v", lease, err)
	}
}

func TestRecoveryTaskRepositoryVerificationMustSucceedBeforeResolvingAttention(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	caseID := config.AttentionCaseID("11111111111111111111111111111111")
	if _, err := database.sql.ExecContext(ctx, `INSERT INTO config_attention_cases(
		id, subject_type, subject_id, state, reason_code, public_evidence_json, opened_at
	) VALUES (?, 'restart', ?, 'open', 'runtime_unknown', '{}', ?)`,
		caseID, "22222222222222222222222222222222", formatTime(testTime(1))); err != nil {
		t.Fatal(err)
	}
	failed := config.Verification{
		ID: "33333333333333333333333333333333", AttentionCaseID: caseID,
		State: config.VerificationStateFailed, ProductionDigest: testDigest(3),
		LastErrorCode: "runtime_identity_invalid", CreatedBy: 1, RequestID: "verify-failed",
		CreatedAt: testTime(2), FinishedAt: testTime(3),
	}
	if err := database.CreateVerification(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if err := database.ResolveAttentionCase(ctx, caseID, config.AttentionResolutionVerification,
		string(failed.ID), config.Actor{UserID: 1, RequestID: "verify-failed"}, testTime(3)); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("failed verification resolution error = %v", err)
	}
	succeeded := config.Verification{
		ID: "44444444444444444444444444444444", AttentionCaseID: caseID,
		State: config.VerificationStateSucceeded, ProductionDigest: testDigest(4),
		MasterPID: 100, WorkerCount: 2, HTTPStatus: 204, CreatedBy: 1,
		RequestID: "verify-success", CreatedAt: testTime(4), FinishedAt: testTime(5),
	}
	if err := database.CreateVerification(ctx, succeeded); err != nil {
		t.Fatal(err)
	}
	if err := database.ResolveAttentionCase(ctx, caseID, config.AttentionResolutionVerification,
		string(succeeded.ID), config.Actor{UserID: 1, RequestID: "verify-success"}, testTime(5)); err != nil {
		t.Fatalf("successful verification resolution error = %v", err)
	}
	stored, err := database.Verification(ctx, succeeded.ID)
	if err != nil || stored.State != config.VerificationStateSucceeded || stored.MasterPID != 100 {
		t.Fatalf("Verification() = %#v, %v", stored, err)
	}
	attention, err := database.AttentionCase(ctx, caseID)
	if err != nil || attention.ResolutionType != config.AttentionResolutionVerification ||
		attention.ResolutionID != string(succeeded.ID) {
		t.Fatalf("resolved attention = %#v, %v", attention, err)
	}
}

func normalizeRestoreTimes(restore config.Restore) config.Restore {
	restore.CreatedAt = restore.CreatedAt.UTC()
	restore.UpdatedAt = restore.UpdatedAt.UTC()
	return restore
}
