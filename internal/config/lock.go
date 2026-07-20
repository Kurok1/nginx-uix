/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const workspaceLockRetry = 25 * time.Millisecond

var workspaceLockFilePath = RelativePath("control/.lock")

// LockMode selects shared read access or exclusive mutation access.
type LockMode uint8

const (
	// LockShared permits concurrent workspace readers.
	LockShared LockMode = iota + 1
	// LockExclusive serializes workspace mutations and recovery.
	LockExclusive
)

// WorkspaceLock owns one advisory lock descriptor until Close.
type WorkspaceLock struct {
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

// AcquireWorkspaceLock acquires the fixed owner-only workspace lock with cancellation.
func AcquireWorkspaceLock(ctx context.Context, control *ScopedRoot, mode LockMode) (*WorkspaceLock, error) {
	if ctx == nil || control == nil {
		return nil, fmt.Errorf("acquire workspace lock: %w", ErrPathInvalid)
	}
	var operation int
	switch mode {
	case LockShared:
		operation = unix.LOCK_SH
	case LockExclusive:
		operation = unix.LOCK_EX
	default:
		return nil, fmt.Errorf("acquire workspace lock: %w", ErrPathInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := control.CreateRegular(ctx, workspaceLockFilePath, nil, controlFileMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create workspace lock: %w", err)
	}
	file, parent, info, err := control.openEntry(ctx, workspaceLockFilePath)
	if err != nil {
		return nil, fmt.Errorf("open workspace lock: %w", err)
	}
	if err := unix.Close(parent); err != nil {
		return nil, errors.Join(fmt.Errorf("close workspace lock parent: %w", err), file.Close())
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != controlFileMode {
		return nil, errors.Join(fmt.Errorf("open workspace lock: %w", ErrPathInvalid), file.Close())
	}

	ticker := time.NewTicker(workspaceLockRetry)
	defer ticker.Stop()
	for {
		// #nosec G115 -- Unix file descriptors are non-negative and fit int.
		err := unix.Flock(int(file.Fd()), operation|unix.LOCK_NB)
		switch {
		case err == nil:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, errors.Join(ctxErr, file.Close())
			}
			return &WorkspaceLock{file: file}, nil
		case !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN):
			return nil, errors.Join(fmt.Errorf("acquire workspace lock: %w", err), file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-ticker.C:
		}
	}
}

// Close releases and closes the lock exactly once.
func (l *WorkspaceLock) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		if l.file == nil {
			return
		}
		// #nosec G115 -- Unix file descriptors are non-negative and fit int.
		unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
		l.closeErr = errors.Join(unlockErr, l.file.Close())
	})
	return l.closeErr
}
