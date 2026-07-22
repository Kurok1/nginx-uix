/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

func TestAgentServerSocketLifecycle(t *testing.T) {
	t.Run("rejects a regular file without removing it", func(t *testing.T) {
		path := filepath.Join(shortUnixSocketDir(t), "agent.sock")
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		err := runAgentServer(context.Background(), testAgentServerConfig(path, func(*net.UnixConn) (uint32, error) {
			return agentClientUID, nil
		}))
		if err == nil || !strings.Contains(err.Error(), "socket") {
			t.Fatalf("runAgentServer() error = %v, want socket rejection", err)
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("ReadFile() error = %v", readErr)
		}
		if got, want := string(contents), "keep"; got != want {
			t.Fatalf("contents = %q, want %q", got, want)
		}
	})

	t.Run("rejects a symlink without removing its target", func(t *testing.T) {
		directory := shortUnixSocketDir(t)
		target := filepath.Join(directory, "target")
		path := filepath.Join(directory, "agent.sock")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}

		err := runAgentServer(context.Background(), testAgentServerConfig(path, func(*net.UnixConn) (uint32, error) {
			return agentClientUID, nil
		}))
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("runAgentServer() error = %v, want symlink rejection", err)
		}
		contents, readErr := os.ReadFile(target)
		if readErr != nil {
			t.Fatalf("ReadFile(target) error = %v", readErr)
		}
		if got, want := string(contents), "keep"; got != want {
			t.Fatalf("target contents = %q, want %q", got, want)
		}
	})

	t.Run("replaces only a stale socket and removes its socket on shutdown", func(t *testing.T) {
		directory := shortUnixSocketDir(t)
		path := filepath.Join(directory, "agent.sock")
		otherPath := filepath.Join(directory, "other.sock")
		createStaleUnixSocket(t, path)
		createStaleUnixSocket(t, otherPath)

		config := testAgentServerConfig(path, func(*net.UnixConn) (uint32, error) {
			return agentClientUID, nil
		})
		type chownCall struct {
			path     string
			uid, gid int
		}
		chownCalls := make(chan chownCall, 1)
		config.chown = func(path string, uid, gid int) error {
			chownCalls <- chownCall{path: path, uid: uid, gid: gid}
			return nil
		}
		cancel, done := startTestAgentServer(t, config)
		chown := <-chownCalls

		information, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Lstat(socket) error = %v", err)
		}
		if information.Mode()&os.ModeSocket == 0 {
			t.Fatalf("socket mode = %v, want Unix socket", information.Mode())
		}
		if got, want := information.Mode().Perm(), fs.FileMode(0o660); got != want {
			t.Fatalf("socket permissions = %04o, want %04o", got, want)
		}
		if chown.path != path || chown.uid != 0 || chown.gid != agentSocketGroupID {
			t.Fatalf("chown = (%q, %d, %d), want (%q, 0, %d)", chown.path, chown.uid, chown.gid, path, agentSocketGroupID)
		}
		if _, err := os.Lstat(otherPath); err != nil {
			t.Fatalf("Lstat(other socket) error = %v, want untouched socket", err)
		}

		cancel()
		if err := waitAgentServer(done); err != nil {
			t.Fatalf("runAgentServer() error = %v", err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Lstat(socket) error = %v, want not exist after shutdown", err)
		}
		if _, err := os.Lstat(otherPath); err != nil {
			t.Fatalf("Lstat(other socket) error = %v, want untouched socket", err)
		}
	})
}

