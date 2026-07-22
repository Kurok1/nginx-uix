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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testCredentialID = "0123456789abcdef0123456789abcdef"

func TestOpenVaultHonorsCancellationBeforeCreatingSecrets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenVault(ctx, root, bytes.NewReader(bytes.Repeat([]byte{0x42}, 128))); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenVault(cancelled) error = %v, want %v", err, context.Canceled)
	}
	if _, err := os.Stat(filepath.Join(root, "master.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("master key exists after cancelled open: %v", err)
	}
}

func TestVaultEncryptsCloudflareTokenAndBindsCiphertextToCredential(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	vault, err := OpenVault(context.Background(), root, bytes.NewReader(bytes.Repeat([]byte{0x42}, 128)))
	if err != nil {
		t.Fatalf("OpenVault() error = %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })

	token := "cfut_secret-value-that-must-not-be-written-in-plaintext"
	if err := vault.StoreCloudflareToken(context.Background(), testCredentialID, token); err != nil {
		t.Fatalf("StoreCloudflareToken() error = %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "credentials", testCredentialID+".token"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), token) {
		t.Fatal("credential file contains plaintext token")
	}
	information, err := os.Stat(filepath.Join(root, "credentials", testCredentialID+".token"))
	if err != nil {
		t.Fatal(err)
	}
	if information.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %04o, want 0600", information.Mode().Perm())
	}

	loaded, err := vault.LoadCloudflareToken(context.Background(), testCredentialID)
	if err != nil {
		t.Fatalf("LoadCloudflareToken() error = %v", err)
	}
	if loaded != token {
		t.Fatalf("LoadCloudflareToken() = %q, want original", loaded)
	}

	otherPath := filepath.Join(root, "credentials", "fedcba9876543210fedcba9876543210.token")
	if err := os.WriteFile(otherPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.LoadCloudflareToken(context.Background(), "fedcba9876543210fedcba9876543210"); !errors.Is(err, ErrSecretInvalid) {
		t.Fatalf("LoadCloudflareToken(other ID) error = %v, want %v", err, ErrSecretInvalid)
	}
}

func TestOpenVaultRejectsLooseRootPermissionsAndSymlinkCredential(t *testing.T) {
	t.Parallel()

	loose := t.TempDir()
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenVault(context.Background(), loose, bytes.NewReader(bytes.Repeat([]byte{1}, 64))); !errors.Is(err, ErrSecretInvalid) {
		t.Fatalf("OpenVault(loose) error = %v, want %v", err, ErrSecretInvalid)
	}

	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	vault, err := OpenVault(context.Background(), root, bytes.NewReader(bytes.Repeat([]byte{2}, 128)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "credentials", testCredentialID+".token")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := vault.StoreCloudflareToken(context.Background(), testCredentialID, "cfut_value"); !errors.Is(err, ErrSecretInvalid) {
		t.Fatalf("StoreCloudflareToken(symlink) error = %v, want %v", err, ErrSecretInvalid)
	}
}

func TestVaultStoresAccountKeyAndImmutableValidatedCertificateVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	vault, err := OpenVault(context.Background(), root, bytes.NewReader(bytes.Repeat([]byte{0x33}, 1024)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accountID := AccountID("11111111111111111111111111111111")
	if err := vault.StoreAccountKey(context.Background(), accountID, accountKey); err != nil {
		t.Fatalf("StoreAccountKey() error = %v", err)
	}
	loadedAccountKey, err := vault.LoadAccountKey(context.Background(), accountID)
	if err != nil {
		t.Fatalf("LoadAccountKey() error = %v", err)
	}
	if !publicKeysEqual(loadedAccountKey.Public(), accountKey.Public()) {
		t.Fatal("loaded account key does not match")
	}
	assertSecretMode(t, filepath.Join(root, "accounts", string(accountID), "account.key"))

	certificateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	issued, err := ValidateIssuedCertificate(
		[][]byte{mustIssueLeaf(t, certificateKey, []string{"example.com"}, now)},
		certificateKey, []string{"example.com"}, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	certificateID := CertificateID("22222222222222222222222222222222")
	versionID := VersionID("33333333333333333333333333333333")
	stored, err := vault.StoreCertificateVersion(context.Background(), certificateID, versionID, issued, certificateKey)
	if err != nil {
		t.Fatalf("StoreCertificateVersion() error = %v", err)
	}
	if stored.LeafFingerprint != issued.LeafFingerprint || stored.FullchainDigest == "" || stored.PrivateKeyDigest == "" {
		t.Fatalf("stored material = %#v", stored)
	}
	for _, name := range []string{"fullchain.pem", "leaf.pem", "privkey.pem"} {
		assertSecretMode(t, filepath.Join(root, "certificates", string(certificateID), "versions", string(versionID), name))
	}
	loaded, err := vault.LoadCertificateVersion(context.Background(), certificateID, versionID)
	if err != nil {
		t.Fatalf("LoadCertificateVersion() error = %v", err)
	}
	if loaded.FullchainDigest != stored.FullchainDigest || !publicKeysEqual(loaded.PrivateKey.Public(), certificateKey.Public()) {
		t.Fatalf("loaded material = %#v", loaded)
	}
	if _, err := vault.StoreCertificateVersion(context.Background(), certificateID, versionID, issued, certificateKey); !errors.Is(err, ErrSecretInvalid) {
		t.Fatalf("overwrite error = %v, want ErrSecretInvalid", err)
	}
	secondVersionID := VersionID("44444444444444444444444444444444")
	if _, err := vault.StoreCertificateVersion(context.Background(), certificateID, secondVersionID, issued, certificateKey); err != nil {
		t.Fatalf("StoreCertificateVersion(second) error = %v", err)
	}
	if err := vault.DeleteCertificateVersion(context.Background(), certificateID, versionID); err != nil {
		t.Fatalf("DeleteCertificateVersion() error = %v", err)
	}
	if _, err := vault.LoadCertificateVersion(context.Background(), certificateID, versionID); !errors.Is(err, ErrSecretInvalid) {
		t.Fatalf("LoadCertificateVersion(deleted version) error = %v", err)
	}
	if _, err := vault.LoadCertificateVersion(context.Background(), certificateID, secondVersionID); err != nil {
		t.Fatalf("LoadCertificateVersion(remaining version) error = %v", err)
	}
	if err := vault.DeleteCertificate(context.Background(), certificateID); err != nil {
		t.Fatalf("DeleteCertificate() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "certificates", string(certificateID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("certificate directory after delete error = %v, want not exist", err)
	}
	if _, err := vault.LoadCertificateVersion(context.Background(), certificateID, versionID); !errors.Is(err, ErrSecretInvalid) {
		t.Fatalf("LoadCertificateVersion(after delete) error = %v", err)
	}
}

func TestParsePrivateKeyPEMRejectsTrailingDataAndWeakRSA(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalPrivateKeyPEM(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePrivateKeyPEM(append(payload, []byte("not pem")...)); !errors.Is(err, ErrSecretInvalid) {
		t.Fatalf("trailing data error = %v, want ErrSecretInvalid", err)
	}
}

func assertSecretMode(t *testing.T, path string) {
	t.Helper()
	information, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !information.Mode().IsRegular() || information.Mode().Perm() != 0o600 {
		t.Fatalf("%s mode = %v, want regular 0600", path, information.Mode())
	}
}
