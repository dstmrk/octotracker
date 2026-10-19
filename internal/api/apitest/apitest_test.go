package apitest

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/dstmrk/octotracker/internal/logging"
)

func TestDiffAndCheck(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-A", "1")
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"e":"Must be one of: {'gas', 'luce'}","q":%q}`, r.URL.Query().Get("q"))
	})
	raw := hex.EncodeToString([]byte("GET /x?q=v HTTP/1.1\r\nHost: x\r\n\r\n"))
	c := Case{
		Name:    "x",
		RawHex:  raw,
		Status:  201,
		Headers: [][2]string{{"X-A", "1"}},
		// formato e ordine dei set diversi: stesso JSON dopo la normalizzazione
		BodyHex: hex.EncodeToString([]byte(`{"q": "v", "e": "Must be one of: {'luce', 'gas'}"}`)),
	}
	if d := Diff(h, c); len(d) != 0 {
		t.Errorf("differenze inattese: %v", d)
	}
	Check(t, h, c)

	bad := c
	bad.Status = 200
	bad.Headers = [][2]string{{"X-A", "2"}}
	bad.BodyHex = hex.EncodeToString([]byte(`{"q": "w"}`))
	if d := Diff(h, bad); len(d) != 3 {
		t.Errorf("differenze = %v", d)
	}
	noBody := c
	noBody.BodyHex = ""
	if d := Diff(h, noBody); len(d) != 0 {
		t.Errorf("corpo vuoto controllato: %v", d)
	}
	for _, broken := range []Case{{Name: "hex", RawHex: "zz"}, {Name: "req", RawHex: hex.EncodeToString([]byte("x"))}, {Name: "body", RawHex: raw, Status: 201, BodyHex: "zz"}} {
		if d := Diff(h, broken); len(d) != 1 {
			t.Errorf("%s: differenze = %v", broken.Name, d)
		}
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
