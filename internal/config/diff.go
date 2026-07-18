/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

const (
	defaultDiffMaxWork = 8_000_000
	diffContextLines   = 3
)

// FileDiffSummary describes one managed path's base-to-draft state.
type FileDiffSummary struct {
	Path         RelativePath `json:"path"`
	Status       string       `json:"status"`
	AddedLines   int          `json:"added_lines"`
	RemovedLines int          `json:"removed_lines"`
}

// DiffResult is bounded, deterministic review data derived from a workspace.
type DiffResult struct {
	Files    []FileDiffSummary `json:"files"`
	Complete bool              `json:"complete"`
	Reason   string            `json:"reason"`
	Patch    string            `json:"patch"`
}

type reviewWorkspace struct {
	workspace     Workspace
	root          *ScopedRoot
	lock          *WorkspaceLock
	draftManifest Manifest
	baseManifest  Manifest
}

type diffOperation struct {
	kind byte
	line []byte
}

const (
	diffEqual  byte = ' '
	diffDelete byte = '-'
	diffAdd    byte = '+'
)

// Diff derives managed-file summaries and a unified patch without persisting review data.
func (s *Service) Diff(ctx context.Context, id WorkspaceID, rawPath *RelativePath) (_ DiffResult, returnErr error) {
	var selectedPath *RelativePath
	if rawPath != nil {
		parsed, err := s.parseFilePath(*rawPath)
		if err != nil {
			return DiffResult{}, fmt.Errorf("derive workspace diff: %w", err)
		}
		selectedPath = &parsed
	}

	review, err := s.openReviewWorkspace(ctx, id, true)
	if err != nil {
		return DiffResult{}, fmt.Errorf("derive workspace diff: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeReviewWorkspace(review))
	}()

	base, err := OpenScopedRoot(review.root.path + "/base")
	if err != nil {
		return DiffResult{}, fmt.Errorf("derive workspace diff: open base: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close diff base", base.Close()))
	}()
	draft, err := OpenScopedRoot(review.root.path + "/draft")
	if err != nil {
		return DiffResult{}, fmt.Errorf("derive workspace diff: open draft: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close diff draft", draft.Close()))
	}()

	baseEntries := managedEntriesByPath(review.baseManifest)
	draftEntries := managedEntriesByPath(review.draftManifest)
	paths := managedDiffPaths(baseEntries, draftEntries, selectedPath)
	if selectedPath != nil && len(paths) == 0 {
		return DiffResult{}, fmt.Errorf("derive workspace diff: %w", ErrEntryNotManaged)
	}

	result := DiffResult{Files: make([]FileDiffSummary, 0, len(paths)), Complete: true}
	var patch strings.Builder
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return DiffResult{}, err
		}
		var before, after []byte
		if entry, ok := baseEntries[path]; ok {
			before, err = readReviewFile(ctx, base, entry, s.limits, baseManagedFileMode)
			if err != nil {
				return DiffResult{}, fmt.Errorf("derive workspace diff: read base file: %w", err)
			}
		}
		if entry, ok := draftEntries[path]; ok {
			after, err = readReviewFile(ctx, draft, entry, s.limits, draftManagedFileMode)
			if err != nil {
				return DiffResult{}, fmt.Errorf("derive workspace diff: read draft file: %w", err)
			}
		}
		filePatch, summary, err := UnifiedDiff(ctx, path, before, after, defaultDiffMaxWork)
		if err != nil {
			return DiffResult{}, fmt.Errorf("derive workspace diff: compare file: %w", err)
		}
		result.Files = append(result.Files, summary)
		patch.WriteString(filePatch)
	}
	result.Patch = patch.String()

	if err := verifyManagedTreeDigest(ctx, review.root.path, "base", review.baseManifest, s.limits, baseManagedFileMode, review.workspace.BaseDigest); err != nil {
		return DiffResult{}, fmt.Errorf("derive workspace diff: verify base: %w", err)
	}
	if err := verifyManagedTreeDigest(ctx, review.root.path, "draft", review.draftManifest, s.limits, draftManagedFileMode, review.workspace.DraftDigest); err != nil {
		return DiffResult{}, fmt.Errorf("derive workspace diff: verify draft: %w", err)
	}
	return boundDiffResult(result, s.limits.MaxDiffResponseBytes)
}

