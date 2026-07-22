/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"
	"unicode"

	"golang.org/x/sys/unix"
)

const (
	productionDigestDeadline   = 15 * time.Second
	productionSnapshotDeadline = 60 * time.Second
	workspaceDirectoryMode     = fs.FileMode(0o700)
	baseManagedFileMode        = fs.FileMode(0o400)
	draftManagedFileMode       = fs.FileMode(0o600)
	workspaceObjectType        = "workspace"
	operationResultSuccess     = "success"
)

// Clock supplies deterministic UTC lifecycle timestamps.
type Clock interface {
	Now() time.Time
}

// Dependencies contains the exact external capabilities consumed by Service.
type Dependencies struct {
	WorkspaceRoot string
	Production    ProductionReader
	Reader        WorkspaceReader
	Writer        WorkspaceWriter
	Groups        GroupRepository
	Attention     AttentionReader
	Clock         Clock
	Random        io.Reader
	Limits        Limits
}

// Service owns the recoverable filesystem and repository workspace lifecycle.
type Service struct {
	workspaceRoot string
	production    ProductionReader
	reader        WorkspaceReader
	writer        WorkspaceWriter
	groups        GroupRepository
	attention     AttentionReader
	clock         Clock
	random        io.Reader
	limits        Limits
	mutationHook  func(string) error
}

// NewService validates and retains the explicit configuration lifecycle dependencies.
func NewService(dependencies Dependencies) (*Service, error) {
	if dependencies.Production == nil || dependencies.Reader == nil || dependencies.Writer == nil ||
		dependencies.Groups == nil || dependencies.Attention == nil ||
		dependencies.Clock == nil || dependencies.Random == nil {
		return nil, fmt.Errorf("create config service: dependencies are required")
	}
	if dependencies.WorkspaceRoot == "" || !filepath.IsAbs(dependencies.WorkspaceRoot) ||
		filepath.Clean(dependencies.WorkspaceRoot) != dependencies.WorkspaceRoot {
		return nil, fmt.Errorf("create config service: workspace root: %w", ErrPathInvalid)
	}
	information, err := os.Lstat(dependencies.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("create config service: inspect workspace root: %w", err)
	}
	if information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() || information.Mode().Perm() != workspaceDirectoryMode {
		return nil, fmt.Errorf("create config service: workspace root: %w", ErrPathInvalid)
	}
	if !validServiceLimits(dependencies.Limits) {
		return nil, fmt.Errorf("create config service: limits: %w", ErrLimitExceeded)
	}
	return &Service{
		workspaceRoot: dependencies.WorkspaceRoot,
		production:    dependencies.Production,
		reader:        dependencies.Reader,
		writer:        dependencies.Writer,
		groups:        dependencies.Groups,
		attention:     dependencies.Attention,
		clock:         dependencies.Clock,
		random:        dependencies.Random,
		limits:        dependencies.Limits,
	}, nil
}

// ETag returns the immutable strong tag for the workspace's last known draft.
func (w Workspace) ETag() string {
	return DraftETag(w.DraftDigest)
}

