// Package rateupdate contiene gli handler dei pulsanti "Aggiorna tariffe" e
// "No grazie" mostrati nelle notifiche di tariffe migliori.
//
// Quando l'utente adotta una nuova offerta a prezzo fisso, la vecchia
// scadenza viene azzerata (l'offerta è cambiata) e viene proposto, tramite
// una breve conversazione, di registrare la nuova data di attivazione. La
// coda dei servizi per cui chiedere la data è in user_data
// (scadenza_update_services, come []string).
//
// Gli handler restituiscono lo stato successivo o
// handlers.EndConversation(); gli altri errori arrivano all'error handler.
// Handlers.Conversation costruisce la conversazione e
// Handlers.RateUpdateNoHandler l'handler autonomo di "No grazie".
package rateupdate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/conversation"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/tg"
)

var logger = logging.Logger("handlers.rate_update")

const parseModeHTML = "HTML"

// Testo che viene sostituito nel messaggio dopo la scelta dell'utente.
const (
	// PromptText è la domanda in fondo alla notifica.
	PromptText = "👇 Vuoi aggiornare le tariffe memorizzate su OctoTracker con quelle nuove?"
	// ConfirmedText sostituisce PromptText dopo l'aggiornamento.
	ConfirmedText = "✅ Tariffe aggiornate!"
	// DeclinedText sostituisce PromptText dopo "No grazie" o senza tariffe
	// pendenti.
	DeclinedText = "🔧 Puoi sempre aggiornare le tariffe con /update."
	// updateErrorText sostituisce PromptText se l'aggiornamento fallisce.
	updateErrorText = "❌ Errore nell'aggiornamento. Riprova con /update."
)

// Messaggi della conversazione scadenza post-aggiornamento.
const (
	// MsgDateInvalid è l'errore per una data non valida.
	MsgDateInvalid = "❌ Data non valida. Usa il formato GG/MM/AAAA (es. 15/03/2025)"
	// ScadenzaAskText propone di registrare la nuova scadenza.
	ScadenzaAskText = "📅 Hai adottato una nuova offerta a <b>prezzo fisso</b>: la scadenza precedente " +
		"non è più valida.\n\nVuoi registrare la nuova data di scadenza per ricevere il " +
		"promemoria un mese prima?"
	// ScadenzaDeclinedText risponde a "No" / «Salta».
	ScadenzaDeclinedText = "👍 Ok! Se cambi idea puoi impostare la scadenza in qualsiasi momento con /update."
	// ScadenzaDoneText chiude la conversazione dopo l'ultima data.
	ScadenzaDoneText = "🔔 Perfetto, ti avviserò un mese prima della scadenza!"
)

// Callback data dei pulsanti.
const (
	CallbackYes          = "rate_update_yes"      // "✅ Aggiorna tariffe" della notifica
	CallbackNo           = "rate_update_no"       // "❌ No grazie" della notifica
	CallbackScadenzaYes  = "scadenza_update_yes"  // "✅ Sì" alla nuova scadenza
	CallbackScadenzaNo   = "scadenza_update_no"   // "❌ No" alla nuova scadenza
	CallbackScadenzaSkip = "skip_scadenza_update" // "⏭️ Salta" la data
)

// servicesKey è la chiave di user_data con la coda dei servizi fissi per
// cui chiedere la data.
const servicesKey = "scadenza_update_services"

// Stati della conversazione: il valore è il nome dello stato nella versione
// Python.
const (
	StateAskScadenza = "ASK_SCADENZA" // ScadenzaUpdateState.ASK (0)
	StateUpdateDate  = "UPDATE_DATE"  // ScadenzaUpdateState.DATE (1)
)

// Store è il sottoinsieme di *database.Store usato dagli handler.
type Store interface {
	LoadPendingRates(userID string) *database.PendingRates
	ApplyPendingRates(userID string) (bool, string)
	ClearPendingRates(userID string) bool
	UpdateUserScadenza(userID, servizio, scadenza string) bool
}

