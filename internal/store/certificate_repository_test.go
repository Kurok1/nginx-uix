/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
	"github.com/kuroky/nginx-uix/internal/certificate"
	"github.com/kuroky/nginx-uix/internal/config"
)

func TestCertificateRepositoryPersistsAccountCredentialAndExpiringPlan(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(12).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateCertificateAccount() error = %v", err)
	}
	credential := testCertificateCredential(now)
	if err := database.CreateCertificateDNSCredential(context.Background(), credential); err != nil {
		t.Fatalf("CreateCertificateDNSCredential() error = %v", err)
	}
	usedAt := now.Add(time.Minute)
	if err := database.MarkCertificateDNSCredentialUsed(context.Background(), credential.ID, usedAt); err != nil {
		t.Fatalf("MarkCertificateDNSCredentialUsed() error = %v", err)
	}
	plan := testCertificatePlan(now, account.ID, credential.ID)
	if err := database.CreateCertificateOrderPlan(context.Background(), plan); err != nil {
		t.Fatalf("CreateCertificateOrderPlan() error = %v", err)
	}

	gotAccount, err := database.CertificateAccount(context.Background(), account.ID)
	if err != nil || gotAccount != account {
		t.Fatalf("CertificateAccount() = %+v, %v, want %+v", gotAccount, err, account)
	}
	gotCredential, err := database.CertificateDNSCredential(context.Background(), credential.ID)
	if err != nil || gotCredential.ID != credential.ID || !gotCredential.LastUsedAt.Equal(usedAt) {
		t.Fatalf("CertificateDNSCredential() = %+v, %v, want last_used_at %v", gotCredential, err, usedAt)
	}
	gotPlan, err := database.CertificateOrderPlan(context.Background(), plan.ID, now.Add(9*time.Minute))
	if err != nil || gotPlan != plan {
		t.Fatalf("CertificateOrderPlan() = %+v, %v, want %+v", gotPlan, err, plan)
	}
	if _, err := database.CertificateOrderPlan(context.Background(), plan.ID, plan.ExpiresAt); !errors.Is(err, certificate.ErrPlanExpired) {
		t.Fatalf("expired plan error = %v, want ErrPlanExpired", err)
	}
}

func TestCertificateRepositoryPersistsRenewalInventoryAcrossReopen(t *testing.T) {
	path := filepath.Join(secureTempDir(t), "certificate-reopen.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if database != nil {
			_ = database.Close()
		}
	})
	if _, err := database.CreateInitialUser(context.Background(), auth.NewUser{
		Username: "operator", NormalizedName: "operator", PasswordHash: "hash", CreatedAt: testTime(0),
	}); err != nil {
		t.Fatal(err)
	}
	now := testTime(16).UTC()
	account := testCertificateAccount(now)
	account.Environment = certificate.EnvironmentProduction
	account.DirectoryURL = certificate.LetsEncryptProductionDirectory
	account.URI = "https://acme-v02.api.letsencrypt.org/acme/acct/16"
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	item := testCertificate(now, account.ID, 6)
	item.NextRenewalAt = now.Add(-time.Minute)
	version := testCertificateVersion(now, item.ID)
	binding := testCertificateBinding(now, item.ID, version.ID, "77777777777777777777777777777777")
	if err := database.CreateIssuedCertificate(context.Background(), item, version, []certificate.Binding{binding}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = nil

	database, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	storedAccount, accountErr := database.CertificateAccount(context.Background(), account.ID)
	storedCertificate, certificateErr := database.Certificate(context.Background(), item.ID)
	versions, versionsErr := database.CertificateVersions(context.Background(), item.ID)
	bindings, bindingsErr := database.CertificateBindings(context.Background(), item.ID)
	due, dueErr := database.DueCertificates(context.Background(), now, 10)
	if accountErr != nil || certificateErr != nil || versionsErr != nil || bindingsErr != nil || dueErr != nil ||
		storedAccount.Status != certificate.AccountStatusValid || storedCertificate.ActiveVersionID != version.ID ||
		!storedCertificate.NextRenewalAt.Equal(item.NextRenewalAt) || len(versions) != 1 || versions[0].ID != version.ID ||
		len(bindings) != 1 || bindings[0].ID != binding.ID || len(due) != 1 || due[0].ID != item.ID {
		t.Fatalf(
			"reopened account/certificate/versions/bindings/due/errors=%#v/%#v/%#v/%#v/%#v/%v/%v/%v/%v/%v",
			storedAccount, storedCertificate, versions, bindings, due,
			accountErr, certificateErr, versionsErr, bindingsErr, dueErr,
		)
	}
}

