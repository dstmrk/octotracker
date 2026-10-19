// Package registration contiene la conversazione di registrazione (e
// aggiornamento) delle tariffe luce/gas degli utenti di OctoTracker.
//
// I dati inseriti sono salvati in tg.UserData con le chiavi della versione
// Python (numeri come float64, is_variabile come bool, il resto come
// stringhe).
//
// Ogni handler è un metodo di Handlers con la firma gotgbot
// func(b *gotgbot.Bot, ctx *ext.Context) error e restituisce lo stato
// successivo (handlers.NextConversationState) oppure
// handlers.EndConversation(); gli altri errori arrivano all'error handler
// dell'applicazione. La conversazione completa si ottiene con
// Handlers.Conversation.
package registration

import (
	"strconv"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/tg"
)

var logger = logging.Logger("handlers.registration")

const parseModeHTML = "HTML"

// Costanti messaggi.
const (
	// MsgHasGas è la domanda sulla fornitura gas (MSG_HAS_GAS).
	MsgHasGas = "Hai anche una fornitura gas attiva con Octopus Energy?"
	// MsgGasConsumo chiede il consumo annuo di gas (MSG_GAS_CONSUMO).
	MsgGasConsumo = "Inserisci il tuo consumo annuo di gas in Smc.\n\n💬 Esempio: 1200"
	// MsgDateInvalid è l'errore per una data di attivazione non valida (MSG_DATE_INVALID).
	MsgDateInvalid = "❌ Data non valida. Usa il formato GG/MM/AAAA (es. 15/03/2025)"

	// fonteOctopus chiude l'indicazione su come inserire i prezzi.
	fonteOctopus = "(come riportato sul sito Octopus Energy/ARERA)\n\n"
	// esempioConsumoFascia è l'esempio delle domande sui consumi per fascia.
	esempioConsumoFascia = "💬 Esempio: 900"
)

// Callback data per saltare l'inserimento della data di attivazione.
const (
	// SkipScadenzaLuce è la callback_data del pulsante «Salta» per la luce (SKIP_SCADENZA_LUCE).
	SkipScadenzaLuce = "skip_scadenza_luce"
	// SkipScadenzaGas è la callback_data del pulsante «Salta» per il gas (SKIP_SCADENZA_GAS).
	SkipScadenzaGas = "skip_scadenza_gas"
)

// Stati della conversazione di registrazione: il valore è il nome dello
// stato nella versione Python (tra parentesi il suo valore intero).
const (
	StateTipoTariffa       = "TIPO_TARIFFA"        // 0
	StateLuceTipoVariabile = "LUCE_TIPO_VARIABILE" // 1
	StateLuceEnergia       = "LUCE_ENERGIA"        // 2
	StateLuceComm          = "LUCE_COMM"           // 3
	StateVuoiConsumiLuce   = "VUOI_CONSUMI_LUCE"   // 7
	StateLuceConsumoF1     = "LUCE_CONSUMO_F1"     // 8
	StateLuceConsumoF2     = "LUCE_CONSUMO_F2"     // 9
	StateLuceConsumoF3     = "LUCE_CONSUMO_F3"     // 10
	StateLuceAttivazione   = "LUCE_ATTIVAZIONE"    // 14: data attivazione offerta fissa luce (reminder scadenza)
	StateHaGas             = "HA_GAS"              // 4
	StateGasTipo           = "GAS_TIPO"            // 13: scelta tipo tariffa gas (fissa/variabile)
	StateGasEnergia        = "GAS_ENERGIA"         // 5
	StateGasComm           = "GAS_COMM"            // 6
	StateGasAttivazione    = "GAS_ATTIVAZIONE"     // 15: data attivazione offerta fissa gas (reminder scadenza)
	StateVuoiConsumiGas    = "VUOI_CONSUMI_GAS"    // 11
	StateGasConsumo        = "GAS_CONSUMO"         // 12
)

