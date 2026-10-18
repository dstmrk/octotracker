package registration

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/formatters"
)

// Chiavi obbligatorie di user_data per luce e gas (required_luce_keys /
// required_gas_keys di _build_user_data).
var (
	requiredLuceKeys = []string{keyLuceTipo, keyLuceFascia, keyLuceEnergia, keyLuceComm}
	requiredGasKeys  = []string{keyGasTipo, keyGasFascia, keyGasEnergia, keyGasComm}
)

// userDataKeyOrder è l'ordine in cui la conversazione inserisce le chiavi in
// user_data. Serve a riprodurre l'ordine di inserimento del dict Python nel
// log "Disponibili" (tg.UserData è una mappa senza ordine).
var userDataKeyOrder = []string{
	keyIsVariabile, keyLuceTipo, keyLuceFascia, keyLuceEnergia, keyLuceComm, keyLuceScadenza,
	keyLuceConsumoF1, keyLuceConsumoF2, keyLuceConsumoF3,
	keyGasTipo, keyGasFascia, keyGasEnergia, keyGasComm, keyGasScadenza, keyGasConsumoAnnuo,
}

// keyError replica KeyError(msg) di Python: str(e) è la repr del messaggio
// (tra apici), che è ciò che finisce nei log.
type keyError struct{ msg string }

func (e *keyError) Error() string { return pyStrRepr(e.msg) }

