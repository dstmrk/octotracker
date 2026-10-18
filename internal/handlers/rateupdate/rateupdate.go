// Package rateupdate è il port di handlers/rate_update.py: gli handler dei
// pulsanti "Aggiorna tariffe" e "No grazie" mostrati nelle notifiche di
// tariffe migliori.
//
// Quando l'utente adotta una nuova offerta a prezzo fisso, la vecchia
// scadenza viene azzerata (l'offerta è cambiata) e viene proposto, tramite
// una breve conversazione, di registrare la nuova data di attivazione.
//
// Testi, tastiere, callback_data e stati sono identici alla versione
// Python; la coda dei servizi per cui chiedere la data è in user_data sotto
// la stessa chiave (scadenza_update_services, come []string). Gli handler
// restituiscono, come in Python, lo stato successivo o
// handlers.EndConversation() (nil dove Python restituisce None); gli altri
// errori corrispondono alle eccezioni che in Python arrivano all'error
// handler. Handlers.Conversation costruisce la conversazione e
// Handlers.RateUpdateNoHandler l'handler autonomo di "No grazie", come in
// bot.py.
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

// logger replica logging.getLogger(__name__) di handlers/rate_update.py.
var logger = logging.Logger("handlers.rate_update")

// parseModeHTML corrisponde a ParseMode.HTML di python-telegram-bot.
const parseModeHTML = "HTML"

// Testo che viene sostituito nel messaggio dopo la scelta dell'utente.
const (
	// PromptText è la domanda in fondo alla notifica (PROMPT_TEXT).
	PromptText = "👇 Vuoi aggiornare le tariffe memorizzate su OctoTracker con quelle nuove?"
	// ConfirmedText sostituisce PromptText dopo l'aggiornamento (CONFIRMED_TEXT).
	ConfirmedText = "✅ Tariffe aggiornate!"
	// DeclinedText sostituisce PromptText dopo "No grazie" o senza tariffe
	// pendenti (DECLINED_TEXT).
	DeclinedText = "🔧 Puoi sempre aggiornare le tariffe con /update."
	// updateErrorText sostituisce PromptText se l'aggiornamento fallisce.
	updateErrorText = "❌ Errore nell'aggiornamento. Riprova con /update."
)

// Messaggi della conversazione scadenza post-aggiornamento.
const (
	// MsgDateInvalid è l'errore per una data non valida (MSG_DATE_INVALID).
	MsgDateInvalid = "❌ Data non valida. Usa il formato GG/MM/AAAA (es. 15/03/2025)"
	// ScadenzaAskText propone di registrare la nuova scadenza (SCADENZA_ASK_TEXT).
	ScadenzaAskText = "📅 Hai adottato una nuova offerta a <b>prezzo fisso</b>: la scadenza precedente " +
		"non è più valida.\n\nVuoi registrare la nuova data di scadenza per ricevere il " +
		"promemoria un mese prima?"
	// ScadenzaDeclinedText risponde a "No" / «Salta» (SCADENZA_DECLINED_TEXT).
	ScadenzaDeclinedText = "👍 Ok! Se cambi idea puoi impostare la scadenza in qualsiasi momento con /update."
	// ScadenzaDoneText chiude la conversazione dopo l'ultima data (SCADENZA_DONE_TEXT).
	ScadenzaDoneText = "🔔 Perfetto, ti avviserò un mese prima della scadenza!"
)

// Callback data dei pulsanti.
const (
	CallbackYes          = "rate_update_yes"      // "✅ Aggiorna tariffe" della notifica (CALLBACK_YES)
	CallbackNo           = "rate_update_no"       // "❌ No grazie" della notifica (CALLBACK_NO)
	CallbackScadenzaYes  = "scadenza_update_yes"  // CALLBACK_SCADENZA_YES
	CallbackScadenzaNo   = "scadenza_update_no"   // CALLBACK_SCADENZA_NO
	CallbackScadenzaSkip = "skip_scadenza_update" // CALLBACK_SCADENZA_SKIP
)

