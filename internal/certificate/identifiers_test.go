/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"errors"
	"slices"
	"testing"
)

func TestNormalizeIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []string
		challenge ChallengeType
		want      []string
		wantErr   error
	}{
		{name: "canonical SANs", input: []string{"WWW.Example.COM.", "example.com"}, challenge: ChallengeHTTP01, want: []string{"example.com", "www.example.com"}},
		{name: "punycode", input: []string{"xn--bcher-kva.example"}, challenge: ChallengeHTTP01, want: []string{"xn--bcher-kva.example"}},
		{name: "wildcard DNS", input: []string{"*.Example.com", "example.com"}, challenge: ChallengeCloudflareDNS01, want: []string{"*.example.com", "example.com"}},
		{name: "wildcard HTTP", input: []string{"*.example.com"}, challenge: ChallengeHTTP01, wantErr: ErrWildcardRequiresDNS},
		{name: "duplicate", input: []string{"example.com", "EXAMPLE.COM."}, challenge: ChallengeHTTP01, wantErr: ErrIdentifierInvalid},
		{name: "IP", input: []string{"192.0.2.1"}, challenge: ChallengeHTTP01, wantErr: ErrIdentifierInvalid},
		{name: "misplaced wildcard", input: []string{"www.*.example.com"}, challenge: ChallengeCloudflareDNS01, wantErr: ErrIdentifierInvalid},
		{name: "unicode requires ASCII form", input: []string{"bücher.example"}, challenge: ChallengeCloudflareDNS01, wantErr: ErrIdentifierInvalid},
		{name: "empty", input: nil, challenge: ChallengeHTTP01, wantErr: ErrIdentifierInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeIdentifiers(test.input, test.challenge)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("NormalizeIdentifiers() error = %v, want %v", err, test.wantErr)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("NormalizeIdentifiers() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNormalizeIdentifiersRejectsMoreThanOneHundredNames(t *testing.T) {
	t.Parallel()

	identifiers := make([]string, 101)
	for index := range identifiers {
		identifiers[index] = string(rune('a'+index%26)) + "-" + string(rune('a'+index/26)) + ".example.com"
	}
	if _, err := NormalizeIdentifiers(identifiers, ChallengeCloudflareDNS01); !errors.Is(err, ErrIdentifierInvalid) {
		t.Fatalf("NormalizeIdentifiers() error = %v, want %v", err, ErrIdentifierInvalid)
	}
}
