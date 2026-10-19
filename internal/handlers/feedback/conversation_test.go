package feedback

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Test della conversazione completa (ConversationHandler di bot.py) e degli
// edge case non coperti dai test Python.

// convo esegue update attraverso la conversazione come farebbe il dispatcher:
// restituisce se la conversazione ha gestito l'update e l'errore non di stato.
type convo struct {
	t    *testing.T
	b    *gotgbot.Bot
	fc   *testutil.FakeClient
	conv handlers.Conversation
}

func newConvo(t *testing.T, h *Handlers) *convo {
	b, fc := testutil.NewBot(t)
	return &convo{t: t, b: b, fc: fc, conv: h.Conversation()}
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
	u := testutil.CallbackUpdate(testutil.DefaultUserID, data)
	u.CallbackQuery.Message = *u.CallbackQuery.Message.(*gotgbot.Message)
	return c.feed(u)
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

func TestConversationFullFlow(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	c := newConvo(t, e.h)

	c.mustIgnore(c.button("rating_4")) // nessuna conversazione attiva
	c.mustIgnore(c.text("/cancel"))    // i fallback valgono solo a conversazione attiva
	c.mustHandle(c.text("/feedback"))
	if kb := c.fc.LastCall(t, "sendMessage").InlineKeyboard(); len(kb) != 2 || len(kb[0]) != 3 || kb[1][1].CallbackData != "rating_5" {
		t.Fatalf("tastiera rating: %+v", kb)
	}

	// Stato RATING: testo, comandi diversi, rating a due cifre e skip non corrispondono
	c.mustIgnore(c.text("ciao"))
	c.mustIgnore(c.text("/feedback")) // niente allow_reentry
	c.mustIgnore(c.button("rating_10"))
	c.mustIgnore(c.button("skip_comment"))
	c.mustHandle(c.button("rating_4"))

	// Stato COMMENT: i comandi non sono commenti
	c.mustIgnore(c.text("/status"))
	c.mustHandle(c.text("  Molto utile  "))
	f := e.s.GetRecentFeedbacks(1)
	if len(f) != 1 || deref(f[0].Rating) != 4 || deref(f[0].Comment) != "Molto utile" {
		t.Fatalf("feedback salvato: %+v", f)
	}
	// Conversazione terminata
	c.mustIgnore(c.text("altro testo"))
}

func TestConversationPatterns(t *testing.T) {
	e := newEnv(t)
	for _, data := range []string{"rating_5\n", "xrating_5", "rating_a", "rating_", "rating_10"} {
		c := newConvo(t, e.h)
		c.mustHandle(c.text("/feedback"))
		c.mustIgnore(c.button(data))
	}
	c := newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	c.mustHandle(c.button("rating_2"))
	c.mustIgnore(c.button("skip_commentx"))
	c.mustHandle(c.button("skip_comment"))
	c.mustIgnore(c.text("dopo la fine"))
}

func TestConversationCancelAndSkip(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)

	c := newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	c.mustHandle(c.text("/cancel"))
	if got := c.fc.LastCall(t, "sendMessage").Text(); got != msgFeedbackCancelled {
		t.Errorf("cancel: %q", got)
	}
	c.mustIgnore(c.button("rating_3"))

	c = newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	c.mustHandle(c.button("rating_3"))
	c.mustHandle(c.text("/CANCEL@" + testutil.BotUsername))
	c.mustIgnore(c.text("commento dopo cancel"))

	c = newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	c.mustHandle(c.button("rating_1"))
	c.mustHandle(c.button("skip_comment"))
	if f := e.s.GetRecentFeedbacks(1); len(f) != 1 || deref(f[0].Rating) != 1 || f[0].Comment != nil {
		t.Fatalf("feedback saltato: %+v", f)
	}
}

// Un messaggio modificato nello stato COMMENT arriva a FeedbackComment
// senza messaggio: errore e stato invariato.
func TestConversationEditedMessageKeepsState(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	c := newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	c.mustHandle(c.button("rating_5"))

	edited := testutil.MessageUpdate(testutil.DefaultUserID, "modificato")
	edited.EditedMessage, edited.Message = edited.Message, nil
	handled, err := c.feed(edited)
	if !handled || !errors.Is(err, tg.ErrNoMessage) {
		t.Fatalf("edited: handled=%v err=%v", handled, err)
	}
	if e.s.GetFeedbackCount() != 0 {
		t.Fatal("nessun feedback atteso")
	}
	// Lo stato resta COMMENT: il commento successivo viene salvato
	c.mustHandle(c.text("ora sì"))
	if n := e.s.GetFeedbackCount(); n != 1 {
		t.Fatalf("count = %d", n)
	}
}

// In cooldown la conversazione non parte.
func TestConversationCooldownDoesNotStart(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	e.s.SaveFeedback(testUserID, "command", ip(5), nil)
	c := newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	assertContains(t, c.fc.LastCall(t, "sendMessage").Text(), "⏰ Hai già inviato un feedback di recente!")
	c.mustIgnore(c.button("rating_4"))
}

