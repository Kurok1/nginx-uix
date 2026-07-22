/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kuroky/nginx-uix/internal/certificate"
	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	certificateRequestBodyLimit = int64(128 << 10)
	certificateSecretBodyLimit  = int64(256 << 10)
	certificateListDefault      = 20
	certificateListMaximum      = 100
	certificateStageReadLimit   = 512
)

// CertificateAccountAPI is the safe ACME account lifecycle exposed at the HTTP boundary.
type CertificateAccountAPI interface {
	Directories(context.Context) ([]certificate.ACMEDirectory, error)
	Accounts(context.Context) ([]certificate.Account, error)
	Create(context.Context, config.Actor, certificate.CreateAccountInput) (certificate.Account, error)
	Import(context.Context, config.Actor, certificate.ImportAccountInput) (certificate.Account, error)
	Deactivate(context.Context, config.Actor, certificate.AccountID) (certificate.Account, error)
}

// CertificateCredentialAPI owns Cloudflare Token verification and secret-free metadata.
type CertificateCredentialAPI interface {
	Create(context.Context, config.Actor, certificate.CreateDNSCredentialInput) (certificate.DNSCredential, error)
	Credentials(context.Context) ([]certificate.DNSCredential, error)
	Delete(context.Context, config.Actor, certificate.DNSCredentialID) (certificate.DNSCredential, error)
}

// CertificatePlanAPI creates bounded, digest-bound issuance reviews.
type CertificatePlanAPI interface {
	ServerCandidates(context.Context, config.Actor) ([]certificate.ServerCandidate, error)
	Create(context.Context, config.Actor, certificate.CreateOrderPlanInput) (certificate.PlannedOrder, error)
}

// CertificatePlanReader retrieves a persisted execution contract.
type CertificatePlanReader interface {
	CertificateOrderPlan(context.Context, certificate.OrderPlanID, time.Time) (certificate.OrderPlan, error)
}

// CertificateQueueAPI consumes a reviewed issuance plan.
type CertificateQueueAPI interface {
	Execute(
		context.Context,
		config.Actor,
		certificate.OrderPlanID,
		certificate.ExecuteOrderPlanInput,
	) (certificate.Task, error)
}

// CertificateTaskAPI exposes durable task history, progress and cancellation intent.
type CertificateTaskAPI interface {
	Tasks(context.Context, int) ([]certificate.Task, error)
	Task(context.Context, certificate.TaskID) (certificate.Task, error)
	Stages(context.Context, certificate.TaskID, uint64, int) ([]certificate.TaskStage, error)
	Cancel(context.Context, config.Actor, certificate.TaskID) (certificate.Task, error)
}

// CertificateTaskController owns request-independent task contexts.
type CertificateTaskController interface {
	Start(certificate.Task) bool
	Cancel(certificate.TaskID) bool
}

// CertificateInventoryAPI returns only safe certificate metadata and exact bindings.
type CertificateInventoryAPI interface {
	Certificates(context.Context, int) ([]certificate.Certificate, error)
	Certificate(context.Context, certificate.CertificateID) (certificate.Certificate, error)
	CertificateVersions(context.Context, certificate.CertificateID) ([]certificate.Version, error)
	CertificateBindings(context.Context, certificate.CertificateID) ([]certificate.Binding, error)
}

// CertificateRenewalAPI queues the same durable flow used by the scheduler.
type CertificateRenewalAPI interface {
	Queue(
		context.Context,
		config.Actor,
		certificate.CertificateID,
		certificate.ManualRenewalInput,
	) (certificate.Task, error)
}

// CertificateBindingAPI creates and consumes standalone digest-bound binding reviews.
type CertificateBindingAPI interface {
	CreatePlan(
		context.Context, config.Actor, certificate.CertificateID, certificate.CreateBindingPlanInput,
	) (certificate.PlannedBinding, error)
	ExecutePlan(
		context.Context, config.Actor, certificate.BindingPlanID, certificate.ExecuteBindingPlanInput,
	) (certificate.Task, error)
}

// CertificateLifecycleAPI owns destructive operations and short-lived PEM exports.
type CertificateLifecycleAPI interface {
	Export(
		context.Context,
		config.Actor,
		certificate.CertificateID,
		certificate.ExportCertificateInput,
	) (certificate.CertificateExport, error)
	Unbind(context.Context, config.Actor, certificate.CertificateID, string) (certificate.Certificate, error)
	UpdateRenewalPolicy(
		context.Context, config.Actor, certificate.CertificateID, certificate.RenewalPolicyInput,
	) (certificate.Certificate, error)
	Delete(context.Context, config.Actor, certificate.CertificateID, string) (certificate.Certificate, error)
}

type certificateHandler struct {
	accounts     CertificateAccountAPI
	credentials  CertificateCredentialAPI
	plans        CertificatePlanAPI
	planReader   CertificatePlanReader
	queue        CertificateQueueAPI
	tasks        CertificateTaskAPI
	taskOwner    CertificateTaskController
	inventory    CertificateInventoryAPI
	renewals     CertificateRenewalAPI
	bindingPlans CertificateBindingAPI
	lifecycle    CertificateLifecycleAPI
	sessions     SessionService
	publicURL    *url.URL
}

type acmeAccountResponse struct {
	ID            string                    `json:"id"`
	Environment   certificate.Environment   `json:"environment"`
	DirectoryURL  string                    `json:"directory_url"`
	AccountURI    string                    `json:"account_uri"`
	Email         string                    `json:"email"`
	Status        certificate.AccountStatus `json:"status"`
	TermsURL      string                    `json:"terms_url"`
	TermsAgreedAt time.Time                 `json:"terms_agreed_at"`
	TermsAgreedBy int64                     `json:"terms_agreed_by"`
	CreatedAt     time.Time                 `json:"created_at"`
	UpdatedAt     time.Time                 `json:"updated_at"`
}

