/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestLifecycleServiceExportsFullchainAndRequiresSecondPrivateKeyConfirmation(t *testing.T) {
	service, repository, vault, _ := newLifecycleFixture(t, false)
	actor := config.Actor{UserID: 7, RequestID: "request-export"}

	public, err := service.Export(context.Background(), actor, repository.item.ID, ExportCertificateInput{
		Confirmation: string(repository.item.ID),
	})
	if err != nil || public.IncludedPrivateKey || !bytes.Equal(public.Content, vault.material.FullChainPEM) {
		t.Fatalf("Export(public)=%#v error=%v", public, err)
	}
	if _, err := service.Export(context.Background(), actor, repository.item.ID, ExportCertificateInput{
		Confirmation: string(repository.item.ID), IncludePrivateKey: true,
	}); !errors.Is(err, ErrPrivateKeyExportConfirmationRequired) {
		t.Fatalf("Export(private without phrase) error=%v", err)
	}
	private, err := service.Export(context.Background(), actor, repository.item.ID, ExportCertificateInput{
		Confirmation: string(repository.item.ID), IncludePrivateKey: true,
		PrivateKeyConfirmation: PrivateKeyExportConfirmation,
	})
	if err != nil || !private.IncludedPrivateKey || !bytes.Contains(private.Content, []byte("PRIVATE KEY")) ||
		!repository.exportPrivate {
		t.Fatalf("Export(private)=%#v error=%v audit=%v", private, err, repository.exportPrivate)
	}
}

func TestLifecycleServiceUnbindsExactOwnedDirectivesBeforeMetadata(t *testing.T) {
	service, repository, _, publisher := newLifecycleFixture(t, true)
	actor := config.Actor{UserID: 7, RequestID: "request-unbind"}

	updated, err := service.Unbind(context.Background(), actor, repository.item.ID, repository.item.PrimaryIdentifier)
	if err != nil {
		t.Fatalf("Unbind() error=%v", err)
	}
	if updated.State != CertificateStateUnbound || repository.unbindRelease != publisher.result.ReleaseID ||
		len(publisher.changes) != 1 || len(publisher.changes[0].Replacements) != 1 {
		t.Fatalf("updated/repository/changes=%#v/%#v/%#v", updated, repository, publisher.changes)
	}
	replacement := string(publisher.changes[0].Replacements[0].Content)
	if strings.Contains(replacement, CertificateFullchainPath(repository.item.ID, repository.item.ActiveVersionID)) ||
		strings.Contains(replacement, CertificatePrivateKeyPath(repository.item.ID, repository.item.ActiveVersionID)) {
		t.Fatalf("unbinding replacement retained owned paths: %s", replacement)
	}
}

func TestLifecycleServiceDeleteRejectsLatestProductionReference(t *testing.T) {
	service, repository, vault, _ := newLifecycleFixture(t, true)
	repository.bindings = nil
	repository.item.State = CertificateStateUnbound
	actor := config.Actor{UserID: 7, RequestID: "request-delete"}

	if _, err := service.Delete(
		context.Background(), actor, repository.item.ID, string(repository.item.ID),
	); !errors.Is(err, ErrCertificateReferenced) {
		t.Fatalf("Delete(referenced) error=%v", err)
	}
	if repository.deleted || vault.deleted {
		t.Fatalf("referenced delete mutated metadata/material: %#v/%#v", repository, vault)
	}

	service.publisher = &configurationPublisherStub{
		snapshot: certificatePlanSnapshot(t, "events {}\nhttp { server { listen 443 ssl; server_name example.com; } }\n"),
		events:   &[]string{}, result: ConfigurationPublication{},
	}
	deleted, err := service.Delete(context.Background(), actor, repository.item.ID, string(repository.item.ID))
	if err != nil || deleted.State != CertificateStateDeleted || !repository.deleted || !vault.deleted {
		t.Fatalf("Delete(unreferenced)=%#v error=%v repository/vault=%#v/%#v", deleted, err, repository, vault)
	}
}

