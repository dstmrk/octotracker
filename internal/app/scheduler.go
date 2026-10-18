package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/reminder"
	"github.com/dstmrk/octotracker/internal/scraper"
)

// CalculateSecondsUntilNextRun è il port di calculate_seconds_until_next_run:
// secondi da now alla prossima esecuzione alle targetHour:00.
//
// Come in Python il calcolo usa l'ora locale "naive" (datetime.now() senza
// fuso): la differenza è tra orari da orologio da parete, quindi nei giorni
// di cambio dell'ora legale l'attesa reale differisce di un'ora (il task
// parte alle 10:00 invece delle 9:00 il giorno del passaggio all'ora legale)
// e il calcolo successivo riallinea l'orario.
//
// Un'ora fuori da 0..23 restituisce il ValueError di datetime.replace.
func CalculateSecondsUntilNextRun(now time.Time, targetHour int) (float64, error) {
	if targetHour < 0 || targetHour > 23 {
		return 0, valueError("hour must be in 0..23")
	}
	// datetime.now() ha la precisione dei microsecondi
	wall := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(),
		now.Nanosecond()/1000*1000, time.UTC)
	target := time.Date(now.Year(), now.Month(), now.Day(), targetHour, 0, 0, 0, time.UTC)

	// Se l'orario è già passato oggi, schedula per domani
	if !wall.Before(target) {
		target = target.AddDate(0, 0, 1)
	}

	// timedelta.total_seconds(): microsecondi interi / 10**6
	return float64(target.Sub(wall).Microseconds()) / 1e6, nil
}

// dailyTaskSpec descrive uno dei tre task giornalieri (i testi dei log di
// scraper_daily_task, checker_daily_task e reminder_daily_task).
type dailyTaskSpec struct {
	funcName  string // nome della coroutine Python, per "Errore non gestito in ..."
	label     string // "scraper", "checker", "reminder" (per "⏰ Prossimo ...")
	schedLog  string // formato del log iniziale: %d ora, %.1f ore
	hour      int
	runOnce   func(ctx context.Context)
	firstWait float64
}

// Scheduler è il port dello scheduler di bot.py: i task giornalieri dello
// scraper (SCRAPER_HOUR), del checker (CHECKER_HOUR) e del reminder
// (REMINDER_HOUR), in ora locale.
type Scheduler struct {
	// Ore di esecuzione (0-23).
	ScraperHour, CheckerHour, ReminderHour int
	// ScraperTimeout è SCRAPER_TIMEOUT_SECONDS: limite di un'esecuzione
	// dello scraper (asyncio.wait_for).
	ScraperTimeout time.Duration

	// FetchTariffe è fetch_octopus_tariffe().
	FetchTariffe func(ctx context.Context) (*scraper.Tariffe, error)
	// CheckAndNotify è check_and_notify_users(bot_token).
	CheckAndNotify func(ctx context.Context) error
	// SendReminders è check_and_send_reminders(bot_token).
	SendReminders func(ctx context.Context) error

	// Now è l'orologio (datetime.now(), ora locale); nil = time.Now.
	Now func() time.Time
	// Sleep è asyncio.sleep; nil = attesa interrompibile da ctx.
	Sleep func(ctx context.Context, d time.Duration) error

	// Sostituti di run_scraper/run_checker/run_reminder per i test (nil =
	// implementazione reale).
	runScraperFn, runCheckerFn, runReminderFn func(ctx context.Context)
}

