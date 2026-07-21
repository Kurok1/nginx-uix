/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/kuroky/nginx-uix/internal/routelab"
)

// routeTaskOwner owns request-independent Route Lab work and per-run cancellation.
type routeTaskOwner struct {
	run      func(context.Context, routelab.RunID, routelab.ValidatedRequest) (routelab.Run, error)
	logger   *slog.Logger
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	stopping bool
	active   map[routelab.RunID]context.CancelFunc
	wait     sync.WaitGroup
}

func newRouteTaskOwner(
	parent context.Context,
	run func(context.Context, routelab.RunID, routelab.ValidatedRequest) (routelab.Run, error),
	logger *slog.Logger,
) *routeTaskOwner {
	// #nosec G118 -- Stop owns cancellation and grants the documented bounded cleanup window.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	return &routeTaskOwner{
		run: run, logger: logger, ctx: ctx, cancel: cancel,
		active: make(map[routelab.RunID]context.CancelFunc),
	}
}

func (owner *routeTaskOwner) Start(queued routelab.QueuedRun) bool {
	if owner == nil || owner.run == nil {
		return false
	}
	if _, err := routelab.ParseRunID(string(queued.Run.ID)); err != nil {
		return false
	}
	owner.mu.Lock()
	if owner.stopping {
		owner.mu.Unlock()
		return false
	}
	if _, duplicate := owner.active[queued.Run.ID]; duplicate {
		owner.mu.Unlock()
		return false
	}
	runContext, cancel := context.WithCancel(owner.ctx)
	owner.active[queued.Run.ID] = cancel
	owner.wait.Add(1)
	owner.mu.Unlock()

	go func() {
		defer owner.wait.Done()
		defer cancel()
		defer func() {
			owner.mu.Lock()
			delete(owner.active, queued.Run.ID)
			owner.mu.Unlock()
		}()
		_, err := owner.run(runContext, queued.Run.ID, queued.Request)
		if err != nil && !errors.Is(err, context.Canceled) && owner.logger != nil {
			owner.logger.Error("route lab task finished with failure",
				"run_id", queued.Run.ID, "result", "failed", "error", err)
		}
	}()
	return true
}

func (owner *routeTaskOwner) Cancel(id routelab.RunID) bool {
	if owner == nil {
		return false
	}
	owner.mu.Lock()
	cancel, exists := owner.active[id]
	owner.mu.Unlock()
	if !exists {
		return false
	}
	cancel()
	return true
}

func (owner *routeTaskOwner) Stop(ctx context.Context) error {
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	owner.stopping = true
	cancellations := make([]context.CancelFunc, 0, len(owner.active))
	for _, cancel := range owner.active {
		cancellations = append(cancellations, cancel)
	}
	owner.mu.Unlock()
	owner.cancel()
	for _, cancel := range cancellations {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		owner.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop route lab tasks: %w", ctx.Err())
	}
}
