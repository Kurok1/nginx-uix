/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

var controlBaseManifestPath = RelativePath("control/base-manifest.bin")

// Tree returns the complete canonical workspace inventory under a shared lock.
func (s *Service) Tree(ctx context.Context, id WorkspaceID) (_ TreeView, returnErr error) {
	workspace, root, lock, manifest, baseManifest, err := s.openVerifiedWorkspace(ctx, id, LockShared)
	if err != nil {
		return TreeView{}, fmt.Errorf("read workspace tree: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("release workspace tree lock", lock.Close()))
		returnErr = errors.Join(returnErr, wrapServiceError("close workspace tree", root.Close()))
	}()
	if err := verifyManagedTreeDigest(ctx, root.path, "draft", manifest, s.limits, draftManagedFileMode, workspace.DraftDigest); err != nil {
		return TreeView{}, fmt.Errorf("read workspace tree: %w", err)
	}
	return TreeView{
		Entries: slices.Clone(manifest.Entries), Dependencies: slices.Clone(manifest.Dependencies),
		DiffStatuses: treeDiffStatuses(baseManifest, manifest), WorkspaceETag: workspace.ETag(),
	}, nil
}

// ReadFile returns one explicit managed text file under a shared lock.
func (s *Service) ReadFile(ctx context.Context, id WorkspaceID, rawPath RelativePath) (_ FileView, returnErr error) {
	path, err := s.parseFilePath(rawPath)
	if err != nil {
		return FileView{}, fmt.Errorf("read workspace file: %w", err)
	}
	workspace, root, lock, manifest, _, err := s.openVerifiedWorkspace(ctx, id, LockShared)
	if err != nil {
		return FileView{}, fmt.Errorf("read workspace file: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("release workspace file lock", lock.Close()))
		returnErr = errors.Join(returnErr, wrapServiceError("close workspace file", root.Close()))
	}()
	entry, ok := manifest.Entry(path)
	if !ok || entry.Type != EntryRegular || entry.Class != EntryManagedText {
		return FileView{}, fmt.Errorf("read workspace file: %w", ErrEntryNotManaged)
	}
	draft, err := OpenScopedRoot(root.path + "/draft")
	if err != nil {
		return FileView{}, fmt.Errorf("read workspace file: open draft: %w", err)
	}
	content, info, readErr := draft.ReadRegular(ctx, path, s.limits.MaxFileBytes)
	closeErr := draft.Close()
	if readErr != nil || closeErr != nil {
		return FileView{}, errors.Join(readErr, wrapServiceError("close draft", closeErr))
	}
	if info.Mode().Perm() != draftManagedFileMode || int64(len(content)) != entry.Size ||
		Digest(sha256.Sum256(content)) != entry.ContentDigest {
		return FileView{}, fmt.Errorf("read workspace file: %w", ErrConflict)
	}
	if err := verifyManagedTreeDigest(ctx, root.path, "draft", manifest, s.limits, draftManagedFileMode, workspace.DraftDigest); err != nil {
		return FileView{}, fmt.Errorf("read workspace file: %w", err)
	}
	return FileView{
		Entry: entry, Content: string(content), LineEnding: detectLineEnding(content), WorkspaceETag: workspace.ETag(),
	}, nil
}

// ReplaceFile atomically replaces one managed text file and advances the workspace revision.
func (s *Service) ReplaceFile(
	ctx context.Context,
	actor Actor,
	id WorkspaceID,
	input ReplaceFileInput,
) (MutationResult, error) {
	path, err := s.parseFilePath(input.Path)
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	if err := validateManagedContent(path, input.Content, s.limits); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	return s.mutateFile(ctx, actor, id, fileProposal{kind: "replace", source: path, content: slices.Clone(input.Content)}, input.IfMatch)
}

// CreateFile exclusively creates one managed text file in an existing writable directory.
func (s *Service) CreateFile(ctx context.Context, actor Actor, id WorkspaceID, input CreateFileInput) (MutationResult, error) {
	path, err := s.parseFilePath(input.Path)
	if err != nil {
		return MutationResult{}, fmt.Errorf("create workspace file: %w", err)
	}
	if err := validateManagedContent(path, input.Content, s.limits); err != nil {
		return MutationResult{}, fmt.Errorf("create workspace file: %w", err)
	}
	return s.mutateFile(ctx, actor, id, fileProposal{kind: "create", destination: path, content: slices.Clone(input.Content)}, input.IfMatch)
}

// CopyFile copies one managed text source to a new managed destination.
func (s *Service) CopyFile(ctx context.Context, actor Actor, id WorkspaceID, input CopyFileInput) (MutationResult, error) {
	source, err := s.parseFilePath(input.SourcePath)
	if err != nil {
		return MutationResult{}, fmt.Errorf("copy workspace file: %w", err)
	}
	destination, err := s.parseFilePath(input.DestinationPath)
	if err != nil {
		return MutationResult{}, fmt.Errorf("copy workspace file: %w", err)
	}
	return s.mutateFile(ctx, actor, id, fileProposal{kind: "copy", source: source, destination: destination}, input.IfMatch)
}

// RenameFile copies one managed source to a new destination and then removes the source.
func (s *Service) RenameFile(ctx context.Context, actor Actor, id WorkspaceID, input RenameFileInput) (MutationResult, error) {
	source, err := s.parseFilePath(input.SourcePath)
	if err != nil {
		return MutationResult{}, fmt.Errorf("rename workspace file: %w", err)
	}
	destination, err := s.parseFilePath(input.DestinationPath)
	if err != nil {
		return MutationResult{}, fmt.Errorf("rename workspace file: %w", err)
	}
	return s.mutateFile(ctx, actor, id, fileProposal{kind: "rename", source: source, destination: destination}, input.IfMatch)
}

// DeleteFile removes one managed text file after exact path confirmation.
func (s *Service) DeleteFile(ctx context.Context, actor Actor, id WorkspaceID, input DeleteFileInput) (MutationResult, error) {
	path, err := s.parseFilePath(input.Path)
	if err != nil {
		return MutationResult{}, fmt.Errorf("delete workspace file: %w", err)
	}
	confirmed, err := s.parseFilePath(RelativePath(input.ConfirmPath))
	if err != nil || confirmed != path || input.ConfirmPath != string(path) {
		return MutationResult{}, fmt.Errorf("delete workspace file: confirm path: %w", ErrConflict)
	}
	return s.mutateFile(ctx, actor, id, fileProposal{kind: "delete", source: path}, input.IfMatch)
}

func (s *Service) mutateFile(
	ctx context.Context,
	actor Actor,
	id WorkspaceID,
	proposal fileProposal,
	ifMatch string,
) (_ MutationResult, returnErr error) {
	if ctx == nil || s == nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	parsedID, err := ParseWorkspaceID(string(id))
	if err != nil || parsedID != id {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", ErrIdentifierInvalid)
	}
	if err := s.requireResolvedAttention(ctx); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	root, err := OpenScopedRoot(s.workspacePath(id))
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: open workspace: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close replaced workspace", root.Close()))
	}()
	lock, err := AcquireWorkspaceLock(ctx, root, LockExclusive)
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("release replaced workspace lock", lock.Close()))
	}()

	workspace, manifest, baseManifest, ensureBase, err := s.verifiedWorkspaceUnderLock(ctx, id, root, true)
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	if err := requireWorkspaceActor(workspace, actor); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	if err := requireWorkspaceETag(ifMatch, workspace); err != nil {
		return MutationResult{}, err
	}
	if err := s.verifyProductionForMutation(ctx, actor.RequestID, workspace, root); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	beforeContent, err := validateFileProposal(ctx, root.path, manifest, s.limits, &proposal)
	if err != nil {
		return MutationResult{}, fmt.Errorf("mutate workspace file: %w", err)
	}
	afterManifest, err := proposedManifest(ctx, root.path, manifest, s.limits, proposal)
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: build manifest: %w", err)
	}
	operationID, err := s.newOperationID()
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	startedAt := s.clock.Now().UTC()
	journal := Journal{
		SchemaVersion: JournalSchemaVersion, OperationID: operationID, Kind: proposal.kind, Phase: JournalPrepared,
		WorkspaceID: id, ExpectedRevision: workspace.Revision, NextRevision: workspace.Revision + 1,
		BeforeDigest: workspace.DraftDigest, AfterDigest: afterManifest.Digest(), Actor: actor,
		Paths: proposalPaths(proposal), StartedAt: startedAt,
	}
	journalBytes, err := journalPayloadSize(journal)
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	additional, err := mutationCapacityBytes(
		int64(len(beforeContent)),
		int64(len(proposal.content)),
		int64(manifestPayloadSize(manifest)),
		int64(manifestPayloadSize(afterManifest)),
		journalBytes,
	)
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", ErrLimitExceeded)
	}
	if ensureBase {
		baseBytes := int64(manifestPayloadSize(baseManifest))
		additional, err = mutationCapacityBytes(additional, baseBytes, baseBytes)
		if err != nil {
			return MutationResult{}, fmt.Errorf("replace workspace file: %w", ErrLimitExceeded)
		}
	}
	if err := s.checkMutationCapacity(ctx, root, workspace, additional); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	if err := s.mutationCheckpoint("before_recovery"); err != nil {
		return MutationResult{}, err
	}
	if err := writeRecovery(ctx, root, journal, beforeContent, manifest, afterManifest, baseManifest, ensureBase); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: prepare recovery: %w", err)
	}
	if err := s.mutationCheckpoint("after_recovery"); err != nil {
		return MutationResult{}, err
	}
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: %w", err)
	}
	draft, err := OpenScopedRoot(root.path + "/draft")
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: open draft: %w", err)
	}
	if err := s.mutationCheckpoint("before_filesystem"); err != nil {
		return MutationResult{}, errors.Join(err, draft.Close())
	}
	applyErr := applyFileProposal(ctx, draft, proposal, s.mutationCheckpoint)
	closeErr := draft.Close()
	if applyErr != nil || closeErr != nil {
		return MutationResult{}, errors.Join(applyErr, wrapServiceError("close draft", closeErr))
	}
	if err := s.mutationCheckpoint("after_filesystem"); err != nil {
		return MutationResult{}, err
	}
	if err := verifyManagedTreeDigest(ctx, root.path, "draft", afterManifest, s.limits, draftManagedFileMode, journal.AfterDigest); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: verify applied files: %w", err)
	}
	journal.Phase = JournalFilesApplied
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return MutationResult{}, err
	}

	next := workspace
	next.DraftDigest = journal.AfterDigest
	next.EntryCount = afterManifest.EntryCount
	next.ManagedBytes = afterManifest.ManagedBytes
	next.Revision = journal.NextRevision
	next.UpdatedAt = startedAt
	next.WorkspaceBytes, err = stableWorkspaceBytes(ctx, root, afterManifest, baseManifest, ensureBase, controlStateFromWorkspace(next))
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: measure final workspace: %w", err)
	}
	change, err := fileWorkspaceChange(workspace, next, journal)
	if err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: build audit: %w", err)
	}
	if err := s.mutationCheckpoint("before_database"); err != nil {
		return MutationResult{}, err
	}
	if err := s.writer.UpdateWorkspace(ctx, change); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file metadata: %w", err)
	}
	if err := s.mutationCheckpoint("after_database"); err != nil {
		return MutationResult{}, err
	}
	journal.Phase = JournalSQLCommitted
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return MutationResult{}, err
	}
	if ensureBase {
		if err := s.mutationCheckpoint("before_control_base_manifest"); err != nil {
			return MutationResult{}, err
		}
		if err := writeManifestAt(ctx, root, controlBaseManifestPath, baseManifest); err != nil {
			return MutationResult{}, fmt.Errorf("replace workspace base manifest: %w", err)
		}
		if err := s.mutationCheckpoint("after_control_base_manifest"); err != nil {
			return MutationResult{}, err
		}
	}
	if err := s.mutationCheckpoint("before_control_manifest"); err != nil {
		return MutationResult{}, err
	}
	if err := WriteControlManifest(ctx, root, afterManifest); err != nil {
		return MutationResult{}, err
	}
	if err := s.mutationCheckpoint("after_control_manifest"); err != nil {
		return MutationResult{}, err
	}
	if err := s.mutationCheckpoint("before_control_state"); err != nil {
		return MutationResult{}, err
	}
	if err := WriteControlState(ctx, root, controlStateFromWorkspace(next)); err != nil {
		return MutationResult{}, err
	}
	if err := s.mutationCheckpoint("after_control_state"); err != nil {
		return MutationResult{}, err
	}
	journal.Phase = JournalControlCommitted
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return MutationResult{}, err
	}
	if err := removeRecovery(ctx, root, journal); err != nil {
		return MutationResult{}, fmt.Errorf("replace workspace file: cleanup recovery: %w", err)
	}
	if err := RemoveJournal(ctx, root); err != nil {
		return MutationResult{}, err
	}
	resultPath := proposalResultPath(proposal)
	if resultPath == "" {
		return MutationResult{Workspace: next}, nil
	}
	resultEntry, ok := afterManifest.Entry(resultPath)
	if !ok {
		return MutationResult{}, fmt.Errorf("mutate workspace file: resulting entry missing: %w", ErrConflict)
	}
	return MutationResult{Workspace: next, Entry: &resultEntry}, nil
}

