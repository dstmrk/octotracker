package scraper

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

// immediateSink logga subito sul logger del modulo.
func immediateSink(level slog.Level, msg string) {
	logger.Log(context.Background(), level, msg)
}

// parseElement restituisce la radice del documento (namespace rimossi).
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

// areraJSON restituisce le tariffe di un file come JSON: {"luce": {...}},
// {"gas": {...}} oppure {}.
func areraJSON(t *testing.T, d areraData) string {
	t.Helper()
	v := map[string]ServiceRates{}
	if d.servizio != "" {
		v[d.servizio] = d.rates
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// jsonTokens decodifica un documento JSON nella sequenza dei suoi token:
// confronta valori e ordine delle chiavi, non la formattazione (72.0 e 72
// sono lo stesso numero).
func jsonTokens(t *testing.T, s string) []any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var out []any
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("JSON non valido %q: %v", s, err)
		}
		out = append(out, tok)
	}
}

func assertSameJSON(t *testing.T, got, want string) {
	t.Helper()
	if !reflect.DeepEqual(jsonTokens(t, got), jsonTokens(t, want)) {
		t.Errorf("JSON:\n got  %s\n want %s", got, want)
	}
}

// ========== Log ==========

// TestMain silenzia i log dei test che non li catturano.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

type logRecord struct {
	Logger string
	Level  string
	Msg    string
}

type logCapture struct {
	mu      sync.Mutex
	records []logRecord
}

type captureHandler struct {
	c      *logCapture
	logger string
}

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	h.c.records = append(h.c.records, logRecord{h.logger, logging.LevelName(r.Level), r.Message})
	return nil
}

// WithAttrs registra il nome del logger (attributo "logger").
func (h captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	for _, a := range attrs {
		if a.Key == "logger" {
			h.logger = a.Value.String()
		}
	}
	return h
}
func (h captureHandler) WithGroup(string) slog.Handler { return h }

// captureLogs installa un handler che registra logger, livello e messaggio
// di ogni log.
func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(captureHandler{c: c}))
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

// assertLogs confronta i log di un logger con quelli attesi ([livello, messaggio]).
func assertLogs(t *testing.T, got []logRecord, loggerName string, want [][2]string) {
	t.Helper()
	var msgs [][2]string
	for _, r := range got {
		if r.Logger == loggerName {
			msgs = append(msgs, [2]string{r.Level, r.Msg})
		}
	}
	if len(msgs) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(msgs, want) {
		t.Errorf("log %s:\n got  %q\n want %q", loggerName, msgs, want)
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

// row è una riga passata a SaveRatesBatch, in forma confrontabile.
type row struct {
	Servizio, Tipo, Fascia string
	Energia                float64
	Comm                   *float64
	Cod                    *string
}

func rowsOf(rates []database.RateRecord) []row {
	out := []row{}
	for _, r := range rates {
		out = append(out, row{r.Servizio, r.Tipo, r.Fascia, r.Energia, r.Commercializzazione, r.CodOfferta})
	}
	return out
}

// goldenRows legge le righe dei golden: [servizio, tipo, fascia, energia,
// commercializzazione|null, cod_offerta|null], con i numeri come stringhe.
func goldenRows(t *testing.T, rows [][]*string) []row {
	t.Helper()
	num := func(s string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	out := []row{}
	for _, r := range rows {
		got := row{Servizio: *r[0], Tipo: *r[1], Fascia: *r[2], Energia: num(*r[3]), Cod: r[5]}
		if r[4] != nil {
			got.Comm = ptr(num(*r[4]))
		}
		out = append(out, got)
	}
	return out
}

func assertRows(t *testing.T, got []database.RateRecord, want [][]*string) {
	t.Helper()
	if g, w := rowsOf(got), goldenRows(t, want); !reflect.DeepEqual(g, w) {
		t.Errorf("righe:\n got  %s\n want %s", rowsJSON(t, g), rowsJSON(t, w))
	}
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
// valori restituiti.
type fakeStore struct {
	t        *testing.T
	returns  []int
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
	return r
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

// parseAreraXML legge un file XML già in memoria.
func parseAreraXML(xmlContent, service string) (areraData, error) {
	head := xmlContent[:min(len(xmlContent), headBytes)]
	return scanAreraXML(strings.NewReader(xmlContent), service).result(strings.ToValidUTF8(head, ""))
}
