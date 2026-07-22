/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"golang.org/x/crypto/acme"
)

func TestAccountServiceRequiresExactTermsBeforeRemoteRegistration(t *testing.T) {
	client := &accountClientStub{directory: ACMEDirectory{
		URL: LetsEncryptStagingDirectory, TermsURL: "https://example.invalid/terms",
	}}
	service := newAccountTestService(t, client, &accountRepositoryStub{}, &accountKeyVaultStub{})
	_, err := service.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request-1"}, CreateAccountInput{
		Environment: EnvironmentStaging, Email: "operator@example.com",
	})
	if !errors.Is(err, ErrACMETermsRequired) {
		t.Fatalf("Create() error = %v, want ErrACMETermsRequired", err)
	}
	if client.registered {
		t.Fatal("remote registration occurred before terms agreement")
	}
}

func TestAccountServiceCreatesSeparateKeyAndSafeMetadata(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	client := &accountClientStub{
		directory: ACMEDirectory{URL: LetsEncryptProductionDirectory, TermsURL: "https://example.invalid/terms"},
		remote: RemoteAccount{
			URI: "https://acme-v02.api.letsencrypt.org/acme/acct/42", Status: AccountStatusValid,
		},
	}
	repository := &accountRepositoryStub{}
	vault := &accountKeyVaultStub{}
	service := newAccountTestService(t, client, repository, vault)
	service.now = func() time.Time { return now }
	account, err := service.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request-2"}, CreateAccountInput{
		Environment: EnvironmentProduction, Email: "operator@example.com", TermsOfServiceAgreed: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !client.registered || client.email != "operator@example.com" || client.terms != client.directory.TermsURL {
		t.Fatalf("remote registration = %#v", client)
	}
	if vault.storedID != account.ID || vault.storedKey == nil || repository.created.ID != account.ID {
		t.Fatalf("vault/repository did not receive one account: %#v %#v", vault, repository)
	}
	if account.Environment != EnvironmentProduction || account.Status != AccountStatusValid ||
		account.TermsAgreedBy != 7 || !account.TermsAgreedAt.Equal(now) {
		t.Fatalf("account = %#v", account)
	}
}

func TestAccountServiceDeletesLocalKeyWhenMetadataCommitFails(t *testing.T) {
	client := &accountClientStub{
		directory: ACMEDirectory{URL: LetsEncryptStagingDirectory, TermsURL: "https://example.invalid/terms"},
		remote: RemoteAccount{
			URI: "https://acme-staging-v02.api.letsencrypt.org/acme/acct/42", Status: AccountStatusValid,
		},
	}
	repository := &accountRepositoryStub{createErr: errors.New("database unavailable")}
	vault := &accountKeyVaultStub{}
	service := newAccountTestService(t, client, repository, vault)
	_, err := service.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request-3"}, CreateAccountInput{
		Environment: EnvironmentStaging, Email: "operator@example.com", TermsOfServiceAgreed: true,
	})
	if err == nil || vault.deletedID == "" || vault.deletedID != vault.storedID {
		t.Fatalf("Create() error/deletion = %v, %#v", err, vault)
	}
}