// servicesKey è la chiave di user_data con la coda dei servizi fissi per
// cui chiedere la data (_SERVICES_KEY).
const servicesKey = "scadenza_update_services"

// Stati della conversazione (ScadenzaUpdateState, esposti in bot.py come
// ASK_SCADENZA e UPDATE_DATE).
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
	// UserData è l'equivalente di context.user_data, condiviso con le
	// altre conversazioni del bot.
	UserData *tg.UserData
}

// ========== HELPER ==========

// effectiveUserID replica str(update.effective_user.id).
func effectiveUserID(ctx *ext.Context) (string, error) {
	if ctx.EffectiveUser == nil {
		return "", attributeError("id")
	}
	return strconv.FormatInt(ctx.EffectiveUser.Id, 10), nil
}

// updatedFissaServices restituisce i servizi fissi effettivamente
// aggiornati dall'offerta (_updated_fissa_services): l'elenco ordinato
// ("luce", "gas") dei servizi in updated_services con tipo "fissa".
func updatedFissaServices(pending *database.PendingRates) []string {
	if pending == nil {
		return []string{}
	}
	tipo := map[string]string{"luce": pending.Luce.Tipo}
	if pending.Gas != nil {
		tipo["gas"] = pending.Gas.Tipo
	}
	services := []string{}
	for _, servizio := range []string{"luce", "gas"} {
		if slices.Contains(pending.UpdatedServices, servizio) && tipo[servizio] == "fissa" {
			services = append(services, servizio)
		}
	}
	return services
}

// askScadenzaKeyboard è la tastiera Sì/No per la domanda sulla scadenza
// (_ask_scadenza_keyboard).
func askScadenzaKeyboard() gotgbot.InlineKeyboardMarkup {
	return gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{{
		{Text: "✅ Sì", CallbackData: CallbackScadenzaYes},
		{Text: "❌ No", CallbackData: CallbackScadenzaNo},
	}}}
}

// datePrompt restituisce testo e tastiera per chiedere la data di
// attivazione di un servizio (_date_prompt).
func datePrompt(servizio string) (string, gotgbot.InlineKeyboardMarkup) {
	keyboard := gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{{
		{Text: "⏭️ Salta", CallbackData: CallbackScadenzaSkip},
	}}}
	text := "📅 Inserisci la <b>data di attivazione</b> della tua nuova offerta " + servizio + ".\n\n" +
		"Calcolerò la scadenza a 12 mesi e ti avviserò un mese prima.\n\n" +
		"💬 Formato GG/MM/AAAA (es. 15/03/2025), oppure premi «Salta»."
	return text, keyboard
}

// services restituisce context.user_data.get(_SERVICES_KEY, []).
func (h *Handlers) services(ctx *ext.Context) ([]string, error) {
	if ctx.EffectiveUser == nil {
		return nil, attributeError("get")
	}
	v, _ := h.UserData.Get(ctx.EffectiveUser.Id, servicesKey)
	services, _ := v.([]string)
	return services, nil
}

// setServices replica context.user_data[_SERVICES_KEY] = services (una
// copia, così la coda in user_data non condivide memoria con il chiamante).
func (h *Handlers) setServices(ctx *ext.Context, services []string) {
	h.UserData.Set(ctx.EffectiveUser.Id, servicesKey, slices.Clone(services))
}

// replacePrompt replica query.message.text_html.replace(PROMPT_TEXT, with).
func replacePrompt(query *gotgbot.CallbackQuery, with string) (string, error) {
	html, err := textHTML(callbackMessage(query))
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(html, PromptText, with), nil
}

