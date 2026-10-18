package tornado

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/api/apitest"
)

func jsonDefaults(h *RequestHandler) {
	h.SetHeader("Content-Type", "application/json")
	h.SetHeader("Access-Control-Allow-Origin", "*")
}

func serve(t *testing.T, req *http.Request, defaults func(*RequestHandler), methods Methods) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Serve(rec, req, defaults, methods)
	return rec
}

func TestFinishEtagAndContentLength(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	get := Methods{http.MethodGet: func(h *RequestHandler) error { h.Write("ciao"); return nil }}
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/x?a=1", nil), jsonDefaults, get)
	if rec.Code != 200 || rec.Body.String() != "ciao" {
		t.Fatalf("status %d corpo %q", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("Etag")
	if etag != `"1e4e888ac66f8dd41e00c5a7ac36a32a9950d271"` || rec.Header().Get("Content-Length") != "4" ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("header = %v", rec.Header())
	}
	recs := logs.Take()
	if len(recs) != 1 || recs[0][0] != "tornado.access" || recs[0][1] != "INFO" ||
		!strings.HasPrefix(recs[0][2], "200 GET /x?a=1 (192.0.2.1) ") || !strings.HasSuffix(recs[0][2], "ms") {
		t.Errorf("log = %q", recs)
	}

	for inm, want := range map[string]int{
		etag: 304, "W/" + etag: 304, `"a", ` + etag: 304, "*": 304, `"altro"`: 200, "": 200, "senza-virgolette": 200,
	} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		rec := serve(t, req, jsonDefaults, get)
		if rec.Code != want {
			t.Errorf("If-None-Match %q: status %d; atteso %d", inm, rec.Code, want)
		}
		if want == 304 && (rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" || rec.Header().Get("Etag") != etag) {
			t.Errorf("304: corpo %q header %v", rec.Body.String(), rec.Header())
		}
	}

	// HEAD: stesso Content-Length, nessun corpo (qui 405 perché head non è implementato)
	rec = serve(t, httptest.NewRequest(http.MethodHead, "/x", nil), jsonDefaults, get)
	if rec.Code != 405 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "87" {
		t.Errorf("HEAD: status %d corpo %q header %v", rec.Code, rec.Body.String(), rec.Header())
	}
	// HEAD implementato: ETag calcolato, corpo non inviato
	rec = serve(t, httptest.NewRequest(http.MethodHead, "/x", nil), nil, Methods{http.MethodHead: get[http.MethodGet]})
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Etag") != etag {
		t.Errorf("HEAD implementato: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	// un ETag già impostato non viene ricalcolato
	rec = serve(t, httptest.NewRequest(http.MethodGet, "/x", nil), nil, Methods{http.MethodGet: func(h *RequestHandler) error {
		h.SetHeader("Etag", `"mio"`)
		h.Write("x")
		return nil
	}})
	if rec.Header().Get("Etag") != `"mio"` {
		t.Errorf("Etag = %q", rec.Header().Get("Etag"))
	}
}

func TestNoContentClearsRepresentationHeaders(t *testing.T) {
	rec := serve(t, httptest.NewRequest(http.MethodOptions, "/", nil), jsonDefaults, Methods{
		http.MethodOptions: func(h *RequestHandler) error {
			h.SetHeader("Content-Language", "it")
			h.SetStatus(http.StatusNoContent)
			h.Finish()
			return nil
		},
	})
	if rec.Code != 204 || rec.Header().Get("Content-Type") != "" || rec.Header().Get("Content-Language") != "" ||
		rec.Header().Get("Content-Length") != "" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("status %d header %v", rec.Code, rec.Header())
	}
}

func TestMethodNotAllowed(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH", "FOO"} {
		rec := serve(t, httptest.NewRequest(m, "/", nil), jsonDefaults, Methods{})
		if rec.Code != 405 || rec.Body.String() != "<html><title>405: Method Not Allowed</title><body>405: Method Not Allowed</body></html>" ||
			rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %q %v", m, rec.Code, rec.Body.String(), rec.Header())
		}
	}
	rec := serve(t, httptest.NewRequest("POST", "/", nil), nil, Methods{})
	if rec.Header().Get("Content-Type") != "text/html; charset=UTF-8" {
		t.Errorf("Content-Type senza default = %q", rec.Header().Get("Content-Type"))
	}
	for _, r := range logs.Take() {
		if r[0] != "tornado.access" || r[1] != "WARNING" {
			t.Errorf("log inatteso %q", r)
		}
	}
}

