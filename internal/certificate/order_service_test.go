/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

func TestOrderServiceRunsCloudflareDNS01ThroughExactCleanupAndDeployment(t *testing.T) {
	now := time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC)
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plan := OrderPlan{
		ID: "11111111111111111111111111111111", State: PlanStateExecuted,
		Environment: EnvironmentProduction, Challenge: ChallengeCloudflareDNS01,
		AccountID:         "22222222222222222222222222222222",
		DNSCredentialID:   "33333333333333333333333333333333",
		CertificateID:     "44444444444444444444444444444444",
		VersionID:         "55555555555555555555555555555555",
		PrimaryIdentifier: "*.example.com", IdentifiersJSON: `["*.example.com","example.com"]`,
		ServerRefsJSON:  `[{"path":"nginx.conf","start_offset":20,"server_names":["example.com"],"listeners":["80"],"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`,
		BindingDiffJSON: `[{"path":"nginx.conf","patch":"safe diff","added_lines":3,"removed_lines":0}]`,
		ExpiresAt:       now.Add(9 * time.Minute), CreatedBy: 7, RequestID: "request-plan", CreatedAt: now.Add(-time.Minute),
		ExecutedAt: now,
	}
	task := Task{
		ID: "66666666666666666666666666666666", Kind: TaskKindIssue,
		State: TaskStateQueued, Stage: TaskStageQueued, PlanID: plan.ID,
		CertificateID: plan.CertificateID, VersionID: plan.VersionID, AccountID: plan.AccountID,
		DNSCredentialID: plan.DNSCredentialID, Challenge: plan.Challenge,
		CreatedBy: 7, RequestID: "request-task", CreatedAt: now, UpdatedAt: now,
		Stages: []TaskStage{{
			TaskID: "66666666666666666666666666666666", Sequence: 1,
			Stage: TaskStageQueued, Result: StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now,
		}},
	}
	repository := &orderRepositoryStub{
		plan: plan, task: task,
		account: Account{
			ID: plan.AccountID, Environment: EnvironmentProduction,
			DirectoryURL: LetsEncryptProductionDirectory,
			URI:          "https://acme-v02.api.letsencrypt.org/acme/acct/1",
			Status:       AccountStatusValid,
		},
	}
	acmeClient := newOrderACMEClientStub(t, now)
	cloudflare := &orderCloudflareStub{}
	waiter := &orderDNSWaiterStub{}
	vault := &orderVaultStub{accountKey: accountKey, token: "cfut_secret-value"}
	publisher := &orderPublisherStub{}
	service, err := NewOrderService(OrderServiceOptions{
		Repository: repository, Vault: vault, ACME: orderACMEFactoryStub{client: acmeClient},
		Cloudflare: cloudflare, DNSWaiter: waiter, Publisher: publisher,
		Random: rand.Reader, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.State != TaskStateSucceeded || result.Stage != TaskStageCompleted {
		t.Fatalf("result = %#v", result)
	}
	propagating := false
	for _, stage := range result.Stages {
		propagating = propagating || stage.Stage == TaskStagePropagating
	}
	if !propagating {
		t.Fatalf("DNS task stages did not expose propagation: %#v", result.Stages)
	}
	if !acmeClient.accepted || !acmeClient.waited || !acmeClient.orderReady || !acmeClient.finalized {
		t.Fatalf("ACME flow = %#v", acmeClient)
	}
	if cloudflare.createdName != "_acme-challenge.example.com" ||
		cloudflare.readZoneID != cloudflare.zone.ID || cloudflare.readRecordID != cloudflare.record.ID ||
		cloudflare.deletedZoneID != cloudflare.zone.ID || cloudflare.deletedRecordID != cloudflare.record.ID {
		t.Fatalf("Cloudflare flow = %#v", cloudflare)
	}
	if waiter.name != cloudflare.createdName || waiter.value != acmeClient.dnsValue {
		t.Fatalf("DNS propagation = %#v", waiter)
	}
	if len(repository.artifacts) != 1 || repository.artifacts[0].State != ArtifactStateCleaned ||
		repository.artifacts[0].RecordID != cloudflare.record.ID {
		t.Fatalf("durable artifacts = %#v", repository.artifacts)
	}
	if vault.storedCertificateID != plan.CertificateID || vault.storedVersionID != plan.VersionID ||
		repository.usedCredentialID != plan.DNSCredentialID ||
		!publisher.deployed || repository.completedCertificate.State != CertificateStateActive ||
		repository.completedCertificate.NextRenewalAt.IsZero() {
		t.Fatalf("material/deployment/metadata = %#v %#v %#v", vault, publisher, repository.completedCertificate)
	}
}

func TestOrderServiceCompletesRenewalBySupersedingExistingVersion(t *testing.T) {
	now := time.Date(2026, 7, 21, 21, 0, 0, 0, time.UTC)
	oldVersionID := VersionID("11111111111111111111111111111111")
	newVersionID := VersionID("22222222222222222222222222222222")
	certificateID := CertificateID("33333333333333333333333333333333")
	existing := Certificate{
		ID: certificateID, PrimaryIdentifier: "example.com", IdentifiersJSON: `["example.com"]`,
		Challenge: ChallengeHTTP01, AccountID: "44444444444444444444444444444444",
		State: CertificateStateActive, ActiveVersionID: oldVersionID, AutoRenew: true,
		RenewBeforeSeconds: int64(defaultRenewBefore / time.Second), NextRenewalAt: now,
		NotBefore: now.Add(-60 * 24 * time.Hour), NotAfter: now.Add(30 * 24 * time.Hour),
		CreatedBy: 7, RequestID: "request-original", CreatedAt: now.Add(-60 * 24 * time.Hour), UpdatedAt: now,
	}
	task := Task{
		ID: "55555555555555555555555555555555", Kind: TaskKindRenew,
		State: TaskStateRunning, Stage: TaskStageDeploying,
		PlanID: "66666666666666666666666666666666", CertificateID: certificateID, VersionID: newVersionID,
		AccountID: existing.AccountID, Challenge: ChallengeHTTP01,
		CreatedBy: 7, RequestID: "request-renew", CreatedAt: now, UpdatedAt: now, StartedAt: now,
		Stages: []TaskStage{{
			TaskID: "55555555555555555555555555555555", Sequence: 1,
			Stage: TaskStageDeploying, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now,
		}},
	}
	plan := OrderPlan{
		ID: task.PlanID, State: PlanStateExecuted, Environment: EnvironmentProduction,
		Challenge: ChallengeHTTP01, AccountID: task.AccountID, CertificateID: certificateID, VersionID: newVersionID,
		PrimaryIdentifier: "example.com", IdentifiersJSON: `["example.com"]`, ServerRefsJSON: `[]`,
		BindingDiffJSON: `[]`, ExpiresAt: now.Add(time.Minute), CreatedBy: 7,
		RequestID: "request-renew", CreatedAt: now, ExecutedAt: now,
	}
	repository := &orderRepositoryStub{task: task, existingCertificate: existing}
	service := &OrderService{repository: repository, random: rand.Reader, now: func() time.Time { return now }}
	material := validStoredMaterial(now)
	result, err := service.completeProduction(context.Background(), task, plan, material, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != TaskStateSucceeded || repository.renewedCertificate.ActiveVersionID != newVersionID ||
		repository.supersededVersionID != oldVersionID ||
		repository.renewedCertificate.CreatedAt != existing.CreatedAt ||
		repository.renewedCertificate.NextRenewalAt.IsZero() {
		t.Fatalf("result=%#v renewed=%#v old=%s", result, repository.renewedCertificate, repository.supersededVersionID)
	}
}

func TestOrderServiceHonorsPersistedCancellationBeforeExternalWork(t *testing.T) {
	now := time.Date(2026, 7, 21, 22, 0, 0, 0, time.UTC)
	task := Task{
		ID: "11111111111111111111111111111111", Kind: TaskKindIssue,
		State: TaskStateQueued, Stage: TaskStageQueued,
		PlanID: "22222222222222222222222222222222", AccountID: "33333333333333333333333333333333",
		Challenge: ChallengeHTTP01, CreatedBy: 7, RequestID: "request-cancel",
		CancelRequestedAt: now, CreatedAt: now.Add(-time.Second), UpdatedAt: now,
		Stages: []TaskStage{{
			TaskID: "11111111111111111111111111111111", Sequence: 1,
			Stage: TaskStageQueued, Result: StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now.Add(-time.Second),
		}},
	}
	repository := &orderRepositoryStub{task: task}
	service := &OrderService{repository: repository, now: func() time.Time { return now }}
	result, err := service.Run(context.Background(), task.ID)
	if !errors.Is(err, context.Canceled) || result.State != TaskStateCancelled ||
		result.Stage != TaskStageCancelled || result.LastErrorCode != "certificate_task_cancelled" {
		t.Fatalf("Run(cancelled)=%#v error=%v", result, err)
	}
}

func TestOrderServiceSkipsChallengePublicationWhenAuthorizationsAreAlreadyValid(t *testing.T) {
	now := time.Date(2026, 7, 21, 22, 30, 0, 0, time.UTC)
	task := Task{
		ID: "11111111111111111111111111111111", Kind: TaskKindIssue,
		State: TaskStateRunning, Stage: TaskStageProvisioning,
		PlanID: "22222222222222222222222222222222", AccountID: "33333333333333333333333333333333",
		Challenge: ChallengeHTTP01, CreatedBy: 7, RequestID: "request-valid-authz",
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, StartedAt: now.Add(-time.Minute),
	}
	plan := OrderPlan{
		Challenge:      ChallengeHTTP01,
		ServerRefsJSON: `[{"path":"nginx.conf","start_offset":1,"server_names":["example.com"],"listeners":["80"],"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`,
	}
	httpManager := &orderHTTPChallengeStub{err: errors.New("challenge publication must not run")}
	service := &OrderService{http: httpManager}
	cleanup, err := service.provision(context.Background(), task, plan, nil, nil)
	if err != nil || cleanup != nil || httpManager.provisioned {
		t.Fatalf("provision(already valid) cleanup=%v error=%v manager=%#v", cleanup != nil, err, httpManager)
	}
}

func TestOrderServiceMarksUntrackedDNSRecordCleanupFailure(t *testing.T) {
	now := time.Date(2026, 7, 21, 22, 45, 0, 0, time.UTC)
	task := Task{
		ID: "11111111111111111111111111111111", Kind: TaskKindIssue,
		State: TaskStateRunning, Stage: TaskStageProvisioning,
		PlanID: "22222222222222222222222222222222", AccountID: "33333333333333333333333333333333",
		DNSCredentialID: "44444444444444444444444444444444", Challenge: ChallengeCloudflareDNS01,
		CreatedBy: 7, RequestID: "request-untracked-record", CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now, StartedAt: now.Add(-time.Minute),
	}
	repository := &orderRepositoryStub{artifactErr: errors.New("persist artifact failed")}
	provider := &orderCloudflareStub{deleteErr: errors.New("exact delete failed")}
	client := newOrderACMEClientStub(t, now)
	service := &OrderService{
		repository: repository, vault: &orderVaultStub{token: "cfut_secret-value"},
		cloudflare: provider, dnsWaiter: &orderDNSWaiterStub{}, random: rand.Reader,
		now: func() time.Time { return now },
	}
	cleanup, err := service.provision(context.Background(), task, OrderPlan{
		Challenge: ChallengeCloudflareDNS01, DNSCredentialID: task.DNSCredentialID,
	}, client, []preparedAuthorization{{
		authorization: ACMEAuthorization{Identifier: "example.com"},
		challenge:     ACMEChallenge{Type: "dns-01", Token: "secret-token"},
	}})
	if cleanup == nil || !errors.Is(err, ErrChallengeCleanupFailed) || len(repository.artifacts) != 0 ||
		provider.deletedRecordID != provider.record.ID {
		t.Fatalf("provision cleanup=%v error=%v artifacts=%#v provider=%#v", cleanup != nil, err, repository.artifacts, provider)
	}
}

func TestOrderServiceRefusesDNSCleanupWithMismatchedCredentialOwnership(t *testing.T) {
	now := time.Date(2026, 7, 21, 22, 50, 0, 0, time.UTC)
	taskID := TaskID("11111111111111111111111111111111")
	wantedCredential := DNSCredentialID("22222222222222222222222222222222")
	repository := &orderRepositoryStub{artifacts: []ChallengeArtifact{{
		ID: "33333333333333333333333333333333", TaskID: taskID, Kind: ArtifactCloudflareTXT,
		State: ArtifactStateCreated, DNSCredentialID: "44444444444444444444444444444444",
		ZoneID: "55555555555555555555555555555555", RecordID: "66666666666666666666666666666666",
		RecordName: "_acme-challenge.example.com", CreatedAt: now, UpdatedAt: now,
	}}}
	provider := &orderCloudflareStub{}
	service := &OrderService{repository: repository, cloudflare: provider, now: func() time.Time { return now }}
	err := service.cleanupDNS(context.Background(), taskID, wantedCredential, "cfut_secret-value")
	if !errors.Is(err, ErrChallengeCleanupFailed) || provider.deletedRecordID != "" {
		t.Fatalf("cleanup error=%v provider=%#v", err, provider)
	}
}

func TestOrderServiceReconcilesInterruptedDNSArtifactBeforeFailingTask(t *testing.T) {
	now := time.Date(2026, 7, 21, 23, 0, 0, 0, time.UTC)
	task := Task{
		ID: "11111111111111111111111111111111", Kind: TaskKindIssue,
		State: TaskStateRunning, Stage: TaskStageProvisioning,
		PlanID: "22222222222222222222222222222222", AccountID: "33333333333333333333333333333333",
		DNSCredentialID: "44444444444444444444444444444444", Challenge: ChallengeCloudflareDNS01,
		CreatedBy: 7, RequestID: "request-interrupted", CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
		StartedAt: now.Add(-time.Minute), Stages: []TaskStage{{
			TaskID: "11111111111111111111111111111111", Sequence: 1,
			Stage: TaskStageProvisioning, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now,
		}},
	}
	repository := &orderRepositoryStub{task: task, artifacts: []ChallengeArtifact{{
		ID: "55555555555555555555555555555555", TaskID: task.ID, Kind: ArtifactCloudflareTXT,
		State: ArtifactStateCreated, DNSCredentialID: task.DNSCredentialID,
		ZoneID: "66666666666666666666666666666666", RecordID: "77777777777777777777777777777777",
		RecordName: "_acme-challenge.example.com", CreatedAt: now, UpdatedAt: now,
	}}}
	provider := &orderCloudflareStub{}
	service := &OrderService{
		repository: repository, vault: &orderVaultStub{token: "cfut_secret-value"},
		cloudflare: provider, now: func() time.Time { return now },
	}
	reconciled, err := service.ReconcileInterrupted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled) != 1 || reconciled[0].State != TaskStateFailed ||
		reconciled[0].LastErrorCode != "certificate_task_interrupted" ||
		repository.artifacts[0].State != ArtifactStateCleaned ||
		provider.deletedRecordID != repository.artifacts[0].RecordID {
		t.Fatalf("reconciled=%#v artifacts=%#v provider=%#v", reconciled, repository.artifacts, provider)
	}
}

func TestOrderServicePersistsRenewalBackoffWithFailureTerminal(t *testing.T) {
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	task := Task{
		ID: "11111111111111111111111111111111", Kind: TaskKindRenew,
		State: TaskStateRunning, Stage: TaskStageAuthorizing,
		PlanID: "22222222222222222222222222222222", CertificateID: "33333333333333333333333333333333",
		VersionID: "44444444444444444444444444444444", AccountID: "55555555555555555555555555555555",
		Challenge: ChallengeHTTP01, CreatedBy: 7, RequestID: "request-renew-failure",
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, StartedAt: now.Add(-time.Minute),
		Stages: []TaskStage{{
			TaskID: "11111111111111111111111111111111", Sequence: 1,
			Stage: TaskStageAuthorizing, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now,
		}},
	}
	existing := Certificate{
		ID: task.CertificateID, PrimaryIdentifier: "example.com", IdentifiersJSON: `["example.com"]`,
		Challenge: ChallengeHTTP01, AccountID: task.AccountID, State: CertificateStateActive,
		ActiveVersionID: "66666666666666666666666666666666", AutoRenew: true,
		RenewBeforeSeconds: int64(defaultRenewBefore / time.Second), NextRenewalAt: now.Add(-time.Hour), RetryCount: 2,
		NotBefore: now.Add(-60 * 24 * time.Hour), NotAfter: now.Add(20 * 24 * time.Hour),
		CreatedBy: 7, RequestID: "request-original", CreatedAt: now.Add(-60 * 24 * time.Hour), UpdatedAt: now,
	}
	repository := &orderRepositoryStub{task: task, existingCertificate: existing}
	service := &OrderService{
		repository: repository, random: bytes.NewReader(make([]byte, 8)), now: func() time.Time { return now },
	}
	result, err := service.fail(context.Background(), task, "acme_order_failed", ErrACMEUnavailable)
	if !errors.Is(err, ErrACMEUnavailable) || result.State != TaskStateFailed ||
		repository.failedRenewal.RetryCount != 3 || repository.failedRenewal.RetryAt.IsZero() ||
		repository.failedRenewal.LastErrorCode != "acme_order_failed" {
		t.Fatalf("result=%#v error=%v failed certificate=%#v", result, err, repository.failedRenewal)
	}
}

func TestOrderServicePersistsLaterACMERetryAfterForRenewal(t *testing.T) {
	now := time.Date(2026, 7, 22, 4, 0, 0, 0, time.UTC)
	task := Task{
		ID: "11111111111111111111111111111111", Kind: TaskKindRenew,
		State: TaskStateRunning, Stage: TaskStageOrdering,
		PlanID: "22222222222222222222222222222222", CertificateID: "33333333333333333333333333333333",
		VersionID: "44444444444444444444444444444444", AccountID: "55555555555555555555555555555555",
		Challenge: ChallengeHTTP01, CreatedBy: 7, RequestID: "request-renew-rate-limit",
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, StartedAt: now.Add(-time.Minute),
		Stages: []TaskStage{{
			TaskID: "11111111111111111111111111111111", Sequence: 1,
			Stage: TaskStageOrdering, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now,
		}},
	}
	existing := Certificate{
		ID: task.CertificateID, PrimaryIdentifier: "example.com", IdentifiersJSON: `["example.com"]`,
		Challenge: ChallengeHTTP01, AccountID: task.AccountID, State: CertificateStateActive,
		ActiveVersionID: "66666666666666666666666666666666", AutoRenew: true,
		RenewBeforeSeconds: int64(defaultRenewBefore / time.Second), NextRenewalAt: now.Add(-time.Hour),
		NotBefore: now.Add(-60 * 24 * time.Hour), NotAfter: now.Add(20 * 24 * time.Hour),
		CreatedBy: 7, RequestID: "request-original", CreatedAt: now.Add(-60 * 24 * time.Hour), UpdatedAt: now,
	}
	repository := &orderRepositoryStub{task: task, existingCertificate: existing}
	service := &OrderService{
		repository: repository, random: bytes.NewReader(make([]byte, 8)), now: func() time.Time { return now },
	}
	cause := &ACMERateLimitError{RetryAfter: 10 * time.Hour}
	result, err := service.fail(context.Background(), task, "acme_rate_limited", cause)
	if !errors.Is(err, ErrACMERateLimited) || result.State != TaskStateFailed ||
		!repository.failedRenewal.RetryAt.Equal(now.Add(10*time.Hour)) ||
		repository.failedRenewal.LastErrorCode != "acme_rate_limited" {
		t.Fatalf("result=%#v error=%v failed certificate=%#v", result, err, repository.failedRenewal)
	}
}

func TestOrderServiceCleansMaterialOnlyAfterProvenSafeDeploymentFailure(t *testing.T) {
	now := time.Date(2026, 7, 22, 5, 0, 0, 0, time.UTC)
	task := Task{
		ID: "11111111111111111111111111111111", Kind: TaskKindIssue,
		State: TaskStateRunning, Stage: TaskStageValidating,
		PlanID: "22222222222222222222222222222222", CertificateID: "33333333333333333333333333333333",
		VersionID: "44444444444444444444444444444444", AccountID: "55555555555555555555555555555555",
		Challenge: ChallengeHTTP01, CreatedBy: 7, RequestID: "request-deployment",
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, StartedAt: now.Add(-time.Minute),
		Stages: []TaskStage{{
			TaskID: "11111111111111111111111111111111", Sequence: 1,
			Stage: TaskStageValidating, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now,
		}},
	}
	plan := OrderPlan{ID: task.PlanID, CertificateID: task.CertificateID, VersionID: task.VersionID}
	material := StoredCertificateMaterial{}

	for _, test := range []struct {
		name          string
		deploymentErr error
		wantState     TaskState
		wantDeleted   bool
	}{
		{name: "safe rollback", deploymentErr: ErrConfigurationReleaseFailed, wantState: TaskStateFailed, wantDeleted: true},
		{name: "uncertain release", deploymentErr: ErrConfigurationReleaseUncertain, wantState: TaskStateNeedsAttention},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &orderRepositoryStub{task: task}
			vault := &orderVaultStub{}
			publisher := &orderPublisherStub{err: test.deploymentErr}
			service := &OrderService{
				repository: repository, vault: vault, publisher: publisher,
				random: bytes.NewReader(make([]byte, 8)), now: func() time.Time { return now },
			}
			result, err := service.deployStoredCertificate(context.Background(), task, plan, material)
			if !errors.Is(err, test.deploymentErr) || result.State != test.wantState ||
				(vault.deletedVersionID != "") != test.wantDeleted {
				t.Fatalf("result=%#v error=%v vault=%#v", result, err, vault)
			}
		})
	}
}