type acmeAccountsResponse struct {
	Accounts []acmeAccountResponse `json:"accounts"`
}

type importACMEAccountRequest struct {
	Environment          certificate.Environment `json:"environment"`
	Email                string                  `json:"email"`
	AccountURI           string                  `json:"account_uri"`
	PrivateKeyPEM        string                  `json:"private_key_pem"`
	TermsOfServiceAgreed bool                    `json:"terms_of_service_agreed"`
}

type dnsCredentialResponse struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Provider    certificate.DNSProvider      `json:"provider"`
	Fingerprint string                       `json:"fingerprint"`
	Status      certificate.CredentialStatus `json:"status"`
	VerifiedAt  time.Time                    `json:"verified_at"`
	LastUsedAt  *time.Time                   `json:"last_used_at,omitempty"`
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
}

type dnsCredentialsResponse struct {
	Credentials []dnsCredentialResponse `json:"credentials"`
}

type certificatePlanResponse struct {
	ID                     string                          `json:"id"`
	State                  certificate.PlanState           `json:"state"`
	Environment            certificate.Environment         `json:"environment"`
	Challenge              certificate.ChallengeType       `json:"challenge"`
	AccountID              string                          `json:"account_id"`
	StagingAccountID       string                          `json:"staging_account_id,omitempty"`
	DNSCredentialID        string                          `json:"dns_credential_id,omitempty"`
	CertificateID          string                          `json:"certificate_id"`
	PrimaryIdentifier      string                          `json:"primary_identifier"`
	Identifiers            []string                        `json:"identifiers"`
	ServerRefs             []certificate.ServerRef         `json:"server_refs"`
	BindingDiff            []certificate.BindingFileChange `json:"binding_diff"`
	ProductionDigest       string                          `json:"production_digest"`
	StagingEvidence        bool                            `json:"staging_evidence"`
	RequiresRiskConfirm    bool                            `json:"requires_risk_confirmation"`
	RiskConfirmationPhrase string                          `json:"risk_confirmation_phrase,omitempty"`
	ExpiresAt              time.Time                       `json:"expires_at"`
	CreatedAt              time.Time                       `json:"created_at"`
}

type certificateBindingPlanResponse struct {
	ID               string                          `json:"id"`
	State            certificate.PlanState           `json:"state"`
	CertificateID    string                          `json:"certificate_id"`
	VersionID        string                          `json:"version_id"`
	ServerRefs       []certificate.ServerRef         `json:"server_refs"`
	BindingDiff      []certificate.BindingFileChange `json:"binding_diff"`
	ProductionDigest string                          `json:"production_digest"`
	ExpiresAt        time.Time                       `json:"expires_at"`
	CreatedAt        time.Time                       `json:"created_at"`
}

type certificateTaskStageResponse struct {
	Sequence   uint64                    `json:"sequence"`
	Stage      certificate.TaskStageName `json:"stage"`
	Result     certificate.StageResult   `json:"result"`
	Code       string                    `json:"code,omitempty"`
	Details    json.RawMessage           `json:"details"`
	OccurredAt time.Time                 `json:"occurred_at"`
}

type certificateTaskResponse struct {
	ID                string                         `json:"id"`
	Kind              certificate.TaskKind           `json:"kind"`
	State             certificate.TaskState          `json:"state"`
	Stage             certificate.TaskStageName      `json:"stage"`
	PlanID            string                         `json:"plan_id,omitempty"`
	CertificateID     string                         `json:"certificate_id,omitempty"`
	VersionID         string                         `json:"version_id,omitempty"`
	AccountID         string                         `json:"account_id,omitempty"`
	DNSCredentialID   string                         `json:"dns_credential_id,omitempty"`
	Challenge         certificate.ChallengeType      `json:"challenge"`
	ReleaseID         string                         `json:"release_id,omitempty"`
	LastErrorCode     string                         `json:"last_error_code,omitempty"`
	CancelRequestedAt *time.Time                     `json:"cancel_requested_at,omitempty"`
	CreatedAt         time.Time                      `json:"created_at"`
	UpdatedAt         time.Time                      `json:"updated_at"`
	StartedAt         *time.Time                     `json:"started_at,omitempty"`
	FinishedAt        *time.Time                     `json:"finished_at,omitempty"`
	Stages            []certificateTaskStageResponse `json:"stages"`
}

type certificateTasksResponse struct {
	Tasks []certificateTaskResponse `json:"tasks"`
}

type certificateVersionResponse struct {
	ID              string                   `json:"id"`
	State           certificate.VersionState `json:"state"`
	LeafFingerprint string                   `json:"leaf_fingerprint"`
	SerialNumber    string                   `json:"serial_number"`
	Issuer          string                   `json:"issuer"`
	NotBefore       time.Time                `json:"not_before"`
	NotAfter        time.Time                `json:"not_after"`
	CreatedAt       time.Time                `json:"created_at"`
}

