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
