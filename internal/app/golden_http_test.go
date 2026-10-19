package app

// Test differenziali HTTP: le stesse richieste grezze inviate da
// testdata/*_golden.json all'applicazione Tornado di run_health_server
// (StaticFileHandler reale, Tornado 6.5.9, sui file di
// testdata/static_fixture) e alla WebhookAppClass reale di
// python-telegram-bot 22.6 vengono inviate qui a un http.Server Go con gli
// handler del port; status, header rilevanti, corpo e log devono coincidere.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/health"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// httpGoldenCase è una richiesta del golden con la risposta di Python.
type httpGoldenCase struct {
	Name          string              `json:"name"`
	RawRequestB64 string              `json:"raw_request_b64"`
	Method        string              `json:"method"`
	Status        int                 `json:"status"`
	Reason        string              `json:"reason"`
	Headers       map[string][]string `json:"headers"`
	HasExpires    bool                `json:"has_expires"`
	BodyB64       string              `json:"body_b64"`
	Logs          []goldenLog         `json:"logs"`
	Queued        []int64             `json:"queued"`
}

// goldenLog è un record di log Python (nome del logger, livello, messaggio).
type goldenLog struct {
	Name  string `json:"name"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// comparedHeaders sono gli header confrontati (gli stessi del generatore).
var comparedHeaders = []string{
	"Content-Type", "Content-Length", "Etag", "Last-Modified",
	"Cache-Control", "Accept-Ranges", "Content-Range", "Location",
}

// accessTiming è la durata variabile alla fine delle righe di tornado.access.
var accessTiming = regexp.MustCompile(` \d+\.\d{2}ms$`)

// recordCapture raccoglie i record di log con il nome del logger.
type recordCapture struct {
	mu      *sync.Mutex
	records *[]goldenLog
	name    string
}

func newRecordCapture() recordCapture {
	return recordCapture{mu: &sync.Mutex{}, records: &[]goldenLog{}, name: "root"}
}

func (c recordCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c recordCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.records = append(*c.records, goldenLog{Name: c.name, Level: logging.LevelName(r.Level), Msg: r.Message})
	return nil
}

func (c recordCapture) WithAttrs(attrs []slog.Attr) slog.Handler {
	for _, a := range attrs {
		if a.Key == loggerNameKey {
			c.name = a.Value.String()
		}
	}
	return c
}

func (c recordCapture) WithGroup(string) slog.Handler { return c }

// take restituisce (e svuota) i record dei logger indicati, con la durata
// delle righe di accesso normalizzata.
func (c recordCapture) take(names ...string) []goldenLog {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []goldenLog
	for _, r := range *c.records {
		if slices.Contains(names, r.Name) && r.Level != "DEBUG" {
			r.Msg = accessTiming.ReplaceAllString(r.Msg, " Xms")
			out = append(out, r)
		}
	}
	*c.records = nil
	return out
}

// captureRecords installa recordCapture come logger di default.
func captureRecords(t *testing.T) recordCapture {
	t.Helper()
	c := newRecordCapture()
	prev := slog.Default()
	slog.SetDefault(slog.New(c))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

func loadHTTPGolden(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

// sendRaw invia la richiesta grezza a addr e legge la risposta.
func sendRaw(t *testing.T, addr string, raw []byte, method string) (*http.Response, []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(raw); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp, body
}

// compareHTTPCase confronta la risposta Go con quella Python.
func compareHTTPCase(t *testing.T, c httpGoldenCase, resp *http.Response, body []byte) {
	t.Helper()
	if resp.StatusCode != c.Status {
		t.Errorf("status = %d, atteso %d", resp.StatusCode, c.Status)
	}
	// net/http usa sempre la frase standard nella riga di stato: Tornado
	// usa il reason dell'HTTPError (es. "Request had the wrong secret
	// token"), che compare comunque nel corpo.
	if want := fmt.Sprintf("%d %s", c.Status, c.Reason); c.Reason == http.StatusText(c.Status) && resp.Status != want {
		t.Errorf("riga di stato = %q, attesa %q", resp.Status, want)
	}
	for _, name := range comparedHeaders {
		got, want := resp.Header.Values(name), c.Headers[name]
		if !slices.Equal(got, want) {
			t.Errorf("header %s = %q, atteso %q", name, got, want)
		}
	}
	if has := resp.Header.Get("Expires") != ""; has != c.HasExpires {
		t.Errorf("Expires presente = %v, atteso %v", has, c.HasExpires)
	}
	wantBody, _ := base64.StdEncoding.DecodeString(c.BodyB64)
	if !bytes.Equal(body, wantBody) {
		t.Errorf("corpo = %q, atteso %q", truncateForLog(body), truncateForLog(wantBody))
	}
}

func truncateForLog(b []byte) []byte {
	if len(b) > 120 {
		return b[:120]
	}
	return b
}

// staticKnownDivergences sono i casi in cui il server HTTP di Go risponde
// prima di arrivare all'handler (comportamento non replicabile con
// net/http), con lo status che dà Go.
var staticKnownDivergences = map[string]int{
	// "%zz" non è un escape valido: net/http rifiuta la richiesta con 400
	// durante il parsing, Tornado la passa allo StaticFileHandler (404).
	"app_invalid_escape": http.StatusBadRequest,
}

// TestGoldenStaticAndRouting: Mini App (/app/*) e percorsi senza route del
// server su HEALTH_PORT, confrontati con Tornado.
func TestGoldenStaticAndRouting(t *testing.T) {
	var golden struct {
		Mtime int64            `json:"mtime"`
		Cases []httpGoldenCase `json:"cases"`
	}
	loadHTTPGolden(t, "static_golden.json", &golden)
	logs := captureRecords(t)

	store := newTestStore(t)
	// I file serviti sono una copia fissa (testdata/static_fixture), non la
	// Mini App reale: modificare webapp/ (cache busting) non cambia il golden.
	h := NewHTTPHandler(store, health.NewRegistry(), testToken, os.DirFS("testdata/static_fixture"), time.Unix(golden.Mtime, 0))
	srv := httptest.NewServer(h)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	for _, c := range golden.Cases {
		t.Run(c.Name, func(t *testing.T) {
			raw, _ := base64.StdEncoding.DecodeString(c.RawRequestB64)
			_ = logs.take()
			resp, body := sendRaw(t, addr, raw, c.Method)
			if status, ok := staticKnownDivergences[c.Name]; ok {
				if resp.StatusCode != status {
					t.Errorf("divergenza nota: status = %d, atteso %d", resp.StatusCode, status)
				}
				return
			}
			compareHTTPCase(t, c, resp, body)
			got := logs.take("tornado.access", "tornado.general", "tornado.application")
			for i, r := range got {
				// "Uncaught exception": al posto del traceback Go accoda una
				// riga con l'errore
				if r.Name == "tornado.application" && strings.Count(r.Msg, "\n") == 2 {
					got[i].Msg = r.Msg[:strings.LastIndex(r.Msg, "\n")]
				}
			}
			want := c.Logs
			for i := range want {
				want[i].Msg = accessTiming.ReplaceAllString(want[i].Msg, " Xms")
			}
			if !slices.Equal(got, want) {
				t.Errorf("log = %q\natteso %q", got, want)
			}
		})
	}
}

// TestGoldenWebhook: webhook server di PTB (WebhookAppClass) contro
// WebhookHandler + gotgbot Updater; gli update accodati sono elaborati in
// ordine.
func TestGoldenWebhook(t *testing.T) {
	var golden struct {
		Token  string           `json:"token"`
		Secret string           `json:"secret"`
		Cases  []httpGoldenCase `json:"cases"`
	}
	loadHTTPGolden(t, "webhook_golden.json", &golden)
	logs := captureRecords(t)

	b, _ := testutil.NewBot(t)
	b.Token = golden.Token
	rec := &recordingHandler{}
	d := ext.NewDispatcher(&ext.DispatcherOpts{MaxRoutines: 1, Logger: ptbLogger("telegram.ext.Application")})
	d.AddHandler(rec)
	updater := ext.NewUpdater(NewSequentialDispatcher(d), &ext.UpdaterOpts{Logger: ptbLogger("telegram.ext.Updater")})
	if err := updater.AddWebhook(b, golden.Token, &ext.AddWebhookOpts{SecretToken: golden.Secret}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewWebhookHandler(golden.Token, golden.Secret, updater.GetHandlerFunc("/")))
	addr := srv.Listener.Addr().String()

	var wantQueued []int64
	for _, c := range golden.Cases {
		t.Run(c.Name, func(t *testing.T) {
			raw, _ := base64.StdEncoding.DecodeString(c.RawRequestB64)
			_ = logs.take()
			resp, body := sendRaw(t, addr, raw, c.Method)
			compareHTTPCase(t, c, resp, body)
			got := logs.take("telegram.ext.Updater", "tornado.access", "tornado.general")
			if !slices.Equal(got, c.Logs) {
				t.Errorf("log = %q\natteso %q", got, c.Logs)
			}
		})
		wantQueued = append(wantQueued, c.Queued...)
	}

	srv.Close()
	_ = updater.Stop()
	if got := rec.processed(); !slices.Equal(got, wantQueued) {
		t.Errorf("update elaborati = %v, attesi %v", got, wantQueued)
	}
}
