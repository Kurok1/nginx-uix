/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"errors"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

func TestAnalyzeSelectsServerNameByDocumentedPriority(t *testing.T) {
	project := mustProject(t, `
events {}
http {
    server {
        listen 80 default_server;
        server_name fallback.test;
    }
    server {
        listen 80;
        server_name *.example.test;
    }
    server {
        listen 80;
        server_name api.example.test;
    }
    server {
        listen 80;
        server_name ~^api[0-9]+\\.internal$;
    }
}`)

	tests := []struct {
		name       string
		host       string
		wantSource int
		wantReason CandidateReason
	}{
		{name: "exact before wildcard", host: "API.EXAMPLE.TEST.", wantSource: 12, wantReason: ReasonServerNameExact},
		{name: "leading wildcard", host: "www.example.test", wantSource: 8, wantReason: ReasonServerNameLeadingWildcard},
		{name: "regular expression", host: "api42.internal", wantSource: 16, wantReason: ReasonServerNameRegex},
		{name: "listener default", host: "unknown.test", wantSource: 4, wantReason: ReasonListenerDefault},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analysis, err := Analyze(project, StaticRequest{Scheme: SchemeHTTP, Host: test.host, Port: 80, URI: "/"})
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			selected := selectedServer(t, analysis)
			if selected.Source.StartLine != test.wantSource || selected.Reason != test.wantReason {
				t.Fatalf("selected server = %+v, want line %d reason %q", selected, test.wantSource, test.wantReason)
			}
			if analysis.PredictedServerRouteID != selected.RouteID || selected.RouteID == "" {
				t.Fatalf("predicted server ID = %q, selected = %q", analysis.PredictedServerRouteID, selected.RouteID)
			}
		})
	}
}

