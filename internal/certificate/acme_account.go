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
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"golang.org/x/crypto/acme"
)

const (
	// LetsEncryptStagingDirectory is the fixed safe preflight ACME directory.
	LetsEncryptStagingDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"
	// LetsEncryptProductionDirectory is the fixed production ACME directory.
	LetsEncryptProductionDirectory = "https://acme-v02.api.letsencrypt.org/directory"
)

var (
	// ErrACMETermsRequired indicates missing explicit agreement to the discovered terms URL.
	ErrACMETermsRequired = errors.New("ACME terms required")
	// ErrACMEAccountInvalid indicates an invalid, revoked or mismatched remote account.
	ErrACMEAccountInvalid = errors.New("ACME account invalid")
	// ErrACMEAccountDeactivated indicates an account that cannot accept new orders or renewals.
	ErrACMEAccountDeactivated = errors.New("ACME account deactivated")
	// ErrACMEAccountNeedsAttention indicates a fail-closed deactivation that requires retry or repair.
	ErrACMEAccountNeedsAttention = errors.New("ACME account needs attention")
	// ErrACMEUnavailable indicates a bounded ACME operation that could not be completed.
	ErrACMEUnavailable = errors.New("ACME unavailable")
	// ErrACMERateLimited indicates that the ACME server refused work until a later time.
	ErrACMERateLimited = errors.New("ACME rate limited")
)

func acmeAccountStatusError(status AccountStatus) error {
	switch status {
	case AccountStatusValid:
		return nil
	case AccountStatusDeactivated:
		return ErrACMEAccountDeactivated
	case AccountStatusDeactivating:
		return ErrACMEAccountNeedsAttention
	default:
		return ErrACMEAccountInvalid
	}
}

const maximumACMERetryAfter = 30 * 24 * time.Hour

// ACMERateLimitError carries only a bounded retry duration, never remote problem details.
type ACMERateLimitError struct {
	RetryAfter time.Duration
}

func (value *ACMERateLimitError) Error() string {
	return ErrACMERateLimited.Error()
}

// Unwrap supports errors.Is without exposing the ACME response body.
func (value *ACMERateLimitError) Unwrap() error {
	return ErrACMERateLimited
}

// ACMEDirectory contains only safe account-registration discovery data.
type ACMEDirectory struct {
	Environment             Environment `json:"environment"`
	URL                     string      `json:"directory_url"`
	TermsURL                string      `json:"terms_url"`
	Website                 string      `json:"website,omitempty"`
	ExternalAccountRequired bool        `json:"external_account_required"`
}

// RemoteAccount is the minimal safe registration response required by the domain.
type RemoteAccount struct {
	URI    string
	Status AccountStatus
}

// ACMEAccountClient is the narrow registration capability consumed by AccountService.
type ACMEAccountClient interface {
	Discover(context.Context) (ACMEDirectory, error)
	Register(context.Context, string, string) (RemoteAccount, error)
	GetRegistration(context.Context, string) (RemoteAccount, error)
	Deactivate(context.Context) error
}

// ACMEAccountClientFactory binds one account key and optional account URI to a directory.
type ACMEAccountClientFactory interface {
	NewAccountClient(string, crypto.Signer, string) (ACMEAccountClient, error)
}

// AccountRepository owns safe account metadata and deactivation transactions.
type AccountRepository interface {
	CreateCertificateAccount(context.Context, Account) error
	CertificateAccount(context.Context, AccountID) (Account, error)
	CertificateAccounts(context.Context) ([]Account, error)
	BeginCertificateAccountDeactivation(context.Context, AccountID, int64, string, time.Time) (Account, error)
	CompleteCertificateAccountDeactivation(context.Context, AccountID, int64, string, time.Time) (Account, error)
}

// AccountKeyVault owns account private keys outside SQLite.
type AccountKeyVault interface {
	StoreAccountKey(context.Context, AccountID, crypto.Signer) error
	LoadAccountKey(context.Context, AccountID) (crypto.Signer, error)
	DeleteAccountKey(context.Context, AccountID) error
}

