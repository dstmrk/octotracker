package tg

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// captureLogs installa un logger di default in memoria per la durata del test.
func captureLogs(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(logging.NewTextHandler(&buf, level)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return &buf
}

func callbackQuery() *gotgbot.CallbackQuery {
	return testutil.CallbackUpdate(testutil.DefaultUserID, "data").CallbackQuery
}

// ========== SafeAnswerCallback ==========

func TestSafeAnswerCallbackSuccess(t *testing.T) {
	b, fc := testutil.NewBot(t)
	if err := SafeAnswerCallback(b, callbackQuery()); err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	calls := fc.CallsTo("answerCallbackQuery")
	if len(calls) != 1 || calls[0].String("callback_query_id") != "cbq-1" {
		t.Fatalf("answerCallbackQuery: %+v", calls)
	}
}

func TestSafeAnswerCallbackExpiredQueryIgnored(t *testing.T) {
	for _, desc := range []string{
		"Bad Request: query is too old and response timeout expired or query id is invalid",
		"Bad Request: Query ID is invalid",
	} {
		b, fc := testutil.NewBot(t)
		logs := captureLogs(t, slog.LevelDebug)
		fc.FailMethod("answerCallbackQuery", testutil.TelegramError("answerCallbackQuery", 400, desc))
		if err := SafeAnswerCallback(b, callbackQuery()); err != nil {
			t.Fatalf("%q: l'errore doveva essere ignorato: %v", desc, err)
		}
		want := " - handlers - DEBUG - Callback query scaduta (ignorata): " + desc + "\n"
		if !strings.HasSuffix(logs.String(), want) {
			t.Errorf("log = %q, want suffisso %q", logs.String(), want)
		}
	}
}

func TestSafeAnswerCallbackOtherErrorsReturned(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"altro bad request", testutil.TelegramError("answerCallbackQuery", 400, "Bad Request: some other error")},
		// Solo i BadRequest vengono ignorati, anche se il testo combacia
		{"forbidden", testutil.TelegramError("answerCallbackQuery", 403, "Forbidden: query is too old")},
		{"retry after", &gotgbot.TelegramError{
			Method: "answerCallbackQuery", Code: 400, Description: "query is too old",
			ResponseParams: &gotgbot.ResponseParameters{RetryAfter: 3},
		}},
		{"rete", errors.New("connection reset: query is too old")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, fc := testutil.NewBot(t)
			fc.FailMethod("answerCallbackQuery", tt.err)
			if err := SafeAnswerCallback(b, callbackQuery()); !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestSafeAnswerCallbackNilQuery(t *testing.T) {
	b, fc := testutil.NewBot(t)
	if err := SafeAnswerCallback(b, nil); !errors.Is(err, ErrNoCallback) {
		t.Fatalf("err = %v", err)
	}
	if n := len(fc.Calls()); n != 0 {
		t.Fatalf("nessuna chiamata attesa, trovate %d", n)
	}
}

// ========== Errori ==========

func TestIsBadRequest(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"400", testutil.TelegramError("m", 400, "Bad Request: chat not found"), true},
		{"400 wrapped", fmt.Errorf("wrap: %w", testutil.TelegramError("m", 400, "x")), true},
		{"400 params vuoti", &gotgbot.TelegramError{Code: 400, ResponseParams: &gotgbot.ResponseParameters{}}, true},
		{"403", testutil.TelegramError("m", 403, "Forbidden: bot was blocked by the user"), false},
		{"429", testutil.RetryAfterError("m", 5), false},
		{"migrated", &gotgbot.TelegramError{Code: 400, ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -100}}, false},
		{"altro", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		if got := IsBadRequest(tt.err); got != tt.want {
			t.Errorf("%s: IsBadRequest = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// ErrorString restituisce la descrizione di Telegram: internal/checker vi
// cerca (in minuscolo) le frasi che fanno rimuovere l'utente.
func TestErrorString(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("dial tcp: timeout"), "dial tcp: timeout"},
		{testutil.TelegramError("m", 400, "Bad Request: chat not found"), "Bad Request: chat not found"},
		{fmt.Errorf("wrap: %w", testutil.TelegramError("m", 403, "Forbidden: bot was blocked by the user")), "Forbidden: bot was blocked by the user"},
		{testutil.TelegramError("m", 403, "Forbidden: user is deactivated"), "Forbidden: user is deactivated"},
		{testutil.TelegramError("m", 403, "Forbidden: bot was kicked from the group chat"), "Forbidden: bot was kicked from the group chat"},
		{&gotgbot.TelegramError{Method: "m", Code: 502}, "unable to m: "},
	}
	for _, tt := range tests {
		if got := ErrorString(tt.err); got != tt.want {
			t.Errorf("ErrorString(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

// ========== Filtri e comandi ==========

func TestCommandFilters(t *testing.T) {
	tests := []struct {
		name            string
		msg             *gotgbot.Message
		isCmd, textOnly bool
	}{
		{"comando", testutil.MessageUpdate(1, "/start").Message, true, false},
		{"comando con argomenti", testutil.MessageUpdate(1, "/status arg").Message, true, false},
		{"testo", testutil.MessageUpdate(1, "ciao").Message, false, true},
		{"comando non in testa", &gotgbot.Message{Text: "ciao /start", Entities: []gotgbot.MessageEntity{{Type: "bot_command", Offset: 5, Length: 6}}}, false, true},
		{"entità non comando", &gotgbot.Message{Text: "/x", Entities: []gotgbot.MessageEntity{{Type: "bold", Offset: 0, Length: 2}}}, false, true},
		{"messaggio senza testo", &gotgbot.Message{}, false, false},
		{"nil", nil, false, false},
	}
	for _, tt := range tests {
		if got := AnyCommand(tt.msg); got != tt.isCmd {
			t.Errorf("%s: AnyCommand = %v, want %v", tt.name, got, tt.isCmd)
		}
		if got := TextNotCommand(tt.msg); got != tt.textOnly {
			t.Errorf("%s: TextNotCommand = %v, want %v", tt.name, got, tt.textOnly)
		}
	}
}

func TestCallbackPattern(t *testing.T) {
	tests := []struct {
		pattern, data string
		want          bool
	}{
		{"^rate_update_yes$", "rate_update_yes", true},
		{"^rate_update_yes$", "rate_update_yes_2", false},
		{`^rating_\d$`, "rating_4", true},
		{`^rating_\d$`, "rating_10", false},
		{"^scadenza_update_(yes|no)$", "scadenza_update_no", true},
		{"^scadenza_update_(yes|no)$", "scadenza_update_maybe", false},
	}
	for _, tt := range tests {
		if got := CallbackPattern(tt.pattern)(&gotgbot.CallbackQuery{Data: tt.data}); got != tt.want {
			t.Errorf("CallbackPattern(%q)(%q) = %v, want %v", tt.pattern, tt.data, got, tt.want)
		}
	}
}

func commandUpdate(text string, entities ...gotgbot.MessageEntity) *gotgbot.Update {
	u := testutil.MessageUpdate(testutil.DefaultUserID, text)
	if entities != nil {
		u.Message.Entities = entities
	}
	return u
}

func TestCommandCheckUpdate(t *testing.T) {
	b, _ := testutil.NewBot(t)
	h := NewCommand("status", func(*gotgbot.Bot, *ext.Context) error { return nil })
	cmd := func(n int64) gotgbot.MessageEntity { return gotgbot.MessageEntity{Type: "bot_command", Length: n} }

	edited := testutil.MessageUpdate(testutil.DefaultUserID, "/status")
	edited.EditedMessage, edited.Message = edited.Message, nil
	caption := testutil.MessageUpdate(testutil.DefaultUserID, "")
	caption.Message.Caption, caption.Message.CaptionEntities = "/status", []gotgbot.MessageEntity{cmd(7)}

	cases := []struct {
		name   string
		update *gotgbot.Update
		want   bool
	}{
		{"semplice", commandUpdate("/status"), true},
		{"con argomenti", commandUpdate("/status ciao"), true},
		{"maiuscole", commandUpdate("/STATUS"), true},
		{"username del bot", commandUpdate("/status@" + testutil.BotUsername), true},
		{"username maiuscolo", commandUpdate("/status@OCTOTRACKER_TEST_BOT"), true},
		{"messaggio modificato", edited, true},
		{"altro bot", commandUpdate("/status@altro_bot"), false},
		{"altro comando", commandUpdate("/statusx"), false},
		{"prefisso", commandUpdate("/stat"), false},
		{"entità oltre il testo", commandUpdate("/status", cmd(50)), true},
		{"entità non in testa", commandUpdate("x /status", gotgbot.MessageEntity{Type: "bot_command", Offset: 2, Length: 7}), false},
		{"senza entità", commandUpdate("/status", []gotgbot.MessageEntity{}...), false},
		{"caption", caption, false},
		{"callback", testutil.CallbackUpdate(testutil.DefaultUserID, "/status"), false},
	}
	for _, c := range cases {
		if got := h.CheckUpdate(b, ext.NewContext(b, c.update, nil)); got != c.want {
			t.Errorf("%s: CheckUpdate = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCommandHandleUpdate(t *testing.T) {
	b, _ := testutil.NewBot(t)
	called := false
	h := NewCommand("help", func(*gotgbot.Bot, *ext.Context) error { called = true; return nil })
	if err := h.HandleUpdate(b, testutil.MessageContext(b, testutil.DefaultUserID, "/help")); err != nil || !called {
		t.Fatalf("HandleUpdate: %v %v", err, called)
	}
	if h.Name() != "command_help" {
		t.Errorf("Name = %q", h.Name())
	}
}

// ========== Messaggi e callback ==========

func TestUserID(t *testing.T) {
	b, _ := testutil.NewBot(t)
	if id, err := UserID(testutil.MessageContext(b, 42, "x")); err != nil || id != "42" {
		t.Errorf("UserID = %q, %v", id, err)
	}
	u := testutil.MessageUpdate(42, "x")
	u.Message.From = nil
	if _, err := UserID(ext.NewContext(b, u, nil)); !errors.Is(err, ErrNoUser) {
		t.Errorf("senza utente: %v", err)
	}
}

func TestReply(t *testing.T) {
	b, fc := testutil.NewBot(t)
	msg := &gotgbot.Message{Chat: gotgbot.Chat{Id: 4}, IsTopicMessage: true, MessageThreadId: 12,
		BusinessConnectionId: "bc", DirectMessagesTopic: &gotgbot.DirectMessagesTopic{TopicId: 3}}
	markup := &gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{{{Text: "a", CallbackData: "b"}}}}
	if err := Reply(b, msg, "x", "HTML", markup); err != nil {
		t.Fatal(err)
	}
	c := fc.LastCall(t, "sendMessage")
	for k, want := range map[string]string{"chat_id": "4", "text": "x", "parse_mode": "HTML", "message_thread_id": "12",
		"business_connection_id": "bc", "direct_messages_topic_id": "3"} {
		if got := c.String(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if kb := c.InlineKeyboard(); len(kb) != 1 || kb[0][0].CallbackData != "b" {
		t.Errorf("reply_markup: %+v", kb)
	}
	if _, quoted := c.Params["reply_parameters"]; quoted {
		t.Error("il messaggio non va citato")
	}

	// Il thread vale solo per i messaggi in un topic; senza opzioni nessun parametro
	fc.Reset()
	if err := Reply(b, &gotgbot.Message{Chat: gotgbot.Chat{Id: 9}, MessageThreadId: 3}, "x", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"message_thread_id", "parse_mode", "reply_markup"} {
		if _, ok := fc.LastCall(t, "sendMessage").Params[k]; ok {
			t.Errorf("%s non atteso", k)
		}
	}
	if err := Reply(b, nil, "x", "", nil); !errors.Is(err, ErrNoMessage) {
		t.Errorf("msg nil: %v", err)
	}
}

func TestEditText(t *testing.T) {
	b, fc := testutil.NewBot(t)
	cases := []struct {
		name string
		cq   *gotgbot.CallbackQuery
		want map[string]string
		err  error
	}{
		{"inline", &gotgbot.CallbackQuery{InlineMessageId: "im-1"}, map[string]string{"inline_message_id": "im-1"}, nil},
		{"valore", &gotgbot.CallbackQuery{Message: gotgbot.Message{MessageId: 7, Chat: gotgbot.Chat{Id: 9}, BusinessConnectionId: "bc"}},
			map[string]string{"chat_id": "9", "message_id": "7", "business_connection_id": "bc"}, nil},
		{"puntatore", &gotgbot.CallbackQuery{Message: &gotgbot.Message{MessageId: 8, Chat: gotgbot.Chat{Id: 10}}},
			map[string]string{"chat_id": "10", "message_id": "8"}, nil},
		{"inaccessibile", &gotgbot.CallbackQuery{Message: gotgbot.InaccessibleMessage{MessageId: 5, Chat: gotgbot.Chat{Id: 11}}},
			map[string]string{"chat_id": "11", "message_id": "5"}, nil},
		{"senza messaggio", &gotgbot.CallbackQuery{}, nil, ErrNoMessage},
		{"query nil", nil, nil, ErrNoCallback},
	}
	for _, c := range cases {
		fc.Reset()
		err := EditText(b, c.cq, "testo", "", nil)
		if !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
		}
		if c.err != nil {
			continue
		}
		call := fc.LastCall(t, "editMessageText")
		for k, v := range c.want {
			if call.String(k) != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, call.String(k), v)
			}
		}
		if _, ok := call.Params["parse_mode"]; ok {
			t.Errorf("%s: parse_mode non richiesto", c.name)
		}
	}
}

func TestCallbackMessage(t *testing.T) {
	if CallbackMessage(nil) != nil || CallbackMessage(&gotgbot.CallbackQuery{}) != nil {
		t.Error("callback senza messaggio")
	}
	m := CallbackMessage(&gotgbot.CallbackQuery{Message: gotgbot.InaccessibleMessage{MessageId: 5, Chat: gotgbot.Chat{Id: 11}}})
	if m == nil || m.MessageId != 5 || m.Chat.Id != 11 || m.Text != "" {
		t.Errorf("inaccessibile: %+v", m)
	}
}

// ========== user_data ==========

func TestUserDataGetSet(t *testing.T) {
	u := NewUserData()
	if _, ok := u.Get(1, "k"); ok {
		t.Fatal("chiave inattesa")
	}
	u.Set(1, "tipo", "fissa")
	u.Set(2, "tipo", "variabile")
	if v, ok := u.Get(1, "tipo"); !ok || v != "fissa" {
		t.Errorf("Get = %v, %v", v, ok)
	}
	snap := u.Snapshot(1)
	snap["tipo"] = "modificato"
	if v, _ := u.Get(1, "tipo"); v != "fissa" {
		t.Error("Snapshot deve essere una copia")
	}
}

func TestUserDataDeleteClear(t *testing.T) {
	u := NewUserData()
	u.Set(1, "a", 1)
	u.Set(1, "b", 2)
	u.Set(2, "a", 3)
	u.Delete(1, "a")
	u.Delete(1, "manca")
	u.Delete(12345, "qualsiasi")
	if snap := u.Snapshot(1); len(snap) != 1 || snap["b"] != 2 {
		t.Errorf("dopo Delete: %v", snap)
	}
	u.Clear(1)
	if len(u.Snapshot(1)) != 0 || len(u.Snapshot(2)) != 1 {
		t.Error("Clear deve svuotare solo l'utente indicato")
	}
}

func TestUserDataConcurrent(t *testing.T) {
	u := NewUserData()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				u.Set(id%4, "k", j)
				u.Get(id%4, "k")
				u.Snapshot(id % 4)
				if j%10 == 0 {
					u.Clear(id % 4)
				}
			}
		}(int64(i))
	}
	wg.Wait()
}
