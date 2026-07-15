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

const (
	authCleanupInterval     = time.Minute
	authCleanupInitialRetry = time.Second
	authCleanupMaxRetry     = time.Minute
	authCleanupTimeout      = 10 * time.Second
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

	cleanupContext, cancelCleanup := context.WithCancel(ctx)
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		runAuthCleanupWorker(cleanupContext, sessions, logger, authCleanupSchedule{
			interval:     authCleanupInterval,
			initialRetry: authCleanupInitialRetry,
			maxRetry:     authCleanupMaxRetry,
			timeout:      authCleanupTimeout,
			wait:         waitForAuthCleanup,
		})
	}()

	runErr := runHTTPServer(ctx, server, logger, config.ShutdownTimeout)
	cancelCleanup()
	<-cleanupDone
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

type authStateCleaner interface {
	CleanupExpired(context.Context) (auth.CleanupResult, error)
}

type authCleanupWaiter func(context.Context, time.Duration) bool

type authCleanupSchedule struct {
	interval     time.Duration
	initialRetry time.Duration
	maxRetry     time.Duration
	timeout      time.Duration
	wait         authCleanupWaiter
}

func runAuthCleanupWorker(
	ctx context.Context,
	cleaner authStateCleaner,
	logger *slog.Logger,
	schedule authCleanupSchedule,
) {
	retryDelay := schedule.initialRetry
	nextDelay := time.Duration(0)
	for {
		if nextDelay > 0 && !schedule.wait(ctx, nextDelay) {
			return
		}
		if ctx.Err() != nil {
			return
		}

		startedAt := time.Now()
		cleanupContext, cancelCleanup := context.WithTimeout(ctx, schedule.timeout)
		result, err := cleaner.CleanupExpired(cleanupContext)
		cancelCleanup()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.WarnContext(
				ctx,
				"authentication state cleanup failed",
				"component", "auth_cleanup",
				"action", "delete_expired_state",
				"result", "failed",
				"duration_ms", time.Since(startedAt).Milliseconds(),
				"error", err,
			)
			nextDelay = retryDelay
			retryDelay = min(retryDelay*2, schedule.maxRetry)
			continue
		}

		if result.SessionsDeleted > 0 || result.ThrottlesDeleted > 0 {
			logger.InfoContext(
				ctx,
				"expired authentication state deleted",
				"component", "auth_cleanup",
				"action", "delete_expired_state",
				"result", "succeeded",
				"duration_ms", time.Since(startedAt).Milliseconds(),
				"sessions_deleted", result.SessionsDeleted,
				"throttles_deleted", result.ThrottlesDeleted,
			)
		}
		retryDelay = schedule.initialRetry
		nextDelay = schedule.interval
	}
}

func waitForAuthCleanup(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
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
