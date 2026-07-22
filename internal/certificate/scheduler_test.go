/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestRenewalRetryTimeUsesExponentialCapAndBoundedJitter(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	for retryCount, base := range []time.Duration{
		time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour,
	} {
		retryAt, err := RenewalRetryTime(now, retryCount+1, bytes.NewReader(make([]byte, 8)))
		if err != nil {
			t.Fatal(err)
		}
		minimum := now.Add(base - base/10)
		maximum := now.Add(base + base/10)
		if retryAt.Before(minimum) || retryAt.After(maximum) {
			t.Fatalf("retry %d = %v, want [%v,%v]", retryCount+1, retryAt, minimum, maximum)
		}
	}
}

func TestRenewalSchedulerQueuesStableBoundedDueBatch(t *testing.T) {
	now := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	repository := &schedulerRepositoryStub{items: []Certificate{
		{ID: "11111111111111111111111111111111"},
		{ID: "22222222222222222222222222222222"},
	}}
	queue := &automaticRenewalQueueStub{}
	starter := &certificateTaskStarterStub{}
	scheduler, err := NewRenewalScheduler(RenewalSchedulerOptions{
		Repository: repository, Queue: queue, Tasks: starter, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if repository.limit != maximumConcurrentRenewals || len(started) != 2 ||
		len(queue.ids) != 2 || len(starter.tasks) != 2 {
		t.Fatalf("started=%#v repository=%#v queue=%#v starter=%#v", started, repository, queue, starter)
	}
}

type schedulerRepositoryStub struct {
	items []Certificate
	limit int
}

func (repository *schedulerRepositoryStub) DueCertificates(
	_ context.Context, _ time.Time, limit int,
) ([]Certificate, error) {
	repository.limit = limit
	return append([]Certificate{}, repository.items...), nil
}

type automaticRenewalQueueStub struct{ ids []CertificateID }

func (queue *automaticRenewalQueueStub) QueueAutomatic(_ context.Context, id CertificateID) (Task, error) {
	queue.ids = append(queue.ids, id)
	return Task{ID: TaskID(id), CertificateID: id, Kind: TaskKindRenew}, nil
}

type certificateTaskStarterStub struct{ tasks []Task }

func (starter *certificateTaskStarterStub) Start(task Task) bool {
	starter.tasks = append(starter.tasks, task)
	return true
}
