/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"time"
)

const (
	publishCheckLifetime          = 10 * time.Minute
	releaseStageLimit             = 512
	releaseProgressPollInterval   = 200 * time.Millisecond
	releaseProgressRequestTimeout = 2 * time.Second
	unavailableValidatorVersion   = uint16(1)
	unavailableValidatorBuildID   = "unavailable"
)

// ReleaseDependencies are the exact capabilities required by the publish coordinator.
type ReleaseDependencies struct {
	Workspaces *Service
	Repository ReleaseRepository
	Agent      ReleaseAgent
	Clock      Clock
	Random     io.Reader
}

// ReleaseService coordinates digest-bound checks and persists Agent transaction evidence.
type ReleaseService struct {
	workspaces *Service
	repository ReleaseRepository
	agent      ReleaseAgent
	clock      Clock
	random     io.Reader
}

type publicCandidateDiagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Summary string `json:"summary"`
}

// NewReleaseService validates explicit release dependencies.
func NewReleaseService(dependencies ReleaseDependencies) (*ReleaseService, error) {
	if dependencies.Workspaces == nil || dependencies.Repository == nil || dependencies.Agent == nil || dependencies.Clock == nil || dependencies.Random == nil {
		return nil, errors.New("create release service: dependencies are required")
	}
	return &ReleaseService{
		workspaces: dependencies.Workspaces, repository: dependencies.Repository,
		agent: dependencies.Agent, clock: dependencies.Clock, random: dependencies.Random,
	}, nil
}

