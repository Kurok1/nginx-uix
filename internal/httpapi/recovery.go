/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	recoveryDefaultPageLimit = 50
	recoveryMaximumPageLimit = 100
)

// RecoveryAPI is the content-free recovery and history behavior consumed by HTTP.
type RecoveryAPI interface {
	ListBackups(context.Context, config.BackupQuery) ([]config.BackupView, error)
	Backup(context.Context, config.BackupID) (config.BackupView, error)
	ChangeBackupProtection(context.Context, config.Actor, config.BackupID, config.ChangeBackupProtectionInput) (config.Backup, error)
	PlanRetention(context.Context, config.Actor) (config.RetentionRun, []config.RetentionItem, error)
	QueueRetentionExecution(context.Context, config.Actor, config.RetentionRunID, string) (config.RetentionRun, error)
	RetentionRun(context.Context, config.RetentionRunID) (config.RetentionRun, []config.RetentionItem, error)
	QueueRestore(context.Context, config.Actor, config.QueueRestoreInput) (config.Restore, error)
	Restore(context.Context, config.RestoreID) (config.Restore, error)
	RestoreStages(context.Context, config.RestoreID, uint64) ([]config.RestoreStage, error)
	QueueRestart(context.Context, config.Actor, config.QueueRestartInput) (config.Restart, error)
	Restart(context.Context, config.RestartID) (config.Restart, error)
	RestartStages(context.Context, config.RestartID, uint64) ([]config.RestartStage, error)
	ListReleases(context.Context, config.HistoryQuery) ([]config.Release, error)
	ListRestores(context.Context, config.HistoryQuery) ([]config.Restore, error)
	ListRestarts(context.Context, config.HistoryQuery) ([]config.Restart, error)
	ListAuditEvents(context.Context, config.AuditQuery) ([]config.AuditRecord, error)
	ListAttentionCases(context.Context, config.AttentionQuery) ([]config.AttentionCase, error)
	AttentionCase(context.Context, config.AttentionCaseID) (config.AttentionCase, error)
	VerifyAttentionCase(context.Context, config.Actor, config.AttentionCaseID) (config.Verification, error)
}

// RecoveryTaskStarter starts request-independent recovery work owned by the application lifecycle.
type RecoveryTaskStarter interface {
	StartRestore(config.RestoreID) bool
	StartRestart(config.RestartID) bool
	StartRetention(config.RetentionRunID) bool
}

type recoveryHandler struct {
	service   RecoveryAPI
	tasks     RecoveryTaskStarter
	sessions  SessionService
	publicURL *url.URL
}

type recoveryCursor struct {
	Version int       `json:"v"`
	Kind    string    `json:"kind"`
	Time    time.Time `json:"time"`
	ID      string    `json:"id,omitempty"`
	Number  int64     `json:"number,omitempty"`
}

type backupProtectionRequest struct {
	ExpectedProtected bool   `json:"expected_protected"`
	Protected         bool   `json:"protected"`
	Reason            string `json:"reason"`
	Confirmation      string `json:"confirmation"`
}

type restoreRequest struct {
	AttentionCaseID string `json:"attention_case_id"`
	Reason          string `json:"reason"`
	ConfirmBackupID string `json:"confirm_backup_id"`
}

type restartRequest struct {
	AttentionCaseID string `json:"attention_case_id"`
	Reason          string `json:"reason"`
	Confirmation    string `json:"confirmation"`
}

type retentionExecutionRequest struct {
	Confirmation string `json:"confirmation"`
}

type backupListResponse struct {
	Items      []backupResponse `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type backupProtectionResponse struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

type backupResponse struct {
	ID                string                     `json:"id"`
	OriginType        config.BackupOriginType    `json:"origin_type"`
	OriginID          string                     `json:"origin_id"`
	ReleaseID         string                     `json:"release_id,omitempty"`
	ProductionDigest  string                     `json:"production_digest"`
	State             config.BackupState         `json:"state"`
	EntryCount        int                        `json:"entry_count"`
	TotalBytes        int64                      `json:"total_bytes"`
	BodyPresent       bool                       `json:"body_present"`
	Protected         bool                       `json:"protected"`
	ManuallyProtected bool                       `json:"manually_protected"`
	ProtectionReason  string                     `json:"protection_reason,omitempty"`
	Protections       []backupProtectionResponse `json:"protections"`
	CreatedAt         time.Time                  `json:"created_at"`
	VerifiedAt        *time.Time                 `json:"verified_at,omitempty"`
	DeletedAt         *time.Time                 `json:"deleted_at,omitempty"`
}

type restoreStageResponse struct {
	Sequence   uint64                  `json:"sequence"`
	Stage      config.RestoreStageName `json:"stage"`
	Result     config.StageResult      `json:"result"`
	Code       string                  `json:"code,omitempty"`
	Details    json.RawMessage         `json:"details"`
	OccurredAt time.Time               `json:"occurred_at"`
}

type restoreResponse struct {
	ID               string                  `json:"id"`
	TargetBackupID   string                  `json:"target_backup_id"`
	SafetyBackupID   string                  `json:"safety_backup_id"`
	AttentionCaseID  string                  `json:"attention_case_id,omitempty"`
	State            config.RestoreState     `json:"state"`
	Stage            config.RestoreStageName `json:"stage"`
	ProductionDigest string                  `json:"source_digest"`
	TargetDigest     string                  `json:"target_digest"`
	LastErrorCode    string                  `json:"last_error_code,omitempty"`
	Reason           string                  `json:"reason"`
	RequestID        string                  `json:"request_id"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
	FinishedAt       *time.Time              `json:"finished_at,omitempty"`
	Stages           []restoreStageResponse  `json:"stages"`
}

