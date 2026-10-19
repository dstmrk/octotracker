package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/testutil"
)

// recordSleep registra le attese di networkRetryLoop senza attendere.
type recordSleep struct{ waits []time.Duration }

func (r *recordSleep) sleep(ctx context.Context, d time.Duration) error {
	r.waits = append(r.waits, d)
	return ctx.Err()
}

// failing restituisce un'azione che fallisce con errs[i] alla chiamata i
// (poi riesce) e conta le chiamate.
func failing(calls *int, errs ...error) func(context.Context) error {
	return func(context.Context) error {
		*calls++
		if *calls <= len(errs) {
			return errs[*calls-1]
		}
		return nil
	}
}

func repeat(err error, n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = err
	}
	return out
}

// netDown è un errore di rete (connessione rifiutata).
var netDown = &url.Error{Op: "Post", URL: "https://api.telegram.org", Err: errors.New("connection refused")}

// TestNetworkRetryLoopSucceedsFirstTry: nessun nuovo tentativo né attesa.
func TestNetworkRetryLoopSucceedsFirstTry(t *testing.T) {
	var calls int
	rs := &recordSleep{}
	if err := networkRetryLoop(context.Background(), "X", time.Second, 3, rs.sleep, failing(&calls)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(rs.waits) != 0 {
		t.Errorf("chiamate %d, attese %v", calls, rs.waits)
	}
}

// TestNetworkRetryLoopTelegramErrorBackoff: con 3 nuovi tentativi ci sono 4
// chiamate con attese crescenti (×1,5), poi l'errore.
func TestNetworkRetryLoopTelegramErrorBackoff(t *testing.T) {
	logs := captureLogs(t)
	var calls int
	rs := &recordSleep{}
	boom := testutil.TelegramError("setWebhook", 500, "Internal Server Error")
	err := networkRetryLoop(context.Background(), "setWebhook", time.Second, 3, rs.sleep, failing(&calls, repeat(boom, 10)...))
	if !errors.Is(err, boom) {
		t.Fatalf("errore = %v", err)
	}
	if calls != 4 {
		t.Errorf("tentativi = %d, attesi 4", calls)
	}
	want := []time.Duration{1500 * time.Millisecond, 2250 * time.Millisecond, 3375 * time.Millisecond}
	if fmt.Sprint(rs.waits) != fmt.Sprint(want) {
		t.Errorf("attese = %v, attese %v", rs.waits, want)
	}
	assertLogContains(t, logs, "ERROR - ❌ setWebhook: fallito dopo 4 tentativi: Internal Server Error")
}

// TestNetworkRetryLoopBackoffCap: le attese crescono fino a 30 secondi
// (maxRetries < 0 = senza limite).
func TestNetworkRetryLoopBackoffCap(t *testing.T) {
	var calls int
	rs := &recordSleep{}
	err := networkRetryLoop(context.Background(), "X", time.Second, -1, rs.sleep, failing(&calls, repeat(netDown, 12)...))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 13 || rs.waits[len(rs.waits)-1] != 30*time.Second || rs.waits[8] != 30*time.Second {
		t.Errorf("chiamate %d, attese %v", calls, rs.waits)
	}
}

// TestNetworkRetryLoopTimedOutRetriesImmediately: un timeout viene
// ritentato subito; dopo un timeout l'attesa per un errore di rete riparte
// da 1 secondo.
func TestNetworkRetryLoopTimedOutRetriesImmediately(t *testing.T) {
	var calls int
	rs := &recordSleep{}
	err := networkRetryLoop(context.Background(), "X", time.Second, 3, rs.sleep, failing(&calls, repeat(context.DeadlineExceeded, 10)...))
	if !errors.Is(err, context.DeadlineExceeded) || calls != 4 || fmt.Sprint(rs.waits) != "[0s 0s 0s]" {
		t.Errorf("errore %v, chiamate %d, attese %v", err, calls, rs.waits)
	}

	calls = 0
	rs = &recordSleep{}
	err = networkRetryLoop(context.Background(), "X", time.Second, 3, rs.sleep, failing(&calls, context.DeadlineExceeded, netDown))
	if err != nil || fmt.Sprint(rs.waits) != fmt.Sprint([]time.Duration{0, time.Second}) {
		t.Errorf("errore %v, attese %v", err, rs.waits)
	}
}

// TestNetworkRetryLoopRetryAfter: un rate limit attende retry_after + 0,5 s.
func TestNetworkRetryLoopRetryAfter(t *testing.T) {
	var calls int
	rs := &recordSleep{}
	flood := testutil.RetryAfterError("setWebhook", 7)
	if err := networkRetryLoop(context.Background(), "X", time.Second, 3, rs.sleep, failing(&calls, flood)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rs.waits) != fmt.Sprint([]time.Duration{7500 * time.Millisecond}) {
		t.Errorf("attese = %v", rs.waits)
	}

	calls = 0
	err := networkRetryLoop(context.Background(), "X", time.Second, 0, rs.sleep, failing(&calls, flood))
	if !errors.Is(err, flood) || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}
}