// AccountServiceOptions are the explicit account lifecycle dependencies.
type AccountServiceOptions struct {
	Repository AccountRepository
	Vault      AccountKeyVault
	Factory    ACMEAccountClientFactory
	Random     io.Reader
	Now        func() time.Time
}

// AccountService coordinates fixed-directory remote registration and local durability.
type AccountService struct {
	repository AccountRepository
	vault      AccountKeyVault
	factory    ACMEAccountClientFactory
	random     io.Reader
	now        func() time.Time
}

// CreateAccountInput is the strict account-registration request.
type CreateAccountInput struct {
	Environment          Environment `json:"environment"`
	Email                string      `json:"email"`
	TermsOfServiceAgreed bool        `json:"terms_of_service_agreed"`
}

// ImportAccountInput binds an existing supported key to a verified remote account URI.
type ImportAccountInput struct {
	Environment          Environment
	Email                string
	AccountURI           string
	PrivateKeyPEM        []byte
	TermsOfServiceAgreed bool
}

// NewAccountService creates an explicit account coordinator.
func NewAccountService(options AccountServiceOptions) (*AccountService, error) {
	if options.Repository == nil || options.Vault == nil || options.Factory == nil || options.Random == nil {
		return nil, fmt.Errorf("create ACME account service: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &AccountService{
		repository: options.Repository, vault: options.Vault, factory: options.Factory,
		random: options.Random, now: options.Now,
	}, nil
}

// Directories discovers both fixed environments without accepting a caller URL.
func (service *AccountService) Directories(ctx context.Context) ([]ACMEDirectory, error) {
	if ctx == nil || service == nil {
		return nil, fmt.Errorf("discover ACME directories: %w", ErrACMEUnavailable)
	}
	result := make([]ACMEDirectory, 0, 2)
	for _, environment := range []Environment{EnvironmentStaging, EnvironmentProduction} {
		key, err := generateAccountKey(service.random)
		if err != nil {
			return nil, fmt.Errorf("discover ACME directories: %w", ErrACMEUnavailable)
		}
		directory, err := service.discover(ctx, environment, key)
		if err != nil {
			return nil, err
		}
		result = append(result, directory)
	}
	return result, nil
}

// Create registers one environment-specific key, then commits local key and metadata.
func (service *AccountService) Create(
	ctx context.Context,
	actor config.Actor,
	input CreateAccountInput,
) (Account, error) {
	if err := validateAccountServiceRequest(ctx, service, actor, input.Environment, input.Email); err != nil {
		return Account{}, err
	}
	key, err := generateAccountKey(service.random)
	if err != nil {
		return Account{}, fmt.Errorf("create ACME account: %w", ErrACMEUnavailable)
	}
	directory, err := service.discover(ctx, input.Environment, key)
	if err != nil {
		return Account{}, err
	}
	if !input.TermsOfServiceAgreed {
		return Account{}, fmt.Errorf("create ACME account: %w", ErrACMETermsRequired)
	}
	client, err := service.factory.NewAccountClient(directory.URL, key, "")
	if err != nil {
		return Account{}, fmt.Errorf("create ACME account: %w", ErrACMEUnavailable)
	}
	remote, err := client.Register(ctx, input.Email, directory.TermsURL)
	if err != nil {
		return Account{}, externalACMEError(ctx, "create ACME account", err)
	}
	return service.commit(ctx, actor, input.Environment, input.Email, directory, remote, key)
}

// Import verifies an existing key and account URI before committing it locally.
func (service *AccountService) Import(
	ctx context.Context,
	actor config.Actor,
	input ImportAccountInput,
) (Account, error) {
	if err := validateAccountServiceRequest(ctx, service, actor, input.Environment, input.Email); err != nil ||
		!validHTTPSURL(input.AccountURI) {
		return Account{}, fmt.Errorf("import ACME account: %w", ErrACMEAccountInvalid)
	}
	key, err := ParsePrivateKeyPEM(input.PrivateKeyPEM)
	if err != nil {
		return Account{}, fmt.Errorf("import ACME account: %w", ErrACMEAccountInvalid)
	}
	directory, err := service.discover(ctx, input.Environment, key)
	if err != nil {
		return Account{}, err
	}
	if !input.TermsOfServiceAgreed {
		return Account{}, fmt.Errorf("import ACME account: %w", ErrACMETermsRequired)
	}
	if !validACMEAccountURI(directory.URL, input.AccountURI) {
		return Account{}, fmt.Errorf("import ACME account: %w", ErrACMEAccountInvalid)
	}
	client, err := service.factory.NewAccountClient(directory.URL, key, input.AccountURI)
	if err != nil {
		return Account{}, fmt.Errorf("import ACME account: %w", ErrACMEUnavailable)
	}
	remote, err := client.GetRegistration(ctx, input.AccountURI)
	if err != nil {
		return Account{}, externalACMEError(ctx, "import ACME account", err)
	}
	if remote.URI != input.AccountURI {
		return Account{}, fmt.Errorf("import ACME account: %w", ErrACMEAccountInvalid)
	}
	return service.commit(ctx, actor, input.Environment, input.Email, directory, remote, key)
}

// Accounts returns the bounded secret-free account inventory.
func (service *AccountService) Accounts(ctx context.Context) ([]Account, error) {
	if ctx == nil || service == nil {
		return nil, fmt.Errorf("list ACME accounts: %w", ErrACMEUnavailable)
	}
	accounts, err := service.repository.CertificateAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list ACME accounts: %w", err)
	}
	return accounts, nil
}

