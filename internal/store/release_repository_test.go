/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */

package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kuroky/nginx-uix/internal/auth"
	"github.com/kuroky/nginx-uix/internal/config"
)

func TestReleaseRepositoryPersistsCheckReleaseStagesAndBackup(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create-release-test")

	check := testPublishCheck(workspace)
	if err := database.CreatePublishCheck(context.Background(), check); err != nil {
		t.Fatalf("CreatePublishCheck() error = %v", err)
	}
	gotCheck, err := database.PublishCheck(context.Background(), check.ID)
	if err != nil {
		t.Fatalf("PublishCheck() error = %v", err)
	}
	check.StartedAt = check.StartedAt.UTC()
	check.FinishedAt = check.FinishedAt.UTC()
	check.ExpiresAt = check.ExpiresAt.UTC()
	if !reflect.DeepEqual(gotCheck, check) {
		t.Fatalf("PublishCheck() = %#v, want %#v", gotCheck, check)
	}

	release := testRelease(workspace, check)
	release.BackupID = config.BackupID("00000000000000000000000000000004")
	initial := config.ReleaseStage{
		ReleaseID: release.ID, Sequence: 1, Stage: config.ReleaseStageQueued,
		Result: config.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: release.CreatedAt,
	}
	if err := database.CreateRelease(context.Background(), release, initial); err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}

	release.State = config.ReleaseStateRunning
	release.Stage = config.ReleaseStageRechecking
	release.UpdatedAt = testTime(5)
	next := config.ReleaseStage{
		ReleaseID: release.ID, Sequence: 2, Stage: config.ReleaseStageRechecking,
		Result: config.StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: release.UpdatedAt,
	}
	if err := database.TransitionRelease(
		context.Background(), config.ReleaseStateQueued, config.ReleaseStageQueued, release, next,
	); err != nil {
		t.Fatalf("TransitionRelease() error = %v", err)
	}

	backup := config.Backup{
		ID:         config.BackupID("00000000000000000000000000000004"),
		OriginType: config.BackupOriginRelease, OriginID: string(release.ID), ReleaseID: release.ID,
		ProductionDigest: release.ProductionDigest, TreeDigest: testDigest(0x55),
		State: config.BackupStateComplete, EntryCount: 7, TotalBytes: 2048, BodyPresent: true,
		CreatedAt: testTime(6), VerifiedAt: testTime(7),
	}
	if err := database.PutBackup(context.Background(), backup); err != nil {
		t.Fatalf("PutBackup() error = %v", err)
	}

	gotRelease, err := database.Release(context.Background(), release.ID)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	release.CreatedAt = release.CreatedAt.UTC()
	release.UpdatedAt = release.UpdatedAt.UTC()
	if !reflect.DeepEqual(gotRelease, release) {
		t.Fatalf("Release() = %#v, want %#v", gotRelease, release)
	}
	stages, err := database.ReleaseStages(context.Background(), release.ID, 0, 10)
	if err != nil {
		t.Fatalf("ReleaseStages() error = %v", err)
	}
	if len(stages) != 2 || stages[0].Stage != config.ReleaseStageQueued || stages[1].Stage != config.ReleaseStageRechecking {
		t.Fatalf("ReleaseStages() = %#v, want queued then rechecking", stages)
	}
	gotBackup, err := database.Backup(context.Background(), backup.ID)
	if err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	backup.CreatedAt = backup.CreatedAt.UTC()
	backup.VerifiedAt = backup.VerifiedAt.UTC()
	if gotBackup != backup {
		t.Fatalf("Backup() = %#v, want %#v", gotBackup, backup)
	}
	for objectID, want := range map[string]int{string(check.ID): 2, string(release.ID): 2} {
		var count int
		if err := database.sql.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM audit_events WHERE object_id = ?", objectID,
		).Scan(&count); err != nil {
			t.Fatalf("count audit events for %s: %v", objectID, err)
		}
		if count != want {
			t.Fatalf("audit events for %s = %d, want %d", objectID, count, want)
		}
	}
}

