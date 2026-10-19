package checker

import (
	"fmt"
	"math"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/formatters"
)

// formatHeader formatta l'intestazione della notifica (_format_header).
func formatHeader(isMixed bool) string {
	var header string
	if isMixed {
		header = "⚖️ <b>Aggiornamento tariffe Octopus Energy</b>\n"
		header += "OctoTracker ha rilevato una variazione nelle tariffe, ma non è detto che sia automaticamente più conveniente: una delle due componenti è migliorata, l'altra è aumentata.\n\n"
	} else {
		header = "⚡️ <b>Buone notizie!</b>\n"
		header += "OctoTracker ha trovato una tariffa Octopus Energy più conveniente rispetto a quella che hai attiva.\n\n"
	}
	return header
}

// formatRateValue formatta un singolo valore tariffa con styling HTML
// (_format_rate_value): grassetto per un risparmio, sottolineato per un
// peggioramento.
func formatRateValue(formattedValue, unit string, hasSaving, isWorse bool) string {
	if hasSaving {
		return fmt.Sprintf("<b>%s %s</b>", formattedValue, unit)
	}
	if isWorse {
		return fmt.Sprintf("<u>%s %s</u>", formattedValue, unit)
	}
	return fmt.Sprintf("%s %s", formattedValue, unit)
}

// formatUtilitySection formatta la sezione utility (luce o gas) della
// notifica (_format_utility_section).
//
// emoji è 💡 per la luce e 🔥 per il gas, unit l'unità di misura energia
// (€/kWh o €/Smc). estimatedSavings è il risparmio stimato in €/anno (nil se
// non disponibile). Restituisce "" se l'utility non ha risparmi o se è il gas
// e l'utente non ce l'ha.
func formatUtilitySection(utilityName string, savings Savings, user *database.User, currentRates database.CurrentRates, emoji, unit string, estimatedSavings *float64) string {
	// Check se utility è presente e ha risparmi
	userRates := userUtility(user, utilityName)
	if utilityName == "gas" && userRates == nil {
		return ""
	}

	s := savings.forUtility(utilityName)
	if !s.hasImprovement() {
		return ""
	}

	// Estrai tipo e fascia
	tipo := s.Tipo
	fascia := s.Fascia

	// Formatta tipo e label usando helper functions
	tipoDisplay := formatters.FormatUtilityTypeDisplay(tipo, fascia)
	label := formatters.GetUtilityLabel(tipo, utilityName)

	// Header sezione
	utilityDisplay := formatters.Capitalize(utilityName)
	section := fmt.Sprintf("%s <b>%s (%s):</b>\n", emoji, utilityDisplay, tipoDisplay)

	// Formatta tariffe utente
	userEnergia := formatters.FormatNumber(userRates.Energia, formatters.MaxDecimalsEnergy)
	userComm := formatters.FormatNumber(userRates.Commercializzazione, formatters.MaxDecimalsCost)
	section += fmt.Sprintf("Tua tariffa: %s %s %s, Comm. %s €/anno\n", label, userEnergia, unit, userComm)

	// Formatta nuove tariffe se disponibili
	if rate, ok := currentRates.Get(utilityName, tipo, fascia); ok {
		energiaNew := rate.Energia
		commNew := rateComm(rate)

		energiaFormatted := formatters.FormatNumber(energiaNew, formatters.MaxDecimalsEnergy)
		commFormatted := formatters.FormatNumber(commNew, formatters.MaxDecimalsCost)

		// Formatta energia usando helper
		energiaStr := formatRateValue(energiaFormatted, unit, s.Energia != nil, s.EnergiaWorse)

		// Formatta commercializzazione usando helper
		commStr := formatRateValue(commFormatted, "€/anno", s.Comm != nil, s.CommWorse)

		section += fmt.Sprintf("Nuova tariffa: %s %s, Comm. %s\n", label, energiaStr, commStr)

		// Aggiungi codice offerta se disponibile
		if rate.CodOfferta != nil && *rate.CodOfferta != "" {
			section += fmt.Sprintf("📋 Codice offerta: <code>%s</code>\n", *rate.CodOfferta)
		}
	}

	// Aggiungi stima risparmio se disponibile (per utility mixed con consumi)
	if estimatedSavings != nil {
		risparmioFormatted := formatters.FormatNumber(math.Abs(*estimatedSavings), formatters.MaxDecimalsCost)
		section += fmt.Sprintf("💰 In base ai tuoi consumi di %s, stimiamo un risparmio di circa %s €/anno.\n", utilityName, risparmioFormatted)
	}

	section += "\n"
	return section
}

