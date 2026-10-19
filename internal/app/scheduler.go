package app

import (
	"context"
	"fmt"
	"time"

	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/reminder"
	"github.com/dstmrk/octotracker/internal/scraper"
	"github.com/dstmrk/octotracker/internal/tg"
)

// CalculateSecondsUntilNextRun restituisce i secondi da now alla prossima
// esecuzione alle targetHour:00, con la precisione dei microsecondi.
//
// Il calcolo usa l'orario da orologio da parete (senza fuso): nei giorni di
// cambio dell'ora legale l'attesa reale differisce di un'ora (il task parte
// alle 10:00 invece delle 9:00 il giorno del passaggio all'ora legale) e il
// calcolo successivo riallinea l'orario.
func CalculateSecondsUntilNextRun(now time.Time, targetHour int) (float64, error) {
	if targetHour < 0 || targetHour > 23 {
		return 0, fmt.Errorf("ora non valida: %d (deve essere tra 0 e 23)", targetHour)
	}
	wall := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(),
		now.Nanosecond()/1000*1000, time.UTC)
	target := time.Date(now.Year(), now.Month(), now.Day(), targetHour, 0, 0, 0, time.UTC)

	// Se l'orario è già passato oggi, schedula per domani
	if !wall.Before(target) {
		target = target.AddDate(0, 0, 1)
	}
	return float64(target.Sub(wall).Microseconds()) / 1e6, nil
}

