/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"
)

const (
	privateKeyPEMLimit = 64 << 10
	leafPEMLimit       = 1 << 20
)

// StoredCertificateMaterial is verified immutable material re-read from disk.
type StoredCertificateMaterial struct {
	FullChainPEM     []byte
	LeafPEM          []byte
	PrivateKey       crypto.Signer
	FullchainDigest  string
	PrivateKeyDigest string
	LeafFingerprint  string
	SerialNumber     string
	Issuer           string
	NotBefore        time.Time
	NotAfter         time.Time
}

// StoreAccountKey stores one write-once account key below its exact account ID.
func (v *Vault) StoreAccountKey(ctx context.Context, accountID AccountID, key crypto.Signer) (returnErr error) {
	if err := validateVaultOperation(ctx, v, string(accountID)); err != nil || !validPrivateKey(key) {
		return fmt.Errorf("store ACME account key: %w", ErrSecretInvalid)
	}
	payload, err := MarshalPrivateKeyPEM(key)
	if err != nil {
		return fmt.Errorf("store ACME account key: %w", ErrSecretInvalid)
	}
	directory := accountDirectory(accountID)
	if _, err := v.root.Lstat(directory); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("store ACME account key: target: %w", ErrSecretInvalid)
	}
	if err := v.root.Mkdir(directory, vaultDirectoryMode); err != nil {
		return fmt.Errorf("store ACME account key: create directory: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			returnErr = errors.Join(returnErr, v.root.RemoveAll(directory), syncVaultDirectory(v.root, "accounts"))
		}
	}()
	if err := syncVaultDirectory(v.root, "accounts"); err != nil {
		return fmt.Errorf("store ACME account key: sync directory: %w", err)
	}
	if err := atomicVaultCreate(ctx, v.root, v.random, accountKeyPath(accountID), payload); err != nil {
		return fmt.Errorf("store ACME account key: %w", err)
	}
	owned = false
	return nil
}

// LoadAccountKey loads and strictly parses one exact account key.
func (v *Vault) LoadAccountKey(ctx context.Context, accountID AccountID) (crypto.Signer, error) {
	if err := validateVaultOperation(ctx, v, string(accountID)); err != nil {
		return nil, fmt.Errorf("load ACME account key: %w", ErrSecretInvalid)
	}
	payload, err := readVaultSecret(v.root, accountKeyPath(accountID), privateKeyPEMLimit)
	if err != nil {
		return nil, fmt.Errorf("load ACME account key: %w", ErrSecretInvalid)
	}
	key, err := ParsePrivateKeyPEM(payload)
	if err != nil {
		return nil, fmt.Errorf("load ACME account key: %w", ErrSecretInvalid)
	}
	return key, nil
}

// DeleteAccountKey removes one exact account key directory.
func (v *Vault) DeleteAccountKey(ctx context.Context, accountID AccountID) error {
	if _, err := v.LoadAccountKey(ctx, accountID); err != nil {
		return err
	}
	directory := accountDirectory(accountID)
	if err := v.root.Remove(accountKeyPath(accountID)); err != nil {
		return fmt.Errorf("delete ACME account key: %w", err)
	}
	if err := v.root.Remove(directory); err != nil {
		return fmt.Errorf("delete ACME account key directory: %w", err)
	}
	return syncVaultDirectory(v.root, "accounts")
}

