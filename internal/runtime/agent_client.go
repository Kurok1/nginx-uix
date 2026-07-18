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
	agentClientTransportTimeout = 65 * time.Second
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
	dialer := &net.Dialer{Timeout: agentClientTransportTimeout}
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
	return effectiveConfigFromAgentResponse(response), nil
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

func effectiveConfigFromAgentResponse(response agentEffectiveConfigResponse) EffectiveConfig {
	configuration := EffectiveConfig{Occurrences: make([]ConfigOccurrence, len(response.Occurrences))}
	for index, occurrence := range response.Occurrences {
		configuration.Occurrences[index] = ConfigOccurrence(occurrence)
	}
	return configuration
}
