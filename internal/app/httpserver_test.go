package app

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dstmrk/octotracker/internal/health"
	"github.com/dstmrk/octotracker/webapp"
)

const indexHTML = "<html>index</html>"

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte(indexHTML)},
		"css/style.css": {Data: []byte("body{}")},
		"js/app.js":     {Data: []byte("console.log(1)")},
	}
}

func newTestHTTP(t *testing.T, webFS fs.FS) http.Handler {
	t.Helper()
	return NewHTTPHandler(newTestStore(t), health.NewRegistry(), "", webFS)
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

// TestStaticFiles: /app/ restituisce index.html, CSS e JavaScript hanno il
// Content-Type giusto anche con ?v=N, i file inesistenti danno 404 e i
// metodi diversi da GET e HEAD 405.
func TestStaticFiles(t *testing.T) {
	h := newTestHTTP(t, testFS())
	cases := []struct {
		method, target string
		code           int
		ct, body       string
	}{
		{http.MethodGet, "/app/", 200, "text/html", indexHTML},
		{http.MethodHead, "/app/", 200, "text/html", ""},
		{http.MethodGet, "/app/css/style.css?v=3", 200, "text/css", "body{}"},
		{http.MethodGet, "/app/js/app.js?v=3", 200, "text/javascript", "console.log(1)"},
		{http.MethodGet, "/app/js/manca.js", 404, "", ""},
		{http.MethodPost, "/app/", 405, "", ""},
	}
	for _, tc := range cases {
		w := do(h, tc.method, tc.target)
		if w.Code != tc.code {
			t.Errorf("%s %s: status %d, atteso %d", tc.method, tc.target, w.Code, tc.code)
			continue
		}
		if tc.code != http.StatusOK {
			continue
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.ct) {
			t.Errorf("%s: Content-Type %q, atteso %q", tc.target, ct, tc.ct)
		}
		if w.Body.String() != tc.body {
			t.Errorf("%s: corpo %q", tc.target, w.Body.String())
		}
	}
}

// TestHTTPRoutes: /health e le API rispondono sui loro percorsi esatti,
// ogni altro percorso dà 404.
func TestHTTPRoutes(t *testing.T) {
	h := newTestHTTP(t, testFS())

	w := do(h, http.MethodGet, "/health")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"scraper": "missing"`) {
		t.Errorf("/health: %d %s", w.Code, w.Body.String())
	}
	for _, target := range []string{"/api/rates/history", "/api/rates/current", "/api/user/rates"} {
		w := do(h, http.MethodGet, target)
		if w.Code != http.StatusUnauthorized || w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s: %d %v", target, w.Code, w.Header())
		}
	}
	for _, target := range []string{"/health/", "/", "/api/rates/history/", "/altro"} {
		if w := do(h, http.MethodGet, target); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
}

// TestHTTPServesEmbeddedMiniApp: i file reali della Mini App incorporati.
func TestHTTPServesEmbeddedMiniApp(t *testing.T) {
	h := newTestHTTP(t, webapp.FS())
	index, err := os.ReadFile("../../webapp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	w := do(h, http.MethodGet, "/app/")
	if w.Code != 200 || w.Body.String() != string(index) {
		t.Fatalf("/app/: %d", w.Code)
	}
	for target, ct := range map[string]string{"/app/css/style.css?v=3": "text/css", "/app/js/app.js?v=3": "text/javascript"} {
		w := do(h, http.MethodGet, target)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), ct) || w.Body.Len() == 0 {
			t.Errorf("%s: %d %q", target, w.Code, w.Header().Get("Content-Type"))
		}
	}
}