func TestCertificateTaskTransitionsAreAtomicAndSequenceBound(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(20).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	plan := testCertificatePlan(now, account.ID, "")
	plan.Challenge = certificate.ChallengeHTTP01
	plan.DNSCredentialID = ""
	if err := database.CreateCertificateOrderPlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	task := testCertificateTask(now, plan.ID, account.ID)
	first := certificate.TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: certificate.TaskStageQueued,
		Result: certificate.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now,
	}
	if err := database.CreateCertificateTask(context.Background(), task, first); err != nil {
		t.Fatalf("CreateCertificateTask() error = %v", err)
	}

	next := task
	next.State = certificate.TaskStateRunning
	next.Stage = certificate.TaskStagePreparing
	next.StartedAt = now.Add(time.Second)
	next.UpdatedAt = next.StartedAt
	second := certificate.TaskStage{
		TaskID: task.ID, Sequence: 2, Stage: next.Stage,
		Result: certificate.StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: next.UpdatedAt,
	}
	if err := database.TransitionCertificateTask(
		context.Background(), task.State, task.Stage, next, second,
	); err != nil {
		t.Fatalf("TransitionCertificateTask() error = %v", err)
	}
	if err := database.TransitionCertificateTask(
		context.Background(), task.State, task.Stage, next, second,
	); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("stale transition error = %v, want conflict", err)
	}

	got, err := database.CertificateTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != certificate.TaskStateRunning || got.Stage != certificate.TaskStagePreparing || len(got.Stages) != 2 {
		t.Fatalf("task = %+v, want running/preparing with two stages", got)
	}
	cancelled, err := database.RequestCertificateTaskCancellation(
		context.Background(), task.ID, 1, "request-cancel", now.Add(2*time.Second),
	)
	if err != nil || cancelled.State != certificate.TaskStateRunning ||
		!cancelled.CancelRequestedAt.Equal(now.Add(2*time.Second)) || len(cancelled.Stages) != 2 {
		t.Fatalf("RequestCertificateTaskCancellation()=%#v error=%v", cancelled, err)
	}
	again, err := database.RequestCertificateTaskCancellation(
		context.Background(), task.ID, 1, "request-cancel-again", now.Add(3*time.Second),
	)
	if err != nil || !again.CancelRequestedAt.Equal(cancelled.CancelRequestedAt) {
		t.Fatalf("idempotent cancellation=%#v error=%v", again, err)
	}
	if _, err := database.CertificateTask(context.Background(), certificate.TaskID("ffffffffffffffffffffffffffffffff")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing task error = %v, want fs.ErrNotExist", err)
	}
}

func TestCertificateRepositoryQueuesIssuanceBeforeCertificateMetadataExists(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(25).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	plan := testCertificatePlan(now, account.ID, "")
	plan.Challenge = certificate.ChallengeHTTP01
	plan.DNSCredentialID = ""
	plan.CertificateID = "77777777777777777777777777777777"
	plan.VersionID = "88888888888888888888888888888888"
	if err := database.CreateCertificateOrderPlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	task := testCertificateTask(now, plan.ID, account.ID)
	task.CertificateID = plan.CertificateID
	task.VersionID = plan.VersionID
	stage := certificate.TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: task.Stage,
		Result: certificate.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now,
	}
	if err := database.ExecuteCertificateOrderPlan(context.Background(), plan.ID, now, task, stage); err != nil {
		t.Fatalf("ExecuteCertificateOrderPlan() error = %v", err)
	}
	got, err := database.CertificateTask(context.Background(), task.ID)
	if err != nil || got.CertificateID != plan.CertificateID || got.VersionID != plan.VersionID {
		t.Fatalf("CertificateTask()=%#v error=%v", got, err)
	}
}

