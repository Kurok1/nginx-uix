/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestRenewalServiceBuildsNewVersionPlanFromPersistedBindings(t *testing.T) {
	now := time.Date(2026, 7, 21, 20, 0, 0, 0, time.UTC)
	certificateID := CertificateID("11111111111111111111111111111111")
	activeVersionID := VersionID("22222222222222222222222222222222")
	source := "events {}\nhttp {\n  server {\n    listen 443 ssl;\n    server_name example.com;\n    ssl_certificate " +
		CertificateFullchainPath(certificateID, activeVersionID) + ";\n    ssl_certificate_key " +
		CertificatePrivateKeyPath(certificateID, activeVersionID) + ";\n  }\n}\n"
	snapshot := certificatePlanSnapshot(t, source)
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ref := oneEditableServerRef(t, project)
	names, _ := json.Marshal(ref.ServerNames)
	listeners, _ := json.Marshal(ref.Listeners)
	repository := &renewalRepositoryStub{
		item: Certificate{
			ID: certificateID, PrimaryIdentifier: "example.com", IdentifiersJSON: `["example.com"]`,
			Challenge: ChallengeHTTP01, AccountID: "33333333333333333333333333333333",
			State: CertificateStateActive, ActiveVersionID: activeVersionID, AutoRenew: true,
			RenewBeforeSeconds: int64(defaultRenewBefore / time.Second), NextRenewalAt: now,
			NotBefore: now.Add(-60 * 24 * time.Hour), NotAfter: now.Add(30 * 24 * time.Hour),
			CreatedBy: 7, RequestID: "request-original", CreatedAt: now.Add(-60 * 24 * time.Hour), UpdatedAt: now,
		},
		account: Account{ID: "33333333333333333333333333333333", Environment: EnvironmentProduction, Status: AccountStatusValid},
		bindings: []Binding{{
			ID: "44444444444444444444444444444444", CertificateID: certificateID, VersionID: activeVersionID,
			ConfigPath: ref.Path, ServerStartOffset: int64(ref.StartOffset), ServerNamesJSON: string(names),
			ListenersJSON: string(listeners), ServerFingerprint: ref.Fingerprint, CreatedAt: now, UpdatedAt: now,
		}},
	}
	planner := &PlanService{
		workspaces: &planWorkspaceStub{snapshot: snapshot}, random: bytes.NewReader(bytes.Repeat([]byte{0xaa}, 64)),
		now: func() time.Time { return now },
	}
	service, err := NewRenewalService(RenewalServiceOptions{
		Repository: repository, Planner: planner,
		Random: bytes.NewReader(bytes.Repeat([]byte{0xbb}, 64)), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Queue(context.Background(), config.Actor{UserID: 7, RequestID: "request-renew"}, certificateID,
		ManualRenewalInput{Confirmation: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Kind != TaskKindRenew || task.State != TaskStateQueued || task.CertificateID != certificateID ||
		task.VersionID == activeVersionID || repository.plan.State != PlanStateExecuted ||
		repository.plan.CertificateID != certificateID || repository.plan.VersionID != task.VersionID ||
		!strings.Contains(repository.plan.BindingDiffJSON, CertificateFullchainPath(certificateID, task.VersionID)) {
		t.Fatalf("task=%#v plan=%#v", task, repository.plan)
	}
}

type renewalRepositoryStub struct {
	item       Certificate
	account    Account
	credential DNSCredential
	bindings   []Binding
	plan       OrderPlan
	task       Task
}

func (repository *renewalRepositoryStub) Certificate(context.Context, CertificateID) (Certificate, error) {
	return repository.item, nil
}

func (repository *renewalRepositoryStub) CertificateBindings(context.Context, CertificateID) ([]Binding, error) {
	return append([]Binding{}, repository.bindings...), nil
}

func (repository *renewalRepositoryStub) CertificateAccount(context.Context, AccountID) (Account, error) {
	return repository.account, nil
}

func (repository *renewalRepositoryStub) CertificateDNSCredential(context.Context, DNSCredentialID) (DNSCredential, error) {
	return repository.credential, nil
}

func (repository *renewalRepositoryStub) CreateCertificateRenewal(
	_ context.Context, plan OrderPlan, task Task, _ TaskStage,
) error {
	repository.plan = plan
	repository.task = task
	return nil
}
