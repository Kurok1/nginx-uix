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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
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

func TestAuthCleanupWorkerBacksOffAndResetsAfterSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failure := errors.New("database temporarily unavailable")
	outcomes := []error{failure, failure, failure, failure, nil, nil}
	calls := 0
	allCallsBounded := true
	cleaner := authCleanupFunc(func(callContext context.Context) (auth.CleanupResult, error) {
		deadline, ok := callContext.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 10*time.Second {
			allCallsBounded = false
		}
		calls++
		if calls == len(outcomes) {
			cancel()
		}
		return auth.CleanupResult{}, outcomes[calls-1]
	})

	waits := make([]time.Duration, 0, len(outcomes)-1)
	schedule := authCleanupSchedule{
		interval:     30 * time.Second,
		initialRetry: time.Second,
		maxRetry:     4 * time.Second,
		timeout:      10 * time.Second,
		wait: func(waitContext context.Context, delay time.Duration) bool {
			waits = append(waits, delay)
			return waitContext.Err() == nil
		},
	}
	runAuthCleanupWorker(ctx, cleaner, slog.New(slog.DiscardHandler), schedule)

	if got, want := calls, len(outcomes); got != want {
		t.Fatalf("cleanup calls = %d, want %d", got, want)
	}
	if !allCallsBounded {
		t.Fatal("at least one cleanup call had no valid timeout")
	}
	wantWaits := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 30 * time.Second}
	if len(waits) != len(wantWaits) {
		t.Fatalf("waits = %v, want %v", waits, wantWaits)
	}
	for index := range wantWaits {
		if waits[index] != wantWaits[index] {
			t.Errorf("waits[%d] = %s, want %s", index, waits[index], wantWaits[index])
		}
	}
}

func TestAuthCleanupWorkerCancelsInFlightCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	cleaner := authCleanupFunc(func(callContext context.Context) (auth.CleanupResult, error) {
		close(started)
		<-callContext.Done()
		return auth.CleanupResult{}, callContext.Err()
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAuthCleanupWorker(ctx, cleaner, slog.New(slog.DiscardHandler), authCleanupSchedule{
			interval: time.Hour, initialRetry: time.Second, maxRetry: time.Minute,
			timeout: time.Hour, wait: waitForAuthCleanup,
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop after cancellation")
	}
}

type authCleanupFunc func(context.Context) (auth.CleanupResult, error)

func (function authCleanupFunc) CleanupExpired(ctx context.Context) (auth.CleanupResult, error) {
	return function(ctx)
}
