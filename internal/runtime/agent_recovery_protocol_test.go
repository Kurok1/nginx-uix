/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package runtime

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestAgentClientRoundTripsTypedBackupRestoreAndRestartOperations(t *testing.T) {
	now := time.Now().UTC()
	operations := &recordingRecoveryAgentOperations{recordingAgentOperations: &recordingAgentOperations{}}
	operations.backup = config.BackupEvidence{
		BackupID: "11111111111111111111111111111111", OriginType: config.BackupOriginRestore,
		OriginID: "22222222222222222222222222222222", ProductionDigest: config.Digest{1},
		TreeDigest: config.Digest{2}, EntryCount: 2, TotalBytes: 20, VerifiedAt: now,
	}
	restoreRequest := config.RestoreExecutionRequest{
		RestoreID:      "22222222222222222222222222222222",
		TargetBackupID: "33333333333333333333333333333333",
		SafetyBackupID: operations.backup.BackupID, SourceDigest: config.Digest{1},
		TargetDigest: config.Digest{3}, TargetTreeDigest: config.Digest{4}, SafetyTreeDigest: config.Digest{2},
	}
	operations.preparation = config.RestorePreparationResult{
		RestoreID: restoreRequest.RestoreID, State: config.RestoreStateRunning,
		Stage: config.RestoreStageSafetyBackupVerified, SafetyBackup: operations.backup,
		Stages: []config.RestoreStage{{
			RestoreID: restoreRequest.RestoreID, Sequence: 1, Stage: config.RestoreStageSafetyBackupVerified,
			Result: config.StageResultSuccess, PublicDetailsJSON: "{}", OccurredAt: now,
		}},
	}
	operations.restore = config.RestoreExecutionResult{
		RestoreID: restoreRequest.RestoreID, State: config.RestoreStateSucceeded,
		Stage: config.RestoreStageSucceeded, SafetyBackup: operations.backup,
		Stages: []config.RestoreStage{{
			RestoreID: restoreRequest.RestoreID, Sequence: 1, Stage: config.RestoreStageSucceeded,
			Result: config.StageResultSuccess, PublicDetailsJSON: "{}", OccurredAt: now,
		}},
		MasterPID: 100, WorkerCount: 1, HTTPStatus: 204, FinishedAt: now,
	}
	restartRequest := config.RestartExecutionRequest{
		RestartID: "55555555555555555555555555555555", ProductionDigest: config.Digest{6},
	}
	operations.restart = config.RestartExecutionResult{
		RestartID: restartRequest.RestartID, State: config.RestartStateSucceeded,
		Stage: config.RestartStageSucceeded, Stages: []config.RestartStage{{
			RestartID: restartRequest.RestartID, Sequence: 1, Stage: config.RestartStageSucceeded,
			Result: config.StageResultSuccess, PublicDetailsJSON: "{}", OccurredAt: now,
		}},
		BeforeMasterPID: 100, AfterMasterPID: 200, WorkerCount: 1, HTTPStatus: 204, FinishedAt: now,
	}
	verificationRequest := config.RuntimeVerificationRequest{
		VerificationID: "88888888888888888888888888888888", ProductionDigest: config.Digest{8},
	}
	operations.verification = config.RuntimeVerificationResult{
		VerificationID: verificationRequest.VerificationID, State: config.VerificationStateSucceeded,
		ProductionDigest: verificationRequest.ProductionDigest, MasterPID: 200, WorkerCount: 1,
		HTTPStatus: 204, CheckedAt: now,
	}
	path := startAgentClientUnixServer(t, newAgentProtocolHandler(operations, nil))
	client := newAgentClient(path)

	verified, err := client.VerifyBackup(context.Background(), "recovery-verify", operations.backup.BackupID)
	if err != nil || !reflect.DeepEqual(verified, operations.backup) {
		t.Fatalf("VerifyBackup() = %#v, %v", verified, err)
	}
	deletion := config.BackupDeletionRequest{
		RunID: "77777777777777777777777777777777", BackupID: operations.backup.BackupID,
		ProductionDigest: operations.backup.ProductionDigest, TreeDigest: operations.backup.TreeDigest,
		SnapshotCreatedAt: now.Add(-time.Hour), SnapshotTotalBytes: operations.backup.TotalBytes,
	}
	if err := client.DeleteBackup(context.Background(), "recovery-delete", deletion); err != nil ||
		!reflect.DeepEqual(operations.deletion, deletion) {
		t.Fatalf("DeleteBackup() error = %v, request = %#v", err, operations.deletion)
	}
	prepared, err := client.PrepareRestore(context.Background(), "recovery-prepare", restoreRequest)
	if err != nil || !reflect.DeepEqual(prepared, operations.preparation) {
		t.Fatalf("PrepareRestore() = %#v, %v", prepared, err)
	}
	restored, err := client.ExecuteRestore(context.Background(), "recovery-restore", restoreRequest)
	if err != nil || !reflect.DeepEqual(restored, operations.restore) {
		t.Fatalf("ExecuteRestore() = %#v, %v", restored, err)
	}
	restarted, err := client.ExecuteRestart(context.Background(), "recovery-restart", restartRequest)
	if err != nil || !reflect.DeepEqual(restarted, operations.restart) {
		t.Fatalf("ExecuteRestart() = %#v, %v", restarted, err)
	}
	verifiedRuntime, err := client.VerifyRuntime(context.Background(), "recovery-runtime", verificationRequest)
	if err != nil || !reflect.DeepEqual(verifiedRuntime, operations.verification) {
		t.Fatalf("VerifyRuntime() = %#v, %v", verifiedRuntime, err)
	}
}

