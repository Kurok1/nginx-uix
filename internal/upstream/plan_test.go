/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package upstream

import (
	"errors"
	"strings"
	"testing"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

func TestPlanRenameChangesOnlyHeaderAndDirectReferenceHostsAcrossFiles(t *testing.T) {
	t.Parallel()

	files := []nginxast.SourceFile{
		{Path: "nginx.conf", Source: "http {\n include upstreams.conf;\n include sites.conf;\n}\n"},
		{
			Path:   "sites.conf",
			Source: "server { location / { proxy_pass \"http://old:8080/api/old?q=old\"; } }\n",
		},
		{
			Path:   "upstreams.conf",
			Source: "# independent\nupstream 'old' { server 127.0.0.1:8080; }\n",
		},
	}
	edges := []nginxast.IncludeEdge{
		{
			Source: "nginx.conf", Line: 2, Column: 2,
			Target: "upstreams.conf", Status: nginxast.IncludeResolved,
		},
		{
			Source: "nginx.conf", Line: 3, Column: 2,
			Target: "sites.conf", Status: nginxast.IncludeResolved,
		},
	}
	project := mustProject(t, files, edges)
	catalog := BuildCatalog(project)

	plan, err := PlanRename(project, catalog, RenameInput{
		UpstreamID: catalog.Upstreams[0].ID,
		NewName:    "backend",
	})
	if err != nil {
		t.Fatalf("PlanRename() error = %v", err)
	}
	if plan.Kind != "upstream.rename" || len(plan.Edits) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	rendered, err := project.ApplyEdits(plan.Edits)
	if err != nil {
		t.Fatal(err)
	}
	if got := rendered["upstreams.conf"]; got != "# independent\nupstream 'backend' { server 127.0.0.1:8080; }\n" {
		t.Fatalf("upstreams.conf = %q", got)
	}
	wantSite := "server { location / { proxy_pass \"http://backend:8080/api/old?q=old\"; } }\n"
	if got := rendered["sites.conf"]; got != wantSite {
		t.Fatalf("sites.conf = %q, want %q", got, wantSite)
	}

	updatedFiles := []nginxast.SourceFile{
		{Path: "nginx.conf", Source: files[0].Source},
		{Path: "sites.conf", Source: rendered["sites.conf"]},
		{Path: "upstreams.conf", Source: rendered["upstreams.conf"]},
	}
	updated := mustProject(t, updatedFiles, edges)
	updatedCatalog := BuildCatalog(updated)
	if len(updatedCatalog.Upstreams) != 1 || updatedCatalog.Upstreams[0].Name != "backend" ||
		len(updatedCatalog.Upstreams[0].References) != 1 {
		t.Fatalf("updated catalog = %#v", updatedCatalog)
	}
}

func TestPlanRenameAndDeleteFailClosedForIncompleteOrExistingReferences(t *testing.T) {
	t.Parallel()

	dynamicProject := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: "http {\n" +
			" upstream backend { server 127.0.0.1; }\n" +
			" server { location / { proxy_pass http://$target; } }\n" +
			"}\n",
	}}, nil)
	dynamicCatalog := BuildCatalog(dynamicProject)
	_, err := PlanRename(dynamicProject, dynamicCatalog, RenameInput{
		UpstreamID: dynamicCatalog.Upstreams[0].ID, NewName: "renamed",
	})
	if !errors.Is(err, ErrReferenceIncomplete) {
		t.Fatalf("PlanRename(dynamic) error = %v, want ErrReferenceIncomplete", err)
	}
	_, err = PlanDelete(dynamicProject, dynamicCatalog, DeleteInput{
		UpstreamID: dynamicCatalog.Upstreams[0].ID, ConfirmName: "backend",
	})
	if !errors.Is(err, ErrReferenceIncomplete) {
		t.Fatalf("PlanDelete(dynamic) error = %v, want ErrReferenceIncomplete", err)
	}

	referencedProject := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: "http {\n" +
			" upstream backend { server 127.0.0.1; }\n" +
			" server { location / { proxy_pass http://backend; } }\n" +
			"}\n",
	}}, nil)
	referencedCatalog := BuildCatalog(referencedProject)
	_, err = PlanDelete(referencedProject, referencedCatalog, DeleteInput{
		UpstreamID: referencedCatalog.Upstreams[0].ID, ConfirmName: "backend",
	})
	if !errors.Is(err, ErrReferenced) {
		t.Fatalf("PlanDelete(referenced) error = %v, want ErrReferenced", err)
	}
}