// promptNextService chiede la data per il prossimo servizio in coda, o
// termina se non ce ne sono (_prompt_next_service). Restituisce
// UPDATE_DATE se resta un servizio da processare, END altrimenti.
func (h *Handlers) promptNextService(b *gotgbot.Bot, ctx *ext.Context) error {
	services, err := h.services(ctx)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		h.UserData.Delete(ctx.EffectiveUser.Id, servicesKey)
		if err := replyText(b, effectiveMessage(ctx), ScadenzaDoneText, parseModeHTML, nil); err != nil {
			return err
		}
		return handlers.EndConversation()
	}

	text, keyboard := datePrompt(services[0])
	if err := replyText(b, effectiveMessage(ctx), text, parseModeHTML, &keyboard); err != nil {
		return err
	}
	return handlers.NextConversationState(StateUpdateDate)
}

// ========== HANDLERS ==========

// RateUpdateYes è la callback del pulsante "Aggiorna tariffe"
// (rate_update_yes): applica le tariffe pendenti e, se l'utente ha
// adottato un'offerta fissa, avvia la conversazione per registrare la
// nuova scadenza (stato ASK_SCADENZA); altrimenti termina.
func (h *Handlers) RateUpdateYes(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := answer(b, query); err != nil {
		return err
	}

	userID, err := effectiveUserID(ctx)
	if err != nil {
		return err
	}

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

	if err := editMessageText(b, query, newText, parseModeHTML, nil); err != nil {
		return err
	}

	// Se ha adottato una o più offerte fisse, proponi di registrare la scadenza
	if success {
		if fissaServices := updatedFissaServices(pending); len(fissaServices) > 0 {
			h.setServices(ctx, fissaServices)
			keyboard := askScadenzaKeyboard()
			if err := replyText(b, callbackMessage(query), ScadenzaAskText, parseModeHTML, &keyboard); err != nil {
				return err
			}
			return handlers.NextConversationState(StateAskScadenza)
		}
	}

	return handlers.EndConversation()
}

// RateUpdateNo è la callback del pulsante "No grazie" (rate_update_no):
// rimuove le tariffe pendenti e aggiorna il messaggio rimuovendo i
// pulsanti. Come in Python non restituisce alcuno stato (None).
func (h *Handlers) RateUpdateNo(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := answer(b, query); err != nil {
		return err
	}

	userID, err := effectiveUserID(ctx)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("ℹ️ Utente %s: ha rifiutato aggiornamento tariffe via bottone", userID))

	// Rimuovi tariffe pendenti
	h.Store.ClearPendingRates(userID)

	// Aggiorna messaggio rimuovendo i pulsanti
	newText, err := replacePrompt(query, DeclinedText)
	if err != nil {
		return err
	}
	return editMessageText(b, query, newText, parseModeHTML, nil)
}

// ScadenzaUpdateChoice gestisce la risposta Sì/No alla proposta di
// registrare la scadenza (scadenza_update_choice): "No" termina, "Sì"
// chiede la data del primo servizio in coda (stato UPDATE_DATE).
func (h *Handlers) ScadenzaUpdateChoice(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := safeAnswerCallback(b, query); err != nil {
		return err
	}

	if query.Data == CallbackScadenzaNo {
		if ctx.EffectiveUser == nil {
			return attributeError("pop")
		}
		h.UserData.Delete(ctx.EffectiveUser.Id, servicesKey)
		if err := editMessageText(b, query, ScadenzaDeclinedText, parseModeHTML, nil); err != nil {
			return err
		}
		return handlers.EndConversation()
	}

	// Sì → chiedi la data per il primo servizio (rimuove i bottoni Sì/No)
	services, err := h.services(ctx)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return handlers.EndConversation()
	}

	text, keyboard := datePrompt(services[0])
	if err := editMessageText(b, query, text, parseModeHTML, &keyboard); err != nil {
		return err
	}
	return handlers.NextConversationState(StateUpdateDate)
}