// Chiavi di user_data usate dalla conversazione.
const (
	keyIsVariabile     = "is_variabile"
	keyLuceTipo        = "luce_tipo"
	keyLuceFascia      = "luce_fascia"
	keyLuceEnergia     = "luce_energia"
	keyLuceComm        = "luce_comm"
	keyLuceScadenza    = "luce_scadenza"
	keyLuceConsumoF1   = "luce_consumo_f1"
	keyLuceConsumoF2   = "luce_consumo_f2"
	keyLuceConsumoF3   = "luce_consumo_f3"
	keyGasTipo         = "gas_tipo"
	keyGasFascia       = "gas_fascia"
	keyGasEnergia      = "gas_energia"
	keyGasComm         = "gas_comm"
	keyGasScadenza     = "gas_scadenza"
	keyGasConsumoAnnuo = "gas_consumo_annuo"
)

// Handlers raccoglie le dipendenze degli handler di registrazione.
type Handlers struct {
	// Store è il database degli utenti.
	Store *database.Store
	// UserData sono i dati di conversazione (condivisi con le altre
	// conversazioni del bot).
	UserData *tg.UserData
	// TelegramChannel è TELEGRAM_CHANNEL senza spazi; vuoto se il canale
	// non è configurato.
	TelegramChannel string
}

// next restituisce il cambio di stato verso state.
func next(state string) error {
	return handlers.NextConversationState(state)
}

// userIDString è l'id utente come chiave del database.
func userIDString(id int64) string {
	return strconv.FormatInt(id, 10)
}

// ========== PROMPT BUILDERS ==========

// keyboard costruisce una tastiera inline con una sola riga di pulsanti
// (testo, callback_data).
func keyboard(buttons ...[2]string) gotgbot.InlineKeyboardMarkup {
	row := make([]gotgbot.InlineKeyboardButton, 0, len(buttons))
	for _, b := range buttons {
		row = append(row, gotgbot.InlineKeyboardButton{Text: b[0], CallbackData: b[1]})
	}
	return gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{row}}
}

// haGasKeyboard è la tastiera Sì/No della domanda MSG_HAS_GAS.
func haGasKeyboard() gotgbot.InlineKeyboardMarkup {
	return keyboard([2]string{"✅ Sì", "gas_si"}, [2]string{"❌ No", "gas_no"})
}

// consumiLucePrompt costruisce testo e tastiera per la domanda sui consumi
// luce.
func consumiLucePrompt() (string, gotgbot.InlineKeyboardMarkup) {
	kb := keyboard([2]string{"✅ Sì", "consumi_luce_si"}, [2]string{"❌ No", "consumi_luce_no"})
	text := "Vuoi indicare anche il tuo consumo annuale di energia elettrica (in kWh)?\n\n" +
		"💡 Serve solo per valutare meglio quando una tariffa può convenirti."
	return text, kb
}

// consumiGasPrompt costruisce testo e tastiera per la domanda sui consumi
// gas.
func consumiGasPrompt() (string, gotgbot.InlineKeyboardMarkup) {
	kb := keyboard([2]string{"✅ Sì", "consumi_gas_si"}, [2]string{"❌ No", "consumi_gas_no"})
	text := "Vuoi indicare anche il tuo consumo annuale di gas (in Smc)?\n\n" +
		"🔥 Serve solo per valutare meglio quando una tariffa può convenirti."
	return text, kb
}

// attivazionePrompt costruisce testo e tastiera per la domanda sulla data di
// attivazione. servizio è "luce" o "gas" (per personalizzare il testo),
// skipCallback la callback_data del pulsante «Salta».
func attivazionePrompt(servizio, skipCallback string) (string, gotgbot.InlineKeyboardMarkup) {
	kb := keyboard([2]string{"⏭️ Salta", skipCallback})
	text := "📅 <b>Reminder scadenza offerta</b>\n\n" +
		"Le offerte a prezzo fisso durano 12 mesi dall'attivazione: alla scadenza Octopus " +
		"Energy passa in automatico alla tariffa variabile del momento.\n\n" +
		"Se vuoi, indicami la <b>data di attivazione</b> della tua offerta " + servizio + " e ti " +
		"invierò un promemoria un mese prima della scadenza.\n\n" +
		"💬 Scrivila nel formato GG/MM/AAAA (es. 15/03/2025), oppure premi «Salta»."
	return text, kb
}

// ========== CONVERSATION HANDLERS ==========

