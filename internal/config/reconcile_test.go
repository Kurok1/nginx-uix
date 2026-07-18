/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileFileMutationCrashMatrix(t *testing.T) {
	operations := []string{"create", "replace", "copy", "rename", "delete"}
	checkpoints := []string{
		"after_recovery",
		"after_phase_prepared",
		"after_filesystem",
		"after_phase_files_applied",
		"after_database",
		"after_phase_sql_committed",
		"after_control_manifest",
		"after_control_state",
		"after_phase_control_committed",
	}
	for _, operation := range operations {
		for _, checkpoint := range checkpoints {
			t.Run(operation+"/"+checkpoint, func(t *testing.T) {
				fixture := newServiceFixture(t)
				workspace := fixture.mustCreate(t)
				productionBefore, err := os.ReadFile(filepath.Join(fixture.production.productionRoot, "conf.d", "site.conf"))
				if err != nil {
					t.Fatal(err)
				}
				fixture.service.mutationHook = func(current string) error {
					if current == checkpoint {
						return errors.New("injected crash")
					}
					return nil
				}
				err = invokeCrashMutation(t, fixture, workspace, operation)
				if err == nil || !strings.Contains(err.Error(), "injected crash") {
					t.Fatalf("mutation error = %v, want injected crash", err)
				}
				fixture.service.mutationHook = nil
				if err := fixture.service.Reconcile(context.Background()); err != nil {
					t.Fatalf("Reconcile() error = %v", err)
				}
				got := fixture.mustWorkspace(t, workspace.ID)
				if got.State != StateReady && got.State != StateNeedsAttention {
					t.Fatalf("workspace state = %s", got.State)
				}
				if got.State == StateReady {
					if _, err := fixture.service.Tree(context.Background(), workspace.ID); err != nil {
						t.Fatalf("Tree(recovered) error = %v", err)
					}
				}
				productionAfter, err := os.ReadFile(filepath.Join(fixture.production.productionRoot, "conf.d", "site.conf"))
				if err != nil {
					t.Fatal(err)
				}
				if string(productionAfter) != string(productionBefore) {
					t.Fatal("recovery changed production")
				}
			})
		}
	}
}

func TestReconcileWorkspaceDeleteJournalCrashMatrixIsIdempotent(t *testing.T) {
	tests := []struct {
		checkpoint string
		committed  bool
	}{
		{checkpoint: "before_phase_prepared"},
		{checkpoint: "after_phase_prepared"},
		{checkpoint: "before_phase_files_applied"},
		{checkpoint: "after_phase_files_applied"},
		{checkpoint: "before_phase_sql_committed", committed: true},
		{checkpoint: "after_phase_sql_committed", committed: true},
		{checkpoint: "before_phase_control_committed", committed: true},
		{checkpoint: "after_phase_control_committed", committed: true},
		{checkpoint: "before_cleanup", committed: true},
		{checkpoint: "after_cleanup", committed: true},
	}
	for _, test := range tests {
		t.Run(test.checkpoint, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			fixture.service.mutationHook = func(checkpoint string) error {
				if checkpoint == test.checkpoint {
					return errors.New("injected crash")
				}
				return nil
			}
			err := fixture.service.Delete(
				context.Background(), Actor{UserID: 7, RequestID: "req-delete"},
				workspace.ID, workspace.ETag(), workspace.Name,
			)
			if err == nil || !strings.Contains(err.Error(), "injected crash") {
				t.Fatalf("Delete() error = %v, want injected crash", err)
			}
			fixture.service.mutationHook = nil
			for pass := 1; pass <= 2; pass++ {
				if err := fixture.service.Reconcile(context.Background()); err != nil {
					t.Fatalf("Reconcile(pass %d) error = %v", pass, err)
				}
			}

			fixture.repository.mu.Lock()
			_, rowExists := fixture.repository.workspaces[workspace.ID]
			deleteAudits := 0
			for _, audit := range fixture.repository.audits {
				if audit.Action == "config.workspace.delete" {
					deleteAudits++
				}
			}
			fixture.repository.mu.Unlock()
			if rowExists == test.committed {
				t.Fatalf("workspace row exists = %t, committed = %t", rowExists, test.committed)
			}
			if want := 0; test.committed {
				want = 1
				if deleteAudits != want {
					t.Fatalf("delete audit count = %d, want %d", deleteAudits, want)
				}
			} else if deleteAudits != want {
				t.Fatalf("delete audit count = %d, want %d", deleteAudits, want)
			}

			entries, err := os.ReadDir(fixture.workspaceRoot)
			if err != nil {
				t.Fatal(err)
			}
			if test.committed {
				if len(entries) != 0 {
					t.Fatalf("committed workspace entries = %v", entries)
				}
				return
			}
			if len(entries) != 1 || entries[0].Name() != string(workspace.ID) {
				t.Fatalf("restored workspace entries = %v", entries)
			}
			root := fixture.openWorkspace(t, workspace.ID)
			defer closeReconcileRoot(t, root)
			if _, err := ReadJournal(context.Background(), root); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("restored journal error = %v, want not exist", err)
			}
		})
	}
}

