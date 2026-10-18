package app

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/logging"
)

// logger replica logging.getLogger(__name__) di bot.py, avviato come
// script (python -u bot.py): il nome del modulo era "__main__".
var logger = logging.Logger("__main__")

// MaxAlertMessageLength è la lunghezza massima (in caratteri) del
// messaggio d'errore nell'alert all'admin (str(error)[:200]).
const MaxAlertMessageLength = 200

// ErrorHandler è il port di error_handler di bot.py: gestisce gli errori
// restituiti dagli handler (le eccezioni che in Python arrivano all'error
// handler dell'applicazione).
type ErrorHandler struct {
	// AdminUserID è ADMIN_USER_ID: se non vuoto, gli errori non gestiti
	// vengono segnalati all'admin con un messaggio Telegram.
	AdminUserID string
	// Sleep attende la durata indicata (asyncio.sleep per il rate limit);
	// nil significa time.Sleep.
	Sleep func(time.Duration)
}

// HandleError implementa ext.DispatcherErrorHandler: dopo la gestione
// l'update passa al gruppo successivo, come in PTB.
func (h *ErrorHandler) HandleError(b *gotgbot.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
	var update *gotgbot.Update
	if ctx != nil {
		update = ctx.Update
	}
	h.handle(b, update, err, "")
	return ext.DispatcherActionNoop
}

// HandlePanic implementa ext.DispatcherPanicHandler: un panic in un handler
// è l'equivalente di un'eccezione Python non prevista, quindi viene
// recuperato e gestito come gli altri errori (con lo stack nel log, al
// posto del traceback).
func (h *ErrorHandler) HandlePanic(b *gotgbot.Bot, ctx *ext.Context, r any) {
	var update *gotgbot.Update
	if ctx != nil {
		update = ctx.Update
	}
	h.handle(b, update, panicError(r), string(debug.Stack()))
}

// handle replica il corpo di error_handler.
func (h *ErrorHandler) handle(b *gotgbot.Bot, update *gotgbot.Update, err error, stack string) {
	e := describe(err)

	// Log base (in Python con stack trace completo: exc_info=True)
	if stack != "" {
		logger.Error(fmt.Sprintf("❌ Errore: %s", e.msg), "stack", stack)
	} else {
		logger.Error(fmt.Sprintf("❌ Errore: %s", e.msg))
	}

	// Gestione per tipo di errore
	switch {
	case isInstance(e.class, "NetworkError"):
		// TimedOut, NetworkError e BadRequest (sottoclasse di NetworkError in PTB)
		logger.Warn("⏱️  Timeout/errore di rete - probabilmente connessione lenta. Il bot riproverà automaticamente.")

	case e.class == "RetryAfter":
		// Rate limit di Telegram - dobbiamo aspettare
		retryAfter := retryAfterSeconds(err)
		logger.Warn(fmt.Sprintf("⏱️  Rate limit: attendo %ds", retryAfter))
		h.sleep(time.Duration(retryAfter) * time.Second)

	default:
		// Errore non gestito - potenzialmente critico
		logger.Error(fmt.Sprintf("⚠️  Errore non gestito: %s", e.class))

		// Invia alert all'admin se configurato
		if h.AdminUserID != "" && b != nil {
			errorMsg := fmt.Sprintf("🚨 <b>Errore Bot OctoTracker</b>\n\n"+
				"<b>Tipo:</b> %s\n"+
				"<b>Messaggio:</b> %s\n"+
				"<b>Update:</b> %s",
				e.class, truncateRunes(e.msg, MaxAlertMessageLength), updateString(update))
			if sendErr := sendAdminAlert(b, h.AdminUserID, errorMsg); sendErr != nil {
				logger.Error(fmt.Sprintf("❌ Errore invio alert admin: %s", describe(sendErr).msg))
				return
			}
			logger.Info(fmt.Sprintf("📨 Alert errore inviato all'admin %s", h.AdminUserID))
		}
	}
}

func (h *ErrorHandler) sleep(d time.Duration) {
	if h.Sleep != nil {
		h.Sleep(d)
		return
	}
	time.Sleep(d)
}

// sendAdminAlert replica bot.send_message(chat_id=ADMIN_USER_ID, text=...,
// parse_mode=HTML): ADMIN_USER_ID viene inviato così com'è (stringa), come
// fa PTB.
func sendAdminAlert(b *gotgbot.Bot, adminUserID, text string) error {
	params := map[string]any{
		"chat_id":    adminUserID,
		"text":       text,
		"parse_mode": "HTML",
	}
	_, err := b.RequestWithContext(context.Background(), "sendMessage", params, nil)
	return err
}

// updateString rappresenta l'update per la riga "Update:" dell'alert. Python
// interpola repr(update), non riproducibile: qui l'update è serializzato in
// JSON (json.Marshal sostituisce <, > e & con sequenze di escape Unicode,
// così il testo resta HTML valido).
// Un update assente è "None", come in Python.
func updateString(update *gotgbot.Update) string {
	if update == nil {
		return "None"
	}
	raw, err := json.Marshal(update)
	if err != nil {
		return fmt.Sprintf("(update non serializzabile: %v)", err)
	}
	return string(raw)
}

// truncateRunes replica s[:n] di Python (n code point).
func truncateRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
