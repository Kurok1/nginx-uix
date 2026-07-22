/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/certificate"
	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	testCertificatePlanID = certificate.OrderPlanID("11111111111111111111111111111111")
	testBindingPlanID     = certificate.BindingPlanID("77777777777777777777777777777777")
	testCertificateTaskID = certificate.TaskID("22222222222222222222222222222222")
	testCertificateID     = certificate.CertificateID("33333333333333333333333333333333")
)

func TestCertificateCredentialTokenIsAcceptedOnceAndNeverReturned(t *testing.T) {
	api := &certificateCredentialAPIStub{credential: testDNSCredential()}
	recorder := serveCertificateRequest(t, http.MethodPost, "/api/v1/certificate-dns-credentials",
		`{"name":"production zones","api_token":"cf-secret-token"}`, Dependencies{CertificateCredentials: api}, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	if api.input.APIToken != "cf-secret-token" || strings.Contains(recorder.Body.String(), "cf-secret-token") ||
		strings.Contains(recorder.Body.String(), "api_token") {
		t.Fatalf("credential request/response leaked or lost token: input=%#v body=%s", api.input, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
}

func TestCertificateOrderPlanExecutionStartsRequestIndependentTask(t *testing.T) {
	plan := testOrderPlan()
	plans := &certificatePlanAPIStub{planned: certificate.PlannedOrder{
		Plan: plan,
		Binding: certificate.BindingChangePlan{Mode: "bind", Files: []certificate.BindingFileChange{{
			Path: "conf.d/site.conf", Patch: "@@ -1 +1 @@\n", AddedLines: 2, RemovedLines: 0,
		}}},
	}}
	queue := &certificateQueueAPIStub{task: testCertificateTask()}
	tasks := &certificateTaskControllerStub{}
	dependencies := Dependencies{CertificatePlans: plans, CertificateQueue: queue, CertificateTaskController: tasks}

	planRecorder := serveCertificateRequest(t, http.MethodPost, "/api/v1/certificate-order-plans",
		`{"identifiers":["www.example.com"],"challenge":"http_01","account_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","server_refs":[{"path":"conf.d/site.conf","start_offset":0,"server_names":["www.example.com"],"listeners":["443 ssl"],"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`,
		dependencies, "")
	if planRecorder.Code != http.StatusCreated || !strings.Contains(planRecorder.Body.String(), `"binding_diff"`) {
		t.Fatalf("plan status/body = %d/%s", planRecorder.Code, planRecorder.Body.String())
	}

	executeRecorder := serveCertificateRequest(t, http.MethodPost,
		"/api/v1/certificate-order-plans/"+string(testCertificatePlanID)+"/executions",
		`{"confirmation":"www.example.com","production_risk_confirmation":""}`, dependencies, "")
	if executeRecorder.Code != http.StatusAccepted || queue.planID != testCertificatePlanID ||
		queue.input.Confirmation != "www.example.com" || tasks.started.ID != testCertificateTaskID {
		t.Fatalf("execution status/input/start = %d/%#v/%#v; body=%s", executeRecorder.Code, queue.input, tasks.started, executeRecorder.Body.String())
	}
	if location := executeRecorder.Header().Get("Location"); location != "/api/v1/certificate-tasks/"+string(testCertificateTaskID) {
		t.Fatalf("Location = %q", location)
	}
}

func TestCertificateDetailExposesSafeMetadataAndTaskSSEReconnects(t *testing.T) {
	item := testCertificateRecord()
	inventory := &certificateInventoryAPIStub{
		item: item,
		versions: []certificate.Version{{
			ID: item.ActiveVersionID, CertificateID: item.ID, State: certificate.VersionStateActive,
			FullchainDigest: strings.Repeat("a", 64), PrivateKeyDigest: strings.Repeat("b", 64),
			LeafFingerprint: strings.Repeat("c", 64), SerialNumber: "42", Issuer: "Local ACME",
			NotBefore: item.NotBefore, NotAfter: item.NotAfter, CreatedAt: item.CreatedAt,
		}},
		bindings: []certificate.Binding{{
			ID: "44444444444444444444444444444444", CertificateID: item.ID, VersionID: item.ActiveVersionID,
			ConfigPath: "conf.d/site.conf", ServerStartOffset: 0, ServerNamesJSON: `["www.example.com"]`,
			ListenersJSON: `["443 ssl"]`, ServerFingerprint: strings.Repeat("d", 64),
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		}},
	}
	detail := serveCertificateRequest(t, http.MethodGet, "/api/v1/certificates/"+string(item.ID), "",
		Dependencies{Certificates: inventory}, "")
	if detail.Code != http.StatusOK || strings.Contains(detail.Body.String(), strings.Repeat("b", 64)) ||
		!strings.Contains(detail.Body.String(), `"primary_identifier":"www.example.com"`) {
		t.Fatalf("certificate detail status/body = %d/%s", detail.Code, detail.Body.String())
	}

	task := testCertificateTask()
	task.State = certificate.TaskStateSucceeded
	task.Stage = certificate.TaskStageCompleted
	task.FinishedAt = task.UpdatedAt
	stages := []certificate.TaskStage{{
		TaskID: task.ID, Sequence: 2, Stage: certificate.TaskStageCompleted,
		Result: certificate.StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: task.UpdatedAt,
	}}
	taskAPI := &certificateTaskAPIStub{task: task, stages: stages}
	events := serveCertificateRequest(t, http.MethodGet,
		"/api/v1/certificate-tasks/"+string(task.ID)+"/events", "",
		Dependencies{CertificateTasks: taskAPI}, "1")
	if events.Code != http.StatusOK || events.Header().Get("Content-Type") != "text/event-stream" ||
		strings.Contains(events.Body.String(), "event: snapshot") || !strings.Contains(events.Body.String(), "id: 2") ||
		!strings.Contains(events.Body.String(), "event: terminal") {
		t.Fatalf("SSE status/headers/body = %d/%v/%s", events.Code, events.Header(), events.Body.String())
	}
}

func TestCertificateErrorsUseStableSecretFreeCodes(t *testing.T) {
	api := &certificateCredentialAPIStub{err: certificate.ErrCloudflarePermission}
	recorder := serveCertificateRequest(t, http.MethodPost, "/api/v1/certificate-dns-credentials",
		`{"name":"zones","api_token":"cf-secret-token"}`, Dependencies{CertificateCredentials: api}, "")
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), `"code":"CLOUDFLARE_PERMISSION_DENIED"`) ||
		strings.Contains(recorder.Body.String(), "cf-secret-token") {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestCertificateDeactivatedAccountUsesDistinctStableCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://example.test/api/v1/certificate-order-plans", nil)
	request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, "request-deactivated"))

	writeCertificateAPIError(recorder, request, certificate.ErrACMEAccountDeactivated)

	if recorder.Code != http.StatusConflict ||
		!strings.Contains(recorder.Body.String(), `"code":"ACME_ACCOUNT_DEACTIVATED"`) {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestCertificateACMERateLimitReturnsBoundedRetryAfter(t *testing.T) {
	api := &certificateCredentialAPIStub{err: &certificate.ACMERateLimitError{RetryAfter: 30 * time.Minute}}
	recorder := serveCertificateRequest(t, http.MethodPost, "/api/v1/certificate-dns-credentials",
		`{"name":"zones","api_token":"cf-secret-token"}`, Dependencies{CertificateCredentials: api}, "")
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "1800" ||
		!strings.Contains(recorder.Body.String(), `"code":"ACME_RATE_LIMITED"`) ||
		strings.Contains(recorder.Body.String(), "cf-secret-token") {
		t.Fatalf("status/headers/body = %d/%v/%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestCertificateExportStreamsNoStorePEMAndLifecycleMutationsConfirmExactly(t *testing.T) {
	item := testCertificateRecord()
	item.State = certificate.CertificateStateUnbound
	lifecycle := &certificateLifecycleAPIStub{item: item, export: certificate.CertificateExport{
		Content:  []byte("-----BEGIN CERTIFICATE-----\npublic\n-----END CERTIFICATE-----\n"),
		Filename: "certificate-" + string(item.ID) + ".pem",
	}}
	dependencies := Dependencies{CertificateLifecycle: lifecycle}
	exported := serveCertificateRequest(t, http.MethodPost, "/api/v1/certificates/"+string(item.ID)+"/exports",
		`{"confirmation":"`+string(item.ID)+`","include_private_key":false,"private_key_confirmation":""}`,
		dependencies, "")
	if exported.Code != http.StatusOK || exported.Header().Get("Content-Type") != "application/x-pem-file" ||
		exported.Header().Get("Content-Encoding") != "" || !strings.Contains(exported.Header().Get("Content-Disposition"), string(item.ID)) ||
		strings.Contains(exported.Body.String(), "PRIVATE KEY") {
		t.Fatalf("export status/headers/body=%d/%v/%s", exported.Code, exported.Header(), exported.Body.String())
	}

	unbound := serveCertificateRequest(t, http.MethodPost, "/api/v1/certificates/"+string(item.ID)+"/unbindings",
		`{"confirmation":"www.example.com"}`, dependencies, "")
	if unbound.Code != http.StatusOK || lifecycle.unbindConfirmation != "www.example.com" {
		t.Fatalf("unbind status/confirmation=%d/%q body=%s", unbound.Code, lifecycle.unbindConfirmation, unbound.Body.String())
	}
	deleted := serveCertificateRequest(t, http.MethodDelete, "/api/v1/certificates/"+string(item.ID),
		`{"confirmation":"`+string(item.ID)+`"}`, dependencies, "")
	if deleted.Code != http.StatusNoContent || lifecycle.deleteConfirmation != string(item.ID) {
		t.Fatalf("delete status/confirmation=%d/%q body=%s", deleted.Code, lifecycle.deleteConfirmation, deleted.Body.String())
	}
}

func TestCertificateRenewalPolicyRequiresMutationBoundaryAndReturnsUpdatedMetadata(t *testing.T) {
	item := testCertificateRecord()
	lifecycle := &certificateLifecycleAPIStub{item: item}
	recorder := serveCertificateRequest(t, http.MethodPut,
		"/api/v1/certificates/"+string(item.ID)+"/renewal-policy",
		`{"confirmation":"www.example.com","auto_renew":false,"renew_before_seconds":1209600}`,
		Dependencies{CertificateLifecycle: lifecycle}, "")
	if recorder.Code != http.StatusOK || lifecycle.policy.Confirmation != item.PrimaryIdentifier ||
		lifecycle.policy.AutoRenew || lifecycle.policy.RenewBeforeSeconds != 1_209_600 ||
		!strings.Contains(recorder.Body.String(), `"auto_renew":false`) {
		t.Fatalf("renewal policy status/input/body=%d/%#v/%s", recorder.Code, lifecycle.policy, recorder.Body.String())
	}
}

func TestCertificateStandaloneBindingPlanPreviewsThenQueuesExactTask(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	bindings := &certificateBindingAPIStub{planned: certificate.PlannedBinding{
		Plan: certificate.BindingPlan{
			ID: testBindingPlanID, State: certificate.PlanStatePlanned, CertificateID: testCertificateID,
			VersionID: "55555555555555555555555555555555", ServerRefsJSON: `[]`, BindingDiffJSON: `[]`,
			ExpiresAt: now.Add(10 * time.Minute), CreatedBy: 7, RequestID: "request-bind", CreatedAt: now,
		},
		Binding: certificate.BindingChangePlan{Mode: "bind", Files: []certificate.BindingFileChange{{
			Path: "conf.d/site.conf", Patch: "@@ -1 +1 @@\n", AddedLines: 2,
		}}},
	}, task: testCertificateTask()}
	bindings.task.Kind = certificate.TaskKindBind
	owner := &certificateTaskControllerStub{}
	dependencies := Dependencies{CertificateBindings: bindings, CertificateTaskController: owner}

	preview := serveCertificateRequest(t, http.MethodPost,
		"/api/v1/certificates/"+string(testCertificateID)+"/binding-plans",
		`{"server_refs":[{"path":"conf.d/site.conf","start_offset":0,"server_names":["www.example.com"],"listeners":["443 ssl"],"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`,
		dependencies, "")
	if preview.Code != http.StatusCreated || !strings.Contains(preview.Body.String(), `"binding_diff"`) {
		t.Fatalf("binding preview status/body=%d/%s", preview.Code, preview.Body.String())
	}
	executed := serveCertificateRequest(t, http.MethodPost,
		"/api/v1/certificate-binding-plans/"+string(testBindingPlanID)+"/executions",
		`{"confirmation":"www.example.com"}`, dependencies, "")
	if executed.Code != http.StatusAccepted || bindings.executed != testBindingPlanID ||
		bindings.confirmation != "www.example.com" || owner.started.Kind != certificate.TaskKindBind {
		t.Fatalf("binding execution status/stub/owner=%d/%#v/%#v body=%s", executed.Code, bindings, owner.started, executed.Body.String())
	}
}

func serveCertificateRequest(
	t *testing.T,
	method string,
	target string,
	body string,
	dependencies Dependencies,
	lastEventID string,
) *httptest.ResponseRecorder {
	t.Helper()
	issued := testIssuedSession()
	dependencies.Sessions = &authorizationSessionStub{issued: issued}
	dependencies.RequestIDSource = bytes.NewReader(bytes.Repeat([]byte{1}, 128))
	request := httptest.NewRequest(method, "http://example.test"+target, strings.NewReader(body))
	request.Host = "example.test"
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: issued.Token})
	if method != http.MethodGet {
		request.Header.Set("Origin", "http://example.test")
		request.Header.Set(csrfHeaderName, issued.CSRFToken)
		request.Header.Set("Content-Type", "application/json")
	}
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	recorder := httptest.NewRecorder()
	NewHandler(dependencies).ServeHTTP(recorder, request)
	return recorder
}

func testDNSCredential() certificate.DNSCredential {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	return certificate.DNSCredential{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "production zones",
		Provider: certificate.DNSProviderCloudflare, Fingerprint: "1234567890abcdef",
		Status: certificate.CredentialStatusValid, VerifiedAt: now, CreatedBy: 7,
		RequestID: "request-cf", CreatedAt: now, UpdatedAt: now,
	}
}

func testOrderPlan() certificate.OrderPlan {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	return certificate.OrderPlan{
		ID: testCertificatePlanID, State: certificate.PlanStatePlanned,
		Environment: certificate.EnvironmentStaging, Challenge: certificate.ChallengeHTTP01,
		AccountID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CertificateID: testCertificateID,
		VersionID: "55555555555555555555555555555555", PrimaryIdentifier: "www.example.com",
		IdentifiersJSON: `["www.example.com"]`, ServerRefsJSON: `[]`, BindingDiffJSON: `[]`,
		ExpiresAt: now.Add(10 * time.Minute), CreatedBy: 7, RequestID: "request-plan", CreatedAt: now,
	}
}

func testCertificateTask() certificate.Task {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	return certificate.Task{
		ID: testCertificateTaskID, Kind: certificate.TaskKindIssue, State: certificate.TaskStateQueued,
		Stage: certificate.TaskStageQueued, PlanID: testCertificatePlanID, CertificateID: testCertificateID,
		VersionID: "55555555555555555555555555555555", AccountID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Challenge: certificate.ChallengeHTTP01, CreatedBy: 7, RequestID: "request-task",
		CreatedAt: now, UpdatedAt: now,
	}
}

func testCertificateRecord() certificate.Certificate {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	return certificate.Certificate{
		ID: testCertificateID, PrimaryIdentifier: "www.example.com", IdentifiersJSON: `["www.example.com"]`,
		Challenge: certificate.ChallengeHTTP01, AccountID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		State: certificate.CertificateStateActive, ActiveVersionID: "55555555555555555555555555555555",
		AutoRenew: true, RenewBeforeSeconds: 2_592_000, NextRenewalAt: now.Add(30 * 24 * time.Hour),
		NotBefore: now, NotAfter: now.Add(90 * 24 * time.Hour), CreatedBy: 7,
		RequestID: "request-certificate", CreatedAt: now, UpdatedAt: now,
	}
}

type certificateCredentialAPIStub struct {
	credential certificate.DNSCredential
	input      certificate.CreateDNSCredentialInput
	err        error
}

func (stub *certificateCredentialAPIStub) Create(
	_ context.Context, _ config.Actor, input certificate.CreateDNSCredentialInput,
) (certificate.DNSCredential, error) {
	stub.input = input
	return stub.credential, stub.err
}

func (stub *certificateCredentialAPIStub) Credentials(context.Context) ([]certificate.DNSCredential, error) {
	return []certificate.DNSCredential{stub.credential}, stub.err
}

func (stub *certificateCredentialAPIStub) Delete(
	context.Context, config.Actor, certificate.DNSCredentialID,
) (certificate.DNSCredential, error) {
	return stub.credential, stub.err
}

type certificatePlanAPIStub struct {
	planned certificate.PlannedOrder
	input   certificate.CreateOrderPlanInput
}

func (stub *certificatePlanAPIStub) ServerCandidates(context.Context, config.Actor) ([]certificate.ServerCandidate, error) {
	return []certificate.ServerCandidate{}, nil
}

func (stub *certificatePlanAPIStub) Create(
	_ context.Context, _ config.Actor, input certificate.CreateOrderPlanInput,
) (certificate.PlannedOrder, error) {
	stub.input = input
	return stub.planned, nil
}

type certificateQueueAPIStub struct {
	task   certificate.Task
	planID certificate.OrderPlanID
	input  certificate.ExecuteOrderPlanInput
}

type certificateBindingAPIStub struct {
	planned      certificate.PlannedBinding
	task         certificate.Task
	executed     certificate.BindingPlanID
	confirmation string
}

func (stub *certificateBindingAPIStub) CreatePlan(
	_ context.Context,
	_ config.Actor,
	_ certificate.CertificateID,
	_ certificate.CreateBindingPlanInput,
) (certificate.PlannedBinding, error) {
	return stub.planned, nil
}

func (stub *certificateBindingAPIStub) ExecutePlan(
	_ context.Context,
	_ config.Actor,
	id certificate.BindingPlanID,
	input certificate.ExecuteBindingPlanInput,
) (certificate.Task, error) {
	stub.executed = id
	stub.confirmation = input.Confirmation
	return stub.task, nil
}

func (stub *certificateQueueAPIStub) Execute(
	_ context.Context, _ config.Actor, planID certificate.OrderPlanID, input certificate.ExecuteOrderPlanInput,
) (certificate.Task, error) {
	stub.planID = planID
	stub.input = input
	return stub.task, nil
}

type certificateTaskControllerStub struct {
	started certificate.Task
	cancel  certificate.TaskID
}

func (stub *certificateTaskControllerStub) Start(task certificate.Task) bool {
	stub.started = task
	return true
}

func (stub *certificateTaskControllerStub) Cancel(id certificate.TaskID) bool {
	stub.cancel = id
	return true
}

type certificateTaskAPIStub struct {
	task   certificate.Task
	stages []certificate.TaskStage
}

func (stub *certificateTaskAPIStub) Tasks(context.Context, int) ([]certificate.Task, error) {
	return []certificate.Task{stub.task}, nil
}

func (stub *certificateTaskAPIStub) Task(context.Context, certificate.TaskID) (certificate.Task, error) {
	return stub.task, nil
}

func (stub *certificateTaskAPIStub) Stages(
	_ context.Context, _ certificate.TaskID, after uint64, _ int,
) ([]certificate.TaskStage, error) {
	result := make([]certificate.TaskStage, 0, len(stub.stages))
	for _, stage := range stub.stages {
		if stage.Sequence > after {
			result = append(result, stage)
		}
	}
	return result, nil
}

func (stub *certificateTaskAPIStub) Cancel(
	context.Context, config.Actor, certificate.TaskID,
) (certificate.Task, error) {
	return stub.task, nil
}

type certificateInventoryAPIStub struct {
	item     certificate.Certificate
	versions []certificate.Version
	bindings []certificate.Binding
}

type certificateLifecycleAPIStub struct {
	item               certificate.Certificate
	export             certificate.CertificateExport
	unbindConfirmation string
	deleteConfirmation string
	policy             certificate.RenewalPolicyInput
}

func (stub *certificateLifecycleAPIStub) Export(
	context.Context, config.Actor, certificate.CertificateID, certificate.ExportCertificateInput,
) (certificate.CertificateExport, error) {
	return stub.export, nil
}

func (stub *certificateLifecycleAPIStub) Unbind(
	_ context.Context, _ config.Actor, _ certificate.CertificateID, confirmation string,
) (certificate.Certificate, error) {
	stub.unbindConfirmation = confirmation
	return stub.item, nil
}

func (stub *certificateLifecycleAPIStub) Delete(
	_ context.Context, _ config.Actor, _ certificate.CertificateID, confirmation string,
) (certificate.Certificate, error) {
	stub.deleteConfirmation = confirmation
	stub.item.State = certificate.CertificateStateDeleted
	return stub.item, nil
}

func (stub *certificateLifecycleAPIStub) UpdateRenewalPolicy(
	_ context.Context,
	_ config.Actor,
	_ certificate.CertificateID,
	input certificate.RenewalPolicyInput,
) (certificate.Certificate, error) {
	stub.policy = input
	stub.item.AutoRenew = input.AutoRenew
	stub.item.RenewBeforeSeconds = input.RenewBeforeSeconds
	stub.item.NextRenewalAt = time.Time{}
	return stub.item, nil
}

func (stub *certificateInventoryAPIStub) Certificates(context.Context, int) ([]certificate.Certificate, error) {
	return []certificate.Certificate{stub.item}, nil
}

func (stub *certificateInventoryAPIStub) Certificate(
	context.Context, certificate.CertificateID,
) (certificate.Certificate, error) {
	return stub.item, nil
}

func (stub *certificateInventoryAPIStub) CertificateVersions(
	context.Context, certificate.CertificateID,
) ([]certificate.Version, error) {
	return stub.versions, nil
}

func (stub *certificateInventoryAPIStub) CertificateBindings(
	context.Context, certificate.CertificateID,
) ([]certificate.Binding, error) {
	return stub.bindings, nil
}
