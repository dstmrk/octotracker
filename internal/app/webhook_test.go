package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/testutil"
)

const (
	testToken  = "123456:TEST-TOKEN"
	testSecret = "s3cret-token"
)

// recordingHandler registra gli update_id elaborati; se block non è nil,
// ogni update attende un valore da block prima di terminare.
type recordingHandler struct {
	mu    sync.Mutex
	ids   []int64
	block chan struct{}
}

func (h *recordingHandler) CheckUpdate(*gotgbot.Bot, *ext.Context) bool { return true }
func (h *recordingHandler) HandleUpdate(_ *gotgbot.Bot, ctx *ext.Context) error {
	if h.block != nil {
		<-h.block
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ids = append(h.ids, ctx.UpdateId)
	return nil
}
func (h *recordingHandler) Name() string { return "recording" }

func (h *recordingHandler) processed() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]int64(nil), h.ids...)
}

// webhookFixture collega WebhookHandler → Updater → SequentialDispatcher
// → Dispatcher (MaxRoutines 1) → recordingHandler, come in runWebhook.
type webhookFixture struct {
	handler  *WebhookHandler
	updater  *ext.Updater
	recorder *recordingHandler
	stopOnce sync.Once
}

func newWebhookFixture(t *testing.T, block chan struct{}) *webhookFixture {
	t.Helper()
	b, _ := testutil.NewBot(t)
	b.Token = testToken
	rec := &recordingHandler{block: block}
	d := ext.NewDispatcher(&ext.DispatcherOpts{MaxRoutines: 1, Logger: ptbLogger("telegram.ext.Application")})
	d.AddHandler(rec)
	updater := ext.NewUpdater(NewSequentialDispatcher(d), &ext.UpdaterOpts{Logger: ptbLogger("telegram.ext.Updater")})
	if err := updater.AddWebhook(b, testToken, &ext.AddWebhookOpts{SecretToken: testSecret}); err != nil {
		t.Fatal(err)
	}
	f := &webhookFixture{
		handler:  NewWebhookHandler(testToken, testSecret, updater.GetHandlerFunc("/")),
		updater:  updater,
		recorder: rec,
	}
	t.Cleanup(f.stop)
	return f
}

// stop ferma l'updater: gli update in coda vengono elaborati prima.
func (f *webhookFixture) stop() {
	f.stopOnce.Do(func() { _ = f.updater.Stop() })
}

func updateBody(id int64) string {
	return updateBodyText(id, "ciao")
}

// updateBodyText è il JSON di un update con un messaggio privato di testo.
func updateBodyText(id int64, text string) string {
	raw, _ := json.Marshal(testutil.MessageUpdate(testutil.DefaultUserID, text))
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["update_id"] = id
	raw, _ = json.Marshal(m)
	return string(raw)
}

