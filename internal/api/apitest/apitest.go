// Package apitest contiene gli strumenti comuni ai test di internal/api,
// internal/health e internal/broadcast: la cattura dei log e la
// riesecuzione dei casi golden HTTP registrati con la versione Python
// (richieste grezze e parte di contratto delle risposte).
package apitest

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/dstmrk/octotracker/internal/logging"

	_ "modernc.org/sqlite" // driver "sqlite"
)

// Record è un record di log: nome del logger, livello e messaggio.
type Record [3]string

// capture è un slog.Handler che registra i record con il nome del logger
// (attributo "logger" aggiunto da logging.Logger).
type capture struct {
	logs *Logs
	name string
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }

func (c *capture) Handle(_ context.Context, r slog.Record) error {
	c.logs.mu.Lock()
	defer c.logs.mu.Unlock()
	c.logs.recs = append(c.logs.recs, Record{c.name, logging.LevelName(r.Level), r.Message})
	return nil
}

func (c *capture) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *c
	for _, a := range attrs {
		if a.Key == "logger" {
			cp.name = a.Value.String()
		}
	}
	return &cp
}

func (c *capture) WithGroup(string) slog.Handler { return c }

// Logs raccoglie i log emessi durante un test.
type Logs struct {
	mu   sync.Mutex
	recs []Record
}

// CaptureLogs installa un logger di default che registra tutti i record
// (anche DEBUG) e lo ripristina a fine test. Non usare con t.Parallel.
func CaptureLogs(t testing.TB) *Logs {
	t.Helper()
	l := &Logs{}
	prev := slog.Default()
	slog.SetDefault(slog.New(&capture{logs: l, name: "root"}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return l
}

// Take restituisce i record raccolti e svuota il buffer.
func (l *Logs) Take() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.recs
	l.recs = nil
	return out
}

// Case è un caso golden HTTP: la richiesta grezza e la risposta attesa
// (status, header di contratto, corpo JSON; corpo vuoto = non controllato).
type Case struct {
	Name    string      `json:"name"`
	RawHex  string      `json:"raw_hex"`
	Status  int         `json:"status"`
	Headers [][2]string `json:"headers"`
	BodyHex string      `json:"body_hex"`
}

var setRe = regexp.MustCompile(`\{('[a-z]+'(?:, '[a-z]+')*)\}`)

// NormalizeSets riordina alfabeticamente gli elementi dei repr di set di
// stringhe ({'luce', 'gas'} → {'gas', 'luce'}): in Python l'ordine dipende
// da PYTHONHASHSEED, in Go i messaggi usano l'ordine alfabetico.
func NormalizeSets(s string) string {
	return setRe.ReplaceAllStringFunc(s, func(m string) string {
		items := strings.Split(m[1:len(m)-1], ", ")
		sort.Strings(items)
		return "{" + strings.Join(items, ", ") + "}"
	})
}

// Diff esegue la richiesta del caso su h e restituisce le differenze dalla
// risposta attesa. Il corpo è confrontato come JSON decodificato (chiavi,
// valori, null), non byte per byte.
func Diff(h http.Handler, c Case) []string {
	raw, err := hex.DecodeString(c.RawHex)
	if err != nil {
		return []string{fmt.Sprintf("%s: raw_hex: %v", c.Name, err)}
	}
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return []string{fmt.Sprintf("%s: richiesta: %v", c.Name, err)}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var diffs []string
	if rec.Code != c.Status {
		diffs = append(diffs, fmt.Sprintf("%s: status %d; atteso %d", c.Name, rec.Code, c.Status))
	}
	for _, kv := range c.Headers {
		if got := rec.Header().Get(kv[0]); got != kv[1] {
			diffs = append(diffs, fmt.Sprintf("%s: header %s = %q; atteso %q", c.Name, kv[0], got, kv[1]))
		}
	}
	want, err := hex.DecodeString(c.BodyHex)
	if err != nil {
		return append(diffs, fmt.Sprintf("%s: body_hex: %v", c.Name, err))
	}
	if len(want) > 0 && !equalJSON([]byte(NormalizeSets(string(want))), rec.Body.Bytes()) {
		diffs = append(diffs, fmt.Sprintf("%s: corpo\n  %s\natteso\n  %s", c.Name, rec.Body.Bytes(), want))
	}
	return diffs
}

// Check segnala con t.Error ogni differenza trovata da Diff.
func Check(t testing.TB, h http.Handler, c Case) {
	t.Helper()
	for _, d := range Diff(h, c) {
		t.Error(d)
	}
}

func equalJSON(a, b []byte) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// ExecSQL esegue le istruzioni sul database in path con una connessione
// separata (come fa lo script Python con sqlite3).
func ExecSQL(t testing.TB, path string, stmts []string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}
