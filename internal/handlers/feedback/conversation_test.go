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

func TestConversationPatternsLikePythonRe(t *testing.T) {
	e := newEnv(t)
	for _, data := range []string{"rating_٣", "rating_5\n", "rating_0"} {
		c := newConvo(t, e.h)
		c.mustHandle(c.text("/feedback"))
		c.mustHandle(c.button(data)) // \d Unicode e $ prima di "\n" finale
	}
	for _, data := range []string{"rating_5\n\n", "xrating_5", "rating_a", "rating_"} {
		c := newConvo(t, e.h)
		c.mustHandle(c.text("/feedback"))
		c.mustIgnore(c.button(data))
	}
	// skip_comment\n è accettato come in re.match(r"^skip_comment$")
	c := newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	c.mustHandle(c.button("rating_2"))
	c.mustIgnore(c.button("skip_commentx"))
	c.mustHandle(c.button("skip_comment\n"))
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

// Un messaggio modificato nello stato COMMENT arriva a feedback_comment ma
// update.message è None: eccezione e stato invariato (come PTB).
func TestConversationEditedMessageKeepsState(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	c := newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	c.mustHandle(c.button("rating_5"))

	edited := testutil.MessageUpdate(testutil.DefaultUserID, "modificato")
	edited.EditedMessage, edited.Message = edited.Message, nil
	handled, err := c.feed(edited)
	if !handled || err == nil || err.Error() != "AttributeError: 'NoneType' object has no attribute 'text'" {
		t.Fatalf("edited: handled=%v err=%v", handled, err)
	}
	if n := e.s.GetFeedbackCount(); n != 0 {
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
// conversazione, come in PTB con per_chat=True.
func TestConversationIgnoresInlineCallbacks(t *testing.T) {
	e := newEnv(t)
	c := newConvo(t, e.h)
	c.mustHandle(c.text("/feedback"))
	u := testutil.CallbackUpdate(testutil.DefaultUserID, "rating_4")
	u.CallbackQuery.Message = nil
	u.CallbackQuery.InlineMessageId = "inline-1"
	c.mustIgnore(c.feed(u))
}

// /feedback modificato (edited_message): CommandHandler lo accetta, ma
// update.message è None → AttributeError dopo il controllo del cooldown.
func TestFeedbackCommandEditedMessage(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	e.h.UserData.Set(testutil.DefaultUserID, "k", "v")
	res := call(t, fc, e.h.UserData, e.h.FeedbackCommand, b, editedMessageContext(b, "/feedback"))
	if res.err == nil || !strings.Contains(res.err.Error(), "'reply_text'") || len(res.userAfter) != 0 {
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
		res := call(t, fc, e.h.UserData, h, b, ext.NewContext(b, u, nil))
		if res.err == nil || !strings.Contains(res.err.Error(), "'id'") {
			t.Errorf("%s: err=%v", name, res.err)
		}
	}
}

func TestCallbackErrors(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)

	// update.callback_query None → AttributeError su answer
	msgCtx := testutil.MessageContext(b, testutil.DefaultUserID, "x")
	for _, h := range []handlers.Response{e.h.FeedbackRating, e.h.FeedbackSkipComment} {
		if res := call(t, fc, e.h.UserData, h, b, msgCtx); res.err == nil || !strings.Contains(res.err.Error(), "'answer'") {
			t.Errorf("err=%v", res.err)
		}
	}

	// Query scaduta: safe_answer_callback ignora l'errore e il flusso continua
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
	fc.FailMethod("answerCallbackQuery", nil)

	// Errore di modifica del messaggio: propagato, stato invariato
	editErr := testutil.TelegramError("editMessageText", 400, "Bad Request: message is not modified")
	fc.FailMethod("editMessageText", editErr)
	for _, h := range []handlers.Response{e.h.FeedbackRating, e.h.FeedbackSkipComment, e.h.FeedbackCancel} {
		if res := call(t, fc, e.h.UserData, h, b, callbackContext(b, "rating_3")); !errors.Is(res.err, editErr) || res.state != "" {
			t.Errorf("edit: err=%v state=%q", res.err, res.state)
		}
	}
	fc.FailMethod("editMessageText", nil)

	// Errore di invio: propagato
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

func TestEditMessageTextTargets(t *testing.T) {
	b, fc := testutil.NewBot(t)
	cases := []struct {
		name string
		cq   *gotgbot.CallbackQuery
		want map[string]string
		err  string
	}{
		{"inline", &gotgbot.CallbackQuery{InlineMessageId: "im-1"}, map[string]string{"inline_message_id": "im-1"}, ""},
		{"valore", &gotgbot.CallbackQuery{Message: gotgbot.Message{MessageId: 7, Chat: gotgbot.Chat{Id: 9}, BusinessConnectionId: "bc"}},
			map[string]string{"chat_id": "9", "message_id": "7", "business_connection_id": "bc"}, ""},
		{"puntatore", &gotgbot.CallbackQuery{Message: &gotgbot.Message{MessageId: 8, Chat: gotgbot.Chat{Id: 10}}},
			map[string]string{"chat_id": "10", "message_id": "8"}, ""},
		{"inaccessibile", &gotgbot.CallbackQuery{Message: gotgbot.InaccessibleMessage{MessageId: 5, Chat: gotgbot.Chat{Id: 11}}},
			map[string]string{"chat_id": "11", "message_id": "5"}, ""},
		{"senza messaggio", &gotgbot.CallbackQuery{}, nil, "TypeError: Cannot edit an inaccessible message"},
		{"query nil", nil, nil, "AttributeError: 'NoneType' object has no attribute 'edit_message_text'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fc.Reset()
			err := editMessageText(b, c.cq, "testo", "", nil)
			if c.err != "" {
				if err == nil || err.Error() != c.err {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			call := fc.LastCall(t, "editMessageText")
			for k, v := range c.want {
				if call.String(k) != v {
					t.Errorf("%s = %q, want %q", k, call.String(k), v)
				}
			}
			if _, ok := call.Params["parse_mode"]; ok {
				t.Error("parse_mode non richiesto")
			}
		})
	}
}

func TestReplyTextTopic(t *testing.T) {
	b, fc := testutil.NewBot(t)
	msg := &gotgbot.Message{Chat: gotgbot.Chat{Id: 4}, IsTopicMessage: true, MessageThreadId: 12,
		BusinessConnectionId: "bc", DirectMessagesTopic: &gotgbot.DirectMessagesTopic{TopicId: 3}}
	if err := replyText(b, msg, "x", "", nil); err != nil {
		t.Fatal(err)
	}
	c := fc.LastCall(t, "sendMessage")
	if c.String("message_thread_id") != "12" || c.String("business_connection_id") != "bc" || c.String("direct_messages_topic_id") != "3" {
		t.Errorf("parametri: %+v", c.Params)
	}
	if _, ok := c.Params["reply_parameters"]; ok {
		t.Error("reply_text non cita il messaggio")
	}
}

func TestPyInt(t *testing.T) {
	ok := map[string]int{"1_0": 10, "+5": 5, " -5 ": -5, "٣": 3, "𝟗": 9, "𝟎𝟏": 1, "5\n": 5, " 5": 5, "1٣": 13}
	for s, want := range ok {
		if got, err := pyInt(s); err != nil || got != want {
			t.Errorf("pyInt(%q) = %d, %v; want %d", s, got, err, want)
		}
	}
	for _, s := range []string{"_1", "1__0", "1_", "", "+", "-", "12a"} {
		_, err := pyInt(s)
		want := "invalid literal for int() with base 10: " + pyStrRepr(s)
		if err == nil || err.Error() != want {
			t.Errorf("pyInt(%q): err=%v, want %q", s, err, want)
		}
	}
	if _, err := pyInt(strings.Repeat("9", 30)); err == nil || pyErrString(err) != "MemoryError: " {
		t.Errorf("numero enorme: %v", err)
	}
}

func TestPyStrRepr(t *testing.T) {
	cases := map[string]string{
		"invalid":    "'invalid'",
		"l'uno":      `"l'uno"`,
		`a'b"c`:      `'a\'b"c'`,
		"\t\n\r\\":   `'\t\n\r\\'`,
		"\x00\x7f":   `'\x00\x7f'`,
		"\u00a0é":    "'\\xa0é'",
		"\u200b":     "'\\u200b'",
		"😀":          "'😀'",
		"\U000e0001": `'\U000e0001'`,
	}
	for in, want := range cases {
		if got := pyStrRepr(in); got != want {
			t.Errorf("pyStrRepr(%q) = %s, want %s", in, got, want)
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

func TestNowMicrosTruncatesToMicroseconds(t *testing.T) {
	setNow(t, time.Date(2026, 1, 2, 3, 4, 5, 999_999_999, time.UTC))
	want := naiveMicros(2026, 1, 2, 3, 4, 5, 999_999)
	if got := nowMicros(); got != want {
		t.Errorf("nowMicros = %d, want %d", got, want)
	}
}
