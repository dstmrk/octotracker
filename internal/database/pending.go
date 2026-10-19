package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// ========== FUNZIONI PENDING RATES ==========

// SavePendingRates salva le tariffe in attesa di conferma per un utente
// (save_pending_rates). Restituisce true anche se l'utente non esiste
// (UPDATE senza righe), false solo in caso di errore.
func (s *Store) SavePendingRates(userID string, pending PendingRates) bool {
	pendingJSON, err := json.Marshal(pending)
	if err == nil {
		_, err = s.db.ExecContext(context.Background(),
			"UPDATE users SET pending_rates = ?, updated_at = CURRENT_TIMESTAMP WHERE user_id = ?",
			string(pendingJSON), userID)
		if err != nil {
			logDBError(err)
		}
	}
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Errore salvataggio pending_rates per %s: %v", userID, err))
		return false
	}
	logger.Debug(fmt.Sprintf("Pending rates salvate per utente %s", userID))
	return true
}

// LoadPendingRates carica le tariffe in attesa di conferma
// (load_pending_rates). Restituisce nil se non presenti o in caso di errore.
func (s *Store) LoadPendingRates(userID string) *PendingRates {
	var raw sql.NullString
	err := s.db.QueryRowContext(context.Background(),
		"SELECT pending_rates FROM users WHERE user_id = ?", userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw.String == "") {
		return nil
	}
	if err != nil {
		logDBError(err)
	} else {
		var p PendingRates
		if err = json.Unmarshal([]byte(raw.String), &p); err == nil {
			return &p
		}
	}
	logger.Error(fmt.Sprintf("❌ Errore caricamento pending_rates per %s: %v", userID, err))
	return nil
}

// ClearPendingRates rimuove le tariffe in attesa per un utente
// (clear_pending_rates).
func (s *Store) ClearPendingRates(userID string) bool {
	_, err := s.db.ExecContext(context.Background(),
		"UPDATE users SET pending_rates = NULL, updated_at = CURRENT_TIMESTAMP WHERE user_id = ?", userID)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore rimozione pending_rates per %s: %v", userID, err))
		return false
	}
	logger.Debug(fmt.Sprintf("Pending rates rimosse per utente %s", userID))
	return true
}

// safeRollback esegue ROLLBACK ignorando gli errori (_safe_rollback).
func safeRollback(ctx context.Context, conn *sql.Conn) {
	_, _ = conn.ExecContext(ctx, "ROLLBACK")
}

const applyPendingSQL = `
            UPDATE users SET
                luce_tipo = ?, luce_fascia = ?, luce_energia = ?, luce_commercializzazione = ?,
                gas_tipo = ?, gas_fascia = ?, gas_energia = ?, gas_commercializzazione = ?,
                luce_consumo_f1 = ?, luce_consumo_f2 = ?, luce_consumo_f3 = ?,
                gas_consumo_annuo = ?,
                luce_scadenza = ?, gas_scadenza = ?,
                luce_scadenza_reminded = ?, gas_scadenza_reminded = ?,
                last_notified_rates = ?,
                pending_rates = NULL,
                updated_at = CURRENT_TIMESTAMP
            WHERE user_id = ?
            `

