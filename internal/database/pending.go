package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ========== FUNZIONI PENDING RATES ==========

// toODict costruisce il dict Python di _build_pending_rates (stesso ordine
// delle chiavi).
func (p PendingRates) toODict() odict {
	d := odict{{"luce", utilityPendingODict(p.Luce)}}
	if p.Gas != nil {
		d = append(d, okv{"gas", utilityPendingODict(*p.Gas)})
	}
	if p.UpdatedServices != nil {
		d = append(d, okv{"updated_services", p.UpdatedServices})
	}
	return d
}

// utilityPendingODict: chiavi nell'ordine di checker._build_utility_pending
// (tipo, fascia, consumi presenti, energia, commercializzazione).
func utilityPendingODict(u Utility) odict {
	d := odict{{"tipo", u.Tipo}, {"fascia", u.Fascia}}
	for _, c := range []struct {
		k string
		v *float64
	}{{"consumo_f1", u.ConsumoF1}, {"consumo_f2", u.ConsumoF2}, {"consumo_f3", u.ConsumoF3}, {"consumo_annuo", u.ConsumoAnnuo}} {
		if c.v != nil {
			d = append(d, okv{c.k, *c.v})
		}
	}
	d = append(d, okv{"energia", u.Energia}, okv{"commercializzazione", u.Commercializzazione})
	if u.Scadenza != "" {
		d = append(d, okv{"scadenza", u.Scadenza})
	}
	return d
}

// pendingFromValue converte il dict decodificato in PendingRates in modo
// permissivo (chiavi mancanti → valori zero).
func pendingFromValue(v any) (*PendingRates, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errNotObject
	}
	p := &PendingRates{}
	if lv, ok := m["luce"]; ok {
		p.Luce = utilityFromValue(lv)
	}
	if gv := m["gas"]; gv != nil {
		g := utilityFromValue(gv)
		p.Gas = &g
	}
	if us, ok := m["updated_services"]; ok {
		p.UpdatedServices = []string{}
		if list, ok := us.([]any); ok {
			for _, e := range list {
				if s, ok := e.(string); ok {
					p.UpdatedServices = append(p.UpdatedServices, s)
				}
			}
		}
	}
	return p, nil
}

func utilityFromValue(v any) Utility {
	m, _ := v.(map[string]any)
	str := func(k string) string {
		s, _ := m[k].(string)
		return s
	}
	num := func(k string) float64 {
		f, _ := valueFloat(m[k])
		return f
	}
	numPtr := func(k string) *float64 {
		if f, ok := valueFloat(m[k]); ok {
			return &f
		}
		return nil
	}
	return Utility{
		Tipo:                str("tipo"),
		Fascia:              str("fascia"),
		Energia:             num("energia"),
		Commercializzazione: num("commercializzazione"),
		ConsumoF1:           numPtr("consumo_f1"),
		ConsumoF2:           numPtr("consumo_f2"),
		ConsumoF3:           numPtr("consumo_f3"),
		ConsumoAnnuo:        numPtr("consumo_annuo"),
		Scadenza:            str("scadenza"),
	}
}

