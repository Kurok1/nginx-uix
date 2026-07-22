/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"fmt"
	"net"
	"slices"
	"strings"
)

const maxCertificateIdentifiers = 100

// NormalizeIdentifiers validates, canonicalizes and sorts one exact SAN set.
func NormalizeIdentifiers(input []string, challenge ChallengeType) ([]string, error) {
	if challenge != ChallengeHTTP01 && challenge != ChallengeCloudflareDNS01 {
		return nil, fmt.Errorf("normalize certificate identifiers: %w", ErrIdentifierInvalid)
	}
	if len(input) == 0 || len(input) > maxCertificateIdentifiers {
		return nil, fmt.Errorf("normalize certificate identifiers: %w", ErrIdentifierInvalid)
	}
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, raw := range input {
		identifier, wildcard, err := normalizeDNSIdentifier(raw)
		if err != nil {
			return nil, fmt.Errorf("normalize certificate identifiers: %w", err)
		}
		if wildcard && challenge != ChallengeCloudflareDNS01 {
			return nil, fmt.Errorf("normalize certificate identifiers: %w", ErrWildcardRequiresDNS)
		}
		if _, duplicate := seen[identifier]; duplicate {
			return nil, fmt.Errorf("normalize certificate identifiers: duplicate: %w", ErrIdentifierInvalid)
		}
		seen[identifier] = struct{}{}
		result = append(result, identifier)
	}
	slices.Sort(result)
	return result, nil
}

func normalizeDNSIdentifier(raw string) (string, bool, error) {
	if raw == "" || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\x00\r\n") {
		return "", false, ErrIdentifierInvalid
	}
	identifier := strings.ToLower(strings.TrimSuffix(raw, "."))
	if identifier == "" || len(identifier) > 253 || net.ParseIP(identifier) != nil {
		return "", false, ErrIdentifierInvalid
	}
	wildcard := strings.HasPrefix(identifier, "*.")
	if strings.ContainsRune(identifier, '*') {
		if !wildcard || strings.Count(identifier, "*") != 1 {
			return "", false, ErrIdentifierInvalid
		}
		identifier = strings.TrimPrefix(identifier, "*.")
	}
	if len(identifier) > 253 || !validASCIIDomain(identifier) {
		return "", false, ErrIdentifierInvalid
	}
	if wildcard {
		identifier = "*." + identifier
	}
	return identifier, wildcard, nil
}

func validASCIIDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			character := label[index]
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func baseIdentifier(identifier string) string {
	return strings.TrimPrefix(identifier, "*.")
}
