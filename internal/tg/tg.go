// Package tg contiene gli helper Telegram condivisi dagli handler: i filtri
// e l'handler dei comandi, l'invio e la modifica dei messaggi, la risposta
// alle callback query e lo store per-utente user_data.
package tg

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters"

	"github.com/dstmrk/octotracker/internal/logging"
)

var logger = logging.Logger("handlers")

// Errori restituiti quando l'update non contiene ciò che serve all'handler
// (es. un messaggio modificato al posto di un messaggio nuovo).
var (
	// ErrNoMessage indica un update senza il messaggio da usare.
	ErrNoMessage = errors.New("update senza messaggio")
	// ErrNoCallback indica un update che non è una callback query.
	ErrNoCallback = errors.New("update senza callback query")
	// ErrNoUser indica un update senza utente.
	ErrNoUser = errors.New("update senza utente")
)

// ========== Filtri e comandi ==========

// AnyCommand accetta i messaggi la cui prima entità è un bot_command in
// testa al testo.
func AnyCommand(msg *gotgbot.Message) bool {
	if msg == nil || len(msg.Entities) == 0 {
		return false
	}
	e := msg.Entities[0]
	return e.Type == gotgbot.MessageEntityTypeBotCommand && e.Offset == 0
}

// TextNotCommand accetta i messaggi di testo che non sono comandi.
func TextNotCommand(msg *gotgbot.Message) bool {
	return msg != nil && msg.Text != "" && !AnyCommand(msg)
}

// CallbackPattern accetta le callback query il cui callback_data
// corrisponde all'espressione regolare pattern (ancorata dal chiamante).
func CallbackPattern(pattern string) filters.CallbackQuery {
	re := regexp.MustCompile(pattern)
	return func(cq *gotgbot.CallbackQuery) bool {
		return re.MatchString(cq.Data)
	}
}

// Command è l'handler di un comando: risponde ai messaggi, anche
// modificati, il cui testo inizia con l'entità bot_command "/nome" oppure
// "/nome@bot", senza distinzione tra maiuscole e minuscole. Le caption
// non contano.
type Command struct {
	name     string
	response handlers.Response
}

var _ ext.Handler = Command{}

// NewCommand crea l'handler del comando /name.
func NewCommand(name string, r handlers.Response) Command {
	return Command{name: name, response: r}
}

// CheckUpdate implementa ext.Handler.
func (c Command) CheckUpdate(b *gotgbot.Bot, ctx *ext.Context) bool {
	msg := ctx.Message
	if msg == nil {
		msg = ctx.EditedMessage
	}
	if !AnyCommand(msg) {
		return false
	}
	// I comandi sono ASCII: la lunghezza UTF-16 dell'entità è in byte.
	end := int(msg.Entities[0].Length)
	if end < 0 || end > len(msg.Text) {
		end = len(msg.Text)
	}
	command, ok := strings.CutPrefix(msg.Text[:end], "/")
	name, bot, hasBot := strings.Cut(command, "@")
	return ok && strings.EqualFold(name, c.name) && (!hasBot || strings.EqualFold(bot, b.Username))
}

// HandleUpdate implementa ext.Handler.
func (c Command) HandleUpdate(b *gotgbot.Bot, ctx *ext.Context) error {
	return c.response(b, ctx)
}

// Name implementa ext.Handler.
func (c Command) Name() string {
	return "command_" + c.name
}

// ========== Messaggi e callback ==========

// UserID restituisce l'id dell'utente dell'update come stringa, la chiave
// usata nel database.
func UserID(ctx *ext.Context) (string, error) {
	if ctx.EffectiveUser == nil {
		return "", ErrNoUser
	}
	return strconv.FormatInt(ctx.EffectiveUser.Id, 10), nil
}

// Reply invia text nella chat di msg senza citarlo, nello stesso topic e
// con la stessa business connection. parseMode vuoto e markup nil
// omettono i rispettivi parametri.
func Reply(b *gotgbot.Bot, msg *gotgbot.Message, text, parseMode string, markup *gotgbot.InlineKeyboardMarkup) error {
	if msg == nil {
		return ErrNoMessage
	}
	opts := &gotgbot.SendMessageOpts{ParseMode: parseMode, BusinessConnectionId: msg.BusinessConnectionId}
	if msg.IsTopicMessage {
		opts.MessageThreadId = msg.MessageThreadId
	}
	if msg.DirectMessagesTopic != nil {
		opts.DirectMessagesTopicId = msg.DirectMessagesTopic.TopicId
	}
	if markup != nil {
		opts.ReplyMarkup = *markup
	}
	_, err := b.SendMessage(msg.Chat.Id, text, opts)
	return err
}