// UnifiedDiff returns a deterministic line-based unified patch and complete summary.
func UnifiedDiff(ctx context.Context, path RelativePath, before, after []byte, maxWork int) (string, FileDiffSummary, error) {
	if ctx == nil {
		return "", FileDiffSummary{}, fmt.Errorf("derive unified diff: context is required")
	}
	parsed, err := ParseRelativePath(string(path), DefaultLimits())
	if err != nil || parsed != path {
		return "", FileDiffSummary{}, fmt.Errorf("derive unified diff: %w", ErrPathInvalid)
	}
	if err := ctx.Err(); err != nil {
		return "", FileDiffSummary{}, err
	}

	summary := FileDiffSummary{Path: path, Status: diffStatus(before, after)}
	if summary.Status == "unchanged" {
		return "", summary, nil
	}
	beforeLines := splitDiffLines(before)
	afterLines := splitDiffLines(after)
	prefix, suffix, err := commonDiffEdges(ctx, beforeLines, afterLines)
	if err != nil {
		return "", FileDiffSummary{}, err
	}
	middleBefore := beforeLines[prefix : len(beforeLines)-suffix]
	middleAfter := afterLines[prefix : len(afterLines)-suffix]
	middle, complete, err := myersDiff(ctx, middleBefore, middleAfter, maxWork)
	if err != nil {
		return "", FileDiffSummary{}, err
	}
	if !complete {
		middle = replacementOperations(middleBefore, middleAfter)
	}
	operations := make([]diffOperation, 0, prefix+len(middle)+suffix)
	operations = appendEqualOperations(operations, beforeLines[:prefix])
	operations = append(operations, middle...)
	operations = appendEqualOperations(operations, beforeLines[len(beforeLines)-suffix:])
	for _, operation := range operations {
		switch operation.kind {
		case diffAdd:
			summary.AddedLines++
		case diffDelete:
			summary.RemovedLines++
		}
	}
	return renderUnifiedPatch(path, summary.Status, operations), summary, nil
}

func (s *Service) openReviewWorkspace(ctx context.Context, id WorkspaceID, verifyBase bool) (reviewWorkspace, error) {
	if ctx == nil || s == nil {
		return reviewWorkspace{}, fmt.Errorf("service is unavailable")
	}
	parsedID, err := ParseWorkspaceID(string(id))
	if err != nil || parsedID != id {
		return reviewWorkspace{}, ErrIdentifierInvalid
	}
	root, err := OpenScopedRoot(s.workspacePath(id))
	if err != nil {
		return reviewWorkspace{}, err
	}
	lock, err := AcquireWorkspaceLock(ctx, root, LockShared)
	if err != nil {
		return reviewWorkspace{}, errors.Join(err, root.Close())
	}
	fail := func(err error) (reviewWorkspace, error) {
		return reviewWorkspace{}, errors.Join(err, lock.Close(), root.Close())
	}

	workspace, err := s.reader.Workspace(ctx, id)
	if err != nil {
		return fail(err)
	}
	switch workspace.State {
	case StateReady, StateStale, StateNeedsAttention:
	case StatePreparing:
		return fail(ErrConflict)
	default:
		return fail(ErrConflict)
	}
	state, err := ReadControlState(ctx, root)
	if err != nil || state.WorkspaceID != id || state.State != workspace.State ||
		state.StateReasonCode != workspace.StateReasonCode || state.Revision != workspace.Revision ||
		!state.UpdatedAt.Equal(workspace.UpdatedAt) {
		return fail(errors.Join(ErrConflict, err))
	}
	if _, err := ReadJournal(ctx, root); err == nil {
		return fail(ErrConflict)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fail(errors.Join(ErrConflict, err))
	}
	draftManifest, err := ReadControlManifest(ctx, root, s.limits)
	if err != nil || draftManifest.Digest() != workspace.DraftDigest || draftManifest.EntryCount != workspace.EntryCount ||
		draftManifest.ManagedBytes != workspace.ManagedBytes {
		return fail(errors.Join(ErrConflict, err))
	}
	baseManifest, err := readManifestAt(ctx, root, controlBaseManifestPath, s.limits)
	if errors.Is(err, fs.ErrNotExist) && workspace.BaseDigest == workspace.DraftDigest {
		baseManifest = draftManifest
		err = nil
	}
	if err != nil || baseManifest.Digest() != workspace.BaseDigest {
		return fail(errors.Join(ErrConflict, err))
	}
	if err := verifyManagedTreeDigest(ctx, root.path, "draft", draftManifest, s.limits, draftManagedFileMode, workspace.DraftDigest); err != nil {
		return fail(err)
	}
	if verifyBase {
		if err := verifyManagedTreeDigest(ctx, root.path, "base", baseManifest, s.limits, baseManagedFileMode, workspace.BaseDigest); err != nil {
			return fail(err)
		}
	}
	return reviewWorkspace{workspace: workspace, root: root, lock: lock, draftManifest: draftManifest, baseManifest: baseManifest}, nil
}

