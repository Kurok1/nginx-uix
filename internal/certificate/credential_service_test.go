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

func TestCredentialServiceVerifiesCloudflareBeforeSeparatingSecretAndMetadata(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	provider := &credentialProviderStub{zones: []CloudflareZone{{
		ID: "11111111111111111111111111111111", Name: "example.com",
	}}}
	repository := &credentialRepositoryStub{}
	vault := &credentialVaultStub{}
	service, err := NewCredentialService(CredentialServiceOptions{
		Repository: repository, Vault: vault, Cloudflare: provider,
		Random: rand.Reader, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	const token = "cfut_secret-value"
	credential, err := service.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request-cf"}, CreateDNSCredentialInput{
		Name: "Production zones", APIToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !provider.verified || provider.token != token || vault.token != token || vault.id != credential.ID {
		t.Fatalf("provider/vault = %#v %#v", provider, vault)
	}
	if repository.created.ID != credential.ID || repository.created.Fingerprint != TokenFingerprint(token) ||
		repository.created.Name != "Production zones" || repository.created.VerifiedAt != now {
		t.Fatalf("safe metadata = %#v", repository.created)
	}
}

func TestCredentialServiceDoesNotPersistRejectedOrZonelessToken(t *testing.T) {
	tests := []struct {
		name     string
		provider *credentialProviderStub
		want     error
	}{
		{name: "rejected", provider: &credentialProviderStub{verifyErr: ErrCloudflareTokenInvalid}, want: ErrCloudflareTokenInvalid},
		{name: "no accessible zones", provider: &credentialProviderStub{}, want: ErrCloudflarePermission},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &credentialRepositoryStub{}
			vault := &credentialVaultStub{}
			service, err := NewCredentialService(CredentialServiceOptions{
				Repository: repository, Vault: vault, Cloudflare: test.provider,
				Random: rand.Reader, Now: time.Now,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request"}, CreateDNSCredentialInput{
				Name: "Zones", APIToken: "cfut_secret-value",
			})
			if !errors.Is(err, test.want) || vault.token != "" || repository.created.ID != "" {
				t.Fatalf("Create() = %v, vault=%#v repository=%#v", err, vault, repository)
			}
		})
	}
}

func TestCredentialServiceRemovesEnvelopeWhenMetadataCommitFails(t *testing.T) {
	provider := &credentialProviderStub{zones: []CloudflareZone{{
		ID: "11111111111111111111111111111111", Name: "example.com",
	}}}
	repository := &credentialRepositoryStub{createErr: errors.New("database unavailable")}
	vault := &credentialVaultStub{}
	service, err := NewCredentialService(CredentialServiceOptions{
		Repository: repository, Vault: vault, Cloudflare: provider,
		Random: rand.Reader, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), config.Actor{UserID: 7, RequestID: "request"}, CreateDNSCredentialInput{
		Name: "Zones", APIToken: "cfut_secret-value",
	})
	if err == nil || vault.deletedID == "" || vault.deletedID != vault.id {
		t.Fatalf("Create() error/deletion = %v, %#v", err, vault)
	}
}

func TestCredentialServiceDeletesMetadataBeforeExactCiphertext(t *testing.T) {
	now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	repository := &credentialRepositoryStub{created: DNSCredential{
		ID: "22222222222222222222222222222222", Name: "Zones", Provider: DNSProviderCloudflare,
		Fingerprint: "0123456789abcdef", Status: CredentialStatusValid, VerifiedAt: now.Add(-time.Hour),
		CreatedBy: 7, RequestID: "request-create", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}}
	vault := &credentialVaultStub{id: repository.created.ID, token: "cfut_secret-value"}
	service, err := NewCredentialService(CredentialServiceOptions{
		Repository: repository, Vault: vault, Cloudflare: &credentialProviderStub{},
		Random: rand.Reader, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := service.Delete(
		context.Background(), config.Actor{UserID: 7, RequestID: "request-delete"}, repository.created.ID,
	)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.Status != CredentialStatusDeleted || repository.deletedID != repository.created.ID ||
		vault.deletedID != repository.created.ID {
		t.Fatalf("deleted/repository/vault = %#v/%s/%s", deleted, repository.deletedID, vault.deletedID)
	}
}

type credentialProviderStub struct {
	zones     []CloudflareZone
	token     string
	verified  bool
	verifyErr error
}

func (provider *credentialProviderStub) VerifyToken(_ context.Context, token string) error {
	provider.token = token
	provider.verified = true
	return provider.verifyErr
}

func (provider *credentialProviderStub) ListZones(_ context.Context, token string) ([]CloudflareZone, error) {
	provider.token = token
	return provider.zones, nil
}

type credentialRepositoryStub struct {
	created   DNSCredential
	createErr error
	deletedID DNSCredentialID
}

func (repository *credentialRepositoryStub) CreateCertificateDNSCredential(_ context.Context, value DNSCredential) error {
	repository.created = value
	return repository.createErr
}

func (repository *credentialRepositoryStub) CertificateDNSCredential(context.Context, DNSCredentialID) (DNSCredential, error) {
	return repository.created, nil
}

func (repository *credentialRepositoryStub) CertificateDNSCredentials(context.Context) ([]DNSCredential, error) {
	return []DNSCredential{repository.created}, nil
}

func (repository *credentialRepositoryStub) DeleteCertificateDNSCredential(
	_ context.Context, id DNSCredentialID, _ int64, _ string, at time.Time,
) (DNSCredential, error) {
	repository.deletedID = id
	repository.created.Status = CredentialStatusDeleted
	repository.created.UpdatedAt = at
	return repository.created, nil
}

type credentialVaultStub struct {
	id        DNSCredentialID
	token     string
	deletedID DNSCredentialID
}

func (vault *credentialVaultStub) StoreCloudflareToken(_ context.Context, id, token string) error {
	vault.id = DNSCredentialID(id)
	vault.token = token
	return nil
}

func (vault *credentialVaultStub) LoadCloudflareToken(context.Context, string) (string, error) {
	return vault.token, nil
}

func (vault *credentialVaultStub) DeleteCloudflareToken(_ context.Context, id string) error {
	vault.deletedID = DNSCredentialID(id)
	return nil
}
