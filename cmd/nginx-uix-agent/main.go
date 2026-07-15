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
	agent := app.NewAgent(nginxruntime.NewService(), logger, app.ProductionInitializeOptions())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return agent.Run(ctx, mode, modeArguments)
}
