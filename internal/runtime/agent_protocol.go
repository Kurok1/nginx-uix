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
	"log/slog"
	"net/http"
	"slices"
	"time"
)

const (
	agentProtocolResponseLimit = 34 * 1024 * 1024

	agentErrorCodeInvalidRequest       = "invalid_request"
	agentErrorCodeMethodNotAllowed     = "method_not_allowed"
	agentErrorCodeNotFound             = "not_found"
	agentErrorCodeInternal             = "internal_error"
	agentErrorCodeResponseTooLarge     = "response_too_large"
	agentErrorCodeConfigInvalid        = "nginx_config_invalid"
	agentErrorCodeCommandTimeout       = "nginx_command_timeout"
	agentErrorCodeCommandOutputLarge   = "nginx_output_too_large"
	agentProtocolContentType           = "application/json"
	agentProtocolHealthPath            = "/v1/health"
	agentProtocolStatusPath            = "/v1/status"
	agentProtocolBuildInfoPath         = "/v1/build-info"
	agentProtocolStartupValidationPath = "/v1/startup-validation"
	agentProtocolEffectiveConfigPath   = "/v1/effective-config"
)

var errAgentResponseTooLarge = errors.New("agent response too large")

type agentOperations interface {
	Health(context.Context) error
	Status(context.Context) (Status, error)
	BuildInfo(context.Context) (BuildInfo, error)
	StartupValidation(context.Context) (StartupState, error)
	EffectiveConfig(context.Context) (EffectiveConfig, error)
}

// AgentProtocolError is one stable, non-sensitive error returned by the local Agent.
type AgentProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	cause   error
}

func (e *AgentProtocolError) Error() string {
	return e.Message
}

func (e *AgentProtocolError) Unwrap() error {
	return e.cause
}

type agentErrorEnvelope struct {
	Error AgentProtocolError `json:"error"`
}

type agentProtocolHandler struct {
	operations agentOperations
	logger     *slog.Logger
	now        func() time.Time
}

func newAgentProtocolHandler(operations agentOperations, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &agentProtocolHandler{
		operations: operations,
		logger:     logger,
		now:        time.Now,
	}
}

func (h *agentProtocolHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	startedAt := h.now()
	action, found := agentAction(request.URL.Path)
	if !found {
		bytesWritten := writeAgentProtocolError(writer, http.StatusNotFound, agentErrorCodeNotFound, "agent endpoint not found")
		h.log(action, agentErrorCodeNotFound, startedAt, bytesWritten)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		bytesWritten := writeAgentProtocolError(writer, http.StatusMethodNotAllowed, agentErrorCodeMethodNotAllowed, "agent method not allowed")
		h.log(action, agentErrorCodeMethodNotAllowed, startedAt, bytesWritten)
		return
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		bytesWritten := writeAgentProtocolError(writer, http.StatusBadRequest, agentErrorCodeInvalidRequest, "agent request is invalid")
		h.log(action, agentErrorCodeInvalidRequest, startedAt, bytesWritten)
		return
	}
	hasBody, err := agentRequestHasBody(request)
	if err != nil || hasBody {
		bytesWritten := writeAgentProtocolError(writer, http.StatusBadRequest, agentErrorCodeInvalidRequest, "agent request is invalid")
		h.log(action, agentErrorCodeInvalidRequest, startedAt, bytesWritten)
		return
	}

	response, err := h.execute(request.Context(), action)
	if err != nil {
		status, protocolError := classifyAgentOperationError(err)
		bytesWritten := writeAgentProtocolError(writer, status, protocolError.Code, protocolError.Message)
		h.log(action, protocolError.Code, startedAt, bytesWritten)
		return
	}
	payload, err := encodeAgentProtocolResponse(response)
	if err != nil {
		code := agentErrorCodeInternal
		message := "agent operation failed"
		if errors.Is(err, errAgentResponseTooLarge) {
			code = agentErrorCodeResponseTooLarge
			message = "agent response exceeded limit"
		}
		bytesWritten := writeAgentProtocolError(writer, http.StatusInternalServerError, code, message)
		h.log(action, code, startedAt, bytesWritten)
		return
	}
	bytesWritten := writeAgentProtocolPayload(writer, http.StatusOK, payload)
	h.log(action, "success", startedAt, bytesWritten)
}

