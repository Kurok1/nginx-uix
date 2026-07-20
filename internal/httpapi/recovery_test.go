/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestRecoveryHTTPReturnsCurrentDynamicProtectionEvidence(t *testing.T) {
	now := time.Date(2026, time.July, 19, 18, 45, 0, 0, time.UTC)
	backupID := config.BackupID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	api := &recoveryAPIStub{backup: config.BackupView{
		Backup: config.Backup{
			ID: backupID, OriginType: config.BackupOriginRelease,
			OriginID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ProductionDigest: config.Digest{1},
			State: config.BackupStateComplete, EntryCount: 1, TotalBytes: 32,
			BodyPresent: true, CreatedAt: now, VerifiedAt: now,
		},
		Protected:   true,
		Protections: []config.BackupProtectionReason{{Kind: "active", Code: "active_operation"}},
	}}
	recorder := serveRecoveryMutation(t, http.MethodPut,
		"/api/v1/config/backups/"+string(backupID)+"/protection",
		`{"expected_protected":false,"protected":true,"reason":"known recovery point","confirmation":""}`, api)
	if recorder.Code != http.StatusOK {
		t.Fatalf("protection status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Protected   bool `json:"protected"`
		Protections []struct {
			Code string `json:"code"`
		} `json:"protections"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Protected || len(response.Protections) != 2 || response.Protections[1].Code != "active_operation" {
		t.Fatalf("protection response = %#v", response)
	}
}

func TestRecoveryHTTPMapsInvalidRestoreTargetToStableUnprocessableError(t *testing.T) {
	backupID := config.BackupID("cccccccccccccccccccccccccccccccc")
	api := &recoveryAPIStub{restoreErr: errors.Join(config.ErrBackupTargetInvalid, errors.New("private diagnostic"))}
	recorder := serveRecoveryMutation(t, http.MethodPost,
		"/api/v1/config/backups/"+string(backupID)+"/restores",
		`{"reason":"recover known configuration","confirm_backup_id":"`+string(backupID)+`"}`, api)
	if recorder.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(recorder.Body.String(), `"code":"CONFIG_BACKUP_TARGET_INVALID"`) ||
		strings.Contains(recorder.Body.String(), "private diagnostic") {
		t.Fatalf("restore target response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSafeAuditDetailsRejectsNestedInvalidAndNonIntegralValues(t *testing.T) {
	details := safeAuditDetails(`{
		"stage":"runtime_confirming","result":"success","protected":true,"http_status":204,
		"backup_id":"dddddddddddddddddddddddddddddddd","sequence":2,
		"delete_count":-1,"bytes":1.5,"resolution_id":"not-an-id","state":["running"]
	}`)
	if len(details) != 6 || details["stage"] != "runtime_confirming" || details["http_status"] != int64(204) ||
		details["sequence"] != int64(2) || details["protected"] != true ||
		details["backup_id"] != "dddddddddddddddddddddddddddddddd" {
		t.Fatalf("safe details = %#v", details)
	}
	for _, rejected := range []string{"delete_count", "bytes", "resolution_id", "state"} {
		if _, exists := details[rejected]; exists {
			t.Errorf("unsafe detail %s survived: %#v", rejected, details)
		}
	}
}

func TestRecoveryHTTPQueuesRestoreRestartAndReturnsTerminalRestoreSSE(t *testing.T) {
	now := time.Date(2026, time.July, 19, 18, 30, 0, 0, time.UTC)
	backupID := config.BackupID("11111111111111111111111111111111")
	restoreID := config.RestoreID("22222222222222222222222222222222")
	restartID := config.RestartID("33333333333333333333333333333333")
	api := &recoveryAPIStub{
		backup: config.BackupView{Backup: config.Backup{
			ID: backupID, OriginType: config.BackupOriginRelease,
			OriginID:         "44444444444444444444444444444444",
			ReleaseID:        "44444444444444444444444444444444",
			ProductionDigest: config.Digest{1}, State: config.BackupStateComplete,
			EntryCount: 2, TotalBytes: 128, BodyPresent: true, CreatedAt: now, VerifiedAt: now,
		}},
		restore: config.Restore{
			ID: restoreID, TargetBackupID: backupID, SafetyBackupID: "55555555555555555555555555555555",
			State: config.RestoreStateSucceeded, Stage: config.RestoreStageSucceeded,
			SourceDigest: config.Digest{2}, TargetDigest: config.Digest{1}, CreatedBy: 1,
			Reason: "recover known configuration", RequestID: "request", CreatedAt: now,
			UpdatedAt: now, FinishedAt: now,
		},
		restoreStages: []config.RestoreStage{
			{RestoreID: restoreID, Sequence: 1, Stage: config.RestoreStageQueued, Result: config.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now},
			{RestoreID: restoreID, Sequence: 2, Stage: config.RestoreStageSucceeded, Result: config.StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: now},
		},
		restart: config.Restart{
			ID: restartID, State: config.RestartStateQueued, Stage: config.RestartStageQueued,
			ProductionDigest: config.Digest{2}, CreatedBy: 1, Reason: "restart nginx",
			RequestID: "request", CreatedAt: now, UpdatedAt: now,
		},
	}

	recorder := serveRecoveryMutation(t, http.MethodPost,
		"/api/v1/config/backups/"+string(backupID)+"/restores",
		`{"reason":"recover known configuration","confirm_backup_id":"`+string(backupID)+`"}`, api)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Location") != "/api/v1/config/restores/"+string(restoreID) ||
		api.restoreInput.TargetBackupID != backupID || api.startedRestore != restoreID {
		t.Fatalf("restore status/location/input/start = %d/%q/%#v/%s; body = %s",
			recorder.Code, recorder.Header().Get("Location"), api.restoreInput, api.startedRestore, recorder.Body.String())
	}
	recorder = serveRecoveryMutation(t, http.MethodPost, "/api/v1/nginx/restarts",
		`{"reason":"restart nginx","confirmation":"RESTART NGINX"}`, api)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Location") != "/api/v1/nginx/restarts/"+string(restartID) ||
		api.restartInput.Confirmation != "RESTART NGINX" || api.startedRestart != restartID {
		t.Fatalf("restart status/location/input/start = %d/%q/%#v/%s; body = %s",
			recorder.Code, recorder.Header().Get("Location"), api.restartInput, api.startedRestart, recorder.Body.String())
	}
	recorder = serveRecoveryGET(t, "/api/v1/config/restores/"+string(restoreID)+"/events", api, "1")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" ||
		!strings.Contains(recorder.Body.String(), "id: 2\nevent: terminal\n") {
		t.Fatalf("restore SSE status/headers/body = %d/%v/%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func serveRecoveryMutation(t *testing.T, method, target, body string, api *recoveryAPIStub) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	request := httptest.NewRequest(method, "http://example.test"+target, strings.NewReader(body))
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set(csrfHeaderName, issued.CSRFToken)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{
		Sessions: &authorizationSessionStub{issued: issued}, Recovery: api, RecoveryTasks: api,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
	}).ServeHTTP(recorder, request)
	return recorder
}

func serveRecoveryGET(t *testing.T, target string, api *recoveryAPIStub, lastEventID string) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	request := httptest.NewRequest(http.MethodGet, "http://example.test"+target, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	recorder := httptest.NewRecorder()
	NewHandler(Dependencies{
		Sessions: &authorizationSessionStub{issued: issued}, Recovery: api, RecoveryTasks: api,
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
	}).ServeHTTP(recorder, request)
	return recorder
}

type recoveryAPIStub struct {
	backup           config.BackupView
	restore          config.Restore
	restoreStages    []config.RestoreStage
	restart          config.Restart
	restartStages    []config.RestartStage
	restoreInput     config.QueueRestoreInput
	restartInput     config.QueueRestartInput
	startedRestore   config.RestoreID
	startedRestart   config.RestartID
	startedRetention config.RetentionRunID
	restoreErr       error
}

func (s *recoveryAPIStub) ListBackups(context.Context, config.BackupQuery) ([]config.BackupView, error) {
	return []config.BackupView{s.backup}, nil
}
func (s *recoveryAPIStub) Backup(context.Context, config.BackupID) (config.BackupView, error) {
	return s.backup, nil
}
func (s *recoveryAPIStub) ChangeBackupProtection(_ context.Context, _ config.Actor, _ config.BackupID, input config.ChangeBackupProtectionInput) (config.Backup, error) {
	s.backup.Backup.ManuallyProtected = input.Protected
	if input.Protected {
		s.backup.Protections = append([]config.BackupProtectionReason{{Kind: "manual", Code: "manual_protection"}}, s.backup.Protections...)
	}
	s.backup.Protected = len(s.backup.Protections) > 0
	return s.backup.Backup, nil
}
func (s *recoveryAPIStub) PlanRetention(context.Context, config.Actor) (config.RetentionRun, []config.RetentionItem, error) {
	return config.RetentionRun{}, nil, nil
}
func (s *recoveryAPIStub) QueueRetentionExecution(context.Context, config.Actor, config.RetentionRunID, string) (config.RetentionRun, error) {
	return config.RetentionRun{}, nil
}
func (s *recoveryAPIStub) RetentionRun(context.Context, config.RetentionRunID) (config.RetentionRun, []config.RetentionItem, error) {
	return config.RetentionRun{}, nil, nil
}
func (s *recoveryAPIStub) QueueRestore(_ context.Context, _ config.Actor, input config.QueueRestoreInput) (config.Restore, error) {
	s.restoreInput = input
	return s.restore, s.restoreErr
}
func (s *recoveryAPIStub) Restore(context.Context, config.RestoreID) (config.Restore, error) {
	return s.restore, nil
}
func (s *recoveryAPIStub) RestoreStages(_ context.Context, _ config.RestoreID, after uint64) ([]config.RestoreStage, error) {
	return recoveryRestoreStagesAfter(s.restoreStages, after), nil
}
func (s *recoveryAPIStub) QueueRestart(_ context.Context, _ config.Actor, input config.QueueRestartInput) (config.Restart, error) {
	s.restartInput = input
	return s.restart, nil
}
func (s *recoveryAPIStub) Restart(context.Context, config.RestartID) (config.Restart, error) {
	return s.restart, nil
}
func (s *recoveryAPIStub) RestartStages(context.Context, config.RestartID, uint64) ([]config.RestartStage, error) {
	return s.restartStages, nil
}
func (s *recoveryAPIStub) ListReleases(context.Context, config.HistoryQuery) ([]config.Release, error) {
	return nil, nil
}
func (s *recoveryAPIStub) ListRestores(context.Context, config.HistoryQuery) ([]config.Restore, error) {
	return []config.Restore{s.restore}, nil
}
func (s *recoveryAPIStub) ListRestarts(context.Context, config.HistoryQuery) ([]config.Restart, error) {
	return []config.Restart{s.restart}, nil
}
func (s *recoveryAPIStub) ListAuditEvents(context.Context, config.AuditQuery) ([]config.AuditRecord, error) {
	return nil, nil
}
func (s *recoveryAPIStub) ListAttentionCases(context.Context, config.AttentionQuery) ([]config.AttentionCase, error) {
	return nil, nil
}
func (s *recoveryAPIStub) AttentionCase(context.Context, config.AttentionCaseID) (config.AttentionCase, error) {
	return config.AttentionCase{}, nil
}
func (s *recoveryAPIStub) VerifyAttentionCase(context.Context, config.Actor, config.AttentionCaseID) (config.Verification, error) {
	return config.Verification{}, nil
}
func (s *recoveryAPIStub) StartRestore(id config.RestoreID) bool { s.startedRestore = id; return true }
func (s *recoveryAPIStub) StartRestart(id config.RestartID) bool { s.startedRestart = id; return true }
func (s *recoveryAPIStub) StartRetention(id config.RetentionRunID) bool {
	s.startedRetention = id
	return true
}

func recoveryRestoreStagesAfter(stages []config.RestoreStage, after uint64) []config.RestoreStage {
	result := make([]config.RestoreStage, 0, len(stages))
	for _, stage := range stages {
		if stage.Sequence > after {
			result = append(result, stage)
		}
	}
	return result
}
