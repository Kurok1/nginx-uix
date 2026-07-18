/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestWorkspaceUsageListOrderAndLookup(t *testing.T) {
	database := openRepositoryDatabase(t)
	alpha := testWorkspace(1, "Alpha", testTime(3))
	beta := testWorkspace(2, "Beta", testTime(3))
	newest := testWorkspace(3, "Newest", testTime(4))
	createWorkspaceRecord(t, database, beta, "config.workspace.create-beta")
	createWorkspaceRecord(t, database, newest, "config.workspace.create-newest")
	createWorkspaceRecord(t, database, alpha, "config.workspace.create-alpha")

	count, bytes, err := database.WorkspaceUsage(context.Background())
	if err != nil {
		t.Fatalf("WorkspaceUsage() error = %v", err)
	}
	if count != 3 || bytes != alpha.WorkspaceBytes+beta.WorkspaceBytes+newest.WorkspaceBytes {
		t.Fatalf("WorkspaceUsage() = %d/%d, want 3/%d", count, bytes, alpha.WorkspaceBytes+beta.WorkspaceBytes+newest.WorkspaceBytes)
	}

	workspaces, err := database.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaces() error = %v", err)
	}
	if len(workspaces) != 3 {
		t.Fatalf("ListWorkspaces() length = %d, want 3", len(workspaces))
	}
	if workspaces[0].ID != newest.ID || workspaces[1].ID != alpha.ID || workspaces[2].ID != beta.ID {
		t.Fatalf("workspace order = %v, want newest then ID ascending", []config.WorkspaceID{workspaces[0].ID, workspaces[1].ID, workspaces[2].ID})
	}

	got, err := database.Workspace(context.Background(), beta.ID)
	if err != nil {
		t.Fatalf("Workspace() error = %v", err)
	}
	beta.CreatedAt = beta.CreatedAt.UTC()
	beta.UpdatedAt = beta.UpdatedAt.UTC()
	if got != beta {
		t.Fatalf("Workspace() = %#v, want %#v", got, beta)
	}
}

func TestWorkspaceNotFoundIsDistinctFromRevisionConflict(t *testing.T) {
	database := openRepositoryDatabase(t)
	missing := testWorkspace(99, "Missing", testTime(2))

	_, err := database.Workspace(context.Background(), missing.ID)
	assertNotFound(t, err)

	next := missing
	next.Revision = 2
	operation := testOperation("config.workspace.update-missing", "workspace", string(missing.ID), testTime(3))
	err = database.UpdateWorkspace(context.Background(), config.WorkspaceChange{
		ExpectedRevision: 1,
		Next:             next,
		Operation:        operation,
		Audit:            testAudit(operation, `{}`),
	})
	assertNotFound(t, err)

	operation = testOperation("config.workspace.delete-missing", "workspace", string(missing.ID), testTime(3))
	err = database.DeleteWorkspace(context.Background(), config.WorkspaceDeletion{
		ID:               missing.ID,
		ExpectedRevision: 1,
		Operation:        operation,
		Audit:            testAudit(operation, `{}`),
	})
	assertNotFound(t, err)
}

func TestWorkspaceUpdateAndDeleteUseExactRevisionCAS(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create")

	next := workspace
	next.Name = "Primary stale"
	next.State = config.StateStale
	next.StateReasonCode = "production_changed"
	next.Revision = 2
	next.UpdatedAt = testTime(3)
	operation := testOperation("config.workspace.stale", "workspace", string(workspace.ID), testTime(3))
	before := workspace.DraftDigest
	after := next.DraftDigest
	operation.BeforeDigest = &before
	operation.AfterDigest = &after
	if err := database.UpdateWorkspace(context.Background(), config.WorkspaceChange{
		ExpectedRevision: 1,
		Next:             next,
		Operation:        operation,
		Audit:            testAudit(operation, `{"reason_code":"production_changed"}`),
	}); err != nil {
		t.Fatalf("UpdateWorkspace() error = %v", err)
	}

	staleAttempt := next
	staleAttempt.State = config.StateNeedsAttention
	staleAttempt.Revision = 2
	operation = testOperation("config.workspace.stale-cas", "workspace", string(workspace.ID), testTime(4))
	err := database.UpdateWorkspace(context.Background(), config.WorkspaceChange{
		ExpectedRevision: 1,
		Next:             staleAttempt,
		Operation:        operation,
		Audit:            testAudit(operation, `{}`),
	})
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("stale UpdateWorkspace() error = %v, want ErrConflict", err)
	}

	operation = testOperation("config.workspace.delete-stale-cas", "workspace", string(workspace.ID), testTime(4))
	err = database.DeleteWorkspace(context.Background(), config.WorkspaceDeletion{
		ID:               workspace.ID,
		ExpectedRevision: 1,
		Operation:        operation,
		Audit:            testAudit(operation, `{}`),
	})
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("stale DeleteWorkspace() error = %v, want ErrConflict", err)
	}

	got, err := database.Workspace(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("Workspace() after conflicts error = %v", err)
	}
	if got.State != config.StateStale || got.Revision != 2 {
		t.Fatalf("workspace changed after stale CAS: %#v", got)
	}

	operation = testOperation("config.workspace.delete", "workspace", string(workspace.ID), testTime(5))
	if err := database.DeleteWorkspace(context.Background(), config.WorkspaceDeletion{
		ID:               workspace.ID,
		ExpectedRevision: 2,
		Operation:        operation,
		Audit:            testAudit(operation, `{}`),
	}); err != nil {
		t.Fatalf("DeleteWorkspace() error = %v", err)
	}
	_, err = database.Workspace(context.Background(), workspace.ID)
	assertNotFound(t, err)
	assertMutationCounts(t, database, 0, 3, 3)
}

