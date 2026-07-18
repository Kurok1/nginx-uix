/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSearchReturnsFirstFiveHundredInStableOrder(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	content := []byte(strings.Repeat("# proxy_pass\n", 501))
	_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-search-limit"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: content, IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Search(context.Background(), workspace.ID, "proxy_pass")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 500 || result.Complete {
		t.Fatalf("Search() = %#v", result)
	}
	for index, match := range result.Matches {
		if match.Path != "conf.d/site.conf" || match.Line != index+1 || match.Column != 3 || match.Snippet != "# proxy_pass" {
			t.Fatalf("match[%d] = %#v", index, match)
		}
	}
}

func TestSearchRejectsEmptyInvalidUTF8AndOversizeQueries(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	queries := []string{"", string([]byte{0xff}), strings.Repeat("x", 257)}
	for _, query := range queries {
		if _, err := fixture.service.Search(context.Background(), workspace.ID, query); err == nil {
			t.Fatalf("Search(%d-byte query) error = nil", len(query))
		}
	}
}

func TestSearchRejectsUnavailableService(t *testing.T) {
	var service *Service
	if _, err := service.Search(context.Background(), WorkspaceID("unused"), "needle"); err == nil {
		t.Fatal("Search(nil service) error = nil")
	}
}

func TestSearchTreatsMetacharactersAsLiteralBytes(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-search-literal"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("# literal .*[]? value\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Search(context.Background(), workspace.ID, ".*[]?")
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Column != 11 {
		t.Fatalf("Search(literal) = %#v, %v", result, err)
	}
}

func TestSearchFindsNonOverlappingOccurrencesOnOneLine(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-search-overlap"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("# aaaaa\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Search(context.Background(), workspace.ID, "aa")
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchMatch{{Path: "conf.d/site.conf", Line: 1, Column: 3, Snippet: "# aaaaa"}, {Path: "conf.d/site.conf", Line: 1, Column: 5, Snippet: "# aaaaa"}}
	if !reflect.DeepEqual(result.Matches, want) || !result.Complete {
		t.Fatalf("Search(non-overlap) = %#v, want %#v", result, want)
	}
}

func TestSearchUsesOneBasedRuneColumnsAndStripsCRLF(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-search-column"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte("# 世界target\r\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Search(context.Background(), workspace.ID, "target")
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("Search(column) = %#v, %v", result, err)
	}
	match := result.Matches[0]
	if match.Line != 1 || match.Column != 5 || match.Snippet != "# 世界target" {
		t.Fatalf("match = %#v", match)
	}
}

func TestSearchClipsSnippetToTwoHundredFortyUnicodeScalars(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	line := "# " + strings.Repeat("界", 200) + "needle" + strings.Repeat("文", 200)
	_, err := fixture.service.ReplaceFile(context.Background(), Actor{UserID: 7, RequestID: "req-search-snippet"}, workspace.ID, ReplaceFileInput{
		Path: mustRelativePath(t, "conf.d/site.conf"), Content: []byte(line + "\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Search(context.Background(), workspace.ID, "needle")
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("Search(snippet) = %#v, %v", result, err)
	}
	snippet := result.Matches[0].Snippet
	if !utf8.ValidString(snippet) || utf8.RuneCountInString(snippet) != 240 || !strings.Contains(snippet, "needle") {
		t.Fatalf("snippet rune_count=%d valid=%t contains=%t", utf8.RuneCountInString(snippet), utf8.ValidString(snippet), strings.Contains(snippet, "needle"))
	}
}

func TestSearchReadsManagedFilesOnly(t *testing.T) {
	fixture := newServiceFixture(t)
	writeFixtureFile(t, filepath.Join(fixture.production.productionRoot, "private.key"), "needle\n", 0o600)
	workspace := fixture.mustCreate(t)
	result, err := fixture.service.Search(context.Background(), workspace.ID, "needle")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 0 || !result.Complete {
		t.Fatalf("Search(sensitive) = %#v", result)
	}
}

func TestSearchStableOrder(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	first, err := fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-search-z"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/z.conf"), Content: []byte("# needle needle\n"), IfMatch: workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CreateFile(context.Background(), Actor{UserID: 7, RequestID: "req-search-a"}, workspace.ID, CreateFileInput{
		Path: mustRelativePath(t, "conf.d/a.conf"), Content: []byte("# needle\n"), IfMatch: first.Workspace.ETag(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Search(context.Background(), workspace.ID, "needle")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 3 || !slices.IsSortedFunc(result.Matches, compareSearchMatches) {
		t.Fatalf("matches are not stable: %#v", result.Matches)
	}
	second, err := fixture.service.Search(context.Background(), workspace.ID, "needle")
	if err != nil || !reflect.DeepEqual(result, second) {
		t.Fatalf("Search() unstable: %#v then %#v, %v", result, second, err)
	}
}

func TestSearchReadsStaleAndNeedsAttentionWorkspaces(t *testing.T) {
	for _, state := range []WorkspaceState{StateStale, StateNeedsAttention} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newServiceFixture(t)
			workspace := fixture.mustCreate(t)
			setReviewWorkspaceState(t, fixture, workspace.ID, state)
			if _, err := fixture.service.Search(context.Background(), workspace.ID, "server"); err != nil {
				t.Fatalf("Search(%s) error = %v", state, err)
			}
		})
	}
}

func TestSearchFailsClosedOnDraftTamper(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	writeExistingFixtureFile(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "needle\n", 0o600)
	_, err := fixture.service.Search(context.Background(), workspace.ID, "needle")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Search(tamper) error = %v, want ErrConflict", err)
	}
	if err != nil && strings.Contains(err.Error(), "needle") {
		t.Fatalf("Search(tamper) error retained query: %v", err)
	}
}

func TestSearchHonorsCancellation(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.Search(ctx, workspace.ID, "server"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search(canceled) error = %v", err)
	}
}

func TestSearchManifestRejectsChangedManagedContent(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	root, err := OpenScopedRoot(fixture.path(workspace.ID, "draft"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close(draft) error = %v", err)
		}
	}()
	workspaceRoot := fixture.openWorkspace(t, workspace.ID)
	manifest, err := ReadControlManifest(context.Background(), workspaceRoot, DefaultLimits())
	closeErr := workspaceRoot.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read manifest = %v, close = %v", err, closeErr)
	}
	writeExistingFixtureFile(t, fixture.path(workspace.ID, "draft/conf.d/site.conf"), "needle\n", 0o600)
	if _, err := SearchManifest(context.Background(), root, manifest, "needle", DefaultLimits()); !errors.Is(err, ErrConflict) {
		t.Fatalf("SearchManifest(tamper) error = %v, want ErrConflict", err)
	}
}

func compareSearchMatches(left, right SearchMatch) int {
	if byPath := bytes.Compare([]byte(left.Path), []byte(right.Path)); byPath != 0 {
		return byPath
	}
	if left.Line != right.Line {
		return left.Line - right.Line
	}
	return left.Column - right.Column
}