// TestNetworkRetryLoopInvalidTokenAndOtherErrors: un token rifiutato e gli
// errori che non vengono da Telegram interrompono subito il loop.
func TestNetworkRetryLoopInvalidTokenAndOtherErrors(t *testing.T) {
	logs := captureLogs(t)
	var calls int
	rs := &recordSleep{}
	unauthorized := testutil.TelegramError("getMe", 401, "Unauthorized")
	err := networkRetryLoop(context.Background(), "getMe", time.Second, 3, rs.sleep, failing(&calls, repeat(unauthorized, 5)...))
	if !errors.Is(err, unauthorized) || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}
	assertLogContains(t, logs, "ERROR - ❌ getMe: token del bot rifiutato da Telegram")

	calls = 0
	boom := errors.New("boom")
	if err := networkRetryLoop(context.Background(), "X", time.Second, 3, rs.sleep, failing(&calls, boom)); !errors.Is(err, boom) || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}
}

// TestNetworkRetryLoopCancellation: la cancellazione di ctx interrompe il
// loop (azione o attesa).
func TestNetworkRetryLoopCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	err := networkRetryLoop(ctx, "X", time.Second, 3, sleepCtx, failing(&calls, repeat(netDown, 5)...))
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	calls = 0
	sleepCancel := func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	action := func(context.Context) error { calls++; return netDown }
	if err := networkRetryLoop(ctx, "X", time.Second, 3, sleepCancel, action); err == nil || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}
}

