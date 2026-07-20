/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

func TestAgentServeModeRunsFixedServer(t *testing.T) {
	service := nginxruntime.NewService()
	ctx := context.WithValue(context.Background(), agentTestContextKey{}, "serve")
	serverCalls := 0
	agent := &Agent{
		service: service,
		logger:  slog.New(slog.DiscardHandler),
		runServer: func(gotContext context.Context, gotService *nginxruntime.Service, _ *slog.Logger) error {
			serverCalls++
			if gotContext != ctx {
				t.Errorf("server context differs from Run context")
			}
			if gotService != service {
				t.Errorf("server service = %p, want %p", gotService, service)
			}
			return nil
		},
	}

	if got, want := agent.Run(ctx, "serve", nil), 0; got != want {
		t.Fatalf("Run(serve) = %d, want %d", got, want)
	}
	if serverCalls != 1 {
		t.Fatalf("server calls = %d, want 1", serverCalls)
	}
}

func TestAgentValidateStartupRecordsValidState(t *testing.T) {
	ctx := context.WithValue(context.Background(), agentTestContextKey{}, "validate")
	validation := nginxruntime.StartupValidation{
		Valid: true, CheckedAt: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), Diagnostic: "syntax is ok",
	}
	validateCalls := 0
	writeCalls := 0
	agent := &Agent{
		logger: slog.New(slog.DiscardHandler),
		validateStartup: func(gotContext context.Context) (nginxruntime.StartupValidation, error) {
			validateCalls++
			if gotContext != ctx {
				t.Errorf("validator context differs from Run context")
			}
			return validation, nil
		},
		recordStartupValidation: func(gotContext context.Context, gotValidation nginxruntime.StartupValidation) error {
			writeCalls++
			if gotContext != ctx {
				t.Errorf("writer context differs from Run context")
			}
			if !reflect.DeepEqual(gotValidation, validation) {
				t.Errorf("written validation = %#v, want %#v", gotValidation, validation)
			}
			return nil
		},
	}

	if got, want := agent.Run(ctx, "validate-startup", nil), 0; got != want {
		t.Fatalf("Run(validate-startup) = %d, want %d", got, want)
	}
	if validateCalls != 1 || writeCalls != 1 {
		t.Fatalf("calls = (validate %d, write %d), want (1, 1)", validateCalls, writeCalls)
	}
}

func TestAgentValidateStartupRecordsInvalidStateBeforeExit100(t *testing.T) {
	validation := nginxruntime.StartupValidation{
		Valid: false, CheckedAt: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), Diagnostic: "configuration rejected",
	}
	writeCalls := 0
	agent := &Agent{
		logger: slog.New(slog.DiscardHandler),
		validateStartup: func(context.Context) (nginxruntime.StartupValidation, error) {
			return validation, fmt.Errorf("fixed nginx validation: %w", nginxruntime.ErrConfigInvalid)
		},
		recordStartupValidation: func(_ context.Context, gotValidation nginxruntime.StartupValidation) error {
			writeCalls++
			if !reflect.DeepEqual(gotValidation, validation) {
				t.Errorf("written validation = %#v, want %#v", gotValidation, validation)
			}
			return nil
		},
	}

	if got, want := agent.Run(context.Background(), "validate-startup", nil), 100; got != want {
		t.Fatalf("Run(invalid validate-startup) = %d, want %d", got, want)
	}
	if writeCalls != 1 {
		t.Fatalf("writer calls = %d, want 1", writeCalls)
	}
}

func TestAgentRecordNginxExitParsesS6Values(t *testing.T) {
	ctx := context.WithValue(context.Background(), agentTestContextKey{}, "record")
	recordCalls := 0
	agent := &Agent{
		logger: slog.New(slog.DiscardHandler),
		recordNginxExit: func(gotContext context.Context, event nginxruntime.ExitEvent) (nginxruntime.RecoveryState, error) {
			recordCalls++
			if gotContext != ctx {
				t.Errorf("recorder context differs from Run context")
			}
			want := nginxruntime.ExitEvent{ExitCode: 256, Signal: 255}
			if !reflect.DeepEqual(event, want) {
				t.Errorf("recorded event = %#v, want %#v", event, want)
			}
			return nginxruntime.RecoveryState{}, nil
		},
	}

	if got, want := agent.Run(ctx, "record-nginx-exit", []string{"256", "255"}), 0; got != want {
		t.Fatalf("Run(record-nginx-exit) = %d, want %d", got, want)
	}
	if recordCalls != 1 {
		t.Fatalf("recorder calls = %d, want 1", recordCalls)
	}
}