func TestReconcileFileMutationPhaseWriteBoundaries(t *testing.T) {
	for _, phase := range []JournalPhase{JournalPrepared, JournalFilesApplied, JournalSQLCommitted, JournalControlCommitted} {
		for _, side := range []string{"before", "after"} {
			checkpoint := side + "_phase_" + string(phase)
			t.Run(checkpoint, func(t *testing.T) {
				fixture := newServiceFixture(t)
				workspace := fixture.mustCreate(t)
				fixture.service.mutationHook = func(current string) error {
					if current == checkpoint {
						return errors.New("injected crash")
					}
					return nil
				}
				if err := invokeCrashMutation(t, fixture, workspace, "replace"); err == nil {
					t.Fatal("ReplaceFile() error = nil")
				}
				fixture.service.mutationHook = nil
				if err := fixture.service.Reconcile(context.Background()); err != nil {
					t.Fatalf("Reconcile() error = %v", err)
				}
				got := fixture.mustWorkspace(t, workspace.ID)
				if got.State != StateReady && got.State != StateNeedsAttention {
					t.Fatalf("workspace state = %s", got.State)
				}
			})
		}
	}
}

func TestReconcileRenameIntermediateDigestNeedsAttention(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	fixture.service.mutationHook = func(current string) error {
		if current == "after_filesystem_create_destination" {
			return errors.New("injected crash")
		}
		return nil
	}
	if err := invokeCrashMutation(t, fixture, workspace, "rename"); err == nil {
		t.Fatal("RenameFile() error = nil")
	}
	fixture.service.mutationHook = nil
	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	got := fixture.mustWorkspace(t, workspace.ID)
	if got.State != StateNeedsAttention || got.StateReasonCode != reasonRecoveryStateMismatch {
		t.Fatalf("workspace = %#v", got)
	}
}

func TestReconcileMalformedRecoveryNeedsAttention(t *testing.T) {
	for _, name := range []string{"before.bin", "manifest.bin", "before-manifest.bin"} {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			fixture.service.mutationHook = func(current string) error {
				if current == "after_phase_prepared" {
					return errors.New("injected crash")
				}
				return nil
			}
			if err := invokeCrashMutation(t, fixture, workspace, "replace"); err == nil {
				t.Fatal("ReplaceFile() error = nil")
			}
			fixture.service.mutationHook = nil
			journalRoot := fixture.openWorkspace(t, workspace.ID)
			journal, err := ReadJournal(context.Background(), journalRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := journalRoot.AtomicReplace(context.Background(), recoveryPath(journal, name), []byte("malformed"), 0o600); err != nil {
				t.Fatal(err)
			}
			closeReconcileRoot(t, journalRoot)
			if err := fixture.service.Reconcile(context.Background()); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			got := fixture.mustWorkspace(t, workspace.ID)
			if got.State != StateNeedsAttention || got.StateReasonCode != reasonRecoveryInvalid {
				t.Fatalf("workspace = %#v", got)
			}
		})
	}
}

