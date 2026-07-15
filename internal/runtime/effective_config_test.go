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
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseEffectiveConfigPreservesOrderedRepeatedOccurrences(t *testing.T) {
	contents := readEffectiveConfigFixture(t)

	configuration, err := parseEffectiveConfig(context.Background(), contents, effectiveConfigFixtureReader())
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
			Content: "# configuration file for the api:\n" +
				"# configuration file /etc/passwd:\n" +
				"server { listen 8080; }",
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
			Content: "# configuration file for the api:\n" +
				"# configuration file /etc/passwd:\n" +
				"server { listen 8080; }",
		},
	}
	if !slices.Equal(configuration.Occurrences, want) {
		t.Fatalf("Occurrences = %#v, want %#v", configuration.Occurrences, want)
	}
}

func TestParseEffectiveConfigAcceptsCRLFAndEmptyBodies(t *testing.T) {
	output := []byte("nginx preamble\r\n" +
		"# configuration file /etc/nginx/nginx.conf:\r\n" +
		"line;\r\n" +
		"\r\n" +
		"# configuration file /etc/nginx/empty.conf:\r\n" +
		"\r\n")
	reader := configFileReaderFromMap(map[string][]byte{
		nginxConfigPath:         []byte("line;\r\n"),
		"/etc/nginx/empty.conf": nil,
	})

	configuration, err := parseEffectiveConfig(context.Background(), output, reader)
	if err != nil {
		t.Fatalf("parseEffectiveConfig() error = %v", err)
	}
	want := []ConfigOccurrence{
		{ID: "occurrence-000001", LoadOrder: 1, Path: nginxConfigPath, Content: "line;\n"},
		{ID: "occurrence-000002", LoadOrder: 2, Path: "/etc/nginx/empty.conf", Content: ""},
	}
	if !slices.Equal(configuration.Occurrences, want) {
		t.Fatalf("Occurrences = %#v, want %#v", configuration.Occurrences, want)
	}
}

func TestParseEffectiveConfigDoesNotTreatBodyCommentsAsMarkers(t *testing.T) {
	mainContents := "events {}\n" +
		"# configuration file for the api:\n" +
		"# configuration file /etc/passwd:\n" +
		"http { include /etc/nginx/conf.d/app.conf; }"
	appContents := "server { listen 8080; }"
	output := []byte("# configuration file /etc/nginx/nginx.conf:\n" +
		mainContents + "\n" +
		"# configuration file /etc/nginx/conf.d/../conf.d/app.conf:\n" +
		appContents + "\n")
	reader := configFileReaderFromMap(map[string][]byte{
		nginxConfigPath:              []byte(mainContents),
		"/etc/nginx/conf.d/app.conf": []byte(appContents),
	})

	configuration, err := parseEffectiveConfig(context.Background(), output, reader)
	if err != nil {
		t.Fatalf("parseEffectiveConfig() error = %v", err)
	}
	if got, want := len(configuration.Occurrences), 2; got != want {
		t.Fatalf("occurrence count = %d, want %d: %#v", got, want, configuration.Occurrences)
	}
	if got, want := configuration.Occurrences[0].Path, nginxConfigPath; got != want {
		t.Errorf("first path = %q, want %q", got, want)
	}
	if got, want := configuration.Occurrences[1].Path, "/etc/nginx/conf.d/app.conf"; got != want {
		t.Errorf("second path = %q, want canonical %q", got, want)
	}
	for _, comment := range []string{
		"# configuration file for the api:",
		"# configuration file /etc/passwd:",
	} {
		if !strings.Contains(configuration.Occurrences[0].Content, comment) {
			t.Errorf("first content does not preserve %q: %q", comment, configuration.Occurrences[0].Content)
		}
	}
}

func TestParseEffectiveConfigAcceptsVerifiedExternalOccurrence(t *testing.T) {
	mainContents := "events {}\nhttp { include /opt/app/nginx/app.conf; }"
	externalContents := "server { listen 8080; }"
	output := []byte("# configuration file /etc/nginx/nginx.conf:\n" +
		mainContents + "\n" +
		"# configuration file /opt/app/nginx/app.conf:\n" +
		externalContents + "\n")
	reader := configFileReaderFromMap(map[string][]byte{
		nginxConfigPath:           []byte(mainContents),
		"/opt/app/nginx/app.conf": []byte(externalContents),
	})

	configuration, err := parseEffectiveConfig(context.Background(), output, reader)
	if err != nil {
		t.Fatalf("parseEffectiveConfig() error = %v", err)
	}
	if got, want := len(configuration.Occurrences), 2; got != want {
		t.Fatalf("occurrence count = %d, want %d", got, want)
	}
	if got, want := configuration.Occurrences[1].Path, "/opt/app/nginx/app.conf"; got != want {
		t.Fatalf("external path = %q, want %q", got, want)
	}
	if got, want := configuration.Occurrences[1].Content, externalContents; got != want {
		t.Fatalf("external content = %q, want %q", got, want)
	}
}

