// Package reminder è il port di reminder.py: invia un promemoria agli utenti
// con offerte a prezzo fisso in scadenza.
//
// Le offerte fisse Octopus Energy durano 12 mesi dall'attivazione; alla
// scadenza l'utente viene spostato in automatico sulla tariffa variabile del
// momento. Questo package, un mese prima della scadenza, avvisa l'utente e
// mostra un confronto con l'offerta fissa Octopus attualmente disponibile.
//
// I messaggi sono identici byte per byte alla versione Python (verificato
// dai test golden in testdata/). L'invio riusa checker.SendNotification.
package reminder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/formatters"
	"github.com/dstmrk/octotracker/internal/logging"
)

var logger = logging.Logger("reminder")

// now è l'orologio usato per misurare la durata (time.time()); sostituibile nei test.
var now = time.Now

// Services sono i servizi che possono avere un'offerta fissa con scadenza (SERVICES).
var Services = []string{"luce", "gas"}

// serviceEmoji è l'emoji per servizio (_SERVICE_EMOJI).
var serviceEmoji = map[string]string{"luce": "💡", "gas": "🔥"}

// Store è il sottoinsieme di *database.Store usato dal reminder (comprese
// le funzioni usate da checker.SendNotification). *database.Store lo
// implementa.
type Store interface {
	checker.UserRemover
	LoadUsers() []*database.User
	GetCurrentRates() database.CurrentRates
	GetScadenzaRemindedMap() map[string]database.ScadenzaReminded
	MarkScadenzaReminded(userID, servizio, scadenza string) bool
}

// ExpiringService è un servizio in scadenza: la tupla
// (servizio, scadenza_iso, giorni_mancanti) di _get_expiring_services.
type ExpiringService struct {
	Servizio string // "luce" o "gas"
	Scadenza string // data di scadenza ISO (YYYY-MM-DD) come salvata nel DB
	DaysLeft int    // giorni mancanti (0..ReminderDaysBefore)
}

// userUtility replica user_rates.get(servizio): nil se l'utente non ha la
// fornitura.
func userUtility(user *database.User, servizio string) *database.Utility {
	switch servizio {
	case "luce":
		return &user.Luce
	case "gas":
		return user.Gas
	}
	return nil
}

// getExpiringServices determina per quali servizi va inviato il reminder di
// scadenza (_get_expiring_services).
//
// Un servizio è in scadenza se: è a prezzo fisso, ha una data di scadenza,
// mancano tra 0 e dateutil.ReminderDaysBefore giorni e non è già stato
// notificato per quella stessa scadenza (reminded).
func getExpiringServices(user *database.User, reminded database.ScadenzaReminded, today time.Time) []ExpiringService {
	var expiring []ExpiringService

	for _, servizio := range Services {
		utility := userUtility(user, servizio)
		if utility == nil {
			continue
		}

		if utility.Tipo != "fissa" {
			continue
		}

		scadenza := utility.Scadenza
		if scadenza == "" {
			continue
		}

		// Già notificato per questa scadenza
		if r := reminded.Get(servizio); r != nil && *r == scadenza {
			continue
		}

		daysLeft, ok := dateutil.DaysUntil(scadenza, &today)
		if !ok {
			continue
		}

		// Finestra: da oggi fino a ReminderDaysBefore giorni prima della scadenza
		if daysLeft >= 0 && daysLeft <= dateutil.ReminderDaysBefore {
			expiring = append(expiring, ExpiringService{Servizio: servizio, Scadenza: scadenza, DaysLeft: daysLeft})
		}
	}

	return expiring
}

// formatDaysLeft formatta l'anticipo in linguaggio naturale (_format_days_left).
func formatDaysLeft(daysLeft int) string {
	if daysLeft == 0 {
		return "oggi"
	}
	if daysLeft == 1 {
		return "tra 1 giorno"
	}
	return fmt.Sprintf("tra %d giorni", daysLeft)
}