func TestCertificateRepositoryStagingEvidenceExpiresAfterTwentyFourHours(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	now := testTime(26).UTC()
	completedAt := now.Add(-24*time.Hour - time.Second)
	account := testCertificateAccount(completedAt.Add(-time.Hour))
	if err := database.CreateCertificateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	plan := testCertificatePlan(completedAt.Add(-2*time.Minute), account.ID, "")
	plan.Challenge = certificate.ChallengeHTTP01
	plan.DNSCredentialID = ""
	if err := database.CreateCertificateOrderPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	task := testCertificateTask(completedAt.Add(-time.Minute), plan.ID, account.ID)
	first := certificate.TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: task.Stage,
		Result: certificate.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: task.CreatedAt,
	}
	if err := database.CreateCertificateTask(ctx, task, first); err != nil {
		t.Fatal(err)
	}
	terminal := task
	terminal.State = certificate.TaskStateSucceeded
	terminal.Stage = certificate.TaskStageCompleted
	terminal.StartedAt = task.CreatedAt.Add(time.Second)
	terminal.UpdatedAt = completedAt
	terminal.FinishedAt = completedAt
	second := certificate.TaskStage{
		TaskID: task.ID, Sequence: 2, Stage: terminal.Stage,
		Result: certificate.StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: completedAt,
	}
	if err := database.TransitionCertificateTask(ctx, task.State, task.Stage, terminal, second); err != nil {
		t.Fatal(err)
	}

	identifiers := plan.IdentifiersJSON
	evidence, err := database.HasCertificateStagingEvidence(ctx, identifiers, plan.Challenge, now)
	if err != nil || evidence {
		t.Fatalf("expired staging evidence = %v, %v, want false", evidence, err)
	}
	freshAt := now.Add(-23*time.Hour - 59*time.Minute)
	if _, err := database.sql.ExecContext(ctx, `UPDATE certificate_tasks
		SET updated_at = ?, finished_at = ? WHERE id = ?`, formatTime(freshAt), formatTime(freshAt), task.ID); err != nil {
		t.Fatal(err)
	}
	evidence, err = database.HasCertificateStagingEvidence(ctx, identifiers, plan.Challenge, now)
	if err != nil || !evidence {
		t.Fatalf("fresh staging evidence = %v, %v, want true", evidence, err)
	}
}

