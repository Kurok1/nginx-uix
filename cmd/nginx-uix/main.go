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
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := app.NewLogger(os.Stdout, slog.LevelInfo)
	config, err := app.LoadConfig()
	if err != nil {
		logger.Error("load configuration", "error", err)
		return 2
	}
	config.Logger = logger

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.RunUI(ctx, config); err != nil {
		logger.Error("run UI", "error", err)
		return 1
	}
	return 0
}
