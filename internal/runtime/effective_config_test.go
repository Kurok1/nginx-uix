/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseEffectiveConfigPreservesOrderedRepeatedOccurrences(t *testing.T) {
	contents := readEffectiveConfigFixture(t)

	configuration, err := parseEffectiveConfig(contents)
	if err != nil {
		t.Fatalf("parseEffectiveConfig() error = %v", err)
	}
	want := []ConfigOccurrence{
		{
			ID:        "occurrence-000001",
			LoadOrder: 1,
			Path:      "/etc/nginx/nginx.conf",
			Content:   "events {}\nhttp {\n    include /etc/nginx/conf.d/repeated.conf;\n    include /etc/nginx/conf.d/empty.conf;\n    include /etc/nginx/conf.d/repeated.conf;\n}\n",
		},
		{
			ID:        "occurrence-000002",
			LoadOrder: 2,
			Path:      "/etc/nginx/conf.d/repeated.conf",
			Content:   "server { listen 8080; }\n",
		},
		{
			ID:        "occurrence-000003",
			LoadOrder: 3,
			Path:      "/etc/nginx/conf.d/empty.conf",
			Content:   "",
		},
		{
			ID:        "occurrence-000004",
			LoadOrder: 4,
			Path:      "/etc/nginx/conf.d/repeated.conf",
			Content:   "server { listen 8081; }\n",
		},
	}
	if !slices.Equal(configuration.Occurrences, want) {
		t.Fatalf("Occurrences = %#v, want %#v", configuration.Occurrences, want)
	}
}

func TestParseEffectiveConfigAcceptsCRLFAndEmptyBodies(t *testing.T) {
	output := []byte("nginx preamble\r\n# configuration file /first.conf:\r\nline;\r\n# configuration file /empty.conf:\r\n")

	configuration, err := parseEffectiveConfig(output)
	if err != nil {
		t.Fatalf("parseEffectiveConfig() error = %v", err)
	}
	want := []ConfigOccurrence{
		{ID: "occurrence-000001", LoadOrder: 1, Path: "/first.conf", Content: "line;\n"},
		{ID: "occurrence-000002", LoadOrder: 2, Path: "/empty.conf", Content: ""},
	}
	if !slices.Equal(configuration.Occurrences, want) {
		t.Fatalf("Occurrences = %#v, want %#v", configuration.Occurrences, want)
	}
}

func TestParseEffectiveConfigRejectsMalformedMarkers(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "missing marker", output: "nginx preamble only\n"},
		{name: "missing colon", output: "# configuration file /etc/nginx/nginx.conf\n"},
		{name: "empty path", output: "# configuration file :\n"},
		{name: "trailing marker data", output: "# configuration file /etc/nginx/nginx.conf: unexpected\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseEffectiveConfig([]byte(test.output)); err == nil {
				t.Fatal("parseEffectiveConfig() error = nil, want malformed marker rejection")
			}
		})
	}
}

func TestEffectiveConfigUsesFixedCommandAndDoesNotCacheCompletedResults(t *testing.T) {
	contents := readEffectiveConfigFixture(t)
	var calls atomic.Int32
	service := newServiceWithExecutor(func(ctx context.Context, specification commandSpec) (commandResult, error) {
		if err := validateEffectiveConfigSpecification(ctx, specification); err != nil {
			return commandResult{}, err
		}
		calls.Add(1)
		return commandResult{stdout: contents}, nil
	})

	first, err := service.EffectiveConfig(context.Background())
	if err != nil {
		t.Fatalf("first EffectiveConfig() error = %v", err)
	}
	first.Occurrences[0].Path = "mutated"
	second, err := service.EffectiveConfig(context.Background())
	if err != nil {
		t.Fatalf("second EffectiveConfig() error = %v", err)
	}
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("executor calls = %d, want %d", got, want)
	}
	if second.Occurrences[0].Path == "mutated" {
		t.Fatal("EffectiveConfig() retained a completed result in memory")
	}
}

