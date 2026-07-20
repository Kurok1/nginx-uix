/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

type agentBackupVerifyRequest struct {
	ProtocolVersion uint16 `json:"protocol_version"`
	BackupID        string `json:"backup_id"`
}

type agentBackupDeleteRequest struct {
	ProtocolVersion    uint16    `json:"protocol_version"`
	RunID              string    `json:"run_id"`
	BackupID           string    `json:"backup_id"`
	ProductionDigest   string    `json:"production_digest"`
	TreeDigest         string    `json:"tree_digest"`
	SnapshotCreatedAt  time.Time `json:"snapshot_created_at"`
	SnapshotTotalBytes int64     `json:"snapshot_total_bytes"`
}

type agentRestoreRequest struct {
	ProtocolVersion  uint16 `json:"protocol_version"`
	RestoreID        string `json:"restore_id"`
	TargetBackupID   string `json:"target_backup_id"`
	SafetyBackupID   string `json:"safety_backup_id"`
	SourceDigest     string `json:"source_digest"`
	TargetDigest     string `json:"target_digest"`
	TargetTreeDigest string `json:"target_tree_digest"`
	SafetyTreeDigest string `json:"safety_tree_digest"`
}

type agentRestartRequest struct {
	ProtocolVersion  uint16 `json:"protocol_version"`
	RestartID        string `json:"restart_id"`
	ProductionDigest string `json:"production_digest"`
}

type agentRuntimeVerificationRequest struct {
	ProtocolVersion  uint16 `json:"protocol_version"`
	VerificationID   string `json:"verification_id"`
	ProductionDigest string `json:"production_digest"`
}

type agentRecoveryRequest struct {
	BackupID config.BackupID
	Deletion config.BackupDeletionRequest
	Restore  config.RestoreExecutionRequest
	Restart  config.RestartExecutionRequest
	Verify   config.RuntimeVerificationRequest
}

func isAgentRecoveryAction(action string) bool {
	switch action {
	case "backup_verify", "backup_delete", "restore_prepare", "restore", "restore_progress",
		"restore_recovery", "restart", "restart_progress", "restart_recovery", "runtime_verification":
		return true
	default:
		return false
	}
}

func decodeAgentRecoveryRequest(request *http.Request, action string) (agentRecoveryRequest, error) {
	switch action {
	case "backup_verify":
		var decoded agentBackupVerifyRequest
		if err := decodeAgentTypedRequest(request, &decoded); err != nil || decoded.ProtocolVersion != agentProtocolVersion {
			return agentRecoveryRequest{}, errors.New("backup verification request is invalid")
		}
		backupID, err := config.ParseBackupID(decoded.BackupID)
		if err != nil {
			return agentRecoveryRequest{}, errors.New("backup verification request fields are invalid")
		}
		return agentRecoveryRequest{BackupID: backupID}, nil
	case "backup_delete":
		var decoded agentBackupDeleteRequest
		if err := decodeAgentTypedRequest(request, &decoded); err != nil || decoded.ProtocolVersion != agentProtocolVersion {
			return agentRecoveryRequest{}, errors.New("backup deletion request is invalid")
		}
		deletion, err := backupDeletionRequestFromAgent(decoded)
		if err != nil {
			return agentRecoveryRequest{}, errors.New("backup deletion request fields are invalid")
		}
		return agentRecoveryRequest{Deletion: deletion}, nil
	case "restore_prepare", "restore", "restore_progress", "restore_recovery":
		var decoded agentRestoreRequest
		if err := decodeAgentTypedRequest(request, &decoded); err != nil || decoded.ProtocolVersion != agentProtocolVersion {
			return agentRecoveryRequest{}, errors.New("restore request is invalid")
		}
		restore, err := restoreRequestFromAgent(decoded, action != "restore_prepare")
		if err != nil {
			return agentRecoveryRequest{}, errors.New("restore request fields are invalid")
		}
		return agentRecoveryRequest{Restore: restore}, nil
	case "restart", "restart_progress", "restart_recovery":
		var decoded agentRestartRequest
		if err := decodeAgentTypedRequest(request, &decoded); err != nil || decoded.ProtocolVersion != agentProtocolVersion {
			return agentRecoveryRequest{}, errors.New("restart request is invalid")
		}
		restart, err := restartRequestFromAgent(decoded)
		if err != nil {
			return agentRecoveryRequest{}, errors.New("restart request fields are invalid")
		}
		return agentRecoveryRequest{Restart: restart}, nil
	case "runtime_verification":
		var decoded agentRuntimeVerificationRequest
		if err := decodeAgentTypedRequest(request, &decoded); err != nil || decoded.ProtocolVersion != agentProtocolVersion {
			return agentRecoveryRequest{}, errors.New("runtime verification request is invalid")
		}
		verificationID, idErr := config.ParseVerificationID(decoded.VerificationID)
		productionDigest, digestErr := config.ParseDigest(decoded.ProductionDigest)
		if idErr != nil || digestErr != nil || productionDigest == (config.Digest{}) {
			return agentRecoveryRequest{}, errors.New("runtime verification request fields are invalid")
		}
		return agentRecoveryRequest{Verify: config.RuntimeVerificationRequest{
			VerificationID: verificationID, ProductionDigest: productionDigest,
		}}, nil
	default:
		return agentRecoveryRequest{}, errors.New("recovery request action is invalid")
	}
}