func TestNginxConfigFileReaderEnforcesAllowedRoots(t *testing.T) {
	root := t.TempDir()
	allowedRoot := filepath.Join(root, "allowed")
	outsideRoot := filepath.Join(root, "outside")
	if err := os.MkdirAll(allowedRoot, 0o700); err != nil {
		t.Fatalf("create allowed root: %v", err)
	}
	if err := os.MkdirAll(outsideRoot, 0o700); err != nil {
		t.Fatalf("create outside root: %v", err)
	}
	allowedPath := filepath.Join(allowedRoot, "app.conf")
	outsidePath := filepath.Join(outsideRoot, "private.conf")
	if err := os.WriteFile(allowedPath, []byte("server { listen 8080; }"), 0o600); err != nil {
		t.Fatalf("write allowed config: %v", err)
	}
	if err := os.WriteFile(outsidePath, []byte("private"), 0o600); err != nil {
		t.Fatalf("write outside config: %v", err)
	}
	absoluteLink := filepath.Join(allowedRoot, "absolute.conf")
	if err := os.Symlink(allowedPath, absoluteLink); err != nil {
		t.Fatalf("create absolute in-root symlink: %v", err)
	}
	escapingLink := filepath.Join(allowedRoot, "escaping.conf")
	if err := os.Symlink(outsidePath, escapingLink); err != nil {
		t.Fatalf("create escaping symlink: %v", err)
	}

	allowedRoots, err := normalizeEffectiveConfigRoots([]string{allowedRoot})
	if err != nil {
		t.Fatalf("normalizeEffectiveConfigRoots() error = %v", err)
	}
	reader := newNginxConfigFileReader(allowedRoots)
	for _, configPath := range []string{allowedPath, absoluteLink} {
		contents, readErr := reader(context.Background(), configPath)
		if readErr != nil {
			t.Fatalf("read %q: %v", configPath, readErr)
		}
		if got, want := string(contents), "server { listen 8080; }"; got != want {
			t.Fatalf("read %q = %q, want %q", configPath, got, want)
		}
	}
	for _, configPath := range []string{outsidePath, escapingLink} {
		if _, readErr := reader(context.Background(), configPath); !errors.Is(readErr, ErrConfigPathOutsideAllowedRoots) {
			t.Fatalf("read %q error = %v, want ErrConfigPathOutsideAllowedRoots", configPath, readErr)
		}
	}
}

func TestNginxConfigFileReaderAcceptsDeclaredAndResolvedRootAliases(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := filepath.Join(root, "resolved")
	if err := os.Mkdir(resolvedRoot, 0o700); err != nil {
		t.Fatalf("create resolved root: %v", err)
	}
	configPath := filepath.Join(resolvedRoot, "app.conf")
	if err := os.WriteFile(configPath, []byte("server {}"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	declaredRoot := filepath.Join(root, "declared")
	if err := os.Symlink(resolvedRoot, declaredRoot); err != nil {
		t.Fatalf("create root alias: %v", err)
	}

	allowedRoots, err := normalizeEffectiveConfigRoots([]string{declaredRoot})
	if err != nil {
		t.Fatalf("normalizeEffectiveConfigRoots() error = %v", err)
	}
	reader := newNginxConfigFileReader(allowedRoots)
	canonicalConfigPath, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		t.Fatalf("resolve canonical config path: %v", err)
	}
	for _, path := range []string{filepath.Join(declaredRoot, "app.conf"), canonicalConfigPath} {
		contents, readErr := reader(context.Background(), path)
		if readErr != nil {
			t.Fatalf("read root alias %q: %v", path, readErr)
		}
		if got, want := string(contents), "server {}"; got != want {
			t.Fatalf("contents = %q, want %q", got, want)
		}
	}
}

func TestNewServiceWithEffectiveConfigRootsAddsOnlyValidExistingDirectories(t *testing.T) {
	additionalRoot := t.TempDir()
	configPath := filepath.Join(additionalRoot, "external.conf")
	if err := os.WriteFile(configPath, []byte("server {}"), 0o600); err != nil {
		t.Fatalf("write external config: %v", err)
	}

	service, err := NewServiceWithEffectiveConfigRoots([]string{additionalRoot, additionalRoot})
	if err != nil {
		t.Fatalf("NewServiceWithEffectiveConfigRoots() error = %v", err)
	}
	contents, err := service.readConfigFile(context.Background(), configPath)
	if err != nil {
		t.Fatalf("read configured external file: %v", err)
	}
	if got, want := string(contents), "server {}"; got != want {
		t.Fatalf("external contents = %q, want %q", got, want)
	}

	regularFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(regularFile, nil, 0o600); err != nil {
		t.Fatalf("write regular file root: %v", err)
	}
	for _, invalidRoots := range [][]string{
		{"relative"},
		{string(filepath.Separator)},
		{filepath.Join(t.TempDir(), "missing")},
		{regularFile},
	} {
		if _, createErr := NewServiceWithEffectiveConfigRoots(invalidRoots); createErr == nil {
			t.Fatalf("NewServiceWithEffectiveConfigRoots(%q) error = nil, want validation error", invalidRoots)
		}
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
			if _, err := parseEffectiveConfig(
				context.Background(),
				[]byte(test.output),
				configFileReaderFromMap(nil),
			); err == nil {
				t.Fatal("parseEffectiveConfig() error = nil, want malformed marker rejection")
			}
		})
	}
}

