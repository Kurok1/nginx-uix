/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	defaultRenewBefore       = 30 * 24 * time.Hour
	maximumRenewalJitter     = 12 * time.Hour
	certificateTaskTimeout   = 15 * time.Minute
	challengeCleanupTimeout  = 2 * time.Minute
	certificateCommitTimeout = 10 * time.Second
)

// OrderRepository owns task, artifact and completion transactions.
type OrderRepository interface {
	CertificateTask(context.Context, TaskID) (Task, error)
	ActiveCertificateTasks(context.Context, int) ([]Task, error)
	CertificateOrderPlan(context.Context, OrderPlanID, time.Time) (OrderPlan, error)
	CertificateAccount(context.Context, AccountID) (Account, error)
	Certificate(context.Context, CertificateID) (Certificate, error)
	MarkCertificateDNSCredentialUsed(context.Context, DNSCredentialID, time.Time) error
	TransitionCertificateTask(context.Context, TaskState, TaskStageName, Task, TaskStage) error
	CreateCertificateChallengeArtifact(context.Context, ChallengeArtifact) error
	CertificateChallengeArtifacts(context.Context, TaskID) ([]ChallengeArtifact, error)
	UpdateCertificateChallengeArtifact(context.Context, ArtifactID, ArtifactState, time.Time) error
	CompleteIssuedCertificateTask(
		context.Context, TaskState, TaskStageName, Task, TaskStage, Certificate, Version, []Binding,
	) error
	CompleteRenewedCertificateTask(
		context.Context, TaskState, TaskStageName, Task, TaskStage, Certificate, Version, []Binding, VersionID,
	) error
	FailCertificateRenewalTask(
		context.Context, TaskState, TaskStageName, Task, TaskStage, Certificate,
	) error
}

// OrderVault exposes only the secrets and material operations required during issuance.
type OrderVault interface {
	LoadAccountKey(context.Context, AccountID) (crypto.Signer, error)
	LoadCloudflareToken(context.Context, string) (string, error)
	StoreCertificateVersion(
		context.Context, CertificateID, VersionID, IssuedCertificate, crypto.Signer,
	) (StoredCertificateMaterial, error)
	DeleteCertificateVersion(context.Context, CertificateID, VersionID) error
}

// CloudflareDNSClient owns exact record creation and cleanup.
type CloudflareDNSClient interface {
	FindZone(context.Context, string, string) (CloudflareZone, error)
	CreateTXT(context.Context, string, string, string, string, string) (CloudflareRecord, error)
	ReadTXT(context.Context, string, string, string) (CloudflareRecord, error)
	DeleteRecord(context.Context, string, string, string) error
}

// DNSPropagationWaiter proves at least one authoritative server sees an exact value.
type DNSPropagationWaiter interface {
	Wait(context.Context, CloudflareZone, string, string) error
}

// HTTPChallengeResponse remains in memory and must never enter task JSON.
type HTTPChallengeResponse struct {
	Identifier       string
	Token            string
	KeyAuthorization string
}

// HTTPChallengeManager publishes and cleans task-owned exact challenge locations.
type HTTPChallengeManager interface {
	Provision(context.Context, Task, []ServerRef, []HTTPChallengeResponse) error
	Cleanup(context.Context, TaskID) error
}

// DeploymentResult is the safe evidence from the normal configuration release path.
type DeploymentResult struct {
	ReleaseID string
	Bindings  []Binding
}

// CertificatePublisher deploys only an already validated immutable version.
type CertificatePublisher interface {
	Deploy(context.Context, Task, OrderPlan, StoredCertificateMaterial) (DeploymentResult, error)
}

// OrderServiceOptions are the complete issuance runner dependencies.
type OrderServiceOptions struct {
	Repository OrderRepository
	Vault      OrderVault
	ACME       ACMEOrderClientFactory
	Cloudflare CloudflareDNSClient
	DNSWaiter  DNSPropagationWaiter
	HTTP       HTTPChallengeManager
	Publisher  CertificatePublisher
	Random     io.Reader
	Now        func() time.Time
}

// OrderService owns one task from durable queue through cleanup-proven terminal state.
type OrderService struct {
	repository OrderRepository
	vault      OrderVault
	acme       ACMEOrderClientFactory
	cloudflare CloudflareDNSClient
	dnsWaiter  DNSPropagationWaiter
	http       HTTPChallengeManager
	publisher  CertificatePublisher
	random     io.Reader
	now        func() time.Time
}

type preparedAuthorization struct {
	authorization ACMEAuthorization
	challenge     ACMEChallenge
}

