/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"encoding/asn1"
	"testing"
)

func TestPolicyClassifiesSensitiveMaterialBeforeManagedText(t *testing.T) {
	policy := NewPolicy()
	tests := []struct {
		path       string
		content    string
		referenced bool
		want       EntryClass
	}{
		{path: "nginx.conf", content: "events {}", want: EntryManagedText},
		{path: "conf.d/site.conf", content: "server {}", want: EntryManagedText},
		{path: "mime.types", content: "types {}", want: EntryManagedText},
		{path: "secrets/server.key", content: "not a key", want: EntrySensitiveMaterial},
		{path: "conf.d/embedded.conf", content: "-----BEGIN PRIVATE KEY-----\n", want: EntrySensitiveMaterial},
		{path: "users.db", content: "admin:x", referenced: true, want: EntrySensitiveMaterial},
		{path: "notes.txt", content: "text", want: EntryNotCandidate},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			path, err := ParseRelativePath(test.path, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			got := policy.Classify(path, []byte(test.content), test.referenced, false)
			if got != test.want {
				t.Fatalf("Classify(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestPolicyVersionAndPositiveCandidatesAreStable(t *testing.T) {
	policy := NewPolicy()
	if policy.Version() != 1 {
		t.Fatalf("Version() = %d, want 1", policy.Version())
	}
	tests := []struct {
		path     string
		included bool
		want     bool
	}{
		{path: "nginx.conf", want: true},
		{path: "nested/nginx.conf", want: true},
		{path: "nested/nginx", want: false},
		{path: "modules/fastcgi_params", want: true},
		{path: "modules/fastcgi.conf", want: true},
		{path: "modules/uwsgi_params", want: true},
		{path: "modules/scgi_params", want: true},
		{path: "modules/koi-win", want: true},
		{path: "modules/koi-utf", want: true},
		{path: "modules/win-utf", want: true},
		{path: "notes.txt", included: true, want: true},
		{path: "notes.txt", want: false},
	}
	for _, test := range tests {
		path := mustPolicyPath(t, test.path)
		if got := policy.IsPositiveCandidate(path, test.included); got != test.want {
			t.Fatalf("IsPositiveCandidate(%q, %t) = %t, want %t", test.path, test.included, got, test.want)
		}
	}
}

func TestPolicyZeroValueReportsImmutableVersion(t *testing.T) {
	if got := (Policy{}).Version(); got != 1 {
		t.Fatalf("Policy{}.Version() = %d, want 1", got)
	}
}

func TestPolicyClassifiesEverySensitiveSuffix(t *testing.T) {
	policy := NewPolicy()
	for _, suffix := range []string{
		".key", ".pem", ".crt", ".cer", ".der", ".p12", ".pfx", ".jks", ".keystore", ".htpasswd", ".passwd",
	} {
		path := mustPolicyPath(t, "secrets/material"+suffix)
		if got := policy.Classify(path, []byte("ordinary text"), false, false); got != EntrySensitiveMaterial {
			t.Fatalf("Classify(%q) = %q, want %q", path, got, EntrySensitiveMaterial)
		}
	}
}

func TestPolicyRecognizesSensitiveDirectiveSet(t *testing.T) {
	policy := NewPolicy()
	for _, name := range []string{
		"ssl_password_file",
		"auth_basic_user_file",
		"ssl_certificate",
		"ssl_certificate_key",
		"ssl_trusted_certificate",
		"ssl_crl",
	} {
		if !policy.IsSensitiveDirective(name) {
			t.Fatalf("IsSensitiveDirective(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"include", "certificate", "SSL_CERTIFICATE"} {
		if policy.IsSensitiveDirective(name) {
			t.Fatalf("IsSensitiveDirective(%q) = true, want false", name)
		}
	}
}

func TestPolicyClassifiesBoundedCredentialSignatures(t *testing.T) {
	policy := NewPolicy()
	tests := []struct {
		name    string
		content []byte
	}{
		{name: "pem private key", content: []byte("-----BEGIN RSA PRIVATE KEY-----\n")},
		{name: "openssh private key", content: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n")},
		{name: "x509 certificate", content: []byte("-----BEGIN CERTIFICATE-----\n")},
		{name: "pkcs12", content: mustPKCS12Fixture(t)},
		{name: "der private key", content: mustDERPrivateKeyFixture(t)},
		{name: "der rsa private key", content: mustDERRSAPrivateKeyFixture(t)},
		{name: "der certificate", content: mustDERCertificateFixture(t)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := mustPolicyPath(t, "conf.d/embedded.conf")
			if got := policy.Classify(path, test.content, false, false); got != EntrySensitiveMaterial {
				t.Fatalf("Classify() = %q, want %q", got, EntrySensitiveMaterial)
			}
		})
	}
}

func TestPolicyRejectsInvalidOrOversizedText(t *testing.T) {
	policy := NewPolicy()
	path := mustPolicyPath(t, "conf.d/site.conf")
	tests := []struct {
		name    string
		content []byte
		want    EntryClass
	}{
		{name: "invalid utf8", content: []byte{0xff, 0xfe}, want: EntryInvalidText},
		{name: "nul", content: []byte("server {}\x00"), want: EntryInvalidText},
		{name: "ambiguous asn1", content: []byte{0x30, 0x03, 0x02, 0x01, 0x01}, want: EntryInvalidText},
		{name: "file limit", content: bytes.Repeat([]byte{'a'}, (2<<20)+1), want: EntryFileLimit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := policy.Classify(path, test.content, false, false); got != test.want {
				t.Fatalf("Classify() = %q, want %q", got, test.want)
			}
		})
	}
	oversizedKey := mustPolicyPath(t, "secrets/server.key")
	if got := policy.Classify(oversizedKey, bytes.Repeat([]byte{'a'}, (2<<20)+1), false, false); got != EntrySensitiveMaterial {
		t.Fatalf("Classify(sensitive oversized) = %q, want %q", got, EntrySensitiveMaterial)
	}
}

type testAlgorithmIdentifier struct {
	Algorithm asn1.ObjectIdentifier
}

func mustPKCS12Fixture(t *testing.T) []byte {
	t.Helper()
	fixture := struct {
		Version  int
		AuthSafe struct {
			ContentType asn1.ObjectIdentifier
			Content     asn1.RawValue `asn1:"optional"`
		}
	}{Version: 3}
	fixture.AuthSafe.ContentType = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	fixture.AuthSafe.Content = asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: []byte{0x04, 0x00}}
	return mustASN1Marshal(t, fixture)
}

func mustDERPrivateKeyFixture(t *testing.T) []byte {
	t.Helper()
	fixture := struct {
		Version    int
		Algorithm  testAlgorithmIdentifier
		PrivateKey []byte
	}{Version: 0, Algorithm: testAlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}}, PrivateKey: []byte{1, 2, 3}}
	return mustASN1Marshal(t, fixture)
}

func mustDERRSAPrivateKeyFixture(t *testing.T) []byte {
	t.Helper()
	fixture := struct {
		Version         int
		Modulus         int
		PublicExponent  int
		PrivateExponent int
		Prime1          int
		Prime2          int
		Exponent1       int
		Exponent2       int
		Coefficient     int
	}{0, 3233, 17, 2753, 61, 53, 53, 49, 38}
	return mustASN1Marshal(t, fixture)
}

func mustDERCertificateFixture(t *testing.T) []byte {
	t.Helper()
	fixture := struct {
		TBSCertificate asn1.RawValue
		Algorithm      testAlgorithmIdentifier
		Signature      asn1.BitString
	}{
		TBSCertificate: asn1.RawValue{Class: 0, Tag: 16, IsCompound: true, Bytes: []byte{0x02, 0x01, 0x01}},
		Algorithm:      testAlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}},
		Signature:      asn1.BitString{Bytes: []byte{1}, BitLength: 8},
	}
	return mustASN1Marshal(t, fixture)
}

func mustASN1Marshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := asn1.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustPolicyPath(t *testing.T, raw string) RelativePath {
	t.Helper()
	path, err := ParseRelativePath(raw, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return path
}
