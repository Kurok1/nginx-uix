/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/certificate"
)

func TestCertificateTaskOwnerAcceptsBoundedPendingWorkWithoutExceedingNetworkConcurrency(t *testing.T) {
	started := make(chan certificate.TaskID, 6)
	release := make(chan struct{})
	owner := newCertificateTaskOwner(context.Background(), func(
		ctx context.Context,
		id certificate.TaskID,
	) (certificate.Task, error) {
		started <- id
		select {
		case <-release:
			return certificate.Task{ID: id, State: certificate.TaskStateSucceeded}, nil
		case <-ctx.Done():
			return certificate.Task{ID: id, State: certificate.TaskStateCancelled}, ctx.Err()
		}
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for index := 1; index <= 6; index++ {
		id := certificate.TaskID(fmt.Sprintf("%032x", index))
		if !owner.Start(certificate.Task{ID: id}) {
			t.Fatalf("Start(task %d) = false; bounded pending work must be accepted", index)
		}
	}
	for index := 0; index < certificateTaskConcurrency; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for an owned task to start")
		}
	}
	select {
	case id := <-started:
		t.Fatalf("task %s started above concurrency limit", id)
	case <-time.After(25 * time.Millisecond):
	}

	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("pending task was stranded after a network slot became available")
	}
	close(release)
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := owner.Stop(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestCertificateTaskOwnerCancelsPendingWorkBeforeItStarts(t *testing.T) {
	started := make(chan certificate.TaskID, certificateTaskConcurrency+1)
	release := make(chan struct{})
	owner := newCertificateTaskOwner(context.Background(), func(
		ctx context.Context,
		id certificate.TaskID,
	) (certificate.Task, error) {
		started <- id
		select {
		case <-release:
			return certificate.Task{ID: id, State: certificate.TaskStateSucceeded}, nil
		case <-ctx.Done():
			return certificate.Task{ID: id, State: certificate.TaskStateCancelled}, ctx.Err()
		}
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for index := 1; index <= certificateTaskConcurrency; index++ {
		id := certificate.TaskID(fmt.Sprintf("%032x", index))
		if !owner.Start(certificate.Task{ID: id}) {
			t.Fatalf("Start(active task %d) = false", index)
		}
	}
	for index := 0; index < certificateTaskConcurrency; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for active tasks")
		}
	}

	pendingID := certificate.TaskID("ffffffffffffffffffffffffffffffff")
	if !owner.Start(certificate.Task{ID: pendingID}) {
		t.Fatal("Start(pending task) = false")
	}
	if !owner.Cancel(pendingID) {
		t.Fatal("Cancel(pending task) = false")
	}
	release <- struct{}{}
	select {
	case id := <-started:
		t.Fatalf("cancelled pending task %s started", id)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := owner.Stop(shutdown); err != nil {
		t.Fatal(err)
	}
}
