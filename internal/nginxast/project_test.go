/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import (
	"errors"
	"reflect"
	"testing"
)

func TestBuildProjectExpandsIncludesWithSemanticContext(t *testing.T) {
	t.Parallel()

	files := []SourceFile{
		{Path: "conf.d/site.conf", Source: "upstream backend { server 127.0.0.1:8080; }\nserver { location / { proxy_pass http://backend; } }\n"},
		{Path: "nginx.conf", Source: "events {}\nhttp {\n    include conf.d/*.conf;\n}\n"},
	}
	edges := []IncludeEdge{{
		Source: "nginx.conf", Line: 3, Column: 5, Target: "conf.d/site.conf", Status: IncludeResolved,
	}}
	project, err := BuildProject(files, edges, DefaultProjectLimits())
	if err != nil {
		t.Fatalf("BuildProject() error = %v", err)
	}
	if !project.Complete || len(project.Diagnostics) != 0 {
		t.Fatalf("project completeness = %t, diagnostics = %#v", project.Complete, project.Diagnostics)
	}

	httpRef := requireProjectBlock(t, project, "nginx.conf", "http")
	upstreamRef := requireProjectBlock(t, project, "conf.d/site.conf", "upstream")
	serverRef := requireProjectBlock(t, project, "conf.d/site.conf", "server")
	locationRef := requireProjectBlock(t, project, "conf.d/site.conf", "location")
	proxyRef := requireProjectDirective(t, project, "conf.d/site.conf", "proxy_pass")

	assertPlacement(t, httpRef, ContextMain, "")
	assertPlacement(t, upstreamRef, ContextHTTP, httpRef.ID)
	assertPlacement(t, serverRef, ContextHTTP, httpRef.ID)
	assertPlacement(t, locationRef, ContextServer, serverRef.ID)
	assertPlacement(t, proxyRef, ContextLocation, locationRef.ID)
	if upstreamRef.ID == "" || upstreamRef.ID != NodeID("conf.d/site.conf", upstreamRef.Node) {
		t.Fatalf("upstream ID = %q", upstreamRef.ID)
	}

	second, err := BuildProject(files, edges, DefaultProjectLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project.safeIdentityProjection(), second.safeIdentityProjection()) {
		t.Fatalf("project identities are not deterministic:\nfirst=%#v\nsecond=%#v", project.Nodes, second.Nodes)
	}
}

func TestBuildProjectMarksMultipleSemanticPlacementsAmbiguous(t *testing.T) {
	t.Parallel()

	files := []SourceFile{
		{Path: "nginx.conf", Source: "include shared.conf;\nhttp {\n    include shared.conf;\n}\n"},
		{Path: "shared.conf", Source: "server { listen 8080; }\n"},
	}
	edges := []IncludeEdge{
		{Source: "nginx.conf", Line: 1, Column: 1, Target: "shared.conf", Status: IncludeResolved},
		{Source: "nginx.conf", Line: 3, Column: 5, Target: "shared.conf", Status: IncludeResolved},
	}
	project, err := BuildProject(files, edges, DefaultProjectLimits())
	if err != nil {
		t.Fatalf("BuildProject() error = %v", err)
	}
	server := requireProjectBlock(t, project, "shared.conf", "server")
	if !server.Ambiguous || len(server.Placements) != 2 {
		t.Fatalf("server placements = %#v, ambiguous = %t", server.Placements, server.Ambiguous)
	}
	if !hasProjectDiagnostic(project.Diagnostics, DiagnosticAmbiguousContext, "shared.conf") {
		t.Fatalf("diagnostics = %#v, want ambiguous context", project.Diagnostics)
	}
}

func TestBuildProjectReportsUnresolvedAndCyclicIncludes(t *testing.T) {
	t.Parallel()

	files := []SourceFile{
		{Path: "nginx.conf", Source: "http {\n include a.conf;\n include missing.conf;\n}\n"},
		{Path: "a.conf", Source: "include nginx.conf;\n"},
	}
	edges := []IncludeEdge{
		{Source: "nginx.conf", Line: 2, Column: 2, Target: "a.conf", Status: IncludeResolved},
		{Source: "nginx.conf", Line: 3, Column: 2, Target: "missing.conf", Status: IncludeMissing},
		{Source: "a.conf", Line: 1, Column: 1, Target: "nginx.conf", Status: IncludeCycle},
	}
	project, err := BuildProject(files, edges, DefaultProjectLimits())
	if err != nil {
		t.Fatalf("BuildProject() error = %v", err)
	}
	if project.Complete {
		t.Fatal("project.Complete = true, want false")
	}
	for _, want := range []DiagnosticCode{DiagnosticIncludeMissing, DiagnosticIncludeCycle} {
		if !hasProjectDiagnostic(project.Diagnostics, want, "") {
			t.Fatalf("diagnostics = %#v, want %q", project.Diagnostics, want)
		}
	}
}

func TestBuildProjectKeepsParseFailureAsSafeDiagnostic(t *testing.T) {
	t.Parallel()

	files := []SourceFile{
		{Path: "nginx.conf", Source: "http { include bad.conf; }\n"},
		{Path: "bad.conf", Source: "server { location / {\n"},
	}
	edges := []IncludeEdge{{
		Source: "nginx.conf", Line: 1, Column: 8, Target: "bad.conf", Status: IncludeResolved,
	}}
	project, err := BuildProject(files, edges, DefaultProjectLimits())
	if err != nil {
		t.Fatalf("BuildProject() error = %v", err)
	}
	if project.Complete || project.Documents["bad.conf"].Error == nil ||
		!hasProjectDiagnostic(project.Diagnostics, DiagnosticParseFailed, "bad.conf") {
		t.Fatalf("project = %#v", project)
	}
}

func TestBuildProjectClassifiesProjectBounds(t *testing.T) {
	t.Parallel()

	limits := DefaultProjectLimits()
	limits.MaxNodes = 1
	_, err := BuildProject(
		[]SourceFile{{Path: "nginx.conf", Source: "events {}\nhttp {}\n"}},
		nil,
		limits,
	)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("BuildProject() error = %v, want ErrLimitExceeded", err)
	}
}

func requireProjectBlock(t *testing.T, project *Project, path, name string) *NodeRef {
	t.Helper()
	for _, reference := range project.Nodes {
		block, ok := reference.Node.(*Block)
		if ok && reference.Path == path && block.Name.Value == name {
			return reference
		}
	}
	t.Fatalf("block %s:%s not found in %#v", path, name, project.Nodes)
	return nil
}

func requireProjectDirective(t *testing.T, project *Project, path, name string) *NodeRef {
	t.Helper()
	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*Directive)
		if ok && reference.Path == path && directive.Name.Value == name {
			return reference
		}
	}
	t.Fatalf("directive %s:%s not found in %#v", path, name, project.Nodes)
	return nil
}

func assertPlacement(t *testing.T, reference *NodeRef, context ContextKind, parentID string) {
	t.Helper()
	if reference.Ambiguous || len(reference.Placements) != 1 ||
		reference.Placements[0].Context != context || reference.Placements[0].ParentID != parentID {
		t.Fatalf("reference %q placements = %#v, ambiguous = %t; want %q/%q", reference.ID, reference.Placements, reference.Ambiguous, context, parentID)
	}
}

func hasProjectDiagnostic(diagnostics []ProjectDiagnostic, code DiagnosticCode, path string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && (path == "" || diagnostic.Path == path) {
			return true
		}
	}
	return false
}
