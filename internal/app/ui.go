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
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
	"github.com/kuroky/nginx-uix/internal/certificate"
	configservice "github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/httpapi"
	"github.com/kuroky/nginx-uix/internal/httpapi/uiassets"
	"github.com/kuroky/nginx-uix/internal/routelab"
	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
	"github.com/kuroky/nginx-uix/internal/store"
	"github.com/kuroky/nginx-uix/internal/structuredconfig"
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

type uiCertificateDatabase interface {
	certificate.AccountRepository
	certificate.DNSCredentialRepository
	certificate.PlanRepository
	certificate.QueueRepository
	certificate.OrderRepository
	certificate.TaskRepository
	certificate.RenewalRepository
	certificate.RenewalSchedulerRepository
	certificate.LifecycleRepository
	certificate.BindingRepository
	httpapi.CertificateInventoryAPI
	httpapi.CertificatePlanReader
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
	var releaseCoordinator *configservice.ReleaseService
	workspaceService, workspaceOK := configurationService.(*configservice.Service)
	var structured httpapi.StructuredAPI
	if workspaceOK {
		structuredService, structuredErr := structuredconfig.NewService(workspaceService)
		if structuredErr != nil {
			if closeErr := database.Close(); closeErr != nil {
				return errors.Join(fmt.Errorf("create structured configuration service: %w", structuredErr), closeErr)
			}
			return fmt.Errorf("create structured configuration service: %w", structuredErr)
		}
		structured = structuredService
	}
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
		releaseCoordinator = releaseService
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
	var routeLab httpapi.RouteLabAPI
	var routeTasks *routeTaskOwner
	routeRepository, routeRepositoryOK := database.(routelab.Repository)
	routeAgent, routeAgentOK := agent.(routelab.Agent)
	if workspaceOK && routeRepositoryOK && routeAgentOK {
		routeService, routeErr := routelab.NewService(routelab.ServiceOptions{
			Workspaces: workspaceService, Repository: routeRepository, Agent: routeAgent,
			TokenSource: rand.Reader, Now: systemClock{}.Now,
		})
		if routeErr == nil {
			_, routeErr = routeService.ReconcileInterrupted(ctx)
		}
		if routeErr != nil {
			if closeErr := database.Close(); closeErr != nil {
				return errors.Join(fmt.Errorf("reconcile route lab tasks: %w", routeErr), closeErr)
			}
			return fmt.Errorf("reconcile route lab tasks: %w", routeErr)
		}
		routeLab = routeService
		routeTasks = newRouteTaskOwner(ctx, routeService.Execute, logger)
	}
	if err := configurationService.Reconcile(ctx); err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("reconcile configuration workspaces: %w", err), closeErr)
		}
		return fmt.Errorf("reconcile configuration workspaces: %w", err)
	}
	var certificateAccounts httpapi.CertificateAccountAPI
	var certificateCredentials httpapi.CertificateCredentialAPI
	var certificatePlans httpapi.CertificatePlanAPI
	var certificatePlanReader httpapi.CertificatePlanReader
	var certificateQueue httpapi.CertificateQueueAPI
	var certificateTaskService httpapi.CertificateTaskAPI
	var certificateInventory httpapi.CertificateInventoryAPI
	var certificateRenewals httpapi.CertificateRenewalAPI
	var certificateBindings httpapi.CertificateBindingAPI
	var certificateLifecycle httpapi.CertificateLifecycleAPI
	var certificateTasks *certificateTaskOwner
	var certificateVault *certificate.Vault
	var renewalSchedulerCancel context.CancelFunc
	var renewalSchedulerDone chan error
	if applicationConfig.CertificateRoot != "" {
		certificateDatabase, databaseOK := database.(uiCertificateDatabase)
		if !databaseOK || !workspaceOK || releaseCoordinator == nil {
			if closeErr := database.Close(); closeErr != nil {
				return errors.Join(fmt.Errorf("create certificate services: dependencies are unavailable"), closeErr)
			}
			return fmt.Errorf("create certificate services: dependencies are unavailable")
		}
		if err := ensureCertificateVaultRoot(applicationConfig.CertificateRoot); err != nil {
			if closeErr := database.Close(); closeErr != nil {
				return errors.Join(fmt.Errorf("prepare certificate vault: %w", err), closeErr)
			}
			return fmt.Errorf("prepare certificate vault: %w", err)
		}
		certificateVault, err = certificate.OpenVault(ctx, applicationConfig.CertificateRoot, rand.Reader)
		if err != nil {
			if closeErr := database.Close(); closeErr != nil {
				return errors.Join(fmt.Errorf("open certificate vault: %w", err), closeErr)
			}
			return fmt.Errorf("open certificate vault: %w", err)
		}
		acmeFactory := certificate.NewXCryptoACMEFactory()
		cloudflare, certificateErr := certificate.NewCloudflareClient()
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create Cloudflare client", certificateErr)
		}
		publication, certificateErr := certificate.NewConfigPublicationService(certificate.ConfigPublicationServiceOptions{
			Workspaces: workspaceService, Releases: releaseCoordinator,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate publication service", certificateErr)
		}
		httpChallenges, certificateErr := certificate.NewConfigHTTPChallengeManager(certificate.ConfigHTTPChallengeManagerOptions{
			Publisher: publication, Repository: certificateDatabase, Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create HTTP challenge service", certificateErr)
		}
		publisher, certificateErr := certificate.NewConfigCertificatePublisher(certificate.ConfigCertificatePublisherOptions{
			Publisher: publication, Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate deployment service", certificateErr)
		}
		lifecycleService, certificateErr := certificate.NewLifecycleService(certificate.LifecycleServiceOptions{
			Repository: certificateDatabase, Vault: certificateVault, Publisher: publication,
			Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate lifecycle service", certificateErr)
		}
		if _, certificateErr = lifecycleService.ReconcileMaterial(ctx); certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "reconcile certificate material", certificateErr)
		}
		orderService, certificateErr := certificate.NewOrderService(certificate.OrderServiceOptions{
			Repository: certificateDatabase, Vault: certificateVault, ACME: acmeFactory,
			Cloudflare: cloudflare, DNSWaiter: certificate.NewAuthoritativeDNSWaiter(), HTTP: httpChallenges,
			Publisher: publisher, Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate order service", certificateErr)
		}
		if _, certificateErr = orderService.ReconcileInterrupted(ctx); certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "reconcile certificate tasks", certificateErr)
		}
		accountService, certificateErr := certificate.NewAccountService(certificate.AccountServiceOptions{
			Repository: certificateDatabase, Vault: certificateVault, Factory: acmeFactory,
			Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create ACME account service", certificateErr)
		}
		credentialService, certificateErr := certificate.NewCredentialService(certificate.CredentialServiceOptions{
			Repository: certificateDatabase, Vault: certificateVault, Cloudflare: cloudflare,
			Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create Cloudflare credential service", certificateErr)
		}
		planService, certificateErr := certificate.NewPlanService(certificate.PlanServiceOptions{
			Repository: certificateDatabase, Workspaces: workspaceService, Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate plan service", certificateErr)
		}
		queueService, certificateErr := certificate.NewQueueService(certificate.QueueServiceOptions{
			Repository: certificateDatabase, Planner: planService, Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate queue service", certificateErr)
		}
		bindingService, certificateErr := certificate.NewBindingService(certificate.BindingServiceOptions{
			Repository: certificateDatabase, Planner: planService, Publisher: publisher,
			Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate binding service", certificateErr)
		}
		taskService, certificateErr := certificate.NewTaskService(certificate.TaskServiceOptions{
			Repository: certificateDatabase, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate task service", certificateErr)
		}
		renewalService, certificateErr := certificate.NewRenewalService(certificate.RenewalServiceOptions{
			Repository: certificateDatabase, Planner: planService, Random: rand.Reader, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate renewal service", certificateErr)
		}
		certificateTasks = newCertificateTaskOwner(ctx, func(taskContext context.Context, id certificate.TaskID) (certificate.Task, error) {
			task, readErr := certificateDatabase.CertificateTask(taskContext, id)
			if readErr != nil {
				return certificate.Task{}, readErr
			}
			if task.Kind == certificate.TaskKindBind {
				return bindingService.Run(taskContext, id)
			}
			return orderService.Run(taskContext, id)
		}, logger)
		scheduler, certificateErr := certificate.NewRenewalScheduler(certificate.RenewalSchedulerOptions{
			Repository: certificateDatabase, Queue: renewalService, Tasks: certificateTasks, Now: systemClock{}.Now,
		})
		if certificateErr != nil {
			return closeCertificateStartup(database, certificateVault, "create certificate renewal scheduler", certificateErr)
		}
		certificateAccounts = accountService
		certificateCredentials = credentialService
		certificatePlans = planService
		certificatePlanReader = certificateDatabase
		certificateQueue = queueService
		certificateTaskService = taskService
		certificateInventory = certificateDatabase
		certificateRenewals = renewalService
		certificateBindings = bindingService
		certificateLifecycle = lifecycleService
		// #nosec G118 -- cancellation and completion are owned below by the UI shutdown sequence.
		schedulerContext, cancelScheduler := context.WithCancel(context.WithoutCancel(ctx))
		renewalSchedulerCancel = cancelScheduler
		renewalSchedulerDone = make(chan error, 1)
		go func() { renewalSchedulerDone <- scheduler.Run(schedulerContext) }()
	}
	server := &http.Server{
		Addr: applicationConfig.ListenAddr,
		Handler: httpapi.NewHandler(httpapi.Dependencies{
			Assets: uiassets.FS(), Sessions: sessions, PublicURL: applicationConfig.PublicURL,
			Workspaces: workspaces, Structured: structured, Groups: groups, Releases: releases, ReleaseTasks: releaseTasks,
			Recovery: recovery, RecoveryTasks: recoveryTasks,
			RouteLab: routeLab, RouteTasks: routeTasks,
			CertificateAccounts: certificateAccounts, CertificateCredentials: certificateCredentials,
			CertificatePlans: certificatePlans, CertificatePlanReader: certificatePlanReader,
			CertificateQueue: certificateQueue, CertificateTasks: certificateTaskService,
			CertificateTaskController: certificateTasks, Certificates: certificateInventory,
			CertificateRenewals: certificateRenewals, CertificateBindings: certificateBindings,
			CertificateLifecycle: certificateLifecycle,
			Agent:                agent, Database: databasePingProbe(database.Ping), Logger: logger,
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
	if renewalSchedulerCancel != nil {
		renewalSchedulerCancel()
	}
	if releaseTasks != nil || recoveryTasks != nil || routeTasks != nil || certificateTasks != nil || renewalSchedulerDone != nil {
		shutdownTimeout := applicationConfig.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 10 * time.Second
		}
		taskCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		if recoveryTasks != nil {
			taskErr = errors.Join(taskErr, recoveryTasks.Stop(taskCtx))
		}
		if renewalSchedulerDone != nil {
			select {
			case schedulerErr := <-renewalSchedulerDone:
				if schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
					taskErr = errors.Join(taskErr, fmt.Errorf("stop certificate renewal scheduler: %w", schedulerErr))
				}
			case <-taskCtx.Done():
				taskErr = errors.Join(taskErr, fmt.Errorf("stop certificate renewal scheduler: %w", taskCtx.Err()))
			}
		}
		if certificateTasks != nil {
			taskErr = errors.Join(taskErr, certificateTasks.Stop(taskCtx))
		}
		if routeTasks != nil {
			taskErr = errors.Join(taskErr, routeTasks.Stop(taskCtx))
		}
		if releaseTasks != nil {
			taskErr = errors.Join(taskErr, releaseTasks.Stop(taskCtx))
		}
		cancel()
	}
	var certificateCloseErr error
	if certificateVault != nil {
		certificateCloseErr = certificateVault.Close()
	}
	closeErr := database.Close()
	return errors.Join(runErr, taskErr, certificateCloseErr, closeErr)
}

func ensureCertificateVaultRoot(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	information, err := os.Lstat(path)
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() || information.Mode().Perm() != 0o700 {
		return certificate.ErrSecretInvalid
	}
	return nil
}

func closeCertificateStartup(database uiDatabase, vault *certificate.Vault, action string, cause error) error {
	return errors.Join(fmt.Errorf("%s: %w", action, cause), vault.Close(), database.Close())
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
