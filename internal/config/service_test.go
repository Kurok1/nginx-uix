/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCreateWorkspaceProducesReadyIndependentBaseAndDraft(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace, err := fixture.service.Create(
		context.Background(),
		Actor{UserID: 7, RequestID: "req-1"},
		"primary review",
	)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.State != StateReady || workspace.ETag() != DraftETag(workspace.DraftDigest) {
		t.Fatalf("workspace = %#v", workspace)
	}
	base := fixture.path(workspace.ID, "base/nginx.conf")
	draft := fixture.path(workspace.ID, "draft/nginx.conf")
	assertRegularFile(t, base, 0o400)
	assertRegularFile(t, draft, 0o600)
	assertServiceDifferentInode(t, base, draft)
	assertAuditAction(t, fixture.repository, "config.workspace.create", "req-1")

	root, err := OpenScopedRoot(fixture.path(workspace.ID, ""))
	if err != nil {
		t.Fatalf("OpenScopedRoot(workspace) error = %v", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close(workspace) error = %v", err)
		}
	}()
	state, err := ReadControlState(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadControlState() error = %v", err)
	}
	if state.State != StateReady || state.Revision != workspace.Revision {
		t.Fatalf("control state = %#v, workspace = %#v", state, workspace)
	}
}

func TestCertificateSystemWorkspaceRequiresSystemActor(t *testing.T) {
	fixture := newServiceFixture(t)
	name := "ACME HTTP challenge deadbeef"

	if _, err := fixture.service.Create(
		context.Background(), Actor{UserID: 7, RequestID: "req-user-create"}, name,
	); !errors.Is(err, ErrSystemWorkspaceAccess) {
		t.Fatalf("Create() error = %v, want ErrSystemWorkspaceAccess", err)
	}

	created, err := fixture.service.Create(
		context.Background(), Actor{UserID: 7, RequestID: "req-system-create", System: true}, name,
	)
	if err != nil {
		t.Fatalf("Create() system workspace error = %v", err)
	}
	if !IsSystemWorkspaceName(created.Name) {
		t.Fatalf("IsSystemWorkspaceName(%q) = false", created.Name)
	}

	if _, err := fixture.service.ReplaceFile(
		context.Background(), Actor{UserID: 7, RequestID: "req-user-edit"}, created.ID,
		ReplaceFileInput{Path: "nginx.conf", Content: []byte("events {}\nhttp {}\n"), IfMatch: created.ETag()},
	); !errors.Is(err, ErrSystemWorkspaceAccess) {
		t.Fatalf("ReplaceFile() error = %v, want ErrSystemWorkspaceAccess", err)
	}

	if err := fixture.service.Delete(
		context.Background(), Actor{UserID: 7, RequestID: "req-user-delete"},
		created.ID, created.ETag(), created.Name,
	); !errors.Is(err, ErrSystemWorkspaceAccess) {
		t.Fatalf("Delete() error = %v, want ErrSystemWorkspaceAccess", err)
	}
}

func TestListWorkspaceMovesOnlyReadyToStale(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "req-create"}, "review")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeExistingFixtureFile(t, fixture.production.productionRoot+"/nginx.conf", "events {}\nhttp {}\n", 0o644)
	fixture.clock.now = fixture.clock.now.Add(time.Minute)

	listed, err := fixture.service.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || listed[0].State != StateStale ||
		listed[0].StateReasonCode != "PRODUCTION_CHANGED" || listed[0].Revision != created.Revision+1 {
		t.Fatalf("List() = %#v", listed)
	}
	digestCalls := fixture.production.digestCalls
	fixture.production.digestErr = errors.New("agent unavailable")
	got, err := fixture.service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get(stale) error = %v", err)
	}
	if got != listed[0] || fixture.production.digestCalls != digestCalls {
		t.Fatalf("Get(stale) = %#v, digest calls = %d, want unchanged/%d", got, fixture.production.digestCalls, digestCalls)
	}
	assertAuditAction(t, fixture.repository, "config.workspace.stale", "")
}

