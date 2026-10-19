// Package commands contiene i comandi utility di OctoTracker (/status,
// /history, /remove, /help, /cancel e la risposta ai comandi non
// riconosciuti).
//
// Ogni metodo di Handlers è un handler gotgbot. I comandi restituiscono
// handlers.EndConversation(), così CancelConversation e HelpCommand si
// possono usare come fallback delle conversazioni; registrati fuori da una
// conversazione vanno avvolti con Standalone, che scarta il cambio di
// stato. Gli altri errori arrivano all'error handler dell'applicazione.
//
// La configurazione (CHECKER_HOUR, WEBAPP_URL, TELEGRAM_CHANNEL) arriva dai
// campi di Handlers.
package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/formatters"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/tg"
)

var logger = logging.Logger("handlers.commands")

const parseModeHTML = "HTML"

// Testi dei comandi.
const (
	// msgNotRegistered è la risposta di /status e /remove a un utente senza tariffe.
	msgNotRegistered = "ℹ️ Non hai ancora registrato le tue tariffe.\n\n" +
		"Per iniziare a usare OctoTracker, inserisci i tuoi dati con il comando /start.\n\n" +
		"🐙 Ti guiderò passo passo: ci vogliono meno di 60 secondi!"

	// msgHistoryNotRegistered è la risposta di /history a un utente senza tariffe.
	msgHistoryNotRegistered = "ℹ️ Non hai ancora registrato le tue tariffe.\n\n" +
		"Per visualizzare i grafici dello storico tariffe, " +
		"devi prima inserire i tuoi dati con il comando /start.\n\n" +
		"🐙 Ti guiderò passo passo: ci vogliono meno di 60 secondi!"

	// msgWebAppNotConfigured è la risposta di /history senza WEBAPP_URL.
	msgWebAppNotConfigured = "La Mini App non è ancora configurata.\nContatta l'amministratore del bot."

	// msgHistory è il testo che accompagna il pulsante della Mini App.
	msgHistory = "📈 <b>Storico Tariffe</b>\n\n" +
		"Clicca il pulsante qui sotto per aprire i grafici interattivi " +
		"con l'andamento delle tariffe Octopus Energy.\n\n" +
		"Puoi filtrare per:\n" +
		"• Servizio (Luce/Gas)\n" +
		"• Tipo tariffa (Fissa/Variabile)\n" +
		"• Fascia oraria\n" +
		"• Periodo (7, 30, 90, 365 giorni)"

	// historyButtonText è il testo del pulsante che apre la Mini App.
	historyButtonText = "📊 Apri Grafici Tariffe"

	// msgRemoved è la conferma di /remove.
	msgRemoved = "✅ <b>Dati cancellati con successo</b>\n\n" +
		"Tutte le informazioni che avevi registrato (tariffe e preferenze) sono state rimosse.\n" +
		"Da questo momento non riceverai più notifiche da OctoTracker.\n\n" +
		"🐙 Ti ringrazio per averlo provato!\n\n" +
		"Se in futuro vuoi ricominciare a monitorare le tariffe, ti basta usare il comando /start."

	// msgCancelled è la risposta di /cancel.
	msgCancelled = "❌ <b>Registrazione annullata</b>\n\n" +
		"Nessun problema! Hai annullato la procedura di registrazione.\n\n" +
		"Quando vuoi riprovarci, usa il comando /start.\n" +
		"Se hai bisogno di aiuto, puoi usare /help."

	// msgUnknownCommand è la risposta ai comandi non riconosciuti.
	msgUnknownCommand = "Comando non riconosciuto 🤷‍♂️\n" +
		"Dai un'occhiata a /help per vedere cosa puoi fare con OctoTracker."
)

// Store è il sottoinsieme di *database.Store usato dai comandi.
type Store interface {
	LoadUser(userID string) *database.User
	UserExists(userID string) bool
	RemoveUser(userID string) bool
}

var _ Store = (*database.Store)(nil)

// Handlers raccoglie le dipendenze dei comandi.
type Handlers struct {
	// Store è il database degli utenti.
	Store Store
	// UserData sono i dati di conversazione, condivisi con le
	// conversazioni del bot.
	UserData *tg.UserData
	// CheckerHour è CHECKER_HOUR, l'ora del controllo giornaliero mostrata
	// da /help (default 10 in config).
	CheckerHour int
	// WebAppURL è WEBAPP_URL, l'URL della Mini App dei grafici; vuoto se la
	// Mini App non è configurata.
	WebAppURL string
	// TelegramChannel è TELEGRAM_CHANNEL senza spazi
	// (config.Config.TelegramChannel); vuoto se il canale non è configurato.
	TelegramChannel string
}