func TestAgentInitContainerRunsAsRootWithTrustedOptions(t *testing.T) {
	ctx := context.WithValue(context.Background(), agentTestContextKey{}, "initialize")
	options := nginxruntime.InitializeOptions{
		DefaultsRoot:  "/trusted/defaults",
		NginxRoot:     "/trusted/nginx",
		DataRoot:      "/trusted/data",
		WorkspaceRoot: "/trusted/data/workspaces",
		RunRoot:       "/trusted/run",
		DataUID:       1234,
		DataGID:       5678,
	}
	calls := make([]string, 0, 2)
	agent := &Agent{
		logger:            slog.New(slog.DiscardHandler),
		initializeOptions: options,
		effectiveUID: func() int {
			calls = append(calls, "uid")
			return 0
		},
		initializeContainer: func(gotContext context.Context, gotOptions nginxruntime.InitializeOptions) error {
			calls = append(calls, "initialize")
			if gotContext != ctx {
				t.Errorf("initializer context differs from Run context")
			}
			if !reflect.DeepEqual(gotOptions, options) {
				t.Errorf("initializer options = %#v, want %#v", gotOptions, options)
			}
			return nil
		},
	}

	if got, want := agent.Run(ctx, "init-container", nil), 0; got != want {
		t.Fatalf("Run(init-container) = %d, want %d", got, want)
	}
	if want := []string{"uid", "initialize"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestAgentSeparatedContainerInitializersRunOnlyTheirFixedOperation(t *testing.T) {
	options := nginxruntime.InitializeOptions{
		DefaultsRoot:  "/trusted/defaults",
		NginxRoot:     "/trusted/nginx",
		DataRoot:      "/trusted/data",
		WorkspaceRoot: "/trusted/data/workspaces",
		RunRoot:       "/trusted/run",
		DataUID:       1234,
		DataGID:       5678,
	}
	tests := []struct {
		mode       string
		wantAction string
	}{
		{mode: "init-nginx-runtime", wantAction: "runtime"},
		{mode: "prepare-ui-data", wantAction: "data"},
	}

	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			calls := make([]string, 0, 2)
			record := func(action string) containerInitializer {
				return func(_ context.Context, gotOptions nginxruntime.InitializeOptions) error {
					calls = append(calls, action)
					if !reflect.DeepEqual(gotOptions, options) {
						t.Errorf("initializer options = %#v, want %#v", gotOptions, options)
					}
					return nil
				}
			}
			agent := &Agent{
				logger:                 slog.New(slog.DiscardHandler),
				initializeOptions:      options,
				initializeContainer:    record("combined"),
				initializeNginxRuntime: record("runtime"),
				prepareContainerData:   record("data"),
				effectiveUID: func() int {
					calls = append(calls, "uid")
					return 0
				},
			}

			if got, want := agent.Run(context.Background(), test.mode, nil), 0; got != want {
				t.Fatalf("Run(%s) = %d, want %d", test.mode, got, want)
			}
			if want := []string{"uid", test.wantAction}; !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %#v, want %#v", calls, want)
			}
		})
	}
}

