/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"io/fs"
	"net"
	"syscall"
)

func agentSocketOwnerMatches(information fs.FileInfo, uid, gid int) bool {
	stat, ok := information.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid && int(stat.Gid) == gid
}

func peerUID(*net.UnixConn) (uint32, error) {
	return 0, ErrPeerCredentialsUnsupported
}
