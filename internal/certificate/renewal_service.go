/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

// RenewalRepository owns safe certificate reads and atomic plan/task creation.
type RenewalRepository interface {
	Certificate(context.Context, CertificateID) (Certificate, error)
	CertificateBindings(context.Context, CertificateID) ([]Binding, error)
	CertificateAccount(context.Context, AccountID) (Account, error)
	CertificateDNSCredential(context.Context, DNSCredentialID) (DNSCredential, error)
	CreateCertificateRenewal(context.Context, OrderPlan, Task, TaskStage) error
}

// RenewalServiceOptions are the complete manual and scheduled renewal dependencies.
type RenewalServiceOptions struct {
	Repository RenewalRepository
	Planner    *PlanService
	Random     io.Reader
	Now        func() time.Time
}

// RenewalService creates a new immutable version plan from persisted certificate ownership.
type RenewalService struct {
	repository RenewalRepository
	planner    *PlanService
	random     io.Reader
	now        func() time.Time
}

// ManualRenewalInput requires exact primary-identifier confirmation.
type ManualRenewalInput struct {
	Confirmation string `json:"confirmation"`
}

// NewRenewalService validates the renewal planner dependencies.
func NewRenewalService(options RenewalServiceOptions) (*RenewalService, error) {
	if options.Repository == nil || options.Planner == nil || options.Random == nil {
		return nil, fmt.Errorf("create certificate renewal service: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &RenewalService{
		repository: options.Repository, planner: options.Planner, random: options.Random, now: options.Now,
	}, nil
}

// Queue creates one manual renewal after exact certificate confirmation.
func (service *RenewalService) Queue(
	ctx context.Context,
	actor config.Actor,
	certificateID CertificateID,
	input ManualRenewalInput,
) (Task, error) {
	return service.queue(ctx, actor, certificateID, input.Confirmation, true)
}

// QueueAutomatic creates the same renewal contract without weakening any issuance checks.
func (service *RenewalService) QueueAutomatic(
	ctx context.Context,
	certificateID CertificateID,
) (Task, error) {
	item, err := service.repository.Certificate(ctx, certificateID)
	if err != nil {
		return Task{}, fmt.Errorf("queue automatic certificate renewal: %w", err)
	}
	actor := config.Actor{
		UserID: item.CreatedBy, RequestID: "certificate-scheduler-" + string(certificateID[:8]),
	}
	return service.queue(ctx, actor, certificateID, item.PrimaryIdentifier, false)
}

func (service *RenewalService) queue(
	ctx context.Context,
	actor config.Actor,
	certificateID CertificateID,
	confirmation string,
	requireConfirmation bool,
) (Task, error) {
	if ctx == nil || service == nil || parseOpaqueID(string(certificateID)) != nil || actor.UserID <= 0 ||
		!validRequestID(actor.RequestID) {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", ErrIdentifierInvalid)
	}
	item, err := service.repository.Certificate(ctx, certificateID)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: certificate: %w", err)
	}
	if ValidateCertificate(item) != nil || item.ID != certificateID || !renewableCertificateState(item.State) ||
		requireConfirmation && confirmation != item.PrimaryIdentifier {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", ErrPlanChanged)
	}
	account, err := service.repository.CertificateAccount(ctx, item.AccountID)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", ErrACMEAccountInvalid)
	}
	if statusErr := acmeAccountStatusError(account.Status); statusErr != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", statusErr)
	}
	if account.Environment != EnvironmentProduction {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", ErrACMEAccountInvalid)
	}
	if item.Challenge == ChallengeCloudflareDNS01 {
		credential, credentialErr := service.repository.CertificateDNSCredential(ctx, item.DNSCredentialID)
		if credentialErr != nil || credential.Status != CredentialStatusValid || credential.Provider != DNSProviderCloudflare {
			return Task{}, fmt.Errorf("queue certificate renewal: %w", ErrCloudflarePermission)
		}
	}
	bindings, err := service.repository.CertificateBindings(ctx, certificateID)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: bindings: %w", err)
	}
	refs, err := renewalServerRefs(item, bindings)
	if err != nil {
		return Task{}, err
	}
	planID, err := NewOrderPlanID(service.random)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", err)
	}
	versionID, err := NewVersionID(service.random)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", err)
	}
	taskID, err := NewTaskID(service.random)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", err)
	}
	snapshot, bindingPlan, err := service.planner.bindingReview(
		ctx, actor, planID, refs, item.ID, versionID,
	)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: review: %w", err)
	}
	serverRefsJSON, err := json.Marshal(bindingPlan.ServerRefs)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: encode refs: %w", err)
	}
	bindingDiffJSON, err := json.Marshal(bindingPlan.Files)
	if err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: encode diff: %w", err)
	}
	now := service.now().UTC()
	plan := OrderPlan{
		ID: planID, State: PlanStateExecuted, Environment: EnvironmentProduction,
		Challenge: item.Challenge, AccountID: item.AccountID, DNSCredentialID: item.DNSCredentialID,
		CertificateID: item.ID, VersionID: versionID, PrimaryIdentifier: item.PrimaryIdentifier,
		IdentifiersJSON: item.IdentifiersJSON, ServerRefsJSON: string(serverRefsJSON),
		ProductionDigest: Digest(snapshot.Workspace.ProductionDigest), BindingDiffJSON: string(bindingDiffJSON),
		StagingEvidence: true, ExpiresAt: now.Add(orderPlanLifetime), CreatedBy: actor.UserID,
		RequestID: actor.RequestID, CreatedAt: now, ExecutedAt: now,
	}
	task := Task{
		ID: taskID, Kind: TaskKindRenew, State: TaskStateQueued, Stage: TaskStageQueued,
		PlanID: plan.ID, CertificateID: item.ID, VersionID: versionID, AccountID: item.AccountID,
		DNSCredentialID: item.DNSCredentialID, Challenge: item.Challenge,
		CreatedBy: actor.UserID, RequestID: actor.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	stage := TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: TaskStageQueued, Result: StageResultPending,
		PublicDetailsJSON: `{}`, OccurredAt: now,
	}
	if ValidateOrderPlan(plan) != nil || ValidateTask(task) != nil || ValidateTaskStage(stage) != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: %w", ErrPlanChanged)
	}
	if err := service.repository.CreateCertificateRenewal(ctx, plan, task, stage); err != nil {
		return Task{}, fmt.Errorf("queue certificate renewal: persist: %w", err)
	}
	task.Stages = []TaskStage{stage}
	return task, nil
}