// ApplyPendingRates applica le tariffe pendenti in modo atomico
// (apply_pending_rates): in un'unica transazione BEGIN IMMEDIATE carica
// pending_rates, verifica l'utente, valida i dati, aggiorna le tariffe
// preservando i consumi indicati nelle pending, azzera last_notified_rates
// e pending_rates, e azzera scadenza e marker reminder dei soli servizi in
// updated_services.
//
// Restituisce (true, ApplyOK) oppure (false, ApplyNoPending | ApplyNoUser |
// ApplyValidationError | ApplyDBError).
func (s *Store) ApplyPendingRates(userID string) (bool, string) {
	ctx := context.Background()
	dbError := func(conn *sql.Conn, err error) (bool, string) {
		logger.Error(fmt.Sprintf("❌ Errore database apply_pending_rates per %s: %v", userID, err))
		if conn != nil {
			safeRollback(ctx, conn)
		}
		return false, ApplyDBError
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return dbError(nil, err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return dbError(conn, err)
	}

	// 1. Carica pending_rates
	var raw sql.NullString
	err = conn.QueryRowContext(ctx, "SELECT pending_rates FROM users WHERE user_id = ?", userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw.String == "") {
		safeRollback(ctx, conn)
		return false, ApplyNoPending
	}
	if err != nil {
		return dbError(conn, err)
	}

	// 2. Carica dati utente corrente per validare esistenza utente
	var current [4]any // luce_scadenza, gas_scadenza, luce_scadenza_reminded, gas_scadenza_reminded
	err = conn.QueryRowContext(ctx,
		"SELECT luce_scadenza, gas_scadenza, luce_scadenza_reminded, gas_scadenza_reminded FROM users WHERE user_id = ?",
		userID).Scan(&current[0], &current[1], &current[2], &current[3])
	if errors.Is(err, sql.ErrNoRows) {
		safeRollback(ctx, conn)
		return false, ApplyNoUser
	}
	if err != nil {
		return dbError(conn, err)
	}

	// 3-4b. Decodifica e valida i dati, calcola scadenze/marker da preservare
	params, err := buildApplyParams(raw.String, current)
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Validazione fallita per %s: %v", userID, err))
		safeRollback(ctx, conn)
		return false, ApplyValidationError
	}

	// 5. Aggiorna utente e pulisci pending_rates in un'unica transazione
	if _, err := conn.ExecContext(ctx, applyPendingSQL, append(params, userID)...); err != nil {
		return dbError(conn, err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return dbError(conn, err)
	}
	logger.Info(fmt.Sprintf("✅ Utente %s: tariffe aggiornate atomicamente", userID))
	return true, ApplyOK
}

// pendingUtility è una fornitura del blob pending_rates letta da
// ApplyPendingRates: energia e commercializzazione sono obbligatorie,
// quindi i puntatori distinguono i valori mancanti.
type pendingUtility struct {
	Tipo                string   `json:"tipo"`
	Fascia              string   `json:"fascia"`
	Energia             *float64 `json:"energia"`
	Commercializzazione *float64 `json:"commercializzazione"`
	ConsumoF1           *float64 `json:"consumo_f1"`
	ConsumoF2           *float64 `json:"consumo_f2"`
	ConsumoF3           *float64 `json:"consumo_f3"`
	ConsumoAnnuo        *float64 `json:"consumo_annuo"`
}

// validate verifica tipo, fascia e la presenza delle tariffe.
func (u *pendingUtility) validate(servizio string) error {
	if err := validateUtility(servizio, u.Tipo, u.Fascia); err != nil {
		return err
	}
	if u.Energia == nil || u.Commercializzazione == nil {
		return fmt.Errorf("%s: energia o commercializzazione mancante", servizio)
	}
	return nil
}

// buildApplyParams decodifica e valida il blob pending_rates e restituisce i
// parametri dell'UPDATE di applyPendingSQL (senza user_id). current contiene
// i valori attuali di luce_scadenza, gas_scadenza, luce_scadenza_reminded e
// gas_scadenza_reminded, conservati per i servizi non in updated_services.
func buildApplyParams(raw string, current [4]any) ([]any, error) {
	var pending struct {
		Luce            *pendingUtility `json:"luce"`
		Gas             *pendingUtility `json:"gas"`
		UpdatedServices []string        `json:"updated_services"`
	}
	if err := json.Unmarshal([]byte(raw), &pending); err != nil {
		return nil, err
	}
	luce, gas := pending.Luce, pending.Gas
	if luce == nil {
		return nil, errors.New("luce mancante")
	}
	if err := luce.validate("luce"); err != nil {
		return nil, err
	}
	gasFields := make([]any, 5) // tipo, fascia, energia, commercializzazione, consumo_annuo
	if gas != nil {
		if err := gas.validate("gas"); err != nil {
			return nil, err
		}
		gasFields = []any{gas.Tipo, gas.Fascia, *gas.Energia, *gas.Commercializzazione, nullableFloat(gas.ConsumoAnnuo)}
	}

	// 4b. Azzera scadenza e marker reminder solo per i servizi aggiornati
	luceUpdated := slices.Contains(pending.UpdatedServices, "luce")
	gasUpdated := slices.Contains(pending.UpdatedServices, "gas")
	keep := func(updated bool, v any) any {
		if updated {
			return nil
		}
		return v
	}

	return []any{
		luce.Tipo, luce.Fascia, *luce.Energia, *luce.Commercializzazione,
		gasFields[0], gasFields[1], gasFields[2], gasFields[3],
		nullableFloat(luce.ConsumoF1), nullableFloat(luce.ConsumoF2), nullableFloat(luce.ConsumoF3),
		gasFields[4],
		keep(luceUpdated, current[0]),
		keep(gasUpdated, current[1]),
		keep(luceUpdated, current[2]),
		keep(gasUpdated, current[3]),
		nil, // 3. last_notified_rates azzerato (come /update)
	}, nil
}
