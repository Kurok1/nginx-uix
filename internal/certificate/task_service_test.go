/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestTaskServicePersistsCancellationAndReturnsReconnectStages(t *testing.T) {
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	repository := &taskRepositoryStub{task: validHTTPChallengeTask(now)}
	repository.task.Stages = []TaskStage{
		{TaskID: repository.task.ID, Sequence: 1, Stage: TaskStageQueued, Result: StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: now},
		{TaskID: repository.task.ID, Sequence: 2, Stage: TaskStagePreparing, Result: StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: now},
	}
	service, err := NewTaskService(TaskServiceOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	stages, err := service.Stages(context.Background(), repository.task.ID, 1, 10)
	if err != nil || len(stages) != 1 || stages[0].Sequence != 2 {
		t.Fatalf("Stages()=%#v error=%v", stages, err)
	}
	cancelled, err := service.Cancel(
		context.Background(), config.Actor{UserID: 7, RequestID: "request-cancel"}, repository.task.ID,
	)
	if err != nil || cancelled.CancelRequestedAt != now || repository.requestID != "request-cancel" {
		t.Fatalf("Cancel()=%#v error=%v repository=%#v", cancelled, err, repository)
	}
}

type taskRepositoryStub struct {
	task      Task
	requestID string
}

func (repository *taskRepositoryStub) CertificateTask(context.Context, TaskID) (Task, error) {
	return repository.task, nil
}

func (repository *taskRepositoryStub) CertificateTasks(context.Context, int) ([]Task, error) {
	return []Task{repository.task}, nil
}

func (repository *taskRepositoryStub) RequestCertificateTaskCancellation(
	_ context.Context, _ TaskID, _ int64, requestID string, at time.Time,
) (Task, error) {
	repository.requestID = requestID
	repository.task.CancelRequestedAt = at
	repository.task.UpdatedAt = at
	return repository.task, nil
}
