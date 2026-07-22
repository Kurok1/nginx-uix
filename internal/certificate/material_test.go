/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

func TestValidateIssuedCertificateRequiresExactSANAndMatchingKey(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := mustIssueLeaf(t, key, []string{"example.com", "www.example.com"}, now)
	material, err := ValidateIssuedCertificate([][]byte{der}, key, []string{"www.example.com", "example.com"}, now)
	if err != nil {
		t.Fatalf("ValidateIssuedCertificate() error = %v", err)
	}
	if material.NotAfter.IsZero() || len(material.FullChainPEM) == 0 || len(material.LeafPEM) == 0 || material.LeafFingerprint == "" {
		t.Fatalf("ValidateIssuedCertificate() returned incomplete material: %#v", material)
	}

	if _, err := ValidateIssuedCertificate([][]byte{der}, key, []string{"example.com"}, now); !errors.Is(err, ErrCertificateSANMismatch) {
		t.Fatalf("ValidateIssuedCertificate(SAN mismatch) error = %v", err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateIssuedCertificate([][]byte{der}, other, []string{"example.com", "www.example.com"}, now); !errors.Is(err, ErrCertificateKeyMismatch) {
		t.Fatalf("ValidateIssuedCertificate(key mismatch) error = %v", err)
	}
}

func mustIssueLeaf(t *testing.T, key *ecdsa.PrivateKey, names []string, now time.Time) []byte {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}