// Le callback da messaggi inline (senza message) sono ignorate dalla
// conversazione, che è per utente e chat.
func TestConversationIgnoresInlineCallbacks(t *testing.T) {
	e := newEnv(t)
	c := newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	u := testutil.CallbackUpdate(testutil.DefaultUserID, "rating_4")
	u.CallbackQuery.Message = nil
	u.CallbackQuery.InlineMessageId = "inline-1"
	c.mustIgnore(c.feed(u))
}

// /feedback modificato (edited_message): il comando lo accetta, ma non c'è
// un messaggio a cui rispondere → errore dopo il controllo del cooldown.
func TestFeedbackCommandEditedMessage(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	e.h.UserData.Set(testutil.DefaultUserID, "k", "v")
	res := call(t, fc, e.h.UserData, e.h.FeedbackCommand, b, editedMessageContext(b, "/feedback"))
	if !errors.Is(res.err, tg.ErrNoMessage) || len(res.userAfter) != 0 {
		t.Fatalf("got err=%v ud=%v", res.err, res.userAfter)
	}
	// In cooldown la risposta fallisce senza pulire user_data
	e.h.Store = &fakeStore{last: sp(time.Now().Format("2006-01-02 15:04:05"))}
	e.h.UserData.Set(testutil.DefaultUserID, "k", "v")
	res = call(t, fc, e.h.UserData, e.h.FeedbackCommand, b, editedMessageContext(b, "/feedback"))
	if res.err == nil || len(res.userAfter) != 1 {
		t.Fatalf("cooldown: err=%v ud=%v", res.err, res.userAfter)
	}
}

func TestHandlersWithoutEffectiveUser(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	u := testutil.MessageUpdate(testutil.DefaultUserID, "x")
	u.Message.From = nil
	for name, h := range map[string]handlers.Response{"command": e.h.FeedbackCommand, "comment": e.h.FeedbackComment} {
		if res := call(t, fc, e.h.UserData, h, b, ext.NewContext(b, u, nil)); !errors.Is(res.err, tg.ErrNoUser) {
			t.Errorf("%s: err=%v", name, res.err)
		}
	}
}

func TestCallbackHandlersWithoutCallback(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	msgCtx := testutil.MessageContext(b, testutil.DefaultUserID, "x")
	for _, h := range []handlers.Response{e.h.FeedbackRating, e.h.FeedbackSkipComment} {
		if res := call(t, fc, e.h.UserData, h, b, msgCtx); !errors.Is(res.err, tg.ErrNoCallback) {
			t.Errorf("err=%v", res.err)
		}
	}
}

func TestAnswerCallbackErrors(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)

	// Query scaduta: l'errore è ignorato e il flusso continua
	fc.FailMethod("answerCallbackQuery", testutil.TelegramError("answerCallbackQuery", 400, "Bad Request: query is too old and response timeout expired or query ID is invalid"))
	if res := call(t, fc, e.h.UserData, e.h.FeedbackRating, b, callbackContext(b, "rating_2")); res.state != StateComment {
		t.Errorf("query scaduta: state=%q err=%v", res.state, res.err)
	}
	// Altri errori di answer vengono propagati prima di salvare il voto
	other := testutil.TelegramError("answerCallbackQuery", 400, "Bad Request: something else")
	fc.FailMethod("answerCallbackQuery", other)
	e.h.UserData.Clear(testutil.DefaultUserID)
	res := call(t, fc, e.h.UserData, e.h.FeedbackRating, b, callbackContext(b, "rating_2"))
	if !errors.Is(res.err, other) || len(res.userAfter) != 0 {
		t.Errorf("errore answer: err=%v ud=%v", res.err, res.userAfter)
	}
	if res := call(t, fc, e.h.UserData, e.h.FeedbackSkipComment, b, callbackContext(b, "skip_comment")); !errors.Is(res.err, other) {
		t.Errorf("skip: err=%v", res.err)
	}
}

// Errore di modifica del messaggio: propagato, stato invariato.
func TestEditErrorsPropagate(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	editErr := testutil.TelegramError("editMessageText", 400, "Bad Request: message is not modified")
	fc.FailMethod("editMessageText", editErr)
	for _, h := range []handlers.Response{e.h.FeedbackRating, e.h.FeedbackSkipComment, e.h.FeedbackCancel} {
		if res := call(t, fc, e.h.UserData, h, b, callbackContext(b, "rating_3")); !errors.Is(res.err, editErr) || res.state != "" {
			t.Errorf("edit: err=%v state=%q", res.err, res.state)
		}
	}
}

