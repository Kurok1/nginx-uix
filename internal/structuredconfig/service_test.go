/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package structuredconfig

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

func TestCatalogBuildsOneSnapshotBoundCrossFileProjection(t *testing.T) {
	t.Parallel()

	store := newMemoryWorkspace()
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.Catalog(context.Background(), store.snapshot.Workspace.ID)
	if err != nil {
		t.Fatalf("Catalog() error = %v", err)
	}
	if projection.WorkspaceID != store.snapshot.Workspace.ID ||
		projection.DraftETag != store.snapshot.WorkspaceETag ||
		!projection.Complete || len(projection.HTTPBlocks) != 1 ||
		len(projection.Upstreams.Upstreams) != 1 ||
		len(projection.Locations.Servers) != 1 ||
		len(projection.Locations.Servers[0].Locations) != 1 {
		t.Fatalf("projection = %#v", projection)
	}
	if block := projection.HTTPBlocks[0]; len(block.ID) != 32 || !block.Editable ||
		block.Instances != 1 || block.Source.Path != "nginx.conf" {
		t.Fatalf("HTTP block = %#v", block)
	}
	if projection.Upstreams.Upstreams[0].Name != "old" ||
		len(projection.Upstreams.Upstreams[0].References) != 1 {
		t.Fatalf("upstream projection = %#v", projection.Upstreams)
	}
}

func TestCatalogKeepsProjectCompleteWhenOnlyReferenceAnalysisIsIncomplete(t *testing.T) {
	t.Parallel()

	store := newMemoryWorkspace()
	store.snapshot.Files[1] = draftFile(
		"sites.conf",
		"server { location / { proxy_pass http://$target; } }\n",
	)
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.Catalog(context.Background(), store.snapshot.Workspace.ID)
	if err != nil {
		t.Fatalf("Catalog() error = %v", err)
	}
	if !projection.Complete || projection.Upstreams.ReferenceAnalysisComplete {
		t.Fatalf(
			"Complete = %v, ReferenceAnalysisComplete = %v",
			projection.Complete,
			projection.Upstreams.ReferenceAnalysisComplete,
		)
	}
}

