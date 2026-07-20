/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package structuredconfig

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/location"
	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

const (
	structuredPatchLimit = 4 << 20
	structuredDiffWork   = 8_000_000
)

// WorkspaceStore is the minimal verified snapshot and recoverable replacement boundary.
type WorkspaceStore interface {
	DraftSnapshot(context.Context, config.WorkspaceID) (config.DraftSnapshot, error)
	ReplaceFiles(
		context.Context,
		config.Actor,
		config.WorkspaceID,
		config.ReplaceFilesInput,
	) (config.ReplaceFilesResult, error)
}

// Service coordinates structured read, preview, and draft application use cases.
type Service struct {
	workspaces WorkspaceStore
}

// NewService validates and assembles the structured configuration service.
func NewService(workspaces WorkspaceStore) (*Service, error) {
	if workspaces == nil {
		return nil, fmt.Errorf("create structured config service: workspace store is required")
	}
	return &Service{workspaces: workspaces}, nil
}

// Catalog returns one structured projection built entirely from one verified draft snapshot.
func (s *Service) Catalog(ctx context.Context, id config.WorkspaceID) (Projection, error) {
	snapshot, project, err := s.snapshotProject(ctx, id)
	if err != nil {
		return Projection{}, fmt.Errorf("read structured catalog: %w", err)
	}
	return projectionFrom(snapshot, project), nil
}

func (s *Service) snapshotProject(
	ctx context.Context,
	id config.WorkspaceID,
) (config.DraftSnapshot, *nginxast.Project, error) {
	if ctx == nil || s == nil || s.workspaces == nil {
		return config.DraftSnapshot{}, nil, fmt.Errorf("structured config service is unavailable")
	}
	if _, err := config.ParseWorkspaceID(string(id)); err != nil {
		return config.DraftSnapshot{}, nil, err
	}
	snapshot, err := s.workspaces.DraftSnapshot(ctx, id)
	if err != nil {
		return config.DraftSnapshot{}, nil, err
	}
	if snapshot.Workspace.ID != id || snapshot.Workspace.State != config.StateReady ||
		snapshot.WorkspaceETag != snapshot.Workspace.ETag() {
		return config.DraftSnapshot{}, nil, config.ErrConflict
	}
	project, err := projectFromSnapshot(snapshot, nil)
	if err != nil {
		return config.DraftSnapshot{}, nil, err
	}
	return snapshot, project, nil
}

func projectFromSnapshot(
	snapshot config.DraftSnapshot,
	replacements map[config.RelativePath]string,
) (*nginxast.Project, error) {
	files := make([]nginxast.SourceFile, len(snapshot.Files))
	seen := make(map[config.RelativePath]struct{}, len(snapshot.Files))
	for index, file := range snapshot.Files {
		if _, duplicate := seen[file.Path]; duplicate {
			return nil, fmt.Errorf("build structured project: duplicate source")
		}
		seen[file.Path] = struct{}{}
		content := file.Content
		if replacement, exists := replacements[file.Path]; exists {
			content = []byte(replacement)
		} else if config.Digest(sha256.Sum256(content)) != file.ContentDigest {
			return nil, fmt.Errorf("build structured project: %w", config.ErrConflict)
		}
		files[index] = nginxast.SourceFile{Path: string(file.Path), Source: string(content)}
	}
	if len(replacements) != 0 {
		for path := range replacements {
			if _, exists := seen[path]; !exists {
				return nil, fmt.Errorf("build structured project: replacement source unavailable")
			}
		}
	}
	edges := make([]nginxast.IncludeEdge, len(snapshot.Dependencies))
	for index, dependency := range snapshot.Dependencies {
		status, ok := includeStatus(dependency.Status)
		if !ok {
			return nil, fmt.Errorf("build structured project: invalid include status")
		}
		edges[index] = nginxast.IncludeEdge{
			Source: string(dependency.Source), Line: dependency.Line, Column: dependency.Column,
			Target: string(dependency.Target), Status: status,
		}
	}
	project, err := nginxast.BuildProject(files, edges, nginxast.DefaultProjectLimits())
	if errors.Is(err, nginxast.ErrLimitExceeded) {
		return nil, fmt.Errorf("build structured project: %w", ErrLimitExceeded)
	}
	return project, err
}

func includeStatus(status config.DependencyStatus) (nginxast.IncludeStatus, bool) {
	switch status {
	case config.DependencyResolved:
		return nginxast.IncludeResolved, true
	case config.DependencyMissing:
		return nginxast.IncludeMissing, true
	case config.DependencyExternal:
		return nginxast.IncludeExternal, true
	case config.DependencyUnresolved:
		return nginxast.IncludeUnresolved, true
	case config.DependencySymlink:
		return nginxast.IncludeSymlink, true
	case config.DependencySpecial:
		return nginxast.IncludeSpecial, true
	case config.DependencyCycle:
		return nginxast.IncludeCycle, true
	default:
		return "", false
	}
}

func projectionFrom(snapshot config.DraftSnapshot, project *nginxast.Project) Projection {
	if project == nil {
		return Projection{WorkspaceID: snapshot.Workspace.ID, DraftETag: snapshot.WorkspaceETag}
	}
	upstreams := upstream.BuildCatalog(project)
	locations := location.BuildCatalog(project, upstreams)
	return Projection{
		WorkspaceID: snapshot.Workspace.ID, DraftETag: snapshot.WorkspaceETag,
		Complete:           project.Complete,
		ProjectDiagnostics: append([]nginxast.ProjectDiagnostic(nil), project.Diagnostics...),
		HTTPBlocks:         httpBlocksFromProject(project),
		Upstreams:          upstreams, Locations: locations,
	}
}

func projectReadinessError(project *nginxast.Project) error {
	if project == nil {
		return ErrParseFailed
	}
	for _, source := range project.Documents {
		if source.Error != nil && source.Error.Code == nginxast.ErrorLimitExceeded {
			return ErrLimitExceeded
		}
	}
	if !project.Complete {
		return ErrParseFailed
	}
	return nil
}

func httpBlocksFromProject(project *nginxast.Project) []HTTPBlock {
	blocks := make([]HTTPBlock, 0)
	if project == nil {
		return blocks
	}
	for _, reference := range project.Nodes {
		block, ok := reference.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "http" ||
			!slices.Contains(reference.Placements, nginxast.Placement{
				Context: nginxast.ContextMain,
			}) {
			continue
		}
		editable := project.Complete && len(block.Arguments) == 0 &&
			!reference.Ambiguous && reference.Instances == 1
		readOnlyReason := ""
		switch {
		case !project.Complete:
			readOnlyReason = "project_incomplete"
		case len(block.Arguments) != 0:
			readOnlyReason = "unsupported_header"
		case reference.Ambiguous || reference.Instances != 1:
			readOnlyReason = "context_ambiguous"
		}
		blocks = append(blocks, HTTPBlock{
			ID: reference.ID,
			Source: SourceLocation{
				Path: reference.Path, StartLine: block.Span.Start.Line,
				StartColumn: block.Span.Start.Column, EndLine: block.Span.End.Line,
				EndColumn: block.Span.End.Column,
			},
			Editable: editable, ReadOnlyReason: readOnlyReason, Instances: reference.Instances,
		})
	}
	return blocks
}