// Deactivate changes remote status before atomically changing safe local metadata.
func (service *AccountService) Deactivate(
	ctx context.Context,
	actor config.Actor,
	id AccountID,
) (Account, error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		parseOpaqueID(string(id)) != nil {
		return Account{}, fmt.Errorf("deactivate ACME account: %w", ErrACMEAccountInvalid)
	}
	now := service.now().UTC()
	account, err := service.repository.BeginCertificateAccountDeactivation(
		ctx, id, actor.UserID, actor.RequestID, now,
	)
	if err != nil {
		return Account{}, fmt.Errorf("reserve ACME account deactivation: %w", err)
	}
	if account.Status == AccountStatusDeactivated {
		return account, nil
	}
	if account.Status != AccountStatusDeactivating {
		return Account{}, fmt.Errorf("reserve ACME account deactivation: %w", ErrACMEAccountNeedsAttention)
	}
	key, err := service.vault.LoadAccountKey(ctx, id)
	if err != nil {
		return Account{}, fmt.Errorf("deactivate ACME account: %w", ErrACMEAccountNeedsAttention)
	}
	client, err := service.factory.NewAccountClient(account.DirectoryURL, key, account.URI)
	if err != nil {
		return Account{}, fmt.Errorf("deactivate ACME account: %w", ErrACMEUnavailable)
	}
	if err := client.Deactivate(ctx); err != nil {
		return Account{}, externalACMEError(ctx, "deactivate ACME account", err)
	}
	commitContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
	updated, err := service.repository.CompleteCertificateAccountDeactivation(
		commitContext, id, actor.UserID, actor.RequestID, service.now().UTC(),
	)
	cancel()
	if err != nil {
		return Account{}, fmt.Errorf("complete ACME account deactivation: %w",
			errors.Join(ErrACMEAccountNeedsAttention, err))
	}
	return updated, nil
}

func (service *AccountService) discover(
	ctx context.Context,
	environment Environment,
	key crypto.Signer,
) (ACMEDirectory, error) {
	directoryURL, err := directoryURL(environment)
	if err != nil {
		return ACMEDirectory{}, err
	}
	client, err := service.factory.NewAccountClient(directoryURL, key, "")
	if err != nil {
		return ACMEDirectory{}, fmt.Errorf("discover ACME directory: %w", ErrACMEUnavailable)
	}
	directory, err := client.Discover(ctx)
	if err != nil {
		return ACMEDirectory{}, externalACMEError(ctx, "discover ACME directory", err)
	}
	if directory.URL != directoryURL || !validHTTPSURL(directory.TermsURL) || directory.ExternalAccountRequired {
		return ACMEDirectory{}, fmt.Errorf("discover ACME directory: %w", ErrACMEAccountInvalid)
	}
	directory.Environment = environment
	return directory, nil
}