func TestReleaseRepositoryCompletesLegacyBackupIntegrityIndex(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Legacy recovery", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create-legacy-backup")
	check := testPublishCheck(workspace)
	if err := database.CreatePublishCheck(context.Background(), check); err != nil {
		t.Fatal(err)
	}
	release := testRelease(workspace, check)
	release.State = config.ReleaseStateSucceeded
	release.Stage = config.ReleaseStageCommitted
	release.FinishedAt = release.UpdatedAt
	release.BackupID = "00000000000000000000000000000004"
	if _, err := database.sql.ExecContext(context.Background(), `INSERT INTO config_releases(
		id, workspace_id, check_id, backup_id, state, stage, production_digest, draft_digest,
		candidate_digest, last_error_code, created_by, request_id, created_at, updated_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?)`, release.ID, release.WorkspaceID,
		release.CheckID, release.BackupID, release.State, release.Stage, release.ProductionDigest[:],
		release.DraftDigest[:], release.CandidateDigest[:], release.CreatedBy, release.RequestID,
		formatTime(release.CreatedAt), formatTime(release.UpdatedAt), formatTime(release.FinishedAt)); err != nil {
		t.Fatal(err)
	}
	legacy := config.Backup{
		ID: release.BackupID, OriginType: config.BackupOriginRelease, OriginID: string(release.ID),
		ReleaseID: release.ID, ProductionDigest: release.ProductionDigest, State: config.BackupStateComplete,
		EntryCount: 7, TotalBytes: 2048, BodyPresent: true, CreatedAt: testTime(6), VerifiedAt: testTime(7),
	}
	if err := database.PutBackup(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	completed := legacy
	completed.TreeDigest = testDigest(0x55)
	if err := database.PutBackup(context.Background(), completed); err != nil {
		t.Fatal(err)
	}
	got, err := database.Backup(context.Background(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TreeDigest != completed.TreeDigest {
		t.Fatalf("legacy tree digest = %x, want %x", got.TreeDigest, completed.TreeDigest)
	}
}

func TestReleaseRepositoryAllowsOnlyOneActiveReleaseAndExactStageCAS(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create-release-cas")
	check := testPublishCheck(workspace)
	if err := database.CreatePublishCheck(context.Background(), check); err != nil {
		t.Fatal(err)
	}

	release := testRelease(workspace, check)
	initial := config.ReleaseStage{
		ReleaseID: release.ID, Sequence: 1, Stage: config.ReleaseStageQueued,
		Result: config.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: release.CreatedAt,
	}
	if err := database.CreateRelease(context.Background(), release, initial); err != nil {
		t.Fatal(err)
	}
	other := release
	other.ID = config.ReleaseID("00000000000000000000000000000005")
	other.RequestID = "release-second-request"
	otherStage := initial
	otherStage.ReleaseID = other.ID
	if err := database.CreateRelease(context.Background(), other, otherStage); !errors.Is(err, config.ErrReleaseInProgress) {
		t.Fatalf("second CreateRelease() error = %v, want ErrReleaseInProgress", err)
	}

	stale := release
	stale.State = config.ReleaseStateRunning
	stale.Stage = config.ReleaseStageRechecking
	stale.UpdatedAt = testTime(5)
	staleStage := config.ReleaseStage{
		ReleaseID: release.ID, Sequence: 2, Stage: config.ReleaseStageRechecking,
		Result: config.StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: stale.UpdatedAt,
	}
	err := database.TransitionRelease(
		context.Background(), config.ReleaseStateRunning, config.ReleaseStageQueued, stale, staleStage,
	)
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("stale TransitionRelease() error = %v, want ErrConflict", err)
	}
	stages, readErr := database.ReleaseStages(context.Background(), release.ID, 0, 10)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(stages) != 1 {
		t.Fatalf("stages after stale transition = %d, want 1", len(stages))
	}
}

func TestReleaseRepositoryOwnsProductionLeaseUntilTerminalTransition(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create-release-lease")
	check := testPublishCheck(workspace)
	if err := database.CreatePublishCheck(ctx, check); err != nil {
		t.Fatal(err)
	}
	release := testRelease(workspace, check)
	initial := config.ReleaseStage{
		ReleaseID: release.ID, Sequence: 1, Stage: config.ReleaseStageQueued,
		Result: config.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: release.CreatedAt,
	}
	restoreID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := database.AcquireProductionLease(ctx, config.ProductionOperationRestore, restoreID, testTime(3)); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRelease(ctx, release, initial); !errors.Is(err, config.ErrOperationInProgress) {
		t.Fatalf("CreateRelease() with restore lease error = %v, want ErrOperationInProgress", err)
	}
	if _, err := database.Release(ctx, release.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Release() after blocked create error = %v, want fs.ErrNotExist", err)
	}
	if err := database.ReleaseProductionLease(ctx, config.ProductionOperationRestore, restoreID); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRelease(ctx, release, initial); err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	lease, err := database.ProductionLease(ctx)
	if err != nil || lease.OwnerType != config.ProductionOperationRelease || lease.OwnerID != string(release.ID) {
		t.Fatalf("release lease = %#v, error = %v", lease, err)
	}
	release.State = config.ReleaseStateSucceeded
	release.Stage = config.ReleaseStageCommitted
	release.UpdatedAt = testTime(6)
	release.FinishedAt = testTime(6)
	if err := database.TransitionRelease(ctx, config.ReleaseStateQueued, config.ReleaseStageQueued, release, config.ReleaseStage{
		ReleaseID: release.ID, Sequence: 2, Stage: config.ReleaseStageCommitted,
		Result: config.StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: release.UpdatedAt,
	}); err != nil {
		t.Fatalf("TransitionRelease() error = %v", err)
	}
	lease, err = database.ProductionLease(ctx)
	if err != nil || lease != (config.ProductionLease{}) {
		t.Fatalf("terminal release lease = %#v, error = %v, want empty", lease, err)
	}
}

func TestReleaseRepositoryReturnsNotFoundForMissingRecords(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()

	if _, err := database.PublishCheck(ctx, config.PublishCheckID("00000000000000000000000000000001")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("PublishCheck() error = %v, want fs.ErrNotExist", err)
	}
	if _, err := database.Release(ctx, config.ReleaseID("00000000000000000000000000000002")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Release() error = %v, want fs.ErrNotExist", err)
	}
	if _, err := database.Backup(ctx, config.BackupID("00000000000000000000000000000003")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Backup() error = %v, want fs.ErrNotExist", err)
	}
}

func TestReleaseRepositoryStageAndAuditWritesAreAtomic(t *testing.T) {
	t.Run("publish check", func(t *testing.T) {
		database := openRepositoryDatabase(t)
		workspace := testWorkspace(1, "Primary", testTime(2))
		createWorkspaceRecord(t, database, workspace, "config.workspace.create-release-audit-check")
		breakAuditInsert(t, database)

		check := testPublishCheck(workspace)
		if err := database.CreatePublishCheck(context.Background(), check); err == nil {
			t.Fatal("CreatePublishCheck() error = nil, want injected audit failure")
		}
		if _, err := database.PublishCheck(context.Background(), check.ID); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("PublishCheck() error = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("initial release stage", func(t *testing.T) {
		database := openRepositoryDatabase(t)
		workspace := testWorkspace(1, "Primary", testTime(2))
		createWorkspaceRecord(t, database, workspace, "config.workspace.create-release-audit-create")
		check := testPublishCheck(workspace)
		if err := database.CreatePublishCheck(context.Background(), check); err != nil {
			t.Fatal(err)
		}
		breakAuditInsert(t, database)

		release := testRelease(workspace, check)
		stage := config.ReleaseStage{
			ReleaseID: release.ID, Sequence: 1, Stage: config.ReleaseStageQueued,
			Result: config.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: release.CreatedAt,
		}
		if err := database.CreateRelease(context.Background(), release, stage); err == nil {
			t.Fatal("CreateRelease() error = nil, want injected audit failure")
		}
		if _, err := database.Release(context.Background(), release.ID); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Release() error = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("release transition", func(t *testing.T) {
		database := openRepositoryDatabase(t)
		workspace := testWorkspace(1, "Primary", testTime(2))
		createWorkspaceRecord(t, database, workspace, "config.workspace.create-release-audit-transition")
		check := testPublishCheck(workspace)
		if err := database.CreatePublishCheck(context.Background(), check); err != nil {
			t.Fatal(err)
		}
		release := testRelease(workspace, check)
		initial := config.ReleaseStage{
			ReleaseID: release.ID, Sequence: 1, Stage: config.ReleaseStageQueued,
			Result: config.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: release.CreatedAt,
		}
		if err := database.CreateRelease(context.Background(), release, initial); err != nil {
			t.Fatal(err)
		}
		breakAuditInsert(t, database)

		next := release
		next.State = config.ReleaseStateRunning
		next.Stage = config.ReleaseStageRechecking
		next.UpdatedAt = testTime(5)
		stage := config.ReleaseStage{
			ReleaseID: release.ID, Sequence: 2, Stage: config.ReleaseStageRechecking,
			Result: config.StageResultRunning, PublicDetailsJSON: `{}`, OccurredAt: next.UpdatedAt,
		}
		if err := database.TransitionRelease(context.Background(), release.State, release.Stage, next, stage); err == nil {
			t.Fatal("TransitionRelease() error = nil, want injected audit failure")
		}
		stored, err := database.Release(context.Background(), release.ID)
		if err != nil || stored.State != config.ReleaseStateQueued || stored.Stage != config.ReleaseStageQueued {
			t.Fatalf("Release() = %+v, error = %v", stored, err)
		}
		stages, err := database.ReleaseStages(context.Background(), release.ID, 0, 10)
		if err != nil || len(stages) != 1 {
			t.Fatalf("ReleaseStages() = %+v, error = %v", stages, err)
		}
	})
}

func TestReleaseRepositoryPersistsTerminalEvidenceAcrossReopen(t *testing.T) {
	path := filepath.Join(secureTempDir(t), "release-reopen.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if database != nil {
			_ = database.Close()
		}
	})
	if _, err := database.CreateInitialUser(context.Background(), auth.NewUser{
		Username: "operator", NormalizedName: "operator", PasswordHash: "hash", CreatedAt: testTime(0),
	}); err != nil {
		t.Fatal(err)
	}
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create-release-reopen")
	check := testPublishCheck(workspace)
	if err := database.CreatePublishCheck(context.Background(), check); err != nil {
		t.Fatal(err)
	}
	release := testRelease(workspace, check)
	release.BackupID = config.BackupID("00000000000000000000000000000004")
	initial := config.ReleaseStage{
		ReleaseID: release.ID, Sequence: 1, Stage: config.ReleaseStageQueued,
		Result: config.StageResultPending, PublicDetailsJSON: `{}`, OccurredAt: release.CreatedAt,
	}
	if err := database.CreateRelease(context.Background(), release, initial); err != nil {
		t.Fatal(err)
	}
	release.State = config.ReleaseStateSucceeded
	release.Stage = config.ReleaseStageCommitted
	release.UpdatedAt = testTime(6)
	release.FinishedAt = testTime(6)
	if err := database.TransitionRelease(
		context.Background(), config.ReleaseStateQueued, config.ReleaseStageQueued, release,
		config.ReleaseStage{
			ReleaseID: release.ID, Sequence: 2, Stage: config.ReleaseStageCommitted,
			Result: config.StageResultSuccess, PublicDetailsJSON: `{}`, OccurredAt: release.UpdatedAt,
		},
	); err != nil {
		t.Fatal(err)
	}
	backup := config.Backup{
		ID: release.BackupID, OriginType: config.BackupOriginRelease, OriginID: string(release.ID),
		ReleaseID: release.ID, ProductionDigest: release.ProductionDigest, TreeDigest: testDigest(0x55),
		State: config.BackupStateComplete, EntryCount: 3, TotalBytes: 512, BodyPresent: true,
		CreatedAt: testTime(4), VerifiedAt: testTime(5),
	}
	if err := database.PutBackup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = nil

	database, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	storedCheck, checkErr := database.PublishCheck(context.Background(), check.ID)
	storedRelease, releaseErr := database.Release(context.Background(), release.ID)
	storedStages, stagesErr := database.ReleaseStages(context.Background(), release.ID, 0, 10)
	storedBackup, backupErr := database.Backup(context.Background(), backup.ID)
	if checkErr != nil || releaseErr != nil || stagesErr != nil || backupErr != nil ||
		storedCheck.State != config.PublishCheckStateValid || storedRelease.State != config.ReleaseStateSucceeded ||
		len(storedStages) != 2 || storedBackup.State != config.BackupStateComplete {
		t.Fatalf("reopened evidence = check:%+v/%v release:%+v/%v stages:%+v/%v backup:%+v/%v",
			storedCheck, checkErr, storedRelease, releaseErr, storedStages, stagesErr, storedBackup, backupErr)
	}
}

func testPublishCheck(workspace config.Workspace) config.PublishCheck {
	return config.PublishCheck{
		ID:          config.PublishCheckID("00000000000000000000000000000002"),
		WorkspaceID: workspace.ID, WorkspaceRevision: workspace.Revision,
		ProductionDigest: workspace.ProductionDigest, BaseDigest: workspace.BaseDigest,
		DraftDigest: workspace.DraftDigest, CandidateDigest: testDigest(0x44),
		ManifestVersion: 1, PolicyVersion: 1, ValidatorVersion: 1,
		ValidatorBuildID: "build-v1", State: config.PublishCheckStateValid,
		DiagnosticCount: 0, PublicDetailsJSON: `[]`, CreatedBy: 1,
		RequestID: "publish-check-request", StartedAt: testTime(3), FinishedAt: testTime(3),
		ExpiresAt: testTime(4),
	}
}

func testRelease(workspace config.Workspace, check config.PublishCheck) config.Release {
	return config.Release{
		ID: config.ReleaseID("00000000000000000000000000000003"), WorkspaceID: workspace.ID,
		CheckID: check.ID, State: config.ReleaseStateQueued, Stage: config.ReleaseStageQueued,
		ProductionDigest: check.ProductionDigest, DraftDigest: check.DraftDigest,
		CandidateDigest: check.CandidateDigest, CreatedBy: 1, RequestID: "release-request",
		CreatedAt: testTime(4), UpdatedAt: testTime(4),
	}
}
