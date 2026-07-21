/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package store

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/routelab"
)

func TestRouteRunRepositoryTransitionsAndListsStableHistory(t *testing.T) {
	database := openRepositoryDatabase(t)
	first := testRouteRun(1, testTime(2))
	second := testRouteRun(2, testTime(3))
	for _, run := range []routelab.Run{first, second} {
		if err := database.CreateRouteRun(context.Background(), run, routelab.RunStage{
			RunID: run.ID, Sequence: 1, Stage: routelab.RunStageQueued,
			Result: routelab.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: run.CreatedAt,
		}); err != nil {
			t.Fatalf("CreateRouteRun() error = %v", err)
		}
	}

	running := first
	running.State = routelab.RunStateRunning
	running.Stage = routelab.RunStagePreparing
	running.StartedAt = testTime(4)
	running.UpdatedAt = running.StartedAt
	if err := database.TransitionRouteRun(
		context.Background(), routelab.RunStateQueued, routelab.RunStageQueued, running,
		routelab.RunStage{
			RunID: first.ID, Sequence: 2, Stage: running.Stage,
			Result: routelab.StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: running.UpdatedAt,
		},
	); err != nil {
		t.Fatalf("TransitionRouteRun() error = %v", err)
	}
	completed := running
	completed.State = routelab.RunStateSucceeded
	completed.Stage = routelab.RunStageCompleted
	completed.CandidateDigest = testDigest(0x71)
	completed.TerminalResultJSON = `{"response":{"status_code":200}}`
	completed.UpdatedAt = testTime(5)
	completed.FinishedAt = completed.UpdatedAt
	if err := database.TransitionRouteRun(
		context.Background(), routelab.RunStateRunning, routelab.RunStagePreparing, completed,
		routelab.RunStage{
			RunID: first.ID, Sequence: 3, Stage: completed.Stage,
			Result: routelab.StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: completed.UpdatedAt,
		},
	); err != nil {
		t.Fatalf("TransitionRouteRun() error = %v", err)
	}

	got, err := database.RouteRun(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	completed.CreatedAt = completed.CreatedAt.UTC()
	completed.UpdatedAt = completed.UpdatedAt.UTC()
	completed.StartedAt = completed.StartedAt.UTC()
	completed.FinishedAt = completed.FinishedAt.UTC()
	if !reflect.DeepEqual(got, completed) {
		t.Fatalf("RouteRun() = %#v, want %#v", got, completed)
	}
	stages, err := database.RouteRunStages(context.Background(), first.ID, 0, 10)
	if err != nil || len(stages) != 3 || stages[2].Stage != routelab.RunStageCompleted {
		t.Fatalf("RouteRunStages() = %+v, %v", stages, err)
	}
	history, err := database.ListRouteRuns(context.Background(), routelab.HistoryQuery{Limit: 10})
	if err != nil || len(history) != 2 || history[0].ID != second.ID || history[1].ID != first.ID {
		t.Fatalf("ListRouteRuns() = %+v, %v", history, err)
	}
}

func TestRouteRunCancellationAndRestartReconciliationAreDurable(t *testing.T) {
	database := openRepositoryDatabase(t)
	queued := testRouteRun(3, testTime(2))
	if err := database.CreateRouteRun(context.Background(), queued, testRouteStage(queued, 1)); err != nil {
		t.Fatal(err)
	}
	cancelled, err := database.RequestRouteRunCancellation(context.Background(), queued.ID, testTime(3))
	if err != nil || cancelled.State != routelab.RunStateCancelled || cancelled.Stage != routelab.RunStageCancelled ||
		cancelled.LastErrorCode != "cancelled_by_user" || cancelled.CancelRequestedAt.IsZero() {
		t.Fatalf("RequestRouteRunCancellation() = %+v, %v", cancelled, err)
	}
	if _, err := database.RequestRouteRunCancellation(context.Background(), queued.ID, testTime(4)); !errors.Is(err, routelab.ErrAlreadyTerminal) {
		t.Fatalf("second cancellation error = %v", err)
	}

	running := testRouteRun(4, testTime(4))
	if err := database.CreateRouteRun(context.Background(), running, testRouteStage(running, 1)); err != nil {
		t.Fatal(err)
	}
	next := running
	next.State = routelab.RunStateRunning
	next.Stage = routelab.RunStagePreparing
	next.StartedAt = testTime(5)
	next.UpdatedAt = next.StartedAt
	if err := database.TransitionRouteRun(context.Background(), running.State, running.Stage, next, routelab.RunStage{
		RunID: running.ID, Sequence: 2, Stage: next.Stage, Result: routelab.StageResultRunning,
		PublicDetailsJSON: `{}`, OccurredAt: next.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	count, err := database.FailInterruptedRouteRuns(context.Background(), testTime(6), "ui_interrupted")
	if err != nil || count != 1 {
		t.Fatalf("FailInterruptedRouteRuns() = %d, %v", count, err)
	}
	failed, err := database.RouteRun(context.Background(), running.ID)
	if err != nil || failed.State != routelab.RunStateFailed || failed.LastErrorCode != "ui_interrupted" {
		t.Fatalf("interrupted run = %+v, %v", failed, err)
	}
	if _, err := database.RouteRun(context.Background(), "ffffffffffffffffffffffffffffffff"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing RouteRun() error = %v", err)
	}
}

func testRouteRun(number int, createdAt time.Time) routelab.Run {
	return routelab.Run{
		ID: routelab.RunID(formatTestID(number)), WorkspaceID: testWorkspace(number, "route", createdAt).ID,
		WorkspaceRevision: 1, WorkspaceETag: `"draft:etag"`,
		ProductionDigest: testDigest(0x41), DraftDigest: testDigest(0x42),
		State: routelab.RunStateQueued, Stage: routelab.RunStageQueued,
		SafeRequestJSON:    `{"scheme":"http","method":"GET"}`,
		StaticAnalysisJSON: `{"complete":true}`,
		Replayable:         true, BodyDigest: testDigest(0x43), SensitiveHeaderNamesJSON: `[]`,
		CreatedBy: 1, RequestID: "route-request", CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func testRouteStage(run routelab.Run, sequence uint64) routelab.RunStage {
	return routelab.RunStage{
		RunID: run.ID, Sequence: sequence, Stage: run.Stage, Result: routelab.StageResultPending,
		PublicDetailsJSON: `{}`, OccurredAt: run.UpdatedAt,
	}
}

func formatTestID(number int) string {
	const digits = "00000000000000000000000000000000"
	text := []byte(digits)
	text[len(text)-1] = byte('0' + number)
	return string(text)
}
