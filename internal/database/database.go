package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dstmrk/octotracker/internal/logging"

	_ "modernc.org/sqlite" // driver "sqlite"
)

var logger = logging.Logger("database")

// DBFileName è il nome del file database nella directory dati (DB_FILE).
const DBFileName = "octotracker.db"

// Schema è lo schema SQL, identico a SCHEMA di database.py.
const Schema = `
CREATE TABLE IF NOT EXISTS users (
    user_id TEXT PRIMARY KEY,
    luce_tipo TEXT NOT NULL,
    luce_fascia TEXT NOT NULL,
    luce_energia REAL NOT NULL,
    luce_commercializzazione REAL NOT NULL,
    gas_tipo TEXT,
    gas_fascia TEXT,
    gas_energia REAL,
    gas_commercializzazione REAL,
    luce_consumo_f1 REAL,
    luce_consumo_f2 REAL,
    luce_consumo_f3 REAL,
    gas_consumo_annuo REAL,
    luce_scadenza TEXT,
    gas_scadenza TEXT,
    luce_scadenza_reminded TEXT,
    gas_scadenza_reminded TEXT,
    last_notified_rates TEXT,
    pending_rates TEXT,
    last_feedback_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS feedback (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    feedback_type TEXT NOT NULL,
    rating INTEGER,
    comment TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_feedback_user ON feedback(user_id);
CREATE INDEX IF NOT EXISTS idx_feedback_created ON feedback(created_at DESC);

CREATE TABLE IF NOT EXISTS rate_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    data_fonte TEXT NOT NULL,
    servizio TEXT NOT NULL,
    tipo TEXT NOT NULL,
    fascia TEXT NOT NULL,
    energia REAL NOT NULL,
    commercializzazione REAL,
    cod_offerta TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(data_fonte, servizio, tipo, fascia)
);

CREATE INDEX IF NOT EXISTS idx_rate_history_data ON rate_history(data_fonte);
CREATE INDEX IF NOT EXISTS idx_rate_history_servizio ON rate_history(servizio, tipo, fascia);
`

// usersMigrations sono le colonne aggiunte dopo la creazione iniziale della
// tabella users (_USERS_MIGRATIONS), nell'ordine in cui vengono applicate.
// Ogni voce porta la sua istruzione DDL costante: niente SQL formattato a
// runtime.
var usersMigrations = []struct{ column, columnType, ddl string }{
	{"luce_scadenza", "TEXT", "ALTER TABLE users ADD COLUMN luce_scadenza TEXT"},
	{"gas_scadenza", "TEXT", "ALTER TABLE users ADD COLUMN gas_scadenza TEXT"},
	{"luce_scadenza_reminded", "TEXT", "ALTER TABLE users ADD COLUMN luce_scadenza_reminded TEXT"},
	{"gas_scadenza_reminded", "TEXT", "ALTER TABLE users ADD COLUMN gas_scadenza_reminded TEXT"},
}

// Store è il database SQLite di OctoTracker. Sostituisce le funzioni di
// modulo e la variabile globale DB_FILE di database.py.
type Store struct {
	db   *sql.DB
	path string
}

// querier è l'insieme di metodi comune a *sql.DB, *sql.Conn e *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Open apre (creandolo se serve) il database in path e lo inizializza con
// InitDB. Ogni connessione usa busy_timeout di 30s e foreign_keys ON, come
// get_connection(). L'accesso è serializzato su una sola connessione.
func Open(path string) (*Store, error) {
	if strings.ContainsRune(path, '?') {
		return nil, fmt.Errorf("percorso database non valido (contiene '?'): %q", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.Error(fmt.Sprintf("❌ Errore inizializzazione database: %v", err))
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)")
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Errore inizializzazione database: %v", err))
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: path}
	if err := s.InitDB(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Path restituisce il percorso del file database.
func (s *Store) Path() string { return s.path }

// Close chiude il database.
func (s *Store) Close() error { return s.db.Close() }

// logDBError replica il log di get_connection() su sqlite3.Error.
func logDBError(err error) {
	logger.Error(fmt.Sprintf("❌ Errore database: %v", err))
}

// InitDB inizializza il database e crea tabelle (init_db): esegue lo schema
// e applica la migrazione idempotente delle colonne users. Restituisce
// l'errore (in Python l'eccezione viene rilanciata).
func (s *Store) InitDB() error {
	ctx := context.Background()
	err := func() error {
		if _, err := s.db.ExecContext(ctx, Schema); err != nil {
			return err
		}
		return s.migrateUsersSchema(ctx)
	}()
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore inizializzazione database: %v", err))
		return err
	}
	logger.Info("✅ Database inizializzato")
	return nil
}

// migrateUsersSchema aggiunge le colonne mancanti alla tabella users
// (_migrate_users_schema). SQLite non supporta ADD COLUMN IF NOT EXISTS,
// quindi controlliamo prima le colonne esistenti tramite PRAGMA table_info.
func (s *Store) migrateUsersSchema(ctx context.Context) error {
	maps, err := queryMaps(ctx, s.db, "PRAGMA table_info(users)")
	if err != nil {
		return err
	}
	existing := make(map[string]bool, len(maps))
	for _, m := range maps {
		existing[m.str("name")] = true
	}
	for _, mig := range usersMigrations {
		if existing[mig.column] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, mig.ddl); err != nil {
			return err
		}
		logger.Info(fmt.Sprintf("🔧 Migrazione DB: aggiunta colonna users.%s", mig.column))
	}
	return nil
}

