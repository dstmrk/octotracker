package scraper

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

// immediateSink logga subito sul logger del modulo (per chiamare le funzioni
// di parsing delle singole offerte come in Python).
func immediateSink(level slog.Level, msg string) {
	logger.Log(context.Background(), level, msg)
}

// parseElement replica ET.fromstring per i test di unità sulle singole
// offerte: restituisce la radice del documento (namespace rimossi).
func parseElement(xmlContent string) (*element, error) {
	var root *element
	err := scanXML(strings.NewReader(xmlContent),
		func(_ string, depth int) bool { return depth == 1 },
		func(e *element) { root = e })
	return root, err
}

func mustElement(t *testing.T, xmlContent string) *element {
	t.Helper()
	e, err := parseElement(xmlContent)
	if err != nil {
		t.Fatalf("parseElement: %v", err)
	}
	return e
}

// pyJSON restituisce json.dumps(v) con i separatori di default.
func (v pyValue) pyJSON() string {
	var b strings.Builder
	v.dumps(&b, -1, 0)
	return b.String()
}

// pyValue di areraData: {"luce": {...}}, {"gas": {...}} o {}.
func (d areraData) pyValue() pyValue {
	out := pyDict()
	if d.servizio != "" {
		out.put(d.servizio, d.rates.pyValue())
	}
	return out
}

// ========== Log ==========

// TestMain silenzia i log dei test che non li catturano.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

type logRecord struct {
	Level string
	Msg   string
}

type logCapture struct {
	mu      sync.Mutex
	records []logRecord
}

type captureHandler struct{ c *logCapture }

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	h.c.records = append(h.c.records, logRecord{logging.LevelName(r.Level), r.Message})
	return nil
}
func (h captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h captureHandler) WithGroup(string) slog.Handler      { return h }

// captureLogs installa un handler che registra livello e messaggio di ogni
// log (come logging.Handler nei generatori Python).
func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(captureHandler{c}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

func (c *logCapture) all() []logRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]logRecord{}, c.records...)
}

func (c *logCapture) messages(level string) []string {
	var out []string
	for _, r := range c.all() {
		if level == "" || r.Level == level {
			out = append(out, r.Msg)
		}
	}
	return out
}

