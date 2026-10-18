// Package checker è il port di checker.py: controlla se ci sono tariffe
// Octopus Energy più convenienti di quelle registrate dagli utenti e li
// notifica su Telegram.
//
// Il flusso è identico alla versione Python (check_and_notify_users):
//
//  1. carica utenti e tariffe correnti dal database;
//  2. per ogni utente confronta le tariffe (CheckBetterRates), decide quali
//     forniture mostrare (shouldShowUtility), formatta il messaggio
//     (FormatNotification) e prepara le tariffe pendenti (buildPendingRates);
//  3. invia le notifiche in parallelo (max 10 simultanee) e, per ogni invio
//     riuscito, salva last_notified_rates e pending_rates.
//
// I messaggi sono identici byte per byte a quelli Python (verificato dai
// test golden in testdata/). Dove Python solleverebbe un'eccezione (per
// esempio KeyError su una tariffa senza commercializzazione) le funzioni Go
// vanno in panic con un valore error equivalente; CheckAndNotifyUsers lo
// recupera e lo restituisce come errore, come l'eccezione che in Python
// risale fino a run_checker.
package checker

import (
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

// logger replica logging.getLogger(__name__) di checker.py.
var logger = logging.Logger("checker")

// TelegramErrorsToDelete sono gli errori Telegram che indicano che l'utente
// deve essere rimosso dal database (TELEGRAM_ERRORS_TO_DELETE). Il confronto
// è fatto per sottostringa sul messaggio d'errore in minuscolo.
var TelegramErrorsToDelete = []string{
	"bot was blocked by the user",
	"user is deactivated", // Account Telegram disabilitato
	"bot was kicked",      // Bot rimosso da gruppo/canale
	"chat not found",      // Chat non esiste più
}

// Store è il sottoinsieme di *database.Store usato dal checker (le funzioni
// importate da database in checker.py). *database.Store lo implementa.
type Store interface {
	UserRemover
	LoadUsers() []*database.User
	GetCurrentRates() database.CurrentRates
	SaveUser(userID string, u database.User) bool
	SavePendingRates(userID string, pending database.PendingRates) bool
}

// UserRemover è ciò che serve a SendNotification per rimuovere un utente
// non più raggiungibile (database.remove_user).
type UserRemover interface {
	RemoveUser(userID string) bool
}

// KeyError replica l'eccezione KeyError di Python sollevata accedendo a una
// chiave mancante (es. rate["commercializzazione"] quando nel DB la
// commercializzazione è NULL). Error() restituisce str(KeyError(key)),
// cioè la chiave tra apici: 'commercializzazione'.
type KeyError string

// Error implementa error come str(KeyError) di Python.
func (e KeyError) Error() string { return "'" + string(e) + "'" }

// rateComm replica rate["commercializzazione"]: va in panic con KeyError se
// la chiave è assente, come Python.
func rateComm(r database.Rate) float64 {
	if r.Commercializzazione == nil {
		panic(KeyError("commercializzazione"))
	}
	return *r.Commercializzazione
}

// RateSaving è il risparmio su un singolo campo tariffa (il dict
// {"attuale", "nuova", "risparmio"} di _compare_rate_field).
type RateSaving struct {
	Attuale   float64 // valore attuale dell'utente
	Nuova     float64 // valore della nuova tariffa Octopus
	Risparmio float64 // attuale - nuova
}

// UtilitySavings raccoglie le chiavi di check_better_rates relative a una
// fornitura (prefisso "luce_" o "gas_").
type UtilitySavings struct {
	Energia      *RateSaving // <utility>_energia: nil = None
	Comm         *RateSaving // <utility>_comm: nil = None
	EnergiaWorse bool        // <utility>_energia_worse
	CommWorse    bool        // <utility>_comm_worse
	IsMixed      bool        // <utility>_is_mixed: una componente migliora, l'altra peggiora
	Tipo         string      // <utility>_tipo: "" = None (utente senza gas)
	Fascia       string      // <utility>_fascia: "" = None (utente senza gas)
}

// hasImprovement replica `savings[energia] or savings[comm]`.
func (u UtilitySavings) hasImprovement() bool {
	return u.Energia != nil || u.Comm != nil
}

// Savings è il risultato di CheckBetterRates (il dict "savings" di Python).
type Savings struct {
	Luce       UtilitySavings
	Gas        UtilitySavings
	HasSavings bool // almeno un componente (luce o gas) è migliorato
	IsMixed    bool // luce_is_mixed or gas_is_mixed (backward compatibility)
}

// forUtility restituisce le chiavi "<utilityName>_*".
func (s Savings) forUtility(utilityName string) UtilitySavings {
	if utilityName == "gas" {
		return s.Gas
	}
	return s.Luce
}

// userUtility replica user_rates.get(utility_name): nil se l'utente non ha
// la fornitura (gas assente).
func userUtility(user *database.User, utilityName string) *database.Utility {
	if utilityName == "gas" {
		return user.Gas
	}
	return &user.Luce
}

// CheckBetterRates confronta le tariffe utente con le tariffe attuali dello
// stesso tipo e fascia e restituisce risparmi e peggioramenti trovati
// (check_better_rates).
func CheckBetterRates(user *database.User, currentRates database.CurrentRates) Savings {
	// Confronta luce
	luceResult := checkUtilityRates(user.Luce, currentRates, "luce")

	// Confronta gas (se presente)
	hasGas := user.Gas != nil
	var gasResult utilityCheck
	if hasGas {
		gasResult = checkUtilityRates(*user.Gas, currentRates, "gas")
	}

	// Determina se è un caso "mixed" PER FORNITURA (una componente migliora, l'altra peggiora)
	luceHasImprovement := luceResult.energiaSaving != nil || luceResult.commSaving != nil
	luceHasWorsening := luceResult.energiaWorse || luceResult.commWorse
	luceIsMixed := luceHasImprovement && luceHasWorsening

	gasHasImprovement := gasResult.energiaSaving != nil || gasResult.commSaving != nil
	gasHasWorsening := gasResult.energiaWorse || gasResult.commWorse
	gasIsMixed := gasHasImprovement && gasHasWorsening

	// Costruisci risultato finale
	savings := Savings{
		Luce: UtilitySavings{
			Energia:      luceResult.energiaSaving,
			Comm:         luceResult.commSaving,
			EnergiaWorse: luceResult.energiaWorse,
			CommWorse:    luceResult.commWorse,
			IsMixed:      luceIsMixed,
			Tipo:         user.Luce.Tipo,
			Fascia:       user.Luce.Fascia,
		},
		Gas: UtilitySavings{
			Energia:      gasResult.energiaSaving,
			Comm:         gasResult.commSaving,
			EnergiaWorse: gasResult.energiaWorse,
			CommWorse:    gasResult.commWorse,
			IsMixed:      gasIsMixed,
		},
		HasSavings: luceResult.hasSavings || gasResult.hasSavings,
		// Mantieni is_mixed globale per backward compatibility
		IsMixed: luceIsMixed || gasIsMixed,
	}
	if hasGas {
		savings.Gas.Tipo = user.Gas.Tipo
		savings.Gas.Fascia = user.Gas.Fascia
	}
	return savings
}

// ========== HELPER FUNCTIONS ==========

// compareRateField confronta un singolo campo tariffa (energia o
// commercializzazione) (_compare_rate_field).
//
// currentValue nil significa valore non disponibile (None). Restituisce il
// risparmio (non nil solo se la nuova tariffa è migliore) e se la nuova
// tariffa è peggiore.
func compareRateField(userValue float64, currentValue *float64) (*RateSaving, bool) {
	if currentValue == nil {
		return nil, false
	}
	cur := *currentValue

	if cur < userValue {
		// Miglioramento
		return &RateSaving{Attuale: userValue, Nuova: cur, Risparmio: userValue - cur}, false
	} else if cur > userValue {
		// Peggioramento
		return nil, true
	}

	// Nessun cambiamento
	return nil, false
}

// utilityCheck è il risultato di checkUtilityRates (il dict con chiavi
// energia_saving, comm_saving, energia_worse, comm_worse, has_savings).
type utilityCheck struct {
	energiaSaving *RateSaving
	commSaving    *RateSaving
	energiaWorse  bool
	commWorse     bool
	hasSavings    bool
}

// checkUtilityRates confronta le tariffe luce o gas e restituisce
// risparmi/peggioramenti (_check_utility_rates). utilityName è "luce" o "gas".
func checkUtilityRates(userUtility database.Utility, currentRates database.CurrentRates, utilityName string) utilityCheck {
	var result utilityCheck

	// Accedi alla tariffa corrente specifica
	utilityRate, ok := currentRates.Get(utilityName, userUtility.Tipo, userUtility.Fascia)
	if !ok {
		return result
	}

	// Confronta energia
	energia := utilityRate.Energia
	result.energiaSaving, result.energiaWorse = compareRateField(userUtility.Energia, &energia)
	if result.energiaSaving != nil {
		result.hasSavings = true
	}

	// Confronta commercializzazione
	result.commSaving, result.commWorse = compareRateField(userUtility.Commercializzazione, utilityRate.Commercializzazione)
	if result.commSaving != nil {
		result.hasSavings = true
	}

	return result
}

// buildCurrentOctopusRates costruisce le tariffe Octopus attuali per
// l'utente (_build_current_octopus_rates): luce (e gas se presente) con
// energia e commercializzazione. È il valore salvato in last_notified_rates.
//
// Va in panic con KeyError se la tariffa trovata non ha commercializzazione.
func buildCurrentOctopusRates(user *database.User, currentRates database.CurrentRates) database.LastNotifiedRates {
	result := database.LastNotifiedRates{}

	// Luce
	if luceRate, ok := currentRates.Get("luce", user.Luce.Tipo, user.Luce.Fascia); ok {
		result["luce"] = database.NotifiedRate(luceRate.Energia, rateComm(luceRate))
	}

	// Gas (se l'utente ce l'ha)
	if user.Gas != nil {
		if gasRate, ok := currentRates.Get("gas", user.Gas.Tipo, user.Gas.Fascia); ok {
			result["gas"] = database.NotifiedRate(gasRate.Energia, rateComm(gasRate))
		}
	}

	return result
}

// shouldNotifyUser controlla se l'utente va notificato (_should_notify_user):
// false se è già stato notificato per le stesse tariffe Octopus.
func shouldNotifyUser(user *database.User, currentOctopus database.LastNotifiedRates) bool {
	return !user.LastNotifiedRates.Equal(currentOctopus)
}

// calculateUtilitySavings calcola il risparmio stimato annuo in € per una
// singola utility, "luce" o "gas" (_calculate_utility_savings).
//
// Restituisce nil (None) se l'utente non ha inserito consumi per l'utility,
// se non ha la fornitura o se non esiste una tariffa corrente corrispondente;
// altrimenti il risparmio (positivo = risparmio, negativo = aumento).
func calculateUtilitySavings(utilityType string, user *database.User, currentRates database.CurrentRates) *float64 {
	var consumo float64
	var utility database.Utility

	switch utilityType {
	case "luce":
		utility = user.Luce
		// Verifica consumi luce
		if utility.ConsumoF1 == nil {
			return nil
		}
		// Calcola consumo totale luce (consumo_f2/f3 assenti valgono 0)
		consumoF2, consumoF3 := 0.0, 0.0
		if utility.ConsumoF2 != nil {
			consumoF2 = *utility.ConsumoF2
		}
		if utility.ConsumoF3 != nil {
			consumoF3 = *utility.ConsumoF3
		}
		consumo = *utility.ConsumoF1 + consumoF2 + consumoF3

	case "gas":
		// Verifica che l'utente abbia il gas
		if user.Gas == nil {
			return nil
		}
		utility = *user.Gas
		// Verifica consumi gas
		if utility.ConsumoAnnuo == nil {
			return nil
		}
		consumo = *utility.ConsumoAnnuo

	default:
		return nil
	}

	// Nuove tariffe Octopus
	rate, ok := currentRates.Get(utilityType, utility.Tipo, utility.Fascia)
	if !ok {
		return nil
	}
	newEnergia := rate.Energia
	newComm := rateComm(rate)

	// Calcola risparmio. La conversione esplicita impedisce al compilatore di
	// fondere moltiplicazione e somma (FMA), che darebbe arrotondamenti
	// diversi da Python.
	risparmioEnergia := float64((utility.Energia - newEnergia) * consumo)
	risparmioComm := utility.Commercializzazione - newComm
	risparmio := risparmioEnergia + risparmioComm
	return &risparmio
}

// shouldShowUtility determina se mostrare una utility ("luce" o "gas") nel
// messaggio di notifica (_should_show_utility).
//
// Logica:
//   - non mixed (ha savings) → MOSTRA sempre (con stima risparmio se ci sono consumi)
//   - mixed senza consumi → MOSTRA (con suggerimento)
//   - mixed con consumi: risparmio > 0 → MOSTRA (con stima); risparmio ≤ 0 → NON MOSTRA
//
// Restituisce (shouldShow, estimatedSavings); estimatedSavings è nil se non
// disponibile.
func shouldShowUtility(utilityType string, savings Savings, user *database.User, currentRates database.CurrentRates) (bool, *float64) {
	var isMixed, hasSavings bool
	switch utilityType {
	case "luce":
		isMixed = savings.Luce.IsMixed
		hasSavings = savings.Luce.hasImprovement()
	case "gas":
		// Se utente non ha gas, non mostrare
		if user.Gas == nil {
			return false, nil
		}
		isMixed = savings.Gas.IsMixed
		hasSavings = savings.Gas.hasImprovement()
	default:
		return false, nil
	}

	// Non mixed con savings → mostra sempre, calcola risparmio se ci sono consumi
	if !isMixed && hasSavings {
		return true, calculateUtilitySavings(utilityType, user, currentRates)
	}

	// Mixed → calcola risparmio se ci sono consumi
	if isMixed {
		estimatedSavings := calculateUtilitySavings(utilityType, user, currentRates)
		if estimatedSavings == nil {
			// Nessun consumo → mostra con suggerimento
			return true, nil
		}
		// Ha consumi → mostra solo se risparmio > 0
		return *estimatedSavings > 0, estimatedSavings
	}

	// Nessun savings → non mostrare
	return false, nil
}
