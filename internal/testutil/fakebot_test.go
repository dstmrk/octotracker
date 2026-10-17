package testutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

func TestNewBot(t *testing.T) {
	b, fc := NewBot(t)
	if b.User.Username != BotUsername || !b.User.IsBot || b.User.Id != 123456 {
		t.Fatalf("utente bot = %+v", b.User)
	}
	if len(fc.Calls()) != 0 {
		t.Fatalf("NewBot non deve chiamare l'API: %v", fc.Calls())
	}
	if b.Token != "123456:TEST-TOKEN" {
		t.Errorf("token = %q", b.Token)
	}
}

func TestSendMessageRecordedWithDefaultResponse(t *testing.T) {
	b, fc := NewBot(t)
	kb := gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
		{{Text: "✅ Sì", CallbackData: "si"}, {Text: "❌ No", CallbackData: "no"}},
	}}
	msg, err := b.SendMessage(42, "<b>Ciao</b>", &gotgbot.SendMessageOpts{ParseMode: "HTML", ReplyMarkup: kb})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Chat.Id != 42 || msg.Text != "<b>Ciao</b>" || msg.MessageId != 1001 {
		t.Errorf("risposta = %+v", msg)
	}
	msg2, err := b.SendMessage(42, "secondo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg2.MessageId != 1002 {
		t.Errorf("message_id non incrementale: %d", msg2.MessageId)
	}

	c := fc.LastCall(t, "sendMessage")
	if c.Text() != "secondo" || c.ParseMode() != "" || c.ChatID() != "42" {
		t.Errorf("ultima chiamata = %+v", c)
	}
	first := fc.CallsTo("sendMessage")[0]
	if first.Text() != "<b>Ciao</b>" || first.ParseMode() != "HTML" {
		t.Errorf("prima chiamata = %+v", first)
	}
	got := first.InlineKeyboard()
	if len(got) != 1 || len(got[0]) != 2 || got[0][0].CallbackData != "si" || got[0][1].Text != "❌ No" {
		t.Errorf("tastiera = %+v", got)
	}
	if !strings.Contains(first.JSON("reply_markup"), `"callback_data":"si"`) {
		t.Errorf("JSON reply_markup = %s", first.JSON("reply_markup"))
	}
	if first.InlineKeyboard() == nil || c.InlineKeyboard() != nil {
		t.Error("InlineKeyboard deve essere nil senza reply_markup")
	}
}

