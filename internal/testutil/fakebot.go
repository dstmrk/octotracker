// Package testutil fornisce un finto client Telegram per i test.
//
// FakeClient implementa gotgbot.BotClient: registra ogni chiamata all'API
// (metodo + parametri) e restituisce risposte plausibili senza rete.
// I test possono iniettare errori o risposte custom per singolo metodo.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

// BotUsername è lo username del bot finto (usato dai CommandHandler per /cmd@bot).
const BotUsername = "octotracker_test_bot"

// Call è una chiamata registrata verso l'API Telegram.
type Call struct {
	Method string
	Params map[string]any
}

// String restituisce il parametro key come stringa ("" se assente).
func (c Call) String(key string) string {
	v, ok := c.Params[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// Text è una scorciatoia per il parametro "text".
func (c Call) Text() string { return c.String("text") }

// ParseMode è una scorciatoia per il parametro "parse_mode".
func (c Call) ParseMode() string { return c.String("parse_mode") }

// ChatID restituisce il parametro "chat_id" come stringa (es. "123").
func (c Call) ChatID() string { return c.String("chat_id") }

// JSON serializza il parametro key in JSON (utile per reply_markup).
func (c Call) JSON(key string) string {
	v, ok := c.Params[key]
	if !ok || v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	return string(b)
}

// InlineKeyboard decodifica reply_markup come tastiera inline.
// Restituisce nil se il parametro manca o non è una tastiera inline.
func (c Call) InlineKeyboard() [][]gotgbot.InlineKeyboardButton {
	raw := c.JSON("reply_markup")
	if raw == "" {
		return nil
	}
	var m gotgbot.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m.InlineKeyboard
}

// Responder produce la risposta per una chiamata. Restituire (nil, nil)
// significa "usa la risposta di default".
type Responder func(method string, params map[string]any) (json.RawMessage, error)

// FakeClient implementa gotgbot.BotClient in memoria.
type FakeClient struct {
	mu        sync.Mutex
	calls     []Call
	responder Responder
	errors    map[string]error
	nextMsgID int64
}

var _ gotgbot.BotClient = (*FakeClient)(nil)

// NewBot crea un *gotgbot.Bot collegato a un FakeClient.
func NewBot(t testing.TB) (*gotgbot.Bot, *FakeClient) {
	t.Helper()
	fc := &FakeClient{errors: map[string]error{}, nextMsgID: 1000}
	b, err := gotgbot.NewBot("123456:TEST-TOKEN", &gotgbot.BotOpts{
		BotClient:         fc,
		DisableTokenCheck: true,
	})
	if err != nil {
		t.Fatalf("NewBot: %v", err)
	}
	b.User = gotgbot.User{Id: 123456, IsBot: true, FirstName: "OctoTracker", Username: BotUsername}
	return b, fc
}

// SetResponder installa una funzione che può sovrascrivere le risposte.
func (f *FakeClient) SetResponder(r Responder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responder = r
}

// FailMethod fa fallire tutte le chiamate a method con err.
// Passare nil per rimuovere l'errore.
func (f *FakeClient) FailMethod(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.errors, method)
		return
	}
	f.errors[method] = err
}

// TelegramError costruisce un errore Telegram come quelli restituiti dall'API.
func TelegramError(method string, code int, description string) *gotgbot.TelegramError {
	return &gotgbot.TelegramError{Method: method, Code: code, Description: description}
}

// RetryAfterError costruisce un errore 429 con retry_after.
func RetryAfterError(method string, seconds int64) *gotgbot.TelegramError {
	return &gotgbot.TelegramError{
		Method:         method,
		Code:           429,
		Description:    fmt.Sprintf("Too Many Requests: retry after %d", seconds),
		ResponseParams: &gotgbot.ResponseParameters{RetryAfter: seconds},
	}
}

// Calls restituisce una copia di tutte le chiamate registrate.
func (f *FakeClient) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

// CallsTo restituisce le chiamate a un metodo specifico (es. "sendMessage").
func (f *FakeClient) CallsTo(method string) []Call {
	var out []Call
	for _, c := range f.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// LastCall restituisce l'ultima chiamata a method; fallisce il test se assente.
func (f *FakeClient) LastCall(t testing.TB, method string) Call {
	t.Helper()
	calls := f.CallsTo(method)
	if len(calls) == 0 {
		t.Fatalf("nessuna chiamata a %s (chiamate: %v)", method, f.methods())
		return Call{}
	}
	return calls[len(calls)-1]
}

// Reset cancella le chiamate registrate.
func (f *FakeClient) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *FakeClient) methods() []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method)
	}
	return out
}