// StoreCertificateVersion commits one immutable certificate version and verifies it from disk.
func (v *Vault) StoreCertificateVersion(
	ctx context.Context,
	certificateID CertificateID,
	versionID VersionID,
	issued IssuedCertificate,
	key crypto.Signer,
) (_ StoredCertificateMaterial, returnErr error) {
	if err := validateVaultOperation(ctx, v, string(certificateID)); err != nil ||
		parseOpaqueID(string(versionID)) != nil || !validPrivateKey(key) {
		return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: %w", ErrSecretInvalid)
	}
	keyPEM, err := MarshalPrivateKeyPEM(key)
	if err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: %w", ErrSecretInvalid)
	}
	if err := validateIssuedPEM(issued, key); err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: %w", err)
	}
	certificateDirectory := certificateDirectory(certificateID)
	versionsDirectory := certificateDirectory + "/versions"
	for _, directory := range []string{certificateDirectory, versionsDirectory} {
		if err := ensureVaultDirectory(v.root, directory); err != nil {
			return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: %w", err)
		}
	}
	versionDirectory := certificateVersionDirectory(certificateID, versionID)
	if _, err := v.root.Lstat(versionDirectory); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: target: %w", ErrSecretInvalid)
	}
	if err := v.root.Mkdir(versionDirectory, vaultDirectoryMode); err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: create directory: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			returnErr = errors.Join(returnErr, v.root.RemoveAll(versionDirectory), syncVaultDirectory(v.root, versionsDirectory))
		}
	}()
	if err := syncVaultDirectory(v.root, versionsDirectory); err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: sync versions: %w", err)
	}
	files := []struct {
		name    string
		payload []byte
	}{
		{name: "fullchain.pem", payload: issued.FullChainPEM},
		{name: "leaf.pem", payload: issued.LeafPEM},
		{name: "privkey.pem", payload: keyPEM},
	}
	for _, file := range files {
		if err := atomicVaultCreate(ctx, v.root, v.random, versionDirectory+"/"+file.name, file.payload); err != nil {
			return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: write material: %w", err)
		}
	}
	material, err := v.LoadCertificateVersion(ctx, certificateID, versionID)
	if err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: verify material: %w", err)
	}
	if material.LeafFingerprint != issued.LeafFingerprint || material.SerialNumber != issued.SerialNumber ||
		!material.NotBefore.Equal(issued.NotBefore) || !material.NotAfter.Equal(issued.NotAfter) ||
		!publicKeysEqual(material.PrivateKey.Public(), key.Public()) {
		return StoredCertificateMaterial{}, fmt.Errorf("store certificate version: verify material: %w", ErrSecretInvalid)
	}
	owned = false
	return material, nil
}

// LoadCertificateVersion verifies file type, mode, PEM, key match and digests.
func (v *Vault) LoadCertificateVersion(
	ctx context.Context,
	certificateID CertificateID,
	versionID VersionID,
) (StoredCertificateMaterial, error) {
	if err := validateVaultOperation(ctx, v, string(certificateID)); err != nil || parseOpaqueID(string(versionID)) != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("load certificate version: %w", ErrSecretInvalid)
	}
	directory := certificateVersionDirectory(certificateID, versionID)
	fullchain, err := readVaultSecret(v.root, directory+"/fullchain.pem", maximumCertificateChainBytes)
	if err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("load certificate version: %w", ErrSecretInvalid)
	}
	leafPEM, err := readVaultSecret(v.root, directory+"/leaf.pem", leafPEMLimit)
	if err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("load certificate version: %w", ErrSecretInvalid)
	}
	privateKeyPEM, err := readVaultSecret(v.root, directory+"/privkey.pem", privateKeyPEMLimit)
	if err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("load certificate version: %w", ErrSecretInvalid)
	}
	chain, err := parseCertificatePEM(fullchain, maximumCertificateChainLength)
	if err != nil {
		return StoredCertificateMaterial{}, fmt.Errorf("load certificate version: %w", ErrSecretInvalid)
	}
	leaves, err := parseCertificatePEM(leafPEM, 1)
	if err != nil || len(leaves) != 1 || !bytes.Equal(chain[0].Raw, leaves[0].Raw) {
		return StoredCertificateMaterial{}, fmt.Errorf("load certificate version: %w", ErrSecretInvalid)
	}
	key, err := ParsePrivateKeyPEM(privateKeyPEM)
	if err != nil || !publicKeysEqual(leaves[0].PublicKey, key.Public()) {
		return StoredCertificateMaterial{}, fmt.Errorf("load certificate version: %w", ErrSecretInvalid)
	}
	fullchainDigest := sha256.Sum256(fullchain)
	privateKeyDigest := sha256.Sum256(privateKeyPEM)
	leafFingerprint := sha256.Sum256(leaves[0].Raw)
	serial := ""
	if leaves[0].SerialNumber != nil {
		serial = leaves[0].SerialNumber.Text(16)
	}
	if serial == "" || leaves[0].NotAfter.IsZero() || leaves[0].NotBefore.IsZero() ||
		!leaves[0].NotAfter.After(leaves[0].NotBefore) {
		return StoredCertificateMaterial{}, fmt.Errorf("load certificate version: %w", ErrSecretInvalid)
	}
	return StoredCertificateMaterial{
		FullChainPEM: fullchain, LeafPEM: leafPEM, PrivateKey: key,
		FullchainDigest:  hex.EncodeToString(fullchainDigest[:]),
		PrivateKeyDigest: hex.EncodeToString(privateKeyDigest[:]),
		LeafFingerprint:  hex.EncodeToString(leafFingerprint[:]), SerialNumber: serial,
		Issuer: leaves[0].Issuer.String(), NotBefore: leaves[0].NotBefore.UTC(), NotAfter: leaves[0].NotAfter.UTC(),
	}, nil
}