func TestCertificateRepositoryReservesThenCompletesAccountDeactivation(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(27).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	reserved, err := database.BeginCertificateAccountDeactivation(
		context.Background(), account.ID, 1, "request-deactivate-begin", now.Add(time.Minute),
	)
	if err != nil || reserved.Status != certificate.AccountStatusDeactivating {
		t.Fatalf("BeginCertificateAccountDeactivation()=%#v error=%v", reserved, err)
	}
	completed, err := database.CompleteCertificateAccountDeactivation(
		context.Background(), account.ID, 1, "request-deactivate-complete", now.Add(2*time.Minute),
	)
	if err != nil || completed.Status != certificate.AccountStatusDeactivated {
		t.Fatalf("CompleteCertificateAccountDeactivation()=%#v error=%v", completed, err)
	}
	var auditCount int
	if err := database.sql.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events
		WHERE object_id = ? AND action IN ('certificate.account.deactivate.begin','certificate.account.deactivate.complete')`,
		account.ID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("deactivation audit count=%d error=%v", auditCount, err)
	}
}

func TestCertificateRepositoryActivatesIssuedVersionAndOrdersRenewals(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(30).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	first := testCertificate(now, account.ID, 1)
	second := testCertificate(now, account.ID, 2)
	first.NextRenewalAt = now.Add(-time.Minute)
	second.NextRenewalAt = now.Add(-2 * time.Minute)
	for _, item := range []certificate.Certificate{first, second} {
		version := testCertificateVersion(now, item.ID)
		if err := database.CreateIssuedCertificate(context.Background(), item, version, nil); err != nil {
			t.Fatalf("CreateIssuedCertificate() error = %v", err)
		}
	}

	due, err := database.DueCertificates(context.Background(), now, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []certificate.CertificateID{second.ID, first.ID}
	got := make([]certificate.CertificateID, 0, len(due))
	for _, item := range due {
		got = append(got, item.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("due IDs = %v, want %v", got, want)
	}
}

func TestCertificateRepositoryUnbindsAuditsExportAndSoftDeletes(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(35).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	item := testCertificate(now, account.ID, 1)
	version := testCertificateVersion(now, item.ID)
	binding := testCertificateBinding(now, item.ID, version.ID, "77777777777777777777777777777777")
	if err := database.CreateIssuedCertificate(context.Background(), item, version, []certificate.Binding{binding}); err != nil {
		t.Fatal(err)
	}
	actor := config.Actor{UserID: 1, RequestID: "request-lifecycle"}
	unbound, err := database.CompleteCertificateUnbinding(
		context.Background(), item.ID, actor, "88888888888888888888888888888888", now.Add(time.Minute),
	)
	if err != nil || unbound.State != certificate.CertificateStateUnbound {
		t.Fatalf("CompleteCertificateUnbinding()=%#v error=%v", unbound, err)
	}
	bindings, err := database.CertificateBindings(context.Background(), item.ID)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("CertificateBindings()=%#v error=%v", bindings, err)
	}
	if err := database.RecordCertificateExport(
		context.Background(), item.ID, actor, true, now.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("RecordCertificateExport() error=%v", err)
	}
	deleted, err := database.DeleteCertificate(context.Background(), item.ID, actor, now.Add(3*time.Minute))
	if err != nil || deleted.State != certificate.CertificateStateDeleted || deleted.ActiveVersionID != "" || deleted.AutoRenew {
		t.Fatalf("DeleteCertificate()=%#v error=%v", deleted, err)
	}
	versions, err := database.CertificateVersions(context.Background(), item.ID)
	if err != nil || len(versions) != 0 {
		t.Fatalf("CertificateVersions(after delete)=%#v error=%v", versions, err)
	}
	listed, err := database.Certificates(context.Background(), 10)
	if err != nil || len(listed) != 0 {
		t.Fatalf("Certificates(after delete)=%#v error=%v", listed, err)
	}
	var exportPrivate int
	if err := database.sql.QueryRowContext(context.Background(), `SELECT json_extract(details_json, '$.included_private_key')
		FROM audit_events WHERE action = 'certificate.export' AND object_id = ?`, item.ID).Scan(&exportPrivate); err != nil || exportPrivate != 1 {
		t.Fatalf("export audit private=%d error=%v", exportPrivate, err)
	}
}

func TestCertificateRepositoryUpdatesRenewalPolicyAtomically(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(38).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	item := testCertificate(now, account.ID, 1)
	version := testCertificateVersion(now, item.ID)
	if err := database.CreateIssuedCertificate(context.Background(), item, version, nil); err != nil {
		t.Fatal(err)
	}
	actor := config.Actor{UserID: 1, RequestID: "request-policy"}
	updated, err := database.UpdateCertificateRenewalPolicy(
		context.Background(), item.ID, actor, false, 14*24*60*60, time.Time{}, now.Add(time.Minute),
	)
	if err != nil || updated.AutoRenew || !updated.NextRenewalAt.IsZero() || updated.RenewBeforeSeconds != 14*24*60*60 {
		t.Fatalf("UpdateCertificateRenewalPolicy()=%#v error=%v", updated, err)
	}
	secondActor := config.Actor{UserID: 1, RequestID: "request-policy-again"}
	updated, err = database.UpdateCertificateRenewalPolicy(
		context.Background(), item.ID, secondActor, true, 21*24*60*60,
		now.Add(20*24*time.Hour), now.Add(2*time.Minute),
	)
	if err != nil || !updated.AutoRenew || updated.RenewBeforeSeconds != 21*24*60*60 {
		t.Fatalf("second UpdateCertificateRenewalPolicy()=%#v error=%v", updated, err)
	}
	var autoRenew int
	var auditCount int
	if err := database.sql.QueryRowContext(context.Background(), `SELECT auto_renew FROM certificates WHERE id = ?`, item.ID).
		Scan(&autoRenew); err != nil || autoRenew != 1 {
		t.Fatalf("persisted auto_renew=%d error=%v", autoRenew, err)
	}
	if err := database.sql.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events
		WHERE object_id = ? AND action = 'certificate.renewal_policy'`, item.ID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("renewal policy audit count=%d error=%v", auditCount, err)
	}
}

