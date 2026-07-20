/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"encoding/asn1"
	"path"
	"strings"
	"unicode/utf8"
)

const policyVersion uint16 = 1
const maxPolicyFileBytes = 2 << 20

const (
	// EntryRegular identifies a regular filesystem entry.
	EntryRegular EntryType = "regular"
	// EntryDirectory identifies a directory entry.
	EntryDirectory EntryType = "directory"
	// EntrySymlink identifies a symbolic link entry.
	EntrySymlink EntryType = "symlink"
	// EntrySpecial identifies a non-regular special entry.
	EntrySpecial EntryType = "special"

	// EntryManagedText is editable UTF-8 Nginx configuration text.
	EntryManagedText EntryClass = "managed_text"
	// EntrySensitiveMaterial is credential or authentication material.
	EntrySensitiveMaterial EntryClass = "sensitive_material"
	// EntryNotCandidate is a regular file outside the positive candidate set.
	EntryNotCandidate EntryClass = "not_candidate"
	// EntryInvalidText is content that cannot be safely managed as text.
	EntryInvalidText EntryClass = "invalid_text"
	// EntryFileLimit is content over the immutable single-file limit.
	EntryFileLimit EntryClass = "file_limit"
	// EntryDirectoryReadOnly is a navigable, read-only directory.
	EntryDirectoryReadOnly EntryClass = "directory"
	// EntrySymlinkInternal is a read-only symlink resolving inside the root.
	EntrySymlinkInternal EntryClass = "symlink_internal"
	// EntrySymlinkExternal is a read-only symlink resolving outside the root.
	EntrySymlinkExternal EntryClass = "symlink_external"
	// EntrySymlinkUnavailable is a broken, looping, or unavailable symlink.
	EntrySymlinkUnavailable EntryClass = "symlink_unavailable"
	// EntrySpecialReadOnly is a non-regular read-only entry.
	EntrySpecialReadOnly EntryClass = "special"
)

// Policy is the immutable managed-entry classification policy.
type Policy struct {
	version uint16
}

// NewPolicy returns the immutable v1 classification policy.
func NewPolicy() Policy {
	return Policy{version: policyVersion}
}

// Version returns the classification policy version.
func (Policy) Version() uint16 {
	return policyVersion
}

// IsPositiveCandidate reports whether a path may contain managed Nginx text.
func (Policy) IsPositiveCandidate(candidate RelativePath, included bool) bool {
	if included || candidate == "nginx.conf" {
		return true
	}
	base := path.Base(string(candidate))
	if strings.HasSuffix(base, ".conf") {
		return true
	}
	switch base {
	case "mime.types", "fastcgi_params", "fastcgi.conf", "uwsgi_params", "scgi_params", "koi-win", "koi-utf", "win-utf":
		return true
	default:
		return false
	}
}

// IsSensitiveDirective reports whether a directive references credential material.
func (Policy) IsSensitiveDirective(name string) bool {
	return isSensitiveDirectiveName(name)
}