func (service *AccountService) commit(
	ctx context.Context,
	actor config.Actor,
	environment Environment,
	email string,
	directory ACMEDirectory,
	remote RemoteAccount,
	key crypto.Signer,
) (Account, error) {
	if remote.Status != AccountStatusValid || !validACMEAccountURI(directory.URL, remote.URI) {
		return Account{}, fmt.Errorf("commit ACME account: %w", ErrACMEAccountInvalid)
	}
	id, err := NewAccountID(service.random)
	if err != nil {
		return Account{}, fmt.Errorf("commit ACME account: %w", ErrACMEUnavailable)
	}
	now := service.now().UTC()
	account := Account{
		ID: id, Environment: environment, DirectoryURL: directory.URL, URI: remote.URI,
		Email: email, Status: AccountStatusValid, TermsURL: directory.TermsURL,
		TermsAgreedAt: now, TermsAgreedBy: actor.UserID, CreatedBy: actor.UserID,
		RequestID: actor.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.vault.StoreAccountKey(ctx, id, key); err != nil {
		return Account{}, fmt.Errorf("commit ACME account key: %w", err)
	}
	if err := service.repository.CreateCertificateAccount(ctx, account); err != nil {
		cleanupContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
		cleanupErr := service.vault.DeleteAccountKey(cleanupContext, id)
		cancel()
		return Account{}, errors.Join(fmt.Errorf("commit ACME account metadata: %w", err), cleanupErr)
	}
	return account, nil
}

func validACMEAccountURI(directoryURL, accountURI string) bool {
	directory, directoryErr := url.Parse(directoryURL)
	account, accountErr := url.Parse(accountURI)
	if directoryErr != nil || accountErr != nil || directory.Scheme != "https" || account.Scheme != "https" ||
		directory.Host == "" || account.Host != directory.Host || account.User != nil || account.RawQuery != "" ||
		account.Fragment != "" || account.Path == "" || account.Path == "/" {
		return false
	}
	return true
}

func validateAccountServiceRequest(
	ctx context.Context,
	service *AccountService,
	actor config.Actor,
	environment Environment,
	email string,
) error {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		!environment.Valid() || !validEmail(email) {
		return fmt.Errorf("validate ACME account request: %w", ErrACMEAccountInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func directoryURL(environment Environment) (string, error) {
	switch environment {
	case EnvironmentStaging:
		return LetsEncryptStagingDirectory, nil
	case EnvironmentProduction:
		return LetsEncryptProductionDirectory, nil
	default:
		return "", fmt.Errorf("select ACME directory: %w", ErrACMEAccountInvalid)
	}
}

func generateAccountKey(random io.Reader) (crypto.Signer, error) {
	if random == nil {
		return nil, ErrSecretInvalid
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, fmt.Errorf("generate ACME account key: %w", err)
	}
	return key, nil
}

func externalACMEError(ctx context.Context, action string, cause error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if retryAfter, limited := acmeRateLimit(cause); limited {
		if retryAfter < 0 {
			retryAfter = 0
		}
		if retryAfter > maximumACMERetryAfter {
			retryAfter = maximumACMERetryAfter
		}
		return &ACMERateLimitError{RetryAfter: retryAfter}
	}
	return fmt.Errorf("%s: %w", action, ErrACMEUnavailable)
}

func acmeRateLimit(cause error) (time.Duration, bool) {
	if cause == nil {
		return 0, false
	}
	var problem *acme.Error
	if errors.As(cause, &problem) {
		return acme.RateLimit(problem)
	}
	var order *acme.OrderError
	if errors.As(cause, &order) && order.Problem != nil {
		if retryAfter, limited := acme.RateLimit(order.Problem); limited {
			return retryAfter, true
		}
	}
	var authorization *acme.AuthorizationError
	if errors.As(cause, &authorization) {
		for _, authorizationError := range authorization.Errors {
			if retryAfter, limited := acmeRateLimit(authorizationError); limited {
				return retryAfter, true
			}
		}
	}
	return 0, false
}
