/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/nginxast"
)

const (
	// PrivateKeyExportConfirmation is the required second phrase for an explicitly requested private key.
	PrivateKeyExportConfirmation = "EXPORT PRIVATE KEY"
	maximumCertificateExportSize = 4 << 20
	maximumMaterialInventory     = 1000
)

var (
	// ErrCertificateReferenced indicates that current metadata or production config still owns material.
	ErrCertificateReferenced = errors.New("certificate is referenced")
	// ErrPrivateKeyExportConfirmationRequired indicates that the second private-key warning was not confirmed.
	ErrPrivateKeyExportConfirmationRequired = errors.New("private key export confirmation required")
	// ErrRenewalPolicyInvalid indicates an unsafe schedule or mismatched exact confirmation.
	ErrRenewalPolicyInvalid = errors.New("certificate renewal policy invalid")
)

// LifecycleRepository owns exact certificate lifecycle transactions and safe audit evidence.
type LifecycleRepository interface {
	Certificate(context.Context, CertificateID) (Certificate, error)
	CertificateVersions(context.Context, CertificateID) ([]Version, error)
	CertificateBindings(context.Context, CertificateID) ([]Binding, error)
	CompleteCertificateUnbinding(context.Context, CertificateID, config.Actor, string, time.Time) (Certificate, error)
	RecordCertificateExport(context.Context, CertificateID, config.Actor, bool, time.Time) error
	UpdateCertificateRenewalPolicy(
		context.Context, CertificateID, config.Actor, bool, int64, time.Time, time.Time,
	) (Certificate, error)
	CertificateMaterialInventory(context.Context, int) ([]CertificateMaterialRecord, error)
	MarkCertificateMaterialNeedsAttention(context.Context, CertificateID, VersionID, string, time.Time) error
	DeleteCertificate(context.Context, CertificateID, config.Actor, time.Time) (Certificate, error)
}

// LifecycleVault exposes only exact active-material reads and certificate-directory deletion.
type LifecycleVault interface {
	LoadCertificateVersion(context.Context, CertificateID, VersionID) (StoredCertificateMaterial, error)
	DeleteCertificate(context.Context, CertificateID) error
}

// LifecycleServiceOptions are the exact certificate lifecycle dependencies.
type LifecycleServiceOptions struct {
	Repository LifecycleRepository
	Vault      LifecycleVault
	Publisher  ConfigurationPublisher
	Random     io.Reader
	Now        func() time.Time
}

// LifecycleService coordinates export, exact unbinding and reference-safe local deletion.
type LifecycleService struct {
	repository LifecycleRepository
	vault      LifecycleVault
	publisher  ConfigurationPublisher
	random     io.Reader
	now        func() time.Time
}

// ExportCertificateInput carries confirmations that are never persisted.
type ExportCertificateInput struct {
	Confirmation           string `json:"confirmation"`
	IncludePrivateKey      bool   `json:"include_private_key"`
	PrivateKeyConfirmation string `json:"private_key_confirmation"`
}

// CertificateExport is one short-lived PEM response assembled only after authorization.
type CertificateExport struct {
	Content            []byte
	Filename           string
	IncludedPrivateKey bool
}

// CertificateMaterialRecord joins safe metadata required for startup verification.
type CertificateMaterialRecord struct {
	Certificate Certificate
	Version     Version
}

// MaterialReconciliation reports bounded startup verification without exposing filesystem paths.
type MaterialReconciliation struct {
	Checked        int
	NeedsAttention []CertificateID
}

// RenewalPolicyInput changes one persisted automatic-renewal schedule after exact confirmation.
type RenewalPolicyInput struct {
	Confirmation       string `json:"confirmation"`
	AutoRenew          bool   `json:"auto_renew"`
	RenewBeforeSeconds int64  `json:"renew_before_seconds"`
}