type orderRepositoryStub struct {
	plan                 OrderPlan
	task                 Task
	account              Account
	artifacts            []ChallengeArtifact
	completedCertificate Certificate
	existingCertificate  Certificate
	renewedCertificate   Certificate
	supersededVersionID  VersionID
	failedRenewal        Certificate
	usedCredentialID     DNSCredentialID
	artifactErr          error
}

func (repository *orderRepositoryStub) CertificateTask(context.Context, TaskID) (Task, error) {
	return repository.task, nil
}

func (repository *orderRepositoryStub) CertificateTasks(context.Context, int) ([]Task, error) {
	return []Task{repository.task}, nil
}

func (repository *orderRepositoryStub) ActiveCertificateTasks(context.Context, int) ([]Task, error) {
	if repository.task.State.Terminal() {
		return nil, nil
	}
	return []Task{repository.task}, nil
}

func (repository *orderRepositoryStub) CertificateOrderPlan(context.Context, OrderPlanID, time.Time) (OrderPlan, error) {
	return repository.plan, nil
}

func (repository *orderRepositoryStub) CertificateAccount(context.Context, AccountID) (Account, error) {
	return repository.account, nil
}

func (repository *orderRepositoryStub) MarkCertificateDNSCredentialUsed(
	_ context.Context,
	id DNSCredentialID,
	_ time.Time,
) error {
	repository.usedCredentialID = id
	return nil
}

