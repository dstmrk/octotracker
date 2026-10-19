package database

// Valori validi per la validazione (VALID_TYPES, VALID_FASCE_LUCE, VALID_FASCE_GAS).
var (
	ValidTypes     = map[string]bool{"fissa": true, "variabile": true}
	ValidFasceLuce = map[string]bool{"monoraria": true, "bioraria": true, "trioraria": true}
	ValidFasceGas  = map[string]bool{"monoraria": true}
)

// Esiti di ApplyPendingRates (secondo elemento della tupla Python).
const (
	ApplyOK              = "ok"
	ApplyNoPending       = "no_pending"
	ApplyNoUser          = "no_user"
	ApplyValidationError = "validation_error"
	ApplyDBError         = "db_error"
)

// Utility è la fornitura luce o gas di un utente (il dict "luce"/"gas" di Python).
//
// I campi puntatore nil e Scadenza vuota corrispondono a chiavi assenti nel
// dict Python (vedi _row_to_dict) e sono omessi da json.Marshal.
type Utility struct {
	Tipo                string   `json:"tipo"`
	Fascia              string   `json:"fascia"`
	Energia             float64  `json:"energia"`
	Commercializzazione float64  `json:"commercializzazione"`
	ConsumoF1           *float64 `json:"consumo_f1,omitempty"`    // solo luce
	ConsumoF2           *float64 `json:"consumo_f2,omitempty"`    // solo luce
	ConsumoF3           *float64 `json:"consumo_f3,omitempty"`    // solo luce
	ConsumoAnnuo        *float64 `json:"consumo_annuo,omitempty"` // solo gas
	Scadenza            string   `json:"scadenza,omitempty"`      // ISO YYYY-MM-DD, solo offerte fisse
}

// User è l'utente restituito da LoadUser/LoadUsers e salvato da SaveUser.
//
// Gas nil equivale a "gas" assente (o None) nel dict Python.
// LastNotifiedRates nil/vuoto equivale a "last_notified_rates" assente.
type User struct {
	ID                string            `json:"-"`
	Luce              Utility           `json:"luce"`
	Gas               *Utility          `json:"gas,omitempty"`
	LastNotifiedRates LastNotifiedRates `json:"last_notified_rates,omitempty"`
}

// LastNotifiedRates è il contenuto JSON della colonna last_notified_rates,
// decodificato con encoding/json (oggetti → map[string]any, numeri →
// float64). Il formato scritto dal checker è
//
//	{"luce": {"energia": x, "commercializzazione": y}, "gas": {...}}
//
// (vedi NotifiedRate). Il confronto va fatto con Equal; una mappa nil
// equivale a {}.
type LastNotifiedRates map[string]any

// NotifiedRate costruisce la voce per servizio di LastNotifiedRates:
// {"energia": energia, "commercializzazione": commercializzazione}.
func NotifiedRate(energia, commercializzazione float64) map[string]any {
	return map[string]any{"energia": energia, "commercializzazione": commercializzazione}
}

// Equal confronta due LastNotifiedRates per valore, come l'uguaglianza tra
// dict della versione Python: i numeri sono confrontati come float64 (72 e
// 72.0 sono uguali), una chiave mancante è diversa da una chiave null e nil
// equivale a {}.
func (l LastNotifiedRates) Equal(other LastNotifiedRates) bool {
	return jsonEqual(map[string]any(l), map[string]any(other))
}

// jsonEqual confronta due valori JSON generici (map[string]any, []any,
// float64, string, bool, nil). Le mappe nil e vuote sono equivalenti.
func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		return ok && jsonMapEqual(x, y)
	case []any:
		y, ok := b.([]any)
		return ok && jsonSliceEqual(x, y)
	case float64, string, bool, nil:
		return a == b
	}
	return false
}

func jsonMapEqual(x, y map[string]any) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if w, ok := y[k]; !ok || !jsonEqual(v, w) {
			return false
		}
	}
	return true
}

func jsonSliceEqual(x, y []any) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if !jsonEqual(x[i], y[i]) {
			return false
		}
	}
	return true
}