// Create builds and independently verifies an immutable base and mutable draft workspace.
func (s *Service) Create(ctx context.Context, actor Actor, name string) (_ Workspace, returnErr error) {
	if ctx == nil || s == nil {
		return Workspace{}, fmt.Errorf("create workspace: service is unavailable")
	}
	displayName, err := ValidateDisplayName(name)
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	if err := validateActor(actor); err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	if IsSystemWorkspaceName(displayName) != actor.System {
		return Workspace{}, fmt.Errorf("create workspace: %w", ErrSystemWorkspaceAccess)
	}
	if err := s.requireResolvedAttention(ctx); err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	capacityLock, err := acquireWorkspaceRootLock(ctx, s.workspaceRoot)
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: acquire capacity lock: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("release capacity lock", capacityLock.Close()))
	}()

	digestCtx, cancelDigest := context.WithTimeout(ctx, productionDigestDeadline)
	production, err := s.production.ConfigDigest(digestCtx, actor.RequestID)
	cancelDigest()
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: read production usage: %w", err)
	}
	if err := validateProductionState(production, s.limits); err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	registeredCount, registeredBytes, err := s.reader.WorkspaceUsage(ctx)
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: read capacity: %w", err)
	}
	reservation, err := workspaceReservation(production.ManagedBytes, s.limits)
	if err != nil || registeredCount >= s.limits.MaxWorkspaces || exceedsCapacity(registeredBytes, reservation, s.limits.MaxWorkspaceBytes) {
		return Workspace{}, fmt.Errorf("create workspace: reserve capacity: %w", ErrLimitExceeded)
	}

	id, err := NewWorkspaceID(s.random)
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	operationID, err := s.newOperationID()
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	workspacePath := filepath.Join(s.workspaceRoot, string(id))
	if err := os.Mkdir(workspacePath, workspaceDirectoryMode); err != nil {
		return Workspace{}, fmt.Errorf("create workspace directory: %w", err)
	}
	owned := true
	root, err := OpenScopedRoot(workspacePath)
	if err != nil {
		cleanupErr := removeOwnedWorkspace(context.WithoutCancel(ctx), workspacePath, nil, s.limits)
		return Workspace{}, errors.Join(fmt.Errorf("open created workspace: %w", err), cleanupErr)
	}
	defer func() {
		if root != nil {
			returnErr = errors.Join(returnErr, wrapServiceError("close workspace", root.Close()))
		}
	}()
	cleanupUnregistered := func(primary error) error {
		if !owned {
			return primary
		}
		cleanupErr := removeOwnedWorkspace(context.WithoutCancel(ctx), workspacePath, root, s.limits)
		root = nil
		return errors.Join(primary, wrapServiceError("clean unregistered workspace", cleanupErr))
	}

	if err := root.EnsureDirectory(ctx, "control", workspaceDirectoryMode); err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace control: %w", err))
	}
	createdAt := s.clock.Now().UTC()
	preparing := ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: id, State: StatePreparing,
		Revision: 1, UpdatedAt: createdAt,
	}
	if err := WriteControlState(ctx, root, preparing); err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	lock, err := AcquireWorkspaceLock(ctx, root, LockExclusive)
	if err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("release workspace lock", lock.Close()))
	}()

	if err := s.checkStagedCapacity(ctx, root, registeredBytes, reservation); err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	snapshotCtx, cancelSnapshot := context.WithTimeout(ctx, productionSnapshotDeadline)
	snapshot, err := s.production.ConfigSnapshot(snapshotCtx, actor.RequestID, id)
	cancelSnapshot()
	if err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace: snapshot production: %w", err))
	}
	if err := validateSnapshot(snapshot, production, s.limits); err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace: %w", err))
	}
	if err := s.checkStagedCapacity(ctx, root, registeredBytes, reservation); err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	baseDigest, err := verifyManagedTree(ctx, workspacePath, "base", snapshot.Manifest, s.limits, baseManagedFileMode)
	if err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace: validate base: %w", err))
	}
	if baseDigest != snapshot.BaseDigest {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace: validate base: %w", ErrSnapshotChanged))
	}
	if err := os.Mkdir(filepath.Join(workspacePath, "draft"), workspaceDirectoryMode); err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace draft: %w", err))
	}
	if err := copyManagedTree(ctx, workspacePath, snapshot.Manifest, s.limits, func(additional int64) error {
		return s.checkAdditionalCapacity(ctx, root, registeredBytes, reservation, additional)
	}); err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace: copy draft: %w", err))
	}
	if err := verifyWorkspaceDirectories(workspacePath); err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace: %w", err))
	}
	draftDigest, err := verifyManagedTree(ctx, workspacePath, "draft", snapshot.Manifest, s.limits, draftManagedFileMode)
	if err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace: validate draft: %w", err))
	}
	if err := s.checkAdditionalCapacity(ctx, root, registeredBytes, reservation, int64(manifestPayloadSize(snapshot.Manifest))); err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	if err := writePreparedControlManifest(ctx, root, snapshot.Manifest); err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	if err := s.checkStagedCapacity(ctx, root, registeredBytes, reservation); err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}

	readyState := ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: id, State: StateReady,
		Revision: 1, UpdatedAt: createdAt,
	}
	readyPayload, err := marshalControlState(readyState)
	if err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	stagedBytes, err := workspaceLogicalBytes(ctx, root, s.limits)
	if err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	preparingPayload, err := marshalControlState(preparing)
	if err != nil {
		return Workspace{}, cleanupUnregistered(err)
	}
	finalBytes := stagedBytes - int64(len(preparingPayload)) + int64(len(readyPayload))
	if finalBytes < 0 || exceedsCapacity(registeredBytes, finalBytes, s.limits.MaxWorkspaceBytes) {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace: final capacity: %w", ErrLimitExceeded))
	}
	workspace := Workspace{
		ID: id, Name: displayName, State: StateReady,
		ProductionDigest: snapshot.ProductionDigest, BaseDigest: snapshot.BaseDigest, DraftDigest: draftDigest,
		EntryCount: snapshot.Manifest.EntryCount, ManagedBytes: snapshot.Manifest.ManagedBytes,
		WorkspaceBytes: finalBytes, Revision: 1, CreatedBy: actor.UserID,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	creation := workspaceCreation(workspace, actor, operationID)
	if err := s.writer.CreateWorkspace(ctx, creation); err != nil {
		return Workspace{}, cleanupUnregistered(fmt.Errorf("create workspace metadata: %w", err))
	}
	owned = false
	if err := s.checkAdditionalCapacity(ctx, root, registeredBytes, reservation, int64(manifestPayloadSize(snapshot.Manifest))); err != nil {
		return Workspace{}, err
	}
	if err := WriteControlManifest(ctx, root, snapshot.Manifest); err != nil {
		return Workspace{}, err
	}
	if err := s.checkAdditionalCapacity(ctx, root, registeredBytes, reservation, int64(len(readyPayload))); err != nil {
		return Workspace{}, err
	}
	if err := WriteControlState(ctx, root, readyState); err != nil {
		return Workspace{}, err
	}
	if err := root.RemoveRegular(ctx, controlPreparedManifestPath); err != nil {
		return Workspace{}, fmt.Errorf("remove prepared workspace manifest: %w", err)
	}

	persisted, err := s.reader.Workspace(ctx, id)
	if err != nil {
		return Workspace{}, fmt.Errorf("reread created workspace: %w", err)
	}
	control, err := ReadControlState(ctx, root)
	if err != nil {
		return Workspace{}, err
	}
	manifest, err := ReadControlManifest(ctx, root, s.limits)
	if err != nil {
		return Workspace{}, err
	}
	if persisted != workspace || control.State != StateReady || control.WorkspaceID != id ||
		control.Revision != persisted.Revision || manifest.Digest() != persisted.DraftDigest {
		return Workspace{}, fmt.Errorf("reread created workspace: %w", ErrConflict)
	}
	return persisted, nil
}

