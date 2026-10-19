// Package scraper legge le tariffe Octopus Energy dai file Open Data ARERA
// e popola lo storico tariffe.
//
// # Lettura giornaliera (bot e `octotracker scrape`)
//
//	tariffe, err := scraper.FetchOctopusTariffe(ctx, store, scraper.DefaultMaxDaysBack)
//
// Per luce ("E") e poi gas ("G") scarica il file XML del giorno, tornando
// indietro fino a maxDaysBack giorni se non è ancora pubblicato, estrae le
// offerte Octopus (P.IVA 01771990445) e salva le tariffe con
// store.SaveRatesBatch. Download falliti e XML malformati vengono loggati e
// danno tariffe vuote; ctx limita la durata complessiva.
//
// # Backfill (`octotracker backfill`)
//
//	opts := scraper.DefaultBackfillOptions() // --days 365, --delay 0.5
//	err := scraper.Backfill(ctx, store, opts)
//
// # Memoria
//
// I file ARERA sono di decine di MB: il corpo HTTP passa in streaming al
// parser (encoding/xml) e viene materializzata una sola offerta alla volta;
// dei risultati si conservano solo le offerte Octopus. Gli effetti del
// parsing (log, tariffe, errori) vengono applicati solo a documento
// completo, ben formato e scaricato senza errori, così un file danneggiato
// in qualunque punto non produce tariffe parziali.
package scraper
