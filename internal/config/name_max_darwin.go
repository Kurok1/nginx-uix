/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

func filesystemNameMax(_ int) (int, error) {
	const nameMax = 255
	if nameMax <= 0 {
		return 0, invalidPath("filesystem name limit unavailable")
	}
	return nameMax, nil
}
