/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package config

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReleaseServiceCreatesBoundCheckAndPublishesWorkspace(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Production change")
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
		Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = mutation.Workspace
	repository := newMemoryReleaseRepository()
	repository.requireBackupBeforeTerminal = true
	agent := &recordingReleaseAgent{candidate: CandidateValidation{
		Valid: true, CandidateDigest: Digest{9}, ValidatorVersion: 1, ValidatorBuildID: "build-id", CheckedAt: fixture.clock.Now(), Diagnostics: []CandidateDiagnostic{},
	}}
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: repository, Agent: agent, Clock: fixture.clock,
		Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 7, RequestID: "publish-request"}
	check, err := service.Check(context.Background(), actor, PublishCheckInput{WorkspaceID: workspace.ID, IfMatch: workspace.ETag()})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if check.State != PublishCheckStateValid || check.WorkspaceRevision != workspace.Revision || check.ProductionDigest != workspace.ProductionDigest || check.BaseDigest != workspace.BaseDigest || check.DraftDigest != workspace.DraftDigest || check.CandidateDigest != (Digest{9}) || !check.ExpiresAt.Equal(check.FinishedAt.Add(10*time.Minute)) {
		t.Fatalf("check = %+v", check)
	}
	release, err := service.Queue(context.Background(), actor, QueueReleaseInput{
		WorkspaceID: workspace.ID, CheckID: check.ID, IfMatch: workspace.ETag(), ConfirmName: workspace.Name,
	})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}
	agent.release = successfulAgentRelease(release, fixture.clock.Now())
	if err := service.Run(context.Background(), release.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	stored, err := repository.Release(context.Background(), release.ID)
	if err != nil || stored.State != ReleaseStateSucceeded || stored.Stage != ReleaseStageCommitted || stored.BackupID == "" {
		t.Fatalf("stored release = %+v, err = %v", stored, err)
	}
	published, err := fixture.repository.Workspace(context.Background(), workspace.ID)
	if err != nil || published.State != StatePublished || published.LastReleaseID != release.ID {
		t.Fatalf("published workspace = %+v, err = %v", published, err)
	}
}