func mutationCapacityBytes(parts ...int64) (int64, error) {
	var total int64
	for _, part := range parts {
		if part < 0 || part > math.MaxInt64-total {
			return 0, ErrLimitExceeded
		}
		total += part
	}
	return total, nil
}

func (s *Service) openVerifiedWorkspace(
	ctx context.Context,
	id WorkspaceID,
	mode LockMode,
) (Workspace, *ScopedRoot, *WorkspaceLock, Manifest, Manifest, error) {
	if ctx == nil || s == nil {
		return Workspace{}, nil, nil, Manifest{}, Manifest{}, fmt.Errorf("service is unavailable")
	}
	parsedID, err := ParseWorkspaceID(string(id))
	if err != nil || parsedID != id {
		return Workspace{}, nil, nil, Manifest{}, Manifest{}, ErrIdentifierInvalid
	}
	root, err := OpenScopedRoot(s.workspacePath(id))
	if err != nil {
		return Workspace{}, nil, nil, Manifest{}, Manifest{}, err
	}
	lock, err := AcquireWorkspaceLock(ctx, root, mode)
	if err != nil {
		return Workspace{}, nil, nil, Manifest{}, Manifest{}, errors.Join(err, root.Close())
	}
	workspace, manifest, baseManifest, _, err := s.verifiedWorkspaceUnderLock(ctx, id, root, false)
	if err != nil {
		return Workspace{}, nil, nil, Manifest{}, Manifest{}, errors.Join(err, lock.Close(), root.Close())
	}
	return workspace, root, lock, manifest, baseManifest, nil
}