// ReconcileInterrupted cleans durable challenge ownership before terminating work left by a prior process.
func (service *OrderService) ReconcileInterrupted(ctx context.Context) ([]Task, error) {
	if ctx == nil || service == nil {
		return nil, fmt.Errorf("reconcile certificate orders: service is unavailable")
	}
	reconciled := make([]Task, 0)
	for len(reconciled) < 1000 {
		tasks, err := service.repository.ActiveCertificateTasks(ctx, 100)
		if err != nil {
			return nil, fmt.Errorf("reconcile certificate orders: list tasks: %w", err)
		}
		if len(tasks) == 0 {
			return reconciled, nil
		}
		for _, task := range tasks {
			if ValidateTask(task) != nil || task.State.Terminal() {
				return nil, fmt.Errorf("reconcile certificate orders: invalid task")
			}
			cleanupErr := service.cleanupInterruptedTask(ctx, task)
			if task.Kind == TaskKindBind || task.Kind == TaskKindUnbind {
				cleanupErr = errors.Join(cleanupErr, ErrConfigurationReleaseUncertain)
			}
			state := TaskStateFailed
			stage := TaskStageFailed
			code := "certificate_task_interrupted"
			if cleanupErr != nil {
				state = TaskStateNeedsAttention
				stage = TaskStageNeedsAttention
				code = "challenge_cleanup_failed"
			}
			terminal, err := service.reconcileTerminal(ctx, task, state, stage, code)
			if err != nil {
				return nil, errors.Join(fmt.Errorf("reconcile certificate order: %w", err), cleanupErr)
			}
			reconciled = append(reconciled, terminal)
		}
	}
	return nil, fmt.Errorf("reconcile certificate orders: active task limit exceeded")
}

func (service *OrderService) cleanupInterruptedTask(ctx context.Context, task Task) error {
	artifacts, err := service.repository.CertificateChallengeArtifacts(ctx, task.ID)
	if err != nil {
		return err
	}
	hasHTTP := false
	hasDNS := false
	for _, artifact := range artifacts {
		if artifact.State == ArtifactStateCleaned {
			continue
		}
		switch artifact.Kind {
		case ArtifactHTTPInclude:
			hasHTTP = true
		case ArtifactCloudflareTXT:
			if artifact.DNSCredentialID != task.DNSCredentialID {
				return ErrConfigurationReleaseUncertain
			}
			hasDNS = true
		default:
			return ErrConfigurationReleaseUncertain
		}
	}
	if hasHTTP && hasDNS {
		return ErrConfigurationReleaseUncertain
	}
	if hasHTTP {
		if service.http == nil {
			return ErrConfigurationReleaseUncertain
		}
		return runChallengeCleanup(ctx, func(cleanupContext context.Context) error {
			return service.http.Cleanup(cleanupContext, task.ID)
		})
	}
	if hasDNS {
		if service.cloudflare == nil || service.vault == nil {
			return ErrCloudflareUnavailable
		}
		token, err := service.vault.LoadCloudflareToken(ctx, string(task.DNSCredentialID))
		if err != nil {
			return ErrCloudflareTokenInvalid
		}
		return runChallengeCleanup(ctx, func(cleanupContext context.Context) error {
			return service.cleanupDNS(cleanupContext, task.ID, task.DNSCredentialID, token)
		})
	}
	return nil
}

func (service *OrderService) reconcileTerminal(
	ctx context.Context,
	task Task,
	state TaskState,
	stageName TaskStageName,
	code string,
) (Task, error) {
	now := service.now().UTC()
	next := task
	next.State = state
	next.Stage = stageName
	next.LastErrorCode = code
	next.UpdatedAt = now
	next.FinishedAt = now
	stage := service.nextStage(next, stageName, StageResultFailed, code)
	commitContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
	err := service.repository.TransitionCertificateTask(commitContext, task.State, task.Stage, next, stage)
	cancel()
	if err != nil {
		return task, err
	}
	next.Stages = append(append([]TaskStage{}, task.Stages...), stage)
	return next, nil
}

