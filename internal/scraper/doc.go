// Package scraper porta data_reader.py (lettura delle tariffe Octopus Energy
// dai file Open Data ARERA) e backfill_rate_history.py (popolamento dello
// storico tariffe).
//
// # Lettura giornaliera (bot e `octotracker scrape`)
//
//	tariffe, err := scraper.FetchOctopusTariffe(ctx, store, scraper.DefaultMaxDaysBack)
//	logger.Info(fmt.Sprintf("✅ Scraper completato: %v", tariffe)) // %v = repr() Python
//
// Per luce ("E") e poi gas ("G") scarica il file XML del giorno, tornando
// indietro fino a maxDaysBack giorni se non è ancora pubblicato, estrae le
// offerte Octopus (P.IVA 01771990445) e salva le tariffe con
// store.SaveRatesBatch. Download falliti e XML malformati vengono loggati e
// danno una struttura vuota, come in Python; ctx sostituisce
// asyncio.wait_for (timeout → errors.Is(err, context.DeadlineExceeded)) e le
// eccezioni Python non gestite diventano *PyException.
//
// Il blocco __main__ di data_reader.py è RunScrape; il logging dei comandi
// standalone (logging.basicConfig) è NewBasicHandler.
//
// # Backfill (`octotracker backfill`)
//
//	opts := scraper.DefaultBackfillOptions() // --days 365, --delay 0.5
//	err := scraper.Backfill(ctx, store, opts)
//
// # Memoria
//
// Python scarica il file in una stringa e costruisce l'intero albero
// ElementTree (per file ARERA da decine di MB). Qui il corpo HTTP passa in
// streaming da un validatore UTF-8 al parser (xml.Decoder.RawToken) e viene
// materializzata una sola offerta alla volta; dei risultati si conservano solo
// le offerte Octopus. Gli effetti del parsing (log, tariffe, eccezioni)
// vengono riprodotti solo a documento completo e ben formato, perché in
// Python un errore di download, di decodifica o di sintassi in qualunque
// punto del file prevale su tutto il resto.
package scraper
