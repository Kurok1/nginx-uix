/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import "golang.org/x/sys/unix"

func filesystemNameMax(descriptor int) (int, error) {
	var state unix.Statfs_t
	if err := unix.Fstatfs(descriptor, &state); err != nil {
		return 0, err
	}
	nameMax := int(state.Namelen)
	if nameMax <= 0 {
		return 0, invalidPath("filesystem name limit unavailable")
	}
	return nameMax, nil
}
