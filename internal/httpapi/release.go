/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	releaseEventLimit      = uint64(512)
	releaseSSEWriteTimeout = 5 * time.Second
)

// ReleaseAPI is the persisted configuration publication behavior consumed by HTTP.
type ReleaseAPI interface {
	Check(context.Context, config.Actor, config.PublishCheckInput) (config.PublishCheck, error)
	Queue(context.Context, config.Actor, config.QueueReleaseInput) (config.Release, error)
	PublishCheck(context.Context, config.PublishCheckID) (config.PublishCheck, error)
	Release(context.Context, config.ReleaseID) (config.Release, error)
	Stages(context.Context, config.ReleaseID, uint64) ([]config.ReleaseStage, error)
}

// ReleaseTaskStarter starts request-independent publication work owned by the application lifecycle.
type ReleaseTaskStarter interface {
	Start(config.ReleaseID) bool
}

type releaseHandler struct {
	service   ReleaseAPI
	sessions  SessionService
	publicURL *url.URL
	tasks     ReleaseTaskStarter
}

type queueReleaseRequest struct {
	CheckID     string `json:"check_id"`
	ConfirmName string `json:"confirm_name"`
}

type publishCheckResponse struct {
	ID                string                   `json:"id"`
	WorkspaceID       string                   `json:"workspace_id"`
	WorkspaceRevision uint64                   `json:"workspace_revision"`
	ProductionDigest  string                   `json:"production_digest"`
	BaseDigest        string                   `json:"base_digest"`
	DraftDigest       string                   `json:"draft_digest"`
	CandidateDigest   string                   `json:"candidate_digest"`
	ManifestVersion   uint16                   `json:"manifest_version"`
	PolicyVersion     uint16                   `json:"policy_version"`
	ValidatorVersion  uint16                   `json:"validator_version"`
	ValidatorBuildID  string                   `json:"validator_build_id"`
	State             config.PublishCheckState `json:"state"`
	DiagnosticCount   int                      `json:"diagnostic_count"`
	Details           json.RawMessage          `json:"details"`
	StartedAt         time.Time                `json:"started_at"`
	FinishedAt        time.Time                `json:"finished_at"`
	ExpiresAt         time.Time                `json:"expires_at"`
}

type releaseStageResponse struct {
	Sequence   uint64                  `json:"sequence"`
	Stage      config.ReleaseStageName `json:"stage"`
	Result     config.StageResult      `json:"result"`
	Code       string                  `json:"code,omitempty"`
	Details    json.RawMessage         `json:"details"`
	OccurredAt time.Time               `json:"occurred_at"`
}