type certificateBindingResponse struct {
	ID                string    `json:"id"`
	VersionID         string    `json:"version_id"`
	ConfigPath        string    `json:"config_path"`
	ServerStartOffset int64     `json:"server_start_offset"`
	ServerNames       []string  `json:"server_names"`
	Listeners         []string  `json:"listeners"`
	ServerFingerprint string    `json:"server_fingerprint"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type certificateResponse struct {
	ID                 string                       `json:"id"`
	PrimaryIdentifier  string                       `json:"primary_identifier"`
	Identifiers        []string                     `json:"identifiers"`
	Challenge          certificate.ChallengeType    `json:"challenge"`
	AccountID          string                       `json:"account_id"`
	DNSCredentialID    string                       `json:"dns_credential_id,omitempty"`
	State              certificate.CertificateState `json:"state"`
	ActiveVersionID    string                       `json:"active_version_id"`
	AutoRenew          bool                         `json:"auto_renew"`
	RenewBeforeSeconds int64                        `json:"renew_before_seconds"`
	NextRenewalAt      *time.Time                   `json:"next_renewal_at,omitempty"`
	RetryCount         int                          `json:"retry_count"`
	RetryAt            *time.Time                   `json:"retry_at,omitempty"`
	NotBefore          time.Time                    `json:"not_before"`
	NotAfter           time.Time                    `json:"not_after"`
	LastErrorCode      string                       `json:"last_error_code,omitempty"`
	CreatedAt          time.Time                    `json:"created_at"`
	UpdatedAt          time.Time                    `json:"updated_at"`
	Versions           []certificateVersionResponse `json:"versions,omitempty"`
	Bindings           []certificateBindingResponse `json:"bindings,omitempty"`
}

type certificatesResponse struct {
	Certificates []certificateResponse `json:"certificates"`
}

type certificateConfirmationRequest struct {
	Confirmation string `json:"confirmation"`
}

func (handler *certificateHandler) directories(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if handler.accounts == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	directories, err := handler.accounts.Directories(request.Context())
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	if directories == nil {
		directories = []certificate.ACMEDirectory{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"directories": directories})
}

func (handler *certificateHandler) accountsCollection(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if !authorizeBusinessGET(writer, request, handler.sessions) || !requireNoQuery(writer, request) {
			return
		}
		if handler.accounts == nil {
			writeCertificateUnavailable(writer, request)
			return
		}
		accounts, err := handler.accounts.Accounts(request.Context())
		if err != nil {
			writeCertificateAPIError(writer, request, err)
			return
		}
		response := acmeAccountsResponse{Accounts: make([]acmeAccountResponse, 0, len(accounts))}
		for _, account := range accounts {
			response.Accounts = append(response.Accounts, newACMEAccountResponse(account))
		}
		writeJSON(writer, http.StatusOK, response)
	case http.MethodPost:
		actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
		if !ok || !requireNoQuery(writer, request) {
			return
		}
		if handler.accounts == nil {
			writeCertificateUnavailable(writer, request)
			return
		}
		input, err := decodeStrictJSON[certificate.CreateAccountInput](request, certificateRequestBodyLimit)
		if err != nil {
			writeCertificateDecodeError(writer, request, err)
			return
		}
		account, err := handler.accounts.Create(request.Context(), actor, input)
		if err != nil {
			writeCertificateAPIError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusCreated, newACMEAccountResponse(account))
	}
}

func (handler *certificateHandler) accountImports(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.accounts == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	input, err := decodeStrictJSON[importACMEAccountRequest](request, certificateSecretBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	key := []byte(input.PrivateKeyPEM)
	defer clear(key)
	account, err := handler.accounts.Import(request.Context(), actor, certificate.ImportAccountInput{
		Environment: input.Environment, Email: input.Email, AccountURI: input.AccountURI,
		PrivateKeyPEM: key, TermsOfServiceAgreed: input.TermsOfServiceAgreed,
	})
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, newACMEAccountResponse(account))
}

func (handler *certificateHandler) deactivateAccount(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.accounts == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	if _, err := decodeStrictJSON[struct{}](request, certificateRequestBodyLimit); err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	id, err := certificate.ParseAccountID(request.PathValue("account_id"))
	if err != nil {
		writeCertificateInvalidField(writer, request, "account_id")
		return
	}
	account, err := handler.accounts.Deactivate(request.Context(), actor, id)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, newACMEAccountResponse(account))
}

func (handler *certificateHandler) credentialsCollection(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if !authorizeBusinessGET(writer, request, handler.sessions) || !requireNoQuery(writer, request) {
			return
		}
		if handler.credentials == nil {
			writeCertificateUnavailable(writer, request)
			return
		}
		credentials, err := handler.credentials.Credentials(request.Context())
		if err != nil {
			writeCertificateAPIError(writer, request, err)
			return
		}
		response := dnsCredentialsResponse{Credentials: make([]dnsCredentialResponse, 0, len(credentials))}
		for _, credential := range credentials {
			response.Credentials = append(response.Credentials, newDNSCredentialResponse(credential))
		}
		writeJSON(writer, http.StatusOK, response)
	case http.MethodPost:
		actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
		if !ok || !requireNoQuery(writer, request) {
			return
		}
		if handler.credentials == nil {
			writeCertificateUnavailable(writer, request)
			return
		}
		input, err := decodeStrictJSON[certificate.CreateDNSCredentialInput](request, certificateSecretBodyLimit)
		if err != nil {
			writeCertificateDecodeError(writer, request, err)
			return
		}
		credential, err := handler.credentials.Create(request.Context(), actor, input)
		input.APIToken = ""
		if err != nil {
			writeCertificateAPIError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusCreated, newDNSCredentialResponse(credential))
	}
}

func (handler *certificateHandler) credential(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.credentials == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	if _, err := decodeStrictJSON[struct{}](request, certificateRequestBodyLimit); err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	id, err := certificate.ParseDNSCredentialID(request.PathValue("credential_id"))
	if err != nil {
		writeCertificateInvalidField(writer, request, "credential_id")
		return
	}
	if _, err := handler.credentials.Delete(request.Context(), actor, id); err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *certificateHandler) serverCandidates(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeCertificateRead(writer, request, handler.sessions)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.plans == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	candidates, err := handler.plans.ServerCandidates(request.Context(), actor)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	if candidates == nil {
		candidates = []certificate.ServerCandidate{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"candidates": candidates})
}

func (handler *certificateHandler) plansCollection(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.plans == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	input, err := decodeStrictJSON[certificate.CreateOrderPlanInput](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	planned, err := handler.plans.Create(request.Context(), actor, input)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	response, err := newCertificatePlanResponse(planned.Plan, &planned.Binding)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, response)
}

func (handler *certificateHandler) plan(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if handler.planReader == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, err := certificate.ParseOrderPlanID(request.PathValue("plan_id"))
	if err != nil {
		writeCertificateInvalidField(writer, request, "plan_id")
		return
	}
	plan, err := handler.planReader.CertificateOrderPlan(request.Context(), id, time.Now().UTC())
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	response, err := newCertificatePlanResponse(plan, nil)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *certificateHandler) executePlan(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.queue == nil || handler.taskOwner == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, err := certificate.ParseOrderPlanID(request.PathValue("plan_id"))
	if err != nil {
		writeCertificateInvalidField(writer, request, "plan_id")
		return
	}
	input, err := decodeStrictJSON[certificate.ExecuteOrderPlanInput](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	task, err := handler.queue.Execute(request.Context(), actor, id, input)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	if !handler.taskOwner.Start(task) {
		handler.cancelUnownedTask(request, actor, task.ID)
		writeCertificateUnavailable(writer, request)
		return
	}
	writer.Header().Set("Location", "/api/v1/certificate-tasks/"+string(task.ID))
	writeJSON(writer, http.StatusAccepted, newCertificateTaskResponse(task, []certificate.TaskStage{}))
}

func (handler *certificateHandler) tasksCollection(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) {
		return
	}
	if handler.tasks == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	limit, ok := parseCertificateLimit(writer, request)
	if !ok {
		return
	}
	tasks, err := handler.tasks.Tasks(request.Context(), limit)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	response := certificateTasksResponse{Tasks: make([]certificateTaskResponse, 0, len(tasks))}
	for _, task := range tasks {
		response.Tasks = append(response.Tasks, newCertificateTaskResponse(task, task.Stages))
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *certificateHandler) task(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if handler.tasks == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateTaskID(writer, request)
	if !ok {
		return
	}
	task, err := handler.tasks.Task(request.Context(), id)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	stages, err := handler.tasks.Stages(request.Context(), id, 0, certificateStageReadLimit)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, newCertificateTaskResponse(task, stages))
}

func (handler *certificateHandler) cancelTask(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.tasks == nil || handler.taskOwner == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	if _, err := decodeStrictJSON[struct{}](request, certificateRequestBodyLimit); err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	id, ok := parseCertificateTaskID(writer, request)
	if !ok {
		return
	}
	task, err := handler.tasks.Cancel(request.Context(), actor, id)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	_ = handler.taskOwner.Cancel(id)
	writeJSON(writer, http.StatusAccepted, newCertificateTaskResponse(task, []certificate.TaskStage{}))
}

func (handler *certificateHandler) taskEvents(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if handler.tasks == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateTaskID(writer, request)
	if !ok {
		return
	}
	after, ok := parseLastEventID(writer, request)
	if !ok {
		return
	}
	task, err := handler.tasks.Task(request.Context(), id)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	controller := http.NewResponseController(writer)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	if after == 0 {
		if err := writeReleaseSSEEvent(writer, controller, "0", "snapshot", newCertificateTaskResponse(task, nil)); err != nil {
			return
		}
	}
	for {
		stages, stageErr := handler.tasks.Stages(request.Context(), id, after, certificateStageReadLimit)
		if stageErr != nil {
			return
		}
		task, err = handler.tasks.Task(request.Context(), id)
		if err != nil {
			return
		}
		for _, stage := range stages {
			eventName := "stage"
			if task.State.Terminal() && stage.Stage == task.Stage {
				eventName = "terminal"
			}
			if err := writeReleaseSSEEvent(writer, controller, strconv.FormatUint(stage.Sequence, 10), eventName,
				newCertificateTaskStageResponse(stage)); err != nil {
				return
			}
			after = stage.Sequence
		}
		if task.State.Terminal() {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(2 * time.Second):
			if err := writeReleaseSSEEvent(writer, controller, "", "heartbeat", struct{}{}); err != nil {
				return
			}
		}
	}
}

func (handler *certificateHandler) certificatesCollection(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) {
		return
	}
	if handler.inventory == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	limit, ok := parseCertificateLimit(writer, request)
	if !ok {
		return
	}
	items, err := handler.inventory.Certificates(request.Context(), limit)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	response := certificatesResponse{Certificates: make([]certificateResponse, 0, len(items))}
	for _, item := range items {
		value, responseErr := newCertificateResponse(item, nil, nil)
		if responseErr != nil {
			writeCertificateAPIError(writer, request, responseErr)
			return
		}
		response.Certificates = append(response.Certificates, value)
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *certificateHandler) certificate(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, handler.sessions) || !requireNoQuery(writer, request) {
		return
	}
	if handler.inventory == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateRouteID(writer, request)
	if !ok {
		return
	}
	item, err := handler.inventory.Certificate(request.Context(), id)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	versions, err := handler.inventory.CertificateVersions(request.Context(), id)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	bindings, err := handler.inventory.CertificateBindings(request.Context(), id)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	response, err := newCertificateResponse(item, versions, bindings)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *certificateHandler) renewCertificate(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.renewals == nil || handler.taskOwner == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateRouteID(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[certificate.ManualRenewalInput](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	task, err := handler.renewals.Queue(request.Context(), actor, id, input)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	if !handler.taskOwner.Start(task) {
		handler.cancelUnownedTask(request, actor, task.ID)
		writeCertificateUnavailable(writer, request)
		return
	}
	writer.Header().Set("Location", "/api/v1/certificate-tasks/"+string(task.ID))
	writeJSON(writer, http.StatusAccepted, newCertificateTaskResponse(task, []certificate.TaskStage{}))
}

func (handler *certificateHandler) createBindingPlan(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.bindingPlans == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateRouteID(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[certificate.CreateBindingPlanInput](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	planned, err := handler.bindingPlans.CreatePlan(request.Context(), actor, id, input)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	response, err := newCertificateBindingPlanResponse(planned.Plan, &planned.Binding)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, response)
}

func (handler *certificateHandler) executeBindingPlan(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.bindingPlans == nil || handler.taskOwner == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, err := certificate.ParseBindingPlanID(request.PathValue("plan_id"))
	if err != nil {
		writeCertificateInvalidField(writer, request, "plan_id")
		return
	}
	input, err := decodeStrictJSON[certificate.ExecuteBindingPlanInput](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	task, err := handler.bindingPlans.ExecutePlan(request.Context(), actor, id, input)
	input.Confirmation = ""
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	if !handler.taskOwner.Start(task) {
		handler.cancelUnownedTask(request, actor, task.ID)
		writeCertificateUnavailable(writer, request)
		return
	}
	writer.Header().Set("Location", "/api/v1/certificate-tasks/"+string(task.ID))
	writeJSON(writer, http.StatusAccepted, newCertificateTaskResponse(task, []certificate.TaskStage{}))
}

func (handler *certificateHandler) exportCertificate(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.lifecycle == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateRouteID(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[certificate.ExportCertificateInput](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	exported, err := handler.lifecycle.Export(request.Context(), actor, id, input)
	input.PrivateKeyConfirmation = ""
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	defer clear(exported.Content)
	if len(exported.Content) == 0 || len(exported.Content) > 4<<20 {
		writeCertificateAPIError(writer, request, certificate.ErrCertificateInvalid)
		return
	}
	writer.Header().Set("Content-Type", "application/x-pem-file")
	writer.Header().Set("Content-Disposition", `attachment; filename="certificate-`+string(id)+`.pem"`)
	writer.Header().Set("Content-Length", strconv.Itoa(len(exported.Content)))
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Del("Content-Encoding")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(exported.Content)
}

func (handler *certificateHandler) unbindCertificate(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.lifecycle == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateRouteID(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[certificateConfirmationRequest](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	item, err := handler.lifecycle.Unbind(request.Context(), actor, id, input.Confirmation)
	input.Confirmation = ""
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	response, err := newCertificateResponse(item, nil, nil)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *certificateHandler) updateRenewalPolicy(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.lifecycle == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateRouteID(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[certificate.RenewalPolicyInput](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	item, err := handler.lifecycle.UpdateRenewalPolicy(request.Context(), actor, id, input)
	input.Confirmation = ""
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	response, err := newCertificateResponse(item, nil, nil)
	if err != nil {
		writeCertificateAPIError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *certificateHandler) deleteCertificate(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, handler.sessions, handler.publicURL)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if handler.lifecycle == nil {
		writeCertificateUnavailable(writer, request)
		return
	}
	id, ok := parseCertificateRouteID(writer, request)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[certificateConfirmationRequest](request, certificateRequestBodyLimit)
	if err != nil {
		writeCertificateDecodeError(writer, request, err)
		return
	}
	if _, err := handler.lifecycle.Delete(request.Context(), actor, id, input.Confirmation); err != nil {
		input.Confirmation = ""
		writeCertificateAPIError(writer, request, err)
		return
	}
	input.Confirmation = ""
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *certificateHandler) cancelUnownedTask(request *http.Request, actor config.Actor, id certificate.TaskID) {
	if handler.tasks == nil {
		return
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 2*time.Second)
	_, _ = handler.tasks.Cancel(cleanupContext, actor, id)
	cancel()
}

func authorizeCertificateRead(
	writer http.ResponseWriter,
	request *http.Request,
	sessions SessionService,
) (config.Actor, bool) {
	requestID := requestIDFromContext(request.Context())
	rawToken, ok := readSessionCookie(request)
	if !ok || sessions == nil {
		writeAPIError(writer, requestID, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
		return config.Actor{}, false
	}
	issued, err := sessions.Current(request.Context(), rawToken)
	if err != nil {
		(&sessionHandler{service: sessions}).writeAuthError(writer, requestID, err)
		return config.Actor{}, false
	}
	return config.Actor{UserID: issued.User.ID, RequestID: requestID}, true
}

func newACMEAccountResponse(account certificate.Account) acmeAccountResponse {
	return acmeAccountResponse{
		ID: string(account.ID), Environment: account.Environment, DirectoryURL: account.DirectoryURL,
		AccountURI: account.URI, Email: account.Email, Status: account.Status, TermsURL: account.TermsURL,
		TermsAgreedAt: account.TermsAgreedAt.UTC(), TermsAgreedBy: account.TermsAgreedBy,
		CreatedAt: account.CreatedAt.UTC(), UpdatedAt: account.UpdatedAt.UTC(),
	}
}

func newDNSCredentialResponse(value certificate.DNSCredential) dnsCredentialResponse {
	return dnsCredentialResponse{
		ID: string(value.ID), Name: value.Name, Provider: value.Provider, Fingerprint: value.Fingerprint,
		Status: value.Status, VerifiedAt: value.VerifiedAt.UTC(), LastUsedAt: certificateTimePointer(value.LastUsedAt),
		CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC(),
	}
}

func newCertificatePlanResponse(
	plan certificate.OrderPlan,
	binding *certificate.BindingChangePlan,
) (certificatePlanResponse, error) {
	var identifiers []string
	var refs []certificate.ServerRef
	var diff []certificate.BindingFileChange
	if err := json.Unmarshal([]byte(plan.IdentifiersJSON), &identifiers); err != nil || identifiers == nil {
		return certificatePlanResponse{}, certificate.ErrIdentifierInvalid
	}
	if binding == nil {
		if err := json.Unmarshal([]byte(plan.ServerRefsJSON), &refs); err != nil || refs == nil {
			return certificatePlanResponse{}, certificate.ErrBindingConflict
		}
		if err := json.Unmarshal([]byte(plan.BindingDiffJSON), &diff); err != nil || diff == nil {
			return certificatePlanResponse{}, certificate.ErrBindingConflict
		}
	} else {
		refs = append([]certificate.ServerRef(nil), binding.ServerRefs...)
		diff = append([]certificate.BindingFileChange(nil), binding.Files...)
	}
	response := certificatePlanResponse{
		ID: string(plan.ID), State: plan.State, Environment: plan.Environment, Challenge: plan.Challenge,
		AccountID: string(plan.AccountID), StagingAccountID: string(plan.StagingAccountID),
		DNSCredentialID: string(plan.DNSCredentialID), CertificateID: string(plan.CertificateID),
		PrimaryIdentifier: plan.PrimaryIdentifier, Identifiers: identifiers, ServerRefs: refs,
		BindingDiff: diff, ProductionDigest: hex.EncodeToString(plan.ProductionDigest[:]),
		StagingEvidence: plan.StagingEvidence, RequiresRiskConfirm: plan.RequiresRiskConfirm,
		ExpiresAt: plan.ExpiresAt.UTC(), CreatedAt: plan.CreatedAt.UTC(),
	}
	if plan.RequiresRiskConfirm {
		response.RiskConfirmationPhrase = certificate.ProductionRiskConfirmation
	}
	return response, nil
}

func newCertificateBindingPlanResponse(
	plan certificate.BindingPlan,
	binding *certificate.BindingChangePlan,
) (certificateBindingPlanResponse, error) {
	var refs []certificate.ServerRef
	var diff []certificate.BindingFileChange
	if binding == nil {
		if err := json.Unmarshal([]byte(plan.ServerRefsJSON), &refs); err != nil || refs == nil {
			return certificateBindingPlanResponse{}, certificate.ErrBindingConflict
		}
		if err := json.Unmarshal([]byte(plan.BindingDiffJSON), &diff); err != nil || diff == nil {
			return certificateBindingPlanResponse{}, certificate.ErrBindingConflict
		}
	} else {
		refs = append([]certificate.ServerRef(nil), binding.ServerRefs...)
		diff = append([]certificate.BindingFileChange(nil), binding.Files...)
	}
	return certificateBindingPlanResponse{
		ID: string(plan.ID), State: plan.State, CertificateID: string(plan.CertificateID),
		VersionID: string(plan.VersionID), ServerRefs: refs, BindingDiff: diff,
		ProductionDigest: hex.EncodeToString(plan.ProductionDigest[:]), ExpiresAt: plan.ExpiresAt.UTC(),
		CreatedAt: plan.CreatedAt.UTC(),
	}, nil
}

func newCertificateTaskResponse(
	task certificate.Task,
	stages []certificate.TaskStage,
) certificateTaskResponse {
	response := certificateTaskResponse{
		ID: string(task.ID), Kind: task.Kind, State: task.State, Stage: task.Stage,
		PlanID: string(task.PlanID), CertificateID: string(task.CertificateID), VersionID: string(task.VersionID),
		AccountID: string(task.AccountID), DNSCredentialID: string(task.DNSCredentialID), Challenge: task.Challenge,
		ReleaseID: task.ReleaseID, LastErrorCode: task.LastErrorCode,
		CancelRequestedAt: certificateTimePointer(task.CancelRequestedAt), CreatedAt: task.CreatedAt.UTC(),
		UpdatedAt: task.UpdatedAt.UTC(), StartedAt: certificateTimePointer(task.StartedAt),
		FinishedAt: certificateTimePointer(task.FinishedAt),
		Stages:     make([]certificateTaskStageResponse, 0, len(stages)),
	}
	for _, stage := range stages {
		response.Stages = append(response.Stages, newCertificateTaskStageResponse(stage))
	}
	return response
}

func newCertificateTaskStageResponse(stage certificate.TaskStage) certificateTaskStageResponse {
	details := json.RawMessage(stage.PublicDetailsJSON)
	if !json.Valid(details) || int64(len(details)) > certificateRequestBodyLimit {
		details = json.RawMessage(`{}`)
	}
	return certificateTaskStageResponse{
		Sequence: stage.Sequence, Stage: stage.Stage, Result: stage.Result, Code: stage.Code,
		Details: details, OccurredAt: stage.OccurredAt.UTC(),
	}
}

func newCertificateResponse(
	item certificate.Certificate,
	versions []certificate.Version,
	bindings []certificate.Binding,
) (certificateResponse, error) {
	var identifiers []string
	if err := json.Unmarshal([]byte(item.IdentifiersJSON), &identifiers); err != nil || identifiers == nil {
		return certificateResponse{}, certificate.ErrCertificateInvalid
	}
	response := certificateResponse{
		ID: string(item.ID), PrimaryIdentifier: item.PrimaryIdentifier, Identifiers: identifiers,
		Challenge: item.Challenge, AccountID: string(item.AccountID), DNSCredentialID: string(item.DNSCredentialID),
		State: item.State, ActiveVersionID: string(item.ActiveVersionID), AutoRenew: item.AutoRenew,
		RenewBeforeSeconds: item.RenewBeforeSeconds, NextRenewalAt: certificateTimePointer(item.NextRenewalAt),
		RetryCount: item.RetryCount, RetryAt: certificateTimePointer(item.RetryAt), NotBefore: item.NotBefore.UTC(),
		NotAfter: item.NotAfter.UTC(), LastErrorCode: item.LastErrorCode,
		CreatedAt: item.CreatedAt.UTC(), UpdatedAt: item.UpdatedAt.UTC(),
	}
	if versions != nil {
		response.Versions = make([]certificateVersionResponse, 0, len(versions))
		for _, version := range versions {
			response.Versions = append(response.Versions, certificateVersionResponse{
				ID: string(version.ID), State: version.State, LeafFingerprint: version.LeafFingerprint,
				SerialNumber: version.SerialNumber, Issuer: version.Issuer,
				NotBefore: version.NotBefore.UTC(), NotAfter: version.NotAfter.UTC(), CreatedAt: version.CreatedAt.UTC(),
			})
		}
	}
	if bindings != nil {
		response.Bindings = make([]certificateBindingResponse, 0, len(bindings))
		for _, binding := range bindings {
			var names, listeners []string
			if err := json.Unmarshal([]byte(binding.ServerNamesJSON), &names); err != nil || names == nil {
				return certificateResponse{}, certificate.ErrBindingConflict
			}
			if err := json.Unmarshal([]byte(binding.ListenersJSON), &listeners); err != nil || listeners == nil {
				return certificateResponse{}, certificate.ErrBindingConflict
			}
			response.Bindings = append(response.Bindings, certificateBindingResponse{
				ID: string(binding.ID), VersionID: string(binding.VersionID), ConfigPath: binding.ConfigPath,
				ServerStartOffset: binding.ServerStartOffset, ServerNames: names, Listeners: listeners,
				ServerFingerprint: binding.ServerFingerprint, CreatedAt: binding.CreatedAt.UTC(),
				UpdatedAt: binding.UpdatedAt.UTC(),
			})
		}
	}
	return response, nil
}

func certificateTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	result := value.UTC()
	return &result
}

func parseCertificateTaskID(writer http.ResponseWriter, request *http.Request) (certificate.TaskID, bool) {
	id, err := certificate.ParseTaskID(request.PathValue("task_id"))
	if err != nil {
		writeCertificateInvalidField(writer, request, "task_id")
		return "", false
	}
	return id, true
}

func parseCertificateRouteID(writer http.ResponseWriter, request *http.Request) (certificate.CertificateID, bool) {
	id, err := certificate.ParseCertificateID(request.PathValue("certificate_id"))
	if err != nil {
		writeCertificateInvalidField(writer, request, "certificate_id")
		return "", false
	}
	return id, true
}

func parseCertificateLimit(writer http.ResponseWriter, request *http.Request) (int, bool) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "limit" || len(values) != 1 {
			writeCertificateInvalidField(writer, request, "query")
			return 0, false
		}
	}
	limit := certificateListDefault
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > certificateListMaximum {
			writeCertificateInvalidField(writer, request, "limit")
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

func writeCertificateDecodeError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, errRequestBodyTooLarge):
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusRequestEntityTooLarge,
			"CERTIFICATE_LIMIT_EXCEEDED", "证书请求超过安全限制", nil)
	case errors.Is(err, errUnsupportedJSONMediaType):
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusUnsupportedMediaType,
			"unsupported_media_type", "仅接受 application/json", nil)
	default:
		writeCertificateInvalidField(writer, request, "body")
	}
}

func writeCertificateInvalidField(writer http.ResponseWriter, request *http.Request, field string) {
	writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusUnprocessableEntity,
		"CERTIFICATE_REQUEST_INVALID", "证书请求无效", map[string]any{"field": field})
}

func writeCertificateUnavailable(writer http.ResponseWriter, request *http.Request) {
	writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusServiceUnavailable,
		"CERTIFICATE_SERVICE_UNAVAILABLE", "证书服务暂时不可用", nil)
}

func writeCertificateAPIError(writer http.ResponseWriter, request *http.Request, err error) {
	requestID := requestIDFromContext(request.Context())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeAPIError(writer, requestID, http.StatusNotFound, "CERTIFICATE_RESOURCE_NOT_FOUND", "证书资源不存在", nil)
	case errors.Is(err, certificate.ErrACMETermsRequired):
		writeAPIError(writer, requestID, http.StatusConflict, "ACME_TERMS_REQUIRED", "必须明确同意当前 ACME 服务条款", nil)
	case errors.Is(err, certificate.ErrACMEAccountDeactivated):
		writeAPIError(writer, requestID, http.StatusConflict, "ACME_ACCOUNT_DEACTIVATED", "ACME 账户已停用，必须选择有效账户", nil)
	case errors.Is(err, certificate.ErrACMEAccountInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "ACME_ACCOUNT_INVALID", "ACME 账户无效或已停用", nil)
	case errors.Is(err, certificate.ErrACMEAccountNeedsAttention):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_NEEDS_ATTENTION", "ACME 账户停用状态需要重试或人工处理", nil)
	case errors.Is(err, certificate.ErrACMERateLimited):
		var limited *certificate.ACMERateLimitError
		var details map[string]any
		if errors.As(err, &limited) && limited.RetryAfter > 0 {
			seconds := int64((limited.RetryAfter + time.Second - 1) / time.Second)
			writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			details = map[string]any{"retry_after_seconds": seconds}
		}
		writeAPIError(writer, requestID, http.StatusTooManyRequests, "ACME_RATE_LIMITED", "ACME 服务限制了请求频率，请按提示稍后重试", details)
	case errors.Is(err, certificate.ErrACMEUnavailable):
		writeAPIError(writer, requestID, http.StatusServiceUnavailable, "ACME_ORDER_FAILED", "ACME 服务暂时不可用", nil)
	case errors.Is(err, certificate.ErrWildcardRequiresDNS):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CERTIFICATE_WILDCARD_REQUIRES_DNS", "通配符证书必须使用 Cloudflare DNS-01", nil)
	case errors.Is(err, certificate.ErrIdentifierInvalid), errors.Is(err, certificate.ErrIDInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CERTIFICATE_IDENTIFIER_INVALID", "证书标识符无效", nil)
	case errors.Is(err, certificate.ErrCloudflareTokenInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CLOUDFLARE_TOKEN_INVALID", "Cloudflare API Token 无效或未激活", nil)
	case errors.Is(err, certificate.ErrCloudflarePermission):
		writeAPIError(writer, requestID, http.StatusForbidden, "CLOUDFLARE_PERMISSION_DENIED", "Cloudflare Token 缺少 Zone Read 或 DNS Write 权限", nil)
	case errors.Is(err, certificate.ErrCloudflareZoneNotFound):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CLOUDFLARE_ZONE_NOT_FOUND", "找不到 Token 可访问的目标 Zone", nil)
	case errors.Is(err, certificate.ErrCloudflareRateLimited):
		writeAPIError(writer, requestID, http.StatusTooManyRequests, "ACME_RATE_LIMITED", "Cloudflare 请求受到速率限制，请稍后重试", nil)
	case errors.Is(err, certificate.ErrCloudflareUnavailable):
		writeAPIError(writer, requestID, http.StatusServiceUnavailable, "CLOUDFLARE_UNAVAILABLE", "Cloudflare 服务暂时不可用", nil)
	case errors.Is(err, certificate.ErrDNSPropagationTimeout):
		writeAPIError(writer, requestID, http.StatusGatewayTimeout, "DNS_PROPAGATION_TIMEOUT", "权威 DNS 尚未观察到验证记录", nil)
	case errors.Is(err, certificate.ErrCertificateKeyMismatch):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CERTIFICATE_KEY_MISMATCH", "签发证书与私钥不匹配", nil)
	case errors.Is(err, certificate.ErrCertificateSANMismatch):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CERTIFICATE_SAN_MISMATCH", "签发证书的域名集合与计划不一致", nil)
	case errors.Is(err, certificate.ErrCertificateInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CERTIFICATE_FILE_INVALID", "签发证书无法通过完整校验", nil)
	case errors.Is(err, certificate.ErrServerNotFound):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_SERVER_NOT_FOUND", "目标 Nginx server 已不存在", nil)
	case errors.Is(err, certificate.ErrServerAmbiguous):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_SERVER_AMBIGUOUS", "目标 Nginx server 无法唯一定位", nil)
	case errors.Is(err, certificate.ErrBindingConflict), errors.Is(err, certificate.ErrPlanChanged),
		errors.Is(err, config.ErrProductionChanged), errors.Is(err, config.ErrConflict):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_BINDING_CONFLICT", "证书计划或 Nginx 绑定事实已变化，请重新预览", nil)
	case errors.Is(err, certificate.ErrProductionRiskConfirmationRequired):
		writeAPIError(writer, requestID, http.StatusConflict, "ACME_STAGING_PREFLIGHT_REQUIRED", "缺少 staging 证据，必须明确确认 production 风险", nil)
	case errors.Is(err, certificate.ErrPlanExpired):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_PLAN_EXPIRED", "证书计划已过期，请重新预览", nil)
	case errors.Is(err, certificate.ErrTaskActive):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_TASK_ACTIVE", "该证书已有进行中的任务", nil)
	case errors.Is(err, certificate.ErrPrivateKeyExportConfirmationRequired):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_PRIVATE_KEY_CONFIRMATION_REQUIRED", "导出私钥需要第二次明确确认", nil)
	case errors.Is(err, certificate.ErrCertificateReferenced):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_REFERENCED", "证书仍被引用或确认内容不匹配", nil)
	case errors.Is(err, certificate.ErrRenewalPolicyInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "CERTIFICATE_RENEWAL_POLICY_INVALID", "自动续期策略或确认内容无效", nil)
	case errors.Is(err, certificate.ErrConfigurationReleaseUncertain), errors.Is(err, certificate.ErrSecretInvalid):
		writeAPIError(writer, requestID, http.StatusConflict, "CERTIFICATE_NEEDS_ATTENTION", "证书状态无法安全判定，需要人工处理", nil)
	case errors.Is(err, certificate.ErrConfigurationReleaseFailed):
		writeAPIError(writer, requestID, http.StatusConflict, "CHALLENGE_CLEANUP_FAILED", "证书配置发布或清理失败，旧配置保持不变", nil)
	case errors.Is(err, context.DeadlineExceeded):
		writeAPIError(writer, requestID, http.StatusGatewayTimeout, "CERTIFICATE_OPERATION_TIMEOUT", "证书操作超时", nil)
	default:
		writeAPIError(writer, requestID, http.StatusInternalServerError, "internal_error", "服务暂时不可用", nil)
	}
}
