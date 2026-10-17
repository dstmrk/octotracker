package database

import "encoding/json"

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
// dict Python (vedi _row_to_dict). L'ordine dei campi riproduce l'ordine
// delle chiavi del dict, quindi json.Marshal produce le stesse chiavi
// nello stesso ordine della versione Python (usato dalla API Mini App).
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

// LastNotifiedRates è il contenuto JSON della colonna last_notified_rates:
// un oggetto JSON generico decodificato con la semantica di json.loads
// (oggetti → map[string]any, array → []any, interi → int64, decimali →
// float64, stringhe, bool, nil).
//
// Il formato scritto dal checker è
//
//	{"luce": {"energia": x, "commercializzazione": y}, "gas": {...}}
//
// (vedi NotifiedRate). Il confronto va fatto con Equal, che replica
// l'uguaglianza tra dict Python (72 == 72.0, chiave mancante ≠ None, ...).
// Una mappa nil equivale a {} (user_rates.get("last_notified_rates", {})).
type LastNotifiedRates map[string]any

// NotifiedRate costruisce la voce per servizio di LastNotifiedRates:
// {"energia": energia, "commercializzazione": commercializzazione}.
func NotifiedRate(energia, commercializzazione float64) map[string]any {
	return map[string]any{"energia": energia, "commercializzazione": commercializzazione}
}

// Equal replica `last_notified == other` tra dict Python. Nil e mappa vuota
// sono equivalenti ({}).
func (l LastNotifiedRates) Equal(other LastNotifiedRates) bool {
	return pyEqual(map[string]any(l), map[string]any(other))
}

// MarshalJSON serializza come json.dumps di Python (stesso ordine delle chiavi
// usato dal checker: luce, gas, energia, commercializzazione).
func (l LastNotifiedRates) MarshalJSON() ([]byte, error) {
	s, err := pyDumps(map[string]any(l))
	return []byte(s), err
}

// UnmarshalJSON decodifica con la semantica di json.loads.
func (l *LastNotifiedRates) UnmarshalJSON(b []byte) error {
	v, err := pyLoads(string(b))
	if err != nil {
		return err
	}
	if v == nil {
		*l = nil
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return errNotObject
	}
	*l = m
	return nil
}

// PendingRates sono le tariffe in attesa di conferma (colonna pending_rates),
// nel formato prodotto da checker._build_pending_rates:
//
//	{"luce": {...}, "gas": {...}, "updated_services": ["luce"]}
//
// Gas nil equivale a "gas" assente. UpdatedServices nil equivale a chiave
// assente (righe salvate prima dell'introduzione di updated_services);
// una slice vuota non-nil viene scritta come [].
type PendingRates struct {
	Luce            Utility
	Gas             *Utility
	UpdatedServices []string
}

// MarshalJSON serializza come json.dumps di Python, con lo stesso ordine di
// chiavi di _build_pending_rates.
func (p PendingRates) MarshalJSON() ([]byte, error) {
	s, err := pyDumps(p.toODict())
	return []byte(s), err
}

// UnmarshalJSON decodifica in modo permissivo (come load_pending_rates, che
// restituisce il dict così com'è): chiavi mancanti → valori zero.
func (p *PendingRates) UnmarshalJSON(b []byte) error {
	v, err := pyLoads(string(b))
	if err != nil {
		return err
	}
	pr, err := pendingFromValue(v)
	if err != nil {
		return err
	}
	*p = *pr
	return nil
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

// compile-time check: i tipi con serializzazione Python implementano le interfacce JSON.
var (
	_ json.Marshaler   = LastNotifiedRates(nil)
	_ json.Unmarshaler = (*LastNotifiedRates)(nil)
	_ json.Marshaler   = PendingRates{}
	_ json.Unmarshaler = (*PendingRates)(nil)
)