func TestPlanCreateAndServerUpdateReparseWithoutChangingPreservedParameters(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path:   "nginx.conf",
		Source: "events {}\nhttp {\n    server { }\n}\n",
	}}, nil)
	httpID := blockID(t, project, "http")
	weight := 2
	create, err := PlanCreate(project, BuildCatalog(project), CreateInput{
		HTTPBlockID: httpID,
		Name:        "backend",
		Servers: []ServerInput{{
			Endpoint: Endpoint{Address: "127.0.0.1", Port: port(8080)},
			Weight:   &weight,
		}},
	})
	if err != nil {
		t.Fatalf("PlanCreate() error = %v", err)
	}
	rendered, err := project.ApplyEdits(create.Edits)
	if err != nil {
		t.Fatal(err)
	}
	want := "events {}\nhttp {\n" +
		"    server { }\n" +
		"    upstream backend {\n" +
		"        server 127.0.0.1:8080 weight=2;\n" +
		"    }\n" +
		"}\n"
	if rendered["nginx.conf"] != want {
		t.Fatalf("created source = %q, want %q", rendered["nginx.conf"], want)
	}

	createdProject := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: strings.Replace(
			rendered["nginx.conf"],
			"weight=2;",
			"weight=2 resolve;",
			1,
		),
	}}, nil)
	createdCatalog := BuildCatalog(createdProject)
	server := createdCatalog.Upstreams[0].Servers[0]
	maxFails := 0
	failTimeout := "5s"
	update, err := PlanUpdateServer(createdProject, createdCatalog, UpdateServerInput{
		UpstreamID: createdCatalog.Upstreams[0].ID,
		ServerID:   server.ID,
		Server: ServerInput{
			Endpoint:    Endpoint{Address: "2001:db8::1", Port: port(8443)},
			Weight:      intPointer(3),
			Backup:      true,
			MaxFails:    &maxFails,
			FailTimeout: &failTimeout,
		},
	})
	if err != nil {
		t.Fatalf("PlanUpdateServer() error = %v", err)
	}
	updated, err := createdProject.ApplyEdits(update.Edits)
	if err != nil {
		t.Fatal(err)
	}
	wantDirective := "server [2001:db8::1]:8443 weight=3 max_fails=0 fail_timeout=5s backup resolve;"
	if !strings.Contains(updated["nginx.conf"], wantDirective) {
		t.Fatalf("updated source = %q, want directive %q", updated["nginx.conf"], wantDirective)
	}
	if _, err := nginxast.Parse(updated["nginx.conf"]); err != nil {
		t.Fatalf("Parse(updated) error = %v", err)
	}
}

func TestPlanDeleteServerRejectsLastUsableServer(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path:   "nginx.conf",
		Source: "http { upstream backend { server 127.0.0.1; server 127.0.0.2 down; } }\n",
	}}, nil)
	catalog := BuildCatalog(project)
	_, err := PlanDeleteServer(project, catalog, DeleteServerInput{
		UpstreamID: catalog.Upstreams[0].ID,
		ServerID:   catalog.Upstreams[0].Servers[0].ID,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanDeleteServer() error = %v, want ErrInvalid", err)
	}
}

func TestPlanCreateAndDeleteServerCompleteThePeerLifecycle(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: "http { upstream backend {\n" +
			" keepalive 16;\n" +
			" server 127.0.0.1;\n" +
			" server 127.0.0.2;\n" +
			"} }\n",
	}}, nil)
	catalog := BuildCatalog(project)
	group := catalog.Upstreams[0]
	create, err := PlanCreateServer(project, catalog, CreateServerInput{
		UpstreamID: group.ID,
		Server: ServerInput{
			Endpoint: Endpoint{Address: "127.0.0.3", Port: port(8080)},
		},
	})
	if err != nil {
		t.Fatalf("PlanCreateServer() error = %v", err)
	}
	created, err := project.ApplyEdits(create.Edits)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created["nginx.conf"], "server 127.0.0.3:8080;") ||
		!strings.Contains(created["nginx.conf"], "keepalive 16;") {
		t.Fatalf("created source = %q", created["nginx.conf"])
	}

	remove, err := PlanDeleteServer(project, catalog, DeleteServerInput{
		UpstreamID: group.ID,
		ServerID:   group.Servers[0].ID,
	})
	if err != nil {
		t.Fatalf("PlanDeleteServer() error = %v", err)
	}
	removed, err := project.ApplyEdits(remove.Edits)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(removed["nginx.conf"], "server 127.0.0.1;") ||
		!strings.Contains(removed["nginx.conf"], "server 127.0.0.2;") {
		t.Fatalf("removed source = %q", removed["nginx.conf"])
	}
}

