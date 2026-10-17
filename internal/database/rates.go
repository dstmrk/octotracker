package database

import (
	"context"
	"database/sql"
	"fmt"
)

// ========== FUNZIONI STORICO TARIFFE ==========

const insertRateSQL = `
                INSERT OR IGNORE INTO rate_history
                    (data_fonte, servizio, tipo, fascia, energia, commercializzazione, cod_offerta)
                VALUES (?, ?, ?, ?, ?, ?, ?)
                `

func nullableStringPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// SaveRate salva una tariffa nello storico (save_rate). I duplicati (stessa
// data/servizio/tipo/fascia) sono ignorati e restituiscono comunque true;
// false solo in caso di errore.
func (s *Store) SaveRate(dataFonte, servizio, tipo, fascia string, energia float64, commercializzazione *float64, codOfferta *string) bool {
	_, err := s.db.ExecContext(context.Background(), insertRateSQL,
		dataFonte, servizio, tipo, fascia, energia, nullableFloat(commercializzazione), nullableStringPtr(codOfferta))
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore salvataggio tariffa storico: %v", err))
		return false
	}
	return true
}

// SaveRatesBatch salva un batch di tariffe per una data (YYYY-MM-DD) in
// un'unica transazione, ignorando i duplicati (save_rates_batch).
// Restituisce il numero di tariffe inserite (>= 0), oppure -1 in caso di
// errore (rollback: nessuna tariffa salvata).
func (s *Store) SaveRatesBatch(dataFonte string, rates []RateRecord) int {
	if len(rates) == 0 {
		return 0
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Errore salvataggio batch tariffe: %v", err))
		return -1
	}

	inserted := 0
	err = func() error {
		for _, r := range rates {
			res, err := tx.ExecContext(ctx, insertRateSQL,
				dataFonte, r.Servizio, r.Tipo, r.Fascia, r.Energia,
				nullableFloat(r.Commercializzazione), nullableStringPtr(r.CodOfferta))
			if err != nil {
				return err
			}
			n, err := res.RowsAffected() // SELECT changes()
			if err != nil {
				return err
			}
			if n > 0 {
				inserted++
			}
		}
		return tx.Commit()
	}()
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Errore salvataggio batch tariffe: %v", err))
		if rbErr := tx.Rollback(); rbErr == nil {
			logger.Debug("Rollback transazione completato")
		}
		return -1
	}
	logger.Debug(fmt.Sprintf("Salvate %d tariffe per %s", inserted, dataFonte))
	return inserted
}

// GetCurrentRates legge le tariffe della data più recente e le restituisce
// nella struttura servizio → tipo → fascia (get_current_rates). Restituisce
// nil se non ci sono tariffe o in caso di errore.
func (s *Store) GetCurrentRates() CurrentRates {
	type row struct {
		servizio, tipo, fascia string
		rate                   Rate
	}
	var rows []row
	q, err := s.db.QueryContext(context.Background(), `
                SELECT servizio, tipo, fascia, energia, commercializzazione, cod_offerta
                FROM rate_history
                WHERE data_fonte = (SELECT MAX(data_fonte) FROM rate_history)
                `)
	if err == nil {
		err = func() error {
			defer q.Close()
			for q.Next() {
				var r row
				var comm sql.NullFloat64
				var cod sql.NullString
				if err := q.Scan(&r.servizio, &r.tipo, &r.fascia, &r.rate.Energia, &comm, &cod); err != nil {
					return err
				}
				if comm.Valid {
					v := comm.Float64
					r.rate.Commercializzazione = &v
				}
				r.rate.CodOfferta = nullStringPtr(cod)
				rows = append(rows, r)
			}
			return q.Err()
		}()
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore lettura tariffe correnti: %v", err))
		return nil
	}
	if len(rows) == 0 {
		return nil
	}

	result := CurrentRates{
		"luce": {"fissa": {}, "variabile": {}},
		"gas":  {"fissa": {}, "variabile": {}},
	}
	for _, r := range rows {
		if byTipo, ok := result[r.servizio]; ok {
			if byFascia, ok := byTipo[r.tipo]; ok {
				byFascia[r.fascia] = r.rate
			}
		}
	}
	return result
}

