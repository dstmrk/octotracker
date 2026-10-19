package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/testutil"
)

// TestErrorHandlerNetworkErrorsOnlyWarn: timeout, errori di rete, 400 e 5xx
// dell'API producono solo il warning, senza alert all'admin.
func TestErrorHandlerNetworkErrorsOnlyWarn(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"timeout", context.DeadlineExceeded, "context deadline exceeded"},
		{"bad request", testutil.TelegramError("editMessageText", 400, "Bad Request: message is not modified"), "Bad Request: message is not modified"},
		{"server error", testutil.TelegramError("sendMessage", 500, "Internal Server Error"), "Internal Server Error"},
		{"rete", &url.Error{Op: "Post", URL: "https://api.telegram.org", Err: errors.New("connection refused")}, `Post "https://api.telegram.org": connection refused`},
		{"risposta illeggibile", errors.New("failed to decode POST request to sendMessage: invalid character"), "failed to decode POST request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			b, fc := testutil.NewBot(t)
			h := &ErrorHandler{AdminUserID: "12345"}
			action := h.HandleError(b, testutil.MessageContext(b, 1, "/start"), tc.err)
			if action != ext.DispatcherActionNoop {
				t.Errorf("action = %v", action)
			}
			assertLogContains(t, logs, "ERROR - ❌ Errore: "+tc.wantMsg)
			assertLogContains(t, logs, "WARNING - ⏱️  Timeout/errore di rete - probabilmente connessione lenta. Il bot riproverà automaticamente.")
			if calls := fc.Calls(); len(calls) != 0 {
				t.Errorf("chiamate inattese: %v", calls)
			}
		})
	}
}

// TestErrorHandlerRetryAfterSleeps: rate limit → warning e attesa di
// retry_after secondi.
func TestErrorHandlerRetryAfterSleeps(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	var slept []time.Duration
	h := &ErrorHandler{AdminUserID: "12345", Sleep: func(d time.Duration) { slept = append(slept, d) }}
	h.HandleError(b, nil, testutil.RetryAfterError("sendMessage", 7))

	assertLogContains(t, logs, "WARNING - ⏱️  Rate limit: attendo 7s")
	if len(slept) != 1 || slept[0] != 7*time.Second {
		t.Errorf("attese = %v", slept)
	}
	if len(fc.Calls()) != 0 {
		t.Errorf("chiamate inattese: %v", fc.Calls())
	}
}

