/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package config

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
)

// DraftFile is one verified managed file in a point-in-time draft snapshot.
type DraftFile struct {
	Path          RelativePath
	Content       []byte
	ContentDigest Digest
	LineEnding    string
}

// DraftSnapshot is a complete immutable projection consumed by syntax-aware domains.
type DraftSnapshot struct {
	Workspace     Workspace
	WorkspaceETag string
	Entries       []Entry
	Dependencies  []Dependency
	Files         []DraftFile
}

// DraftSnapshot reads all managed draft files while holding one shared workspace lock.
func (s *Service) DraftSnapshot(
	ctx context.Context,
	id WorkspaceID,
) (_ DraftSnapshot, returnErr error) {
	workspace, root, lock, manifest, _, err := s.openVerifiedWorkspace(ctx, id, LockShared, workspaceAccessReadyRead)
	if err != nil {
		return DraftSnapshot{}, fmt.Errorf("read draft snapshot: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("release draft snapshot lock", lock.Close()))
		returnErr = errors.Join(returnErr, wrapServiceError("close draft snapshot workspace", root.Close()))
	}()

	draft, err := OpenScopedRoot(root.path + "/draft")
	if err != nil {
		return DraftSnapshot{}, fmt.Errorf("read draft snapshot: open draft: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close draft snapshot", draft.Close()))
	}()

	files := make([]DraftFile, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if entry.Type != EntryRegular || entry.Class != EntryManagedText {
			continue
		}
		content, information, err := draft.ReadRegular(ctx, entry.Path, s.limits.MaxFileBytes)
		if err != nil {
			return DraftSnapshot{}, fmt.Errorf("read draft snapshot file: %w", err)
		}
		if information.Mode().Perm() != draftManagedFileMode || int64(len(content)) != entry.Size ||
			Digest(sha256.Sum256(content)) != entry.ContentDigest {
			return DraftSnapshot{}, fmt.Errorf("read draft snapshot file: %w", ErrConflict)
		}
		files = append(files, DraftFile{
			Path: entry.Path, Content: slices.Clone(content), ContentDigest: entry.ContentDigest,
			LineEnding: detectLineEnding(content),
		})
	}
	return DraftSnapshot{
		Workspace: workspace, WorkspaceETag: workspace.ETag(),
		Entries: slices.Clone(manifest.Entries), Dependencies: slices.Clone(manifest.Dependencies), Files: files,
	}, nil
}