func TestAnalyzeDoesNotPredictServerAcrossHigherPriorityIndeterminatePCRE(t *testing.T) {
	project := mustProject(t, `
events {}
http {
    server {
        listen 80 default_server;
        server_name fallback.test;
    }
    server {
        listen 80;
        server_name ~^api(?=\.example\.test$);
    }
    server {
        listen 80;
        server_name ~^api\.example\.test$;
    }
}`)
	analysis, err := Analyze(project, StaticRequest{
		Scheme: SchemeHTTP, Host: "api.example.test", Port: 80, URI: "/",
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.Complete || analysis.PredictedServerRouteID != "" || analysis.PredictedLocationRouteID != "" {
		t.Fatalf("analysis = %+v, want no prediction across an earlier indeterminate regex", analysis)
	}
	if candidate := serverCandidate(t, analysis, "~^api(?=.example.test$)"); candidate.Disposition != DispositionIndeterminate || candidate.Reason != ReasonServerNameIndeterminate {
		t.Fatalf("indeterminate server = %+v", candidate)
	}
}

func TestAnalyzeKnownExactServerRemainsCertainWithLowerPriorityIndeterminatePCRE(t *testing.T) {
	project := mustProject(t, `
events {}
http {
    server {
        listen 80;
        server_name api.example.test;
    }
    server {
        listen 80;
        server_name ~^api(?=\.example\.test$);
    }
}`)
	analysis, err := Analyze(project, StaticRequest{
		Scheme: SchemeHTTP, Host: "api.example.test", Port: 80, URI: "/",
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	selected := selectedServer(t, analysis)
	if analysis.Complete || analysis.PredictedServerRouteID != selected.RouteID || selected.Reason != ReasonServerNameExact {
		t.Fatalf("analysis = %+v, want a certain exact winner with incomplete lower-priority facts", analysis)
	}
}

func TestAnalyzeSeparatesHTTPHostFromTLSSNISelection(t *testing.T) {
	project := mustProject(t, `
events {}
http {
    server { listen 443 ssl default_server; server_name fallback.test; }
    server { listen 443 ssl; server_name sni.example.test; }
    server { listen 443 ssl; server_name host.example.test; }
}`)
	analysis, err := Analyze(project, StaticRequest{
		Scheme: SchemeHTTPS, Host: "host.example.test", SNI: "sni.example.test", Port: 443, URI: "/",
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.PredictedServerRouteID == "" || analysis.PredictedTLSServerRouteID == "" ||
		analysis.PredictedServerRouteID == analysis.PredictedTLSServerRouteID {
		t.Fatalf("analysis = %+v, want distinct Host and SNI server selections", analysis)
	}
}

func TestAnalyzeExplainsLocationPriorityAndNestedSelection(t *testing.T) {
	project := mustProject(t, `
events {}
http {
    server {
        listen 80;
        server_name example.test;
        location / { return 200 root; }
        location = /exact { return 200 exact; }
        location /assets/ { return 200 assets; }
        location ^~ /assets/private/ { return 200 private; }
        location ~ \\.php$ { return 200 regex; }
        location /nested/ {
            location /nested/deep/ { return 200 nested; }
        }
    }
}`)

	tests := []struct {
		name        string
		uri         string
		wantMatcher string
		wantReason  CandidateReason
	}{
		{name: "exact", uri: "/exact", wantMatcher: "/exact", wantReason: ReasonLocationExact},
		{name: "longest prefix priority", uri: "/assets/private/file.php", wantMatcher: "/assets/private/", wantReason: ReasonLocationPrefixPriority},
		{name: "regex after ordinary prefix", uri: "/index.php", wantMatcher: `\.php$`, wantReason: ReasonLocationRegex},
		{name: "nested longest prefix", uri: "/nested/deep/value", wantMatcher: "/nested/deep/", wantReason: ReasonLocationLongestPrefix},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analysis, err := Analyze(project, StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: test.uri})
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			selected := selectedLocation(t, analysis)
			if selected.Matcher != test.wantMatcher || selected.Reason != test.wantReason {
				t.Fatalf("selected location = %+v, want matcher %q reason %q", selected, test.wantMatcher, test.wantReason)
			}
			if analysis.PredictedLocationRouteID != selected.RouteID {
				t.Fatalf("predicted location ID = %q, selected = %q", analysis.PredictedLocationRouteID, selected.RouteID)
			}
		})
	}
}

func TestAnalyzeNestedLocationRespectsOuterRegexAndTerminalNestedMatch(t *testing.T) {
	project := mustProject(t, `
events {}
http {
    server {
        listen 80;
        server_name example.test;
        location /nested/ {
            location /nested/deep/ { return 200 nested-prefix; }
            location = /nested/exact { return 200 nested-exact; }
        }
        location ~ ^/nested/deep/ { return 200 first-outer-regex; }
        location ~ ^/nested/ { return 200 second-outer-regex; }
    }
}`)

	t.Run("outer regex overrides a non-terminal nested prefix", func(t *testing.T) {
		analysis, err := Analyze(project, StaticRequest{
			Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/nested/deep/value",
		})
		if err != nil {
			t.Fatalf("Analyze() error = %v", err)
		}
		selected := selectedLocation(t, analysis)
		if selected.Matcher != "^/nested/deep/" || selected.Reason != ReasonLocationRegex {
			t.Fatalf("selected location = %+v, want first outer regex", selected)
		}
		later := locationCandidate(t, analysis, "^/nested/")
		if later.Disposition != DispositionExcluded || later.Reason != ReasonLocationEarlierRegex {
			t.Fatalf("later regex = %+v, want earlier-regex exclusion", later)
		}
	})

	t.Run("nested exact terminates before outer regex", func(t *testing.T) {
		analysis, err := Analyze(project, StaticRequest{
			Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/nested/exact",
		})
		if err != nil {
			t.Fatalf("Analyze() error = %v", err)
		}
		selected := selectedLocation(t, analysis)
		if selected.Matcher != "/nested/exact" || selected.Reason != ReasonLocationExact {
			t.Fatalf("selected location = %+v, want terminal nested exact", selected)
		}
	})
}

func TestAnalyzeLeavesWinnerEmptyWhenEarlierPCREIsIndeterminate(t *testing.T) {
	project := mustProject(t, `
events {}
http {
    server {
        listen 80;
        server_name example.test;
        location ~ ^/api(?=/) { return 200 pcre; }
        location ~ ^/api/ { return 200 re2; }
    }
}`)
	analysis, err := Analyze(project, StaticRequest{
		Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/api/value",
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.Complete || analysis.PredictedLocationRouteID != "" {
		t.Fatalf("analysis = %+v, want incomplete analysis without a predicted location", analysis)
	}
	unknown := locationCandidate(t, analysis, "^/api(?=/)")
	if unknown.Disposition != DispositionIndeterminate || unknown.Reason != ReasonLocationRegexIndeterminate {
		t.Fatalf("PCRE candidate = %+v, want indeterminate", unknown)
	}
}

func TestAnalyzeRespectsMergeSlashesForSelectedServer(t *testing.T) {
	tests := []struct {
		name           string
		mergeDirective string
		wantNormalized string
		wantMatcher    string
		wantComplete   bool
		wantLocationID bool
	}{
		{name: "default on", wantNormalized: "/double/value", wantMatcher: "/double/", wantComplete: true, wantLocationID: true},
		{name: "explicit off", mergeDirective: "merge_slashes off;", wantNormalized: "/double//value", wantMatcher: "/double//", wantComplete: true, wantLocationID: true},
		{name: "indeterminate value", mergeDirective: "merge_slashes dynamic;", wantNormalized: "/double//value", wantComplete: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := mustProject(t, `events {}
http {
    `+test.mergeDirective+`
    server {
        listen 80;
        server_name example.test;
        location /double/ { return 200 single; }
        location /double// { return 201 double; }
    }
}`)
			analysis, err := Analyze(project, StaticRequest{
				Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/double//value",
			})
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			if analysis.NormalizedURI != test.wantNormalized || analysis.Complete != test.wantComplete {
				t.Fatalf("analysis = %+v, want normalized %q complete=%v", analysis, test.wantNormalized, test.wantComplete)
			}
			if !test.wantLocationID {
				if analysis.PredictedLocationRouteID != "" {
					t.Fatalf("predicted location = %q, want none", analysis.PredictedLocationRouteID)
				}
				return
			}
			if selected := selectedLocation(t, analysis); selected.Matcher != test.wantMatcher {
				t.Fatalf("selected location = %+v, want matcher %q", selected, test.wantMatcher)
			}
		})
	}
}

func TestAnalyzeRejectsAmbiguousListenerAddressGroups(t *testing.T) {
	project := mustProject(t, `
events {}
http {
    server { listen 192.0.2.10:8080; server_name a.test; }
    server { listen 192.0.2.11:8080; server_name b.test; }
}`)

	_, err := Analyze(project, StaticRequest{Scheme: SchemeHTTP, Host: "a.test", Port: 8080, URI: "/"})
	if !errors.Is(err, ErrListenerAmbiguous) {
		t.Fatalf("Analyze() error = %v, want ErrListenerAmbiguous", err)
	}
}

func TestRouteIDsAreStableAndChangeWithSourceIdentity(t *testing.T) {
	first := mustProject(t, `events {} http { server { listen 80; location /a { return 200; } } }`)
	second := mustProject(t, `events {} http { server { listen 80; location /b { return 200; } } }`)

	firstAnalysis, err := Analyze(first, StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/a"})
	if err != nil {
		t.Fatalf("Analyze(first) error = %v", err)
	}
	repeated, err := Analyze(first, StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/a"})
	if err != nil {
		t.Fatalf("Analyze(repeated) error = %v", err)
	}
	secondAnalysis, err := Analyze(second, StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/b"})
	if err != nil {
		t.Fatalf("Analyze(second) error = %v", err)
	}
	if firstAnalysis.PredictedServerRouteID != repeated.PredictedServerRouteID ||
		firstAnalysis.PredictedLocationRouteID != repeated.PredictedLocationRouteID {
		t.Fatalf("route IDs are not stable: first=%+v repeated=%+v", firstAnalysis, repeated)
	}
	if firstAnalysis.PredictedLocationRouteID == secondAnalysis.PredictedLocationRouteID {
		t.Fatalf("location route ID did not change with matcher source")
	}
}

func TestValidateRequestRequiresConfirmationAndRejectsReservedHeaders(t *testing.T) {
	base := Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/submit"},
		Method:        "POST",
		Timeout:       time.Second,
	}
	if _, err := ValidateRequest(base); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("ValidateRequest() error = %v, want ErrConfirmationRequired", err)
	}

	base.Confirmation = SideEffectConfirmation
	base.Headers = []Header{{Name: "X-Nginx-UIX-Test-ID", Value: "attacker-controlled"}}
	if _, err := ValidateRequest(base); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ValidateRequest(reserved header) error = %v, want ErrInvalidRequest", err)
	}

	base.Headers = []Header{{Name: "Authorization", Value: "Bearer secret"}}
	validated, err := ValidateRequest(base)
	if err != nil {
		t.Fatalf("ValidateRequest(valid) error = %v", err)
	}
	if validated.Replayable || !validated.SideEffecting || len(validated.SensitiveHeaderNames) != 1 ||
		validated.SensitiveHeaderNames[0] != "Authorization" {
		t.Fatalf("validated request = %+v", validated)
	}
}

func TestProjectMayContactUpstreamIsConservative(t *testing.T) {
	local := mustProject(t, `events {} http { server { listen 80; location / { return 200; } } }`)
	proxied := mustProject(t, `events {} http { server { listen 80; location / { proxy_pass http://backend; } } }`)
	if ProjectMayContactUpstream(local) || !ProjectMayContactUpstream(proxied) {
		t.Fatalf("upstream classification local=%v proxied=%v", ProjectMayContactUpstream(local), ProjectMayContactUpstream(proxied))
	}
}

func TestValidateAgentRequestRetainsProvenUpstreamSideEffect(t *testing.T) {
	validated, err := ValidateRequest(Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/"},
		Method:        "GET", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	validated.SideEffecting = true
	validated.UpstreamSideEffect = true
	got, err := ValidateAgentRequest(validated)
	if err != nil || !got.SideEffecting || !got.UpstreamSideEffect {
		t.Fatalf("ValidateAgentRequest() = %+v, %v", got, err)
	}
	validated.UpstreamSideEffect = false
	if _, err := ValidateAgentRequest(validated); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ValidateAgentRequest(unproven side effect) error = %v", err)
	}
}

func selectedServer(t *testing.T, analysis Analysis) ServerCandidate {
	t.Helper()
	for _, candidate := range analysis.Servers {
		if candidate.Disposition == DispositionSelected {
			return candidate
		}
	}
	t.Fatalf("no selected server in %+v", analysis.Servers)
	return ServerCandidate{}
}

func serverCandidate(t *testing.T, analysis Analysis, serverName string) ServerCandidate {
	t.Helper()
	for _, candidate := range analysis.Servers {
		for _, name := range candidate.ServerNames {
			if name == serverName {
				return candidate
			}
		}
	}
	t.Fatalf("no server_name %q in %+v", serverName, analysis.Servers)
	return ServerCandidate{}
}

func selectedLocation(t *testing.T, analysis Analysis) LocationCandidate {
	t.Helper()
	for _, candidate := range analysis.Locations {
		if candidate.Disposition == DispositionSelected {
			return candidate
		}
	}
	t.Fatalf("no selected location in %+v", analysis.Locations)
	return LocationCandidate{}
}

func locationCandidate(t *testing.T, analysis Analysis, matcher string) LocationCandidate {
	t.Helper()
	for _, candidate := range analysis.Locations {
		if candidate.Matcher == matcher {
			return candidate
		}
	}
	t.Fatalf("no location %q in %+v", matcher, analysis.Locations)
	return LocationCandidate{}
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
