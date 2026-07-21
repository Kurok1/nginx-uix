/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/kuroky/nginx-uix/internal/config"
)

const activeRunLimit = 8

// WorkspaceStore supplies one verified immutable workspace draft.
type WorkspaceStore interface {
	DraftSnapshot(context.Context, config.WorkspaceID) (config.DraftSnapshot, error)
}

// Repository owns durable Route Lab state and immutable stage transactions.
type Repository interface {
	CreateRouteRun(context.Context, Run, RunStage) error
	TransitionRouteRun(context.Context, RunState, RunStageName, Run, RunStage) error
	RouteRun(context.Context, RunID) (Run, error)
	RouteRunStages(context.Context, RunID, uint64, int) ([]RunStage, error)
	ListRouteRuns(context.Context, HistoryQuery) ([]Run, error)
	ActiveRouteRunCount(context.Context) (int, error)
	RequestRouteRunCancellation(context.Context, RunID, time.Time) (Run, error)
	FailInterruptedRouteRuns(context.Context, time.Time, string) (int, error)
}

// Agent executes one fixed-root isolated route request.
type Agent interface {
	ExecuteRouteTest(context.Context, AgentRequest) (AgentResult, error)
}

// ServiceOptions are explicit stable dependencies for Route Lab orchestration.
type ServiceOptions struct {
	Workspaces  WorkspaceStore
	Repository  Repository
	Agent       Agent
	TokenSource io.Reader
	Now         func() time.Time
}

// Service coordinates static analysis, durable state and the typed local Agent.
type Service struct {
	workspaces WorkspaceStore
	repository Repository
	agent      Agent
	tokens     io.Reader
	now        func() time.Time
}

// QueuedRun keeps the validated secret-bearing request in memory only until the task owner starts it.
type QueuedRun struct {
	Run     Run
	Request ValidatedRequest
}