func backupDeletionRequestFromAgent(decoded agentBackupDeleteRequest) (config.BackupDeletionRequest, error) {
	runID, runErr := config.ParseRetentionRunID(decoded.RunID)
	backupID, backupErr := config.ParseBackupID(decoded.BackupID)
	productionDigest, productionErr := config.ParseDigest(decoded.ProductionDigest)
	treeDigest, treeErr := config.ParseDigest(decoded.TreeDigest)
	if runErr != nil || backupErr != nil || productionErr != nil || treeErr != nil ||
		productionDigest == (config.Digest{}) || treeDigest == (config.Digest{}) ||
		decoded.SnapshotCreatedAt.IsZero() || decoded.SnapshotCreatedAt.Location() != time.UTC || decoded.SnapshotTotalBytes < 0 {
		return config.BackupDeletionRequest{}, config.ErrDigestInvalid
	}
	return config.BackupDeletionRequest{
		RunID: runID, BackupID: backupID, ProductionDigest: productionDigest, TreeDigest: treeDigest,
		SnapshotCreatedAt: decoded.SnapshotCreatedAt, SnapshotTotalBytes: decoded.SnapshotTotalBytes,
	}, nil
}

func restoreRequestFromAgent(decoded agentRestoreRequest, requireSafety bool) (config.RestoreExecutionRequest, error) {
	restoreID, restoreErr := config.ParseRestoreID(decoded.RestoreID)
	targetBackupID, targetErr := config.ParseBackupID(decoded.TargetBackupID)
	safetyBackupID, safetyErr := config.ParseBackupID(decoded.SafetyBackupID)
	sourceDigest, sourceErr := config.ParseDigest(decoded.SourceDigest)
	targetDigest, targetDigestErr := config.ParseDigest(decoded.TargetDigest)
	targetTreeDigest, targetTreeErr := config.ParseDigest(decoded.TargetTreeDigest)
	var safetyTreeDigest config.Digest
	var safetyTreeErr error
	if decoded.SafetyTreeDigest != "" {
		safetyTreeDigest, safetyTreeErr = config.ParseDigest(decoded.SafetyTreeDigest)
	}
	result := config.RestoreExecutionRequest{
		RestoreID: restoreID, TargetBackupID: targetBackupID, SafetyBackupID: safetyBackupID,
		SourceDigest: sourceDigest, TargetDigest: targetDigest, TargetTreeDigest: targetTreeDigest,
		SafetyTreeDigest: safetyTreeDigest,
	}
	if restoreErr != nil || targetErr != nil || safetyErr != nil || sourceErr != nil || targetDigestErr != nil ||
		targetTreeErr != nil || safetyTreeErr != nil || validateRestoreExecutionRequest(result, requireSafety) != nil {
		return config.RestoreExecutionRequest{}, config.ErrDigestInvalid
	}
	return result, nil
}