func (repository *orderRepositoryStub) TransitionCertificateTask(
	_ context.Context,
	expectedState TaskState,
	expectedStage TaskStageName,
	next Task,
	stage TaskStage,
) error {
	if repository.task.State != expectedState || repository.task.Stage != expectedStage {
		return ErrTaskActive
	}
	next.Stages = append(append([]TaskStage{}, repository.task.Stages...), stage)
	repository.task = next
	return nil
}

func (repository *orderRepositoryStub) CreateCertificateChallengeArtifact(_ context.Context, artifact ChallengeArtifact) error {
	if repository.artifactErr != nil {
		return repository.artifactErr
	}
	repository.artifacts = append(repository.artifacts, artifact)
	return nil
}

func (repository *orderRepositoryStub) CertificateChallengeArtifacts(context.Context, TaskID) ([]ChallengeArtifact, error) {
	return append([]ChallengeArtifact{}, repository.artifacts...), nil
}

func (repository *orderRepositoryStub) UpdateCertificateChallengeArtifact(
	_ context.Context,
	id ArtifactID,
	state ArtifactState,
	at time.Time,
) error {
	for index := range repository.artifacts {
		if repository.artifacts[index].ID == id {
			repository.artifacts[index].State = state
			repository.artifacts[index].UpdatedAt = at
		}
	}
	return nil
}