// availableKeys replica list(context.user_data.keys()): prima le chiavi non
// gestite da questa conversazione (inserite da altre conversazioni, in
// ordine alfabetico), poi quelle della registrazione nell'ordine del flusso.
func (h *Handlers) availableKeys(userID int64) []string {
	snap := h.UserData.Snapshot(userID)
	rank := make(map[string]int, len(userDataKeyOrder))
	for i, k := range userDataKeyOrder {
		rank[k] = i
	}
	var unknown, known []string
	for k := range snap {
		if _, ok := rank[k]; ok {
			known = append(known, k)
		} else {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	sort.Slice(known, func(i, j int) bool { return rank[known[i]] < rank[known[j]] })
	return append(unknown, known...)
}

// missingKeys restituisce le chiavi richieste assenti da user_data, in ordine.
func (h *Handlers) missingKeys(userID int64, required []string) []string {
	var missing []string
	for _, k := range required {
		if _, ok := h.UserData.Get(userID, k); !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

// logMissing replica il logger.error dettagliato di _build_user_data.
func (h *Handlers) logMissing(userID int64, required, missing []string) {
	logger.Error(fmt.Sprintf(
		"Dati mancanti in context.user_data. Richiesti: %s, Mancanti: %s, Disponibili: %s",
		pyListRepr(required), pyListRepr(missing), pyListRepr(h.availableKeys(userID)),
	))
}

// floatValue legge un numero da user_data (i valori numerici sono float64).
func (h *Handlers) floatValue(userID int64, key string) float64 {
	v, _ := h.UserData.Get(userID, key)
	f, _ := v.(float64)
	return f
}

// floatPtr legge un numero opzionale da user_data: nil se la chiave è assente.
func (h *Handlers) floatPtr(userID int64, key string) *float64 {
	v, ok := h.UserData.Get(userID, key)
	if !ok {
		return nil
	}
	f, _ := v.(float64)
	return &f
}

// stringValue legge una stringa da user_data ("" se assente).
func (h *Handlers) stringValue(userID int64, key string) string {
	v, _ := h.UserData.Get(userID, key)
	s, _ := v.(string)
	return s
}

// buildUserData costruisce la struttura utente da salvare nel database
// (_build_user_data). soloLuce è true se l'utente ha solo la luce.
//
// Restituisce un *keyError (KeyError in Python) se mancano dati necessari in
// user_data, dopo aver loggato il dettaglio delle chiavi.
func (h *Handlers) buildUserData(userID int64, soloLuce bool) (database.User, error) {
	// Verifica che tutti i dati necessari per luce esistano
	if missing := h.missingKeys(userID, requiredLuceKeys); len(missing) > 0 {
		// Log dettagliato per debug
		h.logMissing(userID, requiredLuceKeys, missing)
		return database.User{}, &keyError{"Dati mancanti per luce: " + strings.Join(missing, ", ")}
	}

	u := database.User{
		Luce: database.Utility{
			Tipo:                h.stringValue(userID, keyLuceTipo),
			Fascia:              h.stringValue(userID, keyLuceFascia),
			Energia:             h.floatValue(userID, keyLuceEnergia),
			Commercializzazione: h.floatValue(userID, keyLuceComm),
			// Aggiungi consumi luce se presenti
			ConsumoF1: h.floatPtr(userID, keyLuceConsumoF1),
			ConsumoF2: h.floatPtr(userID, keyLuceConsumoF2),
			ConsumoF3: h.floatPtr(userID, keyLuceConsumoF3),
			// Aggiungi scadenza offerta fissa luce se presente
			Scadenza: h.stringValue(userID, keyLuceScadenza),
		},
	}

	if soloLuce {
		// user_data["gas"] = None
		return u, nil
	}

	// Verifica che tutti i dati necessari per gas esistano
	if missing := h.missingKeys(userID, requiredGasKeys); len(missing) > 0 {
		h.logMissing(userID, requiredGasKeys, missing)
		return database.User{}, &keyError{"Dati mancanti per gas: " + strings.Join(missing, ", ")}
	}

	u.Gas = &database.Utility{
		Tipo:                h.stringValue(userID, keyGasTipo),
		Fascia:              h.stringValue(userID, keyGasFascia),
		Energia:             h.floatValue(userID, keyGasEnergia),
		Commercializzazione: h.floatValue(userID, keyGasComm),
		// Aggiungi consumo gas se presente
		ConsumoAnnuo: h.floatPtr(userID, keyGasConsumoAnnuo),
		// Aggiungi scadenza offerta fissa gas se presente
		Scadenza: h.stringValue(userID, keyGasScadenza),
	}
	return u, nil
}

// formatConfirmationMessage formatta il messaggio di conferma (HTML) con i
// dati inseriti dall'utente (_format_confirmation_message).
//
// Come la versione Python va in panic (ValueError/OverflowError/TypeError)
// con valori NaN o infiniti o consumi trioraria incompleti; vedi
// confirmationMessage per la variante che restituisce un errore.
func formatConfirmationMessage(u database.User) string {
	// Formatta numeri luce
	luceEnergiaFmt := formatters.FormatNumber(u.Luce.Energia, formatters.MaxDecimalsEnergy)
	luceCommFmt := formatters.FormatNumber(u.Luce.Commercializzazione, formatters.MaxDecimalsCost)

	// Formatta header luce
	tipoDisplay, luceLabel, luceUnit := formatters.FormatUtilityHeader("luce", u.Luce.Tipo, u.Luce.Fascia)

	var sb strings.Builder
	sb.WriteString("✅ <b>Abbiamo finito!</b>\n\n" +
		"Ecco i dati che hai inserito:\n\n" +
		"💡 <b>Luce (" + tipoDisplay + ")</b>\n" +
		"- " + luceLabel + ": " + luceEnergiaFmt + " " + luceUnit + "\n" +
		"- Commercializzazione: " + luceCommFmt + " €/anno\n")

	// Aggiungi consumi luce se presenti
	sb.WriteString(formatters.FormatLuceConsumption(u.Luce.Fascia, u.Luce.ConsumoF1, u.Luce.ConsumoF2,
		u.Luce.ConsumoF3, formatters.DefaultConsumptionPrefix))

	// Aggiungi scadenza offerta fissa luce se presente
	if u.Luce.Scadenza != "" {
		sb.WriteString("- Scadenza offerta: <b>" + dateutil.FormatDateDisplay(u.Luce.Scadenza) + "</b>\n")
	}

	// Aggiungi sezione gas se presente
	if gas := u.Gas; gas != nil {
		gasEnergiaFmt := formatters.FormatNumber(gas.Energia, formatters.MaxDecimalsEnergy)
		gasCommFmt := formatters.FormatNumber(gas.Commercializzazione, formatters.MaxDecimalsCost)

		// Formatta header gas
		tipoDisplayGas, gasLabel, gasUnit := formatters.FormatUtilityHeader("gas", gas.Tipo, gas.Fascia)

		sb.WriteString("\n🔥 <b>Gas (" + tipoDisplayGas + ")</b>\n" +
			"- " + gasLabel + ": " + gasEnergiaFmt + " " + gasUnit + "\n" +
			"- Commercializzazione: " + gasCommFmt + " €/anno\n")

		// Aggiungi consumo gas se presente
		if gas.ConsumoAnnuo != nil {
			sb.WriteString("- Consumo: <b>" + formatters.FormatNumber(*gas.ConsumoAnnuo, 0) + "</b> Smc/anno\n")
		}

		// Aggiungi scadenza offerta fissa gas se presente
		if gas.Scadenza != "" {
			sb.WriteString("- Scadenza offerta: <b>" + dateutil.FormatDateDisplay(gas.Scadenza) + "</b>\n")
		}
	}

	// Aggiungi footer
	sb.WriteString("\nTutto corretto?\n" +
		"Se in futuro vuoi modificare qualcosa, puoi usare il comando /update.\n\n" +
		"⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy.")

	return sb.String()
}

// confirmationMessage è formatConfirmationMessage con le eccezioni Python
// (i panic dei formatter) convertite in errore, così che arrivino all'error
// handler dell'applicazione come in python-telegram-bot.
func confirmationMessage(u database.User) (msg string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New(fmt.Sprint(r))
		}
	}()
	return formatConfirmationMessage(u), nil
}

// msgSaveError è il messaggio inviato quando mancano dati in user_data.
const msgSaveError = "❌ Si è verificato un errore durante il salvataggio dei dati.\n\n" +
	"Per favore riprova usando il comando /start per ricominciare la registrazione."

// SalvaEConferma salva i dati utente e mostra la conferma (salva_e_conferma),
// terminando la conversazione.
//
// In Python riceve un Update oppure una CallbackQuery: con query nil si usa
// l'update (utente effettivo e risposta con reply_text al messaggio), con
// query non nil l'utente della query e la modifica del messaggio del
// pulsante (edit_message_text).
func (h *Handlers) SalvaEConferma(b *gotgbot.Bot, ctx *ext.Context, query *gotgbot.CallbackQuery, soloLuce bool) error {
	// Distingui tra Update (con message) e CallbackQuery
	var userID int64
	var sendMessage func(text string) error
	if query == nil {
		// È un Update
		if ctx.EffectiveUser == nil {
			return attributeError("id")
		}
		userID = ctx.EffectiveUser.Id
		sendMessage = func(text string) error { return replyText(b, ctx.Message, text, parseModeHTML, nil) }
	} else {
		// È un CallbackQuery
		userID = query.From.Id
		sendMessage = func(text string) error { return editMessageText(b, query, text, parseModeHTML, nil) }
	}
	uid := userIDString(userID)

	// Costruisci struttura dati utente (context.user_data è quello dell'utente dell'update)
	u, err := h.buildUserData(ctx.EffectiveUser.Id, soloLuce)
	if err != nil {
		// Dati mancanti - chiedi all'utente di riprovare
		logger.Error(fmt.Sprintf("User %s: Errore costruzione dati utente - %v", uid, err))
		if err := sendMessage(msgSaveError); err != nil {
			return err
		}
		return handlers.EndConversation()
	}

	// Salva nel database
	h.Store.SaveUser(uid, u)

	// Formatta messaggio di conferma
	messaggio, err := confirmationMessage(u)
	if err != nil {
		return err
	}

	if err := sendMessage(messaggio); err != nil {
		return err
	}
	return handlers.EndConversation()
}