// TestErrorHandlerUnhandledSendsAdminAlert: un errore non gestito viene
// loggato con il tipo e segnalato all'admin con il testo HTML dell'alert.
func TestErrorHandlerUnhandledSendsAdminAlert(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	ctx := testutil.Context(b, &gotgbot.Update{UpdateId: 7, EditedMessage: &gotgbot.Message{
		MessageId: 3, Date: 1700000000, Chat: gotgbot.Chat{Id: 99, Type: "private"}, Text: "/foo <b>",
	}})
	h := &ErrorHandler{AdminUserID: "12345"}
	h.HandleError(b, ctx, errors.New("qualcosa <b>non</b> va"))

	assertLogContains(t, logs, "ERROR - ❌ Errore: qualcosa <b>non</b> va")
	assertLogContains(t, logs, "ERROR - ⚠️  Errore non gestito: *errors.errorString")
	assertLogContains(t, logs, "INFO - 📨 Alert errore inviato all'admin 12345")

	call := fc.LastCall(t, "sendMessage")
	if call.Params["chat_id"] != "12345" || call.ParseMode() != "HTML" {
		t.Errorf("parametri = %v", call.Params)
	}
	updateJSON, _ := json.Marshal(ctx.Update)
	// Il messaggio d'errore non viene sottoposto a escape HTML.
	want := "🚨 <b>Errore Bot OctoTracker</b>\n\n" +
		"<b>Tipo:</b> *errors.errorString\n" +
		"<b>Messaggio:</b> qualcosa <b>non</b> va\n" +
		"<b>Update:</b> " + string(updateJSON)
	if call.Text() != want {
		t.Errorf("testo alert =\n%q\natteso\n%q", call.Text(), want)
	}
	escaped := "/foo " + `\` + "u003cb" + `\` + "u003e"
	if strings.Contains(call.Text(), "/foo <b>") || !strings.Contains(call.Text(), escaped) {
		t.Errorf("l'update deve avere <, > e & in forma \\u003c: %s", call.Text())
	}
}

// TestErrorHandlerTelegramErrorsAlertAdmin: gli errori dell'API che non sono
// transitori né rate limit vanno all'admin con il codice e la descrizione.
func TestErrorHandlerTelegramErrorsAlertAdmin(t *testing.T) {
	cases := []struct {
		err  error
		kind string
		msg  string
	}{
		{testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"), "TelegramError 403", "Forbidden: bot was blocked by the user"},
		{testutil.TelegramError("sendMessage", 401, "Unauthorized"), "TelegramError 401", "Unauthorized"},
		{testutil.TelegramError("getUpdates", 409, "Conflict: terminated by other getUpdates request"), "TelegramError 409", "Conflict: terminated by other getUpdates request"},
		{&gotgbot.TelegramError{Code: 400, Description: "Bad Request: group chat was upgraded to a supergroup chat",
			ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -100123, RetryAfter: 3}}, "TelegramError 400", "Bad Request: group chat was upgraded to a supergroup chat"},
	}
	for _, tc := range cases {
		logs := captureLogs(t)
		b, fc := testutil.NewBot(t)
		h := &ErrorHandler{AdminUserID: "@admin", Sleep: func(time.Duration) { t.Error("attesa inattesa") }}
		h.HandleError(b, nil, tc.err)
		assertLogContains(t, logs, "⚠️  Errore non gestito: "+tc.kind)
		call := fc.LastCall(t, "sendMessage")
		if !strings.Contains(call.Text(), "<b>Tipo:</b> "+tc.kind+"\n<b>Messaggio:</b> "+tc.msg+"\n<b>Update:</b> None") {
			t.Errorf("testo alert = %q", call.Text())
		}
		if call.Params["chat_id"] != "@admin" {
			t.Errorf("chat_id = %v", call.Params["chat_id"])
		}
	}
}

// TestErrorHandlerNoAdmin: senza ADMIN_USER_ID nessun alert.
func TestErrorHandlerNoAdmin(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	h := &ErrorHandler{}
	h.HandleError(b, nil, fmt.Errorf("lettura: %w", fs.ErrNotExist))
	assertLogContains(t, logs, "ERROR - ❌ Errore: lettura: file does not exist")
	assertLogContains(t, logs, "ERROR - ⚠️  Errore non gestito: *errors.errorString")
	if len(fc.Calls()) != 0 {
		t.Errorf("chiamate inattese: %v", fc.Calls())
	}
}

// TestErrorHandlerAdminAlertFailure: se l'invio dell'alert fallisce
// l'errore viene loggato.
func TestErrorHandlerAdminAlertFailure(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"))
	h := &ErrorHandler{AdminUserID: "12345"}
	h.HandleError(b, nil, errors.New("errore inatteso"))
	assertLogContains(t, logs, "ERROR - ❌ Errore invio alert admin: Forbidden: bot was blocked by the user")
	assertLogNotContains(t, logs, "📨 Alert errore inviato")
}

// TestErrorHandlerTruncatesMessage: il messaggio dell'alert è limitato a 200
// caratteri (non byte).
func TestErrorHandlerTruncatesMessage(t *testing.T) {
	captureLogs(t)
	b, fc := testutil.NewBot(t)
	h := &ErrorHandler{AdminUserID: "1"}
	h.HandleError(b, nil, errors.New(strings.Repeat("è", 250)))
	text := fc.LastCall(t, "sendMessage").Text()
	if !strings.Contains(text, "<b>Messaggio:</b> "+strings.Repeat("è", 200)+"\n") {
		t.Errorf("messaggio non troncato a 200 caratteri: %q", text)
	}
}

// TestErrorHandlerPanic: un panic in un handler viene recuperato dal
// dispatcher e gestito come un errore (log con stack e alert).
func TestErrorHandlerPanic(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	h := &ErrorHandler{AdminUserID: "1"}
	d := ext.NewDispatcher(&ext.DispatcherOpts{Error: h.HandleError, Panic: h.HandlePanic, MaxRoutines: 1})
	d.AddHandler(panicHandler{})
	if err := d.ProcessUpdate(b, testutil.MessageUpdate(1, "ciao"), nil); err != nil {
		t.Fatalf("ProcessUpdate = %v", err)
	}
	assertLogContains(t, logs, "ERROR - ❌ Errore: indice fuori intervallo stack=")
	if !strings.Contains(fc.LastCall(t, "sendMessage").Text(), "<b>Messaggio:</b> indice fuori intervallo\n") {
		t.Error("alert senza il messaggio del panic")
	}
	h.HandlePanic(b, nil, 42)
	assertLogContains(t, logs, "ERROR - ❌ Errore: 42")
}

type panicHandler struct{}

func (panicHandler) CheckUpdate(*gotgbot.Bot, *ext.Context) bool { return true }
func (panicHandler) HandleUpdate(*gotgbot.Bot, *ext.Context) error {
	panic("indice fuori intervallo")
}
func (panicHandler) Name() string { return "panic" }

// TestErrorHandlerDefaultSleep: senza Sleep si usa time.Sleep.
func TestErrorHandlerDefaultSleep(t *testing.T) {
	h := &ErrorHandler{}
	start := time.Now()
	h.sleep(10 * time.Millisecond)
	if time.Since(start) < 10*time.Millisecond {
		t.Error("sleep troppo breve")
	}
}

// TestUpdateStringAndTruncate verifica gli helper dell'alert.
func TestUpdateStringAndTruncate(t *testing.T) {
	if updateString(nil) != "None" {
		t.Errorf("updateString(nil) = %q", updateString(nil))
	}
	if got := updateString(&gotgbot.Update{UpdateId: 5}); got != `{"update_id":5}` {
		t.Errorf("updateString = %q", got)
	}
	for _, tc := range []struct {
		s    string
		n    int
		want string
	}{{"abc", 2, "ab"}, {"abc", 3, "abc"}, {"àèì", 1, "à"}, {"", 5, ""}, {"🐙x", 1, "🐙"}} {
		if got := truncateRunes(tc.s, tc.n); got != tc.want {
			t.Errorf("truncateRunes(%q, %d) = %q", tc.s, tc.n, got)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

// TestErrorClassification verifica le categorie degli errori.
func TestErrorClassification(t *testing.T) {
	flood := testutil.RetryAfterError("x", 3)
	migrated := &gotgbot.TelegramError{Code: 400, ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -1, RetryAfter: 3}}
	cases := []struct {
		name                      string
		err                       error
		network, timeout, invalid bool
		retryAfter                int64
		kind                      string
	}{
		{"400", testutil.TelegramError("x", 400, "Bad Request: chat not found"), true, false, false, 0, "TelegramError 400"},
		{"502", testutil.TelegramError("x", 502, "Bad Gateway"), true, false, false, 0, "TelegramError 502"},
		{"401", testutil.TelegramError("x", 401, "Unauthorized"), false, false, true, 0, "TelegramError 401"},
		{"404", testutil.TelegramError("x", 404, "Not Found"), false, false, true, 0, "TelegramError 404"},
		{"rate limit", flood, false, false, false, 3, "TelegramError 429"},
		{"migrazione", migrated, false, false, false, 0, "TelegramError 400"},
		{"timeout di rete", fmt.Errorf("failed to execute POST request to x: %w", timeoutErr{}), true, true, false, 0, "app.timeoutErr"},
		{"connessione", &net.OpError{Op: "dial", Err: errors.New("refused")}, true, false, false, 0, "*errors.errorString"},
		{"generico", errors.New("boom"), false, false, false, 0, "*errors.errorString"},
	}
	for _, tc := range cases {
		if got := isNetworkError(tc.err); got != tc.network {
			t.Errorf("%s: isNetworkError = %v", tc.name, got)
		}
		if got := isTimeout(tc.err); got != tc.timeout {
			t.Errorf("%s: isTimeout = %v", tc.name, got)
		}
		if got := isInvalidToken(tc.err); got != tc.invalid {
			t.Errorf("%s: isInvalidToken = %v", tc.name, got)
		}
		if got := retryAfter(tc.err); got != tc.retryAfter {
			t.Errorf("%s: retryAfter = %d", tc.name, got)
		}
		if got := errorType(tc.err); got != tc.kind {
			t.Errorf("%s: errorType = %q, atteso %q", tc.name, got, tc.kind)
		}
	}
	if !isIOError(&fs.PathError{Op: "open", Path: "x", Err: fs.ErrNotExist}) || isIOError(errors.New("x")) {
		t.Error("isIOError")
	}
}

// TestPanicError verifica la conversione dei valori di panic.
func TestPanicError(t *testing.T) {
	base := errors.New("x")
	if panicError(base) != base {
		t.Error("un error deve restare invariato")
	}
	if panicError("y").Error() != "y" || panicError(3).Error() != "3" {
		t.Error("valore non convertito")
	}
}
