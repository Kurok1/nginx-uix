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
	"mime"
	"net/http"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	agentProtocolResponseLimit = 34 * 1024 * 1024

	agentErrorCodeInvalidRequest         = "invalid_request"
	agentErrorCodeMethodNotAllowed       = "method_not_allowed"
	agentErrorCodeNotFound               = "not_found"
	agentErrorCodeInternal               = "internal_error"
	agentErrorCodeResponseTooLarge       = "response_too_large"
	agentErrorCodeConfigInvalid          = "nginx_config_invalid"
	agentErrorCodeCommandTimeout         = "nginx_command_timeout"
	agentErrorCodeCommandOutputLarge     = "nginx_output_too_large"
	agentErrorCodeConfigPathInvalid      = "config_path_invalid"
	agentErrorCodeConfigLimitExceeded    = "config_limit_exceeded"
	agentErrorCodeConfigSnapshotChanged  = "config_snapshot_changed"
	agentErrorCodeConfigOperationTimeout = "config_operation_timeout"
	agentProtocolContentType             = "application/json"
	agentProtocolVersion                 = uint16(1)
	agentProtocolHealthPath              = "/v1/health"
	agentProtocolStatusPath              = "/v1/status"
	agentProtocolBuildInfoPath           = "/v1/build-info"
	agentProtocolStartupValidationPath   = "/v1/startup-validation"
	agentProtocolEffectiveConfigPath     = "/v1/effective-config"
	agentProtocolConfigSnapshotPath      = "/v1/config/snapshot"
	agentProtocolConfigDigestPath        = "/v1/config/digest"
	agentSnapshotRequestLimit            = 4 * 1024
	agentSnapshotTimeout                 = configSnapshotTimeout
	agentDigestTimeout                   = configDigestTimeout
)

var errAgentResponseTooLarge = errors.New("agent response too large")

type agentOperations interface {
	Health(context.Context) error
	Status(context.Context) (Status, error)
	BuildInfo(context.Context) (BuildInfo, error)
	StartupValidation(context.Context) (StartupState, error)
	EffectiveConfig(context.Context) (EffectiveConfig, error)
	ConfigSnapshot(context.Context, config.WorkspaceID) (config.Snapshot, error)
	ConfigDigest(context.Context) (config.ProductionState, error)
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
		h.log(action, agentErrorCodeNotFound, "", startedAt, bytesWritten)
		return
	}
	expectedMethod := agentActionMethod(action)
	if request.Method != expectedMethod {
		writer.Header().Set("Allow", expectedMethod)
		bytesWritten := writeAgentProtocolError(writer, http.StatusMethodNotAllowed, agentErrorCodeMethodNotAllowed, "agent method not allowed")
		h.log(action, agentErrorCodeMethodNotAllowed, "", startedAt, bytesWritten)
		return
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		bytesWritten := writeAgentProtocolError(writer, http.StatusBadRequest, agentErrorCodeInvalidRequest, "agent request is invalid")
		h.log(action, agentErrorCodeInvalidRequest, "", startedAt, bytesWritten)
		return
	}
	requestID := ""
	var snapshotID config.WorkspaceID
	if agentActionRequiresRequestID(action) {
		requestID = agentRequestID(request)
		if requestID == "" {
			bytesWritten := writeAgentProtocolError(writer, http.StatusBadRequest, agentErrorCodeInvalidRequest, "agent request is invalid")
			h.log(action, agentErrorCodeInvalidRequest, "", startedAt, bytesWritten)
			return
		}
	}
	if action == "config_snapshot" {
		decoded, err := decodeAgentConfigSnapshotRequest(request)
		if err != nil {
			bytesWritten := writeAgentProtocolError(writer, http.StatusBadRequest, agentErrorCodeInvalidRequest, "agent request is invalid")
			h.log(action, agentErrorCodeInvalidRequest, requestID, startedAt, bytesWritten)
			return
		}
		snapshotID, err = config.ParseWorkspaceID(decoded.WorkspaceID)
		if err != nil {
			bytesWritten := writeAgentProtocolError(writer, http.StatusBadRequest, agentErrorCodeInvalidRequest, "agent request is invalid")
			h.log(action, agentErrorCodeInvalidRequest, requestID, startedAt, bytesWritten)
			return
		}
	} else if hasBody, err := agentRequestHasBody(request); err != nil || hasBody {
		bytesWritten := writeAgentProtocolError(writer, http.StatusBadRequest, agentErrorCodeInvalidRequest, "agent request is invalid")
		h.log(action, agentErrorCodeInvalidRequest, requestID, startedAt, bytesWritten)
		return
	}

	response, err := h.execute(request.Context(), action, snapshotID)
	if err != nil {
		status, protocolError := classifyAgentOperationError(action, err)
		bytesWritten := writeAgentProtocolError(writer, status, protocolError.Code, protocolError.Message)
		h.log(action, protocolError.Code, requestID, startedAt, bytesWritten)
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
		h.log(action, code, requestID, startedAt, bytesWritten)
		return
	}
	bytesWritten := writeAgentProtocolPayload(writer, http.StatusOK, payload)
	h.log(action, "success", requestID, startedAt, bytesWritten)
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
	case agentProtocolConfigSnapshotPath:
		return "config_snapshot", true
	case agentProtocolConfigDigestPath:
		return "config_digest", true
	default:
		return "reject_request", false
	}
}