func restartRequestFromAgent(decoded agentRestartRequest) (config.RestartExecutionRequest, error) {
	restartID, restartErr := config.ParseRestartID(decoded.RestartID)
	productionDigest, digestErr := config.ParseDigest(decoded.ProductionDigest)
	result := config.RestartExecutionRequest{RestartID: restartID, ProductionDigest: productionDigest}
	if restartErr != nil || digestErr != nil || validateRestartExecutionRequest(result) != nil {
		return config.RestartExecutionRequest{}, config.ErrDigestInvalid
	}
	return result, nil
}

func executeAgentRecovery(
	ctx context.Context,
	operations agentRecoveryOperations,
	action string,
	request agentRecoveryRequest,
) (any, error) {
	switch action {
	case "backup_verify":
		evidence, err := operations.VerifyBackup(ctx, request.BackupID)
		if err != nil {
			return nil, err
		}
		return newAgentBackupEvidenceResponse(evidence), nil
	case "backup_delete":
		if err := operations.DeleteBackup(ctx, request.Deletion); err != nil {
			return nil, err
		}
		return agentBackupDeleteResponse{Deleted: true}, nil
	case "restore_prepare":
		result, err := operations.PrepareRestore(ctx, request.Restore)
		if result.RestoreID != "" && result.State != "" {
			return newAgentRestorePreparationResponse(result), nil
		}
		return nil, err
	case "restore", "restore_progress", "restore_recovery":
		var result config.RestoreExecutionResult
		var err error
		switch action {
		case "restore":
			result, err = operations.ExecuteRestore(ctx, request.Restore)
		case "restore_progress":
			result, err = operations.RestoreProgress(ctx, request.Restore)
		case "restore_recovery":
			result, err = operations.RecoverRestore(ctx, request.Restore)
		}
		if result.RestoreID != "" && result.State != "" {
			return newAgentRestoreResponse(result), nil
		}
		return nil, err
	case "restart", "restart_progress", "restart_recovery":
		var result config.RestartExecutionResult
		var err error
		switch action {
		case "restart":
			result, err = operations.ExecuteRestart(ctx, request.Restart)
		case "restart_progress":
			result, err = operations.RestartProgress(ctx, request.Restart)
		case "restart_recovery":
			result, err = operations.RecoverRestart(ctx, request.Restart)
		}
		if result.RestartID != "" && result.State != "" {
			return newAgentRestartResponse(result), nil
		}
		return nil, err
	case "runtime_verification":
		result, err := operations.VerifyRuntime(ctx, request.Verify)
		if result.VerificationID != "" && result.State != "" {
			return newAgentRuntimeVerificationResponse(result), nil
		}
		return nil, err
	default:
		return nil, errors.New("recovery operation is invalid")
	}
}

type agentBackupDeleteResponse struct {
	Deleted bool `json:"deleted"`
}

func newAgentBackupEvidenceResponse(evidence config.BackupEvidence) agentBackupEvidenceResponse {
	return agentBackupEvidenceResponse{
		BackupID: string(evidence.BackupID), OriginType: string(evidence.OriginType), OriginID: evidence.OriginID,
		ReleaseID: string(evidence.ReleaseID), ProductionDigest: evidence.ProductionDigest.String(),
		TreeDigest: evidence.TreeDigest.String(), EntryCount: evidence.EntryCount, TotalBytes: evidence.TotalBytes,
		VerifiedAt: evidence.VerifiedAt,
	}
}

