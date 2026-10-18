package scraper

// Porting di backfill_rate_history.py: popola lo storico tariffe scaricando i
// dati ARERA degli ultimi N giorni. Può essere eseguito più volte: le
// combinazioni (data, servizio, tipo, fascia) già presenti vengono saltate.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

var backfillLogger = logging.Logger("backfill_rate_history")

// Default degli argomenti di backfill() e di argparse.
const (
	// DefaultBackfillDays è il default di --days.
	DefaultBackfillDays = 365
	// DefaultBackfillDelay è il default di --delay (secondi).
	DefaultBackfillDelay = 0.5
)

// HistoryStore è la parte di *database.Store usata da Backfill
// (get_rate_history_dates e save_rates_batch).
type HistoryStore interface {
	RateStore
	GetRateHistoryDates() map[database.RateKey]struct{}
}

// BackfillOptions sono gli argomenti di backfill(days, dry_run, delay).
// Usare DefaultBackfillOptions per i default Python (lo zero value ha
// Days=0 e Delay=0).
type BackfillOptions struct {
	// Days è il numero di giorni indietro da scaricare (--days).
	Days int
	// DryRun simula senza scrivere nel database (--dry-run).
	DryRun bool
	// Delay sono i secondi di attesa dopo ogni salvataggio (--delay), con la
	// validazione di time.sleep (negativo, NaN o troppo grande sollevano).
	Delay float64

	// Client sostituisce il Client di default (nil = &Client{}).
	Client *Client
	// Sleep sostituisce l'attesa (nil = attesa interrompibile da ctx).
	Sleep func(ctx context.Context, d time.Duration) error
}

// DefaultBackfillOptions restituisce i default di backfill(): 365 giorni,
// niente dry-run, 0.5 secondi di attesa.
func DefaultBackfillOptions() BackfillOptions {
	return BackfillOptions{Days: DefaultBackfillDays, Delay: DefaultBackfillDelay}
}

// downloadAndParseDate è _download_and_parse_date: scarica e parsea luce e
// gas per una data; un servizio non disponibile (download fallito o
// eccezione nel parsing, normale per weekend/festivi) viene saltato in
// silenzio. Restituisce un errore solo se ctx viene annullato.
func (c *Client) downloadAndParseDate(ctx context.Context, targetDate time.Time) ([]database.RateRecord, error) {
	var luce, gas ServiceRates
	for _, s := range []struct {
		code string
		dst  *ServiceRates
	}{{"E", &luce}, {"G", &gas}} {
		url := c.buildAreraURL(targetDate, s.code)
		result, err := c.fetchXML(ctx, url, s.code)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			continue
		}
		parsed, err := result()
		if err != nil {
			continue
		}
		*s.dst = parsed.rates
	}
	return flattenRates(luce, gas), nil
}