func TestCertificateRepositoryConsumesAndCompletesStandaloneBindingPlanAtomically(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(39).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	item := testCertificate(now, account.ID, 1)
	item.State = certificate.CertificateStateUnbound
	version := testCertificateVersion(now, item.ID)
	if err := database.CreateIssuedCertificate(context.Background(), item, version, nil); err != nil {
		t.Fatal(err)
	}
	plan := certificate.BindingPlan{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: certificate.PlanStatePlanned,
		CertificateID: item.ID, VersionID: version.ID, ServerRefsJSON: `[{"path":"nginx.conf"}]`,
		BindingDiffJSON: `[{"path":"nginx.conf"}]`, ExpiresAt: now.Add(10 * time.Minute),
		CreatedBy: 1, RequestID: "request-binding-plan", CreatedAt: now,
	}
	plan.ProductionDigest[0] = 0x44
	if err := database.CreateCertificateBindingPlan(context.Background(), plan); err != nil {
		t.Fatalf("CreateCertificateBindingPlan() error=%v", err)
	}
	task := testCertificateTask(now.Add(time.Minute), certificate.OrderPlanID(plan.ID), account.ID)
	task.Kind = certificate.TaskKindBind
	task.CertificateID = item.ID
	task.VersionID = version.ID
	first := certificate.TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: task.Stage, Result: certificate.StageResultPending,
		PublicDetailsJSON: `{}`, OccurredAt: task.CreatedAt,
	}
	if err := database.ExecuteCertificateBindingPlan(context.Background(), plan.ID, task.CreatedAt, task, first); err != nil {
		t.Fatalf("ExecuteCertificateBindingPlan() error=%v", err)
	}
	storedPlan, err := database.CertificateBindingPlan(context.Background(), plan.ID, task.CreatedAt)
	if err != nil || storedPlan.State != certificate.PlanStateExecuted {
		t.Fatalf("CertificateBindingPlan()=%#v error=%v", storedPlan, err)
	}
	current := task
	current.Stages = []certificate.TaskStage{first}
	for sequence, stageName := range []certificate.TaskStageName{
		certificate.TaskStagePreparing, certificate.TaskStageDeploying,
	} {
		next := current
		next.State = certificate.TaskStateRunning
		next.Stage = stageName
		next.StartedAt = task.CreatedAt.Add(time.Second)
		next.UpdatedAt = task.CreatedAt.Add(time.Duration(sequence+1) * time.Second)
		stage := certificate.TaskStage{
			TaskID: task.ID, Sequence: uint64(sequence + 2), Stage: stageName,
			Result: certificate.StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: next.UpdatedAt,
		}
		if err := database.TransitionCertificateTask(
			context.Background(), current.State, current.Stage, next, stage,
		); err != nil {
			t.Fatal(err)
		}
		next.Stages = append(append([]certificate.TaskStage(nil), current.Stages...), stage)
		current = next
	}
	binding := testCertificateBinding(now.Add(4*time.Minute), item.ID, version.ID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	updatedItem := item
	updatedItem.State = certificate.CertificateStateActive
	updatedItem.UpdatedAt = now.Add(4 * time.Minute)
	terminal := current
	terminal.State = certificate.TaskStateSucceeded
	terminal.Stage = certificate.TaskStageCompleted
	terminal.ReleaseID = "cccccccccccccccccccccccccccccccc"
	terminal.UpdatedAt = updatedItem.UpdatedAt
	terminal.FinishedAt = updatedItem.UpdatedAt
	completed := certificate.TaskStage{
		TaskID: task.ID, Sequence: 4, Stage: terminal.Stage, Result: certificate.StageResultSuccess,
		PublicDetailsJSON: `{}`, OccurredAt: terminal.UpdatedAt,
	}
	if err := database.CompleteCertificateBindingTask(
		context.Background(), current.State, current.Stage, terminal, completed, updatedItem,
		[]certificate.Binding{binding},
	); err != nil {
		t.Fatalf("CompleteCertificateBindingTask() error=%v", err)
	}
	got, err := database.Certificate(context.Background(), item.ID)
	bindings, bindingErr := database.CertificateBindings(context.Background(), item.ID)
	finished, taskErr := database.CertificateTask(context.Background(), task.ID)
	if err != nil || bindingErr != nil || taskErr != nil || got.State != certificate.CertificateStateActive ||
		len(bindings) != 1 || finished.State != certificate.TaskStateSucceeded || finished.ReleaseID == "" {
		t.Fatalf("certificate/bindings/task/errors=%#v/%#v/%#v/%v/%v/%v", got, bindings, finished, err, bindingErr, taskErr)
	}
}

func TestCertificateRepositoryMarksInvalidActiveMaterialNeedsAttention(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(39).UTC()
	account := testCertificateAccount(now)
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	item := testCertificate(now, account.ID, 3)
	version := testCertificateVersion(now, item.ID)
	if err := database.CreateIssuedCertificate(context.Background(), item, version, nil); err != nil {
		t.Fatal(err)
	}
	inventory, err := database.CertificateMaterialInventory(context.Background(), 10)
	if err != nil || len(inventory) != 1 || inventory[0].Version.ID != version.ID {
		t.Fatalf("CertificateMaterialInventory()=%#v error=%v", inventory, err)
	}
	if err := database.MarkCertificateMaterialNeedsAttention(
		context.Background(), item.ID, version.ID, "certificate_material_invalid", now.Add(time.Minute),
	); err != nil {
		t.Fatalf("MarkCertificateMaterialNeedsAttention() error=%v", err)
	}
	updated, err := database.Certificate(context.Background(), item.ID)
	versions, versionErr := database.CertificateVersions(context.Background(), item.ID)
	if err != nil || versionErr != nil || updated.State != certificate.CertificateStateNeedsAttention ||
		updated.LastErrorCode != "certificate_material_invalid" || len(versions) != 1 ||
		versions[0].State != certificate.VersionStateNeedsAttention {
		t.Fatalf("certificate/versions/errors=%#v/%#v/%v/%v", updated, versions, err, versionErr)
	}
}

