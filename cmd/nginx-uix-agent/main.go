/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kuroky/nginx-uix/internal/app"
	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

const agentStartupFailureExitCode = 101

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	mode := ""
	modeArguments := []string(nil)
	if len(arguments) > 0 {
		mode = arguments[0]
		modeArguments = arguments[1:]
	}

	logger := app.NewLogger(os.Stdout, slog.LevelInfo)
	service, err := nginxruntime.NewServiceWithEffectiveConfigRoots(
		app.AdditionalEffectiveConfigRoots(os.Getenv(app.EffectiveConfigRootsEnvironment)),
	)
	if err != nil {
		logger.Error("initialize agent configuration", "result", "invalid_effective_config_roots")
		return agentStartupFailureExitCode
	}
	agent := app.NewAgent(service, logger, app.ProductionInitializeOptions())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return agent.Run(ctx, mode, modeArguments)
}