func agentAction(path string) (string, bool) {
	switch path {
	case agentProtocolHealthPath:
		return "health", true
	case agentProtocolStatusPath:
		return "status", true
	case agentProtocolBuildInfoPath:
		return "build_info", true
	case agentProtocolStartupValidationPath:
		return "startup_validation", true
	case agentProtocolEffectiveConfigPath:
		return "effective_config", true
	default:
		return "reject_request", false
	}
}

func agentRequestHasBody(request *http.Request) (bool, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return false, nil
	}
	if request.ContentLength > 0 {
		return true, nil
	}
	if request.ContentLength == 0 && len(request.TransferEncoding) == 0 {
		return false, nil
	}
	var singleByte [1]byte
	count, err := request.Body.Read(singleByte[:])
	if count > 0 {
		return true, nil
	}
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read agent request body: %w", err)
	}
	return false, nil
}

func (h *agentProtocolHandler) execute(ctx context.Context, action string) (any, error) {
	switch action {
	case "health":
		if err := h.operations.Health(ctx); err != nil {
			return nil, err
		}
		return agentHealthResponse{Status: "healthy"}, nil
	case "status":
		status, err := h.operations.Status(ctx)
		return newAgentStatusResponse(status), err
	case "build_info":
		build, err := h.operations.BuildInfo(ctx)
		return newAgentBuildInfoResponse(build), err
	case "startup_validation":
		startup, err := h.operations.StartupValidation(ctx)
		return newAgentStartupStateResponse(startup), err
	case "effective_config":
		configuration, err := h.operations.EffectiveConfig(ctx)
		return newAgentEffectiveConfigResponse(configuration), err
	default:
		return nil, fmt.Errorf("execute agent operation: unknown action")
	}
}

func classifyAgentOperationError(err error) (int, *AgentProtocolError) {
	switch {
	case errors.Is(err, ErrConfigInvalid):
		return http.StatusUnprocessableEntity, &AgentProtocolError{Code: agentErrorCodeConfigInvalid, Message: "nginx configuration is invalid"}
	case errors.Is(err, ErrCommandTimeout), errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, &AgentProtocolError{Code: agentErrorCodeCommandTimeout, Message: "nginx operation timed out"}
	case errors.Is(err, ErrOutputTooLarge):
		return http.StatusBadGateway, &AgentProtocolError{Code: agentErrorCodeCommandOutputLarge, Message: "nginx output exceeded limit"}
	default:
		return http.StatusInternalServerError, &AgentProtocolError{Code: agentErrorCodeInternal, Message: "agent operation failed"}
	}
}

func encodeAgentProtocolResponse(value any) ([]byte, error) {
	buffer := &agentBoundedBuffer{remaining: agentProtocolResponseLimit}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		if errors.Is(err, errAgentResponseTooLarge) {
			return nil, errAgentResponseTooLarge
		}
		return nil, fmt.Errorf("encode agent response: %w", err)
	}
	return bytes.Clone(buffer.buffer.Bytes()), nil
}

type agentBoundedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func (b *agentBoundedBuffer) Write(payload []byte) (int, error) {
	if len(payload) > b.remaining {
		return 0, errAgentResponseTooLarge
	}
	written, err := b.buffer.Write(payload)
	b.remaining -= written
	return written, err
}

func writeAgentProtocolError(writer http.ResponseWriter, status int, code, message string) int {
	payload, err := json.Marshal(agentErrorEnvelope{Error: AgentProtocolError{Code: code, Message: message}})
	if err != nil {
		status = http.StatusInternalServerError
		payload = []byte(`{"error":{"code":"internal_error","message":"agent operation failed"}}`)
	}
	return writeAgentProtocolPayload(writer, status, append(payload, '\n'))
}

func writeAgentProtocolPayload(writer http.ResponseWriter, status int, payload []byte) int {
	writer.Header().Set("Content-Type", agentProtocolContentType)
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	written, _ := writer.Write(payload)
	return written
}

func (h *agentProtocolHandler) log(action, result string, startedAt time.Time, responseBytes int) {
	h.logger.Info("agent request",
		"action", action,
		"result", result,
		"duration_ms", h.now().Sub(startedAt).Milliseconds(),
		"response_bytes", responseBytes,
	)
}

// Health reports whether the in-process Agent service can accept work.
func (s *Service) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("check agent health: %w", err)
	}
	if s == nil {
		return fmt.Errorf("check agent health: service is unavailable")
	}
	return nil
}

