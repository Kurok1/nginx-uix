/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

// Limits bounds configuration workspace resource consumption.
type Limits struct {
	MaxFileBytes             int64
	MaxEntries               int
	MaxManagedBytes          int64
	MaxPathBytes             int
	MaxPathDepth             int
	MaxPathComponentBytes    int
	MaxWorkspaces            int
	MaxWorkspaceBytes        int64
	MaxGroups                int
	MaxGroupMembers          int
	MaxTotalGroupMembers     int
	MaxDiffResponseBytes     int
	MaxSearchMatches         int
	MaxSearchQueryBytes      int
	MaxIncludeTokenBytes     int
	MaxIncludeDirectiveBytes int
	MaxIncludeEdges          int
	MaxIncludeDepth          int
}

// DefaultLimits returns the fixed v0.2.1 configuration workspace limits.
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:             2 << 20,
		MaxEntries:               4096,
		MaxManagedBytes:          32 << 20,
		MaxPathBytes:             1024,
		MaxPathDepth:             64,
		MaxPathComponentBytes:    255,
		MaxWorkspaces:            8,
		MaxWorkspaceBytes:        512 << 20,
		MaxGroups:                128,
		MaxGroupMembers:          1024,
		MaxTotalGroupMembers:     4096,
		MaxDiffResponseBytes:     4 << 20,
		MaxSearchMatches:         500,
		MaxSearchQueryBytes:      256,
		MaxIncludeTokenBytes:     64 << 10,
		MaxIncludeDirectiveBytes: 256 << 10,
		MaxIncludeEdges:          16384,
		MaxIncludeDepth:          64,
	}
}