func TestAgentServerFailsClosedWhenOwnershipOrModeCannotBeEstablished(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentServerConfig)
	}{
		{
			name: "owner",
			mutate: func(config *agentServerConfig) {
				config.chown = func(string, int, int) error { return errors.New("chown denied") }
			},
		},
		{
			name: "owner verification",
			mutate: func(config *agentServerConfig) {
				config.ownerMatches = func(fs.FileInfo, int, int) bool { return false }
			},
		},
		{
			name: "mode",
			mutate: func(config *agentServerConfig) {
				config.chmod = func(string, fs.FileMode) error { return errors.New("chmod denied") }
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(shortUnixSocketDir(t), "agent.sock")
			config := testAgentServerConfig(path, func(*net.UnixConn) (uint32, error) {
				return agentClientUID, nil
			})
			test.mutate(&config)

			err := runAgentServer(context.Background(), config)
			if err == nil {
				t.Fatal("runAgentServer() error = nil, want startup failure")
			}
			if _, statErr := os.Lstat(path); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("Lstat(socket) error = %v, want failed-start socket removed", statErr)
			}
		})
	}
}

func TestAgentServerChecksInjectedPeerUID(t *testing.T) {
	t.Run("accepts only UID 10001", func(t *testing.T) {
		path := filepath.Join(shortUnixSocketDir(t), "agent.sock")
		operations := &recordingAgentOperations{}
		config := testAgentServerConfig(path, func(*net.UnixConn) (uint32, error) {
			return agentClientUID, nil
		})
		config.handler = newAgentProtocolHandler(operations, nil)
		cancel, done := startTestAgentServer(t, config)
		defer stopTestAgentServer(t, cancel, done)

		status, err := requestAgentHealth(path)
		if err != nil {
			t.Fatalf("requestAgentHealth() error = %v", err)
		}
		if got, want := status, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := operations.calls, []string{"health"}; !equalStrings(got, want) {
			t.Fatalf("runtime calls = %#v, want %#v", got, want)
		}
	})

	t.Run("rejects every other UID before HTTP", func(t *testing.T) {
		path := filepath.Join(shortUnixSocketDir(t), "agent.sock")
		operations := &recordingAgentOperations{}
		config := testAgentServerConfig(path, func(*net.UnixConn) (uint32, error) {
			return agentClientUID + 1, nil
		})
		config.handler = newAgentProtocolHandler(operations, nil)
		cancel, done := startTestAgentServer(t, config)
		defer stopTestAgentServer(t, cancel, done)

		assertPeerConnectionClosed(t, path)
		if len(operations.calls) != 0 {
			t.Fatalf("runtime calls = %#v, want none", operations.calls)
		}
	})

	t.Run("rejects a credential lookup error before HTTP", func(t *testing.T) {
		path := filepath.Join(shortUnixSocketDir(t), "agent.sock")
		operations := &recordingAgentOperations{}
		config := testAgentServerConfig(path, func(*net.UnixConn) (uint32, error) {
			return 0, ErrPeerCredentialsUnsupported
		})
		config.handler = newAgentProtocolHandler(operations, nil)
		cancel, done := startTestAgentServer(t, config)
		defer stopTestAgentServer(t, cancel, done)

		assertPeerConnectionClosed(t, path)
		if len(operations.calls) != 0 {
			t.Fatalf("runtime calls = %#v, want none", operations.calls)
		}
	})
}

func TestAgentServerUsesBoundedHTTPHeadersAndTimeouts(t *testing.T) {
	server := newAgentHTTPServer(http.NotFoundHandler())
	if server.MaxHeaderBytes <= 0 || server.MaxHeaderBytes > 64*1024 {
		t.Fatalf("MaxHeaderBytes = %d, want a positive limit no greater than 64 KiB", server.MaxHeaderBytes)
	}
	for name, timeout := range map[string]time.Duration{
		"ReadHeaderTimeout": server.ReadHeaderTimeout,
		"ReadTimeout":       server.ReadTimeout,
		"IdleTimeout":       server.IdleTimeout,
	} {
		if timeout <= 0 || timeout > time.Minute {
			t.Fatalf("%s = %v, want a positive timeout no greater than one minute", name, timeout)
		}
	}
	if server.WriteTimeout < 65*time.Second || server.WriteTimeout > 2*time.Minute {
		t.Fatalf("WriteTimeout = %v, want finite snapshot-safe timeout of at least 65s", server.WriteTimeout)
	}
}