func TestCertificateRepositoryCreatesAndCompletesRenewalAtomically(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(40).UTC()
	account := testCertificateAccount(now)
	account.Environment = certificate.EnvironmentProduction
	account.DirectoryURL = certificate.LetsEncryptProductionDirectory
	account.URI = "https://acme-v02.api.letsencrypt.org/acme/acct/1"
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	item := testCertificate(now, account.ID, 1)
	oldVersion := testCertificateVersion(now, item.ID)
	oldBinding := testCertificateBinding(now, item.ID, oldVersion.ID, "77777777777777777777777777777777")
	if err := database.CreateIssuedCertificate(context.Background(), item, oldVersion, []certificate.Binding{oldBinding}); err != nil {
		t.Fatal(err)
	}
	plan := testCertificatePlan(now, account.ID, "")
	plan.State = certificate.PlanStateExecuted
	plan.Environment = certificate.EnvironmentProduction
	plan.Challenge = certificate.ChallengeHTTP01
	plan.DNSCredentialID = ""
	plan.CertificateID = item.ID
	plan.VersionID = "88888888888888888888888888888888"
	plan.ExecutedAt = now
	task := testCertificateTask(now, plan.ID, account.ID)
	task.ID = "99999999999999999999999999999999"
	task.Kind = certificate.TaskKindRenew
	task.CertificateID = item.ID
	task.VersionID = plan.VersionID
	first := certificate.TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: task.Stage,
		Result: certificate.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now,
	}
	if err := database.CreateCertificateRenewal(context.Background(), plan, task, first); err != nil {
		t.Fatalf("CreateCertificateRenewal() error = %v", err)
	}
	running := task
	running.State = certificate.TaskStateRunning
	running.Stage = certificate.TaskStageDeploying
	running.StartedAt = now.Add(time.Second)
	running.UpdatedAt = running.StartedAt
	second := certificate.TaskStage{
		TaskID: task.ID, Sequence: 2, Stage: running.Stage,
		Result: certificate.StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: running.UpdatedAt,
	}
	if err := database.TransitionCertificateTask(context.Background(), task.State, task.Stage, running, second); err != nil {
		t.Fatal(err)
	}
	newVersion := oldVersion
	newVersion.ID = plan.VersionID
	newVersion.State = certificate.VersionStateActive
	newVersion.SerialNumber = "02"
	newVersion.CreatedAt = now.Add(2 * time.Second)
	newBinding := testCertificateBinding(now.Add(2*time.Second), item.ID, newVersion.ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	updated := item
	updated.ActiveVersionID = newVersion.ID
	updated.NotBefore = newVersion.NotBefore
	updated.NotAfter = newVersion.NotAfter
	updated.NextRenewalAt = now.Add(20 * 24 * time.Hour)
	updated.RetryCount = 0
	updated.RetryAt = time.Time{}
	updated.LastErrorCode = ""
	updated.UpdatedAt = now.Add(2 * time.Second)
	completed := running
	completed.State = certificate.TaskStateSucceeded
	completed.Stage = certificate.TaskStageCompleted
	completed.UpdatedAt = updated.UpdatedAt
	completed.FinishedAt = updated.UpdatedAt
	third := certificate.TaskStage{
		TaskID: task.ID, Sequence: 3, Stage: completed.Stage,
		Result: certificate.StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: completed.UpdatedAt,
	}
	if err := database.CompleteRenewedCertificateTask(
		context.Background(), running.State, running.Stage, completed, third, updated, newVersion,
		[]certificate.Binding{newBinding}, oldVersion.ID,
	); err != nil {
		t.Fatalf("CompleteRenewedCertificateTask() error = %v", err)
	}
	got, err := database.Certificate(context.Background(), item.ID)
	if err != nil || got.ActiveVersionID != newVersion.ID || got.RetryCount != 0 {
		t.Fatalf("Certificate()=%#v error=%v", got, err)
	}
	bindings, err := database.CertificateBindings(context.Background(), item.ID)
	if err != nil || len(bindings) != 1 || bindings[0].ID != newBinding.ID || bindings[0].VersionID != newVersion.ID {
		t.Fatalf("CertificateBindings()=%#v error=%v", bindings, err)
	}
	versions, err := database.CertificateVersions(context.Background(), item.ID)
	if err != nil || len(versions) != 2 || versions[0].ID != newVersion.ID ||
		versions[1].ID != oldVersion.ID || versions[1].State != certificate.VersionStateSuperseded {
		t.Fatalf("CertificateVersions()=%#v error=%v", versions, err)
	}
}