// Check validates a complete candidate and persists expiring digest-bound evidence.
func (s *ReleaseService) Check(ctx context.Context, actor Actor, input PublishCheckInput) (_ PublishCheck, returnErr error) {
	if ctx == nil || s == nil {
		return PublishCheck{}, errors.New("check release candidate: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return PublishCheck{}, err
	}
	if _, err := ParseWorkspaceID(string(input.WorkspaceID)); err != nil {
		return PublishCheck{}, err
	}
	review, err := s.workspaces.openReviewWorkspace(ctx, input.WorkspaceID, true)
	if err != nil {
		return PublishCheck{}, fmt.Errorf("check release candidate: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, closeReviewWorkspace(review)) }()
	if err := requirePublishableWorkspaceState(review.workspace.State); err != nil {
		return PublishCheck{}, err
	}
	if review.workspace.BaseDigest == review.workspace.DraftDigest {
		return PublishCheck{}, ErrNoChanges
	}
	if err := requireWorkspaceETag(input.IfMatch, review.workspace); err != nil {
		return PublishCheck{}, err
	}
	production, err := s.workspaces.production.ConfigDigest(ctx, actor.RequestID)
	if err != nil {
		return PublishCheck{}, fmt.Errorf("read production digest for release check: %w", err)
	}
	if production.Digest != review.workspace.ProductionDigest {
		return PublishCheck{}, ErrProductionChanged
	}
	startedAt := s.clock.Now().UTC()
	validation, validationErr := s.agent.ValidateCandidate(ctx, actor.RequestID, CandidateValidationRequest{
		WorkspaceID: input.WorkspaceID, ProductionDigest: review.workspace.ProductionDigest, DraftDigest: review.workspace.DraftDigest,
	})
	finishedAt := s.clock.Now().UTC()
	checkID, err := NewPublishCheckID(s.random)
	if err != nil {
		return PublishCheck{}, err
	}
	state := PublishCheckStateValid
	if validationErr != nil && !errors.Is(validationErr, ErrCandidateInvalid) {
		state = PublishCheckStateFailed
		if validation.ValidatorVersion == 0 {
			validation.ValidatorVersion = unavailableValidatorVersion
		}
		if validation.ValidatorBuildID == "" {
			validation.ValidatorBuildID = unavailableValidatorBuildID
		}
	} else if !validation.Valid {
		state = PublishCheckStateInvalid
	}
	publicDiagnostics := make([]publicCandidateDiagnostic, 0, len(validation.Diagnostics))
	for _, diagnostic := range validation.Diagnostics {
		publicDiagnostics = append(publicDiagnostics, publicCandidateDiagnostic{
			Code: diagnostic.Code, Path: string(diagnostic.Path), Line: diagnostic.Line, Summary: diagnostic.Summary,
		})
	}
	details, marshalErr := json.Marshal(struct {
		Diagnostics []publicCandidateDiagnostic `json:"diagnostics"`
	}{Diagnostics: publicDiagnostics})
	if marshalErr != nil {
		return PublishCheck{}, marshalErr
	}
	check := PublishCheck{
		ID: checkID, WorkspaceID: review.workspace.ID, WorkspaceRevision: review.workspace.Revision,
		ProductionDigest: review.workspace.ProductionDigest, BaseDigest: review.workspace.BaseDigest,
		DraftDigest: review.workspace.DraftDigest, CandidateDigest: validation.CandidateDigest,
		ManifestVersion: review.draftManifest.SchemaVersion, PolicyVersion: review.draftManifest.PolicyVersion,
		ValidatorVersion: validation.ValidatorVersion, ValidatorBuildID: validation.ValidatorBuildID,
		State: state, DiagnosticCount: len(validation.Diagnostics), PublicDetailsJSON: string(details),
		CreatedBy: actor.UserID, RequestID: actor.RequestID, StartedAt: startedAt,
		FinishedAt: finishedAt, ExpiresAt: finishedAt.Add(publishCheckLifetime),
	}
	if err := s.repository.CreatePublishCheck(ctx, check); err != nil {
		return PublishCheck{}, fmt.Errorf("persist publish check: %w", err)
	}
	if validationErr != nil {
		return check, validationErr
	}
	if !validation.Valid {
		return check, ErrCandidateInvalid
	}
	return check, nil
}

// Queue verifies named confirmation and persists the globally serialized queued task.
func (s *ReleaseService) Queue(ctx context.Context, actor Actor, input QueueReleaseInput) (_ Release, returnErr error) {
	if ctx == nil || s == nil {
		return Release{}, errors.New("queue release: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return Release{}, err
	}
	if _, err := ParseWorkspaceID(string(input.WorkspaceID)); err != nil {
		return Release{}, err
	}
	if _, err := ParsePublishCheckID(string(input.CheckID)); err != nil {
		return Release{}, err
	}
	openAttention, err := s.repository.HasOpenAttentionCases(ctx)
	if err != nil {
		return Release{}, fmt.Errorf("check unresolved configuration attention: %w", err)
	}
	if openAttention {
		return Release{}, ErrAttentionUnresolved
	}
	review, err := s.workspaces.openReviewWorkspace(ctx, input.WorkspaceID, true)
	if err != nil {
		return Release{}, fmt.Errorf("queue release: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, closeReviewWorkspace(review)) }()
	if err := requirePublishableWorkspaceState(review.workspace.State); err != nil {
		return Release{}, err
	}
	if input.ConfirmName != review.workspace.Name {
		return Release{}, ErrConflict
	}
	if err := requireWorkspaceETag(input.IfMatch, review.workspace); err != nil {
		return Release{}, err
	}
	check, err := s.repository.PublishCheck(ctx, input.CheckID)
	if err != nil {
		return Release{}, fmt.Errorf("read publish check: %w", err)
	}
	now := s.clock.Now().UTC()
	if check.State != PublishCheckStateValid || !now.Before(check.ExpiresAt) || check.WorkspaceID != review.workspace.ID ||
		check.WorkspaceRevision != review.workspace.Revision || check.ProductionDigest != review.workspace.ProductionDigest ||
		check.BaseDigest != review.workspace.BaseDigest || check.DraftDigest != review.workspace.DraftDigest ||
		check.ManifestVersion != review.draftManifest.SchemaVersion || check.PolicyVersion != review.draftManifest.PolicyVersion {
		return Release{}, ErrPublishCheckExpired
	}
	production, err := s.workspaces.production.ConfigDigest(ctx, actor.RequestID)
	if err != nil {
		return Release{}, fmt.Errorf("read production digest for release queue: %w", err)
	}
	if production.Digest != check.ProductionDigest {
		return Release{}, ErrProductionChanged
	}
	releaseID, err := NewReleaseID(s.random)
	if err != nil {
		return Release{}, err
	}
	backupID, err := NewBackupID(s.random)
	if err != nil {
		return Release{}, err
	}
	release := Release{
		ID: releaseID, WorkspaceID: input.WorkspaceID, CheckID: input.CheckID, BackupID: backupID,
		State: ReleaseStateQueued, Stage: ReleaseStageQueued, ProductionDigest: check.ProductionDigest,
		DraftDigest: check.DraftDigest, CandidateDigest: check.CandidateDigest,
		CreatedBy: actor.UserID, RequestID: actor.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	stage := ReleaseStage{
		ReleaseID: release.ID, Sequence: 1, Stage: ReleaseStageQueued, Result: StageResultPending,
		PublicDetailsJSON: "{}", OccurredAt: now,
	}
	if err := s.repository.CreateRelease(ctx, release, stage); err != nil {
		return Release{}, err
	}
	return release, nil
}

// Run executes one queued Agent transaction and mirrors every durable stage into SQLite.
func (s *ReleaseService) Run(ctx context.Context, id ReleaseID) error {
	if ctx == nil || s == nil {
		return errors.New("run release: service is unavailable")
	}
	release, err := s.repository.Release(ctx, id)
	if err != nil {
		return err
	}
	if release.State != ReleaseStateQueued || release.Stage != ReleaseStageQueued {
		return ErrConflict
	}
	now := s.clock.Now().UTC()
	running := release
	running.State = ReleaseStateRunning
	running.Stage = ReleaseStageRechecking
	running.UpdatedAt = now
	if err := s.repository.TransitionRelease(ctx, release.State, release.Stage, running, ReleaseStage{
		ReleaseID: id, Sequence: 2, Stage: ReleaseStageRechecking, Result: StageResultRunning,
		PublicDetailsJSON: "{}", OccurredAt: now,
	}); err != nil {
		return err
	}
	release = running
	request := releaseExecutionRequest(release)
	requestID := release.RequestID
	type executionOutcome struct {
		result ReleaseExecutionResult
		err    error
	}
	completed := make(chan executionOutcome, 1)
	go func() {
		result, executionErr := s.agent.ExecuteRelease(ctx, requestID, request)
		completed <- executionOutcome{result: result, err: executionErr}
	}()

	ticker := time.NewTicker(releaseProgressPollInterval)
	defer ticker.Stop()
	mirroredAgentStages := 1
	sequence := uint64(3)
	var persistenceErr error
	for {
		select {
		case outcome := <-completed:
			if !validAgentReleaseResult(outcome.result, release, mirroredAgentStages) {
				return s.markReleaseUncertain(ctx, release, sequence, "agent_evidence_invalid", outcome.err)
			}
			if persistenceErr != nil {
				backupErr := s.persistReleaseBackup(ctx, release, outcome.result.Backup)
				return s.markReleaseUncertain(
					ctx, release, sequence, "release_stage_persistence_failed",
					errors.Join(persistenceErr, backupErr, outcome.err),
				)
			}
			return s.mirrorReleaseResult(
				ctx, release, outcome.result, outcome.result.Stages[mirroredAgentStages:], sequence, outcome.err,
			)
		case <-ticker.C:
			if persistenceErr != nil {
				continue
			}
			progressCtx, cancel := context.WithTimeout(ctx, releaseProgressRequestTimeout)
			progress, progressErr := s.agent.ReleaseProgress(progressCtx, requestID, request)
			cancel()
			if progressErr != nil || !validAgentReleaseProgress(progress, release, mirroredAgentStages) {
				continue
			}
			available := nonTerminalAgentStageCount(progress.Stages)
			if available <= mirroredAgentStages {
				continue
			}
			var mirrorErr error
			release, sequence, mirrorErr = s.mirrorReleaseStages(
				ctx, release, progress, progress.Stages[mirroredAgentStages:available], sequence,
			)
			if mirrorErr != nil {
				persistenceErr = mirrorErr
				continue
			}
			mirroredAgentStages = available
		case <-ctx.Done():
			return fmt.Errorf("run release: %w", ctx.Err())
		}
	}
}

// Reconcile resolves the sole active release before the UI begins serving requests.
func (s *ReleaseService) Reconcile(ctx context.Context) error {
	if ctx == nil || s == nil {
		return errors.New("reconcile release: service is unavailable")
	}
	release, err := s.repository.ActiveRelease(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reconcile release: %w", err)
	}
	stages, err := s.repository.ReleaseStages(ctx, release.ID, 0, releaseStageLimit)
	if err != nil || len(stages) == 0 || stages[len(stages)-1].Stage != release.Stage {
		return errors.Join(fmt.Errorf("reconcile release stages: %w", ErrConflict), err)
	}
	if release.State == ReleaseStateQueued && release.Stage == ReleaseStageQueued {
		now := s.clock.Now().UTC()
		next := release
		next.State = ReleaseStateFailed
		next.Stage = ReleaseStageFailed
		next.LastErrorCode = "interrupted_before_agent"
		next.UpdatedAt = now
		next.FinishedAt = now
		return s.repository.TransitionRelease(ctx, release.State, release.Stage, next, ReleaseStage{
			ReleaseID: release.ID, Sequence: uint64(len(stages) + 1), Stage: ReleaseStageFailed,
			Result: StageResultFailed, Code: next.LastErrorCode, PublicDetailsJSON: "{}", OccurredAt: now,
		})
	}
	if release.State != ReleaseStateRunning && release.State != ReleaseStateRollingBack {
		return ErrConflict
	}
	result, recoveryErr := s.agent.RecoverRelease(ctx, release.RequestID, releaseExecutionRequest(release))
	if result.ReleaseID != release.ID || len(result.Stages) == 0 || result.Stages[0].Stage != ReleaseStageRechecking ||
		len(stages) > len(result.Stages)+1 {
		return s.markReleaseUncertain(ctx, release, uint64(len(stages)+1), "agent_recovery_evidence_invalid", recoveryErr)
	}
	for index := 1; index < len(stages); index++ {
		if stages[index].Stage != result.Stages[index-1].Stage {
			return s.markReleaseUncertain(ctx, release, uint64(len(stages)+1), "agent_recovery_stage_mismatch", recoveryErr)
		}
	}
	if !validAgentReleaseResult(result, release, len(stages)-1) {
		return s.markReleaseUncertain(ctx, release, uint64(len(stages)+1), "agent_recovery_evidence_invalid", recoveryErr)
	}
	start := len(stages) - 1
	return s.mirrorReleaseResult(ctx, release, result, result.Stages[start:], uint64(len(stages)+1), nil)
}

func (s *ReleaseService) mirrorReleaseResult(
	ctx context.Context,
	release Release,
	result ReleaseExecutionResult,
	agentStages []ReleaseStage,
	sequence uint64,
	executionErr error,
) error {
	if err := s.persistReleaseBackup(ctx, release, result.Backup); err != nil {
		return s.markReleaseUncertain(ctx, release, sequence, "backup_index_persistence_failed", errors.Join(err, executionErr))
	}
	var err error
	release, sequence, err = s.mirrorReleaseStages(ctx, release, result, agentStages, sequence)
	if err != nil {
		return err
	}
	if release.State != result.State || release.Stage != result.Stage {
		return s.markReleaseUncertain(ctx, release, sequence, "agent_terminal_mismatch", executionErr)
	}
	if err := s.applyWorkspaceReleaseOutcome(ctx, release); err != nil {
		return err
	}
	return executionErr
}

func (s *ReleaseService) persistReleaseBackup(ctx context.Context, release Release, evidence BackupEvidence) error {
	if evidence.BackupID == "" {
		return nil
	}
	if !validReleaseBackupEvidence(evidence, release) {
		return ErrConflict
	}
	return s.repository.PutBackup(ctx, Backup{
		ID: evidence.BackupID, OriginType: BackupOriginRelease, OriginID: string(release.ID),
		ReleaseID: release.ID, ProductionDigest: evidence.ProductionDigest,
		TreeDigest: evidence.TreeDigest, State: BackupStateComplete,
		EntryCount: evidence.EntryCount, TotalBytes: evidence.TotalBytes, BodyPresent: true,
		CreatedAt: release.CreatedAt, VerifiedAt: evidence.VerifiedAt,
	})
}

func (s *ReleaseService) mirrorReleaseStages(
	ctx context.Context,
	release Release,
	result ReleaseExecutionResult,
	agentStages []ReleaseStage,
	sequence uint64,
) (Release, uint64, error) {
	for _, agentStage := range agentStages {
		if sequence > releaseStageLimit {
			return release, sequence, s.markReleaseUncertain(ctx, release, sequence, "stage_limit_exceeded", ErrLimitExceeded)
		}
		next := release
		next.Stage = agentStage.Stage
		next.State = releaseStateForAgentStage(agentStage.Stage)
		if terminalReleaseState(result.State) && agentStage.Sequence == uint64(len(result.Stages)) && agentStage.Stage == result.Stage {
			next.State = result.State
		}
		next.UpdatedAt = agentStage.OccurredAt.UTC()
		next.LastErrorCode = agentStage.Code
		if terminalReleaseState(next.State) {
			next.FinishedAt = result.FinishedAt.UTC()
			next.LastErrorCode = result.ErrorCode
		}
		stage := agentStage
		stage.ReleaseID = release.ID
		stage.Sequence = sequence
		stage.OccurredAt = stage.OccurredAt.UTC()
		if err := s.repository.TransitionRelease(ctx, release.State, release.Stage, next, stage); err != nil {
			return release, sequence, err
		}
		release = next
		sequence++
	}
	return release, sequence, nil
}

func releaseExecutionRequest(release Release) ReleaseExecutionRequest {
	return ReleaseExecutionRequest{
		ReleaseID: release.ID, BackupID: release.BackupID, WorkspaceID: release.WorkspaceID,
		ProductionDigest: release.ProductionDigest, DraftDigest: release.DraftDigest, CandidateDigest: release.CandidateDigest,
	}
}

func requirePublishableWorkspaceState(state WorkspaceState) error {
	switch state {
	case StateReady:
		return nil
	case StateStale:
		return ErrWorkspaceStale
	case StateNeedsAttention:
		return ErrWorkspaceNeedsAttention
	case StatePreparing, StatePublished:
		return ErrConflict
	}
	return ErrConflict
}

// PublishCheck returns persisted candidate evidence.
func (s *ReleaseService) PublishCheck(ctx context.Context, id PublishCheckID) (PublishCheck, error) {
	if ctx == nil || s == nil {
		return PublishCheck{}, errors.New("read publish check: service is unavailable")
	}
	return s.repository.PublishCheck(ctx, id)
}

// Release returns one persisted task projection.
func (s *ReleaseService) Release(ctx context.Context, id ReleaseID) (Release, error) {
	if ctx == nil || s == nil {
		return Release{}, errors.New("read release: service is unavailable")
	}
	return s.repository.Release(ctx, id)
}

// Stages returns bounded durable events after one sequence.
func (s *ReleaseService) Stages(ctx context.Context, id ReleaseID, after uint64) ([]ReleaseStage, error) {
	if ctx == nil || s == nil {
		return nil, errors.New("read release stages: service is unavailable")
	}
	return s.repository.ReleaseStages(ctx, id, after, releaseStageLimit)
}

func releaseStateForAgentStage(stage ReleaseStageName) ReleaseState {
	switch stage {
	case ReleaseStageQueued:
		return ReleaseStateQueued
	case ReleaseStageRechecking, ReleaseStageBackupCreating, ReleaseStageBackupVerified, ReleaseStageCandidateValidated,
		ReleaseStageFilesApplying, ReleaseStageFilesApplied, ReleaseStageProductionValidated,
		ReleaseStageReloadRequested, ReleaseStageRuntimeConfirmed:
		return ReleaseStateRunning
	case ReleaseStageRollbackApplying, ReleaseStageRollbackFilesRestored, ReleaseStageRollbackValidated, ReleaseStageRollbackReloadRequested:
		return ReleaseStateRollingBack
	case ReleaseStageCommitted:
		return ReleaseStateSucceeded
	case ReleaseStageRolledBack:
		return ReleaseStateRolledBack
	case ReleaseStageNeedsAttention:
		return ReleaseStateNeedsAttention
	case ReleaseStageFailed:
		return ReleaseStateFailed
	}
	return ReleaseStateNeedsAttention
}

func validAgentReleaseProgress(result ReleaseExecutionResult, release Release, mirrored int) bool {
	if result.ReleaseID != release.ID || mirrored < 1 || len(result.Stages) < mirrored || len(result.Stages) >= releaseStageLimit ||
		!knownAgentReleaseState(result.State) || agentReleaseStageOrder(result.Stage) < 0 ||
		(result.State != ReleaseStateCancelled && releaseStateForAgentStage(result.Stage) != result.State) ||
		!validAgentReleaseStages(result.Stages, release.ID) {
		return false
	}
	if len(result.Stages) == 0 || result.Stages[0].Stage != ReleaseStageRechecking {
		return false
	}
	if mirrored > 1 && result.Stages[mirrored-1].Stage != release.Stage {
		return false
	}
	return true
}

func validAgentReleaseResult(result ReleaseExecutionResult, release Release, mirrored int) bool {
	if !validAgentReleaseProgress(result, release, mirrored) || !terminalReleaseState(result.State) ||
		result.FinishedAt.IsZero() || result.Stage != result.Stages[len(result.Stages)-1].Stage ||
		nonTerminalAgentStageCount(result.Stages) == len(result.Stages) ||
		(releaseResultRequiresBackup(result) && !validReleaseBackupEvidence(result.Backup, release)) ||
		(result.Backup.BackupID != "" && !validReleaseBackupEvidence(result.Backup, release)) {
		return false
	}
	return true
}

func releaseResultRequiresBackup(result ReleaseExecutionResult) bool {
	switch result.State {
	case ReleaseStateSucceeded, ReleaseStateRolledBack, ReleaseStateNeedsAttention:
		return true
	case ReleaseStateQueued, ReleaseStateRunning, ReleaseStateRollingBack,
		ReleaseStateFailed, ReleaseStateCancelled:
	}
	for _, stage := range result.Stages {
		if stage.Stage == ReleaseStageBackupVerified {
			return true
		}
	}
	return false
}

func validReleaseBackupEvidence(evidence BackupEvidence, release Release) bool {
	return evidence.BackupID == release.BackupID && evidence.ReleaseID == release.ID &&
		evidence.ProductionDigest == release.ProductionDigest && evidence.TreeDigest != (Digest{}) &&
		evidence.EntryCount > 0 && evidence.TotalBytes >= 0 && !evidence.VerifiedAt.IsZero() &&
		evidence.VerifiedAt.Location() == time.UTC
}

func validAgentReleaseStages(stages []ReleaseStage, id ReleaseID) bool {
	lastOrder := -1
	for index, stage := range stages {
		order := agentReleaseStageOrder(stage.Stage)
		if stage.ReleaseID != id || stage.Sequence != uint64(index+1) || order < 0 || order < lastOrder || !knownAgentStageResult(stage.Result) ||
			stage.OccurredAt.IsZero() || !json.Valid([]byte(stage.PublicDetailsJSON)) {
			return false
		}
		if index+1 < len(stages) && terminalReleaseState(releaseStateForAgentStage(stage.Stage)) {
			return false
		}
		lastOrder = order
	}
	return true
}

func knownAgentReleaseState(state ReleaseState) bool {
	switch state {
	case ReleaseStateQueued, ReleaseStateRunning, ReleaseStateRollingBack, ReleaseStateSucceeded,
		ReleaseStateFailed, ReleaseStateRolledBack, ReleaseStateNeedsAttention, ReleaseStateCancelled:
		return true
	}
	return false
}

func knownAgentStageResult(result StageResult) bool {
	switch result {
	case StageResultPending, StageResultRunning, StageResultSuccess, StageResultFailed, StageResultWarning:
		return true
	}
	return false
}

func agentReleaseStageOrder(stage ReleaseStageName) int {
	switch stage {
	case ReleaseStageQueued:
		return 0
	case ReleaseStageRechecking:
		return 1
	case ReleaseStageBackupCreating:
		return 2
	case ReleaseStageBackupVerified:
		return 3
	case ReleaseStageCandidateValidated:
		return 4
	case ReleaseStageFilesApplying:
		return 5
	case ReleaseStageFilesApplied:
		return 6
	case ReleaseStageProductionValidated:
		return 7
	case ReleaseStageReloadRequested:
		return 8
	case ReleaseStageRuntimeConfirmed:
		return 9
	case ReleaseStageCommitted:
		return 10
	case ReleaseStageRollbackApplying:
		return 20
	case ReleaseStageRollbackFilesRestored:
		return 21
	case ReleaseStageRollbackValidated:
		return 22
	case ReleaseStageRollbackReloadRequested:
		return 23
	case ReleaseStageRolledBack:
		return 24
	case ReleaseStageFailed:
		return 30
	case ReleaseStageNeedsAttention:
		return 31
	}
	return -1
}

func nonTerminalAgentStageCount(stages []ReleaseStage) int {
	count := len(stages)
	if count > 0 && terminalReleaseState(releaseStateForAgentStage(stages[count-1].Stage)) {
		return count - 1
	}
	return count
}

func terminalReleaseState(state ReleaseState) bool {
	switch state {
	case ReleaseStateSucceeded, ReleaseStateFailed, ReleaseStateRolledBack, ReleaseStateNeedsAttention, ReleaseStateCancelled:
		return true
	case ReleaseStateQueued, ReleaseStateRunning, ReleaseStateRollingBack:
		return false
	}
	return false
}

func (s *ReleaseService) markReleaseUncertain(ctx context.Context, release Release, sequence uint64, code string, primary error) error {
	now := s.clock.Now().UTC()
	next := release
	next.State = ReleaseStateNeedsAttention
	next.Stage = ReleaseStageNeedsAttention
	next.LastErrorCode = code
	next.UpdatedAt = now
	next.FinishedAt = now
	err := s.repository.TransitionRelease(ctx, release.State, release.Stage, next, ReleaseStage{
		ReleaseID: release.ID, Sequence: sequence, Stage: next.Stage, Result: StageResultFailed,
		Code: code, PublicDetailsJSON: "{}", OccurredAt: now,
	})
	if err == nil {
		err = s.applyWorkspaceReleaseOutcome(ctx, next)
	}
	return errors.Join(primary, err)
}

func (s *ReleaseService) applyWorkspaceReleaseOutcome(ctx context.Context, release Release) (returnErr error) {
	var state WorkspaceState
	var reason string
	switch release.State {
	case ReleaseStateSucceeded:
		state = StatePublished
	case ReleaseStateNeedsAttention:
		state = StateNeedsAttention
		reason = "CONFIG_RELEASE_NEEDS_ATTENTION"
	case ReleaseStateRolledBack, ReleaseStateFailed, ReleaseStateCancelled:
		return nil
	case ReleaseStateQueued, ReleaseStateRunning, ReleaseStateRollingBack:
		return ErrConflict
	}
	workspace, err := s.workspaces.reader.Workspace(ctx, release.WorkspaceID)
	if err != nil {
		return err
	}
	if (release.State == ReleaseStateSucceeded && workspace.State == StatePublished && workspace.LastReleaseID == release.ID) ||
		(release.State == ReleaseStateNeedsAttention && workspace.State == StateNeedsAttention) {
		return nil
	}
	root, err := OpenScopedRoot(filepath.Join(s.workspaces.workspaceRoot, string(workspace.ID)))
	if err != nil {
		return err
	}
	lock, err := AcquireWorkspaceLock(ctx, root, LockExclusive)
	if err != nil {
		return errors.Join(err, root.Close())
	}
	defer func() {
		returnErr = errors.Join(returnErr, lock.Close(), root.Close())
	}()
	if workspace.State != StateReady || workspace.DraftDigest != release.DraftDigest || workspace.ProductionDigest != release.ProductionDigest {
		return ErrConflict
	}
	next := workspace
	next.State = state
	next.StateReasonCode = reason
	next.Revision++
	next.UpdatedAt = s.clock.Now().UTC()
	if state == StatePublished {
		next.LastReleaseID = release.ID
	}
	control := ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: next.ID, State: next.State,
		StateReasonCode: next.StateReasonCode, Revision: next.Revision, UpdatedAt: next.UpdatedAt,
	}
	if err := WriteControlState(ctx, root, control); err != nil {
		return err
	}
	before := workspace.DraftDigest
	after := next.DraftDigest
	details, _ := json.Marshal(struct {
		ReleaseID ReleaseID      `json:"release_id"`
		State     WorkspaceState `json:"state"`
	}{ReleaseID: release.ID, State: state})
	change := WorkspaceChange{
		ExpectedRevision: workspace.Revision, Next: next,
		Operation: OperationRecord{
			ID: string(release.ID), ObjectType: workspaceObjectType, ObjectID: string(workspace.ID),
			Action: "config.workspace.release", BeforeDigest: &before, AfterDigest: &after,
			Result: operationResultSuccess, RequestID: release.RequestID, OccurredAt: next.UpdatedAt,
		},
	}
	change.Audit = AuditEvent{
		OperationID: change.Operation.ID, OccurredAt: next.UpdatedAt, ActorUserID: release.CreatedBy,
		Action: change.Operation.Action, ObjectType: change.Operation.ObjectType, ObjectID: change.Operation.ObjectID,
		Result: change.Operation.Result, RequestID: release.RequestID, DetailsJSON: string(details),
	}
	if err := s.workspaces.writer.UpdateWorkspace(ctx, change); err != nil {
		restoreErr := WriteControlState(context.WithoutCancel(ctx), root, controlStateFromWorkspace(workspace))
		return errors.Join(err, restoreErr)
	}
	return nil
}
