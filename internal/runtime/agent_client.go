/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	agentClientBaseURL          = "http://nginx-uix-agent"
	agentClientRequestTimeout   = 30 * time.Second
	agentClientDialTimeout      = 10 * time.Second
	agentClientTransportTimeout = 5 * time.Minute
	agentReleaseTimeout         = 5 * time.Minute
)

var errAgentInvalidResponse = errors.New("invalid agent response")

// AgentClient performs the fixed read-only operations exposed by the local Agent.
type AgentClient struct {
	socketPath string
	httpClient *http.Client
}

// NewAgentClient creates the production client for the fixed local Agent socket.
func NewAgentClient() *AgentClient {
	return newAgentClient(agentSocketPath)
}

func newAgentClient(socketPath string) *AgentClient {
	dialer := &net.Dialer{Timeout: agentClientDialTimeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression:     true,
		MaxIdleConns:           1,
		MaxIdleConnsPerHost:    1,
		IdleConnTimeout:        agentClientTransportTimeout,
		ResponseHeaderTimeout:  agentClientTransportTimeout,
		MaxResponseHeaderBytes: agentMaxHeaderBytes,
		ForceAttemptHTTP2:      false,
	}
	return &AgentClient{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Health reports whether the local Agent can accept work.
func (c *AgentClient) Health(ctx context.Context) error {
	var response agentHealthResponse
	if err := c.get(ctx, agentProtocolHealthPath, &response); err != nil {
		return fmt.Errorf("get agent health: %w", err)
	}
	if response.Status != "healthy" {
		return fmt.Errorf("get agent health: %w", newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return nil
}

// Status returns one bounded Nginx runtime snapshot from the local Agent.
func (c *AgentClient) Status(ctx context.Context) (Status, error) {
	var response agentStatusResponse
	if err := c.get(ctx, agentProtocolStatusPath, &response); err != nil {
		return Status{}, fmt.Errorf("get agent status: %w", err)
	}
	return statusFromAgentResponse(response), nil
}

// BuildInfo returns the fixed Nginx build metadata from the local Agent.
func (c *AgentClient) BuildInfo(ctx context.Context) (BuildInfo, error) {
	var response agentBuildInfoResponse
	if err := c.get(ctx, agentProtocolBuildInfoPath, &response); err != nil {
		return BuildInfo{}, fmt.Errorf("get agent build information: %w", err)
	}
	return buildInfoFromAgentResponse(response), nil
}

// StartupValidation returns the persisted startup and recovery state from the local Agent.
func (c *AgentClient) StartupValidation(ctx context.Context) (StartupState, error) {
	var response agentStartupStateResponse
	if err := c.get(ctx, agentProtocolStartupValidationPath, &response); err != nil {
		return StartupState{}, fmt.Errorf("get agent startup validation: %w", err)
	}
	return startupStateFromAgentResponse(response), nil
}

// EffectiveConfig returns the ordered effective Nginx configuration from the local Agent.
func (c *AgentClient) EffectiveConfig(ctx context.Context) (EffectiveConfig, error) {
	var response agentEffectiveConfigResponse
	if err := c.get(ctx, agentProtocolEffectiveConfigPath, &response); err != nil {
		return EffectiveConfig{}, fmt.Errorf("get agent effective configuration: %w", err)
	}
	configuration, err := effectiveConfigFromAgentResponse(response)
	if err != nil {
		return EffectiveConfig{}, newAgentClientProtocolError(agentErrorCodeInternal)
	}
	return configuration, nil
}

// ConfigSnapshot asks the Agent to snapshot one opaque ID beneath its fixed workspace root.
func (c *AgentClient) ConfigSnapshot(ctx context.Context, requestID string, id config.WorkspaceID) (config.Snapshot, error) {
	if !validAgentRequestID(requestID) {
		return config.Snapshot{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	parsedID, err := config.ParseWorkspaceID(string(id))
	if err != nil || parsedID != id {
		return config.Snapshot{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := json.Marshal(agentConfigSnapshotRequest{ProtocolVersion: agentProtocolVersion, WorkspaceID: string(id)})
	if err != nil {
		return config.Snapshot{}, newAgentClientProtocolError(agentErrorCodeInternal)
	}
	payload = append(payload, '\n')
	var response agentConfigSnapshotResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, agentProtocolConfigSnapshotPath, payload, agentSnapshotTimeout, &response); err != nil {
		return config.Snapshot{}, fmt.Errorf("get agent config snapshot: %w", err)
	}
	snapshot, err := configSnapshotFromAgentResponse(response)
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("get agent config snapshot: %w", newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return snapshot, nil
}

// ConfigDigest asks the Agent to digest only its fixed production root.
func (c *AgentClient) ConfigDigest(ctx context.Context, requestID string) (config.ProductionState, error) {
	if !validAgentRequestID(requestID) {
		return config.ProductionState{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	var response agentConfigDigestResponse
	if err := c.doJSON(ctx, requestID, http.MethodGet, agentProtocolConfigDigestPath, nil, agentDigestTimeout, &response); err != nil {
		return config.ProductionState{}, fmt.Errorf("get agent config digest: %w", err)
	}
	state, err := configDigestFromAgentResponse(response)
	if err != nil {
		return config.ProductionState{}, fmt.Errorf("get agent config digest: %w", newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return state, nil
}

// ValidateCandidate asks the Agent to validate exact fixed-root filesystem identities.
func (c *AgentClient) ValidateCandidate(ctx context.Context, requestID string, request config.CandidateValidationRequest) (config.CandidateValidation, error) {
	if !validAgentRequestID(requestID) {
		return config.CandidateValidation{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	if err := validateCandidateClientRequest(request); err != nil {
		return config.CandidateValidation{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := json.Marshal(agentCandidateValidationRequest{
		ProtocolVersion: agentProtocolVersion, WorkspaceID: string(request.WorkspaceID),
		ProductionDigest: request.ProductionDigest.String(), DraftDigest: request.DraftDigest.String(),
	})
	if err != nil {
		return config.CandidateValidation{}, newAgentClientProtocolError(agentErrorCodeInternal)
	}
	var response agentCandidateValidationResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, agentProtocolCandidateValidationPath, append(payload, '\n'), candidateValidationTimeout, &response); err != nil {
		return config.CandidateValidation{}, fmt.Errorf("validate agent candidate: %w", err)
	}
	validation, err := candidateValidationFromAgentResponse(response)
	if err != nil {
		return config.CandidateValidation{}, fmt.Errorf("validate agent candidate: %w", newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return validation, nil
}

// ExecuteRelease asks the Agent to run one globally serialized durable transaction.
func (c *AgentClient) ExecuteRelease(ctx context.Context, requestID string, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	return c.executeReleaseOperation(ctx, requestID, agentProtocolReleasePath, request, "execute", agentReleaseTimeout)
}

// ReleaseProgress reads the Agent's durable stages while a transaction remains in progress.
func (c *AgentClient) ReleaseProgress(ctx context.Context, requestID string, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	return c.executeReleaseOperation(ctx, requestID, agentProtocolReleaseProgressPath, request, "read progress", agentClientRequestTimeout)
}

// RecoverRelease asks the Agent to reconcile one interrupted durable transaction.
func (c *AgentClient) RecoverRelease(ctx context.Context, requestID string, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	return c.executeReleaseOperation(ctx, requestID, agentProtocolReleaseRecoveryPath, request, "recover", agentReleaseTimeout)
}

func (c *AgentClient) executeReleaseOperation(
	ctx context.Context,
	requestID string,
	path string,
	request config.ReleaseExecutionRequest,
	action string,
	timeout time.Duration,
) (config.ReleaseExecutionResult, error) {
	if !validAgentRequestID(requestID) {
		return config.ReleaseExecutionResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	if err := validateReleaseExecutionRequest(request); err != nil {
		return config.ReleaseExecutionResult{}, newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	payload, err := json.Marshal(agentReleaseRequest{
		ProtocolVersion: agentProtocolVersion, ReleaseID: string(request.ReleaseID), BackupID: string(request.BackupID),
		WorkspaceID: string(request.WorkspaceID), ProductionDigest: request.ProductionDigest.String(),
		DraftDigest: request.DraftDigest.String(), CandidateDigest: request.CandidateDigest.String(),
	})
	if err != nil {
		return config.ReleaseExecutionResult{}, newAgentClientProtocolError(agentErrorCodeInternal)
	}
	var response agentReleaseResponse
	if err := c.doJSON(ctx, requestID, http.MethodPost, path, append(payload, '\n'), timeout, &response); err != nil {
		return config.ReleaseExecutionResult{}, fmt.Errorf("%s agent release: %w", action, err)
	}
	result, err := releaseFromAgentResponse(response)
	if err != nil || result.ReleaseID != request.ReleaseID ||
		(result.Backup.BackupID != "" && (result.Backup.BackupID != request.BackupID || result.Backup.ProductionDigest != request.ProductionDigest)) {
		return config.ReleaseExecutionResult{}, fmt.Errorf("%s agent release: %w", action, newAgentClientProtocolError(agentErrorCodeInternal))
	}
	return result, nil
}

func validateCandidateClientRequest(request config.CandidateValidationRequest) error {
	if _, err := config.ParseWorkspaceID(string(request.WorkspaceID)); err != nil {
		return err
	}
	if request.ProductionDigest == (config.Digest{}) || request.DraftDigest == (config.Digest{}) {
		return config.ErrDigestInvalid
	}
	return nil
}

func (c *AgentClient) get(ctx context.Context, path string, target any) error {
	return c.doJSON(ctx, "", http.MethodGet, path, nil, agentClientRequestTimeout, target)
}

func (c *AgentClient) doJSON(
	ctx context.Context,
	requestID string,
	method string,
	path string,
	body []byte,
	timeout time.Duration,
	target any,
) error {
	if ctx == nil {
		return newAgentClientProtocolError(agentErrorCodeInternal)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.httpClient == nil {
		return newAgentClientProtocolError(agentErrorCodeInternal)
	}
	if timeout <= 0 || (requestID != "" && !validAgentRequestID(requestID)) {
		return newAgentClientProtocolError(agentErrorCodeInvalidRequest)
	}
	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(operationCtx, method, agentClientBaseURL+path, reader)
	if err != nil {
		return newAgentClientProtocolError(agentErrorCodeInternal)
	}
	request.Header.Set("Accept", agentProtocolContentType)
	if body != nil {
		request.Header.Set("Content-Type", agentProtocolContentType)
	}
	if requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return classifyAgentClientIOError(operationCtx, err)
	}
	payload, err := readAgentClientResponse(response)
	if ctxErr := operationCtx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		if errors.Is(err, errAgentResponseTooLarge) {
			return newAgentClientProtocolError(agentErrorCodeResponseTooLarge)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return newAgentClientProtocolError(agentErrorCodeInternal)
	}
	if response.StatusCode != http.StatusOK {
		var envelope agentErrorEnvelope
		if decodeAgentClientJSON(payload, &envelope) != nil || envelope.Error.Code == "" || envelope.Error.Message == "" {
			return newAgentClientProtocolError(agentErrorCodeInternal)
		}
		return newAgentClientProtocolError(envelope.Error.Code)
	}
	if decodeAgentClientJSON(payload, target) != nil {
		return newAgentClientProtocolError(agentErrorCodeInternal)
	}
	return nil
}

func readAgentClientResponse(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, errAgentInvalidResponse
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != agentProtocolContentType {
		if closeErr := response.Body.Close(); closeErr != nil {
			return nil, errors.Join(errAgentInvalidResponse, closeErr)
		}
		return nil, errAgentInvalidResponse
	}
	if response.ContentLength > agentProtocolResponseLimit {
		if closeErr := response.Body.Close(); closeErr != nil {
			return nil, errors.Join(errAgentResponseTooLarge, closeErr)
		}
		return nil, errAgentResponseTooLarge
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, agentProtocolResponseLimit+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(payload) > agentProtocolResponseLimit {
		return nil, errAgentResponseTooLarge
	}
	return payload, nil
}

func decodeAgentClientJSON(payload []byte, target any) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errAgentInvalidResponse
	}
	if rejectDuplicateAgentJSONFields(payload) != nil {
		return errAgentInvalidResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errAgentInvalidResponse
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errAgentInvalidResponse
	}
	return nil
}

func rejectDuplicateAgentJSONFields(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := consumeAgentJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errAgentInvalidResponse
	}
	return nil
}

func consumeAgentJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			rawKey, err := decoder.Token()
			key, ok := rawKey.(string)
			if err != nil || !ok {
				return errAgentInvalidResponse
			}
			if _, duplicate := seen[key]; duplicate {
				return errAgentInvalidResponse
			}
			seen[key] = struct{}{}
			if err := consumeAgentJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errAgentInvalidResponse
		}
	case '[':
		for decoder.More() {
			if err := consumeAgentJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errAgentInvalidResponse
		}
	default:
		return errAgentInvalidResponse
	}
	return nil
}

func classifyAgentClientIOError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return newAgentClientProtocolError(agentErrorCodeInternal)
}

func newAgentClientProtocolError(code string) *AgentProtocolError {
	if routeError := agentRouteProtocolError(code); routeError != nil {
		return routeError
	}
	switch code {
	case agentErrorCodeInvalidRequest:
		return &AgentProtocolError{Code: code, Message: "agent request is invalid"}
	case agentErrorCodeMethodNotAllowed:
		return &AgentProtocolError{Code: code, Message: "agent method not allowed"}
	case agentErrorCodeNotFound:
		return &AgentProtocolError{Code: code, Message: "agent endpoint not found"}
	case agentErrorCodeResponseTooLarge:
		return &AgentProtocolError{Code: code, Message: "agent response exceeded limit", cause: errAgentResponseTooLarge}
	case agentErrorCodeConfigInvalid:
		return &AgentProtocolError{Code: code, Message: "nginx configuration is invalid", cause: ErrConfigInvalid}
	case agentErrorCodeCommandTimeout:
		return &AgentProtocolError{Code: code, Message: "nginx operation timed out", cause: ErrCommandTimeout}
	case agentErrorCodeCommandOutputLarge:
		return &AgentProtocolError{Code: code, Message: "nginx output exceeded limit", cause: ErrOutputTooLarge}
	case agentErrorCodeConfigPathInvalid:
		return &AgentProtocolError{Code: code, Message: "configuration path is invalid", cause: config.ErrPathInvalid}
	case agentErrorCodeConfigLimitExceeded:
		return &AgentProtocolError{Code: code, Message: "configuration limit exceeded", cause: config.ErrLimitExceeded}
	case agentErrorCodeConfigSnapshotChanged:
		return &AgentProtocolError{Code: code, Message: "configuration changed during snapshot", cause: config.ErrSnapshotChanged}
	case agentErrorCodeConfigOperationTimeout:
		return &AgentProtocolError{Code: code, Message: "configuration operation timed out", cause: context.DeadlineExceeded}
	case agentErrorCodeObjectNotFound:
		return &AgentProtocolError{Code: code, Message: "configuration object was not found", cause: fs.ErrNotExist}
	case agentErrorCodeInternal:
		return &AgentProtocolError{Code: code, Message: "agent operation failed"}
	default:
		return &AgentProtocolError{Code: agentErrorCodeInternal, Message: "agent operation failed"}
	}
}

func configSnapshotFromAgentResponse(response agentConfigSnapshotResponse) (config.Snapshot, error) {
	if !response.BaseComplete {
		return config.Snapshot{}, errAgentInvalidResponse
	}
	manifest, err := config.ParseManifest(response.Manifest, config.DefaultLimits())
	if err != nil || response.ManifestVersion != manifest.SchemaVersion || response.EntryCount != manifest.EntryCount ||
		response.ManagedBytes != manifest.ManagedBytes {
		return config.Snapshot{}, errAgentInvalidResponse
	}
	productionDigest, err := config.ParseDigest(response.ProductionDigest)
	if err != nil {
		return config.Snapshot{}, errAgentInvalidResponse
	}
	baseDigest, err := config.ParseDigest(response.BaseDigest)
	if err != nil {
		return config.Snapshot{}, errAgentInvalidResponse
	}
	manifestDigest := manifest.Digest()
	if manifestDigest == (config.Digest{}) || productionDigest != manifestDigest || baseDigest != manifestDigest {
		return config.Snapshot{}, errAgentInvalidResponse
	}
	return config.Snapshot{Manifest: manifest, ProductionDigest: productionDigest, BaseDigest: baseDigest}, nil
}

func configDigestFromAgentResponse(response agentConfigDigestResponse) (config.ProductionState, error) {
	digest, err := config.ParseDigest(response.ProductionDigest)
	limits := config.DefaultLimits()
	if err != nil || digest == (config.Digest{}) || response.ManifestVersion != config.ManifestSchemaVersion ||
		response.EntryCount <= 0 || response.EntryCount > limits.MaxEntries ||
		response.ManagedBytes < 0 || response.ManagedBytes > limits.MaxManagedBytes {
		return config.ProductionState{}, errAgentInvalidResponse
	}
	return config.ProductionState{
		Digest: digest, ManifestVersion: response.ManifestVersion,
		EntryCount: response.EntryCount, ManagedBytes: response.ManagedBytes,
	}, nil
}

func candidateValidationFromAgentResponse(response agentCandidateValidationResponse) (config.CandidateValidation, error) {
	digest, err := config.ParseDigest(response.CandidateDigest)
	if err != nil || digest == (config.Digest{}) || response.ValidatorVersion != candidateValidatorVersion || response.ValidatorBuildID == "" || response.CheckedAt.IsZero() || response.CheckedAt.Location() != time.UTC {
		return config.CandidateValidation{}, errAgentInvalidResponse
	}
	diagnostics := make([]config.CandidateDiagnostic, 0, len(response.Diagnostics))
	for _, diagnostic := range response.Diagnostics {
		var path config.RelativePath
		if diagnostic.Path != "" {
			path, err = config.ParseRelativePath(diagnostic.Path, config.DefaultLimits())
			if err != nil {
				return config.CandidateValidation{}, errAgentInvalidResponse
			}
		}
		if diagnostic.Code == "" || diagnostic.Summary == "" || diagnostic.Line < 0 {
			return config.CandidateValidation{}, errAgentInvalidResponse
		}
		diagnostics = append(diagnostics, config.CandidateDiagnostic{Code: diagnostic.Code, Path: path, Line: diagnostic.Line, Summary: diagnostic.Summary})
	}
	if response.Valid && len(diagnostics) != 0 || !response.Valid && len(diagnostics) == 0 {
		return config.CandidateValidation{}, errAgentInvalidResponse
	}
	return config.CandidateValidation{
		Valid: response.Valid, CandidateDigest: digest, ValidatorVersion: response.ValidatorVersion,
		ValidatorBuildID: response.ValidatorBuildID, CheckedAt: response.CheckedAt, Diagnostics: diagnostics,
	}, nil
}

func releaseFromAgentResponse(response agentReleaseResponse) (config.ReleaseExecutionResult, error) {
	releaseID, err := config.ParseReleaseID(response.ReleaseID)
	terminal := terminalAgentReleaseState(response.State)
	if err != nil || !knownAgentReleaseState(response.State) || !knownReleaseStage(response.Stage) ||
		(response.State != config.ReleaseStateCancelled && releaseStateForJournalStage(response.Stage) != response.State) ||
		(terminal && (response.FinishedAt.IsZero() || response.FinishedAt.Location() != time.UTC)) ||
		(!terminal && !response.FinishedAt.IsZero()) {
		return config.ReleaseExecutionResult{}, errAgentInvalidResponse
	}
	result := config.ReleaseExecutionResult{
		ReleaseID: releaseID, State: response.State, Stage: response.Stage, ErrorCode: response.ErrorCode,
		MasterPID: response.MasterPID, WorkerCount: response.WorkerCount, HTTPStatus: response.HTTPStatus,
		FinishedAt: response.FinishedAt, Stages: make([]config.ReleaseStage, 0, len(response.Stages)),
	}
	if response.Backup.BackupID != "" {
		backupID, backupErr := config.ParseBackupID(response.Backup.BackupID)
		backupReleaseID, releaseErr := config.ParseReleaseID(response.Backup.ReleaseID)
		productionDigest, productionErr := config.ParseDigest(response.Backup.ProductionDigest)
		treeDigest, treeErr := config.ParseDigest(response.Backup.TreeDigest)
		if backupErr != nil || releaseErr != nil || backupReleaseID != releaseID || productionErr != nil || treeErr != nil ||
			productionDigest == (config.Digest{}) || treeDigest == (config.Digest{}) || response.Backup.EntryCount <= 0 ||
			response.Backup.TotalBytes < 0 || response.Backup.VerifiedAt.IsZero() || response.Backup.VerifiedAt.Location() != time.UTC {
			return config.ReleaseExecutionResult{}, errAgentInvalidResponse
		}
		result.Backup = config.BackupEvidence{
			BackupID: backupID, OriginType: config.BackupOriginType(response.Backup.OriginType),
			OriginID: response.Backup.OriginID, ReleaseID: backupReleaseID,
			ProductionDigest: productionDigest, TreeDigest: treeDigest,
			EntryCount: response.Backup.EntryCount, TotalBytes: response.Backup.TotalBytes, VerifiedAt: response.Backup.VerifiedAt,
		}
	}
	for index, stage := range response.Stages {
		if stage.Sequence != uint64(index+1) || !knownReleaseStage(stage.Stage) || !knownAgentStageResult(stage.Result) ||
			stage.OccurredAt.IsZero() || stage.OccurredAt.Location() != time.UTC || !json.Valid([]byte(stage.PublicDetailsJSON)) {
			return config.ReleaseExecutionResult{}, errAgentInvalidResponse
		}
		result.Stages = append(result.Stages, config.ReleaseStage{
			ReleaseID: releaseID, Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result,
			Code: stage.Code, PublicDetailsJSON: stage.PublicDetailsJSON, OccurredAt: stage.OccurredAt,
		})
	}
	if terminal && (len(result.Stages) == 0 || result.Stages[len(result.Stages)-1].Stage != result.Stage) {
		return config.ReleaseExecutionResult{}, errAgentInvalidResponse
	}
	if terminalAgentResultRequiresBackup(result) && result.Backup.BackupID == "" {
		return config.ReleaseExecutionResult{}, errAgentInvalidResponse
	}
	return result, nil
}

func terminalAgentResultRequiresBackup(result config.ReleaseExecutionResult) bool {
	if !terminalAgentReleaseState(result.State) {
		return false
	}
	switch result.State {
	case config.ReleaseStateSucceeded, config.ReleaseStateRolledBack, config.ReleaseStateNeedsAttention:
		return true
	case config.ReleaseStateQueued, config.ReleaseStateRunning, config.ReleaseStateRollingBack,
		config.ReleaseStateFailed, config.ReleaseStateCancelled:
	}
	for _, stage := range result.Stages {
		if stage.Stage == config.ReleaseStageBackupVerified {
			return true
		}
	}
	return false
}

func knownAgentReleaseState(state config.ReleaseState) bool {
	switch state {
	case config.ReleaseStateQueued, config.ReleaseStateRunning, config.ReleaseStateRollingBack,
		config.ReleaseStateSucceeded, config.ReleaseStateFailed, config.ReleaseStateRolledBack,
		config.ReleaseStateNeedsAttention, config.ReleaseStateCancelled:
		return true
	}
	return false
}

func knownAgentStageResult(result config.StageResult) bool {
	switch result {
	case config.StageResultPending, config.StageResultRunning, config.StageResultSuccess,
		config.StageResultFailed, config.StageResultWarning:
		return true
	}
	return false
}

func buildInfoFromAgentResponse(response agentBuildInfoResponse) BuildInfo {
	return BuildInfo{
		Version:            response.Version,
		ConfigureArguments: slices.Clone(response.ConfigureArguments),
		PIDPath:            response.PIDPath,
		SbinPath:           response.SbinPath,
	}
}

func startupValidationFromAgentResponse(response *agentStartupValidationResponse) *StartupValidation {
	if response == nil {
		return nil
	}
	return &StartupValidation{
		Valid: response.Valid, CheckedAt: response.CheckedAt, ExitCode: response.ExitCode,
		Diagnostic: response.Diagnostic,
	}
}

func startupStateFromAgentResponse(response agentStartupStateResponse) StartupState {
	return StartupState{
		Validation: startupValidationFromAgentResponse(response.Validation),
		Recovery:   response.Recovery,
	}
}

func statusFromAgentResponse(response agentStatusResponse) Status {
	status := Status{
		SampledAt: response.SampledAt, State: response.State, Master: response.Master,
		Workers: slices.Clone(response.Workers), StartupValidation: startupValidationFromAgentResponse(response.StartupValidation),
		Recovery: response.Recovery, Issues: slices.Clone(response.Issues),
	}
	if response.Build != nil {
		build := buildInfoFromAgentResponse(*response.Build)
		status.Build = &build
	}
	return status
}

func effectiveConfigFromAgentResponse(response agentEffectiveConfigResponse) (EffectiveConfig, error) {
	if response.Occurrences == nil || response.Warnings == nil {
		return EffectiveConfig{}, errAgentInvalidResponse
	}
	switch response.DisplayMode {
	case EffectiveConfigDisplayModeStructured:
		if response.RawContent != nil || len(response.Warnings) != 0 {
			return EffectiveConfig{}, errAgentInvalidResponse
		}
	case EffectiveConfigDisplayModeRaw:
		if len(response.Occurrences) != 0 || response.RawContent == nil || *response.RawContent == "" || len(response.Warnings) == 0 {
			return EffectiveConfig{}, errAgentInvalidResponse
		}
	default:
		return EffectiveConfig{}, errAgentInvalidResponse
	}

	warnings := make(map[EffectiveConfigWarning]struct{}, len(response.Warnings))
	for _, warning := range response.Warnings {
		if warning != EffectiveConfigWarningPathOutsideAllowedRoots && warning != EffectiveConfigWarningStructureUnverified {
			return EffectiveConfig{}, errAgentInvalidResponse
		}
		if _, found := warnings[warning]; found {
			return EffectiveConfig{}, errAgentInvalidResponse
		}
		warnings[warning] = struct{}{}
	}

	configuration := EffectiveConfig{
		DisplayMode: response.DisplayMode,
		Occurrences: make([]ConfigOccurrence, len(response.Occurrences)),
		Warnings:    slices.Clone(response.Warnings),
	}
	identifiers := make(map[string]struct{}, len(response.Occurrences))
	for index, occurrence := range response.Occurrences {
		if occurrence.ID == "" || occurrence.Path == "" || occurrence.LoadOrder != index+1 {
			return EffectiveConfig{}, errAgentInvalidResponse
		}
		if _, found := identifiers[occurrence.ID]; found {
			return EffectiveConfig{}, errAgentInvalidResponse
		}
		identifiers[occurrence.ID] = struct{}{}
		configuration.Occurrences[index] = ConfigOccurrence(occurrence)
	}
	if response.RawContent != nil {
		configuration.RawContent = *response.RawContent
	}
	return configuration, nil
}
