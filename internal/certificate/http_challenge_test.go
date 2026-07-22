/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

const testHTTPTaskID TaskID = "1234567890abcdef1234567890abcdef"

func TestHTTPChallengePlannerRoundTripsOnlyExactTaskInclude(t *testing.T) {
	source := "events {}\nhttp {\n  server {\n    listen 80;\n    server_name example.com;\n    location / { return 204; }\n  }\n}\n"
	project := bindingProject(t, source)
	ref := oneEditableServerRef(t, project)
	provision, err := PlanHTTPChallengeProvision(context.Background(), project, []ServerRef{ref}, testHTTPTaskID)
	if err != nil {
		t.Fatal(err)
	}
	include := "include /etc/nginx/" + HTTPChallengeConfigPath(testHTTPTaskID) + ";"
	if len(provision.Files) != 1 || !strings.Contains(provision.Files[0].After, include) ||
		!strings.Contains(provision.Files[0].After, "location / { return 204; }") {
		t.Fatalf("provision = %#v", provision)
	}

	withInclude := incompleteHTTPChallengeProject(t, provision.Files[0].After)
	cleanup, err := PlanHTTPChallengeCleanup(context.Background(), withInclude, testHTTPTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleanup.Files) != 1 || cleanup.Files[0].After != source {
		t.Fatalf("cleanup = %#v", cleanup)
	}
}

func incompleteHTTPChallengeProject(t *testing.T, source string) *nginxast.Project {
	t.Helper()
	project, err := nginxast.BuildProject(
		[]nginxast.SourceFile{{Path: "nginx.conf", Source: source}}, nil, nginxast.DefaultProjectLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if project.Complete {
		t.Fatal("project unexpectedly complete without a manifest edge for the task fragment")
	}
	return project
}

func TestRenderHTTPChallengeFragmentRejectsInjectionAndSortsTokens(t *testing.T) {
	fragment, err := RenderHTTPChallengeFragment([]HTTPChallengeResponse{
		{Identifier: "b.example.com", Token: "token-b", KeyAuthorization: "token-b.thumbprint"},
		{Identifier: "a.example.com", Token: "token-a", KeyAuthorization: "token-a.thumbprint"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(fragment, "token-a") > strings.Index(fragment, "token-b") ||
		!strings.Contains(fragment, "location = /.well-known/acme-challenge/token-a") ||
		!strings.Contains(fragment, `return 200 "token-a.thumbprint";`) {
		t.Fatalf("fragment = %q", fragment)
	}
	for _, invalid := range []HTTPChallengeResponse{
		{Identifier: "example.com", Token: "bad/token", KeyAuthorization: "value"},
		{Identifier: "example.com", Token: "token", KeyAuthorization: "value\"; return 500; #"},
	} {
		if _, err := RenderHTTPChallengeFragment([]HTTPChallengeResponse{invalid}); !errors.Is(err, ErrIdentifierInvalid) {
			t.Fatalf("RenderHTTPChallengeFragment(%#v) error = %v", invalid, err)
		}
	}
}

func TestHTTPChallengeCleanupIsIdempotentWhenIncludeWasNeverPublished(t *testing.T) {
	project := bindingProject(t, "events {}\nhttp { server { listen 80; server_name example.com; } }\n")
	plan, err := PlanHTTPChallengeCleanup(context.Background(), project, testHTTPTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 0 || plan.Mode != "http_challenge_cleanup" {
		t.Fatalf("plan = %#v", plan)
	}
}