func TestReleaseServiceMirrorsDurableAgentProgressBeforeExecutionCompletes(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Live progress")
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
		Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = mutation.Workspace
	repository := newMemoryReleaseRepository()
	agent := &progressiveReleaseAgent{
		candidate: CandidateValidation{
			Valid: true, CandidateDigest: Digest{9}, ValidatorVersion: 1,
			ValidatorBuildID: "build-id", CheckedAt: fixture.clock.Now(), Diagnostics: []CandidateDiagnostic{},
		},
		started: make(chan struct{}), finish: make(chan struct{}), polled: make(chan struct{}),
	}
	var finishOnce sync.Once
	finishAgent := func() { finishOnce.Do(func() { close(agent.finish) }) }
	t.Cleanup(finishAgent)
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: repository, Agent: agent, Clock: fixture.clock, Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 7, RequestID: "publish-request"}
	check, err := service.Check(context.Background(), actor, PublishCheckInput{WorkspaceID: workspace.ID, IfMatch: workspace.ETag()})
	if err != nil {
		t.Fatal(err)
	}
	release, err := service.Queue(context.Background(), actor, QueueReleaseInput{
		WorkspaceID: workspace.ID, CheckID: check.ID, IfMatch: workspace.ETag(), ConfirmName: workspace.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.release = successfulAgentRelease(release, fixture.clock.Now())
	agent.progress = agent.release
	agent.progress.State = ReleaseStateRunning
	agent.progress.Stage = ReleaseStageBackupCreating
	agent.progress.Backup = BackupEvidence{}
	agent.progress.Stages = agent.progress.Stages[:2]
	agent.progress.FinishedAt = time.Time{}

	completed := make(chan error, 1)
	go func() { completed <- service.Run(context.Background(), release.ID) }()
	select {
	case <-agent.started:
	case <-time.After(time.Second):
		t.Fatal("Agent execution did not start")
	}
	select {
	case <-agent.polled:
	case <-time.After(2 * time.Second):
		t.Fatal("durable Agent progress was not polled")
	}
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	mirrored := false
	for !mirrored {
		select {
		case <-deadline.C:
			t.Fatal("Agent progress was not mirrored before execution completed")
		case <-ticker.C:
			stages, stageErr := repository.ReleaseStages(context.Background(), release.ID, 0, releaseStageLimit)
			if stageErr != nil {
				t.Fatal(stageErr)
			}
			mirrored = len(stages) == 3 && stages[2].Stage == ReleaseStageBackupCreating
		}
	}
	deadline.Stop()
	ticker.Stop()
	finishAgent()
	select {
	case runErr := <-completed:
		if runErr != nil {
			t.Fatalf("Run() error = %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("release did not finish after Agent execution completed")
	}
	stored, err := repository.Release(context.Background(), release.ID)
	if err != nil || stored.State != ReleaseStateSucceeded || stored.Stage != ReleaseStageCommitted {
		t.Fatalf("stored release = %+v, error = %v", stored, err)
	}
}

func TestReleaseServiceRejectsExpiredOrChangedCheckBeforeAgentRelease(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Production change")
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
		Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = mutation.Workspace
	repository := newMemoryReleaseRepository()
	agent := &recordingReleaseAgent{candidate: CandidateValidation{
		Valid: true, CandidateDigest: Digest{9}, ValidatorVersion: 1, ValidatorBuildID: "build-id", CheckedAt: fixture.clock.Now(), Diagnostics: []CandidateDiagnostic{},
	}}
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: repository, Agent: agent, Clock: fixture.clock, Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 7, RequestID: "publish-request"}
	check, err := service.Check(context.Background(), actor, PublishCheckInput{WorkspaceID: workspace.ID, IfMatch: workspace.ETag()})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.now = check.ExpiresAt.Add(time.Nanosecond)
	_, err = service.Queue(context.Background(), actor, QueueReleaseInput{
		WorkspaceID: workspace.ID, CheckID: check.ID, IfMatch: workspace.ETag(), ConfirmName: workspace.Name,
	})
	if !errors.Is(err, ErrPublishCheckExpired) || agent.releaseCalls != 0 {
		t.Fatalf("Queue() error = %v, release calls = %d", err, agent.releaseCalls)
	}
}

func TestReleaseServiceReportsProductionChangeBeforeCandidateValidation(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Concurrent production")
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
		Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = mutation.Workspace
	writeExistingFixtureFile(
		t,
		filepath.Join(fixture.production.productionRoot, "conf.d", "site.conf"),
		"server { listen 9090; }\n",
		0o640,
	)
	agent := &recordingReleaseAgent{}
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: newMemoryReleaseRepository(), Agent: agent,
		Clock: fixture.clock, Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Check(context.Background(), Actor{UserID: 7, RequestID: "publish-request"}, PublishCheckInput{
		WorkspaceID: workspace.ID, IfMatch: workspace.ETag(),
	})
	if !errors.Is(err, ErrProductionChanged) || agent.candidateCalls != 0 {
		t.Fatalf("Check() error = %v, candidate calls = %d", err, agent.candidateCalls)
	}
}

func TestReleaseServiceRejectsUnchangedWorkspaceBeforeCandidateValidation(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "No changes")
	if err != nil {
		t.Fatal(err)
	}
	repository := newMemoryReleaseRepository()
	agent := &recordingReleaseAgent{}
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: repository, Agent: agent, Clock: fixture.clock, Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Check(context.Background(), Actor{UserID: 7, RequestID: "publish-request"}, PublishCheckInput{
		WorkspaceID: workspace.ID, IfMatch: workspace.ETag(),
	})
	if !errors.Is(err, ErrNoChanges) || agent.candidateCalls != 0 {
		t.Fatalf("Check() error = %v, candidate calls = %d", err, agent.candidateCalls)
	}
}

func TestReleaseServicePersistsFailedCheckWhenAgentValidationFailsOperationally(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Failed check")
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
		Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = mutation.Workspace
	repository := newMemoryReleaseRepository()
	agentErr := errors.New("agent validation unavailable")
	agent := &recordingReleaseAgent{candidateErr: agentErr}
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: repository, Agent: agent, Clock: fixture.clock, Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}

	check, err := service.Check(context.Background(), Actor{UserID: 7, RequestID: "publish-request"}, PublishCheckInput{
		WorkspaceID: workspace.ID, IfMatch: workspace.ETag(),
	})
	if !errors.Is(err, agentErr) || check.State != PublishCheckStateFailed ||
		check.ValidatorVersion != unavailableValidatorVersion || check.ValidatorBuildID != unavailableValidatorBuildID {
		t.Fatalf("Check() = %+v, %v", check, err)
	}
	stored, readErr := repository.PublishCheck(context.Background(), check.ID)
	if readErr != nil || stored.State != PublishCheckStateFailed || stored.ValidatorBuildID != unavailableValidatorBuildID {
		t.Fatalf("stored check = %+v, %v", stored, readErr)
	}
}

