/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	vaultDirectoryMode = fs.FileMode(0o700)
	vaultSecretMode    = fs.FileMode(0o600)
	vaultMasterKeySize = 32
	vaultSecretLimit   = 4 << 10
)

var tokenEnvelopeMagic = [4]byte{'N', 'U', 'X', 1}

// Vault owns fixed-root secret envelopes and private material.
type Vault struct {
	root   *os.Root
	random io.Reader
	aead   cipher.AEAD
}

// OpenVault verifies an owner-only root and loads or creates its master key.
func OpenVault(ctx context.Context, path string, random io.Reader) (*Vault, error) {
	if ctx == nil {
		return nil, fmt.Errorf("open certificate vault: %w", ErrSecretInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open certificate vault: %w", err)
	}
	if random == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("open certificate vault: %w", ErrSecretInvalid)
	}
	information, err := os.Lstat(path)
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() || information.Mode().Perm() != vaultDirectoryMode {
		return nil, fmt.Errorf("open certificate vault: root: %w", ErrSecretInvalid)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open certificate vault root: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = root.Close()
		}
	}()
	for _, directory := range []string{"accounts", "credentials", "certificates", "staging"} {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("open certificate vault: %w", err)
		}
		if err := ensureVaultDirectory(root, directory); err != nil {
			return nil, err
		}
	}
	key, err := loadOrCreateMasterKey(ctx, root, random)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("open certificate vault cipher: %w", ErrSecretInvalid)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("open certificate vault AEAD: %w", ErrSecretInvalid)
	}
	failed = false
	return &Vault{root: root, random: random, aead: aead}, nil
}

// Close releases the fixed-root descriptor.
func (v *Vault) Close() error {
	if v == nil || v.root == nil {
		return nil
	}
	if err := v.root.Close(); err != nil {
		return fmt.Errorf("close certificate vault: %w", err)
	}
	return nil
}

// StoreCloudflareToken encrypts one write-once API Token under credential-bound AAD.
func (v *Vault) StoreCloudflareToken(ctx context.Context, credentialID, token string) error {
	if err := validateVaultOperation(ctx, v, credentialID); err != nil || !validCloudflareTokenSecret(token) {
		return fmt.Errorf("store Cloudflare token: %w", ErrSecretInvalid)
	}
	path := credentialTokenPath(credentialID)
	if _, err := v.root.Lstat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("store Cloudflare token: target: %w", ErrSecretInvalid)
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(v.random, nonce); err != nil {
		return fmt.Errorf("store Cloudflare token: generate nonce: %w", err)
	}
	payload := make([]byte, 0, len(tokenEnvelopeMagic)+len(nonce)+len(token)+v.aead.Overhead())
	payload = append(payload, tokenEnvelopeMagic[:]...)
	payload = append(payload, nonce...)
	payload = v.aead.Seal(payload, nonce, []byte(token), tokenAAD(credentialID))
	if err := atomicVaultCreate(ctx, v.root, v.random, path, payload); err != nil {
		return fmt.Errorf("store Cloudflare token: %w", err)
	}
	return nil
}

// LoadCloudflareToken decrypts one exact credential envelope.
func (v *Vault) LoadCloudflareToken(ctx context.Context, credentialID string) (string, error) {
	if err := validateVaultOperation(ctx, v, credentialID); err != nil {
		return "", fmt.Errorf("load Cloudflare token: %w", ErrSecretInvalid)
	}
	payload, err := readVaultSecret(v.root, credentialTokenPath(credentialID), vaultSecretLimit)
	if err != nil || len(payload) < len(tokenEnvelopeMagic)+v.aead.NonceSize()+v.aead.Overhead() ||
		!equalBytes(payload[:len(tokenEnvelopeMagic)], tokenEnvelopeMagic[:]) {
		return "", fmt.Errorf("load Cloudflare token: %w", ErrSecretInvalid)
	}
	nonceStart := len(tokenEnvelopeMagic)
	nonceEnd := nonceStart + v.aead.NonceSize()
	plaintext, err := v.aead.Open(nil, payload[nonceStart:nonceEnd], payload[nonceEnd:], tokenAAD(credentialID))
	if err != nil || !validCloudflareTokenSecret(string(plaintext)) {
		return "", fmt.Errorf("load Cloudflare token: %w", ErrSecretInvalid)
	}
	return string(plaintext), nil
}