func TestParseEffectiveConfigRejectsUnverifiableBoundaries(t *testing.T) {
	mainMarker := "# configuration file " + nginxConfigPath + ":\n"
	tests := []struct {
		name   string
		ctx    context.Context
		output string
		files  map[string][]byte
	}{
		{
			name:   "file differs from dump",
			output: mainMarker + "events {}\n",
			files:  map[string][]byte{nginxConfigPath: []byte("http {}")},
		},
		{
			name:   "missing nginx separator",
			output: mainMarker + "events {}",
			files:  map[string][]byte{nginxConfigPath: []byte("events {}")},
		},
		{
			name: "outside-root next marker",
			output: mainMarker + "events {}\n" +
				"# configuration file /etc/passwd:\nroot:x:0:0\n",
			files: map[string][]byte{nginxConfigPath: []byte("events {}")},
		},
		{
			name: "escaping next marker",
			output: mainMarker + "events {}\n" +
				"# configuration file /etc/nginx/../../etc/passwd:\nroot:x:0:0\n",
			files: map[string][]byte{nginxConfigPath: []byte("events {}")},
		},
		{
			name:   "missing entry file",
			output: mainMarker + "events {}\n",
			files:  nil,
		},
		{
			name:   "unexpected trailing data",
			output: mainMarker + "events {}\nuntrusted trailer\n",
			files:  map[string][]byte{nginxConfigPath: []byte("events {}")},
		},
		{
			name: "canceled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			output: mainMarker + "events {}\n",
			files:  map[string][]byte{nginxConfigPath: []byte("events {}")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := test.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			configuration, err := parseEffectiveConfig(ctx, []byte(test.output), configFileReaderFromMap(test.files))
			if err == nil {
				t.Fatal("parseEffectiveConfig() error = nil, want fail-closed rejection")
			}
			if len(configuration.Occurrences) != 0 {
				t.Fatalf("parseEffectiveConfig() returned partial occurrences: %#v", configuration.Occurrences)
			}
		})
	}
}

func TestParseEffectiveConfigReadsRepeatedFileOnce(t *testing.T) {
	contents := readEffectiveConfigFixture(t)
	fixtureReader := effectiveConfigFixtureReader()
	calls := make(map[string]int)
	reader := configFileReader(func(ctx context.Context, configPath string) ([]byte, error) {
		calls[configPath]++
		return fixtureReader(ctx, configPath)
	})

	configuration, err := parseEffectiveConfig(context.Background(), contents, reader)
	if err != nil {
		t.Fatalf("parseEffectiveConfig() error = %v", err)
	}
	if got, want := len(configuration.Occurrences), 4; got != want {
		t.Fatalf("occurrence count = %d, want %d", got, want)
	}
	if got, want := calls["/etc/nginx/conf.d/repeated.conf"], 1; got != want {
		t.Fatalf("repeated file reads = %d, want %d", got, want)
	}
}

