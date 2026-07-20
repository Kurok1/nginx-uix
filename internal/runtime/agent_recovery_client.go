/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	agentBackupTimeout  = 2 * time.Minute
	agentRestoreTimeout = 5 * time.Minute
	agentRestartTimeout = time.Minute
)

// VerifyBackup asks the Agent to validate one opaque backup beneath its fixed backup root.
func (c *AgentClient) VerifyBackup(
	ctx context.Context,
	requestID string,
	id config.BackupID,
) (config.BackupEvidence, error) {
	if !validAgentRequestID(requestID) {
		return config.BackupEvidence{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	if _, err := config.ParseBackupID(string(id)); err != nil {
		return config.BackupEvidence{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := marshalAgentRecoveryRequest(agentBackupVerifyRequest{
		ProtocolVersion: agentProtocolVersion, BackupID: string(id),
	})
	if err != nil {
		return config.BackupEvidence{}, err
	}
	var response agentBackupEvidenceResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, agentProtocolBackupVerifyPath, payload, agentBackupTimeout, &response); err != nil {
		return config.BackupEvidence{}, fmt.Errorf("verify agent backup: %w", err)
	}
	evidence, err := backupEvidenceFromAgentResponse(response, false)
	if err != nil || evidence.BackupID != id {
		return config.BackupEvidence{}, fmt.Errorf("verify agent backup: %w", newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return evidence, nil
}

// DeleteBackup asks the Agent to remove one exact, already-authorized fixed-root backup body.
func (c *AgentClient) DeleteBackup(
	ctx context.Context,
	requestID string,
	request config.BackupDeletionRequest,
) error {
	if !validAgentRequestID(requestID) || validateBackupDeletionClientRequest(request) != nil {
		return newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := marshalAgentRecoveryRequest(agentBackupDeleteRequest{
		ProtocolVersion: agentProtocolVersion, RunID: string(request.RunID), BackupID: string(request.BackupID),
		ProductionDigest: request.ProductionDigest.String(), TreeDigest: request.TreeDigest.String(),
		SnapshotCreatedAt: request.SnapshotCreatedAt, SnapshotTotalBytes: request.SnapshotTotalBytes,
	})
	if err != nil {
		return err
	}
	var response agentBackupDeleteResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, agentProtocolBackupDeletePath, payload, agentBackupTimeout, &response); err != nil {
		return fmt.Errorf("delete agent backup: %w", err)
	}
	if !response.Deleted {
		return fmt.Errorf("delete agent backup: %w", newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return nil
}

// PrepareRestore verifies the target and creates a safety backup without modifying production.
func (c *AgentClient) PrepareRestore(
	ctx context.Context,
	requestID string,
	request config.RestoreExecutionRequest,
) (config.RestorePreparationResult, error) {
	if !validAgentRequestID(requestID) || validateRestoreExecutionRequest(request, false) != nil {
		return config.RestorePreparationResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := marshalAgentRecoveryRequest(newAgentRestoreRequest(request))
	if err != nil {
		return config.RestorePreparationResult{}, err
	}
	var response agentRestorePreparationResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, agentProtocolRestorePreparePath, payload, agentRestoreTimeout, &response); err != nil {
		return config.RestorePreparationResult{}, fmt.Errorf("prepare agent restore: %w", err)
	}
	result, err := restorePreparationFromAgentResponse(response)
	if err != nil || result.RestoreID != request.RestoreID ||
		(result.SafetyBackup.BackupID != "" && result.SafetyBackup.BackupID != request.SafetyBackupID) {
		return config.RestorePreparationResult{}, fmt.Errorf("prepare agent restore: %w", newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return result, nil
}

// ExecuteRestore applies one previously prepared restore transaction.
func (c *AgentClient) ExecuteRestore(
	ctx context.Context,
	requestID string,
	request config.RestoreExecutionRequest,
) (config.RestoreExecutionResult, error) {
	return c.executeRestoreOperation(ctx, requestID, agentProtocolRestorePath, request, "execute", agentRestoreTimeout)
}

// RestoreProgress reads durable Agent restore evidence.
func (c *AgentClient) RestoreProgress(
	ctx context.Context,
	requestID string,
	request config.RestoreExecutionRequest,
) (config.RestoreExecutionResult, error) {
	return c.executeRestoreOperation(ctx, requestID, agentProtocolRestoreProgressPath, request, "read progress", agentClientRequestTimeout)
}

// RecoverRestore reconciles one interrupted Agent restore transaction.
func (c *AgentClient) RecoverRestore(
	ctx context.Context,
	requestID string,
	request config.RestoreExecutionRequest,
) (config.RestoreExecutionResult, error) {
	return c.executeRestoreOperation(ctx, requestID, agentProtocolRestoreRecoveryPath, request, "recover", agentRestoreTimeout)
}

func (c *AgentClient) executeRestoreOperation(
	ctx context.Context,
	requestID string,
	path string,
	request config.RestoreExecutionRequest,
	action string,
	timeout time.Duration,
) (config.RestoreExecutionResult, error) {
	if !validAgentRequestID(requestID) || validateRestoreExecutionRequest(request, true) != nil {
		return config.RestoreExecutionResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := marshalAgentRecoveryRequest(newAgentRestoreRequest(request))
	if err != nil {
		return config.RestoreExecutionResult{}, err
	}
	var response agentRestoreResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, path, payload, timeout, &response); err != nil {
		return config.RestoreExecutionResult{}, fmt.Errorf("%s agent restore: %w", action, err)
	}
	result, err := restoreFromAgentResponse(response)
	if err != nil || result.RestoreID != request.RestoreID ||
		(result.SafetyBackup.BackupID != "" && result.SafetyBackup.BackupID != request.SafetyBackupID) {
		return config.RestoreExecutionResult{}, fmt.Errorf("%s agent restore: %w", action, newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return result, nil
}

// ExecuteRestart invokes only the Agent's fixed supervisor restart operation.
func (c *AgentClient) ExecuteRestart(
	ctx context.Context,
	requestID string,
	request config.RestartExecutionRequest,
) (config.RestartExecutionResult, error) {
	return c.executeRestartOperation(ctx, requestID, agentProtocolRestartPath, request, "execute", agentRestartTimeout)
}

// RestartProgress reads durable Agent restart evidence.
func (c *AgentClient) RestartProgress(
	ctx context.Context,
	requestID string,
	request config.RestartExecutionRequest,
) (config.RestartExecutionResult, error) {
	return c.executeRestartOperation(ctx, requestID, agentProtocolRestartProgressPath, request, "read progress", agentClientRequestTimeout)
}

// RecoverRestart reconciles one interrupted Agent restart transaction.
func (c *AgentClient) RecoverRestart(
	ctx context.Context,
	requestID string,
	request config.RestartExecutionRequest,
) (config.RestartExecutionResult, error) {
	return c.executeRestartOperation(ctx, requestID, agentProtocolRestartRecoveryPath, request, "recover", agentRestartTimeout)
}

func (c *AgentClient) executeRestartOperation(
	ctx context.Context,
	requestID string,
	path string,
	request config.RestartExecutionRequest,
	action string,
	timeout time.Duration,
) (config.RestartExecutionResult, error) {
	if !validAgentRequestID(requestID) || validateRestartExecutionRequest(request) != nil {
		return config.RestartExecutionResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := marshalAgentRecoveryRequest(agentRestartRequest{
		ProtocolVersion: agentProtocolVersion, RestartID: string(request.RestartID),
		ProductionDigest: request.ProductionDigest.String(),
	})
	if err != nil {
		return config.RestartExecutionResult{}, err
	}
	var response agentRestartResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, path, payload, timeout, &response); err != nil {
		return config.RestartExecutionResult{}, fmt.Errorf("%s agent restart: %w", action, err)
	}
	result, err := restartFromAgentResponse(response)
	if err != nil || result.RestartID != request.RestartID {
		return config.RestartExecutionResult{}, fmt.Errorf("%s agent restart: %w", action, newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return result, nil
}

// VerifyRuntime asks the Agent to perform the fixed, read-only current-production health proof.
func (c *AgentClient) VerifyRuntime(
	ctx context.Context,
	requestID string,
	request config.RuntimeVerificationRequest,
) (config.RuntimeVerificationResult, error) {
	if !validAgentRequestID(requestID) {
		return config.RuntimeVerificationResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	if _, err := config.ParseVerificationID(string(request.VerificationID)); err != nil ||
		request.ProductionDigest == (config.Digest{}) {
		return config.RuntimeVerificationResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := marshalAgentRecoveryRequest(agentRuntimeVerificationRequest{
		ProtocolVersion: agentProtocolVersion, VerificationID: string(request.VerificationID),
		ProductionDigest: request.ProductionDigest.String(),
	})
	if err != nil {
		return config.RuntimeVerificationResult{}, err
	}
	var response agentRuntimeVerificationResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, agentProtocolRuntimeVerificationPath,
		payload, runtimeVerificationTimeout, &response); err != nil {
		return config.RuntimeVerificationResult{}, fmt.Errorf("verify agent runtime: %w", err)
	}
	result, err := runtimeVerificationFromAgentResponse(response)
	if err != nil || result.VerificationID != request.VerificationID ||
		result.ProductionDigest != request.ProductionDigest {
		return config.RuntimeVerificationResult{}, fmt.Errorf("verify agent runtime: %w",
			newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return result, nil
}

func validateBackupDeletionClientRequest(request config.BackupDeletionRequest) error {
	if _, err := config.ParseRetentionRunID(string(request.RunID)); err != nil {
		return err
	}
	if _, err := config.ParseBackupID(string(request.BackupID)); err != nil {
		return err
	}
	if request.ProductionDigest == (config.Digest{}) || request.TreeDigest == (config.Digest{}) ||
		request.SnapshotCreatedAt.IsZero() || request.SnapshotCreatedAt.Location() != time.UTC ||
		request.SnapshotTotalBytes < 0 {
		return config.ErrDigestInvalid
	}
	return nil
}

func newAgentRestoreRequest(request config.RestoreExecutionRequest) agentRestoreRequest {
	safetyTreeDigest := ""
	if request.SafetyTreeDigest != (config.Digest{}) {
		safetyTreeDigest = request.SafetyTreeDigest.String()
	}
	return agentRestoreRequest{
		ProtocolVersion: agentProtocolVersion, RestoreID: string(request.RestoreID),
		TargetBackupID: string(request.TargetBackupID), SafetyBackupID: string(request.SafetyBackupID),
		SourceDigest: request.SourceDigest.String(), TargetDigest: request.TargetDigest.String(),
		TargetTreeDigest: request.TargetTreeDigest.String(), SafetyTreeDigest: safetyTreeDigest,
	}
}

func marshalAgentRecoveryRequest(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, newAgentClientProtocolError(agentErrorCodeInternal)
	}
	return append(payload, '\n'), nil
}
