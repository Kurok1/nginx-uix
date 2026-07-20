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
	"sync"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
	configservice "github.com/kuroky/nginx-uix/internal/config"
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
func RunUI(ctx context.Context, applicationConfig Config) error {
	return runUI(ctx, applicationConfig, defaultUIOperations())
}

type uiDatabase interface {
	auth.Repository
	configservice.WorkspaceReader
	configservice.WorkspaceWriter
	configservice.GroupRepository
	configservice.AttentionReader
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
		Attention:     database,
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
	var releases httpapi.ReleaseAPI
	var releaseTasks *releaseTaskOwner
	workspaceService, workspaceOK := configurationService.(*configservice.Service)
	releaseRepository, repositoryOK := database.(configservice.ReleaseRepository)
	releaseAgent, agentOK := agent.(configservice.ReleaseAgent)
	if workspaceOK && repositoryOK && agentOK {
		releaseService, releaseErr := configservice.NewReleaseService(configservice.ReleaseDependencies{
			Workspaces: workspaceService, Repository: releaseRepository, Agent: releaseAgent,
			Clock: systemClock{}, Random: rand.Reader,
		})
		if releaseErr == nil {
			releaseErr = releaseService.Reconcile(ctx)
		}
		if releaseErr != nil {
			if closeErr := database.Close(); closeErr != nil {
				return errors.Join(fmt.Errorf("reconcile configuration releases: %w", releaseErr), closeErr)
			}
			return fmt.Errorf("reconcile configuration releases: %w", releaseErr)
		}
		releases = releaseService
		releaseTasks = newReleaseTaskOwner(ctx, releaseService.Run, logger)
	}
	var recovery httpapi.RecoveryAPI
	var recoveryTasks *recoveryTaskOwner
	recoveryRepository, recoveryRepositoryOK := database.(configservice.RecoveryRepository)
	recoveryAgent, recoveryAgentOK := agent.(configservice.RecoveryAgent)
	if recoveryRepositoryOK && recoveryAgentOK {
		recoveryService, recoveryErr := configservice.NewRecoveryService(configservice.RecoveryDependencies{
			Repository: recoveryRepository, Agent: recoveryAgent, Clock: systemClock{},
			Random: rand.Reader, Policy: configservice.DefaultRetentionPolicy(),
		})
		if recoveryErr == nil {
			recoveryErr = recoveryService.Reconcile(ctx)
		}
		if recoveryErr != nil {
			if closeErr := database.Close(); closeErr != nil {
				return errors.Join(fmt.Errorf("reconcile configuration recovery: %w", recoveryErr), closeErr)
			}
			return fmt.Errorf("reconcile configuration recovery: %w", recoveryErr)
		}
		recovery = recoveryService
		recoveryTasks = newRecoveryTaskOwner(
			ctx, recoveryService.RunRestore, recoveryService.RunRestart, recoveryService.RunRetention, logger,
		)
	}
	if err := configurationService.Reconcile(ctx); err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("reconcile configuration workspaces: %w", err), closeErr)
		}
		return fmt.Errorf("reconcile configuration workspaces: %w", err)
	}
	server := &http.Server{
		Addr: applicationConfig.ListenAddr,
		Handler: httpapi.NewHandler(httpapi.Dependencies{
			Assets: uiassets.FS(), Sessions: sessions, PublicURL: applicationConfig.PublicURL,
			Workspaces: workspaces, Groups: groups, Releases: releases, ReleaseTasks: releaseTasks,
			Recovery: recovery, RecoveryTasks: recoveryTasks,
			Agent: agent, Database: databasePingProbe(database.Ping), Logger: logger,
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

	runErr := operations.runServer(ctx, server, logger, applicationConfig.ShutdownTimeout)
	cancelCleanup()
	<-cleanupDone
	var taskErr error
	if releaseTasks != nil || recoveryTasks != nil {
		shutdownTimeout := applicationConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 10 * time.Second
		}
		taskCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		if recoveryTasks != nil {
			taskErr = errors.Join(taskErr, recoveryTasks.Stop(taskCtx))
		}
		if releaseTasks != nil {
			taskErr = errors.Join(taskErr, releaseTasks.Stop(taskCtx))
		}
		cancel()
	}
	closeErr := database.Close()
	return errors.Join(runErr, taskErr, closeErr)
}

type releaseTaskOwner struct {
	run      func(context.Context, configservice.ReleaseID) error
	logger   *slog.Logger
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	stopping bool
	wait     sync.WaitGroup
}

func newReleaseTaskOwner(
	parent context.Context,
	run func(context.Context, configservice.ReleaseID) error,
	logger *slog.Logger,
) *releaseTaskOwner {
	// #nosec G118 -- Stop owns and invokes cancel; detaching cancellation grants the documented bounded shutdown window.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	return &releaseTaskOwner{run: run, logger: logger, ctx: ctx, cancel: cancel}
}

func (o *releaseTaskOwner) Start(id configservice.ReleaseID) bool {
	if o == nil || o.run == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopping {
		return false
	}
	o.wait.Add(1)
	go func() {
		defer o.wait.Done()
		if err := o.run(o.ctx, id); err != nil && o.logger != nil {
			o.logger.Error("configuration release finished with failure", "release_id", id, "error", err)
		}
	}()
	return true
}

func (o *releaseTaskOwner) Stop(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	o.stopping = true
	o.mu.Unlock()
	done := make(chan struct{})
	go func() {
		o.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		o.cancel()
		return nil
	case <-ctx.Done():
		o.cancel()
		return fmt.Errorf("stop configuration release tasks: %w", ctx.Err())
	}
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