func (repository *orderRepositoryStub) CompleteIssuedCertificateTask(
	_ context.Context,
	expectedState TaskState,
	expectedStage TaskStageName,
	next Task,
	stage TaskStage,
	item Certificate,
	_ Version,
	_ []Binding,
) error {
	if repository.task.State != expectedState || repository.task.Stage != expectedStage {
		return ErrTaskActive
	}
	next.Stages = append(append([]TaskStage{}, repository.task.Stages...), stage)
	repository.task = next
	repository.completedCertificate = item
	return nil
}

func (repository *orderRepositoryStub) Certificate(context.Context, CertificateID) (Certificate, error) {
	return repository.existingCertificate, nil
}

func (repository *orderRepositoryStub) CompleteRenewedCertificateTask(
	_ context.Context,
	expectedState TaskState,
	expectedStage TaskStageName,
	next Task,
	stage TaskStage,
	item Certificate,
	_ Version,
	_ []Binding,
	oldVersionID VersionID,
) error {
	if repository.task.State != expectedState || repository.task.Stage != expectedStage {
		return ErrTaskActive
	}
	next.Stages = append(append([]TaskStage{}, repository.task.Stages...), stage)
	repository.task = next
	repository.renewedCertificate = item
	repository.supersededVersionID = oldVersionID
	return nil
}

