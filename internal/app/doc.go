// Package app orchestra il bot: il dispatcher degli update Telegram con gli
// handler nell'ordine di registrazione, l'error handler con alert
// all'admin, il webhook (secret token, coda degli update elaborati uno alla
// volta), lo scheduler dei tre task giornalieri (scraper, checker,
// reminder), il server HTTP su HEALTH_PORT (/health, /api/*, /app/* con la
// Mini App incorporata) e lo spegnimento ordinato.
//
// Punti di ingresso:
//
//   - Serve: avvio completo (sottocomando serve)
//   - NewDispatcher / RegisterHandlers: handler e routing degli update
//   - ErrorHandler: gestione degli errori degli handler
//   - Scheduler, CalculateSecondsUntilNextRun: task giornalieri
//   - NewHTTPHandler: /health, API e Mini App
//   - NewWebhookHandler, SequentialDispatcher: ricezione degli update
//
// I log usano il logger "__main__", il nome storico dei log di produzione.
package app