func TestGetReadyWorkspaceDoesNotClearStateOnAgentFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "req-create"}, "review")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	fixture.production.digestErr = errors.New("agent unavailable")
	if _, err := fixture.service.Get(context.Background(), created.ID); err == nil {
		t.Fatal("Get(ready with failed Agent) error = nil")
	}
	persisted, err := fixture.repository.Workspace(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Workspace() error = %v", err)
	}
	if persisted.State != StateReady || persisted.Revision != created.Revision {
		t.Fatalf("workspace mutated after Agent failure: %#v", persisted)
	}
}

func TestDeleteWorkspaceRequiresCurrentETagAndNameThenRemovesUnreadableDraft(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.Create(context.Background(), Actor{UserID: 7, RequestID: "req-create"}, "review")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := fixture.service.Delete(
		context.Background(), Actor{UserID: 7, RequestID: "req-wrong"}, created.ID, created.ETag(), "wrong",
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete(wrong name) error = %v, want ErrConflict", err)
	}
	if err := os.Remove(fixture.path(created.ID, "draft/nginx.conf")); err != nil {
		t.Fatalf("Remove(draft) error = %v", err)
	}
	fixture.repository.forceState(created.ID, StateNeedsAttention, "DRAFT_DIGEST_MISMATCH")
	current, err := fixture.repository.Workspace(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Workspace() error = %v", err)
	}
	if err := fixture.service.Delete(
		context.Background(), Actor{UserID: 7, RequestID: "req-delete"}, current.ID, current.ETag(), current.Name,
	); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := fixture.repository.Workspace(context.Background(), created.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Workspace(after delete) error = %v, want not exist", err)
	}
	entries, err := os.ReadDir(fixture.workspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir(workspace root) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace root entries after delete = %v", entries)
	}
	assertAuditAction(t, fixture.repository, "config.workspace.delete", "req-delete")
}

func TestDeleteWorkspaceTombstonesBeforeDatabaseAndRestoresOnDatabaseFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	observedTombstone := false
	fixture.repository.deleteHook = func() {
		entries, err := os.ReadDir(fixture.workspaceRoot)
		if err != nil {
			return
		}
		originalMissing := false
		if _, err := os.Lstat(fixture.path(workspace.ID, "")); errors.Is(err, fs.ErrNotExist) {
			originalMissing = true
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".delete-"+string(workspace.ID)+"-") && entry.IsDir() {
				observedTombstone = originalMissing
			}
		}
	}
	fixture.repository.deleteErr = errors.New("database unavailable")
	err := fixture.service.Delete(
		context.Background(), Actor{UserID: 7, RequestID: "req-delete"},
		workspace.ID, workspace.ETag(), workspace.Name,
	)
	if err == nil {
		t.Fatal("Delete() error = nil")
	}
	if !observedTombstone {
		t.Fatal("database deletion did not observe a tombstoned workspace")
	}
	if _, err := os.Lstat(fixture.path(workspace.ID, "")); err != nil {
		t.Fatalf("original workspace was not restored: %v", err)
	}
	if _, err := fixture.repository.Workspace(context.Background(), workspace.ID); err != nil {
		t.Fatalf("workspace row was not preserved: %v", err)
	}
}

