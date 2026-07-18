/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
	"github.com/kuroky/nginx-uix/internal/config"
)

func TestWorkspaceMutationPersistsLinkedOperationAndAudit(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	afterDigest := workspace.DraftDigest
	operation := testOperation("config.workspace.create", "workspace", string(workspace.ID), testTime(3))
	operation.AfterDigest = &afterDigest
	audit := testAudit(operation, `{"workspace_count":1}`)

	if err := database.CreateWorkspace(context.Background(), config.WorkspaceCreation{
		Workspace: workspace,
		Operation: operation,
		Audit:     audit,
	}); err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}

	var objectType, objectID, action, requestID, occurredAt string
	var beforeDigest, gotAfterDigest []byte
	if err := database.sql.QueryRowContext(
		context.Background(),
		`SELECT object_type, object_id, action, before_digest, after_digest, request_id, occurred_at
		 FROM config_operations WHERE id = ?`,
		operation.ID,
	).Scan(&objectType, &objectID, &action, &beforeDigest, &gotAfterDigest, &requestID, &occurredAt); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if objectType != operation.ObjectType || objectID != operation.ObjectID || action != operation.Action ||
		beforeDigest != nil || string(gotAfterDigest) != string(afterDigest[:]) || requestID != operation.RequestID ||
		occurredAt != formatTime(operation.OccurredAt) {
		t.Fatalf("operation metadata was not preserved")
	}

	var operationID, detailsJSON, auditOccurredAt string
	var actorUserID int64
	if err := database.sql.QueryRowContext(
		context.Background(),
		`SELECT operation_id, actor_user_id, details_json, occurred_at
		 FROM audit_events WHERE operation_id = ?`,
		operation.ID,
	).Scan(&operationID, &actorUserID, &detailsJSON, &auditOccurredAt); err != nil {
		t.Fatalf("read linked audit: %v", err)
	}
	if operationID != operation.ID || actorUserID != audit.ActorUserID || detailsJSON != audit.DetailsJSON ||
		auditOccurredAt != formatTime(audit.OccurredAt) {
		t.Fatalf("linked audit metadata was not preserved")
	}
}

func TestWorkspaceMutationRejectsUnsafeAuditDetailsWithoutWritingRows(t *testing.T) {
	tests := []struct {
		name    string
		details string
	}{
		{name: "empty", details: ""},
		{name: "malformed", details: "{"},
		{name: "array", details: "[]"},
		{name: "null", details: "null"},
		{name: "scalar", details: `"metadata"`},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openRepositoryDatabase(t)
			workspace := testWorkspace(index+1, "Rejected", testTime(2))
			operation := testOperation("config.workspace.invalid-"+test.name, "workspace", string(workspace.ID), testTime(3))
			audit := testAudit(operation, test.details)

			err := database.CreateWorkspace(context.Background(), config.WorkspaceCreation{
				Workspace: workspace,
				Operation: operation,
				Audit:     audit,
			})
			if err == nil {
				t.Fatal("CreateWorkspace() error = nil")
			}
			assertMutationCounts(t, database, 0, 0, 0)
		})
	}
}

func TestWorkspaceMutationRejectsMismatchedAuditOperation(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Rejected", testTime(2))
	operation := testOperation("config.workspace.create", "workspace", string(workspace.ID), testTime(3))
	audit := testAudit(operation, `{}`)
	audit.OperationID = "different-operation"

	err := database.CreateWorkspace(context.Background(), config.WorkspaceCreation{
		Workspace: workspace,
		Operation: operation,
		Audit:     audit,
	})
	if err == nil {
		t.Fatal("CreateWorkspace() error = nil")
	}
	assertMutationCounts(t, database, 0, 0, 0)
}

