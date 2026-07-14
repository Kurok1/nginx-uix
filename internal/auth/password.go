/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	passwordFileLimit = 4096
	argonMemory       = 19456
	argonIterations   = 2
	argonParallelism  = 1
	argonSaltLength   = 16
	argonKeyLength    = 32
)

// ReadPassword reads the preferred Secret file or the environment fallback.
func ReadPassword(ctx context.Context, filePath, fallback string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read bootstrap password: %w", err)
	}

	var contents []byte
	if filePath == "" {
		contents = []byte(fallback)
	} else {
		file, err := openPasswordFile(filePath)
		if err != nil {
			return "", err
		}
		contents, err = io.ReadAll(io.LimitReader(file, passwordFileLimit+1))
		if err != nil {
			// Closing after a failed read is best effort; the read error is the actionable cause.
			_ = file.Close()
			return "", fmt.Errorf("read bootstrap password file: %w", err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("close bootstrap password file: %w", err)
		}
		if len(contents) > passwordFileLimit {
			return "", fmt.Errorf("read bootstrap password file: exceeds 4096 bytes")
		}
		if bytes.HasSuffix(contents, []byte("\r\n")) {
			contents = contents[:len(contents)-2]
		} else if bytes.HasSuffix(contents, []byte("\n")) {
			contents = contents[:len(contents)-1]
		}
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read bootstrap password: %w", err)
	}

	password := string(contents)
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	return password, nil
}

// NormalizeUsername validates and ASCII-case-folds an administrator name.
func NormalizeUsername(username string) (string, error) {
	if len(username) < 3 || len(username) > 64 {
		return "", fmt.Errorf("validate username: length must be between 3 and 64 characters")
	}
	if strings.TrimSpace(username) != username {
		return "", fmt.Errorf("validate username: leading or trailing whitespace is not allowed")
	}
	for _, character := range []byte(username) {
		if character < 0x20 || character > 0x7e {
			return "", fmt.Errorf("validate username: printable ASCII characters required")
		}
	}
	return strings.ToLower(username), nil
}

// ValidatePassword enforces the bootstrap password boundary in Unicode runes.
func ValidatePassword(password string) error {
	if !utf8.ValidString(password) {
		return fmt.Errorf("validate password: valid UTF-8 required")
	}
	if strings.IndexByte(password, 0) >= 0 {
		return fmt.Errorf("validate password: NUL is not allowed")
	}
	length := utf8.RuneCountInString(password)
	if length < 12 || length > 128 {
		return fmt.Errorf("validate password: length must be between 12 and 128 characters")
	}
	return nil
}

// HashPassword encodes a password with the locked Argon2id profile.
func HashPassword(random io.Reader, password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLength)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword strictly decodes the locked PHC form and compares in constant time.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false, fmt.Errorf("decode password hash: unsupported PHC parameters")
	}
	strictEncoding := base64.RawStdEncoding.Strict()
	salt, err := strictEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltLength {
		return false, fmt.Errorf("decode password hash: invalid salt")
	}
	expected, err := strictEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != argonKeyLength {
		return false, fmt.Errorf("decode password hash: invalid key")
	}
	actual := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func openPasswordFile(path string) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bootstrap password file: %w", err)
	}
	return file, nil
}