type recordingRecoveryAgentOperations struct {
	*recordingAgentOperations
	backup       config.BackupEvidence
	deletion     config.BackupDeletionRequest
	preparation  config.RestorePreparationResult
	restore      config.RestoreExecutionResult
	restart      config.RestartExecutionResult
	verification config.RuntimeVerificationResult
}

func (o *recordingRecoveryAgentOperations) VerifyBackup(context.Context, config.BackupID) (config.BackupEvidence, error) {
	return o.backup, nil
}

func (o *recordingRecoveryAgentOperations) DeleteBackup(_ context.Context, request config.BackupDeletionRequest) error {
	o.deletion = request
	return nil
}

func (o *recordingRecoveryAgentOperations) PrepareRestore(_ context.Context, _ config.RestoreExecutionRequest) (config.RestorePreparationResult, error) {
	return o.preparation, nil
}

func (o *recordingRecoveryAgentOperations) ExecuteRestore(_ context.Context, _ config.RestoreExecutionRequest) (config.RestoreExecutionResult, error) {
	return o.restore, nil
}

func (o *recordingRecoveryAgentOperations) RestoreProgress(_ context.Context, _ config.RestoreExecutionRequest) (config.RestoreExecutionResult, error) {
	return o.restore, nil
}

func (o *recordingRecoveryAgentOperations) RecoverRestore(_ context.Context, _ config.RestoreExecutionRequest) (config.RestoreExecutionResult, error) {
	return o.restore, nil
}

func (o *recordingRecoveryAgentOperations) ExecuteRestart(_ context.Context, _ config.RestartExecutionRequest) (config.RestartExecutionResult, error) {
	return o.restart, nil
}

func (o *recordingRecoveryAgentOperations) RestartProgress(_ context.Context, _ config.RestartExecutionRequest) (config.RestartExecutionResult, error) {
	return o.restart, nil
}

func (o *recordingRecoveryAgentOperations) RecoverRestart(_ context.Context, _ config.RestartExecutionRequest) (config.RestartExecutionResult, error) {
	return o.restart, nil
}

func (o *recordingRecoveryAgentOperations) VerifyRuntime(_ context.Context, _ config.RuntimeVerificationRequest) (config.RuntimeVerificationResult, error) {
	return o.verification, nil
}