// formatLuceSection formatta la sezione luce della notifica (_format_luce_section).
func formatLuceSection(savings Savings, user *database.User, currentRates database.CurrentRates, estimatedSavings *float64) string {
	return formatUtilitySection("luce", savings, user, currentRates, "💡", "€/kWh", estimatedSavings)
}

// formatGasSection formatta la sezione gas della notifica (_format_gas_section).
func formatGasSection(savings Savings, user *database.User, currentRates database.CurrentRates, estimatedSavings *float64) string {
	return formatUtilitySection("gas", savings, user, currentRates, "🔥", "€/Smc", estimatedSavings)
}

// mixedUtility è una utility mixed da mostrare con il suo risparmio stimato
// (la tupla (nome, risparmio) di _get_mixed_utilities).
type mixedUtility struct {
	name             string
	estimatedSavings *float64
}

// getMixedUtilities restituisce la lista delle utility mixed da mostrare
// (_get_mixed_utilities).
func getMixedUtilities(showLuce, showGas, luceIsMixed, gasIsMixed bool, luceEstimatedSavings, gasEstimatedSavings *float64) []mixedUtility {
	var mixedUtilities []mixedUtility
	if showLuce && luceIsMixed {
		mixedUtilities = append(mixedUtilities, mixedUtility{"luce", luceEstimatedSavings})
	}
	if showGas && gasIsMixed {
		mixedUtilities = append(mixedUtilities, mixedUtility{"gas", gasEstimatedSavings})
	}
	return mixedUtilities
}

// formatMixedConsumptionMessage formatta il messaggio consumi per le utility
// mixed (_format_mixed_consumption_message).
//
// NOTA: le stime di risparmio sono mostrate inline nelle sezioni utility,
// quindi questa funzione mostra solo i suggerimenti per le utility senza
// consumi.
func formatMixedConsumptionMessage(mixedUtilities []mixedUtility) string {
	if len(mixedUtilities) == 0 {
		return ""
	}

	hasMissingConsumption, hasConsumption := false, false
	for _, m := range mixedUtilities {
		if m.estimatedSavings == nil {
			hasMissingConsumption = true
		} else {
			hasConsumption = true
		}
	}

	if hasMissingConsumption && !hasConsumption {
		// Tutte le utility mixed non hanno consumi
		return "📊 In questi casi la convenienza dipende dai tuoi consumi.\n" +
			"Se vuoi una stima più precisa, puoi indicare i tuoi consumi usando il comando /update.\n\n"
	}

	if hasConsumption && hasMissingConsumption {
		// Alcune utility hanno consumi, altre no → suggerisci di aggiungere tutti i consumi
		return "📊 Per una stima ancora più precisa, puoi indicare tutti i tuoi consumi con /update.\n\n"
	}

	// Tutte le utility hanno consumi → le stime sono già mostrate inline, nessun messaggio aggiuntivo
	return ""
}

// formatFooter formatta il footer della notifica con gestione per-utility
// (_format_footer).
func formatFooter(luceIsMixed, gasIsMixed bool, luceEstimatedSavings, gasEstimatedSavings *float64, showLuce, showGas bool) string {
	mixedUtilities := getMixedUtilities(showLuce, showGas, luceIsMixed, gasIsMixed, luceEstimatedSavings, gasEstimatedSavings)

	footer := formatMixedConsumptionMessage(mixedUtilities)
	footer += "👇 Vuoi aggiornare le tariffe memorizzate su OctoTracker con quelle nuove?\n\n"
	footer += "🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe\n\n"
	footer += "☕️ Se pensi che questo bot ti sia utile, puoi offrirmi un caffè su ko-fi.com/dstmrk — grazie di cuore! 💙"

	return footer
}

// FormatNotification formatta il messaggio di notifica (format_notification).
//
// showLuce/showGas indicano se includere le sezioni luce e gas (in Python
// entrambi True di default); luceEstimatedSavings/gasEstimatedSavings sono i
// risparmi stimati (nil = None, il default Python).
//
// Va in panic con KeyError se una tariffa corrente da mostrare non ha
// commercializzazione, come Python.
func FormatNotification(savings Savings, user *database.User, currentRates database.CurrentRates, showLuce, showGas bool, luceEstimatedSavings, gasEstimatedSavings *float64) string {
	// Determina se mostrare header mixed
	message := formatHeader(savings.IsMixed)

	// Aggiungi sezioni solo per le utility da mostrare
	if showLuce {
		message += formatLuceSection(savings, user, currentRates, luceEstimatedSavings)
	}
	if showGas {
		message += formatGasSection(savings, user, currentRates, gasEstimatedSavings)
	}

	message += formatFooter(
		savings.Luce.IsMixed,
		savings.Gas.IsMixed,
		luceEstimatedSavings,
		gasEstimatedSavings,
		showLuce,
		showGas,
	)
	return message
}