// RequestWithContext registra la chiamata e restituisce la risposta.
func (f *FakeClient) RequestWithContext(_ context.Context, _ string, method string, params map[string]any, _ *gotgbot.RequestOpts) (json.RawMessage, error) {
	f.mu.Lock()
	cp := make(map[string]any, len(params))
	for k, v := range params {
		cp[k] = v
	}
	f.calls = append(f.calls, Call{Method: method, Params: cp})
	err := f.errors[method]
	responder := f.responder
	f.nextMsgID++
	msgID := f.nextMsgID
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if responder != nil {
		if raw, rerr := responder(method, cp); raw != nil || rerr != nil {
			return raw, rerr
		}
	}
	return defaultResponse(method, cp, msgID), nil
}

// GetAPIURL implementa gotgbot.BotClient.
func (f *FakeClient) GetAPIURL(*gotgbot.RequestOpts) string { return gotgbot.DefaultAPIURL }

// FileURL implementa gotgbot.BotClient.
func (f *FakeClient) FileURL(token, path string, _ *gotgbot.RequestOpts) string {
	return gotgbot.DefaultAPIURL + "/file/bot" + token + "/" + path
}

func defaultResponse(method string, params map[string]any, msgID int64) json.RawMessage {
	switch method {
	case "sendMessage", "editMessageText", "editMessageReplyMarkup":
		chatID := chatIDFromParams(params)
		text, _ := params["text"].(string)
		msg := map[string]any{
			"message_id": msgID,
			"date":       1700000000,
			"chat":       map[string]any{"id": chatID, "type": "private"},
			"text":       text,
		}
		b, _ := json.Marshal(msg)
		return b
	case "getMe":
		b, _ := json.Marshal(map[string]any{
			"id": 123456, "is_bot": true, "first_name": "OctoTracker", "username": BotUsername,
		})
		return b
	default:
		return json.RawMessage("true")
	}
}

func chatIDFromParams(params map[string]any) int64 {
	switch v := params["chat_id"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		var id int64
		_, _ = fmt.Sscan(v, &id)
		return id
	default:
		return 0
	}
}

// ========== Costruzione di update finti ==========

// DefaultUserID è l'id utente usato dai test Python (123456789).
const DefaultUserID int64 = 123456789

// User restituisce un utente Telegram di test.
func User(id int64) gotgbot.User {
	return gotgbot.User{Id: id, IsBot: false, FirstName: "TestUser"}
}

// MessageUpdate costruisce un update con un messaggio testuale privato.
// Se text inizia con "/", aggiunge l'entità bot_command come fa Telegram.
func MessageUpdate(userID int64, text string) *gotgbot.Update {
	u := User(userID)
	msg := &gotgbot.Message{
		MessageId: 1,
		Date:      1700000000,
		Chat:      gotgbot.Chat{Id: userID, Type: "private"},
		From:      &u,
		Text:      text,
	}
	if len(text) > 0 && text[0] == '/' {
		end := len(text)
		for i, r := range text {
			if r == ' ' || r == '\n' || r == '\t' {
				end = i
				break
			}
		}
		msg.Entities = []gotgbot.MessageEntity{{Type: "bot_command", Offset: 0, Length: int64(utf16Len(text[:end]))}}
	}
	return &gotgbot.Update{UpdateId: 1, Message: msg}
}

// CallbackUpdate costruisce un update con una callback query da un pulsante inline.
func CallbackUpdate(userID int64, data string) *gotgbot.Update {
	u := User(userID)
	return &gotgbot.Update{
		UpdateId: 2,
		CallbackQuery: &gotgbot.CallbackQuery{
			Id:   "cbq-1",
			From: u,
			Data: data,
			Message: &gotgbot.Message{
				MessageId: 42,
				Date:      1700000000,
				Chat:      gotgbot.Chat{Id: userID, Type: "private"},
				Text:      "messaggio originale",
			},
			ChatInstance: "ci-1",
		},
	}
}

// Context avvolge un update in un *ext.Context pronto per gli handler.
func Context(b *gotgbot.Bot, u *gotgbot.Update) *ext.Context {
	return ext.NewContext(b, u, nil)
}

// MessageContext è una scorciatoia per Context(b, MessageUpdate(userID, text)).
func MessageContext(b *gotgbot.Bot, userID int64, text string) *ext.Context {
	return Context(b, MessageUpdate(userID, text))
}

// CallbackContext è una scorciatoia per Context(b, CallbackUpdate(userID, data)).
func CallbackContext(b *gotgbot.Bot, userID int64, data string) *ext.Context {
	return Context(b, CallbackUpdate(userID, data))
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}
