// Package database porta database.py: gestione del database SQLite di
// OctoTracker (utenti, tariffe pendenti, reminder scadenze, feedback e
// storico tariffe).
//
// Lo schema SQL (Schema) è identico a quello Python e i blob JSON
// (last_notified_rates, pending_rates) hanno le stesse chiavi e gli stessi
// valori scritti dalla versione Python (la formattazione è quella di
// encoding/json), così il DB di produzione resta leggibile da entrambe le
// versioni (anche in caso di rollback). Le stringhe CURRENT_TIMESTAMP
// mantengono il formato "YYYY-MM-DD HH:MM:SS" (UTC).
//
// # Apertura
//
//	store, err := database.Open(filepath.Join(cfg.DataDir, database.DBFileName))
//	defer store.Close()
//
// Open crea la directory padre, apre SQLite con busy_timeout 30s e
// foreign_keys ON, ed esegue InitDB (schema + migrazione colonne scadenza,
// logga "✅ Database inizializzato"). L'accesso è serializzato su una sola
// connessione: lo Store è sicuro per l'uso concorrente.
//
// # Convenzioni di ritorno (come Python)
//
// Gli errori SQLite non vengono restituiti: sono loggati con gli stessi
// messaggi Python e il metodo restituisce il valore di fallback Python
// (false, 0, nil, mappa/slice vuota). nil corrisponde a None.
//
// # Utenti
//
//	LoadUsers() []*User                      // tutti gli utenti, in ordine di inserimento (rowid) come il dict Python; vuoto su errore
//	LoadUser(userID) *User                   // nil se non esiste o errore
//	SaveUser(userID, User) bool              // UPSERT; valida tipo/fascia; non tocca reminded/pending/feedback
//	RemoveUser(userID) bool                  // true se rimosso (cascade sui feedback)
//	UserExists(userID) bool
//	GetUserCount() int
//
// User.Gas nil = solo luce. User.LastNotifiedRates è un oggetto JSON generico
// (map[string]any) da confrontare con Equal (72 == 72.0, nil == {});
// SaveUser lo scrive solo se non vuoto (altrimenti NULL). Il checker lo
// costruisce così:
//
//	cur := database.LastNotifiedRates{"luce": database.NotifiedRate(e, c)}
//	if !user.LastNotifiedRates.Equal(cur) { ... }
//
// # Tariffe pendenti
//
//	SavePendingRates(userID, PendingRates) bool   // true anche se l'utente non esiste
//	LoadPendingRates(userID) *PendingRates        // nil se assenti
//	ClearPendingRates(userID) bool
//	ApplyPendingRates(userID) (bool, string)      // transazione BEGIN IMMEDIATE atomica
//
// ApplyPendingRates restituisce (true, ApplyOK) oppure (false, uno tra
// ApplyNoPending, ApplyNoUser, ApplyValidationError, ApplyDBError). Azzera
// last_notified_rates e pending_rates; azzera scadenza e marker reminder solo
// per i servizi in updated_services. Un blob non decodificabile, con
// tipo/fascia non validi o senza energia/commercializzazione è un
// ApplyValidationError.
//
// # Reminder scadenza
//
//	GetScadenzaRemindedMap() map[string]ScadenzaReminded  // user_id → {Luce, Gas *string}
//	MarkScadenzaReminded(userID, servizio, scadenza) bool  // servizio "luce"/"gas"
//	UpdateUserScadenza(userID, servizio, scadenza) bool    // imposta scadenza e azzera il marker
//
// # Feedback
//
//	SaveFeedback(userID, feedbackType, rating *int, comment *string) bool  // aggiorna anche users.last_feedback_at
//	GetLastFeedbackTime(userID) *string     // "YYYY-MM-DD HH:MM:SS" UTC (CURRENT_TIMESTAMP), nil se mai
//	GetRecentFeedbacks(limit) []Feedback    // ordinati per created_at (ora locale) desc, id desc
//	GetFeedbackCount() int
//
// # Storico tariffe
//
//	SaveRate(dataFonte, servizio, tipo, fascia, energia, comm *float64, codOfferta *string) bool  // INSERT OR IGNORE
//	SaveRatesBatch(dataFonte, []RateRecord) int     // inserite (>=0), -1 su errore (rollback)
//	GetCurrentRates() CurrentRates                  // ultima data_fonte; nil se nessuna tariffa
//	GetLatestRateDate() *string                     // MAX(data_fonte), nil se vuoto
//	GetRateHistoryDates() map[RateKey]struct{}
//	GetRateHistory(servizio, tipo, fascia, days, includeComm, includeStats) RateHistory
//
// GetRateHistory: days <= 0 → risultato vuoto; il default Python è days=365.
// RateHistory si serializza con encoding/json nella stessa struttura del dict
// Python (chiavi opzionali e null comprese); stats arrotondate come round(x, 6)
// e media con somma compensata come sum() di Python 3.12+.
//
// # Differenze note rispetto a Python
//
// Con modelli tipizzati alcuni stati del dict Python non sono rappresentabili:
// SaveUser non può ricevere campi mancanti (il ramo KeyError non esiste; un
// tipo/fascia vuoti falliscono la validazione come ValueError). JSON
// malformato in last_notified_rates/pending_rates, che in Python solleva
// un'eccezione non gestita, qui viene loggato e trattato come errore del
// metodo (LoadUser → nil, LoadUsers → vuoto, LoadPendingRates → nil). I
// numeri dei blob sono float64: valori non finiti (NaN, ±Inf), che
// json.dumps scriverebbe come NaN/Infinity, non sono serializzabili e il
// salvataggio fallisce.
package database