func TestReleaseServicePersistsCandidateDiagnosticsWithPublicSnakeCaseShape(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Invalid candidate")
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
		Path: "conf.d/site.conf", Content: []byte("server { listen broken; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = mutation.Workspace
	agent := &recordingReleaseAgent{candidate: CandidateValidation{
		Valid: false, CandidateDigest: Digest{9}, ValidatorVersion: 1, ValidatorBuildID: "build-id", CheckedAt: fixture.clock.Now(),
		Diagnostics: []CandidateDiagnostic{{Code: "syntax_error", Path: "conf.d/site.conf", Line: 1, Summary: "配置语法无效"}},
	}}
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: newMemoryReleaseRepository(), Agent: agent,
		Clock: fixture.clock, Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}

	check, err := service.Check(context.Background(), Actor{UserID: 7, RequestID: "publish-request"}, PublishCheckInput{
		WorkspaceID: workspace.ID, IfMatch: workspace.ETag(),
	})
	if !errors.Is(err, ErrCandidateInvalid) || !strings.Contains(
		check.PublicDetailsJSON,
		`{"diagnostics":[{"code":"syntax_error","path":"conf.d/site.conf","line":1,"summary":"配置语法无效"}]}`,
	) {
		t.Fatalf("Check() details/error = %s/%v", check.PublicDetailsJSON, err)
	}
}

