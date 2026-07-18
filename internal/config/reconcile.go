/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	reasonWorkspaceMissing         = "WORKSPACE_MISSING"
	reasonWorkspaceInvalid         = "WORKSPACE_INVALID"
	reasonControlMissing           = "CONTROL_MISSING"
	reasonControlInvalid           = "CONTROL_INVALID"
	reasonControlSchemaUnsupported = "CONTROL_SCHEMA_UNSUPPORTED"
	reasonRevisionMismatch         = "REVISION_MISMATCH"
	reasonManifestMissing          = "MANIFEST_MISSING"
	reasonManifestInvalid          = "MANIFEST_INVALID"
	reasonManifestMetadataMismatch = "MANIFEST_METADATA_MISMATCH"
	reasonPreparedManifestMissing  = "PREPARED_MANIFEST_MISSING"
	reasonPreparedManifestInvalid  = "PREPARED_MANIFEST_INVALID"
	reasonPreparedManifestMismatch = "PREPARED_MANIFEST_MISMATCH"
	reasonBaseDigestMismatch       = "BASE_DIGEST_MISMATCH"
	reasonDraftDigestMismatch      = "DRAFT_DIGEST_MISMATCH"
	reasonDatabaseMissing          = "DATABASE_MISSING"
	reasonDeleteTombstoneMismatch  = "DELETE_TOMBSTONE_MISMATCH"
	reasonJournalInvalid           = "JOURNAL_INVALID"
	reasonRecoveryInvalid          = "RECOVERY_INVALID"
	reasonRecoveryStateMismatch    = "RECOVERY_STATE_MISMATCH"
	reasonRecoveryFinalized        = "RECOVERY_FINALIZED"
	reconcileAction                = "config.workspace.reconcile"
)

