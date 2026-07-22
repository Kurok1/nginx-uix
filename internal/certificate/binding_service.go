/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const maximumStandaloneBindingRefs = 100

// BindingRepository owns standalone binding plan, task and completion transactions.
type BindingRepository interface {
	Certificate(context.Context, CertificateID) (Certificate, error)
	CertificateBindingByFingerprint(context.Context, string) (Binding, error)
	CreateCertificateBindingPlan(context.Context, BindingPlan) error
	CertificateBindingPlan(context.Context, BindingPlanID, time.Time) (BindingPlan, error)
	ExecuteCertificateBindingPlan(context.Context, BindingPlanID, time.Time, Task, TaskStage) error
	CertificateTask(context.Context, TaskID) (Task, error)
	TransitionCertificateTask(context.Context, TaskState, TaskStageName, Task, TaskStage) error
	CompleteCertificateBindingTask(
		context.Context, TaskState, TaskStageName, Task, TaskStage, Certificate, []Binding,
	) error
}

// StandaloneBindingPublisher applies one already-reviewed binding through the normal release path.
type StandaloneBindingPublisher interface {
	Bind(context.Context, Task, BindingPlan) (DeploymentResult, error)
}

// BindingServiceOptions are the complete standalone binding dependencies.
type BindingServiceOptions struct {
	Repository BindingRepository
	Planner    *PlanService
	Publisher  StandaloneBindingPublisher
	Random     io.Reader
	Now        func() time.Time
}

// BindingService creates, consumes and runs exact standalone binding contracts.
type BindingService struct {
	repository BindingRepository
	planner    *PlanService
	publisher  StandaloneBindingPublisher
	random     io.Reader
	now        func() time.Time
}

// CreateBindingPlanInput selects exact source-derived server references.
type CreateBindingPlanInput struct {
	ServerRefs []ServerRef `json:"server_refs"`
}

// ExecuteBindingPlanInput carries an exact primary-identifier confirmation that is never persisted.
type ExecuteBindingPlanInput struct {
	Confirmation string `json:"confirmation"`
}

// PlannedBinding combines persisted metadata with its response-only diff.
type PlannedBinding struct {
	Plan    BindingPlan
	Binding BindingChangePlan
}

