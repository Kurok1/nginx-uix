/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"slices"
	"time"
)

const (
	maximumCertificateChainLength = 10
	maximumCertificateChainBytes  = 2 << 20
	certificateClockTolerance     = 5 * time.Minute
)

// IssuedCertificate is validated, bounded public certificate material.
type IssuedCertificate struct {
	FullChainPEM    []byte
	LeafPEM         []byte
	LeafFingerprint string
	SerialNumber    string
	Issuer          string
	NotBefore       time.Time
	NotAfter        time.Time
}

// ValidateIssuedCertificate proves that one CA response exactly matches its plan and key.
func ValidateIssuedCertificate(
	chainDER [][]byte,
	key crypto.Signer,
	identifiers []string,
	now time.Time,
) (IssuedCertificate, error) {
	if key == nil || now.IsZero() || len(chainDER) == 0 || len(chainDER) > maximumCertificateChainLength {
		return IssuedCertificate{}, fmt.Errorf("validate issued certificate: %w", ErrCertificateInvalid)
	}
	expected, err := NormalizeIdentifiers(identifiers, ChallengeCloudflareDNS01)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("validate issued certificate: identifiers: %w", ErrCertificateInvalid)
	}
	totalBytes := 0
	certificates := make([]*x509.Certificate, len(chainDER))
	for index, raw := range chainDER {
		totalBytes += len(raw)
		if len(raw) == 0 || totalBytes > maximumCertificateChainBytes {
			return IssuedCertificate{}, fmt.Errorf("validate issued certificate: chain: %w", ErrCertificateInvalid)
		}
		certificate, parseErr := x509.ParseCertificate(raw)
		if parseErr != nil {
			return IssuedCertificate{}, fmt.Errorf("validate issued certificate: chain: %w", ErrCertificateInvalid)
		}
		certificates[index] = certificate
	}
	leaf := certificates[0]
	actual, err := NormalizeIdentifiers(leaf.DNSNames, ChallengeCloudflareDNS01)
	if err != nil || !slices.Equal(actual, expected) {
		return IssuedCertificate{}, fmt.Errorf("validate issued certificate: %w", ErrCertificateSANMismatch)
	}
	leafPublic, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("validate issued certificate: leaf key: %w", ErrCertificateInvalid)
	}
	stagedPublic, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil || !bytes.Equal(leafPublic, stagedPublic) {
		return IssuedCertificate{}, fmt.Errorf("validate issued certificate: %w", ErrCertificateKeyMismatch)
	}
	current := now.UTC()
	if leaf.IsCA || !leaf.BasicConstraintsValid || leaf.NotAfter.IsZero() || leaf.NotBefore.IsZero() ||
		!leaf.NotAfter.After(leaf.NotBefore) || current.Before(leaf.NotBefore.Add(-certificateClockTolerance)) ||
		!current.Before(leaf.NotAfter) || !allowsServerAuthentication(leaf.ExtKeyUsage) {
		return IssuedCertificate{}, fmt.Errorf("validate issued certificate: leaf policy: %w", ErrCertificateInvalid)
	}
	var fullChain bytes.Buffer
	for _, raw := range chainDER {
		if err := pem.Encode(&fullChain, &pem.Block{Type: "CERTIFICATE", Bytes: raw}); err != nil {
			return IssuedCertificate{}, fmt.Errorf("validate issued certificate: encode chain: %w", ErrCertificateInvalid)
		}
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: chainDER[0]})
	if len(leafPEM) == 0 {
		return IssuedCertificate{}, fmt.Errorf("validate issued certificate: encode leaf: %w", ErrCertificateInvalid)
	}
	fingerprint := sha256.Sum256(chainDER[0])
	serial := ""
	if leaf.SerialNumber != nil {
		serial = leaf.SerialNumber.Text(16)
	}
	return IssuedCertificate{
		FullChainPEM: fullChain.Bytes(), LeafPEM: leafPEM,
		LeafFingerprint: hex.EncodeToString(fingerprint[:]), SerialNumber: serial,
		Issuer: leaf.Issuer.String(), NotBefore: leaf.NotBefore.UTC(), NotAfter: leaf.NotAfter.UTC(),
	}, nil
}

func allowsServerAuthentication(usages []x509.ExtKeyUsage) bool {
	if len(usages) == 0 {
		return true
	}
	for _, usage := range usages {
		if usage == x509.ExtKeyUsageAny || usage == x509.ExtKeyUsageServerAuth {
			return true
		}
	}
	return false
}