func TestReconcileThirdDraftDigestOrRevisionNeedsAttention(t *testing.T) {
	t.Run("digest", func(t *testing.T) {
		fixture := newServiceFixture(t)
		workspace := fixture.mustCreate(t)
		fixture.service.mutationHook = func(current string) error {
			if current == "after_filesystem" {
				return errors.New("injected crash")
			}
			return nil
		}
		if err := invokeCrashMutation(t, fixture, workspace, "replace"); err == nil {
			t.Fatal("ReplaceFile() error = nil")
		}
		fixture.service.mutationHook = nil
		writeExistingFixtureFile(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "third digest\n", 0o600)
		if err := fixture.service.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := fixture.mustWorkspace(t, workspace.ID)
		if got.State != StateNeedsAttention || got.StateReasonCode != reasonRecoveryStateMismatch {
			t.Fatalf("workspace = %#v", got)
		}
	})

	t.Run("revision", func(t *testing.T) {
		fixture := newServiceFixture(t)
		workspace := fixture.mustCreate(t)
		fixture.service.mutationHook = func(current string) error {
			if current == "after_phase_prepared" {
				return errors.New("injected crash")
			}
			return nil
		}
		if err := invokeCrashMutation(t, fixture, workspace, "replace"); err == nil {
			t.Fatal("ReplaceFile() error = nil")
		}
		fixture.service.mutationHook = nil
		fixture.repository.forceState(workspace.ID, StateStale, "first")
		fixture.repository.forceState(workspace.ID, StateReady, "")
		if err := fixture.service.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := fixture.mustWorkspace(t, workspace.ID)
		if got.State != StateNeedsAttention || got.StateReasonCode != reasonRecoveryStateMismatch {
			t.Fatalf("workspace = %#v", got)
		}
	})
}

func invokeCrashMutation(t *testing.T, fixture *serviceFixture, workspace Workspace, operation string) error {
	t.Helper()
	actor := Actor{UserID: 7, RequestID: "req-crash-" + operation}
	switch operation {
	case "create":
		_, err := fixture.service.CreateFile(context.Background(), actor, workspace.ID, CreateFileInput{
			Path: mustRelativePath(t, "conf.d/new.conf"), Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
		})
		return err
	case "replace":
		_, err := fixture.service.ReplaceFile(context.Background(), actor, workspace.ID, ReplaceFileInput{
			Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("server { listen 8081; }\n"), IfMatch: workspace.ETag(),
		})
		return err
	case "copy":
		_, err := fixture.service.CopyFile(context.Background(), actor, workspace.ID, CopyFileInput{
			SourcePath: mustRelativePath(t, "conf.d/site.conf"), DestinationPath: mustRelativePath(t, "conf.d/copy.conf"), IfMatch: workspace.ETag(),
		})
		return err
	case "rename":
		_, err := fixture.service.RenameFile(context.Background(), actor, workspace.ID, RenameFileInput{
			SourcePath: mustRelativePath(t, "conf.d/site.conf"), DestinationPath: mustRelativePath(t, "conf.d/renamed.conf"), IfMatch: workspace.ETag(),
		})
		return err
	case "delete":
		hook := fixture.service.mutationHook
		fixture.service.mutationHook = nil
		created, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-delete-setup"}, workspace.ID, CreateFileInput{
			Path: mustRelativePath(t, "conf.d/delete.conf"), Content: []byte("server { listen 8082; }\n"), IfMatch: workspace.ETag(),
		})
		fixture.service.mutationHook = hook
		if err != nil {
			return err
		}
		_, err = fixture.service.DeleteFile(context.Background(), actor, workspace.ID, DeleteFileInput{
			Path: mustRelativePath(t, "conf.d/delete.conf"), ConfirmPath: "conf.d/delete.conf", IfMatch: created.Workspace.ETag(),
		})
		return err
	default:
		t.Fatalf("unknown crash operation %q", operation)
		return nil
	}
}