type agentRestoreStageResponse struct {
	Sequence          uint64                  `json:"sequence"`
	Stage             config.RestoreStageName `json:"stage"`
	Result            config.StageResult      `json:"result"`
	Code              string                  `json:"code"`
	PublicDetailsJSON string                  `json:"public_details_json"`
	OccurredAt        time.Time               `json:"occurred_at"`
}

type agentRestorePreparationResponse struct {
	RestoreID    string                      `json:"restore_id"`
	State        config.RestoreState         `json:"state"`
	Stage        config.RestoreStageName     `json:"stage"`
	SafetyBackup agentBackupEvidenceResponse `json:"safety_backup"`
	Stages       []agentRestoreStageResponse `json:"stages"`
	ErrorCode    string                      `json:"error_code"`
	FinishedAt   time.Time                   `json:"finished_at"`
}

type agentRestoreResponse struct {
	RestoreID    string                      `json:"restore_id"`
	State        config.RestoreState         `json:"state"`
	Stage        config.RestoreStageName     `json:"stage"`
	SafetyBackup agentBackupEvidenceResponse `json:"safety_backup"`
	Stages       []agentRestoreStageResponse `json:"stages"`
	ErrorCode    string                      `json:"error_code"`
	MasterPID    int                         `json:"master_pid"`
	WorkerCount  int                         `json:"worker_count"`
	HTTPStatus   int                         `json:"http_status"`
	FinishedAt   time.Time                   `json:"finished_at"`
}

func newAgentRestorePreparationResponse(result config.RestorePreparationResult) agentRestorePreparationResponse {
	return agentRestorePreparationResponse{
		RestoreID: string(result.RestoreID), State: result.State, Stage: result.Stage,
		SafetyBackup: newAgentBackupEvidenceResponse(result.SafetyBackup), Stages: newAgentRestoreStageResponses(result.Stages),
		ErrorCode: result.ErrorCode, FinishedAt: result.FinishedAt,
	}
}

func newAgentRestoreResponse(result config.RestoreExecutionResult) agentRestoreResponse {
	return agentRestoreResponse{
		RestoreID: string(result.RestoreID), State: result.State, Stage: result.Stage,
		SafetyBackup: newAgentBackupEvidenceResponse(result.SafetyBackup), Stages: newAgentRestoreStageResponses(result.Stages),
		ErrorCode: result.ErrorCode, MasterPID: result.MasterPID, WorkerCount: result.WorkerCount,
		HTTPStatus: result.HTTPStatus, FinishedAt: result.FinishedAt,
	}
}

func newAgentRestoreStageResponses(stages []config.RestoreStage) []agentRestoreStageResponse {
	responses := make([]agentRestoreStageResponse, 0, len(stages))
	for _, stage := range stages {
		responses = append(responses, agentRestoreStageResponse{
			Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result, Code: stage.Code,
			PublicDetailsJSON: stage.PublicDetailsJSON, OccurredAt: stage.OccurredAt,
		})
	}
	return responses
}

type agentRestartStageResponse struct {
	Sequence          uint64                  `json:"sequence"`
	Stage             config.RestartStageName `json:"stage"`
	Result            config.StageResult      `json:"result"`
	Code              string                  `json:"code"`
	PublicDetailsJSON string                  `json:"public_details_json"`
	OccurredAt        time.Time               `json:"occurred_at"`
}

type agentRestartResponse struct {
	RestartID       string                      `json:"restart_id"`
	State           config.RestartState         `json:"state"`
	Stage           config.RestartStageName     `json:"stage"`
	Stages          []agentRestartStageResponse `json:"stages"`
	ErrorCode       string                      `json:"error_code"`
	BeforeMasterPID int                         `json:"before_master_pid"`
	AfterMasterPID  int                         `json:"after_master_pid"`
	WorkerCount     int                         `json:"worker_count"`
	HTTPStatus      int                         `json:"http_status"`
	FinishedAt      time.Time                   `json:"finished_at"`
}

