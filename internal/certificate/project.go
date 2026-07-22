/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"fmt"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/nginxast"
)

// ProjectFromDraft builds an include-aware lossless project from one verified draft.
func ProjectFromDraft(snapshot config.DraftSnapshot) (*nginxast.Project, error) {
	files := make([]nginxast.SourceFile, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		files = append(files, nginxast.SourceFile{Path: string(file.Path), Source: string(file.Content)})
	}
	edges := make([]nginxast.IncludeEdge, 0, len(snapshot.Dependencies))
	for _, dependency := range snapshot.Dependencies {
		status, ok := certificateIncludeStatus(dependency.Status)
		if !ok {
			return nil, fmt.Errorf("build certificate config project: invalid include status")
		}
		edges = append(edges, nginxast.IncludeEdge{
			Source: string(dependency.Source), Line: dependency.Line, Column: dependency.Column,
			Target: string(dependency.Target), Status: status,
		})
	}
	project, err := nginxast.BuildProject(files, edges, nginxast.DefaultProjectLimits())
	if err != nil {
		return nil, fmt.Errorf("build certificate config project: %w", err)
	}
	return project, nil
}

func certificateIncludeStatus(status config.DependencyStatus) (nginxast.IncludeStatus, bool) {
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