func TestPlanDeleteRemovesOneUnreferencedUpstreamAndKeepsAdjacentSyntax(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path: "nginx.conf",
		Source: "http {\n" +
			" # retained explanation\n" +
			" upstream remove_me { server 127.0.0.1; }\n" +
			" upstream keep_me { server 127.0.0.2; }\n" +
			"}\n",
	}}, nil)
	catalog := BuildCatalog(project)
	target := catalog.Upstreams[0]
	plan, err := PlanDelete(project, catalog, DeleteInput{
		UpstreamID: target.ID, ConfirmName: target.Name,
	})
	if err != nil {
		t.Fatalf("PlanDelete() error = %v", err)
	}
	rendered, err := project.ApplyEdits(plan.Edits)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered["nginx.conf"], "upstream remove_me") ||
		!strings.Contains(rendered["nginx.conf"], "# retained explanation") ||
		!strings.Contains(rendered["nginx.conf"], "upstream keep_me") {
		t.Fatalf("rendered source = %q", rendered["nginx.conf"])
	}
}

func TestPlanDeleteServerRejectsDeletingTheOnlyDownServer(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path:   "nginx.conf",
		Source: "http { upstream backend { server 127.0.0.1 down; } }\n",
	}}, nil)
	catalog := BuildCatalog(project)
	_, err := PlanDeleteServer(project, catalog, DeleteServerInput{
		UpstreamID: catalog.Upstreams[0].ID,
		ServerID:   catalog.Upstreams[0].Servers[0].ID,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanDeleteServer() error = %v, want ErrInvalid", err)
	}
}

func TestServerPlansRejectAResultWithoutAnyUsablePeer(t *testing.T) {
	t.Parallel()

	emptyProject := mustProject(t, []nginxast.SourceFile{{
		Path:   "nginx.conf",
		Source: "http { upstream backend { } }\n",
	}}, nil)
	emptyCatalog := BuildCatalog(emptyProject)
	_, err := PlanCreateServer(emptyProject, emptyCatalog, CreateServerInput{
		UpstreamID: emptyCatalog.Upstreams[0].ID,
		Server: ServerInput{
			Endpoint: Endpoint{Address: "127.0.0.1"},
			Down:     true,
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanCreateServer(down only) error = %v, want ErrInvalid", err)
	}

	project := mustProject(t, []nginxast.SourceFile{{
		Path:   "nginx.conf",
		Source: "http { upstream backend { server 127.0.0.1; } }\n",
	}}, nil)
	catalog := BuildCatalog(project)
	group := catalog.Upstreams[0]
	_, err = PlanUpdateServer(project, catalog, UpdateServerInput{
		UpstreamID: group.ID,
		ServerID:   group.Servers[0].ID,
		Server: ServerInput{
			Endpoint: Endpoint{Address: "127.0.0.1"},
			Down:     true,
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanUpdateServer(last down) error = %v, want ErrInvalid", err)
	}

	httpID := blockID(t, emptyProject, "http")
	_, err = PlanCreate(emptyProject, emptyCatalog, CreateInput{
		HTTPBlockID: httpID,
		Name:        "all_down",
		Servers: []ServerInput{{
			Endpoint: Endpoint{Address: "127.0.0.2"},
			Down:     true,
		}},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("PlanCreate(all down) error = %v, want ErrInvalid", err)
	}
}

func TestServerPlansFailClosedForIncompleteProjects(t *testing.T) {
	t.Parallel()

	project := mustProject(t, []nginxast.SourceFile{{
		Path:   "nginx.conf",
		Source: "http { upstream backend { server 127.0.0.1; server 127.0.0.2; } }\n",
	}}, nil)
	catalog := BuildCatalog(project)
	group := catalog.Upstreams[0]
	server := group.Servers[0]
	project.Complete = false

	checks := []struct {
		name string
		plan func() (Plan, error)
	}{
		{
			name: "create",
			plan: func() (Plan, error) {
				return PlanCreateServer(project, catalog, CreateServerInput{
					UpstreamID: group.ID,
					Server:     ServerInput{Endpoint: Endpoint{Address: "127.0.0.3"}},
				})
			},
		},
		{
			name: "update",
			plan: func() (Plan, error) {
				return PlanUpdateServer(project, catalog, UpdateServerInput{
					UpstreamID: group.ID,
					ServerID:   server.ID,
					Server:     ServerInput{Endpoint: Endpoint{Address: "127.0.0.4"}},
				})
			},
		},
		{
			name: "delete",
			plan: func() (Plan, error) {
				return PlanDeleteServer(project, catalog, DeleteServerInput{
					UpstreamID: group.ID,
					ServerID:   server.ID,
				})
			},
		},
	}
	for _, check := range checks {
		check := check
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()
			if _, err := check.plan(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("plan error = %v, want ErrInvalid", err)
			}
		})
	}
}

func blockID(t *testing.T, project *nginxast.Project, name string) string {
	t.Helper()
	for _, reference := range project.Nodes {
		block, ok := reference.Node.(*nginxast.Block)
		if ok && block.Name.Value == name {
			return reference.ID
		}
	}
	t.Fatalf("block %q not found", name)
	return ""
}

func port(value uint16) *uint16 {
	return &value
}

func intPointer(value int) *int {
	return &value
}
