/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

func TestValidateRouteLabOptionsAcceptsTraversableProductionRoot(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "route-lab")
	for _, directory := range []string{production, workspaces, stages} {
		mustMkdirCandidate(t, directory)
	}
	if err := os.Chmod(production, 0o755); err != nil {
		t.Fatal(err)
	}

	options := defaultRouteLabOptions()
	options.NginxRoot = production
	options.WorkspaceRoot = workspaces
	options.StageRoot = stages
	if err := validateRouteLabOptions(options); err != nil {
		t.Fatalf("validateRouteLabOptions() error = %v", err)
	}
}

func TestValidateRouteLabOptionsRejectsUnsafeRootPermissions(t *testing.T) {
	tests := []struct {
		name       string
		root       string
		permission os.FileMode
	}{
		{name: "writable production root", root: "production", permission: 0o775},
		{name: "group-readable workspace root", root: "workspaces", permission: 0o750},
		{name: "world-traversable stage root", root: "route-lab", permission: 0o701},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			production := filepath.Join(root, "production")
			workspaces := filepath.Join(root, "workspaces")
			stages := filepath.Join(root, "route-lab")
			for _, directory := range []string{production, workspaces, stages} {
				mustMkdirCandidate(t, directory)
			}
			if err := os.Chmod(filepath.Join(root, test.root), test.permission); err != nil {
				t.Fatal(err)
			}

			options := defaultRouteLabOptions()
			options.NginxRoot = production
			options.WorkspaceRoot = workspaces
			options.StageRoot = stages
			if err := validateRouteLabOptions(options); !errors.Is(err, config.ErrPathInvalid) {
				t.Fatalf("validateRouteLabOptions() error = %v, want path invalid", err)
			}
		})
	}
}

