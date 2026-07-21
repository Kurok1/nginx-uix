/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"errors"
	"strings"
	"testing"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

func TestInstrumentRewritesRuntimePathsListenersAndFinalRouteEvidence(t *testing.T) {
	source := strings.Join([]string{
		"daemon on;",
		"master_process off;",
		"pid /run/nginx.pid;",
		"error_log /var/log/nginx/error.log;",
		"events {}",
		"http {",
		"    client_body_temp_path /var/cache/nginx/client;",
		"    access_log off;",
		"    server {",
		"        listen 80 default_server;",
		"        server_name example.test;",
		"        return 200 \"server\";",
		"        location / {",
		"            access_log /var/log/nginx/access.log combined;",
		"            return 200 \"root\";",
		"        }",
		"    }",
		"}",
	}, "\n")
	project := mustProject(t, source)
	prefix := "/tmp/nginx-uix-route-test"
	result, err := Instrument(project, InstrumentOptions{
		Prefix: prefix, RunToken: "0123456789abcdef0123456789abcdef",
		Request: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/"},
		ListenerPorts: map[ListenerKey]int{
			{Address: "*", Port: 80, SSL: false}: 18080,
		},
	})
	if err != nil {
		t.Fatalf("Instrument() error = %v", err)
	}
	rendered := result.Files["nginx.conf"]
	for _, forbidden := range []string{
		"daemon on;", "master_process off;", "/run/nginx.pid", "/var/log/nginx",
		"/var/cache/nginx/client", "access_log off;",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("rendered configuration contains %q:\n%s", forbidden, rendered)
		}
	}
	for _, required := range []string{
		"daemon off;",
		"master_process on;",
		"pid \"/tmp/nginx-uix-route-test/nginx.pid\";",
		"error_log \"/tmp/nginx-uix-route-test/logs/error.log\" notice;",
		"listen 127.0.0.1:18080 default_server;",
		"client_body_temp_path \"/tmp/nginx-uix-route-test/temp/client\";",
		"proxy_temp_path \"/tmp/nginx-uix-route-test/temp/proxy\";",
		"log_format nginx_uix_route_0123456789ab escape=json",
		"access_log \"/tmp/nginx-uix-route-test/logs/access.log\" nginx_uix_route_0123456789ab;",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered configuration does not contain %q:\n%s", required, rendered)
		}
	}
	if got, want := result.TargetPort, 18080; got != want {
		t.Fatalf("TargetPort = %d, want %d", got, want)
	}
	if len(result.Routes) != 2 {
		t.Fatalf("routes = %+v, want server and location", result.Routes)
	}
	serverSet := "set $nginx_uix_server_0123456789ab " + result.Routes[0].RouteID + ";"
	locationSet := "set $nginx_uix_route_0123456789ab " + result.Routes[1].RouteID + ";"
	if serverIndex, returnIndex := strings.Index(rendered, serverSet), strings.Index(rendered, "return 200 \"server\""); serverIndex < 0 || serverIndex > returnIndex {
		t.Fatalf("server marker must precede return:\n%s", rendered)
	}
	if locationIndex, returnIndex := strings.Index(rendered, locationSet), strings.LastIndex(rendered, "return 200 \"root\""); locationIndex < 0 || locationIndex > returnIndex {
		t.Fatalf("location marker must precede return:\n%s", rendered)
	}
	if _, err := nginxast.Parse(rendered); err != nil {
		t.Fatalf("Parse(instrumented) error = %v\n%s", err, rendered)
	}
}

func TestInstrumentAddsListenerForDerivedDefaultAndKeepsSourceImmutable(t *testing.T) {
	source := "events {} http { server { server_name example.test; location / { return 204; } } }"
	project := mustProject(t, source)
	result, err := Instrument(project, InstrumentOptions{
		Prefix: "/tmp/route", RunToken: "abcdefabcdefabcdefabcdefabcdefab",
		Request: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/"},
		ListenerPorts: map[ListenerKey]int{
			{Address: "*", Port: 80, SSL: false}: 19090,
		},
	})
	if err != nil {
		t.Fatalf("Instrument() error = %v", err)
	}
	if !strings.Contains(result.Files["nginx.conf"], "listen 127.0.0.1:19090;") {
		t.Fatalf("derived listener was not inserted:\n%s", result.Files["nginx.conf"])
	}
	if project.Documents["nginx.conf"].Document.Render() != source {
		t.Fatalf("Instrument() mutated source project")
	}
}

func TestInstrumentFailsClosedForIncompleteProjectAndMissingPortMapping(t *testing.T) {
	incomplete, err := nginxast.BuildProject(
		[]nginxast.SourceFile{{Path: "nginx.conf", Source: "events {}\nhttp {\n    include missing.conf;\n}"}},
		[]nginxast.IncludeEdge{{
			Source: "nginx.conf", Line: 3, Column: 5,
			Target: "missing.conf", Status: nginxast.IncludeMissing,
		}},
		nginxast.DefaultProjectLimits(),
	)
	if err != nil {
		t.Fatalf("BuildProject() error = %v", err)
	}
	_, err = Instrument(incomplete, InstrumentOptions{
		Prefix: "/tmp/route", RunToken: "abcdefabcdefabcdefabcdefabcdefab",
		Request: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/"},
	})
	if !errors.Is(err, ErrProjectIncomplete) {
		t.Fatalf("Instrument(incomplete) error = %v, want ErrProjectIncomplete", err)
	}

	complete := mustProject(t, "events {} http { server { listen 8080; } }")
	_, err = Instrument(complete, InstrumentOptions{
		Prefix: "/tmp/route", RunToken: "abcdefabcdefabcdefabcdefabcdefab",
		Request: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 8080, URI: "/"},
	})
	if !errors.Is(err, ErrInvalidInstrumentation) {
		t.Fatalf("Instrument(missing mapping) error = %v, want ErrInvalidInstrumentation", err)
	}
}