// GetLatestRateDate restituisce la data più recente nello storico tariffe
// (YYYY-MM-DD), nil se lo storico è vuoto o in caso di errore
// (get_latest_rate_date).
func (s *Store) GetLatestRateDate() *string {
	var v sql.NullString
	err := s.db.QueryRowContext(context.Background(),
		"SELECT MAX(data_fonte) as max_date FROM rate_history").Scan(&v)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore lettura data tariffe: %v", err))
		return nil
	}
	return nullStringPtr(v)
}

// GetRateHistoryDates restituisce le chiavi (data_fonte, servizio, tipo,
// fascia) già presenti nello storico (get_rate_history_dates). Su errore
// restituisce un insieme vuoto.
func (s *Store) GetRateHistoryDates() map[RateKey]struct{} {
	out := map[RateKey]struct{}{}
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT DISTINCT data_fonte, servizio, tipo, fascia FROM rate_history")
	if err == nil {
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var k RateKey
				if err := rows.Scan(&k.DataFonte, &k.Servizio, &k.Tipo, &k.Fascia); err != nil {
					return err
				}
				out[k] = struct{}{}
			}
			return rows.Err()
		}()
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore lettura date storico: %v", err))
		return map[RateKey]struct{}{}
	}
	return out
}

// emptyRateHistory è il risultato vuoto di get_rate_history.
func emptyRateHistory(includeCommercializzazione, includeStats bool) RateHistory {
	r := RateHistory{Labels: []string{}, Data: []float64{}}
	if includeCommercializzazione {
		r.Commercializzazione = []*float64{}
	}
	if includeStats {
		r.Stats = &Stats{}
	}
	return r
}

// GetRateHistory recupera lo storico tariffe filtrato per il grafico della
// Mini App (get_rate_history): righe con data_fonte >= date('now', '-days
// days') (UTC) in ordine di data crescente. Con days <= 0 o in caso di
// errore restituisce il risultato vuoto. Il default Python è days=365.
//
// includeCommercializzazione aggiunge la lista dei costi di
// commercializzazione (null se NULL nel DB); includeStats aggiunge
// min/max/avg arrotondati come round(x, 6).
func (s *Store) GetRateHistory(servizio, tipo, fascia string, days int, includeCommercializzazione, includeStats bool) RateHistory {
	// Gestisci days invalidi
	if days <= 0 {
		return emptyRateHistory(includeCommercializzazione, includeStats)
	}

	var labels []string
	var data []float64
	var comms []*float64
	rows, err := s.db.QueryContext(context.Background(), `
                SELECT data_fonte, energia, commercializzazione
                FROM rate_history
                WHERE servizio = ?
                  AND tipo = ?
                  AND fascia = ?
                  AND data_fonte >= date('now', '-' || ? || ' days')
                ORDER BY data_fonte ASC
                `, servizio, tipo, fascia, days)
	if err == nil {
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var label string
				var energia float64
				var comm sql.NullFloat64
				if err := rows.Scan(&label, &energia, &comm); err != nil {
					return err
				}
				labels = append(labels, label)
				data = append(data, energia)
				if comm.Valid {
					v := comm.Float64
					comms = append(comms, &v)
				} else {
					comms = append(comms, nil)
				}
			}
			return rows.Err()
		}()
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore lettura storico tariffe: %v", err))
		return emptyRateHistory(includeCommercializzazione, includeStats)
	}
	if len(labels) == 0 {
		return emptyRateHistory(includeCommercializzazione, includeStats)
	}

	from, to := labels[0], labels[len(labels)-1]
	result := RateHistory{
		Labels: labels,
		Data:   data,
		Period: Period{From: &from, To: &to},
	}
	if includeCommercializzazione {
		result.Commercializzazione = comms
	}
	if includeStats {
		result.Stats = computeStats(data)
	}
	return result
}

// computeStats calcola {"min", "max", "avg"} come get_rate_history:
// round(min(data), 6), round(max(data), 6), round(sum(data) / len(data), 6).
func computeStats(data []float64) *Stats {
	minV := pyRound(pyMin(data), 6)
	maxV := pyRound(pyMax(data), 6)
	avg := pyRound(pySum(data)/float64(len(data)), 6)
	return &Stats{Min: &minV, Max: &maxV, Avg: &avg}
}
