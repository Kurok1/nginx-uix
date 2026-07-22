/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/kuroky/nginx-uix/internal/certificate"
)

const (
	certificateTaskConcurrency = 4
	certificateTaskOwnerLimit  = 100
)

// certificateTaskOwner bounds request-independent ACME work and owns exact cancellation contexts.
type certificateTaskOwner struct {
	run      func(context.Context, certificate.TaskID) (certificate.Task, error)
	logger   *slog.Logger
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	stopping bool
	active   map[certificate.TaskID]context.CancelFunc
	pending  map[certificate.TaskID]context.CancelFunc
	slots    chan struct{}
	wait     sync.WaitGroup
}

func newCertificateTaskOwner(
	parent context.Context,
	run func(context.Context, certificate.TaskID) (certificate.Task, error),
	logger *slog.Logger,
) *certificateTaskOwner {
	// #nosec G118 -- Stop owns cancellation and grants each task its bounded cleanup window.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	return &certificateTaskOwner{
		run: run, logger: logger, ctx: ctx, cancel: cancel,
		active:  make(map[certificate.TaskID]context.CancelFunc, certificateTaskConcurrency),
		pending: make(map[certificate.TaskID]context.CancelFunc, certificateTaskOwnerLimit),
		slots:   make(chan struct{}, certificateTaskConcurrency),
	}
}

func (owner *certificateTaskOwner) Start(task certificate.Task) bool {
	if owner == nil || owner.run == nil {
		return false
	}
	id, err := certificate.ParseTaskID(string(task.ID))
	if err != nil || id != task.ID {
		return false
	}
	owner.mu.Lock()
	if owner.stopping || len(owner.active)+len(owner.pending) >= certificateTaskOwnerLimit {
		owner.mu.Unlock()
		return false
	}
	if _, duplicate := owner.active[task.ID]; duplicate {
		owner.mu.Unlock()
		return false
	}
	if _, duplicate := owner.pending[task.ID]; duplicate {
		owner.mu.Unlock()
		return false
	}
	taskContext, cancel := context.WithCancel(owner.ctx)
	owner.pending[task.ID] = cancel
	owner.wait.Add(1)
	owner.mu.Unlock()

	go func() {
		defer owner.wait.Done()
		defer cancel()
		select {
		case owner.slots <- struct{}{}:
			defer func() { <-owner.slots }()
		case <-taskContext.Done():
			owner.removePending(task.ID)
			return
		}
		if taskContext.Err() != nil {
			owner.removePending(task.ID)
			return
		}
		owner.mu.Lock()
		delete(owner.pending, task.ID)
		if owner.stopping || taskContext.Err() != nil {
			owner.mu.Unlock()
			return
		}
		owner.active[task.ID] = cancel
		owner.mu.Unlock()
		defer func() {
			owner.mu.Lock()
			delete(owner.active, task.ID)
			owner.mu.Unlock()
		}()
		result, runErr := owner.run(taskContext, task.ID)
		if runErr != nil && !errors.Is(runErr, context.Canceled) && owner.logger != nil {
			owner.logger.Error("certificate task finished with failure",
				"task_id", task.ID, "state", result.State, "stage", result.Stage,
				"error_code", result.LastErrorCode, "result", "failed")
		}
	}()
	return true
}

func (owner *certificateTaskOwner) Cancel(id certificate.TaskID) bool {
	if owner == nil {
		return false
	}
	owner.mu.Lock()
	if cancel, pending := owner.pending[id]; pending {
		cancel()
		owner.mu.Unlock()
		return true
	}
	cancel, exists := owner.active[id]
	owner.mu.Unlock()
	if !exists {
		return false
	}
	cancel()
	return true
}

func (owner *certificateTaskOwner) removePending(id certificate.TaskID) {
	owner.mu.Lock()
	delete(owner.pending, id)
	owner.mu.Unlock()
}

func (owner *certificateTaskOwner) Stop(ctx context.Context) error {
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
		return fmt.Errorf("stop certificate tasks: %w", ctx.Err())
	}
}