func TestPreviewAllowsAnUnrelatedServerEditWithIncompleteReferenceAnalysis(t *testing.T) {
	t.Parallel()

	store := newMemoryWorkspace()
	store.snapshot.Files[1] = draftFile(
		"sites.conf",
		"server { location / { proxy_pass http://$target; } }\n",
	)
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.Catalog(context.Background(), store.snapshot.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	group := projection.Upstreams.Upstreams[0]
	preview, err := service.Preview(context.Background(), store.snapshot.Workspace.ID, Operation{
		Kind: OperationUpstreamServerUpdate,
		UpstreamServerUpdate: &upstream.UpdateServerInput{
			UpstreamID: group.ID,
			ServerID:   group.Servers[0].ID,
			Server: upstream.ServerInput{
				Endpoint: upstream.Endpoint{Address: "127.0.0.2", Port: port(8080)},
			},
		},
	})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if !preview.Complete || len(preview.ChangedFiles) != 1 ||
		preview.ChangedFiles[0].Path != "upstreams.conf" {
		t.Fatalf("preview = %#v", preview)
	}
}

func TestPreviewIsPureDeterministicAndBindsOperationAndChangedDigests(t *testing.T) {
	t.Parallel()

	store := newMemoryWorkspace()
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.Catalog(context.Background(), store.snapshot.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	operation := Operation{
		Kind: OperationUpstreamRename,
		UpstreamRename: &upstream.RenameInput{
			UpstreamID: projection.Upstreams.Upstreams[0].ID,
			NewName:    "backend",
		},
	}

	first, err := service.Preview(context.Background(), store.snapshot.Workspace.ID, operation)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	second, err := service.Preview(context.Background(), store.snapshot.Workspace.ID, operation)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.PreviewID) != 64 ||
		first.OperationKind != OperationUpstreamRename || first.TargetID == "" ||
		!first.Complete || len(first.ChangedFiles) != 2 || store.replaceCalls != 0 {
		t.Fatalf("preview = %#v, second = %#v, calls = %d", first, second, store.replaceCalls)
	}
	if !slices.IsSortedFunc(first.ChangedFiles, func(left, right ChangedFile) int {
		switch {
		case left.Path < right.Path:
			return -1
		case left.Path > right.Path:
			return 1
		default:
			return 0
		}
	}) {
		t.Fatalf("changed files are not stable: %#v", first.ChangedFiles)
	}
	for _, file := range first.ChangedFiles {
		if file.BeforeDigest == file.AfterDigest || file.Patch == "" {
			t.Fatalf("changed file = %#v", file)
		}
	}

	changedOperation := operation
	changedInput := *operation.UpstreamRename
	changedInput.NewName = "another"
	changedOperation.UpstreamRename = &changedInput
	changed, err := service.Preview(context.Background(), store.snapshot.Workspace.ID, changedOperation)
	if err != nil {
		t.Fatal(err)
	}
	if changed.PreviewID == first.PreviewID {
		t.Fatal("preview ID did not bind operation fields")
	}
}

func TestApplyRecomputesPreviewAndPassesOnlyServerGeneratedFilesToBatchStore(t *testing.T) {
	t.Parallel()

	store := newMemoryWorkspace()
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.Catalog(context.Background(), store.snapshot.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	operation := Operation{
		Kind: OperationUpstreamRename,
		UpstreamRename: &upstream.RenameInput{
			UpstreamID: projection.Upstreams.Upstreams[0].ID,
			NewName:    "backend",
		},
	}
	preview, err := service.Preview(context.Background(), store.snapshot.Workspace.ID, operation)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Apply(
		context.Background(),
		config.Actor{UserID: 7, RequestID: "req-structured-apply"},
		store.snapshot.Workspace.ID,
		operation,
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		preview.DraftETag,
	)
	if !errors.Is(err, ErrPreviewStale) || store.replaceCalls != 0 {
		t.Fatalf("Apply(stale preview) error = %v, calls = %d", err, store.replaceCalls)
	}

	result, err := service.Apply(
		context.Background(),
		config.Actor{UserID: 7, RequestID: "req-structured-apply"},
		store.snapshot.Workspace.ID,
		operation,
		preview.PreviewID,
		preview.DraftETag,
	)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if store.replaceCalls != 1 || len(store.lastInput.Replacements) != 2 ||
		store.lastInput.OperationKind != string(OperationUpstreamRename) ||
		store.lastInput.PreviewID != preview.PreviewID ||
		store.lastInput.TargetID != preview.TargetID ||
		!reflect.DeepEqual(result.ChangedPaths, []config.RelativePath{"sites.conf", "upstreams.conf"}) {
		t.Fatalf("apply result = %#v, input = %#v, calls = %d", result, store.lastInput, store.replaceCalls)
	}
	for _, replacement := range store.lastInput.Replacements {
		if replacement.Path == "sites.conf" && string(replacement.Content) !=
			"server { location / { proxy_pass http://backend/api/; } }\n" {
			t.Fatalf("sites replacement = %q", replacement.Content)
		}
		if replacement.Path == "upstreams.conf" && string(replacement.Content) !=
			"upstream backend { server 127.0.0.1:8080; }\n" {
			t.Fatalf("upstreams replacement = %q", replacement.Content)
		}
	}
}

func TestPreviewClassifiesIncompleteSyntaxAndParserLimits(t *testing.T) {
	t.Parallel()

	store := newMemoryWorkspace()
	store.snapshot.Files[1] = draftFile("sites.conf", "server { location / {\n")
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.Catalog(context.Background(), store.snapshot.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Complete || len(projection.Upstreams.Upstreams) != 1 {
		t.Fatalf("projection = %#v", projection)
	}
	_, err = service.Preview(context.Background(), store.snapshot.Workspace.ID, Operation{
		Kind: OperationUpstreamRename,
		UpstreamRename: &upstream.RenameInput{
			UpstreamID: projection.Upstreams.Upstreams[0].ID,
			NewName:    "backend",
		},
	})
	if !errors.Is(err, ErrParseFailed) {
		t.Fatalf("Preview() error = %v, want ErrParseFailed", err)
	}

	project := &nginxast.Project{
		Complete: false,
		Documents: map[string]nginxast.ParsedSource{
			"nginx.conf": {
				Error: &nginxast.SyntaxError{Code: nginxast.ErrorLimitExceeded},
			},
		},
	}
	if err := projectReadinessError(project); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("projectReadinessError() = %v, want ErrLimitExceeded", err)
	}
}

type memoryWorkspace struct {
	snapshot     config.DraftSnapshot
	replaceCalls int
	lastInput    config.ReplaceFilesInput
}

func newMemoryWorkspace() *memoryWorkspace {
	files := []config.DraftFile{
		draftFile("nginx.conf", "http {\n include upstreams.conf;\n include sites.conf;\n}\n"),
		draftFile("sites.conf", "server { location / { proxy_pass http://old/api/; } }\n"),
		draftFile("upstreams.conf", "upstream old { server 127.0.0.1:8080; }\n"),
	}
	draftDigest := config.Digest(sha256.Sum256([]byte("structured-snapshot-v1")))
	workspace := config.Workspace{
		ID: "0102030405060708090a0b0c0d0e0f10", State: config.StateReady,
		DraftDigest: draftDigest, Revision: 1,
	}
	return &memoryWorkspace{snapshot: config.DraftSnapshot{
		Workspace: workspace, WorkspaceETag: config.DraftETag(draftDigest),
		Files: files,
		Dependencies: []config.Dependency{
			{
				Source: "nginx.conf", Line: 2, Column: 2,
				DisplayValue: "upstreams.conf", Target: "upstreams.conf",
				Status: config.DependencyResolved,
			},
			{
				Source: "nginx.conf", Line: 3, Column: 2,
				DisplayValue: "sites.conf", Target: "sites.conf",
				Status: config.DependencyResolved,
			},
		},
	}}
}

func (m *memoryWorkspace) DraftSnapshot(
	_ context.Context,
	_ config.WorkspaceID,
) (config.DraftSnapshot, error) {
	snapshot := m.snapshot
	snapshot.Files = make([]config.DraftFile, len(m.snapshot.Files))
	for index, file := range m.snapshot.Files {
		file.Content = slices.Clone(file.Content)
		snapshot.Files[index] = file
	}
	snapshot.Dependencies = slices.Clone(m.snapshot.Dependencies)
	return snapshot, nil
}

func (m *memoryWorkspace) ReplaceFiles(
	_ context.Context,
	_ config.Actor,
	_ config.WorkspaceID,
	input config.ReplaceFilesInput,
) (config.ReplaceFilesResult, error) {
	m.replaceCalls++
	m.lastInput = input
	next := m.snapshot.Workspace
	next.Revision++
	return config.ReplaceFilesResult{
		Workspace:    next,
		ChangedPaths: []config.RelativePath{"sites.conf", "upstreams.conf"},
	}, nil
}

func draftFile(path config.RelativePath, content string) config.DraftFile {
	payload := []byte(content)
	return config.DraftFile{
		Path: path, Content: payload,
		ContentDigest: config.Digest(sha256.Sum256(payload)),
		LineEnding:    "lf",
	}
}

func port(value uint16) *uint16 {
	return &value
}