// SavePendingRates salva le tariffe in attesa di conferma per un utente
// (save_pending_rates). Restituisce true anche se l'utente non esiste
// (UPDATE senza righe), false solo in caso di errore.
func (s *Store) SavePendingRates(userID string, pending PendingRates) bool {
	pendingJSON, err := pyDumps(pending.toODict())
	if err == nil {
		_, err = s.db.ExecContext(context.Background(),
			"UPDATE users SET pending_rates = ?, updated_at = CURRENT_TIMESTAMP WHERE user_id = ?",
			pendingJSON, userID)
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
	var raw any
	err := s.db.QueryRowContext(context.Background(),
		"SELECT pending_rates FROM users WHERE user_id = ?", userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !pyTruthy(raw)) {
		return nil
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore caricamento pending_rates per %s: %v", userID, err))
		return nil
	}
	v, err := pyLoads(valueString(raw))
	var p *PendingRates
	if err == nil {
		p, err = pendingFromValue(v)
	}
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Errore caricamento pending_rates per %s: %v", userID, err))
		return nil
	}
	return p
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
	var raw any
	err = conn.QueryRowContext(ctx, "SELECT pending_rates FROM users WHERE user_id = ?", userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !pyTruthy(raw)) {
		safeRollback(ctx, conn)
		return false, ApplyNoPending
	}
	if err != nil {
		return dbError(conn, err)
	}

	validationError := func(err error) (bool, string) {
		logger.Error(fmt.Sprintf("❌ Validazione fallita per %s: %v", userID, err))
		safeRollback(ctx, conn)
		return false, ApplyValidationError
	}

	pending, err := pyLoads(valueString(raw))
	if err != nil {
		return validationError(err)
	}

	// 2. Carica dati utente corrente per validare esistenza utente
	var luceScadenza, gasScadenza, luceReminded, gasReminded any
	err = conn.QueryRowContext(ctx,
		"SELECT luce_scadenza, gas_scadenza, luce_scadenza_reminded, gas_scadenza_reminded FROM users WHERE user_id = ?",
		userID).Scan(&luceScadenza, &gasScadenza, &luceReminded, &gasReminded)
	if errors.Is(err, sql.ErrNoRows) {
		safeRollback(ctx, conn)
		return false, ApplyNoUser
	}
	if err != nil {
		return dbError(conn, err)
	}

	// 3-4b. Estrai e valida dati, calcola scadenze/marker da preservare
	params, err := buildApplyParams(pending, [4]any{luceScadenza, gasScadenza, luceReminded, gasReminded})
	if err != nil {
		return validationError(err)
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

// buildApplyParams replica i passi 3-5 di apply_pending_rates sul dict
// decodificato, nello stesso ordine di accesso alle chiavi (così il primo
// KeyError/ValueError è lo stesso di Python). current contiene i valori
// attuali di luce_scadenza, gas_scadenza, luce_scadenza_reminded,
// gas_scadenza_reminded. Restituisce i parametri dell'UPDATE (senza user_id).
func buildApplyParams(pending any, current [4]any) ([]any, error) {
	// 4. Estrai e valida dati
	luce, err := pyGetItem(pending, "luce")
	if err != nil {
		return nil, err
	}
	if err := validateUtilityValue(luce, validateLuceData); err != nil {
		return nil, err
	}
	gas, err := pyDictGet(pending, "gas", nil)
	if err != nil {
		return nil, err
	}
	if gas != nil {
		if err := validateUtilityValue(gas, validateGasData); err != nil {
			return nil, err
		}
	}
	gasFields := make([]any, 5)
	if gas != nil {
		for i, k := range []string{"tipo", "fascia", "energia", "commercializzazione"} {
			if gasFields[i], err = pyGetItem(gas, k); err != nil {
				return nil, err
			}
		}
		gasFields[4], _ = pyDictGet(gas, "consumo_annuo", nil)
	}

	// 4b. Azzera scadenza e marker reminder solo per i servizi aggiornati
	updated, _ := pyDictGet(pending, "updated_services", []any{})
	luceUpdated, err := pyContains(updated, "luce")
	if err != nil {
		return nil, err
	}
	gasUpdated, err := pyContains(updated, "gas")
	if err != nil {
		return nil, err
	}
	keep := func(updated bool, v any) any {
		if updated {
			return nil
		}
		return v
	}

	luceFields := make([]any, 4)
	for i, k := range []string{"tipo", "fascia", "energia", "commercializzazione"} {
		if luceFields[i], err = pyGetItem(luce, k); err != nil {
			return nil, err
		}
	}
	consumi := make([]any, 3)
	for i, k := range []string{"consumo_f1", "consumo_f2", "consumo_f3"} {
		consumi[i], _ = pyDictGet(luce, k, nil)
	}

	params := append([]any{}, luceFields...)
	params = append(params, gasFields[:4]...)
	params = append(params, consumi...)
	params = append(params,
		gasFields[4],
		keep(luceUpdated, current[0]),
		keep(gasUpdated, current[1]),
		keep(luceUpdated, current[2]),
		keep(gasUpdated, current[3]),
		nil, // 3. last_notified_rates azzerato (come /update)
	)
	for i, p := range params {
		params[i] = sqlParam(p)
	}
	return params, nil
}

// validateUtilityValue valida tipo/fascia di un dict utility decodificato.
func validateUtilityValue(u any, validate func(tipo, fascia any) error) error {
	tipo, err := pyGetItem(u, "tipo")
	if err != nil {
		return err
	}
	// Python valuta u["fascia"] solo se tipo è valido.
	if err := validate(tipo, "monoraria"); err != nil {
		return err
	}
	fascia, err := pyGetItem(u, "fascia")
	if err != nil {
		return err
	}
	return validate(tipo, fascia)
}

// sqlParam converte un valore JSON decodificato nel parametro che sqlite3
// riceverebbe da Python (bool → intero).
func sqlParam(v any) any {
	if b, ok := v.(bool); ok {
		if b {
			return int64(1)
		}
		return int64(0)
	}
	return v
}