func TestAccountServiceDeactivatesRemoteBeforeMetadata(t *testing.T) {
	now := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	account := Account{
		ID: "11111111111111111111111111111111", Environment: EnvironmentStaging,
		DirectoryURL: LetsEncryptStagingDirectory,
		URI:          "https://acme-staging-v02.api.letsencrypt.org/acme/acct/42",
		Email:        "operator@example.com", Status: AccountStatusValid,
		TermsURL: "https://example.invalid/terms", TermsAgreedAt: now.Add(-time.Hour),
		TermsAgreedBy: 7, CreatedBy: 7, RequestID: "created", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	client := &accountClientStub{directory: ACMEDirectory{URL: account.DirectoryURL, TermsURL: account.TermsURL}}
	repository := &accountRepositoryStub{account: account}
	vault := &accountKeyVaultStub{storedKey: mustAccountTestKey(t)}
	service := newAccountTestService(t, client, repository, vault)
	service.now = func() time.Time { return now }
	updated, err := service.Deactivate(
		context.Background(), config.Actor{UserID: 7, RequestID: "request-deactivate"}, account.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !client.deactivated || repository.beganDeactivationID != account.ID ||
		repository.completedDeactivationID != account.ID || updated.Status != AccountStatusDeactivated {
		t.Fatalf("deactivation = client:%v repository:%#v account:%#v", client.deactivated, repository, updated)
	}
}

func TestAccountServiceReservesDeactivationBeforeCallingRemote(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	account := testAccountServiceAccount(now)
	client := &accountClientStub{}
	repository := &accountRepositoryStub{account: account, beginErr: ErrTaskActive}
	service := newAccountTestService(t, client, repository, &accountKeyVaultStub{storedKey: mustAccountTestKey(t)})
	_, err := service.Deactivate(
		context.Background(), config.Actor{UserID: 7, RequestID: "request-active"}, account.ID,
	)
	if !errors.Is(err, ErrTaskActive) || client.deactivated {
		t.Fatalf("Deactivate(active task) error=%v remote_called=%v", err, client.deactivated)
	}
}

func TestAccountServiceLeavesFailedRemoteDeactivationReserved(t *testing.T) {
	now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	account := testAccountServiceAccount(now)
	client := &accountClientStub{deactivateErr: errors.New("remote unavailable")}
	repository := &accountRepositoryStub{account: account}
	service := newAccountTestService(t, client, repository, &accountKeyVaultStub{storedKey: mustAccountTestKey(t)})
	service.now = func() time.Time { return now }
	_, err := service.Deactivate(
		context.Background(), config.Actor{UserID: 7, RequestID: "request-remote-failure"}, account.ID,
	)
	if !errors.Is(err, ErrACMEUnavailable) || repository.account.Status != AccountStatusDeactivating ||
		repository.completedDeactivationID != "" {
		t.Fatalf("Deactivate(remote failure) error=%v repository=%#v", err, repository)
	}
}

func TestAccountServiceRejectsImportedAccountURIOutsideFixedDirectoryOrigin(t *testing.T) {
	key := mustAccountTestKey(t)
	keyPEM, err := MarshalPrivateKeyPEM(key)
	if err != nil {
		t.Fatal(err)
	}
	client := &accountClientStub{directory: ACMEDirectory{
		URL: LetsEncryptStagingDirectory, TermsURL: "https://letsencrypt.org/repository/",
	}}
	service := newAccountTestService(t, client, &accountRepositoryStub{}, &accountKeyVaultStub{})
	_, err = service.Import(context.Background(), config.Actor{UserID: 7, RequestID: "request-import"}, ImportAccountInput{
		Environment: EnvironmentStaging, Email: "operator@example.com",
		AccountURI: "https://169.254.169.254/latest/meta-data", PrivateKeyPEM: keyPEM,
		TermsOfServiceAgreed: true,
	})
	if !errors.Is(err, ErrACMEAccountInvalid) || client.registrationRead {
		t.Fatalf("Import(arbitrary origin) error=%v registration_read=%v", err, client.registrationRead)
	}
}

func TestExternalACMEErrorClassifiesRetryAfterWithoutLeakingProblemDetails(t *testing.T) {
	original := &acme.Error{
		StatusCode:  429,
		ProblemType: "urn:ietf:params:acme:error:rateLimited",
		Detail:      "account and domain specific provider detail",
		Header:      http.Header{"Retry-After": []string{"7200"}},
	}
	err := externalACMEError(context.Background(), "create ACME order", original)
	var limited *ACMERateLimitError
	if !errors.Is(err, ErrACMERateLimited) || !errors.As(err, &limited) || limited.RetryAfter != 2*time.Hour {
		t.Fatalf("externalACMEError()=%#v, want two-hour rate limit", err)
	}
	if strings.Contains(err.Error(), original.Detail) {
		t.Fatalf("external error leaked ACME detail: %q", err)
	}
}

func TestACMEAccountStatusErrorDistinguishesDeactivatedAndUncertainStates(t *testing.T) {
	tests := []struct {
		name   string
		status AccountStatus
		want   error
	}{
		{name: "valid", status: AccountStatusValid},
		{name: "deactivated", status: AccountStatusDeactivated, want: ErrACMEAccountDeactivated},
		{name: "deactivating", status: AccountStatusDeactivating, want: ErrACMEAccountNeedsAttention},
		{name: "unknown", status: AccountStatus("unknown"), want: ErrACMEAccountInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := acmeAccountStatusError(test.status)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("acmeAccountStatusError(%q) = %v, want %v", test.status, err, test.want)
			}
		})
	}
}

