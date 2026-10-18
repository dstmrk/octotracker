package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/config"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/health"
	"github.com/dstmrk/octotracker/webapp"
)

// ServeOptions permette di sostituire le dipendenze esterne di Serve (per
// i test). Lo zero value usa quelle reali.
type ServeOptions struct {
	// Bot sostituisce il bot creato con NewBot(cfg.BotToken).
	Bot *gotgbot.Bot
	// Listen apre i socket del webhook e del server HTTP (nil = net.Listen).
	Listen func(network, address string) (net.Listener, error)
	// Sleep è l'attesa tra i tentativi di bootstrap (nil = attesa reale).
	Sleep func(ctx context.Context, d time.Duration) error
	// ConfigureScheduler può modificare lo scheduler prima dell'avvio.
	ConfigureScheduler func(*Scheduler)
	// WebApp sostituisce i file della Mini App (nil = webapp.FS()).
	WebApp fs.FS
	// Ready viene chiamata quando il webhook accetta update.
	Ready func(webhookAddr net.Addr)
}

// Serve è il port di main() di bot.py: avvia il bot in modalità webhook con
// scheduler integrato e resta in esecuzione finché ctx non viene
// cancellato (SIGINT/SIGTERM), poi si ferma in ordine.
//
// Gli errori di configurazione sono i ValueError di bot.py, nello stesso
// ordine: WEBHOOK_SECRET (controllato all'import), TELEGRAM_BOT_TOKEN e,
// dopo la configurazione del bot, WEBHOOK_URL.
func Serve(ctx context.Context, cfg config.Config, opts ServeOptions) error {
	// Validazione WEBHOOK_SECRET obbligatorio (protezione da webhook spoofing)
	if cfg.WebhookSecret == "" {
		return valueError(config.ErrMissingWebhookSecret.Error())
	}
	if cfg.BotToken == "" {
		return valueError("TELEGRAM_BOT_TOKEN non impostato")
	}

	// Inizializza database
	store, err := database.Open(filepath.Join(cfg.DataDir, database.DBFileName))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	logger.Info("💾 Database inizializzato")

	logger.Info("🤖 Avvio OctoTracker...")
	logger.Info("📡 Modalità: WEBHOOK")
	logger.Info(fmt.Sprintf("⏰ Scraper schedulato: %d:00", cfg.ScraperHour))
	logger.Info(fmt.Sprintf("⏰ Checker schedulato: %d:00", cfg.CheckerHour))
	logger.Info(fmt.Sprintf("⏰ Reminder schedulato: %d:00", cfg.ReminderHour))
	logger.Info(fmt.Sprintf("🌐 Webhook URL: %s", cfg.WebhookURL))
	logger.Info(fmt.Sprintf("🔌 Porta webhook: %d", cfg.WebhookPort))
	logger.Info(fmt.Sprintf("🏥 Porta health check: %d", cfg.HealthPort))

	b := opts.Bot
	if b == nil {
		b = NewBot(cfg.BotToken)
	}
	dispatcher := NewDispatcher(DispatcherConfig{Config: cfg, Store: store})

	logger.Info("✅ Bot configurato!")

	// Verifica webhook URL
	if cfg.WebhookURL == "" {
		return valueError("WEBHOOK_URL richiesto")
	}

	logger.Info(fmt.Sprintf("🚀 Avvio webhook su %s...", cfg.WebhookURL))
	return runWebhook(ctx, cfg, store, b, dispatcher, opts)
}

// runWebhook replica app.run_webhook(listen="0.0.0.0", port=WEBHOOK_PORT,
// url_path=token, webhook_url=f"{WEBHOOK_URL}/{token}",
// secret_token=WEBHOOK_SECRET, allowed_updates=Update.ALL_TYPES,
// drop_pending_updates=True, bootstrap_retries=3) con la sequenza di
// Application.__run:
//
//  1. initialize (getMe) con retry;
//  2. post_init: task in background (scheduler e server HTTP);
//  3. deleteWebhook + setWebhook con retry, poi ascolto sulla porta del webhook;
//  4. elaborazione degli update fino alla cancellazione di ctx;
//  5. stop: chiusura del webhook server, elaborazione degli update ancora
//     in coda, cancellazione dei task in background.
func runWebhook(ctx context.Context, cfg config.Config, store *database.Store, b *gotgbot.Bot, dispatcher *ext.Dispatcher, opts ServeOptions) error {
	sleep := sleepFunc(sleepCtx)
	if opts.Sleep != nil {
		sleep = opts.Sleep
	}
	listen := opts.Listen
	if listen == nil {
		listen = net.Listen
	}

	// 1. Bootstrap initialize (Bot.initialize → getMe)
	err := networkRetryLoop(ctx, "Bootstrap Initialize Application", BootstrapInterval, BootstrapRetries, sleep,
		func(ctx context.Context) error { return initializeBot(ctx, b) })
	if err != nil {
		return stopOrError(ctx, err)
	}

	// 2. post_init: scheduler e server HTTP in background
	tasksCtx, cancelTasks := context.WithCancel(context.Background())
	reg := health.NewRegistry()
	sched := NewScheduler(cfg.ScraperHour, cfg.CheckerHour, cfg.ReminderHour, cfg.ScraperTimeoutSeconds, cfg.BotToken, store)
	if opts.ConfigureScheduler != nil {
		opts.ConfigureScheduler(sched)
	}
	webFS := opts.WebApp
	if webFS == nil {
		webFS = webapp.FS()
	}
	hs := &healthServer{
		port:    cfg.HealthPort,
		handler: NewHTTPHandler(store, reg, cfg.BotToken, webFS, time.Now()),
		listen:  listen,
	}
	tasks := startBackgroundTasks(tasksCtx, sched, hs, reg)
	defer func() {
		cancelTasks()
		tasks.wait()
	}()

	// 3. updater.start_webhook: coda degli update, bootstrap del webhook, ascolto
	updater := ext.NewUpdater(NewSequentialDispatcher(dispatcher), &ext.UpdaterOpts{
		Logger:           ptbLogger("telegram.ext.Updater"),
		UnhandledErrFunc: logUnhandledError,
	})
	if err := updater.AddWebhook(b, cfg.BotToken, &ext.AddWebhookOpts{SecretToken: cfg.WebhookSecret}); err != nil {
		return err
	}
	stopUpdater := sync.OnceFunc(func() { _ = updater.Stop() })
	defer stopUpdater()

	webhookURL := cfg.WebhookURL + "/" + cfg.BotToken
	if err := bootstrapWebhook(ctx, b, webhookURL, cfg.WebhookSecret, sleep); err != nil {
		return stopOrError(ctx, err)
	}

	ln, err := listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.WebhookPort)))
	if err != nil {
		return pycompat.OSError(err)
	}
	srv := &http.Server{
		Handler:           NewWebhookHandler(cfg.BotToken, cfg.WebhookSecret, updater.GetHandlerFunc("/")),
		ReadHeaderTimeout: ReadHeaderTimeout,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	if opts.Ready != nil {
		opts.Ready(ln.Addr())
	}

	// 4. run_forever fino al segnale di stop
	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = pycompat.OSError(err)
		}
	}

	// 5. updater.stop(): chiude il webhook server; app.stop(): elabora gli
	// update in coda
	shutdownCtx, cancel := context.WithTimeout(context.Background(), HTTPShutdownTimeout)
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	stopUpdater()
	return runErr
}

// stopOrError restituisce nil se l'avvio è stato interrotto da un segnale
// di stop (in PTB il SystemExit durante il bootstrap porta a uno
// spegnimento ordinato, non a un errore), altrimenti err.
func stopOrError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}
