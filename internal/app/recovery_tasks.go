/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	configservice "github.com/kuroky/nginx-uix/internal/config"
)

// recoveryTaskOwner owns request-independent recovery work until bounded application shutdown.
type recoveryTaskOwner struct {
	runRestore   func(context.Context, configservice.RestoreID) error
	runRestart   func(context.Context, configservice.RestartID) error
	runRetention func(context.Context, configservice.RetentionRunID) error
	logger       *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	stopping     bool
	wait         sync.WaitGroup
}

func newRecoveryTaskOwner(
	parent context.Context,
	runRestore func(context.Context, configservice.RestoreID) error,
	runRestart func(context.Context, configservice.RestartID) error,
	runRetention func(context.Context, configservice.RetentionRunID) error,
	logger *slog.Logger,
) *recoveryTaskOwner {
	// #nosec G118 -- Stop owns cancellation and grants the documented bounded shutdown window.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	return &recoveryTaskOwner{
		runRestore: runRestore, runRestart: runRestart, runRetention: runRetention,
		logger: logger, ctx: ctx, cancel: cancel,
	}
}

func (o *recoveryTaskOwner) StartRestore(id configservice.RestoreID) bool {
	if o == nil || o.runRestore == nil {
		return false
	}
	return o.start("restore", string(id), func(ctx context.Context) error { return o.runRestore(ctx, id) })
}

func (o *recoveryTaskOwner) StartRestart(id configservice.RestartID) bool {
	if o == nil || o.runRestart == nil {
		return false
	}
	return o.start("restart", string(id), func(ctx context.Context) error { return o.runRestart(ctx, id) })
}

func (o *recoveryTaskOwner) StartRetention(id configservice.RetentionRunID) bool {
	if o == nil || o.runRetention == nil {
		return false
	}
	return o.start("retention", string(id), func(ctx context.Context) error { return o.runRetention(ctx, id) })
}

func (o *recoveryTaskOwner) start(kind, id string, run func(context.Context) error) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopping {
		return false
	}
	o.wait.Add(1)
	go func() {
		defer o.wait.Done()
		if err := run(o.ctx); err != nil && o.logger != nil {
			o.logger.Error("configuration recovery task finished with failure",
				"operation_kind", kind, "object_id", id, "error", err)
		}
	}()
	return true
}

func (o *recoveryTaskOwner) Stop(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	o.stopping = true
	o.mu.Unlock()
	done := make(chan struct{})
	go func() {
		o.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		o.cancel()
		return nil
	case <-ctx.Done():
		o.cancel()
		return fmt.Errorf("stop configuration recovery tasks: %w", ctx.Err())
	}
}