func TestEditAndOtherMethods(t *testing.T) {
	b, fc := NewBot(t)
	m, _, err := b.EditMessageText(&gotgbot.EditMessageTextOpts{ChatId: 7, MessageId: 42, Text: "modificato"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "modificato" || m.Chat.Id != 7 {
		t.Errorf("edit = %+v", m)
	}
	ok, err := b.AnswerCallbackQuery("cbq", nil)
	if err != nil || !ok {
		t.Fatalf("answerCallbackQuery = %v, %v", ok, err)
	}
	me, err := b.GetMe(nil)
	if err != nil || me.Username != BotUsername {
		t.Fatalf("getMe = %+v, %v", me, err)
	}
	methods := []string{}
	for _, c := range fc.Calls() {
		methods = append(methods, c.Method)
	}
	if strings.Join(methods, ",") != "editMessageText,answerCallbackQuery,getMe" {
		t.Errorf("metodi = %v", methods)
	}
	if c := fc.LastCall(t, "editMessageText"); c.String("message_id") != "42" || c.ChatID() != "7" {
		t.Errorf("edit params = %v", c.Params)
	}

	fc.Reset()
	if len(fc.Calls()) != 0 {
		t.Error("Reset non ha cancellato le chiamate")
	}
}

func TestFailMethod(t *testing.T) {
	b, fc := NewBot(t)
	tgErr := TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user")
	fc.FailMethod("sendMessage", tgErr)
	_, err := b.SendMessage(1, "x", nil)
	var got *gotgbot.TelegramError
	if !errors.As(err, &got) || got.Code != 403 || got.Description != "Forbidden: bot was blocked by the user" {
		t.Fatalf("err = %v", err)
	}
	if len(fc.CallsTo("sendMessage")) != 1 {
		t.Error("anche le chiamate fallite vanno registrate")
	}
	// Gli altri metodi non sono influenzati
	if _, err := b.AnswerCallbackQuery("q", nil); err != nil {
		t.Fatal(err)
	}
	fc.FailMethod("sendMessage", nil)
	if _, err := b.SendMessage(1, "x", nil); err != nil {
		t.Fatalf("dopo FailMethod(nil): %v", err)
	}
}

func TestErrorBuilders(t *testing.T) {
	e := RetryAfterError("sendMessage", 7)
	if e.Code != 429 || e.ResponseParams == nil || e.ResponseParams.RetryAfter != 7 ||
		e.Description != "Too Many Requests: retry after 7" || e.Method != "sendMessage" {
		t.Fatalf("RetryAfterError = %+v", e)
	}
	if e := TelegramError("m", 400, "Bad Request: x"); e.Error() != "unable to m: Bad Request: x" {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestSetResponder(t *testing.T) {
	b, fc := NewBot(t)
	fc.SetResponder(func(method string, params map[string]any) (json.RawMessage, error) {
		switch method {
		case "getChat":
			return json.RawMessage(`{"id": 99, "type": "private", "first_name": "Mario"}`), nil
		case "deleteMessage":
			return nil, errors.New("errore custom")
		}
		return nil, nil // risposta di default
	})
	chat, err := b.GetChat(99, nil)
	if err != nil || chat.FirstName != "Mario" {
		t.Fatalf("getChat = %+v, %v", chat, err)
	}
	if _, err := b.DeleteMessage(1, 2, nil); err == nil || err.Error() != "errore custom" {
		t.Fatalf("deleteMessage err = %v", err)
	}
	if m, err := b.SendMessage(5, "default", nil); err != nil || m.Text != "default" {
		t.Fatalf("sendMessage = %+v, %v", m, err)
	}
}

// fakeTB intercetta Fatalf per verificare LastCall senza fallire il test.
type fakeTB struct {
	testing.TB
	msg string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Fatalf(format string, args ...any) { f.msg = fmt.Sprintf(format, args...) }

func TestLastCallMissing(t *testing.T) {
	b, fc := NewBot(t)
	if _, err := b.AnswerCallbackQuery("q", nil); err != nil {
		t.Fatal(err)
	}
	tb := &fakeTB{TB: t}
	if c := fc.LastCall(tb, "sendMessage"); c.Method != "" {
		t.Errorf("LastCall senza chiamate = %+v", c)
	}
	if !strings.Contains(tb.msg, "nessuna chiamata a sendMessage") || !strings.Contains(tb.msg, "answerCallbackQuery") {
		t.Fatalf("messaggio = %q", tb.msg)
	}
}

func TestCallAccessors(t *testing.T) {
	c := Call{Method: "m", Params: map[string]any{
		"s":   "testo",
		"n":   int64(12),
		"nil": nil,
		"ch":  make(chan int),
		"rm":  "non una tastiera",
	}}
	if c.String("s") != "testo" || c.String("n") != "12" || c.String("nil") != "" || c.String("manca") != "" {
		t.Error("String non corretto")
	}
	if c.JSON("manca") != "" || c.JSON("nil") != "" || c.JSON("n") != "12" {
		t.Error("JSON non corretto")
	}
	if !strings.HasPrefix(c.JSON("ch"), "<marshal error:") {
		t.Errorf("JSON(chan) = %q", c.JSON("ch"))
	}
	bad := Call{Params: map[string]any{"reply_markup": "stringa"}}
	if bad.InlineKeyboard() != nil {
		t.Error("InlineKeyboard su JSON non valido deve essere nil")
	}
}

func TestChatIDFromParams(t *testing.T) {
	tests := []struct {
		v    any
		want int64
	}{
		{int64(5), 5}, {7, 7}, {"-100123", -100123}, {"@canale", 0}, {3.5, 0}, {nil, 0},
	}
	for _, tt := range tests {
		if got := chatIDFromParams(map[string]any{"chat_id": tt.v}); got != tt.want {
			t.Errorf("chatIDFromParams(%v) = %d, want %d", tt.v, got, tt.want)
		}
	}
}

func TestURLs(t *testing.T) {
	_, fc := NewBot(t)
	if fc.GetAPIURL(nil) != gotgbot.DefaultAPIURL {
		t.Error("GetAPIURL")
	}
	if got := fc.FileURL("tok", "photos/a.jpg", nil); got != gotgbot.DefaultAPIURL+"/file/bottok/photos/a.jpg" {
		t.Errorf("FileURL = %q", got)
	}
}

func TestMessageUpdate(t *testing.T) {
	u := MessageUpdate(DefaultUserID, "ciao")
	m := u.Message
	if m.Text != "ciao" || m.From.Id != DefaultUserID || m.Chat.Id != DefaultUserID || m.Chat.Type != "private" || len(m.Entities) != 0 {
		t.Fatalf("messaggio = %+v", m)
	}

	tests := []struct {
		text   string
		length int64
	}{
		{"/start", 6},
		{"/status arg", 7},
		{"/update\naltro", 7},
		{"/cmd\targ", 4},
		{"/🐙x", 4}, // emoji fuori dal BMP: 2 unità UTF-16
	}
	for _, tt := range tests {
		e := MessageUpdate(1, tt.text).Message.Entities
		if len(e) != 1 || e[0].Type != "bot_command" || e[0].Offset != 0 || e[0].Length != tt.length {
			t.Errorf("entità di %q = %+v, want lunghezza %d", tt.text, e, tt.length)
		}
	}
	if len(MessageUpdate(1, "").Message.Entities) != 0 {
		t.Error("testo vuoto senza entità")
	}
}

func TestCallbackUpdateAndContexts(t *testing.T) {
	b, _ := NewBot(t)
	ctx := CallbackContext(b, DefaultUserID, "tipo_fissa")
	cq := ctx.CallbackQuery
	if cq == nil || cq.Data != "tipo_fissa" || cq.From.Id != DefaultUserID || cq.Id != "cbq-1" {
		t.Fatalf("callback = %+v", cq)
	}
	if ctx.EffectiveUser.Id != DefaultUserID || ctx.EffectiveChat.Id != DefaultUserID {
		t.Errorf("effective user/chat = %+v / %+v", ctx.EffectiveUser, ctx.EffectiveChat)
	}
	if msg := cq.Message; msg == nil || msg.GetMessageId() != 42 || msg.GetChat().Id != DefaultUserID {
		t.Errorf("messaggio della callback = %+v", cq.Message)
	}

	mctx := MessageContext(b, 555, "/start")
	if mctx.EffectiveMessage == nil || mctx.EffectiveMessage.Text != "/start" || mctx.EffectiveUser.Id != 555 {
		t.Fatalf("contesto messaggio = %+v", mctx)
	}
	if User(9).FirstName != "TestUser" || User(9).IsBot {
		t.Error("User di test")
	}
}

func TestFakeClientConcurrent(t *testing.T) {
	b, fc := NewBot(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = b.SendMessage(int64(i), "x", nil)
			fc.CallsTo("sendMessage")
		}(i)
	}
	wg.Wait()
	if n := len(fc.CallsTo("sendMessage")); n != 10 {
		t.Fatalf("chiamate = %d, want 10", n)
	}
}
