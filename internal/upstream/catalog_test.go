/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package upstream

import (
	"testing"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

func TestBuildCatalogReadsKnownServerFieldsAndPreservesUnknownSyntax(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: "http {\n" +
			"    upstream backend {\n" +
			"        zone backend 64k;\n" +
			"        server 127.0.0.1:8080 weight=5 max_fails=0 fail_timeout=30s backup resolve;\n" +
			"        server [2001:db8::1]:8443 down;\n" +
			"        server unix:/run/backend.sock;\n" +
			"    }\n" +
			"    server { location / { proxy_pass http://backend/api/; } }\n" +
			"}\n",
	}}, nil)

	catalog := BuildCatalog(project)
	if len(catalog.Upstreams) != 1 {
		t.Fatalf("upstreams = %#v", catalog.Upstreams)
	}
	group := catalog.Upstreams[0]
	if group.Name != "backend" || !group.Editable || len(group.PreservedDirectives) != 1 ||
		group.PreservedDirectives[0].Name != "zone" || len(group.Servers) != 3 {
		t.Fatalf("upstream = %#v", group)
	}

	first := group.Servers[0]
	if first.Endpoint.Address != "127.0.0.1" || first.Endpoint.Port == nil || *first.Endpoint.Port != 8080 ||
		first.Weight == nil || *first.Weight != 5 || first.MaxFails == nil || *first.MaxFails != 0 ||
		first.FailTimeout == nil || *first.FailTimeout != "30s" || !first.Backup || first.Down ||
		!first.Editable {
		t.Fatalf("first server = %#v", first)
	}
	if len(first.PreservedParameters) != 1 || first.PreservedParameters[0].Name != "resolve" ||
		first.PreservedParameters[0].Raw != "resolve" {
		t.Fatalf("preserved parameters = %#v", first.PreservedParameters)
	}

	second := group.Servers[1]
	if second.Endpoint.Address != "2001:db8::1" || second.Endpoint.Port == nil || *second.Endpoint.Port != 8443 ||
		!second.Down || second.Endpoint.Unix {
		t.Fatalf("IPv6 server = %#v", second)
	}
	third := group.Servers[2]
	if !third.Endpoint.Unix || third.Endpoint.Address != "/run/backend.sock" || third.Endpoint.Port != nil {
		t.Fatalf("Unix server = %#v", third)
	}

	if len(group.References) != 1 || group.References[0].State != ReferenceResolved ||
		group.References[0].UpstreamID != group.ID || group.References[0].URI != "/api/" {
		t.Fatalf("references = %#v", group.References)
	}
	if !catalog.ReferenceAnalysisComplete {
		t.Fatalf("ReferenceAnalysisComplete = false; diagnostics = %#v", catalog.Diagnostics)
	}
}

func TestBuildCatalogClassifiesDynamicDanglingAndExternalProxyPass(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: "http {\n" +
			" upstream backend { server 127.0.0.1; }\n" +
			" server {\n" +
			"  location /ok { proxy_pass https://backend:8443/v1; }\n" +
			"  location /missing { proxy_pass http://missing_backend; }\n" +
			"  location /external { proxy_pass http://api.example.test; }\n" +
			"  location /dynamic { proxy_pass http://$target; }\n" +
			" }\n" +
			"}\n",
	}}, nil)

	catalog := BuildCatalog(project)
	if catalog.ReferenceAnalysisComplete {
		t.Fatal("ReferenceAnalysisComplete = true with dynamic proxy_pass")
	}
	states := make(map[ReferenceState]int)
	for _, reference := range catalog.References {
		states[reference.State]++
	}
	want := map[ReferenceState]int{
		ReferenceResolved: 1,
		ReferenceDangling: 1,
		ReferenceExternal: 1,
		ReferenceDynamic:  1,
	}
	for state, count := range want {
		if states[state] != count {
			t.Fatalf("reference states = %#v, want %q=%d", states, state, count)
		}
	}
	for _, code := range []DiagnosticCode{DiagnosticReferenceDangling, DiagnosticReferenceDynamic} {
		if !hasDiagnostic(catalog.Diagnostics, code) {
			t.Fatalf("diagnostics = %#v, want %q", catalog.Diagnostics, code)
		}
	}
}

func TestBuildCatalogMakesDuplicateAndMalformedUpstreamsReadOnly(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: "http {\n" +
			" upstream duplicate { server 127.0.0.1; }\n" +
			" upstream duplicate { server 127.0.0.2; }\n" +
			" upstream malformed { server 127.0.0.1 weight=bad weight=2; }\n" +
			"}\n",
	}}, nil)

	catalog := BuildCatalog(project)
	if len(catalog.Upstreams) != 3 {
		t.Fatalf("upstreams = %#v", catalog.Upstreams)
	}
	for _, group := range catalog.Upstreams[:2] {
		if group.Editable || group.ReadOnlyReason != "duplicate_name" {
			t.Fatalf("duplicate group = %#v", group)
		}
	}
	malformed := catalog.Upstreams[2]
	if len(malformed.Servers) != 1 || malformed.Servers[0].Editable ||
		malformed.Servers[0].ReadOnlyReason != "ambiguous_parameters" {
		t.Fatalf("malformed group = %#v", malformed)
	}
	if !hasDiagnostic(catalog.Diagnostics, DiagnosticDuplicateName) ||
		!hasDiagnostic(catalog.Diagnostics, DiagnosticServerRawOnly) {
		t.Fatalf("diagnostics = %#v", catalog.Diagnostics)
	}
}

func TestBuildCatalogMakesUnrenderableEndpointsAndInlineCommentsRawOnly(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: "http { upstream backend {\n" +
			" server invalid_name;\n" +
			" server 127.0.0.1 # preserve this explanation\n" +
			"  weight=2;\n" +
			"} }\n",
	}}, nil)

	servers := BuildCatalog(project).Upstreams[0].Servers
	if len(servers) != 2 || servers[0].Editable || servers[0].ReadOnlyReason != "invalid_address" ||
		servers[1].Editable || servers[1].ReadOnlyReason != "inline_comment" {
		t.Fatalf("servers = %#v", servers)
	}
}

func mustProject(t *testing.T, files []nginxast.SourceFile, edges []nginxast.IncludeEdge) *nginxast.Project {
	t.Helper()
	project, err := nginxast.BuildProject(files, edges, nginxast.DefaultProjectLimits())
	if err != nil {
		t.Fatalf("BuildProject() error = %v", err)
	}
	return project
}

func hasDiagnostic(diagnostics []Diagnostic, code DiagnosticCode) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
