/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestAgentProtocolCandidateValidationAcceptsOnlyTypedDigestRequest(t *testing.T) {
	operations := &recordingReleaseAgentOperations{recordingAgentOperations: &recordingAgentOperations{}}
	operations.candidate = config.CandidateValidation{
		Valid: true, CandidateDigest: config.Digest{1}, ValidatorVersion: 1,
		ValidatorBuildID: "build-id", CheckedAt: time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC),
	}
	body := `{"protocol_version":1,"workspace_id":"11111111111111111111111111111111","production_digest":"` + config.Digest{2}.String() + `","draft_digest":"` + config.Digest{3}.String() + `"}`
	request := httptest.NewRequest(http.MethodPost, agentProtocolCandidateValidationPath, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", agentProtocolContentType)
	request.Header.Set("X-Request-ID", "request-1")
	recorder := httptest.NewRecorder()

	newAgentProtocolHandler(operations, nil).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if operations.candidateRequest.WorkspaceID != "11111111111111111111111111111111" || operations.candidateRequest.ProductionDigest != (config.Digest{2}) || operations.candidateRequest.DraftDigest != (config.Digest{3}) {
		t.Fatalf("candidate request = %+v", operations.candidateRequest)
	}
	var response agentCandidateValidationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || !response.Valid || response.CandidateDigest != operations.candidate.CandidateDigest.String() {
		t.Fatalf("response = %+v, err = %v", response, err)
	}
}

func TestAgentProtocolReleaseReturnsRolledBackTerminalEvidence(t *testing.T) {
	operations := &recordingReleaseAgentOperations{recordingAgentOperations: &recordingAgentOperations{}}
	operations.release = config.ReleaseExecutionResult{
		ReleaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: config.ReleaseStateRolledBack,
		Stage: config.ReleaseStageRolledBack, ErrorCode: "reload_failed", FinishedAt: time.Now().UTC(),
	}
	body := `{"protocol_version":1,"release_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","backup_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","workspace_id":"11111111111111111111111111111111","production_digest":"` + config.Digest{2}.String() + `","draft_digest":"` + config.Digest{3}.String() + `","candidate_digest":"` + config.Digest{4}.String() + `"}`
	request := httptest.NewRequest(http.MethodPost, agentProtocolReleasePath, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", agentProtocolContentType)
	request.Header.Set("X-Request-ID", "request-2")
	recorder := httptest.NewRecorder()

	newAgentProtocolHandler(operations, nil).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response agentReleaseResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.State != config.ReleaseStateRolledBack || response.ErrorCode != "reload_failed" {
		t.Fatalf("response = %+v, err = %v", response, err)
	}
}

func TestAgentClientRoundTripsCandidateAndReleaseTypedOperations(t *testing.T) {
	operations := &recordingReleaseAgentOperations{recordingAgentOperations: &recordingAgentOperations{}}
	operations.candidate = config.CandidateValidation{
		Valid: true, CandidateDigest: config.Digest{9}, ValidatorVersion: 1,
		ValidatorBuildID: "build-id", CheckedAt: time.Now().UTC(), Diagnostics: []config.CandidateDiagnostic{},
	}
	operations.release = config.ReleaseExecutionResult{
		ReleaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: config.ReleaseStateSucceeded,
		Stage: config.ReleaseStageCommitted, Backup: config.BackupEvidence{BackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ReleaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProductionDigest: config.Digest{2}, TreeDigest: config.Digest{8}, EntryCount: 2, TotalBytes: 10, VerifiedAt: time.Now().UTC()},
		FinishedAt: time.Now().UTC(), Stages: []config.ReleaseStage{{
			ReleaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1, Stage: config.ReleaseStageCommitted,
			Result: config.StageResultSuccess, PublicDetailsJSON: "{}", OccurredAt: time.Now().UTC(),
		}},
	}
	path := startAgentClientUnixServer(t, newAgentProtocolHandler(operations, nil))
	client := newAgentClient(path)
	candidateRequest := config.CandidateValidationRequest{
		WorkspaceID: "11111111111111111111111111111111", ProductionDigest: config.Digest{2}, DraftDigest: config.Digest{3},
	}
	validation, err := client.ValidateCandidate(context.Background(), "request-3", candidateRequest)
	if err != nil || !reflect.DeepEqual(validation, operations.candidate) {
		t.Fatalf("ValidateCandidate() = %+v, %v", validation, err)
	}
	releaseRequest := config.ReleaseExecutionRequest{
		ReleaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		WorkspaceID: "11111111111111111111111111111111", ProductionDigest: config.Digest{2}, DraftDigest: config.Digest{3}, CandidateDigest: config.Digest{9},
	}
	result, err := client.ExecuteRelease(context.Background(), "request-4", releaseRequest)
	if err != nil || !reflect.DeepEqual(result, operations.release) {
		t.Fatalf("ExecuteRelease() = %+v, %v", result, err)
	}
	operations.progress = config.ReleaseExecutionResult{
		ReleaseID: releaseRequest.ReleaseID, State: config.ReleaseStateRunning,
		Stage: config.ReleaseStageBackupCreating, Stages: []config.ReleaseStage{{
			ReleaseID: releaseRequest.ReleaseID, Sequence: 1, Stage: config.ReleaseStageRechecking,
			Result: config.StageResultRunning, PublicDetailsJSON: "{}", OccurredAt: time.Now().UTC(),
		}, {
			ReleaseID: releaseRequest.ReleaseID, Sequence: 2, Stage: config.ReleaseStageBackupCreating,
			Result: config.StageResultRunning, PublicDetailsJSON: "{}", OccurredAt: time.Now().UTC(),
		}},
	}
	progress, err := client.ReleaseProgress(context.Background(), "request-5", releaseRequest)
	if err != nil || !reflect.DeepEqual(progress, operations.progress) || !reflect.DeepEqual(operations.progressRequest, releaseRequest) {
		t.Fatalf("ReleaseProgress() = %+v, %v", progress, err)
	}
	operations.recovery = operations.release
	recovered, err := client.RecoverRelease(context.Background(), "request-6", releaseRequest)
	if err != nil || !reflect.DeepEqual(recovered, operations.recovery) {
		t.Fatalf("RecoverRelease() = %+v, %v", recovered, err)
	}
}

func TestAgentClientRejectsTerminalReleaseEvidenceNotBoundToRequest(t *testing.T) {
	request := config.ReleaseExecutionRequest{
		ReleaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BackupID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		WorkspaceID: "11111111111111111111111111111111", ProductionDigest: config.Digest{2}, DraftDigest: config.Digest{3}, CandidateDigest: config.Digest{9},
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*config.ReleaseExecutionResult)
	}{
		{name: "release id", mutate: func(result *config.ReleaseExecutionResult) {
			result.ReleaseID = "cccccccccccccccccccccccccccccccc"
		}},
		{name: "backup id", mutate: func(result *config.ReleaseExecutionResult) {
			result.Backup.BackupID = "dddddddddddddddddddddddddddddddd"
		}},
		{name: "production digest", mutate: func(result *config.ReleaseExecutionResult) {
			result.Backup.ProductionDigest = config.Digest{7}
		}},
		{name: "missing verified backup", mutate: func(result *config.ReleaseExecutionResult) {
			result.Backup = config.BackupEvidence{}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			operations := &recordingReleaseAgentOperations{recordingAgentOperations: &recordingAgentOperations{}}
			operations.release = successfulProtocolRelease(request, time.Now().UTC())
			testCase.mutate(&operations.release)
			path := startAgentClientUnixServer(t, newAgentProtocolHandler(operations, nil))
			client := newAgentClient(path)

			if _, err := client.ExecuteRelease(context.Background(), "request-boundary", request); err == nil {
				t.Fatal("ExecuteRelease() accepted terminal evidence not bound to its request")
			}
		})
	}
}