func TestLifecycleServiceUpdatesRenewalPolicyWithFreshPersistedSchedule(t *testing.T) {
	service, repository, _, _ := newLifecycleFixture(t, false)
	actor := config.Actor{UserID: 7, RequestID: "request-policy"}

	disabled, err := service.UpdateRenewalPolicy(context.Background(), actor, repository.item.ID, RenewalPolicyInput{
		Confirmation: repository.item.PrimaryIdentifier, AutoRenew: false,
		RenewBeforeSeconds: int64((14 * 24 * time.Hour) / time.Second),
	})
	if err != nil || disabled.AutoRenew || !disabled.NextRenewalAt.IsZero() || disabled.RenewBeforeSeconds != 14*24*60*60 {
		t.Fatalf("UpdateRenewalPolicy(disabled)=%#v error=%v", disabled, err)
	}
	enabled, err := service.UpdateRenewalPolicy(context.Background(), actor, repository.item.ID, RenewalPolicyInput{
		Confirmation: repository.item.PrimaryIdentifier, AutoRenew: true,
		RenewBeforeSeconds: int64((14 * 24 * time.Hour) / time.Second),
	})
	if err != nil || !enabled.AutoRenew || enabled.NextRenewalAt.IsZero() ||
		enabled.NextRenewalAt.Before(enabled.NotAfter.Add(-14*24*time.Hour-maximumRenewalJitter)) ||
		enabled.NextRenewalAt.After(enabled.NotAfter.Add(-14*24*time.Hour+maximumRenewalJitter)) {
		t.Fatalf("UpdateRenewalPolicy(enabled)=%#v error=%v", enabled, err)
	}
	if repository.policyUpdates != 2 {
		t.Fatalf("policy updates=%d, want 2", repository.policyUpdates)
	}
}

func TestLifecycleServiceReconcilesInvalidActiveMaterialToNeedsAttention(t *testing.T) {
	service, repository, vault, _ := newLifecycleFixture(t, false)
	repository.inventory = []CertificateMaterialRecord{{
		Certificate: repository.item,
		Version: Version{
			ID: repository.item.ActiveVersionID, CertificateID: repository.item.ID, State: VersionStateActive,
			FullchainDigest: strings.Repeat("a", 64), PrivateKeyDigest: strings.Repeat("b", 64),
			LeafFingerprint: strings.Repeat("c", 64), SerialNumber: "42", Issuer: "Test CA",
			NotBefore: repository.item.NotBefore, NotAfter: repository.item.NotAfter, CreatedAt: repository.item.CreatedAt,
		},
	}}
	vault.loadErr = ErrSecretInvalid

	result, err := service.ReconcileMaterial(context.Background())
	if err != nil || len(result.NeedsAttention) != 1 || result.NeedsAttention[0] != repository.item.ID ||
		repository.item.State != CertificateStateNeedsAttention || repository.materialErrorCode != "certificate_material_invalid" {
		t.Fatalf("ReconcileMaterial()=%#v error=%v repository=%#v", result, err, repository)
	}
}

