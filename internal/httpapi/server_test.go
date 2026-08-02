/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"bytes"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

const testCSPNoncePlaceholder = "__NGINX_UIX_CSP_NONCE__"

func TestSPAFallbackInjectsFreshNonceIntoCSPAndHTML(t *testing.T) {
	t.Parallel()

	index := `<!doctype html><meta name="nginx-uix-csp-nonce" content="` + testCSPNoncePlaceholder + `"><div id="app"></div>`
	handler := NewHandler(Dependencies{Assets: fstest.MapFS{
		"index.html": {Data: []byte(index)},
	}})
	noncePattern := regexp.MustCompile(`'nonce-([0-9a-f]{32})'`)
	nonces := make([]string, 0, 2)
	for range 2 {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/config/workspaces", nil)
		request.Header.Set("X-Request-ID", "csp-test-request")

		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %q", recorder.Code, http.StatusOK, recorder.Body.String())
		}
		matches := noncePattern.FindStringSubmatch(recorder.Header().Get("Content-Security-Policy"))
		if len(matches) != 2 {
			t.Fatalf("Content-Security-Policy = %q, want one 128-bit style nonce", recorder.Header().Get("Content-Security-Policy"))
		}
		nonce := matches[1]
		nonces = append(nonces, nonce)
		if !strings.Contains(recorder.Body.String(), `content="`+nonce+`"`) ||
			strings.Contains(recorder.Body.String(), testCSPNoncePlaceholder) {
			t.Fatalf("HTML nonce bootstrap does not match CSP: %q", recorder.Body.String())
		}
		if strings.Contains(recorder.Header().Get("Content-Security-Policy"), "unsafe-inline") {
			t.Fatalf("Content-Security-Policy enables unsafe-inline: %q", recorder.Header().Get("Content-Security-Policy"))
		}
	}
	if nonces[0] == nonces[1] {
		t.Fatalf("HTML responses reused nonce %q", nonces[0])
	}
}

func TestSPAFallbackUsesNonceSourceIndependentFromRequestID(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Dependencies{
		Assets:          fstest.MapFS{"index.html": {Data: testNonceIndexHTML("spa index")}},
		RequestIDSource: bytes.NewReader(bytes.Repeat([]byte{0x2a}, 16)),
		CSPNonceSource:  bytes.NewReader(bytes.Repeat([]byte{0x3b}, 16)),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(recorder, request)

	if got, want := recorder.Header().Get("X-Request-ID"), strings.Repeat("2a", 16); got != want {
		t.Fatalf("X-Request-ID = %q, want %q", got, want)
	}
	wantNonce := strings.Repeat("3b", 16)
	if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "'nonce-"+wantNonce+"'") {
		t.Fatalf("Content-Security-Policy = %q, want independent nonce %q", csp, wantNonce)
	}
	if !strings.Contains(recorder.Body.String(), `content="`+wantNonce+`"`) {
		t.Fatalf("HTML nonce bootstrap = %q, want nonce %q", recorder.Body.String(), wantNonce)
	}
}

func TestSPAFallbackDoesNotLogCSPNonce(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	nonce := strings.Repeat("4c", 16)
	handler := NewHandler(Dependencies{
		Assets:         fstest.MapFS{"index.html": {Data: testNonceIndexHTML("spa index")}},
		CSPNonceSource: bytes.NewReader(bytes.Repeat([]byte{0x4c}, 16)),
		Logger:         slog.New(slog.NewTextHandler(&logs, nil)),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Request-ID", "nonce-log-test")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Security-Policy"), nonce) {
		t.Fatalf("status/CSP = %d/%q, want rendered nonce", recorder.Code, recorder.Header().Get("Content-Security-Policy"))
	}
	if strings.Contains(logs.String(), nonce) {
		t.Fatalf("request log contains CSP nonce: %s", logs.String())
	}
}