type agentRuntimeVerificationResponse struct {
	VerificationID   string                   `json:"verification_id"`
	State            config.VerificationState `json:"state"`
	ProductionDigest string                   `json:"production_digest"`
	MasterPID        int                      `json:"master_pid"`
	WorkerCount      int                      `json:"worker_count"`
	HTTPStatus       int                      `json:"http_status"`
	ErrorCode        string                   `json:"error_code"`
	CheckedAt        time.Time                `json:"checked_at"`
}

func newAgentRuntimeVerificationResponse(result config.RuntimeVerificationResult) agentRuntimeVerificationResponse {
	return agentRuntimeVerificationResponse{
		VerificationID: string(result.VerificationID), State: result.State,
		ProductionDigest: result.ProductionDigest.String(), MasterPID: result.MasterPID,
		WorkerCount: result.WorkerCount, HTTPStatus: result.HTTPStatus,
		ErrorCode: result.ErrorCode, CheckedAt: result.CheckedAt,
	}
}

func newAgentRestartResponse(result config.RestartExecutionResult) agentRestartResponse {
	stages := make([]agentRestartStageResponse, 0, len(result.Stages))
	for _, stage := range result.Stages {
		stages = append(stages, agentRestartStageResponse{
			Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result, Code: stage.Code,
			PublicDetailsJSON: stage.PublicDetailsJSON, OccurredAt: stage.OccurredAt,
		})
	}
	return agentRestartResponse{
		RestartID: string(result.RestartID), State: result.State, Stage: result.Stage, Stages: stages,
		ErrorCode: result.ErrorCode, BeforeMasterPID: result.BeforeMasterPID, AfterMasterPID: result.AfterMasterPID,
		WorkerCount: result.WorkerCount, HTTPStatus: result.HTTPStatus, FinishedAt: result.FinishedAt,
	}
}

func backupEvidenceFromAgentResponse(response agentBackupEvidenceResponse, allowLegacyRelease bool) (config.BackupEvidence, error) {
	backupID, backupErr := config.ParseBackupID(response.BackupID)
	productionDigest, productionErr := config.ParseDigest(response.ProductionDigest)
	treeDigest, treeErr := config.ParseDigest(response.TreeDigest)
	evidence := config.BackupEvidence{
		BackupID: backupID, OriginType: config.BackupOriginType(response.OriginType), OriginID: response.OriginID,
		ProductionDigest: productionDigest, TreeDigest: treeDigest, EntryCount: response.EntryCount,
		TotalBytes: response.TotalBytes, VerifiedAt: response.VerifiedAt,
	}
	if response.ReleaseID != "" {
		releaseID, err := config.ParseReleaseID(response.ReleaseID)
		if err != nil {
			return config.BackupEvidence{}, errAgentInvalidResponse
		}
		evidence.ReleaseID = releaseID
	}
	if backupErr != nil || productionErr != nil || treeErr != nil || productionDigest == (config.Digest{}) ||
		treeDigest == (config.Digest{}) || response.EntryCount <= 0 || response.TotalBytes < 0 ||
		response.VerifiedAt.IsZero() || response.VerifiedAt.Location() != time.UTC {
		return config.BackupEvidence{}, errAgentInvalidResponse
	}
	switch evidence.OriginType {
	case config.BackupOriginRelease:
		originID, err := config.ParseReleaseID(evidence.OriginID)
		if err != nil || evidence.ReleaseID != originID {
			return config.BackupEvidence{}, errAgentInvalidResponse
		}
	case config.BackupOriginRestore:
		if _, err := config.ParseRestoreID(evidence.OriginID); err != nil || evidence.ReleaseID != "" {
			return config.BackupEvidence{}, errAgentInvalidResponse
		}
	case "":
		if !allowLegacyRelease || evidence.ReleaseID == "" {
			return config.BackupEvidence{}, errAgentInvalidResponse
		}
	default:
		return config.BackupEvidence{}, errAgentInvalidResponse
	}
	return evidence, nil
}

