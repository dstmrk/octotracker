// Package tg contiene helper Telegram condivisi tra gli handler:
// filtri equivalenti a quelli di python-telegram-bot, risposta sicura
// alle callback query e lo store per-utente equivalente a context.user_data.
package tg

import (
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters"
)

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
// re.match (ancorato all'inizio della stringa).
func CallbackPattern(pattern string) filters.CallbackQuery {
	if !strings.HasPrefix(pattern, "^") {
		pattern = "^(?:" + pattern + ")"
	}
	re := regexp.MustCompile(pattern)
	return func(cq *gotgbot.CallbackQuery) bool {
		return re.MatchString(cq.Data)
	}
}

// SafeAnswerCallback risponde a una callback query ignorando l'errore se la
// query è scaduta ("query is too old" / "query id is invalid").
// Port di handlers.safe_answer_callback.
func SafeAnswerCallback(b *gotgbot.Bot, cq *gotgbot.CallbackQuery) error {
	if cq == nil {
		return nil
	}
	_, err := cq.Answer(b, nil)
	if err == nil {
		return nil
	}
	var tgErr *gotgbot.TelegramError
	if errors.As(err, &tgErr) && tgErr.Code == 400 {
		desc := strings.ToLower(tgErr.Description)
		if strings.Contains(desc, "too old") || strings.Contains(desc, "query id is invalid") {
			slog.Debug("Callback query scaduta (ignorata)", "error", err)
			return nil
		}
	}
	return err
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