func TestAuditOperationReplayIsIdempotentAndMetadataSafe(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create")

	next := workspace
	next.DraftDigest = testDigest(0x44)
	next.Revision++
	next.UpdatedAt = testTime(3)
	before := workspace.DraftDigest
	after := next.DraftDigest
	operation := testOperation("config.file.replace", "workspace", string(workspace.ID), next.UpdatedAt)
	operation.BeforeDigest = &before
	operation.AfterDigest = &after
	audit := testAudit(operation, `{"after_digest":"safe","path_count":1,"paths":["conf.d/site.conf"]}`)
	change := config.WorkspaceChange{
		ExpectedRevision: workspace.Revision,
		Next:             next,
		Operation:        operation,
		Audit:            audit,
	}

	if err := database.UpdateWorkspace(context.Background(), change); err != nil {
		t.Fatalf("first UpdateWorkspace() error = %v", err)
	}
	if err := database.UpdateWorkspace(context.Background(), change); err != nil {
		t.Fatalf("replayed UpdateWorkspace() error = %v", err)
	}
	assertMutationCounts(t, database, 1, 2, 2)

	changed := change
	changed.Audit.DetailsJSON = `{"after_digest":"changed","path_count":1,"paths":["conf.d/site.conf"]}`
	if err := database.UpdateWorkspace(context.Background(), changed); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("changed replay UpdateWorkspace() error = %v, want ErrConflict", err)
	}
	assertMutationCounts(t, database, 1, 2, 2)
}

func TestAuditOperationCreateReplayIsIdempotent(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	operation := testOperation("config.workspace.create", "workspace", string(workspace.ID), testTime(3))
	after := workspace.DraftDigest
	operation.AfterDigest = &after
	creation := config.WorkspaceCreation{
		Workspace: workspace,
		Operation: operation,
		Audit:     testAudit(operation, `{"name":"Primary"}`),
	}

	if err := database.CreateWorkspace(context.Background(), creation); err != nil {
		t.Fatalf("first CreateWorkspace() error = %v", err)
	}
	if err := database.CreateWorkspace(context.Background(), creation); err != nil {
		t.Fatalf("replayed CreateWorkspace() error = %v", err)
	}
	assertMutationCounts(t, database, 1, 1, 1)
}

func TestAuditOperationDeleteReplayIsIdempotent(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create")
	operation := testOperation("config.workspace.delete", "workspace", string(workspace.ID), testTime(3))
	before := workspace.DraftDigest
	operation.BeforeDigest = &before
	deletion := config.WorkspaceDeletion{
		ID:               workspace.ID,
		ExpectedRevision: workspace.Revision,
		Operation:        operation,
		Audit:            testAudit(operation, `{"name":"Primary"}`),
	}

	if err := database.DeleteWorkspace(context.Background(), deletion); err != nil {
		t.Fatalf("first DeleteWorkspace() error = %v", err)
	}
	gotOperation, gotAudit, found, err := database.OperationAudit(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("OperationAudit() error = %v", err)
	}
	if !found || !sameOperation(gotOperation, operation, formatTime(operation.OccurredAt)) || !sameAudit(gotAudit, deletion.Audit) {
		t.Fatalf("OperationAudit() = %#v, %#v, %t; want %#v, %#v", gotOperation, gotAudit, found, operation, deletion.Audit)
	}
	if err := database.DeleteWorkspace(context.Background(), deletion); err != nil {
		t.Fatalf("replayed DeleteWorkspace() error = %v", err)
	}
	assertMutationCounts(t, database, 0, 2, 2)
}

func TestAuditOperationLegacyEmptyIDBindsNullOutsideUniqueIndex(t *testing.T) {
	database := openRepositoryDatabase(t)
	for index := range 2 {
		audit := config.AuditEvent{
			OccurredAt:  testTime(index + 1),
			ActorUserID: 1,
			Action:      "legacy.config.read",
			ObjectType:  "legacy",
			ObjectID:    fmt.Sprintf("legacy-%d", index),
			Result:      "success",
			RequestID:   fmt.Sprintf("legacy-request-%d", index),
			DetailsJSON: `{}`,
		}
		if err := database.withImmediateTransaction(context.Background(), func(connection *sql.Conn) error {
			return insertAudit(context.Background(), connection, audit)
		}); err != nil {
			t.Fatalf("insertAudit(legacy %d) error = %v", index, err)
		}
	}

	var nullCount, emptyCount int
	if err := database.sql.QueryRowContext(
		context.Background(), "SELECT COUNT(*) FROM audit_events WHERE operation_id IS NULL",
	).Scan(&nullCount); err != nil {
		t.Fatal(err)
	}
	if err := database.sql.QueryRowContext(
		context.Background(), "SELECT COUNT(*) FROM audit_events WHERE operation_id = ''",
	).Scan(&emptyCount); err != nil {
		t.Fatal(err)
	}
	if nullCount != 2 || emptyCount != 0 {
		t.Fatalf("legacy operation ids: NULL = %d, empty = %d", nullCount, emptyCount)
	}
}