// Start avvia la registrazione tariffe, entry point di /start e /update.
func (h *Handlers) Start(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.EffectiveUser == nil {
		return tg.ErrNoUser
	}
	userID := userIDString(ctx.EffectiveUser.Id)
	isUpdate := h.Store.UserExists(userID)

	// Reset context per nuova conversazione
	h.UserData.Clear(ctx.EffectiveUser.Id)

	replyMarkup := keyboard([2]string{"📊 Fissa", "tipo_fissa"}, [2]string{"📈 Variabile", "tipo_variabile"})

	// Link canale Telegram (se configurato)
	channelInfo := ""
	if h.TelegramChannel != "" {
		// Rimuovi @ iniziale per URL Telegram (https://t.me/ richiede solo il nome)
		channelName := strings.TrimLeft(h.TelegramChannel, "@")
		channelInfo = "Per avere aggiornamenti sulle nuove funzionalità, iscriviti al canale " +
			`<a href="https://t.me/` + channelName + `">@` + channelName + "</a>!\n\n"
	}

	var messaggio string
	if isUpdate {
		messaggio = "♻️ <b>Aggiorniamo le tue tariffe!</b>\n\n" +
			"Inserisci di nuovo i valori attuali così OctoTracker potrà confrontarli " +
			"con le nuove offerte di Octopus Energy.\n\n" +
			"Ti guiderò passo passo come la prima volta: prima la luce, poi (se ce l'hai) il gas.\n\n" +
			"👉 Iniziamo: che tipo di tariffa <b>luce</b> hai?"
	} else {
		messaggio = "🐙 <b>Benvenuto su OctoTracker!</b>\n\n" +
			"Questo bot controlla ogni giorno le tariffe di Octopus Energy e ti avvisa " +
			"se ne trova di più convenienti rispetto alle tue attuali.\n\n" +
			channelInfo +
			"Ti farò qualche semplice domanda per registrare le tue tariffe luce e (se ce l'hai) gas.\n" +
			"Rispondi passo passo ai messaggi: ci vorrà meno di un minuto. ⚡️\n\n" +
			"👉 Iniziamo: che tipo di tariffa <b>luce</b> hai?"
	}

	if err := tg.Reply(b, ctx.Message, messaggio, parseModeHTML, &replyMarkup); err != nil {
		return err
	}
	return next(StateTipoTariffa)
}

// TipoTariffa gestisce la scelta del tipo tariffa luce, Fissa/Variabile.
func (h *Handlers) TipoTariffa(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}
	uid := ctx.EffectiveUser.Id

	if query.Data == "tipo_fissa" {
		h.UserData.Set(uid, keyIsVariabile, false)
		h.UserData.Set(uid, keyLuceTipo, "fissa")
		h.UserData.Set(uid, keyLuceFascia, "monoraria")
		// gas_tipo e gas_fascia verranno chiesti separatamente in GAS_TIPO

		err := tg.EditText(b, query,
			"📊 <b>Tariffa Fissa</b>\n\n"+
				"Perfetto! Ora inserisci i dati della tua tariffa luce.\n\n"+
				"👉 Quanto paghi per la materia energia luce (€/kWh)?\n\n"+
				"ℹ️ Inserisci il prezzo <b>IVA e imposte escluse, perdite incluse</b> "+
				fonteOctopus+
				"💬 Esempio: 0,145",
			parseModeHTML, nil)
		if err != nil {
			return err
		}
		return next(StateLuceEnergia)
	}

	// tipo_variabile
	h.UserData.Set(uid, keyIsVariabile, true)

	replyMarkup := keyboard([2]string{"⏱️ Monoraria", "luce_mono"}, [2]string{"⏱️⏱️⏱️ Trioraria", "luce_tri"})
	err := tg.EditText(b, query,
		"📈 <b>Tariffa Variabile</b>\n\n"+
			"La tua tariffa luce è monoraria o trioraria (F1/F2/F3)?",
		parseModeHTML, &replyMarkup)
	if err != nil {
		return err
	}
	return next(StateLuceTipoVariabile)
}

