/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package config

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ReplaceFiles atomically replaces a stable set of existing managed draft files.
func (s *Service) ReplaceFiles(
	ctx context.Context,
	actor Actor,
	id WorkspaceID,
	input ReplaceFilesInput,
) (_ ReplaceFilesResult, returnErr error) {
	if ctx == nil || s == nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	parsedID, err := ParseWorkspaceID(string(id))
	if err != nil || parsedID != id {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", ErrIdentifierInvalid)
	}
	replacements, err := s.canonicalReplacements(input)
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	if err := validateStructuredMutationMetadata(input); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	if err := s.requireResolvedAttention(ctx); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}

	root, err := OpenScopedRoot(s.workspacePath(id))
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: open workspace: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close replaced workspace files", root.Close()))
	}()
	lock, err := AcquireWorkspaceLock(ctx, root, LockExclusive)
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("release replaced workspace files lock", lock.Close()))
	}()

	workspace, manifest, baseManifest, ensureBase, err := s.verifiedWorkspaceUnderLock(ctx, id, root, workspaceAccessMutation)
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	if err := requireWorkspaceActor(workspace, actor); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	if err := requireWorkspaceETag(input.IfMatch, workspace); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := s.verifyProductionForMutation(ctx, actor.RequestID, workspace, root); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}

	afterManifest, beforeContents, err := proposedReplacementManifest(
		ctx, root.path, manifest, s.limits, replacements,
	)
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: build manifest: %w", err)
	}
	operationID, err := s.newOperationID()
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	startedAt := s.clock.Now().UTC()
	paths := replacementPaths(replacements)
	journal := Journal{
		SchemaVersion: JournalSchemaVersion, OperationID: operationID, Kind: "replace_batch", Phase: JournalPrepared,
		WorkspaceID: id, ExpectedRevision: workspace.Revision, NextRevision: workspace.Revision + 1,
		BeforeDigest: workspace.DraftDigest, AfterDigest: afterManifest.Digest(), Actor: actor,
		Paths: paths, StartedAt: startedAt,
	}
	additional, err := replacementRecoveryBytes(
		beforeContents,
		replacements,
		manifest,
		afterManifest,
		baseManifest,
		ensureBase,
		journal,
	)
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	if err := s.checkMutationCapacity(ctx, root, workspace, additional); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}
	if err := s.mutationCheckpoint("before_recovery"); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := writeReplacementRecovery(
		ctx, root, journal, beforeContents, manifest, afterManifest, baseManifest, ensureBase,
	); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: prepare recovery: %w", err)
	}
	if err := s.mutationCheckpoint("after_recovery"); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: %w", err)
	}

	draft, err := OpenScopedRoot(root.path + "/draft")
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: open draft: %w", err)
	}
	if err := s.mutationCheckpoint("before_filesystem"); err != nil {
		return ReplaceFilesResult{}, errors.Join(err, draft.Close())
	}
	applyErr := s.applyReplacements(ctx, draft, replacements)
	closeErr := draft.Close()
	if applyErr != nil || closeErr != nil {
		return ReplaceFilesResult{}, errors.Join(applyErr, wrapServiceError("close replacement draft", closeErr))
	}
	if err := s.mutationCheckpoint("after_filesystem"); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := verifyManagedTreeDigest(
		ctx, root.path, "draft", afterManifest, s.limits, draftManagedFileMode, journal.AfterDigest,
	); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: verify applied files: %w", err)
	}
	journal.Phase = JournalFilesApplied
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return ReplaceFilesResult{}, err
	}

	next := workspace
	next.DraftDigest = journal.AfterDigest
	next.EntryCount = afterManifest.EntryCount
	next.ManagedBytes = afterManifest.ManagedBytes
	next.Revision = journal.NextRevision
	next.UpdatedAt = startedAt
	next.WorkspaceBytes, err = stableWorkspaceBytes(
		ctx, root, afterManifest, baseManifest, ensureBase, controlStateFromWorkspace(next),
	)
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: measure final workspace: %w", err)
	}
	change, err := replacementWorkspaceChange(workspace, next, journal, input)
	if err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: build audit: %w", err)
	}
	if err := s.mutationCheckpoint("before_database"); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := s.writer.UpdateWorkspace(ctx, change); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files metadata: %w", err)
	}
	if err := s.mutationCheckpoint("after_database"); err != nil {
		return ReplaceFilesResult{}, err
	}
	journal.Phase = JournalSQLCommitted
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return ReplaceFilesResult{}, err
	}
	if ensureBase {
		if err := s.mutationCheckpoint("before_control_base_manifest"); err != nil {
			return ReplaceFilesResult{}, err
		}
		if err := writeManifestAt(ctx, root, controlBaseManifestPath, baseManifest); err != nil {
			return ReplaceFilesResult{}, fmt.Errorf("replace workspace files base manifest: %w", err)
		}
		if err := s.mutationCheckpoint("after_control_base_manifest"); err != nil {
			return ReplaceFilesResult{}, err
		}
	}
	if err := s.mutationCheckpoint("before_control_manifest"); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := WriteControlManifest(ctx, root, afterManifest); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := s.mutationCheckpoint("after_control_manifest"); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := s.mutationCheckpoint("before_control_state"); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := WriteControlState(ctx, root, controlStateFromWorkspace(next)); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := s.mutationCheckpoint("after_control_state"); err != nil {
		return ReplaceFilesResult{}, err
	}
	journal.Phase = JournalControlCommitted
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return ReplaceFilesResult{}, err
	}
	if err := removeRecovery(ctx, root, journal); err != nil {
		return ReplaceFilesResult{}, fmt.Errorf("replace workspace files: cleanup recovery: %w", err)
	}
	if err := RemoveJournal(ctx, root); err != nil {
		return ReplaceFilesResult{}, err
	}
	return ReplaceFilesResult{Workspace: next, ChangedPaths: slices.Clone(paths)}, nil
}