func restorePreparationFromAgentResponse(response agentRestorePreparationResponse) (config.RestorePreparationResult, error) {
	restoreID, err := config.ParseRestoreID(response.RestoreID)
	if err != nil || !validAgentRestoreStateStage(response.State, response.Stage, response.FinishedAt) {
		return config.RestorePreparationResult{}, errAgentInvalidResponse
	}
	stages, err := restoreStagesFromAgentResponse(restoreID, response.Stages)
	if err != nil {
		return config.RestorePreparationResult{}, err
	}
	result := config.RestorePreparationResult{
		RestoreID: restoreID, State: response.State, Stage: response.Stage, Stages: stages,
		ErrorCode: response.ErrorCode, FinishedAt: response.FinishedAt,
	}
	if response.SafetyBackup.BackupID != "" {
		result.SafetyBackup, err = backupEvidenceFromAgentResponse(response.SafetyBackup, false)
		if err != nil || result.SafetyBackup.OriginType != config.BackupOriginRestore ||
			result.SafetyBackup.OriginID != string(restoreID) {
			return config.RestorePreparationResult{}, errAgentInvalidResponse
		}
	}
	if response.Stage == config.RestoreStageSafetyBackupVerified && result.SafetyBackup.BackupID == "" {
		return config.RestorePreparationResult{}, errAgentInvalidResponse
	}
	return result, nil
}

func restoreFromAgentResponse(response agentRestoreResponse) (config.RestoreExecutionResult, error) {
	restoreID, err := config.ParseRestoreID(response.RestoreID)
	if err != nil || !validAgentRestoreStateStage(response.State, response.Stage, response.FinishedAt) {
		return config.RestoreExecutionResult{}, errAgentInvalidResponse
	}
	stages, err := restoreStagesFromAgentResponse(restoreID, response.Stages)
	if err != nil {
		return config.RestoreExecutionResult{}, err
	}
	result := config.RestoreExecutionResult{
		RestoreID: restoreID, State: response.State, Stage: response.Stage, Stages: stages,
		ErrorCode: response.ErrorCode, MasterPID: response.MasterPID, WorkerCount: response.WorkerCount,
		HTTPStatus: response.HTTPStatus, FinishedAt: response.FinishedAt,
	}
	if response.SafetyBackup.BackupID != "" {
		result.SafetyBackup, err = backupEvidenceFromAgentResponse(response.SafetyBackup, false)
		if err != nil || result.SafetyBackup.OriginType != config.BackupOriginRestore ||
			result.SafetyBackup.OriginID != string(restoreID) {
			return config.RestoreExecutionResult{}, errAgentInvalidResponse
		}
	}
	if terminalRuntimeRestoreState(result.State) &&
		(len(stages) == 0 || stages[len(stages)-1].Stage != result.Stage) {
		return config.RestoreExecutionResult{}, errAgentInvalidResponse
	}
	return result, nil
}

func restoreStagesFromAgentResponse(id config.RestoreID, responses []agentRestoreStageResponse) ([]config.RestoreStage, error) {
	stages := make([]config.RestoreStage, 0, len(responses))
	lastOrder := -1
	for index, response := range responses {
		order := restoreStageOrder(response.Stage)
		if response.Sequence != uint64(index+1) || order < 0 || order < lastOrder ||
			!knownAgentStageResult(response.Result) || response.OccurredAt.IsZero() ||
			response.OccurredAt.Location() != time.UTC || !json.Valid([]byte(response.PublicDetailsJSON)) {
			return nil, errAgentInvalidResponse
		}
		stages = append(stages, config.RestoreStage{
			RestoreID: id, Sequence: response.Sequence, Stage: response.Stage, Result: response.Result,
			Code: response.Code, PublicDetailsJSON: response.PublicDetailsJSON, OccurredAt: response.OccurredAt,
		})
		lastOrder = order
	}
	return stages, nil
}

