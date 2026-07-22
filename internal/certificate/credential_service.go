/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

// CloudflareCredentialVerifier exposes only pre-storage Token checks.
type CloudflareCredentialVerifier interface {
	VerifyToken(context.Context, string) error
	ListZones(context.Context, string) ([]CloudflareZone, error)
}

// DNSCredentialRepository owns only secret-free credential metadata.
type DNSCredentialRepository interface {
	CreateCertificateDNSCredential(context.Context, DNSCredential) error
	CertificateDNSCredential(context.Context, DNSCredentialID) (DNSCredential, error)
	CertificateDNSCredentials(context.Context) ([]DNSCredential, error)
	DeleteCertificateDNSCredential(context.Context, DNSCredentialID, int64, string, time.Time) (DNSCredential, error)
}

// CredentialTokenVault owns encrypted Cloudflare Token envelopes.
type CredentialTokenVault interface {
	StoreCloudflareToken(context.Context, string, string) error
	LoadCloudflareToken(context.Context, string) (string, error)
	DeleteCloudflareToken(context.Context, string) error
}

// CredentialServiceOptions are the exact Cloudflare credential dependencies.
type CredentialServiceOptions struct {
	Repository DNSCredentialRepository
	Vault      CredentialTokenVault
	Cloudflare CloudflareCredentialVerifier
	Random     io.Reader
	Now        func() time.Time
}

// CredentialService validates Tokens before separating secret and safe metadata.
type CredentialService struct {
	repository DNSCredentialRepository
	vault      CredentialTokenVault
	cloudflare CloudflareCredentialVerifier
	random     io.Reader
	now        func() time.Time
}

// CreateDNSCredentialInput is the only request shape that carries a Cloudflare Token.
type CreateDNSCredentialInput struct {
	Name     string `json:"name"`
	APIToken string `json:"api_token"`
}

// NewCredentialService creates a fixed-provider credential coordinator.
func NewCredentialService(options CredentialServiceOptions) (*CredentialService, error) {
	if options.Repository == nil || options.Vault == nil || options.Cloudflare == nil || options.Random == nil {
		return nil, fmt.Errorf("create certificate credential service: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &CredentialService{
		repository: options.Repository, vault: options.Vault, cloudflare: options.Cloudflare,
		random: options.Random, now: options.Now,
	}, nil
}

// Create verifies Token status and Zone Read before writing any local state.
func (service *CredentialService) Create(
	ctx context.Context,
	actor config.Actor,
	input CreateDNSCredentialInput,
) (DNSCredential, error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		!validDisplayName(input.Name) || !validCloudflareTokenSecret(input.APIToken) {
		return DNSCredential{}, fmt.Errorf("create Cloudflare credential: %w", ErrCloudflareTokenInvalid)
	}
	if err := service.cloudflare.VerifyToken(ctx, input.APIToken); err != nil {
		return DNSCredential{}, fmt.Errorf("create Cloudflare credential: %w", err)
	}
	zones, err := service.cloudflare.ListZones(ctx, input.APIToken)
	if err != nil {
		return DNSCredential{}, fmt.Errorf("create Cloudflare credential: %w", err)
	}
	accessible := false
	for _, zone := range zones {
		canonical, _, normalizeErr := normalizeDNSIdentifier(zone.Name)
		if normalizeErr == nil && canonical == zone.Name && validCloudflareID(zone.ID) {
			accessible = true
			break
		}
	}
	if !accessible {
		return DNSCredential{}, fmt.Errorf("create Cloudflare credential: %w", ErrCloudflarePermission)
	}
	id, err := NewDNSCredentialID(service.random)
	if err != nil {
		return DNSCredential{}, fmt.Errorf("create Cloudflare credential: %w", ErrCloudflareUnavailable)
	}
	now := service.now().UTC()
	credential := DNSCredential{
		ID: id, Name: input.Name, Provider: DNSProviderCloudflare,
		Fingerprint: TokenFingerprint(input.APIToken), Status: CredentialStatusValid,
		VerifiedAt: now, CreatedBy: actor.UserID, RequestID: actor.RequestID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := service.vault.StoreCloudflareToken(ctx, string(id), input.APIToken); err != nil {
		return DNSCredential{}, fmt.Errorf("store Cloudflare credential: %w", err)
	}
	if err := service.repository.CreateCertificateDNSCredential(ctx, credential); err != nil {
		cleanupContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
		cleanupErr := service.vault.DeleteCloudflareToken(cleanupContext, string(id))
		cancel()
		return DNSCredential{}, errors.Join(fmt.Errorf("commit Cloudflare credential metadata: %w", err), cleanupErr)
	}
	return credential, nil
}

// Credentials returns the bounded secret-free credential inventory.
func (service *CredentialService) Credentials(ctx context.Context) ([]DNSCredential, error) {
	if ctx == nil || service == nil {
		return nil, fmt.Errorf("list Cloudflare credentials: %w", ErrCloudflareUnavailable)
	}
	credentials, err := service.repository.CertificateDNSCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Cloudflare credentials: %w", err)
	}
	return credentials, nil
}

// Credential returns one secret-free credential.
func (service *CredentialService) Credential(ctx context.Context, id DNSCredentialID) (DNSCredential, error) {
	if ctx == nil || service == nil || parseOpaqueID(string(id)) != nil {
		return DNSCredential{}, fmt.Errorf("read Cloudflare credential: %w", ErrCloudflareUnavailable)
	}
	credential, err := service.repository.CertificateDNSCredential(ctx, id)
	if err != nil {
		return DNSCredential{}, fmt.Errorf("read Cloudflare credential: %w", err)
	}
	return credential, nil
}

// Delete makes credential metadata unusable after repository reference checks, then removes its exact envelope.
func (service *CredentialService) Delete(
	ctx context.Context,
	actor config.Actor,
	id DNSCredentialID,
) (DNSCredential, error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		parseOpaqueID(string(id)) != nil {
		return DNSCredential{}, fmt.Errorf("delete Cloudflare credential: %w", ErrCloudflareTokenInvalid)
	}
	deleted, err := service.repository.DeleteCertificateDNSCredential(
		ctx, id, actor.UserID, actor.RequestID, service.now().UTC(),
	)
	if err != nil {
		return DNSCredential{}, fmt.Errorf("delete Cloudflare credential: %w", err)
	}
	if deleted.Status != CredentialStatusDeleted {
		return DNSCredential{}, fmt.Errorf("delete Cloudflare credential: %w", ErrCloudflareUnavailable)
	}
	cleanupContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
	cleanupErr := service.vault.DeleteCloudflareToken(cleanupContext, string(id))
	cancel()
	if cleanupErr != nil {
		return deleted, fmt.Errorf("delete Cloudflare credential envelope: %w", ErrSecretInvalid)
	}
	return deleted, nil
}