// NewBindingService validates explicit dependencies and starts no implicit workers.
func NewBindingService(options BindingServiceOptions) (*BindingService, error) {
	if options.Repository == nil || options.Planner == nil || options.Publisher == nil || options.Random == nil {
		return nil, fmt.Errorf("create certificate binding service: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &BindingService{
		repository: options.Repository, planner: options.Planner, publisher: options.Publisher,
		random: options.Random, now: options.Now,
	}, nil
}

// CreatePlan persists one exact diff without changing production.
func (service *BindingService) CreatePlan(
	ctx context.Context,
	actor config.Actor,
	certificateID CertificateID,
	input CreateBindingPlanInput,
) (PlannedBinding, error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		parseOpaqueID(string(certificateID)) != nil || len(input.ServerRefs) == 0 ||
		len(input.ServerRefs) > maximumStandaloneBindingRefs {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: %w", ErrBindingConflict)
	}
	item, err := service.repository.Certificate(ctx, certificateID)
	if err != nil {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: certificate: %w", err)
	}
	if ValidateCertificate(item) != nil || item.ID != certificateID || !bindableCertificateState(item.State) ||
		parseOpaqueID(string(item.ActiveVersionID)) != nil {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: %w", ErrBindingConflict)
	}
	if err := service.rejectManagedBindingOwners(ctx, input.ServerRefs); err != nil {
		return PlannedBinding{}, err
	}
	planID, err := NewBindingPlanID(service.random)
	if err != nil {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: %w", err)
	}
	snapshot, binding, err := service.planner.bindingReview(
		ctx, actor, OrderPlanID(planID), input.ServerRefs, item.ID, item.ActiveVersionID,
	)
	if err != nil {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: review: %w", err)
	}
	refsJSON, err := json.Marshal(binding.ServerRefs)
	if err != nil {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: encode refs: %w", err)
	}
	diffJSON, err := json.Marshal(binding.Files)
	if err != nil {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: encode diff: %w", err)
	}
	now := service.now().UTC()
	plan := BindingPlan{
		ID: planID, State: PlanStatePlanned, CertificateID: item.ID, VersionID: item.ActiveVersionID,
		ServerRefsJSON: string(refsJSON), ProductionDigest: Digest(snapshot.Workspace.ProductionDigest),
		BindingDiffJSON: string(diffJSON), ExpiresAt: now.Add(orderPlanLifetime), CreatedBy: actor.UserID,
		RequestID: actor.RequestID, CreatedAt: now,
	}
	if ValidateBindingPlan(plan) != nil {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: %w", ErrBindingConflict)
	}
	if err := service.repository.CreateCertificateBindingPlan(ctx, plan); err != nil {
		return PlannedBinding{}, fmt.Errorf("create certificate binding plan: persist: %w", err)
	}
	return PlannedBinding{Plan: plan, Binding: binding}, nil
}

// ExecutePlan revalidates the complete diff and atomically queues a standalone binding task.
func (service *BindingService) ExecutePlan(
	ctx context.Context,
	actor config.Actor,
	planID BindingPlanID,
	input ExecuteBindingPlanInput,
) (Task, error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		parseOpaqueID(string(planID)) != nil {
		return Task{}, fmt.Errorf("execute certificate binding plan: %w", ErrPlanChanged)
	}
	now := service.now().UTC()
	plan, err := service.repository.CertificateBindingPlan(ctx, planID, now)
	if err != nil {
		return Task{}, fmt.Errorf("execute certificate binding plan: %w", err)
	}
	item, err := service.repository.Certificate(ctx, plan.CertificateID)
	if err != nil || ValidateCertificate(item) != nil || item.ActiveVersionID != plan.VersionID ||
		!bindableCertificateState(item.State) || input.Confirmation != item.PrimaryIdentifier {
		return Task{}, fmt.Errorf("execute certificate binding plan: %w", ErrPlanChanged)
	}
	var refs []ServerRef
	if err := json.Unmarshal([]byte(plan.ServerRefsJSON), &refs); err != nil || len(refs) == 0 {
		return Task{}, fmt.Errorf("execute certificate binding plan: %w", ErrPlanChanged)
	}
	if err := service.rejectManagedBindingOwners(ctx, refs); err != nil {
		return Task{}, err
	}
	snapshot, binding, err := service.planner.bindingReview(
		ctx, actor, OrderPlanID(plan.ID), refs, plan.CertificateID, plan.VersionID,
	)
	if err != nil || Digest(snapshot.Workspace.ProductionDigest) != plan.ProductionDigest {
		return Task{}, fmt.Errorf("execute certificate binding plan: %w", ErrPlanChanged)
	}
	currentRefs, refsErr := json.Marshal(binding.ServerRefs)
	currentDiff, diffErr := json.Marshal(binding.Files)
	if refsErr != nil || diffErr != nil || string(currentRefs) != plan.ServerRefsJSON ||
		string(currentDiff) != plan.BindingDiffJSON {
		return Task{}, fmt.Errorf("execute certificate binding plan: %w", ErrPlanChanged)
	}
	taskID, err := NewTaskID(service.random)
	if err != nil {
		return Task{}, fmt.Errorf("execute certificate binding plan: %w", err)
	}
	task := Task{
		ID: taskID, Kind: TaskKindBind, State: TaskStateQueued, Stage: TaskStageQueued,
		PlanID: OrderPlanID(plan.ID), CertificateID: plan.CertificateID, VersionID: plan.VersionID,
		AccountID: item.AccountID, DNSCredentialID: item.DNSCredentialID, Challenge: item.Challenge,
		CreatedBy: actor.UserID, RequestID: actor.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	stage := TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: task.Stage, Result: StageResultPending,
		PublicDetailsJSON: `{}`, OccurredAt: now,
	}
	if ValidateTask(task) != nil || ValidateTaskStage(stage) != nil {
		return Task{}, fmt.Errorf("execute certificate binding plan: %w", ErrPlanChanged)
	}
	if err := service.repository.ExecuteCertificateBindingPlan(ctx, plan.ID, now, task, stage); err != nil {
		return Task{}, fmt.Errorf("execute certificate binding plan: persist: %w", err)
	}
	task.Stages = []TaskStage{stage}
	return task, nil
}

// Run publishes one queued binding task and commits metadata only after release success.
func (service *BindingService) Run(ctx context.Context, id TaskID) (Task, error) {
	if ctx == nil || service == nil || parseOpaqueID(string(id)) != nil {
		return Task{}, fmt.Errorf("run certificate binding: invalid task")
	}
	taskContext, cancel := context.WithTimeout(ctx, certificateTaskTimeout)
	defer cancel()
	task, err := service.repository.CertificateTask(taskContext, id)
	if err != nil {
		return Task{}, fmt.Errorf("run certificate binding: %w", err)
	}
	if task.Kind != TaskKindBind || task.State != TaskStateQueued || task.Stage != TaskStageQueued ||
		parseOpaqueID(string(task.PlanID)) != nil {
		return task, fmt.Errorf("run certificate binding: %w", ErrTaskActive)
	}
	if !task.CancelRequestedAt.IsZero() {
		return service.finishBindingTask(taskContext, task, TaskStateCancelled, TaskStageCancelled,
			"certificate_task_cancelled", context.Canceled)
	}
	plan, err := service.repository.CertificateBindingPlan(taskContext, BindingPlanID(task.PlanID), service.now().UTC())
	if err != nil || plan.State != PlanStateExecuted || plan.CertificateID != task.CertificateID ||
		plan.VersionID != task.VersionID {
		return service.finishBindingTask(taskContext, task, TaskStateFailed, TaskStageFailed,
			"certificate_plan_invalid", errors.Join(ErrPlanChanged, err))
	}
	item, err := service.repository.Certificate(taskContext, task.CertificateID)
	if err != nil || item.ActiveVersionID != task.VersionID || !bindableCertificateState(item.State) {
		return service.finishBindingTask(taskContext, task, TaskStateFailed, TaskStageFailed,
			"certificate_binding_conflict", errors.Join(ErrBindingConflict, err))
	}
	task, err = service.advanceBindingTask(taskContext, task, TaskStagePreparing)
	if err != nil {
		return task, err
	}
	task, err = service.advanceBindingTask(taskContext, task, TaskStageDeploying)
	if err != nil {
		return task, err
	}
	deployment, err := service.publisher.Bind(taskContext, task, plan)
	if err != nil || deployment.ReleaseID == "" || len(deployment.Bindings) == 0 {
		state, stage, code := TaskStateFailed, TaskStageFailed, "certificate_deployment_failed"
		if errors.Is(err, ErrConfigurationReleaseUncertain) {
			state, stage, code = TaskStateNeedsAttention, TaskStageNeedsAttention, "certificate_release_uncertain"
		}
		return service.finishBindingTask(taskContext, task, state, stage, code, errors.Join(ErrBindingConflict, err))
	}
	now := service.now().UTC()
	item.State = boundCertificateState(item, now)
	item.UpdatedAt = now
	next := task
	next.State = TaskStateSucceeded
	next.Stage = TaskStageCompleted
	next.ReleaseID = deployment.ReleaseID
	next.LastErrorCode = ""
	next.UpdatedAt = now
	next.FinishedAt = now
	stage := service.bindingStage(next, TaskStageCompleted, StageResultSuccess, "")
	commitContext, commitCancel := detachedOperationContext(taskContext, certificateCommitTimeout)
	commitErr := service.repository.CompleteCertificateBindingTask(
		commitContext, task.State, task.Stage, next, stage, item, deployment.Bindings,
	)
	commitCancel()
	if commitErr != nil {
		return service.finishBindingTask(taskContext, task, TaskStateNeedsAttention, TaskStageNeedsAttention,
			"certificate_metadata_commit_failed", commitErr)
	}
	next.Stages = append(append([]TaskStage(nil), task.Stages...), stage)
	return next, nil
}

func (service *BindingService) rejectManagedBindingOwners(ctx context.Context, refs []ServerRef) error {
	for _, ref := range refs {
		if !validServerRef(ref) {
			return fmt.Errorf("review certificate binding owners: %w", ErrBindingConflict)
		}
		_, err := service.repository.CertificateBindingByFingerprint(ctx, ref.Fingerprint)
		switch {
		case err == nil:
			return fmt.Errorf("review certificate binding owners: %w", ErrBindingConflict)
		case errors.Is(err, fs.ErrNotExist):
			continue
		default:
			return fmt.Errorf("review certificate binding owners: %w", err)
		}
	}
	return nil
}

func (service *BindingService) advanceBindingTask(
	ctx context.Context,
	current Task,
	stageName TaskStageName,
) (Task, error) {
	next := current
	now := service.now().UTC()
	next.State = TaskStateRunning
	next.Stage = stageName
	next.UpdatedAt = now
	if next.StartedAt.IsZero() {
		next.StartedAt = now
	}
	stage := service.bindingStage(next, stageName, StageResultRunning, "")
	if err := service.repository.TransitionCertificateTask(ctx, current.State, current.Stage, next, stage); err != nil {
		return current, fmt.Errorf("advance certificate binding task: %w", err)
	}
	next.Stages = append(append([]TaskStage(nil), current.Stages...), stage)
	return next, nil
}

func (service *BindingService) finishBindingTask(
	ctx context.Context,
	current Task,
	state TaskState,
	stageName TaskStageName,
	code string,
	cause error,
) (Task, error) {
	if current.State.Terminal() {
		return current, cause
	}
	if errors.Is(cause, context.Canceled) || ctx.Err() != nil {
		state, stageName, code = TaskStateCancelled, TaskStageCancelled, "certificate_task_cancelled"
	}
	next := current
	now := service.now().UTC()
	next.State = state
	next.Stage = stageName
	next.LastErrorCode = code
	next.UpdatedAt = now
	next.FinishedAt = now
	stage := service.bindingStage(next, stageName, StageResultFailed, code)
	commitContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
	err := service.repository.TransitionCertificateTask(commitContext, current.State, current.Stage, next, stage)
	cancel()
	if err != nil {
		return current, errors.Join(cause, err)
	}
	next.Stages = append(append([]TaskStage(nil), current.Stages...), stage)
	return next, cause
}

func (service *BindingService) bindingStage(
	task Task,
	stageName TaskStageName,
	result StageResult,
	code string,
) TaskStage {
	return TaskStage{
		TaskID: task.ID, Sequence: uint64(len(task.Stages) + 1), Stage: stageName,
		Result: result, Code: code, PublicDetailsJSON: `{}`, OccurredAt: task.UpdatedAt,
	}
}

func bindableCertificateState(state CertificateState) bool {
	return slices.Contains([]CertificateState{
		CertificateStateActive, CertificateStateExpiring, CertificateStateExpired, CertificateStateUnbound,
	}, state)
}

func boundCertificateState(item Certificate, now time.Time) CertificateState {
	switch {
	case !now.Before(item.NotAfter):
		return CertificateStateExpired
	case item.NotAfter.Sub(now) <= 7*24*time.Hour:
		return CertificateStateExpiring
	default:
		return CertificateStateActive
	}
}
