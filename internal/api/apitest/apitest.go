// Package apitest contiene gli strumenti dei test differenziali HTTP di
// internal/api e internal/health: i casi golden sono richieste HTTP grezze
// inviate alla vera app Tornado (Python) con le risposte e i log ottenuti;
// qui vengono reinviate byte per byte all'handler Go e confrontate.
package apitest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/logging"

	_ "modernc.org/sqlite" // driver "sqlite"
)

// Record è un record di log: nome del logger, livello Python e messaggio.
type Record [3]string

// capture è un slog.Handler che registra i record con il nome del logger
// (attributo "logger" aggiunto da logging.Logger).
type capture struct {
	mu   *sync.Mutex
	recs *[]Record
	name string
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }

func (c *capture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.recs = append(*c.recs, Record{c.name, logging.LevelName(r.Level), r.Message})
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
	slog.SetDefault(slog.New(&capture{mu: &l.mu, recs: &l.recs, name: "root"}))
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

// Response è una risposta HTTP grezza.
type Response struct {
	Status  int
	Headers [][2]string // ordinati, senza Date e Server
	Body    []byte
}

// Do invia raw a addr su una nuova connessione e legge la risposta fino
// alla chiusura (le richieste golden usano "Connection: close").
func Do(t testing.TB, addr string, raw []byte) Response {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return ParseResponse(t, data)
}

// ParseResponse analizza una risposta HTTP/1.1 grezza (senza chunked).
func ParseResponse(t testing.TB, data []byte) Response {
	t.Helper()
	head, body, ok := bytes.Cut(data, []byte("\r\n\r\n"))
	if !ok {
		t.Fatalf("risposta senza fine header: %q", data)
	}
	lines := strings.Split(string(head), "\r\n")
	fields := strings.SplitN(lines[0], " ", 3)
	if len(fields) < 2 {
		t.Fatalf("status line non valida: %q", lines[0])
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("status non valido: %q", lines[0])
	}
	var headers [][2]string
	for _, line := range lines[1:] {
		k, v, _ := strings.Cut(line, ": ")
		switch strings.ToLower(k) {
		case "date", "server":
			continue
		}
		headers = append(headers, [2]string{k, v})
	}
	sortHeaders(headers)
	return Response{Status: status, Headers: headers, Body: body}
}

func sortHeaders(h [][2]string) {
	sort.Slice(h, func(i, j int) bool {
		if h[i][0] != h[j][0] {
			return h[i][0] < h[j][0]
		}
		return h[i][1] < h[j][1]
	})
}

// Case è un caso golden registrato dalla vera app Tornado.
type Case struct {
	Name    string      `json:"name"`
	RawHex  string      `json:"raw_hex"`
	Status  int         `json:"status"`
	Headers [][2]string `json:"headers"`
	BodyHex string      `json:"body_hex"`
	Logs    []Record    `json:"logs"`
}

// Raw restituisce i byte della richiesta.
func (c Case) Raw() []byte {
	b, err := hex.DecodeString(c.RawHex)
	if err != nil {
		panic(err)
	}
	return b
}

// Body restituisce il corpo atteso.
func (c Case) Body() []byte {
	b, err := hex.DecodeString(c.BodyHex)
	if err != nil {
		panic(err)
	}
	return b
}

var (
	msRe  = regexp.MustCompile(`\d+\.\d\dms$`)
	setRe = regexp.MustCompile(`\{('[a-z]+'(?:, '[a-z]+')*)\}`)
)

// NormalizeSets riordina alfabeticamente gli elementi dei repr di set di
// stringhe ({'luce', 'gas'} → {'gas', 'luce'}): in Python l'ordine dipende
// da PYTHONHASHSEED, in Go è sempre alfabetico.
func NormalizeSets(s string) string {
	return setRe.ReplaceAllStringFunc(s, func(m string) string {
		items := strings.Split(m[1:len(m)-1], ", ")
		sort.Strings(items)
		return "{" + strings.Join(items, ", ") + "}"
	})
}

// normalizeLog maschera la durata del log di accesso e i repr di set.
func normalizeLog(r Record) Record {
	if r[0] == "tornado.access" {
		r[2] = msRe.ReplaceAllString(r[2], "<ms>")
	}
	r[2] = NormalizeSets(r[2])
	return r
}

// Check confronta risposta e log ottenuti con il caso golden e segnala
// ogni differenza con t.Errorf (vedi Diff).
func Check(t testing.TB, c Case, got Response, logs []Record) {
	t.Helper()
	for _, d := range Diff(c, got, logs) {
		t.Error(d)
	}
}

// Diff restituisce le differenze tra il caso golden e la risposta/i log
// ottenuti. Per i 400 "grezzi" di Tornado (connessione chiusa senza
// header) confronta solo status e corpo vuoto.
func Diff(c Case, got Response, logs []Record) []string {
	var diffs []string
	if got.Status != c.Status {
		diffs = append(diffs, fmt.Sprintf("%s: status %d; atteso %d", c.Name, got.Status, c.Status))
	}
	wantBody := NormalizeSets(string(c.Body()))
	if string(got.Body) != wantBody {
		diffs = append(diffs, fmt.Sprintf("%s: corpo\n  %q\natteso\n  %q", c.Name, got.Body, wantBody))
	}
	if len(c.Headers) > 0 {
		want := append([][2]string(nil), c.Headers...)
		sortHeaders(want)
		if !equalHeaders(got.Headers, want) {
			diffs = append(diffs, fmt.Sprintf("%s: header\n  %q\nattesi\n  %q", c.Name, got.Headers, want))
		}
	}
	if !equalLogs(logs, c.Logs) {
		diffs = append(diffs, fmt.Sprintf("%s: log\n  %q\nattesi\n  %q", c.Name, logs, c.Logs))
	}
	return diffs
}

func equalHeaders(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalLogs(got, want []Record) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if normalizeLog(got[i]) != normalizeLog(want[i]) {
			return false
		}
	}
	return true
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
