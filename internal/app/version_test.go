/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"os"
	"testing"
)

func TestCurrentBuildInfoSnapshotsLinkerValues(t *testing.T) {
	previousVersion, previousCommit, previousBuildTime := Version, Commit, BuildTime
	Version, Commit, BuildTime = "0.1.0", "0123456789abcdef", "2026-07-15T04:05:06Z"
	t.Cleanup(func() {
		Version, Commit, BuildTime = previousVersion, previousCommit, previousBuildTime
	})

	got := CurrentBuildInfo()
	if got.Version != Version {
		t.Errorf("Version = %q, want %q", got.Version, Version)
	}
	if got.Commit != Commit {
		t.Errorf("Commit = %q, want %q", got.Commit, Commit)
	}
	if got.BuildTime != BuildTime {
		t.Errorf("BuildTime = %q, want %q", got.BuildTime, BuildTime)
	}
}

func TestBuildInfoMatchesExpectedLinkerValues(t *testing.T) {
	expected := BuildInfo{Version: "development", Commit: "unknown", BuildTime: "unknown"}
	if value, ok := os.LookupEnv("NGINX_UIX_TEST_EXPECT_VERSION"); ok {
		expected.Version = value
	}
	if value, ok := os.LookupEnv("NGINX_UIX_TEST_EXPECT_COMMIT"); ok {
		expected.Commit = value
	}
	if value, ok := os.LookupEnv("NGINX_UIX_TEST_EXPECT_BUILD_TIME"); ok {
		expected.BuildTime = value
	}

	got := CurrentBuildInfo()
	if got.Version != expected.Version {
		t.Errorf("Version = %q, want %q", got.Version, expected.Version)
	}
	if got.Commit != expected.Commit {
		t.Errorf("Commit = %q, want %q", got.Commit, expected.Commit)
	}
	if got.BuildTime != expected.BuildTime {
		t.Errorf("BuildTime = %q, want %q", got.BuildTime, expected.BuildTime)
	}
}