// Reconcile deterministically recovers bounded workspace crash states without consulting production.
func (s *Service) Reconcile(ctx context.Context) error {
	if ctx == nil || s == nil {
		return fmt.Errorf("reconcile workspaces: service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("reconcile workspaces: %w", err)
	}
	entries, err := readWorkspaceRootEntries(s.workspaceRoot, s.limits.MaxWorkspaces)
	if err != nil {
		return fmt.Errorf("reconcile workspaces: %w", err)
	}
	workspaces, err := s.reader.ListWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("reconcile workspaces: list metadata: %w", err)
	}
	if len(workspaces) > s.limits.MaxWorkspaces {
		return fmt.Errorf("reconcile workspaces: %w", ErrLimitExceeded)
	}
	byID := make(map[WorkspaceID]Workspace, len(workspaces))
	for _, workspace := range workspaces {
		byID[workspace.ID] = workspace
	}
	handled := make(map[WorkspaceID]struct{})

	for _, entry := range entries {
		id, operationID, ok := parseDeleteTombstoneName(entry.Name())
		if !ok {
			continue
		}
		workspace, registered := byID[id]
		if err := s.reconcileTombstone(ctx, entry, id, operationID, workspace, registered); err != nil {
			return err
		}
		if registered {
			handled[id] = struct{}{}
		}
	}

	entryByName := make(map[string]fs.DirEntry, len(entries))
	for _, entry := range entries {
		entryByName[entry.Name()] = entry
	}
	for _, workspace := range workspaces {
		if _, alreadyHandled := handled[workspace.ID]; alreadyHandled {
			continue
		}
		entry, exists := entryByName[string(workspace.ID)]
		if !exists {
			if err := s.markNeedsAttention(ctx, workspace, reasonWorkspaceMissing, nil, false); err != nil {
				return err
			}
			continue
		}
		if err := s.reconcileRegistered(ctx, workspace, entry); err != nil {
			return err
		}
	}

	for _, entry := range entries {
		id, err := ParseWorkspaceID(entry.Name())
		if err != nil {
			continue
		}
		if _, registered := byID[id]; registered {
			continue
		}
		if err := s.reconcileOrphan(ctx, id, entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileRegistered(ctx context.Context, workspace Workspace, entry fs.DirEntry) error {
	if entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
		return s.markNeedsAttention(ctx, workspace, reasonWorkspaceInvalid, nil, false)
	}
	workspacePath := filepath.Join(s.workspaceRoot, string(workspace.ID))
	root, err := OpenScopedRoot(workspacePath)
	if err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonWorkspaceInvalid, nil, false)
	}
	defer func() {
		// Lifecycle state is already fail-closed; descriptor cleanup cannot change the outcome.
		_ = root.Close()
	}()
	lock, err := AcquireWorkspaceLock(ctx, root, LockExclusive)
	if err != nil {
		return fmt.Errorf("reconcile workspace lock: %w", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	journal, journalErr := ReadJournal(ctx, root)
	switch {
	case journalErr == nil:
		if journal.Kind == "workspace_delete" {
			return s.reconcileWorkspaceDeleteOriginal(ctx, workspace, root, journal)
		}
		return s.reconcileFileJournal(ctx, workspace, root, journal)
	case !errors.Is(journalErr, fs.ErrNotExist):
		return s.markNeedsAttention(ctx, workspace, reasonJournalInvalid, root, true)
	}
	if err := cleanupOrphanRecovery(ctx, root); err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
	}

	if workspace.State != StateReady {
		return nil
	}

	state, err := ReadControlState(ctx, root)
	if err != nil {
		reason := reasonControlInvalid
		allowWrite := true
		switch {
		case errors.Is(err, fs.ErrNotExist):
			reason = reasonControlMissing
		case errors.Is(err, errControlSchemaUnsupported):
			reason = reasonControlSchemaUnsupported
			allowWrite = false
		}
		return s.markNeedsAttention(ctx, workspace, reason, root, allowWrite)
	}
	if state.WorkspaceID != workspace.ID {
		return s.markNeedsAttention(ctx, workspace, reasonControlInvalid, root, true)
	}
	if state.Revision > workspace.Revision {
		return s.markNeedsAttention(ctx, workspace, reasonRevisionMismatch, root, true)
	}
	if state.State != StateReady && state.State != StatePreparing {
		return s.markNeedsAttention(ctx, workspace, reasonControlInvalid, root, true)
	}

	manifest, prepared, reason := s.reconciliationManifest(ctx, root, state)
	if reason != "" {
		return s.markNeedsAttention(ctx, workspace, reason, root, true)
	}
	if manifest.EntryCount != workspace.EntryCount || manifest.ManagedBytes != workspace.ManagedBytes ||
		manifest.Digest() != workspace.DraftDigest {
		if prepared {
			reason = reasonPreparedManifestMismatch
		} else {
			reason = reasonManifestMetadataMismatch
		}
		return s.markNeedsAttention(ctx, workspace, reason, root, true)
	}
	if err := verifyWorkspaceDirectories(workspacePath); err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonWorkspaceInvalid, root, true)
	}
	baseManifest, err := readManifestAt(ctx, root, controlBaseManifestPath, s.limits)
	if errors.Is(err, fs.ErrNotExist) && workspace.Revision == 1 {
		baseManifest = manifest
		err = nil
	}
	if err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonBaseDigestMismatch, root, true)
	}
	baseDigest, err := verifyManagedTree(ctx, workspacePath, "base", baseManifest, s.limits, baseManagedFileMode)
	if err != nil || baseDigest != workspace.BaseDigest {
		return s.markNeedsAttention(ctx, workspace, reasonBaseDigestMismatch, root, true)
	}
	draftDigest, err := verifyManagedTree(ctx, workspacePath, "draft", manifest, s.limits, draftManagedFileMode)
	if err != nil || draftDigest != workspace.DraftDigest {
		return s.markNeedsAttention(ctx, workspace, reasonDraftDigestMismatch, root, true)
	}

	if state.State == StatePreparing {
		return s.finalizeCommittedWorkspace(ctx, workspace, state, manifest, prepared, root)
	}
	if state.Revision < workspace.Revision {
		if err := WriteControlState(ctx, root, controlStateFromWorkspace(workspace)); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonControlInvalid, root, true)
		}
	}
	removePreparedManifest(ctx, root)
	return nil
}

func (s *Service) reconciliationManifest(
	ctx context.Context,
	root *ScopedRoot,
	state ControlState,
) (Manifest, bool, string) {
	manifest, err := ReadControlManifest(ctx, root, s.limits)
	if err == nil {
		return manifest, false, ""
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, reasonManifestInvalid
	}
	if state.State != StatePreparing {
		return Manifest{}, false, reasonManifestMissing
	}
	manifest, err = readPreparedControlManifest(ctx, root, s.limits)
	if err == nil {
		return manifest, true, ""
	}
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, true, reasonPreparedManifestMissing
	}
	return Manifest{}, true, reasonPreparedManifestInvalid
}