func newAccountTestService(
	t *testing.T,
	client *accountClientStub,
	repository *accountRepositoryStub,
	vault *accountKeyVaultStub,
) *AccountService {
	t.Helper()
	service, err := NewAccountService(AccountServiceOptions{
		Repository: repository, Vault: vault, Factory: accountClientFactoryStub{client: client},
		Random: rand.Reader, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type accountClientFactoryStub struct{ client *accountClientStub }

func (factory accountClientFactoryStub) NewAccountClient(string, crypto.Signer, string) (ACMEAccountClient, error) {
	return factory.client, nil
}

type accountClientStub struct {
	directory        ACMEDirectory
	remote           RemoteAccount
	registered       bool
	deactivated      bool
	email            string
	terms            string
	registrationRead bool
	deactivateErr    error
}

func (client *accountClientStub) Discover(context.Context) (ACMEDirectory, error) {
	return client.directory, nil
}

func (client *accountClientStub) Register(_ context.Context, email, terms string) (RemoteAccount, error) {
	client.registered = true
	client.email = email
	client.terms = terms
	return client.remote, nil
}

func (client *accountClientStub) GetRegistration(context.Context, string) (RemoteAccount, error) {
	client.registrationRead = true
	return client.remote, nil
}

func (client *accountClientStub) Deactivate(context.Context) error {
	client.deactivated = true
	return client.deactivateErr
}

type accountRepositoryStub struct {
	account                 Account
	created                 Account
	createErr               error
	beginErr                error
	beganDeactivationID     AccountID
	completedDeactivationID AccountID
}

func (repository *accountRepositoryStub) CreateCertificateAccount(_ context.Context, account Account) error {
	repository.created = account
	return repository.createErr
}

func (repository *accountRepositoryStub) CertificateAccount(context.Context, AccountID) (Account, error) {
	return repository.account, nil
}

func (repository *accountRepositoryStub) CertificateAccounts(context.Context) ([]Account, error) {
	return []Account{repository.account}, nil
}

func (repository *accountRepositoryStub) BeginCertificateAccountDeactivation(
	_ context.Context,
	id AccountID,
	_ int64,
	_ string,
	_ time.Time,
) (Account, error) {
	if repository.beginErr != nil {
		return Account{}, repository.beginErr
	}
	repository.beganDeactivationID = id
	if repository.account.Status == AccountStatusValid {
		repository.account.Status = AccountStatusDeactivating
	}
	return repository.account, nil
}

func (repository *accountRepositoryStub) CompleteCertificateAccountDeactivation(
	_ context.Context,
	id AccountID,
	_ int64,
	_ string,
	_ time.Time,
) (Account, error) {
	repository.completedDeactivationID = id
	repository.account.Status = AccountStatusDeactivated
	return repository.account, nil
}

func testAccountServiceAccount(at time.Time) Account {
	return Account{
		ID: "11111111111111111111111111111111", Environment: EnvironmentStaging,
		DirectoryURL: LetsEncryptStagingDirectory,
		URI:          "https://acme-staging-v02.api.letsencrypt.org/acme/acct/42",
		Email:        "operator@example.com", Status: AccountStatusValid,
		TermsURL: "https://example.invalid/terms", TermsAgreedAt: at.Add(-time.Hour),
		TermsAgreedBy: 7, CreatedBy: 7, RequestID: "created", CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Hour),
	}
}

type accountKeyVaultStub struct {
	storedID  AccountID
	storedKey crypto.Signer
	deletedID AccountID
}

func (vault *accountKeyVaultStub) StoreAccountKey(_ context.Context, id AccountID, key crypto.Signer) error {
	vault.storedID = id
	vault.storedKey = key
	return nil
}

func (vault *accountKeyVaultStub) LoadAccountKey(context.Context, AccountID) (crypto.Signer, error) {
	return vault.storedKey, nil
}

func (vault *accountKeyVaultStub) DeleteAccountKey(_ context.Context, id AccountID) error {
	vault.deletedID = id
	return nil
}

func mustAccountTestKey(t *testing.T) crypto.Signer {
	t.Helper()
	key, err := generateAccountKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
