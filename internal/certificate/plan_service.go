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

const orderPlanLifetime = 10 * time.Minute

// PlanRepository supplies safe dependencies and persists exact execution contracts.
type PlanRepository interface {
	CertificateAccount(context.Context, AccountID) (Account, error)
	CertificateDNSCredential(context.Context, DNSCredentialID) (DNSCredential, error)
	CreateCertificateOrderPlan(context.Context, OrderPlan) error
	HasCertificateStagingEvidence(context.Context, string, ChallengeType, time.Time) (bool, error)
}

// PlanWorkspace supplies one isolated production snapshot and cleanup.
type PlanWorkspace interface {
	Create(context.Context, config.Actor, string) (config.Workspace, error)
	DraftSnapshot(context.Context, config.WorkspaceID) (config.DraftSnapshot, error)
	Delete(context.Context, config.Actor, config.WorkspaceID, string, string) error
}

// PlanServiceOptions are the exact read/plan dependencies.
type PlanServiceOptions struct {
	Repository PlanRepository
	Workspaces PlanWorkspace
	Random     io.Reader
	Now        func() time.Time
}

// PlanService creates reviewable order plans without external or production mutation.
type PlanService struct {
	repository PlanRepository
	workspaces PlanWorkspace
	random     io.Reader
	now        func() time.Time
}

// CreateOrderPlanInput selects identifiers, challenge, account and exact server refs.
type CreateOrderPlanInput struct {
	Identifiers      []string        `json:"identifiers"`
	Challenge        ChallengeType   `json:"challenge"`
	AccountID        AccountID       `json:"account_id"`
	StagingAccountID AccountID       `json:"staging_account_id,omitempty"`
	DNSCredentialID  DNSCredentialID `json:"dns_credential_id,omitempty"`
	ServerRefs       []ServerRef     `json:"server_refs"`
}

// PlannedOrder combines persisted metadata with the response-only bounded binding review.
type PlannedOrder struct {
	Plan    OrderPlan         `json:"plan"`
	Binding BindingChangePlan `json:"binding"`
}

// NewPlanService creates a certificate plan coordinator.
func NewPlanService(options PlanServiceOptions) (*PlanService, error) {
	if options.Repository == nil || options.Workspaces == nil || options.Random == nil {
		return nil, fmt.Errorf("create certificate plan service: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &PlanService{
		repository: options.Repository, workspaces: options.Workspaces,
		random: options.Random, now: options.Now,
	}, nil
}

// ServerCandidates returns one fresh production projection and removes its temporary workspace.
func (service *PlanService) ServerCandidates(
	ctx context.Context,
	actor config.Actor,
) (_ []ServerCandidate, returnErr error) {
	if ctx == nil || service == nil || service.workspaces == nil || actor.UserID <= 0 ||
		!validRequestID(actor.RequestID) {
		return nil, fmt.Errorf("list certificate server candidates: invalid input")
	}
	actor.System = true
	workspace, err := service.workspaces.Create(ctx, actor, "Certificate server discovery")
	if err != nil {
		return nil, fmt.Errorf("list certificate server candidates: create workspace: %w", err)
	}
	workspaceETag := workspace.ETag()
	defer func() {
		cleanupContext, cancel := detachedOperationContext(ctx, configurationCleanupTimeout)
		cleanupErr := service.workspaces.Delete(
			cleanupContext, actor, workspace.ID, workspaceETag, workspace.Name,
		)
		cancel()
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	snapshot, err := service.workspaces.DraftSnapshot(ctx, workspace.ID)
	if err != nil {
		return nil, fmt.Errorf("list certificate server candidates: snapshot: %w", err)
	}
	workspace = snapshot.Workspace
	workspaceETag = snapshot.WorkspaceETag
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		return nil, err
	}
	candidates, err := BuildServerCandidates(project)
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

// Create persists an exact digest-bound plan only after its temporary snapshot is removed.
func (service *PlanService) Create(
	ctx context.Context,
	actor config.Actor,
	input CreateOrderPlanInput,
) (PlannedOrder, error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		!validChallenge(input.Challenge) {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: %w", ErrIdentifierInvalid)
	}
	identifiers, err := NormalizeIdentifiers(input.Identifiers, input.Challenge)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: %w", err)
	}
	account, err := service.repository.CertificateAccount(ctx, input.AccountID)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: account: %w", err)
	}
	if statusErr := acmeAccountStatusError(account.Status); statusErr != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: %w", statusErr)
	}
	if !account.Environment.Valid() {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: %w", ErrACMEAccountInvalid)
	}
	if err := service.validateChallengeDependencies(ctx, input, account); err != nil {
		return PlannedOrder{}, err
	}
	identifiersJSON, err := json.Marshal(identifiers)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: encode identifiers: %w", err)
	}
	planID, err := NewOrderPlanID(service.random)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: %w", err)
	}
	certificateID, err := NewCertificateID(service.random)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: %w", err)
	}
	versionID, err := NewVersionID(service.random)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: %w", err)
	}
	snapshot, binding, err := service.bindingReview(
		ctx, actor, planID, input.ServerRefs, certificateID, versionID,
	)
	if err != nil {
		return PlannedOrder{}, err
	}
	serverRefsJSON, err := json.Marshal(binding.ServerRefs)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: encode server refs: %w", err)
	}
	bindingDiffJSON, err := json.Marshal(binding.Files)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: encode binding diff: %w", err)
	}
	stagingEvidence, err := service.repository.HasCertificateStagingEvidence(
		ctx,
		string(identifiersJSON),
		input.Challenge,
		service.now().UTC(),
	)
	if err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: staging evidence: %w", err)
	}
	now := service.now().UTC()
	plan := OrderPlan{
		ID: planID, State: PlanStatePlanned, Environment: account.Environment, Challenge: input.Challenge,
		AccountID: input.AccountID, StagingAccountID: input.StagingAccountID,
		DNSCredentialID: input.DNSCredentialID, CertificateID: certificateID, VersionID: versionID,
		PrimaryIdentifier: identifiers[0], IdentifiersJSON: string(identifiersJSON),
		ServerRefsJSON: string(serverRefsJSON), ProductionDigest: Digest(snapshot.Workspace.ProductionDigest),
		BindingDiffJSON: string(bindingDiffJSON), StagingEvidence: stagingEvidence,
		RequiresRiskConfirm: account.Environment == EnvironmentProduction && !stagingEvidence,
		ExpiresAt:           now.Add(orderPlanLifetime), CreatedBy: actor.UserID, RequestID: actor.RequestID, CreatedAt: now,
	}
	if err := service.repository.CreateCertificateOrderPlan(ctx, plan); err != nil {
		return PlannedOrder{}, fmt.Errorf("create certificate order plan: persist: %w", err)
	}
	return PlannedOrder{Plan: plan, Binding: binding}, nil
}