func TestReleaseServiceReconcilesInterruptedAgentTransaction(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Production change")
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
		Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = mutation.Workspace
	repository := newMemoryReleaseRepository()
	agent := &recordingReleaseAgent{candidate: CandidateValidation{
		Valid: true, CandidateDigest: Digest{9}, ValidatorVersion: 1, ValidatorBuildID: "build-id", CheckedAt: fixture.clock.Now(),
	}}
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: repository, Agent: agent, Clock: fixture.clock, Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 7, RequestID: "publish-request"}
	check, err := service.Check(context.Background(), actor, PublishCheckInput{WorkspaceID: workspace.ID, IfMatch: workspace.ETag()})
	if err != nil {
		t.Fatal(err)
	}
	release, err := service.Queue(context.Background(), actor, QueueReleaseInput{
		WorkspaceID: workspace.ID, CheckID: check.ID, IfMatch: workspace.ETag(), ConfirmName: workspace.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	running := release
	running.State = ReleaseStateRunning
	running.Stage = ReleaseStageRechecking
	running.UpdatedAt = fixture.clock.Now()
	if err := repository.TransitionRelease(context.Background(), release.State, release.Stage, running, ReleaseStage{
		ReleaseID: release.ID, Sequence: 2, Stage: ReleaseStageRechecking, Result: StageResultRunning,
		PublicDetailsJSON: "{}", OccurredAt: fixture.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	agent.recovery = successfulAgentRelease(release, fixture.clock.Now())

	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	stored, err := repository.Release(context.Background(), release.ID)
	if err != nil || stored.State != ReleaseStateSucceeded || stored.Stage != ReleaseStageCommitted || agent.recoveryCalls != 1 {
		t.Fatalf("stored = %+v, error = %v, recovery calls = %d", stored, err, agent.recoveryCalls)
	}
	published, err := fixture.repository.Workspace(context.Background(), workspace.ID)
	if err != nil || published.State != StatePublished || published.LastReleaseID != release.ID {
		t.Fatalf("published workspace = %+v, error = %v", published, err)
	}
}

func TestReleaseServiceFailsQueuedTaskDuringStartupReconciliation(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Production change")
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
		Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = mutation.Workspace
	repository := newMemoryReleaseRepository()
	agent := &recordingReleaseAgent{candidate: CandidateValidation{
		Valid: true, CandidateDigest: Digest{9}, ValidatorVersion: 1, ValidatorBuildID: "build-id", CheckedAt: fixture.clock.Now(),
	}}
	service, err := NewReleaseService(ReleaseDependencies{
		Workspaces: fixture.service, Repository: repository, Agent: agent, Clock: fixture.clock, Random: &incrementingReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{UserID: 7, RequestID: "publish-request"}
	check, err := service.Check(context.Background(), actor, PublishCheckInput{WorkspaceID: workspace.ID, IfMatch: workspace.ETag()})
	if err != nil {
		t.Fatal(err)
	}
	release, err := service.Queue(context.Background(), actor, QueueReleaseInput{
		WorkspaceID: workspace.ID, CheckID: check.ID, IfMatch: workspace.ETag(), ConfirmName: workspace.Name,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	stored, err := repository.Release(context.Background(), release.ID)
	if err != nil || stored.State != ReleaseStateFailed || stored.Stage != ReleaseStageFailed || agent.recoveryCalls != 0 {
		t.Fatalf("stored = %+v, error = %v, recovery calls = %d", stored, err, agent.recoveryCalls)
	}
}

func TestReleaseServiceFailsClosedWhenTerminalBackupEvidenceCannotBeIndexed(t *testing.T) {
	putBackupErr := errors.New("persist backup index")
	for _, testCase := range []struct {
		name            string
		mutateResult    func(*ReleaseExecutionResult)
		putBackupErr    error
		wantCode        string
		wantPersistence bool
	}{
		{
			name: "terminal evidence omits verified backup",
			mutateResult: func(result *ReleaseExecutionResult) {
				result.Backup = BackupEvidence{}
			},
			wantCode: "agent_evidence_invalid",
		},
		{
			name:            "verified backup index cannot be persisted",
			mutateResult:    func(*ReleaseExecutionResult) {},
			putBackupErr:    putBackupErr,
			wantCode:        "backup_index_persistence_failed",
			wantPersistence: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "create-request"}, "Backup evidence")
			if err != nil {
				t.Fatal(err)
			}
			mutation, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "edit-request"}, workspace.ID, ReplaceFileInput{
				Path: "conf.d/site.conf", Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
			})
			if err != nil {
				t.Fatal(err)
			}
			workspace = mutation.Workspace
			repository := newMemoryReleaseRepository()
			repository.putBackupErr = testCase.putBackupErr
			agent := &recordingReleaseAgent{candidate: CandidateValidation{
				Valid: true, CandidateDigest: Digest{9}, ValidatorVersion: 1,
				ValidatorBuildID: "build-id", CheckedAt: fixture.clock.Now(), Diagnostics: []CandidateDiagnostic{},
			}}
			service, err := NewReleaseService(ReleaseDependencies{
				Workspaces: fixture.service, Repository: repository, Agent: agent,
				Clock: fixture.clock, Random: &incrementingReader{},
			})
			if err != nil {
				t.Fatal(err)
			}
			actor := Actor{UserID: 7, RequestID: "publish-request"}
			check, err := service.Check(context.Background(), actor, PublishCheckInput{WorkspaceID: workspace.ID, IfMatch: workspace.ETag()})
			if err != nil {
				t.Fatal(err)
			}
			release, err := service.Queue(context.Background(), actor, QueueReleaseInput{
				WorkspaceID: workspace.ID, CheckID: check.ID, IfMatch: workspace.ETag(), ConfirmName: workspace.Name,
			})
			if err != nil {
				t.Fatal(err)
			}
			agent.release = successfulAgentRelease(release, fixture.clock.Now())
			testCase.mutateResult(&agent.release)

			runErr := service.Run(context.Background(), release.ID)
			if testCase.wantPersistence != errors.Is(runErr, putBackupErr) {
				t.Fatalf("Run() error = %v, want persistence error = %t", runErr, testCase.wantPersistence)
			}
			stored, err := repository.Release(context.Background(), release.ID)
			if err != nil || stored.State != ReleaseStateNeedsAttention || stored.Stage != ReleaseStageNeedsAttention || stored.LastErrorCode != testCase.wantCode {
				t.Fatalf("stored release = %+v, error = %v", stored, err)
			}
			blocked, err := fixture.repository.Workspace(context.Background(), workspace.ID)
			if err != nil || blocked.State != StateNeedsAttention {
				t.Fatalf("workspace = %+v, error = %v", blocked, err)
			}
		})
	}
}

type recordingReleaseAgent struct {
	candidate      CandidateValidation
	candidateErr   error
	release        ReleaseExecutionResult
	recovery       ReleaseExecutionResult
	candidateCalls int
	releaseCalls   int
	recoveryCalls  int
}

type progressiveReleaseAgent struct {
	candidate CandidateValidation
	progress  ReleaseExecutionResult
	release   ReleaseExecutionResult
	started   chan struct{}
	finish    chan struct{}
	polled    chan struct{}
	startOnce sync.Once
	pollOnce  sync.Once
}

func (a *progressiveReleaseAgent) ValidateCandidate(_ context.Context, _ string, _ CandidateValidationRequest) (CandidateValidation, error) {
	return a.candidate, nil
}

func (a *progressiveReleaseAgent) ExecuteRelease(_ context.Context, _ string, _ ReleaseExecutionRequest) (ReleaseExecutionResult, error) {
	a.startOnce.Do(func() { close(a.started) })
	<-a.finish
	return a.release, nil
}

func (a *progressiveReleaseAgent) ReleaseProgress(_ context.Context, _ string, _ ReleaseExecutionRequest) (ReleaseExecutionResult, error) {
	a.pollOnce.Do(func() { close(a.polled) })
	return a.progress, nil
}

func (a *progressiveReleaseAgent) RecoverRelease(_ context.Context, _ string, _ ReleaseExecutionRequest) (ReleaseExecutionResult, error) {
	return ReleaseExecutionResult{}, errors.New("unexpected release recovery")
}

func (a *recordingReleaseAgent) ValidateCandidate(_ context.Context, _ string, _ CandidateValidationRequest) (CandidateValidation, error) {
	a.candidateCalls++
	return a.candidate, a.candidateErr
}

func (a *recordingReleaseAgent) ExecuteRelease(_ context.Context, _ string, _ ReleaseExecutionRequest) (ReleaseExecutionResult, error) {
	a.releaseCalls++
	return a.release, nil
}

func (a *recordingReleaseAgent) ReleaseProgress(_ context.Context, _ string, _ ReleaseExecutionRequest) (ReleaseExecutionResult, error) {
	return a.release, nil
}

func (a *recordingReleaseAgent) RecoverRelease(_ context.Context, _ string, _ ReleaseExecutionRequest) (ReleaseExecutionResult, error) {
	a.recoveryCalls++
	return a.recovery, nil
}

func successfulAgentRelease(release Release, now time.Time) ReleaseExecutionResult {
	names := []ReleaseStageName{
		ReleaseStageRechecking, ReleaseStageBackupCreating, ReleaseStageBackupVerified, ReleaseStageCandidateValidated,
		ReleaseStageFilesApplying, ReleaseStageFilesApplied, ReleaseStageProductionValidated,
		ReleaseStageReloadRequested, ReleaseStageRuntimeConfirmed, ReleaseStageCommitted,
	}
	stages := make([]ReleaseStage, 0, len(names))
	for index, name := range names {
		stages = append(stages, ReleaseStage{ReleaseID: release.ID, Sequence: uint64(index + 1), Stage: name, Result: StageResultSuccess, PublicDetailsJSON: "{}", OccurredAt: now.Add(time.Duration(index) * time.Second)})
	}
	return ReleaseExecutionResult{
		ReleaseID: release.ID, State: ReleaseStateSucceeded, Stage: ReleaseStageCommitted,
		Backup: BackupEvidence{BackupID: release.BackupID, ReleaseID: release.ID, ProductionDigest: release.ProductionDigest, TreeDigest: Digest{8}, EntryCount: 2, TotalBytes: 10, VerifiedAt: now},
		Stages: stages, MasterPID: 10, WorkerCount: 1, HTTPStatus: 204, FinishedAt: now.Add(10 * time.Second),
	}
}

type memoryReleaseRepository struct {
	mu                          sync.RWMutex
	checks                      map[PublishCheckID]PublishCheck
	releases                    map[ReleaseID]Release
	stages                      map[ReleaseID][]ReleaseStage
	backups                     map[BackupID]Backup
	requireBackupBeforeTerminal bool
	putBackupErr                error
}

func newMemoryReleaseRepository() *memoryReleaseRepository {
	return &memoryReleaseRepository{checks: make(map[PublishCheckID]PublishCheck), releases: make(map[ReleaseID]Release), stages: make(map[ReleaseID][]ReleaseStage), backups: make(map[BackupID]Backup)}
}

func (r *memoryReleaseRepository) CreatePublishCheck(_ context.Context, check PublishCheck) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[check.ID] = check
	return nil
}
func (r *memoryReleaseRepository) PublishCheck(_ context.Context, id PublishCheckID) (PublishCheck, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	check, ok := r.checks[id]
	if !ok {
		return PublishCheck{}, sql.ErrNoRows
	}
	return check, nil
}
func (r *memoryReleaseRepository) CreateRelease(_ context.Context, release Release, stage ReleaseStage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, current := range r.releases {
		if current.State == ReleaseStateQueued || current.State == ReleaseStateRunning || current.State == ReleaseStateRollingBack {
			return ErrReleaseInProgress
		}
	}
	r.releases[release.ID] = release
	r.stages[release.ID] = []ReleaseStage{stage}
	return nil
}
func (r *memoryReleaseRepository) TransitionRelease(_ context.Context, expectedState ReleaseState, expectedStage ReleaseStageName, next Release, stage ReleaseStage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.releases[next.ID]
	if !ok {
		return sql.ErrNoRows
	}
	if current.State != expectedState || current.Stage != expectedStage || stage.Sequence != uint64(len(r.stages[next.ID])+1) {
		return ErrConflict
	}
	if r.requireBackupBeforeTerminal && (next.State == ReleaseStateSucceeded || next.State == ReleaseStateRolledBack || next.State == ReleaseStateNeedsAttention) {
		if _, found := r.backups[next.BackupID]; !found {
			return errors.New("terminal release persisted before backup index")
		}
	}
	r.releases[next.ID] = next
	r.stages[next.ID] = append(r.stages[next.ID], stage)
	return nil
}
func (r *memoryReleaseRepository) Release(_ context.Context, id ReleaseID) (Release, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	release, ok := r.releases[id]
	if !ok {
		return Release{}, sql.ErrNoRows
	}
	return release, nil
}
func (r *memoryReleaseRepository) ActiveRelease(_ context.Context) (Release, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, release := range r.releases {
		if release.State == ReleaseStateQueued || release.State == ReleaseStateRunning || release.State == ReleaseStateRollingBack {
			return release, nil
		}
	}
	return Release{}, fs.ErrNotExist
}
func (r *memoryReleaseRepository) ReleaseStages(_ context.Context, id ReleaseID, after uint64, limit int) ([]ReleaseStage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := r.stages[id]
	result := make([]ReleaseStage, 0)
	for _, stage := range all {
		if stage.Sequence > after && len(result) < limit {
			result = append(result, stage)
		}
	}
	return result, nil
}
func (r *memoryReleaseRepository) PutBackup(_ context.Context, backup Backup) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.putBackupErr != nil {
		return r.putBackupErr
	}
	r.backups[backup.ID] = backup
	return nil
}
func (r *memoryReleaseRepository) Backup(_ context.Context, id BackupID) (Backup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	backup, ok := r.backups[id]
	if !ok {
		return Backup{}, sql.ErrNoRows
	}
	return backup, nil
}
