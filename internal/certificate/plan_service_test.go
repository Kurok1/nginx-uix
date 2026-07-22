/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestPlanServicePersistsDigestBoundCloudflareWildcardReview(t *testing.T) {
	now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	source := "events {}\nhttp {\n  server { listen 80; server_name example.com; }\n}\n"
	snapshot := certificatePlanSnapshot(t, source)
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := BuildServerCandidates(project)
	if err != nil {
		t.Fatal(err)
	}
	repository := &planRepositoryStub{
		account: Account{
			ID: "11111111111111111111111111111111", Environment: EnvironmentProduction,
			Status: AccountStatusValid,
		},
		credential: DNSCredential{
			ID: "22222222222222222222222222222222", Provider: DNSProviderCloudflare,
			Status: CredentialStatusValid,
		},
	}
	workspaces := &planWorkspaceStub{snapshot: snapshot}
	service, err := NewPlanService(PlanServiceOptions{
		Repository: repository, Workspaces: workspaces, Random: rand.Reader,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := service.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request-plan"}, CreateOrderPlanInput{
		Identifiers: []string{"*.example.com", "example.com"}, Challenge: ChallengeCloudflareDNS01,
		AccountID: repository.account.ID, DNSCredentialID: repository.credential.ID,
		ServerRefs: []ServerRef{refs[0].Ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.plan.ID != planned.Plan.ID || !planned.Plan.RequiresRiskConfirm || planned.Plan.StagingEvidence {
		t.Fatalf("persisted plan = %#v", repository.plan)
	}
	if planned.Plan.PrimaryIdentifier != "*.example.com" || planned.Plan.ExpiresAt != now.Add(10*time.Minute) {
		t.Fatalf("planned order = %#v", planned.Plan)
	}
	if len(planned.Binding.Files) != 1 || !strings.Contains(planned.Binding.Files[0].Patch, CertificateFullchainPath(planned.Plan.CertificateID, planned.Plan.VersionID)) {
		t.Fatalf("binding review = %#v", planned.Binding)
	}
	if !workspaces.deleted || strings.Contains(repository.plan.BindingDiffJSON, "PRIVATE KEY") {
		t.Fatalf("workspace cleanup/secret-free diff = %#v %q", workspaces, repository.plan.BindingDiffJSON)
	}
}

func TestPlanServiceRejectsHTTPWildcardBeforeCreatingWorkspace(t *testing.T) {
	repository := &planRepositoryStub{account: Account{
		ID: "11111111111111111111111111111111", Environment: EnvironmentStaging, Status: AccountStatusValid,
	}}
	workspaces := &planWorkspaceStub{}
	service, err := NewPlanService(PlanServiceOptions{
		Repository: repository, Workspaces: workspaces, Random: rand.Reader, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request-plan"}, CreateOrderPlanInput{
		Identifiers: []string{"*.example.com"}, Challenge: ChallengeHTTP01, AccountID: repository.account.ID,
		ServerRefs: []ServerRef{{}},
	})
	if !errors.Is(err, ErrWildcardRequiresDNS) || workspaces.created {
		t.Fatalf("Create() = %v, workspace created=%v", err, workspaces.created)
	}
}

func TestPlanServiceReturnsFreshServerCandidatesAndRemovesWorkspace(t *testing.T) {
	snapshot := certificatePlanSnapshot(t, "events {}\nhttp { server { listen 80; server_name example.com; } }\n")
	workspaces := &planWorkspaceStub{snapshot: snapshot}
	service := &PlanService{workspaces: workspaces}
	candidates, err := service.ServerCandidates(
		context.Background(), config.Actor{UserID: 7, RequestID: "request-candidates"},
	)
	if err != nil || len(candidates) != 1 || !candidates[0].Editable || !workspaces.deleted {
		t.Fatalf("ServerCandidates()=%#v error=%v workspaces=%#v", candidates, err, workspaces)
	}
}

func certificatePlanSnapshot(t *testing.T, source string) config.DraftSnapshot {
	t.Helper()
	var digest config.Digest
	digest[0] = 0x44
	workspace := config.Workspace{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Certificate plan",
		State: config.StateReady, ProductionDigest: digest, BaseDigest: digest, DraftDigest: digest,
		Revision: 1, CreatedBy: 7, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	return config.DraftSnapshot{
		Workspace: workspace, WorkspaceETag: workspace.ETag(),
		Files: []config.DraftFile{{Path: "nginx.conf", Content: []byte(source)}},
	}
}

type planRepositoryStub struct {
	account    Account
	credential DNSCredential
	plan       OrderPlan
	evidence   bool
}

func (repository *planRepositoryStub) CertificateAccount(context.Context, AccountID) (Account, error) {
	return repository.account, nil
}

func (repository *planRepositoryStub) CertificateDNSCredential(context.Context, DNSCredentialID) (DNSCredential, error) {
	return repository.credential, nil
}

func (repository *planRepositoryStub) CreateCertificateOrderPlan(_ context.Context, plan OrderPlan) error {
	repository.plan = plan
	return nil
}

func (repository *planRepositoryStub) HasCertificateStagingEvidence(
	context.Context,
	string,
	ChallengeType,
	time.Time,
) (bool, error) {
	return repository.evidence, nil
}

type planWorkspaceStub struct {
	snapshot config.DraftSnapshot
	created  bool
	deleted  bool
}

func (workspace *planWorkspaceStub) Create(_ context.Context, _ config.Actor, name string) (config.Workspace, error) {
	workspace.created = true
	workspace.snapshot.Workspace.Name = name
	return workspace.snapshot.Workspace, nil
}

func (workspace *planWorkspaceStub) DraftSnapshot(context.Context, config.WorkspaceID) (config.DraftSnapshot, error) {
	return workspace.snapshot, nil
}

func (workspace *planWorkspaceStub) Delete(
	_ context.Context,
	_ config.Actor,
	_ config.WorkspaceID,
	_ string,
	_ string,
) error {
	workspace.deleted = true
	return nil
}