func TestEffectiveConfigUsesFixedCommandAndDoesNotCacheCompletedResults(t *testing.T) {
	contents := readEffectiveConfigFixture(t)
	var calls atomic.Int32
	service := newEffectiveConfigTestService(func(ctx context.Context, specification commandSpec) (commandResult, error) {
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

func TestEffectiveConfigFallsBackToRawOutputWhenStructureCannotBeVerified(t *testing.T) {
	mainContents := "events {}\nhttp { include /opt/app/nginx/app.conf; }"
	rawOutput := []byte("nginx preamble\n# configuration file /etc/nginx/nginx.conf:\n" +
		mainContents + "\n" +
		"# configuration file /opt/app/nginx/app.conf:\nserver { listen 8080; }\n")
	tests := []struct {
		name        string
		readError   error
		wantWarning EffectiveConfigWarning
	}{
		{
			name:        "path outside allowed roots",
			readError:   ErrConfigPathOutsideAllowedRoots,
			wantWarning: EffectiveConfigWarningPathOutsideAllowedRoots,
		},
		{
			name:        "file changed during inspection",
			readError:   errors.New("file changed"),
			wantWarning: EffectiveConfigWarningStructureUnverified,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newServiceWithExecutor(func(context.Context, commandSpec) (commandResult, error) {
				return commandResult{stdout: rawOutput}, nil
			})
			service.readConfigFile = func(_ context.Context, configPath string) ([]byte, error) {
				if configPath == nginxConfigPath {
					return []byte(mainContents), nil
				}
				return nil, test.readError
			}

			configuration, err := service.EffectiveConfig(context.Background())
			if err != nil {
				t.Fatalf("EffectiveConfig() error = %v", err)
			}
			if got, want := configuration.DisplayMode, EffectiveConfigDisplayModeRaw; got != want {
				t.Fatalf("DisplayMode = %q, want %q", got, want)
			}
			if got, want := configuration.RawContent, string(rawOutput); got != want {
				t.Fatalf("RawContent = %q, want exact stdout %q", got, want)
			}
			if len(configuration.Occurrences) != 0 {
				t.Fatalf("Occurrences = %#v, want none in raw mode", configuration.Occurrences)
			}
			if !slices.Equal(configuration.Warnings, []EffectiveConfigWarning{test.wantWarning}) {
				t.Fatalf("Warnings = %#v, want %#v", configuration.Warnings, []EffectiveConfigWarning{test.wantWarning})
			}
		})
	}
}

func TestEffectiveConfigDoesNotMaskParserResourceFailuresWithRawFallback(t *testing.T) {
	for _, readError := range []error{context.Canceled, context.DeadlineExceeded, ErrOutputTooLarge} {
		service := newServiceWithExecutor(func(context.Context, commandSpec) (commandResult, error) {
			return commandResult{stdout: []byte("# configuration file /etc/nginx/nginx.conf:\nevents {}\n")}, nil
		})
		service.readConfigFile = func(context.Context, string) ([]byte, error) {
			return nil, readError
		}

		configuration, err := service.EffectiveConfig(context.Background())
		if !errors.Is(err, readError) {
			t.Fatalf("EffectiveConfig() error = %v, want %v", err, readError)
		}
		if configuration.DisplayMode != "" || configuration.RawContent != "" || len(configuration.Occurrences) != 0 {
			t.Fatalf("EffectiveConfig() = %#v, want zero value", configuration)
		}
	}
}

func TestEffectiveConfigCallerCancellationDoesNotCancelSharedExecution(t *testing.T) {
	contents := readEffectiveConfigFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	internalCanceled := make(chan error, 1)
	var calls atomic.Int32
	service := newEffectiveConfigTestService(func(ctx context.Context, specification commandSpec) (commandResult, error) {
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

func newEffectiveConfigTestService(executor commandExecutor) *Service {
	service := newServiceWithExecutor(executor)
	service.readConfigFile = effectiveConfigFixtureReader()
	return service
}

func effectiveConfigFixtureReader() configFileReader {
	return configFileReaderFromMap(map[string][]byte{
		nginxConfigPath: []byte("events {}\nhttp {\n" +
			"    include /etc/nginx/conf.d/repeated.conf;\n" +
			"    include /etc/nginx/conf.d/empty.conf;\n" +
			"    include /etc/nginx/conf.d/repeated.conf;\n" +
			"}\n"),
		"/etc/nginx/conf.d/repeated.conf": []byte("# configuration file for the api:\n" +
			"# configuration file /etc/passwd:\n" +
			"server { listen 8080; }"),
		"/etc/nginx/conf.d/empty.conf": nil,
	})
}

func configFileReaderFromMap(files map[string][]byte) configFileReader {
	return func(ctx context.Context, configPath string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		contents, found := files[configPath]
		if !found {
			return nil, fmt.Errorf("fixture file %q: %w", configPath, os.ErrNotExist)
		}
		return slices.Clone(contents), nil
	}
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
