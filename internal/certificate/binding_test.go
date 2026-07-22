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

const (
	testBindingCertificateID CertificateID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testBindingVersionID     VersionID     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestBuildServerCandidatesProducesSecretFreeStableRefs(t *testing.T) {
	project := bindingProject(t, "events {}\nhttp {\n  server { listen 80; server_name Example.COM www.example.com; }\n}\n")
	candidates, err := BuildServerCandidates(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !candidates[0].Editable {
		t.Fatalf("candidates = %#v", candidates)
	}
	ref := candidates[0].Ref
	if ref.Path != "nginx.conf" || ref.StartOffset <= 0 || len(ref.Fingerprint) != 64 {
		t.Fatalf("ref = %#v", ref)
	}
	if strings.Join(ref.ServerNames, ",") != "example.com,www.example.com" || strings.Join(ref.Listeners, ",") != "80" {
		t.Fatalf("normalized ref = %#v", ref)
	}

	shifted := bindingProject(t, "# unrelated preface\n"+project.Documents["nginx.conf"].Document.Render())
	resolved, err := ResolveServerRefs(shifted, []ServerRef{ref})
	if err != nil || len(resolved) != 1 || resolved[0].Ref.StartOffset == ref.StartOffset {
		t.Fatalf("ResolveServerRefs() = %#v, %v", resolved, err)
	}
}

func TestPlanCertificateBindingReplacesOnlyDirectTLSSettings(t *testing.T) {
	source := `events {}
http {
    server {
        listen 443 default_server;
        server_name example.com;
        # keep this policy comment
        ssl_certificate /old/fullchain.pem;
        ssl_certificate_key /old/privkey.pem;
        ssl_protocols TLSv1.3;
    }
}
`
	project := bindingProject(t, source)
	ref := oneEditableServerRef(t, project)
	plan, err := PlanCertificateBinding(context.Background(), project, []ServerRef{ref}, testBindingCertificateID, testBindingVersionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || !strings.Contains(plan.Files[0].Patch, "ssl_certificate /var/lib/nginx-uix/certs/") {
		t.Fatalf("plan = %#v", plan)
	}
	want := strings.ReplaceAll(source, "listen 443 default_server;", "listen 443 default_server ssl;")
	want = strings.ReplaceAll(want, "/old/fullchain.pem", CertificateFullchainPath(testBindingCertificateID, testBindingVersionID))
	want = strings.ReplaceAll(want, "/old/privkey.pem", CertificatePrivateKeyPath(testBindingCertificateID, testBindingVersionID))
	if plan.Files[0].After != want {
		t.Fatalf("after =\n%s\nwant =\n%s", plan.Files[0].After, want)
	}
	if !strings.Contains(plan.Files[0].After, "# keep this policy comment") || !strings.Contains(plan.Files[0].After, "ssl_protocols TLSv1.3;") {
		t.Fatal("unrelated syntax was not preserved")
	}
}

func TestPlanCertificateBindingAppendsMissingSettingsWithLocalFormatting(t *testing.T) {
	source := "events {}\r\nhttp {\r\n\tserver {\r\n\t\tlisten 80;\r\n\t\tserver_name example.com;\r\n\t}\r\n}\r\n"
	project := bindingProject(t, source)
	plan, err := PlanCertificateBinding(
		context.Background(), project, []ServerRef{oneEditableServerRef(t, project)},
		testBindingCertificateID, testBindingVersionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	after := plan.Files[0].After
	for _, line := range []string{
		"\t\tlisten 443 ssl;\r\n",
		"\t\tssl_certificate " + CertificateFullchainPath(testBindingCertificateID, testBindingVersionID) + ";\r\n",
		"\t\tssl_certificate_key " + CertificatePrivateKeyPath(testBindingCertificateID, testBindingVersionID) + ";\r\n",
	} {
		if !strings.Contains(after, line) {
			t.Fatalf("after does not contain %q:\n%s", line, after)
		}
	}
	if strings.Contains(strings.ReplaceAll(after, "\r\n", ""), "\n") {
		t.Fatal("binding changed CRLF line endings")
	}
}

func TestPlanCertificateBindingRejectsAmbiguousOrDynamicCertificateSyntax(t *testing.T) {
	tests := []string{
		"events {} http { server { server_name example.com; ssl_certificate /a; ssl_certificate /b; } }",
		"events {} http { server { server_name example.com; ssl_certificate $dynamic_path; } }",
	}
	for _, source := range tests {
		project := bindingProject(t, source)
		_, err := PlanCertificateBinding(
			context.Background(), project, []ServerRef{oneEditableServerRef(t, project)},
			testBindingCertificateID, testBindingVersionID,
		)
		if !errors.Is(err, ErrBindingConflict) {
			t.Fatalf("PlanCertificateBinding(%q) error = %v, want ErrBindingConflict", source, err)
		}
	}
}

func TestPlanCertificateUnbindingDeletesOnlyExactOwnedPaths(t *testing.T) {
	fullchain := CertificateFullchainPath(testBindingCertificateID, testBindingVersionID)
	privateKey := CertificatePrivateKeyPath(testBindingCertificateID, testBindingVersionID)
	source := "events {}\nhttp {\n  server {\n    listen 443 ssl;\n    server_name example.com;\n" +
		"    ssl_certificate " + fullchain + ";\n    ssl_certificate_key " + privateKey + ";\n" +
		"    ssl_protocols TLSv1.3;\n  }\n}\n"
	project := bindingProject(t, source)
	plan, err := PlanCertificateUnbinding(
		context.Background(), project, []ServerRef{oneEditableServerRef(t, project)},
		testBindingCertificateID, testBindingVersionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	after := plan.Files[0].After
	if strings.Contains(after, "ssl_certificate ") || strings.Contains(after, "ssl_certificate_key ") {
		t.Fatalf("certificate directives remain:\n%s", after)
	}
	if !strings.Contains(after, "listen 443 ssl;") || !strings.Contains(after, "ssl_protocols TLSv1.3;") {
		t.Fatalf("unbinding changed user TLS policy:\n%s", after)
	}

	other := strings.Replace(source, fullchain, "/etc/other/fullchain.pem", 1)
	otherProject := bindingProject(t, other)
	_, err = PlanCertificateUnbinding(
		context.Background(), otherProject, []ServerRef{oneEditableServerRef(t, otherProject)},
		testBindingCertificateID, testBindingVersionID,
	)
	if !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("foreign path unbind error = %v, want ErrBindingConflict", err)
	}
}

func bindingProject(t *testing.T, source string) *nginxast.Project {
	t.Helper()
	project, err := nginxast.BuildProject(
		[]nginxast.SourceFile{{Path: "nginx.conf", Source: source}}, nil, nginxast.DefaultProjectLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !project.Complete {
		t.Fatalf("project is incomplete: %#v", project.Diagnostics)
	}
	return project
}

func oneEditableServerRef(t *testing.T, project *nginxast.Project) ServerRef {
	t.Helper()
	candidates, err := BuildServerCandidates(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !candidates[0].Editable {
		t.Fatalf("candidates = %#v", candidates)
	}
	return candidates[0].Ref
}