func (s *Service) requireResolvedAttention(ctx context.Context) error {
	open, err := s.attention.HasOpenAttentionCases(ctx)
	if err != nil {
		return fmt.Errorf("read open attention cases: %w", err)
	}
	if open {
		return ErrAttentionUnresolved
	}
	return nil
}

// List returns the stable repository order after refreshing only ready workspaces.
func (s *Service) List(ctx context.Context) ([]Workspace, error) {
	if ctx == nil || s == nil {
		return nil, fmt.Errorf("list workspaces: service is unavailable")
	}
	workspaces, err := s.reader.ListWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	for index, workspace := range workspaces {
		if workspace.State != StateReady {
			continue
		}
		refreshed, err := s.refreshReady(ctx, workspace)
		if err != nil {
			return nil, fmt.Errorf("list workspaces: refresh %s: %w", workspace.ID, err)
		}
		workspaces[index] = refreshed
	}
	return workspaces, nil
}

// Get returns one workspace after refreshing it only when it is ready.
func (s *Service) Get(ctx context.Context, id WorkspaceID) (Workspace, error) {
	if ctx == nil || s == nil {
		return Workspace{}, fmt.Errorf("get workspace: service is unavailable")
	}
	if _, err := ParseWorkspaceID(string(id)); err != nil {
		return Workspace{}, fmt.Errorf("get workspace: %w", err)
	}
	workspace, err := s.reader.Workspace(ctx, id)
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace: %w", err)
	}
	if workspace.State != StateReady {
		return workspace, nil
	}
	refreshed, err := s.refreshReady(ctx, workspace)
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace: refresh: %w", err)
	}
	return refreshed, nil
}