func TestExceptions(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	cases := []struct {
		name   string
		fn     Method
		status int
		log    apitest.Record
	}{
		{"http error con messaggio", func(*RequestHandler) error { return &HTTPError{StatusCode: 400, LogMessage: "msg"} },
			400, apitest.Record{"tornado.general", "WARNING", "400 GET /e (192.0.2.1): msg"}},
		{"http error senza messaggio", func(*RequestHandler) error { return &HTTPError{StatusCode: 404} }, 404, apitest.Record{}},
		{"errore generico", func(*RequestHandler) error { return errors.New("boom") }, 500,
			apitest.Record{"tornado.application", "ERROR", "Uncaught exception GET /e (192.0.2.1)\nHTTPServerRequest(protocol='http', host='example.com', method='GET', uri='/e', version='HTTP/1.1', remote_ip='192.0.2.1')\nboom"}},
		{"panic", func(*RequestHandler) error { panic("kaboom") }, 500, apitest.Record{"tornado.application", "ERROR", ""}},
		{"panic error", func(*RequestHandler) error { panic(&HTTPError{StatusCode: 418}) }, 418, apitest.Record{}},
	}
	for _, c := range cases {
		rec := serve(t, httptest.NewRequest(http.MethodGet, "/e", nil), nil, Methods{http.MethodGet: c.fn})
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), "<html><title>") {
			t.Errorf("%s: %d %q", c.name, rec.Code, rec.Body.String())
		}
		recs := logs.Take()
		if c.log != (apitest.Record{}) {
			if len(recs) < 2 || recs[0][0] != c.log[0] || recs[0][1] != c.log[1] || (c.log[2] != "" && recs[0][2] != c.log[2]) {
				t.Errorf("%s: log %q", c.name, recs)
			}
		}
	}
	// errore dopo finish(): niente seconda risposta
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/e", nil), nil, Methods{http.MethodGet: func(h *RequestHandler) error {
		h.Write("ok")
		h.Finish()
		return &HTTPError{StatusCode: 500}
	}})
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("dopo finish: %d %q", rec.Code, rec.Body.String())
	}
	rec = serve(t, httptest.NewRequest(http.MethodGet, "/e", nil), nil, Methods{http.MethodGet: func(h *RequestHandler) error {
		h.Finish()
		return errors.New("tardi")
	}})
	if rec.Code != 200 {
		t.Errorf("dopo finish (errore generico): %d", rec.Code)
	}
}

func TestFinishTwicePanics(t *testing.T) {
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/", nil), nil, Methods{http.MethodGet: func(h *RequestHandler) error {
		h.Finish()
		h.Finish()
		return nil
	}})
	if rec.Code != 200 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestHTTPErrorString(t *testing.T) {
	tests := map[string]*HTTPError{
		"HTTP 400: Bad Request (Invalid unicode in x: b'\\xff')": {StatusCode: 400, LogMessage: `Invalid unicode in x: b'\xff'`},
		"HTTP 405: Method Not Allowed":                           {StatusCode: 405},
		"HTTP 599: Unknown":                                      {StatusCode: 599},
		"HTTP 400: Mio motivo":                                   {StatusCode: 400, Reason: "Mio motivo"},
	}
	for want, e := range tests {
		if e.Error() != want {
			t.Errorf("Error() = %q; atteso %q", e.Error(), want)
		}
	}
}

func TestGetArgument(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?a=1&a=%202%09&c=x%01y&d=%FF&e="+strings.Repeat("z", 45)+"%FF", nil)
	var got []any
	serve(t, req, nil, Methods{http.MethodGet: func(h *RequestHandler) error {
		a, err := h.GetArgument("a", nil)
		got = append(got, *a, err)
		def := "def"
		b, _ := h.GetArgument("b", &def)
		got = append(got, *b)
		none, _ := h.GetArgument("b", nil)
		got = append(got, none == nil)
		c, _ := h.GetArgument("c", nil)
		got = append(got, *c)
		raw, _ := h.GetArguments("a", false)
		got = append(got, strings.Join(raw, "|"))
		_, err = h.GetArgument("d", nil)
		got = append(got, err.Error())
		_, err = h.GetArgument("e", nil)
		got = append(got, err.Error())
		return nil
	}})
	want := []any{"2", nil, "def", true, "x y", "1| 2\t", "HTTP 400: Bad Request (Invalid unicode in d: b'\\xff')",
		"HTTP 400: Bad Request (Invalid unicode in e: b'" + strings.Repeat("z", 40) + "')"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("#%d = %#v; atteso %#v", i, got[i], want[i])
		}
	}
}