func TestWorkspaceRejectsNonMonotonicRevision(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create")

	next := workspace
	next.Revision = 3
	operation := testOperation("config.workspace.skip-revision", "workspace", string(workspace.ID), testTime(3))
	err := database.UpdateWorkspace(context.Background(), config.WorkspaceChange{
		ExpectedRevision: 1,
		Next:             next,
		Operation:        operation,
		Audit:            testAudit(operation, `{}`),
	})
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("UpdateWorkspace() error = %v, want ErrConflict", err)
	}
	assertMutationCounts(t, database, 1, 1, 1)
}

func TestWorkspaceConstraintConflictIsMapped(t *testing.T) {
	database := openRepositoryDatabase(t)
	workspace := testWorkspace(1, "Primary", testTime(2))
	createWorkspaceRecord(t, database, workspace, "config.workspace.create")

	operation := testOperation("config.workspace.duplicate", "workspace", string(workspace.ID), testTime(3))
	err := database.CreateWorkspace(context.Background(), config.WorkspaceCreation{
		Workspace: workspace,
		Operation: operation,
		Audit:     testAudit(operation, `{}`),
	})
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("duplicate CreateWorkspace() error = %v, want ErrConflict", err)
	}
	assertMutationCounts(t, database, 1, 1, 1)
}

func TestWorkspaceMutationsAndAuditAreAtomic(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		database := openRepositoryDatabase(t)
		breakAuditInsert(t, database)
		workspace := testWorkspace(1, "Primary", testTime(2))
		operation := testOperation("config.workspace.create", "workspace", string(workspace.ID), testTime(3))
		err := database.CreateWorkspace(context.Background(), config.WorkspaceCreation{
			Workspace: workspace,
			Operation: operation,
			Audit:     testAudit(operation, `{}`),
		})
		if err == nil {
			t.Fatal("CreateWorkspace() error = nil")
		}
		assertMutationCounts(t, database, 0, 0, 0)
	})

	t.Run("update", func(t *testing.T) {
		database := openRepositoryDatabase(t)
		workspace := testWorkspace(1, "Primary", testTime(2))
		createWorkspaceRecord(t, database, workspace, "config.workspace.create")
		breakAuditInsert(t, database)
		next := workspace
		next.State = config.StateStale
		next.Revision = 2
		operation := testOperation("config.workspace.stale", "workspace", string(workspace.ID), testTime(3))
		err := database.UpdateWorkspace(context.Background(), config.WorkspaceChange{
			ExpectedRevision: workspace.Revision,
			Next:             next,
			Operation:        operation,
			Audit:            testAudit(operation, `{}`),
		})
		if err == nil {
			t.Fatal("UpdateWorkspace() error = nil")
		}
		got, readErr := database.Workspace(context.Background(), workspace.ID)
		if readErr != nil {
			t.Fatalf("Workspace() error = %v", readErr)
		}
		if got.State != config.StateReady || got.Revision != workspace.Revision {
			t.Fatalf("workspace changed after audit failure: %#v", got)
		}
		assertMutationCounts(t, database, 1, 1, 1)
	})

	t.Run("delete", func(t *testing.T) {
		database := openRepositoryDatabase(t)
		workspace := testWorkspace(1, "Primary", testTime(2))
		createWorkspaceRecord(t, database, workspace, "config.workspace.create")
		breakAuditInsert(t, database)
		operation := testOperation("config.workspace.delete", "workspace", string(workspace.ID), testTime(3))
		err := database.DeleteWorkspace(context.Background(), config.WorkspaceDeletion{
			ID:               workspace.ID,
			ExpectedRevision: workspace.Revision,
			Operation:        operation,
			Audit:            testAudit(operation, `{}`),
		})
		if err == nil {
			t.Fatal("DeleteWorkspace() error = nil")
		}
		if _, readErr := database.Workspace(context.Background(), workspace.ID); readErr != nil {
			t.Fatalf("Workspace() after audit failure error = %v", readErr)
		}
		assertMutationCounts(t, database, 1, 1, 1)
	})
}
