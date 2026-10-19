package rateupdate

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/handlers/commands"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Test della conversazione completa (ConversationHandler di bot.py, con il
// fallback /cancel del package commands come la cablerà l'applicazione),
// dell'handler autonomo di "No grazie" e degli edge case non coperti dai
// test Python.

type convo struct {
	t    *testing.T
	b    *gotgbot.Bot
	fc   *testutil.FakeClient
	conv handlers.Conversation
}

func newConvo(t *testing.T, h *Handlers) *convo {
	b, fc := testutil.NewBot(t)
	cmd := &commands.Handlers{UserData: h.UserData}
	fallbacks := []ext.Handler{tg.NewCommand("cancel", cmd.CancelConversation)}
	return &convo{t: t, b: b, fc: fc, conv: h.Conversation(fallbacks)}
}

func (c *convo) feed(u *gotgbot.Update) (bool, error) {
	c.t.Helper()
	c.fc.Reset()
	ctx := ext.NewContext(c.b, u, nil)
	if !c.conv.CheckUpdate(c.b, ctx) {
		return false, nil
	}
	return true, c.conv.HandleUpdate(c.b, ctx)
}

func (c *convo) text(s string) (bool, error) {
	return c.feed(testutil.MessageUpdate(testutil.DefaultUserID, s))
}

func (c *convo) button(data string) (bool, error) {
	return c.feed(callbackUpdate(data, notifica()))
}

func (c *convo) mustHandle(handled bool, err error) {
	c.t.Helper()
	if !handled || err != nil {
		c.t.Fatalf("update non gestito: handled=%v err=%v", handled, err)
	}
}

func (c *convo) mustIgnore(handled bool, err error) {
	c.t.Helper()
	if handled {
		c.t.Fatalf("update gestito inaspettatamente (err=%v)", err)
	}
}

func setupFissaLuceGas(t *testing.T, e *env) {
	t.Helper()
	e.save(sampleUserDataWithGas())
	e.s.SavePendingRates(testUserID, database.PendingRates{
		Luce:            database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.13, Commercializzazione: 65},
		Gas:             &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.42, Commercializzazione: 80},
		UpdatedServices: []string{"luce", "gas"},
	})
}

func TestConversationFullFlow(t *testing.T) {
	e := newEnv(t)
	setupFissaLuceGas(t, e)
	c := newConvo(t, e.h)

	c.mustIgnore(c.button(CallbackScadenzaYes)) // nessuna conversazione attiva
	c.mustIgnore(c.button(CallbackNo))          // "No grazie" è fuori dalla conversazione
	c.mustHandle(c.button(CallbackYes))
	if kb := c.fc.LastCall(t, "sendMessage").InlineKeyboard(); len(kb) != 1 || kb[0][0].CallbackData != CallbackScadenzaYes || kb[0][1].CallbackData != CallbackScadenzaNo {
		t.Fatalf("tastiera Sì/No: %+v", kb)
	}

	// ASK_SCADENZA: niente re-entry, niente testo
	c.mustIgnore(c.button(CallbackYes))
	c.mustIgnore(c.text("15/03/2025"))
	c.mustIgnore(c.button(CallbackScadenzaSkip))
	c.mustHandle(c.button(CallbackScadenzaYes))

	// UPDATE_DATE: i comandi non sono date; data non valida resta nello stato
	c.mustIgnore(c.text("/status"))
	c.mustHandle(c.text("non una data"))
	if got := c.fc.LastCall(t, "sendMessage").Text(); got != MsgDateInvalid {
		t.Errorf("data non valida: %q", got)
	}
	c.mustHandle(c.text("15/03/2025"))
	if got := e.s.LoadUser(testUserID).Luce.Scadenza; got != "2026-03-15" {
		t.Errorf("scadenza luce = %q", got)
	}
	c.mustHandle(c.button(CallbackScadenzaSkip)) // salta il gas
	c.mustIgnore(c.text("01/06/2025"))           // conversazione terminata
	if got := e.s.LoadUser(testUserID).Gas.Scadenza; got != "" {
		t.Errorf("scadenza gas = %q", got)
	}
}

