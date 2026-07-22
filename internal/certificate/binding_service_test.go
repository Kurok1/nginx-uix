/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io/fs"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestBindingServicePlansQueuesAndCompletesExactStandaloneBinding(t *testing.T) {
	now := time.Date(2026, 7, 21, 21, 0, 0, 0, time.UTC)
	snapshot := certificatePlanSnapshot(t, "events {}\nhttp { server { listen 80; server_name example.com; } }\n")
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := BuildServerCandidates(project)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("BuildServerCandidates()=%#v error=%v", candidates, err)
	}
	repository := &bindingRepositoryStub{item: Certificate{
		ID: "33333333333333333333333333333333", PrimaryIdentifier: "example.com",
		IdentifiersJSON: `["example.com"]`, Challenge: ChallengeCloudflareDNS01,
		AccountID: "11111111111111111111111111111111", DNSCredentialID: "22222222222222222222222222222222",
		State: CertificateStateUnbound, ActiveVersionID: "44444444444444444444444444444444",
		AutoRenew: true, RenewBeforeSeconds: 30 * 24 * 60 * 60, NextRenewalAt: now.Add(30 * 24 * time.Hour),
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour), CreatedBy: 7,
		RequestID: "request-issue", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}}
	planner := &PlanService{workspaces: &planWorkspaceStub{snapshot: snapshot}}
	publisher := &bindingPublisherStub{}
	service, err := NewBindingService(BindingServiceOptions{
		Repository: repository, Planner: planner, Publisher: publisher, Random: rand.Reader,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := config.Actor{UserID: 7, RequestID: "request-bind"}
	planned, err := service.CreatePlan(context.Background(), actor, repository.item.ID, CreateBindingPlanInput{
		ServerRefs: []ServerRef{candidates[0].Ref},
	})
	if err != nil || repository.plan.ID != planned.Plan.ID || len(planned.Binding.Files) != 1 {
		t.Fatalf("CreatePlan()=%#v error=%v repository=%#v", planned, err, repository)
	}
	task, err := service.ExecutePlan(context.Background(), actor, planned.Plan.ID, ExecuteBindingPlanInput{
		Confirmation: repository.item.PrimaryIdentifier,
	})
	if err != nil || task.Kind != TaskKindBind || task.State != TaskStateQueued || repository.task.ID != task.ID {
		t.Fatalf("ExecutePlan()=%#v error=%v repository=%#v", task, err, repository)
	}
	terminal, err := service.Run(context.Background(), task.ID)
	if err != nil || terminal.State != TaskStateSucceeded || repository.item.State != CertificateStateActive ||
		len(repository.bindings) != 1 || repository.bindings[0].CertificateID != repository.item.ID ||
		publisher.plan.ID != planned.Plan.ID {
		t.Fatalf("Run()=%#v error=%v repository/publisher=%#v/%#v", terminal, err, repository, publisher)
	}
}

type bindingRepositoryStub struct {
	item     Certificate
	bindings []Binding
	plan     BindingPlan
	task     Task
}

func (stub *bindingRepositoryStub) Certificate(context.Context, CertificateID) (Certificate, error) {
	return stub.item, nil
}

func (stub *bindingRepositoryStub) CertificateBindings(context.Context, CertificateID) ([]Binding, error) {
	return append([]Binding(nil), stub.bindings...), nil
}

func (stub *bindingRepositoryStub) CertificateBindingByFingerprint(context.Context, string) (Binding, error) {
	return Binding{}, fs.ErrNotExist
}

func (stub *bindingRepositoryStub) CreateCertificateBindingPlan(_ context.Context, plan BindingPlan) error {
	stub.plan = plan
	return nil
}

func (stub *bindingRepositoryStub) CertificateBindingPlan(
	_ context.Context, _ BindingPlanID, now time.Time,
) (BindingPlan, error) {
	if stub.plan.State == PlanStatePlanned && !now.Before(stub.plan.ExpiresAt) {
		return BindingPlan{}, ErrPlanExpired
	}
	return stub.plan, nil
}

func (stub *bindingRepositoryStub) ExecuteCertificateBindingPlan(
	_ context.Context, _ BindingPlanID, _ time.Time, task Task, stage TaskStage,
) error {
	stub.plan.State = PlanStateExecuted
	stub.plan.ExecutedAt = task.CreatedAt
	stub.task = task
	stub.task.Stages = []TaskStage{stage}
	return nil
}

func (stub *bindingRepositoryStub) CertificateTask(context.Context, TaskID) (Task, error) {
	return stub.task, nil
}

func (stub *bindingRepositoryStub) TransitionCertificateTask(
	_ context.Context, _ TaskState, _ TaskStageName, next Task, stage TaskStage,
) error {
	stub.task = next
	stub.task.Stages = append(append([]TaskStage(nil), next.Stages...), stage)
	return nil
}

func (stub *bindingRepositoryStub) CompleteCertificateBindingTask(
	_ context.Context,
	_ TaskState,
	_ TaskStageName,
	next Task,
	stage TaskStage,
	item Certificate,
	bindings []Binding,
) error {
	stub.item = item
	stub.bindings = append([]Binding(nil), bindings...)
	stub.task = next
	stub.task.Stages = append(append([]TaskStage(nil), next.Stages...), stage)
	return nil
}

type bindingPublisherStub struct {
	plan BindingPlan
}

func (stub *bindingPublisherStub) Bind(_ context.Context, task Task, plan BindingPlan) (DeploymentResult, error) {
	stub.plan = plan
	var refs []ServerRef
	if err := json.Unmarshal([]byte(plan.ServerRefsJSON), &refs); err != nil {
		return DeploymentResult{}, err
	}
	bindings := make([]Binding, 0, len(refs))
	for index, ref := range refs {
		names, _ := json.Marshal(ref.ServerNames)
		listeners, _ := json.Marshal(ref.Listeners)
		bindingID := "55555555555555555555555555555555"
		if index > 0 {
			bindingID = "66666666666666666666666666666666"
		}
		bindings = append(bindings, Binding{
			ID:            BindingID(bindingID),
			CertificateID: plan.CertificateID, VersionID: plan.VersionID, ConfigPath: ref.Path,
			ServerStartOffset: int64(ref.StartOffset), ServerNamesJSON: string(names), ListenersJSON: string(listeners),
			ServerFingerprint: ref.Fingerprint, CreatedAt: task.UpdatedAt, UpdatedAt: task.UpdatedAt,
		})
	}
	return DeploymentResult{ReleaseID: "66666666666666666666666666666666", Bindings: bindings}, nil
}