// Delete durably records deletion intent before removing repository metadata and owned bytes.
func (s *Service) Delete(
	ctx context.Context,
	actor Actor,
	id WorkspaceID,
	ifMatch string,
	confirmName string,
) (returnErr error) {
	if ctx == nil || s == nil {
		return fmt.Errorf("delete workspace: service is unavailable")
	}
	if _, err := ParseWorkspaceID(string(id)); err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	if err := validateActor(actor); err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	workspace, err := s.reader.Workspace(ctx, id)
	if err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	if err := requireWorkspaceActor(workspace, actor); err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	wantETag := workspace.ETag()
	if subtle.ConstantTimeCompare([]byte(ifMatch), []byte(wantETag)) != 1 || confirmName != workspace.Name {
		return fmt.Errorf("delete workspace: validate confirmation: %w", ErrConflict)
	}
	operationID, err := s.newOperationID()
	if err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	workspacePath := filepath.Join(s.workspaceRoot, string(id))
	root, err := OpenScopedRoot(workspacePath)
	if err != nil {
		return fmt.Errorf("delete workspace: open tree: %w", err)
	}
	defer func() {
		if root != nil {
			returnErr = errors.Join(returnErr, wrapServiceError("close deleted workspace", root.Close()))
		}
	}()
	lock, err := AcquireWorkspaceLock(ctx, root, LockExclusive)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("release deleted workspace lock", lock.Close()))
	}()

	journal := Journal{
		SchemaVersion: JournalSchemaVersion, OperationID: operationID, Kind: "workspace_delete", Phase: JournalPrepared,
		WorkspaceID: id, ExpectedRevision: workspace.Revision, NextRevision: workspace.Revision + 1,
		BeforeDigest: workspace.DraftDigest, AfterDigest: workspace.DraftDigest, Actor: actor,
		StartedAt: s.clock.Now().UTC(),
	}
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return err
	}
	tombstonePath := filepath.Join(s.workspaceRoot, deleteTombstoneName(id, operationID))
	if err := s.mutationCheckpoint("before_filesystem"); err != nil {
		return err
	}
	if err := renameOwnedWorkspace(root, workspacePath, tombstonePath); err != nil {
		return fmt.Errorf("tombstone deleted workspace: %w", err)
	}
	if err := s.mutationCheckpoint("after_filesystem"); err != nil {
		return err
	}
	journal.Phase = JournalFilesApplied
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return err
	}
	if err := s.mutationCheckpoint("before_database"); err != nil {
		return err
	}
	if err := s.writer.DeleteWorkspace(ctx, workspaceDeletion(workspace, journal)); err != nil {
		rollbackErr := renameOwnedWorkspace(root, tombstonePath, workspacePath)
		if rollbackErr == nil {
			rollbackErr = RemoveJournal(context.WithoutCancel(ctx), root)
		}
		return errors.Join(
			fmt.Errorf("delete workspace metadata: %w", err),
			wrapServiceError("restore workspace after metadata failure", rollbackErr),
		)
	}
	if err := s.mutationCheckpoint("after_database"); err != nil {
		return err
	}
	journal.Phase = JournalSQLCommitted
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return err
	}
	journal.Phase = JournalControlCommitted
	if err := s.writeMutationPhase(ctx, root, journal); err != nil {
		return err
	}
	if err := s.mutationCheckpoint("before_cleanup"); err != nil {
		return err
	}
	if err := removeOwnedWorkspace(context.WithoutCancel(ctx), tombstonePath, root, s.limits); err != nil {
		root = nil
		return fmt.Errorf("remove deleted workspace: %w", err)
	}
	root = nil
	return s.mutationCheckpoint("after_cleanup")
}

