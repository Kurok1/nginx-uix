/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRelativePath(t *testing.T) {
	component := strings.Repeat("a", 255)
	tooLongPath := strings.Join([]string{component, component, component, component, "b"}, "/")
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "root file", raw: "nginx.conf", ok: true},
		{name: "nested file", raw: "conf.d/site.conf", ok: true},
		{name: "unicode", raw: "站点/默认.conf", ok: true},
		{name: "empty", raw: "", ok: false},
		{name: "leading slash", raw: "/nginx.conf", ok: false},
		{name: "trailing slash", raw: "conf.d/", ok: false},
		{name: "double slash", raw: "conf.d//site.conf", ok: false},
		{name: "dot", raw: ".", ok: false},
		{name: "nested dot", raw: "conf.d/./site.conf", ok: false},
		{name: "dot dot", raw: "..", ok: false},
		{name: "nested dot dot", raw: "conf.d/../nginx.conf", ok: false},
		{name: "windows separator", raw: `conf.d\site.conf`, ok: false},
		{name: "nul", raw: "conf.d/site\x00.conf", ok: false},
		{name: "invalid utf8", raw: string([]byte{'a', '/', 0xff}), ok: false},
		{name: "65 levels", raw: strings.Repeat("a/", 64) + "a", ok: false},
		{name: "1025 bytes", raw: tooLongPath, ok: false},
		{name: "256 byte component", raw: strings.Repeat("a", 256), ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseRelativePath(test.raw, DefaultLimits())
			if (err == nil) != test.ok {
				t.Fatalf("ParseRelativePath() error = %v, want ok %v", err, test.ok)
			}
			if test.ok && string(got) != test.raw {
				t.Fatalf("ParseRelativePath() = %q, want %q", got, test.raw)
			}
			if !test.ok {
				if !errors.Is(err, ErrPathInvalid) {
					t.Fatalf("ParseRelativePath() error = %v, want ErrPathInvalid", err)
				}
				var pathError *PathError
				if !errors.As(err, &pathError) {
					t.Fatalf("ParseRelativePath() error type = %T, want *PathError", err)
				}
			}
		})
	}
}

func TestParseRelativePathUsesFilesystemComponentLimit(t *testing.T) {
	if _, err := parseRelativePath("abcde", DefaultLimits(), 4); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("parseRelativePath() error = %v, want ErrPathInvalid", err)
	}
	if _, err := parseRelativePath("abcd", DefaultLimits(), 4); err != nil {
		t.Fatalf("parseRelativePath() error = %v", err)
	}
	if _, err := parseRelativePath("a", DefaultLimits(), 0); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("parseRelativePath(nonpositive limit) error = %v, want ErrPathInvalid", err)
	}
}