// NewScheduler crea lo scheduler con le operazioni reali: lo scraper salva
// nel database, checker e reminder creano a ogni esecuzione un client
// Telegram nuovo per token (Bot(token) in Python, con i timeout di default
// di PTB).
func NewScheduler(scraperHour, checkerHour, reminderHour, scraperTimeoutSeconds int, token string, store *database.Store) *Scheduler {
	return &Scheduler{
		ScraperHour:    scraperHour,
		CheckerHour:    checkerHour,
		ReminderHour:   reminderHour,
		ScraperTimeout: time.Duration(scraperTimeoutSeconds) * time.Second,
		FetchTariffe: func(ctx context.Context) (*scraper.Tariffe, error) {
			return scraper.FetchOctopusTariffe(ctx, store, scraper.DefaultMaxDaysBack)
		},
		CheckAndNotify: func(ctx context.Context) error {
			return checker.CheckAndNotifyUsers(ctx, checker.NewBot(token), store)
		},
		SendReminders: func(ctx context.Context) error {
			return reminder.CheckAndSendReminders(ctx, checker.NewBot(token), store, nil)
		},
	}
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) sleep(ctx context.Context, d time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	return sleepCtx(ctx, d)
}

// ========== RUN ==========

// RunScraper esegue lo scraper delle tariffe (run_scraper), con il limite
// ScraperTimeout. Gli errori vengono loggati e non propagati; se ctx viene
// cancellato (spegnimento) termina senza log, come una CancelledError.
func (s *Scheduler) RunScraper(ctx context.Context) {
	logger.Info("🕷️  Avvio scraper...")
	runCtx, cancel := context.WithTimeout(ctx, s.ScraperTimeout)
	defer cancel()

	result, err := s.FetchTariffe(runCtx)
	switch {
	case err == nil:
		logger.Info(fmt.Sprintf("✅ Scraper completato: %v", result))
		return
	case ctx.Err() != nil:
		return // cancellato
	}

	e := describe(err)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || isInstance(e.class, "TimeoutError"):
		logger.Error(fmt.Sprintf("⏱️  Scraper interrotto: nessuna risposta entro %ss "+
			"(probabile download bloccato lato ARERA)", formatSeconds(s.ScraperTimeout.Seconds())))
	case isInstance(e.class, "ConnectionError"):
		logger.Error(fmt.Sprintf("🌐 Errore di connessione scraper: %s", e.msg))
	case isInstance(e.class, "OSError"):
		logger.Error(fmt.Sprintf("💾 Errore I/O scraper: %s", e.msg))
	default:
		logger.Error(fmt.Sprintf("❌ Errore inatteso scraper: %s", e.msg))
	}
}

// RunChecker esegue il checker e invia le notifiche (run_checker).
func (s *Scheduler) RunChecker(ctx context.Context) {
	logger.Info("🔍 Avvio checker...")
	err := s.CheckAndNotify(ctx)
	if ctx.Err() != nil {
		return // cancellato
	}
	if err == nil {
		logger.Info("✅ Checker completato")
		return
	}
	logRunError(err, "checker")
}

// RunReminder esegue il controllo scadenze e invia i reminder (run_reminder).
func (s *Scheduler) RunReminder(ctx context.Context) {
	logger.Info("📅 Avvio reminder scadenze...")
	err := s.SendReminders(ctx)
	if ctx.Err() != nil {
		return // cancellato
	}
	if err == nil {
		logger.Info("✅ Reminder scadenze completato")
		return
	}
	logRunError(err, "reminder")
}

// logRunError replica i rami except di run_checker e run_reminder.
func logRunError(err error, what string) {
	e := describe(err)
	switch {
	case isInstance(e.class, "NetworkError"):
		logger.Error(fmt.Sprintf("🌐 Errore di rete %s: %s", what, e.msg))
	case isInstance(e.class, "TelegramError"):
		logger.Error(fmt.Sprintf("❌ Errore Telegram %s: %s", what, e.msg))
	case isInstance(e.class, "OSError"):
		logger.Error(fmt.Sprintf("💾 Errore I/O %s: %s", what, e.msg))
	default:
		logger.Error(fmt.Sprintf("❌ Errore inatteso %s: %s", what, e.msg))
	}
}

// ========== TASK GIORNALIERI ==========

func (s *Scheduler) scraperSpec() *dailyTaskSpec {
	run := s.runScraperFn
	if run == nil {
		run = s.RunScraper
	}
	return &dailyTaskSpec{
		funcName: "scraper_daily_task",
		label:    "scraper",
		schedLog: "🕷️  Scraper schedulato per le %d:00 (tra %.1f ore)",
		hour:     s.ScraperHour,
		runOnce:  run,
	}
}

