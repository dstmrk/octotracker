package app

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"runtime/debug"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/tg"
)

// logger è il logger del bot; il nome "__main__" è quello storico dei log
// di produzione.
var logger = logging.Logger("__main__")

// MaxAlertMessageLength è la lunghezza massima (in caratteri) del
// messaggio d'errore nell'alert all'admin.
const MaxAlertMessageLength = 200

// MaxAlertUpdateLength è la lunghezza massima (in caratteri) della riga
// "Update:" dell'alert: con il resto del testo l'alert resta sotto il limite
// di 4096 caratteri di Telegram anche con il messaggio d'errore escapato
// (200 caratteri possono diventare 1000 con &#34;).
const MaxAlertUpdateLength = 2800

// ErrorHandler gestisce gli errori restituiti dagli handler:
//
//   - timeout ed errori di rete (inclusi i 400 dell'API): solo un warning;
//   - rate limit: attesa di retry_after secondi;
//   - ogni altro errore: log e, se ADMIN_USER_ID è impostato, alert
//     all'admin.
type ErrorHandler struct {
	// AdminUserID è ADMIN_USER_ID: se non vuoto, gli errori non gestiti
	// vengono segnalati all'admin con un messaggio Telegram.
	AdminUserID string
	// Sleep attende la durata indicata (rate limit); nil significa
	// time.Sleep.
	Sleep func(time.Duration)
}

// HandleError implementa ext.DispatcherErrorHandler: dopo la gestione
// l'update passa al gruppo successivo.
func (h *ErrorHandler) HandleError(b *gotgbot.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
	h.handle(b, contextUpdate(ctx), err, "")
	return ext.DispatcherActionNoop
}

// HandlePanic implementa ext.DispatcherPanicHandler: un panic in un handler
// viene gestito come gli altri errori, con lo stack nel log.
func (h *ErrorHandler) HandlePanic(b *gotgbot.Bot, ctx *ext.Context, r any) {
	h.handle(b, contextUpdate(ctx), panicError(r), string(debug.Stack()))
}

func contextUpdate(ctx *ext.Context) *gotgbot.Update {
	if ctx == nil {
		return nil
	}
	return ctx.Update
}

func (h *ErrorHandler) handle(b *gotgbot.Bot, update *gotgbot.Update, err error, stack string) {
	msg := tg.ErrorString(err)
	var attrs []any
	if stack != "" {
		attrs = []any{"stack", stack}
	}
	logger.Error(fmt.Sprintf("❌ Errore: %s", msg), attrs...)

	if seconds := retryAfter(err); seconds > 0 {
		// Rate limit di Telegram - dobbiamo aspettare
		logger.Warn(fmt.Sprintf("⏱️  Rate limit: attendo %ds", seconds))
		h.sleep(time.Duration(seconds) * time.Second)
		return
	}
	if isNetworkError(err) {
		logger.Warn("⏱️  Timeout/errore di rete - probabilmente connessione lenta. Il bot riproverà automaticamente.")
		return
	}

	// Errore non gestito - potenzialmente critico
	kind := errorType(err)
	logger.Error(fmt.Sprintf("⚠️  Errore non gestito: %s", kind))
	if h.AdminUserID == "" || b == nil {
		return
	}
	// Tipo e messaggio sono escapati dopo il troncamento, così un'entità
	// non viene tagliata a metà.
	alert := fmt.Sprintf("🚨 <b>Errore Bot OctoTracker</b>\n\n"+
		"<b>Tipo:</b> %s\n"+
		"<b>Messaggio:</b> %s\n"+
		"<b>Update:</b> %s",
		html.EscapeString(kind), html.EscapeString(truncateRunes(msg, MaxAlertMessageLength)),
		truncateUpdate(updateString(update)))
	if sendErr := sendAdminAlert(b, h.AdminUserID, alert); sendErr != nil {
		logger.Error(fmt.Sprintf("❌ Errore invio alert admin: %s", tg.ErrorString(sendErr)))
		return
	}
	logger.Info(fmt.Sprintf("📨 Alert errore inviato all'admin %s", h.AdminUserID))
}

func (h *ErrorHandler) sleep(d time.Duration) {
	if h.Sleep != nil {
		h.Sleep(d)
		return
	}
	time.Sleep(d)
}

// sendAdminAlert invia l'alert in HTML; ADMIN_USER_ID viene inviato così
// com'è (stringa), quindi può essere anche un @username.
func sendAdminAlert(b *gotgbot.Bot, adminUserID, text string) error {
	params := map[string]any{
		"chat_id":    adminUserID,
		"text":       text,
		"parse_mode": "HTML",
	}
	_, err := b.RequestWithContext(context.Background(), "sendMessage", params, nil)
	return err
}

// updateString rappresenta l'update per la riga "Update:" dell'alert, in
// JSON: json.Marshal sostituisce <, > e & con sequenze di escape Unicode,
// così il testo resta HTML valido. Un update assente è "None".
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

// truncateUpdate limita la riga "Update:" a MaxAlertUpdateLength caratteri,
// con il suffisso "…" se è stata accorciata. Il JSON ha già <, > e & in forma
// \uXXXX, quindi il taglio non può produrre HTML non valido.
func truncateUpdate(s string) string {
	if r := []rune(s); len(r) > MaxAlertUpdateLength {
		return string(r[:MaxAlertUpdateLength]) + "…"
	}
	return s
}

// truncateRunes restituisce i primi n caratteri di s.
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
