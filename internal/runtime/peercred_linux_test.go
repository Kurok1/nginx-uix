/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPeerUIDReadsLinuxSOPEERCRED(t *testing.T) {
	path := filepath.Join(shortUnixSocketDir(t), "peercred.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix() error = %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("Close(listener) error = %v", err)
		}
	}()

	type acceptResult struct {
		connection *net.UnixConn
		err        error
	}
	accepted := make(chan acceptResult, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		accepted <- acceptResult{connection: connection, err: acceptErr}
	}()

	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("DialUnix() error = %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close(client) error = %v", err)
		}
	}()
	result := <-accepted
	if result.err != nil {
		t.Fatalf("AcceptUnix() error = %v", result.err)
	}
	defer func() {
		if err := result.connection.Close(); err != nil {
			t.Errorf("Close(server connection) error = %v", err)
		}
	}()

	uid, err := peerUID(result.connection)
	if err != nil {
		t.Fatalf("peerUID() error = %v", err)
	}
	if got, want := uid, uint32(os.Getuid()); got != want {
		t.Fatalf("peer UID = %d, want current UID %d", got, want)
	}
}
