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
)

const (
	agentClientBaseURL        = "http://nginx-uix-agent"
	agentClientRequestTimeout = 30 * time.Second
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
	dialer := &net.Dialer{Timeout: agentClientRequestTimeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression:     true,
		MaxIdleConns:           1,
		MaxIdleConnsPerHost:    1,
		IdleConnTimeout:        agentClientRequestTimeout,
		ResponseHeaderTimeout:  agentClientRequestTimeout,
		MaxResponseHeaderBytes: agentMaxHeaderBytes,
		ForceAttemptHTTP2:      false,
	}
	return &AgentClient{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   agentClientRequestTimeout,
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

func (c *AgentClient) get(ctx context.Context, path string, target any) error {
	if ctx == nil {
		return newAgentClientProtocolError(agentErrorCodeInternal)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.httpClient == nil {
		return newAgentClientProtocolError(agentErrorCodeInternal)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, agentClientBaseURL+path, nil)
	if err != nil {
		return newAgentClientProtocolError(agentErrorCodeInternal)
	}
	request.Header.Set("Accept", agentProtocolContentType)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return classifyAgentClientIOError(ctx, err)
	}
	payload, err := readAgentClientResponse(response)
	if ctxErr := ctx.Err(); ctxErr != nil {
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
	case agentErrorCodeInternal:
		return &AgentProtocolError{Code: code, Message: "agent operation failed"}
	default:
		return &AgentProtocolError{Code: agentErrorCodeInternal, Message: "agent operation failed"}
	}
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