func treeDiffStatuses(base, draft Manifest) map[RelativePath]string {
	baseEntries := make(map[RelativePath]Entry, len(base.Entries))
	for _, entry := range base.Entries {
		if entry.Type == EntryRegular && entry.Class == EntryManagedText {
			baseEntries[entry.Path] = entry
		}
	}
	statuses := make(map[RelativePath]string, len(draft.Entries))
	for _, entry := range draft.Entries {
		if entry.Type != EntryRegular || entry.Class != EntryManagedText {
			continue
		}
		baseEntry, existed := baseEntries[entry.Path]
		switch {
		case !existed:
			statuses[entry.Path] = "created"
		case baseEntry.ContentDigest == entry.ContentDigest:
			statuses[entry.Path] = "unchanged"
		default:
			statuses[entry.Path] = "modified"
		}
	}
	return statuses
}

func (s *Service) verifiedWorkspaceUnderLock(
	ctx context.Context,
	id WorkspaceID,
	root *ScopedRoot,
	mutation bool,
) (Workspace, Manifest, Manifest, bool, error) {
	workspace, err := s.reader.Workspace(ctx, id)
	if err != nil {
		return Workspace{}, Manifest{}, Manifest{}, false, err
	}
	if workspace.State != StateReady {
		return Workspace{}, Manifest{}, Manifest{}, false, ErrConflict
	}
	state, err := ReadControlState(ctx, root)
	if err != nil || state.WorkspaceID != id || state.State != StateReady || state.Revision != workspace.Revision {
		return Workspace{}, Manifest{}, Manifest{}, false, ErrConflict
	}
	if _, err := ReadJournal(ctx, root); err == nil {
		return Workspace{}, Manifest{}, Manifest{}, false, ErrConflict
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Workspace{}, Manifest{}, Manifest{}, false, ErrConflict
	}
	manifest, err := ReadControlManifest(ctx, root, s.limits)
	if err != nil || manifest.Digest() != workspace.DraftDigest || manifest.EntryCount != workspace.EntryCount ||
		manifest.ManagedBytes != workspace.ManagedBytes {
		return Workspace{}, Manifest{}, Manifest{}, false, ErrConflict
	}
	baseManifest, err := readManifestAt(ctx, root, controlBaseManifestPath, s.limits)
	ensureBase := false
	if errors.Is(err, fs.ErrNotExist) && workspace.Revision == 1 {
		baseManifest = manifest
		ensureBase = true
		err = nil
	}
	if err != nil || baseManifest.Digest() != workspace.BaseDigest {
		if mutation {
			_ = s.markNeedsAttention(context.WithoutCancel(ctx), workspace, reasonBaseDigestMismatch, root, true)
		}
		return Workspace{}, Manifest{}, Manifest{}, false, ErrConflict
	}
	if err := verifyManagedTreeDigest(ctx, root.path, "base", baseManifest, s.limits, baseManagedFileMode, workspace.BaseDigest); err != nil {
		if mutation {
			_ = s.markNeedsAttention(context.WithoutCancel(ctx), workspace, reasonBaseDigestMismatch, root, true)
		}
		return Workspace{}, Manifest{}, Manifest{}, false, ErrConflict
	}
	if err := verifyManagedTreeDigest(ctx, root.path, "draft", manifest, s.limits, draftManagedFileMode, workspace.DraftDigest); err != nil {
		if mutation {
			_ = s.markNeedsAttention(context.WithoutCancel(ctx), workspace, reasonDraftDigestMismatch, root, true)
		}
		return Workspace{}, Manifest{}, Manifest{}, false, ErrConflict
	}
	return workspace, manifest, baseManifest, ensureBase, nil
}