func TestConversationCancelFallback(t *testing.T) {
	e := newEnv(t)
	setupFissaLuceGas(t, e)
	c := newConvo(t, e.h)
	c.mustHandle(c.button(CallbackYes))
	c.mustHandle(c.button(CallbackScadenzaYes))
	c.mustHandle(c.text("/cancel"))
	assertContains(t, c.fc.LastCall(t, "sendMessage").Text(), "Registrazione annullata")
	if _, ok := e.h.UserData.Get(testutil.DefaultUserID, servicesKey); ok {
		t.Error("cancel_conversation pulisce user_data")
	}
	c.mustIgnore(c.text("15/03/2025"))
}

// Senza offerte fisse aggiornate la conversazione non parte.
func TestConversationNotStartedForVariabile(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	e.s.SavePendingRates(testUserID, database.PendingRates{Luce: database.Utility{Tipo: "variabile", Fascia: "monoraria"}, UpdatedServices: []string{"luce"}})
	c := newConvo(t, e.h)
	c.mustHandle(c.button(CallbackYes))
	c.mustIgnore(c.button(CallbackScadenzaYes))
}

// I pattern accettano solo le callback_data esatte dei pulsanti.
func TestConversationPatterns(t *testing.T) {
	e := newEnv(t)
	setupFissaLuceGas(t, e)
	c := newConvo(t, e.h)
	c.mustIgnore(c.button(CallbackYes + "x"))
	c.mustIgnore(c.button(CallbackYes + "\n"))
	c.mustHandle(c.button(CallbackYes))
	c.mustIgnore(c.button("scadenza_update_maybe"))
	c.mustIgnore(c.button(CallbackScadenzaNo + "\n"))
	c.mustHandle(c.button(CallbackScadenzaYes))
	c.mustIgnore(c.button(CallbackScadenzaSkip + "\n"))
	c.mustHandle(c.button(CallbackScadenzaSkip))
}

func TestRateUpdateNoHandler(t *testing.T) {
	e := newEnv(t)
	b, _ := testutil.NewBot(t)
	h := e.h.RateUpdateNoHandler()
	for data, want := range map[string]bool{CallbackNo: true, CallbackNo + "\n": false, CallbackNo + "x": false, CallbackYes: false} {
		if got := h.CheckUpdate(b, ext.NewContext(b, callbackUpdate(data, notifica()), nil)); got != want {
			t.Errorf("CheckUpdate(%q) = %v, want %v", data, got, want)
		}
	}
	e.save(sampleUserData())
	e.s.SavePendingRates(testUserID, samplePendingRates())
	if err := h.HandleUpdate(b, ext.NewContext(b, callbackUpdate(CallbackNo, notifica()), nil)); err != nil {
		t.Fatal(err)
	}
	if e.s.LoadPendingRates(testUserID) != nil {
		t.Error("pending non rimosse")
	}
}

// Un messaggio modificato in UPDATE_DATE arriva all'handler senza
// messaggio: resta in UPDATE_DATE senza rispondere.
func TestConversationEditedMessageStays(t *testing.T) {
	e := newEnv(t)
	setupFissaLuceGas(t, e)
	c := newConvo(t, e.h)
	c.mustHandle(c.button(CallbackYes))
	c.mustHandle(c.button(CallbackScadenzaYes))
	edited := testutil.MessageUpdate(testutil.DefaultUserID, "15/03/2025")
	edited.EditedMessage, edited.Message = edited.Message, nil
	c.mustHandle(c.feed(edited))
	if len(c.fc.Calls()) != 0 {
		t.Errorf("nessuna chiamata attesa: %+v", c.fc.Calls())
	}
	c.mustHandle(c.text("15/03/2025")) // ancora in UPDATE_DATE
}

// Le callback da messaggi inline (senza message) sono ignorate dalla
// conversazione, che è per utente e chat.
func TestConversationIgnoresInlineCallbacks(t *testing.T) {
	e := newEnv(t)
	c := newConvo(t, e.h)
	u := testutil.CallbackUpdate(testutil.DefaultUserID, CallbackYes)
	u.CallbackQuery.Message = nil
	u.CallbackQuery.InlineMessageId = "inline-1"
	c.mustIgnore(c.feed(u))
}