// ========== Righe come dict (sqlite3.Row) ==========

// rowMap è una riga letta per nome di colonna, come sqlite3.Row.
type rowMap map[string]any

// queryMaps esegue una query e restituisce tutte le righe come rowMap.
func queryMaps(ctx context.Context, q querier, query string, args ...any) ([]rowMap, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []rowMap
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(rowMap, len(cols))
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// has replica _row_has_column.
func (m rowMap) has(col string) bool {
	_, ok := m[col]
	return ok
}

// truthy replica la truthiness Python del valore di colonna.
func (m rowMap) truthy(col string) bool {
	return pyTruthy(m[col])
}

func pyTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case []byte:
		return len(x) > 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case bool:
		return x
	}
	return true
}

func (m rowMap) str(col string) string {
	return valueString(m[col])
}

func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return pyFloatRepr(x)
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	}
	return fmt.Sprint(v)
}

func (m rowMap) float(col string) float64 {
	f, _ := valueFloat(m[col])
	return f
}

func (m rowMap) floatPtr(col string) *float64 {
	if m[col] == nil {
		return nil
	}
	f, _ := valueFloat(m[col])
	return &f
}

func valueFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	case []byte:
		return valueFloat(string(x))
	}
	return 0, false
}

func nullableFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ========== Utenti ==========

// rowToUser converte una riga users nel modello User (_row_to_dict).
func rowToUser(m rowMap) (*User, error) {
	u := &User{
		ID: m.str("user_id"),
		Luce: Utility{
			Tipo:                m.str("luce_tipo"),
			Fascia:              m.str("luce_fascia"),
			Energia:             m.float("luce_energia"),
			Commercializzazione: m.float("luce_commercializzazione"),
			ConsumoF1:           m.floatPtr("luce_consumo_f1"),
			ConsumoF2:           m.floatPtr("luce_consumo_f2"),
			ConsumoF3:           m.floatPtr("luce_consumo_f3"),
		},
	}
	if m.has("luce_scadenza") && m.truthy("luce_scadenza") {
		u.Luce.Scadenza = m.str("luce_scadenza")
	}

	if m.truthy("gas_tipo") {
		gas := Utility{
			Tipo:                m.str("gas_tipo"),
			Fascia:              m.str("gas_fascia"),
			Energia:             m.float("gas_energia"),
			Commercializzazione: m.float("gas_commercializzazione"),
			ConsumoAnnuo:        m.floatPtr("gas_consumo_annuo"),
		}
		if m.has("gas_scadenza") && m.truthy("gas_scadenza") {
			gas.Scadenza = m.str("gas_scadenza")
		}
		u.Gas = &gas
	}

	if m.truthy("last_notified_rates") {
		v, err := pyLoads(m.str("last_notified_rates"))
		if err != nil {
			return nil, err
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, errNotObject
		}
		u.LastNotifiedRates = obj
	}
	return u, nil
}

// LoadUsers carica tutti gli utenti dal database (load_users), nell'ordine
// delle righe (rowid) come il dict Python. Su errore restituisce una slice
// vuota.
func (s *Store) LoadUsers() []*User {
	ctx := context.Background()
	maps, err := queryMaps(ctx, s.db, "SELECT * FROM users")
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore caricamento utenti: %v", err))
		return []*User{}
	}
	users := make([]*User, 0, len(maps))
	for _, m := range maps {
		u, err := rowToUser(m)
		if err != nil {
			logger.Error(fmt.Sprintf("❌ Errore caricamento utenti: %v", err))
			return []*User{}
		}
		users = append(users, u)
	}
	logger.Debug(fmt.Sprintf("Caricati %d utenti dal database", len(users)))
	return users
}

