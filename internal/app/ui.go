/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
	"github.com/kuroky/nginx-uix/internal/httpapi"
	"github.com/kuroky/nginx-uix/internal/httpapi/uiassets"
	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
	"github.com/kuroky/nginx-uix/internal/store"
)

// RunUI owns the HTTP server lifecycle until cancellation or a serving error.
func RunUI(ctx context.Context, config Config) error {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	database, err := store.Open(ctx, config.DatabasePath)
	if err != nil {
		return fmt.Errorf("open UI database: %w", err)
	}
	sessions, err := auth.NewService(database, systemClock{}, rand.Reader)
	if err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("create authentication service: %w", err), closeErr)
		}
		return fmt.Errorf("create authentication service: %w", err)
	}
	if err := sessions.Bootstrap(ctx, auth.BootstrapInput{
		Username: config.AdminUsername, PasswordFile: config.AdminPasswordFile, Password: config.AdminPassword,
	}); err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("bootstrap administrator: %w", err), closeErr)
		}
		return fmt.Errorf("bootstrap administrator: %w", err)
	}

	server := &http.Server{
		Addr: config.ListenAddr,
		Handler: httpapi.NewHandler(httpapi.Dependencies{
			Assets: uiassets.FS(), Sessions: sessions, PublicURL: config.PublicURL,
			Agent: nginxruntime.NewAgentClient(), Database: databasePingProbe(database.Ping), Logger: logger,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	runErr := runHTTPServer(ctx, server, logger, config.ShutdownTimeout)
	closeErr := database.Close()
	if runErr != nil && closeErr != nil {
		return errors.Join(runErr, closeErr)
	}
	if runErr != nil {
		return runErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func runHTTPServer(ctx context.Context, server *http.Server, logger *slog.Logger, shutdownTimeout time.Duration) error {
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("ui server listening", "address", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve UI HTTP: %w", err)
	case <-ctx.Done():
		if shutdownTimeout <= 0 {
			shutdownTimeout = 10 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
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

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

type databasePingProbe func(context.Context) error

func (probe databasePingProbe) PingContext(ctx context.Context) error {
	return probe(ctx)
}
