/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestRecoveryRepositoryProductionLeaseUsesExactOwnerCAS(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	releaseID := "11111111111111111111111111111111"
	restoreID := "22222222222222222222222222222222"

	lease, err := database.ProductionLease(ctx)
	if err != nil || lease != (config.ProductionLease{}) {
		t.Fatalf("ProductionLease() = %#v, error = %v, want empty", lease, err)
	}
	acquiredAt := testTime(1)
	if err := database.AcquireProductionLease(ctx, config.ProductionOperationRelease, releaseID, acquiredAt); err != nil {
		t.Fatalf("AcquireProductionLease() error = %v", err)
	}
	lease, err = database.ProductionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	acquiredAt = acquiredAt.UTC()
	if lease.OwnerType != config.ProductionOperationRelease || lease.OwnerID != releaseID || !lease.AcquiredAt.Equal(acquiredAt) {
		t.Fatalf("ProductionLease() = %#v", lease)
	}
	if err := database.AcquireProductionLease(ctx, config.ProductionOperationRestore, restoreID, testTime(2)); !errors.Is(err, config.ErrOperationInProgress) {
		t.Fatalf("second AcquireProductionLease() error = %v, want ErrOperationInProgress", err)
	}
	if err := database.ReleaseProductionLease(ctx, config.ProductionOperationRestore, restoreID); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("wrong ReleaseProductionLease() error = %v, want ErrConflict", err)
	}
	if err := database.ReleaseProductionLease(ctx, config.ProductionOperationRelease, releaseID); err != nil {
		t.Fatalf("ReleaseProductionLease() error = %v", err)
	}
	lease, err = database.ProductionLease(ctx)
	if err != nil || lease != (config.ProductionLease{}) {
		t.Fatalf("released ProductionLease() = %#v, error = %v", lease, err)
	}
}

