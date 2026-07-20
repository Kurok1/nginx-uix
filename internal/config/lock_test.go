/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceLockAllowsSharedReadersAndSerializesWriter(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "control"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := openTestScopedRoot(t, rootPath)

	first, err := AcquireWorkspaceLock(context.Background(), root, LockShared)
	if err != nil {
		t.Fatalf("AcquireWorkspaceLock(first shared) error = %v", err)
	}
	second, err := AcquireWorkspaceLock(context.Background(), root, LockShared)
	if err != nil {
		t.Fatalf("AcquireWorkspaceLock(second shared) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := AcquireWorkspaceLock(ctx, root, LockExclusive); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireWorkspaceLock(contended exclusive) error = %v, want deadline", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first shared) error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close(second shared) error = %v", err)
	}

	exclusive, err := AcquireWorkspaceLock(context.Background(), root, LockExclusive)
	if err != nil {
		t.Fatalf("AcquireWorkspaceLock(exclusive) error = %v", err)
	}
	if err := exclusive.Close(); err != nil {
		t.Fatalf("Close(exclusive) error = %v", err)
	}
	if err := exclusive.Close(); err != nil {
		t.Fatalf("Close(exclusive again) error = %v", err)
	}

	info, err := os.Lstat(filepath.Join(rootPath, "control", ".lock"))
	if err != nil {
		t.Fatalf("Lstat(control/.lock) error = %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("control/.lock mode = %v", info.Mode())
	}
}

func TestWorkspaceLockRejectsInvalidModeAndCanceledContext(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "control"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := openTestScopedRoot(t, rootPath)
	if _, err := AcquireWorkspaceLock(context.Background(), root, LockMode(99)); !errors.Is(err, ErrPathInvalid) {
		t.Fatalf("AcquireWorkspaceLock(invalid mode) error = %v, want ErrPathInvalid", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AcquireWorkspaceLock(ctx, root, LockShared); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireWorkspaceLock(canceled) error = %v, want context.Canceled", err)
	}
}
