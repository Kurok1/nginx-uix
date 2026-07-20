/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package location

import (
	"errors"
	"strings"
	"testing"

	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

func TestPlanCreateNestedLocationWithSelectedUpstream(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http {\n"+
		" upstream backend { server 127.0.0.1; }\n"+
		" server {\n"+
		"  location /api/ { }\n"+
		" }\n"+
		"}\n")
	upstreams := upstream.BuildCatalog(project)
	catalog := BuildCatalog(project, upstreams)
	parent := catalog.Servers[0].Locations[0]

	plan, err := PlanCreate(project, catalog, upstreams, CreateInput{
		ParentID: parent.ID,
		Type:     MatcherExact,
		Matcher:  "/api/users",
		ProxyPass: &ProxyPassInput{
			UpstreamID: upstreams.Upstreams[0].ID,
			Scheme:     "https",
			Port:       uint16Pointer(8443),
			URI:        "/v1/users",
		},
	})
	if err != nil {
		t.Fatalf("PlanCreate() error = %v", err)
	}
	rendered, err := project.ApplyEdits(plan.Edits)
	if err != nil {
		t.Fatal(err)
	}
	wantBlock := "location = /api/users {\n" +
		"          proxy_pass https://backend:8443/v1/users;\n" +
		"      }"
	if !strings.Contains(rendered["nginx.conf"], wantBlock) {
		t.Fatalf("created source = %q, want block %q", rendered["nginx.conf"], wantBlock)
	}
	updated := mustProject(t, rendered["nginx.conf"])
	updatedUpstreams := upstream.BuildCatalog(updated)
	updatedCatalog := BuildCatalog(updated, updatedUpstreams)
	child := updatedCatalog.Servers[0].Locations[0].Children[0]
	if child.Type != MatcherExact || child.Matcher != "/api/users" ||
		len(child.ProxyPasses) != 1 || child.ProxyPasses[0].State != upstream.ReferenceResolved {
		t.Fatalf("created child = %#v", child)
	}
}

func TestPlanUpdateChangesOnlyHeaderAndDirectProxyPass(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http {\n"+
		" upstream backend { server 127.0.0.1; }\n"+
		" server {\n"+
		"  location /old/ {\n"+
		"   add_header X-Keep yes;\n"+
		"   proxy_pass http://backend/old/;\n"+
		"  }\n"+
		" }\n"+
		"}\n")
	upstreams := upstream.BuildCatalog(project)
	catalog := BuildCatalog(project, upstreams)
	target := catalog.Servers[0].Locations[0]

	plan, err := PlanUpdate(project, catalog, upstreams, UpdateInput{
		LocationID: target.ID,
		Type:       MatcherExact,
		Matcher:    "/new",
		ProxyMode:  ProxySet,
		ProxyPass: &ProxyPassInput{
			UpstreamID: upstreams.Upstreams[0].ID,
			Scheme:     "https",
			URI:        "/v2/",
		},
	})
	if err != nil {
		t.Fatalf("PlanUpdate() error = %v", err)
	}
	rendered, err := project.ApplyEdits(plan.Edits)
	if err != nil {
		t.Fatal(err)
	}
	want := "location = /new {\n" +
		"   add_header X-Keep yes;\n" +
		"   proxy_pass https://backend/v2/;\n" +
		"  }"
	if !strings.Contains(rendered["nginx.conf"], want) {
		t.Fatalf("updated source = %q, want %q", rendered["nginx.conf"], want)
	}
	if strings.Count(rendered["nginx.conf"], "add_header X-Keep yes;") != 1 {
		t.Fatalf("preserved directive changed: %q", rendered["nginx.conf"])
	}
	if _, err := nginxast.Parse(rendered["nginx.conf"]); err != nil {
		t.Fatalf("Parse(updated) error = %v", err)
	}
}

func TestPlanCreateRejectsDuplicateAndInvalidNestedRules(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server {\n"+
		" location /api/ { location /api/users { } }\n"+
		" location = /exact { }\n"+
		"} }\n")
	upstreams := upstream.BuildCatalog(project)
	catalog := BuildCatalog(project, upstreams)
	api := catalog.Servers[0].Locations[0]

	_, err := PlanCreate(project, catalog, upstreams, CreateInput{
		ParentID: api.ID, Type: MatcherPrefix, Matcher: "/api/users",
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("PlanCreate(duplicate) error = %v, want ErrDuplicate", err)
	}
	_, err = PlanCreate(project, catalog, upstreams, CreateInput{
		ParentID: api.ID, Type: MatcherPrefix, Matcher: "/outside",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanCreate(outside parent) error = %v, want ErrInvalid", err)
	}
	exact := catalog.Servers[0].Locations[1]
	_, err = PlanCreate(project, catalog, upstreams, CreateInput{
		ParentID: exact.ID, Type: MatcherPrefix, Matcher: "/exact/child",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanCreate(under exact) error = %v, want ErrInvalid", err)
	}
	_, err = PlanCreate(project, catalog, upstreams, CreateInput{
		ParentID: api.ID, Type: MatcherNamed, Matcher: "@nested",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanCreate(nested named) error = %v, want ErrInvalid", err)
	}
}

func TestPlanUpdateRejectsURIProxyPassForRegexOrNamedLocation(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http {\n"+
		" upstream backend { server 127.0.0.1; }\n"+
		" server { location /old { } }\n"+
		"}\n")
	upstreams := upstream.BuildCatalog(project)
	catalog := BuildCatalog(project, upstreams)
	target := catalog.Servers[0].Locations[0]
	_, err := PlanUpdate(project, catalog, upstreams, UpdateInput{
		LocationID: target.ID,
		Type:       MatcherRegex,
		Matcher:    "^/new",
		ProxyMode:  ProxySet,
		ProxyPass: &ProxyPassInput{
			UpstreamID: upstreams.Upstreams[0].ID,
			Scheme:     "http",
			URI:        "/not-allowed",
		},
	})
	if !errors.Is(err, ErrProxyPassInvalid) {
		t.Fatalf("PlanUpdate() error = %v, want ErrProxyPassInvalid", err)
	}
}

func TestPlanDeleteRequiresExactMatcherConfirmationAndPreservesLeadingComment(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server {\n"+
		" # keep\n"+
		" location /remove { return 204; }\n"+
		" location /keep { }\n"+
		"} }\n")
	upstreams := upstream.BuildCatalog(project)
	catalog := BuildCatalog(project, upstreams)
	target := catalog.Servers[0].Locations[0]
	_, err := PlanDelete(project, catalog, DeleteInput{
		LocationID: target.ID, ConfirmMatcher: "/wrong",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanDelete(wrong confirmation) error = %v", err)
	}
	plan, err := PlanDelete(project, catalog, DeleteInput{
		LocationID: target.ID, ConfirmMatcher: "/remove",
	})
	if err != nil {
		t.Fatalf("PlanDelete() error = %v", err)
	}
	rendered, err := project.ApplyEdits(plan.Edits)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered["nginx.conf"], " # keep\n") ||
		strings.Contains(rendered["nginx.conf"], "location /remove") ||
		!strings.Contains(rendered["nginx.conf"], "location /keep") {
		t.Fatalf("deleted source = %q", rendered["nginx.conf"])
	}
}

func TestPlanUpdateRejectsAParentMatcherThatInvalidatesExistingChildren(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server {\n"+
		" location /api { location /api/users { } }\n"+
		"} }\n")
	upstreams := upstream.BuildCatalog(project)
	catalog := BuildCatalog(project, upstreams)
	target := catalog.Servers[0].Locations[0]

	_, err := PlanUpdate(project, catalog, upstreams, UpdateInput{
		LocationID: target.ID,
		Type:       MatcherRegex,
		Matcher:    "^/api",
		ProxyMode:  ProxyPreserve,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanUpdate() error = %v, want ErrInvalid", err)
	}
}

func TestPlanDeleteFailsClosedForIncompleteProjects(t *testing.T) {
	t.Parallel()

	project := mustProject(t, "http { server { location /remove { } } }\n")
	catalog := BuildCatalog(project, upstream.BuildCatalog(project))
	target := catalog.Servers[0].Locations[0]
	project.Complete = false

	_, err := PlanDelete(project, catalog, DeleteInput{
		LocationID: target.ID, ConfirmMatcher: target.Matcher,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanDelete() error = %v, want ErrInvalid", err)
	}
}

func uint16Pointer(value uint16) *uint16 {
	return &value
}