func (repository *orderRepositoryStub) FailCertificateRenewalTask(
	_ context.Context,
	expectedState TaskState,
	expectedStage TaskStageName,
	next Task,
	stage TaskStage,
	item Certificate,
) error {
	if repository.task.State != expectedState || repository.task.Stage != expectedStage {
		return ErrTaskActive
	}
	next.Stages = append(append([]TaskStage{}, repository.task.Stages...), stage)
	repository.task = next
	repository.failedRenewal = item
	return nil
}

type orderVaultStub struct {
	accountKey           crypto.Signer
	token                string
	storedCertificateID  CertificateID
	storedVersionID      VersionID
	deletedCertificateID CertificateID
	deletedVersionID     VersionID
}

func (vault *orderVaultStub) DeleteCertificateVersion(
	_ context.Context,
	certificateID CertificateID,
	versionID VersionID,
) error {
	vault.deletedCertificateID = certificateID
	vault.deletedVersionID = versionID
	return nil
}

func (vault *orderVaultStub) LoadAccountKey(context.Context, AccountID) (crypto.Signer, error) {
	return vault.accountKey, nil
}

func (vault *orderVaultStub) LoadCloudflareToken(context.Context, string) (string, error) {
	return vault.token, nil
}

func (vault *orderVaultStub) StoreCertificateVersion(
	_ context.Context,
	certificateID CertificateID,
	versionID VersionID,
	issued IssuedCertificate,
	_ crypto.Signer,
) (StoredCertificateMaterial, error) {
	vault.storedCertificateID = certificateID
	vault.storedVersionID = versionID
	return StoredCertificateMaterial{
		FullchainDigest:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PrivateKeyDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		LeafFingerprint:  issued.LeafFingerprint, SerialNumber: issued.SerialNumber,
		Issuer: issued.Issuer, NotBefore: issued.NotBefore, NotAfter: issued.NotAfter,
	}, nil
}