// StartupValidation returns the most recently persisted startup and recovery state.
func (s *Service) StartupValidation(ctx context.Context) (StartupState, error) {
	if err := ctx.Err(); err != nil {
		return StartupState{}, fmt.Errorf("read startup validation: %w", err)
	}
	if s == nil || s.readStartupState == nil {
		return StartupState{}, fmt.Errorf("read startup validation: state reader is unavailable")
	}
	state, err := s.readStartupState(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		return StartupState{}, nil
	}
	if err != nil {
		return StartupState{}, fmt.Errorf("read startup validation: %w", err)
	}
	if state == nil {
		return StartupState{}, nil
	}
	return *cloneStartupState(state), nil
}

type agentHealthResponse struct {
	Status string `json:"status"`
}

type agentBuildInfoResponse struct {
	Version            string   `json:"version"`
	ConfigureArguments []string `json:"configure_arguments"`
	PIDPath            string   `json:"pid_path"`
	SbinPath           string   `json:"sbin_path"`
}

func newAgentBuildInfoResponse(build BuildInfo) agentBuildInfoResponse {
	return agentBuildInfoResponse(build)
}

type agentStartupValidationResponse struct {
	Valid      bool      `json:"valid"`
	CheckedAt  time.Time `json:"checked_at"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	Diagnostic string    `json:"diagnostic"`
}

func newAgentStartupValidationResponse(validation *StartupValidation) *agentStartupValidationResponse {
	if validation == nil {
		return nil
	}
	return &agentStartupValidationResponse{
		Valid: validation.Valid, CheckedAt: validation.CheckedAt, ExitCode: validation.ExitCode,
		Diagnostic: validation.Diagnostic,
	}
}

type agentStartupStateResponse struct {
	Validation *agentStartupValidationResponse `json:"validation,omitempty"`
	Recovery   *RecoveryState                  `json:"recovery,omitempty"`
}

func newAgentStartupStateResponse(startup StartupState) agentStartupStateResponse {
	return agentStartupStateResponse{
		Validation: newAgentStartupValidationResponse(startup.Validation),
		Recovery:   startup.Recovery,
	}
}

type agentStatusResponse struct {
	SampledAt         time.Time                       `json:"sampled_at"`
	State             State                           `json:"state"`
	Master            *NginxProcess                   `json:"master,omitempty"`
	Workers           []NginxProcess                  `json:"workers"`
	Build             *agentBuildInfoResponse         `json:"build,omitempty"`
	StartupValidation *agentStartupValidationResponse `json:"startup_validation,omitempty"`
	Recovery          *RecoveryState                  `json:"recovery,omitempty"`
	Issues            []string                        `json:"issues"`
}

func newAgentStatusResponse(status Status) agentStatusResponse {
	response := agentStatusResponse{
		SampledAt: status.SampledAt, State: status.State, Master: status.Master, Workers: status.Workers,
		StartupValidation: newAgentStartupValidationResponse(status.StartupValidation),
		Recovery:          status.Recovery, Issues: status.Issues,
	}
	if status.Build != nil {
		build := newAgentBuildInfoResponse(*status.Build)
		response.Build = &build
	}
	return response
}

type agentConfigOccurrenceResponse struct {
	ID        string `json:"id"`
	LoadOrder int    `json:"load_order"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

type agentEffectiveConfigResponse struct {
	DisplayMode EffectiveConfigDisplayMode      `json:"display_mode"`
	Occurrences []agentConfigOccurrenceResponse `json:"occurrences"`
	RawContent  *string                         `json:"raw_content"`
	Warnings    []EffectiveConfigWarning        `json:"warnings"`
}

func newAgentEffectiveConfigResponse(configuration EffectiveConfig) agentEffectiveConfigResponse {
	occurrences := make([]agentConfigOccurrenceResponse, 0, len(configuration.Occurrences))
	for _, occurrence := range configuration.Occurrences {
		occurrences = append(occurrences, agentConfigOccurrenceResponse(occurrence))
	}
	response := agentEffectiveConfigResponse{
		DisplayMode: configuration.DisplayMode,
		Occurrences: occurrences,
		Warnings:    slices.Clone(configuration.Warnings),
	}
	if configuration.DisplayMode == EffectiveConfigDisplayModeRaw {
		response.RawContent = &configuration.RawContent
	}
	return response
}
