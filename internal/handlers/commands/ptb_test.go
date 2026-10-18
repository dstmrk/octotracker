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

// Edge case non coperti dai test Python: semantica di CommandHandler e
// Message.reply_text di PTB, eccezioni (AttributeError) e ordine degli
// effetti collaterali rispetto agli errori di invio.

func msgUpdate(text string, entities []gotgbot.MessageEntity) *gotgbot.Update {
	u := testutil.MessageUpdate(testutil.DefaultUserID, text)
	if entities != nil {
		u.Message.Entities = entities
	}
	return u
}

func TestCommandHandlerCheckUpdate(t *testing.T) {
	b, _ := testutil.NewBot(t)
	h := NewCommandHandler("status", func(*gotgbot.Bot, *ext.Context) error { return nil })
	cmd := func(n int64) []gotgbot.MessageEntity {
		return []gotgbot.MessageEntity{{Type: "bot_command", Offset: 0, Length: n}}
	}

	cases := []struct {
		name   string
		update *gotgbot.Update
		want   bool
	}{
		{"semplice", msgUpdate("/status", nil), true},
		{"con argomenti", msgUpdate("/status ciao", nil), true},
		{"maiuscole", msgUpdate("/STATUS", nil), true},
		{"username del bot", msgUpdate("/status@"+testutil.BotUsername, nil), true},
		{"username maiuscolo", msgUpdate("/status@OCTOTRACKER_TEST_BOT", nil), true},
		{"altro bot", msgUpdate("/status@altro_bot", nil), false},
		{"altro comando", msgUpdate("/statusx", nil), false},
		{"prefisso", msgUpdate("/stat", nil), false},
		// L'entità decide la lunghezza: "/status!" con entità di 7 → status
		{"entità corta", msgUpdate("/status!", cmd(7)), true},
		{"entità oltre il testo", msgUpdate("/status", cmd(50)), true},
		{"entità vuota", msgUpdate("/status", cmd(1)), false},
		{"entità non in testa", msgUpdate("x /status", []gotgbot.MessageEntity{{Type: "bot_command", Offset: 2, Length: 7}}), false},
		{"prima entità non comando", msgUpdate("/status", []gotgbot.MessageEntity{{Type: "bold", Offset: 0, Length: 7}, {Type: "bot_command", Offset: 0, Length: 7}}), false},
		{"senza entità", msgUpdate("/status", []gotgbot.MessageEntity{}), false},
		// str.lower() di Python: "İ" diventa "i + U+0307", non "i"
		{"I con punto", msgUpdate("/statİs", nil), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := h.CheckUpdate(b, ext.NewContext(b, c.update, nil)); got != c.want {
				t.Errorf("CheckUpdate = %v, want %v", got, c.want)
			}
		})
	}

	// Messaggi modificati sì (filters.UpdateType.MESSAGES), post dei canali,
	// messaggi business e callback no.
	edited := testutil.MessageUpdate(testutil.DefaultUserID, "/status")
	edited.EditedMessage, edited.Message = edited.Message, nil
	if !h.CheckUpdate(b, ext.NewContext(b, edited, nil)) {
		t.Error("edited_message deve essere accettato")
	}
	channel := testutil.MessageUpdate(testutil.DefaultUserID, "/status")
	channel.ChannelPost, channel.Message = channel.Message, nil
	business := testutil.MessageUpdate(testutil.DefaultUserID, "/status")
	business.BusinessMessage, business.Message = business.Message, nil
	callback := testutil.CallbackUpdate(testutil.DefaultUserID, "x")
	for name, u := range map[string]*gotgbot.Update{"channel": channel, "business": business, "callback": callback} {
		if h.CheckUpdate(b, ext.NewContext(b, u, nil)) {
			t.Errorf("%s non deve essere accettato", name)
		}
	}

	// Caption con comando: CommandHandler guarda solo il testo
	caption := testutil.MessageUpdate(testutil.DefaultUserID, "")
	caption.Message.Caption = "/status"
	caption.Message.CaptionEntities = cmd(7)
	if h.CheckUpdate(b, ext.NewContext(b, caption, nil)) {
		t.Error("la caption non deve essere accettata")
	}

	if !strings.HasPrefix(h.Name(), "ptb_command_status_") {
		t.Errorf("Name = %q", h.Name())
	}
	called := false
	h2 := NewCommandHandler("Help", func(*gotgbot.Bot, *ext.Context) error { called = true; return nil })
	ctx := testutil.MessageContext(b, testutil.DefaultUserID, "/help")
	if !h2.CheckUpdate(b, ctx) {
		t.Fatal("il comando registrato in maiuscolo va confrontato in minuscolo")
	}
	if err := h2.HandleUpdate(b, ctx); err != nil || !called {
		t.Fatalf("HandleUpdate: %v %v", err, called)
	}
}

func TestPyLower(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"STATUS":       "status",
		"İ":            "i\u0307",
		"aİbİc":        "ai\u0307bi\u0307c",
		"KELVİN\u212a": "kelvi\u0307nk",
	}
	for in, want := range cases {
		if got := pyLower(in); got != want {
			t.Errorf("pyLower(%q) = %q, want %q", in, got, want)
		}
	}
}

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