// formatServiceSection costruisce la sezione del messaggio per un singolo
// servizio in scadenza (_format_service_section).
func formatServiceSection(servizio, scadenza string, daysLeft int, user *database.User, currentRates database.CurrentRates) string {
	utility := userUtility(user, servizio)
	fascia := utility.Fascia
	emoji := serviceEmoji[servizio]
	unit := formatters.GetUtilityUnit(servizio)
	label := formatters.GetUtilityLabel("fissa", servizio)

	section := fmt.Sprintf("%s <b>%s</b>\n", emoji, formatters.Capitalize(servizio)) +
		fmt.Sprintf("La tua offerta a prezzo fisso scade il <b>%s</b> ", dateutil.FormatDateDisplay(scadenza)) +
		fmt.Sprintf("(%s).\n", formatDaysLeft(daysLeft))

	// Tariffa attuale dell'utente
	userEnergia := formatters.FormatNumber(utility.Energia, formatters.MaxDecimalsEnergy)
	userComm := formatters.FormatNumber(utility.Commercializzazione, formatters.MaxDecimalsCost)
	section += fmt.Sprintf("La tua tariffa: %s %s %s, Comm. %s €/anno\n", label, userEnergia, unit, userComm)

	// Offerta fissa Octopus attualmente disponibile (stesso servizio/fascia)
	if octopusRate, ok := currentRates.Get(servizio, "fissa", fascia); ok {
		newEnergia := formatters.FormatNumber(octopusRate.Energia, formatters.MaxDecimalsEnergy)
		section += fmt.Sprintf("Offerta fissa Octopus oggi: %s %s %s", label, newEnergia, unit)
		if newComm := octopusRate.Commercializzazione; newComm != nil {
			commFmt := formatters.FormatNumber(*newComm, formatters.MaxDecimalsCost)
			section += fmt.Sprintf(", Comm. %s €/anno", commFmt)
		}
		section += "\n"
		if cod := octopusRate.CodOfferta; cod != nil && *cod != "" {
			section += fmt.Sprintf("📋 Codice offerta: <code>%s</code>\n", *cod)
		}
	}

	section += "\n"
	return section
}

// FormatReminderMessage costruisce il messaggio HTML di reminder scadenza
// (format_reminder_message) per i servizi in scadenza dell'utente, con il
// confronto con le tariffe correnti Octopus.
func FormatReminderMessage(expiring []ExpiringService, user *database.User, currentRates database.CurrentRates) string {
	message := "⏰ <b>La tua offerta a prezzo fisso sta per scadere</b>\n\n"

	for _, e := range expiring {
		message += formatServiceSection(e.Servizio, e.Scadenza, e.DaysLeft, user, currentRates)
	}

	message += "ℹ️ Alla scadenza Octopus Energy applica in automatico la tariffa variabile del " +
		"momento. Se preferisci un'altra offerta, valuta le opzioni per tempo.\n\n" +
		"🔎 Con /history vedi lo storico delle tariffe, con /update aggiorni i tuoi dati.\n\n" +
		"🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe\n\n" +
		"⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy."

	return message
}

// prepareReminder valuta un utente e prepara il messaggio di reminder se
// necessario (_prepare_reminder). ok è false (None in Python) se nessun
// servizio è in scadenza.
func prepareReminder(userID string, user *database.User, remindedMap map[string]database.ScadenzaReminded, currentRates database.CurrentRates, today time.Time) (message string, expiring []ExpiringService, ok bool) {
	reminded := remindedMap[userID] // .get(user_id, {})
	expiring = getExpiringServices(user, reminded, today)

	if len(expiring) == 0 {
		return "", nil, false
	}

	message = FormatReminderMessage(expiring, user, currentRates)
	services := make([]string, len(expiring))
	for i, e := range expiring {
		services[i] = e.Servizio
	}
	logger.Info(fmt.Sprintf("📅 Utente %s: reminder scadenza accodato (%s)", userID, strings.Join(services, ", ")))
	return message, expiring, true
}

// queuedReminder è un reminder pronto per l'invio (la tupla
// (user_id, message, expiring)).
type queuedReminder struct {
	userID   string
	message  string
	expiring []ExpiringService
}

