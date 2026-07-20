/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	configservice "github.com/kuroky/nginx-uix/internal/config"
	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
	"github.com/kuroky/nginx-uix/internal/store"
)

func TestRunUIFailsBootstrapBeforeBinding(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listen address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err = RunUI(ctx, Config{
		ListenAddr:      address,
		DatabasePath:    filepath.Join(directory, "nginx-uix.db"),
		WorkspaceRoot:   secureWorkspaceRoot(t, directory),
		ShutdownTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("RunUI() error = nil, want missing bootstrap input failure")
	}

	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen address was bound before bootstrap completed: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close verification listener: %v", err)
	}
}

func TestRunUIServesAfterBootstrapAndShutsDown(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listen address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- RunUI(ctx, Config{
			ListenAddr: address, DatabasePath: filepath.Join(directory, "nginx-uix.db"),
			WorkspaceRoot: secureWorkspaceRoot(t, directory),
			AdminUsername: "operator", AdminPassword: "correct-password-123", ShutdownTimeout: time.Second,
		})
	}()
	t.Cleanup(cancel)

	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.NewTimer(3 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	ready := false
	for !ready {
		select {
		case <-deadline.C:
			t.Fatal("UI did not become ready before deadline")
		case <-ticker.C:
			response, err := client.Get("http://" + address + "/health/live")
			if err != nil {
				continue
			}
			if err := response.Body.Close(); err != nil {
				t.Fatalf("Close(response body) error = %v", err)
			}
			ready = response.StatusCode == http.StatusOK
		}
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("RunUI() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunUI() did not stop before deadline")
	}
}

func TestRunUIReconcileErrorClosesDatabaseAndPreventsServing(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := secureWorkspaceRoot(t, directory)
	var database *trackingUIDatabase
	served := false
	operations := defaultUIOperations()
	operations.openDatabase = func(ctx context.Context, path string) (uiDatabase, error) {
		opened, err := store.Open(ctx, path)
		if err != nil {
			return nil, err
		}
		database = &trackingUIDatabase{DB: opened}
		return database, nil
	}
	operations.newConfigService = func(configservice.Dependencies) (workspaceReconciler, error) {
		return errorReconciler{err: errors.New("reconcile failed")}, nil
	}
	operations.runServer = func(context.Context, *http.Server, *slog.Logger, time.Duration) error {
		served = true
		return nil
	}
	err := runUI(context.Background(), Config{
		ListenAddr: "127.0.0.1:9000", DatabasePath: filepath.Join(directory, "nginx-uix.db"),
		WorkspaceRoot: workspaceRoot, AdminUsername: "operator", AdminPassword: "correct-password-123",
	}, operations)
	if err == nil {
		t.Fatal("runUI() error = nil")
	}
	if database == nil || !database.closed {
		t.Fatal("database was not closed after reconciliation failure")
	}
	if served {
		t.Fatal("HTTP serving started after reconciliation failure")
	}
}

func TestRunUIFilesystemReconcileDoesNotCallUnavailableAgent(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	agent := &unavailableUIAgent{}
	served := false
	operations := defaultUIOperations()
	operations.newAgent = func() uiAgent { return agent }
	operations.runServer = func(context.Context, *http.Server, *slog.Logger, time.Duration) error {
		served = true
		return nil
	}
	err := runUI(context.Background(), Config{
		ListenAddr: "127.0.0.1:9000", DatabasePath: filepath.Join(directory, "nginx-uix.db"),
		WorkspaceRoot: secureWorkspaceRoot(t, directory),
		AdminUsername: "operator", AdminPassword: "correct-password-123",
	}, operations)
	if err != nil {
		t.Fatalf("runUI() error = %v", err)
	}
	if !served || agent.configCalls != 0 {
		t.Fatalf("served = %v, Agent config calls = %d", served, agent.configCalls)
	}
}

func TestRunUIWiresConfigurationServiceIntoHTTPHandler(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	operations := defaultUIOperations()
	operations.newConfigService = func(configservice.Dependencies) (workspaceReconciler, error) {
		return &apiWorkspaceReconciler{}, nil
	}
	status := 0
	operations.runServer = func(_ context.Context, server *http.Server, _ *slog.Logger, _ time.Duration) error {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/config/workspaces", nil)
		server.Handler.ServeHTTP(recorder, request)
		status = recorder.Code
		return nil
	}
	err := runUI(context.Background(), Config{
		ListenAddr: "127.0.0.1:9000", DatabasePath: filepath.Join(directory, "nginx-uix.db"),
		WorkspaceRoot: secureWorkspaceRoot(t, directory),
		AdminUsername: "operator", AdminPassword: "correct-password-123",
	}, operations)
	if err != nil {
		t.Fatalf("runUI() error = %v", err)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("config route status = %d, want authenticated service route 401", status)
	}
}