// PendingRates sono le tariffe in attesa di conferma (colonna pending_rates),
// nel formato prodotto dal checker:
//
//	{"luce": {...}, "gas": {...}, "updated_services": ["luce"]}
//
// Gas nil equivale a "gas" assente. UpdatedServices nil equivale a chiave
// assente (righe salvate prima dell'introduzione di updated_services);
// una slice vuota non-nil viene scritta come [].
type PendingRates struct {
	Luce            Utility  `json:"luce"`
	Gas             *Utility `json:"gas,omitempty"`
	UpdatedServices []string `json:"updated_services,omitzero"`
}

// Rate è una tariffa corrente (foglia di get_current_rates).
// Commercializzazione e CodOfferta nil corrispondono a chiavi assenti
// (valori NULL nel DB).
type Rate struct {
	Energia             float64  `json:"energia"`
	Commercializzazione *float64 `json:"commercializzazione,omitempty"`
	CodOfferta          *string  `json:"cod_offerta,omitempty"`
}

// CurrentRates è il risultato di GetCurrentRates: servizio → tipo → fascia → Rate.
// Quando non è nil contiene sempre "luce" e "gas", ciascuno con le mappe
// "fissa" e "variabile" (eventualmente vuote).
type CurrentRates map[string]map[string]map[string]Rate

// Get replica current_rates.get(servizio, {}).get(tipo, {}).get(fascia).
func (c CurrentRates) Get(servizio, tipo, fascia string) (Rate, bool) {
	r, ok := c[servizio][tipo][fascia]
	return r, ok
}

// RateRecord è una tariffa da salvare con SaveRatesBatch (dict con chiavi
// servizio, tipo, fascia, energia, commercializzazione, cod_offerta).
type RateRecord struct {
	Servizio            string
	Tipo                string
	Fascia              string
	Energia             float64
	Commercializzazione *float64
	CodOfferta          *string
}

// RateKey è la chiave (data_fonte, servizio, tipo, fascia) di GetRateHistoryDates.
type RateKey struct {
	DataFonte string
	Servizio  string
	Tipo      string
	Fascia    string
}

// Period è il periodo coperto dallo storico ({"from": ..., "to": ...}).
type Period struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

// Stats sono le statistiche dello storico ({"min", "max", "avg"}, null se vuoto).
type Stats struct {
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
	Avg *float64 `json:"avg"`
}

// RateHistory è il risultato di GetRateHistory, serializzabile con
// encoding/json nella stessa struttura del dict Python:
//
//	{"labels": [...], "data": [...], "period": {"from": ..., "to": ...},
//	 "commercializzazione": [...],   // solo se richiesto (può contenere null)
//	 "stats": {"min": ..., "max": ..., "avg": ...}}  // solo se richiesto
//
// Labels e Data non sono mai nil (→ []). Commercializzazione è nil (chiave
// assente) se non richiesta, slice non-nil (eventualmente vuota) altrimenti.
type RateHistory struct {
	Labels              []string   `json:"labels"`
	Data                []float64  `json:"data"`
	Period              Period     `json:"period"`
	Commercializzazione []*float64 `json:"commercializzazione,omitzero"`
	Stats               *Stats     `json:"stats,omitempty"`
}

// Feedback è una riga restituita da GetRecentFeedbacks.
// CreatedAt è in ora locale ("YYYY-MM-DD HH:MM:SS", datetime(created_at, 'localtime')).
type Feedback struct {
	ID           int64   `json:"id"`
	UserID       string  `json:"user_id"`
	FeedbackType string  `json:"feedback_type"`
	Rating       *int    `json:"rating"`
	Comment      *string `json:"comment"`
	CreatedAt    *string `json:"created_at"`
}

// ScadenzaReminded è, per un utente, la scadenza già notificata per servizio
// (il dict {"luce": ..., "gas": ...} di get_scadenza_reminded_map; nil = None).
type ScadenzaReminded struct {
	Luce *string `json:"luce"`
	Gas  *string `json:"gas"`
}

// Get replica reminded.get(servizio): nil se il servizio è sconosciuto o non notificato.
func (r ScadenzaReminded) Get(servizio string) *string {
	switch servizio {
	case "luce":
		return r.Luce
	case "gas":
		return r.Gas
	}
	return nil
}