func (s *Service) canonicalReplacements(input ReplaceFilesInput) ([]FileReplacement, error) {
	if len(input.Replacements) == 0 || len(input.Replacements) > s.limits.MaxEntries {
		return nil, ErrLimitExceeded
	}
	replacements := make([]FileReplacement, len(input.Replacements))
	for index, replacement := range input.Replacements {
		path, err := s.parseFilePath(replacement.Path)
		if err != nil {
			return nil, err
		}
		if err := validateManagedContent(path, replacement.Content, s.limits); err != nil {
			return nil, err
		}
		replacements[index] = FileReplacement{Path: path, Content: slices.Clone(replacement.Content)}
	}
	slices.SortFunc(replacements, func(left, right FileReplacement) int {
		return strings.Compare(string(left.Path), string(right.Path))
	})
	for index := 1; index < len(replacements); index++ {
		if replacements[index-1].Path == replacements[index].Path {
			return nil, ErrPathInvalid
		}
	}
	return replacements, nil
}

func validateStructuredMutationMetadata(input ReplaceFilesInput) error {
	if !validStructuredOperationKind(input.OperationKind) {
		return ErrIdentifierInvalid
	}
	if _, err := ParseDigest(input.PreviewID); err != nil || !validOpaqueID(input.TargetID) {
		return ErrIdentifierInvalid
	}
	return nil
}