// CallbackMessage restituisce il messaggio del pulsante premuto: un
// messaggio inaccessibile diventa un Message senza testo, nil se la
// callback non ha messaggio (pulsante di un messaggio inline).
func CallbackMessage(cq *gotgbot.CallbackQuery) *gotgbot.Message {
	if cq == nil {
		return nil
	}
	switch m := cq.Message.(type) {
	case gotgbot.Message:
		return &m
	case *gotgbot.Message:
		return m
	case gotgbot.InaccessibleMessage:
		return m.ToMessage()
	}
	return nil
}

// EditText modifica il messaggio del pulsante premuto (o il messaggio
// inline). gotgbot invia sempre reply_markup: senza markup il messaggio
// resta senza tastiera.
func EditText(b *gotgbot.Bot, cq *gotgbot.CallbackQuery, text, parseMode string, markup *gotgbot.InlineKeyboardMarkup) error {
	if cq == nil {
		return ErrNoCallback
	}
	opts := &gotgbot.EditMessageTextOpts{Text: text, ParseMode: parseMode}
	if markup != nil {
		opts.ReplyMarkup = *markup
	}
	if cq.InlineMessageId != "" {
		opts.InlineMessageId = cq.InlineMessageId
	} else if msg := CallbackMessage(cq); msg != nil {
		opts.ChatId, opts.MessageId, opts.BusinessConnectionId = msg.Chat.Id, msg.MessageId, msg.BusinessConnectionId
	} else {
		return ErrNoMessage
	}
	_, _, err := b.EditMessageText(opts)
	return err
}

// SafeAnswerCallback risponde a una callback query ignorando l'errore se la
// query è scaduta.
//
// Telegram impone un timeout di ~30 secondi per rispondere alle callback
// query: dopo il timeout l'API restituisce "query is too old and response
// timeout expired or query id is invalid". L'errore è innocuo e non deve
// bloccare la conversazione; ogni altro errore viene restituito.
func SafeAnswerCallback(b *gotgbot.Bot, cq *gotgbot.CallbackQuery) error {
	if cq == nil {
		return ErrNoCallback
	}
	_, err := cq.Answer(b, nil)
	if err == nil {
		return nil
	}
	if IsBadRequest(err) {
		msg := strings.ToLower(ErrorString(err))
		if strings.Contains(msg, "too old") || strings.Contains(msg, "query id is invalid") {
			logger.Debug(fmt.Sprintf("Callback query scaduta (ignorata): %s", ErrorString(err)))
			return nil
		}
	}
	return err
}

// ========== Errori ==========

// IsBadRequest indica una risposta HTTP 400 dell'API che non è una
// migrazione del gruppo né un rate limit.
func IsBadRequest(err error) bool {
	var tgErr *gotgbot.TelegramError
	if !errors.As(err, &tgErr) || tgErr.Code != http.StatusBadRequest {
		return false
	}
	p := tgErr.ResponseParams
	return p == nil || (p.MigrateToChatId == 0 && p.RetryAfter == 0)
}

// ErrorString restituisce il testo di un errore per i log: la descrizione
// di Telegram per gli errori dell'API (es. "Forbidden: bot was blocked by
// the user"), err.Error() per gli altri e "" per nil.
func ErrorString(err error) string {
	if err == nil {
		return ""
	}
	var tgErr *gotgbot.TelegramError
	if errors.As(err, &tgErr) && tgErr.Description != "" {
		return tgErr.Description
	}
	return err.Error()
}

// ========== user_data ==========

// UserData è l'equivalente di context.user_data: un dizionario in memoria
// per utente, condiviso tra tutte le conversazioni.
type UserData struct {
	mu    sync.Mutex
	users map[int64]map[string]any
}

// NewUserData crea uno store vuoto.
func NewUserData() *UserData {
	return &UserData{users: map[int64]map[string]any{}}
}

// Get restituisce il valore per (userID, key).
func (u *UserData) Get(userID int64, key string) (any, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	v, ok := u.users[userID][key]
	return v, ok
}

// Set imposta il valore per (userID, key).
func (u *UserData) Set(userID int64, key string, value any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	m := u.users[userID]
	if m == nil {
		m = map[string]any{}
		u.users[userID] = m
	}
	m[key] = value
}

// Delete rimuove (userID, key), se presente.
func (u *UserData) Delete(userID int64, key string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.users[userID], key)
}

// Clear svuota i dati dell'utente.
func (u *UserData) Clear(userID int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.users, userID)
}

// Snapshot restituisce una copia dei dati dell'utente.
func (u *UserData) Snapshot(userID int64) map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]any, len(u.users[userID]))
	for k, v := range u.users[userID] {
		out[k] = v
	}
	return out
}
