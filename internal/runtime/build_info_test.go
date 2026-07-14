/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"
)

func TestParseBuildInfoPreservesOrderedQuotedArguments(t *testing.T) {
	contents, err := os.ReadFile("testdata/nginx_v.txt")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	info, err := parseBuildInfo(contents)
	if err != nil {
		t.Fatalf("parseBuildInfo() error = %v", err)
	}
	if got, want := info.Version, "1.30.3"; got != want {
		t.Fatalf("Version = %q, want %q", got, want)
	}
	if got, want := info.SbinPath, "/usr/sbin/nginx"; got != want {
		t.Fatalf("SbinPath = %q, want %q", got, want)
	}
	if got, want := info.PIDPath, "/run/nginx.pid"; got != want {
		t.Fatalf("PIDPath = %q, want %q", got, want)
	}
	wantArguments := []string{
		"--prefix=/etc/nginx",
		"--sbin-path=/usr/sbin/nginx",
		"--pid-path=/run/nginx.pid",
		"--with-cc-opt=-g -O2 -ffile-prefix-map=/build nginx=.",
		"--with-ld-opt=-Wl,-z,relro -Wl,-z,now",
		"--with-http_ssl_module",
	}
	if !slices.Equal(info.ConfigureArguments, wantArguments) {
		t.Fatalf("ConfigureArguments = %#v, want %#v", info.ConfigureArguments, wantArguments)
	}
}

func TestParseBuildInfoRejectsMalformedOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "missing version", output: "configure arguments: --pid-path=/run/nginx.pid --sbin-path=/usr/sbin/nginx\n"},
		{name: "missing configure arguments", output: "nginx version: nginx/1.30.3\n"},
		{name: "unterminated quote", output: "nginx version: nginx/1.30.3\nconfigure arguments: --pid-path='/run/nginx.pid\n"},
		{name: "missing pid path", output: "nginx version: nginx/1.30.3\nconfigure arguments: --sbin-path=/usr/sbin/nginx\n"},
		{name: "missing sbin path", output: "nginx version: nginx/1.30.3\nconfigure arguments: --pid-path=/run/nginx.pid\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseBuildInfo([]byte(test.output)); err == nil {
				t.Fatal("parseBuildInfo() error = nil, want malformed output rejection")
			}
		})
	}
}

func TestSplitPOSIXArgumentsPreservesUnescapedBackslashesInDoubleQuotes(t *testing.T) {
	arguments, err := splitPOSIXArguments("\"preserve\\q remove\\\"quote\"")
	if err != nil {
		t.Fatalf("splitPOSIXArguments() error = %v", err)
	}
	want := []string{`preserve\q remove"quote`}
	if !slices.Equal(arguments, want) {
		t.Fatalf("splitPOSIXArguments() = %#v, want %#v", arguments, want)
	}
}

func TestBuildInfoCachesOnlySuccessfulResult(t *testing.T) {
	contents, err := os.ReadFile("testdata/nginx_v.txt")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	calls := 0
	service := newServiceWithExecutor(func(_ context.Context, specification commandSpec) (commandResult, error) {
		calls++
		if specification.executable != nginxExecutable || !slices.Equal(specification.arguments, []string{"-V"}) || specification.timeout != 3*time.Second {
			t.Fatalf("unexpected build command specification: %+v", specification)
		}
		if calls == 1 {
			return commandResult{stderr: []byte("malformed")}, nil
		}
		return commandResult{stderr: contents}, nil
	})

	if _, err := service.BuildInfo(context.Background()); err == nil {
		t.Fatal("first BuildInfo() error = nil, want parse error")
	}
	first, err := service.BuildInfo(context.Background())
	if err != nil {
		t.Fatalf("second BuildInfo() error = %v", err)
	}
	first.ConfigureArguments[0] = "mutated"
	third, err := service.BuildInfo(context.Background())
	if err != nil {
		t.Fatalf("third BuildInfo() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("executor calls = %d, want 2", calls)
	}
	if third.ConfigureArguments[0] == "mutated" {
		t.Fatal("BuildInfo() returned mutable cached slice")
	}
}

func TestBuildInfoDoesNotCacheCommandFailure(t *testing.T) {
	calls := 0
	service := newServiceWithExecutor(func(context.Context, commandSpec) (commandResult, error) {
		calls++
		return commandResult{}, errors.New("temporary command failure")
	})
	for range 2 {
		if _, err := service.BuildInfo(context.Background()); err == nil {
			t.Fatal("BuildInfo() error = nil, want command failure")
		}
	}
	if calls != 2 {
		t.Fatalf("executor calls = %d, want retry on each failure", calls)
	}
}