func successfulProtocolRelease(request config.ReleaseExecutionRequest, now time.Time) config.ReleaseExecutionResult {
	return config.ReleaseExecutionResult{
		ReleaseID: request.ReleaseID, State: config.ReleaseStateSucceeded, Stage: config.ReleaseStageCommitted,
		Backup: config.BackupEvidence{
			BackupID: request.BackupID, ReleaseID: request.ReleaseID, ProductionDigest: request.ProductionDigest,
			TreeDigest: config.Digest{8}, EntryCount: 2, TotalBytes: 10, VerifiedAt: now,
		},
		Stages: []config.ReleaseStage{{
			ReleaseID: request.ReleaseID, Sequence: 1, Stage: config.ReleaseStageCommitted,
			Result: config.StageResultSuccess, PublicDetailsJSON: "{}", OccurredAt: now,
		}},
		MasterPID: 10, WorkerCount: 1, HTTPStatus: http.StatusNoContent, FinishedAt: now,
	}
}

type recordingReleaseAgentOperations struct {
	*recordingAgentOperations
	candidateRequest config.CandidateValidationRequest
	candidate        config.CandidateValidation
	releaseRequest   config.ReleaseExecutionRequest
	release          config.ReleaseExecutionResult
	progressRequest  config.ReleaseExecutionRequest
	progress         config.ReleaseExecutionResult
	recoveryRequest  config.ReleaseExecutionRequest
	recovery         config.ReleaseExecutionResult
}

func (o *recordingReleaseAgentOperations) ValidateCandidate(_ context.Context, request config.CandidateValidationRequest) (config.CandidateValidation, error) {
	o.candidateRequest = request
	return o.candidate, nil
}

func (o *recordingReleaseAgentOperations) ExecuteRelease(_ context.Context, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	o.releaseRequest = request
	return o.release, context.DeadlineExceeded
}

func (o *recordingReleaseAgentOperations) ReleaseProgress(_ context.Context, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	o.progressRequest = request
	return o.progress, nil
}

func (o *recordingReleaseAgentOperations) RecoverRelease(_ context.Context, request config.ReleaseExecutionRequest) (config.ReleaseExecutionResult, error) {
	o.recoveryRequest = request
	return o.recovery, nil
}