func TestDeleteWorkspacePreparesPublicJournalWithoutLegacyDeleteRecord(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	fixture.service.mutationHook = func(checkpoint string) error {
		if checkpoint == "after_phase_prepared" {
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
	root := fixture.openWorkspace(t, workspace.ID)
	defer closeReconcileRoot(t, root)
	journal, err := ReadJournal(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadJournal() error = %v", err)
	}
	if journal.Kind != "workspace_delete" || journal.Phase != JournalPrepared ||
		journal.WorkspaceID != workspace.ID || journal.ExpectedRevision != workspace.Revision {
		t.Fatalf("delete journal = %#v", journal)
	}
	if _, _, err := root.ReadRegular(context.Background(), "control/delete.json", controlStateLimit); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy delete record error = %v, want not exist", err)
	}
}

func TestReconcileRenameBackFailureMarksRowWithoutTouchingReplacement(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	fixture.repository.deleteHook = func() {
		path := fixture.path(workspace.ID, "")
		if err := os.Mkdir(path, 0o700); err != nil {
			return
		}
		if err := os.Mkdir(filepath.Join(path, "control"), 0o700); err != nil {
			return
		}
		root, err := OpenScopedRoot(path)
		if err != nil {
			return
		}
		defer func() {
			if err := root.Close(); err != nil {
				t.Errorf("Close(replacement) error = %v", err)
			}
		}()
		_ = WriteControlState(context.Background(), root, ControlState{
			SchemaVersion: ControlSchemaVersion, WorkspaceID: workspace.ID,
			State: StatePreparing, Revision: 1, UpdatedAt: workspace.UpdatedAt,
		})
		_ = root.CreateRegular(context.Background(), "sentinel", []byte("keep"), 0o600)
	}
	fixture.repository.deleteErr = errors.New("database unavailable")
	if err := fixture.service.Delete(
		context.Background(), Actor{UserID: 7, RequestID: "req-delete"},
		workspace.ID, workspace.ETag(), workspace.Name,
	); err == nil {
		t.Fatal("Delete() error = nil")
	}
	if err := fixture.service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	got := fixture.mustWorkspace(t, workspace.ID)
	if got.State != StateNeedsAttention || got.StateReasonCode != reasonDeleteTombstoneMismatch {
		t.Fatalf("workspace = %#v", got)
	}
	contents, err := os.ReadFile(fixture.path(workspace.ID, "sentinel"))
	if err != nil || string(contents) != "keep" {
		t.Fatalf("replacement sentinel = %q, %v", contents, err)
	}
}

func TestCreateWorkspaceCleansOwnedTreeWhenDatabaseCommitFails(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.repository.createErr = errors.New("database unavailable")
	if _, err := fixture.service.Create(
		context.Background(), Actor{UserID: 7, RequestID: "req-create"}, "review",
	); err == nil {
		t.Fatal("Create() error = nil")
	}
	entries, err := os.ReadDir(fixture.workspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir(workspace root) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unregistered workspace entries = %v", entries)
	}
}

func TestCreateWorkspaceRejectsCapacityBeforeAgentSnapshot(t *testing.T) {
	fixture := newServiceFixture(t)
	limits := DefaultLimits()
	limits.MaxWorkspaceBytes = 1
	service, err := NewService(Dependencies{
		WorkspaceRoot: fixture.workspaceRoot, Production: fixture.production,
		Reader: fixture.repository, Writer: fixture.repository, Groups: fixture.repository,
		Attention: fixedAttentionReader{},
		Clock:     fixture.clock, Random: &incrementingReader{}, Limits: limits,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Create(
		context.Background(), Actor{UserID: 7, RequestID: "req-create"}, "review",
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Create() error = %v, want ErrLimitExceeded", err)
	}
	if fixture.production.snapshotCalls != 0 {
		t.Fatalf("ConfigSnapshot() calls = %d, want 0", fixture.production.snapshotCalls)
	}
}

func TestCreateWorkspaceRejectsOpenAttentionBeforeReadingProduction(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.attention = fixedAttentionReader{open: true}
	_, err := fixture.service.Create(
		context.Background(), Actor{UserID: 7, RequestID: "req-attention-create"}, "review",
	)
	if !errors.Is(err, ErrAttentionUnresolved) {
		t.Fatalf("Create() error = %v, want ErrAttentionUnresolved", err)
	}
	if fixture.production.digestCalls != 0 || fixture.production.snapshotCalls != 0 {
		t.Fatalf("production calls = digest %d snapshot %d", fixture.production.digestCalls, fixture.production.snapshotCalls)
	}
}

func TestCreateWorkspaceWaitsForExclusiveCapacityLockBeforeAgent(t *testing.T) {
	fixture := newServiceFixture(t)
	descriptor, err := unix.Open(
		fixture.workspaceRoot,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Flock(descriptor, unix.LOCK_UN); err != nil {
			t.Errorf("Flock(unlock) error = %v", err)
		}
		if err := unix.Close(descriptor); err != nil {
			t.Errorf("Close(root descriptor) error = %v", err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.Create(
		ctx, Actor{UserID: 7, RequestID: "req-create"}, "review",
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v, want context.Canceled", err)
	}
	if fixture.production.digestCalls != 0 {
		t.Fatalf("Agent digest calls = %d, want 0 before capacity lock", fixture.production.digestCalls)
	}
}

func TestCreateWorkspaceCleansStagedTreeWhenProductionGrowsAfterPreflight(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.production.beforeSnapshot = func() {
		writeFixtureFile(
			t, filepath.Join(fixture.production.productionRoot, "conf.d", "grown.conf"),
			"server { listen 8081; }\n", 0o640,
		)
	}
	if _, err := fixture.service.Create(
		context.Background(), Actor{UserID: 7, RequestID: "req-create"}, "review",
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Create() error = %v, want ErrLimitExceeded", err)
	}
	entries, err := os.ReadDir(fixture.workspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir(workspace root) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("staged workspace entries after growth = %v", entries)
	}
}

type serviceFixture struct {
	service       *Service
	repository    *memoryWorkspaceRepository
	production    *filesystemProduction
	workspaceRoot string
	clock         *fixedClock
}

type fixedAttentionReader struct {
	open bool
	err  error
}

func (r fixedAttentionReader) HasOpenAttentionCases(context.Context) (bool, error) {
	return r.open, r.err
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(temp root) error = %v", err)
	}
	productionRoot := filepath.Join(root, "production")
	workspaceRoot := filepath.Join(root, "workspaces")
	for _, directory := range []string{productionRoot, workspaceRoot, filepath.Join(productionRoot, "conf.d")} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("Mkdir(%s) error = %v", directory, err)
		}
	}
	writeFixtureFile(t, filepath.Join(productionRoot, "nginx.conf"), "events {}\nhttp { include conf.d/*.conf; }\n", 0o644)
	writeFixtureFile(t, filepath.Join(productionRoot, "conf.d", "site.conf"), "server { listen 8080; }\n", 0o640)

	repository := newMemoryWorkspaceRepository()
	clock := &fixedClock{now: time.Date(2026, time.July, 16, 2, 0, 0, 0, time.UTC)}
	production := &filesystemProduction{productionRoot: productionRoot, workspaceRoot: workspaceRoot}
	service, err := NewService(Dependencies{
		WorkspaceRoot: workspaceRoot,
		Production:    production,
		Reader:        repository,
		Writer:        repository,
		Groups:        repository,
		Attention:     fixedAttentionReader{},
		Clock:         clock,
		Random:        &incrementingReader{},
		Limits:        DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &serviceFixture{
		service: service, repository: repository, production: production,
		workspaceRoot: workspaceRoot, clock: clock,
	}
}

func (f *serviceFixture) path(id WorkspaceID, relative string) string {
	return filepath.Join(f.workspaceRoot, string(id), filepath.FromSlash(relative))
}

type filesystemProduction struct {
	productionRoot   string
	workspaceRoot    string
	digestErr        error
	snapshotErr      error
	digestCalls      int
	snapshotCalls    int
	digestRequests   []string
	snapshotRequests []string
	beforeSnapshot   func()
}

func (p *filesystemProduction) ConfigDigest(ctx context.Context, requestID string) (ProductionState, error) {
	p.digestCalls++
	p.digestRequests = append(p.digestRequests, requestID)
	if p.digestErr != nil {
		return ProductionState{}, p.digestErr
	}
	root, err := OpenScopedRoot(p.productionRoot)
	if err != nil {
		return ProductionState{}, err
	}
	state, digestErr := DigestRoot(ctx, root, managedTreeSnapshotOptions(DefaultLimits(), 0o600))
	closeErr := root.Close()
	return state, errors.Join(digestErr, closeErr)
}

func (p *filesystemProduction) ConfigSnapshot(ctx context.Context, requestID string, id WorkspaceID) (Snapshot, error) {
	p.snapshotCalls++
	p.snapshotRequests = append(p.snapshotRequests, requestID)
	if p.beforeSnapshot != nil {
		p.beforeSnapshot()
	}
	if p.snapshotErr != nil {
		return Snapshot{}, p.snapshotErr
	}
	basePath := filepath.Join(p.workspaceRoot, string(id), "base")
	if err := os.Mkdir(basePath, 0o700); err != nil {
		return Snapshot{}, err
	}
	source, err := OpenScopedRoot(p.productionRoot)
	if err != nil {
		return Snapshot{}, err
	}
	target, err := OpenScopedRoot(basePath)
	if err != nil {
		return Snapshot{}, errors.Join(err, source.Close())
	}
	snapshot, snapshotErr := SnapshotTo(ctx, source, target, managedTreeSnapshotOptions(DefaultLimits(), 0o400))
	closeErr := errors.Join(target.Close(), source.Close())
	return snapshot, errors.Join(snapshotErr, closeErr)
}

type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time { return c.now }

type incrementingReader struct {
	mu   sync.Mutex
	next byte
}

func (r *incrementingReader) Read(payload []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range payload {
		r.next++
		payload[index] = r.next
	}
	return len(payload), nil
}

type memoryWorkspaceRepository struct {
	mu               sync.Mutex
	workspaces       map[WorkspaceID]Workspace
	groups           GroupCollection
	operations       []OperationRecord
	audits           []AuditEvent
	createErr        error
	deleteErr        error
	groupErr         error
	deleteHook       func()
	groupChangeCalls int
}

func newMemoryWorkspaceRepository() *memoryWorkspaceRepository {
	return &memoryWorkspaceRepository{
		workspaces: make(map[WorkspaceID]Workspace),
		groups:     GroupCollection{Revision: 1},
	}
}

func (r *memoryWorkspaceRepository) WorkspaceUsage(context.Context) (int, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var bytes int64
	for _, workspace := range r.workspaces {
		bytes += workspace.WorkspaceBytes
	}
	return len(r.workspaces), bytes, nil
}

func (r *memoryWorkspaceRepository) ListWorkspaces(context.Context) ([]Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	workspaces := make([]Workspace, 0, len(r.workspaces))
	for _, workspace := range r.workspaces {
		workspaces = append(workspaces, workspace)
	}
	slices.SortFunc(workspaces, func(left, right Workspace) int {
		if byTime := right.UpdatedAt.Compare(left.UpdatedAt); byTime != 0 {
			return byTime
		}
		if left.ID < right.ID {
			return -1
		}
		if left.ID > right.ID {
			return 1
		}
		return 0
	})
	return workspaces, nil
}

func (r *memoryWorkspaceRepository) Workspace(_ context.Context, id WorkspaceID) (Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	workspace, ok := r.workspaces[id]
	if !ok {
		return Workspace{}, os.ErrNotExist
	}
	return workspace, nil
}

func (r *memoryWorkspaceRepository) OperationAudit(_ context.Context, operationID string) (OperationRecord, AuditEvent, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var operation OperationRecord
	operationFound := false
	for _, candidate := range r.operations {
		if candidate.ID == operationID {
			operation = candidate
			operationFound = true
			break
		}
	}
	var audit AuditEvent
	auditFound := false
	for _, candidate := range r.audits {
		if candidate.OperationID == operationID {
			audit = candidate
			auditFound = true
			break
		}
	}
	if operationFound != auditFound {
		return OperationRecord{}, AuditEvent{}, false, ErrConflict
	}
	return operation, audit, operationFound, nil
}

func (r *memoryWorkspaceRepository) CreateWorkspace(_ context.Context, creation WorkspaceCreation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	if _, exists := r.workspaces[creation.Workspace.ID]; exists {
		return ErrConflict
	}
	r.workspaces[creation.Workspace.ID] = creation.Workspace
	r.operations = append(r.operations, creation.Operation)
	r.audits = append(r.audits, creation.Audit)
	return nil
}

func (r *memoryWorkspaceRepository) forceState(id WorkspaceID, state WorkspaceState, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	workspace := r.workspaces[id]
	workspace.State = state
	workspace.StateReasonCode = reason
	workspace.Revision++
	r.workspaces[id] = workspace
}

func (r *memoryWorkspaceRepository) UpdateWorkspace(_ context.Context, change WorkspaceChange) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.workspaces[change.Next.ID]
	if !exists {
		return os.ErrNotExist
	}
	if current.Revision != change.ExpectedRevision {
		return ErrConflict
	}
	r.workspaces[change.Next.ID] = change.Next
	r.operations = append(r.operations, change.Operation)
	r.audits = append(r.audits, change.Audit)
	return nil
}

func (r *memoryWorkspaceRepository) DeleteWorkspace(_ context.Context, deletion WorkspaceDeletion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleteHook != nil {
		r.deleteHook()
	}
	if r.deleteErr != nil {
		return r.deleteErr
	}
	current, exists := r.workspaces[deletion.ID]
	if !exists {
		return os.ErrNotExist
	}
	if current.Revision != deletion.ExpectedRevision {
		return ErrConflict
	}
	delete(r.workspaces, deletion.ID)
	r.operations = append(r.operations, deletion.Operation)
	r.audits = append(r.audits, deletion.Audit)
	return nil
}

func (r *memoryWorkspaceRepository) GroupCollection(context.Context) (GroupCollection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneGroupCollection(r.groups), nil
}

func (r *memoryWorkspaceRepository) ChangeGroupCollection(_ context.Context, change GroupChange) (GroupCollection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groupChangeCalls++
	if r.groupErr != nil {
		return GroupCollection{}, r.groupErr
	}
	if change.ExpectedRevision != r.groups.Revision {
		return GroupCollection{}, ErrConflict
	}
	r.groups = canonicalGroupCollection(GroupCollection{Revision: r.groups.Revision + 1, Groups: change.Groups})
	r.operations = append(r.operations, change.Operation)
	r.audits = append(r.audits, change.Audit)
	return cloneGroupCollection(r.groups), nil
}

func cloneGroupCollection(collection GroupCollection) GroupCollection {
	cloned := GroupCollection{Revision: collection.Revision, Groups: make([]Group, len(collection.Groups))}
	for index, group := range collection.Groups {
		group.Members = slices.Clone(group.Members)
		cloned.Groups[index] = group
	}
	return cloned
}

func writeFixtureFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		t.Fatalf("OpenFile(%s) error = %v", path, err)
	}
	if _, err := io.WriteString(file, content); err != nil {
		closeErr := file.Close()
		t.Fatalf("WriteString(%s) error = %v", path, errors.Join(err, closeErr))
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%s) error = %v", path, err)
	}
}

func writeExistingFixtureFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode|0o200); err != nil {
		t.Fatalf("Chmod(writable %s) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%s) error = %v", path, err)
	}
}

func assertRegularFile(t *testing.T, path string, mode fs.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%s) error = %v", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v, want regular %04o", path, info.Mode(), mode)
	}
}

func assertServiceDifferentInode(t *testing.T, left, right string) {
	t.Helper()
	leftInfo, err := os.Stat(left)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", left, err)
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", right, err)
	}
	if os.SameFile(leftInfo, rightInfo) {
		t.Fatalf("%s and %s share an inode", left, right)
	}
}

func assertAuditAction(t *testing.T, repository *memoryWorkspaceRepository, action, requestID string) {
	t.Helper()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, audit := range repository.audits {
		if audit.Action == action && (requestID == "" || audit.RequestID == requestID) {
			return
		}
	}
	t.Fatalf("audit %q/%q not found in %#v", action, requestID, repository.audits)
}