// UpdateScadenzaDate riceve la data di attivazione e salva la scadenza del
// servizio corrente (update_scadenza_date), poi passa al servizio
// successivo. Una data non valida resta nello stato UPDATE_DATE.
func (h *Handlers) UpdateScadenzaDate(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.Message == nil || ctx.Message.Text == "" {
		return handlers.NextConversationState(StateUpdateDate)
	}

	services, err := h.services(ctx)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return handlers.EndConversation()
	}

	activation, ok := dateutil.ParseActivationDate(ctx.Message.Text)
	if !ok {
		if err := replyText(b, ctx.Message, MsgDateInvalid, "", nil); err != nil {
			return err
		}
		return handlers.NextConversationState(StateUpdateDate)
	}

	servizio := services[0]
	scadenza := dateutil.ComputeExpiryDate(activation)
	h.Store.UpdateUserScadenza(strconv.FormatInt(ctx.EffectiveUser.Id, 10), servizio, dateutil.FormatISODate(scadenza))

	msg := "✅ Offerta " + servizio + ": scadenza impostata al <b>" + dateutil.FormatDateDisplay(scadenza) + "</b>."
	if err := replyText(b, ctx.Message, msg, parseModeHTML, nil); err != nil {
		return err
	}

	// Passa al servizio successivo (se presente)
	h.setServices(ctx, services[1:])
	return h.promptNextService(b, ctx)
}

// SkipScadenzaDate salta la data del servizio corrente e passa
// eventualmente al successivo (skip_scadenza_date).
func (h *Handlers) SkipScadenzaDate(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := safeAnswerCallback(b, query); err != nil {
		return err
	}

	services, err := h.services(ctx)
	if err != nil {
		return err
	}
	if len(services) > 0 {
		services = services[1:]
		h.setServices(ctx, services)
	}

	if len(services) > 0 {
		return h.promptNextService(b, ctx)
	}

	h.UserData.Delete(ctx.EffectiveUser.Id, servicesKey)
	if err := editMessageText(b, query, ScadenzaDeclinedText, parseModeHTML, nil); err != nil {
		return err
	}
	return handlers.EndConversation()
}

// ========== CONVERSAZIONE ==========

// Conversation costruisce il ConversationHandler di aggiornamento tariffe
// come in bot.py:
//
//	entry_points=[CallbackQueryHandler(rate_update_yes, pattern=f"^{CALLBACK_YES}$")]
//	ASK_SCADENZA: [CallbackQueryHandler(scadenza_update_choice, pattern=r"^scadenza_update_(yes|no)$")]
//	UPDATE_DATE:  [CallbackQueryHandler(skip_scadenza_date, pattern=f"^{CALLBACK_SCADENZA_SKIP}$"),
//	               MessageHandler(filters.TEXT & ~filters.COMMAND, update_scadenza_date)]
//	fallbacks=[CommandHandler("cancel", cancel_conversation)]
//
// fallbacks è fornito dall'applicazione (/cancel di commands). I pattern
// replicano re.match di Python ($ accetta anche un "\n" finale); il
// MessageHandler accetta anche messaggi modificati e business (filtri PTB),
// che UpdateScadenzaDate ignora restando in UPDATE_DATE. Lo stato è in
// memoria per utente e chat (per_user/per_chat di PTB).
func (h *Handlers) Conversation(fallbacks []ext.Handler) handlers.Conversation {
	return handlers.NewConversation(
		[]ext.Handler{
			handlers.NewCallback(tg.CallbackPattern(`^`+CallbackYes+`\n?$`), h.RateUpdateYes),
		},
		map[string][]ext.Handler{
			StateAskScadenza: {
				handlers.NewCallback(tg.CallbackPattern(`^scadenza_update_(yes|no)\n?$`), h.ScadenzaUpdateChoice),
			},
			StateUpdateDate: {
				handlers.NewCallback(tg.CallbackPattern(`^`+CallbackScadenzaSkip+`\n?$`), h.SkipScadenzaDate),
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
// grazie" (CallbackQueryHandler(rate_update_no, pattern=f"^{CALLBACK_NO}$")
// di bot.py), da registrare dopo la conversazione.
func (h *Handlers) RateUpdateNoHandler() handlers.CallbackQuery {
	return handlers.NewCallback(tg.CallbackPattern(`^`+CallbackNo+`\n?$`), h.RateUpdateNo)
}
