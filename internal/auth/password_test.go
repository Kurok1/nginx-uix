/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPasswordUsesFileBeforeEnvironmentFallback(t *testing.T) {
	tests := []struct {
		name     string
		contents []byte
		want     string
	}{
		{name: "no newline", contents: []byte("file-password-123"), want: "file-password-123"},
		{name: "one LF", contents: []byte("file-password-123\n"), want: "file-password-123"},
		{name: "one CRLF", contents: []byte("file-password-123\r\n"), want: "file-password-123"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writePasswordFile(t, test.contents)
			got, err := ReadPassword(context.Background(), path, "environment-password")
			if err != nil {
				t.Fatalf("ReadPassword() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("ReadPassword() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReadPasswordRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name     string
		contents []byte
	}{
		{name: "empty", contents: nil},
		{name: "only newline", contents: []byte("\n")},
		{name: "NUL", contents: []byte("valid-password\x00")},
		{name: "invalid UTF-8", contents: []byte{0xff, 0xfe, 0xfd}},
		{name: "more than 4096 bytes", contents: bytes.Repeat([]byte{'a'}, 4097)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ReadPassword(context.Background(), writePasswordFile(t, test.contents), "ignored"); err == nil {
				t.Fatal("ReadPassword() error = nil, want rejection")
			}
		})
	}
}

func TestValidatePasswordUsesUnicodeRuneBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "11 runes", password: strings.Repeat("界", 11), wantErr: true},
		{name: "12 runes", password: strings.Repeat("界", 12)},
		{name: "128 runes", password: strings.Repeat("密", 128)},
		{name: "129 runes", password: strings.Repeat("密", 129), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePassword(test.password)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePassword() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestNormalizeUsernameEnforcesPrintableASCIIAndWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		username string
		want     string
		wantErr  bool
	}{
		{name: "case folded", username: "Operator", want: "operator"},
		{name: "interior space", username: "ops admin", want: "ops admin"},
		{name: "three characters", username: "Ops", want: "ops"},
		{name: "64 characters", username: strings.Repeat("A", 64), want: strings.Repeat("a", 64)},
		{name: "two characters", username: "ab", wantErr: true},
		{name: "65 characters", username: strings.Repeat("a", 65), wantErr: true},
		{name: "leading whitespace", username: " operator", wantErr: true},
		{name: "trailing whitespace", username: "operator ", wantErr: true},
		{name: "control character", username: "ops\tadmin", wantErr: true},
		{name: "non ASCII", username: "管理员", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeUsername(test.username)
			if (err != nil) != test.wantErr {
				t.Fatalf("NormalizeUsername() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("NormalizeUsername() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHashPasswordUsesLockedArgon2idParameters(t *testing.T) {
	random := bytes.NewReader(bytes.Repeat([]byte{0x5a}, 16))
	encoded, err := HashPassword(random, "correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("HashPassword() = %q, want locked PHC prefix", encoded)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		t.Fatalf("PHC segment count = %d, want 6", len(parts))
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		t.Fatalf("salt = %x, error = %v, want 16 bytes", salt, err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) != 32 {
		t.Fatalf("key length = %d, error = %v, want 32", len(key), err)
	}

	verified, err := VerifyPassword(encoded, "correct horse battery staple")
	if err != nil || !verified {
		t.Fatalf("VerifyPassword(correct) = %v, %v", verified, err)
	}
	verified, err = VerifyPassword(encoded, "wrong password")
	if err != nil || verified {
		t.Fatalf("VerifyPassword(wrong) = %v, %v", verified, err)
	}
}

func TestVerifyPasswordRejectsMalformedOrUnboundedPHC(t *testing.T) {
	tests := []string{
		"not-a-phc",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=1048576,t=2,p=1$WlpaWlpaWlpaWlpaWlpaWg$WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo",
	}
	for _, encoded := range tests {
		if _, err := VerifyPassword(encoded, "password"); err == nil {
			t.Errorf("VerifyPassword(%q) error = nil, want strict decoder rejection", encoded)
		}
	}
}

func writePasswordFile(t *testing.T, contents []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
