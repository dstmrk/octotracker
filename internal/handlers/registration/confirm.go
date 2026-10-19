package registration

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/formatters"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Chiavi obbligatorie di user_data per luce e gas.
var (
	requiredLuceKeys = []string{keyLuceTipo, keyLuceFascia, keyLuceEnergia, keyLuceComm}
	requiredGasKeys  = []string{keyGasTipo, keyGasFascia, keyGasEnergia, keyGasComm}
)

// missingKeys restituisce le chiavi richieste assenti da user_data, in
// ordine; se ne manca qualcuna logga il dettaglio delle chiavi presenti.
func (h *Handlers) missingKeys(userID int64, required []string) []string {
	snap := h.UserData.Snapshot(userID)
	var missing []string
	for _, k := range required {
		if _, ok := snap[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		logger.Error(fmt.Sprintf("Dati mancanti in context.user_data. Richiesti: %v, Mancanti: %v, Disponibili: %v",
			required, missing, slices.Sorted(maps.Keys(snap))))
	}
	return missing
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

// buildUserData costruisce la struttura utente da salvare nel database.
// soloLuce è true se l'utente ha solo la luce.
//
// Restituisce un errore se mancano dati necessari in user_data, dopo aver
// loggato il dettaglio delle chiavi.
func (h *Handlers) buildUserData(userID int64, soloLuce bool) (database.User, error) {
	// Verifica che tutti i dati necessari per luce esistano
	if missing := h.missingKeys(userID, requiredLuceKeys); len(missing) > 0 {
		return database.User{}, errors.New("dati mancanti per luce: " + strings.Join(missing, ", "))
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
		return database.User{}, errors.New("dati mancanti per gas: " + strings.Join(missing, ", "))
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
// dati inseriti dall'utente.
//
// Va in panic con valori NaN o infiniti o consumi trioraria incompleti;
// vedi confirmationMessage per la variante che restituisce un errore.
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

// confirmationMessage è formatConfirmationMessage con i panic dei
// formatter convertiti in errore, così che arrivino all'error handler
// dell'applicazione.
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

// SalvaEConferma salva i dati utente e mostra la conferma, terminando la
// conversazione.
//
// Con query nil risponde al messaggio dell'update, altrimenti modifica il
// messaggio del pulsante premuto.
func (h *Handlers) SalvaEConferma(b *gotgbot.Bot, ctx *ext.Context, query *gotgbot.CallbackQuery, soloLuce bool) error {
	if ctx.EffectiveUser == nil {
		return tg.ErrNoUser
	}
	sendMessage := func(text string) error { return tg.Reply(b, ctx.Message, text, parseModeHTML, nil) }
	if query != nil {
		sendMessage = func(text string) error { return tg.EditText(b, query, text, parseModeHTML, nil) }
	}
	uid := userIDString(ctx.EffectiveUser.Id)

	// Costruisci struttura dati utente
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
