package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// recordSleep registra le attese di network_retry_loop senza attendere.
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

// TestNetworkRetryLoopSucceedsFirstTry: nessun nuovo tentativo né attesa.
func TestNetworkRetryLoopSucceedsFirstTry(t *testing.T) {
	var calls int
	rs := &recordSleep{}
	if err := networkRetryLoop(context.Background(), "X", 1, 3, rs.sleep, failing(&calls)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(rs.waits) != 0 {
		t.Errorf("chiamate %d, attese %v", calls, rs.waits)
	}
}

// TestNetworkRetryLoopTelegramErrorBackoff: con bootstrap_retries=3 ci sono
// 4 tentativi con attese crescenti (×1,5), poi l'errore con il log di PTB.
func TestNetworkRetryLoopTelegramErrorBackoff(t *testing.T) {
	logs := captureLogs(t)
	var calls int
	rs := &recordSleep{}
	boom := testutil.TelegramError("setWebhook", 500, "Internal Server Error")
	err := networkRetryLoop(context.Background(), "Bootstrap Set Webhook", 1, 3, rs.sleep, failing(&calls, repeat(boom, 10)...))
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
	assertLogContains(t, logs, "telegram.ext - ERROR - Network Retry Loop (Bootstrap Set Webhook): Failed run number 3 of 3. Aborting.")
}

// TestNetworkRetryLoopBackoffCap: le attese crescono fino a 30 secondi
// (max_retries < 0 = all'infinito).
func TestNetworkRetryLoopBackoffCap(t *testing.T) {
	var calls int
	rs := &recordSleep{}
	err := networkRetryLoop(context.Background(), "X", 1, -1, rs.sleep, failing(&calls, repeat(pycompat.NewError("NetworkError", "down"), 12)...))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 13 || rs.waits[len(rs.waits)-1] != 30*time.Second || rs.waits[8] != 30*time.Second {
		t.Errorf("chiamate %d, attese %v", calls, rs.waits)
	}
}

// TestNetworkRetryLoopTimedOutRetriesImmediately: un timeout viene
// ritentato subito; dopo un timeout l'attesa per un errore Telegram riparte
// da 1 secondo.
func TestNetworkRetryLoopTimedOutRetriesImmediately(t *testing.T) {
	logs := captureLogs(t)
	var calls int
	rs := &recordSleep{}
	err := networkRetryLoop(context.Background(), "X", 1, 3, rs.sleep, failing(&calls, repeat(context.DeadlineExceeded, 10)...))
	if !errors.Is(err, context.DeadlineExceeded) || calls != 4 || len(rs.waits) != 0 {
		t.Errorf("errore %v, chiamate %d, attese %v", err, calls, rs.waits)
	}
	assertLogContains(t, logs, "Network Retry Loop (X): Timed out: Timed out. Failed run number 3 of 3. Aborting.")

	calls = 0
	rs = &recordSleep{}
	err = networkRetryLoop(context.Background(), "X", 1, 3, rs.sleep,
		failing(&calls, context.DeadlineExceeded, pycompat.NewError("NetworkError", "down")))
	if err != nil || fmt.Sprint(rs.waits) != fmt.Sprint([]time.Duration{time.Second}) {
		t.Errorf("errore %v, attese %v", err, rs.waits)
	}
}

// TestNetworkRetryLoopRetryAfter: RetryAfter attende retry_after + 0,5 s.
func TestNetworkRetryLoopRetryAfter(t *testing.T) {
	logs := captureLogs(t)
	var calls int
	rs := &recordSleep{}
	flood := testutil.RetryAfterError("setWebhook", 7)
	if err := networkRetryLoop(context.Background(), "X", 1, 3, rs.sleep, failing(&calls, flood)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rs.waits) != fmt.Sprint([]time.Duration{7500 * time.Millisecond}) {
		t.Errorf("attese = %v", rs.waits)
	}

	calls = 0
	err := networkRetryLoop(context.Background(), "X", 1, 0, rs.sleep, failing(&calls, flood))
	if !errors.Is(err, flood) || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}
	assertLogContains(t, logs, "Network Retry Loop (X): Flood control exceeded. Retry in 7 seconds. "+
		"Adding 0.5 seconds to the specified time. Failed run number 0 of 0. Aborting.")
}

// TestNetworkRetryLoopInvalidTokenAndOtherErrors: InvalidToken e gli errori
// non Telegram interrompono subito il loop.
func TestNetworkRetryLoopInvalidTokenAndOtherErrors(t *testing.T) {
	logs := captureLogs(t)
	var calls int
	rs := &recordSleep{}
	unauthorized := testutil.TelegramError("getMe", 401, "Unauthorized")
	err := networkRetryLoop(context.Background(), "Bootstrap Initialize Application", 1, 3, rs.sleep, failing(&calls, repeat(unauthorized, 5)...))
	if !errors.Is(err, unauthorized) || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}
	assertLogContains(t, logs, "Network Retry Loop (Bootstrap Initialize Application): Invalid token. Aborting retry loop.")

	calls = 0
	boom := valueError("boom")
	if err := networkRetryLoop(context.Background(), "X", 1, 3, rs.sleep, failing(&calls, boom)); !errors.Is(err, boom) || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}
}

// TestNetworkRetryLoopCancellation: la cancellazione di ctx interrompe il
// loop (azione o attesa).
func TestNetworkRetryLoopCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	err := networkRetryLoop(ctx, "X", 1, 3, sleepCtx, failing(&calls, repeat(pycompat.NewError("NetworkError", "x"), 5)...))
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	calls = 0
	sleepCancel := func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	action := func(context.Context) error { calls++; return pycompat.NewError("NetworkError", "x") }
	if err := networkRetryLoop(ctx, "X", 1, 3, sleepCancel, action); err == nil || calls != 1 {
		t.Errorf("errore %v, chiamate %d", err, calls)
	}
}

// TestInitializeBot: getMe imposta l'utente del bot; un token rifiutato
// diventa InvalidToken senza il token nel messaggio.
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
	if !pycompat.IsInstance(err, "InvalidToken") || strings.Contains(err.Error(), b.Token) {
		t.Errorf("errore = %v", err)
	}
	if describe(err).class != "InvalidToken" {
		t.Errorf("classe = %s", describe(err).class)
	}

	boom := testutil.TelegramError("getMe", 500, "Internal Server Error")
	fc.FailMethod("getMe", boom)
	if err := initializeBot(context.Background(), b); !errors.Is(err, boom) {
		t.Errorf("errore = %v", err)
	}
}

// TestBootstrapWebhook: deleteWebhook(drop_pending_updates=True) e poi
// setWebhook con i parametri di run_webhook.
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
	if err == nil || describe(err).class != "TimedOut" {
		t.Errorf("stall: errore %v (classe %s)", err, describe(err).class)
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

// TestFormatSeconds verifica la stampa dei secondi come in Python.
func TestFormatSeconds(t *testing.T) {
	for in, want := range map[float64]string{600: "600", 0.5: "0.5", 0.01: "0.01"} {
		if got := formatSeconds(in); got != want {
			t.Errorf("formatSeconds(%v) = %q", in, got)
		}
	}
}