type orderACMEFactoryStub struct{ client *orderACMEClientStub }

func (factory orderACMEFactoryStub) NewOrderClient(string, crypto.Signer, string) (ACMEOrderClient, error) {
	return factory.client, nil
}

type orderACMEClientStub struct {
	t               *testing.T
	now             time.Time
	dnsValue        string
	accepted        bool
	waited          bool
	orderReady      bool
	finalized       bool
	certificateAuth *ecdsa.PrivateKey
}

func newOrderACMEClientStub(t *testing.T, now time.Time) *orderACMEClientStub {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &orderACMEClientStub{t: t, now: now, dnsValue: "secret-dns-value", certificateAuth: key}
}

func (client *orderACMEClientStub) CreateOrder(context.Context, []string) (ACMEOrder, error) {
	return ACMEOrder{URI: "order-1", Status: "pending", AuthorizationURLs: []string{"authz-1"}, FinalizeURL: "finalize-1"}, nil
}

func (client *orderACMEClientStub) Authorization(context.Context, string) (ACMEAuthorization, error) {
	return ACMEAuthorization{
		URI: "authz-1", Status: "pending", Identifier: "example.com", Wildcard: true,
		Challenges: []ACMEChallenge{{Type: "dns-01", URI: "challenge-1", Token: "secret-token"}},
	}, nil
}