func TestReplyTextTopicAndBusiness(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	u := testutil.MessageUpdate(testutil.DefaultUserID, "/unknown")
	u.Message.IsTopicMessage = true
	u.Message.MessageThreadId = 77
	u.Message.BusinessConnectionId = "bc-1"
	u.Message.DirectMessagesTopic = &gotgbot.DirectMessagesTopic{TopicId: 5}
	res := call(t, fc, e.h.UserData, e.h.UnknownCommand, b, ext.NewContext(b, u, nil))
	c := onlySend(t, res)
	if c.String("message_thread_id") != "77" || c.String("business_connection_id") != "bc-1" || c.String("direct_messages_topic_id") != "5" {
		t.Errorf("parametri inattesi: %+v", c.Params)
	}
	if _, ok := c.Params["parse_mode"]; ok {
		t.Error("unknown_command non imposta parse_mode")
	}

	// thread id solo per i messaggi in un topic (is_topic_message)
	u.Message.IsTopicMessage = false
	res = call(t, fc, e.h.UserData, e.h.UnknownCommand, b, ext.NewContext(b, u, nil))
	if _, ok := onlySend(t, res).Params["message_thread_id"]; ok {
		t.Error("message_thread_id senza topic")
	}
}

// editedContext costruisce un update edited_message (update.message None).
func editedContext(b *gotgbot.Bot, text string) *ext.Context {
	u := testutil.MessageUpdate(testutil.DefaultUserID, text)
	u.EditedMessage, u.Message = u.Message, nil
	return ext.NewContext(b, u, nil)
}

func assertAttributeError(t *testing.T, res result, attr string) {
	t.Helper()
	want := "AttributeError: 'NoneType' object has no attribute '" + attr + "'"
	if res.err == nil || res.err.Error() != want {
		t.Fatalf("atteso %q, got err=%v state=%q", want, res.err, res.state)
	}
	if len(res.calls) != 0 {
		t.Errorf("nessuna chiamata attesa: %+v", res.calls)
	}
}

// Un comando modificato (edited_message) arriva all'handler ma
// update.message è None: Python esegue gli effetti precedenti alla
// risposta (pulizia user_data, rimozione utente) e poi solleva
// AttributeError su reply_text.
func TestEditedMessageAttributeError(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	e.save(luceFissaMono(0.145, 72))
	e.h.WebAppURL = "https://example.com/app/"

	for name, h := range map[string]handlers.Response{
		"status": e.h.Status, "history": e.h.HistoryCommand, "help": e.h.HelpCommand, "unknown": e.h.UnknownCommand,
	} {
		e.h.UserData.Set(testutil.DefaultUserID, "k", "v")
		res := call(t, fc, e.h.UserData, h, b, editedContext(b, "/"+name))
		assertAttributeError(t, res, "reply_text")
		if name != "unknown" && len(res.userAfter) != 0 {
			t.Errorf("%s: user_data va pulito prima dell'eccezione: %v", name, res.userAfter)
		}
	}

	// /cancel: reply_text viene prima di user_data.clear()
	e.h.UserData.Set(testutil.DefaultUserID, "k", "v")
	res := call(t, fc, e.h.UserData, e.h.CancelConversation, b, editedContext(b, "/cancel"))
	assertAttributeError(t, res, "reply_text")
	if len(res.userAfter) != 1 {
		t.Errorf("cancel non deve pulire user_data se reply_text fallisce: %v", res.userAfter)
	}

	// /remove: l'utente viene rimosso prima dell'eccezione
	res = call(t, fc, e.h.UserData, e.h.RemoveData, b, editedContext(b, "/remove"))
	assertAttributeError(t, res, "reply_text")
	if e.s.UserExists(testUserID) {
		t.Error("remove_data rimuove l'utente prima di reply_text")
	}
}

func TestNoEffectiveUser(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	u := testutil.MessageUpdate(testutil.DefaultUserID, "/x")
	u.Message.From = nil
	for name, c := range map[string]struct {
		h    handlers.Response
		attr string
	}{
		"status":  {e.h.Status, "id"},
		"history": {e.h.HistoryCommand, "id"},
		"remove":  {e.h.RemoveData, "id"},
		"help":    {e.h.HelpCommand, "clear"},
	} {
		t.Run(name, func(t *testing.T) {
			assertAttributeError(t, call(t, fc, e.h.UserData, c.h, b, ext.NewContext(b, u, nil)), c.attr)
		})
	}
	// /cancel risponde prima di accedere a user_data
	res := call(t, fc, e.h.UserData, e.h.CancelConversation, b, ext.NewContext(b, u, nil))
	if res.err == nil || !strings.Contains(res.err.Error(), "'clear'") || len(res.calls) != 1 {
		t.Fatalf("cancel senza utente: err=%v calls=%d", res.err, len(res.calls))
	}
}

// Un errore di invio (es. utente che ha bloccato il bot) è un'eccezione:
// prevale sul cambio di stato, come in Python dove reply_text solleva.
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
	// Gli altri casi di history/status/remove (utente assente) propagano allo stesso modo
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

// /status con dati incoerenti (bioraria senza F2) solleva TypeError come
// format_luce_consumption: nessun messaggio inviato, user_data già pulito.
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
	if !strings.HasPrefix(res.panicked, "TypeError: ") || len(res.calls) != 0 || len(res.userAfter) != 0 {
		t.Fatalf("got panic=%q calls=%d ud=%v", res.panicked, len(res.calls), res.userAfter)
	}
}

// /history logga l'utente (log identico a Python).
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
