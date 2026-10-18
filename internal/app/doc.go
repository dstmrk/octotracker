// Package app è il port dell'orchestrazione di bot.py: il dispatcher degli
// update Telegram con gli stessi handler e nello stesso ordine, l'error
// handler con alert all'admin, il webhook (con i controlli del webhook
// server di python-telegram-bot), lo scheduler dei tre task giornalieri
// (scraper, checker, reminder), il server HTTP su HEALTH_PORT (/health,
// /api/*, /app/* con la Mini App incorporata) e lo spegnimento ordinato.
//
// Corrispondenze principali con bot.py:
//
//   - main()                         → Serve
//   - app.add_handler(...)           → NewDispatcher / RegisterHandlers
//   - error_handler                  → ErrorHandler
//   - run_scraper / run_checker / run_reminder e *_daily_task → Scheduler
//   - calculate_seconds_until_next_run → CalculateSecondsUntilNextRun
//   - _task_done_callback            → taskDoneCallback
//   - run_health_server              → NewHTTPHandler (+ task health_server)
//   - post_init                      → startBackgroundTasks
//   - app.run_webhook(...)           → runWebhook (bootstrap con retry,
//     webhook server, coda degli update, attesa del segnale di stop)
//
// I log usano il logger "__main__", il nome che il modulo bot.py aveva in
// produzione (avviato con python -u bot.py), e gli stessi testi.
package app
