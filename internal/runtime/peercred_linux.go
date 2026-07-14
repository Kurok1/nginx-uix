/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"fmt"
	"net"
	"syscall"
)

func peerUID(connection *net.UnixConn) (uint32, error) {
	if connection == nil {
		return 0, fmt.Errorf("read peer credentials: connection is required")
	}
	rawConnection, err := connection.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("access peer socket: %w", err)
	}
	var credentials *syscall.Ucred
	var credentialErr error
	if err := rawConnection.Control(func(fileDescriptor uintptr) {
		// #nosec G115 -- RawConn exposes the process's OS int file descriptor as uintptr.
		descriptor := int(fileDescriptor)
		credentials, credentialErr = syscall.GetsockoptUcred(
			descriptor,
			syscall.SOL_SOCKET,
			syscall.SO_PEERCRED,
		)
	}); err != nil {
		return 0, fmt.Errorf("control peer socket: %w", err)
	}
	if credentialErr != nil {
		return 0, fmt.Errorf("read peer credentials: %w", credentialErr)
	}
	if credentials == nil {
		return 0, fmt.Errorf("read peer credentials: empty result")
	}
	return credentials.Uid, nil
}
