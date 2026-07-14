/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import "net"

func peerUID(*net.UnixConn) (uint32, error) {
	return 0, ErrPeerCredentialsUnsupported
}
