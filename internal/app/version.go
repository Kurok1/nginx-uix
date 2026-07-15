/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

var (
	// Version is the application version injected by the release build.
	Version = "development"
	// Commit is the source revision injected by the release build.
	Commit = "unknown"
	// BuildTime is the UTC build timestamp injected by the release build.
	BuildTime = "unknown"
)

// BuildInfo is an immutable snapshot of the release metadata.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

// CurrentBuildInfo snapshots the linker-injected release metadata.
func CurrentBuildInfo() BuildInfo {
	return BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
	}
}