type restartStageResponse struct {
	Sequence   uint64                  `json:"sequence"`
	Stage      config.RestartStageName `json:"stage"`
	Result     config.StageResult      `json:"result"`
	Code       string                  `json:"code,omitempty"`
	Details    json.RawMessage         `json:"details"`
	OccurredAt time.Time               `json:"occurred_at"`
}

type restartResponse struct {
	ID               string                  `json:"id"`
	AttentionCaseID  string                  `json:"attention_case_id,omitempty"`
	State            config.RestartState     `json:"state"`
	Stage            config.RestartStageName `json:"stage"`
	ProductionDigest string                  `json:"production_digest"`
	BeforeMasterPID  int                     `json:"before_master_pid,omitempty"`
	AfterMasterPID   int                     `json:"after_master_pid,omitempty"`
	WorkerCount      int                     `json:"worker_count"`
	HTTPStatus       int                     `json:"http_status,omitempty"`
	LastErrorCode    string                  `json:"last_error_code,omitempty"`
	Reason           string                  `json:"reason"`
	RequestID        string                  `json:"request_id"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
	FinishedAt       *time.Time              `json:"finished_at,omitempty"`
	Stages           []restartStageResponse  `json:"stages"`
}

type retentionItemResponse struct {
	Ordinal            int                       `json:"ordinal"`
	BackupID           string                    `json:"backup_id"`
	Decision           config.RetentionDecision  `json:"decision"`
	ReasonCode         string                    `json:"reason_code"`
	State              config.RetentionItemState `json:"state"`
	SnapshotCreatedAt  time.Time                 `json:"snapshot_created_at"`
	SnapshotTotalBytes int64                     `json:"snapshot_total_bytes"`
}

type retentionResponse struct {
	ID             string                   `json:"id"`
	State          config.RetentionRunState `json:"state"`
	Policy         retentionPolicyResponse  `json:"policy"`
	BackupCount    int                      `json:"backup_count"`
	TotalBytes     int64                    `json:"total_bytes"`
	ProtectedCount int                      `json:"protected_count"`
	DeleteCount    int                      `json:"delete_count"`
	DeleteBytes    int64                    `json:"delete_bytes"`
	DeletedCount   int                      `json:"deleted_count"`
	DeletedBytes   int64                    `json:"deleted_bytes"`
	LastErrorCode  string                   `json:"last_error_code,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	ExpiresAt      time.Time                `json:"expires_at"`
	StartedAt      *time.Time               `json:"started_at,omitempty"`
	FinishedAt     *time.Time               `json:"finished_at,omitempty"`
	Items          []retentionItemResponse  `json:"items"`
}

type retentionPolicyResponse struct {
	MinimumComplete   int   `json:"minimum_complete"`
	MaximumComplete   int   `json:"maximum_complete"`
	MaximumTotalBytes int64 `json:"maximum_total_bytes"`
	MinimumAgeSeconds int64 `json:"minimum_age_seconds"`
}

type attentionResponse struct {
	ID             string                         `json:"id"`
	SubjectType    config.AttentionSubjectType    `json:"subject_type"`
	SubjectID      string                         `json:"subject_id"`
	WorkspaceID    string                         `json:"workspace_id,omitempty"`
	BackupID       string                         `json:"backup_id,omitempty"`
	State          config.AttentionCaseState      `json:"state"`
	ReasonCode     string                         `json:"reason_code"`
	OpenedAt       time.Time                      `json:"opened_at"`
	ResolvedAt     *time.Time                     `json:"resolved_at,omitempty"`
	ResolutionType config.AttentionResolutionType `json:"resolution_type,omitempty"`
	ResolutionID   string                         `json:"resolution_id,omitempty"`
}