func TestCertificateRepositoryFailsRenewalAndPersistsRetryAtomically(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := testTime(50).UTC()
	account := testCertificateAccount(now)
	account.Environment = certificate.EnvironmentProduction
	account.DirectoryURL = certificate.LetsEncryptProductionDirectory
	account.URI = "https://acme-v02.api.letsencrypt.org/acme/acct/1"
	if err := database.CreateCertificateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	item := testCertificate(now, account.ID, 1)
	version := testCertificateVersion(now, item.ID)
	if err := database.CreateIssuedCertificate(context.Background(), item, version, nil); err != nil {
		t.Fatal(err)
	}
	plan := testCertificatePlan(now, account.ID, "")
	plan.State = certificate.PlanStateExecuted
	plan.Environment = certificate.EnvironmentProduction
	plan.Challenge = certificate.ChallengeHTTP01
	plan.DNSCredentialID = ""
	plan.CertificateID = item.ID
	plan.VersionID = "88888888888888888888888888888888"
	plan.ExecutedAt = now
	task := testCertificateTask(now, plan.ID, account.ID)
	task.ID = "99999999999999999999999999999999"
	task.Kind = certificate.TaskKindRenew
	task.CertificateID = item.ID
	task.VersionID = plan.VersionID
	first := certificate.TaskStage{
		TaskID: task.ID, Sequence: 1, Stage: task.Stage,
		Result: certificate.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now,
	}
	if err := database.CreateCertificateRenewal(context.Background(), plan, task, first); err != nil {
		t.Fatal(err)
	}
	running := task
	running.State = certificate.TaskStateRunning
	running.Stage = certificate.TaskStageOrdering
	running.StartedAt = now.Add(time.Second)
	running.UpdatedAt = running.StartedAt
	second := certificate.TaskStage{
		TaskID: task.ID, Sequence: 2, Stage: running.Stage,
		Result: certificate.StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: running.UpdatedAt,
	}
	if err := database.TransitionCertificateTask(context.Background(), task.State, task.Stage, running, second); err != nil {
		t.Fatal(err)
	}
	failed := running
	failed.State = certificate.TaskStateFailed
	failed.Stage = certificate.TaskStageFailed
	failed.LastErrorCode = "acme_order_failed"
	failed.UpdatedAt = now.Add(2 * time.Second)
	failed.FinishedAt = failed.UpdatedAt
	third := certificate.TaskStage{
		TaskID: task.ID, Sequence: 3, Stage: failed.Stage, Result: certificate.StageResultFailed,
		Code: failed.LastErrorCode, PublicDetailsJSON: `{}`, OccurredAt: failed.UpdatedAt,
	}
	failedItem := item
	failedItem.State = certificate.CertificateStateExpiring
	failedItem.RetryCount = 1
	failedItem.RetryAt = now.Add(time.Hour)
	failedItem.LastErrorCode = failed.LastErrorCode
	failedItem.UpdatedAt = failed.UpdatedAt
	if err := database.FailCertificateRenewalTask(
		context.Background(), running.State, running.Stage, failed, third, failedItem,
	); err != nil {
		t.Fatalf("FailCertificateRenewalTask() error=%v", err)
	}
	gotTask, err := database.CertificateTask(context.Background(), task.ID)
	if err != nil || gotTask.State != certificate.TaskStateFailed || len(gotTask.Stages) != 3 {
		t.Fatalf("task=%#v error=%v", gotTask, err)
	}
	gotItem, err := database.Certificate(context.Background(), item.ID)
	if err != nil || gotItem.RetryCount != 1 || !gotItem.RetryAt.Equal(failedItem.RetryAt) ||
		gotItem.State != certificate.CertificateStateExpiring {
		t.Fatalf("certificate=%#v error=%v", gotItem, err)
	}
}