func (service *PlanService) validateChallengeDependencies(
	ctx context.Context,
	input CreateOrderPlanInput,
	account Account,
) error {
	switch input.Challenge {
	case ChallengeHTTP01:
		if input.DNSCredentialID != "" {
			return fmt.Errorf("create certificate order plan: %w", ErrIdentifierInvalid)
		}
	case ChallengeCloudflareDNS01:
		credential, err := service.repository.CertificateDNSCredential(ctx, input.DNSCredentialID)
		if err != nil {
			return fmt.Errorf("create certificate order plan: credential: %w", err)
		}
		if credential.Provider != DNSProviderCloudflare || credential.Status != CredentialStatusValid {
			return fmt.Errorf("create certificate order plan: %w", ErrCloudflarePermission)
		}
	default:
		return fmt.Errorf("create certificate order plan: %w", ErrIdentifierInvalid)
	}
	if input.StagingAccountID != "" {
		staging, err := service.repository.CertificateAccount(ctx, input.StagingAccountID)
		if err != nil {
			return fmt.Errorf("create certificate order plan: staging account: %w", err)
		}
		if statusErr := acmeAccountStatusError(staging.Status); statusErr != nil {
			return fmt.Errorf("create certificate order plan: staging account: %w", statusErr)
		}
		if account.Environment != EnvironmentProduction || staging.Environment != EnvironmentStaging {
			return fmt.Errorf("create certificate order plan: %w", ErrACMEAccountInvalid)
		}
	}
	return nil
}

func (service *PlanService) bindingReview(
	ctx context.Context,
	actor config.Actor,
	planID OrderPlanID,
	refs []ServerRef,
	certificateID CertificateID,
	versionID VersionID,
) (_ config.DraftSnapshot, _ BindingChangePlan, returnErr error) {
	actor.System = true
	name := "Certificate plan " + string(planID[:8])
	workspace, err := service.workspaces.Create(ctx, actor, name)
	if err != nil {
		return config.DraftSnapshot{}, BindingChangePlan{}, fmt.Errorf("create certificate plan workspace: %w", err)
	}
	workspaceETag := workspace.ETag()
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), configurationCleanupTimeout)
		cleanupErr := service.workspaces.Delete(
			cleanupContext, actor, workspace.ID, workspaceETag, workspace.Name,
		)
		cancel()
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	snapshot, err := service.workspaces.DraftSnapshot(ctx, workspace.ID)
	if err != nil {
		return config.DraftSnapshot{}, BindingChangePlan{}, fmt.Errorf("read certificate plan workspace: %w", err)
	}
	workspaceETag = snapshot.WorkspaceETag
	workspace = snapshot.Workspace
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		return config.DraftSnapshot{}, BindingChangePlan{}, err
	}
	binding := BindingChangePlan{
		Mode: "bind", ServerRefs: []ServerRef{}, Files: []BindingFileChange{},
	}
	if len(refs) > 0 {
		binding, err = PlanCertificateBinding(ctx, project, refs, certificateID, versionID)
		if err != nil {
			return config.DraftSnapshot{}, BindingChangePlan{}, err
		}
	}
	return snapshot, binding, nil
}