// pySleepDuration replica la conversione e la validazione di time.sleep(secs).
func pySleepDuration(secs float64) (time.Duration, error) {
	switch {
	case math.IsNaN(secs):
		return 0, newPyException("ValueError", "Invalid value NaN (not a number)")
	case secs < 0:
		return 0, newPyException("ValueError", "sleep length must be non-negative")
	case secs*1e9 >= math.MaxInt64:
		return 0, newPyException("OverflowError", "timestamp out of range for C PyTime_t")
	}
	return time.Duration(math.Ceil(secs * 1e9)), nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// saveBatch esegue save_rates_batch dentro il try/except di backfill(): un
// panic dello store viene trattato come l'eccezione Python.
func saveBatch(store HistoryStore, dateStr string, rates []database.RateRecord) (inserted int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return store.SaveRatesBatch(dateStr, rates), nil
}

// Backfill porta backfill(days, dry_run, delay): per ogni giorno, dal più
// vecchio (oggi - Days) a oggi, scarica i dati ARERA e salva le tariffe non
// ancora presenti nello storico, poi logga il riepilogo.
//
// Lo store deve essere aperto con database.Open, che esegue init_db() (e ne
// logga "✅ Database inizializzato") prima del backfill come in Python. Il
// chiamante (sottocomando CLI) configura il logging come logging.basicConfig
// con format "%(asctime)s %(message)s" e datefmt "%H:%M:%S" (vedi
// NewBasicHandler con BackfillLogTimeLayout).
//
// Restituisce un errore se ctx viene annullato (come un KeyboardInterrupt:
// niente riepilogo) o per le eccezioni che in Python interrompono lo script
// (*PyException: date fuori range, delay non valido per time.sleep).
func Backfill(ctx context.Context, store HistoryStore, opts BackfillOptions) error {
	c := opts.Client
	if c == nil {
		c = &Client{}
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	log := backfillLogger

	existingKeys := store.GetRateHistoryDates()

	log.Info(fmt.Sprintf("📊 Combinazioni già presenti nello storico: %d", len(existingKeys)))
	log.Info(fmt.Sprintf("📅 Periodo: ultimi %d giorni", opts.Days))
	if opts.DryRun {
		log.Info("🔍 Modalità dry-run: nessuna scrittura nel DB")
	}

	totalInserted := 0
	totalSkipped := 0
	totalEmpty := 0
	totalErrors := 0

	for daysBack := opts.Days; daysBack >= 0; daysBack-- { // Dal più vecchio al più recente
		targetDate, err := subDays(c.now(), daysBack)
		if err != nil {
			return err
		}
		dateStr := formatDate(targetDate, "-")

		// Scarica e parsea
		rates, err := c.downloadAndParseDate(ctx, targetDate)
		if err != nil {
			return err
		}

		if len(rates) == 0 {
			totalEmpty++
			log.Debug(fmt.Sprintf("   %s: nessun dato disponibile", dateStr))
			continue
		}

		// Filtra le tariffe già presenti per questa data
		var newRates []database.RateRecord
		for _, r := range rates {
			key := database.RateKey{DataFonte: dateStr, Servizio: r.Servizio, Tipo: r.Tipo, Fascia: r.Fascia}
			if _, ok := existingKeys[key]; !ok {
				newRates = append(newRates, r)
			}
		}

		if len(newRates) == 0 {
			totalSkipped++
			log.Debug(fmt.Sprintf("   %s: tutte le tariffe già presenti", dateStr))
			continue
		}

		if opts.DryRun {
			log.Info(fmt.Sprintf("   %s: %d tariffe nuove trovate (dry-run, non salvate)", dateStr, len(newRates)))
			totalInserted += len(newRates)
			continue
		}

		// Salva nel DB. Come in Python, -1 (errore DB) viene sommato al
		// totale e loggato come "-1 tariffe salvate".
		inserted, err := saveBatch(store, dateStr, newRates)
		if err != nil {
			totalErrors++
			log.Error(fmt.Sprintf("   %s: errore salvataggio: %v", dateStr, err))
		} else {
			totalInserted += inserted
			log.Info(fmt.Sprintf("   %s: %d tariffe salvate", dateStr, inserted))
		}

		// Rate limiting
		d, err := pySleepDuration(opts.Delay)
		if err != nil {
			return err
		}
		if err := sleep(ctx, d); err != nil {
			return err
		}
	}

	// Riepilogo
	log.Info("")
	log.Info(strings.Repeat("=", 50))
	log.Info("📊 Riepilogo backfill:")
	log.Info(fmt.Sprintf("   Tariffe inserite: %d", totalInserted))
	log.Info(fmt.Sprintf("   Giorni saltati (già presenti): %d", totalSkipped))
	log.Info(fmt.Sprintf("   Giorni senza dati: %d", totalEmpty))
	if totalErrors > 0 {
		log.Info(fmt.Sprintf("   Errori: %d", totalErrors))
	}
	log.Info(strings.Repeat("=", 50))
	return nil
}