// TestInitializeBot: getMe imposta l'utente del bot; un token rifiutato non
// compare nel messaggio d'errore.
func TestInitializeBot(t *testing.T) {
	b, fc := testutil.NewBot(t)
	b.User = gotgbot.User{}
	if err := initializeBot(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if b.Username != testutil.BotUsername || b.Id != 123456 {
		t.Errorf("utente = %+v", b.User)
	}

	fc.FailMethod("getMe", testutil.TelegramError("getMe", 401, "Unauthorized"))
	err := initializeBot(context.Background(), b)
	if !isInvalidToken(err) || strings.Contains(err.Error(), b.Token) {
		t.Errorf("errore = %v", err)
	}
}

// TestBootstrapWebhook: deleteWebhook(drop_pending_updates=True) e poi
// setWebhook con i parametri del webhook.
func TestBootstrapWebhook(t *testing.T) {
	b, fc := testutil.NewBot(t)
	rs := &recordSleep{}
	url := "https://octo.example.com/" + testToken
	if err := bootstrapWebhook(context.Background(), b, url, testSecret, rs.sleep); err != nil {
		t.Fatal(err)
	}
	calls := fc.Calls()
	if methods(calls) != "deleteWebhook,setWebhook" {
		t.Fatalf("chiamate = %s", methods(calls))
	}
	if calls[0].String("drop_pending_updates") != "true" {
		t.Errorf("deleteWebhook = %v", calls[0].Params)
	}
	set := calls[1]
	if set.String("url") != url || set.String("max_connections") != "40" ||
		set.String("drop_pending_updates") != "true" || set.String("secret_token") != testSecret {
		t.Errorf("setWebhook = %v", set.Params)
	}
	wantTypes := `["message","edited_message","channel_post","edited_channel_post","inline_query",` +
		`"chosen_inline_result","callback_query","shipping_query","pre_checkout_query","poll","poll_answer",` +
		`"my_chat_member","chat_member","chat_join_request","chat_boost","removed_chat_boost","message_reaction",` +
		`"message_reaction_count","business_connection","business_message","edited_business_message",` +
		`"deleted_business_messages","purchased_paid_media"]`
	if got := set.JSON("allowed_updates"); got != wantTypes {
		t.Errorf("allowed_updates = %s", got)
	}

	// deleteWebhook che fallisce sempre: niente setWebhook
	fc.Reset()
	fc.FailMethod("deleteWebhook", testutil.TelegramError("deleteWebhook", 502, "Bad Gateway"))
	if err := bootstrapWebhook(context.Background(), b, url, testSecret, rs.sleep); err == nil {
		t.Fatal("atteso errore")
	}
	if len(fc.CallsTo("deleteWebhook")) != 4 || len(fc.CallsTo("setWebhook")) != 0 {
		t.Errorf("chiamate = %s", methods(fc.Calls()))
	}
}

// TestNewBotTimeouts verifica la configurazione del client HTTP del bot.
func TestNewBotTimeouts(t *testing.T) {
	b := NewBot("not-a-valid-token")
	if b.Token != "not-a-valid-token" {
		t.Errorf("token = %q", b.Token)
	}
	client, ok := b.BotClient.(*gotgbot.BaseBotClient)
	if !ok {
		t.Fatalf("BotClient = %T", b.BotClient)
	}
	if client.DefaultRequestOpts == nil || client.DefaultRequestOpts.Timeout != -1 {
		t.Errorf("DefaultRequestOpts = %+v", client.DefaultRequestOpts)
	}
	tr, ok := client.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T", client.Client.Transport)
	}
	if tr.TLSHandshakeTimeout != ConnectTimeout || tr.IdleConnTimeout != KeepAliveExpiry ||
		tr.MaxConnsPerHost != MaxConnections || tr.ForceAttemptHTTP2 || tr.Proxy == nil {
		t.Errorf("trasporto = %+v", tr)
	}
}

// TestBotTransportPerOperationTimeouts: il timeout di lettura vale per ogni
// lettura (una risposta lenta ma continua non scade), una risposta che non
// arriva scade come TimedOut.
func TestBotTransportPerOperationTimeouts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow-stream":
			// 6 pezzi a 40 ms l'uno: 240 ms in tutto, ma mai 100 ms di silenzio
			fl := w.(http.Flusher)
			for range 6 {
				_, _ = io.WriteString(w, "x")
				fl.Flush()
				time.Sleep(40 * time.Millisecond)
			}
		case "/stall":
			time.Sleep(400 * time.Millisecond)
		}
	}))
	defer srv.Close()

	client := &http.Client{Transport: newBotTransport(botTimeouts{connect: time.Second, read: 150 * time.Millisecond, write: time.Second})}
	resp, err := client.Get(srv.URL + "/slow-stream")
	if err != nil {
		t.Fatalf("slow-stream: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "xxxxxx" {
		t.Fatalf("slow-stream: %q %v", body, err)
	}

	_, err = client.Get(srv.URL + "/stall")
	if !isTimeout(err) {
		t.Errorf("stall: errore %v", err)
	}
}

// TestDeadlineConn: Read e Write scadono dopo la durata indicata.
func TestDeadlineConn(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := &deadlineConn{Conn: a, read: 20 * time.Millisecond, write: 20 * time.Millisecond}
	var netErr net.Error
	if _, err := c.Read(make([]byte, 1)); !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("Read = %v", err)
	}
	if _, err := c.Write([]byte("x")); !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("Write = %v", err)
	}
	_ = a.Close()
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("Read su connessione chiusa")
	}
	if _, err := c.Write([]byte("x")); err == nil {
		t.Error("Write su connessione chiusa")
	}
}