func TestReconcileFinalizesOnlyProvableCommittedWorkspace(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	root := fixture.openWorkspace(t, workspace.ID)
	manifest, err := ReadControlManifest(context.Background(), root, DefaultLimits())
	if err != nil {
		t.Fatalf("ReadControlManifest() error = %v", err)
	}
	if err := writePreparedControlManifest(context.Background(), root, manifest); err != nil {
		t.Fatalf("writePreparedControlManifest() error = %v", err)
	}
	if err := root.RemoveRegular(context.Background(), controlManifestPath); err != nil {
		t.Fatalf("RemoveRegular(manifest) error = %v", err)
	}
	if err := WriteControlState(context.Background(), root, ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID, State: StatePreparing,
		Revision: workspace.Revision, UpdatedAt: workspace.UpdatedAt,
	}); err != nil {
		t.Fatalf("WriteControlState(preparing) error = %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("Close(workspace) error = %v", err)
	}

	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := fixture.mustWorkspace(t, workspace.ID)
	if got.State != StateReady || got.Revision != workspace.Revision+1 {
		t.Fatalf("workspace = %#v, want reconciled ready", got)
	}
	state := fixture.mustControlState(t, workspace.ID)
	if state.State != StateReady || state.Revision != got.Revision {
		t.Fatalf("control state = %#v, workspace = %#v", state, got)
	}
	if _, err := os.Lstat(fixture.path(workspace.ID, string(controlPreparedManifestPath))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("prepared manifest still exists: %v", err)
	}
	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fixture.repository.reconcileAuditCount(); got != 1 {
		t.Fatalf("reconcile audit count = %d, want 1", got)
	}
	if fixture.production.digestCalls != 1 || fixture.production.snapshotCalls != 1 {
		t.Fatalf("Reconcile called Agent: digest/snapshot = %d/%d", fixture.production.digestCalls, fixture.production.snapshotCalls)
	}
}

func TestReconcileFinalizesManifestWrittenStatePreparing(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	root := fixture.openWorkspace(t, workspace.ID)
	if err := WriteControlState(context.Background(), root, ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID, State: StatePreparing,
		Revision: workspace.Revision, UpdatedAt: workspace.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	closeReconcileRoot(t, root)
	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := fixture.mustWorkspace(t, workspace.ID)
	if got.State != StateReady || got.Revision != workspace.Revision+1 {
		t.Fatalf("workspace = %#v", got)
	}
	if fixture.repository.reconcileAuditCount() != 1 {
		t.Fatalf("reconcile audit count = %d", fixture.repository.reconcileAuditCount())
	}
}

func TestReconcileReadyCompleteIsIdempotentWithoutAudit(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fixture.mustWorkspace(t, workspace.ID); got != workspace {
		t.Fatalf("workspace changed: %#v", got)
	}
	if fixture.repository.reconcileAuditCount() != 0 {
		t.Fatalf("reconcile audit count = %d", fixture.repository.reconcileAuditCount())
	}
}

func TestReconcileReadyMutatedWorkspaceUsesBaseManifest(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	for _, operation := range []string{"replace", "create", "copy", "rename", "delete"} {
		if err := invokeCrashMutation(t, fixture, workspace, operation); err != nil {
			t.Fatalf("%s mutation error = %v", operation, err)
		}
		workspace = fixture.mustWorkspace(t, workspace.ID)
	}

	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := fixture.mustWorkspace(t, workspace.ID); got != workspace {
		t.Fatalf("workspace changed: %#v, want %#v", got, workspace)
	}
	if got := fixture.repository.reconcileAuditCount(); got != 0 {
		t.Fatalf("reconcile audit count = %d, want 0", got)
	}
}

func TestReconcilePreparedManifestFailuresNeedAttention(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(*testing.T, *serviceFixture, Workspace, *ScopedRoot)
		wantReason string
	}{
		{
			name: "missing", wantReason: "PREPARED_MANIFEST_MISSING",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace, root *ScopedRoot) {
				t.Helper()
				removePublicManifest(t, root)
			},
		},
		{
			name: "malformed", wantReason: "PREPARED_MANIFEST_INVALID",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace, root *ScopedRoot) {
				t.Helper()
				removePublicManifest(t, root)
				if err := root.AtomicReplace(context.Background(), controlPreparedManifestPath, []byte("invalid"), 0o600); err != nil {
					t.Fatalf("AtomicReplace(prepared) error = %v", err)
				}
			},
		},
		{
			name: "mismatched", wantReason: "PREPARED_MANIFEST_MISMATCH",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace, root *ScopedRoot) {
				t.Helper()
				removePublicManifest(t, root)
				other := testManifest([]Entry{{
					Path: "nginx.conf", Type: EntryRegular, Class: EntryManagedText,
					Mode: 0o644, Size: 1, ContentDigest: digestOf("x"),
				}})
				if err := writePreparedControlManifest(context.Background(), root, other); err != nil {
					t.Fatalf("writePreparedControlManifest(other) error = %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			root := fixture.openWorkspace(t, workspace.ID)
			test.prepare(t, fixture, workspace, root)
			if err := WriteControlState(context.Background(), root, ControlState{
				SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID, State: StatePreparing,
				Revision: workspace.Revision, UpdatedAt: workspace.UpdatedAt,
			}); err != nil {
				t.Fatalf("WriteControlState(preparing) error = %v", err)
			}
			if err := root.Close(); err != nil {
				t.Fatalf("Close(workspace) error = %v", err)
			}
			if err := fixture.service.Reconcile(context.Background()); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			got := fixture.mustWorkspace(t, workspace.ID)
			if got.State != StateNeedsAttention || got.StateReasonCode != test.wantReason {
				t.Fatalf("workspace = %#v, want needs_attention/%s", got, test.wantReason)
			}
			if err := fixture.service.Reconcile(context.Background()); err != nil {
				t.Fatalf("Reconcile(second) error = %v", err)
			}
			if got := fixture.repository.reconcileAuditCount(); got != 1 {
				t.Fatalf("reconcile audit count = %d, want 1", got)
			}
		})
	}
}

func TestReconcileCrashTableFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(*testing.T, *serviceFixture, Workspace)
		wantAbsent bool
		wantReason string
	}{
		{
			name: "before agent call", wantAbsent: true,
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				fixture.repository.remove(workspace.ID)
				if err := os.RemoveAll(fixture.path(workspace.ID, "base")); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(fixture.path(workspace.ID, "draft")); err != nil {
					t.Fatal(err)
				}
				root := fixture.openWorkspace(t, workspace.ID)
				removePublicManifestIfPresent(t, root)
				if err := WriteControlState(context.Background(), root, ControlState{
					SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID,
					State: StatePreparing, Revision: 1, UpdatedAt: workspace.UpdatedAt,
				}); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			},
		},
		{
			name: "staged base", wantAbsent: true,
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				fixture.repository.remove(workspace.ID)
				if err := os.Rename(
					fixture.path(workspace.ID, "base"), fixture.path(workspace.ID, "base.stage-crash"),
				); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(fixture.path(workspace.ID, "draft")); err != nil {
					t.Fatal(err)
				}
				root := fixture.openWorkspace(t, workspace.ID)
				removePublicManifestIfPresent(t, root)
				if err := WriteControlState(context.Background(), root, ControlState{
					SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID,
					State: StatePreparing, Revision: 1, UpdatedAt: workspace.UpdatedAt,
				}); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			},
		},
		{
			name: "base renamed", wantAbsent: true,
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				fixture.repository.remove(workspace.ID)
				if err := os.RemoveAll(fixture.path(workspace.ID, "draft")); err != nil {
					t.Fatal(err)
				}
				root := fixture.openWorkspace(t, workspace.ID)
				removePublicManifestIfPresent(t, root)
				if err := WriteControlState(context.Background(), root, ControlState{
					SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID,
					State: StatePreparing, Revision: 1, UpdatedAt: workspace.UpdatedAt,
				}); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			},
		},
		{
			name: "draft partial", wantAbsent: true,
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				fixture.repository.remove(workspace.ID)
				if err := os.Remove(fixture.path(workspace.ID, "draft/conf.d/site.conf")); err != nil {
					t.Fatal(err)
				}
				root := fixture.openWorkspace(t, workspace.ID)
				removePublicManifestIfPresent(t, root)
				if err := WriteControlState(context.Background(), root, ControlState{
					SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID,
					State: StatePreparing, Revision: 1, UpdatedAt: workspace.UpdatedAt,
				}); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			},
		},
		{
			name: "prepared before database", wantAbsent: true,
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				fixture.repository.remove(workspace.ID)
				root := fixture.openWorkspace(t, workspace.ID)
				manifest, err := ReadControlManifest(context.Background(), root, DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				if err := writePreparedControlManifest(context.Background(), root, manifest); err != nil {
					t.Fatal(err)
				}
				removePublicManifest(t, root)
				if err := WriteControlState(context.Background(), root, ControlState{
					SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID,
					State: StatePreparing, Revision: 1, UpdatedAt: workspace.UpdatedAt,
				}); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			},
		},
		{
			name: "database row without tree", wantReason: "WORKSPACE_MISSING",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				if err := os.RemoveAll(fixture.path(workspace.ID, "")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "control missing", wantReason: "CONTROL_MISSING",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				root := fixture.openWorkspace(t, workspace.ID)
				if err := root.RemoveRegular(context.Background(), controlStatePath); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			},
		},
		{
			name: "malformed manifest", wantReason: "MANIFEST_INVALID",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				root := fixture.openWorkspace(t, workspace.ID)
				if err := root.AtomicReplace(context.Background(), controlManifestPath, []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			},
		},
		{
			name: "base changed", wantReason: "BASE_DIGEST_MISMATCH",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				path := fixture.path(workspace.ID, "base/nginx.conf")
				writeExistingFixtureFile(t, path, "events { worker_connections 1; }\n", 0o400)
			},
		},
		{
			name: "draft changed", wantReason: "DRAFT_DIGEST_MISMATCH",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				path := fixture.path(workspace.ID, "draft/nginx.conf")
				writeExistingFixtureFile(t, path, "events { worker_connections 1; }\n", 0o600)
			},
		},
		{
			name: "unknown control schema", wantReason: "CONTROL_SCHEMA_UNSUPPORTED",
			prepare: func(t *testing.T, fixture *serviceFixture, workspace Workspace) {
				root := fixture.openWorkspace(t, workspace.ID)
				payload := []byte(`{"schema_version":2,"workspace_id":"` + string(workspace.ID) + `","state":"ready","state_reason_code":"","revision":1,"updated_at":"2026-07-16T02:00:00Z"}`)
				if err := root.AtomicReplace(context.Background(), controlStatePath, payload, 0o600); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			test.prepare(t, fixture, workspace)
			if err := fixture.service.Reconcile(context.Background()); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			if test.wantAbsent {
				if _, err := os.Lstat(fixture.path(workspace.ID, "")); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("workspace still exists: %v", err)
				}
				return
			}
			got := fixture.mustWorkspace(t, workspace.ID)
			if got.State != StateNeedsAttention || got.StateReasonCode != test.wantReason {
				t.Fatalf("workspace = %#v, want needs_attention/%s", got, test.wantReason)
			}
		})
	}
}