// NewOrderService creates an issuance runner with no implicit workers or global state.
func NewOrderService(options OrderServiceOptions) (*OrderService, error) {
	if options.Repository == nil || options.Vault == nil || options.ACME == nil ||
		options.Publisher == nil || options.Random == nil {
		return nil, fmt.Errorf("create certificate order service: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &OrderService{
		repository: options.Repository, vault: options.Vault, acme: options.ACME,
		cloudflare: options.Cloudflare, dnsWaiter: options.DNSWaiter, http: options.HTTP,
		publisher: options.Publisher, random: options.Random, now: options.Now,
	}, nil
}

// Run executes one queued task under a bounded child context.
func (service *OrderService) Run(ctx context.Context, id TaskID) (Task, error) {
	if ctx == nil || service == nil || parseOpaqueID(string(id)) != nil {
		return Task{}, fmt.Errorf("run certificate order: invalid task")
	}
	taskCtx, cancel := context.WithTimeout(ctx, certificateTaskTimeout)
	defer cancel()
	task, err := service.repository.CertificateTask(taskCtx, id)
	if err != nil {
		return Task{}, fmt.Errorf("run certificate order: %w", err)
	}
	if task.State != TaskStateQueued || task.Stage != TaskStageQueued ||
		(task.Kind != TaskKindIssue && task.Kind != TaskKindRenew) {
		return task, fmt.Errorf("run certificate order: %w", ErrTaskActive)
	}
	if !task.CancelRequestedAt.IsZero() {
		return service.cancelled(taskCtx, task)
	}
	plan, err := service.repository.CertificateOrderPlan(taskCtx, task.PlanID, service.now().UTC())
	if err != nil {
		return service.fail(taskCtx, task, "certificate_plan_invalid", err)
	}
	account, err := service.repository.CertificateAccount(taskCtx, task.AccountID)
	if err != nil {
		return service.fail(taskCtx, task, "acme_account_invalid", ErrACMEAccountInvalid)
	}
	if statusErr := acmeAccountStatusError(account.Status); statusErr != nil {
		code := "acme_account_invalid"
		if errors.Is(statusErr, ErrACMEAccountDeactivated) {
			code = "acme_account_deactivated"
		} else if errors.Is(statusErr, ErrACMEAccountNeedsAttention) {
			code = "acme_account_needs_attention"
		}
		return service.fail(taskCtx, task, code, statusErr)
	}
	if account.Environment != plan.Environment {
		return service.fail(taskCtx, task, "acme_account_invalid", ErrACMEAccountInvalid)
	}
	task, err = service.advance(taskCtx, task, TaskStagePreparing)
	if err != nil {
		return task, err
	}
	key, err := service.vault.LoadAccountKey(taskCtx, account.ID)
	if err != nil {
		return service.fail(taskCtx, task, "acme_account_invalid", ErrACMEAccountInvalid)
	}
	client, err := service.acme.NewOrderClient(account.DirectoryURL, key, account.URI)
	if err != nil {
		return service.fail(taskCtx, task, "acme_account_invalid", ErrACMEAccountInvalid)
	}
	identifiers, err := decodePlanIdentifiers(plan.IdentifiersJSON, plan.Challenge)
	if err != nil {
		return service.fail(taskCtx, task, "certificate_identifier_invalid", err)
	}
	task, err = service.advance(taskCtx, task, TaskStageOrdering)
	if err != nil {
		return task, err
	}
	order, err := client.CreateOrder(taskCtx, identifiers)
	if err != nil || order.URI == "" || order.FinalizeURL == "" || len(order.AuthorizationURLs) == 0 || len(order.AuthorizationURLs) > 100 {
		cause := externalACMEError(taskCtx, "create ACME order", err)
		return service.fail(taskCtx, task, certificateErrorCode(cause), cause)
	}
	prepared, err := service.prepareAuthorizations(taskCtx, client, order, identifiers, plan.Challenge)
	if err != nil {
		return service.fail(taskCtx, task, "acme_order_failed", err)
	}
	task, err = service.advance(taskCtx, task, TaskStageProvisioning)
	if err != nil {
		return task, err
	}
	if plan.Challenge == ChallengeCloudflareDNS01 {
		task, err = service.advance(taskCtx, task, TaskStagePropagating)
		if err != nil {
			return task, err
		}
	}
	cleanup, err := service.provision(taskCtx, task, plan, client, prepared)
	if err != nil {
		var cleanupErr error
		if cleanup != nil {
			cleanupErr = runChallengeCleanup(taskCtx, cleanup)
		}
		if cleanupErr != nil || errors.Is(err, ErrChallengeCleanupFailed) {
			return service.needsAttention(
				taskCtx,
				task,
				"challenge_cleanup_failed",
				errors.Join(err, cleanupErr),
			)
		}
		return service.fail(taskCtx, task, certificateErrorCode(err), err)
	}
	task, err = service.advance(taskCtx, task, TaskStageAuthorizing)
	if err != nil {
		return task, err
	}
	for _, authorization := range prepared {
		if err := client.Accept(taskCtx, authorization.challenge); err != nil {
			if cleanupErr := runChallengeCleanup(taskCtx, cleanup); cleanupErr != nil {
				return service.needsAttention(taskCtx, task, "challenge_cleanup_failed", cleanupErr)
			}
			cause := externalACMEError(taskCtx, "accept ACME challenge", err)
			return service.fail(taskCtx, task, certificateErrorCode(cause), cause)
		}
		if err := client.WaitAuthorization(taskCtx, authorization.authorization.URI); err != nil {
			if cleanupErr := runChallengeCleanup(taskCtx, cleanup); cleanupErr != nil {
				return service.needsAttention(taskCtx, task, "challenge_cleanup_failed", cleanupErr)
			}
			cause := externalACMEError(taskCtx, "wait ACME authorization", err)
			return service.fail(taskCtx, task, certificateErrorCode(cause), cause)
		}
	}
	task, err = service.advance(taskCtx, task, TaskStageCleaning)
	if err != nil {
		return task, err
	}
	if err := runChallengeCleanup(taskCtx, cleanup); err != nil {
		return service.needsAttention(taskCtx, task, "challenge_cleanup_failed", err)
	}
	task, err = service.advance(taskCtx, task, TaskStageFinalizing)
	if err != nil {
		return task, err
	}
	if err := client.WaitOrderReady(taskCtx, order.URI); err != nil {
		cause := externalACMEError(taskCtx, "wait ACME order", err)
		return service.fail(taskCtx, task, certificateErrorCode(cause), cause)
	}
	certificateKey, csrDER, err := newCertificateCSR(service.random, identifiers)
	if err != nil {
		return service.fail(taskCtx, task, "certificate_file_invalid", err)
	}
	chainDER, err := client.Finalize(taskCtx, order.FinalizeURL, csrDER)
	if err != nil {
		cause := externalACMEError(taskCtx, "finalize ACME order", err)
		return service.fail(taskCtx, task, certificateErrorCode(cause), cause)
	}
	task, err = service.advance(taskCtx, task, TaskStageValidating)
	if err != nil {
		return task, err
	}
	issued, err := ValidateIssuedCertificate(chainDER, certificateKey, identifiers, service.now().UTC())
	if err != nil {
		return service.fail(taskCtx, task, certificateErrorCode(err), err)
	}
	if plan.Environment == EnvironmentStaging {
		return service.succeedStaging(taskCtx, task)
	}
	material, err := service.vault.StoreCertificateVersion(
		taskCtx, plan.CertificateID, plan.VersionID, issued, certificateKey,
	)
	if err != nil {
		return service.fail(taskCtx, task, "certificate_file_invalid", err)
	}
	return service.deployStoredCertificate(taskCtx, task, plan, material)
}

func (service *OrderService) deployStoredCertificate(
	ctx context.Context,
	task Task,
	plan OrderPlan,
	material StoredCertificateMaterial,
) (Task, error) {
	task, err := service.advance(ctx, task, TaskStageDeploying)
	if err != nil {
		cleanupErr := service.deleteStoredCertificateVersion(ctx, task.CertificateID, task.VersionID)
		return task, errors.Join(err, cleanupErr)
	}
	deployment, err := service.publisher.Deploy(ctx, task, plan, material)
	if err != nil {
		if errors.Is(err, ErrConfigurationReleaseUncertain) {
			return service.needsAttention(ctx, task, "certificate_deployment_uncertain", err)
		}
		cleanupErr := service.deleteStoredCertificateVersion(ctx, task.CertificateID, task.VersionID)
		if cleanupErr != nil {
			return service.needsAttention(ctx, task, "certificate_material_cleanup_failed", errors.Join(err, cleanupErr))
		}
		return service.fail(ctx, task, certificateErrorCode(err), err)
	}
	if deployment.ReleaseID == "" {
		return service.needsAttention(ctx, task, "certificate_deployment_uncertain", ErrConfigurationReleaseUncertain)
	}
	task.ReleaseID = deployment.ReleaseID
	return service.completeProduction(ctx, task, plan, material, deployment.Bindings)
}

func (service *OrderService) deleteStoredCertificateVersion(
	ctx context.Context,
	certificateID CertificateID,
	versionID VersionID,
) error {
	cleanupContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
	err := service.vault.DeleteCertificateVersion(cleanupContext, certificateID, versionID)
	cancel()
	return err
}

func (service *OrderService) prepareAuthorizations(
	ctx context.Context,
	client ACMEOrderClient,
	order ACMEOrder,
	identifiers []string,
	challengeType ChallengeType,
) ([]preparedAuthorization, error) {
	prepared := make([]preparedAuthorization, 0, len(order.AuthorizationURLs))
	for _, uri := range order.AuthorizationURLs {
		if uri == "" || len(uri) > 2048 {
			return nil, ErrACMEUnavailable
		}
		authorization, err := client.Authorization(ctx, uri)
		if err != nil {
			return nil, externalACMEError(ctx, "read ACME authorization", err)
		}
		if authorization.Status == "valid" {
			continue
		}
		if authorization.Status != "pending" || !authorizationMatches(authorization, identifiers) {
			return nil, ErrACMEUnavailable
		}
		want := "http-01"
		if challengeType == ChallengeCloudflareDNS01 {
			want = "dns-01"
		}
		challenge, found := findACMEChallenge(authorization.Challenges, want)
		if !found {
			return nil, ErrACMEUnavailable
		}
		prepared = append(prepared, preparedAuthorization{authorization: authorization, challenge: challenge})
	}
	return prepared, nil
}

func (service *OrderService) provision(
	ctx context.Context,
	task Task,
	plan OrderPlan,
	client ACMEOrderClient,
	prepared []preparedAuthorization,
) (func(context.Context) error, error) {
	if len(prepared) == 0 {
		return nil, nil
	}
	switch plan.Challenge {
	case ChallengeCloudflareDNS01:
		if service.cloudflare == nil || service.dnsWaiter == nil {
			return nil, ErrCloudflareUnavailable
		}
		token, err := service.vault.LoadCloudflareToken(ctx, string(plan.DNSCredentialID))
		if err != nil {
			return nil, ErrCloudflareTokenInvalid
		}
		if err := service.repository.MarkCertificateDNSCredentialUsed(
			ctx, plan.DNSCredentialID, service.now().UTC(),
		); err != nil {
			return nil, fmt.Errorf("record Cloudflare credential use: %w", ErrCloudflareUnavailable)
		}
		cleanup := func(cleanupContext context.Context) error {
			return service.cleanupDNS(cleanupContext, task.ID, plan.DNSCredentialID, token)
		}
		for _, value := range prepared {
			dnsValue, err := client.DNS01Record(value.challenge.Token)
			if err != nil || dnsValue == "" || len(dnsValue) > 1024 {
				return cleanup, ErrACMEUnavailable
			}
			identifier := strings.TrimPrefix(value.authorization.Identifier, "*.")
			zone, err := service.cloudflare.FindZone(ctx, token, identifier)
			if err != nil {
				return cleanup, err
			}
			name := "_acme-challenge." + identifier
			record, err := service.cloudflare.CreateTXT(ctx, token, zone.ID, name, dnsValue, string(task.ID[:8]))
			if err != nil {
				return cleanup, err
			}
			artifactID, err := NewArtifactID(service.random)
			if err != nil {
				deleteContext, cancel := detachedOperationContext(ctx, challengeCleanupTimeout)
				deleteErr := service.cloudflare.DeleteRecord(deleteContext, token, zone.ID, record.ID)
				cancel()
				if deleteErr != nil {
					return cleanup, errors.Join(ErrCloudflareUnavailable, ErrChallengeCleanupFailed, deleteErr)
				}
				return cleanup, ErrCloudflareUnavailable
			}
			now := service.now().UTC()
			artifact := ChallengeArtifact{
				ID: artifactID, TaskID: task.ID, Kind: ArtifactCloudflareTXT, State: ArtifactStateCreated,
				DNSCredentialID: plan.DNSCredentialID, ZoneID: zone.ID, RecordID: record.ID,
				RecordName: record.Name, CreatedAt: now, UpdatedAt: now,
			}
			if err := service.repository.CreateCertificateChallengeArtifact(ctx, artifact); err != nil {
				deleteContext, cancel := detachedOperationContext(ctx, challengeCleanupTimeout)
				deleteErr := service.cloudflare.DeleteRecord(deleteContext, token, zone.ID, record.ID)
				cancel()
				if deleteErr != nil {
					return cleanup, errors.Join(ErrCloudflareUnavailable, ErrChallengeCleanupFailed, deleteErr)
				}
				return cleanup, ErrCloudflareUnavailable
			}
			observed, err := service.cloudflare.ReadTXT(ctx, token, zone.ID, record.ID)
			if err != nil || observed.ID != record.ID || observed.ZoneID != zone.ID ||
				observed.Name != record.Name || observed.Content != dnsValue {
				return cleanup, ErrCloudflareUnavailable
			}
			if err := service.dnsWaiter.Wait(ctx, zone, record.Name, dnsValue); err != nil {
				return cleanup, err
			}
		}
		return cleanup, nil
	case ChallengeHTTP01:
		if service.http == nil {
			return nil, ErrACMEUnavailable
		}
		var refs []ServerRef
		if err := json.Unmarshal([]byte(plan.ServerRefsJSON), &refs); err != nil || len(refs) == 0 {
			return nil, ErrBindingConflict
		}
		responses := make([]HTTPChallengeResponse, 0, len(prepared))
		for _, value := range prepared {
			response, err := client.HTTP01Response(value.challenge.Token)
			if err != nil || response == "" {
				return nil, ErrACMEUnavailable
			}
			responses = append(responses, HTTPChallengeResponse{
				Identifier: value.authorization.Identifier, Token: value.challenge.Token, KeyAuthorization: response,
			})
		}
		if err := service.http.Provision(ctx, task, refs, responses); err != nil {
			return func(cleanupContext context.Context) error { return service.http.Cleanup(cleanupContext, task.ID) }, err
		}
		return func(cleanupContext context.Context) error { return service.http.Cleanup(cleanupContext, task.ID) }, nil
	default:
		return nil, ErrIdentifierInvalid
	}
}

func (service *OrderService) cleanupDNS(
	ctx context.Context,
	taskID TaskID,
	credentialID DNSCredentialID,
	token string,
) error {
	artifacts, err := service.repository.CertificateChallengeArtifacts(ctx, taskID)
	if err != nil {
		return ErrCloudflareUnavailable
	}
	var cleanupErr error
	for _, artifact := range artifacts {
		if artifact.State == ArtifactStateCleaned {
			continue
		}
		if ValidateArtifact(artifact) != nil || artifact.TaskID != taskID ||
			artifact.Kind != ArtifactCloudflareTXT || artifact.DNSCredentialID != credentialID {
			cleanupErr = errors.Join(cleanupErr, ErrChallengeCleanupFailed)
			continue
		}
		if err := service.cloudflare.DeleteRecord(ctx, token, artifact.ZoneID, artifact.RecordID); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		if err := service.repository.UpdateCertificateChallengeArtifact(
			ctx, artifact.ID, ArtifactStateCleaned, service.now().UTC(),
		); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func (service *OrderService) completeProduction(
	ctx context.Context,
	task Task,
	plan OrderPlan,
	material StoredCertificateMaterial,
	bindings []Binding,
) (Task, error) {
	now := service.now().UTC()
	state := CertificateStateUnbound
	var refs []ServerRef
	if json.Unmarshal([]byte(plan.ServerRefsJSON), &refs) == nil && len(refs) > 0 {
		state = CertificateStateActive
	}
	renewBefore := defaultRenewBefore
	var item Certificate
	var oldVersionID VersionID
	switch task.Kind {
	case TaskKindIssue:
		item = Certificate{
			ID: plan.CertificateID, PrimaryIdentifier: plan.PrimaryIdentifier, IdentifiersJSON: plan.IdentifiersJSON,
			Challenge: plan.Challenge, AccountID: plan.AccountID, DNSCredentialID: plan.DNSCredentialID,
			State: state, ActiveVersionID: plan.VersionID, AutoRenew: true,
			RenewBeforeSeconds: int64(defaultRenewBefore / time.Second),
			NotBefore:          material.NotBefore, NotAfter: material.NotAfter,
			CreatedBy: task.CreatedBy, RequestID: task.RequestID, CreatedAt: task.CreatedAt, UpdatedAt: now,
		}
	case TaskKindRenew:
		current, err := service.repository.Certificate(ctx, plan.CertificateID)
		if err != nil || current.ActiveVersionID == "" || current.ActiveVersionID == plan.VersionID {
			if err == nil {
				err = ErrBindingConflict
			}
			return service.needsAttention(ctx, task, "certificate_metadata_commit_failed", err)
		}
		oldVersionID = current.ActiveVersionID
		renewBefore = time.Duration(current.RenewBeforeSeconds) * time.Second
		item = current
		item.State = state
		item.ActiveVersionID = plan.VersionID
		item.RetryCount = 0
		item.RetryAt = time.Time{}
		item.LastErrorCode = ""
		item.NotBefore = material.NotBefore
		item.NotAfter = material.NotAfter
		item.UpdatedAt = now
	case TaskKindBind, TaskKindUnbind:
		return service.fail(ctx, task, "certificate_metadata_commit_failed", ErrBindingConflict)
	default:
		return service.fail(ctx, task, "certificate_metadata_commit_failed", ErrBindingConflict)
	}
	nextRenewal, err := renewalTime(material.NotAfter, renewBefore, service.random)
	if err != nil {
		return service.fail(ctx, task, "certificate_file_invalid", err)
	}
	item.NextRenewalAt = nextRenewal
	version := Version{
		ID: plan.VersionID, CertificateID: plan.CertificateID, State: VersionStateActive,
		FullchainDigest: material.FullchainDigest, PrivateKeyDigest: material.PrivateKeyDigest,
		LeafFingerprint: material.LeafFingerprint, SerialNumber: material.SerialNumber,
		Issuer: material.Issuer, NotBefore: material.NotBefore, NotAfter: material.NotAfter, CreatedAt: now,
	}
	next := task
	next.State = TaskStateSucceeded
	next.Stage = TaskStageCompleted
	next.UpdatedAt = now
	next.FinishedAt = now
	next.LastErrorCode = ""
	stage := service.nextStage(next, TaskStageCompleted, StageResultSuccess, "")
	var commitErr error
	if task.Kind == TaskKindRenew {
		commitErr = service.repository.CompleteRenewedCertificateTask(
			ctx, task.State, task.Stage, next, stage, item, version, bindings, oldVersionID,
		)
	} else {
		commitErr = service.repository.CompleteIssuedCertificateTask(
			ctx, task.State, task.Stage, next, stage, item, version, bindings,
		)
	}
	if commitErr != nil {
		return service.needsAttention(ctx, task, "certificate_metadata_commit_failed", commitErr)
	}
	next.Stages = append(append([]TaskStage{}, task.Stages...), stage)
	return next, nil
}

func (service *OrderService) succeedStaging(ctx context.Context, task Task) (Task, error) {
	now := service.now().UTC()
	next := task
	next.State = TaskStateSucceeded
	next.Stage = TaskStageCompleted
	next.UpdatedAt = now
	next.FinishedAt = now
	stage := service.nextStage(next, TaskStageCompleted, StageResultSuccess, "")
	if err := service.repository.TransitionCertificateTask(ctx, task.State, task.Stage, next, stage); err != nil {
		return task, err
	}
	next.Stages = append(append([]TaskStage{}, task.Stages...), stage)
	return next, nil
}

func (service *OrderService) advance(
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
	stage := service.nextStage(next, stageName, StageResultRunning, "")
	if err := service.repository.TransitionCertificateTask(ctx, current.State, current.Stage, next, stage); err != nil {
		return current, fmt.Errorf("advance certificate task: %w", err)
	}
	next.Stages = append(append([]TaskStage{}, current.Stages...), stage)
	return next, nil
}

func (service *OrderService) fail(ctx context.Context, task Task, code string, cause error) (Task, error) {
	if task.State.Terminal() {
		return task, cause
	}
	if errors.Is(cause, context.Canceled) || ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		lookupContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
		current, readErr := service.repository.CertificateTask(lookupContext, task.ID)
		cancel()
		if readErr == nil && !current.CancelRequestedAt.IsZero() {
			return service.cancelled(ctx, current)
		}
		code = "certificate_task_interrupted"
	}
	if errors.Is(cause, context.DeadlineExceeded) || ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = "certificate_task_timed_out"
	}
	now := service.now().UTC()
	next := task
	next.State = TaskStateFailed
	next.Stage = TaskStageFailed
	next.LastErrorCode = code
	next.UpdatedAt = now
	next.FinishedAt = now
	stage := service.nextStage(next, TaskStageFailed, StageResultFailed, code)
	commitContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
	var err error
	if task.Kind == TaskKindRenew {
		item, readErr := service.repository.Certificate(commitContext, task.CertificateID)
		if readErr != nil {
			err = readErr
		} else {
			item.RetryCount++
			item.RetryAt, err = RenewalRetryTime(now, item.RetryCount, service.random)
			var rateLimit *ACMERateLimitError
			if err == nil && errors.As(cause, &rateLimit) && rateLimit.RetryAfter > 0 {
				retryAfter := now.Add(rateLimit.RetryAfter)
				if retryAfter.After(item.RetryAt) {
					item.RetryAt = retryAfter
				}
			}
			item.LastErrorCode = code
			item.UpdatedAt = now
			switch {
			case !now.Before(item.NotAfter):
				item.State = CertificateStateExpired
			case item.NotAfter.Sub(now) <= 7*24*time.Hour:
				item.State = CertificateStateExpiring
			}
			if err == nil {
				err = service.repository.FailCertificateRenewalTask(
					commitContext, task.State, task.Stage, next, stage, item,
				)
			}
		}
	} else {
		err = service.repository.TransitionCertificateTask(commitContext, task.State, task.Stage, next, stage)
	}
	cancel()
	if err != nil {
		return task, errors.Join(cause, err)
	}
	next.Stages = append(append([]TaskStage{}, task.Stages...), stage)
	return next, cause
}

func (service *OrderService) needsAttention(ctx context.Context, task Task, code string, cause error) (Task, error) {
	if task.State.Terminal() {
		return task, cause
	}
	now := service.now().UTC()
	next := task
	next.State = TaskStateNeedsAttention
	next.Stage = TaskStageNeedsAttention
	next.LastErrorCode = code
	next.UpdatedAt = now
	next.FinishedAt = now
	stage := service.nextStage(next, TaskStageNeedsAttention, StageResultFailed, code)
	commitContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
	err := service.repository.TransitionCertificateTask(commitContext, task.State, task.Stage, next, stage)
	cancel()
	if err != nil {
		return task, errors.Join(cause, err)
	}
	next.Stages = append(append([]TaskStage{}, task.Stages...), stage)
	return next, cause
}

func (service *OrderService) cancelled(ctx context.Context, task Task) (Task, error) {
	if task.State.Terminal() {
		return task, context.Canceled
	}
	now := service.now().UTC()
	next := task
	next.State = TaskStateCancelled
	next.Stage = TaskStageCancelled
	next.LastErrorCode = "certificate_task_cancelled"
	if next.CancelRequestedAt.IsZero() {
		next.CancelRequestedAt = now
	}
	next.UpdatedAt = now
	next.FinishedAt = now
	stage := service.nextStage(next, TaskStageCancelled, StageResultFailed, next.LastErrorCode)
	commitContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
	err := service.repository.TransitionCertificateTask(commitContext, task.State, task.Stage, next, stage)
	cancel()
	if err != nil {
		return task, errors.Join(context.Canceled, err)
	}
	next.Stages = append(append([]TaskStage{}, task.Stages...), stage)
	return next, context.Canceled
}

func detachedOperationContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func runChallengeCleanup(parent context.Context, cleanup func(context.Context) error) error {
	if cleanup == nil {
		return nil
	}
	cleanupContext, cancel := detachedOperationContext(parent, challengeCleanupTimeout)
	err := cleanup(cleanupContext)
	cancel()
	return err
}

func (service *OrderService) nextStage(task Task, name TaskStageName, result StageResult, code string) TaskStage {
	return TaskStage{
		TaskID: task.ID, Sequence: uint64(len(task.Stages) + 1), Stage: name,
		Result: result, Code: code, PublicDetailsJSON: `{}`, OccurredAt: service.now().UTC(),
	}
}

func decodePlanIdentifiers(payload string, challenge ChallengeType) ([]string, error) {
	var identifiers []string
	if len(payload) > 65536 || json.Unmarshal([]byte(payload), &identifiers) != nil {
		return nil, ErrIdentifierInvalid
	}
	return NormalizeIdentifiers(identifiers, challenge)
}

func findACMEChallenge(challenges []ACMEChallenge, challengeType string) (ACMEChallenge, bool) {
	for _, challenge := range challenges {
		if challenge.Type == challengeType && challenge.URI != "" && len(challenge.URI) <= 2048 &&
			challenge.Token != "" && len(challenge.Token) <= 512 && !strings.ContainsAny(challenge.Token, "\x00\r\n") {
			return challenge, true
		}
	}
	return ACMEChallenge{}, false
}

func authorizationMatches(authorization ACMEAuthorization, identifiers []string) bool {
	want := authorization.Identifier
	if authorization.Wildcard {
		want = "*." + strings.TrimPrefix(want, "*.")
	}
	normalized, err := NormalizeIdentifiers([]string{want}, ChallengeCloudflareDNS01)
	if err != nil || len(normalized) != 1 {
		return false
	}
	for _, identifier := range identifiers {
		if identifier == normalized[0] {
			return true
		}
	}
	return false
}

func newCertificateCSR(random io.Reader, identifiers []string) (crypto.Signer, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, nil, fmt.Errorf("generate certificate key: %w", err)
	}
	request := &x509.CertificateRequest{
		Subject: pkix.Name{}, DNSNames: append([]string{}, identifiers...),
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	csrDER, err := x509.CreateCertificateRequest(random, request, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate request: %w", err)
	}
	return key, csrDER, nil
}

func renewalTime(notAfter time.Time, renewBefore time.Duration, random io.Reader) (time.Time, error) {
	if notAfter.IsZero() || renewBefore <= 0 || random == nil {
		return time.Time{}, ErrCertificateInvalid
	}
	var raw [8]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return time.Time{}, err
	}
	span := uint64(2*maximumRenewalJitter + 1)
	jitter := time.Duration(binary.BigEndian.Uint64(raw[:])%span) - maximumRenewalJitter
	return notAfter.UTC().Add(-renewBefore).Add(jitter), nil
}

func certificateErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrACMERateLimited), errors.Is(err, ErrCloudflareRateLimited):
		return "acme_rate_limited"
	case errors.Is(err, ErrConfigurationReleaseFailed):
		return "certificate_deployment_failed"
	case errors.Is(err, ErrPlanChanged), errors.Is(err, ErrBindingConflict):
		return "certificate_binding_conflict"
	case errors.Is(err, ErrCloudflareTokenInvalid):
		return "cloudflare_token_invalid"
	case errors.Is(err, ErrCloudflarePermission):
		return "cloudflare_permission_denied"
	case errors.Is(err, ErrCloudflareZoneNotFound):
		return "cloudflare_zone_not_found"
	case errors.Is(err, ErrCertificateSANMismatch):
		return "certificate_san_mismatch"
	case errors.Is(err, ErrCertificateKeyMismatch):
		return "certificate_key_mismatch"
	case errors.Is(err, context.DeadlineExceeded):
		return "certificate_task_timed_out"
	default:
		return "acme_order_failed"
	}
}