func validStructuredOperationKind(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] == '.' || value[len(value)-1] == '.' ||
		strings.Contains(value, "..") {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func proposedReplacementManifest(
	ctx context.Context,
	workspacePath string,
	current Manifest,
	limits Limits,
	replacements []FileReplacement,
) (Manifest, map[RelativePath][]byte, error) {
	contents := make(map[RelativePath][]byte)
	for _, entry := range current.Entries {
		if entry.Class != EntryManagedText {
			continue
		}
		content, err := readDraftContent(ctx, workspacePath, entry.Path, limits)
		if err != nil {
			return Manifest{}, nil, err
		}
		contents[entry.Path] = content
	}

	entries := slices.Clone(current.Entries)
	before := make(map[RelativePath][]byte, len(replacements))
	for _, replacement := range replacements {
		index := slices.IndexFunc(entries, func(entry Entry) bool { return entry.Path == replacement.Path })
		if index < 0 || entries[index].Type != EntryRegular || entries[index].Class != EntryManagedText {
			return Manifest{}, nil, ErrEntryNotManaged
		}
		previous := contents[replacement.Path]
		if Digest(sha256.Sum256(previous)) != entries[index].ContentDigest {
			return Manifest{}, nil, ErrConflict
		}
		if slices.Equal(previous, replacement.Content) {
			return Manifest{}, nil, ErrConflict
		}
		before[replacement.Path] = slices.Clone(previous)
		contents[replacement.Path] = slices.Clone(replacement.Content)
		entries[index].Size = int64(len(replacement.Content))
		entries[index].ContentDigest = Digest(sha256.Sum256(replacement.Content))
	}
	manifest, err := manifestFromContents(ctx, entries, contents, limits)
	if err != nil {
		return Manifest{}, nil, err
	}
	return manifest, before, nil
}

func replacementPaths(replacements []FileReplacement) []RelativePath {
	paths := make([]RelativePath, len(replacements))
	for index, replacement := range replacements {
		paths[index] = replacement.Path
	}
	return paths
}

func replacementRecoveryBytes(
	contents map[RelativePath][]byte,
	replacements []FileReplacement,
	beforeManifest Manifest,
	afterManifest Manifest,
	baseManifest Manifest,
	includeBase bool,
	journal Journal,
) (int64, error) {
	journalBytes, err := journalPayloadSize(journal)
	if err != nil {
		return 0, err
	}
	additional, err := mutationCapacityBytes(
		int64(manifestPayloadSize(beforeManifest)),
		int64(manifestPayloadSize(afterManifest)),
		journalBytes,
	)
	if err != nil {
		return 0, err
	}
	if includeBase {
		baseBytes := int64(manifestPayloadSize(baseManifest))
		additional, err = mutationCapacityBytes(additional, baseBytes, baseBytes)
		if err != nil {
			return 0, err
		}
	}
	for _, content := range contents {
		additional, err = mutationCapacityBytes(additional, int64(len(content)))
		if err != nil {
			return 0, err
		}
	}
	for _, replacement := range replacements {
		additional, err = mutationCapacityBytes(additional, int64(len(replacement.Content)))
		if err != nil {
			return 0, err
		}
	}
	return additional, nil
}

func (s *Service) applyReplacements(
	ctx context.Context,
	draft *ScopedRoot,
	replacements []FileReplacement,
) error {
	for index, replacement := range replacements {
		checkpoint := fmt.Sprintf("filesystem_replace_%06d", index)
		if err := s.mutationCheckpoint("before_" + checkpoint); err != nil {
			return err
		}
		if err := draft.AtomicReplace(ctx, replacement.Path, replacement.Content, draftManagedFileMode); err != nil {
			return err
		}
		if err := s.mutationCheckpoint("after_" + checkpoint); err != nil {
			return err
		}
	}
	return nil
}

func replacementRecoveryName(index int) string {
	return fmt.Sprintf("before-%06d.bin", index)
}

func writeReplacementRecovery(
	ctx context.Context,
	root *ScopedRoot,
	journal Journal,
	beforeContents map[RelativePath][]byte,
	beforeManifest, afterManifest, baseManifest Manifest,
	includeBase bool,
) error {
	directory := RelativePath("control/recovery/" + journal.OperationID)
	if err := root.EnsureDirectory(ctx, directory, workspaceDirectoryMode); err != nil {
		return err
	}
	for index, path := range journal.Paths {
		content, exists := beforeContents[path]
		if !exists {
			return ErrConflict
		}
		if err := root.CreateRegular(
			ctx, recoveryPath(journal, replacementRecoveryName(index)), content, controlFileMode,
		); err != nil {
			return err
		}
	}
	payload, err := afterManifest.MarshalBinary()
	if err != nil {
		return err
	}
	if err := root.CreateRegular(ctx, recoveryPath(journal, "manifest.bin"), payload, controlFileMode); err != nil {
		return err
	}
	payload, err = beforeManifest.MarshalBinary()
	if err != nil {
		return err
	}
	if err := root.CreateRegular(ctx, recoveryPath(journal, "before-manifest.bin"), payload, controlFileMode); err != nil {
		return err
	}
	if includeBase {
		payload, err = baseManifest.MarshalBinary()
		if err != nil {
			return err
		}
		if err := root.CreateRegular(ctx, recoveryPath(journal, "base-manifest.bin"), payload, controlFileMode); err != nil {
			return err
		}
	}
	return nil
}

func isRecoveryEvidenceName(value string) bool {
	switch value {
	case "before.bin", "manifest.bin", "before-manifest.bin", "base-manifest.bin":
		return true
	}
	if len(value) != len("before-000000.bin") || !strings.HasPrefix(value, "before-") ||
		!strings.HasSuffix(value, ".bin") {
		return false
	}
	for _, character := range value[len("before-") : len(value)-len(".bin")] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (s *Service) reconcileReplacementJournal(
	ctx context.Context,
	workspace Workspace,
	root *ScopedRoot,
	journal Journal,
	beforeManifest Manifest,
	afterManifest Manifest,
) error {
	beforeContents, err := readReplacementRecovery(ctx, root, journal, beforeManifest, afterManifest, s.limits)
	if err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
	}
	baseManifest, includeBase, err := s.recoveryBaseManifest(ctx, root, workspace, journal)
	if err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
	}
	_, allAfter, err := classifyReplacementDraft(
		ctx, root.path, journal.Paths, beforeManifest, afterManifest, s.limits,
	)
	if err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryStateMismatch, root, true)
	}

	switch workspace.Revision {
	case journal.ExpectedRevision:
		if workspace.DraftDigest != journal.BeforeDigest || journal.Phase == JournalSQLCommitted ||
			journal.Phase == JournalControlCommitted {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryStateMismatch, root, true)
		}
		if err := restoreReplacementBatch(ctx, root.path, journal.Paths, beforeContents); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
		}
		if !managedTreeMatches(ctx, root.path, beforeManifest, s.limits, journal.BeforeDigest) {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryStateMismatch, root, true)
		}
		if err := WriteControlManifest(ctx, root, beforeManifest); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
		}
		if err := WriteControlState(ctx, root, controlStateFromWorkspace(workspace)); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
		}
		return cleanupMutationEvidence(ctx, root, journal)
	case journal.NextRevision:
		if workspace.DraftDigest != journal.AfterDigest || !allAfter || journal.Phase == JournalPrepared ||
			!managedTreeMatches(ctx, root.path, afterManifest, s.limits, journal.AfterDigest) {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryStateMismatch, root, true)
		}
		if includeBase {
			if err := writeManifestAt(ctx, root, controlBaseManifestPath, baseManifest); err != nil {
				return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
			}
		}
		if err := WriteControlManifest(ctx, root, afterManifest); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
		}
		if err := WriteControlState(ctx, root, controlStateFromWorkspace(workspace)); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
		}
		return cleanupMutationEvidence(ctx, root, journal)
	default:
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryStateMismatch, root, true)
	}
}