func (client *orderACMEClientStub) HTTP01Response(string) (string, error) { return "", nil }

func (client *orderACMEClientStub) DNS01Record(string) (string, error) { return client.dnsValue, nil }

func (client *orderACMEClientStub) Accept(context.Context, ACMEChallenge) error {
	client.accepted = true
	return nil
}

func (client *orderACMEClientStub) WaitAuthorization(context.Context, string) error {
	client.waited = true
	return nil
}

func (client *orderACMEClientStub) WaitOrderReady(context.Context, string) error {
	client.orderReady = true
	return nil
}

func (client *orderACMEClientStub) Finalize(_ context.Context, _ string, csrDER []byte) ([][]byte, error) {
	if !client.orderReady {
		return nil, errors.New("finalize called before order became ready")
	}
	client.finalized = true
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		client.t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		client.t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: csr.DNSNames[0]}, DNSNames: csr.DNSNames,
		NotBefore: client.now.Add(-time.Hour), NotAfter: client.now.Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	issuer := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: client.now.Add(-24 * time.Hour), NotAfter: client.now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, csr.PublicKey, client.certificateAuth)
	if err != nil {
		client.t.Fatal(err)
	}
	return [][]byte{der}, nil
}

type orderCloudflareStub struct {
	zone            CloudflareZone
	record          CloudflareRecord
	createdName     string
	readZoneID      string
	readRecordID    string
	deletedZoneID   string
	deletedRecordID string
	deleteErr       error
}