func TestDarwinPeerCredentialsAreExplicitlyUnsupported(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("Darwin-only behavior")
	}
	if _, err := peerUID(nil); !errors.Is(err, ErrPeerCredentialsUnsupported) {
		t.Fatalf("peerUID() error = %v, want %v", err, ErrPeerCredentialsUnsupported)
	}
}

func TestAgentSocketOwnerMatchesFileMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ownership")
	if err := os.WriteFile(path, []byte("ownership"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	information, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}
	if !agentSocketOwnerMatches(information, os.Getuid(), os.Getgid()) {
		t.Fatal("agentSocketOwnerMatches() = false for the current owner")
	}
	if agentSocketOwnerMatches(information, os.Getuid()+1, os.Getgid()) {
		t.Fatal("agentSocketOwnerMatches() = true for a different UID")
	}
	if agentSocketOwnerMatches(information, os.Getuid(), os.Getgid()+1) {
		t.Fatal("agentSocketOwnerMatches() = true for a different GID")
	}
}

func testAgentServerConfig(path string, checker peerUIDChecker) agentServerConfig {
	config := newAgentServerConfig(&recordingAgentOperations{}, nil)
	config.socketPath = path
	config.lookupGroupID = func(string) (int, error) { return agentSocketGroupID, nil }
	config.chown = func(string, int, int) error { return nil }
	config.ownerMatches = func(fs.FileInfo, int, int) bool { return true }
	config.peerUID = checker
	return config
}

func shortUnixSocketDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "nginx-uix-agent-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("RemoveAll(%q) error = %v", directory, err)
		}
	})
	return directory
}

func createStaleUnixSocket(t *testing.T, path string) {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix(%q) error = %v", path, err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatalf("Close(stale socket) error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Remove(%q) error = %v", path, err)
		}
	})
}

func startTestAgentServer(t *testing.T, config agentServerConfig) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runAgentServer(ctx, config)
	}()
	waitForAgentSocket(t, config.socketPath, done)
	return cancel, done
}

func waitForAgentSocket(t *testing.T, path string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		information, err := os.Lstat(path)
		if err == nil && information.Mode()&os.ModeSocket != 0 && information.Mode().Perm() == agentSocketMode {
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("runAgentServer() exited before listening: %v", runErr)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("agent socket %q was not created", path)
}

func stopTestAgentServer(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	if err := waitAgentServer(done); err != nil {
		t.Errorf("runAgentServer() error = %v", err)
	}
}

func waitAgentServer(done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		return errors.New("agent server did not stop")
	}
}

func requestAgentHealth(path string) (status int, returnErr error) {
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return 0, fmt.Errorf("dial agent socket: %w", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close agent connection: %w", err))
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return 0, fmt.Errorf("set agent connection deadline: %w", err)
	}
	if _, err := fmt.Fprint(connection, "GET /v1/health HTTP/1.1\r\nHost: agent\r\nConnection: close\r\n\r\n"); err != nil {
		return 0, fmt.Errorf("write agent request: %w", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		return 0, fmt.Errorf("read agent response: %w", err)
	}
	if err := response.Body.Close(); err != nil {
		return 0, fmt.Errorf("close agent response: %w", err)
	}
	return response.StatusCode, nil
}

func assertPeerConnectionClosed(t *testing.T, path string) {
	t.Helper()
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("DialTimeout() error = %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("Close(agent connection) error = %v", err)
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if _, err := fmt.Fprint(connection, "GET /v1/health HTTP/1.1\r\nHost: agent\r\n\r\n"); err != nil {
		// A prompt write failure is also proof that the unauthorized peer was closed.
		return
	}
	var payload [1]byte
	count, readErr := connection.Read(payload[:])
	if count != 0 || readErr == nil {
		t.Fatalf("Read() = (%d, %v), want a closed connection with no HTTP response", count, readErr)
	}
	var networkError net.Error
	if errors.As(readErr, &networkError) && networkError.Timeout() {
		t.Fatalf("Read() error = %v, want prompt credential rejection", readErr)
	}
}