// LuceTipoVariabile gestisce la scelta del tipo luce variabile,
// Monoraria/Trioraria.
func (h *Handlers) LuceTipoVariabile(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}
	uid := ctx.EffectiveUser.Id

	var tipoMsg string
	if query.Data == "luce_mono" {
		h.UserData.Set(uid, keyLuceTipo, "variabile")
		h.UserData.Set(uid, keyLuceFascia, "monoraria")
		tipoMsg = "monoraria (PUN)"
	} else { // luce_tri
		h.UserData.Set(uid, keyLuceTipo, "variabile")
		h.UserData.Set(uid, keyLuceFascia, "trioraria")
		tipoMsg = "trioraria (PUN)"
	}

	// gas_tipo e gas_fascia verranno chiesti separatamente in GAS_TIPO

	err := tg.EditText(b, query,
		"⚡ <b>Luce variabile "+tipoMsg+"</b>\n\n"+
			"Ora inserisci lo spread della tua tariffa rispetto al PUN.\n\n"+
			"ℹ️ Inserisci il valore <b>IVA e imposte escluse, perdite incluse</b> "+
			fonteOctopus+
			"💬 Esempio: se la tua tariffa è <b>PUN + 0,0088</b> €/kWh, scrivi <code>0,0088</code>",
		parseModeHTML, nil)
	if err != nil {
		return err
	}
	return next(StateLuceEnergia)
}

// numericStep raccoglie la logica comune agli step numerici: valida il
// testo del messaggio, risponde con l'errore (restando in state) oppure
// restituisce il valore valido. invalidMsg è la risposta per un testo che
// non è un numero. done è true quando l'handler deve terminare subito
// restituendo err.
func numericStep(b *gotgbot.Bot, msg *gotgbot.Message, state, invalidMsg string) (value float64, done bool, err error) {
	value, ok, errMsg := ValidateNumericInput(msg.Text)
	if ok {
		return value, false, nil
	}
	if errMsg == "" {
		errMsg = invalidMsg // Errore di conversione (non è un numero)
	}
	if err := tg.Reply(b, msg, errMsg, "", nil); err != nil {
		return 0, true, err
	}
	return 0, true, next(state)
}

// LuceEnergia salva il costo energia luce, spread o prezzo fisso.
func (h *Handlers) LuceEnergia(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.Message
	if msg == nil {
		return next(StateLuceEnergia)
	}
	uid := ctx.EffectiveUser.Id

	invalid := "❌ Inserisci un numero valido (es: 0,145)"
	if v, _ := h.UserData.Get(uid, keyIsVariabile); v == true {
		invalid = "❌ Inserisci un numero valido (es: 0,0088)"
	}
	value, done, err := numericStep(b, msg, StateLuceEnergia, invalid)
	if done {
		return err
	}

	h.UserData.Set(uid, keyLuceEnergia, value)
	if err := tg.Reply(b, msg,
		"Perfetto! Ora indica il costo di commercializzazione luce, in euro/anno.\n\n"+
			"💬 Esempio: 72 (se paghi 6 €/mese)",
		"", nil); err != nil {
		return err
	}
	return next(StateLuceComm)
}

// LuceComm salva il costo di commercializzazione luce.
//
// Se la tariffa è fissa chiede la data di attivazione (per il reminder di
// scadenza), altrimenti passa direttamente alla domanda sui consumi.
func (h *Handlers) LuceComm(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.Message
	if msg == nil {
		return next(StateLuceComm)
	}
	uid := ctx.EffectiveUser.Id

	value, done, err := numericStep(b, msg, StateLuceComm, "❌ Inserisci un numero valido (es: 96.50)")
	if done {
		return err
	}

	h.UserData.Set(uid, keyLuceComm, value)

	// Solo per tariffe fisse chiediamo la data di attivazione (reminder scadenza)
	if v, _ := h.UserData.Get(uid, keyLuceTipo); v == "fissa" {
		text, replyMarkup := attivazionePrompt("luce", SkipScadenzaLuce)
		if err := tg.Reply(b, msg, text, parseModeHTML, &replyMarkup); err != nil {
			return err
		}
		return next(StateLuceAttivazione)
	}

	text, replyMarkup := consumiLucePrompt()
	if err := tg.Reply(b, msg, text, "", &replyMarkup); err != nil {
		return err
	}
	return next(StateVuoiConsumiLuce)
}