func TestReleaseTaskOwnerStopsNewWorkAndCancelsAtShutdownDeadline(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	owner := newReleaseTaskOwner(context.Background(), func(ctx context.Context, _ configservice.ReleaseID) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}, slog.New(slog.DiscardHandler))
	if !owner.Start("22222222222222222222222222222222") {
		t.Fatal("Start() = false")
	}
	<-started
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := owner.Stop(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v", err)
	}
	<-stopped
	if owner.Start("33333333333333333333333333333333") {
		t.Fatal("Start() accepted work after shutdown")
	}
}

func TestReleaseTaskOwnerStopReturnsAtDeadlineWhenRunnerDoesNotObserveCancellation(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	finished := make(chan struct{})
	owner := newReleaseTaskOwner(context.Background(), func(context.Context, configservice.ReleaseID) error {
		close(started)
		<-unblock
		close(finished)
		return nil
	}, slog.New(slog.DiscardHandler))
	if !owner.Start("22222222222222222222222222222222") {
		t.Fatal("Start() = false")
	}
	<-started
	stopCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	startedAt := time.Now()
	err := owner.Stop(stopCtx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("Stop() elapsed = %s, want bounded return", elapsed)
	}
	close(unblock)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("runner did not finish after test cleanup")
	}
}

func TestRecoveryTaskOwnerStartsEachTypedOperationAndStopsNewWork(t *testing.T) {
	var mutex sync.Mutex
	started := make([]string, 0, 3)
	record := func(value string) {
		mutex.Lock()
		started = append(started, value)
		mutex.Unlock()
	}
	owner := newRecoveryTaskOwner(
		context.Background(),
		func(context.Context, configservice.RestoreID) error { record("restore"); return nil },
		func(context.Context, configservice.RestartID) error { record("restart"); return nil },
		func(context.Context, configservice.RetentionRunID) error { record("retention"); return nil },
		slog.New(slog.DiscardHandler),
	)
	if !owner.StartRestore("11111111111111111111111111111111") ||
		!owner.StartRestart("22222222222222222222222222222222") ||
		!owner.StartRetention("33333333333333333333333333333333") {
		t.Fatal("typed recovery task start was rejected")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := owner.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	slices.Sort(started)
	if !slices.Equal(started, []string{"restart", "restore", "retention"}) {
		t.Fatalf("started = %v", started)
	}
	if owner.StartRestore("44444444444444444444444444444444") {
		t.Fatal("StartRestore() accepted work after shutdown")
	}
}

type trackingUIDatabase struct {
	*store.DB
	closed bool
}

func (d *trackingUIDatabase) Close() error {
	d.closed = true
	return d.DB.Close()
}

type errorReconciler struct{ err error }

func (r errorReconciler) Reconcile(context.Context) error { return r.err }

type apiWorkspaceReconciler struct {
	*configservice.Service
}

func (*apiWorkspaceReconciler) Reconcile(context.Context) error { return nil }

type unavailableUIAgent struct{ configCalls int }

func (a *unavailableUIAgent) Health(context.Context) error { return errors.New("agent unavailable") }
func (a *unavailableUIAgent) Status(context.Context) (nginxruntime.Status, error) {
	return nginxruntime.Status{}, errors.New("agent unavailable")
}
func (a *unavailableUIAgent) BuildInfo(context.Context) (nginxruntime.BuildInfo, error) {
	return nginxruntime.BuildInfo{}, errors.New("agent unavailable")
}
func (a *unavailableUIAgent) StartupValidation(context.Context) (nginxruntime.StartupState, error) {
	return nginxruntime.StartupState{}, errors.New("agent unavailable")
}
func (a *unavailableUIAgent) EffectiveConfig(context.Context) (nginxruntime.EffectiveConfig, error) {
	return nginxruntime.EffectiveConfig{}, errors.New("agent unavailable")
}
func (a *unavailableUIAgent) ConfigSnapshot(context.Context, string, configservice.WorkspaceID) (configservice.Snapshot, error) {
	a.configCalls++
	return configservice.Snapshot{}, errors.New("agent unavailable")
}
func (a *unavailableUIAgent) ConfigDigest(context.Context, string) (configservice.ProductionState, error) {
	a.configCalls++
	return configservice.ProductionState{}, errors.New("agent unavailable")
}

func secureWorkspaceRoot(t *testing.T, directory string) string {
	t.Helper()
	root := filepath.Join(directory, "workspaces")
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatalf("Mkdir(workspaces) error = %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(workspaces) error = %v", err)
	}
	return root
}
