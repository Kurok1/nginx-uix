/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestStableDomainErrors(t *testing.T) {
	tests := []error{ErrEntryNotManaged, ErrSnapshotChanged, ErrConflict}
	for _, target := range tests {
		if target == nil {
			t.Fatal("stable domain error is nil")
		}
		if err := fmt.Errorf("wrapped: %w", target); !errors.Is(err, target) {
			t.Fatalf("errors.Is(%v, %v) = false", err, target)
		}
	}
}

func TestParseWorkspaceID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "opaque lowercase hex", raw: "0123456789abcdef0123456789abcdef", ok: true},
		{name: "short", raw: "0123", ok: false},
		{name: "uppercase", raw: "0123456789ABCDEF0123456789ABCDEF", ok: false},
		{name: "separator", raw: "0123456789abcdef/123456789abcdef", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseWorkspaceID(test.raw)
			if (err == nil) != test.ok {
				t.Fatalf("ParseWorkspaceID() error = %v, want ok %v", err, test.ok)
			}
		})
	}
}

func TestDefaultLimitsAreReleaseContract(t *testing.T) {
	limits := DefaultLimits()
	if limits.MaxFileBytes != 2<<20 || limits.MaxEntries != 4096 || limits.MaxManagedBytes != 32<<20 ||
		limits.MaxPathBytes != 1024 || limits.MaxPathDepth != 64 || limits.MaxPathComponentBytes != 255 ||
		limits.MaxWorkspaces != 8 || limits.MaxWorkspaceBytes != 512<<20 || limits.MaxGroups != 128 ||
		limits.MaxGroupMembers != 1024 || limits.MaxTotalGroupMembers != 4096 || limits.MaxDiffResponseBytes != 4<<20 ||
		limits.MaxSearchMatches != 500 || limits.MaxSearchQueryBytes != 256 || limits.MaxIncludeTokenBytes != 64<<10 ||
		limits.MaxIncludeDirectiveBytes != 256<<10 || limits.MaxIncludeEdges != 16384 || limits.MaxIncludeDepth != 64 {
		t.Fatal("DefaultLimits() changed the v0.2.1 contract")
	}
}

func TestOpaqueIDGeneration(t *testing.T) {
	random := make([]byte, 16)
	for index := range random {
		random[index] = byte(index)
	}

	workspaceID, err := NewWorkspaceID(bytes.NewReader(random))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(workspaceID), "000102030405060708090a0b0c0d0e0f"; got != want {
		t.Fatalf("NewWorkspaceID() = %q, want %q", got, want)
	}

	groupID, err := NewGroupID(bytes.NewReader(random))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(groupID), "000102030405060708090a0b0c0d0e0f"; got != want {
		t.Fatalf("NewGroupID() = %q, want %q", got, want)
	}

	if _, err := NewWorkspaceID(bytes.NewReader(random[:15])); err == nil {
		t.Fatal("NewWorkspaceID(short reader) error = nil")
	}
	if _, err := NewGroupID(bytes.NewReader(random[:15])); err == nil {
		t.Fatal("NewGroupID(short reader) error = nil")
	}
}

func TestParseGroupID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "opaque lowercase hex", raw: "fedcba9876543210fedcba9876543210", ok: true},
		{name: "short", raw: "fedcba98", ok: false},
		{name: "uppercase", raw: "FEDCBA9876543210FEDCBA9876543210", ok: false},
		{name: "separator", raw: "fedcba9876543210/fedcba987654321", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseGroupID(test.raw)
			if (err == nil) != test.ok {
				t.Fatalf("ParseGroupID() error = %v, want ok %v", err, test.ok)
			}
		})
	}
}

func TestETagAndDigest(t *testing.T) {
	rawDigest := strings.Repeat("ab", 32)
	digest, err := ParseDigest(rawDigest)
	if err != nil {
		t.Fatal(err)
	}
	if got := digest.String(); got != rawDigest {
		t.Fatalf("Digest.String() = %q, want %q", got, rawDigest)
	}

	tests := []struct {
		name   string
		raw    string
		prefix string
		ok     bool
	}{
		{name: "draft", raw: `"draft-v1:` + rawDigest + `"`, prefix: "draft-v1:", ok: true},
		{name: "group", raw: `"groups-v1:` + rawDigest + `"`, prefix: "groups-v1:", ok: true},
		{name: "old draft prefix", raw: `"draft-` + rawDigest + `"`, prefix: "draft-v1:", ok: false},
		{name: "old group prefix", raw: `"groups-` + rawDigest + `"`, prefix: "groups-v1:", ok: false},
		{name: "weak", raw: `W/"draft-v1:` + rawDigest + `"`, prefix: "draft-v1:", ok: false},
		{name: "comma", raw: `"draft-v1:` + rawDigest + `", "other"`, prefix: "draft-v1:", ok: false},
		{name: "leading whitespace", raw: ` "draft-v1:` + rawDigest + `"`, prefix: "draft-v1:", ok: false},
		{name: "trailing whitespace", raw: `"draft-v1:` + rawDigest + `" `, prefix: "draft-v1:", ok: false},
		{name: "wrong prefix", raw: `"groups-v1:` + rawDigest + `"`, prefix: "draft-v1:", ok: false},
		{name: "uppercase digest", raw: `"draft-v1:` + strings.ToUpper(rawDigest) + `"`, prefix: "draft-v1:", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, parseErr := ParseStrongETag(test.raw, test.prefix)
			if (parseErr == nil) != test.ok {
				t.Fatalf("ParseStrongETag() error = %v, want ok %v", parseErr, test.ok)
			}
			if test.ok && parsed != digest {
				t.Fatalf("ParseStrongETag() = %q, want %q", parsed, digest)
			}
		})
	}

	if got, want := DraftETag(digest), `"draft-v1:`+rawDigest+`"`; got != want {
		t.Fatalf("DraftETag() = %q, want %q", got, want)
	}
	if got, want := GroupETag(digest), `"groups-v1:`+rawDigest+`"`; got != want {
		t.Fatalf("GroupETag() = %q, want %q", got, want)
	}

	invalidDigests := []string{"", strings.Repeat("0", 63), strings.Repeat("A", 64), strings.Repeat("g", 64)}
	for _, raw := range invalidDigests {
		if _, err := ParseDigest(raw); !errors.Is(err, ErrDigestInvalid) {
			t.Errorf("ParseDigest(%q) error = %v, want ErrDigestInvalid", raw, err)
		}
	}
}

func TestValidateDisplayName(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "trim", raw: "  Production sites  ", want: "Production sites", ok: true},
		{name: "unicode", raw: "站点组", want: "站点组", ok: true},
		{name: "empty", raw: "   ", ok: false},
		{name: "invalid utf8", raw: string([]byte{0xff}), ok: false},
		{name: "embedded control", raw: "sites\nproduction", ok: false},
		{name: "too many runes", raw: strings.Repeat("界", 129), ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateDisplayName(test.raw)
			if (err == nil) != test.ok {
				t.Fatalf("ValidateDisplayName() error = %v, want ok %v", err, test.ok)
			}
			if test.ok && got != test.want {
				t.Fatalf("ValidateDisplayName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeGroupName(t *testing.T) {
	display, normalized, err := NormalizeGroupName("  Production Sites  ")
	if err != nil {
		t.Fatal(err)
	}
	if display != "Production Sites" || normalized != "production sites" {
		t.Fatalf("NormalizeGroupName() = %q, %q", display, normalized)
	}
}