// NewService creates a Route Lab domain service with no implicit global dependencies.
func NewService(options ServiceOptions) (*Service, error) {
	if options.Workspaces == nil || options.Repository == nil || options.Agent == nil {
		return nil, fmt.Errorf("create route lab service: dependencies are required")
	}
	if options.TokenSource == nil {
		options.TokenSource = rand.Reader
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{
		workspaces: options.Workspaces, repository: options.Repository,
		agent: options.Agent, tokens: options.TokenSource, now: options.Now,
	}, nil
}

// Analyze returns a pure static explanation bound to the exact current workspace ETag.
func (service *Service) Analyze(
	ctx context.Context,
	workspaceID config.WorkspaceID,
	ifMatch string,
	request Request,
) (Analysis, error) {
	// Static analysis does not execute the request and therefore never consumes a side-effect confirmation.
	request.Confirmation = SideEffectConfirmation
	_, _, analysis, err := service.prepare(ctx, workspaceID, ifMatch, request)
	return analysis, err
}

// Queue validates, analyzes and persists one secret-free queued task.
func (service *Service) Queue(
	ctx context.Context,
	workspaceID config.WorkspaceID,
	ifMatch string,
	request Request,
	actor config.Actor,
) (QueuedRun, error) {
	if service == nil || actor.UserID <= 0 || !validPublicRequestID(actor.RequestID) {
		return QueuedRun{}, ErrInvalidRequest
	}
	snapshot, validated, analysis, err := service.prepare(ctx, workspaceID, ifMatch, request)
	if err != nil {
		return QueuedRun{}, err
	}
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		return QueuedRun{}, err
	}
	if ProjectMayContactUpstream(project) {
		if request.Confirmation != SideEffectConfirmation {
			return QueuedRun{}, ErrConfirmationRequired
		}
		validated.SideEffecting = true
		validated.UpstreamSideEffect = true
	}
	if _, err := BuildRouteDefinitions(project); err != nil {
		return QueuedRun{}, err
	}
	active, err := service.repository.ActiveRouteRunCount(ctx)
	if err != nil {
		return QueuedRun{}, fmt.Errorf("count route runs: %w", err)
	}
	if active >= activeRunLimit {
		return QueuedRun{}, ErrBusy
	}
	runID, err := service.newRunID()
	if err != nil {
		return QueuedRun{}, err
	}
	safeRequest := NewSafeRequest(validated)
	safeRequestJSON, err := boundedJSON(safeRequest, 131072)
	if err != nil {
		return QueuedRun{}, err
	}
	if analysis.Servers == nil {
		analysis.Servers = []ServerCandidate{}
	}
	if analysis.Locations == nil {
		analysis.Locations = []LocationCandidate{}
	}
	analysisJSON, err := boundedJSON(analysis, 2097152)
	if err != nil {
		return QueuedRun{}, err
	}
	sensitiveNamesJSON, err := boundedJSON(validated.SensitiveHeaderNames, 8192)
	if err != nil {
		return QueuedRun{}, err
	}
	bodyDigest := sha256.Sum256(validated.Body)
	now := service.now().UTC()
	run := Run{
		ID: runID, WorkspaceID: snapshot.Workspace.ID, WorkspaceRevision: snapshot.Workspace.Revision,
		WorkspaceETag:    snapshot.WorkspaceETag,
		ProductionDigest: snapshot.Workspace.ProductionDigest, DraftDigest: snapshot.Workspace.DraftDigest,
		State: RunStateQueued, Stage: RunStageQueued,
		SafeRequestJSON: safeRequestJSON, StaticAnalysisJSON: analysisJSON,
		Replayable: validated.Replayable, SideEffecting: validated.SideEffecting,
		BodyBytes: int64(len(validated.Body)), BodyDigest: config.Digest(bodyDigest),
		SensitiveHeaderNamesJSON: sensitiveNamesJSON,
		CreatedBy:                actor.UserID, RequestID: actor.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	stage := RunStage{
		RunID: run.ID, Sequence: 1, Stage: RunStageQueued,
		Result: StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now,
	}
	if err := service.repository.CreateRouteRun(ctx, run, stage); err != nil {
		return QueuedRun{}, fmt.Errorf("persist route run: %w", err)
	}
	return QueuedRun{Run: run, Request: validated}, nil
}

// Execute owns one queued run until its cleanup-proven terminal transition.
func (service *Service) Execute(
	ctx context.Context,
	id RunID,
	request ValidatedRequest,
) (Run, error) {
	if ctx == nil || service == nil {
		return Run{}, errors.New("execute route run: service is unavailable")
	}
	validated, err := ValidateAgentRequest(request)
	if err != nil {
		return Run{}, err
	}
	run, err := service.repository.RouteRun(ctx, id)
	if err != nil {
		return Run{}, fmt.Errorf("read queued route run: %w", err)
	}
	if run.State != RunStateQueued || run.Stage != RunStageQueued {
		if run.State.Terminal() {
			return run, ErrAlreadyTerminal
		}
		return run, config.ErrConflict
	}
	safeRequestJSON, err := boundedJSON(NewSafeRequest(validated), 131072)
	if err != nil || safeRequestJSON != run.SafeRequestJSON {
		return run, config.ErrConflict
	}

	now := service.now().UTC()
	running := run
	running.State = RunStateRunning
	running.Stage = RunStagePreparing
	running.StartedAt = now
	running.UpdatedAt = now
	if err := service.repository.TransitionRouteRun(ctx, run.State, run.Stage, running, RunStage{
		RunID: id, Sequence: 2, Stage: running.Stage,
		Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now,
	}); err != nil {
		return run, fmt.Errorf("start route run: %w", err)
	}

	result, executeErr := service.agent.ExecuteRouteTest(ctx, AgentRequest{
		RunID: string(run.ID), WorkspaceID: run.WorkspaceID,
		ProductionDigest: run.ProductionDigest, DraftDigest: run.DraftDigest,
		Request: validated, RequestID: run.RequestID,
	})
	settlementCtx, cancelSettlement := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	latest, latestErr := service.repository.RouteRun(settlementCtx, id)
	cancelSettlement()
	if latestErr != nil {
		return running, errors.Join(executeErr, fmt.Errorf("settle route run: %w", latestErr))
	}
	if latest.State != running.State || latest.Stage != running.Stage {
		return latest, errors.Join(executeErr, config.ErrConflict)
	}
	running.CancelRequestedAt = latest.CancelRequestedAt
	if executeErr == nil && !running.CancelRequestedAt.IsZero() {
		executeErr = context.Canceled
	}
	if executeErr != nil {
		terminal := terminalRouteFailure(running, executeErr, service.now().UTC())
		transitionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		transitionErr := service.repository.TransitionRouteRun(
			transitionCtx, running.State, running.Stage, terminal,
			RunStage{
				RunID: id, Sequence: 3, Stage: terminal.Stage, Result: StageResultFailed,
				Code: terminal.LastErrorCode, PublicDetailsJSON: `{}`, OccurredAt: terminal.UpdatedAt,
			},
		)
		cancel()
		return terminal, errors.Join(executeErr, transitionErr)
	}
	if err := validateSuccessfulAgentResult(result); err != nil {
		terminal := terminalRouteFailure(running, err, service.now().UTC())
		transitionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		transitionErr := service.repository.TransitionRouteRun(
			transitionCtx, running.State, running.Stage, terminal,
			RunStage{
				RunID: id, Sequence: 3, Stage: terminal.Stage, Result: StageResultFailed,
				Code: terminal.LastErrorCode, PublicDetailsJSON: `{}`, OccurredAt: terminal.UpdatedAt,
			},
		)
		cancel()
		return terminal, errors.Join(err, transitionErr)
	}

	collecting := running
	collecting.Stage = RunStageCollecting
	collecting.CandidateDigest = result.CandidateDigest
	collecting.UpdatedAt = service.now().UTC()
	if err := service.repository.TransitionRouteRun(ctx, running.State, running.Stage, collecting, RunStage{
		RunID: id, Sequence: 3, Stage: collecting.Stage,
		Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: collecting.UpdatedAt,
	}); err != nil {
		return collecting, fmt.Errorf("collect route result: %w", err)
	}
	terminalJSON, err := boundedJSON(TerminalResult{AgentResult: result}, 2097152)
	if err != nil {
		return collecting, err
	}
	completed := collecting
	completed.State = RunStateSucceeded
	completed.Stage = RunStageCompleted
	completed.TerminalResultJSON = terminalJSON
	completed.UpdatedAt = service.now().UTC()
	completed.FinishedAt = completed.UpdatedAt
	if err := service.repository.TransitionRouteRun(ctx, collecting.State, collecting.Stage, completed, RunStage{
		RunID: id, Sequence: 4, Stage: completed.Stage,
		Result: StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: completed.UpdatedAt,
	}); err != nil {
		return completed, fmt.Errorf("complete route run: %w", err)
	}
	return completed, nil
}

// Cancel durably requests cancellation. The application task owner must cancel a running context separately.
func (service *Service) Cancel(ctx context.Context, id RunID) (Run, error) {
	if service == nil {
		return Run{}, errors.New("cancel route run: service is unavailable")
	}
	run, err := service.repository.RequestRouteRunCancellation(ctx, id, service.now().UTC())
	if err != nil {
		return Run{}, fmt.Errorf("cancel route run: %w", err)
	}
	return run, nil
}

// Run returns one durable route test.
func (service *Service) Run(ctx context.Context, id RunID) (Run, error) {
	return service.repository.RouteRun(ctx, id)
}

// Stages returns immutable route-test events.
func (service *Service) Stages(ctx context.Context, id RunID, after uint64, limit int) ([]RunStage, error) {
	return service.repository.RouteRunStages(ctx, id, after, limit)
}

// List returns one stable history page.
func (service *Service) List(ctx context.Context, query HistoryQuery) ([]Run, error) {
	return service.repository.ListRouteRuns(ctx, query)
}

// ReconcileInterrupted terminalizes active tasks without replaying their potentially side-effecting requests.
func (service *Service) ReconcileInterrupted(ctx context.Context) (int, error) {
	return service.repository.FailInterruptedRouteRuns(ctx, service.now().UTC(), "ui_interrupted")
}

func (service *Service) prepare(
	ctx context.Context,
	workspaceID config.WorkspaceID,
	ifMatch string,
	request Request,
) (config.DraftSnapshot, ValidatedRequest, Analysis, error) {
	if ctx == nil || service == nil {
		return config.DraftSnapshot{}, ValidatedRequest{}, Analysis{}, errors.New("prepare route analysis: service is unavailable")
	}
	validated, err := ValidateRequest(request)
	if err != nil {
		return config.DraftSnapshot{}, ValidatedRequest{}, Analysis{}, err
	}
	snapshot, err := service.workspaces.DraftSnapshot(ctx, workspaceID)
	if err != nil {
		return config.DraftSnapshot{}, ValidatedRequest{}, Analysis{}, fmt.Errorf("read route workspace: %w", err)
	}
	if snapshot.Workspace.ID != workspaceID || snapshot.Workspace.State != config.StateReady ||
		ifMatch == "" || ifMatch != snapshot.WorkspaceETag {
		return config.DraftSnapshot{}, ValidatedRequest{}, Analysis{}, config.ErrConflict
	}
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		return config.DraftSnapshot{}, ValidatedRequest{}, Analysis{}, err
	}
	analysis, err := Analyze(project, validated.StaticRequest)
	if err != nil {
		return config.DraftSnapshot{}, ValidatedRequest{}, Analysis{}, err
	}
	return snapshot, validated, analysis, nil
}

