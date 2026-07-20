/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package location

import (
	"testing"

	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

func TestBuildCatalogReadsSixMatcherTypesAndNestedLocations(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http {\n"+
		" upstream backend { server 127.0.0.1:8080; }\n"+
		" server {\n"+
		"  listen 443 ssl;\n"+
		"  server_name example.test www.example.test;\n"+
		"  location = /exact { return 200; }\n"+
		"  location /api/ {\n"+
		"   proxy_pass http://backend/v1/;\n"+
		"   location /api/admin/ { proxy_pass https://backend:8443; }\n"+
		"  }\n"+
		"  location ^~ /assets/ { }\n"+
		"  location ~ \\.php$ { }\n"+
		"  location ~* \\.jpg$ { }\n"+
		"  location @fallback { }\n"+
		" }\n"+
		"}\n")
	upstreams := upstream.BuildCatalog(project)

	catalog := BuildCatalog(project, upstreams)
	if len(catalog.Servers) != 1 {
		t.Fatalf("servers = %#v", catalog.Servers)
	}
	server := catalog.Servers[0]
	if !server.Editable || len(server.Listens) != 1 || server.Listens[0] != "443 ssl" ||
		len(server.ServerNames) != 2 || server.ServerNames[1] != "www.example.test" {
		t.Fatalf("server = %#v", server)
	}
	if len(server.Locations) != 6 {
		t.Fatalf("locations = %#v", server.Locations)
	}
	wantTypes := []MatcherType{
		MatcherExact, MatcherPrefix, MatcherPrefixPriority,
		MatcherRegex, MatcherRegexInsensitive, MatcherNamed,
	}
	for index, want := range wantTypes {
		if server.Locations[index].Type != want {
			t.Fatalf("locations[%d].Type = %q, want %q", index, server.Locations[index].Type, want)
		}
	}
	if server.Locations[0].UnknownDirectiveCount != 1 {
		t.Fatalf("exact location = %#v", server.Locations[0])
	}
	prefix := server.Locations[1]
	if len(prefix.Children) != 1 || prefix.Children[0].Matcher != "/api/admin/" {
		t.Fatalf("prefix children = %#v", prefix.Children)
	}
	if len(prefix.ProxyPasses) != 1 || prefix.ProxyPasses[0].State != upstream.ReferenceResolved ||
		prefix.ProxyPasses[0].UpstreamID != upstreams.Upstreams[0].ID || !prefix.ProxyPassEditable {
		t.Fatalf("prefix proxy_pass = %#v", prefix)
	}
	if len(prefix.Children[0].ProxyPasses) != 1 ||
		prefix.Children[0].ProxyPasses[0].Port == nil ||
		*prefix.Children[0].ProxyPasses[0].Port != 8443 {
		t.Fatalf("nested proxy_pass = %#v", prefix.Children[0].ProxyPasses)
	}
	if !catalog.Complete || len(catalog.Diagnostics) != 2 ||
		!hasDiagnostic(catalog.Diagnostics, DiagnosticRegexOrderSensitive) {
		t.Fatalf("catalog complete = %v, diagnostics = %#v", catalog.Complete, catalog.Diagnostics)
	}
}

func TestBuildCatalogDiagnosesUnsafeLocationRelationships(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server {\n"+
		" location = /exact { location /exact/child { } }\n"+
		" location @named { location /nested { } }\n"+
		" location /api/ { location /other { } }\n"+
		" location /duplicate { }\n"+
		" location /duplicate { }\n"+
		" location ~ ^/regex-one { }\n"+
		" location ~* ^/regex-two { }\n"+
		" location ~ \"[invalid\" { }\n"+
		"} }\n")

	catalog := BuildCatalog(project, upstream.BuildCatalog(project))
	for _, code := range []DiagnosticCode{
		DiagnosticNestedUnderExact,
		DiagnosticNestedUnderNamed,
		DiagnosticLiteralOutsideParent,
		DiagnosticDuplicate,
		DiagnosticRegexOrderSensitive,
		DiagnosticInvalidMatcher,
	} {
		if !hasDiagnostic(catalog.Diagnostics, code) {
			t.Fatalf("diagnostics = %#v, want %q", catalog.Diagnostics, code)
		}
	}
	if !catalog.Complete {
		t.Fatal("Complete = false even though all unsafe relationships were fully analyzed")
	}

	server := catalog.Servers[0]
	if server.Locations[0].Editable || server.Locations[1].Editable ||
		server.Locations[2].Children[0].Editable || server.Locations[7].Editable {
		t.Fatalf("unsafe locations remained editable: %#v", server.Locations)
	}
	if !server.Locations[5].Editable || !server.Locations[6].Editable {
		t.Fatalf("regex order warning made locations read-only: %#v", server.Locations[5:7])
	}
}

func TestBuildCatalogMakesRepeatedOrDynamicProxyPassRawOnly(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http {\n"+
		" upstream backend { server 127.0.0.1; }\n"+
		" server { location / {\n"+
		"  proxy_pass http://backend;\n"+
		"  proxy_pass http://$target;\n"+
		" } }\n"+
		"}\n")
	upstreams := upstream.BuildCatalog(project)

	catalog := BuildCatalog(project, upstreams)
	location := catalog.Servers[0].Locations[0]
	if len(location.ProxyPasses) != 2 || location.ProxyPassEditable ||
		location.ProxyPassReadOnlyReason != "multiple_direct_proxy_pass" {
		t.Fatalf("location proxy_pass = %#v", location)
	}
	if !hasDiagnostic(catalog.Diagnostics, DiagnosticMultipleProxyPass) {
		t.Fatalf("diagnostics = %#v", catalog.Diagnostics)
	}
	if catalog.Complete {
		t.Fatal("Complete = true with a dynamic reference")
	}
}

func TestBuildCatalogAllowsReplacingOrRemovingOneStaticDirectProxyPass(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server {\n"+
		" location /external { proxy_pass http://api.example.test/v1; }\n"+
		" location /dangling { proxy_pass http://missing_backend; }\n"+
		" location /dynamic { proxy_pass http://$target; }\n"+
		"} }\n")
	catalog := BuildCatalog(project, upstream.BuildCatalog(project))

	if !catalog.Servers[0].Locations[0].ProxyPassEditable ||
		!catalog.Servers[0].Locations[1].ProxyPassEditable ||
		catalog.Servers[0].Locations[2].ProxyPassEditable {
		t.Fatalf("locations = %#v", catalog.Servers[0].Locations)
	}
}

func TestBuildCatalogMarksAllDescendantsOfAnInvalidParentReadOnly(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server {\n"+
		" location = /exact {\n"+
		"  location /exact/child { location /exact/child/grandchild { } }\n"+
		" }\n"+
		"} }\n")
	catalog := BuildCatalog(project, upstream.BuildCatalog(project))
	root := catalog.Servers[0].Locations[0]
	if root.Editable || root.Children[0].Editable || root.Children[0].Children[0].Editable {
		t.Fatalf("invalid subtree remained editable: %#v", root)
	}
}

func TestBuildCatalogRejectsControlCharactersInRegexMatchers(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server { location ~ \"^/api\nnext\" { } } }\n")
	catalog := BuildCatalog(project, upstream.BuildCatalog(project))
	location := catalog.Servers[0].Locations[0]
	if location.Editable || location.ReadOnlyReason != "invalid_matcher" {
		t.Fatalf("location = %#v", location)
	}
}

func TestBuildCatalogKeepsInlineCommentsOutOfWholeStatementEdits(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http {\n"+
		" upstream backend { server 127.0.0.1; }\n"+
		" server {\n"+
		"  location # keep header context\n"+
		"   /header { }\n"+
		"  location /proxy { proxy_pass # keep target context\n"+
		"   http://backend; }\n"+
		" }\n"+
		"}\n")
	catalog := BuildCatalog(project, upstream.BuildCatalog(project))
	header := catalog.Servers[0].Locations[0]
	proxy := catalog.Servers[0].Locations[1]
	if header.Editable || header.ReadOnlyReason != "inline_comment" {
		t.Fatalf("header location = %#v", header)
	}
	if !proxy.Editable || proxy.ProxyPassEditable ||
		proxy.ProxyPassReadOnlyReason != "inline_comment" {
		t.Fatalf("proxy location = %#v", proxy)
	}
}

func TestBuildCatalogRetainsMalformedLocationAsRawOnly(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server { location = { return 200; } } }\n")
	catalog := BuildCatalog(project, upstream.BuildCatalog(project))

	location := catalog.Servers[0].Locations[0]
	if location.Editable || location.ReadOnlyReason != "invalid_header" ||
		location.Type != MatcherUnknown {
		t.Fatalf("location = %#v", location)
	}
	if !hasDiagnostic(catalog.Diagnostics, DiagnosticInvalidHeader) {
		t.Fatalf("diagnostics = %#v", catalog.Diagnostics)
	}
}

func mustProject(t *testing.T, source string) *nginxast.Project {
	t.Helper()
	project, err := nginxast.BuildProject(
		[]nginxast.SourceFile{{Path: "nginx.conf", Source: source}},
		nil,
		nginxast.DefaultProjectLimits(),
	)
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