var _ Store = (*database.Store)(nil)

// Handlers raccoglie le dipendenze degli handler di aggiornamento tariffe.
type Handlers struct {
	// Store è il database (tariffe pendenti e scadenze).
	Store Store
	// UserData sono i dati di conversazione, condivisi con le altre
	// conversazioni del bot.
	UserData *tg.UserData
}

// ========== HELPER ==========

// updatedFissaServices restituisce i servizi fissi effettivamente
// aggiornati dall'offerta: l'elenco ordinato ("luce", "gas") dei servizi in
// UpdatedServices con tipo "fissa".
func updatedFissaServices(pending *database.PendingRates) []string {
	if pending == nil {
		return nil
	}
	tipo := map[string]string{"luce": pending.Luce.Tipo}
	if pending.Gas != nil {
		tipo["gas"] = pending.Gas.Tipo
	}
	var services []string
	for _, servizio := range []string{"luce", "gas"} {
		if slices.Contains(pending.UpdatedServices, servizio) && tipo[servizio] == "fissa" {
			services = append(services, servizio)
		}
	}
	return services
}

// answer risponde alla callback propagando ogni errore (anche la query
// scaduta, a differenza di tg.SafeAnswerCallback).
func answer(b *gotgbot.Bot, query *gotgbot.CallbackQuery) error {
	if query == nil {
		return tg.ErrNoCallback
	}
	_, err := query.Answer(b, nil)
	return err
}

// askScadenzaKeyboard è la tastiera Sì/No per la domanda sulla scadenza.
func askScadenzaKeyboard() gotgbot.InlineKeyboardMarkup {
	return gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{{
		{Text: "✅ Sì", CallbackData: CallbackScadenzaYes},
		{Text: "❌ No", CallbackData: CallbackScadenzaNo},
	}}}
}

// datePrompt restituisce testo e tastiera per chiedere la data di
// attivazione di un servizio.
func datePrompt(servizio string) (string, gotgbot.InlineKeyboardMarkup) {
	keyboard := gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{{
		{Text: "⏭️ Salta", CallbackData: CallbackScadenzaSkip},
	}}}
	text := "📅 Inserisci la <b>data di attivazione</b> della tua nuova offerta " + servizio + ".\n\n" +
		"Calcolerò la scadenza a 12 mesi e ti avviserò un mese prima.\n\n" +
		"💬 Formato GG/MM/AAAA (es. 15/03/2025), oppure premi «Salta»."
	return text, keyboard
}

// services restituisce la coda dei servizi in user_data (vuota se assente).
func (h *Handlers) services(userID int64) []string {
	v, _ := h.UserData.Get(userID, servicesKey)
	services, _ := v.([]string)
	return services
}

// setServices salva in user_data una copia della coda, così non condivide
// memoria con il chiamante.
func (h *Handlers) setServices(userID int64, services []string) {
	h.UserData.Set(userID, servicesKey, slices.Clone(services))
}

// replacePrompt restituisce l'HTML della notifica con PromptText sostituito.
func replacePrompt(query *gotgbot.CallbackQuery, with string) (string, error) {
	html, err := textHTML(tg.CallbackMessage(query))
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(html, PromptText, with), nil
}

// promptNextService chiede la data per il prossimo servizio in coda, o
// termina se non ce ne sono. Restituisce UPDATE_DATE se resta un servizio
// da processare, END altrimenti.
func (h *Handlers) promptNextService(b *gotgbot.Bot, ctx *ext.Context, userID int64) error {
	services := h.services(userID)
	if len(services) == 0 {
		h.UserData.Delete(userID, servicesKey)
		if err := tg.Reply(b, ctx.EffectiveMessage, ScadenzaDoneText, parseModeHTML, nil); err != nil {
			return err
		}
		return handlers.EndConversation()
	}

	text, keyboard := datePrompt(services[0])
	if err := tg.Reply(b, ctx.EffectiveMessage, text, parseModeHTML, &keyboard); err != nil {
		return err
	}
	return handlers.NextConversationState(StateUpdateDate)
}