// LoadUser carica un singolo utente (load_user). Restituisce nil se
// l'utente non esiste o in caso di errore.
func (s *Store) LoadUser(userID string) *User {
	ctx := context.Background()
	maps, err := queryMaps(ctx, s.db, "SELECT * FROM users WHERE user_id = ?", userID)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore caricamento utente %s: %v", userID, err))
		return nil
	}
	if len(maps) == 0 {
		return nil
	}
	u, err := rowToUser(maps[0])
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Errore caricamento utente %s: %v", userID, err))
		return nil
	}
	return u
}

// validateLuceData valida i dati della fornitura luce (_validate_luce_data).
func validateLuceData(tipo, fascia any) error {
	if !inStringSet(tipo, ValidTypes) {
		return fmt.Errorf("luce.tipo non valido: '%s'. Valori ammessi: %s", pyStr(tipo), pySetRepr(ValidTypes))
	}
	if !inStringSet(fascia, ValidFasceLuce) {
		return fmt.Errorf("luce.fascia non valida: '%s'. Valori ammessi: %s", pyStr(fascia), pySetRepr(ValidFasceLuce))
	}
	return nil
}

// validateGasData valida i dati della fornitura gas (_validate_gas_data).
func validateGasData(tipo, fascia any) error {
	if !inStringSet(tipo, ValidTypes) {
		return fmt.Errorf("gas.tipo non valido: '%s'. Valori ammessi: %s", pyStr(tipo), pySetRepr(ValidTypes))
	}
	if !inStringSet(fascia, ValidFasceGas) {
		return fmt.Errorf("gas.fascia non valida: '%s'. Valori ammessi: %s", pyStr(fascia), pySetRepr(ValidFasceGas))
	}
	return nil
}

func inStringSet(v any, set map[string]bool) bool {
	s, ok := v.(string)
	return ok && set[s]
}

// extractGasFields estrae i campi gas se presenti, nil altrimenti
// (_extract_gas_fields): tipo, fascia, energia, commercializzazione, consumo_annuo.
func extractGasFields(gas *Utility) [5]any {
	if gas == nil {
		return [5]any{}
	}
	return [5]any{gas.Tipo, gas.Fascia, gas.Energia, gas.Commercializzazione, nullableFloat(gas.ConsumoAnnuo)}
}

// userODict rappresenta l'utente come il dict Python (per i log di debug).
func userODict(u User) odict {
	d := odict{{"luce", utilityODict(u.Luce)}}
	if u.Gas != nil {
		d = append(d, okv{"gas", utilityODict(*u.Gas)})
	}
	if len(u.LastNotifiedRates) > 0 {
		d = append(d, okv{"last_notified_rates", map[string]any(u.LastNotifiedRates)})
	}
	return d
}

// utilityODict: chiavi nell'ordine di _row_to_dict.
func utilityODict(u Utility) odict {
	d := odict{{"tipo", u.Tipo}, {"fascia", u.Fascia}, {"energia", u.Energia}, {"commercializzazione", u.Commercializzazione}}
	for _, c := range []struct {
		k string
		v *float64
	}{{"consumo_f1", u.ConsumoF1}, {"consumo_f2", u.ConsumoF2}, {"consumo_f3", u.ConsumoF3}, {"consumo_annuo", u.ConsumoAnnuo}} {
		if c.v != nil {
			d = append(d, okv{c.k, *c.v})
		}
	}
	if u.Scadenza != "" {
		d = append(d, okv{"scadenza", u.Scadenza})
	}
	return d
}