func (s *Service) finalizeCommittedWorkspace(
	ctx context.Context,
	workspace Workspace,
	state ControlState,
	manifest Manifest,
	prepared bool,
	root *ScopedRoot,
) error {
	if state.Revision == workspace.Revision {
		next := workspace
		next.Revision++
		next.UpdatedAt = s.clock.Now().UTC()
		change, err := s.reconcileChange(workspace, next, reasonRecoveryFinalized)
		if err != nil {
			return err
		}
		if err := s.writer.UpdateWorkspace(ctx, change); err != nil {
			return fmt.Errorf("reconcile committed workspace metadata: %w", err)
		}
		workspace = next
	}
	if prepared {
		if err := WriteControlManifest(ctx, root, manifest); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonManifestInvalid, root, true)
		}
	}
	if workspace.Revision > 1 {
		_, err := readManifestAt(ctx, root, controlBaseManifestPath, s.limits)
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist) && manifest.Digest() == workspace.BaseDigest:
			if err := writeManifestAt(ctx, root, controlBaseManifestPath, manifest); err != nil {
				return s.markNeedsAttention(ctx, workspace, reasonBaseDigestMismatch, root, true)
			}
		default:
			return s.markNeedsAttention(ctx, workspace, reasonBaseDigestMismatch, root, true)
		}
	}
	if err := WriteControlState(ctx, root, controlStateFromWorkspace(workspace)); err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonControlInvalid, root, true)
	}
	removePreparedManifest(ctx, root)
	return nil
}

func (s *Service) markNeedsAttention(
	ctx context.Context,
	workspace Workspace,
	reason string,
	root *ScopedRoot,
	writeControl bool,
) error {
	if workspace.State == StateNeedsAttention {
		return nil
	}
	next := workspace
	next.State = StateNeedsAttention
	next.StateReasonCode = reason
	next.Revision++
	next.UpdatedAt = s.clock.Now().UTC()
	change, err := s.reconcileChange(workspace, next, reason)
	if err != nil {
		return err
	}
	if err := s.writer.UpdateWorkspace(ctx, change); err != nil {
		return fmt.Errorf("mark workspace needs attention: %w", err)
	}
	if root != nil && writeControl {
		// SQLite remains the authoritative lifecycle record if damaged control bytes cannot be replaced.
		_ = WriteControlState(context.WithoutCancel(ctx), root, controlStateFromWorkspace(next))
	}
	return nil
}

func (s *Service) reconcileChange(before, next Workspace, reason string) (WorkspaceChange, error) {
	operationID, err := s.newOperationID()
	if err != nil {
		return WorkspaceChange{}, err
	}
	beforeDigest := before.DraftDigest
	afterDigest := next.DraftDigest
	operation := OperationRecord{
		ID: operationID, ObjectType: workspaceObjectType, ObjectID: string(next.ID),
		Action: reconcileAction, BeforeDigest: &beforeDigest, AfterDigest: &afterDigest,
		Result: operationResultSuccess, RequestID: operationID, OccurredAt: next.UpdatedAt,
	}
	details, _ := json.Marshal(struct {
		ReasonCode string `json:"reason_code"`
	}{ReasonCode: reason})
	return WorkspaceChange{
		ExpectedRevision: before.Revision, Next: next, Operation: operation,
		Audit: AuditEvent{
			OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: before.CreatedBy,
			Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		},
	}, nil
}

func controlStateFromWorkspace(workspace Workspace) ControlState {
	return ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID, State: workspace.State,
		StateReasonCode: workspace.StateReasonCode, Revision: workspace.Revision, UpdatedAt: workspace.UpdatedAt,
	}
}

