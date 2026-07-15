/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
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