func readReplacementRecovery(
	ctx context.Context,
	root *ScopedRoot,
	journal Journal,
	beforeManifest Manifest,
	afterManifest Manifest,
	limits Limits,
) (map[RelativePath][]byte, error) {
	contents := make(map[RelativePath][]byte, len(journal.Paths))
	for index, path := range journal.Paths {
		beforeEntry, beforeExists := beforeManifest.Entry(path)
		afterEntry, afterExists := afterManifest.Entry(path)
		if !beforeExists || !afterExists || beforeEntry.Type != EntryRegular || afterEntry.Type != EntryRegular ||
			beforeEntry.Class != EntryManagedText || afterEntry.Class != EntryManagedText ||
			beforeEntry.ContentDigest == afterEntry.ContentDigest {
			return nil, ErrConflict
		}
		content, information, err := root.ReadRegular(
			ctx, recoveryPath(journal, replacementRecoveryName(index)), limits.MaxFileBytes,
		)
		if err != nil || information.Mode().Perm() != controlFileMode ||
			int64(len(content)) != beforeEntry.Size ||
			Digest(sha256.Sum256(content)) != beforeEntry.ContentDigest {
			return nil, errors.Join(err, ErrConflict)
		}
		contents[path] = content
	}
	return contents, nil
}

func classifyReplacementDraft(
	ctx context.Context,
	workspacePath string,
	paths []RelativePath,
	beforeManifest Manifest,
	afterManifest Manifest,
	limits Limits,
) (_ bool, _ bool, returnErr error) {
	draft, err := OpenScopedRoot(workspacePath + "/draft")
	if err != nil {
		return false, false, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close classified replacement draft", draft.Close()))
	}()
	allBefore := true
	allAfter := true
	for _, path := range paths {
		beforeEntry, beforeExists := beforeManifest.Entry(path)
		afterEntry, afterExists := afterManifest.Entry(path)
		if !beforeExists || !afterExists {
			return false, false, ErrConflict
		}
		content, information, err := draft.ReadRegular(ctx, path, limits.MaxFileBytes)
		if err != nil || information.Mode().Perm() != draftManagedFileMode {
			return false, false, errors.Join(err, ErrConflict)
		}
		digest := Digest(sha256.Sum256(content))
		isBefore := int64(len(content)) == beforeEntry.Size && digest == beforeEntry.ContentDigest
		isAfter := int64(len(content)) == afterEntry.Size && digest == afterEntry.ContentDigest
		if !isBefore && !isAfter {
			return false, false, ErrConflict
		}
		allBefore = allBefore && isBefore
		allAfter = allAfter && isAfter
	}
	return allBefore, allAfter, nil
}