func (s *Service) verifyProductionForMutation(ctx context.Context, requestID string, workspace Workspace, root *ScopedRoot) error {
	digestCtx, cancel := context.WithTimeout(ctx, productionDigestDeadline)
	production, err := s.production.ConfigDigest(digestCtx, requestID)
	cancel()
	if err != nil {
		return fmt.Errorf("read production digest: %w", err)
	}
	if err := validateProductionState(production, s.limits); err != nil {
		return err
	}
	if production.Digest == workspace.ProductionDigest {
		return nil
	}
	next := workspace
	next.State = StateStale
	next.StateReasonCode = "PRODUCTION_CHANGED"
	next.Revision++
	next.UpdatedAt = s.clock.Now().UTC()
	change, err := s.reconcileChange(workspace, next, next.StateReasonCode)
	if err != nil {
		return err
	}
	change.Operation.Action = "config.workspace.stale"
	change.Audit.Action = change.Operation.Action
	if err := s.writer.UpdateWorkspace(context.WithoutCancel(ctx), change); err != nil {
		return fmt.Errorf("persist stale workspace: %w", err)
	}
	if err := WriteControlState(context.WithoutCancel(ctx), root, controlStateFromWorkspace(next)); err != nil {
		return err
	}
	return ErrConflict
}

func (s *Service) parseFilePath(raw RelativePath) (RelativePath, error) {
	if s == nil {
		return "", ErrPathInvalid
	}
	path, err := ParseRelativePath(string(raw), s.limits)
	if err != nil || path != raw {
		return "", ErrPathInvalid
	}
	if strings.HasPrefix(string(path), "control/") || strings.HasPrefix(string(path), "base/") ||
		strings.HasPrefix(string(path), "draft/") {
		return "", ErrPathInvalid
	}
	return path, nil
}

func (s *Service) workspacePath(id WorkspaceID) string {
	return s.workspaceRoot + "/" + string(id)
}

func requireWorkspaceETag(ifMatch string, workspace Workspace) error {
	current := workspace.ETag()
	digest, err := ParseStrongETag(ifMatch, draftETagPrefix)
	if err != nil || subtle.ConstantTimeCompare(digest[:], workspace.DraftDigest[:]) != 1 ||
		subtle.ConstantTimeCompare([]byte(ifMatch), []byte(current)) != 1 {
		return &ConflictError{CurrentETag: current}
	}
	return nil
}

func validateManagedContent(path RelativePath, content []byte, limits Limits) error {
	if limits.MaxFileBytes <= 0 || int64(len(content)) > limits.MaxFileBytes {
		return ErrLimitExceeded
	}
	if !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 ||
		NewPolicy().Classify(path, content, false, true) != EntryManagedText {
		return ErrEntryNotManaged
	}
	return nil
}