func TestRecoveryRepositoryListsBackupsAndPersistsManualProtection(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	backups := []config.Backup{
		testRecoveryBackup(1, testTime(1), config.BackupStateComplete),
		testRecoveryBackup(2, testTime(2), config.BackupStateComplete),
		testRecoveryBackup(3, testTime(3), config.BackupStateDeleted),
	}
	for _, backup := range backups {
		insertRecoveryBackup(t, database, backup)
	}

	page, err := database.ListBackups(ctx, config.BackupQuery{Limit: 2, IncludeDeleted: true})
	if err != nil {
		t.Fatalf("ListBackups() error = %v", err)
	}
	if len(page) != 2 || page[0].ID != backups[2].ID || page[1].ID != backups[1].ID {
		t.Fatalf("ListBackups() = %#v, want newest two", page)
	}
	next, err := database.ListBackups(ctx, config.BackupQuery{
		BeforeCreatedAt: page[1].CreatedAt, BeforeID: page[1].ID, Limit: 2, IncludeDeleted: true,
	})
	if err != nil || len(next) != 1 || next[0].ID != backups[0].ID {
		t.Fatalf("next ListBackups() = %#v, error = %v", next, err)
	}
	complete, err := database.RetentionBackups(ctx)
	if err != nil || len(complete) != 2 || complete[0].ID != backups[0].ID || complete[1].ID != backups[1].ID {
		t.Fatalf("RetentionBackups() = %#v, error = %v", complete, err)
	}

	operation := testOperation("config.backup.protect", "config_backup", string(backups[1].ID), testTime(4))
	change := config.BackupProtectionChange{
		BackupID: backups[1].ID, ExpectedProtected: false, NextProtected: true,
		Reason: "incident recovery point", Actor: config.Actor{UserID: 1, RequestID: operation.RequestID},
		Operation: operation, Audit: testAudit(operation, `{"protection":"manual"}`),
	}
	protected, err := database.ChangeBackupProtection(ctx, change)
	if err != nil {
		t.Fatalf("ChangeBackupProtection() error = %v", err)
	}
	if !protected.ManuallyProtected || protected.ProtectionReason != change.Reason ||
		protected.ProtectedBy != 1 || !protected.ProtectedAt.Equal(testTime(4).UTC()) {
		t.Fatalf("protected backup = %#v", protected)
	}
	if _, err := database.ChangeBackupProtection(ctx, change); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("replayed stale ChangeBackupProtection() error = %v, want ErrConflict", err)
	}
	var auditCount int
	if err := database.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events
		WHERE object_type = 'config_backup' AND object_id = ?`, backups[1].ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("backup protection audit count = %d, want 1", auditCount)
	}
}

func TestRecoveryRepositoryPersistsRetentionPlanAndTransitions(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	backups := []config.Backup{
		testRecoveryBackup(1, testTime(1), config.BackupStateComplete),
		testRecoveryBackup(2, testTime(2), config.BackupStateComplete),
		testRecoveryBackup(3, testTime(3), config.BackupStateComplete),
		testRecoveryBackup(4, testTime(4), config.BackupStateComplete),
	}
	for _, backup := range backups {
		insertRecoveryBackup(t, database, backup)
	}
	backup := backups[0]
	run := config.RetentionRun{
		ID: config.RetentionRunID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), State: config.RetentionRunPlanned,
		Policy:      config.RetentionPolicy{MinimumComplete: 3, MaximumComplete: 20, MaximumTotalBytes: 4 << 30, MinimumAge: 24 * time.Hour},
		BackupCount: 1, TotalBytes: backup.TotalBytes, ProtectedCount: 0,
		DeleteCount: 1, DeleteBytes: backup.TotalBytes, CreatedBy: 1, RequestID: "retention-request",
		CreatedAt: testTime(5), ExpiresAt: testTime(6),
	}
	items := []config.RetentionItem{{
		RunID: run.ID, Ordinal: 0, BackupID: backup.ID, Decision: config.RetentionDecisionDelete,
		ReasonCode: "maximum_total_bytes", State: config.RetentionItemPlanned,
		SnapshotCreatedAt: backup.CreatedAt, SnapshotTotalBytes: backup.TotalBytes, UpdatedAt: run.CreatedAt,
	}}
	if err := database.CreateRetentionRun(ctx, run, items); err != nil {
		t.Fatalf("CreateRetentionRun() error = %v", err)
	}
	storedRun, storedItems, err := database.RetentionRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("RetentionRun() error = %v", err)
	}
	normalizeRetentionTimes(&run, items)
	if !reflect.DeepEqual(storedRun, run) || !reflect.DeepEqual(storedItems, items) {
		t.Fatalf("RetentionRun() = %#v/%#v, want %#v/%#v", storedRun, storedItems, run, items)
	}
	next := storedRun
	next.State = config.RetentionRunExecuting
	next.ExecutionRequestID = "retention-execution"
	next.StartedAt = testTime(7)
	if err := database.TransitionRetentionRun(ctx, config.RetentionRunPlanned, next); err != nil {
		t.Fatalf("TransitionRetentionRun() error = %v", err)
	}
	if err := database.BeginRetentionDeletion(
		ctx, run.ID, 0, backup.ID, backup.CreatedAt, backup.TotalBytes, testTime(8),
	); err != nil {
		t.Fatalf("BeginRetentionDeletion() error = %v", err)
	}
	deleting, err := database.Backup(ctx, backup.ID)
	if err != nil || deleting.State != config.BackupStateDeleting || deleting.DeleteRunID != string(run.ID) ||
		deleting.DeleteReason != "retention" || !deleting.BodyPresent {
		t.Fatalf("deleting backup = %#v, error = %v", deleting, err)
	}
	if err := database.CompleteRetentionDeletion(ctx, run.ID, 0, backup.ID, testTime(9)); err != nil {
		t.Fatalf("CompleteRetentionDeletion() error = %v", err)
	}
	deleted, err := database.Backup(ctx, backup.ID)
	if err != nil || deleted.State != config.BackupStateDeleted || deleted.BodyPresent ||
		!deleted.DeletedAt.Equal(testTime(9).UTC()) {
		t.Fatalf("deleted backup = %#v, error = %v", deleted, err)
	}
	storedRun, storedItems, err = database.RetentionRun(ctx, run.ID)
	if err != nil || storedRun.DeletedCount != 1 || storedRun.DeletedBytes != backup.TotalBytes ||
		storedItems[0].State != config.RetentionItemDeleted {
		t.Fatalf("executed retention = %#v/%#v, error = %v", storedRun, storedItems, err)
	}
	finished := storedRun
	finished.State = config.RetentionRunSucceeded
	finished.FinishedAt = testTime(10)
	if err := database.TransitionRetentionRun(ctx, config.RetentionRunExecuting, finished); err != nil {
		t.Fatalf("finish TransitionRetentionRun() error = %v", err)
	}
	lease, err := database.ProductionLease(ctx)
	if err != nil || lease != (config.ProductionLease{}) {
		t.Fatalf("finished retention lease = %#v, error = %v", lease, err)
	}
}

func TestRecoveryRepositoryRetentionDeletionRechecksProtectionAndSnapshot(t *testing.T) {
	tests := []struct {
		name    string
		protect func(*testing.T, *DB, config.Backup)
		want    error
	}{
		{
			name: "manual protection",
			protect: func(t *testing.T, database *DB, backup config.Backup) {
				t.Helper()
				_, err := database.sql.Exec(`UPDATE config_backups SET manually_protected = 1,
					protection_reason = 'operator hold', protected_by = 1, protected_at = ? WHERE id = ?`,
					formatTime(testTime(8)), backup.ID)
				if err != nil {
					t.Fatal(err)
				}
			},
			want: config.ErrBackupProtected,
		},
		{
			name: "open attention",
			protect: func(t *testing.T, database *DB, backup config.Backup) {
				t.Helper()
				_, err := database.sql.Exec(`INSERT INTO config_attention_cases(
					id, subject_type, subject_id, backup_id, state, reason_code, public_evidence_json, opened_at
				) VALUES (?, 'restart', ?, ?, 'open', 'runtime_unknown', '{}', ?)`,
					"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "dddddddddddddddddddddddddddddddd",
					backup.ID, formatTime(testTime(8)))
				if err != nil {
					t.Fatal(err)
				}
			},
			want: config.ErrBackupProtected,
		},
		{
			name: "snapshot changed",
			protect: func(t *testing.T, database *DB, backup config.Backup) {
				t.Helper()
				_, err := database.sql.Exec(`UPDATE config_backups SET total_bytes = total_bytes + 1 WHERE id = ?`, backup.ID)
				if err != nil {
					t.Fatal(err)
				}
			},
			want: config.ErrRetentionPlanExpired,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, run, backup := createExecutingRetentionFixture(t)
			test.protect(t, database, backup)
			err := database.BeginRetentionDeletion(
				context.Background(), run.ID, 0, backup.ID, backup.CreatedAt, backup.TotalBytes, testTime(9),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("BeginRetentionDeletion() error = %v, want %v", err, test.want)
			}
			stored, readErr := database.Backup(context.Background(), backup.ID)
			if readErr != nil || stored.State != config.BackupStateComplete {
				t.Fatalf("protected backup = %#v, error = %v", stored, readErr)
			}
		})
	}
}

func createExecutingRetentionFixture(t *testing.T) (*DB, config.RetentionRun, config.Backup) {
	t.Helper()
	database := openRepositoryDatabase(t)
	backups := make([]config.Backup, 4)
	for index := range backups {
		backups[index] = testRecoveryBackup(index+1, testTime(index+1), config.BackupStateComplete)
		insertRecoveryBackup(t, database, backups[index])
	}
	backup := backups[0]
	run := config.RetentionRun{
		ID: config.RetentionRunID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), State: config.RetentionRunPlanned,
		Policy:      config.RetentionPolicy{MinimumComplete: 3, MaximumComplete: 20, MaximumTotalBytes: 4 << 30, MinimumAge: 24 * time.Hour},
		BackupCount: 4, TotalBytes: 10 * 1024, DeleteCount: 1, DeleteBytes: backup.TotalBytes,
		CreatedBy: 1, RequestID: "retention-protection", CreatedAt: testTime(5), ExpiresAt: testTime(6),
	}
	items := []config.RetentionItem{{
		RunID: run.ID, Ordinal: 0, BackupID: backup.ID, Decision: config.RetentionDecisionDelete,
		ReasonCode: "maximum_complete", State: config.RetentionItemPlanned,
		SnapshotCreatedAt: backup.CreatedAt, SnapshotTotalBytes: backup.TotalBytes, UpdatedAt: run.CreatedAt,
	}}
	if err := database.CreateRetentionRun(context.Background(), run, items); err != nil {
		t.Fatal(err)
	}
	next := run
	next.State = config.RetentionRunExecuting
	next.ExecutionRequestID = "retention-protection-execution"
	next.StartedAt = testTime(7)
	if err := database.TransitionRetentionRun(context.Background(), config.RetentionRunPlanned, next); err != nil {
		t.Fatal(err)
	}
	return database, next, backup
}

func TestRecoveryRepositoryListsAuditEventsWithStableKeyset(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create-history")
	ctx := context.Background()

	page, err := database.ListAuditEvents(ctx, config.AuditQuery{Limit: 1})
	if err != nil || len(page) != 1 {
		t.Fatalf("ListAuditEvents() = %#v, error = %v", page, err)
	}
	if page[0].ActorName != "operator" || page[0].Action != "config.workspace.create-history" {
		t.Fatalf("audit record = %#v", page[0])
	}
	next, err := database.ListAuditEvents(ctx, config.AuditQuery{
		BeforeOccurredAt: page[0].OccurredAt, BeforeID: page[0].ID, Limit: 10,
	})
	if err != nil || len(next) != 0 {
		t.Fatalf("next ListAuditEvents() = %#v, error = %v", next, err)
	}
}

func TestRecoveryRepositoryListsAttentionCasesWithStableKeysetAndOpenStatus(t *testing.T) {
	database := openRepositoryDatabase(t)
	ctx := context.Background()
	cases := []struct {
		id     string
		state  config.AttentionCaseState
		opened time.Time
	}{
		{"11111111111111111111111111111111", config.AttentionCaseOpen, testTime(1)},
		{"22222222222222222222222222222222", config.AttentionCaseOpen, testTime(2)},
		{"33333333333333333333333333333333", config.AttentionCaseOpen, testTime(2)},
		{"44444444444444444444444444444444", config.AttentionCaseResolved, testTime(3)},
	}
	for _, item := range cases {
		_, err := database.sql.ExecContext(ctx, `INSERT INTO config_attention_cases(
			id, subject_type, subject_id, state, reason_code, public_evidence_json, opened_at,
			resolved_by, resolved_at, resolution_type, resolution_id
		) VALUES (?, 'restart', ?, ?, 'runtime_unknown', '{}', ?,
			CASE WHEN ? = 'resolved' THEN 1 END,
			CASE WHEN ? = 'resolved' THEN ? END,
			CASE WHEN ? = 'resolved' THEN 'verification' END,
			CASE WHEN ? = 'resolved' THEN '55555555555555555555555555555555' END)`,
			item.id, item.id, item.state, formatTime(item.opened), item.state,
			item.state, formatTime(item.opened.Add(time.Minute)), item.state, item.state)
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := database.ListAttentionCases(ctx, config.AttentionQuery{
		State: config.AttentionCaseOpen, Limit: 2,
	})
	if err != nil || len(page) != 2 || string(page[0].ID) != cases[2].id || string(page[1].ID) != cases[1].id {
		t.Fatalf("ListAttentionCases() = %#v, error = %v", page, err)
	}
	next, err := database.ListAttentionCases(ctx, config.AttentionQuery{
		State: config.AttentionCaseOpen, BeforeOpenedAt: page[1].OpenedAt,
		BeforeID: page[1].ID, Limit: 2,
	})
	if err != nil || len(next) != 1 || string(next[0].ID) != cases[0].id {
		t.Fatalf("next ListAttentionCases() = %#v, error = %v", next, err)
	}
	open, err := database.HasOpenAttentionCases(ctx)
	if err != nil || !open {
		t.Fatalf("HasOpenAttentionCases() = %v, error = %v", open, err)
	}
	if _, err := database.sql.ExecContext(ctx, `UPDATE config_attention_cases SET state = 'resolved',
		resolved_by = 1, resolved_at = ?, resolution_type = 'verification',
		resolution_id = '55555555555555555555555555555555' WHERE state = 'open'`,
		formatTime(testTime(4))); err != nil {
		t.Fatal(err)
	}
	open, err = database.HasOpenAttentionCases(ctx)
	if err != nil || open {
		t.Fatalf("resolved HasOpenAttentionCases() = %v, error = %v", open, err)
	}
}

func testRecoveryBackup(number int, createdAt time.Time, state config.BackupState) config.Backup {
	id := config.BackupID(fmt.Sprintf("%032x", number))
	backup := config.Backup{
		ID: id, OriginType: config.BackupOriginRestore, OriginID: string(id),
		ProductionDigest: testDigest(byte(0x50 + number)), TreeDigest: testDigest(byte(0x60 + number)),
		State: state, EntryCount: number, TotalBytes: int64(number * 1024), BodyPresent: state != config.BackupStateDeleted,
		CreatedAt: createdAt, VerifiedAt: createdAt.Add(time.Minute),
	}
	if state == config.BackupStateDeleted {
		backup.DeleteRunID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		backup.DeleteReason = "retention"
		backup.DeletedAt = createdAt.Add(2 * time.Minute)
	}
	return backup
}

func insertRecoveryBackup(t *testing.T, database *DB, backup config.Backup) {
	t.Helper()
	if err := database.PutBackup(context.Background(), backup); err != nil {
		t.Fatalf("PutBackup(%s) error = %v", backup.ID, err)
	}
}

func normalizeRetentionTimes(run *config.RetentionRun, items []config.RetentionItem) {
	run.CreatedAt = run.CreatedAt.UTC()
	run.ExpiresAt = run.ExpiresAt.UTC()
	for index := range items {
		items[index].SnapshotCreatedAt = items[index].SnapshotCreatedAt.UTC()
		items[index].UpdatedAt = items[index].UpdatedAt.UTC()
	}
}