// Errore di invio: propagato.
func TestSendErrorsPropagate(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	sendErr := testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user")
	fc.FailMethod("sendMessage", sendErr)
	for text, h := range map[string]handlers.Response{"/feedback": e.h.FeedbackCommand, "commento": e.h.FeedbackComment, "/cancel": e.h.FeedbackCancel,
		strings.Repeat("x", 1001): e.h.FeedbackComment} {
		if res := call(t, fc, e.h.UserData, h, b, testutil.MessageContext(b, testutil.DefaultUserID, text)); !errors.Is(res.err, sendErr) {
			t.Errorf("%q: err=%v", text[:min(len(text), 10)], res.err)
		}
	}
	e.s.SaveUser(testUserID, sampleUser())
	e.s.SaveFeedback(testUserID, "command", ip(1), nil)
	if res := call(t, fc, e.h.UserData, e.h.FeedbackCommand, b, testutil.MessageContext(b, testutil.DefaultUserID, "/feedback")); !errors.Is(res.err, sendErr) {
		t.Errorf("cooldown: err=%v", res.err)
	}
}

// Il voto si legge da callback_data come in Python: "rating_5_x" vale 5,
// spazi ammessi; senza numero l'handler restituisce un errore.
func TestFeedbackRatingParsing(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	for data, want := range map[string]int{"rating_5_x": 5, "rating_ 4 ": 4, "rating_-1": -1, "rating_0": 0} {
		res := call(t, fc, e.h.UserData, e.h.FeedbackRating, b, callbackContext(b, data))
		if res.state != StateComment || res.userAfter[keyRating] != want {
			t.Errorf("%q: state=%q err=%v ud=%v", data, res.state, res.err, res.userAfter)
		}
	}
	for _, data := range []string{"rating", "rating_x", "rating_٣"} {
		if res := call(t, fc, e.h.UserData, e.h.FeedbackRating, b, callbackContext(b, data)); res.err == nil {
			t.Errorf("%q: errore atteso, state=%q", data, res.state)
		}
	}
}

// Il cooldown usa l'ora "da muro" dell'orologio, come datetime.now()
// (naive), qualunque sia il fuso del time.Time restituito.
func TestCooldownUsesWallClock(t *testing.T) {
	setNow(t, time.Date(2026, 3, 29, 3, 30, 0, 0, time.FixedZone("CEST", 2*3600)))
	h := &Handlers{Store: &fakeStore{last: sp("2026-03-28 04:30:00")}, UserData: tg.NewUserData()}
	can, msg := h.canGiveFeedback(testUserID)
	if can || !strings.HasSuffix(msg, "Riprova tra circa 1h.") {
		t.Errorf("got %v %q", can, msg)
	}
	h.Store = &fakeStore{last: sp("2026-03-28 03:30:00")}
	if can, _ := h.canGiveFeedback(testUserID); !can {
		t.Error("24h esatte: il cooldown è scaduto")
	}
}

// Nel fuso di produzione (Europe/Rome) un cambio d'ora non sposta la
// finestra di 24h: valori calcolati con _can_give_feedback di Python.
func TestCooldownAcrossDSTChange(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skipf("tzdata non disponibile: %v", err)
	}
	cases := []struct {
		now     time.Time
		last    string
		can     bool
		retryIn string
	}{
		// Fine dell'ora legale: 25/10/2026 alle 03:00 CEST tornano le 02:00 CET
		{time.Date(2026, 10, 25, 11, 30, 0, 0, rome), "2026-10-24 12:00:00", false, "0h"},
		{time.Date(2026, 10, 25, 9, 30, 0, 0, rome), "2026-10-24 12:00:00", false, "2h"},
		// Inizio dell'ora legale: 29/03/2026 alle 02:00 CET diventano le 03:00 CEST
		{time.Date(2026, 3, 29, 12, 30, 0, 0, rome), "2026-03-28 12:00:00", true, ""},
		{time.Date(2026, 3, 29, 10, 30, 0, 0, rome), "2026-03-28 12:00:00", false, "1h"},
	}
	for _, c := range cases {
		setNow(t, c.now)
		h := &Handlers{Store: &fakeStore{last: sp(c.last)}, UserData: tg.NewUserData()}
		can, msg := h.canGiveFeedback(testUserID)
		if can != c.can || (!can && !strings.HasSuffix(msg, "Riprova tra circa "+c.retryIn+".")) {
			t.Errorf("now=%s last=%s: got %v %q", c.now, c.last, can, msg)
		}
	}
}

// Un timestamp che non è nel formato di SQLite non blocca l'utente e
// produce un warning.
func TestCooldownMalformedTimestamp(t *testing.T) {
	logs := captureLogs(t)
	for _, last := range []string{"invalid", "", "2026-10-18T12:00:00", "2026-10-18 24:00:00", " 2026-10-18 10:00:00"} {
		logs.Reset()
		h := &Handlers{Store: &fakeStore{last: sp(last)}, UserData: tg.NewUserData()}
		if can, msg := h.canGiveFeedback(testUserID); !can || msg != "" {
			t.Errorf("%q: got %v %q", last, can, msg)
		}
		assertContains(t, logs.String(), "- handlers.feedback - WARNING - Errore parsing timestamp feedback per 123456789: ")
	}
}
