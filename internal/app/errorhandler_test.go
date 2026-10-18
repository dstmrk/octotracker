package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/scraper"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// TestErrorHandlerNetworkErrorsOnlyWarn: TimedOut, NetworkError e
// BadRequest (sottoclasse di NetworkError in PTB) producono solo il warning,
// senza alert all'admin.
func TestErrorHandlerNetworkErrorsOnlyWarn(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"timeout", context.DeadlineExceeded, "Timed out"},
		{"bad request", testutil.TelegramError("editMessageText", 400, "Bad Request: message is not modified"), "Message is not modified"},
		{"server error", testutil.TelegramError("sendMessage", 500, "Internal Server Error"), "Internal Server Error"},
		{"rete", &url.Error{Op: "Post", URL: "https://api.telegram.org", Err: errors.New("connection refused")}, `Post "https://api.telegram.org": connection refused`},
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

// TestErrorHandlerRetryAfterSleeps: RetryAfter → warning e attesa di
// retry_after secondi.
func TestErrorHandlerRetryAfterSleeps(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	var slept []time.Duration
	h := &ErrorHandler{AdminUserID: "12345", Sleep: func(d time.Duration) { slept = append(slept, d) }}
	h.HandleError(b, nil, testutil.RetryAfterError("sendMessage", 7))

	assertLogContains(t, logs, "ERROR - ❌ Errore: Flood control exceeded. Retry in 7 seconds")
	assertLogContains(t, logs, "WARNING - ⏱️  Rate limit: attendo 7s")
	if len(slept) != 1 || slept[0] != 7*time.Second {
		t.Errorf("attese = %v", slept)
	}
	if len(fc.Calls()) != 0 {
		t.Errorf("chiamate inattese: %v", fc.Calls())
	}
}