func closeReviewWorkspace(review reviewWorkspace) error {
	return errors.Join(
		wrapServiceError("release review workspace lock", review.lock.Close()),
		wrapServiceError("close review workspace", review.root.Close()),
	)
}

func managedEntriesByPath(manifest Manifest) map[RelativePath]Entry {
	entries := make(map[RelativePath]Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if entry.Type == EntryRegular && entry.Class == EntryManagedText {
			entries[entry.Path] = entry
		}
	}
	return entries
}

func managedDiffPaths(base, draft map[RelativePath]Entry, selected *RelativePath) []RelativePath {
	if selected != nil {
		if _, ok := base[*selected]; ok {
			return []RelativePath{*selected}
		}
		if _, ok := draft[*selected]; ok {
			return []RelativePath{*selected}
		}
		return nil
	}
	paths := make([]RelativePath, 0, len(base)+len(draft))
	seen := make(map[RelativePath]struct{}, len(base)+len(draft))
	for path := range base {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range draft {
		if _, ok := seen[path]; !ok {
			paths = append(paths, path)
		}
	}
	slices.SortFunc(paths, func(left, right RelativePath) int {
		return bytes.Compare([]byte(left), []byte(right))
	})
	return paths
}

func readReviewFile(ctx context.Context, root *ScopedRoot, entry Entry, limits Limits, mode fs.FileMode) ([]byte, error) {
	content, info, err := root.ReadRegular(ctx, entry.Path, limits.MaxFileBytes)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm() != mode || int64(len(content)) != entry.Size || digestBytes(content) != entry.ContentDigest {
		return nil, ErrConflict
	}
	if content == nil {
		content = make([]byte, 0)
	}
	return content, nil
}

func digestBytes(content []byte) Digest {
	return Digest(sha256.Sum256(content))
}

func diffStatus(before, after []byte) string {
	switch {
	case before == nil && after == nil:
		return "unchanged"
	case before == nil:
		return "created"
	case after == nil:
		return "deleted"
	case bytes.Equal(before, after):
		return "unchanged"
	default:
		return "modified"
	}
}

func splitDiffLines(content []byte) [][]byte {
	if len(content) == 0 {
		return nil
	}
	lines := make([][]byte, 0, bytes.Count(content, []byte{'\n'})+1)
	for len(content) > 0 {
		end := bytes.IndexByte(content, '\n')
		if end < 0 {
			lines = append(lines, content)
			break
		}
		end++
		lines = append(lines, content[:end])
		content = content[end:]
	}
	return lines
}

func commonDiffEdges(ctx context.Context, before, after [][]byte) (int, int, error) {
	prefix := 0
	for prefix < len(before) && prefix < len(after) && bytes.Equal(before[prefix], after[prefix]) {
		if prefix&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix &&
		bytes.Equal(before[len(before)-1-suffix], after[len(after)-1-suffix]) {
		if suffix&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		suffix++
	}
	return prefix, suffix, nil
}

func myersDiff(ctx context.Context, before, after [][]byte, maxWork int) ([]diffOperation, bool, error) {
	if len(before) == 0 {
		return replacementOperations(nil, after), true, nil
	}
	if len(after) == 0 {
		return replacementOperations(before, nil), true, nil
	}
	if maxWork <= 0 {
		return nil, false, nil
	}
	work := 0
	tick := func(amount int) (bool, error) {
		if amount < 0 || work > maxWork-amount {
			return false, nil
		}
		work += amount
		if work == amount || work&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	vectors := map[int]int{1: 0}
	trace := make([]map[int]int, 0)
	maximumDistance := len(before) + len(after)
	for distance := 0; distance <= maximumDistance; distance++ {
		if ok, err := tick(len(vectors)); err != nil {
			return nil, false, err
		} else if !ok {
			return nil, false, nil
		}
		snapshot := make(map[int]int, len(vectors))
		for diagonal, x := range vectors {
			snapshot[diagonal] = x
		}
		trace = append(trace, snapshot)

		for diagonal := -distance; diagonal <= distance; diagonal += 2 {
			if ok, err := tick(1); err != nil {
				return nil, false, err
			} else if !ok {
				return nil, false, nil
			}
			var x int
			if diagonal == -distance || (diagonal != distance && vectors[diagonal-1] < vectors[diagonal+1]) {
				x = vectors[diagonal+1]
			} else {
				x = vectors[diagonal-1] + 1
			}
			y := x - diagonal
			for x < len(before) && y < len(after) && bytes.Equal(before[x], after[y]) {
				if ok, err := tick(1); err != nil {
					return nil, false, err
				} else if !ok {
					return nil, false, nil
				}
				x++
				y++
			}
			vectors[diagonal] = x
			if x >= len(before) && y >= len(after) {
				operations, ok, err := backtrackMyers(ctx, before, after, trace, distance, &work, maxWork)
				return operations, ok, err
			}
		}
	}
	return nil, false, nil
}

func backtrackMyers(ctx context.Context, before, after [][]byte, trace []map[int]int, distance int, work *int, maxWork int) ([]diffOperation, bool, error) {
	tick := func() (bool, error) {
		if *work >= maxWork {
			return false, nil
		}
		(*work)++
		if *work&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	x, y := len(before), len(after)
	reversed := make([]diffOperation, 0, len(before)+len(after))
	for current := distance; current > 0; current-- {
		if ok, err := tick(); err != nil {
			return nil, false, err
		} else if !ok {
			return nil, false, nil
		}
		vector := trace[current]
		diagonal := x - y
		previousDiagonal := diagonal - 1
		if diagonal == -current || (diagonal != current && vector[diagonal-1] < vector[diagonal+1]) {
			previousDiagonal = diagonal + 1
		}
		previousX := vector[previousDiagonal]
		previousY := previousX - previousDiagonal
		for x > previousX && y > previousY {
			reversed = append(reversed, diffOperation{kind: diffEqual, line: before[x-1]})
			x--
			y--
		}
		if x == previousX {
			reversed = append(reversed, diffOperation{kind: diffAdd, line: after[y-1]})
			y--
		} else {
			reversed = append(reversed, diffOperation{kind: diffDelete, line: before[x-1]})
			x--
		}
	}
	for x > 0 && y > 0 {
		reversed = append(reversed, diffOperation{kind: diffEqual, line: before[x-1]})
		x--
		y--
	}
	slices.Reverse(reversed)
	return reversed, true, nil
}

func replacementOperations(before, after [][]byte) []diffOperation {
	operations := make([]diffOperation, 0, len(before)+len(after))
	for _, line := range before {
		operations = append(operations, diffOperation{kind: diffDelete, line: line})
	}
	for _, line := range after {
		operations = append(operations, diffOperation{kind: diffAdd, line: line})
	}
	return operations
}

func appendEqualOperations(operations []diffOperation, lines [][]byte) []diffOperation {
	for _, line := range lines {
		operations = append(operations, diffOperation{kind: diffEqual, line: line})
	}
	return operations
}

func renderUnifiedPatch(path RelativePath, status string, operations []diffOperation) string {
	var patch strings.Builder
	switch status {
	case "created":
		patch.WriteString("--- /dev/null\n+++ b/")
		patch.WriteString(string(path))
		patch.WriteByte('\n')
	case "deleted":
		patch.WriteString("--- a/")
		patch.WriteString(string(path))
		patch.WriteString("\n+++ /dev/null\n")
	default:
		patch.WriteString("--- a/")
		patch.WriteString(string(path))
		patch.WriteString("\n+++ b/")
		patch.WriteString(string(path))
		patch.WriteByte('\n')
	}

	for _, hunk := range diffHunks(operations) {
		beforeStart, afterStart, beforeCount, afterCount := hunkCoordinates(operations, hunk[0], hunk[1])
		patch.WriteString("@@ -")
		patch.WriteString(formatHunkRange(beforeStart, beforeCount))
		patch.WriteString(" +")
		patch.WriteString(formatHunkRange(afterStart, afterCount))
		patch.WriteString(" @@\n")
		for _, operation := range operations[hunk[0]:hunk[1]] {
			patch.WriteByte(operation.kind)
			patch.Write(operation.line)
			if len(operation.line) == 0 || operation.line[len(operation.line)-1] != '\n' {
				patch.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return patch.String()
}

func diffHunks(operations []diffOperation) [][2]int {
	var hunks [][2]int
	for index := 0; index < len(operations); {
		for index < len(operations) && operations[index].kind == diffEqual {
			index++
		}
		if index == len(operations) {
			break
		}
		start := max(0, index-diffContextLines)
		lastChange := index
		for index < len(operations) {
			if operations[index].kind != diffEqual {
				lastChange = index
			}
			if index-lastChange > 2*diffContextLines {
				break
			}
			index++
		}
		end := min(len(operations), lastChange+1+diffContextLines)
		hunks = append(hunks, [2]int{start, end})
		index = end
	}
	return hunks
}

func hunkCoordinates(operations []diffOperation, start, end int) (int, int, int, int) {
	beforeLines, afterLines := 0, 0
	for _, operation := range operations[:start] {
		if operation.kind != diffAdd {
			beforeLines++
		}
		if operation.kind != diffDelete {
			afterLines++
		}
	}
	beforeCount, afterCount := 0, 0
	for _, operation := range operations[start:end] {
		if operation.kind != diffAdd {
			beforeCount++
		}
		if operation.kind != diffDelete {
			afterCount++
		}
	}
	beforeStart := beforeLines + 1
	afterStart := afterLines + 1
	if beforeCount == 0 {
		beforeStart = beforeLines
	}
	if afterCount == 0 {
		afterStart = afterLines
	}
	return beforeStart, afterStart, beforeCount, afterCount
}

func formatHunkRange(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func boundDiffResult(result DiffResult, limit int) (DiffResult, error) {
	if limit <= 0 {
		return DiffResult{}, fmt.Errorf("bound diff response: %w", ErrLimitExceeded)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return DiffResult{}, fmt.Errorf("measure diff response: %w", err)
	}
	if len(payload) <= limit {
		return result, nil
	}
	result.Complete = false
	result.Reason = "response_limit"
	result.Patch = ""
	payload, err = json.Marshal(result)
	if err != nil {
		return DiffResult{}, fmt.Errorf("measure diff summaries: %w", err)
	}
	if len(payload) > limit {
		return DiffResult{}, fmt.Errorf("bound diff summaries: %w", ErrLimitExceeded)
	}
	return result, nil
}