// ========== HANDLERS ==========

// RateUpdateYes è la callback del pulsante "Aggiorna tariffe": applica le
// tariffe pendenti e, se l'utente ha adottato un'offerta fissa, avvia la
// conversazione per registrare la nuova scadenza (stato ASK_SCADENZA);
// altrimenti termina.
func (h *Handlers) RateUpdateYes(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := answer(b, query); err != nil {
		return err
	}
	userID := strconv.FormatInt(query.From.Id, 10)

	// Ispeziona le pending prima di applicarle (apply le rimuove)
	pending := h.Store.LoadPendingRates(userID)

	// Applica tariffe pendenti in modo atomico (singola transazione DB)
	success, reason := h.Store.ApplyPendingRates(userID)

	var replacement string
	switch {
	case success:
		replacement = ConfirmedText
	case reason == database.ApplyNoPending:
		logger.Warn(fmt.Sprintf("⚠️ Utente %s: nessuna tariffa pendente trovata", userID))
		replacement = DeclinedText
	case reason == database.ApplyNoUser:
		logger.Error(fmt.Sprintf("❌ Utente %s: non trovato nel database", userID))
		replacement = DeclinedText
	default:
		logger.Error(fmt.Sprintf("❌ Utente %s: errore aggiornamento tariffe (%s)", userID, reason))
		replacement = updateErrorText
	}
	newText, err := replacePrompt(query, replacement)
	if err != nil {
		return err
	}

	if err := tg.EditText(b, query, newText, parseModeHTML, nil); err != nil {
		return err
	}

	// Se ha adottato una o più offerte fisse, proponi di registrare la scadenza
	if success {
		if fissaServices := updatedFissaServices(pending); len(fissaServices) > 0 {
			h.setServices(query.From.Id, fissaServices)
			keyboard := askScadenzaKeyboard()
			if err := tg.Reply(b, tg.CallbackMessage(query), ScadenzaAskText, parseModeHTML, &keyboard); err != nil {
				return err
			}
			return handlers.NextConversationState(StateAskScadenza)
		}
	}

	return handlers.EndConversation()
}

// RateUpdateNo è la callback del pulsante "No grazie": rimuove le tariffe
// pendenti e aggiorna il messaggio rimuovendo i pulsanti. È fuori dalla
// conversazione e non restituisce alcuno stato.
func (h *Handlers) RateUpdateNo(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := answer(b, query); err != nil {
		return err
	}
	userID := strconv.FormatInt(query.From.Id, 10)
	logger.Info(fmt.Sprintf("ℹ️ Utente %s: ha rifiutato aggiornamento tariffe via bottone", userID))

	// Rimuovi tariffe pendenti
	h.Store.ClearPendingRates(userID)

	// Aggiorna messaggio rimuovendo i pulsanti
	newText, err := replacePrompt(query, DeclinedText)
	if err != nil {
		return err
	}
	return tg.EditText(b, query, newText, parseModeHTML, nil)
}

// ScadenzaUpdateChoice gestisce la risposta Sì/No alla proposta di
// registrare la scadenza: "No" termina, "Sì" chiede la data del primo
// servizio in coda (stato UPDATE_DATE).
func (h *Handlers) ScadenzaUpdateChoice(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}

	if query.Data == CallbackScadenzaNo {
		h.UserData.Delete(query.From.Id, servicesKey)
		if err := tg.EditText(b, query, ScadenzaDeclinedText, parseModeHTML, nil); err != nil {
			return err
		}
		return handlers.EndConversation()
	}

	// Sì → chiedi la data per il primo servizio (rimuove i bottoni Sì/No)
	services := h.services(query.From.Id)
	if len(services) == 0 {
		return handlers.EndConversation()
	}

	text, keyboard := datePrompt(services[0])
	if err := tg.EditText(b, query, text, parseModeHTML, &keyboard); err != nil {
		return err
	}
	return handlers.NextConversationState(StateUpdateDate)
}

