/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

const (
	opaqueIDBytes       = 16
	maxSafeJSONBytes    = 2 << 20
	defaultRenewSeconds = int64((30 * 24 * time.Hour) / time.Second)
)

var (
	// ErrIDInvalid indicates a malformed certificate-domain opaque identifier.
	ErrIDInvalid = errors.New("certificate id invalid")
	// ErrPlanExpired indicates that a digest-bound order or binding plan is no longer executable.
	ErrPlanExpired = errors.New("certificate plan expired")
	// ErrTaskActive indicates that a conflicting certificate task is already active.
	ErrTaskActive = errors.New("certificate task active")
)

// Digest is a stable SHA-256 digest captured at a configuration boundary.
type Digest [32]byte

// Domain-specific opaque identifiers prevent accidental cross-resource use.
type (
	AccountID       string
	DNSCredentialID string
	OrderPlanID     string
	BindingPlanID   string
	CertificateID   string
	VersionID       string
	BindingID       string
	TaskID          string
	ArtifactID      string
)

// NewAccountID generates a cryptographically random account identifier.
func NewAccountID(random io.Reader) (AccountID, error) {
	value, err := newOpaqueID(random)
	return AccountID(value), err
}

// NewDNSCredentialID generates a cryptographically random credential identifier.
func NewDNSCredentialID(random io.Reader) (DNSCredentialID, error) {
	value, err := newOpaqueID(random)
	return DNSCredentialID(value), err
}

// NewOrderPlanID generates a cryptographically random plan identifier.
func NewOrderPlanID(random io.Reader) (OrderPlanID, error) {
	value, err := newOpaqueID(random)
	return OrderPlanID(value), err
}

// NewBindingPlanID generates a cryptographically random standalone binding-plan identifier.
func NewBindingPlanID(random io.Reader) (BindingPlanID, error) {
	value, err := newOpaqueID(random)
	return BindingPlanID(value), err
}

// NewCertificateID generates a cryptographically random certificate identifier.
func NewCertificateID(random io.Reader) (CertificateID, error) {
	value, err := newOpaqueID(random)
	return CertificateID(value), err
}

// NewVersionID generates a cryptographically random immutable version identifier.
func NewVersionID(random io.Reader) (VersionID, error) {
	value, err := newOpaqueID(random)
	return VersionID(value), err
}

// NewBindingID generates a cryptographically random binding identifier.
func NewBindingID(random io.Reader) (BindingID, error) {
	value, err := newOpaqueID(random)
	return BindingID(value), err
}

// NewTaskID generates a cryptographically random task identifier.
func NewTaskID(random io.Reader) (TaskID, error) {
	value, err := newOpaqueID(random)
	return TaskID(value), err
}

// NewArtifactID generates a cryptographically random challenge-artifact identifier.
func NewArtifactID(random io.Reader) (ArtifactID, error) {
	value, err := newOpaqueID(random)
	return ArtifactID(value), err
}

// ParseAccountID validates an account identifier.
func ParseAccountID(value string) (AccountID, error) { return AccountID(value), parseOpaqueID(value) }

// ParseDNSCredentialID validates a DNS credential identifier.
func ParseDNSCredentialID(value string) (DNSCredentialID, error) {
	return DNSCredentialID(value), parseOpaqueID(value)
}

// ParseOrderPlanID validates an order plan identifier.
func ParseOrderPlanID(value string) (OrderPlanID, error) {
	return OrderPlanID(value), parseOpaqueID(value)
}

// ParseBindingPlanID validates a standalone binding-plan identifier.
func ParseBindingPlanID(value string) (BindingPlanID, error) {
	return BindingPlanID(value), parseOpaqueID(value)
}

// ParseCertificateID validates a certificate identifier.
func ParseCertificateID(value string) (CertificateID, error) {
	return CertificateID(value), parseOpaqueID(value)
}

// ParseVersionID validates a certificate-version identifier.
func ParseVersionID(value string) (VersionID, error) { return VersionID(value), parseOpaqueID(value) }