func newLifecycleFixture(
	t *testing.T,
	boundSource bool,
) (*LifecycleService, *lifecycleRepositoryStub, *lifecycleVaultStub, *configurationPublisherStub) {
	t.Helper()
	now := time.Date(2026, 7, 21, 20, 0, 0, 0, time.UTC)
	certificateID := CertificateID("33333333333333333333333333333333")
	versionID := VersionID("44444444444444444444444444444444")
	source := "events {}\nhttp {\n  server {\n    listen 443 ssl;\n    server_name example.com;\n  }\n}\n"
	if boundSource {
		source = "events {}\nhttp {\n  server {\n    listen 443 ssl;\n    server_name example.com;\n    ssl_certificate " + CertificateFullchainPath(certificateID, versionID) + ";\n    ssl_certificate_key " + CertificatePrivateKeyPath(certificateID, versionID) + ";\n  }\n}\n"
	}
	snapshot := certificatePlanSnapshot(t, source)
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := BuildServerCandidates(project)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("BuildServerCandidates()=%#v error=%v", candidates, err)
	}
	item := Certificate{
		ID: certificateID, PrimaryIdentifier: "example.com", IdentifiersJSON: `["example.com"]`,
		Challenge: ChallengeHTTP01, AccountID: "11111111111111111111111111111111",
		State: CertificateStateActive, ActiveVersionID: versionID, AutoRenew: true,
		RenewBeforeSeconds: int64((30 * 24 * time.Hour) / time.Second), NextRenewalAt: now.Add(30 * 24 * time.Hour),
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour),
		CreatedBy: 7, RequestID: "request-issue", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
	repository := &lifecycleRepositoryStub{
		item:     item,
		versions: []Version{{ID: versionID, CertificateID: certificateID, State: VersionStateActive}},
		bindings: []Binding{{
			ID: "55555555555555555555555555555555", CertificateID: certificateID, VersionID: versionID,
			ConfigPath: candidates[0].Ref.Path, ServerStartOffset: int64(candidates[0].Ref.StartOffset),
			ServerNamesJSON: `["example.com"]`, ListenersJSON: `["443 ssl"]`,
			ServerFingerprint: candidates[0].Ref.Fingerprint, CreatedAt: now, UpdatedAt: now,
		}},
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := ValidateIssuedCertificate(
		[][]byte{mustIssueLeaf(t, key, []string{"example.com"}, now)}, key, []string{"example.com"}, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	vault := &lifecycleVaultStub{material: StoredCertificateMaterial{
		FullChainPEM: issued.FullChainPEM, LeafPEM: issued.LeafPEM, PrivateKey: key,
	}}
	publisher := &configurationPublisherStub{
		snapshot: snapshot, events: &[]string{},
		result: ConfigurationPublication{ReleaseID: "66666666666666666666666666666666", Changed: true},
	}
	service, err := NewLifecycleService(LifecycleServiceOptions{
		Repository: repository, Vault: vault, Publisher: publisher, Random: bytes.NewReader(make([]byte, 64)),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, vault, publisher
}

type lifecycleRepositoryStub struct {
	item              Certificate
	versions          []Version
	bindings          []Binding
	unbindRelease     string
	exportPrivate     bool
	deleted           bool
	policyUpdates     int
	inventory         []CertificateMaterialRecord
	materialErrorCode string
}

func (stub *lifecycleRepositoryStub) Certificate(context.Context, CertificateID) (Certificate, error) {
	return stub.item, nil
}

func (stub *lifecycleRepositoryStub) CertificateVersions(context.Context, CertificateID) ([]Version, error) {
	return stub.versions, nil
}

func (stub *lifecycleRepositoryStub) CertificateBindings(context.Context, CertificateID) ([]Binding, error) {
	return stub.bindings, nil
}

func (stub *lifecycleRepositoryStub) CompleteCertificateUnbinding(
	_ context.Context, _ CertificateID, _ config.Actor, releaseID string, at time.Time,
) (Certificate, error) {
	stub.unbindRelease = releaseID
	stub.item.State = CertificateStateUnbound
	stub.item.UpdatedAt = at
	stub.bindings = nil
	return stub.item, nil
}

func (stub *lifecycleRepositoryStub) RecordCertificateExport(
	_ context.Context, _ CertificateID, _ config.Actor, private bool, _ time.Time,
) error {
	stub.exportPrivate = private
	return nil
}

func (stub *lifecycleRepositoryStub) DeleteCertificate(
	_ context.Context, _ CertificateID, _ config.Actor, at time.Time,
) (Certificate, error) {
	stub.deleted = true
	stub.item.State = CertificateStateDeleted
	stub.item.ActiveVersionID = ""
	stub.item.AutoRenew = false
	stub.item.UpdatedAt = at
	return stub.item, nil
}

func (stub *lifecycleRepositoryStub) UpdateCertificateRenewalPolicy(
	_ context.Context,
	_ CertificateID,
	_ config.Actor,
	autoRenew bool,
	renewBeforeSeconds int64,
	nextRenewalAt time.Time,
	at time.Time,
) (Certificate, error) {
	stub.policyUpdates++
	stub.item.AutoRenew = autoRenew
	stub.item.RenewBeforeSeconds = renewBeforeSeconds
	stub.item.NextRenewalAt = nextRenewalAt
	stub.item.RetryAt = time.Time{}
	stub.item.RetryCount = 0
	stub.item.LastErrorCode = ""
	stub.item.UpdatedAt = at
	return stub.item, nil
}

func (stub *lifecycleRepositoryStub) CertificateMaterialInventory(
	context.Context, int,
) ([]CertificateMaterialRecord, error) {
	return append([]CertificateMaterialRecord(nil), stub.inventory...), nil
}

func (stub *lifecycleRepositoryStub) MarkCertificateMaterialNeedsAttention(
	_ context.Context, id CertificateID, versionID VersionID, code string, at time.Time,
) error {
	if stub.item.ID != id || stub.item.ActiveVersionID != versionID {
		return ErrCertificateReferenced
	}
	stub.item.State = CertificateStateNeedsAttention
	stub.item.LastErrorCode = code
	stub.item.UpdatedAt = at
	stub.materialErrorCode = code
	return nil
}

type lifecycleVaultStub struct {
	material StoredCertificateMaterial
	deleted  bool
	loadErr  error
}

func (stub *lifecycleVaultStub) LoadCertificateVersion(
	context.Context, CertificateID, VersionID,
) (StoredCertificateMaterial, error) {
	return stub.material, stub.loadErr
}

func (stub *lifecycleVaultStub) DeleteCertificate(context.Context, CertificateID) error {
	stub.deleted = true
	return nil
}