type verificationResponse struct {
	ID               string                   `json:"id"`
	AttentionCaseID  string                   `json:"attention_case_id"`
	State            config.VerificationState `json:"state"`
	ProductionDigest string                   `json:"production_digest"`
	MasterPID        int                      `json:"master_pid,omitempty"`
	WorkerCount      int                      `json:"worker_count"`
	HTTPStatus       int                      `json:"http_status,omitempty"`
	LastErrorCode    string                   `json:"last_error_code,omitempty"`
	RequestID        string                   `json:"request_id"`
	CreatedAt        time.Time                `json:"created_at"`
	FinishedAt       time.Time                `json:"finished_at"`
}

func (h *recoveryHandler) backups(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	query, cursor, ok := parseRecoveryCursorQuery(writer, request, "backups")
	if !ok {
		return
	}
	includeDeleted := false
	if raw := request.URL.Query().Get("include_deleted"); raw != "" {
		var err error
		includeDeleted, err = strconv.ParseBool(raw)
		if err != nil {
			writeInvalidConfigRequest(writer, request, "include_deleted")
			return
		}
	}
	views, err := h.service.ListBackups(request.Context(), config.BackupQuery{
		BeforeCreatedAt: cursor.Time, BeforeID: config.BackupID(cursor.ID),
		Limit: query.limit, IncludeDeleted: includeDeleted,
	})
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	response := backupListResponse{Items: make([]backupResponse, 0, len(views))}
	for _, view := range views {
		response.Items = append(response.Items, newBackupResponse(view))
	}
	if len(views) == query.limit {
		last := views[len(views)-1].Backup
		response.NextCursor = encodeRecoveryCursor(recoveryCursor{Version: 1, Kind: "backups", Time: last.CreatedAt, ID: string(last.ID)})
	}
	writeJSON(writer, http.StatusOK, response)
}

