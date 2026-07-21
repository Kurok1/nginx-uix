/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/routelab"
)

func TestRouteTaskOwnerCancelsOneRunAndStopsAllWork(t *testing.T) {
	started := make(chan routelab.RunID, 2)
	finished := make(chan routelab.RunID, 2)
	run := func(ctx context.Context, id routelab.RunID, _ routelab.ValidatedRequest) (routelab.Run, error) {
		started <- id
		<-ctx.Done()
		finished <- id
		return routelab.Run{}, ctx.Err()
	}
	owner := newRouteTaskOwner(context.Background(), run, nil)
	first := routelab.QueuedRun{Run: routelab.Run{ID: "11111111111111111111111111111111"}}
	second := routelab.QueuedRun{Run: routelab.Run{ID: "22222222222222222222222222222222"}}
	if !owner.Start(first) || !owner.Start(second) || owner.Start(first) {
		t.Fatal("Start() did not enforce unique active runs")
	}
	<-started
	<-started
	if !owner.Cancel(first.Run.ID) || owner.Cancel("33333333333333333333333333333333") {
		t.Fatal("Cancel() did not target exactly one active run")
	}
	select {
	case id := <-finished:
		if id != first.Run.ID {
			t.Fatalf("first finished id = %s", id)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled route task did not finish")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := owner.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if owner.Start(routelab.QueuedRun{Run: routelab.Run{ID: "44444444444444444444444444444444"}}) {
		t.Fatal("Start() accepted work after Stop()")
	}
	select {
	case id := <-finished:
		if id != second.Run.ID {
			t.Fatalf("second finished id = %s", id)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not cancel remaining route task")
	}
}

func TestRouteTaskOwnerStopDeadlineIsExplicit(t *testing.T) {
	release := make(chan struct{})
	owner := newRouteTaskOwner(context.Background(), func(
		context.Context,
		routelab.RunID,
		routelab.ValidatedRequest,
	) (routelab.Run, error) {
		<-release
		return routelab.Run{}, nil
	}, nil)
	if !owner.Start(routelab.QueuedRun{Run: routelab.Run{ID: "55555555555555555555555555555555"}}) {
		t.Fatal("Start() = false")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := owner.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline", err)
	}
	close(release)
}