func verifyManagedTreeDigest(
	ctx context.Context,
	workspacePath, name string,
	manifest Manifest,
	limits Limits,
	mode fs.FileMode,
	want Digest,
) error {
	digest, err := verifyManagedTree(ctx, workspacePath, name, manifest, limits, mode)
	if err != nil || digest != want {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func detectLineEnding(content []byte) string {
	hasLF := false
	hasCRLF := false
	for index, value := range content {
		if value != '\n' {
			continue
		}
		if index > 0 && content[index-1] == '\r' {
			hasCRLF = true
		} else {
			hasLF = true
		}
	}
	switch {
	case hasLF && hasCRLF:
		return "mixed"
	case hasCRLF:
		return "crlf"
	case hasLF:
		return "lf"
	default:
		return "none"
	}
}

type fileProposal struct {
	kind        string
	source      RelativePath
	destination RelativePath
	content     []byte
}

func validateFileProposal(
	ctx context.Context,
	workspacePath string,
	manifest Manifest,
	limits Limits,
	proposal *fileProposal,
) ([]byte, error) {
	if proposal == nil {
		return nil, ErrPathInvalid
	}
	var before []byte
	if proposal.kind != "create" {
		entry, ok := manifest.Entry(proposal.source)
		if !ok || entry.Type != EntryRegular || entry.Class != EntryManagedText {
			return nil, ErrEntryNotManaged
		}
		content, err := readDraftContent(ctx, workspacePath, proposal.source, limits)
		if err != nil {
			return nil, err
		}
		before = content
		if proposal.kind == "copy" || proposal.kind == "rename" {
			proposal.content = slices.Clone(content)
		}
	}
	if proposal.kind == "create" || proposal.kind == "copy" || proposal.kind == "rename" {
		if proposal.destination == proposal.source {
			return nil, fs.ErrExist
		}
		if _, exists := manifest.Entry(proposal.destination); exists {
			return nil, fs.ErrExist
		}
		if err := validateManagedContent(proposal.destination, proposal.content, limits); err != nil {
			return nil, err
		}
		if err := validateDraftParent(ctx, workspacePath, proposal.destination); err != nil {
			return nil, err
		}
	}
	switch proposal.kind {
	case "create", "replace", "copy", "rename", "delete":
	default:
		return nil, ErrPathInvalid
	}
	return before, nil
}

func validateDraftParent(ctx context.Context, workspacePath string, target RelativePath) error {
	parentPath := path.Dir(string(target))
	if parentPath == "." {
		information, err := os.Lstat(workspacePath + "/draft")
		if err != nil {
			return err
		}
		if !information.IsDir() || information.Mode().Perm() != workspaceDirectoryMode {
			return ErrPathInvalid
		}
		return nil
	}
	draft, err := OpenScopedRoot(workspacePath + "/draft")
	if err != nil {
		return err
	}
	information, statErr := draft.Lstat(ctx, RelativePath(parentPath))
	closeErr := draft.Close()
	if statErr != nil || closeErr != nil {
		return errors.Join(statErr, closeErr)
	}
	if !information.IsDir() || information.Mode().Perm() != workspaceDirectoryMode {
		return ErrPathInvalid
	}
	return nil
}

func proposalPaths(proposal fileProposal) []RelativePath {
	switch proposal.kind {
	case "create":
		return []RelativePath{proposal.destination}
	case "copy", "rename":
		return []RelativePath{proposal.source, proposal.destination}
	default:
		return []RelativePath{proposal.source}
	}
}

func proposalResultPath(proposal fileProposal) RelativePath {
	switch proposal.kind {
	case "create", "copy", "rename":
		return proposal.destination
	case "replace":
		return proposal.source
	default:
		return ""
	}
}

func applyFileProposal(ctx context.Context, draft *ScopedRoot, proposal fileProposal, checkpoint func(string) error) error {
	run := func(name string, operation func() error) error {
		if checkpoint != nil {
			if err := checkpoint("before_filesystem_" + name); err != nil {
				return err
			}
		}
		if err := operation(); err != nil {
			return err
		}
		if checkpoint != nil {
			return checkpoint("after_filesystem_" + name)
		}
		return nil
	}
	switch proposal.kind {
	case "create", "copy":
		return run("create_destination", func() error {
			return draft.CreateRegular(ctx, proposal.destination, proposal.content, draftManagedFileMode)
		})
	case "replace":
		return run("replace_source", func() error {
			return draft.AtomicReplace(ctx, proposal.source, proposal.content, draftManagedFileMode)
		})
	case "rename":
		if err := run("create_destination", func() error {
			return draft.CreateRegular(ctx, proposal.destination, proposal.content, draftManagedFileMode)
		}); err != nil {
			return err
		}
		return run("remove_source", func() error { return draft.RemoveRegular(ctx, proposal.source) })
	case "delete":
		return run("remove_source", func() error { return draft.RemoveRegular(ctx, proposal.source) })
	default:
		return ErrPathInvalid
	}
}

func proposedManifest(
	ctx context.Context,
	workspacePath string,
	current Manifest,
	limits Limits,
	proposal fileProposal,
) (Manifest, error) {
	contents := make(map[RelativePath][]byte)
	for _, entry := range current.Entries {
		if entry.Class != EntryManagedText {
			continue
		}
		content, err := readDraftContent(ctx, workspacePath, entry.Path, limits)
		if err != nil {
			return Manifest{}, err
		}
		contents[entry.Path] = content
	}
	entries := slices.Clone(current.Entries)
	sourceIndex := slices.IndexFunc(entries, func(entry Entry) bool { return entry.Path == proposal.source })
	switch proposal.kind {
	case "create":
		entries = append(entries, Entry{
			Path: proposal.destination, Type: EntryRegular, Class: EntryManagedText,
			Mode: draftManagedFileMode, Size: int64(len(proposal.content)),
			ContentDigest: Digest(sha256.Sum256(proposal.content)),
		})
		contents[proposal.destination] = slices.Clone(proposal.content)
	case "replace":
		if sourceIndex < 0 || entries[sourceIndex].Class != EntryManagedText {
			return Manifest{}, ErrEntryNotManaged
		}
		entries[sourceIndex].Size = int64(len(proposal.content))
		entries[sourceIndex].ContentDigest = Digest(sha256.Sum256(proposal.content))
		contents[proposal.source] = slices.Clone(proposal.content)
	case "copy":
		if sourceIndex < 0 || entries[sourceIndex].Class != EntryManagedText {
			return Manifest{}, ErrEntryNotManaged
		}
		copied := entries[sourceIndex]
		copied.Path = proposal.destination
		copied.Mode = draftManagedFileMode
		entries = append(entries, copied)
		contents[proposal.destination] = slices.Clone(proposal.content)
	case "rename":
		if sourceIndex < 0 || entries[sourceIndex].Class != EntryManagedText {
			return Manifest{}, ErrEntryNotManaged
		}
		renamed := entries[sourceIndex]
		renamed.Path = proposal.destination
		renamed.Mode = draftManagedFileMode
		entries[sourceIndex] = renamed
		delete(contents, proposal.source)
		contents[proposal.destination] = slices.Clone(proposal.content)
	case "delete":
		if sourceIndex < 0 || entries[sourceIndex].Class != EntryManagedText {
			return Manifest{}, ErrEntryNotManaged
		}
		entries = slices.Delete(entries, sourceIndex, sourceIndex+1)
		delete(contents, proposal.source)
	default:
		return Manifest{}, ErrPathInvalid
	}
	return manifestFromContents(ctx, entries, contents, limits)
}

func manifestFromContents(
	ctx context.Context,
	entries []Entry,
	contents map[RelativePath][]byte,
	limits Limits,
) (Manifest, error) {
	raw := make([]RawEntry, len(entries))
	for index, entry := range entries {
		raw[index] = RawEntry{
			Path: entry.Path, Type: entry.Type, Mode: entry.Mode, Size: entry.Size,
			SafeLinkTarget: entry.SafeLinkTarget, LinkClass: entry.Class,
		}
	}
	graph, included, sensitive, err := ExpandIncludeGraph(ctx, "nginx.conf", raw, func(_ context.Context, path RelativePath) ([]byte, error) {
		return contents[path], nil
	}, limits)
	if err != nil {
		return Manifest{}, err
	}
	var managedBytes int64
	for index := range entries {
		entry := &entries[index]
		content, managed := contents[entry.Path]
		if !managed {
			continue
		}
		_, isIncluded := included[entry.Path]
		_, isSensitive := sensitive[entry.Path]
		class := NewPolicy().Classify(entry.Path, content, isSensitive, isIncluded)
		if class != EntryManagedText {
			return Manifest{}, ErrEntryNotManaged
		}
		entry.Class = class
		entry.Size = int64(len(content))
		entry.ContentDigest = Digest(sha256.Sum256(content))
		if entry.Size > math.MaxInt64-managedBytes {
			return Manifest{}, ErrLimitExceeded
		}
		managedBytes += entry.Size
	}
	slices.SortFunc(entries, compareEntries)
	dependencies := slices.Clone(graph.Edges)
	slices.SortFunc(dependencies, compareDependencies)
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, PolicyVersion: NewPolicy().Version(), Entries: entries,
		Dependencies: dependencies, EntryCount: len(entries), ManagedBytes: managedBytes,
	}
	if err := manifest.Validate(limits); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func readDraftContent(ctx context.Context, workspacePath string, path RelativePath, limits Limits) ([]byte, error) {
	draft, err := OpenScopedRoot(workspacePath + "/draft")
	if err != nil {
		return nil, err
	}
	content, info, readErr := draft.ReadRegular(ctx, path, limits.MaxFileBytes)
	closeErr := draft.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if info.Mode().Perm() != draftManagedFileMode || int64(len(content)) != info.Size() {
		return nil, ErrConflict
	}
	return content, nil
}

func fileWorkspaceChange(before, next Workspace, journal Journal) (WorkspaceChange, error) {
	beforeDigest := journal.BeforeDigest
	afterDigest := journal.AfterDigest
	operation := OperationRecord{
		ID: journal.OperationID, ObjectType: workspaceObjectType, ObjectID: string(journal.WorkspaceID),
		Action: "config.file." + journal.Kind, BeforeDigest: &beforeDigest, AfterDigest: &afterDigest,
		Result: operationResultSuccess, RequestID: journal.Actor.RequestID, OccurredAt: journal.StartedAt,
	}
	details, err := json.Marshal(struct {
		PathCount int            `json:"path_count"`
		Paths     []RelativePath `json:"paths"`
		Before    string         `json:"before_digest"`
		After     string         `json:"after_digest"`
	}{PathCount: len(journal.Paths), Paths: slices.Clone(journal.Paths), Before: beforeDigest.String(), After: afterDigest.String()})
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

func recoveryPath(journal Journal, name string) RelativePath {
	return RelativePath("control/recovery/" + journal.OperationID + "/" + name)
}

func writeRecovery(
	ctx context.Context,
	root *ScopedRoot,
	journal Journal,
	beforeContent []byte,
	beforeManifest, afterManifest, baseManifest Manifest,
	includeBase bool,
) error {
	directory := RelativePath("control/recovery/" + journal.OperationID)
	if err := root.EnsureDirectory(ctx, directory, workspaceDirectoryMode); err != nil {
		return err
	}
	if journal.Kind == "replace" || journal.Kind == "rename" || journal.Kind == "delete" {
		if err := root.CreateRegular(ctx, recoveryPath(journal, "before.bin"), beforeContent, controlFileMode); err != nil {
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

func removeRecovery(ctx context.Context, root *ScopedRoot, journal Journal) error {
	names := []string{"before.bin", "manifest.bin", "before-manifest.bin", "base-manifest.bin"}
	if journal.Kind == "replace_batch" {
		for index := range journal.Paths {
			names = append(names, replacementRecoveryName(index))
		}
	}
	for _, name := range names {
		if err := root.RemoveRegular(ctx, recoveryPath(journal, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	directory := RelativePath("control/recovery/" + journal.OperationID)
	if err := removeEmptyDirectory(ctx, root, directory); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := removeEmptyDirectory(ctx, root, "control/recovery"); err != nil &&
		!errors.Is(err, fs.ErrNotExist) && !errors.Is(err, unix.ENOTEMPTY) {
		return err
	}
	return nil
}

func removeEmptyDirectory(ctx context.Context, root *ScopedRoot, path RelativePath) (returnErr error) {
	parent, basename, err := root.openParent(ctx, path)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close recovery directory parent", unix.Close(parent)))
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, basename, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	entryType, mode := entryTypeAndMode(uint64(stat.Mode))
	if entryType != EntryDirectory || mode.Perm() != workspaceDirectoryMode {
		return ErrPathInvalid
	}
	if err := unix.Unlinkat(parent, basename, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return root.fsync(parent)
}

func writeManifestAt(ctx context.Context, root *ScopedRoot, path RelativePath, manifest Manifest) error {
	payload, err := manifest.MarshalBinary()
	if err != nil {
		return err
	}
	return root.AtomicReplace(ctx, path, payload, controlFileMode)
}

func stableWorkspaceBytes(
	ctx context.Context,
	root *ScopedRoot,
	manifest, baseManifest Manifest,
	includeBase bool,
	state ControlState,
) (int64, error) {
	entries, err := root.Walk(ctx, workspaceTraversalLimit(DefaultLimits()))
	if err != nil {
		return 0, err
	}
	manifestPayload, err := manifest.MarshalBinary()
	if err != nil {
		return 0, err
	}
	statePayload, err := marshalControlState(state)
	if err != nil {
		return 0, err
	}
	basePayload, err := baseManifest.MarshalBinary()
	if err != nil {
		return 0, err
	}
	var total int64
	baseSeen := false
	for _, entry := range entries {
		if entry.Type != EntryRegular || entry.Path == journalPath || strings.HasPrefix(string(entry.Path), "control/recovery/") {
			continue
		}
		size := entry.Size
		switch entry.Path {
		case controlManifestPath:
			size = int64(len(manifestPayload))
		case controlStatePath:
			size = int64(len(statePayload))
		case controlBaseManifestPath:
			baseSeen = true
			size = int64(len(basePayload))
		case controlPreparedManifestPath:
			// Legacy creation recovery evidence retains its measured size until reconciliation removes it.
		default:
			// Existing non-control regular files retain their measured size.
		}
		if size < 0 || total > math.MaxInt64-size {
			return 0, ErrLimitExceeded
		}
		total += size
	}
	if includeBase && !baseSeen {
		if total > math.MaxInt64-int64(len(basePayload)) {
			return 0, ErrLimitExceeded
		}
		total += int64(len(basePayload))
	}
	return total, nil
}

func (s *Service) checkMutationCapacity(ctx context.Context, root *ScopedRoot, workspace Workspace, additional int64) error {
	_, registered, err := s.reader.WorkspaceUsage(ctx)
	if err != nil {
		return err
	}
	current, err := workspaceLogicalBytes(ctx, root, s.limits)
	if err != nil {
		return err
	}
	if registered < workspace.WorkspaceBytes || current < 0 || additional < 0 ||
		exceedsCapacity(registered-workspace.WorkspaceBytes, current+additional, s.limits.MaxWorkspaceBytes) {
		return ErrLimitExceeded
	}
	return nil
}

func (s *Service) mutationCheckpoint(name string) error {
	if s == nil || s.mutationHook == nil {
		return nil
	}
	return s.mutationHook(name)
}

func (s *Service) writeMutationPhase(ctx context.Context, root *ScopedRoot, journal Journal) error {
	if err := s.mutationCheckpoint("before_phase_" + string(journal.Phase)); err != nil {
		return err
	}
	if err := WriteJournal(ctx, root, journal); err != nil {
		return err
	}
	return s.mutationCheckpoint("after_phase_" + string(journal.Phase))
}

func (s *Service) reconcileFileJournal(ctx context.Context, workspace Workspace, root *ScopedRoot, journal Journal) error {
	if journal.WorkspaceID != workspace.ID || journal.Kind == "workspace_create" || journal.Kind == "workspace_delete" ||
		journal.ExpectedRevision+1 != journal.NextRevision {
		return s.markNeedsAttention(ctx, workspace, reasonJournalInvalid, root, true)
	}
	beforeManifest, err := readManifestAt(ctx, root, recoveryPath(journal, "before-manifest.bin"), s.limits)
	if err != nil || beforeManifest.Digest() != journal.BeforeDigest {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
	}
	afterManifest, err := readManifestAt(ctx, root, recoveryPath(journal, "manifest.bin"), s.limits)
	if err != nil || afterManifest.Digest() != journal.AfterDigest {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
	}
	if journal.Kind == "replace_batch" {
		return s.reconcileReplacementJournal(ctx, workspace, root, journal, beforeManifest, afterManifest)
	}
	beforeContent, err := validateRecoveryContent(ctx, root, journal, beforeManifest, s.limits)
	if err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
	}
	baseManifest, includeBase, err := s.recoveryBaseManifest(ctx, root, workspace, journal)
	if err != nil {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
	}

	actualBefore := managedTreeMatches(ctx, root.path, beforeManifest, s.limits, journal.BeforeDigest)
	actualAfter := managedTreeMatches(ctx, root.path, afterManifest, s.limits, journal.AfterDigest)
	if !actualBefore && !actualAfter {
		return s.markNeedsAttention(ctx, workspace, reasonRecoveryStateMismatch, root, true)
	}

	switch workspace.Revision {
	case journal.ExpectedRevision:
		if workspace.DraftDigest != journal.BeforeDigest || journal.Phase == JournalSQLCommitted ||
			journal.Phase == JournalControlCommitted {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryStateMismatch, root, true)
		}
		if actualAfter && !actualBefore {
			if err := rollbackFileProposal(ctx, root.path, journal, beforeContent); err != nil {
				return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
			}
			if !managedTreeMatches(ctx, root.path, beforeManifest, s.limits, journal.BeforeDigest) {
				return s.markNeedsAttention(ctx, workspace, reasonRecoveryStateMismatch, root, true)
			}
		}
		if err := WriteControlManifest(ctx, root, beforeManifest); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
		}
		if err := WriteControlState(ctx, root, controlStateFromWorkspace(workspace)); err != nil {
			return s.markNeedsAttention(ctx, workspace, reasonRecoveryInvalid, root, true)
		}
		return cleanupMutationEvidence(ctx, root, journal)
	case journal.NextRevision:
		if workspace.DraftDigest != journal.AfterDigest || !actualAfter || journal.Phase == JournalPrepared {
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

func (s *Service) recoveryBaseManifest(
	ctx context.Context,
	root *ScopedRoot,
	workspace Workspace,
	journal Journal,
) (Manifest, bool, error) {
	manifest, err := readManifestAt(ctx, root, controlBaseManifestPath, s.limits)
	if err == nil {
		if manifest.Digest() != workspace.BaseDigest {
			return Manifest{}, false, ErrConflict
		}
		return manifest, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) || journal.ExpectedRevision != 1 {
		return Manifest{}, false, err
	}
	manifest, err = readManifestAt(ctx, root, recoveryPath(journal, "base-manifest.bin"), s.limits)
	if err != nil || manifest.Digest() != workspace.BaseDigest {
		return Manifest{}, false, errors.Join(err, ErrConflict)
	}
	return manifest, true, nil
}

func validateRecoveryContent(
	ctx context.Context,
	root *ScopedRoot,
	journal Journal,
	beforeManifest Manifest,
	limits Limits,
) ([]byte, error) {
	requiresContent := journal.Kind == "replace" || journal.Kind == "rename" || journal.Kind == "delete"
	content, info, err := root.ReadRegular(ctx, recoveryPath(journal, "before.bin"), limits.MaxFileBytes)
	if !requiresContent {
		if err == nil {
			return nil, ErrConflict
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if err != nil || info.Mode().Perm() != controlFileMode {
		return nil, errors.Join(err, ErrPathInvalid)
	}
	entry, ok := beforeManifest.Entry(journal.Paths[0])
	if !ok || entry.Class != EntryManagedText || entry.Size != int64(len(content)) ||
		entry.ContentDigest != Digest(sha256.Sum256(content)) {
		return nil, ErrConflict
	}
	return content, nil
}

func managedTreeMatches(ctx context.Context, workspacePath string, manifest Manifest, limits Limits, want Digest) bool {
	digest, err := verifyManagedTree(ctx, workspacePath, "draft", manifest, limits, draftManagedFileMode)
	return err == nil && digest == want
}

func rollbackFileProposal(
	ctx context.Context,
	workspacePath string,
	journal Journal,
	beforeContent []byte,
) (returnErr error) {
	draft, err := OpenScopedRoot(workspacePath + "/draft")
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close rollback draft", draft.Close()))
	}()
	source := journal.Paths[0]
	switch journal.Kind {
	case "create":
		return removeRegularIfPresent(ctx, draft, source)
	case "replace":
		return draft.AtomicReplace(ctx, source, beforeContent, draftManagedFileMode)
	case "copy":
		return removeRegularIfPresent(ctx, draft, journal.Paths[1])
	case "rename":
		if err := removeRegularIfPresent(ctx, draft, journal.Paths[1]); err != nil {
			return err
		}
		return restoreRegular(ctx, draft, source, beforeContent)
	case "delete":
		return restoreRegular(ctx, draft, source, beforeContent)
	default:
		return ErrPathInvalid
	}
}

func removeRegularIfPresent(ctx context.Context, root *ScopedRoot, path RelativePath) error {
	if _, err := root.Lstat(ctx, path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return root.RemoveRegular(ctx, path)
}

func restoreRegular(ctx context.Context, root *ScopedRoot, path RelativePath, content []byte) error {
	existing, info, err := root.ReadRegular(ctx, path, int64(len(content)))
	if err == nil {
		if info.Mode().Perm() != draftManagedFileMode || !slices.Equal(existing, content) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return root.CreateRegular(ctx, path, content, draftManagedFileMode)
}

func cleanupMutationEvidence(ctx context.Context, root *ScopedRoot, journal Journal) error {
	if err := removeRecovery(ctx, root, journal); err != nil {
		return err
	}
	return RemoveJournal(ctx, root)
}

func cleanupOrphanRecovery(ctx context.Context, root *ScopedRoot) error {
	entries, err := root.Walk(ctx, workspaceTraversalLimit(DefaultLimits()))
	if err != nil {
		return err
	}
	regular := make([]RelativePath, 0)
	directories := make([]RelativePath, 0)
	for _, entry := range entries {
		raw, ok := strings.CutPrefix(string(entry.Path), "control/recovery/")
		if !ok {
			continue
		}
		components := strings.Split(raw, "/")
		if len(components) == 0 || !validOpaqueID(components[0]) {
			return ErrPathInvalid
		}
		switch len(components) {
		case 1:
			if entry.Type != EntryDirectory || entry.Mode.Perm() != workspaceDirectoryMode {
				return ErrPathInvalid
			}
			directories = append(directories, entry.Path)
		case 2:
			if entry.Type != EntryRegular || entry.Mode.Perm() != controlFileMode ||
				!isRecoveryEvidenceName(components[1]) {
				return ErrPathInvalid
			}
			regular = append(regular, entry.Path)
		default:
			return ErrPathInvalid
		}
	}
	for _, path := range regular {
		if err := root.RemoveRegular(ctx, path); err != nil {
			return err
		}
	}
	slices.Reverse(directories)
	for _, path := range directories {
		if err := removeEmptyDirectory(ctx, root, path); err != nil {
			return err
		}
	}
	if len(regular) != 0 || len(directories) != 0 {
		if err := removeEmptyDirectory(ctx, root, "control/recovery"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Keep the fixed recovery timestamp representation tied to the journal contract.
var _ = time.RFC3339Nano
var _ = os.ErrNotExist
