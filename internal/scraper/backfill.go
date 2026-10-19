package scraper

// Backfill dello storico tariffe: scarica i dati ARERA degli ultimi N giorni.
// Può essere eseguito più volte: le combinazioni (data, servizio, tipo,
// fascia) già presenti vengono saltate.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

var backfillLogger = logging.Logger("backfill_rate_history")

const (
	// DefaultBackfillDays è il default di --days.
	DefaultBackfillDays = 365
	// DefaultBackfillDelay è il default di --delay (secondi).
	DefaultBackfillDelay = 0.5
)

// HistoryStore è la parte di *database.Store usata da Backfill.
type HistoryStore interface {
	RateSaver
	GetRateHistoryDates() map[database.RateKey]struct{}
}

// BackfillOptions sono le opzioni di Backfill. DefaultBackfillOptions dà i
// default della CLI (lo zero value ha Days=0 e Delay=0).
type BackfillOptions struct {
	// Days è il numero di giorni indietro da scaricare (--days).
	Days int
	// DryRun simula senza scrivere nel database (--dry-run).
	DryRun bool
	// Delay sono i secondi di attesa dopo ogni salvataggio (--delay).
	Delay float64

	// Client sostituisce il Client di default (nil = &Client{}).
	Client *Client
	// Sleep sostituisce l'attesa (nil = attesa interrompibile da ctx).
	Sleep func(ctx context.Context, d time.Duration) error
}

// DefaultBackfillOptions restituisce 365 giorni, niente dry-run e 0.5
// secondi di attesa.
func DefaultBackfillOptions() BackfillOptions {
	return BackfillOptions{Days: DefaultBackfillDays, Delay: DefaultBackfillDelay}
}

// backfillRun è lo stato di un backfill: contatori del riepilogo compresi.
type backfillRun struct {
	client   *Client
	store    HistoryStore
	existing map[database.RateKey]struct{}
	dryRun   bool

	inserted, skipped, empty int
}

// downloadAndParseDate scarica e legge luce e gas di una data; un servizio
// non disponibile (download fallito o offerta illeggibile, normale nei
// weekend e festivi) viene saltato. Restituisce un errore solo se ctx viene
// annullato.
func (c *Client) downloadAndParseDate(ctx context.Context, targetDate time.Time) ([]database.RateRecord, error) {
	var luce, gas ServiceRates
	for _, s := range []struct {
		code string
		dst  *ServiceRates
	}{{"E", &luce}, {"G", &gas}} {
		result, err := c.fetchXML(ctx, c.buildAreraURL(targetDate, s.code), s.code)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			continue
		}
		if parsed, err := result(); err == nil {
			*s.dst = parsed.rates
		}
	}
	return flattenRates(luce, gas), nil
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

// Backfill scarica i dati ARERA di ogni giorno, dal più vecchio (oggi -
// Days) a oggi, salva le tariffe non ancora presenti nello storico e logga
// il riepilogo. Restituisce un errore (senza riepilogo) solo se ctx viene
// annullato.
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

	run := &backfillRun{client: c, store: store, existing: existingKeys, dryRun: opts.DryRun}
	delay := time.Duration(opts.Delay * float64(time.Second))
	for daysBack := opts.Days; daysBack >= 0; daysBack-- { // dal più vecchio al più recente
		saved, err := run.day(ctx, c.daysAgo(daysBack))
		if err != nil {
			return err
		}
		if saved {
			if err := sleep(ctx, delay); err != nil {
				return err
			}
		}
	}

	log.Info("")
	log.Info(strings.Repeat("=", 50))
	log.Info("📊 Riepilogo backfill:")
	log.Info(fmt.Sprintf("   Tariffe inserite: %d", run.inserted))
	log.Info(fmt.Sprintf("   Giorni saltati (già presenti): %d", run.skipped))
	log.Info(fmt.Sprintf("   Giorni senza dati: %d", run.empty))
	log.Info(strings.Repeat("=", 50))
	return nil
}

// day elabora un giorno e aggiorna i contatori; saved indica che è stato
// chiamato SaveRatesBatch (dopo il quale si attende il delay).
func (b *backfillRun) day(ctx context.Context, date time.Time) (saved bool, err error) {
	log := backfillLogger
	dateStr := formatDate(date)

	rates, err := b.client.downloadAndParseDate(ctx, date)
	if err != nil {
		return false, err
	}
	if len(rates) == 0 {
		b.empty++
		log.Debug(fmt.Sprintf("   %s: nessun dato disponibile", dateStr))
		return false, nil
	}

	var newRates []database.RateRecord
	for _, r := range rates {
		key := database.RateKey{DataFonte: dateStr, Servizio: r.Servizio, Tipo: r.Tipo, Fascia: r.Fascia}
		if _, ok := b.existing[key]; !ok {
			newRates = append(newRates, r)
		}
	}
	switch {
	case len(newRates) == 0:
		b.skipped++
		log.Debug(fmt.Sprintf("   %s: tutte le tariffe già presenti", dateStr))
		return false, nil
	case b.dryRun:
		b.inserted += len(newRates)
		log.Info(fmt.Sprintf("   %s: %d tariffe nuove trovate (dry-run, non salvate)", dateStr, len(newRates)))
		return false, nil
	}

	// Come nella versione Python, -1 (errore del database) viene sommato al
	// totale e loggato come "-1 tariffe salvate".
	inserted := b.store.SaveRatesBatch(dateStr, newRates)
	b.inserted += inserted
	log.Info(fmt.Sprintf("   %s: %d tariffe salvate", dateStr, inserted))
	return true, nil
}