func TestSPAFallbackFailsClosedWithoutOnePlaceholderOrNonce(t *testing.T) {
	t.Parallel()

	tests := map[string]Dependencies{
		"missing placeholder": {
			Assets: fstest.MapFS{"index.html": {Data: []byte("spa index")}},
		},
		"duplicate placeholder": {
			Assets: fstest.MapFS{"index.html": {Data: []byte(testCSPNoncePlaceholder + testCSPNoncePlaceholder)}},
		},
		"random source failure": {
			Assets:         fstest.MapFS{"index.html": {Data: testNonceIndexHTML("spa index")}},
			CSPNonceSource: failingReader{},
		},
	}
	for name, dependencies := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("X-Request-ID", "csp-failure-test")

			NewHandler(dependencies).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d; body = %q", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Security-Policy"); got != baseContentSecurityPolicy {
				t.Fatalf("Content-Security-Policy = %q, want fail-closed baseline %q", got, baseContentSecurityPolicy)
			}
			if strings.Contains(recorder.Body.String(), "spa index") || strings.Contains(recorder.Body.String(), testCSPNoncePlaceholder) {
				t.Fatalf("failure response exposed unprotected HTML: %q", recorder.Body.String())
			}
		})
	}
}

func TestSPAFallbackServesHistoryRoutesAndStaticAssets(t *testing.T) {
	t.Parallel()

	assets := productionShapeAssets(t, fstest.MapFS{
		"dist/index.html":    &fstest.MapFile{Data: testNonceIndexHTML("<div id=\"app\"></div>")},
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
			if got := recorder.Body.String(); !strings.Contains(got, "<div id=\"app\"></div>") || strings.Contains(got, testCSPNoncePlaceholder) {
				t.Fatalf("body = %q, want rendered SPA index", got)
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
		"dist/index.html": &fstest.MapFile{Data: testNonceIndexHTML("<div id=\"app\"></div>")},
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

func testNonceIndexHTML(body string) []byte {
	return []byte(`<!doctype html><meta name="nginx-uix-csp-nonce" content="` + testCSPNoncePlaceholder + `">` + body)
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
		"index.html": {Data: testNonceIndexHTML("spa index")},
	}})
	paths := []string{
		"/login",
		"/configuration",
		"/config/operations",
		"/config/route-lab",
		"/config/workspaces",
		"/config/workspaces/0123456789abcdef0123456789abcdef",
		"/config/workspaces/0123456789abcdef0123456789abcdef/upstreams",
		"/config/workspaces/0123456789abcdef0123456789abcdef/servers",
		"/certificates",
		"/certificates/0123456789abcdef0123456789abcdef",
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
				if method == http.MethodGet && !strings.Contains(recorder.Body.String(), "spa index") {
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
		"index.html":    {Data: testNonceIndexHTML("spa index")},
		"assets/app.js": {Data: []byte("application asset")},
		"styles.css":    {Data: []byte("style asset")},
	}})
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
		contains   bool
	}{
		{name: "root index", method: http.MethodGet, path: "/", wantStatus: http.StatusOK, wantBody: "spa index", contains: true},
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
		{name: "short certificate ID", method: http.MethodGet, path: "/certificates/0123456789abcdef", wantStatus: http.StatusNotFound},
		{name: "uppercase certificate ID", method: http.MethodGet, path: "/certificates/0123456789ABCDEF0123456789ABCDEF", wantStatus: http.StatusNotFound},
		{name: "nested certificate path", method: http.MethodGet, path: "/certificates/0123456789abcdef0123456789abcdef/history", wantStatus: http.StatusNotFound},
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
			if test.wantBody != "" && test.contains && !strings.Contains(recorder.Body.String(), test.wantBody) {
				t.Fatalf("body = %q, want content %q", recorder.Body.String(), test.wantBody)
			}
			if test.wantBody != "" && !test.contains && recorder.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", recorder.Body.String(), test.wantBody)
			}
			if test.wantStatus != http.StatusOK && strings.Contains(recorder.Body.String(), "spa index") {
				t.Fatalf("failure response served SPA index: %q", recorder.Body.String())
			}
		})
	}
}