func openRepositoryDatabase(t *testing.T) *DB {
	t.Helper()

	database := openTestDatabase(t)
	if _, err := database.CreateInitialUser(context.Background(), auth.NewUser{
		Username:       "operator",
		NormalizedName: "operator",
		PasswordHash:   "argon2id-hash",
		CreatedAt:      testTime(0),
	}); err != nil {
		t.Fatalf("CreateInitialUser() error = %v", err)
	}
	return database
}

func testWorkspace(number int, name string, updatedAt time.Time) config.Workspace {
	return config.Workspace{
		ID:               config.WorkspaceID(fmt.Sprintf("%032x", number)),
		Name:             name,
		State:            config.StateReady,
		ProductionDigest: testDigest(0x11),
		BaseDigest:       testDigest(0x22),
		DraftDigest:      testDigest(byte(0x30 + number)),
		EntryCount:       number,
		ManagedBytes:     int64(number * 100),
		WorkspaceBytes:   int64(number * 1000),
		Revision:         1,
		CreatedBy:        1,
		CreatedAt:        testTime(1),
		UpdatedAt:        updatedAt,
	}
}

func testDigest(value byte) config.Digest {
	var digest config.Digest
	for index := range digest {
		digest[index] = value
	}
	return digest
}

func testOperation(action, objectType, objectID string, occurredAt time.Time) config.OperationRecord {
	return config.OperationRecord{
		ID:         action + "-operation",
		ObjectType: objectType,
		ObjectID:   objectID,
		Action:     action,
		Result:     "success",
		RequestID:  action + "-request",
		OccurredAt: occurredAt,
	}
}

func testAudit(operation config.OperationRecord, details string) config.AuditEvent {
	return config.AuditEvent{
		OperationID: operation.ID,
		OccurredAt:  operation.OccurredAt,
		ActorUserID: 1,
		Action:      operation.Action,
		ObjectType:  operation.ObjectType,
		ObjectID:    operation.ObjectID,
		Result:      operation.Result,
		RequestID:   operation.RequestID,
		DetailsJSON: details,
	}
}

func testTime(hour int) time.Time {
	return time.Date(2026, time.July, 16, hour, 2, 3, 456789000, time.FixedZone("test", 8*60*60))
}

func createWorkspaceRecord(t *testing.T, database *DB, workspace config.Workspace, action string) {
	t.Helper()

	operation := testOperation(action, "workspace", string(workspace.ID), workspace.UpdatedAt)
	audit := testAudit(operation, `{"workspace_id":"safe-opaque-id"}`)
	if err := database.CreateWorkspace(context.Background(), config.WorkspaceCreation{
		Workspace: workspace,
		Operation: operation,
		Audit:     audit,
	}); err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
}

func breakAuditInsert(t *testing.T, database *DB) {
	t.Helper()

	if _, err := database.sql.ExecContext(context.Background(), `
		CREATE TRIGGER reject_config_audit
		BEFORE INSERT ON audit_events
		WHEN NEW.operation_id IS NOT NULL
		BEGIN
			SELECT RAISE(FAIL, 'injected audit failure');
		END
	`); err != nil {
		t.Fatalf("create rejecting audit trigger: %v", err)
	}
}

func assertMutationCounts(t *testing.T, database *DB, workspaces, operations, audits int) {
	t.Helper()

	for query, want := range map[string]int{
		"SELECT COUNT(*) FROM config_workspaces":                           workspaces,
		"SELECT COUNT(*) FROM config_operations":                           operations,
		"SELECT COUNT(*) FROM audit_events WHERE operation_id IS NOT NULL": audits,
	} {
		var got int
		if err := database.sql.QueryRowContext(context.Background(), query).Scan(&got); err != nil {
			t.Fatalf("count mutation rows: %v", err)
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", query, got, want)
		}
	}
}

func assertNotFound(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("error = %v, want sql.ErrNoRows", err)
	}
	if errors.Is(err, config.ErrConflict) {
		t.Fatalf("not-found error also matches ErrConflict: %v", err)
	}
}
