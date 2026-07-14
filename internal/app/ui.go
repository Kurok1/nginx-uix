/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/kuroky/nginx-uix/internal/httpapi"
	"github.com/kuroky/nginx-uix/internal/httpapi/uiassets"
)

// RunUI owns the HTTP server lifecycle until cancellation or a serving error.
func RunUI(ctx context.Context, config Config) error {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	server := &http.Server{
		Addr:              config.ListenAddr,
		Handler:           httpapi.NewHandler(httpapi.Dependencies{Assets: uiassets.FS()}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("ui server listening", "address", config.ListenAddr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve UI HTTP: %w", err)
	case <-ctx.Done():
		shutdownTimeout := config.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 10 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown UI HTTP: %w", err)
		}
		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve UI HTTP after shutdown: %w", err)
		}
		return nil
	}
}