func TestAgentInitContainerRejectsUnsafeInvocationAndInitializationFailure(t *testing.T) {
	injected := errors.New("sensitive initialization failure")
	tests := []struct {
		name          string
		arguments     []string
		effectiveUID  int
		initializeErr error
		wantExit      int
		wantUIDCalls  int
		wantInitCalls int
	}{
		{
			name: "extra argument", arguments: []string{"/attacker/path"}, effectiveUID: 0,
			wantExit: 2, wantUIDCalls: 0, wantInitCalls: 0,
		},
		{
			name: "non-root", effectiveUID: 10001,
			wantExit: 101, wantUIDCalls: 1, wantInitCalls: 0,
		},
		{
			name: "initialization failure", effectiveUID: 0, initializeErr: injected,
			wantExit: 101, wantUIDCalls: 1, wantInitCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			uidCalls := 0
			initializeCalls := 0
			agent := &Agent{
				logger: slog.New(slog.DiscardHandler),
				effectiveUID: func() int {
					uidCalls++
					return test.effectiveUID
				},
				initializeContainer: func(context.Context, nginxruntime.InitializeOptions) error {
					initializeCalls++
					return test.initializeErr
				},
			}

			if got := agent.Run(context.Background(), "init-container", test.arguments); got != test.wantExit {
				t.Errorf("Run(init-container, %#v) = %d, want %d", test.arguments, got, test.wantExit)
			}
			if uidCalls != test.wantUIDCalls || initializeCalls != test.wantInitCalls {
				t.Errorf(
					"calls = (uid %d, initialize %d), want (%d, %d)",
					uidCalls,
					initializeCalls,
					test.wantUIDCalls,
					test.wantInitCalls,
				)
			}
		})
	}
}

func TestAgentInitContainerLogsOnlyBoundedOperationMetadata(t *testing.T) {
	const sensitive = "PRIVATE initialization path and diagnostic"
	var output bytes.Buffer
	agent := &Agent{
		logger:       NewLogger(&output, slog.LevelInfo),
		effectiveUID: func() int { return 0 },
		initializeContainer: func(context.Context, nginxruntime.InitializeOptions) error {
			return errors.New(sensitive)
		},
	}

	if got, want := agent.Run(context.Background(), "init-container", nil), 101; got != want {
		t.Fatalf("Run(init-container) = %d, want %d", got, want)
	}
	if strings.Contains(output.String(), sensitive) {
		t.Fatalf("log contains sensitive initialization error: %s", output.String())
	}
	var record map[string]any
	if err := json.NewDecoder(&output).Decode(&record); err != nil {
		t.Fatalf("decode log: %v; output = %q", err, output.String())
	}
	allowedKeys := map[string]bool{
		"time": true, "level": true, "msg": true, "action": true, "result": true, "duration_ms": true,
	}
	for key := range record {
		if !allowedKeys[key] {
			t.Errorf("unexpected log field %q in %#v", key, record)
		}
	}
	if got, want := record["action"], "init_container"; got != want {
		t.Errorf("log action = %#v, want %#v", got, want)
	}
	if got, want := record["result"], "internal_error"; got != want {
		t.Errorf("log result = %#v, want %#v", got, want)
	}
}

func TestAgentRejectsUnknownModesAndInvalidArgumentsWithoutWork(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		arguments []string
	}{
		{name: "missing mode"},
		{name: "unknown mode", mode: "unknown"},
		{name: "serve extra argument", mode: "serve", arguments: []string{"extra"}},
		{name: "init extra argument", mode: "init-container", arguments: []string{"extra"}},
		{name: "runtime init extra argument", mode: "init-nginx-runtime", arguments: []string{"extra"}},
		{name: "data preparation extra argument", mode: "prepare-ui-data", arguments: []string{"extra"}},
		{name: "validate extra argument", mode: "validate-startup", arguments: []string{"extra"}},
		{name: "record missing signal", mode: "record-nginx-exit", arguments: []string{"1"}},
		{name: "record extra argument", mode: "record-nginx-exit", arguments: []string{"1", "0", "extra"}},
		{name: "negative exit", mode: "record-nginx-exit", arguments: []string{"-1", "0"}},
		{name: "signed exit", mode: "record-nginx-exit", arguments: []string{"+1", "0"}},
		{name: "spaced exit", mode: "record-nginx-exit", arguments: []string{" 1", "0"}},
		{name: "non decimal exit", mode: "record-nginx-exit", arguments: []string{"one", "0"}},
		{name: "exit above s6 bound", mode: "record-nginx-exit", arguments: []string{"257", "0"}},
		{name: "negative signal", mode: "record-nginx-exit", arguments: []string{"1", "-1"}},
		{name: "signed signal", mode: "record-nginx-exit", arguments: []string{"1", "+1"}},
		{name: "spaced signal", mode: "record-nginx-exit", arguments: []string{"1", " 1"}},
		{name: "non decimal signal", mode: "record-nginx-exit", arguments: []string{"1", "term"}},
		{name: "signal above bound", mode: "record-nginx-exit", arguments: []string{"256", "256"}},
		{name: "oversized decimal", mode: "record-nginx-exit", arguments: []string{strings.Repeat("9", 1024), "0"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			agent := &Agent{
				service: nginxruntime.NewService(),
				logger:  slog.New(slog.DiscardHandler),
				runServer: func(context.Context, *nginxruntime.Service, *slog.Logger) error {
					calls++
					return nil
				},
				validateStartup: func(context.Context) (nginxruntime.StartupValidation, error) {
					calls++
					return nginxruntime.StartupValidation{}, nil
				},
				recordStartupValidation: func(context.Context, nginxruntime.StartupValidation) error {
					calls++
					return nil
				},
				recordNginxExit: func(context.Context, nginxruntime.ExitEvent) (nginxruntime.RecoveryState, error) {
					calls++
					return nginxruntime.RecoveryState{}, nil
				},
			}

			if got, want := agent.Run(context.Background(), test.mode, test.arguments), 2; got != want {
				t.Errorf("Run(%q, %#v) = %d, want %d", test.mode, test.arguments, got, want)
			}
			if calls != 0 {
				t.Errorf("dependency calls = %d, want 0", calls)
			}
		})
	}
}

