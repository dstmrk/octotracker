package apitest

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dstmrk/octotracker/internal/logging"
)

func TestDoParseAndDiff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-B", "2")
		w.Header().Set("X-A", "1")
		w.WriteHeader(201)
		fmt.Fprint(w, `{"e": "Must be one of: {'luce', 'gas'}"}`)
	}))
	defer srv.Close()
	raw := []byte("GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	got := Do(t, strings.TrimPrefix(srv.URL, "http://"), raw)
	if got.Status != 201 || string(got.Body) != `{"e": "Must be one of: {'luce', 'gas'}"}` {
		t.Fatalf("risposta = %+v", got)
	}
	for _, h := range got.Headers {
		if h[0] == "Date" || h[0] == "Server" {
			t.Errorf("header non filtrato %v", h)
		}
	}
	c := Case{
		Name:    "x",
		RawHex:  hex.EncodeToString(raw),
		Status:  201,
		Headers: got.Headers,
		BodyHex: hex.EncodeToString([]byte(`{"e": "Must be one of: {'gas', 'luce'}"}`)),
		Logs:    []Record{{"tornado.access", "INFO", "201 GET / (127.0.0.1) 9.99ms"}},
	}
	if string(c.Raw()) != string(raw) {
		t.Error("Raw")
	}
	got.Body = []byte(NormalizeSets(string(got.Body)))
	logs := []Record{{"tornado.access", "INFO", "201 GET / (127.0.0.1) 0.01ms"}}
	if d := Diff(c, got, logs); len(d) != 0 {
		t.Errorf("differenze inattese: %v", d)
	}
	Check(t, c, got, logs)
	bad := got
	bad.Status = 500
	bad.Body = []byte("x")
	bad.Headers = nil
	if d := Diff(c, bad, nil); len(d) != 4 {
		t.Errorf("differenze = %v", d)
	}
	bare := Case{Name: "bare", Status: 400}
	if d := Diff(bare, Response{Status: 400, Headers: [][2]string{{"Content-Length", "0"}}}, nil); len(d) != 0 {
		t.Errorf("400 grezzo: %v", d)
	}
	if d := Diff(c, got, []Record{{"a", "INFO", "b"}}); len(d) != 1 {
		t.Errorf("log diversi: %v", d)
	}
}

func TestNormalizeSets(t *testing.T) {
	if got := NormalizeSets("x {'trioraria', 'monoraria', 'bioraria'} y {'b'}"); got != "x {'bioraria', 'monoraria', 'trioraria'} y {'b'}" {
		t.Errorf("NormalizeSets = %q", got)
	}
}

func TestCaptureLogsAndExecSQL(t *testing.T) {
	logs := CaptureLogs(t)
	logging.Logger("prova").Warn("messaggio")
	if got := logs.Take(); len(got) != 1 || got[0] != (Record{"prova", "WARNING", "messaggio"}) {
		t.Errorf("log = %q", got)
	}
	if got := logs.Take(); len(got) != 0 {
		t.Errorf("buffer non svuotato: %q", got)
	}
	path := filepath.Join(t.TempDir(), "t.db")
	ExecSQL(t, path, []string{"CREATE TABLE t (x)", "INSERT INTO t VALUES (1)"})
}