// Standalone adatta un handler per l'uso fuori da una conversazione: il
// cambio di stato (handlers.EndConversation()) viene scartato invece di
// arrivare all'error handler del dispatcher. Gli altri errori passano
// invariati.
//
//	dispatcher.AddHandler(tg.NewCommand("status", commands.Standalone(h.Status)))
func Standalone(r handlers.Response) handlers.Response {
	return func(b *gotgbot.Bot, ctx *ext.Context) error {
		err := r(b, ctx)
		var stateChange *handlers.ConversationStateChange
		if errors.As(err, &stateChange) {
			return nil
		}
		return err
	}
}

// clearUserData pulisce i dati di conversazione dell'utente dell'update.
func (h *Handlers) clearUserData(ctx *ext.Context) error {
	if ctx.EffectiveUser == nil {
		return tg.ErrNoUser
	}
	h.UserData.Clear(ctx.EffectiveUser.Id)
	return nil
}

// end termina la conversazione dopo una risposta: se l'invio fallisce
// prevale l'errore.
func end(err error) error {
	if err != nil {
		return err
	}
	return handlers.EndConversation()
}

// ========== BOT COMMANDS ==========

// HistoryCommand apre la Mini App con i grafici dello storico tariffe.
func (h *Handlers) HistoryCommand(b *gotgbot.Bot, ctx *ext.Context) error {
	userID, err := tg.UserID(ctx)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("User %s: /history command", userID))

	// Pulisci eventuali dati di conversazione in corso
	h.UserData.Clear(ctx.EffectiveUser.Id)

	// Verifica che l'utente abbia tariffe registrate
	if h.Store.LoadUser(userID) == nil {
		return end(tg.Reply(b, ctx.Message, msgHistoryNotRegistered, "", nil))
	}

	if h.WebAppURL == "" {
		return end(tg.Reply(b, ctx.Message, msgWebAppNotConfigured, "", nil))
	}

	// Crea bottone che apre la Mini App
	keyboard := gotgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]gotgbot.InlineKeyboardButton{{
			{Text: historyButtonText, WebApp: &gotgbot.WebAppInfo{Url: h.WebAppURL}},
		}},
	}

	return end(tg.Reply(b, ctx.Message, msgHistory, parseModeHTML, &keyboard))
}

// Status mostra i dati salvati.
func (h *Handlers) Status(b *gotgbot.Bot, ctx *ext.Context) error {
	userID, err := tg.UserID(ctx)
	if err != nil {
		return err
	}

	// Pulisci eventuali dati di conversazione in corso
	h.UserData.Clear(ctx.EffectiveUser.Id)

	data := h.Store.LoadUser(userID)
	if data == nil {
		return end(tg.Reply(b, ctx.Message, msgNotRegistered, "", nil))
	}

	messaggio := formatStatusMessage(data)
	return end(tg.Reply(b, ctx.Message, messaggio, parseModeHTML, nil))
}

// formatStatusMessage costruisce il messaggio di /status per un utente
// registrato. Va in panic se un valore non è formattabile (es. tariffa
// infinita).
func formatStatusMessage(data *database.User) string {
	// Formatta numeri rimuovendo zeri trailing
	luceEnergiaFmt := formatters.FormatNumber(data.Luce.Energia, 4)
	luceCommFmt := formatters.FormatNumber(data.Luce.Commercializzazione, 2)

	// Formatta header luce
	tipoDisplay, luceLabel, luceUnit := formatters.FormatUtilityHeader("luce", data.Luce.Tipo, data.Luce.Fascia)

	var sb strings.Builder
	sb.WriteString("📊 <b>I tuoi dati:</b>\n\n")
	sb.WriteString("💡 <b>Luce (" + tipoDisplay + "):</b>\n")
	sb.WriteString("  - " + luceLabel + ": " + luceEnergiaFmt + " " + luceUnit + "\n")
	sb.WriteString("  - Commercializzazione: " + luceCommFmt + " €/anno\n")

	// Mostra consumi luce se presenti
	sb.WriteString(formatters.FormatLuceConsumption(data.Luce.Fascia,
		data.Luce.ConsumoF1, data.Luce.ConsumoF2, data.Luce.ConsumoF3, "  - "))

	// Mostra scadenza offerta fissa luce se presente
	if data.Luce.Scadenza != "" {
		sb.WriteString("  - Scadenza offerta: <b>" + dateutil.FormatDateDisplay(data.Luce.Scadenza) + "</b>\n")
	}

	if gas := data.Gas; gas != nil {
		gasEnergiaFmt := formatters.FormatNumber(gas.Energia, 4)
		gasCommFmt := formatters.FormatNumber(gas.Commercializzazione, 2)

		// Formatta header gas
		tipoDisplayGas, gasLabel, gasUnit := formatters.FormatUtilityHeader("gas", gas.Tipo, gas.Fascia)

		sb.WriteString("\n🔥 <b>Gas (" + tipoDisplayGas + "):</b>\n")
		sb.WriteString("  - " + gasLabel + ": " + gasEnergiaFmt + " " + gasUnit + "\n")
		sb.WriteString("  - Commercializzazione: " + gasCommFmt + " €/anno\n")

		// Mostra consumo gas se presente
		if gas.ConsumoAnnuo != nil {
			sb.WriteString("  - Consumo: <b>" + formatters.FormatNumber(*gas.ConsumoAnnuo, 0) + "</b> Smc/anno\n")
		}

		// Mostra scadenza offerta fissa gas se presente
		if gas.Scadenza != "" {
			sb.WriteString("  - Scadenza offerta: <b>" + dateutil.FormatDateDisplay(gas.Scadenza) + "</b>\n")
		}
	}

	sb.WriteString("\nPer modificarli usa /update")
	return sb.String()
}