// attivazioneStep è la logica comune a LuceAttivazione e GasAttivazione:
// salva la scadenza (attivazione + 12 mesi) in user_data[key].
func (h *Handlers) attivazioneStep(b *gotgbot.Bot, ctx *ext.Context, servizio, key, state string,
	prompt func() (string, gotgbot.InlineKeyboardMarkup), nextState string) error {
	msg := ctx.Message
	if msg == nil {
		return next(state)
	}

	activation, ok := dateutil.ParseActivationDate(msg.Text)
	if !ok {
		if err := tg.Reply(b, msg, MsgDateInvalid, "", nil); err != nil {
			return err
		}
		return next(state)
	}

	scadenza := dateutil.ComputeExpiryDate(activation)
	h.UserData.Set(ctx.EffectiveUser.Id, key, dateutil.FormatISODate(scadenza))

	if err := tg.Reply(b, msg,
		"✅ Perfetto! La tua offerta "+servizio+" scade il <b>"+dateutil.FormatDateDisplay(scadenza)+"</b>.\n"+
			"Ti avviserò un mese prima. 🔔",
		parseModeHTML, nil); err != nil {
		return err
	}

	text, replyMarkup := prompt()
	if err := tg.Reply(b, msg, text, "", &replyMarkup); err != nil {
		return err
	}
	return next(nextState)
}

// LuceAttivazione salva la data di scadenza (attivazione + 12 mesi) per
// l'offerta fissa luce.
func (h *Handlers) LuceAttivazione(b *gotgbot.Bot, ctx *ext.Context) error {
	return h.attivazioneStep(b, ctx, "luce", keyLuceScadenza, StateLuceAttivazione, consumiLucePrompt, StateVuoiConsumiLuce)
}

// SkipLuceAttivazione salta l'inserimento della data di attivazione luce.
func (h *Handlers) SkipLuceAttivazione(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}

	text, replyMarkup := consumiLucePrompt()
	if err := tg.EditText(b, query, text, "", &replyMarkup); err != nil {
		return err
	}
	return next(StateVuoiConsumiLuce)
}

// VuoiConsumiLuce gestisce la risposta alla domanda sui consumi luce.
func (h *Handlers) VuoiConsumiLuce(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}

	if query.Data == "consumi_luce_si" {
		// Inizia raccolta consumi in base alla fascia
		luceFascia := h.getDefault(ctx.EffectiveUser.Id, keyLuceFascia, "monoraria")

		var messaggio string
		switch luceFascia {
		case "monoraria":
			messaggio = "Inserisci il tuo consumo annuo totale di energia elettrica in kWh.\n\n" +
				"💬 Esempio: 2700"
		case "trioraria":
			messaggio = "Inserisci il tuo consumo annuo in fascia F1 in kWh.\n\n" +
				"(F1 = feriali 8–19)\n\n" +
				esempioConsumoFascia
		default:
			// Fallback (non dovrebbe mai succedere)
			messaggio = "Inserisci il tuo consumo annuo in kWh."
		}

		if err := tg.EditText(b, query, messaggio, parseModeHTML, nil); err != nil {
			return err
		}
		return next(StateLuceConsumoF1)
	}

	// Non vuole inserire consumi, vai direttamente a chiedere se ha gas
	replyMarkup := haGasKeyboard()
	if err := tg.EditText(b, query, MsgHasGas, "", &replyMarkup); err != nil {
		return err
	}
	return next(StateHaGas)
}

// LuceConsumoF1 salva il consumo F1 luce e procede in base alla fascia.
func (h *Handlers) LuceConsumoF1(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.Message
	if msg == nil {
		return next(StateLuceConsumoF1)
	}
	uid := ctx.EffectiveUser.Id

	value, done, err := numericStep(b, msg, StateLuceConsumoF1, "❌ Inserisci un numero valido (es: 2700)")
	if done {
		return err
	}

	h.UserData.Set(uid, keyLuceConsumoF1, value)

	// Se monoraria, abbiamo finito con i consumi luce → vai a HA_GAS
	if h.getDefault(uid, keyLuceFascia, "monoraria") == "monoraria" {
		replyMarkup := haGasKeyboard()
		if err := tg.Reply(b, msg, MsgHasGas, "", &replyMarkup); err != nil {
			return err
		}
		return next(StateHaGas)
	}

	// Se trioraria, chiedi F2
	if err := tg.Reply(b, msg,
		"Ora inserisci il tuo consumo annuo in fascia F2 in kWh.\n\n"+
			"(F2 = feriali 7–8 e 19–23, sabato 7–23)\n\n"+
			esempioConsumoFascia,
		"", nil); err != nil {
		return err
	}
	return next(StateLuceConsumoF2)
}