func (s *Service) reconcileOrphan(ctx context.Context, id WorkspaceID, entry fs.DirEntry) error {
	if entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
		return nil
	}
	path := filepath.Join(s.workspaceRoot, string(id))
	information, err := os.Lstat(path)
	if err != nil || information.Mode().Perm() != workspaceDirectoryMode {
		return nil
	}
	root, err := OpenScopedRoot(path)
	if err != nil {
		return nil
	}
	defer func() {
		// Orphan classification remains fail-closed if descriptor cleanup itself fails.
		_ = root.Close()
	}()
	state, stateErr := ReadControlState(ctx, root)
	if stateErr == nil && state.WorkspaceID == id && state.State == StatePreparing {
		if err := removeOwnedWorkspace(context.WithoutCancel(ctx), path, root, s.limits); err != nil {
			return fmt.Errorf("reconcile unregistered workspace: %w", err)
		}
		root = nil
		return nil
	}
	if errors.Is(stateErr, errControlSchemaUnsupported) {
		return nil
	}
	revision := uint64(1)
	if stateErr == nil && state.WorkspaceID == id {
		revision = state.Revision + 1
	}
	attention := ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: id, State: StateNeedsAttention,
		StateReasonCode: reasonDatabaseMissing, Revision: revision, UpdatedAt: s.clock.Now().UTC(),
	}
	if err := WriteControlState(ctx, root, attention); err != nil {
		return nil
	}
	return nil
}

func (s *Service) reconcileTombstone(
	ctx context.Context,
	entry fs.DirEntry,
	id WorkspaceID,
	operationID string,
	workspace Workspace,
	registered bool,
) error {
	if entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
		if registered {
			return s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, nil, false)
		}
		return nil
	}
	path := filepath.Join(s.workspaceRoot, entry.Name())
	root, err := OpenScopedRoot(path)
	if err != nil {
		if registered {
			return s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, nil, false)
		}
		return nil
	}
	lock, err := AcquireWorkspaceLock(ctx, root, LockExclusive)
	if err != nil {
		_ = root.Close()
		return fmt.Errorf("reconcile tombstone lock: %w", err)
	}
	journal, journalErr := ReadJournal(ctx, root)
	if journalErr != nil || journal.Kind != "workspace_delete" || journal.WorkspaceID != id || journal.OperationID != operationID {
		if registered {
			return errors.Join(
				s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, nil, false),
				lock.Close(), root.Close(),
			)
		}
		markErr := markOrphanTombstoneNeedsAttention(ctx, root, id, reasonDeleteTombstoneMismatch, s.clock.Now().UTC())
		return errors.Join(markErr, lock.Close(), root.Close())
	}
	committed, commitErr := s.workspaceDeleteCommitted(ctx, journal, workspaceNameWhenRegistered(workspace, registered))
	if commitErr != nil {
		if registered {
			return errors.Join(
				s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, nil, false),
				lock.Close(), root.Close(),
			)
		}
		markErr := markOrphanTombstoneNeedsAttention(ctx, root, id, reasonDeleteTombstoneMismatch, s.clock.Now().UTC())
		return errors.Join(markErr, lock.Close(), root.Close())
	}
	if registered {
		if committed || journal.ExpectedRevision != workspace.Revision || journal.BeforeDigest != workspace.DraftDigest ||
			journal.Phase == JournalSQLCommitted || journal.Phase == JournalControlCommitted {
			return errors.Join(
				s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, nil, false),
				lock.Close(), root.Close(),
			)
		}
		original := filepath.Join(s.workspaceRoot, string(id))
		if err := renameOwnedWorkspace(root, path, original); err != nil {
			return errors.Join(
				s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, nil, false),
				lock.Close(), root.Close(),
			)
		}
		removeErr := RemoveJournal(context.WithoutCancel(ctx), root)
		return errors.Join(removeErr, lock.Close(), root.Close())
	}
	if !committed {
		markErr := markOrphanTombstoneNeedsAttention(ctx, root, id, reasonDeleteTombstoneMismatch, s.clock.Now().UTC())
		return errors.Join(markErr, lock.Close(), root.Close())
	}
	if err := removeOwnedWorkspace(context.WithoutCancel(ctx), path, root, s.limits); err != nil {
		return errors.Join(fmt.Errorf("reconcile tombstone cleanup: %w", err), lock.Close())
	}
	return lock.Close()
}