// DeleteCertificateVersion removes one exact uncommitted immutable version while preserving sibling versions.
func (v *Vault) DeleteCertificateVersion(
	ctx context.Context,
	certificateID CertificateID,
	versionID VersionID,
) error {
	if err := validateVaultOperation(ctx, v, string(certificateID)); err != nil || parseOpaqueID(string(versionID)) != nil {
		return fmt.Errorf("delete certificate version: %w", ErrSecretInvalid)
	}
	directory := certificateVersionDirectory(certificateID, versionID)
	information, err := v.root.Lstat(directory)
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() ||
		information.Mode().Perm() != vaultDirectoryMode {
		return fmt.Errorf("delete certificate version: %w", ErrSecretInvalid)
	}
	if err := v.root.RemoveAll(directory); err != nil {
		return fmt.Errorf("delete certificate version: %w", err)
	}
	if err := syncVaultDirectory(v.root, certificateDirectory(certificateID)+"/versions"); err != nil {
		return fmt.Errorf("delete certificate version: %w", err)
	}
	return nil
}

// DeleteCertificate removes one exact unreferenced certificate directory beneath the scoped vault root.
func (v *Vault) DeleteCertificate(ctx context.Context, certificateID CertificateID) error {
	if err := validateVaultOperation(ctx, v, string(certificateID)); err != nil {
		return fmt.Errorf("delete certificate material: %w", ErrSecretInvalid)
	}
	directory := certificateDirectory(certificateID)
	information, err := v.root.Lstat(directory)
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() ||
		information.Mode().Perm() != vaultDirectoryMode {
		return fmt.Errorf("delete certificate material: %w", ErrSecretInvalid)
	}
	if err := v.root.RemoveAll(directory); err != nil {
		return fmt.Errorf("delete certificate material: %w", err)
	}
	if err := syncVaultDirectory(v.root, "certificates"); err != nil {
		return fmt.Errorf("delete certificate material: %w", err)
	}
	return nil
}

// MarshalPrivateKeyPEM encodes one supported signer as a single PKCS#8 block.
func MarshalPrivateKeyPEM(key crypto.Signer) ([]byte, error) {
	if !validPrivateKey(key) {
		return nil, ErrSecretInvalid
	}
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, ErrSecretInvalid
	}
	payload := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})
	if len(payload) == 0 || len(payload) > privateKeyPEMLimit {
		return nil, ErrSecretInvalid
	}
	return payload, nil
}