// "No grazie" da un messaggio inline: risponde, rimuove le pending e
// restituisce un errore perché non c'è il testo della notifica.
func TestRateUpdateNoInlineMessage(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	e.s.SavePendingRates(testUserID, samplePendingRates())
	b, fc := testutil.NewBot(t)
	u := testutil.CallbackUpdate(testutil.DefaultUserID, CallbackNo)
	u.CallbackQuery.Message = nil
	u.CallbackQuery.InlineMessageId = "inline-1"
	res := call(t, fc, e.h.UserData, e.h.RateUpdateNo, b, ext.NewContext(b, u, nil))
	if !errors.Is(res.err, errNoText) {
		t.Fatalf("err = %v", res.err)
	}
	onlyCall(t, res, "answerCallbackQuery")
	if e.s.LoadPendingRates(testUserID) != nil {
		t.Error("pending non rimosse prima dell'eccezione")
	}
}

// Messaggio inaccessibile (date 0, senza testo): dopo l'applicazione delle
// tariffe manca il testo da modificare e l'handler restituisce un errore.
func TestRateUpdateYesInaccessibleMessage(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	e.s.SavePendingRates(testUserID, samplePendingRates())
	b, fc := testutil.NewBot(t)
	u := testutil.CallbackUpdate(testutil.DefaultUserID, CallbackYes)
	u.CallbackQuery.Message = gotgbot.InaccessibleMessage{Chat: gotgbot.Chat{Id: testutil.DefaultUserID, Type: "private"}, MessageId: 42}
	res := call(t, fc, e.h.UserData, e.h.RateUpdateYes, b, ext.NewContext(b, u, nil))
	if !errors.Is(res.err, errNoText) {
		t.Fatalf("err = %v", res.err)
	}
	if u := e.s.LoadUser(testUserID); u.Luce.Energia != 0.130 {
		t.Error("le tariffe vanno applicate prima dell'eccezione")
	}
}

var answerErr = testutil.TelegramError("answerCallbackQuery", 400, "Bad Request: query is too old and response timeout expired or query ID is invalid")

// "Aggiorna tariffe" e "No grazie" non proteggono answer: anche la query
// scaduta interrompe l'handler prima di toccare le pending.
func TestAnswerErrorsPropagate(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	e.s.SavePendingRates(testUserID, samplePendingRates())
	for _, h := range []func(*Handlers) handlers.Response{rateUpdateYes, rateUpdateNo} {
		b, fc := testutil.NewBot(t)
		fc.FailMethod("answerCallbackQuery", answerErr)
		res := call(t, fc, e.h.UserData, h(e.h), b, ext.NewContext(b, callbackUpdate(CallbackYes, notifica()), nil))
		if !errors.Is(res.err, answerErr) || len(res.calls) != 1 {
			t.Errorf("err=%v calls=%d", res.err, len(res.calls))
		}
	}
	if e.s.LoadPendingRates(testUserID) == nil {
		t.Error("le pending non devono essere toccate se answer fallisce")
	}
}

// Nella conversazione la query scaduta è ignorata, gli altri errori no.
func TestExpiredQueryIgnoredInConversation(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
	b, fc := testutil.NewBot(t)
	fc.FailMethod("answerCallbackQuery", answerErr)
	res := call(t, fc, e.h.UserData, e.h.ScadenzaUpdateChoice, b, ext.NewContext(b, callbackUpdate(CallbackScadenzaYes, notifica()), nil))
	if res.err != nil || res.state != StateUpdateDate {
		t.Errorf("choice: %v %q", res.err, res.state)
	}
	res = call(t, fc, e.h.UserData, e.h.SkipScadenzaDate, b, ext.NewContext(b, callbackUpdate(CallbackScadenzaSkip, notifica()), nil))
	if res.err != nil || res.state != "END" {
		t.Errorf("skip: %v %q", res.err, res.state)
	}
	other := testutil.TelegramError("answerCallbackQuery", 400, "Bad Request: altro")
	fc.FailMethod("answerCallbackQuery", other)
	for _, h := range []handlers.Response{e.h.ScadenzaUpdateChoice, e.h.SkipScadenzaDate} {
		if res := call(t, fc, e.h.UserData, h, b, ext.NewContext(b, callbackUpdate(CallbackScadenzaYes, notifica()), nil)); !errors.Is(res.err, other) {
			t.Errorf("altro errore: %v", res.err)
		}
	}
}