func (h *recoveryHandler) backup(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseBackupID(request.PathValue("backup_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "backup_id")
		return
	}
	backup, err := h.service.Backup(request.Context(), id)
	if err != nil {
		writeRecoveryNotFound(writer, request, err, "CONFIG_BACKUP_NOT_FOUND", "备份不存在")
		return
	}
	writeJSON(writer, http.StatusOK, newBackupResponse(backup))
}

func (h *recoveryHandler) protection(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseBackupID(request.PathValue("backup_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "backup_id")
		return
	}
	input, err := decodeStrictJSON[backupProtectionRequest](request, configSmallBodyLimit)
	if err != nil {
		writeConfigRequestError(writer, request, err, configSmallBodyLimit)
		return
	}
	_, err = h.service.ChangeBackupProtection(request.Context(), actor, id, config.ChangeBackupProtectionInput{
		ExpectedProtected: input.ExpectedProtected, Protected: input.Protected,
		Reason: input.Reason, Confirmation: input.Confirmation,
	})
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	view, err := h.service.Backup(request.Context(), id)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, newBackupResponse(view))
}

func (h *recoveryHandler) planRetention(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	if _, err := decodeStrictJSON[struct{}](request, configSmallBodyLimit); err != nil {
		writeConfigRequestError(writer, request, err, configSmallBodyLimit)
		return
	}
	run, items, err := h.service.PlanRetention(request.Context(), actor)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	writer.Header().Set("Location", "/api/v1/config/backup-retention-runs/"+string(run.ID))
	writeJSON(writer, http.StatusCreated, newRetentionResponse(run, items))
}

func (h *recoveryHandler) executeRetention(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil || h.tasks == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseRetentionRunID(request.PathValue("retention_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "retention_id")
		return
	}
	input, err := decodeStrictJSON[retentionExecutionRequest](request, configSmallBodyLimit)
	if err != nil {
		writeConfigRequestError(writer, request, err, configSmallBodyLimit)
		return
	}
	run, err := h.service.QueueRetentionExecution(request.Context(), actor, id, input.Confirmation)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	if !h.tasks.StartRetention(id) {
		writeConfigUnavailable(writer, request)
		return
	}
	writer.Header().Set("Location", "/api/v1/config/backup-retention-runs/"+string(id))
	writeJSON(writer, http.StatusAccepted, newRetentionResponse(run, nil))
}

func (h *recoveryHandler) retention(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseRetentionRunID(request.PathValue("retention_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "retention_id")
		return
	}
	run, items, err := h.service.RetentionRun(request.Context(), id)
	if err != nil {
		writeRecoveryNotFound(writer, request, err, "CONFIG_RETENTION_RUN_NOT_FOUND", "保留计划不存在")
		return
	}
	writeJSON(writer, http.StatusOK, newRetentionResponse(run, items))
}

func (h *recoveryHandler) queueRestore(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil || h.tasks == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	backupID, err := config.ParseBackupID(request.PathValue("backup_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "backup_id")
		return
	}
	input, err := decodeStrictJSON[restoreRequest](request, configSmallBodyLimit)
	if err != nil {
		writeConfigRequestError(writer, request, err, configSmallBodyLimit)
		return
	}
	var attentionID config.AttentionCaseID
	if input.AttentionCaseID != "" {
		attentionID, err = config.ParseAttentionCaseID(input.AttentionCaseID)
		if err != nil {
			writeInvalidConfigRequest(writer, request, "attention_case_id")
			return
		}
	}
	restore, err := h.service.QueueRestore(request.Context(), actor, config.QueueRestoreInput{
		TargetBackupID: backupID, AttentionCaseID: attentionID,
		Reason: input.Reason, ConfirmBackupID: input.ConfirmBackupID,
	})
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	if !h.tasks.StartRestore(restore.ID) {
		writeConfigUnavailable(writer, request)
		return
	}
	location := "/api/v1/config/restores/" + string(restore.ID)
	writer.Header().Set("Location", location)
	writeJSON(writer, http.StatusAccepted, newRestoreResponse(restore, nil))
}

func (h *recoveryHandler) restore(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseRestoreID(request.PathValue("restore_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "restore_id")
		return
	}
	restore, err := h.service.Restore(request.Context(), id)
	if err != nil {
		writeRecoveryNotFound(writer, request, err, "CONFIG_RESTORE_NOT_FOUND", "恢复任务不存在")
		return
	}
	stages, err := h.service.RestoreStages(request.Context(), id, 0)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, newRestoreResponse(restore, stages))
}

func (h *recoveryHandler) restoreEvents(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseRestoreID(request.PathValue("restore_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "restore_id")
		return
	}
	after, ok := parseLastEventID(writer, request)
	if !ok {
		return
	}
	restore, err := h.service.Restore(request.Context(), id)
	if err != nil {
		writeRecoveryNotFound(writer, request, err, "CONFIG_RESTORE_NOT_FOUND", "恢复任务不存在")
		return
	}
	controller := http.NewResponseController(writer)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	if after == 0 {
		if writeReleaseSSEEvent(writer, controller, "0", "snapshot", newRestoreResponse(restore, nil)) != nil {
			return
		}
	}
	for {
		stages, stageErr := h.service.RestoreStages(request.Context(), id, after)
		if stageErr != nil {
			return
		}
		restore, err = h.service.Restore(request.Context(), id)
		if err != nil {
			return
		}
		for _, stage := range stages {
			event := "stage"
			if terminalRestoreState(restore.State) && stage.Stage == restore.Stage {
				event = "terminal"
			}
			if writeReleaseSSEEvent(writer, controller, strconv.FormatUint(stage.Sequence, 10), event,
				newRestoreStageResponse(stage)) != nil {
				return
			}
			after = stage.Sequence
		}
		if terminalRestoreState(restore.State) {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(2 * time.Second):
			if writeReleaseSSEEvent(writer, controller, "", "heartbeat", struct{}{}) != nil {
				return
			}
		}
	}
}

func (h *recoveryHandler) restartCollection(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil || h.tasks == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	input, err := decodeStrictJSON[restartRequest](request, configSmallBodyLimit)
	if err != nil {
		writeConfigRequestError(writer, request, err, configSmallBodyLimit)
		return
	}
	var attentionID config.AttentionCaseID
	if input.AttentionCaseID != "" {
		attentionID, err = config.ParseAttentionCaseID(input.AttentionCaseID)
		if err != nil {
			writeInvalidConfigRequest(writer, request, "attention_case_id")
			return
		}
	}
	restart, err := h.service.QueueRestart(request.Context(), actor, config.QueueRestartInput{
		AttentionCaseID: attentionID, Reason: input.Reason, Confirmation: input.Confirmation,
	})
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	if !h.tasks.StartRestart(restart.ID) {
		writeConfigUnavailable(writer, request)
		return
	}
	location := "/api/v1/nginx/restarts/" + string(restart.ID)
	writer.Header().Set("Location", location)
	writeJSON(writer, http.StatusAccepted, newRestartResponse(restart, nil))
}

func (h *recoveryHandler) restart(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseRestartID(request.PathValue("restart_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "restart_id")
		return
	}
	restart, err := h.service.Restart(request.Context(), id)
	if err != nil {
		writeRecoveryNotFound(writer, request, err, "NGINX_RESTART_NOT_FOUND", "restart 任务不存在")
		return
	}
	stages, err := h.service.RestartStages(request.Context(), id, 0)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, newRestartResponse(restart, stages))
}

func (h *recoveryHandler) restartEvents(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseRestartID(request.PathValue("restart_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "restart_id")
		return
	}
	after, ok := parseLastEventID(writer, request)
	if !ok {
		return
	}
	restart, err := h.service.Restart(request.Context(), id)
	if err != nil {
		writeRecoveryNotFound(writer, request, err, "NGINX_RESTART_NOT_FOUND", "restart 任务不存在")
		return
	}
	controller := http.NewResponseController(writer)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	if after == 0 && writeReleaseSSEEvent(writer, controller, "0", "snapshot", newRestartResponse(restart, nil)) != nil {
		return
	}
	for {
		stages, stageErr := h.service.RestartStages(request.Context(), id, after)
		if stageErr != nil {
			return
		}
		restart, err = h.service.Restart(request.Context(), id)
		if err != nil {
			return
		}
		for _, stage := range stages {
			event := "stage"
			if terminalRestartState(restart.State) && stage.Stage == restart.Stage {
				event = "terminal"
			}
			if writeReleaseSSEEvent(writer, controller, strconv.FormatUint(stage.Sequence, 10), event,
				newRestartStageResponse(stage)) != nil {
				return
			}
			after = stage.Sequence
		}
		if terminalRestartState(restart.State) {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(2 * time.Second):
			if writeReleaseSSEEvent(writer, controller, "", "heartbeat", struct{}{}) != nil {
				return
			}
		}
	}
}

func (h *recoveryHandler) historyReleases(writer http.ResponseWriter, request *http.Request) {
	h.history(writer, request, "releases")
}

func (h *recoveryHandler) historyRestores(writer http.ResponseWriter, request *http.Request) {
	h.history(writer, request, "restores")
}

func (h *recoveryHandler) historyRestarts(writer http.ResponseWriter, request *http.Request) {
	h.history(writer, request, "restarts")
}

func (h *recoveryHandler) history(writer http.ResponseWriter, request *http.Request, kind string) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	query, cursor, ok := parseRecoveryCursorQuery(writer, request, kind)
	if !ok {
		return
	}
	historyQuery := config.HistoryQuery{BeforeCreatedAt: cursor.Time, BeforeID: cursor.ID, Limit: query.limit}
	switch kind {
	case "releases":
		items, err := h.service.ListReleases(request.Context(), historyQuery)
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		responses := make([]releaseResponse, 0, len(items))
		for _, item := range items {
			responses = append(responses, newReleaseResponse(item, nil))
		}
		writeJSON(writer, http.StatusOK, historyPageResponse[releaseResponse]{Items: responses, NextCursor: nextReleaseCursor(kind, items, query.limit)})
	case "restores":
		items, err := h.service.ListRestores(request.Context(), historyQuery)
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		responses := make([]restoreResponse, 0, len(items))
		for _, item := range items {
			responses = append(responses, newRestoreResponse(item, nil))
		}
		writeJSON(writer, http.StatusOK, historyPageResponse[restoreResponse]{Items: responses, NextCursor: nextRestoreCursor(kind, items, query.limit)})
	case "restarts":
		items, err := h.service.ListRestarts(request.Context(), historyQuery)
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		responses := make([]restartResponse, 0, len(items))
		for _, item := range items {
			responses = append(responses, newRestartResponse(item, nil))
		}
		writeJSON(writer, http.StatusOK, historyPageResponse[restartResponse]{Items: responses, NextCursor: nextRestartCursor(kind, items, query.limit)})
	}
}

type historyPageResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func (h *recoveryHandler) audit(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	query, cursor, ok := parseRecoveryCursorQuery(writer, request, "audit")
	if !ok {
		return
	}
	items, err := h.service.ListAuditEvents(request.Context(), config.AuditQuery{
		BeforeOccurredAt: cursor.Time, BeforeID: cursor.Number, Limit: query.limit,
	})
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	type auditResponse struct {
		ID         int64          `json:"id"`
		OccurredAt time.Time      `json:"occurred_at"`
		ActorName  string         `json:"actor_name"`
		Action     string         `json:"action"`
		ObjectType string         `json:"object_type"`
		ObjectID   string         `json:"object_id"`
		Result     string         `json:"result"`
		RequestID  string         `json:"request_id"`
		Details    map[string]any `json:"details"`
	}
	responses := make([]auditResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, auditResponse{
			ID: item.ID, OccurredAt: item.OccurredAt, ActorName: item.ActorName,
			Action: item.Action, ObjectType: item.ObjectType, ObjectID: item.ObjectID,
			Result: item.Result, RequestID: item.RequestID, Details: safeAuditDetails(item.DetailsJSON),
		})
	}
	next := ""
	if len(items) == query.limit {
		last := items[len(items)-1]
		next = encodeRecoveryCursor(recoveryCursor{Version: 1, Kind: "audit", Time: last.OccurredAt, Number: last.ID})
	}
	writeJSON(writer, http.StatusOK, historyPageResponse[auditResponse]{Items: responses, NextCursor: next})
}

func (h *recoveryHandler) attentionCollection(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	query, cursor, ok := parseRecoveryCursorQuery(writer, request, "attention")
	if !ok {
		return
	}
	state := config.AttentionCaseState(request.URL.Query().Get("state"))
	if state != "" && state != config.AttentionCaseOpen && state != config.AttentionCaseResolved {
		writeInvalidConfigRequest(writer, request, "state")
		return
	}
	items, err := h.service.ListAttentionCases(request.Context(), config.AttentionQuery{
		State: state, BeforeOpenedAt: cursor.Time, BeforeID: config.AttentionCaseID(cursor.ID), Limit: query.limit,
	})
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	responses := make([]attentionResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, newAttentionResponse(item))
	}
	next := ""
	if len(items) == query.limit {
		last := items[len(items)-1]
		next = encodeRecoveryCursor(recoveryCursor{
			Version: 1, Kind: "attention", Time: last.OpenedAt, ID: string(last.ID),
		})
	}
	writeJSON(writer, http.StatusOK, historyPageResponse[attentionResponse]{Items: responses, NextCursor: next})
}

func (h *recoveryHandler) attention(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseAttentionCaseID(request.PathValue("attention_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "attention_id")
		return
	}
	attention, err := h.service.AttentionCase(request.Context(), id)
	if err != nil {
		writeRecoveryNotFound(writer, request, err, "CONFIG_ATTENTION_CASE_NOT_FOUND", "人工处置记录不存在")
		return
	}
	writeJSON(writer, http.StatusOK, newAttentionResponse(attention))
}

func (h *recoveryHandler) verifyAttention(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if h.service == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, err := config.ParseAttentionCaseID(request.PathValue("attention_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "attention_id")
		return
	}
	if _, err := decodeStrictJSON[struct{}](request, configSmallBodyLimit); err != nil {
		writeConfigRequestError(writer, request, err, configSmallBodyLimit)
		return
	}
	verification, err := h.service.VerifyAttentionCase(request.Context(), actor, id)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	writeJSON(writer, http.StatusCreated, newVerificationResponse(verification))
}

type parsedRecoveryQuery struct{ limit int }

func parseRecoveryCursorQuery(
	writer http.ResponseWriter,
	request *http.Request,
	kind string,
) (parsedRecoveryQuery, recoveryCursor, bool) {
	allowed := []string{"cursor", "limit"}
	switch kind {
	case "backups":
		allowed = append(allowed, "include_deleted")
	case "attention":
		allowed = append(allowed, "state")
	}
	if !onlyQueryFields(request, allowed...) {
		writeInvalidConfigRequest(writer, request, "query")
		return parsedRecoveryQuery{}, recoveryCursor{}, false
	}
	limit, ok := parseRecoveryLimit(writer, request)
	if !ok {
		return parsedRecoveryQuery{}, recoveryCursor{}, false
	}
	cursor := recoveryCursor{Version: 1, Kind: kind}
	if raw := request.URL.Query().Get("cursor"); raw != "" {
		decoded, err := decodeRecoveryCursor(raw, kind)
		if err != nil {
			writeInvalidConfigRequest(writer, request, "cursor")
			return parsedRecoveryQuery{}, recoveryCursor{}, false
		}
		cursor = decoded
	}
	return parsedRecoveryQuery{limit: limit}, cursor, true
}

func parseRecoveryLimit(writer http.ResponseWriter, request *http.Request) (int, bool) {
	limit := recoveryDefaultPageLimit
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > recoveryMaximumPageLimit {
			writeInvalidConfigRequest(writer, request, "limit")
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

func onlyQueryFields(request *http.Request, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		set[field] = struct{}{}
	}
	for field, values := range request.URL.Query() {
		if _, ok := set[field]; !ok || len(values) != 1 {
			return false
		}
	}
	return true
}

func encodeRecoveryCursor(cursor recoveryCursor) string {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeRecoveryCursor(raw, kind string) (recoveryCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(payload) > 512 {
		return recoveryCursor{}, errors.New("invalid cursor")
	}
	var cursor recoveryCursor
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.Kind != kind || cursor.Time.IsZero() {
		return recoveryCursor{}, errors.New("invalid cursor")
	}
	if kind == "audit" {
		if cursor.Number <= 0 || cursor.ID != "" {
			return recoveryCursor{}, errors.New("invalid cursor")
		}
	} else if cursor.ID == "" || cursor.Number != 0 {
		return recoveryCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func writeRecoveryNotFound(writer http.ResponseWriter, request *http.Request, err error, code, message string) {
	if errors.Is(err, fs.ErrNotExist) {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusNotFound, code, message, nil)
		return
	}
	writeConfigAPIError(writer, request, err, nil)
}

func newBackupResponse(view config.BackupView) backupResponse {
	backup := view.Backup
	response := backupResponse{
		ID: string(backup.ID), OriginType: backup.OriginType, OriginID: backup.OriginID,
		ReleaseID: string(backup.ReleaseID), ProductionDigest: backup.ProductionDigest.String(),
		State: backup.State, EntryCount: backup.EntryCount, TotalBytes: backup.TotalBytes,
		BodyPresent: backup.BodyPresent, Protected: view.Protected,
		ManuallyProtected: backup.ManuallyProtected, ProtectionReason: backup.ProtectionReason,
		Protections: make([]backupProtectionResponse, 0, len(view.Protections)), CreatedAt: backup.CreatedAt,
	}
	if !backup.VerifiedAt.IsZero() {
		verified := backup.VerifiedAt
		response.VerifiedAt = &verified
	}
	if !backup.DeletedAt.IsZero() {
		deleted := backup.DeletedAt
		response.DeletedAt = &deleted
	}
	for _, reason := range view.Protections {
		response.Protections = append(response.Protections, backupProtectionResponse{Kind: reason.Kind, Code: reason.Code})
	}
	return response
}

func newRestoreResponse(restore config.Restore, stages []config.RestoreStage) restoreResponse {
	response := restoreResponse{
		ID: string(restore.ID), TargetBackupID: string(restore.TargetBackupID),
		SafetyBackupID: string(restore.SafetyBackupID), AttentionCaseID: string(restore.AttentionCaseID),
		State: restore.State, Stage: restore.Stage, ProductionDigest: restore.SourceDigest.String(),
		TargetDigest: restore.TargetDigest.String(), LastErrorCode: restore.LastErrorCode,
		Reason: restore.Reason, RequestID: restore.RequestID, CreatedAt: restore.CreatedAt,
		UpdatedAt: restore.UpdatedAt, Stages: make([]restoreStageResponse, 0, len(stages)),
	}
	if !restore.FinishedAt.IsZero() {
		finished := restore.FinishedAt
		response.FinishedAt = &finished
	}
	for _, stage := range stages {
		response.Stages = append(response.Stages, newRestoreStageResponse(stage))
	}
	return response
}

func newRestoreStageResponse(stage config.RestoreStage) restoreStageResponse {
	details := json.RawMessage(stage.PublicDetailsJSON)
	if !json.Valid(details) {
		details = json.RawMessage(`{}`)
	}
	return restoreStageResponse{
		Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result,
		Code: stage.Code, Details: details, OccurredAt: stage.OccurredAt,
	}
}

func newRestartResponse(restart config.Restart, stages []config.RestartStage) restartResponse {
	response := restartResponse{
		ID: string(restart.ID), AttentionCaseID: string(restart.AttentionCaseID),
		State: restart.State, Stage: restart.Stage, ProductionDigest: restart.ProductionDigest.String(),
		BeforeMasterPID: restart.BeforeMasterPID, AfterMasterPID: restart.AfterMasterPID,
		WorkerCount: restart.WorkerCount, HTTPStatus: restart.HTTPStatus,
		LastErrorCode: restart.LastErrorCode, Reason: restart.Reason, RequestID: restart.RequestID,
		CreatedAt: restart.CreatedAt, UpdatedAt: restart.UpdatedAt,
		Stages: make([]restartStageResponse, 0, len(stages)),
	}
	if !restart.FinishedAt.IsZero() {
		finished := restart.FinishedAt
		response.FinishedAt = &finished
	}
	for _, stage := range stages {
		response.Stages = append(response.Stages, newRestartStageResponse(stage))
	}
	return response
}

func newRestartStageResponse(stage config.RestartStage) restartStageResponse {
	details := json.RawMessage(stage.PublicDetailsJSON)
	if !json.Valid(details) {
		details = json.RawMessage(`{}`)
	}
	return restartStageResponse{
		Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result,
		Code: stage.Code, Details: details, OccurredAt: stage.OccurredAt,
	}
}

func newRetentionResponse(run config.RetentionRun, items []config.RetentionItem) retentionResponse {
	response := retentionResponse{
		ID: string(run.ID), State: run.State,
		Policy: retentionPolicyResponse{
			MinimumComplete: run.Policy.MinimumComplete, MaximumComplete: run.Policy.MaximumComplete,
			MaximumTotalBytes: run.Policy.MaximumTotalBytes,
			MinimumAgeSeconds: int64(run.Policy.MinimumAge / time.Second),
		},
		BackupCount: run.BackupCount,
		TotalBytes:  run.TotalBytes, ProtectedCount: run.ProtectedCount,
		DeleteCount: run.DeleteCount, DeleteBytes: run.DeleteBytes,
		DeletedCount: run.DeletedCount, DeletedBytes: run.DeletedBytes,
		LastErrorCode: run.LastErrorCode, CreatedAt: run.CreatedAt, ExpiresAt: run.ExpiresAt,
		Items: make([]retentionItemResponse, 0, len(items)),
	}
	if !run.StartedAt.IsZero() {
		started := run.StartedAt
		response.StartedAt = &started
	}
	if !run.FinishedAt.IsZero() {
		finished := run.FinishedAt
		response.FinishedAt = &finished
	}
	for _, item := range items {
		response.Items = append(response.Items, retentionItemResponse{
			Ordinal: item.Ordinal, BackupID: string(item.BackupID), Decision: item.Decision,
			ReasonCode: item.ReasonCode, State: item.State,
			SnapshotCreatedAt: item.SnapshotCreatedAt, SnapshotTotalBytes: item.SnapshotTotalBytes,
		})
	}
	return response
}

func newAttentionResponse(attention config.AttentionCase) attentionResponse {
	response := attentionResponse{
		ID: string(attention.ID), SubjectType: attention.SubjectType, SubjectID: attention.SubjectID,
		WorkspaceID: string(attention.WorkspaceID), BackupID: string(attention.BackupID),
		State: attention.State, ReasonCode: attention.ReasonCode, OpenedAt: attention.OpenedAt,
		ResolutionType: attention.ResolutionType, ResolutionID: attention.ResolutionID,
	}
	if !attention.ResolvedAt.IsZero() {
		resolved := attention.ResolvedAt
		response.ResolvedAt = &resolved
	}
	return response
}

func newVerificationResponse(verification config.Verification) verificationResponse {
	return verificationResponse{
		ID: string(verification.ID), AttentionCaseID: string(verification.AttentionCaseID),
		State: verification.State, ProductionDigest: verification.ProductionDigest.String(),
		MasterPID: verification.MasterPID, WorkerCount: verification.WorkerCount,
		HTTPStatus: verification.HTTPStatus, LastErrorCode: verification.LastErrorCode,
		RequestID: verification.RequestID, CreatedAt: verification.CreatedAt,
		FinishedAt: verification.FinishedAt,
	}
}

func terminalRestoreState(state config.RestoreState) bool {
	switch state {
	case config.RestoreStateSucceeded, config.RestoreStateFailed, config.RestoreStateRolledBack,
		config.RestoreStateNeedsAttention, config.RestoreStateCancelled:
		return true
	case config.RestoreStateQueued, config.RestoreStateRunning, config.RestoreStateRollingBack:
		return false
	default:
		return false
	}
}

func terminalRestartState(state config.RestartState) bool {
	switch state {
	case config.RestartStateSucceeded, config.RestartStateFailed,
		config.RestartStateNeedsAttention, config.RestartStateCancelled:
		return true
	case config.RestartStateQueued, config.RestartStateRunning:
		return false
	default:
		return false
	}
}

func safeAuditDetails(raw string) map[string]any {
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	if decoder.Decode(&decoded) != nil {
		return map[string]any{}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return map[string]any{}
	}
	allowed := map[string]struct{}{
		"sequence": {}, "stage": {}, "result": {}, "backup_count": {}, "delete_count": {},
		"delete_bytes": {}, "protected": {}, "ordinal": {}, "backup_id": {}, "bytes": {},
		"state": {}, "http_status": {}, "resolution_type": {}, "resolution_id": {},
	}
	result := make(map[string]any)
	for key, value := range decoded {
		if _, ok := allowed[key]; !ok {
			continue
		}
		if safe, ok := safeAuditDetailValue(key, value); ok {
			result[key] = safe
		}
	}
	return result
}

func safeAuditDetailValue(key string, value any) (any, bool) {
	switch key {
	case "protected":
		boolean, ok := value.(bool)
		return boolean, ok
	case "backup_id", "resolution_id":
		identifier, ok := value.(string)
		if !ok {
			return nil, false
		}
		_, err := config.ParseBackupID(identifier)
		return identifier, err == nil
	case "stage", "result", "state", "resolution_type":
		text, ok := value.(string)
		return text, ok && validSafeAuditEnum(text)
	case "sequence", "backup_count", "delete_count", "delete_bytes", "ordinal", "bytes", "http_status":
		number, ok := value.(json.Number)
		if !ok {
			return nil, false
		}
		integer, err := number.Int64()
		if err != nil || integer < 0 {
			return nil, false
		}
		if key == "http_status" && (integer < 100 || integer > 599) {
			return nil, false
		}
		if (key == "sequence" || key == "ordinal") && integer == 0 {
			return nil, false
		}
		return integer, true
	default:
		return nil, false
	}
}

func validSafeAuditEnum(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func nextReleaseCursor(kind string, items []config.Release, limit int) string {
	if len(items) != limit {
		return ""
	}
	last := items[len(items)-1]
	return encodeRecoveryCursor(recoveryCursor{Version: 1, Kind: kind, Time: last.CreatedAt, ID: string(last.ID)})
}

func nextRestoreCursor(kind string, items []config.Restore, limit int) string {
	if len(items) != limit {
		return ""
	}
	last := items[len(items)-1]
	return encodeRecoveryCursor(recoveryCursor{Version: 1, Kind: kind, Time: last.CreatedAt, ID: string(last.ID)})
}

func nextRestartCursor(kind string, items []config.Restart, limit int) string {
	if len(items) != limit {
		return ""
	}
	last := items[len(items)-1]
	return encodeRecoveryCursor(recoveryCursor{Version: 1, Kind: kind, Time: last.CreatedAt, ID: string(last.ID)})
}