type releaseResponse struct {
	ID               string                  `json:"id"`
	WorkspaceID      string                  `json:"workspace_id"`
	CheckID          string                  `json:"check_id"`
	BackupID         string                  `json:"backup_id,omitempty"`
	State            config.ReleaseState     `json:"state"`
	Stage            config.ReleaseStageName `json:"stage"`
	ProductionDigest string                  `json:"production_digest"`
	DraftDigest      string                  `json:"draft_digest"`
	CandidateDigest  string                  `json:"candidate_digest"`
	LastErrorCode    string                  `json:"last_error_code,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
	FinishedAt       *time.Time              `json:"finished_at,omitempty"`
	Stages           []releaseStageResponse  `json:"stages"`
}

func (h *releaseHandler) checkWorkspace(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, ok := parseReleaseWorkspaceID(writer, request)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	ifMatch, ok := requireReleaseIfMatch(writer, request)
	if !ok {
		return
	}
	if _, err := decodeStrictJSON[struct{}](request, configSmallBodyLimit); err != nil {
		writeConfigRequestError(writer, request, err, configSmallBodyLimit)
		return
	}
	check, err := h.service.Check(request.Context(), actor, config.PublishCheckInput{WorkspaceID: id, IfMatch: ifMatch})
	if err != nil {
		if errors.Is(err, config.ErrCandidateInvalid) && check.ID != "" && check.State == config.PublishCheckStateInvalid {
			writeJSON(writer, http.StatusUnprocessableEntity, newPublishCheckResponse(check))
			return
		}
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	writeJSON(writer, http.StatusCreated, newPublishCheckResponse(check))
}

func (h *releaseHandler) check(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	if !requireNoQuery(writer, request) {
		return
	}
	id, err := config.ParsePublishCheckID(request.PathValue("check_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "check_id")
		return
	}
	check, err := h.service.PublishCheck(request.Context(), id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusNotFound, "CONFIG_PUBLISH_CHECK_NOT_FOUND", "发布检查不存在", nil)
			return
		}
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, newPublishCheckResponse(check))
}

func (h *releaseHandler) queue(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok {
		return
	}
	if h.service == nil || h.tasks == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, ok := parseReleaseWorkspaceID(writer, request)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	ifMatch, ok := requireReleaseIfMatch(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[queueReleaseRequest](request, configSmallBodyLimit)
	if err != nil {
		writeConfigRequestError(writer, request, err, configSmallBodyLimit)
		return
	}
	checkID, err := config.ParsePublishCheckID(input.CheckID)
	if err != nil || input.ConfirmName == "" {
		writeInvalidConfigRequest(writer, request, "check_id")
		return
	}
	release, err := h.service.Queue(request.Context(), actor, config.QueueReleaseInput{
		WorkspaceID: id, CheckID: checkID, IfMatch: ifMatch, ConfirmName: input.ConfirmName,
	})
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	if !h.tasks.Start(release.ID) {
		writeConfigUnavailable(writer, request)
		return
	}
	location := "/api/v1/config/releases/" + string(release.ID)
	writer.Header().Set("Location", location)
	writeJSON(writer, http.StatusAccepted, newReleaseResponse(release, []config.ReleaseStage{}))
}

func (h *releaseHandler) release(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	if !requireNoQuery(writer, request) {
		return
	}
	id, err := config.ParseReleaseID(request.PathValue("release_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "release_id")
		return
	}
	release, err := h.service.Release(request.Context(), id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusNotFound, "CONFIG_RELEASE_NOT_FOUND", "配置发布不存在", nil)
			return
		}
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	stages, err := h.service.Stages(request.Context(), id, 0)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, newReleaseResponse(release, stages))
}

func (h *releaseHandler) events(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	if !requireNoQuery(writer, request) {
		return
	}
	id, err := config.ParseReleaseID(request.PathValue("release_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "release_id")
		return
	}
	after, ok := parseLastEventID(writer, request)
	if !ok {
		return
	}
	release, err := h.service.Release(request.Context(), id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusNotFound, "CONFIG_RELEASE_NOT_FOUND", "配置发布不存在", nil)
			return
		}
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	controller := http.NewResponseController(writer)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	if after == 0 {
		if err := writeReleaseSSEEvent(writer, controller, "0", "snapshot", newReleaseResponse(release, []config.ReleaseStage{})); err != nil {
			return
		}
	}
	for {
		stages, stageErr := h.service.Stages(request.Context(), id, after)
		if stageErr != nil {
			return
		}
		release, err = h.service.Release(request.Context(), id)
		if err != nil {
			return
		}
		if terminalReleaseState(release.State) {
			stages, stageErr = h.service.Stages(request.Context(), id, after)
			if stageErr != nil {
				return
			}
		}
		for _, stage := range stages {
			eventName := "stage"
			if terminalReleaseState(release.State) && stage.Stage == release.Stage {
				eventName = "terminal"
			}
			if err := writeReleaseSSEEvent(
				writer, controller, strconv.FormatUint(stage.Sequence, 10), eventName, newReleaseStageResponse(stage),
			); err != nil {
				return
			}
			after = stage.Sequence
		}
		if terminalReleaseState(release.State) {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(2 * time.Second):
			if err := writeReleaseSSEEvent(writer, controller, "", "heartbeat", struct{}{}); err != nil {
				return
			}
		}
	}
}

func writeReleaseSSEEvent(
	writer http.ResponseWriter,
	controller *http.ResponseController,
	id string,
	event string,
	payload any,
) error {
	if err := controller.SetWriteDeadline(time.Now().Add(releaseSSEWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if id != "" {
		if _, err := fmt.Fprintf(writer, "id: %s\n", id); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, encoded); err != nil {
		return err
	}
	return controller.Flush()
}

func parseReleaseWorkspaceID(writer http.ResponseWriter, request *http.Request) (config.WorkspaceID, bool) {
	id, err := config.ParseWorkspaceID(request.PathValue("workspace_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "workspace_id")
		return "", false
	}
	return id, true
}

func requireReleaseIfMatch(writer http.ResponseWriter, request *http.Request) (string, bool) {
	values := request.Header.Values("If-Match")
	if len(values) != 1 {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusConflict, "CONFIG_WORKSPACE_CONFLICT", "配置工作区已变化", nil)
		return "", false
	}
	if _, err := config.ParseStrongETag(values[0], "draft-v1:"); err != nil {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusConflict, "CONFIG_WORKSPACE_CONFLICT", "配置工作区已变化", nil)
		return "", false
	}
	return values[0], true
}

func parseLastEventID(writer http.ResponseWriter, request *http.Request) (uint64, bool) {
	values := request.Header.Values("Last-Event-ID")
	if len(values) == 0 {
		return 0, true
	}
	if len(values) != 1 {
		writeInvalidConfigRequest(writer, request, "last_event_id")
		return 0, false
	}
	value, err := strconv.ParseUint(values[0], 10, 64)
	if err != nil || value > releaseEventLimit {
		writeInvalidConfigRequest(writer, request, "last_event_id")
		return 0, false
	}
	return value, true
}

func newPublishCheckResponse(check config.PublishCheck) publishCheckResponse {
	details := json.RawMessage(check.PublicDetailsJSON)
	if !json.Valid(details) {
		details = json.RawMessage(`{}`)
	}
	return publishCheckResponse{
		ID: string(check.ID), WorkspaceID: string(check.WorkspaceID), WorkspaceRevision: check.WorkspaceRevision,
		ProductionDigest: check.ProductionDigest.String(), BaseDigest: check.BaseDigest.String(),
		DraftDigest: check.DraftDigest.String(), CandidateDigest: check.CandidateDigest.String(),
		ManifestVersion: check.ManifestVersion, PolicyVersion: check.PolicyVersion,
		ValidatorVersion: check.ValidatorVersion, ValidatorBuildID: check.ValidatorBuildID,
		State: check.State, DiagnosticCount: check.DiagnosticCount, Details: details,
		StartedAt: check.StartedAt, FinishedAt: check.FinishedAt, ExpiresAt: check.ExpiresAt,
	}
}

func newReleaseResponse(release config.Release, stages []config.ReleaseStage) releaseResponse {
	response := releaseResponse{
		ID: string(release.ID), WorkspaceID: string(release.WorkspaceID), CheckID: string(release.CheckID), BackupID: string(release.BackupID),
		State: release.State, Stage: release.Stage, ProductionDigest: release.ProductionDigest.String(),
		DraftDigest: release.DraftDigest.String(), CandidateDigest: release.CandidateDigest.String(),
		LastErrorCode: release.LastErrorCode, CreatedAt: release.CreatedAt, UpdatedAt: release.UpdatedAt,
		Stages: make([]releaseStageResponse, 0, len(stages)),
	}
	if !release.FinishedAt.IsZero() {
		finished := release.FinishedAt
		response.FinishedAt = &finished
	}
	for _, stage := range stages {
		response.Stages = append(response.Stages, newReleaseStageResponse(stage))
	}
	return response
}

func newReleaseStageResponse(stage config.ReleaseStage) releaseStageResponse {
	details := json.RawMessage(stage.PublicDetailsJSON)
	if !json.Valid(details) {
		details = json.RawMessage(`{}`)
	}
	return releaseStageResponse{Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result, Code: stage.Code, Details: details, OccurredAt: stage.OccurredAt}
}

func terminalReleaseState(state config.ReleaseState) bool {
	switch state {
	case config.ReleaseStateSucceeded, config.ReleaseStateFailed, config.ReleaseStateRolledBack, config.ReleaseStateNeedsAttention, config.ReleaseStateCancelled:
		return true
	case config.ReleaseStateQueued, config.ReleaseStateRunning, config.ReleaseStateRollingBack:
		return false
	}
	return false
}