func TestBodyArguments(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	newReq := func(ct, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/?a=query", strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		return req
	}
	var values []string
	post := Methods{http.MethodPost: func(h *RequestHandler) error {
		values, _ = h.GetArguments("a", true)
		return nil
	}}
	serve(t, newReq("application/x-www-form-urlencoded; charset=utf-8", "a=body1&a=body2&b=x"), nil, post)
	if strings.Join(values, ",") != "query,body1,body2" {
		t.Errorf("valori = %v", values)
	}
	serve(t, newReq("application/json", "a=body"), nil, post)
	if strings.Join(values, ",") != "query" {
		t.Errorf("corpo JSON: valori = %v", values)
	}
	rec := serve(t, newReq("application/x-www-form-urlencoded", strings.Repeat("a=1&", 1000)+"a=1"), nil, post)
	if rec.Code != 400 {
		t.Errorf("troppi campi: %d", rec.Code)
	}
	req := newReq("application/x-www-form-urlencoded", "a=1")
	req.Header.Set("Content-Encoding", "gzip")
	if rec := serve(t, req, nil, post); rec.Code != 400 {
		t.Errorf("Content-Encoding: %d", rec.Code)
	}
	req = newReq("application/x-www-form-urlencoded", "")
	req.Body = nil
	if rec := serve(t, req, nil, post); rec.Code != 200 {
		t.Errorf("corpo nil: %d", rec.Code)
	}
	req = newReq("application/x-www-form-urlencoded", "")
	req.Body = errReader{}
	if rec := serve(t, req, nil, post); rec.Code != 500 {
		t.Errorf("errore di lettura: %d", rec.Code)
	}
	want := []string{
		"400 POST /?a=query (192.0.2.1): Invalid body: Invalid x-www-form-urlencoded body: Max number of fields exceeded",
		"400 POST /?a=query (192.0.2.1): Invalid body: Unsupported Content-Encoding: gzip",
	}
	var general []string
	for _, r := range logs.Take() {
		if r[0] == "tornado.general" {
			general = append(general, r[2])
		}
	}
	if strings.Join(general, "\n") != strings.Join(want, "\n") {
		t.Errorf("log = %q", general)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error") }
func (errReader) Close() error             { return nil }

func TestQueryTooManyFields(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	called := false
	rec := serve(t, httptest.NewRequest(http.MethodGet, "/?"+strings.Repeat("a=1&", 1000)+"a=1", nil), nil,
		Methods{http.MethodGet: func(*RequestHandler) error { called = true; return nil }})
	if rec.Code != 400 || rec.Body.Len() != 0 || called || rec.Header().Get("Connection") != "close" {
		t.Errorf("status %d corpo %q chiamato %v", rec.Code, rec.Body.String(), called)
	}
	recs := logs.Take()
	if len(recs) != 1 || recs[0] != (apitest.Record{"tornado.general", "INFO",
		"Malformed HTTP message from 192.0.2.1: Invalid query string: Max number of fields exceeded"}) {
		t.Errorf("log = %q", recs)
	}
}

func TestNotFoundHandler(t *testing.T) {
	h := NotFoundHandler()
	for method, want := range map[string]int{"GET": 404, "POST": 404, "HEAD": 404, "FOO": 405} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/nope", nil))
		if rec.Code != want || rec.Header().Get("Content-Type") != "text/html; charset=UTF-8" {
			t.Errorf("%s: %d %v", method, rec.Code, rec.Header())
		}
	}
}

func TestRequestSummaryAndLogLevels(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	req := httptest.NewRequest(http.MethodGet, "/a", nil)
	req.RemoteAddr = "senza-porta"
	req.RequestURI = ""
	if got := RequestSummary(req); got != "GET /a (senza-porta)" {
		t.Errorf("RequestSummary = %q", got)
	}
	req.RemoteAddr = "[::1]:1234"
	req.RequestURI = "/\xc3\xa9"
	if got := RequestSummary(req); got != "GET /Ã© (::1)" {
		t.Errorf("RequestSummary = %q", got)
	}
	prev := now
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now = func() time.Time { return base.Add(1234567 * time.Nanosecond) }
	defer func() { now = prev }()
	for _, s := range []int{200, 302, 404, 500} {
		LogRequest(s, req, base)
	}
	recs := logs.Take()
	levels := []string{"INFO", "INFO", "WARNING", "ERROR"}
	for i, r := range recs {
		if r[1] != levels[i] || !strings.HasSuffix(r[2], " 1.23ms") {
			t.Errorf("log %d = %q", i, r)
		}
	}
}

func TestHandlerAccessors(t *testing.T) {
	serve(t, httptest.NewRequest(http.MethodGet, "/", nil), jsonDefaults, Methods{http.MethodGet: func(h *RequestHandler) error {
		if h.Status() != 200 || h.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("stato iniziale: %d %v", h.Status(), h.Header())
		}
		h.SetStatus(201)
		h.ClearHeader("Access-Control-Allow-Origin")
		if h.Status() != 201 || h.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Error("SetStatus/ClearHeader")
		}
		return nil
	}})
}
