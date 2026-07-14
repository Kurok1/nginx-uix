/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

const (
	agentExitSuccess  = 0
	agentExitUsage    = 2
	agentExitInvalid  = 100
	agentExitInternal = 101

	maxS6ExitCode      = 256
	maxS6Signal        = 255
	maxS6DecimalDigits = 3
)

type agentServerRunner func(context.Context, *nginxruntime.Service, *slog.Logger) error
type startupValidator func(context.Context) (nginxruntime.StartupValidation, error)
type startupStateWriter func(context.Context, nginxruntime.StartupState) error
type nginxExitRecorder func(context.Context, nginxruntime.ExitEvent) (nginxruntime.RecoveryState, error)

// Agent owns the fixed privileged process modes.
type Agent struct {
	service   *nginxruntime.Service
	logger    *slog.Logger
	runServer agentServerRunner

	validateStartup   startupValidator
	writeStartupState startupStateWriter
	recordNginxExit   nginxExitRecorder
}

// NewAgent assembles the fixed production Agent dependencies.
func NewAgent(service *nginxruntime.Service, logger *slog.Logger) *Agent {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Agent{
		service:           service,
		logger:            logger,
		runServer:         nginxruntime.RunAgentServer,
		validateStartup:   service.ValidateStartup,
		writeStartupState: nginxruntime.WriteStartupState,
		recordNginxExit:   nginxruntime.RecordNginxExit,
	}
}

// Run dispatches one fixed Agent mode.
func (a *Agent) Run(ctx context.Context, mode string, arguments []string) int {
	startedAt := time.Now()
	exitCode := a.run(ctx, mode, arguments)
	logger := a.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	logger.Info("agent operation",
		"action", agentModeAction(mode),
		"result", agentExitResult(exitCode),
		"duration_ms", time.Since(startedAt).Milliseconds(),
	)
	return exitCode
}

func (a *Agent) run(ctx context.Context, mode string, arguments []string) int {
	switch mode {
	case "serve":
		if len(arguments) != 0 {
			return agentExitUsage
		}
		if err := a.runServer(ctx, a.service, a.logger); err != nil {
			return agentExitInternal
		}
		return agentExitSuccess
	case "validate-startup":
		if len(arguments) != 0 {
			return agentExitUsage
		}
		validation, err := a.validateStartup(ctx)
		if err != nil && !errors.Is(err, nginxruntime.ErrConfigInvalid) {
			return agentExitInternal
		}
		if err := a.writeStartupState(ctx, nginxruntime.StartupState{Validation: &validation}); err != nil {
			return agentExitInternal
		}
		if err != nil {
			return agentExitInvalid
		}
		return agentExitSuccess
	case "record-nginx-exit":
		if len(arguments) != 2 {
			return agentExitUsage
		}
		exitCode, valid := parseS6Decimal(arguments[0], maxS6ExitCode)
		if !valid {
			return agentExitUsage
		}
		signal, valid := parseS6Decimal(arguments[1], maxS6Signal)
		if !valid {
			return agentExitUsage
		}
		if _, err := a.recordNginxExit(ctx, nginxruntime.ExitEvent{ExitCode: exitCode, Signal: signal}); err != nil {
			return agentExitInternal
		}
		return agentExitSuccess
	default:
		return agentExitUsage
	}
}

func agentModeAction(mode string) string {
	switch mode {
	case "serve":
		return "serve"
	case "validate-startup":
		return "validate_startup"
	case "record-nginx-exit":
		return "record_nginx_exit"
	default:
		return "dispatch"
	}
}

func agentExitResult(exitCode int) string {
	switch exitCode {
	case agentExitSuccess:
		return "success"
	case agentExitUsage:
		return "invalid_arguments"
	case agentExitInvalid:
		return "invalid_config"
	default:
		return "internal_error"
	}
}

func parseS6Decimal(value string, maximum uint64) (int, bool) {
	if value == "" || len(value) > maxS6DecimalDigits {
		return 0, false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil || parsed > maximum {
		return 0, false
	}
	return int(parsed), true
}