func (s *Service) refreshReady(ctx context.Context, workspace Workspace) (Workspace, error) {
	correlationID, err := s.newOperationID()
	if err != nil {
		return Workspace{}, err
	}
	digestCtx, cancel := context.WithTimeout(ctx, productionDigestDeadline)
	production, err := s.production.ConfigDigest(digestCtx, correlationID)
	cancel()
	if err != nil {
		return Workspace{}, fmt.Errorf("read production digest: %w", err)
	}
	if err := validateProductionState(production, s.limits); err != nil {
		return Workspace{}, err
	}
	if production.Digest == workspace.ProductionDigest {
		return workspace, nil
	}
	next := workspace
	next.State = StateStale
	next.StateReasonCode = "PRODUCTION_CHANGED"
	next.Revision++
	next.UpdatedAt = s.clock.Now().UTC()
	before := workspace.DraftDigest
	after := next.DraftDigest
	operation := OperationRecord{
		ID: correlationID, ObjectType: workspaceObjectType, ObjectID: string(workspace.ID),
		Action: "config.workspace.stale", BeforeDigest: &before, AfterDigest: &after,
		Result: operationResultSuccess, RequestID: correlationID, OccurredAt: next.UpdatedAt,
	}
	details, _ := json.Marshal(struct {
		ReasonCode string `json:"reason_code"`
	}{ReasonCode: next.StateReasonCode})
	change := WorkspaceChange{
		ExpectedRevision: workspace.Revision,
		Next:             next,
		Operation:        operation,
		Audit: AuditEvent{
			OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: workspace.CreatedBy,
			Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		},
	}
	if err := s.writer.UpdateWorkspace(ctx, change); err != nil {
		return Workspace{}, fmt.Errorf("persist stale workspace: %w", err)
	}
	root, err := OpenScopedRoot(filepath.Join(s.workspaceRoot, string(workspace.ID)))
	if err != nil {
		return Workspace{}, fmt.Errorf("open stale workspace: %w", err)
	}
	writeErr := WriteControlState(ctx, root, ControlState{
		SchemaVersion: ControlSchemaVersion, WorkspaceID: next.ID, State: next.State,
		StateReasonCode: next.StateReasonCode, Revision: next.Revision, UpdatedAt: next.UpdatedAt,
	})
	closeErr := root.Close()
	if writeErr != nil || closeErr != nil {
		return Workspace{}, errors.Join(writeErr, wrapServiceError("close stale workspace", closeErr))
	}
	return next, nil
}

func workspaceDeletion(workspace Workspace, journal Journal) WorkspaceDeletion {
	before := workspace.DraftDigest
	operation := OperationRecord{
		ID: journal.OperationID, ObjectType: workspaceObjectType, ObjectID: string(workspace.ID),
		Action: "config.workspace.delete", BeforeDigest: &before, Result: operationResultSuccess,
		RequestID: journal.Actor.RequestID, OccurredAt: journal.StartedAt,
	}
	details, _ := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: workspace.Name})
	return WorkspaceDeletion{
		ID: workspace.ID, ExpectedRevision: workspace.Revision, Operation: operation,
		Audit: AuditEvent{
			OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: journal.Actor.UserID,
			Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		},
	}
}

func deleteTombstoneName(id WorkspaceID, operationID string) string {
	return ".delete-" + string(id) + "-" + operationID
}

func renameOwnedWorkspace(root *ScopedRoot, source, target string) error {
	held, err := root.directory.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if current.Mode()&fs.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(held, current) {
		return ErrPathInvalid
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			return fs.ErrExist
		}
		return err
	}
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(source))
}

func validServiceLimits(limits Limits) bool {
	return limits.MaxWorkspaces > 0 && limits.MaxWorkspaceBytes > 0 && limits.MaxEntries > 0 &&
		limits.MaxManagedBytes >= 0 && limits.MaxFileBytes > 0 && maximumManifestPayload(limits) > 0 &&
		limits.MaxGroups > 0 && limits.MaxGroupMembers > 0 && limits.MaxTotalGroupMembers > 0
}

func validateActor(actor Actor) error {
	if actor.UserID <= 0 || actor.RequestID == "" || len(actor.RequestID) > 128 {
		return fmt.Errorf("invalid actor")
	}
	for _, value := range actor.RequestID {
		if unicode.IsControl(value) || unicode.IsSpace(value) {
			return fmt.Errorf("invalid actor")
		}
	}
	return nil
}

func requireWorkspaceActor(workspace Workspace, actor Actor) error {
	if IsSystemWorkspaceName(workspace.Name) != actor.System {
		return ErrSystemWorkspaceAccess
	}
	return nil
}

