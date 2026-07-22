/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestQueueServiceRequiresFreshIdenticalPlanAndProductionRiskConfirmation(t *testing.T) {
	now := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
	snapshot := certificatePlanSnapshot(t, "events {}\nhttp { server { listen 80; server_name example.com; } }\n")
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := BuildServerCandidates(project)
	if err != nil {
		t.Fatal(err)
	}
	repository := &queueRepositoryStub{planRepositoryStub: planRepositoryStub{
		account: Account{
			ID: "11111111111111111111111111111111", Environment: EnvironmentProduction,
			Status: AccountStatusValid,
		},
		credential: DNSCredential{
			ID: "22222222222222222222222222222222", Provider: DNSProviderCloudflare,
			Status: CredentialStatusValid,
		},
	}}
	workspaces := &planWorkspaceStub{snapshot: snapshot}
	planner, err := NewPlanService(PlanServiceOptions{
		Repository: repository, Workspaces: workspaces, Random: rand.Reader,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := planner.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request-plan"}, CreateOrderPlanInput{
		Identifiers: []string{"example.com"}, Challenge: ChallengeCloudflareDNS01,
		AccountID: repository.account.ID, DNSCredentialID: repository.credential.ID,
		ServerRefs: []ServerRef{candidates[0].Ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.plan = planned.Plan
	queue, err := NewQueueService(QueueServiceOptions{
		Repository: repository, Planner: planner, Random: rand.Reader,
		Now: func() time.Time { return now.Add(time.Minute) },
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := config.Actor{UserID: 7, RequestID: "request-execute"}
	_, err = queue.Execute(context.Background(), actor, planned.Plan.ID, ExecuteOrderPlanInput{
		Confirmation: "example.com",
	})
	if !errors.Is(err, ErrProductionRiskConfirmationRequired) || repository.executedTask.ID != "" {
		t.Fatalf("Execute(without risk) = %v, task=%#v", err, repository.executedTask)
	}
	task, err := queue.Execute(context.Background(), actor, planned.Plan.ID, ExecuteOrderPlanInput{
		Confirmation: "example.com", ProductionRiskConfirmation: ProductionRiskConfirmation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.State != TaskStateQueued || repository.executedTask.ID != task.ID || repository.executedAt != now.Add(time.Minute) {
		t.Fatalf("queued task = %#v repository=%#v", task, repository)
	}
}

func TestQueueServiceAllowsUnboundDNSIssuanceButKeepsHTTPBound(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	snapshot := certificatePlanSnapshot(t, "events {}\nhttp { server { listen 80; server_name example.com; } }\n")
	repository := &queueRepositoryStub{planRepositoryStub: planRepositoryStub{
		account: Account{
			ID: "11111111111111111111111111111111", Environment: EnvironmentStaging,
			Status: AccountStatusValid,
		},
		credential: DNSCredential{
			ID: "22222222222222222222222222222222", Provider: DNSProviderCloudflare,
			Status: CredentialStatusValid,
		},
	}}
	planner, err := NewPlanService(PlanServiceOptions{
		Repository: repository, Workspaces: &planWorkspaceStub{snapshot: snapshot}, Random: rand.Reader,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := planner.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request-unbound"}, CreateOrderPlanInput{
		Identifiers: []string{"example.com"}, Challenge: ChallengeCloudflareDNS01,
		AccountID: repository.account.ID, DNSCredentialID: repository.credential.ID, ServerRefs: []ServerRef{},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.plan = planned.Plan
	queue, err := NewQueueService(QueueServiceOptions{
		Repository: repository, Planner: planner, Random: rand.Reader, Now: func() time.Time { return now.Add(time.Minute) },
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := queue.Execute(context.Background(), config.Actor{UserID: 7, RequestID: "request-execute"},
		planned.Plan.ID, ExecuteOrderPlanInput{Confirmation: "example.com"})
	if err != nil || task.State != TaskStateQueued || planned.Plan.ServerRefsJSON != `[]` || planned.Plan.BindingDiffJSON != `[]` {
		t.Fatalf("Execute(unbound DNS)=%#v error=%v plan=%#v", task, err, planned.Plan)
	}
}

type queueRepositoryStub struct {
	planRepositoryStub
	executedTask Task
	executedAt   time.Time
}

func (repository *queueRepositoryStub) CertificateOrderPlan(_ context.Context, _ OrderPlanID, now time.Time) (OrderPlan, error) {
	if !now.Before(repository.plan.ExpiresAt) {
		return OrderPlan{}, ErrPlanExpired
	}
	return repository.plan, nil
}

func (repository *queueRepositoryStub) ExecuteCertificateOrderPlan(
	_ context.Context,
	_ OrderPlanID,
	at time.Time,
	task Task,
	_ TaskStage,
) error {
	repository.executedAt = at
	repository.executedTask = task
	repository.plan.State = PlanStateExecuted
	return nil
}