var (
	editErr = testutil.TelegramError("editMessageText", 400, "Bad Request: message is not modified")
	sendErr = testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user")
)

// Un errore di modifica o di invio interrompe gli handler delle callback
// senza cambiare stato.
func TestCallbackSendErrorsPropagate(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		err     error
		handler func(*Handlers) handlers.Response
		data    string
		setup   func(e *env)
	}{
		{"yes edit", "editMessageText", editErr, rateUpdateYes, CallbackYes, func(e *env) { setupFissaLuceGas(t, e) }},
		{"yes reply", "sendMessage", sendErr, rateUpdateYes, CallbackYes, func(e *env) { setupFissaLuceGas(t, e) }},
		{"no edit", "editMessageText", editErr, rateUpdateNo, CallbackNo, func(*env) {}},
		{"choice no", "editMessageText", editErr, scadenzaUpdateChoice, CallbackScadenzaNo, func(*env) {}},
		{"choice yes", "editMessageText", editErr, scadenzaUpdateChoice, CallbackScadenzaYes, func(e *env) {
			e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
		}},
		{"skip edit", "editMessageText", editErr, skipScadenzaDate, CallbackScadenzaSkip, func(*env) {}},
		{"skip next", "sendMessage", sendErr, skipScadenzaDate, CallbackScadenzaSkip, func(e *env) {
			e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce", "gas"})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			c.setup(e)
			b, fc := testutil.NewBot(t)
			fc.FailMethod(c.method, c.err)
			res := call(t, fc, e.h.UserData, c.handler(e.h), b, ext.NewContext(b, callbackUpdate(c.data, notifica()), nil))
			if !errors.Is(res.err, c.err) || res.state != "" {
				t.Errorf("err=%v state=%q", res.err, res.state)
			}
		})
	}
}

// Un errore di invio interrompe UpdateScadenzaDate in ogni ramo, anche sul
// messaggio finale (ScadenzaDoneText) dopo la conferma.
func TestDateSendErrorsPropagate(t *testing.T) {
	for name, text := range map[string]string{"data non valida": "boh", "conferma data": "15/03/2025"} {
		e := newEnv(t)
		e.save(sampleUserData())
		e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
		b, fc := testutil.NewBot(t)
		fc.FailMethod("sendMessage", sendErr)
		if res := call(t, fc, e.h.UserData, e.h.UpdateScadenzaDate, b, testutil.MessageContext(b, testutil.DefaultUserID, text)); !errors.Is(res.err, sendErr) {
			t.Errorf("%s: err=%v", name, res.err)
		}
	}
	e := newEnv(t)
	e.save(sampleUserData())
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
	b, fc := testutil.NewBot(t)
	fc.SetResponder(func(method string, params map[string]any) (json.RawMessage, error) {
		if method == "sendMessage" && params["text"] == ScadenzaDoneText {
			return nil, sendErr
		}
		return nil, nil
	})
	if res := call(t, fc, e.h.UserData, e.h.UpdateScadenzaDate, b, testutil.MessageContext(b, testutil.DefaultUserID, "15/03/2025")); !errors.Is(res.err, sendErr) {
		t.Errorf("done: err=%v", res.err)
	}
}