func restoreReplacementBatch(
	ctx context.Context,
	workspacePath string,
	paths []RelativePath,
	contents map[RelativePath][]byte,
) (returnErr error) {
	draft, err := OpenScopedRoot(workspacePath + "/draft")
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close replacement rollback draft", draft.Close()))
	}()
	for _, path := range paths {
		content, exists := contents[path]
		if !exists {
			return ErrConflict
		}
		if err := draft.AtomicReplace(ctx, path, content, draftManagedFileMode); err != nil {
			return err
		}
	}
	return nil
}

func replacementWorkspaceChange(
	before Workspace,
	next Workspace,
	journal Journal,
	input ReplaceFilesInput,
) (WorkspaceChange, error) {
	beforeDigest := journal.BeforeDigest
	afterDigest := journal.AfterDigest
	operation := OperationRecord{
		ID: journal.OperationID, ObjectType: workspaceObjectType, ObjectID: string(journal.WorkspaceID),
		Action:       "config.structured." + input.OperationKind,
		BeforeDigest: &beforeDigest, AfterDigest: &afterDigest,
		Result: operationResultSuccess, RequestID: journal.Actor.RequestID, OccurredAt: journal.StartedAt,
	}
	details, err := json.Marshal(struct {
		PathCount int    `json:"path_count"`
		Before    string `json:"before_digest"`
		After     string `json:"after_digest"`
		PreviewID string `json:"preview_id"`
		TargetID  string `json:"target_id"`
	}{
		PathCount: len(journal.Paths), Before: beforeDigest.String(), After: afterDigest.String(),
		PreviewID: input.PreviewID, TargetID: input.TargetID,
	})
	if err != nil {
		return WorkspaceChange{}, err
	}
	return WorkspaceChange{
		ExpectedRevision: before.Revision, Next: next, Operation: operation,
		Audit: AuditEvent{
			OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: journal.Actor.UserID,
			Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		},
	}, nil
}