// ParseBindingID validates a binding identifier.
func ParseBindingID(value string) (BindingID, error) { return BindingID(value), parseOpaqueID(value) }

// ParseTaskID validates a certificate-task identifier.
func ParseTaskID(value string) (TaskID, error) { return TaskID(value), parseOpaqueID(value) }

// ParseArtifactID validates a challenge-artifact identifier.
func ParseArtifactID(value string) (ArtifactID, error) {
	return ArtifactID(value), parseOpaqueID(value)
}

func newOpaqueID(random io.Reader) (string, error) {
	if random == nil {
		return "", fmt.Errorf("generate certificate id: %w", ErrIDInvalid)
	}
	buffer := make([]byte, opaqueIDBytes)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", fmt.Errorf("generate certificate id: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func parseOpaqueID(value string) error {
	if len(value) != opaqueIDBytes*2 || strings.ToLower(value) != value {
		return ErrIDInvalid
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != opaqueIDBytes {
		return ErrIDInvalid
	}
	return nil
}

// Environment separates staging and production ACME identities and evidence.
type Environment string

const (
	EnvironmentStaging    Environment = "staging"
	EnvironmentProduction Environment = "production"
)

// Valid reports whether the environment is supported by v0.5.
func (value Environment) Valid() bool {
	return value == EnvironmentStaging || value == EnvironmentProduction
}

// AccountStatus is the durable ACME registration state.
type AccountStatus string

const (
	AccountStatusValid        AccountStatus = "valid"
	AccountStatusDeactivating AccountStatus = "deactivating"
	AccountStatusDeactivated  AccountStatus = "deactivated"
)

func (value AccountStatus) Valid() bool {
	return value == AccountStatusValid || value == AccountStatusDeactivating ||
		value == AccountStatusDeactivated
}

// DNSProvider is deliberately closed in v0.5.
type DNSProvider string

const DNSProviderCloudflare DNSProvider = "cloudflare"

func (value DNSProvider) Valid() bool { return value == DNSProviderCloudflare }

// CredentialStatus describes whether the encrypted provider credential is usable.
type CredentialStatus string

const (
	CredentialStatusValid          CredentialStatus = "valid"
	CredentialStatusNeedsAttention CredentialStatus = "needs_attention"
	CredentialStatusDeleted        CredentialStatus = "deleted"
)

func (value CredentialStatus) Valid() bool {
	return value == CredentialStatusValid || value == CredentialStatusNeedsAttention ||
		value == CredentialStatusDeleted
}

// PlanState prevents an exact digest-bound plan from being executed more than once.
type PlanState string

const (
	PlanStatePlanned  PlanState = "planned"
	PlanStateExecuted PlanState = "executed"
	PlanStateExpired  PlanState = "expired"
)

func (value PlanState) Valid() bool {
	return value == PlanStatePlanned || value == PlanStateExecuted || value == PlanStateExpired
}

// CertificateState is the user-visible certificate lifecycle.
type CertificateState string

const (
	CertificateStatePending        CertificateState = "pending"
	CertificateStateActive         CertificateState = "active"
	CertificateStateExpiring       CertificateState = "expiring"
	CertificateStateExpired        CertificateState = "expired"
	CertificateStateUnbound        CertificateState = "unbound"
	CertificateStateNeedsAttention CertificateState = "needs_attention"
	CertificateStateDeleted        CertificateState = "deleted"
)

func (value CertificateState) Valid() bool {
	switch value {
	case CertificateStatePending, CertificateStateActive, CertificateStateExpiring,
		CertificateStateExpired, CertificateStateUnbound, CertificateStateNeedsAttention,
		CertificateStateDeleted:
		return true
	default:
		return false
	}
}

// VersionState distinguishes immutable staged material from the active version.
type VersionState string

const (
	VersionStateReady          VersionState = "ready"
	VersionStateActive         VersionState = "active"
	VersionStateSuperseded     VersionState = "superseded"
	VersionStateNeedsAttention VersionState = "needs_attention"
)

func (value VersionState) Valid() bool {
	return value == VersionStateReady || value == VersionStateActive ||
		value == VersionStateSuperseded || value == VersionStateNeedsAttention
}

// TaskKind identifies one bounded certificate operation.
type TaskKind string

const (
	TaskKindIssue  TaskKind = "issue"
	TaskKindRenew  TaskKind = "renew"
	TaskKindBind   TaskKind = "bind"
	TaskKindUnbind TaskKind = "unbind"
)

func (value TaskKind) Valid() bool {
	return value == TaskKindIssue || value == TaskKindRenew || value == TaskKindBind || value == TaskKindUnbind
}

// TaskState is the durable task state machine.
type TaskState string

const (
	TaskStateQueued         TaskState = "queued"
	TaskStateRunning        TaskState = "running"
	TaskStateCancelling     TaskState = "cancelling"
	TaskStateSucceeded      TaskState = "succeeded"
	TaskStateFailed         TaskState = "failed"
	TaskStateCancelled      TaskState = "cancelled"
	TaskStateNeedsAttention TaskState = "needs_attention"
)

func (value TaskState) Valid() bool {
	switch value {
	case TaskStateQueued, TaskStateRunning, TaskStateCancelling, TaskStateSucceeded,
		TaskStateFailed, TaskStateCancelled, TaskStateNeedsAttention:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further ordinary transition may occur.
func (value TaskState) Terminal() bool {
	return value == TaskStateSucceeded || value == TaskStateFailed ||
		value == TaskStateCancelled || value == TaskStateNeedsAttention
}

// TaskStageName is a safe, persisted lifecycle marker.
type TaskStageName string

const (
	TaskStageQueued         TaskStageName = "queued"
	TaskStagePreparing      TaskStageName = "preparing"
	TaskStageOrdering       TaskStageName = "ordering"
	TaskStageProvisioning   TaskStageName = "provisioning"
	TaskStagePropagating    TaskStageName = "propagating"
	TaskStageAuthorizing    TaskStageName = "authorizing"
	TaskStageFinalizing     TaskStageName = "finalizing"
	TaskStageValidating     TaskStageName = "validating"
	TaskStageDeploying      TaskStageName = "deploying"
	TaskStageCleaning       TaskStageName = "cleaning"
	TaskStageCompleted      TaskStageName = "completed"
	TaskStageFailed         TaskStageName = "failed"
	TaskStageCancelled      TaskStageName = "cancelled"
	TaskStageNeedsAttention TaskStageName = "needs_attention"
)

func (value TaskStageName) Valid() bool {
	switch value {
	case TaskStageQueued, TaskStagePreparing, TaskStageOrdering, TaskStageProvisioning,
		TaskStagePropagating, TaskStageAuthorizing, TaskStageFinalizing, TaskStageValidating,
		TaskStageDeploying, TaskStageCleaning, TaskStageCompleted, TaskStageFailed,
		TaskStageCancelled, TaskStageNeedsAttention:
		return true
	default:
		return false
	}
}

// StageResult is the durable result of one task stage.
type StageResult string

const (
	StageResultPending StageResult = "pending"
	StageResultRunning StageResult = "running"
	StageResultSuccess StageResult = "success"
	StageResultFailed  StageResult = "failed"
	StageResultWarning StageResult = "warning"
)

func (value StageResult) Valid() bool {
	return value == StageResultPending || value == StageResultRunning || value == StageResultSuccess ||
		value == StageResultFailed || value == StageResultWarning
}

// ArtifactKind identifies a task-owned cleanup target without storing challenge secrets.
type ArtifactKind string

const (
	ArtifactCloudflareTXT ArtifactKind = "cloudflare_txt"
	ArtifactHTTPInclude   ArtifactKind = "http_include"
)

func (value ArtifactKind) Valid() bool {
	return value == ArtifactCloudflareTXT || value == ArtifactHTTPInclude
}

// ArtifactState records exact cleanup progress.
type ArtifactState string

const (
	ArtifactStateCreated        ArtifactState = "created"
	ArtifactStateCleaned        ArtifactState = "cleaned"
	ArtifactStateNeedsAttention ArtifactState = "needs_attention"
)

func (value ArtifactState) Valid() bool {
	return value == ArtifactStateCreated || value == ArtifactStateCleaned || value == ArtifactStateNeedsAttention
}

// Account contains non-secret ACME registration metadata.
type Account struct {
	ID            AccountID
	Environment   Environment
	DirectoryURL  string
	URI           string
	Email         string
	Status        AccountStatus
	TermsURL      string
	TermsAgreedAt time.Time
	TermsAgreedBy int64
	CreatedBy     int64
	RequestID     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// DNSCredential contains only safe Cloudflare Token metadata.
type DNSCredential struct {
	ID          DNSCredentialID
	Name        string
	Provider    DNSProvider
	Fingerprint string
	Status      CredentialStatus
	VerifiedAt  time.Time
	LastUsedAt  time.Time
	CreatedBy   int64
	RequestID   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// OrderPlan is an expiring, digest-bound execution contract.
type OrderPlan struct {
	ID                  OrderPlanID
	State               PlanState
	Environment         Environment
	Challenge           ChallengeType
	AccountID           AccountID
	StagingAccountID    AccountID
	DNSCredentialID     DNSCredentialID
	CertificateID       CertificateID
	VersionID           VersionID
	PrimaryIdentifier   string
	IdentifiersJSON     string
	ServerRefsJSON      string
	ProductionDigest    Digest
	BindingDiffJSON     string
	StagingEvidence     bool
	RequiresRiskConfirm bool
	ExpiresAt           time.Time
	CreatedBy           int64
	RequestID           string
	CreatedAt           time.Time
	ExecutedAt          time.Time
}

// BindingPlan is an expiring production-digest-bound standalone deployment review.
type BindingPlan struct {
	ID               BindingPlanID
	State            PlanState
	CertificateID    CertificateID
	VersionID        VersionID
	ServerRefsJSON   string
	ProductionDigest Digest
	BindingDiffJSON  string
	ExpiresAt        time.Time
	CreatedBy        int64
	RequestID        string
	CreatedAt        time.Time
	ExecutedAt       time.Time
}

// Certificate is durable safe lifecycle and renewal metadata.
type Certificate struct {
	ID                 CertificateID
	PrimaryIdentifier  string
	IdentifiersJSON    string
	Challenge          ChallengeType
	AccountID          AccountID
	DNSCredentialID    DNSCredentialID
	State              CertificateState
	ActiveVersionID    VersionID
	AutoRenew          bool
	RenewBeforeSeconds int64
	NextRenewalAt      time.Time
	RetryCount         int
	RetryAt            time.Time
	NotBefore          time.Time
	NotAfter           time.Time
	LastErrorCode      string
	CreatedBy          int64
	RequestID          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Version is immutable public metadata for validated material on disk.
type Version struct {
	ID               VersionID
	CertificateID    CertificateID
	State            VersionState
	FullchainDigest  string
	PrivateKeyDigest string
	LeafFingerprint  string
	SerialNumber     string
	Issuer           string
	NotBefore        time.Time
	NotAfter         time.Time
	CreatedAt        time.Time
}

// Binding identifies one exact server block and active certificate version.
type Binding struct {
	ID                BindingID
	CertificateID     CertificateID
	VersionID         VersionID
	ConfigPath        string
	ServerStartOffset int64
	ServerNamesJSON   string
	ListenersJSON     string
	ServerFingerprint string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Task is one persisted issue, renew, bind or unbind operation.
type Task struct {
	ID                TaskID
	Kind              TaskKind
	State             TaskState
	Stage             TaskStageName
	PlanID            OrderPlanID
	CertificateID     CertificateID
	VersionID         VersionID
	AccountID         AccountID
	DNSCredentialID   DNSCredentialID
	Challenge         ChallengeType
	ReleaseID         string
	LastErrorCode     string
	CreatedBy         int64
	RequestID         string
	CancelRequestedAt time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         time.Time
	FinishedAt        time.Time
	Stages            []TaskStage
}

// TaskStage is one immutable public progress event.
type TaskStage struct {
	TaskID            TaskID
	Sequence          uint64
	Stage             TaskStageName
	Result            StageResult
	Code              string
	PublicDetailsJSON string
	OccurredAt        time.Time
}

// ChallengeArtifact stores only exact provider/config cleanup identifiers.
type ChallengeArtifact struct {
	ID              ArtifactID
	TaskID          TaskID
	Kind            ArtifactKind
	State           ArtifactState
	DNSCredentialID DNSCredentialID
	ZoneID          string
	RecordID        string
	RecordName      string
	ConfigPath      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ValidateAccount checks safe metadata before persistence.
func ValidateAccount(value Account) error {
	if parseOpaqueID(string(value.ID)) != nil || !value.Environment.Valid() || !value.Status.Valid() ||
		!validHTTPSURL(value.DirectoryURL) || !validACMEAccountURI(value.DirectoryURL, value.URI) ||
		!validHTTPSURL(value.TermsURL) ||
		!validEmail(value.Email) || value.TermsAgreedBy <= 0 || value.CreatedBy <= 0 ||
		!validRequestID(value.RequestID) || !validLifecycleTimes(value.CreatedAt, value.UpdatedAt) ||
		value.TermsAgreedAt.IsZero() {
		return fmt.Errorf("validate certificate account: invalid metadata")
	}
	return nil
}

// ValidateDNSCredential checks secret-free provider metadata before persistence.
func ValidateDNSCredential(value DNSCredential) error {
	if parseOpaqueID(string(value.ID)) != nil || !value.Provider.Valid() || !value.Status.Valid() ||
		!validDisplayName(value.Name) || !validLowerHex(value.Fingerprint, 16) || value.VerifiedAt.IsZero() ||
		value.CreatedBy <= 0 || !validRequestID(value.RequestID) || !validLifecycleTimes(value.CreatedAt, value.UpdatedAt) ||
		(!value.LastUsedAt.IsZero() && value.LastUsedAt.Before(value.CreatedAt)) {
		return fmt.Errorf("validate certificate DNS credential: invalid metadata")
	}
	return nil
}

// ValidateOrderPlan checks a bounded, secret-free execution plan.
func ValidateOrderPlan(value OrderPlan) error {
	if parseOpaqueID(string(value.ID)) != nil || !value.State.Valid() || !value.Environment.Valid() ||
		!validChallenge(value.Challenge) || parseOpaqueID(string(value.AccountID)) != nil ||
		!validOptionalID(string(value.StagingAccountID)) || !validOptionalID(string(value.DNSCredentialID)) ||
		!validOptionalID(string(value.CertificateID)) || !validOptionalID(string(value.VersionID)) ||
		value.PrimaryIdentifier == "" || !validJSONArray(value.IdentifiersJSON) ||
		!validJSONArray(value.ServerRefsJSON) || !validJSONArray(value.BindingDiffJSON) ||
		value.ExpiresAt.IsZero() || value.CreatedAt.IsZero() || !value.ExpiresAt.After(value.CreatedAt) ||
		value.CreatedBy <= 0 || !validRequestID(value.RequestID) ||
		(value.Challenge == ChallengeCloudflareDNS01 && value.DNSCredentialID == "") ||
		(value.State == PlanStateExecuted && value.ExecutedAt.IsZero()) {
		return fmt.Errorf("validate certificate order plan: invalid metadata")
	}
	return nil
}

// ValidateBindingPlan checks a bounded, secret-free standalone binding contract.
func ValidateBindingPlan(value BindingPlan) error {
	if parseOpaqueID(string(value.ID)) != nil || !value.State.Valid() ||
		parseOpaqueID(string(value.CertificateID)) != nil || parseOpaqueID(string(value.VersionID)) != nil ||
		!validJSONArray(value.ServerRefsJSON) || !validJSONArray(value.BindingDiffJSON) ||
		value.ExpiresAt.IsZero() || value.CreatedAt.IsZero() || !value.ExpiresAt.After(value.CreatedAt) ||
		value.CreatedBy <= 0 || !validRequestID(value.RequestID) ||
		(value.State == PlanStateExecuted && value.ExecutedAt.IsZero()) ||
		(value.State != PlanStateExecuted && !value.ExecutedAt.IsZero()) {
		return fmt.Errorf("validate certificate binding plan: invalid metadata")
	}
	return nil
}

// ValidateCertificate checks safe lifecycle and renewal metadata.
func ValidateCertificate(value Certificate) error {
	if parseOpaqueID(string(value.ID)) != nil || parseOpaqueID(string(value.AccountID)) != nil ||
		!validOptionalID(string(value.DNSCredentialID)) || !value.State.Valid() || !validChallenge(value.Challenge) ||
		value.PrimaryIdentifier == "" || !validJSONArray(value.IdentifiersJSON) || value.CreatedBy <= 0 ||
		!validRequestID(value.RequestID) || !validLifecycleTimes(value.CreatedAt, value.UpdatedAt) ||
		value.RenewBeforeSeconds <= 0 || value.RenewBeforeSeconds > int64((90*24*time.Hour)/time.Second) ||
		value.RetryCount < 0 || value.NotBefore.IsZero() || value.NotAfter.IsZero() || !value.NotAfter.After(value.NotBefore) ||
		(value.State != CertificateStatePending && value.State != CertificateStateDeleted &&
			parseOpaqueID(string(value.ActiveVersionID)) != nil) ||
		((value.State == CertificateStatePending || value.State == CertificateStateDeleted) && value.ActiveVersionID != "") {
		return fmt.Errorf("validate certificate: invalid metadata")
	}
	return nil
}

// ValidateVersion checks immutable certificate material metadata.
func ValidateVersion(value Version) error {
	if parseOpaqueID(string(value.ID)) != nil || parseOpaqueID(string(value.CertificateID)) != nil ||
		!value.State.Valid() || !validLowerHex(value.FullchainDigest, 64) ||
		!validLowerHex(value.PrivateKeyDigest, 64) || !validLowerHex(value.LeafFingerprint, 64) ||
		value.SerialNumber == "" || len(value.SerialNumber) > 256 || value.Issuer == "" || len(value.Issuer) > 512 ||
		value.NotBefore.IsZero() || value.NotAfter.IsZero() || !value.NotAfter.After(value.NotBefore) || value.CreatedAt.IsZero() {
		return fmt.Errorf("validate certificate version: invalid metadata")
	}
	return nil
}

// ValidateBinding checks one stable, source-derived server reference.
func ValidateBinding(value Binding) error {
	if parseOpaqueID(string(value.ID)) != nil || parseOpaqueID(string(value.CertificateID)) != nil ||
		parseOpaqueID(string(value.VersionID)) != nil || value.ConfigPath == "" || len(value.ConfigPath) > 4096 ||
		strings.HasPrefix(value.ConfigPath, "/") || strings.Contains(value.ConfigPath, "\\") ||
		value.ServerStartOffset < 0 || !validJSONArray(value.ServerNamesJSON) || !validJSONArray(value.ListenersJSON) ||
		!validLowerHex(value.ServerFingerprint, 64) || !validLifecycleTimes(value.CreatedAt, value.UpdatedAt) {
		return fmt.Errorf("validate certificate binding: invalid metadata")
	}
	return nil
}

// ValidateTask checks the durable task state-machine snapshot.
func ValidateTask(value Task) error {
	if parseOpaqueID(string(value.ID)) != nil || !value.Kind.Valid() || !value.State.Valid() || !value.Stage.Valid() ||
		!validOptionalID(string(value.PlanID)) || !validOptionalID(string(value.CertificateID)) ||
		!validOptionalID(string(value.VersionID)) || !validOptionalID(string(value.AccountID)) ||
		!validOptionalID(string(value.DNSCredentialID)) || !validChallenge(value.Challenge) ||
		value.CreatedBy <= 0 || !validRequestID(value.RequestID) || !validLifecycleTimes(value.CreatedAt, value.UpdatedAt) ||
		(value.State == TaskStateQueued && !value.StartedAt.IsZero()) ||
		(value.State == TaskStateRunning && value.StartedAt.IsZero()) ||
		(value.State.Terminal() && value.FinishedAt.IsZero()) ||
		(!value.State.Terminal() && !value.FinishedAt.IsZero()) ||
		(value.State == TaskStateSucceeded && value.LastErrorCode != "") ||
		((value.State == TaskStateFailed || value.State == TaskStateCancelled || value.State == TaskStateNeedsAttention) && value.LastErrorCode == "") {
		return fmt.Errorf("validate certificate task: invalid metadata")
	}
	return nil
}

// ValidateTaskStage checks one immutable safe progress event.
func ValidateTaskStage(value TaskStage) error {
	if parseOpaqueID(string(value.TaskID)) != nil || value.Sequence == 0 || value.Sequence > 512 ||
		!value.Stage.Valid() || !value.Result.Valid() || len(value.Code) > 128 ||
		!validJSONObject(value.PublicDetailsJSON) || value.OccurredAt.IsZero() {
		return fmt.Errorf("validate certificate task stage: invalid metadata")
	}
	return nil
}

// ValidateArtifact checks an exact, non-secret cleanup target.
func ValidateArtifact(value ChallengeArtifact) error {
	if parseOpaqueID(string(value.ID)) != nil || parseOpaqueID(string(value.TaskID)) != nil ||
		!value.Kind.Valid() || !value.State.Valid() || !validOptionalID(string(value.DNSCredentialID)) ||
		value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return fmt.Errorf("validate certificate challenge artifact: invalid metadata")
	}
	switch value.Kind {
	case ArtifactCloudflareTXT:
		if value.DNSCredentialID == "" || !validOpaqueID(value.ZoneID) || !validOpaqueID(value.RecordID) ||
			value.RecordName == "" || len(value.RecordName) > 255 || value.ConfigPath != "" {
			return fmt.Errorf("validate certificate challenge artifact: invalid DNS target")
		}
	case ArtifactHTTPInclude:
		if value.ConfigPath == "" || len(value.ConfigPath) > 4096 || value.ZoneID != "" || value.RecordID != "" ||
			value.RecordName != "" || value.DNSCredentialID != "" {
			return fmt.Errorf("validate certificate challenge artifact: invalid HTTP target")
		}
	}
	return nil
}

func validChallenge(value ChallengeType) bool {
	return value == ChallengeHTTP01 || value == ChallengeCloudflareDNS01
}

func validOptionalID(value string) bool { return value == "" || parseOpaqueID(value) == nil }

func validHTTPSURL(value string) bool {
	if len(value) == 0 || len(value) > 2048 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func validEmail(value string) bool {
	if len(value) == 0 || len(value) > 254 || strings.TrimSpace(value) != value {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func validDisplayName(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func validRequestID(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && !strings.ContainsAny(value, "\x00\r\n")
}

func validLifecycleTimes(createdAt, updatedAt time.Time) bool {
	return !createdAt.IsZero() && !updatedAt.IsZero() && !updatedAt.Before(createdAt)
}

func validLowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validJSONArray(value string) bool {
	if len(value) < 2 || len(value) > maxSafeJSONBytes || value[0] != '[' {
		return false
	}
	var target []json.RawMessage
	return json.Unmarshal([]byte(value), &target) == nil
}

func validJSONObject(value string) bool {
	if len(value) < 2 || len(value) > 64<<10 || value[0] != '{' {
		return false
	}
	var target map[string]json.RawMessage
	return json.Unmarshal([]byte(value), &target) == nil
}