// LuceConsumoF2 salva il consumo F2 luce, solo trioraria.
func (h *Handlers) LuceConsumoF2(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.Message
	if msg == nil {
		return next(StateLuceConsumoF2)
	}

	value, done, err := numericStep(b, msg, StateLuceConsumoF2, "❌ Inserisci un numero valido (es: 900)")
	if done {
		return err
	}

	h.UserData.Set(ctx.EffectiveUser.Id, keyLuceConsumoF2, value)

	// Chiedi F3
	if err := tg.Reply(b, msg,
		"Infine, inserisci il tuo consumo annuo in fascia F3 in kWh.\n\n"+
			"(F3 = notte, domeniche e festivi)\n\n"+
			esempioConsumoFascia,
		"", nil); err != nil {
		return err
	}
	return next(StateLuceConsumoF3)
}

// LuceConsumoF3 salva il consumo F3 luce (solo trioraria) e passa a HA_GAS.
func (h *Handlers) LuceConsumoF3(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.Message
	if msg == nil {
		return next(StateLuceConsumoF3)
	}

	value, done, err := numericStep(b, msg, StateLuceConsumoF3, "❌ Inserisci un numero valido (es: 900)")
	if done {
		return err
	}

	h.UserData.Set(ctx.EffectiveUser.Id, keyLuceConsumoF3, value)

	// Consumi luce completati, vai a chiedere se ha gas
	replyMarkup := haGasKeyboard()
	if err := tg.Reply(b, msg, MsgHasGas, "", &replyMarkup); err != nil {
		return err
	}
	return next(StateHaGas)
}

// HaGas gestisce la risposta alla domanda sulla fornitura gas.
func (h *Handlers) HaGas(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}

	if query.Data == "gas_si" {
		// Chiedi che tipo di tariffa gas ha (può essere diversa dalla luce)
		replyMarkup := keyboard([2]string{"📊 Fissa", "gas_tipo_fissa"}, [2]string{"📈 Variabile", "gas_tipo_variabile"})
		if err := tg.EditText(b, query,
			"🔥 <b>Fornitura Gas</b>\n\nChe tipo di tariffa <b>gas</b> hai?",
			parseModeHTML, &replyMarkup); err != nil {
			return err
		}
		return next(StateGasTipo)
	}
	return h.SalvaEConferma(b, ctx, query, true)
}

// GasTipoTariffa gestisce la scelta del tipo tariffa gas, Fissa/Variabile.
func (h *Handlers) GasTipoTariffa(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}
	uid := ctx.EffectiveUser.Id

	var msg string
	if query.Data == "gas_tipo_fissa" {
		h.UserData.Set(uid, keyGasTipo, "fissa")
		h.UserData.Set(uid, keyGasFascia, "monoraria")

		msg = "🔥 <b>Gas fisso</b>\n\n" +
			"Perfetto! Inserisci il costo materia energia gas (€/Smc).\n\n" +
			"ℹ️ Inserisci il prezzo <b>IVA e imposte escluse</b> " +
			fonteOctopus +
			"💬 Esempio: 0,456"
	} else { // gas_tipo_variabile
		h.UserData.Set(uid, keyGasTipo, "variabile")
		h.UserData.Set(uid, keyGasFascia, "monoraria")

		msg = "🔥 <b>Gas variabile</b>\n\n" +
			"Ora inserisci lo spread della tua tariffa rispetto al PSV.\n\n" +
			"ℹ️ Inserisci il valore <b>IVA e imposte escluse</b> " +
			fonteOctopus +
			"💬 Esempio: se la tua tariffa è <b>PSV + 0,08</b> €/Smc, scrivi <code>0,08</code>"
	}

	if err := tg.EditText(b, query, msg, parseModeHTML, nil); err != nil {
		return err
	}
	return next(StateGasEnergia)
}