// RemoveData cancella i dati dell'utente.
func (h *Handlers) RemoveData(b *gotgbot.Bot, ctx *ext.Context) error {
	userID, err := tg.UserID(ctx)
	if err != nil {
		return err
	}

	// Pulisci eventuali dati di conversazione in corso
	h.UserData.Clear(ctx.EffectiveUser.Id)

	if h.Store.UserExists(userID) {
		h.Store.RemoveUser(userID)
		return end(tg.Reply(b, ctx.Message, msgRemoved, parseModeHTML, nil))
	}
	return end(tg.Reply(b, ctx.Message, msgNotRegistered, "", nil))
}

// HelpCommand mostra il messaggio di aiuto. È anche un fallback della
// conversazione di registrazione: pulisce i dati di conversazione.
func (h *Handlers) HelpCommand(b *gotgbot.Bot, ctx *ext.Context) error {
	// Pulisci eventuali dati di conversazione in corso
	if err := h.clearUserData(ctx); err != nil {
		return err
	}
	return end(tg.Reply(b, ctx.Message, h.helpText(), parseModeHTML, nil))
}

// helpText costruisce il testo di /help con l'ora del checker e il link al
// canale Telegram (se configurato).
func (h *Handlers) helpText() string {
	// Link canale Telegram (se configurato)
	channelInfo := ""
	if h.TelegramChannel != "" {
		// Rimuovi @ iniziale per URL Telegram (https://t.me/ richiede solo il nome)
		channelName := strings.TrimLeft(h.TelegramChannel, "@")
		channelInfo = "Per avere aggiornamenti sulle nuove funzionalità, iscriviti al canale " +
			`<a href="https://t.me/` + channelName + `">@` + channelName + "</a>!\n\n"
	}

	return "👋 <b>Benvenuto su OctoTracker!</b>\n\n" +
		"Questo bot ti aiuta a monitorare le tariffe luce e gas di Octopus Energy " +
		"e ti avvisa quando ci sono offerte più convenienti rispetto alle tue.\n\n" +
		"<b>Comandi disponibili:</b>\n" +
		"• /start – Inizia e registra le tue tariffe attuali\n" +
		"• /update – Aggiorna le tariffe che hai impostato\n" +
		"• /status – Mostra le tariffe e lo stato attuale\n" +
		"• /history – Visualizza i grafici dello storico tariffe\n" +
		"• /remove – Cancella i tuoi dati e disattiva il servizio\n" +
		"• /feedback – Invia un feedback per migliorare il bot\n" +
		"• /cancel – Annulla la registrazione in corso\n" +
		"• /help – Mostra questo messaggio di aiuto\n\n" +
		"💡 Il bot controlla le tariffe ogni giorno alle " + strconv.Itoa(h.CheckerHour) + ":00.\n\n" +
		"📅 Se hai un'offerta a prezzo fisso e registri la data di attivazione, ti avviso " +
		"un mese prima della scadenza dei 12 mesi.\n\n" +
		"⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy.\n\n" +
		channelInfo
}

// CancelConversation annulla la conversazione in corso: è il fallback
// /cancel delle conversazioni di registrazione e di aggiornamento tariffe.
// Risponde, poi pulisce i dati di conversazione.
func (h *Handlers) CancelConversation(b *gotgbot.Bot, ctx *ext.Context) error {
	if err := tg.Reply(b, ctx.Message, msgCancelled, parseModeHTML, nil); err != nil {
		return err
	}
	// Pulisci i dati della conversazione
	if err := h.clearUserData(ctx); err != nil {
		return err
	}
	return handlers.EndConversation()
}

// UnknownCommand risponde ai comandi non riconosciuti.
func (h *Handlers) UnknownCommand(b *gotgbot.Bot, ctx *ext.Context) error {
	return tg.Reply(b, ctx.Message, msgUnknownCommand, "", nil)
}