func testCertificateAccount(at time.Time) certificate.Account {
	return certificate.Account{
		ID: "11111111111111111111111111111111", Environment: certificate.EnvironmentStaging,
		DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
		URI:          "https://acme-staging-v02.api.letsencrypt.org/acme/acct/1", Email: "operator@example.com",
		Status: certificate.AccountStatusValid, TermsURL: "https://example.invalid/terms",
		TermsAgreedAt: at, TermsAgreedBy: 1, CreatedBy: 1, RequestID: "request-account",
		CreatedAt: at, UpdatedAt: at,
	}
}

func testCertificateCredential(at time.Time) certificate.DNSCredential {
	return certificate.DNSCredential{
		ID: "22222222222222222222222222222222", Name: "Production zones",
		Provider: certificate.DNSProviderCloudflare, Fingerprint: "0123456789abcdef",
		Status: certificate.CredentialStatusValid, VerifiedAt: at, CreatedBy: 1,
		RequestID: "request-credential", CreatedAt: at, UpdatedAt: at,
	}
}

func testCertificatePlan(at time.Time, accountID certificate.AccountID, credentialID certificate.DNSCredentialID) certificate.OrderPlan {
	var digest certificate.Digest
	digest[0] = 0x42
	return certificate.OrderPlan{
		ID: "33333333333333333333333333333333", State: certificate.PlanStatePlanned,
		Environment: certificate.EnvironmentStaging, Challenge: certificate.ChallengeCloudflareDNS01,
		AccountID: accountID, DNSCredentialID: credentialID, PrimaryIdentifier: "example.com",
		IdentifiersJSON: `["example.com","www.example.com"]`, ServerRefsJSON: `[]`,
		ProductionDigest: digest, BindingDiffJSON: `[]`, ExpiresAt: at.Add(10 * time.Minute),
		CreatedBy: 1, RequestID: "request-plan", CreatedAt: at,
	}
}

func testCertificateTask(at time.Time, planID certificate.OrderPlanID, accountID certificate.AccountID) certificate.Task {
	return certificate.Task{
		ID: "44444444444444444444444444444444", Kind: certificate.TaskKindIssue,
		State: certificate.TaskStateQueued, Stage: certificate.TaskStageQueued, PlanID: planID,
		AccountID: accountID, Challenge: certificate.ChallengeHTTP01, CreatedBy: 1,
		RequestID: "request-task", CreatedAt: at, UpdatedAt: at,
	}
}

func testCertificate(at time.Time, accountID certificate.AccountID, number byte) certificate.Certificate {
	id := certificate.CertificateID("5555555555555555555555555555555" + string('0'+number))
	versionID := certificate.VersionID("6666666666666666666666666666666" + string('0'+number))
	return certificate.Certificate{
		ID: id, PrimaryIdentifier: "example.com", IdentifiersJSON: `["example.com"]`,
		Challenge: certificate.ChallengeHTTP01, AccountID: accountID,
		State: certificate.CertificateStateActive, ActiveVersionID: versionID,
		AutoRenew: true, RenewBeforeSeconds: int64((30 * 24 * time.Hour) / time.Second),
		NotBefore: at.Add(-time.Hour), NotAfter: at.Add(60 * 24 * time.Hour),
		CreatedBy: 1, RequestID: "request-certificate", CreatedAt: at, UpdatedAt: at,
	}
}

func testCertificateVersion(at time.Time, certificateID certificate.CertificateID) certificate.Version {
	last := certificateID[len(certificateID)-1:]
	return certificate.Version{
		ID: certificate.VersionID("6666666666666666666666666666666" + last), CertificateID: certificateID,
		State: certificate.VersionStateActive, FullchainDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PrivateKeyDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		LeafFingerprint:  "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		SerialNumber:     "01", Issuer: "Test CA", NotBefore: at.Add(-time.Hour),
		NotAfter: at.Add(60 * 24 * time.Hour), CreatedAt: at,
	}
}

func testCertificateBinding(
	at time.Time,
	certificateID certificate.CertificateID,
	versionID certificate.VersionID,
	id certificate.BindingID,
) certificate.Binding {
	return certificate.Binding{
		ID: id, CertificateID: certificateID, VersionID: versionID, ConfigPath: "nginx.conf",
		ServerStartOffset: 20, ServerNamesJSON: `["example.com"]`, ListenersJSON: `["443 ssl"]`,
		ServerFingerprint: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		CreatedAt:         at, UpdatedAt: at,
	}
}