// ParsePrivateKeyPEM strictly accepts one supported PKCS#8, EC or PKCS#1 key block.
func ParsePrivateKeyPEM(payload []byte) (crypto.Signer, error) {
	if len(payload) == 0 || len(payload) > privateKeyPEMLimit {
		return nil, ErrSecretInvalid
	}
	block, rest := pem.Decode(payload)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return nil, ErrSecretInvalid
	}
	var parsed any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		parsed, err = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return nil, ErrSecretInvalid
	}
	if err != nil {
		return nil, ErrSecretInvalid
	}
	key, ok := parsed.(crypto.Signer)
	if !ok || !validPrivateKey(key) {
		return nil, ErrSecretInvalid
	}
	return key, nil
}

func validateIssuedPEM(issued IssuedCertificate, key crypto.Signer) error {
	chain, err := parseCertificatePEM(issued.FullChainPEM, maximumCertificateChainLength)
	if err != nil || len(chain) == 0 {
		return ErrSecretInvalid
	}
	leaves, err := parseCertificatePEM(issued.LeafPEM, 1)
	if err != nil || len(leaves) != 1 || !bytes.Equal(chain[0].Raw, leaves[0].Raw) ||
		!publicKeysEqual(leaves[0].PublicKey, key.Public()) {
		return ErrSecretInvalid
	}
	fingerprint := sha256.Sum256(leaves[0].Raw)
	if hex.EncodeToString(fingerprint[:]) != issued.LeafFingerprint ||
		!leaves[0].NotBefore.UTC().Equal(issued.NotBefore) || !leaves[0].NotAfter.UTC().Equal(issued.NotAfter) {
		return ErrSecretInvalid
	}
	return nil
}

func parseCertificatePEM(payload []byte, limit int) ([]*x509.Certificate, error) {
	if len(payload) == 0 || len(payload) > maximumCertificateChainBytes || limit <= 0 {
		return nil, ErrSecretInvalid
	}
	remaining := payload
	certificates := make([]*x509.Certificate, 0, min(limit, maximumCertificateChainLength))
	for len(bytes.TrimSpace(remaining)) > 0 {
		if len(certificates) >= limit {
			return nil, ErrSecretInvalid
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, ErrSecretInvalid
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, ErrSecretInvalid
		}
		certificates = append(certificates, parsed)
		remaining = rest
	}
	if len(certificates) == 0 {
		return nil, ErrSecretInvalid
	}
	return certificates, nil
}

func validPrivateKey(key crypto.Signer) bool {
	if key == nil {
		return false
	}
	switch value := key.(type) {
	case *ecdsa.PrivateKey:
		if value == nil || value.Curve == nil {
			return false
		}
		encoded, err := value.Bytes()
		if err != nil {
			return false
		}
		clear(encoded)
		parameters := value.Params()
		if parameters == nil {
			return false
		}
		bits := parameters.BitSize
		return bits == 256 || bits == 384 || bits == 521
	case *rsa.PrivateKey:
		return value != nil && value.N != nil && value.N.BitLen() >= 2048 && value.N.BitLen() <= 8192 &&
			value.E >= 65537 && value.Validate() == nil
	default:
		return false
	}
}

func publicKeysEqual(left, right any) bool {
	leftDER, leftErr := x509.MarshalPKIXPublicKey(left)
	rightDER, rightErr := x509.MarshalPKIXPublicKey(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftDER, rightDER)
}

func accountDirectory(id AccountID) string {
	return "accounts/" + string(id)
}

func accountKeyPath(id AccountID) string {
	return accountDirectory(id) + "/account.key"
}

func certificateDirectory(id CertificateID) string {
	return "certificates/" + string(id)
}

func certificateVersionDirectory(certificateID CertificateID, versionID VersionID) string {
	return filepath.ToSlash(filepath.Join(certificateDirectory(certificateID), "versions", string(versionID)))
}