func agentActionMethod(action string) string {
	if action == "config_snapshot" {
		return http.MethodPost
	}
	return http.MethodGet
}

func agentActionRequiresRequestID(action string) bool {
	return action == "config_snapshot" || action == "config_digest"
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

func agentRequestID(request *http.Request) string {
	values := request.Header.Values("X-Request-ID")
	if len(values) != 1 || !validAgentRequestID(values[0]) {
		return ""
	}
	return values[0]
}

func validAgentRequestID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func decodeAgentConfigSnapshotRequest(request *http.Request) (agentConfigSnapshotRequest, error) {
	contentTypes := request.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		return agentConfigSnapshotRequest{}, errors.New("snapshot content type is required")
	}
	mediaType, parameters, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != agentProtocolContentType || len(parameters) != 0 {
		return agentConfigSnapshotRequest{}, errors.New("snapshot content type is invalid")
	}
	if request.ContentLength > agentSnapshotRequestLimit {
		return agentConfigSnapshotRequest{}, errors.New("snapshot request exceeds limit")
	}
	payload, err := io.ReadAll(io.LimitReader(request.Body, agentSnapshotRequestLimit+1))
	if err != nil || len(payload) > agentSnapshotRequestLimit {
		return agentConfigSnapshotRequest{}, errors.New("snapshot request exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return agentConfigSnapshotRequest{}, errors.New("snapshot request object is required")
	}
	var decoded agentConfigSnapshotRequest
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		rawKey, err := decoder.Token()
		key, ok := rawKey.(string)
		if err != nil || !ok {
			return agentConfigSnapshotRequest{}, errors.New("snapshot request key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return agentConfigSnapshotRequest{}, errors.New("snapshot request field is duplicated")
		}
		seen[key] = struct{}{}
		switch key {
		case "protocol_version":
			err = decoder.Decode(&decoded.ProtocolVersion)
		case "workspace_id":
			err = decoder.Decode(&decoded.WorkspaceID)
		default:
			return agentConfigSnapshotRequest{}, errors.New("snapshot request field is unknown")
		}
		if err != nil {
			return agentConfigSnapshotRequest{}, errors.New("snapshot request field is invalid")
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return agentConfigSnapshotRequest{}, errors.New("snapshot request object is incomplete")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return agentConfigSnapshotRequest{}, errors.New("snapshot request has trailing data")
	}
	if len(seen) != 2 || decoded.ProtocolVersion != agentProtocolVersion || decoded.WorkspaceID == "" {
		return agentConfigSnapshotRequest{}, errors.New("snapshot request fields are incomplete")
	}
	return decoded, nil
}

func (h *agentProtocolHandler) execute(ctx context.Context, action string, snapshotID config.WorkspaceID) (any, error) {
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
	case "config_snapshot":
		operationCtx, cancel := context.WithTimeout(ctx, agentSnapshotTimeout)
		defer cancel()
		snapshot, err := h.operations.ConfigSnapshot(operationCtx, snapshotID)
		if err != nil {
			return nil, err
		}
		return newAgentConfigSnapshotResponse(snapshot)
	case "config_digest":
		operationCtx, cancel := context.WithTimeout(ctx, agentDigestTimeout)
		defer cancel()
		state, err := h.operations.ConfigDigest(operationCtx)
		return newAgentConfigDigestResponse(state), err
	default:
		return nil, fmt.Errorf("execute agent operation: unknown action")
	}
}

func classifyAgentOperationError(action string, err error) (int, *AgentProtocolError) {
	if agentActionRequiresRequestID(action) {
		switch {
		case errors.Is(err, config.ErrPathInvalid):
			return http.StatusBadRequest, &AgentProtocolError{Code: agentErrorCodeConfigPathInvalid, Message: "configuration path is invalid"}
		case errors.Is(err, config.ErrLimitExceeded):
			return http.StatusRequestEntityTooLarge, &AgentProtocolError{Code: agentErrorCodeConfigLimitExceeded, Message: "configuration limit exceeded"}
		case errors.Is(err, config.ErrSnapshotChanged):
			return http.StatusConflict, &AgentProtocolError{Code: agentErrorCodeConfigSnapshotChanged, Message: "configuration changed during snapshot"}
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			return http.StatusGatewayTimeout, &AgentProtocolError{Code: agentErrorCodeConfigOperationTimeout, Message: "configuration operation timed out"}
		}
	}
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

func (h *agentProtocolHandler) log(action, result, requestID string, startedAt time.Time, responseBytes int) {
	attributes := []any{
		"action", action,
		"result", result,
		"duration_ms", h.now().Sub(startedAt).Milliseconds(),
		"response_bytes", responseBytes,
	}
	if requestID != "" {
		attributes = append(attributes, "request_id", requestID)
	}
	h.logger.Info("agent request", attributes...)
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

type agentConfigSnapshotRequest struct {
	ProtocolVersion uint16 `json:"protocol_version"`
	WorkspaceID     string `json:"workspace_id"`
}

type agentConfigSnapshotResponse struct {
	Manifest         []byte `json:"manifest"`
	ProductionDigest string `json:"production_digest"`
	BaseDigest       string `json:"base_digest"`
	ManifestVersion  uint16 `json:"manifest_version"`
	EntryCount       int    `json:"entry_count"`
	ManagedBytes     int64  `json:"managed_bytes"`
	BaseComplete     bool   `json:"base_complete"`
}

func newAgentConfigSnapshotResponse(snapshot config.Snapshot) (agentConfigSnapshotResponse, error) {
	manifest, err := snapshot.Manifest.MarshalBinary()
	if err != nil {
		return agentConfigSnapshotResponse{}, fmt.Errorf("marshal config snapshot manifest: %w", err)
	}
	return agentConfigSnapshotResponse{
		Manifest:         manifest,
		ProductionDigest: snapshot.ProductionDigest.String(),
		BaseDigest:       snapshot.BaseDigest.String(),
		ManifestVersion:  snapshot.Manifest.SchemaVersion,
		EntryCount:       snapshot.Manifest.EntryCount,
		ManagedBytes:     snapshot.Manifest.ManagedBytes,
		BaseComplete:     snapshot.BaseDigest != (config.Digest{}) && snapshot.BaseDigest == snapshot.ProductionDigest,
	}, nil
}

type agentConfigDigestResponse struct {
	ProductionDigest string `json:"production_digest"`
	ManifestVersion  uint16 `json:"manifest_version"`
	EntryCount       int    `json:"entry_count"`
	ManagedBytes     int64  `json:"managed_bytes"`
}

func newAgentConfigDigestResponse(state config.ProductionState) agentConfigDigestResponse {
	return agentConfigDigestResponse{
		ProductionDigest: state.Digest.String(), ManifestVersion: state.ManifestVersion,
		EntryCount: state.EntryCount, ManagedBytes: state.ManagedBytes,
	}
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
	Occurrences []agentConfigOccurrenceResponse `json:"occurrences"`
}

func newAgentEffectiveConfigResponse(configuration EffectiveConfig) agentEffectiveConfigResponse {
	occurrences := make([]agentConfigOccurrenceResponse, 0, len(configuration.Occurrences))
	for _, occurrence := range configuration.Occurrences {
		occurrences = append(occurrences, agentConfigOccurrenceResponse(occurrence))
	}
	return agentEffectiveConfigResponse{Occurrences: occurrences}
}