func validAgentRestoreStateStage(state config.RestoreState, stage config.RestoreStageName, finishedAt time.Time) bool {
	if restoreStageOrder(stage) < 0 || restoreStateForStage(stage) != state {
		return false
	}
	terminal := terminalRuntimeRestoreState(state)
	return terminal && !finishedAt.IsZero() && finishedAt.Location() == time.UTC || !terminal && finishedAt.IsZero()
}

func restartFromAgentResponse(response agentRestartResponse) (config.RestartExecutionResult, error) {
	restartID, err := config.ParseRestartID(response.RestartID)
	if err != nil || restartStageOrder(response.Stage) < 0 || restartStateForStage(response.Stage) != response.State {
		return config.RestartExecutionResult{}, errAgentInvalidResponse
	}
	terminal := terminalRuntimeRestartState(response.State)
	if terminal && (response.FinishedAt.IsZero() || response.FinishedAt.Location() != time.UTC) ||
		!terminal && !response.FinishedAt.IsZero() {
		return config.RestartExecutionResult{}, errAgentInvalidResponse
	}
	stages := make([]config.RestartStage, 0, len(response.Stages))
	lastOrder := -1
	for index, stage := range response.Stages {
		order := restartStageOrder(stage.Stage)
		if stage.Sequence != uint64(index+1) || order < 0 || order < lastOrder ||
			!knownAgentStageResult(stage.Result) || stage.OccurredAt.IsZero() ||
			stage.OccurredAt.Location() != time.UTC || !json.Valid([]byte(stage.PublicDetailsJSON)) {
			return config.RestartExecutionResult{}, errAgentInvalidResponse
		}
		stages = append(stages, config.RestartStage{
			RestartID: restartID, Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result,
			Code: stage.Code, PublicDetailsJSON: stage.PublicDetailsJSON, OccurredAt: stage.OccurredAt,
		})
		lastOrder = order
	}
	if terminal && (len(stages) == 0 || stages[len(stages)-1].Stage != response.Stage) {
		return config.RestartExecutionResult{}, errAgentInvalidResponse
	}
	return config.RestartExecutionResult{
		RestartID: restartID, State: response.State, Stage: response.Stage, Stages: stages,
		ErrorCode: response.ErrorCode, BeforeMasterPID: response.BeforeMasterPID,
		AfterMasterPID: response.AfterMasterPID, WorkerCount: response.WorkerCount,
		HTTPStatus: response.HTTPStatus, FinishedAt: response.FinishedAt,
	}, nil
}

func runtimeVerificationFromAgentResponse(
	response agentRuntimeVerificationResponse,
) (config.RuntimeVerificationResult, error) {
	id, idErr := config.ParseVerificationID(response.VerificationID)
	digest, digestErr := config.ParseDigest(response.ProductionDigest)
	if idErr != nil || digestErr != nil || digest == (config.Digest{}) ||
		response.CheckedAt.IsZero() || response.CheckedAt.Location() != time.UTC {
		return config.RuntimeVerificationResult{}, errAgentInvalidResponse
	}
	switch response.State {
	case config.VerificationStateSucceeded:
		if response.ErrorCode != "" || response.MasterPID <= 0 || response.WorkerCount <= 0 ||
			response.HTTPStatus < 200 || response.HTTPStatus >= 300 {
			return config.RuntimeVerificationResult{}, errAgentInvalidResponse
		}
	case config.VerificationStateFailed:
		if response.ErrorCode == "" || response.MasterPID < 0 || response.WorkerCount < 0 ||
			response.HTTPStatus < 0 || response.HTTPStatus > 599 {
			return config.RuntimeVerificationResult{}, errAgentInvalidResponse
		}
	default:
		return config.RuntimeVerificationResult{}, errAgentInvalidResponse
	}
	return config.RuntimeVerificationResult{
		VerificationID: id, State: response.State, ProductionDigest: digest,
		MasterPID: response.MasterPID, WorkerCount: response.WorkerCount,
		HTTPStatus: response.HTTPStatus, ErrorCode: response.ErrorCode, CheckedAt: response.CheckedAt,
	}, nil
}