func TestEffectiveConfigCallerCancellationDoesNotCancelSharedExecution(t *testing.T) {
	contents := readEffectiveConfigFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	internalCanceled := make(chan error, 1)
	var calls atomic.Int32
	service := newServiceWithExecutor(func(ctx context.Context, specification commandSpec) (commandResult, error) {
		if err := validateEffectiveConfigSpecification(ctx, specification); err != nil {
			return commandResult{}, err
		}
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return commandResult{stdout: contents}, nil
		case <-ctx.Done():
			internalCanceled <- ctx.Err()
			return commandResult{}, ctx.Err()
		}
	})

	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan effectiveConfigCall, 1)
	go func() {
		configuration, err := service.EffectiveConfig(firstContext)
		firstResult <- effectiveConfigCall{configuration: configuration, err: err}
	}()
	<-started

	secondWaiting := make(chan struct{})
	secondContext := &doneSignalingContext{Context: context.Background(), signaled: secondWaiting}
	secondResult := make(chan effectiveConfigCall, 1)
	go func() {
		configuration, err := service.EffectiveConfig(secondContext)
		secondResult <- effectiveConfigCall{configuration: configuration, err: err}
	}()
	<-secondWaiting

	cancelFirst()
	if result := <-firstResult; !errors.Is(result.err, context.Canceled) {
		t.Fatalf("canceled EffectiveConfig() error = %v, want context.Canceled", result.err)
	}
	select {
	case err := <-internalCanceled:
		t.Fatalf("shared execution canceled with first caller: %v", err)
	default:
	}

	close(release)
	result := <-secondResult
	if result.err != nil {
		t.Fatalf("surviving EffectiveConfig() error = %v", result.err)
	}
	if got, want := len(result.configuration.Occurrences), 4; got != want {
		t.Fatalf("surviving occurrence count = %d, want %d", got, want)
	}
	if got, want := calls.Load(), int32(1); got != want {
		t.Fatalf("concurrent executor calls = %d, want %d", got, want)
	}
}

func TestEffectiveConfigClassifiesExecutionFailures(t *testing.T) {
	tests := []struct {
		name       string
		result     commandResult
		executeErr error
		want       error
	}{
		{
			name:       "invalid configuration",
			result:     commandResult{exitCode: 1, stderr: []byte("invalid\x00configuration")},
			executeErr: &commandExitError{Code: 1, Diagnostic: "invalid?configuration"},
			want:       ErrConfigInvalid,
		},
		{name: "internal deadline", executeErr: context.DeadlineExceeded, want: ErrCommandTimeout},
		{name: "timeout", executeErr: fmt.Errorf("execute: %w", ErrCommandTimeout), want: ErrCommandTimeout},
		{name: "output too large", executeErr: fmt.Errorf("execute: %w", ErrOutputTooLarge), want: ErrOutputTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newServiceWithExecutor(func(context.Context, commandSpec) (commandResult, error) {
				return test.result, test.executeErr
			})
			configuration, err := service.EffectiveConfig(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("EffectiveConfig() error = %v, want %v", err, test.want)
			}
			if len(configuration.Occurrences) != 0 {
				t.Fatalf("EffectiveConfig() returned partial occurrences: %#v", configuration.Occurrences)
			}
		})
	}
}

func TestValidateStartupUsesFixedCommandAndSanitizesDiagnostic(t *testing.T) {
	startedAt := time.Now()
	var gotSpecification commandSpec
	service := newServiceWithExecutor(func(_ context.Context, specification commandSpec) (commandResult, error) {
		gotSpecification = specification
		return commandResult{stderr: []byte("syntax is ok\x00\r\n")}, nil
	})

	validation, err := service.ValidateStartup(context.Background())
	if err != nil {
		t.Fatalf("ValidateStartup() error = %v", err)
	}
	if err := validateStartupSpecification(gotSpecification); err != nil {
		t.Fatal(err)
	}
	if !validation.Valid {
		t.Fatal("Valid = false, want true")
	}
	if validation.CheckedAt.Before(startedAt) || validation.CheckedAt.After(time.Now()) {
		t.Fatalf("CheckedAt = %s, want current validation time", validation.CheckedAt)
	}
	if got, want := validation.Diagnostic, "syntax is ok?\n"; got != want {
		t.Fatalf("Diagnostic = %q, want %q", got, want)
	}
}

