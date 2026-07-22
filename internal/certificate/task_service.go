/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const certificateTaskListLimit = 100

// TaskRepository owns task history and durable cancellation intent.
type TaskRepository interface {
	CertificateTask(context.Context, TaskID) (Task, error)
	CertificateTasks(context.Context, int) ([]Task, error)
	RequestCertificateTaskCancellation(context.Context, TaskID, int64, string, time.Time) (Task, error)
}

// TaskServiceOptions are the safe task-query dependencies.
type TaskServiceOptions struct {
	Repository TaskRepository
	Now        func() time.Time
}

// TaskService exposes durable task state without owning goroutines.
type TaskService struct {
	repository TaskRepository
	now        func() time.Time
}

// NewTaskService validates task query and cancellation dependencies.
func NewTaskService(options TaskServiceOptions) (*TaskService, error) {
	if options.Repository == nil {
		return nil, fmt.Errorf("create certificate task service: repository is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &TaskService{repository: options.Repository, now: options.Now}, nil
}

// Tasks returns newest-first bounded durable history.
func (service *TaskService) Tasks(ctx context.Context, limit int) ([]Task, error) {
	if ctx == nil || service == nil || limit <= 0 || limit > certificateTaskListLimit {
		return nil, fmt.Errorf("list certificate tasks: invalid input")
	}
	return service.repository.CertificateTasks(ctx, limit)
}

// Task returns one task and its current persisted stages.
func (service *TaskService) Task(ctx context.Context, id TaskID) (Task, error) {
	if ctx == nil || service == nil || parseOpaqueID(string(id)) != nil {
		return Task{}, fmt.Errorf("read certificate task: invalid input")
	}
	return service.repository.CertificateTask(ctx, id)
}

// Stages returns an ordered reconnect window after one SSE sequence.
func (service *TaskService) Stages(
	ctx context.Context,
	id TaskID,
	after uint64,
	limit int,
) ([]TaskStage, error) {
	if limit <= 0 || limit > 512 {
		return nil, fmt.Errorf("list certificate task stages: invalid input")
	}
	task, err := service.Task(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]TaskStage, 0, min(limit, len(task.Stages)))
	for _, stage := range task.Stages {
		if stage.Sequence <= after {
			continue
		}
		result = append(result, stage)
		if len(result) == limit {
			break
		}
	}
	return slices.Clone(result), nil
}

// Cancel records intent; the request-independent owner performs cleanup and terminal transition.
func (service *TaskService) Cancel(
	ctx context.Context,
	actor config.Actor,
	id TaskID,
) (Task, error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) ||
		parseOpaqueID(string(id)) != nil {
		return Task{}, fmt.Errorf("cancel certificate task: invalid input")
	}
	task, err := service.repository.RequestCertificateTaskCancellation(
		ctx, id, actor.UserID, actor.RequestID, service.now().UTC(),
	)
	if err != nil {
		return Task{}, fmt.Errorf("cancel certificate task: %w", err)
	}
	return task, nil
}