func renewalServerRefs(item Certificate, bindings []Binding) ([]ServerRef, error) {
	refs := make([]ServerRef, 0, len(bindings))
	for _, binding := range bindings {
		if ValidateBinding(binding) != nil || binding.CertificateID != item.ID ||
			binding.VersionID != item.ActiveVersionID || binding.ServerStartOffset > math.MaxInt {
			return nil, fmt.Errorf("build certificate renewal refs: %w", ErrBindingConflict)
		}
		var names, listeners []string
		if json.Unmarshal([]byte(binding.ServerNamesJSON), &names) != nil ||
			json.Unmarshal([]byte(binding.ListenersJSON), &listeners) != nil {
			return nil, fmt.Errorf("build certificate renewal refs: %w", ErrBindingConflict)
		}
		ref := ServerRef{
			Path: binding.ConfigPath, StartOffset: int(binding.ServerStartOffset),
			ServerNames: names, Listeners: listeners, Fingerprint: binding.ServerFingerprint,
		}
		if !validServerRef(ref) {
			return nil, fmt.Errorf("build certificate renewal refs: %w", ErrBindingConflict)
		}
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(left, right int) bool {
		if refs[left].Path != refs[right].Path {
			return refs[left].Path < refs[right].Path
		}
		return refs[left].Fingerprint < refs[right].Fingerprint
	})
	for index := 1; index < len(refs); index++ {
		if refs[index].Path == refs[index-1].Path && refs[index].Fingerprint == refs[index-1].Fingerprint {
			return nil, fmt.Errorf("build certificate renewal refs: %w", ErrBindingConflict)
		}
	}
	return slices.Clone(refs), nil
}

func renewableCertificateState(state CertificateState) bool {
	return state == CertificateStateActive || state == CertificateStateExpiring ||
		state == CertificateStateExpired || state == CertificateStateUnbound
}