// GasEnergia salva il costo energia gas, spread o prezzo fisso.
func (h *Handlers) GasEnergia(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.Message
	if msg == nil {
		return next(StateGasEnergia)
	}
	uid := ctx.EffectiveUser.Id

	invalid := "❌ Inserisci un numero valido (es: 0,456)"
	if h.getDefault(uid, keyGasTipo, "fissa") == "variabile" {
		invalid = "❌ Inserisci un numero valido (es: 0,08)"
	}
	value, done, err := numericStep(b, msg, StateGasEnergia, invalid)
	if done {
		return err
	}

	h.UserData.Set(uid, keyGasEnergia, value)
	if err := tg.Reply(b, msg,
		"Perfetto! Ora indica il costo di commercializzazione gas, in euro/anno.\n\n"+
			"💬 Esempio: 84 (se paghi 7 €/mese)",
		"", nil); err != nil {
		return err
	}
	return next(StateGasComm)
}

// GasComm salva la commercializzazione gas.
//
// Se la tariffa gas è fissa chiede la data di attivazione (per il reminder
// di scadenza), altrimenti passa direttamente alla domanda sui consumi.
func (h *Handlers) GasComm(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.Message
	if msg == nil {
		return next(StateGasComm)
	}
	uid := ctx.EffectiveUser.Id

	value, done, err := numericStep(b, msg, StateGasComm, "❌ Inserisci un numero valido (es: 144.00)")
	if done {
		return err
	}

	h.UserData.Set(uid, keyGasComm, value)

	// Solo per tariffe fisse chiediamo la data di attivazione (reminder scadenza)
	if v, _ := h.UserData.Get(uid, keyGasTipo); v == "fissa" {
		text, replyMarkup := attivazionePrompt("gas", SkipScadenzaGas)
		if err := tg.Reply(b, msg, text, parseModeHTML, &replyMarkup); err != nil {
			return err
		}
		return next(StateGasAttivazione)
	}

	text, replyMarkup := consumiGasPrompt()
	if err := tg.Reply(b, msg, text, "", &replyMarkup); err != nil {
		return err
	}
	return next(StateVuoiConsumiGas)
}

// GasAttivazione salva la data di scadenza (attivazione + 12 mesi) per
// l'offerta fissa gas.
func (h *Handlers) GasAttivazione(b *gotgbot.Bot, ctx *ext.Context) error {
	return h.attivazioneStep(b, ctx, "gas", keyGasScadenza, StateGasAttivazione, consumiGasPrompt, StateVuoiConsumiGas)
}

// SkipGasAttivazione salta l'inserimento della data di attivazione gas.
func (h *Handlers) SkipGasAttivazione(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}

	text, replyMarkup := consumiGasPrompt()
	if err := tg.EditText(b, query, text, "", &replyMarkup); err != nil {
		return err
	}
	return next(StateVuoiConsumiGas)
}

// VuoiConsumiGas gestisce la risposta alla domanda sui consumi gas.
func (h *Handlers) VuoiConsumiGas(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}

	if query.Data == "consumi_gas_si" {
		// Chiedi consumo gas
		if err := tg.EditText(b, query, MsgGasConsumo, "", nil); err != nil {
			return err
		}
		return next(StateGasConsumo)
	}
	// Non vuole inserire consumi, salva e conferma
	return h.SalvaEConferma(b, ctx, query, false)
}

// GasConsumo salva il consumo gas e passa alla conferma.
func (h *Handlers) GasConsumo(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.Message
	if msg == nil {
		return next(StateGasConsumo)
	}

	value, done, err := numericStep(b, msg, StateGasConsumo, "❌ Inserisci un numero valido (es: 1200)")
	if done {
		return err
	}

	h.UserData.Set(ctx.EffectiveUser.Id, keyGasConsumoAnnuo, value)

	// Consumi completati, salva e conferma
	return h.SalvaEConferma(b, ctx, nil, false)
}

// getDefault restituisce user_data[key], def se la chiave è assente.
func (h *Handlers) getDefault(userID int64, key, def string) any {
	v, ok := h.UserData.Get(userID, key)
	if !ok {
		return def
	}
	return v
}