func TestExecuteRouteTestMaterializesInstrumentsAndCleansCandidate(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "route-lab")
	for _, directory := range []string{production, workspaces, stages, filepath.Join(production, "conf.d"), filepath.Join(production, "ssl")} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {}\nhttp { include "+production+"/conf.d/*.conf; }\n", 0o640)
	mustWriteCandidate(t, filepath.Join(production, "ssl", "cert.pem"), "fixture certificate", 0o600)
	mustWriteCandidate(t, filepath.Join(production, "conf.d", "site.conf"), "server { listen 8080; server_name example.test; ssl_certificate "+production+"/ssl/cert.pem; location / { proxy_pass http://127.0.0.1:9000; } }\n", 0o640)

	workspaceID := config.WorkspaceID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, workspaceID, "", nil)
	validated, err := routelab.ValidateRequest(routelab.Request{
		StaticRequest: routelab.StaticRequest{
			Scheme: routelab.SchemeHTTP, Host: "example.test", Port: 8080, URI: "/",
		},
		Method: "GET", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	validated.SideEffecting = true
	validated.UpstreamSideEffect = true

	var observedStage string
	executor := func(_ context.Context, specification commandSpec) (commandResult, error) {
		if len(specification.arguments) != 5 || specification.arguments[0] != "-t" {
			t.Fatalf("validation command = %+v", specification)
		}
		observedStage = strings.TrimSuffix(specification.arguments[2], string(filepath.Separator))
		entry, readErr := os.ReadFile(filepath.Join(observedStage, "nginx.conf"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		site, readErr := os.ReadFile(filepath.Join(observedStage, "conf.d", "site.conf"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		combined := string(entry) + string(site)
		for _, required := range []string{"daemon off;", "127.0.0.1:18080", "nginx_uix_server_", "nginx_uix_route_"} {
			if !strings.Contains(combined, required) {
				t.Fatalf("instrumented candidate does not contain %q:\n%s", required, combined)
			}
		}
		if strings.Contains(combined, production) {
			t.Fatalf("production path survived isolation: %s", combined)
		}
		if !strings.Contains(combined, filepath.Join(observedStage, "ssl", "cert.pem")) {
			t.Fatalf("certificate path was not rebound to the sandbox stage: %s", combined)
		}
		return commandResult{exitCode: 0}, nil
	}
	runner := func(
		_ context.Context,
		run sandboxRun,
	) (routelab.Response, routelab.RuntimeEvidence, routelab.CleanupEvidence, error) {
		if run.StagePath != observedStage || run.TargetPort != 18080 || run.Request.Host != "example.test" {
			t.Fatalf("sandbox run = %+v", run)
		}
		if len(run.Routes) != 2 {
			t.Fatalf("routes = %+v", run.Routes)
		}
		return routelab.Response{StatusCode: 200, BodySnippet: "ok"}, routelab.RuntimeEvidence{
			ServerRouteID: run.Routes[0].RouteID,
			RouteID:       run.Routes[1].RouteID,
			FinalURI:      "/",
			StatusCode:    200,
		}, routelab.CleanupEvidence{MasterReaped: true, PortClosed: true}, nil
	}
	service := mustRouteLabService(t, routeLabOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(), Executor: executor,
		ReservePorts: func([]routelab.ListenerKey) (map[routelab.ListenerKey]int, func() error, error) {
			return map[routelab.ListenerKey]int{{Address: "*", Port: 8080}: 18080}, func() error { return nil }, nil
		},
		RunSandbox: runner,
	})
	result, err := service.ExecuteRouteTest(context.Background(), routelab.AgentRequest{
		RunID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", WorkspaceID: workspaceID,
		ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
		Request: validated, RequestID: "req-route",
	})
	if err != nil {
		t.Fatalf("ExecuteRouteTest() error = %v", err)
	}
	if result.Response.StatusCode != 200 || result.Evidence.RouteID == "" || result.CandidateDigest == (config.Digest{}) ||
		!result.Cleanup.MasterReaped || !result.Cleanup.PortClosed || !result.Cleanup.StageRemoved {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(observedStage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("route stage survived: %v", err)
	}
	assertCandidateStageEmpty(t, stages)
}

func TestExecuteRouteTestCancellationStillRemovesStage(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "route-lab")
	for _, directory := range []string{production, workspaces, stages} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {} http { server { listen 8080; } }\n", 0o640)
	workspaceID := config.WorkspaceID("cccccccccccccccccccccccccccccccc")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, workspaceID, "", nil)
	validated, err := routelab.ValidateRequest(routelab.Request{
		StaticRequest: routelab.StaticRequest{Scheme: routelab.SchemeHTTP, Host: "example.test", Port: 8080, URI: "/"},
		Method:        "GET", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 1)
	service := mustRouteLabService(t, routeLabOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: func(context.Context, commandSpec) (commandResult, error) {
			return commandResult{exitCode: 0}, nil
		},
		ReservePorts: func([]routelab.ListenerKey) (map[routelab.ListenerKey]int, func() error, error) {
			return map[routelab.ListenerKey]int{{Address: "*", Port: 8080}: 18081}, func() error { return nil }, nil
		},
		RunSandbox: func(ctx context.Context, run sandboxRun) (
			routelab.Response,
			routelab.RuntimeEvidence,
			routelab.CleanupEvidence,
			error,
		) {
			started <- run.StagePath
			<-ctx.Done()
			return routelab.Response{}, routelab.RuntimeEvidence{}, routelab.CleanupEvidence{
				MasterReaped: true, PortClosed: true,
			}, ctx.Err()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, runErr := service.ExecuteRouteTest(ctx, routelab.AgentRequest{
			RunID: "dddddddddddddddddddddddddddddddd", WorkspaceID: workspaceID,
			ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
			Request: validated, RequestID: "req-cancel",
		})
		finished <- runErr
	}()
	stage := <-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteRouteTest() error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled route stage survived: %v", err)
	}
	assertCandidateStageEmpty(t, stages)
}

func TestExecuteRouteTestRetainsOwnedStageWhenMasterCleanupIsUnproven(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "route-lab")
	for _, directory := range []string{production, workspaces, stages} {
		mustMkdirCandidate(t, directory)
	}
	mustWriteCandidate(t, filepath.Join(production, "nginx.conf"), "events {} http { server { listen 8080; } }\n", 0o640)
	workspaceID := config.WorkspaceID("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	manifest, productionDigest := mustCandidateWorkspace(t, production, workspaces, workspaceID, "", nil)
	validated, err := routelab.ValidateRequest(routelab.Request{
		StaticRequest: routelab.StaticRequest{Scheme: routelab.SchemeHTTP, Host: "example.test", Port: 8080, URI: "/"},
		Method:        "GET", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := mustRouteLabService(t, routeLabOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		Entry: "nginx.conf", Limits: config.DefaultLimits(),
		Executor: func(context.Context, commandSpec) (commandResult, error) {
			return commandResult{exitCode: 0}, nil
		},
		ReservePorts: func([]routelab.ListenerKey) (map[routelab.ListenerKey]int, func() error, error) {
			return map[routelab.ListenerKey]int{{Address: "*", Port: 8080}: 18082}, func() error { return nil }, nil
		},
		RunSandbox: func(context.Context, sandboxRun) (
			routelab.Response,
			routelab.RuntimeEvidence,
			routelab.CleanupEvidence,
			error,
		) {
			return routelab.Response{}, routelab.RuntimeEvidence{}, routelab.CleanupEvidence{}, routelab.ErrCleanupFailed
		},
	})
	result, err := service.ExecuteRouteTest(context.Background(), routelab.AgentRequest{
		RunID: "ffffffffffffffffffffffffffffffff", WorkspaceID: workspaceID,
		ProductionDigest: productionDigest, DraftDigest: manifest.Digest(),
		Request: validated, RequestID: "req-cleanup-unproven",
	})
	if !errors.Is(err, routelab.ErrCleanupFailed) || result.Cleanup.StageRemoved {
		t.Fatalf("ExecuteRouteTest() = %+v, %v", result, err)
	}
	entries, readErr := os.ReadDir(stages)
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("retained route stages = %v, %v", entries, readErr)
	}
	if _, markerErr := readRouteOwnerMarker(filepath.Join(stages, entries[0].Name())); markerErr != nil {
		t.Fatalf("retained stage has no valid owner marker: %v", markerErr)
	}
}

func mustRouteLabService(t *testing.T, options routeLabOptions) *Service {
	t.Helper()
	service, err := newRouteLabService(options)
	if err != nil {
		t.Fatalf("newRouteLabService() error = %v", err)
	}
	return service
}

func TestReconcileRouteLabArtifactsRemovesOnlyProvenOwnedStage(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	workspaces := filepath.Join(root, "workspaces")
	stages := filepath.Join(root, "route-lab")
	for _, directory := range []string{production, workspaces, stages} {
		mustMkdirCandidate(t, directory)
	}
	owned := filepath.Join(stages, ".route-owned")
	mustMkdirCandidate(t, owned)
	if err := writeRouteOwnerMarker(owned, routeOwnerMarker{
		Version: 1, RunID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}); err != nil {
		t.Fatal(err)
	}
	service := mustRouteLabService(t, routeLabOptions{
		NginxRoot: production, WorkspaceRoot: workspaces, StageRoot: stages,
		NginxExecutable: "/usr/sbin/nginx",
	})
	if err := service.ReconcileRouteLabArtifacts(context.Background()); err != nil {
		t.Fatalf("ReconcileRouteLabArtifacts() error = %v", err)
	}
	if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned stage survived: %v", err)
	}

	unowned := filepath.Join(stages, ".route-unowned")
	mustMkdirCandidate(t, unowned)
	if err := service.ReconcileRouteLabArtifacts(context.Background()); !errors.Is(err, routelab.ErrCleanupFailed) {
		t.Fatalf("ReconcileRouteLabArtifacts() error = %v, want ErrCleanupFailed", err)
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatalf("unowned stage was removed: %v", err)
	}
}