func validateProductionState(state ProductionState, limits Limits) error {
	if state.Digest == (Digest{}) || state.ManifestVersion != ManifestSchemaVersion || state.EntryCount < 0 ||
		state.EntryCount > limits.MaxEntries || state.ManagedBytes < 0 || state.ManagedBytes > limits.MaxManagedBytes {
		return fmt.Errorf("invalid production state")
	}
	return nil
}

func validateSnapshot(snapshot Snapshot, preflight ProductionState, limits Limits) error {
	if err := snapshot.Manifest.Validate(limits); err != nil {
		return err
	}
	if snapshot.ProductionDigest == (Digest{}) || snapshot.BaseDigest != snapshot.ProductionDigest ||
		snapshot.Manifest.Digest() != snapshot.BaseDigest {
		return ErrSnapshotChanged
	}
	if snapshot.Manifest.ManagedBytes > preflight.ManagedBytes {
		return ErrLimitExceeded
	}
	return nil
}

func workspaceReservation(managedBytes int64, limits Limits) (int64, error) {
	manifestBudget := int64(maximumManifestPayload(limits))
	if managedBytes < 0 || managedBytes > (math.MaxInt64-2*manifestBudget-2*controlStateLimit)/2 {
		return 0, ErrLimitExceeded
	}
	return 2*managedBytes + 2*manifestBudget + 2*controlStateLimit, nil
}

func exceedsCapacity(existing, additional, maximum int64) bool {
	return existing < 0 || additional < 0 || maximum < 0 || existing > maximum || additional > maximum-existing
}

func (s *Service) checkStagedCapacity(ctx context.Context, root *ScopedRoot, registeredBytes, reservation int64) error {
	staged, err := workspaceLogicalBytes(ctx, root, s.limits)
	if err != nil {
		return err
	}
	if staged > reservation || exceedsCapacity(registeredBytes, staged, s.limits.MaxWorkspaceBytes) {
		return fmt.Errorf("workspace staging capacity: %w", ErrLimitExceeded)
	}
	return nil
}

func (s *Service) checkAdditionalCapacity(
	ctx context.Context,
	root *ScopedRoot,
	registeredBytes, reservation, additional int64,
) error {
	staged, err := workspaceLogicalBytes(ctx, root, s.limits)
	if err != nil {
		return err
	}
	if exceedsCapacity(staged, additional, reservation) ||
		exceedsCapacity(registeredBytes, staged+additional, s.limits.MaxWorkspaceBytes) {
		return fmt.Errorf("workspace transient capacity: %w", ErrLimitExceeded)
	}
	return nil
}

func workspaceLogicalBytes(ctx context.Context, root *ScopedRoot, limits Limits) (int64, error) {
	entries, err := root.Walk(ctx, workspaceTraversalLimit(limits))
	if err != nil {
		return 0, fmt.Errorf("measure workspace: %w", err)
	}
	var total int64
	for _, entry := range entries {
		if entry.Type != EntryRegular {
			continue
		}
		if entry.Size < 0 || total > math.MaxInt64-entry.Size {
			return 0, fmt.Errorf("measure workspace: %w", ErrLimitExceeded)
		}
		total += entry.Size
	}
	return total, nil
}

func workspaceTraversalLimit(limits Limits) int {
	if limits.MaxEntries > (math.MaxInt-32)/2 {
		return math.MaxInt
	}
	return 2*limits.MaxEntries + 32
}

func verifyManagedTree(
	ctx context.Context,
	workspacePath, name string,
	manifest Manifest,
	limits Limits,
	fileMode fs.FileMode,
) (Digest, error) {
	root, err := OpenScopedRoot(filepath.Join(workspacePath, name))
	if err != nil {
		return Digest{}, err
	}
	digest, digestErr := digestSnapshotTarget(ctx, root, manifest, managedTreeSnapshotOptions(limits, fileMode))
	closeErr := root.Close()
	return digest, errors.Join(digestErr, wrapServiceError("close managed tree", closeErr))
}

func managedTreeSnapshotOptions(limits Limits, fileMode fs.FileMode) SnapshotOptions {
	return SnapshotOptions{
		Entry: "nginx.conf", Limits: limits, Policy: NewPolicy(),
		FileMode: fileMode, DirectoryMode: workspaceDirectoryMode,
	}
}

