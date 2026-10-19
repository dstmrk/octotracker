package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
var usersMigrations = []struct{ column, ddl string }{
	{"luce_scadenza", "ALTER TABLE users ADD COLUMN luce_scadenza TEXT"},
	{"gas_scadenza", "ALTER TABLE users ADD COLUMN gas_scadenza TEXT"},
	{"luce_scadenza_reminded", "ALTER TABLE users ADD COLUMN luce_scadenza_reminded TEXT"},
	{"gas_scadenza_reminded", "ALTER TABLE users ADD COLUMN gas_scadenza_reminded TEXT"},
}

// Store è il database SQLite di OctoTracker. Sostituisce le funzioni di
// modulo e la variabile globale DB_FILE di database.py.
type Store struct {
	db   *sql.DB
	path string
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
	existing, err := s.userColumns(ctx)
	if err != nil {
		return err
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

// userColumns restituisce i nomi delle colonne della tabella users.
func (s *Store) userColumns(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM pragma_table_info('users')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
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

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// ========== Utenti ==========

// selectUserSQL legge le colonne di users usate da rowToUser.
const selectUserSQL = `SELECT user_id, luce_tipo, luce_fascia, luce_energia, luce_commercializzazione,
	luce_consumo_f1, luce_consumo_f2, luce_consumo_f3, luce_scadenza,
	gas_tipo, gas_fascia, gas_energia, gas_commercializzazione, gas_consumo_annuo, gas_scadenza,
	last_notified_rates FROM users`

// rowToUser converte una riga di selectUserSQL nel modello User
// (_row_to_dict): il gas è presente solo se gas_tipo non è vuoto.
func rowToUser(rows *sql.Rows) (*User, error) {
	var (
		u                                             User
		luceF1, luceF2, luceF3, gasEnergia, gasComm   sql.NullFloat64
		gasConsumo                                    sql.NullFloat64
		luceScadenza, gasTipo, gasFascia, gasScadenza sql.NullString
		lastNotified                                  sql.NullString
	)
	err := rows.Scan(&u.ID, &u.Luce.Tipo, &u.Luce.Fascia, &u.Luce.Energia, &u.Luce.Commercializzazione,
		&luceF1, &luceF2, &luceF3, &luceScadenza,
		&gasTipo, &gasFascia, &gasEnergia, &gasComm, &gasConsumo, &gasScadenza, &lastNotified)
	if err != nil {
		return nil, err
	}
	u.Luce.ConsumoF1, u.Luce.ConsumoF2, u.Luce.ConsumoF3 = floatPtr(luceF1), floatPtr(luceF2), floatPtr(luceF3)
	u.Luce.Scadenza = luceScadenza.String
	if gasTipo.String != "" {
		u.Gas = &Utility{
			Tipo:                gasTipo.String,
			Fascia:              gasFascia.String,
			Energia:             gasEnergia.Float64,
			Commercializzazione: gasComm.Float64,
			ConsumoAnnuo:        floatPtr(gasConsumo),
			Scadenza:            gasScadenza.String,
		}
	}
	if lastNotified.String != "" {
		if err := json.Unmarshal([]byte(lastNotified.String), &u.LastNotifiedRates); err != nil {
			return nil, err
		}
	}
	return &u, nil
}

// queryUsers esegue selectUserSQL (con il filtro where) e converte le righe.
func (s *Store) queryUsers(where string, args ...any) ([]*User, error) {
	rows, err := s.db.QueryContext(context.Background(), selectUserSQL+where, args...)
	if err != nil {
		logDBError(err)
		return nil, err
	}
	defer rows.Close()
	users := []*User{}
	for rows.Next() {
		u, err := rowToUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// LoadUsers carica tutti gli utenti dal database (load_users), nell'ordine
// delle righe (rowid) come il dict Python. Su errore restituisce una slice
// vuota.
func (s *Store) LoadUsers() []*User {
	users, err := s.queryUsers("")
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Errore caricamento utenti: %v", err))
		return []*User{}
	}
	logger.Debug(fmt.Sprintf("Caricati %d utenti dal database", len(users)))
	return users
}

// LoadUser carica un singolo utente (load_user). Restituisce nil se
// l'utente non esiste o in caso di errore.
func (s *Store) LoadUser(userID string) *User {
	users, err := s.queryUsers(" WHERE user_id = ?", userID)
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Errore caricamento utente %s: %v", userID, err))
		return nil
	}
	if len(users) == 0 {
		return nil
	}
	return users[0]
}

// validateUtility verifica tipo e fascia di una fornitura
// (_validate_luce_data / _validate_gas_data); servizio è "luce" o "gas".
func validateUtility(servizio, tipo, fascia string) error {
	fasce := ValidFasceLuce
	if servizio == "gas" {
		fasce = ValidFasceGas
	}
	if !ValidTypes[tipo] {
		return fmt.Errorf("%s.tipo non valido: '%s'. Valori ammessi: %s", servizio, tipo, setString(ValidTypes))
	}
	if !fasce[fascia] {
		return fmt.Errorf("%s.fascia non valida: '%s'. Valori ammessi: %s", servizio, fascia, setString(fasce))
	}
	return nil
}

// setString elenca in ordine alfabetico i valori di un insieme.
func setString(set map[string]bool) string {
	return strings.Join(slices.Sorted(maps.Keys(set)), ", ")
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
	// Valida i dati luce (obbligatori) e gas (opzionali), poi serializza
	// last_notified_rates se presente.
	err := validateUtility("luce", u.Luce.Tipo, u.Luce.Fascia)
	if err == nil && u.Gas != nil {
		err = validateUtility("gas", u.Gas.Tipo, u.Gas.Fascia)
	}
	var lastNotified any
	if err == nil && len(u.LastNotifiedRates) > 0 {
		var js []byte
		js, err = json.Marshal(u.LastNotifiedRates)
		lastNotified = string(js)
	}
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Validazione fallita per %s: %v", userID, err))
		if js, jerr := json.Marshal(u); jerr == nil {
			logger.Debug(fmt.Sprintf("   user_data ricevuto: %s", js))
		}
		return err
	}

	gas := u.Gas
	if gas == nil {
		gas = &Utility{}
	}
	_, err = s.db.ExecContext(context.Background(), saveUserSQL,
		userID,
		u.Luce.Tipo,
		u.Luce.Fascia,
		u.Luce.Energia,
		u.Luce.Commercializzazione,
		nullableString(gas.Tipo),
		nullableString(gas.Fascia),
		gasFloat(u.Gas, gas.Energia),
		gasFloat(u.Gas, gas.Commercializzazione),
		nullableFloat(u.Luce.ConsumoF1),
		nullableFloat(u.Luce.ConsumoF2),
		nullableFloat(u.Luce.ConsumoF3),
		nullableFloat(gas.ConsumoAnnuo),
		nullableString(u.Luce.Scadenza),
		nullableString(gas.Scadenza),
		lastNotified,
	)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore database salvando %s: %v", userID, err))
		return err
	}
	return nil
}

// gasFloat restituisce v, oppure NULL se l'utente non ha il gas.
func gasFloat(gas *Utility, v float64) any {
	if gas == nil {
		return nil
	}
	return v
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
