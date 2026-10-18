package app

import (
	"crypto/sha512"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dstmrk/octotracker/internal/health"
	"github.com/dstmrk/octotracker/webapp"
)

var testModTime = time.Date(2026, 10, 1, 12, 0, 0, 500, time.UTC)

const indexHTML = "<html>index</html>"

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     {Data: []byte(indexHTML)},
		"css/style.css":  {Data: []byte("body{}")},
		"js/app.js":      {Data: []byte("console.log(1)")},
		"sub/index.html": {Data: []byte("sub")},
		"data.bin":       {Data: []byte("0123456789")},
		"pipe":           {Mode: fs.ModeNamedPipe},
	}
}

func newTestHTTP(t *testing.T, webFS fs.FS) http.Handler {
	t.Helper()
	return NewHTTPHandler(newTestStore(t), health.NewRegistry(), "", webFS, testModTime)
}

func do(h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func etagOf(data string) string {
	sum := sha512.Sum512([]byte(data))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func tornadoPage(code int, reason string) string {
	c := strconv.Itoa(code)
	return "<html><title>" + c + ": " + reason + "</title><body>" + c + ": " + reason + "</body></html>"
}

// TestStaticServesIndex: /app/ e /app/index.html restituiscono la pagina,
// senza redirect, con gli header di StaticFileHandler.
func TestStaticServesIndex(t *testing.T) {
	h := newTestHTTP(t, testFS())
	for _, target := range []string{"/app/", "/app/index.html", "/app/index.html/"} {
		w := do(h, http.MethodGet, target, nil)
		if w.Code != http.StatusOK || w.Body.String() != indexHTML {
			t.Fatalf("%s: status %d corpo %q", target, w.Code, w.Body.String())
		}
		hdr := w.Header()
		if hdr.Get("Content-Type") != "text/html" || hdr.Get("Accept-Ranges") != "bytes" ||
			hdr.Get("Etag") != etagOf(indexHTML) || hdr.Get("Last-Modified") != "Thu, 01 Oct 2026 12:00:00 GMT" ||
			hdr.Get("Content-Length") != strconv.Itoa(len(indexHTML)) {
			t.Errorf("%s: header = %v", target, hdr)
		}
		if hdr.Get("Cache-Control") != "" || hdr.Get("Expires") != "" {
			t.Errorf("%s: cache senza ?v: %v", target, hdr)
		}
	}
	if w := do(h, http.MethodGet, "/app/sub/", nil); w.Code != http.StatusOK || w.Body.String() != "sub" {
		t.Errorf("/app/sub/: %d %q", w.Code, w.Body.String())
	}
}

// TestStaticContentTypesAndCacheBusting: Content-Type da mimetypes (senza
// charset) e cache di 10 anni con il parametro v.
func TestStaticContentTypesAndCacheBusting(t *testing.T) {
	prev := staticNow
	staticNow = func() time.Time { return time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC) }
	defer func() { staticNow = prev }()
	h := newTestHTTP(t, testFS())

	cases := []struct{ target, ct, cache string }{
		{"/app/css/style.css?v=3", "text/css", "max-age=315360000"},
		{"/app/js/app.js?v=3", "text/javascript", "max-age=315360000"},
		{"/app/js/app.js?v=", "text/javascript", "max-age=315360000"},
		{"/app/js/app.js?x=1", "text/javascript", ""},
		{"/app/data.bin", "application/octet-stream", ""},
	}
	for _, tc := range cases {
		w := do(h, http.MethodGet, tc.target, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.target, w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != tc.ct {
			t.Errorf("%s: Content-Type %q", tc.target, got)
		}
		if got := w.Header().Get("Cache-Control"); got != tc.cache {
			t.Errorf("%s: Cache-Control %q", tc.target, got)
		}
		if tc.cache != "" && w.Header().Get("Expires") != "Wed, 15 Oct 2036 10:00:00 GMT" {
			t.Errorf("%s: Expires %q", tc.target, w.Header().Get("Expires"))
		}
	}
}

// TestStaticErrors copre route mancanti, redirect delle directory, percorsi
// fuori dalla radice, file inesistenti, UTF-8 non valido e metodi.
func TestStaticErrors(t *testing.T) {
	logs := captureLogs(t)
	h := newTestHTTP(t, testFS())
	html := "text/html; charset=UTF-8"
	cases := []struct {
		method, target string
		code           int
		body           string
		location       string
	}{
		{"GET", "/app", 404, tornadoPage(404, "Not Found"), ""},
		{"GET", "/apps/", 404, tornadoPage(404, "Not Found"), ""},
		{"GET", "/app/css", 301, "", "/app/css/"},
		{"GET", "/app/css?v=3", 301, "", "/app/css/"},
		{"GET", "/app/css/", 404, tornadoPage(404, "Not Found"), ""},
		{"GET", "/app/missing.js", 404, tornadoPage(404, "Not Found"), ""},
		{"GET", "/app/../bot.py", 403, tornadoPage(403, "Forbidden"), ""},
		{"GET", "/app/%2e%2e/bot.py", 403, tornadoPage(403, "Forbidden"), ""},
		{"GET", "/app//etc/passwd", 403, tornadoPage(403, "Forbidden"), ""},
		{"GET", "/app/pipe", 403, tornadoPage(403, "Forbidden"), ""},
		{"GET", "/app/%ff", 400, tornadoPage(400, "Bad Request"), ""},
		{"POST", "/app/index.html", 405, tornadoPage(405, "Method Not Allowed"), ""},
		{"POST", "/app/%ff", 400, tornadoPage(400, "Bad Request"), ""},
		{"PROPFIND", "/app/index.html", 405, tornadoPage(405, "Method Not Allowed"), ""},
	}
	for _, tc := range cases {
		w := do(h, tc.method, tc.target, nil)
		if w.Code != tc.code || w.Body.String() != tc.body {
			t.Errorf("%s %s: status %d corpo %q", tc.method, tc.target, w.Code, w.Body.String())
		}
		if tc.location != "" && w.Header().Get("Location") != tc.location {
			t.Errorf("%s: Location %q", tc.target, w.Header().Get("Location"))
		}
		if tc.body != "" && w.Header().Get("Content-Type") != html {
			t.Errorf("%s: Content-Type %q", tc.target, w.Header().Get("Content-Type"))
		}
	}
	assertLogContains(t, logs, "tornado.general - WARNING - 403 GET /app/../bot.py (192.0.2.1): ../bot.py is not in root static directory")
	assertLogContains(t, logs, "403 GET /app/pipe (192.0.2.1): pipe is not a file")
	assertLogContains(t, logs, `400 GET /app/%ff (192.0.2.1): Invalid unicode in url: b'\xff'`)
	assertLogContains(t, logs, "tornado.access - INFO - 301 GET /app/css (192.0.2.1)")
	assertLogContains(t, logs, "tornado.access - WARNING - 404 GET /app/missing.js (192.0.2.1)")
}

// TestStaticHeadAndConditional: HEAD senza corpo, 304 con If-None-Match e
// If-Modified-Since.
func TestStaticHeadAndConditional(t *testing.T) {
	h := newTestHTTP(t, testFS())
	w := do(h, http.MethodHead, "/app/", nil)
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.Itoa(len(indexHTML)) {
		t.Errorf("HEAD: %d %q %v", w.Code, w.Body.String(), w.Header())
	}

	etag := etagOf(indexHTML)
	cases := []struct {
		headers map[string]string
		code    int
	}{
		{map[string]string{"If-None-Match": etag}, 304},
		{map[string]string{"If-None-Match": "W/" + etag}, 304},
		{map[string]string{"If-None-Match": `"altro", ` + etag}, 304},
		{map[string]string{"If-None-Match": "*"}, 304},
		{map[string]string{"If-None-Match": `"altro"`}, 200},
		{map[string]string{"If-None-Match": "senza virgolette"}, 200},
		{map[string]string{"If-None-Match": `"altro"`, "If-Modified-Since": "Fri, 02 Oct 2026 00:00:00 GMT"}, 200},
		{map[string]string{"If-Modified-Since": "Thu, 01 Oct 2026 12:00:00 GMT"}, 304},
		{map[string]string{"If-Modified-Since": "Fri, 02 Oct 2026 00:00:00 GMT"}, 304},
		{map[string]string{"If-Modified-Since": "Wed, 30 Sep 2026 00:00:00 GMT"}, 200},
		{map[string]string{"If-Modified-Since": "non una data"}, 200},
	}
	for _, tc := range cases {
		w := do(h, http.MethodGet, "/app/", tc.headers)
		if w.Code != tc.code {
			t.Errorf("%v: status %d, atteso %d", tc.headers, w.Code, tc.code)
		}
		if tc.code == 304 && (w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" || w.Header().Get("Etag") != etag) {
			t.Errorf("%v: 304 con corpo o Content-Type: %q %v", tc.headers, w.Body.String(), w.Header())
		}
	}

	// Senza data di modifica: niente Last-Modified né 304 per data
	plain := NewHTTPHandler(newTestStore(t), nil, "", testFS(), time.Time{})
	w = do(plain, http.MethodGet, "/app/", map[string]string{"If-Modified-Since": "Fri, 02 Oct 2026 00:00:00 GMT"})
	if w.Code != 200 || w.Header().Get("Last-Modified") != "" {
		t.Errorf("senza ModTime: %d %v", w.Code, w.Header())
	}
}

// TestStaticRange copre le richieste Range di StaticFileHandler.
func TestStaticRange(t *testing.T) {
	h := newTestHTTP(t, testFS())
	cases := []struct {
		rng, body, contentRange string
		code                    int
	}{
		{"bytes=0-3", "0123", "bytes 0-3/10", 206},
		{"bytes=5-", "56789", "bytes 5-9/10", 206},
		{"bytes=-3", "789", "bytes 7-9/10", 206},
		{"bytes=-20", "0123456789", "", 200},
		{"bytes=0-", "0123456789", "", 200},
		{"bytes=2-100", "23456789", "bytes 2-9/10", 206},
		{"bytes=10-", "", "bytes */10", 416},
		{"bytes=5-2", "", "bytes */10", 416},
		{"bytes=-0", "", "bytes */10", 416},
		{"items=0-1", "0123456789", "", 200},
		{"bytes=a-b", "0123456789", "", 200},
		{"bytes=0-1,4-5", "0123456789", "", 200},
	}
	for _, tc := range cases {
		w := do(h, http.MethodGet, "/app/data.bin", map[string]string{"Range": tc.rng})
		if w.Code != tc.code || w.Body.String() != tc.body || w.Header().Get("Content-Range") != tc.contentRange {
			t.Errorf("%s: status %d corpo %q Content-Range %q", tc.rng, w.Code, w.Body.String(), w.Header().Get("Content-Range"))
		}
		if tc.code == 416 && w.Header().Get("Content-Type") != "text/plain" {
			t.Errorf("%s: Content-Type %q", tc.rng, w.Header().Get("Content-Type"))
		}
	}
}

// TestHTTPRoutes: /health e le API sono instradati con percorsi esatti,
// senza redirect di normalizzazione.
func TestHTTPRoutes(t *testing.T) {
	h := newTestHTTP(t, testFS())

	w := do(h, http.MethodGet, "/health", nil)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Content-Type") != "application/json" ||
		!strings.Contains(w.Body.String(), `"scraper": "missing"`) {
		t.Errorf("/health: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	for _, target := range []string{"/api/rates/history", "/api/rates/current", "/api/user/rates"} {
		w := do(h, http.MethodGet, target, nil)
		if w.Code != http.StatusUnauthorized || w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s: %d %v", target, w.Code, w.Header())
		}
	}
	for _, target := range []string{"/health/", "/api//rates/history", "/", "/api/rates/history/", "/heal%74h"} {
		w := do(h, http.MethodGet, target, nil)
		if w.Code != http.StatusNotFound || w.Body.String() != tornadoPage(404, "Not Found") {
			t.Errorf("%s: %d %q", target, w.Code, w.Body.String())
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
	w := do(h, http.MethodGet, "/app/", nil)
	if w.Code != 200 || w.Body.String() != string(index) {
		t.Fatalf("/app/: %d", w.Code)
	}
	for target, ct := range map[string]string{"/app/css/style.css?v=3": "text/css", "/app/js/app.js?v=3": "text/javascript"} {
		w := do(h, http.MethodGet, target, nil)
		if w.Code != 200 || w.Header().Get("Content-Type") != ct || w.Body.Len() == 0 {
			t.Errorf("%s: %d %q", target, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

// TestStaticDoubleSlashRedirect: il redirect di una directory con due barre
// iniziali sarebbe un open redirect e viene rifiutato.
func TestStaticDoubleSlashRedirect(t *testing.T) {
	captureLogs(t)
	s := &StaticHandler{FS: testFS()}
	r := httptest.NewRequest(http.MethodGet, "/app/css", nil)
	r.RequestURI = "//css"
	w := httptest.NewRecorder()
	s.Serve(w, r, "css")
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d", w.Code)
	}
}

// TestParseRequestRangeAndHelpers verifica gli helper di basso livello.
func TestParseRequestRangeAndHelpers(t *testing.T) {
	if _, _, ok := parseRequestRange("bytes=1-x"); ok {
		t.Error("range non valido accettato")
	}
	if s, e, ok := parseRequestRange(" bytes = 1-2 "); !ok || *s != 1 || *e != 3 {
		t.Error("range con spazi")
	}
	if got := string(unquoteToBytes("a%20b%zz%4")); got != "a b%zz%4" {
		t.Errorf("unquoteToBytes = %q", got)
	}
	if got := string(unquoteToBytes("%C3%A8%e8")); got != "è\xe8" {
		t.Errorf("unquoteToBytes = %q", got)
	}
	long := strings.Repeat("%ff", 50)
	if _, err := decodePathArg(long); err == nil || !strings.Contains(err.Error(), strings.Repeat(`\xff`, 40)+"'") {
		t.Errorf("decodePathArg = %v", err)
	}
	for name, want := range map[string]string{"a.HTML": "text/html", "x.js.gz": "application/gzip", "x.tar.bz2": "application/octet-stream", "noext": "application/octet-stream", "x.svg": "image/svg+xml"} {
		if got := staticContentType(name); got != want {
			t.Errorf("staticContentType(%s) = %q", name, got)
		}
	}
}