// sendRemindersParallel invia i reminder in parallelo con rate limiting
// (_send_reminders_parallel). Restituisce il numero di reminder inviati con
// successo.
func sendRemindersParallel(ctx context.Context, b *gotgbot.Bot, store Store, remindersToSend []queuedReminder) int {
	if len(remindersToSend) == 0 {
		return 0
	}

	logger.Info(fmt.Sprintf("📨 Invio %d reminder scadenza in parallelo...", len(remindersToSend)))

	sendWithLimit := func(i int) bool {
		r := remindersToSend[i]
		success := checker.SendNotification(ctx, b, store, r.userID, r.message, nil)
		if success {
			// Marca come notificato ogni servizio incluso nel reminder
			for _, e := range r.expiring {
				store.MarkScadenzaReminded(r.userID, e.Servizio, e.Scadenza)
			}
			logger.Info(fmt.Sprintf("✅ Reminder scadenza inviato a %s", r.userID))
			return true
		}
		logger.Warn(fmt.Sprintf("❌ Reminder scadenza fallito per %s", r.userID))
		return false
	}

	return checker.ParallelSend(ctx, len(remindersToSend), sendWithLimit)
}

// prepareReminders è la FASE 1 di check_and_send_reminders. Un panic (es.
// una tariffa utente infinita che FormatNumber non sa formattare) scarta
// solo l'utente che lo ha causato: viene loggato e il giro continua.
func prepareReminders(users []*database.User, remindedMap map[string]database.ScadenzaReminded, currentRates database.CurrentRates, today time.Time) []queuedReminder {
	var reminders []queuedReminder
	for _, user := range users {
		message, expiring, ok, err := prepareReminderSafe(user, remindedMap, currentRates, today)
		if err != nil {
			logger.Error(fmt.Sprintf("❌ Errore preparazione reminder per %s: %v", user.ID, err))
			continue
		}
		if ok {
			reminders = append(reminders, queuedReminder{userID: user.ID, message: message, expiring: expiring})
		}
	}
	return reminders
}

// prepareReminderSafe esegue prepareReminder convertendo un panic in errore.
func prepareReminderSafe(user *database.User, remindedMap map[string]database.ScadenzaReminded, currentRates database.CurrentRates, today time.Time) (message string, expiring []ExpiringService, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			message, expiring, ok, err = "", nil, false, checker.PanicError(r)
		}
	}()
	message, expiring, ok = prepareReminder(user.ID, user, remindedMap, currentRates, today)
	return message, expiring, ok, nil
}

// CheckAndSendReminders controlla le offerte fisse in scadenza e invia i
// reminder (check_and_send_reminders, chiamata dallo scheduler del bot).
//
// today è la data odierna (solo anno/mese/giorno); nil equivale alla data
// odierna in ora locale (una data esplicita è utile per i test). Il
// chiamante può passare checker.NewBot(token).
//
// Un errore nella preparazione di un utente salta solo quell'utente.
// L'errore restituito è oggi sempre nil; resta nella firma per i chiamanti.
// Gli errori di invio sono gestiti per utente da checker.SendNotification.
func CheckAndSendReminders(ctx context.Context, b *gotgbot.Bot, store Store, today *time.Time) error {
	startTime := now()
	var referenceDay time.Time
	if today != nil {
		referenceDay = *today
	} else {
		referenceDay = dateutil.Today()
	}
	logger.Info("📅 Inizio controllo scadenze offerte fisse...")

	users := store.LoadUsers()
	if len(users) == 0 {
		logger.Warn("⚠️  Nessun utente registrato per il controllo scadenze")
		return nil
	}

	currentRates := store.GetCurrentRates() // nil equivale a "or {}"
	remindedMap := store.GetScadenzaRemindedMap()

	// ========== FASE 1: Prepara i reminder ==========
	remindersToSend := prepareReminders(users, remindedMap, currentRates, referenceDay)

	// ========== FASE 2: Invia i reminder in parallelo ==========
	sent := sendRemindersParallel(ctx, b, store, remindersToSend)

	duration := now().Sub(startTime).Seconds()
	logger.Info(fmt.Sprintf("✅ Reminder scadenze completato in %.2fs - Inviati: %d/%d", duration, sent, len(users)))
	return nil
}

// Main esegue il reminder standalone (sottocomando remind): token è il
// valore di TELEGRAM_BOT_TOKEN. Restituisce checker.ErrMissingToken
// se il token è vuoto. Caricamento di .env, configurazione del logging e
// apertura del database sono a carico del chiamante.
func Main(ctx context.Context, token string, store Store) error {
	if token == "" {
		return checker.ErrMissingToken
	}
	return CheckAndSendReminders(ctx, checker.NewBot(token), store, nil)
}