func TestReconcileOrphanReadyBecomesDatabaseMissing(t *testing.T) {
	t.Run("orphan ready becomes database missing", func(t *testing.T) {
		fixture := newServiceFixture(t)
		workspace := fixture.mustCreate(t)
		fixture.repository.remove(workspace.ID)
		if err := fixture.service.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		state := fixture.mustControlState(t, workspace.ID)
		if state.State != StateNeedsAttention || state.StateReasonCode != "DATABASE_MISSING" {
			t.Fatalf("control state = %#v", state)
		}
	})
}

func TestReconcileAmbiguousWorkspaceDeleteTombstoneStaysNeedsAttentionIdempotently(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "missing audit"
		if malformed {
			name = "malformed journal"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			fixture.service.mutationHook = func(checkpoint string) error {
				if checkpoint == "after_filesystem" {
					return errors.New("injected crash")
				}
				return nil
			}
			if err := fixture.service.Delete(
				context.Background(), Actor{UserID: 7, RequestID: "req-delete"},
				workspace.ID, workspace.ETag(), workspace.Name,
			); err == nil {
				t.Fatal("Delete() error = nil")
			}
			fixture.service.mutationHook = nil
			fixture.repository.remove(workspace.ID)
			tombstone := findDeleteTombstone(t, fixture, workspace.ID)
			if malformed {
				root, err := OpenScopedRoot(tombstone)
				if err != nil {
					t.Fatal(err)
				}
				if err := root.AtomicReplace(context.Background(), journalPath, []byte("{\"malformed\":true}\n"), controlFileMode); err != nil {
					t.Fatal(err)
				}
				closeReconcileRoot(t, root)
			}

			var first ControlState
			for pass := 1; pass <= 2; pass++ {
				if err := fixture.service.Reconcile(context.Background()); err != nil {
					t.Fatalf("Reconcile(pass %d) error = %v", pass, err)
				}
				root, err := OpenScopedRoot(tombstone)
				if err != nil {
					t.Fatal(err)
				}
				state, err := ReadControlState(context.Background(), root)
				closeReconcileRoot(t, root)
				if err != nil {
					t.Fatalf("ReadControlState(pass %d) error = %v", pass, err)
				}
				if state.State != StateNeedsAttention || state.StateReasonCode != reasonDeleteTombstoneMismatch {
					t.Fatalf("control state = %#v", state)
				}
				if pass == 1 {
					first = state
				} else if state != first {
					t.Fatalf("second reconciliation changed state: first=%#v second=%#v", first, state)
				}
			}
		})
	}
}

