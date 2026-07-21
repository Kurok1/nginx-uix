/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"time"
)

const (
	agentSocketPath      = "/run/nginx-uix/agent.sock"
	agentSocketGroup     = "nginx-uix-agent"
	agentSocketRootUID   = 0
	agentSocketGroupID   = 10002
	agentClientUID       = 10001
	agentSocketMode      = 0o660
	agentMaxHeaderBytes  = 16 * 1024
	agentShutdownTimeout = 10 * time.Second
)

// ErrPeerCredentialsUnsupported indicates that the host cannot securely identify a Unix peer.
var ErrPeerCredentialsUnsupported = errors.New("peer credentials unsupported")

type peerUIDChecker func(*net.UnixConn) (uint32, error)

type agentServerConfig struct {
	socketPath      string
	socketGroup     string
	handler         http.Handler
	shutdownTimeout time.Duration
	peerUID         peerUIDChecker
	lookupGroupID   func(string) (int, error)
	lstat           func(string) (fs.FileInfo, error)
	remove          func(string) error
	listenUnix      func(string, *net.UnixAddr) (*net.UnixListener, error)
	chown           func(string, int, int) error
	chmod           func(string, fs.FileMode) error
}

// RunAgentServer owns the fixed production Unix Socket Agent lifecycle.
func RunAgentServer(ctx context.Context, service *Service, logger *slog.Logger) error {
	if service == nil {
		return fmt.Errorf("run agent server: service is required")
	}
	if err := service.ReconcileReleaseArtifacts(ctx); err != nil {
		return fmt.Errorf("run agent server: reconcile release artifacts: %w", err)
	}
	if err := service.ReconcileRouteLabArtifacts(ctx); err != nil {
		return fmt.Errorf("run agent server: reconcile route lab artifacts: %w", err)
	}
	return runAgentServer(ctx, newAgentServerConfig(service, logger))
}

func newAgentServerConfig(operations agentOperations, logger *slog.Logger) agentServerConfig {
	return agentServerConfig{
		socketPath:      agentSocketPath,
		socketGroup:     agentSocketGroup,
		handler:         newAgentProtocolHandler(operations, logger),
		shutdownTimeout: agentShutdownTimeout,
		peerUID:         peerUID,
		lookupGroupID:   lookupAgentSocketGroupID,
		lstat:           os.Lstat,
		remove:          os.Remove,
		listenUnix:      net.ListenUnix,
		chown:           os.Chown,
		chmod:           os.Chmod,
	}
}

func runAgentServer(ctx context.Context, config agentServerConfig) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("run agent server: %w", err)
	}
	listener, socketInformation, err := prepareAgentSocket(config)
	if err != nil {
		return err
	}

	server := newAgentHTTPServer(config.handler)
	authorizedListener := &peerCredentialListener{
		listener: listener,
		peerUID:  config.peerUID,
		wantUID:  agentClientUID,
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(authorizedListener)
	}()

	var runErr error
	select {
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			runErr = fmt.Errorf("serve agent HTTP: %w", serveErr)
		}
	case <-ctx.Done():
		shutdownTimeout := config.shutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = agentShutdownTimeout
		}
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		shutdownErr := server.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			closeErr := server.Close()
			runErr = errors.Join(fmt.Errorf("shutdown agent HTTP: %w", shutdownErr), wrapAgentServerCloseError(closeErr))
		}
		serveErr := <-serveErrors
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			runErr = errors.Join(runErr, fmt.Errorf("serve agent HTTP after shutdown: %w", serveErr))
		}
	}

	cleanupErr := cleanupAgentSocket(config, listener, socketInformation)
	return errors.Join(runErr, cleanupErr)
}