func (s *Scheduler) checkerSpec() *dailyTaskSpec {
	run := s.runCheckerFn
	if run == nil {
		run = s.RunChecker
	}
	return &dailyTaskSpec{
		funcName: "checker_daily_task",
		label:    "checker",
		schedLog: "🔍 Checker schedulato per le %d:00 (tra %.1f ore)",
		hour:     s.CheckerHour,
		runOnce:  run,
	}
}

func (s *Scheduler) reminderSpec() *dailyTaskSpec {
	run := s.runReminderFn
	if run == nil {
		run = s.RunReminder
	}
	return &dailyTaskSpec{
		funcName: "reminder_daily_task",
		label:    "reminder",
		schedLog: "📅 Reminder schedulato per le %d:00 (tra %.1f ore)",
		hour:     s.ReminderHour,
		runOnce:  run,
	}
}

// start è la prima parte di *_daily_task, fino al primo await: calcola
// l'attesa iniziale e logga l'orario schedulato.
func (s *Scheduler) start(spec *dailyTaskSpec) error {
	secondsUntilRun, err := CalculateSecondsUntilNextRun(s.now(), spec.hour)
	if err != nil {
		return err
	}
	spec.firstWait = secondsUntilRun
	logger.Info(fmt.Sprintf(spec.schedLog, spec.hour, secondsUntilRun/3600))
	return nil
}

// loop è il resto di *_daily_task: attende la prima esecuzione, poi
// esegue il task e ricalcola l'attesa (previene il drift temporale), per
// sempre. Termina con ctx.Err() quando ctx viene cancellato.
func (s *Scheduler) loop(ctx context.Context, spec *dailyTaskSpec) error {
	if err := s.sleep(ctx, secondsDuration(spec.firstWait)); err != nil {
		return err
	}
	for {
		s.runGuarded(ctx, spec)
		if err := ctx.Err(); err != nil {
			return err
		}

		secondsUntilNext, err := CalculateSecondsUntilNextRun(s.now(), spec.hour)
		if err != nil {
			return err
		}
		logger.Info(fmt.Sprintf("⏰ Prossimo %s tra %.1f ore (alle %d:00)", spec.label, secondsUntilNext/3600, spec.hour))
		if err := s.sleep(ctx, secondsDuration(secondsUntilNext)); err != nil {
			return err
		}
	}
}

// runGuarded esegue un'iterazione del task: un panic (l'equivalente di
// un'eccezione che esce da run_*) viene loggato e il loop continua.
func (s *Scheduler) runGuarded(ctx context.Context, spec *dailyTaskSpec) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error(fmt.Sprintf("❌ Errore non gestito in %s: %s", spec.funcName, describe(panicError(r)).msg))
		}
	}()
	spec.runOnce(ctx)
}

// dailyTask esegue un task giornaliero completo (start + loop).
func (s *Scheduler) dailyTask(ctx context.Context, spec *dailyTaskSpec) error {
	if err := s.start(spec); err != nil {
		return err
	}
	return s.loop(ctx, spec)
}

// ScraperDailyTask è scraper_daily_task: esegue lo scraper ogni giorno alle
// ScraperHour:00. Termina solo per cancellazione di ctx (o errore di
// configurazione dell'ora).
func (s *Scheduler) ScraperDailyTask(ctx context.Context) error {
	return s.dailyTask(ctx, s.scraperSpec())
}

// CheckerDailyTask è checker_daily_task.
func (s *Scheduler) CheckerDailyTask(ctx context.Context) error {
	return s.dailyTask(ctx, s.checkerSpec())
}

// ReminderDailyTask è reminder_daily_task.
func (s *Scheduler) ReminderDailyTask(ctx context.Context) error {
	return s.dailyTask(ctx, s.reminderSpec())
}
