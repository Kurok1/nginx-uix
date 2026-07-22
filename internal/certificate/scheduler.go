/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	maximumConcurrentRenewals = 4
	renewalSchedulerInterval  = time.Hour
)

var errCertificateTaskOwnerUnavailable = errors.New("certificate task owner unavailable")

// RenewalSchedulerRepository returns persisted, stably ordered due work.
type RenewalSchedulerRepository interface {
	DueCertificates(context.Context, time.Time, int) ([]Certificate, error)
}

// AutomaticRenewalQueue creates the same reviewed renewal contract as the manual path.
type AutomaticRenewalQueue interface {
	QueueAutomatic(context.Context, CertificateID) (Task, error)
}

// CertificateTaskStarter transfers one durable queued task to its request-independent owner.
type CertificateTaskStarter interface {
	Start(Task) bool
}

// RenewalSchedulerOptions are the bounded polling dependencies.
type RenewalSchedulerOptions struct {
	Repository RenewalSchedulerRepository
	Queue      AutomaticRenewalQueue
	Tasks      CertificateTaskStarter
	Now        func() time.Time
}

// RenewalScheduler restores due timestamps directly from SQLite and never recomputes them on startup.
type RenewalScheduler struct {
	repository RenewalSchedulerRepository
	queue      AutomaticRenewalQueue
	tasks      CertificateTaskStarter
	now        func() time.Time
}

// NewRenewalScheduler validates the automatic renewal worker dependencies.
func NewRenewalScheduler(options RenewalSchedulerOptions) (*RenewalScheduler, error) {
	if options.Repository == nil || options.Queue == nil || options.Tasks == nil {
		return nil, fmt.Errorf("create certificate renewal scheduler: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &RenewalScheduler{
		repository: options.Repository, queue: options.Queue, tasks: options.Tasks, now: options.Now,
	}, nil
}

// RunOnce queues at most four persisted due certificates in repository order.
func (scheduler *RenewalScheduler) RunOnce(ctx context.Context) ([]Task, error) {
	if ctx == nil || scheduler == nil {
		return nil, fmt.Errorf("run certificate renewal scheduler: service is unavailable")
	}
	items, err := scheduler.repository.DueCertificates(ctx, scheduler.now().UTC(), maximumConcurrentRenewals)
	if err != nil {
		return nil, fmt.Errorf("run certificate renewal scheduler: list due: %w", err)
	}
	if len(items) > maximumConcurrentRenewals {
		return nil, fmt.Errorf("run certificate renewal scheduler: invalid due batch")
	}
	started := make([]Task, 0, len(items))
	var runErr error
	for _, item := range items {
		if parseOpaqueID(string(item.ID)) != nil {
			runErr = errors.Join(runErr, ErrIdentifierInvalid)
			continue
		}
		task, queueErr := scheduler.queue.QueueAutomatic(ctx, item.ID)
		if queueErr != nil {
			if !errors.Is(queueErr, ErrTaskActive) {
				runErr = errors.Join(runErr, queueErr)
			}
			continue
		}
		if !scheduler.tasks.Start(task) {
			runErr = errors.Join(runErr, errCertificateTaskOwnerUnavailable)
			continue
		}
		started = append(started, task)
	}
	return started, runErr
}

// Run performs one startup poll and then polls hourly until cancellation.
func (scheduler *RenewalScheduler) Run(ctx context.Context) error {
	if ctx == nil || scheduler == nil {
		return fmt.Errorf("run certificate renewal scheduler: service is unavailable")
	}
	_, runErr := scheduler.RunOnce(ctx)
	ticker := time.NewTicker(renewalSchedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return runErr
		case <-ticker.C:
			_, err := scheduler.RunOnce(ctx)
			runErr = errors.Join(runErr, err)
		}
	}
}

// RenewalRetryTime fixes exponential 1h..24h backoff with cryptographic ±10% jitter.
func RenewalRetryTime(now time.Time, retryCount int, random io.Reader) (time.Time, error) {
	if now.IsZero() || retryCount <= 0 || random == nil {
		return time.Time{}, fmt.Errorf("calculate certificate renewal retry: invalid input")
	}
	hours := 1 << min(retryCount-1, 4)
	if retryCount >= 6 {
		hours = 24
	}
	base := time.Duration(hours) * time.Hour
	jitterLimit := base / 10
	var raw [6]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return time.Time{}, fmt.Errorf("calculate certificate renewal retry: %w", err)
	}
	sample := int64(raw[0])<<40 | int64(raw[1])<<32 | int64(raw[2])<<24 |
		int64(raw[3])<<16 | int64(raw[4])<<8 | int64(raw[5])
	span := int64(2*jitterLimit + 1)
	jitter := time.Duration(sample%span) - jitterLimit
	return now.UTC().Add(base + jitter), nil
}