// TestErrorHandlerUnhandledSendsAdminAlert: un errore non gestito viene
// loggato con il nome della classe e segnalato all'admin con il testo HTML
// di bot.py.
func TestErrorHandlerUnhandledSendsAdminAlert(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	ctx := testutil.Context(b, &gotgbot.Update{UpdateId: 7, EditedMessage: &gotgbot.Message{
		MessageId: 3, Date: 1700000000, Chat: gotgbot.Chat{Id: 99, Type: "private"}, Text: "/foo <b>",
	}})
	h := &ErrorHandler{AdminUserID: "12345"}
	h.HandleError(b, ctx, errors.New("AttributeError: 'NoneType' object has no attribute 'reply_text'"))

	assertLogContains(t, logs, "ERROR - ❌ Errore: 'NoneType' object has no attribute 'reply_text'")
	assertLogContains(t, logs, "ERROR - ⚠️  Errore non gestito: AttributeError")
	assertLogContains(t, logs, "INFO - 📨 Alert errore inviato all'admin 12345")

	call := fc.LastCall(t, "sendMessage")
	if call.Params["chat_id"] != "12345" || call.ParseMode() != "HTML" {
		t.Errorf("parametri = %v", call.Params)
	}
	updateJSON, _ := json.Marshal(ctx.Update)
	want := "🚨 <b>Errore Bot OctoTracker</b>\n\n" +
		"<b>Tipo:</b> AttributeError\n" +
		"<b>Messaggio:</b> 'NoneType' object has no attribute 'reply_text'\n" +
		"<b>Update:</b> " + string(updateJSON)
	if call.Text() != want {
		t.Errorf("testo alert =\n%q\natteso\n%q", call.Text(), want)
	}
	escaped := "/foo " + `\` + "u003cb" + `\` + "u003e"
	if strings.Contains(call.Text(), "/foo <b>") || !strings.Contains(call.Text(), escaped) {
		t.Errorf("l'update deve avere <, > e & in forma \\u003c: %s", call.Text())
	}
}

// TestErrorHandlerTelegramErrorClasses: gli errori Telegram che non sono
// NetworkError né RetryAfter vanno all'admin con la classe PTB.
func TestErrorHandlerTelegramErrorClasses(t *testing.T) {
	cases := []struct {
		err   error
		class string
		msg   string
	}{
		{testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"), "Forbidden", "Forbidden: bot was blocked by the user"},
		{testutil.TelegramError("sendMessage", 401, "Unauthorized"), "InvalidToken", "Unauthorized"},
		{testutil.TelegramError("getUpdates", 409, "Conflict: terminated by other getUpdates request"), "Conflict", "Conflict: terminated by other getUpdates request"},
		{&gotgbot.TelegramError{Code: 400, Description: "Bad Request: group chat was upgraded to a supergroup chat",
			ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -100123}}, "ChatMigrated", "Group migrated to supergroup. New chat id: -100123"},
	}
	for _, tc := range cases {
		logs := captureLogs(t)
		b, fc := testutil.NewBot(t)
		h := &ErrorHandler{AdminUserID: "@admin"}
		h.HandleError(b, nil, tc.err)
		assertLogContains(t, logs, "⚠️  Errore non gestito: "+tc.class)
		call := fc.LastCall(t, "sendMessage")
		if !strings.Contains(call.Text(), "<b>Tipo:</b> "+tc.class+"\n<b>Messaggio:</b> "+tc.msg+"\n<b>Update:</b> None") {
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
	h.HandleError(b, nil, errors.New("boom"))
	assertLogContains(t, logs, "ERROR - ❌ Errore: boom")
	assertLogContains(t, logs, "ERROR - ⚠️  Errore non gestito: Exception")
	if len(fc.Calls()) != 0 {
		t.Errorf("chiamate inattese: %v", fc.Calls())
	}
}

// TestErrorHandlerTruncatesMessage: str(error)[:200] conta i caratteri, non i byte.
func TestErrorHandlerTruncatesMessage(t *testing.T) {
	captureLogs(t)
	b, fc := testutil.NewBot(t)
	long := strings.Repeat("è", 250)
	h := &ErrorHandler{AdminUserID: "1"}
	h.HandleError(b, nil, errors.New("ValueError: "+long))
	text := fc.LastCall(t, "sendMessage").Text()
	if !strings.Contains(text, "<b>Messaggio:</b> "+strings.Repeat("è", 200)+"\n") {
		t.Errorf("messaggio non troncato a 200 caratteri: %q", text)
	}
}

// TestErrorHandlerPanic: un panic in un handler viene recuperato dal
// dispatcher e gestito come un'eccezione (log con stack e alert).
func TestErrorHandlerPanic(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	h := &ErrorHandler{AdminUserID: "1"}
	d := ext.NewDispatcher(&ext.DispatcherOpts{Error: h.HandleError, Panic: h.HandlePanic, MaxRoutines: 1})
	d.AddHandler(panicHandler{})
	if err := d.ProcessUpdate(b, testutil.MessageUpdate(1, "ciao"), nil); err != nil {
		t.Fatalf("ProcessUpdate = %v", err)
	}
	assertLogContains(t, logs, "ERROR - ❌ Errore: list index out of range stack=")
	assertLogContains(t, logs, "⚠️  Errore non gestito: IndexError")
	if !strings.Contains(fc.LastCall(t, "sendMessage").Text(), "<b>Tipo:</b> IndexError") {
		t.Error("alert senza la classe IndexError")
	}
	h.HandlePanic(b, nil, 42)
	assertLogContains(t, logs, "ERROR - ❌ Errore: 42")
}

type panicHandler struct{}

func (panicHandler) CheckUpdate(*gotgbot.Bot, *ext.Context) bool { return true }
func (panicHandler) HandleUpdate(*gotgbot.Bot, *ext.Context) error {
	panic("IndexError: list index out of range")
}
func (panicHandler) Name() string { return "panic" }

// TestErrorHandlerDefaultSleep: senza Sleep si usa time.Sleep.
func TestErrorHandlerDefaultSleep(t *testing.T) {
	captureLogs(t)
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

type typedErr struct{}

func (typedErr) Error() string  { return "boom" }
func (typedErr) PyType() string { return "LookupError" }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

// TestDescribe verifica la classificazione degli errori nelle classi Python.
func TestDescribe(t *testing.T) {
	cases := []struct {
		err   error
		class string
		msg   string
	}{
		{nil, "", ""},
		{testutil.TelegramError("x", 400, "Bad Request: chat not found"), "BadRequest", "Chat not found"},
		{testutil.TelegramError("x", 404, "Not Found"), "InvalidToken", "Not Found"},
		{testutil.TelegramError("x", 502, "Bad Gateway"), "NetworkError", "Bad Gateway"},
		{testutil.RetryAfterError("x", 3), "RetryAfter", "Flood control exceeded. Retry in 3 seconds"},
		{fmt.Errorf("wrap: %w", pycompat.NewError("FileNotFoundError", "[Errno 2] x")), "FileNotFoundError", "[Errno 2] x"},
		{&scraper.PyException{Type: "AttributeError", Msg: "m"}, "AttributeError", "m"},
		{checker.KeyError("commercializzazione"), "KeyError", "'commercializzazione'"},
		{typedErr{}, "LookupError", "boom"},
		{fmt.Errorf("failed to execute POST request to sendMessage: %w", timeoutErr{}), "TimedOut", "Timed out"},
		{errors.New("failed to decode POST request to sendMessage: invalid character"), "NetworkError", "failed to decode POST request to sendMessage: invalid character"},
		{errors.New("TypeError: Cannot edit an inaccessible message"), "TypeError", "Cannot edit an inaccessible message"},
		{errors.New("UnicodeDecodeError: 'utf-16-le' codec can't decode"), "UnicodeDecodeError", "'utf-16-le' codec can't decode"},
		{errors.New("not python: x"), "Exception", "not python: x"},
		{errors.New("Bad-Error: x"), "Exception", "Bad-Error: x"},
		{os.ErrNotExist, "Exception", "file does not exist"},
	}
	for _, tc := range cases {
		got := describe(tc.err)
		if got.class != tc.class || got.msg != tc.msg {
			t.Errorf("describe(%v) = %+v, atteso {%s %s}", tc.err, got, tc.class, tc.msg)
		}
	}
}

// TestIsInstance verifica la gerarchia delle eccezioni.
func TestIsInstance(t *testing.T) {
	yes := [][2]string{
		{"TimedOut", "NetworkError"}, {"TimedOut", "TelegramError"}, {"BadRequest", "NetworkError"},
		{"Forbidden", "TelegramError"}, {"RetryAfter", "TelegramError"}, {"ConnectionResetError", "OSError"},
		{"TimeoutError", "OSError"}, {"KeyError", "LookupError"}, {"JSONDecodeError", "ValueError"},
		{"Whatever", "Exception"}, {"ValueError", "ValueError"},
	}
	no := [][2]string{
		{"Forbidden", "NetworkError"}, {"RetryAfter", "NetworkError"}, {"ValueError", "OSError"},
		{"OSError", "ConnectionError"}, {"Whatever", "ValueError"},
	}
	for _, p := range yes {
		if !isInstance(p[0], p[1]) {
			t.Errorf("isInstance(%s, %s) = false", p[0], p[1])
		}
	}
	for _, p := range no {
		if isInstance(p[0], p[1]) {
			t.Errorf("isInstance(%s, %s) = true", p[0], p[1])
		}
	}
}

// TestExceptionLine verifica l'ultima riga del traceback per gli errori fatali.
func TestExceptionLine(t *testing.T) {
	if got := ExceptionLine(valueError("WEBHOOK_URL richiesto")); got != "ValueError: WEBHOOK_URL richiesto" {
		t.Errorf("ExceptionLine = %q", got)
	}
	if got := ExceptionLine(pycompat.NewError("KeyboardInterrupt", "")); got != "KeyboardInterrupt" {
		t.Errorf("ExceptionLine = %q", got)
	}
}

// TestPanicError verifica la conversione dei valori di panic.
func TestPanicError(t *testing.T) {
	base := errors.New("x")
	if panicError(base) != base {
		t.Error("un error deve restare invariato")
	}
	if panicError("ValueError: y").Error() != "ValueError: y" {
		t.Error("stringa non conservata")
	}
	if panicError(3).Error() != "3" {
		t.Error("valore generico non convertito")
	}
}