func copyManagedTree(
	ctx context.Context,
	workspacePath string,
	manifest Manifest,
	limits Limits,
	checkCapacity func(int64) error,
) (returnErr error) {
	base, err := OpenScopedRoot(filepath.Join(workspacePath, "base"))
	if err != nil {
		return err
	}
	draft, err := OpenScopedRoot(filepath.Join(workspacePath, "draft"))
	if err != nil {
		return errors.Join(err, wrapServiceError("close base tree", base.Close()))
	}
	defer func() {
		returnErr = errors.Join(
			returnErr,
			wrapServiceError("close draft tree", draft.Close()),
			wrapServiceError("close base tree", base.Close()),
		)
	}()
	for _, entry := range manifest.Entries {
		if entry.Type == EntryDirectory {
			if err := draft.EnsureDirectory(ctx, entry.Path, workspaceDirectoryMode); err != nil {
				return err
			}
		}
	}
	for _, entry := range manifest.Entries {
		if entry.Class != EntryManagedText {
			continue
		}
		content, info, err := base.ReadRegular(ctx, entry.Path, limits.MaxFileBytes)
		if err != nil {
			return err
		}
		if info.Mode().Perm() != baseManagedFileMode || int64(len(content)) != entry.Size {
			return ErrSnapshotChanged
		}
		if err := checkCapacity(int64(len(content))); err != nil {
			return err
		}
		if err := draft.CreateRegular(ctx, entry.Path, content, draftManagedFileMode); err != nil {
			return err
		}
	}
	return nil
}

func verifyWorkspaceDirectories(workspacePath string) error {
	paths := []string{
		filepath.Join(workspacePath, "base"),
		filepath.Join(workspacePath, "draft"),
		filepath.Join(workspacePath, "control"),
	}
	information := make([]fs.FileInfo, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != workspaceDirectoryMode {
			return ErrPathInvalid
		}
		information = append(information, info)
	}
	for left := range information {
		for right := left + 1; right < len(information); right++ {
			if os.SameFile(information[left], information[right]) {
				return ErrPathInvalid
			}
		}
	}
	return nil
}

func acquireWorkspaceRootLock(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	// #nosec G115 -- Unix file descriptors are non-negative and fit uintptr.
	file := os.NewFile(uintptr(descriptor), "workspace root lock")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, errors.Join(ctxErr, file.Close())
			}
			return file, nil
		case !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN):
			return nil, errors.Join(err, file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-ticker.C:
		}
	}
}

func (s *Service) newOperationID() (string, error) {
	return newOpaqueID(s.random)
}

func workspaceCreation(workspace Workspace, actor Actor, operationID string) WorkspaceCreation {
	after := workspace.DraftDigest
	operation := OperationRecord{
		ID: operationID, ObjectType: workspaceObjectType, ObjectID: string(workspace.ID),
		Action: "config.workspace.create", AfterDigest: &after, Result: operationResultSuccess,
		RequestID: actor.RequestID, OccurredAt: workspace.CreatedAt,
	}
	details, _ := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: workspace.Name})
	return WorkspaceCreation{
		Workspace: workspace,
		Operation: operation,
		Audit: AuditEvent{
			OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: actor.UserID,
			Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		},
	}
}

func manifestPayloadSize(manifest Manifest) int {
	payload, err := manifest.MarshalBinary()
	if err != nil {
		return math.MaxInt
	}
	return len(payload)
}

func removeOwnedWorkspace(ctx context.Context, path string, root *ScopedRoot, limits Limits) error {
	if root == nil {
		opened, err := OpenScopedRoot(path)
		if err != nil {
			return err
		}
		root = opened
	}
	clearErr := root.clear(ctx, workspaceTraversalLimit(limits))
	closeErr := root.Close()
	if clearErr != nil || closeErr != nil {
		return errors.Join(clearErr, closeErr)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove workspace directory: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	syncErr := unix.Fsync(descriptor)
	closeErr := unix.Close(descriptor)
	if syncErr != nil || closeErr != nil {
		return errors.Join(wrapServiceError("sync directory", syncErr), wrapServiceError("close synced directory", closeErr))
	}
	return nil
}

func wrapServiceError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