// DeleteCloudflareToken removes one exact credential envelope.
func (v *Vault) DeleteCloudflareToken(ctx context.Context, credentialID string) error {
	if err := validateVaultOperation(ctx, v, credentialID); err != nil {
		return fmt.Errorf("delete Cloudflare token: %w", ErrSecretInvalid)
	}
	path := credentialTokenPath(credentialID)
	if _, err := readVaultSecret(v.root, path, vaultSecretLimit); err != nil {
		return fmt.Errorf("delete Cloudflare token: %w", ErrSecretInvalid)
	}
	if err := v.root.Remove(path); err != nil {
		return fmt.Errorf("delete Cloudflare token: %w", err)
	}
	return syncVaultDirectory(v.root, "credentials")
}

// TokenFingerprint returns a non-reversible lowercase SHA-256 prefix for UI identification.
func TokenFingerprint(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:8])
}

func ensureVaultDirectory(root *os.Root, path string) error {
	information, err := root.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := root.Mkdir(path, vaultDirectoryMode); err != nil {
			return fmt.Errorf("create certificate vault directory: %w", err)
		}
		parent := filepath.ToSlash(filepath.Dir(path))
		if parent == "" {
			parent = "."
		}
		return syncVaultDirectory(root, parent)
	case err != nil:
		return fmt.Errorf("inspect certificate vault directory: %w", err)
	case information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() || information.Mode().Perm() != vaultDirectoryMode:
		return fmt.Errorf("inspect certificate vault directory: %w", ErrSecretInvalid)
	default:
		return nil
	}
}

func loadOrCreateMasterKey(ctx context.Context, root *os.Root, random io.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := readVaultSecret(root, "master.key", vaultMasterKeySize)
	if err == nil {
		if len(key) != vaultMasterKeySize {
			return nil, fmt.Errorf("load certificate vault master key: %w", ErrSecretInvalid)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load certificate vault master key: %w", ErrSecretInvalid)
	}
	key = make([]byte, vaultMasterKeySize)
	if _, err := io.ReadFull(random, key); err != nil {
		return nil, fmt.Errorf("generate certificate vault master key: %w", err)
	}
	if err := atomicVaultCreate(ctx, root, random, "master.key", key); err != nil {
		if existing, readErr := readVaultSecret(root, "master.key", vaultMasterKeySize); readErr == nil && len(existing) == vaultMasterKeySize {
			return existing, nil
		}
		return nil, fmt.Errorf("create certificate vault master key: %w", err)
	}
	return key, nil
}

func atomicVaultCreate(ctx context.Context, root *os.Root, random io.Reader, target string, payload []byte) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw := make([]byte, 8)
	if _, err := io.ReadFull(random, raw); err != nil {
		return err
	}
	directory := filepath.ToSlash(filepath.Dir(target))
	if directory == "." {
		directory = "."
	}
	temporary := filepath.ToSlash(filepath.Join(filepath.Dir(target), ".tmp-"+hex.EncodeToString(raw)))
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, vaultSecretMode)
	if err != nil {
		return err
	}
	owned := true
	defer func() {
		if owned {
			_ = root.Remove(temporary)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	information, err := root.Lstat(temporary)
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.Mode().IsRegular() || information.Mode().Perm() != vaultSecretMode {
		return ErrSecretInvalid
	}
	if err := root.Link(temporary, target); err != nil {
		return err
	}
	if err := root.Remove(temporary); err != nil {
		return err
	}
	owned = false
	return syncVaultDirectory(root, directory)
}

func readVaultSecret(root *os.Root, path string, limit int64) ([]byte, error) {
	information, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if information.Mode()&fs.ModeSymlink != 0 || !information.Mode().IsRegular() || information.Mode().Perm() != vaultSecretMode || information.Size() < 0 || information.Size() > limit {
		return nil, ErrSecretInvalid
	}
	file, err := root.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	stat, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, statErr, closeErr)
	}
	if int64(len(payload)) > limit || !os.SameFile(information, stat) || !stat.Mode().IsRegular() || stat.Mode().Perm() != vaultSecretMode {
		return nil, ErrSecretInvalid
	}
	return payload, nil
}

func syncVaultDirectory(root *os.Root, path string) error {
	directory, err := root.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func validateVaultOperation(ctx context.Context, vault *Vault, id string) error {
	if ctx == nil || vault == nil || vault.root == nil || vault.aead == nil || vault.random == nil || !validOpaqueID(id) {
		return ErrSecretInvalid
	}
	return ctx.Err()
}

func validOpaqueID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, character := range id {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validCloudflareTokenSecret(token string) bool {
	if token == "" || len(token) > 512 || token != strings.TrimSpace(token) {
		return false
	}
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func credentialTokenPath(id string) string {
	return "credentials/" + id + ".token"
}

func tokenAAD(id string) []byte {
	return []byte("nginx-uix:cloudflare-token:v1:" + id)
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
