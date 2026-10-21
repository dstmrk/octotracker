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
	// Sleep è l'attesa tra i tentativi di avvio (nil = attesa reale).
	Sleep func(ctx context.Context, d time.Duration) error
	// ConfigureScheduler può modificare lo scheduler prima dell'avvio.
	ConfigureScheduler func(*Scheduler)
	// WebApp sostituisce i file della Mini App (nil = webapp.FS()).
	WebApp fs.FS
	// Ready viene chiamata quando il webhook accetta update.
	Ready func(webhookAddr net.Addr)
}

// Serve avvia il bot in modalità webhook con lo scheduler integrato e resta
// in esecuzione finché ctx non viene cancellato (SIGINT/SIGTERM), poi si
// ferma in ordine.
//
// Gli errori di configurazione vengono controllati in quest'ordine:
// WEBHOOK_SECRET, TELEGRAM_BOT_TOKEN e, dopo la configurazione del bot,
// WEBHOOK_URL.
func Serve(ctx context.Context, cfg config.Config, opts ServeOptions) error {
	// Validazione WEBHOOK_SECRET obbligatorio (protezione da webhook spoofing)
	if cfg.WebhookSecret == "" {
		return config.ErrMissingWebhookSecret
	}
	if cfg.BotToken == "" {
		return errors.New("TELEGRAM_BOT_TOKEN non impostato")
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
		return errors.New("WEBHOOK_URL richiesto")
	}

	logger.Info(fmt.Sprintf("🚀 Avvio webhook su %s...", cfg.WebhookURL))
	return runWebhook(ctx, cfg, store, b, dispatcher, opts)
}

// runWebhook esegue il bot in modalità webhook:
//
//  1. getMe con i tentativi di avvio;
//  2. task in background (scheduler e server HTTP);
//  3. deleteWebhook + setWebhook con i tentativi di avvio, poi ascolto
//     sulla porta del webhook (path = token, secret token obbligatorio);
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

	// 1. getMe
	err := networkRetryLoop(ctx, "getMe", BootstrapInterval, BootstrapRetries, sleep,
		func(ctx context.Context) error { return initializeBot(ctx, b) })
	if err != nil {
		return stopOrError(ctx, err)
	}

	// 2. scheduler e server HTTP in background. I task restano attivi
	// finché gli update in coda non sono stati elaborati: vengono
	// cancellati per ultimi (i defer sono eseguiti in ordine inverso).
	reg := health.NewRegistry()
	tasks := newBackgroundTasks(reg)
	defer tasks.wait()
	tasksCtx, cancelTasks := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTasks()

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
		handler: NewHTTPHandler(store, reg, cfg.BotToken, webFS),
		listen:  listen,
	}
	startBackgroundTasks(tasksCtx, tasks, sched, hs)

	// 3. coda degli update, registrazione del webhook, ascolto
	updater := ext.NewUpdater(NewSequentialDispatcher(dispatcher), &ext.UpdaterOpts{
		Logger:           libLogger,
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
		return err
	}
	srv := newWebhookServer(NewWebhookHandler(cfg.BotToken, cfg.WebhookSecret, updater.GetHandlerFunc("/")))
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	if opts.Ready != nil {
		opts.Ready(ln.Addr())
	}

	// 4. elaborazione degli update fino al segnale di stop
	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}

	// 5. chiusura del webhook server ed elaborazione degli update in coda
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), HTTPShutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	stopUpdater()
	return runErr
}

// stopOrError restituisce nil se l'avvio è stato interrotto da un segnale
// di stop (spegnimento ordinato), altrimenti err.
func stopOrError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}
