/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAFallbackServesHistoryRoutesAndStaticAssets(t *testing.T) {
	t.Parallel()

	assets := productionShapeAssets(t, fstest.MapFS{
		"dist/index.html":    &fstest.MapFile{Data: []byte("<!doctype html><div id=\"app\"></div>")},
		"dist/assets/app.js": &fstest.MapFile{Data: []byte("console.log('nginx-uix')")},
	})
	handler := NewHandler(Dependencies{Assets: assets})

	for _, path := range []string{"/", "/login", "/configuration"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if got, want := recorder.Body.String(), "<!doctype html><div id=\"app\"></div>"; got != want {
				t.Fatalf("body = %q, want %q", got, want)
			}
			if got, want := recorder.Header().Get("Cache-Control"), "no-store"; got != want {
				t.Fatalf("Cache-Control = %q, want %q", got, want)
			}
			if got := recorder.Header().Get("Content-Security-Policy"); got == "" {
				t.Fatal("Content-Security-Policy is empty")
			}
		})
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("static asset status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got, want := recorder.Body.String(), "console.log('nginx-uix')"; got != want {
		t.Fatalf("static asset body = %q, want %q", got, want)
	}
}

func TestSPAFallbackDoesNotSwallowReservedRoutesOrMethods(t *testing.T) {
	t.Parallel()

	assets := productionShapeAssets(t, fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html><div id=\"app\"></div>")},
	})
	handler := NewHandler(Dependencies{Assets: assets})

	for _, path := range []string{"/api/v1/unknown", "/health/unknown", "/assets/missing.js"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
			}
		})
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/configuration", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported method status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if got := recorder.Header().Get("Allow"); !strings.Contains(got, http.MethodGet) {
		t.Fatalf("Allow = %q, want GET", got)
	}
}

func productionShapeAssets(t *testing.T, files fstest.MapFS) fs.FS {
	t.Helper()

	assets, err := fs.Sub(files, "dist")
	if err != nil {
		t.Fatalf("create production-shape assets: %v", err)
	}
	return assets
}

func TestLiveDoesNotExposeRuntimeDetails(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	NewHandler(Dependencies{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got, want := recorder.Body.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got, want := recorder.Header().Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("content-type = %q, want %q", got, want)
	}
	for _, forbidden := range []string{"pid", "version", "/etc/", "/var/"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("body exposes %q: %q", forbidden, recorder.Body.String())
		}
	}
}

func TestSPAFallbackServesIndexOnlyForKnownNavigationRoutes(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Dependencies{Assets: fstest.MapFS{
		"index.html": {Data: []byte("spa index")},
	}})
	paths := []string{
		"/login",
		"/configuration",
		"/config/workspaces",
		"/config/workspaces/0123456789abcdef0123456789abcdef",
		"/config/workspaces/0123456789abcdef0123456789abcdef/upstreams",
		"/config/workspaces/0123456789abcdef0123456789abcdef/servers",
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range paths {
			t.Run(method+" "+path, func(t *testing.T) {
				t.Parallel()
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(method, path, nil)

				handler.ServeHTTP(recorder, request)

				if recorder.Code != http.StatusOK {
					t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
				}
				if method == http.MethodGet && recorder.Body.String() != "spa index" {
					t.Fatalf("body = %q, want SPA index", recorder.Body.String())
				}
				if method == http.MethodHead && recorder.Body.Len() != 0 {
					t.Fatalf("HEAD body = %q, want empty", recorder.Body.String())
				}
			})
		}
	}
}

func TestSPAFallbackServesAssetsFirstAndRejectsUnknownSurfaces(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Dependencies{Assets: fstest.MapFS{
		"index.html":    {Data: []byte("spa index")},
		"assets/app.js": {Data: []byte("application asset")},
		"styles.css":    {Data: []byte("style asset")},
	}})
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "root index", method: http.MethodGet, path: "/", wantStatus: http.StatusOK, wantBody: "spa index"},
		{name: "nested asset", method: http.MethodGet, path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "application asset"},
		{name: "root asset", method: http.MethodGet, path: "/styles.css", wantStatus: http.StatusOK, wantBody: "style asset"},
		{name: "missing JavaScript", method: http.MethodGet, path: "/assets/missing.js", wantStatus: http.StatusNotFound},
		{name: "missing stylesheet", method: http.MethodGet, path: "/missing.css", wantStatus: http.StatusNotFound},
		{name: "unknown path", method: http.MethodGet, path: "/unknown", wantStatus: http.StatusNotFound},
		{name: "unknown API", method: http.MethodGet, path: "/api/v1/unknown", wantStatus: http.StatusNotFound},
		{name: "unknown health", method: http.MethodGet, path: "/health/unknown", wantStatus: http.StatusNotFound},
		{name: "short workspace ID", method: http.MethodGet, path: "/config/workspaces/0123456789abcdef", wantStatus: http.StatusNotFound},
		{name: "uppercase workspace ID", method: http.MethodGet, path: "/config/workspaces/0123456789ABCDEF0123456789ABCDEF", wantStatus: http.StatusNotFound},
		{name: "nested workspace path", method: http.MethodGet, path: "/config/workspaces/0123456789abcdef0123456789abcdef/files", wantStatus: http.StatusNotFound},
		{name: "POST navigation", method: http.MethodPost, path: "/login", wantStatus: http.StatusMethodNotAllowed},
		{name: "PUT navigation", method: http.MethodPut, path: "/configuration", wantStatus: http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, nil)

			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if test.wantBody != "" && recorder.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", recorder.Body.String(), test.wantBody)
			}
			if test.wantStatus != http.StatusOK && strings.Contains(recorder.Body.String(), "spa index") {
				t.Fatalf("failure response served SPA index: %q", recorder.Body.String())
			}
		})
	}
}
