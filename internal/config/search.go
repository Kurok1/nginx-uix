/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

const searchSnippetScalars = 240

var errSearchQueryInvalid = errors.New("search query invalid")

// SearchMatch identifies one literal occurrence in a managed draft file.
type SearchMatch struct {
	Path    RelativePath `json:"path"`
	Line    int          `json:"line"`
	Column  int          `json:"column"`
	Snippet string       `json:"snippet"`
}

// SearchResult contains stable bounded literal matches derived from a draft.
type SearchResult struct {
	Matches  []SearchMatch `json:"matches"`
	Complete bool          `json:"complete"`
}

// Search derives literal managed-draft matches without persisting query or snippet data.
func (s *Service) Search(ctx context.Context, id WorkspaceID, query string) (_ SearchResult, returnErr error) {
	if ctx == nil || s == nil {
		return SearchResult{}, fmt.Errorf("search workspace draft: service is unavailable")
	}
	if err := validateSearchQuery(query, s.limits); err != nil {
		return SearchResult{}, fmt.Errorf("search workspace draft: %w", err)
	}
	review, err := s.openReviewWorkspace(ctx, id, false)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search workspace draft: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeReviewWorkspace(review))
	}()
	draft, err := OpenScopedRoot(review.root.path + "/draft")
	if err != nil {
		return SearchResult{}, fmt.Errorf("search workspace draft: open draft: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapServiceError("close searched draft", draft.Close()))
	}()

	result, err := SearchManifest(ctx, draft, review.draftManifest, query, s.limits)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search workspace draft: %w", err)
	}
	if err := verifyManagedTreeDigest(ctx, review.root.path, "draft", review.draftManifest, s.limits, draftManagedFileMode, review.workspace.DraftDigest); err != nil {
		return SearchResult{}, fmt.Errorf("search workspace draft: verify draft: %w", err)
	}
	return result, nil
}

// SearchManifest searches managed regular files in raw path-byte order.
func SearchManifest(ctx context.Context, root *ScopedRoot, manifest Manifest, query string, limits Limits) (SearchResult, error) {
	if ctx == nil || root == nil {
		return SearchResult{}, fmt.Errorf("search manifest: unavailable root")
	}
	if err := validateSearchQuery(query, limits); err != nil {
		return SearchResult{}, fmt.Errorf("search manifest: %w", err)
	}
	if limits.MaxSearchMatches <= 0 || limits.MaxFileBytes <= 0 {
		return SearchResult{}, fmt.Errorf("search manifest: %w", ErrLimitExceeded)
	}
	if err := manifest.Validate(limits); err != nil {
		return SearchResult{}, fmt.Errorf("search manifest: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}

	entries := slices.Clone(manifest.Entries)
	slices.SortFunc(entries, func(left, right Entry) int {
		return bytes.Compare([]byte(left.Path), []byte(right.Path))
	})
	result := SearchResult{Matches: make([]SearchMatch, 0, min(limits.MaxSearchMatches, 64)), Complete: true}
	queryBytes := []byte(query)
	for _, entry := range entries {
		if entry.Type != EntryRegular || entry.Class != EntryManagedText {
			continue
		}
		if err := ctx.Err(); err != nil {
			return SearchResult{}, err
		}
		content, err := readReviewFile(ctx, root, entry, limits, draftManagedFileMode)
		if err != nil {
			return SearchResult{}, fmt.Errorf("search manifest: read managed file: %w", err)
		}
		complete, err := searchContent(ctx, entry.Path, content, queryBytes, limits.MaxSearchMatches, &result.Matches)
		if err != nil {
			return SearchResult{}, err
		}
		if !complete {
			result.Complete = false
			return result, nil
		}
	}
	return result, nil
}

func validateSearchQuery(query string, limits Limits) error {
	if query == "" || !utf8.ValidString(query) {
		return errSearchQueryInvalid
	}
	if limits.MaxSearchQueryBytes <= 0 || len(query) > limits.MaxSearchQueryBytes {
		return ErrLimitExceeded
	}
	return nil
}

func searchContent(ctx context.Context, path RelativePath, content, query []byte, limit int, matches *[]SearchMatch) (bool, error) {
	lineNumber := 1
	for start := 0; start < len(content); lineNumber++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		end := bytes.IndexByte(content[start:], '\n')
		var line []byte
		if end < 0 {
			line = content[start:]
			start = len(content)
		} else {
			end += start
			line = content[start:end]
			start = end + 1
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		for offset := 0; offset <= len(line)-len(query); {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			relative := bytes.Index(line[offset:], query)
			if relative < 0 {
				break
			}
			matchOffset := offset + relative
			if len(*matches) == limit {
				return false, nil
			}
			*matches = append(*matches, SearchMatch{
				Path: path, Line: lineNumber, Column: utf8.RuneCount(line[:matchOffset]) + 1,
				Snippet: clippedSearchSnippet(line, matchOffset, query),
			})
			offset = matchOffset + len(query)
		}
	}
	return true, nil
}

func clippedSearchSnippet(line []byte, matchOffset int, query []byte) string {
	runes := []rune(string(line))
	if len(runes) <= searchSnippetScalars {
		return string(runes)
	}
	matchStart := utf8.RuneCount(line[:matchOffset])
	queryRunes := utf8.RuneCount(query)
	if queryRunes >= searchSnippetScalars {
		end := min(len(runes), matchStart+searchSnippetScalars)
		start := max(0, end-searchSnippetScalars)
		return string(runes[start:end])
	}
	availableContext := searchSnippetScalars - queryRunes
	start := max(0, matchStart-availableContext/2)
	end := start + searchSnippetScalars
	if end > len(runes) {
		end = len(runes)
		start = end - searchSnippetScalars
	}
	return string(runes[start:end])
}
