/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

// Package certificate owns ACME accounts, challenges, certificate material,
// Nginx bindings and renewal state without becoming a configuration source.
package certificate

import "errors"

var (
	// ErrIdentifierInvalid indicates that a DNS identifier cannot be safely requested.
	ErrIdentifierInvalid = errors.New("certificate identifier invalid")
	// ErrWildcardRequiresDNS indicates that HTTP-01 was selected for a wildcard identifier.
	ErrWildcardRequiresDNS = errors.New("certificate wildcard requires DNS challenge")
	// ErrSecretInvalid indicates an invalid, unsafe or corrupted secret envelope.
	ErrSecretInvalid = errors.New("certificate secret invalid")
	// ErrCloudflareTokenInvalid indicates a rejected or inactive Cloudflare API Token.
	ErrCloudflareTokenInvalid = errors.New("cloudflare token invalid")
	// ErrCloudflarePermission indicates that the Token cannot access a required resource.
	ErrCloudflarePermission = errors.New("cloudflare permission denied")
	// ErrCloudflareZoneNotFound indicates that no active accessible Zone owns an identifier.
	ErrCloudflareZoneNotFound = errors.New("cloudflare zone not found")
	// ErrCloudflareRateLimited indicates a bounded provider request was rate limited.
	ErrCloudflareRateLimited = errors.New("cloudflare rate limited")
	// ErrCloudflareUnavailable indicates a malformed or unavailable provider response.
	ErrCloudflareUnavailable = errors.New("cloudflare unavailable")
	// ErrCertificateInvalid indicates malformed or policy-incompatible certificate material.
	ErrCertificateInvalid = errors.New("issued certificate invalid")
	// ErrCertificateSANMismatch indicates that the CA response does not exactly match the plan.
	ErrCertificateSANMismatch = errors.New("issued certificate SAN mismatch")
	// ErrCertificateKeyMismatch indicates that the returned leaf does not use the staged key.
	ErrCertificateKeyMismatch = errors.New("issued certificate key mismatch")
	// ErrDNSPropagationTimeout indicates that no authoritative Zone server exposed the exact TXT value in time.
	ErrDNSPropagationTimeout = errors.New("certificate DNS propagation timeout")
	// ErrChallengeCleanupFailed indicates that an externally created challenge target cannot be proven removed.
	ErrChallengeCleanupFailed = errors.New("certificate challenge cleanup failed")
)

// ChallengeType is the fixed v0.5 domain-validation method.
type ChallengeType string

const (
	// ChallengeHTTP01 provisions exact Nginx HTTP challenge locations.
	ChallengeHTTP01 ChallengeType = "http_01"
	// ChallengeCloudflareDNS01 provisions exact Cloudflare TXT records using an API Token.
	ChallengeCloudflareDNS01 ChallengeType = "cloudflare_dns_01"
)