func TestValidateStartupReturnsInvalidResultAndClassifiedError(t *testing.T) {
	exitError := &commandExitError{Code: 1, Diagnostic: "invalid?configuration"}
	service := newServiceWithExecutor(func(context.Context, commandSpec) (commandResult, error) {
		return commandResult{exitCode: 1, stderr: []byte("invalid\x00configuration\x1b[31m")}, exitError
	})

	validation, err := service.ValidateStartup(context.Background())
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("ValidateStartup() error = %v, want ErrConfigInvalid", err)
	}
	var gotExitError *commandExitError
	if !errors.As(err, &gotExitError) || gotExitError.Code != 1 {
		t.Fatalf("ValidateStartup() error = %v, want wrapped commandExitError", err)
	}
	if validation.Valid {
		t.Fatal("Valid = true, want false")
	}
	if validation.CheckedAt.IsZero() {
		t.Fatal("CheckedAt is zero, want completed validation time")
	}
	if got, want := validation.Diagnostic, "invalid?configuration?[31m"; got != want {
		t.Fatalf("Diagnostic = %q, want %q", got, want)
	}
}

func TestValidateStartupPropagatesOperationalFailures(t *testing.T) {
	tests := []error{
		fmt.Errorf("execute: %w", ErrCommandTimeout),
		fmt.Errorf("execute: %w", ErrOutputTooLarge),
		context.Canceled,
	}
	for _, executeErr := range tests {
		service := newServiceWithExecutor(func(context.Context, commandSpec) (commandResult, error) {
			return commandResult{}, executeErr
		})
		validation, err := service.ValidateStartup(context.Background())
		if !errors.Is(err, executeErr) {
			t.Fatalf("ValidateStartup() error = %v, want %v", err, executeErr)
		}
		if !validation.CheckedAt.IsZero() {
			t.Fatalf("ValidateStartup() operational failure result = %+v, want zero value", validation)
		}
	}
}

type effectiveConfigCall struct {
	configuration EffectiveConfig
	err           error
}

type doneSignalingContext struct {
	context.Context
	once     sync.Once
	signaled chan struct{}
}

func (c *doneSignalingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.signaled) })
	return c.Context.Done()
}

func readEffectiveConfigFixture(t *testing.T) []byte {
	t.Helper()
	contents, err := os.ReadFile("testdata/nginx_t_repeated.txt")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return contents
}

func validateEffectiveConfigSpecification(ctx context.Context, specification commandSpec) error {
	if specification.executable != nginxExecutable {
		return fmt.Errorf("effective config executable = %q, want %q", specification.executable, nginxExecutable)
	}
	if !slices.Equal(specification.arguments, []string{"-T", "-c", nginxConfigPath}) {
		return fmt.Errorf("effective config arguments = %#v", specification.arguments)
	}
	if specification.timeout != 10*time.Second || specification.maxOutputBytes != 16*1024*1024 {
		return fmt.Errorf("effective config bounds = %s/%d", specification.timeout, specification.maxOutputBytes)
	}
	if len(specification.allowedExitCodes) != 1 {
		return fmt.Errorf("effective config allowed exits = %#v", specification.allowedExitCodes)
	}
	if _, accepted := specification.allowedExitCodes[0]; !accepted {
		return fmt.Errorf("effective config does not accept exit zero")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return fmt.Errorf("effective config internal context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 8*time.Second || remaining > 10*time.Second {
		return fmt.Errorf("effective config internal deadline remaining = %s", remaining)
	}
	return nil
}

func validateStartupSpecification(specification commandSpec) error {
	if specification.executable != nginxExecutable {
		return fmt.Errorf("startup validation executable = %q, want %q", specification.executable, nginxExecutable)
	}
	if !slices.Equal(specification.arguments, []string{"-t", "-c", nginxConfigPath}) {
		return fmt.Errorf("startup validation arguments = %#v", specification.arguments)
	}
	if specification.timeout != 10*time.Second || specification.maxOutputBytes != 256*1024 {
		return fmt.Errorf("startup validation bounds = %s/%d", specification.timeout, specification.maxOutputBytes)
	}
	if len(specification.allowedExitCodes) != 1 {
		return fmt.Errorf("startup validation allowed exits = %#v", specification.allowedExitCodes)
	}
	if _, accepted := specification.allowedExitCodes[0]; !accepted {
		return fmt.Errorf("startup validation does not accept exit zero")
	}
	return nil
}
