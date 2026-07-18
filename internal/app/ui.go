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
	configservice "github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/httpapi"
	"github.com/kuroky/nginx-uix/internal/httpapi/uiassets"
	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
	"github.com/kuroky/nginx-uix/internal/store"
)

// RunUI owns the HTTP server lifecycle until cancellation or a serving error.
func RunUI(ctx context.Context, applicationConfig Config) error {
	return runUI(ctx, applicationConfig, defaultUIOperations())
}

type uiDatabase interface {
	auth.Repository
	configservice.WorkspaceReader
	configservice.WorkspaceWriter
	configservice.GroupRepository
	Ping(context.Context) error
	Close() error
}

type uiAgent interface {
	httpapi.Agent
	configservice.ProductionReader
}

type workspaceReconciler interface {
	Reconcile(context.Context) error
}

type uiOperations struct {
	openDatabase     func(context.Context, string) (uiDatabase, error)
	newAgent         func() uiAgent
	newConfigService func(configservice.Dependencies) (workspaceReconciler, error)
	runServer        func(context.Context, *http.Server, *slog.Logger, time.Duration) error
}

func defaultUIOperations() uiOperations {
	return uiOperations{
		openDatabase: func(ctx context.Context, path string) (uiDatabase, error) {
			return store.Open(ctx, path)
		},
		newAgent: func() uiAgent { return nginxruntime.NewAgentClient() },
		newConfigService: func(dependencies configservice.Dependencies) (workspaceReconciler, error) {
			return configservice.NewService(dependencies)
		},
		runServer: runHTTPServer,
	}
}

func runUI(ctx context.Context, applicationConfig Config, operations uiOperations) error {
	logger := applicationConfig.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if operations.openDatabase == nil || operations.newAgent == nil || operations.newConfigService == nil || operations.runServer == nil {
		return fmt.Errorf("run UI: operations are required")
	}
	database, err := operations.openDatabase(ctx, applicationConfig.DatabasePath)
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
		Username:     applicationConfig.AdminUsername,
		PasswordFile: applicationConfig.AdminPasswordFile,
		Password:     applicationConfig.AdminPassword,
	}); err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("bootstrap administrator: %w", err), closeErr)
		}
		return fmt.Errorf("bootstrap administrator: %w", err)
	}
	agent := operations.newAgent()
	configurationService, err := operations.newConfigService(configservice.Dependencies{
		WorkspaceRoot: applicationConfig.WorkspaceRoot,
		Production:    agent,
		Reader:        database,
		Writer:        database,
		Groups:        database,
		Clock:         systemClock{},
		Random:        rand.Reader,
		Limits:        configservice.DefaultLimits(),
	})
	if err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("create configuration service: %w", err), closeErr)
		}
		return fmt.Errorf("create configuration service: %w", err)
	}
	if err := configurationService.Reconcile(ctx); err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("reconcile configuration workspaces: %w", err), closeErr)
		}
		return fmt.Errorf("reconcile configuration workspaces: %w", err)
	}
	workspaces, ok := configurationService.(httpapi.WorkspaceAPI)
	if !ok {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("create configuration HTTP API: workspace service is unavailable"), closeErr)
		}
		return fmt.Errorf("create configuration HTTP API: workspace service is unavailable")
	}
	groups, ok := configurationService.(httpapi.GroupAPI)
	if !ok {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("create configuration HTTP API: group service is unavailable"), closeErr)
		}
		return fmt.Errorf("create configuration HTTP API: group service is unavailable")
	}
	server := &http.Server{
		Addr: applicationConfig.ListenAddr,
		Handler: httpapi.NewHandler(httpapi.Dependencies{
			Assets: uiassets.FS(), Sessions: sessions, PublicURL: applicationConfig.PublicURL,
			Workspaces: workspaces, Groups: groups,
			Agent: agent, Database: databasePingProbe(database.Ping), Logger: logger,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	runErr := operations.runServer(ctx, server, logger, applicationConfig.ShutdownTimeout)
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
