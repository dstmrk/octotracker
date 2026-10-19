package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// Edge case non coperti dai test Python: Standalone, update senza le parti
// attese e ordine degli effetti collaterali rispetto agli errori di invio.

func TestStandalone(t *testing.T) {
	b, _ := testutil.NewBot(t)
	ctx := testutil.MessageContext(b, testutil.DefaultUserID, "/x")
	boom := errors.New("boom")
	for _, c := range []struct {
		in, want error
	}{
		{nil, nil},
		{handlers.EndConversation(), nil},
		{handlers.NextConversationState("X"), nil},
		{boom, boom},
	} {
		in := c.in
		got := Standalone(func(*gotgbot.Bot, *ext.Context) error { return in })(b, ctx)
		if !errors.Is(got, c.want) {
			t.Errorf("Standalone(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// editedContext costruisce un update edited_message (ctx.Message nil).
func editedContext(b *gotgbot.Bot, text string) *ext.Context {
	u := testutil.MessageUpdate(testutil.DefaultUserID, text)
	u.EditedMessage, u.Message = u.Message, nil
	return ext.NewContext(b, u, nil)
}

// Un comando modificato arriva all'handler senza messaggio a cui
// rispondere: nessuna risposta e un errore, dopo gli effetti precedenti
// alla risposta (pulizia user_data, rimozione utente).
func TestEditedMessageReturnsError(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	e.save(luceFissaMono(0.145, 72))
	e.h.WebAppURL = "https://example.com/app/"

	for name, h := range map[string]handlers.Response{
		"status": e.h.Status, "history": e.h.HistoryCommand, "help": e.h.HelpCommand,
		"cancel": e.h.CancelConversation, "unknown": e.h.UnknownCommand, "remove": e.h.RemoveData,
	} {
		e.h.UserData.Set(testutil.DefaultUserID, "k", "v")
		res := call(t, fc, e.h.UserData, h, b, editedContext(b, "/"+name))
		if res.err == nil || len(res.calls) != 0 {
			t.Errorf("%s: err=%v calls=%d", name, res.err, len(res.calls))
		}
		if (len(res.userAfter) == 0) != (name != "unknown" && name != "cancel") {
			t.Errorf("%s: user_data dopo l'errore: %v", name, res.userAfter)
		}
	}
	if e.s.UserExists(testUserID) {
		t.Error("remove_data rimuove l'utente prima di rispondere")
	}
}

func TestNoEffectiveUser(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	u := testutil.MessageUpdate(testutil.DefaultUserID, "/x")
	u.Message.From = nil
	for name, h := range map[string]handlers.Response{
		"status": e.h.Status, "history": e.h.HistoryCommand, "remove": e.h.RemoveData, "help": e.h.HelpCommand,
	} {
		if res := call(t, fc, e.h.UserData, h, b, ext.NewContext(b, u, nil)); res.err == nil || len(res.calls) != 0 {
			t.Errorf("%s: err=%v calls=%d", name, res.err, len(res.calls))
		}
	}
	// /cancel risponde prima di pulire i dati di conversazione
	res := call(t, fc, e.h.UserData, e.h.CancelConversation, b, ext.NewContext(b, u, nil))
	if res.err == nil || len(res.calls) != 1 {
		t.Fatalf("cancel senza utente: err=%v calls=%d", res.err, len(res.calls))
	}
}

// Un errore di invio (es. utente che ha bloccato il bot) prevale sul cambio
// di stato.
func TestSendErrorPropagates(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	sendErr := testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user")
	fc.FailMethod("sendMessage", sendErr)
	e.save(luceFissaMono(0.145, 72))
	e.h.WebAppURL = "https://example.com/app/"
	for name, h := range map[string]handlers.Response{
		"status": e.h.Status, "history": e.h.HistoryCommand, "remove": e.h.RemoveData,
		"help": e.h.HelpCommand, "cancel": e.h.CancelConversation, "unknown": e.h.UnknownCommand,
	} {
		e.h.UserData.Set(testutil.DefaultUserID, "k", "v")
		res := call(t, fc, e.h.UserData, h, b, testutil.MessageContext(b, testutil.DefaultUserID, "/"+name))
		if !errors.Is(res.err, sendErr) || res.state != "" {
			t.Errorf("%s: got err=%v state=%q", name, res.err, res.state)
		}
		if name == "cancel" && len(res.userAfter) != 1 {
			t.Errorf("cancel: user_data pulito nonostante l'errore di invio")
		}
	}
	// Gli altri rami di history/status/remove (utente assente) propagano allo stesso modo
	e.s.RemoveUser(testUserID)
	for name, h := range map[string]handlers.Response{"status": e.h.Status, "history": e.h.HistoryCommand, "remove": e.h.RemoveData} {
		res := call(t, fc, e.h.UserData, h, b, testutil.MessageContext(b, testutil.DefaultUserID, "/"+name))
		if !errors.Is(res.err, sendErr) {
			t.Errorf("%s (non registrato): got %v", name, res.err)
		}
	}
	e.save(luceFissaMono(0.145, 72))
	e.h.WebAppURL = ""
	if res := call(t, fc, e.h.UserData, e.h.HistoryCommand, b, testutil.MessageContext(b, testutil.DefaultUserID, "/history")); !errors.Is(res.err, sendErr) {
		t.Errorf("history senza Mini App: got %v", res.err)
	}
}

// /status con dati incoerenti (bioraria senza F2): il formatter va in
// panic, nessun messaggio inviato, user_data già pulito.
func TestStatusInconsistentConsumptionPanics(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	e.save(func() database.User {
		u := luceFissaMono(0.1, 1)
		u.Luce.Fascia = "bioraria"
		u.Luce.ConsumoF1 = fp(10)
		return u
	}())
	e.h.UserData.Set(testutil.DefaultUserID, "k", "v")
	res := call(t, fc, e.h.UserData, e.h.Status, b, testutil.MessageContext(b, testutil.DefaultUserID, "/status"))
	if res.panicked == "" || len(res.calls) != 0 || len(res.userAfter) != 0 {
		t.Fatalf("got panic=%q calls=%d ud=%v", res.panicked, len(res.calls), res.userAfter)
	}
}

// /history logga l'utente.
func TestHistoryLogs(t *testing.T) {
	logs := captureLogs(t)
	e := newEnv(t)
	e.command(historyCommand, "/history")
	if !strings.Contains(logs.String(), "- handlers.commands - INFO - User 123456789: /history command") {
		t.Errorf("log mancante:\n%s", logs.String())
	}
}

// La configurazione arriva dai campi di Handlers e non dall'ambiente.
func TestHelpUsesHandlerFields(t *testing.T) {
	t.Setenv("TELEGRAM_CHANNEL", "@da_ignorare")
	t.Setenv("CHECKER_HOUR", "3")
	e := newEnv(t)
	e.h.CheckerHour = 7
	e.h.TelegramChannel = "@octo"
	text := onlySend(t, e.command(helpCommand, "/help")).Text()
	assertContains(t, text, "alle 7:00.", `<a href="https://t.me/octo">@octo</a>!`)
	assertNotContains(t, text, "da_ignorare", "alle 3:00")
	if !strings.HasSuffix(text, "!\n\n") {
		t.Errorf("il testo deve terminare con il blocco canale: %q", text[len(text)-20:])
	}
	e.h.TelegramChannel = ""
	text = onlySend(t, e.command(helpCommand, "/help")).Text()
	if !strings.HasSuffix(text, "Octopus Energy.\n\n") {
		t.Errorf("senza canale: %q", text[len(text)-30:])
	}
}