// UpdateScadenzaDate riceve la data di attivazione e salva la scadenza del
// servizio corrente, poi passa al servizio successivo. Una data non valida
// resta nello stato UPDATE_DATE.
func (h *Handlers) UpdateScadenzaDate(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.Message == nil || ctx.Message.Text == "" {
		return handlers.NextConversationState(StateUpdateDate)
	}
	userID, err := tg.UserID(ctx)
	if err != nil {
		return err
	}
	uid := ctx.EffectiveUser.Id

	services := h.services(uid)
	if len(services) == 0 {
		return handlers.EndConversation()
	}

	activation, ok := dateutil.ParseActivationDate(ctx.Message.Text)
	if !ok {
		if err := tg.Reply(b, ctx.Message, MsgDateInvalid, "", nil); err != nil {
			return err
		}
		return handlers.NextConversationState(StateUpdateDate)
	}

	servizio := services[0]
	scadenza := dateutil.ComputeExpiryDate(activation)
	h.Store.UpdateUserScadenza(userID, servizio, dateutil.FormatISODate(scadenza))

	msg := "✅ Offerta " + servizio + ": scadenza impostata al <b>" + dateutil.FormatDateDisplay(scadenza) + "</b>."
	if err := tg.Reply(b, ctx.Message, msg, parseModeHTML, nil); err != nil {
		return err
	}

	// Passa al servizio successivo (se presente)
	h.setServices(uid, services[1:])
	return h.promptNextService(b, ctx, uid)
}

// SkipScadenzaDate salta la data del servizio corrente e passa
// eventualmente al successivo.
func (h *Handlers) SkipScadenzaDate(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}
	uid := query.From.Id

	services := h.services(uid)
	if len(services) > 0 {
		services = services[1:]
		h.setServices(uid, services)
	}

	if len(services) > 0 {
		return h.promptNextService(b, ctx, uid)
	}

	h.UserData.Delete(uid, servicesKey)
	if err := tg.EditText(b, query, ScadenzaDeclinedText, parseModeHTML, nil); err != nil {
		return err
	}
	return handlers.EndConversation()
}

// ========== CONVERSAZIONE ==========

// Conversation costruisce la conversazione di aggiornamento tariffe: il
// pulsante "Aggiorna tariffe" la avvia, ASK_SCADENZA accetta Sì/No,
// UPDATE_DATE «Salta» o il testo con la data (anche modificato o business,
// che UpdateScadenzaDate ignora restando in UPDATE_DATE). fallbacks è
// fornito dall'applicazione (/cancel di commands). Lo stato è in memoria
// per utente e chat.
func (h *Handlers) Conversation(fallbacks []ext.Handler) handlers.Conversation {
	return handlers.NewConversation(
		[]ext.Handler{
			handlers.NewCallback(tg.CallbackPattern(`^`+CallbackYes+`$`), h.RateUpdateYes),
		},
		map[string][]ext.Handler{
			StateAskScadenza: {
				handlers.NewCallback(tg.CallbackPattern(`^scadenza_update_(yes|no)$`), h.ScadenzaUpdateChoice),
			},
			StateUpdateDate: {
				handlers.NewCallback(tg.CallbackPattern(`^`+CallbackScadenzaSkip+`$`), h.SkipScadenzaDate),
				handlers.NewMessage(tg.TextNotCommand, h.UpdateScadenzaDate).SetAllowEdited(true).SetAllowBusiness(true),
			},
		},
		&handlers.ConversationOpts{
			Fallbacks:    fallbacks,
			StateStorage: conversation.NewInMemoryStorage(conversation.KeyStrategySenderAndChat),
		},
	)
}

// RateUpdateNoHandler restituisce l'handler autonomo del pulsante "No
// grazie", da registrare dopo la conversazione.
func (h *Handlers) RateUpdateNoHandler() handlers.CallbackQuery {
	return handlers.NewCallback(tg.CallbackPattern(`^`+CallbackNo+`$`), h.RateUpdateNo)
}