func (service *Service) newRunID() (RunID, error) {
	var raw [16]byte
	if _, err := io.ReadFull(service.tokens, raw[:]); err != nil {
		return "", fmt.Errorf("generate route run id: %w", err)
	}
	return RunID(hex.EncodeToString(raw[:])), nil
}

func boundedJSON(value any, limit int) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal route JSON: %w", err)
	}
	if len(payload) < 2 || len(payload) > limit {
		return "", ErrLimitExceeded
	}
	return string(payload), nil
}

func terminalRouteFailure(run Run, err error, at time.Time) Run {
	terminal := run
	terminal.State = RunStateFailed
	terminal.Stage = RunStageFailed
	terminal.LastErrorCode = routeErrorCode(err)
	terminal.UpdatedAt = at
	terminal.FinishedAt = at
	switch {
	case errors.Is(err, ErrCleanupFailed):
		// Cleanup uncertainty must remain a failed operation even when cancellation or timeout initiated it.
	case errors.Is(err, context.Canceled):
		terminal.State = RunStateCancelled
		terminal.Stage = RunStageCancelled
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrRequestTimeout):
		terminal.State = RunStateTimedOut
		terminal.Stage = RunStageTimedOut
	}
	return terminal
}

func routeErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrCleanupFailed):
		return "route_cleanup_failed"
	case errors.Is(err, context.Canceled):
		return "route_cancelled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrRequestTimeout):
		return "route_request_timeout"
	case errors.Is(err, ErrProjectIncomplete):
		return "route_project_incomplete"
	case errors.Is(err, ErrListenerAmbiguous):
		return "route_listener_ambiguous"
	case errors.Is(err, ErrCandidateInvalid):
		return "route_candidate_invalid"
	case errors.Is(err, ErrSandboxStart):
		return "route_sandbox_start_failed"
	case errors.Is(err, ErrEvidenceIncomplete):
		return "route_evidence_incomplete"
	case errors.Is(err, ErrLimitExceeded):
		return "route_limit_exceeded"
	default:
		return "route_execution_failed"
	}
}

func validateSuccessfulAgentResult(result AgentResult) error {
	if result.CandidateDigest == (config.Digest{}) || len(result.Routes) == 0 ||
		!result.Cleanup.MasterReaped || !result.Cleanup.PortClosed || !result.Cleanup.StageRemoved ||
		result.Response.StatusCode < 100 || result.Response.StatusCode > 599 ||
		result.Response.StatusCode != result.Evidence.StatusCode || result.Evidence.FinalURI == "" {
		return ErrEvidenceIncomplete
	}
	kinds := make(map[string]RouteKind, len(result.Routes))
	for _, route := range result.Routes {
		kinds[route.RouteID] = route.Kind
	}
	if kinds[result.Evidence.ServerRouteID] != RouteServer ||
		kinds[result.Evidence.RouteID] != RouteServer && kinds[result.Evidence.RouteID] != RouteLocation {
		return ErrEvidenceIncomplete
	}
	return nil
}

func validPublicRequestID(value string) bool {
	if value == "" || len(value) > 64 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