func webhookRequest(method, target, body string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

var validHeaders = map[string]string{"Content-Type": "application/json", SecretTokenHeader: testSecret}

// TestWebhookAcceptsRightSecret: con il secret corretto l'update viene
// accodato ed elaborato; la risposta è 200 con corpo vuoto.
func TestWebhookAcceptsRightSecret(t *testing.T) {
	f := newWebhookFixture(t, nil)
	for i, target := range []string{"/" + testToken, "/" + testToken + "/", "/" + testToken + "?x=1"} {
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, webhookRequest(http.MethodPost, target, updateBody(int64(i+1)), validHeaders))
		if w.Code != http.StatusOK || w.Body.Len() != 0 {
			t.Errorf("%s: status %d corpo %q", target, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != `application/json; charset="utf-8"` {
			t.Errorf("Content-Type = %q", ct)
		}
	}
	f.stop()
	if got := f.recorder.processed(); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("update elaborati = %v", got)
	}
}

// TestWebhookRejectsRequests copre i controlli di TelegramHandler e della
// route Tornado.
func TestWebhookRejectsRequests(t *testing.T) {
	logs := captureLogs(t)
	f := newWebhookFixture(t, nil)
	jsonCT := `application/json; charset="utf-8"`
	htmlCT := "text/html; charset=UTF-8"
	cases := []struct {
		name    string
		method  string
		target  string
		body    string
		headers map[string]string
		code    int
		page    string
		ct      string
	}{
		{"secret errato", "POST", "/" + testToken, updateBody(1),
			map[string]string{"Content-Type": "application/json", SecretTokenHeader: "sbagliato"},
			403, "403: Request had the wrong secret token", jsonCT},
		{"secret assente", "POST", "/" + testToken, updateBody(1),
			map[string]string{"Content-Type": "application/json"},
			403, "403: Request did not include the secret token", jsonCT},
		{"secret vuoto", "POST", "/" + testToken, updateBody(1),
			map[string]string{"Content-Type": "application/json", SecretTokenHeader: ""},
			403, "403: Request did not include the secret token", jsonCT},
		{"content type con charset", "POST", "/" + testToken, updateBody(1),
			map[string]string{"Content-Type": "application/json; charset=utf-8", SecretTokenHeader: testSecret},
			403, "403: Forbidden", jsonCT},
		{"content type assente", "POST", "/" + testToken, updateBody(1),
			map[string]string{SecretTokenHeader: testSecret}, 403, "403: Forbidden", jsonCT},
		{"GET", "GET", "/" + testToken, "", validHeaders, 405, "405: Method Not Allowed", jsonCT},
		{"percorso sconosciuto", "POST", "/altro", updateBody(1), validHeaders, 404, "404: Not Found", htmlCT},
		{"token come prefisso", "POST", "/" + testToken + "x", updateBody(1), validHeaders, 404, "404: Not Found", htmlCT},
		{"metodo sconosciuto", "PROPFIND", "/altro", "", nil, 405, "405: Method Not Allowed", htmlCT},
		{"JSON non valido", "POST", "/" + testToken, "{non json", validHeaders, 500, "500: Internal Server Error", jsonCT},
		{"non UTF-8", "POST", "/" + testToken, "\xff\xfe", validHeaders, 500, "500: Internal Server Error", jsonCT},
		{"lista", "POST", "/" + testToken, "[1, 2]", validHeaders, 400, "400: Update could not be processed", jsonCT},
		{"senza update_id", "POST", "/" + testToken, `{"message": {}}`, validHeaders, 400, "400: Update could not be processed", jsonCT},
		{"tipo errato", "POST", "/" + testToken, `{"update_id": 1, "message": 5}`, validHeaders, 400, "400: Update could not be processed", jsonCT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, webhookRequest(tc.method, tc.target, tc.body, tc.headers))
			if w.Code != tc.code {
				t.Errorf("status = %d, atteso %d", w.Code, tc.code)
			}
			wantBody := "<html><title>" + tc.page + "</title><body>" + tc.page + "</body></html>"
			if w.Body.String() != wantBody {
				t.Errorf("corpo = %q, atteso %q", w.Body.String(), wantBody)
			}
			if ct := w.Header().Get("Content-Type"); ct != tc.ct {
				t.Errorf("Content-Type = %q, atteso %q", ct, tc.ct)
			}
		})
	}
	f.stop()
	if got := f.recorder.processed(); len(got) != 0 {
		t.Errorf("update elaborati nonostante il rifiuto: %v", got)
	}
	assertLogContains(t, logs, "telegram.ext.Updater - CRITICAL - Something went wrong processing the data received from Telegram. "+
		"Received data was *not* processed! Received data was: [1, 2]")
	assertLogContains(t, logs, "Received data was: {'update_id': 1, 'message': 5}")
}

// TestWebhookDoesNotBlockAndKeepsOrder: il webhook risponde subito anche
// mentre un update è in elaborazione (coda illimitata di PTB) e gli update
// vengono elaborati uno alla volta nell'ordine di arrivo.
func TestWebhookDoesNotBlockAndKeepsOrder(t *testing.T) {
	block := make(chan struct{})
	f := newWebhookFixture(t, block)
	srv := httptest.NewServer(f.handler)
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	for id := int64(1); id <= 5; id++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/"+testToken, strings.NewReader(updateBody(id)))
		for k, v := range validHeaders {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("update %d: il webhook è bloccato: %v", id, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("update %d: status %d", id, resp.StatusCode)
		}
	}
	if got := f.recorder.processed(); len(got) != 0 {
		t.Fatalf("elaborati prima dello sblocco: %v", got)
	}
	go func() {
		for range 5 {
			block <- struct{}{}
		}
	}()
	// Lo stop attende che gli update in coda siano elaborati
	f.stop()
	got := f.recorder.processed()
	if len(got) != 5 {
		t.Fatalf("elaborati = %v", got)
	}
	for i, id := range got {
		if id != int64(i+1) {
			t.Fatalf("ordine di elaborazione = %v", got)
		}
	}
}

// TestPumpUpdates verifica la coda illimitata: in non si blocca e out
// riceve tutto, in ordine, prima della chiusura.
func TestPumpUpdates(t *testing.T) {
	in := make(chan json.RawMessage)
	out := make(chan json.RawMessage)
	done := make(chan struct{})
	go func() { pumpUpdates(in, out); close(done) }()
	for i := range 100 {
		in <- json.RawMessage(strings.Repeat("x", i+1))
	}
	close(in)
	n := 0
	for msg := range out {
		n++
		if len(msg) != n {
			t.Fatalf("messaggio %d fuori ordine: %q", n, msg)
		}
	}
	<-done
	if n != 100 {
		t.Errorf("ricevuti %d messaggi", n)
	}
}

// TestRawRequestPath verifica request.path di Tornado (senza decodifica).
func TestRawRequestPath(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/app/a%20b?v=3", nil)
	if got := rawRequestPath(r); got != "/app/a%20b" {
		t.Errorf("rawRequestPath = %q", got)
	}
	r.RequestURI = ""
	if got := rawRequestPath(r); got != "/app/a%20b" {
		t.Errorf("rawRequestPath senza RequestURI = %q", got)
	}
}