func (c *logCapture) has(level, substr string) bool {
	for _, m := range c.messages(level) {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

// malformedPrefix: i messaggi di ParseError di expat ("mismatched tag: line
// 6, column 10") non sono riproducibili con encoding/xml; dei log "XML
// malformato" si confronta solo il prefisso fino al nome del servizio.
func malformedPrefix(msg string) (string, bool) {
	const p = "❌ XML malformato per "
	if !strings.HasPrefix(msg, p) {
		return "", false
	}
	rest := msg[len(p):]
	i := strings.Index(rest, ": ")
	if i < 0 {
		return "", false
	}
	return p + rest[:i+2], true
}

// assertLogs confronta i log Go con quelli registrati da Python.
func assertLogs(t *testing.T, got []logRecord, want [][2]string) {
	t.Helper()
	n := max(len(got), len(want))
	for i := 0; i < n; i++ {
		var g logRecord
		var w [2]string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		ok := g.Level == w[0] && g.Msg == w[1]
		if !ok && g.Level == w[0] {
			if p, isMalformed := malformedPrefix(w[1]); isMalformed && strings.HasPrefix(g.Msg, p) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("log %d:\n got  %s %q\n want %s %q", i, g.Level, g.Msg, w[0], w[1])
		}
	}
}

// ========== Golden ==========

func loadGolden(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("lettura golden %s: %v", name, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decodifica golden %s: %v", name, err)
	}
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// flatRows rende le righe come flat_rows del generatore Python
// ([servizio, tipo, fascia, repr(energia), repr(comm)|null, cod|null]).
func flatRows(rates []database.RateRecord) [][]*string {
	out := [][]*string{}
	for _, r := range rates {
		e := pyFloatRepr(r.Energia)
		var comm *string
		if r.Commercializzazione != nil {
			c := pyFloatRepr(*r.Commercializzazione)
			comm = &c
		}
		s, ti, f := r.Servizio, r.Tipo, r.Fascia
		out = append(out, []*string{&s, &ti, &f, &e, comm, r.CodOfferta})
	}
	return out
}

func rowsJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ptr[T any](v T) *T { return &v }

// ========== HTTP finto ==========

// route descrive la risposta a un URL, come make_urlopen del generatore.
type route struct {
	Body       *string `json:"body"`
	Hex        *string `json:"hex"`
	Status     int     `json:"status"`
	Reason     string  `json:"reason"`
	URLError   *string `json:"urlerror"`
	Incomplete *string `json:"incomplete"`
	FailAfter  int     `json:"fail_after"`
}

// fakeTransport serve le route per URL; gli URL sconosciuti danno 404.
type fakeTransport struct {
	mu       sync.Mutex
	routes   map[string]route
	requests []*http.Request
}

type incompleteBody struct {
	data []byte
	pos  int
}

func (b *incompleteBody) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	r, ok := f.routes[req.URL.String()]
	f.mu.Unlock()
	if !ok {
		r = route{Status: 404, Reason: "Not Found"}
	}
	resp := func(code int, reason string, body io.Reader, length int64) *http.Response {
		return &http.Response{
			StatusCode:    code,
			Status:        fmt.Sprintf("%d %s", code, reason),
			Body:          io.NopCloser(body),
			ContentLength: length,
			Header:        http.Header{},
			Request:       req,
		}
	}
	switch {
	case r.Status != 0:
		return resp(r.Status, r.Reason, strings.NewReader(""), 0), nil
	case r.URLError != nil:
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	case r.Hex != nil:
		b, err := hex.DecodeString(*r.Hex)
		if err != nil {
			return nil, err
		}
		return resp(200, "OK", strings.NewReader(string(b)), int64(len(b))), nil
	case r.Incomplete != nil:
		data := []byte(*r.Incomplete)
		return resp(200, "OK", &incompleteBody{data: data[:r.FailAfter]}, int64(len(data))), nil
	case r.Body != nil:
		return resp(200, "OK", strings.NewReader(*r.Body), int64(len(*r.Body))), nil
	}
	return nil, fmt.Errorf("route non valida per %s", req.URL)
}

func (f *fakeTransport) urls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		out = append(out, r.URL.String())
	}
	return out
}

// fakeClient restituisce un Client con trasporto finto e orologio fermo.
func fakeClient(routes map[string]route, now time.Time) (*Client, *fakeTransport) {
	tr := &fakeTransport{routes: routes}
	return &Client{HTTPClient: &http.Client{Transport: tr}, Now: func() time.Time { return now }}, tr
}

func bodyRoute(s string) route { return route{Body: &s} }

// ========== Store finto ==========

type saveCall struct {
	dataFonte string
	rates     []database.RateRecord
}

// fakeStore registra le chiamate a SaveRatesBatch; returns è la sequenza dei
// valori restituiti (o "raise" per un panic, come l'eccezione del mock Python).
type fakeStore struct {
	t        *testing.T
	returns  []any
	calls    []saveCall
	existing map[database.RateKey]struct{}
}

func (s *fakeStore) SaveRatesBatch(dataFonte string, rates []database.RateRecord) int {
	s.calls = append(s.calls, saveCall{dataFonte, rates})
	if len(s.returns) == 0 {
		s.t.Fatalf("SaveRatesBatch non atteso (%s, %d tariffe)", dataFonte, len(rates))
	}
	r := s.returns[0]
	s.returns = s.returns[1:]
	switch v := r.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		panic("DB error")
	}
}

func (s *fakeStore) GetRateHistoryDates() map[database.RateKey]struct{} {
	if s.existing == nil {
		return map[database.RateKey]struct{}{}
	}
	return s.existing
}

// frozenNow è il datetime fisso dei golden (2026-01-03 10:30:00).
var frozenNow = time.Date(2026, 1, 3, 10, 30, 0, 0, time.UTC)

func nowFrom(v []int) time.Time {
	return time.Date(v[0], time.Month(v[1]), v[2], v[3], v[4], v[5], 0, time.UTC)
}

func noSleep(context.Context, time.Duration) error { return nil }