func prepareAgentSocket(config agentServerConfig) (*net.UnixListener, fs.FileInfo, error) {
	groupID, err := config.lookupGroupID(config.socketGroup)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve agent socket group: %w", err)
	}
	if groupID != agentSocketGroupID {
		return nil, nil, fmt.Errorf("resolve agent socket group: gid must be %d", agentSocketGroupID)
	}

	existing, err := config.lstat(config.socketPath)
	if err == nil {
		if existing.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("prepare agent socket: symlink is not allowed")
		}
		if existing.Mode()&os.ModeSocket == 0 {
			return nil, nil, fmt.Errorf("prepare agent socket: existing path is not a socket")
		}
		if err := config.remove(config.socketPath); err != nil {
			return nil, nil, fmt.Errorf("remove stale agent socket: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("inspect agent socket: %w", err)
	}

	listener, err := config.listenUnix("unix", &net.UnixAddr{Name: config.socketPath, Net: "unix"})
	if err != nil {
		return nil, nil, fmt.Errorf("listen on agent socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	socketInformation, err := config.lstat(config.socketPath)
	if err != nil {
		return nil, nil, errors.Join(
			fmt.Errorf("inspect bound agent socket: %w", err),
			cleanupAgentSocket(config, listener, nil),
		)
	}
	if socketInformation.Mode()&os.ModeSymlink != 0 || socketInformation.Mode()&os.ModeSocket == 0 {
		return nil, nil, errors.Join(
			fmt.Errorf("inspect bound agent socket: Unix socket without symlinks is required"),
			cleanupAgentSocket(config, listener, socketInformation),
		)
	}
	if err := config.chown(config.socketPath, agentSocketRootUID, groupID); err != nil {
		return nil, nil, errors.Join(
			fmt.Errorf("set agent socket owner: %w", err),
			cleanupAgentSocket(config, listener, socketInformation),
		)
	}
	if err := config.chmod(config.socketPath, agentSocketMode); err != nil {
		return nil, nil, errors.Join(
			fmt.Errorf("set agent socket permissions: %w", err),
			cleanupAgentSocket(config, listener, socketInformation),
		)
	}
	configuredInformation, err := config.lstat(config.socketPath)
	if err != nil {
		return nil, nil, errors.Join(
			fmt.Errorf("verify agent socket: %w", err),
			cleanupAgentSocket(config, listener, socketInformation),
		)
	}
	if !os.SameFile(socketInformation, configuredInformation) || configuredInformation.Mode()&os.ModeSocket == 0 {
		return nil, nil, errors.Join(
			fmt.Errorf("verify agent socket: socket changed during setup"),
			cleanupAgentSocket(config, listener, socketInformation),
		)
	}
	if configuredInformation.Mode().Perm() != agentSocketMode {
		return nil, nil, errors.Join(
			fmt.Errorf("verify agent socket: mode must be 0660"),
			cleanupAgentSocket(config, listener, socketInformation),
		)
	}
	return listener, configuredInformation, nil
}

func cleanupAgentSocket(config agentServerConfig, listener *net.UnixListener, expected fs.FileInfo) error {
	var cleanupErr error
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			cleanupErr = fmt.Errorf("close agent socket: %w", err)
		}
	}
	current, err := config.lstat(config.socketPath)
	if errors.Is(err, fs.ErrNotExist) {
		return cleanupErr
	}
	if err != nil {
		return errors.Join(cleanupErr, fmt.Errorf("inspect agent socket during cleanup: %w", err))
	}
	if current.Mode()&os.ModeSymlink != 0 || current.Mode()&os.ModeSocket == 0 {
		return errors.Join(cleanupErr, fmt.Errorf("remove agent socket: path is no longer the owned socket"))
	}
	if expected != nil && !os.SameFile(expected, current) {
		return errors.Join(cleanupErr, fmt.Errorf("remove agent socket: socket changed during lifecycle"))
	}
	if err := config.remove(config.socketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errors.Join(cleanupErr, fmt.Errorf("remove agent socket: %w", err))
	}
	return cleanupErr
}

func lookupAgentSocketGroupID(name string) (int, error) {
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("lookup group %q: %w", name, err)
	}
	groupID, err := strconv.ParseUint(group.Gid, 10, 31)
	if err != nil {
		return 0, fmt.Errorf("parse group %q gid: %w", name, err)
	}
	return int(groupID), nil
}

func newAgentHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      65 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    agentMaxHeaderBytes,
	}
}

type peerCredentialListener struct {
	listener *net.UnixListener
	peerUID  peerUIDChecker
	wantUID  uint32
}

func (l *peerCredentialListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.listener.AcceptUnix()
		if err != nil {
			return nil, err
		}
		uid, credentialErr := l.peerUID(connection)
		if credentialErr == nil && uid == l.wantUID {
			return connection, nil
		}
		_ = connection.Close() // The rejected connection has no remaining owner.
	}
}

func (l *peerCredentialListener) Close() error {
	return l.listener.Close()
}

func (l *peerCredentialListener) Addr() net.Addr {
	return l.listener.Addr()
}

func wrapAgentServerCloseError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("close agent HTTP: %w", err)
}