func TestNewAgentWiresFixedProductionDependencies(t *testing.T) {
	service := nginxruntime.NewService()
	options := ProductionInitializeOptions()
	agent := NewAgent(service, nil, options)

	if agent.service != service {
		t.Fatalf("service = %p, want %p", agent.service, service)
	}
	if agent.logger == nil || agent.runServer == nil || agent.validateStartup == nil ||
		agent.recordStartupValidation == nil || agent.recordNginxExit == nil ||
		agent.initializeContainer == nil || agent.initializeNginxRuntime == nil ||
		agent.prepareContainerData == nil || agent.effectiveUID == nil {
		t.Fatalf("NewAgent() left a production dependency unset: %#v", agent)
	}
	if !reflect.DeepEqual(agent.initializeOptions, options) {
		t.Fatalf("initialize options = %#v, want %#v", agent.initializeOptions, options)
	}
}

func TestProductionInitializeOptionsUseOnlyFixedContainerValues(t *testing.T) {
	want := nginxruntime.InitializeOptions{
		DefaultsRoot:  "/usr/share/nginx-uix/default-nginx",
		NginxRoot:     "/etc/nginx",
		DataRoot:      "/var/lib/nginx-uix",
		WorkspaceRoot: "/var/lib/nginx-uix/workspaces",
		RunRoot:       "/run/nginx-uix",
		DataUID:       10001,
		DataGID:       10001,
	}
	if got := ProductionInitializeOptions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ProductionInitializeOptions() = %#v, want %#v", got, want)
	}
}

func TestAdditionalEffectiveConfigRootsUseOSPathList(t *testing.T) {
	value := strings.Join([]string{"/opt/app/nginx", "/srv/nginx/snippets"}, string(os.PathListSeparator))
	want := []string{"/opt/app/nginx", "/srv/nginx/snippets"}
	if got := AdditionalEffectiveConfigRoots(value); !slices.Equal(got, want) {
		t.Fatalf("AdditionalEffectiveConfigRoots() = %#v, want %#v", got, want)
	}
	if got := AdditionalEffectiveConfigRoots(""); len(got) != 0 {
		t.Fatalf("AdditionalEffectiveConfigRoots(empty) = %#v, want empty", got)
	}
}