// NewLifecycleService validates explicit lifecycle dependencies.
func NewLifecycleService(options LifecycleServiceOptions) (*LifecycleService, error) {
	if options.Repository == nil || options.Vault == nil || options.Publisher == nil || options.Random == nil {
		return nil, fmt.Errorf("create certificate lifecycle service: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &LifecycleService{
		repository: options.Repository, vault: options.Vault, publisher: options.Publisher,
		random: options.Random, now: options.Now,
	}, nil
}

// UpdateRenewalPolicy persists a fresh jittered timestamp and clears stale retry state.
func (service *LifecycleService) UpdateRenewalPolicy(
	ctx context.Context,
	actor config.Actor,
	id CertificateID,
	input RenewalPolicyInput,
) (Certificate, error) {
	if err := validateLifecycleRequest(ctx, service, actor, id); err != nil ||
		input.RenewBeforeSeconds <= 0 || input.RenewBeforeSeconds > int64((90*24*time.Hour)/time.Second) {
		return Certificate{}, fmt.Errorf("update certificate renewal policy: %w", ErrRenewalPolicyInvalid)
	}
	item, err := service.repository.Certificate(ctx, id)
	if err != nil {
		return Certificate{}, fmt.Errorf("update certificate renewal policy: %w", err)
	}
	renewBefore := time.Duration(input.RenewBeforeSeconds) * time.Second
	if input.Confirmation != item.PrimaryIdentifier || !renewableCertificateState(item.State) ||
		renewBefore >= item.NotAfter.Sub(item.NotBefore) {
		return Certificate{}, fmt.Errorf("update certificate renewal policy: %w", ErrRenewalPolicyInvalid)
	}
	now := service.now().UTC()
	var nextRenewalAt time.Time
	if input.AutoRenew {
		nextRenewalAt, err = renewalTime(item.NotAfter, renewBefore, service.random)
		if err != nil {
			return Certificate{}, fmt.Errorf("update certificate renewal policy: %w", ErrRenewalPolicyInvalid)
		}
		if nextRenewalAt.Before(now) {
			nextRenewalAt = now
		}
	}
	updated, err := service.repository.UpdateCertificateRenewalPolicy(
		ctx, id, actor, input.AutoRenew, input.RenewBeforeSeconds, nextRenewalAt, now,
	)
	if err != nil {
		return Certificate{}, fmt.Errorf("update certificate renewal policy: %w", err)
	}
	return updated, nil
}

// ReconcileMaterial re-reads every active immutable version and fail-closes invalid metadata or files.
func (service *LifecycleService) ReconcileMaterial(ctx context.Context) (MaterialReconciliation, error) {
	result := MaterialReconciliation{NeedsAttention: []CertificateID{}}
	if ctx == nil || service == nil {
		return result, fmt.Errorf("reconcile certificate material: service is unavailable")
	}
	records, err := service.repository.CertificateMaterialInventory(ctx, maximumMaterialInventory+1)
	if err != nil {
		return result, fmt.Errorf("reconcile certificate material: inventory: %w", err)
	}
	if len(records) > maximumMaterialInventory {
		return result, fmt.Errorf("reconcile certificate material: inventory limit exceeded")
	}
	for _, record := range records {
		result.Checked++
		invalid := ValidateCertificate(record.Certificate) != nil || ValidateVersion(record.Version) != nil ||
			record.Version.CertificateID != record.Certificate.ID ||
			record.Version.ID != record.Certificate.ActiveVersionID
		if !invalid {
			material, loadErr := service.vault.LoadCertificateVersion(
				ctx, record.Certificate.ID, record.Certificate.ActiveVersionID,
			)
			invalid = loadErr != nil || !materialMatchesVersion(material, record.Version)
		}
		if !invalid {
			continue
		}
		if err := service.repository.MarkCertificateMaterialNeedsAttention(
			ctx, record.Certificate.ID, record.Certificate.ActiveVersionID,
			"certificate_material_invalid", service.now().UTC(),
		); err != nil {
			return result, fmt.Errorf("reconcile certificate material: mark needs attention: %w", err)
		}
		result.NeedsAttention = append(result.NeedsAttention, record.Certificate.ID)
	}
	return result, nil
}

func materialMatchesVersion(material StoredCertificateMaterial, version Version) bool {
	return validStoredMaterialMetadata(material) && material.FullchainDigest == version.FullchainDigest &&
		material.PrivateKeyDigest == version.PrivateKeyDigest && material.LeafFingerprint == version.LeafFingerprint &&
		material.SerialNumber == version.SerialNumber && material.Issuer == version.Issuer &&
		material.NotBefore.Equal(version.NotBefore) && material.NotAfter.Equal(version.NotAfter)
}

// Export loads the active immutable version and optionally appends its key after a second phrase.
func (service *LifecycleService) Export(
	ctx context.Context,
	actor config.Actor,
	id CertificateID,
	input ExportCertificateInput,
) (CertificateExport, error) {
	if err := validateLifecycleRequest(ctx, service, actor, id); err != nil || input.Confirmation != string(id) {
		return CertificateExport{}, fmt.Errorf("export certificate: %w", ErrCertificateReferenced)
	}
	if input.IncludePrivateKey && input.PrivateKeyConfirmation != PrivateKeyExportConfirmation {
		return CertificateExport{}, fmt.Errorf("export certificate: %w", ErrPrivateKeyExportConfirmationRequired)
	}
	item, err := service.repository.Certificate(ctx, id)
	if err != nil {
		return CertificateExport{}, fmt.Errorf("export certificate: %w", err)
	}
	if item.State == CertificateStateDeleted || parseOpaqueID(string(item.ActiveVersionID)) != nil {
		return CertificateExport{}, fmt.Errorf("export certificate: %w", ErrCertificateReferenced)
	}
	material, err := service.vault.LoadCertificateVersion(ctx, id, item.ActiveVersionID)
	if err != nil || len(material.FullChainPEM) == 0 || len(material.FullChainPEM) > maximumCertificateExportSize {
		return CertificateExport{}, fmt.Errorf("export certificate: %w", ErrSecretInvalid)
	}
	content := slices.Clone(material.FullChainPEM)
	if len(content) == 0 || content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	if input.IncludePrivateKey {
		privateKey, marshalErr := MarshalPrivateKeyPEM(material.PrivateKey)
		if marshalErr != nil || len(content)+len(privateKey) > maximumCertificateExportSize {
			clear(privateKey)
			return CertificateExport{}, fmt.Errorf("export certificate: %w", ErrSecretInvalid)
		}
		content = append(content, privateKey...)
		clear(privateKey)
	}
	if err := service.repository.RecordCertificateExport(
		ctx, id, actor, input.IncludePrivateKey, service.now().UTC(),
	); err != nil {
		clear(content)
		return CertificateExport{}, fmt.Errorf("export certificate: record audit: %w", err)
	}
	return CertificateExport{
		Content: content, Filename: "certificate-" + string(id) + ".pem",
		IncludedPrivateKey: input.IncludePrivateKey,
	}, nil
}

// Unbind removes only exact direct paths owned by the active version through the normal release path.
func (service *LifecycleService) Unbind(
	ctx context.Context,
	actor config.Actor,
	id CertificateID,
	confirmation string,
) (Certificate, error) {
	if err := validateLifecycleRequest(ctx, service, actor, id); err != nil {
		return Certificate{}, fmt.Errorf("unbind certificate: %w", err)
	}
	item, err := service.repository.Certificate(ctx, id)
	if err != nil {
		return Certificate{}, fmt.Errorf("unbind certificate: %w", err)
	}
	if confirmation != item.PrimaryIdentifier || item.State == CertificateStateDeleted ||
		parseOpaqueID(string(item.ActiveVersionID)) != nil {
		return Certificate{}, fmt.Errorf("unbind certificate: %w", ErrCertificateReferenced)
	}
	bindings, err := service.repository.CertificateBindings(ctx, id)
	if err != nil {
		return Certificate{}, fmt.Errorf("unbind certificate: read bindings: %w", err)
	}
	if len(bindings) == 0 {
		if item.State != CertificateStateUnbound {
			return Certificate{}, fmt.Errorf("unbind certificate: %w", ErrCertificateReferenced)
		}
		return item, nil
	}
	refs, err := serverRefsFromBindings(bindings, item)
	if err != nil {
		return Certificate{}, err
	}
	publication, err := service.publisher.Publish(
		ctx, actor, "Certificate unbind "+string(id[:8]),
		func(snapshot config.DraftSnapshot) (ConfigurationChange, error) {
			project, projectErr := ProjectFromDraft(snapshot)
			if projectErr != nil {
				return ConfigurationChange{}, projectErr
			}
			plan, planErr := PlanCertificateUnbinding(ctx, project, refs, id, item.ActiveVersionID)
			if planErr != nil {
				return ConfigurationChange{}, planErr
			}
			return ConfigurationChange{
				Replacements: bindingReplacements(plan), OperationKind: "certificate.unbind",
				PreviewID: configurationPlanDigest(plan, nil), TargetID: string(id),
			}, nil
		},
	)
	if err != nil {
		return Certificate{}, fmt.Errorf("unbind certificate: %w", err)
	}
	if !publication.Changed || publication.ReleaseID == "" {
		return Certificate{}, fmt.Errorf("unbind certificate: %w", ErrConfigurationReleaseUncertain)
	}
	updated, err := service.repository.CompleteCertificateUnbinding(
		ctx, id, actor, publication.ReleaseID, service.now().UTC(),
	)
	if err != nil {
		return Certificate{}, fmt.Errorf("unbind certificate: commit metadata: %w", err)
	}
	return updated, nil
}

// Delete proves both repository and latest production references are absent before soft deletion and cleanup.
func (service *LifecycleService) Delete(
	ctx context.Context,
	actor config.Actor,
	id CertificateID,
	confirmation string,
) (Certificate, error) {
	if err := validateLifecycleRequest(ctx, service, actor, id); err != nil || confirmation != string(id) {
		return Certificate{}, fmt.Errorf("delete certificate: %w", ErrCertificateReferenced)
	}
	item, err := service.repository.Certificate(ctx, id)
	if err != nil {
		return Certificate{}, fmt.Errorf("delete certificate: %w", err)
	}
	if item.State != CertificateStateUnbound || parseOpaqueID(string(item.ActiveVersionID)) != nil {
		return Certificate{}, fmt.Errorf("delete certificate: %w", ErrCertificateReferenced)
	}
	bindings, err := service.repository.CertificateBindings(ctx, id)
	if err != nil || len(bindings) != 0 {
		return Certificate{}, fmt.Errorf("delete certificate: %w", ErrCertificateReferenced)
	}
	versions, err := service.repository.CertificateVersions(ctx, id)
	if err != nil || len(versions) == 0 {
		return Certificate{}, fmt.Errorf("delete certificate: versions: %w", ErrCertificateReferenced)
	}
	paths := make(map[string]bool, len(versions)*2)
	for _, version := range versions {
		if parseOpaqueID(string(version.ID)) != nil || version.CertificateID != id {
			return Certificate{}, fmt.Errorf("delete certificate: %w", ErrCertificateReferenced)
		}
		paths[CertificateFullchainPath(id, version.ID)] = true
		paths[CertificatePrivateKeyPath(id, version.ID)] = true
	}
	_, err = service.publisher.Publish(
		ctx, actor, "Certificate delete check "+string(id[:8]),
		func(snapshot config.DraftSnapshot) (ConfigurationChange, error) {
			project, projectErr := ProjectFromDraft(snapshot)
			if projectErr != nil {
				return ConfigurationChange{}, projectErr
			}
			if projectReferencesCertificatePaths(project, paths) {
				return ConfigurationChange{}, ErrCertificateReferenced
			}
			return ConfigurationChange{}, nil
		},
	)
	if err != nil {
		return Certificate{}, fmt.Errorf("delete certificate: inspect production: %w", err)
	}
	deleted, err := service.repository.DeleteCertificate(ctx, id, actor, service.now().UTC())
	if err != nil {
		return Certificate{}, fmt.Errorf("delete certificate: commit metadata: %w", err)
	}
	cleanupContext, cancel := detachedOperationContext(ctx, configurationCleanupTimeout)
	cleanupErr := service.vault.DeleteCertificate(cleanupContext, id)
	cancel()
	if cleanupErr != nil {
		return deleted, fmt.Errorf("delete certificate: clean material: %w", ErrSecretInvalid)
	}
	return deleted, nil
}

func validateLifecycleRequest(
	ctx context.Context,
	service *LifecycleService,
	actor config.Actor,
	id CertificateID,
) error {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		parseOpaqueID(string(id)) != nil {
		return ErrCertificateReferenced
	}
	return nil
}

func serverRefsFromBindings(bindings []Binding, item Certificate) ([]ServerRef, error) {
	refs := make([]ServerRef, 0, len(bindings))
	for _, binding := range bindings {
		if ValidateBinding(binding) != nil || binding.CertificateID != item.ID ||
			binding.VersionID != item.ActiveVersionID || binding.ServerStartOffset > math.MaxInt {
			return nil, fmt.Errorf("build certificate unbinding refs: %w", ErrCertificateReferenced)
		}
		var names, listeners []string
		if err := json.Unmarshal([]byte(binding.ServerNamesJSON), &names); err != nil || names == nil {
			return nil, fmt.Errorf("build certificate unbinding refs: %w", ErrCertificateReferenced)
		}
		if err := json.Unmarshal([]byte(binding.ListenersJSON), &listeners); err != nil || listeners == nil {
			return nil, fmt.Errorf("build certificate unbinding refs: %w", ErrCertificateReferenced)
		}
		refs = append(refs, ServerRef{
			Path: binding.ConfigPath, StartOffset: int(binding.ServerStartOffset), ServerNames: names,
			Listeners: listeners, Fingerprint: binding.ServerFingerprint,
		})
	}
	return refs, nil
}

func projectReferencesCertificatePaths(project *nginxast.Project, paths map[string]bool) bool {
	if project == nil || !project.Complete {
		return true
	}
	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok || (directive.Name.Value != "ssl_certificate" && directive.Name.Value != "ssl_certificate_key") ||
			len(directive.Arguments) != 1 {
			continue
		}
		if paths[directive.Arguments[0].Value] {
			return true
		}
	}
	return false
}