func (s *Service) reconcileWorkspaceDeleteOriginal(
	ctx context.Context,
	workspace Workspace,
	root *ScopedRoot,
	journal Journal,
) error {
	if journal.WorkspaceID != workspace.ID || journal.ExpectedRevision != workspace.Revision ||
		journal.BeforeDigest != workspace.DraftDigest || journal.AfterDigest != workspace.DraftDigest {
		return s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, root, true)
	}
	committed, err := s.workspaceDeleteCommitted(ctx, journal, workspace.Name)
	if err != nil || committed || journal.Phase == JournalSQLCommitted || journal.Phase == JournalControlCommitted {
		return s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, root, true)
	}
	if err := RemoveJournal(context.WithoutCancel(ctx), root); err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonDeleteTombstoneMismatch, root, true)
	}
	return nil
}

func (s *Service) workspaceDeleteCommitted(ctx context.Context, journal Journal, expectedName string) (bool, error) {
	operation, audit, found, err := s.reader.OperationAudit(ctx, journal.OperationID)
	if err != nil || !found {
		return false, err
	}
	if operation.ID != journal.OperationID || operation.ObjectType != workspaceObjectType ||
		operation.ObjectID != string(journal.WorkspaceID) || operation.Action != "config.workspace.delete" ||
		operation.BeforeDigest == nil || *operation.BeforeDigest != journal.BeforeDigest || operation.AfterDigest != nil ||
		operation.Result != operationResultSuccess || operation.RequestID != journal.Actor.RequestID ||
		!operation.OccurredAt.Equal(journal.StartedAt) || audit.OperationID != operation.ID ||
		audit.ActorUserID != journal.Actor.UserID || audit.Action != operation.Action || audit.ObjectType != operation.ObjectType ||
		audit.ObjectID != operation.ObjectID || audit.Result != operation.Result || audit.RequestID != operation.RequestID ||
		!audit.OccurredAt.Equal(journal.StartedAt) {
		return false, ErrConflict
	}
	name, err := workspaceDeleteAuditName(audit.DetailsJSON)
	if err != nil || expectedName != "" && name != expectedName {
		return false, ErrConflict
	}
	return true, nil
}

func workspaceDeleteAuditName(details string) (string, error) {
	if err := rejectDuplicateJSONFields([]byte(details)); err != nil {
		return "", err
	}
	var value struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(strings.NewReader(details))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", ErrConflict
	}
	return ValidateDisplayName(value.Name)
}

func workspaceNameWhenRegistered(workspace Workspace, registered bool) string {
	if !registered {
		return ""
	}
	return workspace.Name
}

func markOrphanTombstoneNeedsAttention(
	ctx context.Context,
	root *ScopedRoot,
	id WorkspaceID,
	reason string,
	updatedAt time.Time,
) error {
	revision := uint64(1)
	if state, err := ReadControlState(ctx, root); err == nil && state.WorkspaceID == id {
		if state.State == StateNeedsAttention && state.StateReasonCode == reason {
			return nil
		}
		revision = state.Revision + 1
	}
	return WriteControlState(context.WithoutCancel(ctx), root, ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: id, State: StateNeedsAttention,
		StateReasonCode: reason, Revision: revision, UpdatedAt: updatedAt,
	})
}

func readWorkspaceRootEntries(root string, maxWorkspaces int) (entries []fs.DirEntry, returnErr error) {
	if maxWorkspaces <= 0 {
		return nil, ErrLimitExceeded
	}
	// #nosec G304 -- root is trusted process configuration validated by NewService.
	directory, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := directory.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close workspace root: %w", err)
		}
	}()
	limit := 2*maxWorkspaces + 2
	entries, err = directory.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > limit {
		return nil, ErrLimitExceeded
	}
	return entries, nil
}

func parseDeleteTombstoneName(name string) (WorkspaceID, string, bool) {
	trimmed, ok := strings.CutPrefix(name, ".delete-")
	if !ok || len(trimmed) != 65 || trimmed[32] != '-' {
		return "", "", false
	}
	id, err := ParseWorkspaceID(trimmed[:32])
	if err != nil || !validOpaqueID(trimmed[33:]) {
		return "", "", false
	}
	return id, trimmed[33:], true
}

func removePreparedManifest(ctx context.Context, root *ScopedRoot) {
	if err := root.RemoveRegular(context.WithoutCancel(ctx), controlPreparedManifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// A leftover private prepared record is safe and will be retried during the next reconciliation.
		return
	}
}
