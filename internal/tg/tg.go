// Package tg contiene helper Telegram condivisi tra gli handler:
// filtri equivalenti a quelli di python-telegram-bot, risposta sicura
// alle callback query (port di handlers/__init__.py), conversione degli
// errori API nel messaggio che python-telegram-bot mostrerebbe e lo store
// per-utente equivalente a context.user_data.
package tg

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters"

	"github.com/dstmrk/octotracker/internal/logging"
)

// logger replica logging.getLogger(__name__) di handlers/__init__.py.
var logger = logging.Logger("handlers")

// IsCommand replica filters.COMMAND di PTB: la prima entità del messaggio
// è un bot_command con offset 0.
func IsCommand(msg *gotgbot.Message) bool {
	if msg == nil || len(msg.Entities) == 0 {
		return false
	}
	e := msg.Entities[0]
	return e.Type == gotgbot.MessageEntityTypeBotCommand && e.Offset == 0
}

// TextNotCommand replica filters.TEXT & ~filters.COMMAND di PTB.
func TextNotCommand(msg *gotgbot.Message) bool {
	return msg != nil && msg.Text != "" && !IsCommand(msg)
}

// AnyCommand replica filters.COMMAND di PTB come filtro gotgbot.
func AnyCommand(msg *gotgbot.Message) bool {
	return IsCommand(msg)
}

// CallbackPattern replica CallbackQueryHandler(pattern=...) di PTB, che usa
// re.match (ancorato all'inizio della stringa, non alla fine).
//
// Come PTB (v22), una callback senza data corrisponde solo se non ha nemmeno
// game_short_name (caso che Telegram non produce mai).
func CallbackPattern(pattern string) filters.CallbackQuery {
	if !strings.HasPrefix(pattern, "^") {
		pattern = "^(?:" + pattern + ")"
	}
	re := regexp.MustCompile(pattern)
	return func(cq *gotgbot.CallbackQuery) bool {
		if cq.Data == "" {
			return cq.GameShortName == ""
		}
		return re.MatchString(cq.Data)
	}
}

// SafeAnswerCallback risponde a una callback query ignorando l'errore se la
// query è scaduta.
//
// Telegram impone un timeout di ~30 secondi per rispondere alle callback
// query: se il bot risponde dopo il timeout, l'API restituisce "Query is too
// old and response timeout expired". L'errore è innocuo e non deve bloccare
// il flusso della conversazione. Come in Python vengono ignorati solo i
// BadRequest (HTTP 400) che contengono "too old" o "query id is invalid";
// ogni altro errore viene restituito. Port di handlers.safe_answer_callback.
func SafeAnswerCallback(b *gotgbot.Bot, cq *gotgbot.CallbackQuery) error {
	if cq == nil {
		return nil
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

// IsBadRequest indica se l'errore corrisponde a telegram.error.BadRequest di
// PTB: risposta HTTP 400 che non è un ChatMigrated o un RetryAfter.
func IsBadRequest(err error) bool {
	var tgErr *gotgbot.TelegramError
	if !errors.As(err, &tgErr) || tgErr.Code != http.StatusBadRequest {
		return false
	}
	p := tgErr.ResponseParams
	return p == nil || (p.MigrateToChatId == 0 && p.RetryAfter == 0)
}

// ErrorString restituisce il messaggio dell'errore come lo mostrerebbe
// str(e) per le eccezioni di python-telegram-bot (v22), così i log restano
// identici alla versione Python. Per esempio la descrizione
// "Bad Request: chat not found" diventa "Chat not found", mentre un 429 con
// retry_after diventa "Flood control exceeded. Retry in 5 seconds".
// Gli errori che non provengono dall'API Telegram restituiscono err.Error().
func ErrorString(err error) string {
	if err == nil {
		return ""
	}
	var tgErr *gotgbot.TelegramError
	if !errors.As(err, &tgErr) {
		return err.Error()
	}
	if p := tgErr.ResponseParams; p != nil {
		if p.MigrateToChatId != 0 {
			return fmt.Sprintf("Group migrated to supergroup. New chat id: %d", p.MigrateToChatId)
		}
		if p.RetryAfter != 0 {
			return fmt.Sprintf("Flood control exceeded. Retry in %d seconds", p.RetryAfter)
		}
	}
	message := tgErr.Description
	if message == "" {
		if phrase := http.StatusText(tgErr.Code); phrase != "" {
			message = fmt.Sprintf("%s (%d)", phrase, tgErr.Code)
		} else {
			message = fmt.Sprintf("Unknown HTTPError (%d)", tgErr.Code)
		}
	}
	return ptbErrorMessage(message)
}

// ptbErrorMessage replica TelegramError.__init__ di PTB: rimuove i prefissi
// "Error: ", "[Error]: " e "Bad Request: " e, se ne ha rimosso almeno uno,
// applica str.capitalize() al resto.
func ptbErrorMessage(message string) string {
	msg := strings.TrimPrefix(message, "Error: ")
	msg = strings.TrimPrefix(msg, "[Error]: ")
	msg = strings.TrimPrefix(msg, "Bad Request: ")
	if msg != message {
		msg = pyCapitalize(msg)
	}
	return msg
}

// pyCapitalize replica str.capitalize() di Python (mappature Unicode semplici).
func pyCapitalize(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	var b strings.Builder
	b.Grow(len(s))
	b.WriteRune(unicode.ToTitle(r))
	for _, c := range s[size:] {
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}

// UserData è l'equivalente di context.user_data di PTB: un dizionario
// in memoria per utente, condiviso tra tutte le conversazioni.
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

// Delete rimuove (userID, key); equivale a user_data.pop(key, None).
func (u *UserData) Delete(userID int64, key string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.users[userID], key)
}

// Clear svuota i dati dell'utente; equivale a user_data.clear().
func (u *UserData) Clear(userID int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.users, userID)
}

// Snapshot restituisce una copia dei dati dell'utente (per test e debug).
func (u *UserData) Snapshot(userID int64) map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]any, len(u.users[userID]))
	for k, v := range u.users[userID] {
		out[k] = v
	}
	return out
}

// Float restituisce il valore come float64 (false se assente o di altro tipo).
func (u *UserData) Float(userID int64, key string) (float64, bool) {
	v, ok := u.Get(userID, key)
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

// String restituisce il valore come stringa (false se assente o di altro tipo).
func (u *UserData) String(userID int64, key string) (string, bool) {
	v, ok := u.Get(userID, key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