// Classify returns a stable entry classification without returning content-derived data.
func (policy Policy) Classify(candidate RelativePath, content []byte, referencedSensitive, included bool) EntryClass {
	if referencedSensitive || hasSensitiveSuffix(candidate) {
		return EntrySensitiveMaterial
	}
	if len(content) > maxPolicyFileBytes {
		return EntryFileLimit
	}
	if hasCredentialSignature(content) {
		return EntrySensitiveMaterial
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 || hasBinaryControl(content) {
		return EntryInvalidText
	}
	if policy.IsPositiveCandidate(candidate, included) {
		return EntryManagedText
	}
	return EntryNotCandidate
}

func hasSensitiveSuffix(candidate RelativePath) bool {
	base := strings.ToLower(path.Base(string(candidate)))
	for _, suffix := range []string{
		".key", ".pem", ".crt", ".cer", ".der", ".p12", ".pfx", ".jks", ".keystore", ".htpasswd", ".passwd",
	} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func hasCredentialSignature(content []byte) bool {
	for _, marker := range [][]byte{
		[]byte("-----BEGIN PRIVATE KEY-----"),
		[]byte("-----BEGIN ENCRYPTED PRIVATE KEY-----"),
		[]byte("-----BEGIN RSA PRIVATE KEY-----"),
		[]byte("-----BEGIN EC PRIVATE KEY-----"),
		[]byte("-----BEGIN DSA PRIVATE KEY-----"),
		[]byte("-----BEGIN OPENSSH PRIVATE KEY-----"),
		[]byte("-----BEGIN CERTIFICATE-----"),
		[]byte("-----BEGIN TRUSTED CERTIFICATE-----"),
	} {
		if bytes.Contains(content, marker) {
			return true
		}
	}
	children, ok := derSequenceChildren(content)
	if !ok {
		return false
	}
	return isPKCS12(children) || isDERPrivateKey(children) || isDERCertificate(children)
}

func hasBinaryControl(content []byte) bool {
	for _, current := range content {
		if (current < 0x20 && current != '\t' && current != '\n' && current != '\r') || current == 0x7f {
			return true
		}
	}
	return false
}

func derSequenceChildren(content []byte) ([]asn1.RawValue, bool) {
	var outer asn1.RawValue
	rest, err := asn1.Unmarshal(content, &outer)
	if err != nil || len(rest) != 0 || !isUniversal(outer, asn1.TagSequence, true) {
		return nil, false
	}
	return rawValues(outer.Bytes)
}

func rawValues(content []byte) ([]asn1.RawValue, bool) {
	values := make([]asn1.RawValue, 0, 4)
	for len(content) != 0 {
		var value asn1.RawValue
		rest, err := asn1.Unmarshal(content, &value)
		if err != nil || len(rest) >= len(content) {
			return nil, false
		}
		values = append(values, value)
		content = rest
	}
	return values, true
}

func isPKCS12(children []asn1.RawValue) bool {
	if len(children) < 2 || len(children) > 3 {
		return false
	}
	version, ok := rawInteger(children[0])
	if !ok || version != 3 {
		return false
	}
	contentInfo, ok := sequenceValues(children[1])
	if !ok || len(contentInfo) != 2 {
		return false
	}
	oid, ok := rawOID(contentInfo[0])
	if !ok || !oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}) {
		return false
	}
	if contentInfo[1].Class != 2 || contentInfo[1].Tag != 0 || !contentInfo[1].IsCompound {
		return false
	}
	return len(children) != 3 || isUniversal(children[2], asn1.TagSequence, true)
}

func isDERPrivateKey(children []asn1.RawValue) bool {
	if isDERRSAPrivateKey(children) {
		return true
	}
	if len(children) == 3 {
		version, versionOK := rawInteger(children[0])
		algorithmOK := hasAlgorithmOID(children[1])
		return versionOK && (version == 0 || version == 1) && algorithmOK && isUniversal(children[2], asn1.TagOctetString, false)
	}
	if len(children) == 2 {
		algorithmOK := hasAlgorithmOID(children[0])
		return algorithmOK && isUniversal(children[1], asn1.TagOctetString, false)
	}
	return false
}

func isDERRSAPrivateKey(children []asn1.RawValue) bool {
	if len(children) != 9 && len(children) != 10 {
		return false
	}
	version, ok := rawInteger(children[0])
	if !ok || (version != 0 && version != 1) {
		return false
	}
	for _, value := range children[1:9] {
		if !isUniversal(value, asn1.TagInteger, false) {
			return false
		}
	}
	return len(children) == 9 || (version == 1 && isUniversal(children[9], asn1.TagSequence, true))
}

func isDERCertificate(children []asn1.RawValue) bool {
	if len(children) != 3 || !isUniversal(children[0], asn1.TagSequence, true) {
		return false
	}
	tbs, ok := rawValues(children[0].Bytes)
	if !ok || len(tbs) == 0 {
		return false
	}
	algorithmOK := hasAlgorithmOID(children[1])
	return algorithmOK && isUniversal(children[2], asn1.TagBitString, false)
}

func sequenceValues(value asn1.RawValue) ([]asn1.RawValue, bool) {
	if !isUniversal(value, asn1.TagSequence, true) {
		return nil, false
	}
	return rawValues(value.Bytes)
}

func hasAlgorithmOID(value asn1.RawValue) bool {
	values, ok := sequenceValues(value)
	if !ok || len(values) == 0 {
		return false
	}
	_, ok = rawOID(values[0])
	return ok
}

func rawInteger(value asn1.RawValue) (int, bool) {
	if !isUniversal(value, asn1.TagInteger, false) {
		return 0, false
	}
	var result int
	rest, err := asn1.Unmarshal(value.FullBytes, &result)
	return result, err == nil && len(rest) == 0
}

func rawOID(value asn1.RawValue) (asn1.ObjectIdentifier, bool) {
	if !isUniversal(value, asn1.TagOID, false) {
		return nil, false
	}
	var result asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(value.FullBytes, &result)
	return result, err == nil && len(rest) == 0
}

func isUniversal(value asn1.RawValue, tag int, compound bool) bool {
	return value.Class == 0 && value.Tag == tag && value.IsCompound == compound
}