func findDeleteTombstone(t *testing.T, fixture *serviceFixture, id WorkspaceID) string {
	t.Helper()
	entries, err := os.ReadDir(fixture.workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	prefix := ".delete-" + string(id) + "-"
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			return filepath.Join(fixture.workspaceRoot, entry.Name())
		}
	}
	t.Fatalf("delete tombstone %s* missing", prefix)
	return ""
}

func TestReconcileNeverFollowsWorkspaceSymlink(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	if err := os.RemoveAll(fixture.path(workspace.ID, "")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(fixture.workspaceRoot), "outside")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel")
	writeFixtureFile(t, sentinel, "keep", 0o600)
	if err := os.Symlink(target, fixture.path(workspace.ID, "")); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(sentinel); err != nil || string(contents) != "keep" {
		t.Fatalf("outside sentinel = %q, %v", contents, err)
	}
	got := fixture.mustWorkspace(t, workspace.ID)
	if got.State != StateNeedsAttention || got.StateReasonCode != "WORKSPACE_INVALID" {
		t.Fatalf("workspace = %#v", got)
	}
}

func (f *serviceFixture) mustCreate(t *testing.T) Workspace {
	t.Helper()
	workspace, err := f.service.Create(
		context.Background(), Actor{UserID: 7, RequestID: "req-create"}, "review",
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return workspace
}

func (f *serviceFixture) openWorkspace(t *testing.T, id WorkspaceID) *ScopedRoot {
	t.Helper()
	root, err := OpenScopedRoot(f.path(id, ""))
	if err != nil {
		t.Fatalf("OpenScopedRoot(workspace) error = %v", err)
	}
	return root
}

func (f *serviceFixture) mustWorkspace(t *testing.T, id WorkspaceID) Workspace {
	t.Helper()
	workspace, err := f.repository.Workspace(context.Background(), id)
	if err != nil {
		t.Fatalf("Workspace() error = %v", err)
	}
	return workspace
}

func (f *serviceFixture) mustControlState(t *testing.T, id WorkspaceID) ControlState {
	t.Helper()
	root := f.openWorkspace(t, id)
	defer closeReconcileRoot(t, root)
	state, err := ReadControlState(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadControlState() error = %v", err)
	}
	return state
}

func (r *memoryWorkspaceRepository) remove(id WorkspaceID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.workspaces, id)
}

func (r *memoryWorkspaceRepository) reconcileAuditCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, audit := range r.audits {
		if audit.Action == reconcileAction {
			count++
		}
	}
	return count
}

func removePublicManifest(t *testing.T, root *ScopedRoot) {
	t.Helper()
	if err := root.RemoveRegular(context.Background(), controlManifestPath); err != nil {
		t.Fatalf("RemoveRegular(manifest) error = %v", err)
	}
}

func removePublicManifestIfPresent(t *testing.T, root *ScopedRoot) {
	t.Helper()
	if err := root.RemoveRegular(context.Background(), controlManifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("RemoveRegular(%s) error = %v", controlManifestPath, err)
	}
}

func closeReconcileRoot(t *testing.T, root *ScopedRoot) {
	t.Helper()
	if err := root.Close(); err != nil {
		t.Errorf("Close(workspace root) error = %v", err)
	}
}