func TestAgentLogsOnlyBoundedOperationMetadata(t *testing.T) {
	const sensitive = "PRIVATE CONFIG diagnostic from environment"
	var output bytes.Buffer
	validation := nginxruntime.StartupValidation{Valid: false, Diagnostic: sensitive}
	agent := &Agent{
		logger: NewLogger(&output, slog.LevelInfo),
		validateStartup: func(context.Context) (nginxruntime.StartupValidation, error) {
			return validation, fmt.Errorf("%s: %w", sensitive, nginxruntime.ErrConfigInvalid)
		},
		recordStartupValidation: func(context.Context, nginxruntime.StartupValidation) error { return nil },
	}

	if got, want := agent.Run(context.Background(), "validate-startup", nil), 100; got != want {
		t.Fatalf("Run(validate-startup) = %d, want %d", got, want)
	}
	if strings.Contains(output.String(), sensitive) {
		t.Fatalf("log contains sensitive diagnostic: %s", output.String())
	}

	var record map[string]any
	if err := json.NewDecoder(&output).Decode(&record); err != nil {
		t.Fatalf("decode log: %v; output = %q", err, output.String())
	}
	allowedKeys := map[string]bool{
		"time": true, "level": true, "msg": true, "action": true, "result": true, "duration_ms": true,
	}
	for key := range record {
		if !allowedKeys[key] {
			t.Errorf("unexpected log field %q in %#v", key, record)
		}
	}
	if got, want := record["action"], "validate_startup"; got != want {
		t.Errorf("log action = %#v, want %#v", got, want)
	}
	if got, want := record["result"], "invalid_config"; got != want {
		t.Errorf("log result = %#v, want %#v", got, want)
	}
	if _, found := record["duration_ms"]; !found {
		t.Errorf("log duration_ms missing: %#v", record)
	}
}

func TestAgentInternalFailuresExit101(t *testing.T) {
	internalFailure := errors.New("internal failure")
	tests := []struct {
		name      string
		mode      string
		arguments []string
		configure func(*Agent, *[]string)
		wantCalls []string
	}{
		{
			name: "server failure", mode: "serve", wantCalls: []string{"serve"},
			configure: func(agent *Agent, calls *[]string) {
				agent.runServer = func(context.Context, *nginxruntime.Service, *slog.Logger) error {
					*calls = append(*calls, "serve")
					return internalFailure
				}
			},
		},
		{
			name: "validation operation failure", mode: "validate-startup", wantCalls: []string{"validate"},
			configure: func(agent *Agent, calls *[]string) {
				agent.validateStartup = func(context.Context) (nginxruntime.StartupValidation, error) {
					*calls = append(*calls, "validate")
					return nginxruntime.StartupValidation{}, internalFailure
				}
				agent.recordStartupValidation = func(context.Context, nginxruntime.StartupValidation) error {
					*calls = append(*calls, "write")
					return nil
				}
			},
		},
		{
			name: "valid state write failure", mode: "validate-startup", wantCalls: []string{"validate", "write"},
			configure: func(agent *Agent, calls *[]string) {
				agent.validateStartup = func(context.Context) (nginxruntime.StartupValidation, error) {
					*calls = append(*calls, "validate")
					return nginxruntime.StartupValidation{Valid: true}, nil
				}
				agent.recordStartupValidation = func(context.Context, nginxruntime.StartupValidation) error {
					*calls = append(*calls, "write")
					return internalFailure
				}
			},
		},
		{
			name: "invalid state write failure", mode: "validate-startup", wantCalls: []string{"validate", "write"},
			configure: func(agent *Agent, calls *[]string) {
				agent.validateStartup = func(context.Context) (nginxruntime.StartupValidation, error) {
					*calls = append(*calls, "validate")
					return nginxruntime.StartupValidation{Valid: false}, nginxruntime.ErrConfigInvalid
				}
				agent.recordStartupValidation = func(context.Context, nginxruntime.StartupValidation) error {
					*calls = append(*calls, "write")
					return internalFailure
				}
			},
		},
		{
			name: "recovery record failure", mode: "record-nginx-exit", arguments: []string{"1", "0"}, wantCalls: []string{"record"},
			configure: func(agent *Agent, calls *[]string) {
				agent.recordNginxExit = func(context.Context, nginxruntime.ExitEvent) (nginxruntime.RecoveryState, error) {
					*calls = append(*calls, "record")
					return nginxruntime.RecoveryState{}, internalFailure
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := make([]string, 0, len(test.wantCalls))
			agent := &Agent{logger: slog.New(slog.DiscardHandler)}
			test.configure(agent, &calls)

			if got, want := agent.Run(context.Background(), test.mode, test.arguments), 101; got != want {
				t.Errorf("Run(%q) = %d, want %d", test.mode, got, want)
			}
			if !reflect.DeepEqual(calls, test.wantCalls) {
				t.Errorf("calls = %#v, want %#v", calls, test.wantCalls)
			}
		})
	}
}

type agentTestContextKey struct{}
