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
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestScanDirectivesIgnoresCommentsAndPreservesQuotedArguments(t *testing.T) {
	input := []byte("# include ignored.conf;\ninclude 'conf.d/a b.conf';\nssl_password_file secrets/passwd;\n")
	directives, err := ScanDirectives(input, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []Directive{
		{Name: "include", Arguments: []string{"conf.d/a b.conf"}, Line: 2, Column: 1},
		{Name: "ssl_password_file", Arguments: []string{"secrets/passwd"}, Line: 3, Column: 1},
	}
	if !reflect.DeepEqual(directives, want) {
		t.Fatalf("ScanDirectives() = %#v, want %#v", directives, want)
	}
}

func TestScanDirectivesSupportsDoubleQuotesAndEscapes(t *testing.T) {
	input := []byte("include \"conf.d/double quoted.conf\";\ninclude conf.d/escaped\\ name\\;v1.conf;\n")
	directives, err := ScanDirectives(input, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []Directive{
		{Name: "include", Arguments: []string{"conf.d/double quoted.conf"}, Line: 1, Column: 1},
		{Name: "include", Arguments: []string{"conf.d/escaped name;v1.conf"}, Line: 2, Column: 1},
	}
	if !reflect.DeepEqual(directives, want) {
		t.Fatalf("ScanDirectives() = %#v, want %#v", directives, want)
	}
}

func TestScanDirectivesRecognizesBracesAndInlineComments(t *testing.T) {
	input := []byte("http { # include ignored.conf;\n  server { listen 80; }\n}\n")
	directives, err := ScanDirectives(input, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []Directive{
		{Name: "http", Arguments: nil, Line: 1, Column: 1},
		{Name: "server", Arguments: nil, Line: 2, Column: 3},
		{Name: "listen", Arguments: []string{"80"}, Line: 2, Column: 12},
	}
	if !reflect.DeepEqual(directives, want) {
		t.Fatalf("ScanDirectives() = %#v, want %#v", directives, want)
	}
}

func TestScanDirectivesPreservesDuplicateIncludesAndPositions(t *testing.T) {
	input := []byte("\n  include conf.d/site.conf; include conf.d/site.conf;\n")
	directives, err := ScanDirectives(input, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []Directive{
		{Name: "include", Arguments: []string{"conf.d/site.conf"}, Line: 2, Column: 3},
		{Name: "include", Arguments: []string{"conf.d/site.conf"}, Line: 2, Column: 29},
	}
	if !reflect.DeepEqual(directives, want) {
		t.Fatalf("ScanDirectives() = %#v, want %#v", directives, want)
	}
}

func TestScanDirectivesRejectsMissingSemicolon(t *testing.T) {
	if _, err := ScanDirectives([]byte("include conf.d/site.conf"), DefaultLimits()); err == nil {
		t.Fatal("ScanDirectives() error = nil, want missing terminator error")
	}
}

func TestScanDirectivesRejectsUnterminatedQuote(t *testing.T) {
	if _, err := ScanDirectives([]byte("include 'conf.d/site.conf;"), DefaultLimits()); err == nil {
		t.Fatal("ScanDirectives() error = nil, want unterminated quote error")
	}
}

func TestScanDirectivesRejectsDanglingEscape(t *testing.T) {
	if _, err := ScanDirectives([]byte("include conf.d/site.conf\\"), DefaultLimits()); err == nil {
		t.Fatal("ScanDirectives() error = nil, want dangling escape error")
	}
}

func TestScanDirectivesRejectsTokenOverLimit(t *testing.T) {
	limits := DefaultLimits()
	input := append([]byte("include "), bytes.Repeat([]byte{'a'}, limits.MaxIncludeTokenBytes+1)...)
	input = append(input, ';')
	if _, err := ScanDirectives(input, limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ScanDirectives() error = %v, want ErrLimitExceeded", err)
	}
}

func TestScanDirectivesRejectsDirectiveOverLimit(t *testing.T) {
	limits := DefaultLimits()
	input := []byte("include ")
	remaining := limits.MaxIncludeDirectiveBytes + 1 - len(input)
	for remaining > 0 {
		chunk := min(remaining, limits.MaxIncludeTokenBytes/2)
		input = append(input, bytes.Repeat([]byte{'a'}, chunk)...)
		remaining -= chunk
		if remaining > 0 {
			input = append(input, ' ')
			remaining--
		}
	}
	input = append(input, ';')
	if _, err := ScanDirectives(input, limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ScanDirectives() error = %v, want ErrLimitExceeded", err)
	}
}

func TestScanDirectivesTreatsCommentAsTokenBoundary(t *testing.T) {
	directives, err := ScanDirectives([]byte("directive one# comment\ntwo;\n"), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []Directive{{Name: "directive", Arguments: []string{"one", "two"}, Line: 1, Column: 1}}
	if !reflect.DeepEqual(directives, want) {
		t.Fatalf("ScanDirectives() = %#v, want %#v", directives, want)
	}
}

func TestExpandIncludeGraphResolvesManifestEntriesAndSensitiveReferences(t *testing.T) {
	limits := DefaultLimits()
	entry := mustIncludePath(t, "nginx.conf")
	entries := []RawEntry{
		{Path: entry, Type: EntryType("regular")},
		{Path: mustIncludePath(t, "conf.d/site.conf"), Type: EntryType("regular")},
		{Path: mustIncludePath(t, "mime.types"), Type: EntryType("regular")},
		{Path: mustIncludePath(t, "secrets/server.pem"), Type: EntryType("regular")},
	}
	contents := map[RelativePath][]byte{
		entry:                                  []byte("include conf.d/site.conf;\ninclude /etc/nginx/mime.types;\nssl_certificate secrets/server.pem;\n"),
		mustIncludePath(t, "conf.d/site.conf"): []byte("server {}\n"),
		mustIncludePath(t, "mime.types"):       []byte("types {}\n"),
	}
	readPaths := make(map[RelativePath]int)
	read := func(_ context.Context, path RelativePath) ([]byte, error) {
		readPaths[path]++
		content, ok := contents[path]
		if !ok {
			return nil, errors.New("unexpected read")
		}
		return content, nil
	}

	graph, included, sensitive, err := ExpandIncludeGraph(context.Background(), entry, entries, read, limits)
	if err != nil {
		t.Fatal(err)
	}
	wantEdges := []Dependency{
		{Source: entry, Line: 1, Column: 1, DisplayValue: "conf.d/site.conf", Target: mustIncludePath(t, "conf.d/site.conf"), Status: DependencyStatus("resolved")},
		{Source: entry, Line: 2, Column: 1, DisplayValue: "mime.types", Target: mustIncludePath(t, "mime.types"), Status: DependencyStatus("resolved")},
	}
	if !reflect.DeepEqual(graph.Edges, wantEdges) {
		t.Fatalf("ExpandIncludeGraph() edges = %#v, want %#v", graph.Edges, wantEdges)
	}
	wantIncluded := map[RelativePath]struct{}{
		mustIncludePath(t, "conf.d/site.conf"): {},
		mustIncludePath(t, "mime.types"):       {},
	}
	if !reflect.DeepEqual(included, wantIncluded) {
		t.Fatalf("ExpandIncludeGraph() included = %#v, want %#v", included, wantIncluded)
	}
	wantSensitive := map[RelativePath]struct{}{
		mustIncludePath(t, "secrets/server.pem"): {},
	}
	if !reflect.DeepEqual(sensitive, wantSensitive) {
		t.Fatalf("ExpandIncludeGraph() sensitive = %#v, want %#v", sensitive, wantSensitive)
	}
	if readPaths[mustIncludePath(t, "secrets/server.pem")] != 0 {
		t.Fatal("ExpandIncludeGraph() read sensitive referenced content")
	}
}

func TestExpandIncludeGraphBoundsResolutionToSortedManifest(t *testing.T) {
	limits := DefaultLimits()
	entry := mustIncludePath(t, "nginx.conf")
	regularPaths := []RelativePath{
		entry,
		mustIncludePath(t, "conf.d/alpha.conf"),
		mustIncludePath(t, "conf.d/beta.conf"),
		mustIncludePath(t, "conf.d/é.conf"),
	}
	entries := make([]RawEntry, 0, len(regularPaths)+2)
	for _, regularPath := range regularPaths {
		entries = append(entries, RawEntry{Path: regularPath, Type: EntryType("regular")})
	}
	entries = append(entries,
		RawEntry{Path: mustIncludePath(t, "conf.d/link.conf"), Type: EntryType("symlink")},
		RawEntry{Path: mustIncludePath(t, "conf.d/pipe.conf"), Type: EntryType("special")},
	)
	contents := map[RelativePath][]byte{
		entry: []byte("include conf.d/*.conf;\ninclude conf.d/alpha.conf;\ninclude conf.d/alpha.conf;\ninclude missing.conf;\ninclude /opt/private.conf;\ninclude ../escape.conf;\ninclude conf.d/$tenant.conf;\n"),
	}
	for _, regularPath := range regularPaths[1:] {
		contents[regularPath] = []byte("events {}\n")
	}
	readPaths := make(map[RelativePath]int)
	read := func(_ context.Context, path RelativePath) ([]byte, error) {
		readPaths[path]++
		content, ok := contents[path]
		if !ok {
			return nil, errors.New("unexpected read")
		}
		return content, nil
	}

	graph, included, _, err := ExpandIncludeGraph(context.Background(), entry, entries, read, limits)
	if err != nil {
		t.Fatal(err)
	}
	wantEdges := []Dependency{
		{Source: entry, Line: 1, Column: 1, DisplayValue: "conf.d/*.conf", Target: mustIncludePath(t, "conf.d/alpha.conf"), Status: DependencyStatus("resolved")},
		{Source: entry, Line: 1, Column: 1, DisplayValue: "conf.d/*.conf", Target: mustIncludePath(t, "conf.d/beta.conf"), Status: DependencyStatus("resolved")},
		{Source: entry, Line: 1, Column: 1, DisplayValue: "conf.d/*.conf", Target: mustIncludePath(t, "conf.d/link.conf"), Status: DependencyStatus("symlink")},
		{Source: entry, Line: 1, Column: 1, DisplayValue: "conf.d/*.conf", Target: mustIncludePath(t, "conf.d/pipe.conf"), Status: DependencyStatus("special")},
		{Source: entry, Line: 1, Column: 1, DisplayValue: "conf.d/*.conf", Target: mustIncludePath(t, "conf.d/é.conf"), Status: DependencyStatus("resolved")},
		{Source: entry, Line: 2, Column: 1, DisplayValue: "conf.d/alpha.conf", Target: mustIncludePath(t, "conf.d/alpha.conf"), Status: DependencyStatus("resolved")},
		{Source: entry, Line: 4, Column: 1, DisplayValue: "missing.conf", Target: mustIncludePath(t, "missing.conf"), Status: DependencyStatus("missing")},
		{Source: entry, Line: 5, Column: 1, DisplayValue: "[external]", Status: DependencyStatus("external")},
		{Source: entry, Line: 6, Column: 1, DisplayValue: "[external]", Status: DependencyStatus("external")},
		{Source: entry, Line: 7, Column: 1, DisplayValue: "[unresolved]", Status: DependencyStatus("unresolved")},
	}
	if !reflect.DeepEqual(graph.Edges, wantEdges) {
		t.Fatalf("ExpandIncludeGraph() edges = %#v, want %#v", graph.Edges, wantEdges)
	}
	if graph.MissingCount != 1 || graph.ExternalCount != 2 {
		t.Fatalf("ExpandIncludeGraph() counts = missing:%d external:%d, want 1 and 2", graph.MissingCount, graph.ExternalCount)
	}
	if len(included) != 3 {
		t.Fatalf("ExpandIncludeGraph() included count = %d, want 3", len(included))
	}
	for _, path := range []RelativePath{
		mustIncludePath(t, "conf.d/link.conf"),
		mustIncludePath(t, "conf.d/pipe.conf"),
		mustIncludePath(t, "missing.conf"),
	} {
		if readPaths[path] != 0 {
			t.Fatalf("ExpandIncludeGraph() read non-regular target %q", path)
		}
	}
}

func TestExpandIncludeGraphDetectsFixtureCycle(t *testing.T) {
	entry := mustIncludePath(t, "nginx.conf")
	alpha := mustIncludePath(t, "conf.d/alpha.conf")
	beta := mustIncludePath(t, "conf.d/beta.conf")
	mimeTypes := mustIncludePath(t, "mime.types")
	serverKey := mustIncludePath(t, "secrets/server.pem")
	users := mustIncludePath(t, "secrets/users.htpasswd")
	entries := []RawEntry{
		{Path: entry, Type: EntryType("regular")},
		{Path: alpha, Type: EntryType("regular")},
		{Path: beta, Type: EntryType("regular")},
		{Path: mimeTypes, Type: EntryType("regular")},
		{Path: serverKey, Type: EntryType("regular")},
		{Path: users, Type: EntryType("regular")},
	}
	fixtureNames := map[RelativePath]string{
		entry: "graph-root.conf",
		alpha: "graph-alpha.conf",
		beta:  "graph-beta.conf",
	}
	readPaths := make(map[RelativePath]int)
	read := func(_ context.Context, path RelativePath) ([]byte, error) {
		readPaths[path]++
		if path == mimeTypes {
			return []byte("types {}\n"), nil
		}
		name, ok := fixtureNames[path]
		if !ok {
			return nil, errors.New("unexpected read")
		}
		return os.ReadFile("testdata/include/" + name)
	}

	graph, included, sensitive, err := ExpandIncludeGraph(context.Background(), entry, entries, read, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	wantCycles := [][]RelativePath{{alpha, beta, alpha}}
	if !reflect.DeepEqual(graph.Cycles, wantCycles) {
		t.Fatalf("ExpandIncludeGraph() cycles = %#v, want %#v", graph.Cycles, wantCycles)
	}
	var cycleEdge *Dependency
	for index := range graph.Edges {
		if graph.Edges[index].Cycle {
			cycleEdge = &graph.Edges[index]
			break
		}
	}
	if cycleEdge == nil || cycleEdge.Source != beta || cycleEdge.Target != alpha || cycleEdge.Status != DependencyCycle {
		t.Fatalf("ExpandIncludeGraph() cycle edge = %#v, want beta -> alpha cycle", cycleEdge)
	}
	if graph.MissingCount != 1 || graph.ExternalCount != 2 {
		t.Fatalf("ExpandIncludeGraph() counts = missing:%d external:%d, want 1 and 2", graph.MissingCount, graph.ExternalCount)
	}
	if len(included) != 3 {
		t.Fatalf("ExpandIncludeGraph() included count = %d, want 3", len(included))
	}
	if _, ok := sensitive[serverKey]; !ok {
		t.Fatalf("ExpandIncludeGraph() sensitive = %#v, want server key", sensitive)
	}
	if _, ok := sensitive[users]; !ok {
		t.Fatalf("ExpandIncludeGraph() sensitive = %#v, want user file", sensitive)
	}
	if readPaths[serverKey] != 0 || readPaths[users] != 0 {
		t.Fatal("ExpandIncludeGraph() read sensitive referenced content")
	}
}

func TestExpandIncludeGraphHonorsCanceledContextBeforeRead(t *testing.T) {
	entry := mustIncludePath(t, "nginx.conf")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	readCalled := false
	_, _, _, err := ExpandIncludeGraph(ctx, entry, []RawEntry{{Path: entry, Type: EntryType("regular")}}, func(context.Context, RelativePath) ([]byte, error) {
		readCalled = true
		return nil, nil
	}, DefaultLimits())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExpandIncludeGraph() error = %v, want context.Canceled", err)
	}
	if readCalled {
		t.Fatal("ExpandIncludeGraph() called read after cancellation")
	}
}

func TestExpandIncludeGraphHonorsCancellationAfterRead(t *testing.T) {
	entry := mustIncludePath(t, "nginx.conf")
	ctx, cancel := context.WithCancel(context.Background())
	_, _, _, err := ExpandIncludeGraph(ctx, entry, []RawEntry{{Path: entry, Type: EntryType("regular")}}, func(context.Context, RelativePath) ([]byte, error) {
		cancel()
		return []byte("events {}\n"), nil
	}, DefaultLimits())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExpandIncludeGraph() error = %v, want context.Canceled", err)
	}
}

func TestExpandIncludeGraphPreservesDistinctUnresolvedDirectives(t *testing.T) {
	entry := mustIncludePath(t, "nginx.conf")
	content := []byte("include one.conf two.conf;\ninclude three.conf four.conf;\n")
	graph, _, _, err := ExpandIncludeGraph(context.Background(), entry, []RawEntry{{Path: entry, Type: EntryType("regular")}}, func(context.Context, RelativePath) ([]byte, error) {
		return content, nil
	}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 2 || graph.Edges[0].Status != DependencyUnresolved || graph.Edges[1].Status != DependencyUnresolved {
		t.Fatalf("ExpandIncludeGraph() edges = %#v, want two unresolved edges", graph.Edges)
	}
}

func TestExpandIncludeGraphRejectsUnmanagedEntry(t *testing.T) {
	entry := mustIncludePath(t, "nginx.conf")
	_, _, _, err := ExpandIncludeGraph(context.Background(), entry, nil, func(context.Context, RelativePath) ([]byte, error) {
		return nil, nil
	}, DefaultLimits())
	if !errors.Is(err, ErrEntryNotManaged) {
		t.Fatalf("ExpandIncludeGraph() error = %v, want ErrEntryNotManaged", err)
	}
}

func TestExpandIncludeGraphRejectsDepth65(t *testing.T) {
	paths := make([]RelativePath, 65)
	paths[0] = mustIncludePath(t, "nginx.conf")
	for index := 1; index < len(paths); index++ {
		paths[index] = mustIncludePath(t, fmt.Sprintf("chain/%03d.conf", index))
	}
	entries := make([]RawEntry, len(paths))
	contents := make(map[RelativePath][]byte, len(paths))
	for index, path := range paths {
		entries[index] = RawEntry{Path: path, Type: EntryType("regular")}
		if index+1 < len(paths) {
			contents[path] = []byte("include " + string(paths[index+1]) + ";\n")
		} else {
			contents[path] = []byte("events {}\n")
		}
	}
	read := func(_ context.Context, path RelativePath) ([]byte, error) {
		return contents[path], nil
	}
	_, _, _, err := ExpandIncludeGraph(context.Background(), paths[0], entries, read, DefaultLimits())
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ExpandIncludeGraph() error = %v, want ErrLimitExceeded", err)
	}
}

func TestExpandIncludeGraphRejectsEdge16385(t *testing.T) {
	entry := mustIncludePath(t, "nginx.conf")
	limits := DefaultLimits()
	var content strings.Builder
	for index := 0; index <= limits.MaxIncludeEdges; index++ {
		fmt.Fprintf(&content, "include missing/%05d.conf;\n", index)
	}
	read := func(context.Context, RelativePath) ([]byte, error) {
		return []byte(content.String()), nil
	}
	_, _, _, err := ExpandIncludeGraph(context.Background(), entry, []RawEntry{{Path: entry, Type: EntryType("regular")}}, read, limits)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ExpandIncludeGraph() error = %v, want ErrLimitExceeded", err)
	}
}

func mustIncludePath(t *testing.T, raw string) RelativePath {
	t.Helper()
	path, err := ParseRelativePath(raw, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return path
}
