package database

import (
	"context"
	"database/sql"
	"fmt"
)

// ========== FUNZIONI REMINDER SCADENZA ==========

// GetScadenzaRemindedMap restituisce, per ogni utente, la scadenza per cui è
// già stato inviato un reminder (get_scadenza_reminded_map). Serve al task
// reminder per non inviare più volte lo stesso avviso. Su errore restituisce
// una mappa vuota.
func (s *Store) GetScadenzaRemindedMap() map[string]ScadenzaReminded {
	out := map[string]ScadenzaReminded{}
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT user_id, luce_scadenza_reminded, gas_scadenza_reminded FROM users")
	if err == nil {
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var userID string
				var luce, gas sql.NullString
				if err := rows.Scan(&userID, &luce, &gas); err != nil {
					return err
				}
				out[userID] = ScadenzaReminded{Luce: nullStringPtr(luce), Gas: nullStringPtr(gas)}
			}
			return rows.Err()
		}()
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore lettura reminder scadenza: %v", err))
		return map[string]ScadenzaReminded{}
	}
	return out
}

func nullStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

// MarkScadenzaReminded registra che il reminder di scadenza è stato inviato
// per un servizio ("luce" o "gas") e per la scadenza ISO indicata
// (mark_scadenza_reminded).
func (s *Store) MarkScadenzaReminded(userID, servizio, scadenza string) bool {
	if servizio != "luce" && servizio != "gas" {
		logger.Error(fmt.Sprintf("❌ Servizio non valido per mark_scadenza_reminded: %s", servizio))
		return false
	}
	column := servizio + "_scadenza_reminded"
	_, err := s.db.ExecContext(context.Background(),
		fmt.Sprintf("UPDATE users SET %s = ? WHERE user_id = ?", column), scadenza, userID)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore aggiornamento reminder scadenza per %s: %v", userID, err))
		return false
	}
	logger.Debug(fmt.Sprintf("Reminder scadenza %s marcato per utente %s", servizio, userID))
	return true
}

// UpdateUserScadenza imposta la data di scadenza (ISO) di un servizio e
// azzera il relativo marker reminder (update_user_scadenza). Usato quando
// l'utente inserisce una nuova data dopo aver adottato una nuova offerta
// a prezzo fisso.
func (s *Store) UpdateUserScadenza(userID, servizio, scadenza string) bool {
	if servizio != "luce" && servizio != "gas" {
		logger.Error(fmt.Sprintf("❌ Servizio non valido per update_user_scadenza: %s", servizio))
		return false
	}
	scadenzaCol := servizio + "_scadenza"
	remindedCol := servizio + "_scadenza_reminded"
	_, err := s.db.ExecContext(context.Background(),
		fmt.Sprintf("UPDATE users SET %s = ?, %s = NULL, updated_at = CURRENT_TIMESTAMP WHERE user_id = ?",
			scadenzaCol, remindedCol),
		scadenza, userID)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore aggiornamento scadenza per %s: %v", userID, err))
		return false
	}
	logger.Debug(fmt.Sprintf("Scadenza %s aggiornata per utente %s: %s", servizio, userID, scadenza))
	return true
}
