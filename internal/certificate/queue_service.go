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
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	// ProductionRiskConfirmation is required only when exact staging evidence is absent.
	ProductionRiskConfirmation = "ISSUE PRODUCTION CERTIFICATE WITHOUT STAGING"
)

var (
	// ErrPlanChanged indicates that production digest, server resolution or exact diff no longer matches.
	ErrPlanChanged = errors.New("certificate plan changed")
	// ErrProductionRiskConfirmationRequired indicates the missing exact secondary phrase.
	ErrProductionRiskConfirmationRequired = errors.New("certificate production risk confirmation required")
)

// QueueRepository atomically consumes one plan and creates its queued task.
type QueueRepository interface {
	CertificateOrderPlan(context.Context, OrderPlanID, time.Time) (OrderPlan, error)
	ExecuteCertificateOrderPlan(context.Context, OrderPlanID, time.Time, Task, TaskStage) error
}

// QueueServiceOptions are the exact plan revalidation and queue dependencies.
type QueueServiceOptions struct {
	Repository QueueRepository
	Planner    *PlanService
	Random     io.Reader
	Now        func() time.Time
}

// QueueService consumes explicit confirmations only after regenerating the complete review.
type QueueService struct {
	repository QueueRepository
	planner    *PlanService
	random     io.Reader
	now        func() time.Time
}

// ExecuteOrderPlanInput carries confirmations that are never persisted.
type ExecuteOrderPlanInput struct {
	Confirmation               string `json:"confirmation"`
	ProductionRiskConfirmation string `json:"production_risk_confirmation"`
}

// NewQueueService creates a digest-bound execution coordinator.
func NewQueueService(options QueueServiceOptions) (*QueueService, error) {
	if options.Repository == nil || options.Planner == nil || options.Random == nil {
		return nil, fmt.Errorf("create certificate queue service: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &QueueService{
		repository: options.Repository, planner: options.Planner,
		random: options.Random, now: options.Now,
	}, nil
}

// Execute revalidates the complete plan and atomically queues it exactly once.
func (service *QueueService) Execute(
	ctx context.Context,
	actor config.Actor,
	planID OrderPlanID,
	input ExecuteOrderPlanInput,
) (Task, error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		parseOpaqueID(string(planID)) != nil {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", ErrPlanChanged)
	}
	now := service.now().UTC()
	plan, err := service.repository.CertificateOrderPlan(ctx, planID, now)
	if err != nil {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", err)
	}
	if plan.State != PlanStatePlanned || input.Confirmation != plan.PrimaryIdentifier {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", ErrPlanChanged)
	}
	if plan.RequiresRiskConfirm && input.ProductionRiskConfirmation != ProductionRiskConfirmation {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", ErrProductionRiskConfirmationRequired)
	}
	var refs []ServerRef
	if err := json.Unmarshal([]byte(plan.ServerRefsJSON), &refs); err != nil || refs == nil ||
		(plan.Challenge == ChallengeHTTP01 && len(refs) == 0) {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", ErrPlanChanged)
	}
	snapshot, binding, err := service.planner.bindingReview(
		ctx, actor, plan.ID, refs, plan.CertificateID, plan.VersionID,
	)
	if err != nil {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", err)
	}
	if Digest(snapshot.Workspace.ProductionDigest) != plan.ProductionDigest {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", ErrPlanChanged)
	}
	currentRefsJSON, err := json.Marshal(binding.ServerRefs)
	if err != nil || string(currentRefsJSON) != plan.ServerRefsJSON {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", ErrPlanChanged)
	}
	currentDiffJSON, err := json.Marshal(binding.Files)
	if err != nil || string(currentDiffJSON) != plan.BindingDiffJSON {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", ErrPlanChanged)
	}
	taskID, err := NewTaskID(service.random)
	if err != nil {
		return Task{}, fmt.Errorf("execute certificate order plan: %w", err)
	}
	task := Task{
		ID: taskID, Kind: TaskKindIssue, State: TaskStateQueued, Stage: TaskStageQueued,
		PlanID: plan.ID, CertificateID: plan.CertificateID, VersionID: plan.VersionID,
		AccountID: plan.AccountID, DNSCredentialID: plan.DNSCredentialID, Challenge: plan.Challenge,
		CreatedBy: actor.UserID, RequestID: actor.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	stage := TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: TaskStageQueued,
		Result: StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now,
	}
	if err := service.repository.ExecuteCertificateOrderPlan(ctx, plan.ID, now, task, stage); err != nil {
		return Task{}, fmt.Errorf("execute certificate order plan: persist: %w", err)
	}
	task.Stages = []TaskStage{stage}
	return task, nil
}
