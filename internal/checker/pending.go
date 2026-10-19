package checker

import (
	"fmt"
	"strings"

	"github.com/dstmrk/octotracker/internal/database"
)

// buildUtilityPending costruisce l'entry pending per una singola utility,
// "luce" o "gas" (_build_utility_pending).
//
// Mantiene tipo, fascia e consumi dall'utente (la scadenza non viene
// copiata); adotta le nuove tariffe Octopus solo se la utility è mostrata
// come conveniente (show) ed esiste una tariffa corrente. Restituisce
// l'entry e se sono state adottate le nuove tariffe.
func buildUtilityPending(utilityName string, userUtility database.Utility, currentRates database.CurrentRates, show bool) (database.Utility, bool) {
	tipo := userUtility.Tipo
	fascia := userUtility.Fascia
	entry := database.Utility{
		Tipo:   tipo,
		Fascia: fascia,
		// Copia i consumi presenti (consumo_f* per luce, consumo_annuo per gas)
		ConsumoF1:    userUtility.ConsumoF1,
		ConsumoF2:    userUtility.ConsumoF2,
		ConsumoF3:    userUtility.ConsumoF3,
		ConsumoAnnuo: userUtility.ConsumoAnnuo,
	}

	rate, ok := currentRates.Get(utilityName, tipo, fascia)
	if show && ok {
		// Adotta le nuove tariffe Octopus
		entry.Energia = rate.Energia
		entry.Commercializzazione = rateComm(rate)
		return entry, true
	}

	// Mantieni le tariffe attuali dell'utente
	entry.Energia = userUtility.Energia
	entry.Commercializzazione = userUtility.Commercializzazione
	return entry, false
}

// buildPendingRates costruisce le tariffe pendenti da proporre all'utente
// (_build_pending_rates).
//
// Contiene le nuove tariffe Octopus che l'utente può scegliere di adottare:
// mantiene tipo, fascia e consumi dall'utente e aggiorna energia e
// commercializzazione SOLO per le utility effettivamente convenienti
// (showLuce/showGas; in Python entrambi True di default).
//
// UpdatedServices traccia quali servizi vengono effettivamente aggiornati
// con le nuove offerte: serve ad ApplyPendingRates per azzerare la scadenza
// solo dei servizi che cambiano davvero. È sempre non nil (scritto come []).
func buildPendingRates(user *database.User, currentRates database.CurrentRates, showLuce, showGas bool) database.PendingRates {
	updatedServices := []string{}

	luceEntry, luceUpdated := buildUtilityPending("luce", user.Luce, currentRates, showLuce)
	pending := database.PendingRates{Luce: luceEntry}
	if luceUpdated {
		updatedServices = append(updatedServices, "luce")
	}

	if user.Gas != nil {
		gasEntry, gasUpdated := buildUtilityPending("gas", *user.Gas, currentRates, showGas)
		pending.Gas = &gasEntry
		if gasUpdated {
			updatedServices = append(updatedServices, "gas")
		}
	}

	pending.UpdatedServices = updatedServices
	return pending
}

// preparedNotification è la notifica preparata per un utente (la tupla
// (current_octopus, message, pending_rates) di _prepare_user_notification).
type preparedNotification struct {
	currentOctopus database.LastNotifiedRates
	message        string
	pendingRates   database.PendingRates
}

// prepareUserNotification valuta un utente e prepara i dati per la notifica
// (_prepare_user_notification). Restituisce nil se la notifica non serve.
//
// Va in panic (KeyError) dove Python solleverebbe un'eccezione.
func prepareUserNotification(userID string, user *database.User, currentRates database.CurrentRates) *preparedNotification {
	savings := CheckBetterRates(user, currentRates)

	if !savings.HasSavings {
		return nil
	}

	// Costruisci tariffe Octopus correnti per questo utente
	currentOctopus := buildCurrentOctopusRates(user, currentRates)

	// Controlla se già notificato
	if !shouldNotifyUser(user, currentOctopus) {
		return nil
	}

	// Valuta separatamente luce e gas per determinare cosa mostrare
	showLuce, luceSavings := shouldShowUtility("luce", savings, user, currentRates)
	showGas, gasSavings := shouldShowUtility("gas", savings, user, currentRates)

	// Skip se nessuna utility è conveniente
	if !showLuce && !showGas {
		// Log solo per casi MIXED non convenienti (informativi)
		if savings.Luce.IsMixed && luceSavings != nil {
			logger.Info(fmt.Sprintf("⏭️  Utente %s: luce MIXED stimato %.2f €/anno, non conveniente", userID, *luceSavings))
		}
		if savings.Gas.IsMixed && gasSavings != nil {
			logger.Info(fmt.Sprintf("⏭️  Utente %s: gas MIXED stimato %.2f €/anno, non conveniente", userID, *gasSavings))
		}
		return nil
	}

	// Log quali utility vengono mostrate
	var utilitiesShown []string
	if showLuce {
		utilitiesShown = append(utilitiesShown, "luce")
	}
	if showGas {
		utilitiesShown = append(utilitiesShown, "gas")
	}
	logger.Info(fmt.Sprintf("📤 Utente %s: notifica accodata (%s)", userID, strings.Join(utilitiesShown, ", ")))

	// Genera messaggio
	message := FormatNotification(savings, user, currentRates, showLuce, showGas, luceSavings, gasSavings)

	// Costruisci tariffe pendenti per aggiornamento rapido
	// Aggiorna solo le tariffe delle utility effettivamente convenienti
	pendingRates := buildPendingRates(user, currentRates, showLuce, showGas)

	return &preparedNotification{
		currentOctopus: currentOctopus,
		message:        message,
		pendingRates:   pendingRates,
	}
}