func (provider *orderCloudflareStub) FindZone(context.Context, string, string) (CloudflareZone, error) {
	provider.zone = CloudflareZone{
		ID: "77777777777777777777777777777777", Name: "example.com", NameServers: []string{"ns1.example.net"},
	}
	return provider.zone, nil
}

func (provider *orderCloudflareStub) CreateTXT(
	_ context.Context,
	_ string,
	zoneID, name, value, _ string,
) (CloudflareRecord, error) {
	provider.createdName = name
	provider.record = CloudflareRecord{
		ID: "88888888888888888888888888888888", ZoneID: zoneID, Name: name, Content: value,
	}
	return provider.record, nil
}

func (provider *orderCloudflareStub) ReadTXT(
	_ context.Context,
	_ string,
	zoneID, recordID string,
) (CloudflareRecord, error) {
	provider.readZoneID = zoneID
	provider.readRecordID = recordID
	return provider.record, nil
}

func (provider *orderCloudflareStub) DeleteRecord(_ context.Context, _ string, zoneID, recordID string) error {
	provider.deletedZoneID = zoneID
	provider.deletedRecordID = recordID
	return provider.deleteErr
}

type orderDNSWaiterStub struct {
	name  string
	value string
}

type orderHTTPChallengeStub struct {
	provisioned bool
	err         error
}

func (manager *orderHTTPChallengeStub) Provision(
	context.Context,
	Task,
	[]ServerRef,
	[]HTTPChallengeResponse,
) error {
	manager.provisioned = true
	return manager.err
}

func (manager *orderHTTPChallengeStub) Cleanup(context.Context, TaskID) error { return nil }

func (waiter *orderDNSWaiterStub) Wait(_ context.Context, _ CloudflareZone, name, value string) error {
	waiter.name = name
	waiter.value = value
	return nil
}

type orderPublisherStub struct {
	deployed bool
	err      error
}

func (publisher *orderPublisherStub) Deploy(
	context.Context,
	Task,
	OrderPlan,
	StoredCertificateMaterial,
) (DeploymentResult, error) {
	publisher.deployed = true
	if publisher.err != nil {
		return DeploymentResult{}, publisher.err
	}
	return DeploymentResult{ReleaseID: "99999999999999999999999999999999", Bindings: []Binding{}}, nil
}