// secondsDuration converte secondi in Duration.
func secondsDuration(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

// Scheduler esegue ogni giorno, in ora locale, lo scraper (SCRAPER_HOUR),
// il checker (CHECKER_HOUR) e il reminder (REMINDER_HOUR).
type Scheduler struct {
	// Ore di esecuzione (0-23).
	ScraperHour, CheckerHour, ReminderHour int
	// ScraperTimeout è SCRAPER_TIMEOUT_SECONDS: limite di un'esecuzione
	// dello scraper.
	ScraperTimeout time.Duration

	// FetchTariffe scarica e salva le tariffe.
	FetchTariffe func(ctx context.Context) (*scraper.Tariffe, error)
	// CheckAndNotify confronta le tariffe e invia le notifiche.
	CheckAndNotify func(ctx context.Context) error
	// SendReminders invia i reminder delle scadenze.
	SendReminders func(ctx context.Context) error

	// Now è l'orologio (ora locale); nil = time.Now.
	Now func() time.Time
	// Sleep è l'attesa tra le esecuzioni; nil = attesa interrompibile da ctx.
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewScheduler crea lo scheduler con le operazioni reali: lo scraper salva
// nel database, checker e reminder creano a ogni esecuzione un client
// Telegram nuovo per token.
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

// RunScraper esegue lo scraper delle tariffe con il limite ScraperTimeout.
// Gli errori vengono loggati e non propagati; se ctx viene cancellato
// (spegnimento) termina senza log.
func (s *Scheduler) RunScraper(ctx context.Context) {
	logger.Info("🕷️  Avvio scraper...")
	runCtx, cancel := context.WithTimeout(ctx, s.ScraperTimeout)
	defer cancel()

	result, err := s.FetchTariffe(runCtx)
	switch {
	case err == nil:
		logger.Info(fmt.Sprintf("✅ Scraper completato: %v", result))
	case ctx.Err() != nil:
		// cancellato
	case isTimeout(err):
		logger.Error(fmt.Sprintf("⏱️  Scraper interrotto: nessuna risposta entro %gs "+
			"(probabile download bloccato lato ARERA)", s.ScraperTimeout.Seconds()))
	case isConnectionError(err):
		logger.Error(fmt.Sprintf("🌐 Errore di connessione scraper: %v", err))
	case isIOError(err):
		logger.Error(fmt.Sprintf("💾 Errore I/O scraper: %v", err))
	default:
		logger.Error(fmt.Sprintf("❌ Errore inatteso scraper: %v", err))
	}
}

// RunChecker esegue il checker e invia le notifiche.
func (s *Scheduler) RunChecker(ctx context.Context) {
	logger.Info("🔍 Avvio checker...")
	s.runLogged(ctx, s.CheckAndNotify, "checker", "✅ Checker completato")
}

// RunReminder esegue il controllo scadenze e invia i reminder.
func (s *Scheduler) RunReminder(ctx context.Context) {
	logger.Info("📅 Avvio reminder scadenze...")
	s.runLogged(ctx, s.SendReminders, "reminder", "✅ Reminder scadenze completato")
}

// runLogged esegue run e ne logga l'esito (nessun log se ctx è stato
// cancellato).
func (s *Scheduler) runLogged(ctx context.Context, run func(context.Context) error, what, done string) {
	err := run(ctx)
	switch {
	case ctx.Err() != nil:
		// cancellato
	case err == nil:
		logger.Info(done)
	case isNetworkError(err):
		logger.Error(fmt.Sprintf("🌐 Errore di rete %s: %s", what, tg.ErrorString(err)))
	case apiError(err) != nil:
		logger.Error(fmt.Sprintf("❌ Errore Telegram %s: %s", what, tg.ErrorString(err)))
	case isIOError(err):
		logger.Error(fmt.Sprintf("💾 Errore I/O %s: %v", what, err))
	default:
		logger.Error(fmt.Sprintf("❌ Errore inatteso %s: %v", what, err))
	}
}

// ========== TASK GIORNALIERI ==========

// dailyTask descrive uno dei tre task giornalieri.
type dailyTask struct {
	key, name string // chiave nel registro di /health e nome nei log
	label     string // "scraper", "checker", "reminder"
	schedLog  string // formato del log iniziale: %d ora, %.1f ore
	hour      int
	run       func(ctx context.Context)
}

// dailyTasks restituisce scraper, checker e reminder, nell'ordine di avvio.
func (s *Scheduler) dailyTasks() []dailyTask {
	return []dailyTask{
		{KeyScraperTask, TaskScraperDaily, "scraper", "🕷️  Scraper schedulato per le %d:00 (tra %.1f ore)", s.ScraperHour, s.RunScraper},
		{KeyCheckerTask, TaskCheckerDaily, "checker", "🔍 Checker schedulato per le %d:00 (tra %.1f ore)", s.CheckerHour, s.RunChecker},
		{KeyReminderTask, TaskReminderDaily, "reminder", "📅 Reminder schedulato per le %d:00 (tra %.1f ore)", s.ReminderHour, s.RunReminder},
	}
}

// start logga l'orario schedulato del task e restituisce l'attesa fino alla
// prima esecuzione (errore per un'ora non valida).
func (s *Scheduler) start(task dailyTask) (time.Duration, error) {
	seconds, err := CalculateSecondsUntilNextRun(s.now(), task.hour)
	if err != nil {
		return 0, err
	}
	logger.Info(fmt.Sprintf(task.schedLog, task.hour, seconds/3600))
	return secondsDuration(seconds), nil
}

// loop attende wait, poi esegue il task e ricalcola l'attesa (previene il
// drift temporale), per sempre. Termina con ctx.Err() quando ctx viene
// cancellato.
func (s *Scheduler) loop(ctx context.Context, task dailyTask, wait time.Duration) error {
	for {
		if err := s.sleep(ctx, wait); err != nil {
			return err
		}
		s.runGuarded(ctx, task)
		if err := ctx.Err(); err != nil {
			return err
		}
		seconds, err := CalculateSecondsUntilNextRun(s.now(), task.hour)
		if err != nil {
			return err
		}
		logger.Info(fmt.Sprintf("⏰ Prossimo %s tra %.1f ore (alle %d:00)", task.label, seconds/3600, task.hour))
		wait = secondsDuration(seconds)
	}
}

// runGuarded esegue un'iterazione del task: un panic viene loggato e il
// loop continua.
func (s *Scheduler) runGuarded(ctx context.Context, task dailyTask) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error(fmt.Sprintf("❌ Errore non gestito nel task %s: %v", task.label, r))
		}
	}()
	task.run(ctx)
}