const saveUserSQL = `
                INSERT INTO users (
                    user_id, luce_tipo, luce_fascia, luce_energia, luce_commercializzazione,
                    gas_tipo, gas_fascia, gas_energia, gas_commercializzazione,
                    luce_consumo_f1, luce_consumo_f2, luce_consumo_f3, gas_consumo_annuo,
                    luce_scadenza, gas_scadenza,
                    last_notified_rates, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
                ON CONFLICT(user_id) DO UPDATE SET
                    luce_tipo = excluded.luce_tipo,
                    luce_fascia = excluded.luce_fascia,
                    luce_energia = excluded.luce_energia,
                    luce_commercializzazione = excluded.luce_commercializzazione,
                    gas_tipo = excluded.gas_tipo,
                    gas_fascia = excluded.gas_fascia,
                    gas_energia = excluded.gas_energia,
                    gas_commercializzazione = excluded.gas_commercializzazione,
                    luce_consumo_f1 = excluded.luce_consumo_f1,
                    luce_consumo_f2 = excluded.luce_consumo_f2,
                    luce_consumo_f3 = excluded.luce_consumo_f3,
                    gas_consumo_annuo = excluded.gas_consumo_annuo,
                    luce_scadenza = excluded.luce_scadenza,
                    gas_scadenza = excluded.gas_scadenza,
                    last_notified_rates = excluded.last_notified_rates,
                    updated_at = CURRENT_TIMESTAMP
            `

// SaveUser salva o aggiorna un utente (save_user, UPSERT). userID è la
// chiave; u.ID viene ignorato. Restituisce false se la validazione di
// tipo/fascia fallisce o in caso di errore database.
func (s *Store) SaveUser(userID string, u User) bool {
	if err := s.saveUser(userID, u); err != nil {
		return false
	}
	logger.Debug(fmt.Sprintf("Utente %s salvato nel database", userID))
	return true
}

func (s *Store) saveUser(userID string, u User) error {
	validationFailed := func(err error) error {
		logger.Error(fmt.Sprintf("❌ Validazione fallita per %s: %v", userID, err))
		logger.Debug(fmt.Sprintf("   user_data ricevuto: %s", pyRepr(userODict(u))))
		return err
	}

	// Estrai e valida dati luce (obbligatori)
	if err := validateLuceData(u.Luce.Tipo, u.Luce.Fascia); err != nil {
		return validationFailed(err)
	}
	// Estrai e valida dati gas (opzionali)
	if u.Gas != nil {
		if err := validateGasData(u.Gas.Tipo, u.Gas.Fascia); err != nil {
			return validationFailed(err)
		}
	}
	gas := extractGasFields(u.Gas)
	var gasScadenza any
	if u.Gas != nil {
		gasScadenza = nullableString(u.Gas.Scadenza)
	}

	// Serializza last_notified_rates se presente
	var lastNotified any
	if len(u.LastNotifiedRates) > 0 {
		js, err := pyDumps(map[string]any(u.LastNotifiedRates))
		if err != nil {
			return validationFailed(err)
		}
		lastNotified = js
	}

	_, err := s.db.ExecContext(context.Background(), saveUserSQL,
		userID,
		u.Luce.Tipo,
		u.Luce.Fascia,
		u.Luce.Energia,
		u.Luce.Commercializzazione,
		gas[0], gas[1], gas[2], gas[3],
		nullableFloat(u.Luce.ConsumoF1),
		nullableFloat(u.Luce.ConsumoF2),
		nullableFloat(u.Luce.ConsumoF3),
		gas[4],
		nullableString(u.Luce.Scadenza),
		gasScadenza,
		lastNotified,
	)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore database salvando %s: %v", userID, err))
		return err
	}
	return nil
}

// RemoveUser rimuove un utente (remove_user). Restituisce true solo se una
// riga è stata effettivamente eliminata.
func (s *Store) RemoveUser(userID string) bool {
	res, err := s.db.ExecContext(context.Background(), "DELETE FROM users WHERE user_id = ?", userID)
	var n int64
	if err == nil {
		n, err = res.RowsAffected()
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore rimozione utente %s: %v", userID, err))
		return false
	}
	if n > 0 {
		logger.Info(fmt.Sprintf("🗑️  Utente %s rimosso dal database", userID))
	}
	return n > 0
}

// UserExists controlla se un utente esiste (user_exists).
func (s *Store) UserExists(userID string) bool {
	var one int64
	err := s.db.QueryRowContext(context.Background(),
		"SELECT 1 FROM users WHERE user_id = ? LIMIT 1", userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore controllo esistenza utente %s: %v", userID, err))
		return false
	}
	return true
}

// GetUserCount restituisce il numero totale di utenti (get_user_count), 0 su errore.
func (s *Store) GetUserCount() int {
	var n int
	err := s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) as count FROM users").Scan(&n)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore conteggio utenti: %v", err))
		return 0
	}
	return n
}
