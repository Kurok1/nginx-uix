/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package structuredconfig

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/location"
	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

// Preview computes a bounded change review without persisting any state.
func (s *Service) Preview(
	ctx context.Context,
	id config.WorkspaceID,
	operation Operation,
) (Preview, error) {
	snapshot, project, err := s.snapshotProject(ctx, id)
	if err != nil {
		return Preview{}, fmt.Errorf("preview structured change: %w", err)
	}
	preview, err := previewSnapshot(ctx, snapshot, project, operation)
	if err != nil {
		return Preview{}, fmt.Errorf("preview structured change: %w", err)
	}
	return preview, nil
}

// Apply recomputes preview identity and persists only server-rendered complete file replacements.
func (s *Service) Apply(
	ctx context.Context,
	actor config.Actor,
	id config.WorkspaceID,
	operation Operation,
	previewID string,
	ifMatch string,
) (config.ReplaceFilesResult, error) {
	if _, err := config.ParseDigest(previewID); err != nil {
		return config.ReplaceFilesResult{}, ErrPreviewStale
	}
	snapshot, project, err := s.snapshotProject(ctx, id)
	if err != nil {
		return config.ReplaceFilesResult{}, fmt.Errorf("apply structured change: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(ifMatch), []byte(snapshot.WorkspaceETag)) != 1 {
		return config.ReplaceFilesResult{}, ErrPreviewStale
	}
	preview, err := previewSnapshot(ctx, snapshot, project, operation)
	if err != nil {
		return config.ReplaceFilesResult{}, fmt.Errorf("apply structured change: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(previewID), []byte(preview.PreviewID)) != 1 {
		return config.ReplaceFilesResult{}, ErrPreviewStale
	}
	if !preview.Complete {
		return config.ReplaceFilesResult{}, ErrPreviewIncomplete
	}
	replacements := make([]config.FileReplacement, len(preview.replacements))
	for index, replacement := range preview.replacements {
		replacements[index] = config.FileReplacement{
			Path: replacement.Path, Content: slices.Clone(replacement.Content),
		}
	}
	result, err := s.workspaces.ReplaceFiles(ctx, actor, id, config.ReplaceFilesInput{
		Replacements: replacements, IfMatch: ifMatch,
		OperationKind: string(preview.OperationKind),
		PreviewID:     preview.PreviewID, TargetID: preview.TargetID,
	})
	if err != nil {
		return config.ReplaceFilesResult{}, fmt.Errorf("apply structured change: %w", err)
	}
	return result, nil
}

func previewSnapshot(
	ctx context.Context,
	snapshot config.DraftSnapshot,
	project *nginxast.Project,
	operation Operation,
) (Preview, error) {
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	if err := projectReadinessError(project); err != nil {
		return Preview{}, err
	}
	beforeProjection := projectionFrom(snapshot, project)
	plan, err := planOperation(project, beforeProjection, operation)
	if err != nil {
		return Preview{}, err
	}
	rendered, err := project.ApplyEdits(plan.edits)
	if err != nil {
		return Preview{}, err
	}
	if len(rendered) == 0 {
		return Preview{}, ErrPostcondition
	}

	updatedSnapshot, err := snapshotWithAdjustedDependencies(snapshot, project, plan.edits, rendered)
	if err != nil {
		return Preview{}, err
	}
	afterProject, err := projectFromSnapshot(updatedSnapshot, renderedPaths(rendered))
	if err != nil {
		return Preview{}, err
	}
	afterProjection := projectionFrom(updatedSnapshot, afterProject)
	if err := validatePostcondition(beforeProjection, afterProjection, operation); err != nil {
		return Preview{}, err
	}

	beforeFiles := make(map[config.RelativePath][]byte, len(snapshot.Files))
	for _, file := range snapshot.Files {
		beforeFiles[file.Path] = file.Content
	}
	paths := make([]config.RelativePath, 0, len(rendered))
	for rawPath := range rendered {
		path, err := config.ParseRelativePath(rawPath, config.DefaultLimits())
		if err != nil {
			return Preview{}, ErrPostcondition
		}
		paths = append(paths, path)
	}
	slices.SortFunc(paths, func(left, right config.RelativePath) int {
		return strings.Compare(string(left), string(right))
	})

	preview := Preview{
		WorkspaceID: snapshot.Workspace.ID, DraftETag: snapshot.WorkspaceETag,
		OperationKind: plan.kind, TargetID: plan.targetID,
		Complete:     project.Complete && afterProject.Complete,
		ChangedFiles: make([]ChangedFile, 0, len(paths)),
		replacements: make([]config.FileReplacement, 0, len(paths)),
	}
	totalPatchBytes := 0
	for _, path := range paths {
		before, exists := beforeFiles[path]
		if !exists {
			return Preview{}, ErrPostcondition
		}
		after := []byte(rendered[string(path)])
		if slices.Equal(before, after) {
			return Preview{}, ErrPostcondition
		}
		beforeDigest := config.Digest(sha256.Sum256(before))
		afterDigest := config.Digest(sha256.Sum256(after))
		patch, summary, err := config.UnifiedDiff(ctx, path, before, after, structuredDiffWork)
		if err != nil {
			return Preview{}, err
		}
		totalPatchBytes += len(patch)
		preview.ChangedFiles = append(preview.ChangedFiles, ChangedFile{
			Path: path, BeforeDigest: beforeDigest.String(), AfterDigest: afterDigest.String(),
			AddedLines: summary.AddedLines, RemovedLines: summary.RemovedLines, Patch: patch,
		})
		preview.replacements = append(preview.replacements, config.FileReplacement{
			Path: path, Content: slices.Clone(after),
		})
	}
	if totalPatchBytes > structuredPatchLimit {
		preview.Complete = false
		for index := range preview.ChangedFiles {
			preview.ChangedFiles[index].Patch = ""
		}
	}
	preview.PreviewID, err = previewIdentity(operation, preview)
	if err != nil {
		return Preview{}, err
	}
	return preview, nil
}

func previewIdentity(operation Operation, preview Preview) (string, error) {
	operationPayload, err := canonicalOperation(operation)
	if err != nil {
		return "", err
	}
	var writer canonicalWriter
	writer.string("structured-preview-v1")
	writer.string(string(preview.WorkspaceID))
	writer.string(preview.DraftETag)
	writer.string(string(preview.OperationKind))
	writer.string(preview.TargetID)
	writer.string(string(operationPayload))
	writer.boolean(preview.Complete)
	writer.uint64(uint64(len(preview.ChangedFiles)))
	for _, file := range preview.ChangedFiles {
		writer.string(string(file.Path))
		writer.string(file.BeforeDigest)
		writer.string(file.AfterDigest)
	}
	digest := sha256.Sum256(writer.Bytes())
	return hex.EncodeToString(digest[:]), nil
}

func renderedPaths(rendered map[string]string) map[config.RelativePath]string {
	paths := make(map[config.RelativePath]string, len(rendered))
	for path, content := range rendered {
		paths[config.RelativePath(path)] = content
	}
	return paths
}

func snapshotWithAdjustedDependencies(
	snapshot config.DraftSnapshot,
	project *nginxast.Project,
	edits []nginxast.SourceEdit,
	rendered map[string]string,
) (config.DraftSnapshot, error) {
	updated := snapshot
	updated.Dependencies = make([]config.Dependency, 0, len(snapshot.Dependencies))
	includeOffsets := make(map[string]int)
	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok || directive.Name.Value != "include" {
			continue
		}
		key := dependencyKey(reference.Path, directive.Name.Span.Start.Line, directive.Name.Span.Start.Column)
		includeOffsets[key] = directive.Name.Span.Start.Offset
	}
	editsByPath := make(map[string][]nginxast.Edit)
	for _, edit := range edits {
		editsByPath[edit.Path] = append(editsByPath[edit.Path], edit.Edit)
	}
	for path := range editsByPath {
		slices.SortFunc(editsByPath[path], func(left, right nginxast.Edit) int {
			return left.Span.Start.Offset - right.Span.Start.Offset
		})
	}

	for _, dependency := range snapshot.Dependencies {
		content, changed := rendered[string(dependency.Source)]
		if !changed {
			updated.Dependencies = append(updated.Dependencies, dependency)
			continue
		}
		key := dependencyKey(string(dependency.Source), dependency.Line, dependency.Column)
		offset, exists := includeOffsets[key]
		if !exists {
			return config.DraftSnapshot{}, ErrPostcondition
		}
		nextOffset, retained, err := transformedOffset(offset, editsByPath[string(dependency.Source)])
		if err != nil {
			return config.DraftSnapshot{}, err
		}
		if !retained {
			continue
		}
		line, column, err := lineColumn(content, nextOffset)
		if err != nil {
			return config.DraftSnapshot{}, err
		}
		dependency.Line = line
		dependency.Column = column
		updated.Dependencies = append(updated.Dependencies, dependency)
	}
	return updated, nil
}

func transformedOffset(offset int, edits []nginxast.Edit) (int, bool, error) {
	shift := 0
	for _, edit := range edits {
		start := edit.Span.Start.Offset
		end := edit.Span.End.Offset
		switch {
		case start == end && start <= offset:
			shift += len(edit.Replacement)
		case end <= offset:
			shift += len(edit.Replacement) - (end - start)
		case start <= offset && offset < end:
			return 0, false, nil
		}
	}
	return offset + shift, true, nil
}

func dependencyKey(path string, line int, column int) string {
	return fmt.Sprintf("%s\x00%d\x00%d", path, line, column)
}

func lineColumn(source string, target int) (int, int, error) {
	if target < 0 || target > len(source) {
		return 0, 0, ErrPostcondition
	}
	line := 1
	column := 1
	for offset := 0; offset < target; {
		switch source[offset] {
		case '\r':
			offset++
			if offset < target && source[offset] == '\n' {
				offset++
			}
			line++
			column = 1
		case '\n':
			offset++
			line++
			column = 1
		default:
			_, size := utf8.DecodeRuneInString(source[offset:])
			offset += size
			column++
		}
	}
	return line, column, nil
}

func validatePostcondition(before Projection, after Projection, operation Operation) error {
	if len(after.ProjectDiagnostics) > len(before.ProjectDiagnostics) ||
		blockingLocationDiagnostics(after.Locations) > blockingLocationDiagnostics(before.Locations) ||
		len(after.Upstreams.Diagnostics) > len(before.Upstreams.Diagnostics) {
		return ErrPostcondition
	}
	beforeUpstreams := len(before.Upstreams.Upstreams)
	afterUpstreams := len(after.Upstreams.Upstreams)
	beforeServers := upstreamServerCount(before.Upstreams)
	afterServers := upstreamServerCount(after.Upstreams)
	beforeLocations := locationCount(before.Locations)
	afterLocations := locationCount(after.Locations)

	switch operation.Kind {
	case OperationUpstreamCreate:
		if afterUpstreams != beforeUpstreams+1 ||
			!hasUpstreamName(after.Upstreams, operation.UpstreamCreate.Name) {
			return ErrPostcondition
		}
	case OperationUpstreamRename:
		if afterUpstreams != beforeUpstreams ||
			!hasUpstreamName(after.Upstreams, operation.UpstreamRename.NewName) {
			return ErrPostcondition
		}
	case OperationUpstreamDelete:
		if afterUpstreams != beforeUpstreams-1 {
			return ErrPostcondition
		}
	case OperationUpstreamServerCreate:
		if afterServers != beforeServers+1 {
			return ErrPostcondition
		}
	case OperationUpstreamServerUpdate:
		if afterServers != beforeServers {
			return ErrPostcondition
		}
	case OperationUpstreamServerDelete:
		if afterServers != beforeServers-1 {
			return ErrPostcondition
		}
	case OperationLocationCreate:
		if afterLocations != beforeLocations+1 ||
			!hasLocationMatcher(after.Locations, operation.LocationCreate.Type, operation.LocationCreate.Matcher) {
			return ErrPostcondition
		}
	case OperationLocationUpdate:
		if afterLocations != beforeLocations ||
			!hasLocationMatcher(after.Locations, operation.LocationUpdate.Type, operation.LocationUpdate.Matcher) {
			return ErrPostcondition
		}
	case OperationLocationDelete:
		if afterLocations != beforeLocations-1 {
			return ErrPostcondition
		}
	default:
		return ErrInvalidOperation
	}
	return nil
}

func upstreamServerCount(catalog upstream.Catalog) int {
	count := 0
	for _, group := range catalog.Upstreams {
		count += len(group.Servers)
	}
	return count
}

func hasUpstreamName(catalog upstream.Catalog, name string) bool {
	for _, group := range catalog.Upstreams {
		if group.Name == name {
			return true
		}
	}
	return false
}

func blockingLocationDiagnostics(catalog location.Catalog) int {
	count := 0
	for _, diagnostic := range catalog.Diagnostics {
		if diagnostic.Severity == location.SeverityBlocking {
			count++
		}
	}
	return count
}

func locationCount(catalog location.Catalog) int {
	count := 0
	for _, server := range catalog.Servers {
		count += locationTreeCount(server.Locations)
	}
	return count
}

func locationTreeCount(locations []location.Location) int {
	count := 0
	for _, candidate := range locations {
		count += 1 + locationTreeCount(candidate.Children)
	}
	return count
}

func hasLocationMatcher(catalog location.Catalog, matcherType location.MatcherType, matcher string) bool {
	for _, server := range catalog.Servers {
		if locationTreeHasMatcher(server.Locations, matcherType, matcher) {
			return true
		}
	}
	return false
}

func locationTreeHasMatcher(
	locations []location.Location,
	matcherType location.MatcherType,
	matcher string,
) bool {
	for _, candidate := range locations {
		if candidate.Type == matcherType && candidate.Matcher == matcher {
			return true
		}
		if locationTreeHasMatcher(candidate.Children, matcherType, matcher) {
			return true
		}
	}
	return false
}