func TestMissingUpdateFields(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	msgCtx := testutil.MessageContext(b, testutil.DefaultUserID, "x")
	for name, h := range map[string]handlers.Response{
		"yes": e.h.RateUpdateYes, "no": e.h.RateUpdateNo, "choice": e.h.ScadenzaUpdateChoice, "skip": e.h.SkipScadenzaDate,
	} {
		if res := call(t, fc, e.h.UserData, h, b, msgCtx); !errors.Is(res.err, tg.ErrNoCallback) {
			t.Errorf("%s senza callback: %v", name, res.err)
		}
	}

	u := testutil.MessageUpdate(testutil.DefaultUserID, "15/03/2025")
	u.Message.From = nil
	if res := call(t, fc, e.h.UserData, e.h.UpdateScadenzaDate, b, ext.NewContext(b, u, nil)); !errors.Is(res.err, tg.ErrNoUser) {
		t.Errorf("date senza utente: %v", res.err)
	}

	// Servizio successivo da una callback inline: nessun messaggio a cui rispondere
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce", "gas"})
	inline := testutil.CallbackUpdate(testutil.DefaultUserID, CallbackScadenzaSkip)
	inline.CallbackQuery.Message = nil
	inline.CallbackQuery.InlineMessageId = "inline"
	if res := call(t, fc, e.h.UserData, e.h.SkipScadenzaDate, b, ext.NewContext(b, inline, nil)); !errors.Is(res.err, tg.ErrNoMessage) {
		t.Errorf("skip inline: %v", res.err)
	}
}

func TestLogs(t *testing.T) {
	logs := captureLogs(t)
	e := newEnv(t)
	e.click(rateUpdateNo, CallbackNo, notifica())
	assertContains(t, logs.String(), "- handlers.rate_update - INFO - ℹ️ Utente 123456789: ha rifiutato aggiornamento tariffe via bottone")
	logs.Reset()
	e.click(rateUpdateYes, CallbackYes, notifica())
	assertContains(t, logs.String(), "- handlers.rate_update - WARNING - ⚠️ Utente 123456789: nessuna tariffa pendente trovata")
	logs.Reset()
	e.h.Store = &fakeStore{Store: e.s, applyReason: database.ApplyValidationError}
	e.click(rateUpdateYes, CallbackYes, notifica())
	assertContains(t, logs.String(), "- handlers.rate_update - ERROR - ❌ Utente 123456789: errore aggiornamento tariffe (validation_error)")
}

// Divergenza nota (internal/database): con pending_rates non JSON Python
// solleva JSONDecodeError in load_pending_rates (prima di applicare);
// in Go LoadPendingRates restituisce nil e ApplyPendingRates
// validation_error, quindi il messaggio mostra l'errore di aggiornamento.
func TestMalformedPendingJSON(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	if _, err := rawDB(t, e.s).Exec("UPDATE users SET pending_rates = '{non json' WHERE user_id = ?", testUserID); err != nil {
		t.Fatal(err)
	}
	res := e.click(rateUpdateYes, CallbackYes, notifica())
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, onlyCall(t, res, "editMessageText").Text(), updateErrorText)
}

func TestTextHTMLHelpers(t *testing.T) {
	u := []uint16{1, 2, 3, 4}
	for _, c := range []struct {
		start, stop int64
		want        int
	}{{1, 3, 2}, {-2, 2, 2}, {3, 1, 0}, {2, 40, 2}, {40, 50, 0}} {
		if got := sliceUTF16(u, c.start, c.stop); len(got) != c.want {
			t.Errorf("sliceUTF16(%d, %d) = %v", c.start, c.stop, got)
		}
	}
	if _, err := textHTML(nil); !errors.Is(err, errNoText) {
		t.Errorf("nil: %v", err)
	}
	msg := notification("<a> & 'b'", gotgbot.MessageEntity{Type: "text_mention", Offset: 0, Length: 3},
		gotgbot.MessageEntity{Type: "pre", Offset: 6, Length: 3})
	if got, _ := textHTML(&msg); got != "&lt;a&gt; &amp; <pre>&#x27;b&#x27;</pre>" {
		t.Errorf("escape e text_mention senza utente: %q", got)
	}
}

func TestUserDataServicesCopy(t *testing.T) {
	// La coda in user_data non condivide memoria con la slice del chiamante
	e := newEnv(t)
	services := []string{"luce", "gas"}
	e.h.setServices(testutil.DefaultUserID, services)
	services[0] = "modificato"
	if got := e.h.services(testutil.DefaultUserID); got[0] != "luce" {
		t.Errorf("coda condivisa: %v", got)
	}
	// Un valore di tipo inatteso equivale a coda vuota
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, "luce")
	if got := e.h.services(testutil.DefaultUserID); len(got) != 0 {
		t.Errorf("tipo inatteso: %v", got)
	}
}
