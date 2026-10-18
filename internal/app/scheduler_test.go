package app

// Port di tests/test_bot_scheduler.py (run_scraper, run_checker,
// run_reminder, *_daily_task, error_handler) più i test di
// calculate_seconds_until_next_run e del loop giornaliero.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/scraper"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// newTestScheduler crea uno scheduler con operazioni finte, orologio fisso e
// attese istantanee.
func newTestScheduler() *Scheduler {
	return &Scheduler{
		ScraperHour:    9,
		CheckerHour:    10,
		ReminderHour:   11,
		ScraperTimeout: 600 * time.Second,
		FetchTariffe:   func(context.Context) (*scraper.Tariffe, error) { return &scraper.Tariffe{}, nil },
		CheckAndNotify: func(context.Context) error { return nil },
		SendReminders:  func(context.Context) error { return nil },
		Now:            func() time.Time { return time.Date(2026, 10, 18, 8, 0, 0, 0, time.Local) },
		Sleep:          func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	}
}

// ========== run_scraper ==========

// TestRunScraperConnectionError: run_scraper logga con logging.exception() su ConnectionError.
func TestRunScraperConnectionError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) {
		return nil, &scraper.PyException{Type: "ConnectionError", Msg: "no network"}
	}
	s.RunScraper(context.Background())
	assertLogContains(t, logs, "ERROR - 🌐 Errore di connessione scraper: no network")
}

// TestRunScraperOSError: run_scraper logga con logging.exception() su OSError.
func TestRunScraperOSError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) {
		return nil, &scraper.PyException{Type: "OSError", Msg: "disk full"}
	}
	s.RunScraper(context.Background())
	assertLogContains(t, logs, "ERROR - 💾 Errore I/O scraper: disk full")
}

// TestRunScraperTimeout: run_scraper logga un errore chiaro se
// fetch_octopus_tariffe non risponde entro il timeout.
func TestRunScraperTimeout(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.ScraperTimeout = 10 * time.Millisecond
	s.FetchTariffe = func(ctx context.Context) (*scraper.Tariffe, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return &scraper.Tariffe{}, nil
		}
	}
	s.RunScraper(context.Background())
	assertLogContains(t, logs, "ERROR - ⏱️  Scraper interrotto: nessuna risposta entro 0.01s (probabile download bloccato lato ARERA)")
}

// TestRunScraperGenericException: run_scraper logga con logging.exception() su errore generico.
func TestRunScraperGenericException(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) {
		return nil, &scraper.PyException{Type: "ValueError", Msg: "boom"}
	}
	s.RunScraper(context.Background())
	assertLogContains(t, logs, "ERROR - ❌ Errore inatteso scraper: boom")
}

// ========== run_checker ==========

// TestRunCheckerTelegramError: run_checker logga con logging.exception() su TelegramError.
func TestRunCheckerTelegramError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.CheckAndNotify = func(context.Context) error { return pycompat.NewError("TelegramError", "telegram down") }
	s.RunChecker(context.Background())
	assertLogContains(t, logs, "ERROR - ❌ Errore Telegram checker: telegram down")
	assertLogNotContains(t, logs, "✅ Checker completato")
}

// TestRunCheckerNetworkError: run_checker logga con logging.exception() su NetworkError.
func TestRunCheckerNetworkError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.CheckAndNotify = func(context.Context) error { return pycompat.NewError("NetworkError", "network down") }
	s.RunChecker(context.Background())
	assertLogContains(t, logs, "ERROR - 🌐 Errore di rete checker: network down")
}

// TestRunCheckerOSError: run_checker logga con logging.exception() su OSError.
func TestRunCheckerOSError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.CheckAndNotify = func(context.Context) error { return pycompat.NewError("OSError", "disk full") }
	s.RunChecker(context.Background())
	assertLogContains(t, logs, "ERROR - 💾 Errore I/O checker: disk full")
}

// TestRunCheckerGenericException: run_checker logga con logging.exception() su errore generico.
func TestRunCheckerGenericException(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.CheckAndNotify = func(context.Context) error { return pycompat.NewError("ValueError", "boom") }
	s.RunChecker(context.Background())
	assertLogContains(t, logs, "ERROR - ❌ Errore inatteso checker: boom")
}

// ========== error_handler ==========

// TestErrorHandlerAdminAlertFailure: error_handler logga con
// logging.exception() se l'invio alert admin fallisce.
func TestErrorHandlerAdminAlertFailure(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", context.DeadlineExceeded) // TimedOut("timeout")
	h := &ErrorHandler{AdminUserID: "12345"}

	h.handle(b, nil, errors.New("RuntimeError: errore inatteso non gestito"), "")

	assertLogContains(t, logs, "ERROR - ❌ Errore invio alert admin: Timed out")
	assertLogNotContains(t, logs, "📨 Alert errore inviato")
}

// ========== *_daily_task ==========

// panicThenCancel restituisce una funzione run che alla prima chiamata va
// in panic (run_* che propaga ValueError("boom")) e alla seconda cancella ctx
// (asyncio.CancelledError).
func panicThenCancel(cancel context.CancelFunc, calls *atomic.Int32) func(context.Context) {
	return func(context.Context) {
		if calls.Add(1) == 1 {
			panic("ValueError: boom")
		}
		cancel()
	}
}

// TestScraperDailyTaskLogsUnhandledError: scraper_daily_task logga con
// logging.exception() se run_scraper propaga un errore.
func TestScraperDailyTaskLogsUnhandledError(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	s := newTestScheduler()
	s.runScraperFn = panicThenCancel(cancel, &calls)

	if err := s.ScraperDailyTask(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ScraperDailyTask = %v, attesa cancellazione", err)
	}
	assertLogContains(t, logs, "ERROR - ❌ Errore non gestito in scraper_daily_task: boom")
	if calls.Load() != 2 {
		t.Errorf("run_scraper chiamato %d volte, atteso 2", calls.Load())
	}
}

// TestCheckerDailyTaskLogsUnhandledError: checker_daily_task logga con
// logging.exception() se run_checker propaga un errore.
func TestCheckerDailyTaskLogsUnhandledError(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	s := newTestScheduler()
	s.runCheckerFn = panicThenCancel(cancel, &calls)

	if err := s.CheckerDailyTask(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckerDailyTask = %v, attesa cancellazione", err)
	}
	assertLogContains(t, logs, "ERROR - ❌ Errore non gestito in checker_daily_task: boom")
}

// ========== run_reminder ==========

// TestRunReminderTelegramError: run_reminder logga con logging.exception() su TelegramError.
func TestRunReminderTelegramError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.SendReminders = func(context.Context) error { return pycompat.NewError("TelegramError", "telegram down") }
	s.RunReminder(context.Background())
	assertLogContains(t, logs, "ERROR - ❌ Errore Telegram reminder: telegram down")
}

// TestRunReminderNetworkError: run_reminder logga con logging.exception() su NetworkError.
func TestRunReminderNetworkError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.SendReminders = func(context.Context) error { return pycompat.NewError("NetworkError", "network down") }
	s.RunReminder(context.Background())
	assertLogContains(t, logs, "ERROR - 🌐 Errore di rete reminder: network down")
}

// TestRunReminderOSError: run_reminder logga con logging.exception() su OSError.
func TestRunReminderOSError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.SendReminders = func(context.Context) error { return pycompat.NewError("OSError", "disk full") }
	s.RunReminder(context.Background())
	assertLogContains(t, logs, "ERROR - 💾 Errore I/O reminder: disk full")
}

// TestRunReminderGenericException: run_reminder logga con logging.exception() su errore generico.
func TestRunReminderGenericException(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.SendReminders = func(context.Context) error { return pycompat.NewError("ValueError", "boom") }
	s.RunReminder(context.Background())
	assertLogContains(t, logs, "ERROR - ❌ Errore inatteso reminder: boom")
}

// TestRunReminderSuccess: run_reminder logga il completamento in caso di successo.
func TestRunReminderSuccess(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.RunReminder(context.Background())
	assertLogContains(t, logs, "INFO - 📅 Avvio reminder scadenze...")
	assertLogContains(t, logs, "INFO - ✅ Reminder scadenze completato")
}

// TestReminderDailyTaskLogsUnhandledError: reminder_daily_task logga con
// logging.exception() se run_reminder propaga un errore.
func TestReminderDailyTaskLogsUnhandledError(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	s := newTestScheduler()
	s.runReminderFn = panicThenCancel(cancel, &calls)

	if err := s.ReminderDailyTask(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReminderDailyTask = %v, attesa cancellazione", err)
	}
	assertLogContains(t, logs, "ERROR - ❌ Errore non gestito in reminder_daily_task: boom")
}

// ========== Test aggiuntivi (Go) ==========

// TestRunScraperSuccessLogsResultRepr: il risultato viene loggato con il
// repr() del dict come f"✅ Scraper completato: {result}".
func TestRunScraperSuccessLogsResultRepr(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	tariffe := &scraper.Tariffe{DataAggiornamento: "18/10/2026"}
	s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) { return tariffe, nil }
	s.RunScraper(context.Background())
	assertLogContains(t, logs, "INFO - 🕷️  Avvio scraper...")
	assertLogContains(t, logs, "INFO - ✅ Scraper completato: "+tariffe.String())
}

// TestRunScraperTimeoutErrorAndCancellation: un TimeoutError Python è
// gestito come il timeout di wait_for; la cancellazione non logga errori.
func TestRunScraperTimeoutErrorAndCancellation(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) {
		return nil, &scraper.PyException{Type: "TimeoutError", Msg: "timed out"}
	}
	s.RunScraper(context.Background())
	assertLogContains(t, logs, "Scraper interrotto: nessuna risposta entro 600s")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.FetchTariffe = func(ctx context.Context) (*scraper.Tariffe, error) { return nil, ctx.Err() }
	before := logs.String()
	s.RunScraper(ctx)
	if after := strings.TrimPrefix(logs.String(), before); strings.Contains(after, "ERROR") {
		t.Errorf("cancellazione loggata come errore: %s", after)
	}
}

// TestRunScraperConnectionErrorSubclass: ConnectionRefusedError è un
// ConnectionError (ramo di connessione), FileNotFoundError un OSError.
func TestRunScraperExceptionHierarchy(t *testing.T) {
	cases := []struct{ class, want string }{
		{"ConnectionRefusedError", "🌐 Errore di connessione scraper: x"},
		{"FileNotFoundError", "💾 Errore I/O scraper: x"},
		{"AttributeError", "❌ Errore inatteso scraper: x"},
	}
	for _, tc := range cases {
		logs := captureLogs(t)
		s := newTestScheduler()
		s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) {
			return nil, &scraper.PyException{Type: tc.class, Msg: "x"}
		}
		s.RunScraper(context.Background())
		assertLogContains(t, logs, tc.want)
	}
}

// TestRunCheckerRealErrors: gli errori reali di checker e Telegram finiscono
// nei rami giusti (KeyError → inatteso, 403 → Telegram, 400 → rete).
func TestRunCheckerRealErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{checker.KeyError("commercializzazione"), "❌ Errore inatteso checker: 'commercializzazione'"},
		{testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"), "❌ Errore Telegram checker: Forbidden: bot was blocked by the user"},
		{testutil.TelegramError("sendMessage", 400, "Bad Request: chat not found"), "🌐 Errore di rete checker: Chat not found"},
		{testutil.RetryAfterError("sendMessage", 5), "❌ Errore Telegram checker: Flood control exceeded. Retry in 5 seconds"},
	}
	for _, tc := range cases {
		logs := captureLogs(t)
		s := newTestScheduler()
		s.CheckAndNotify = func(context.Context) error { return tc.err }
		s.RunChecker(context.Background())
		assertLogContains(t, logs, tc.want)
	}
}

// TestRunCheckerSuccessAndCancellation: successo → "✅ Checker completato";
// con ctx cancellato nessun log di completamento.
func TestRunCheckerSuccessAndCancellation(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.RunChecker(context.Background())
	assertLogContains(t, logs, "INFO - 🔍 Avvio checker...")
	assertLogContains(t, logs, "INFO - ✅ Checker completato")

	logs = captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.RunChecker(ctx)
	s.RunReminder(ctx)
	assertLogNotContains(t, logs, "completato")
}

// TestCalculateSecondsUntilNextRun copre i casi di
// calculate_seconds_until_next_run: prima dell'ora, dopo, esattamente
// all'ora (→ domani), con i microsecondi.
func TestCalculateSecondsUntilNextRun(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		now  time.Time
		hour int
		want float64
	}{
		{"prima dell'ora", time.Date(2026, 10, 18, 8, 0, 0, 0, rome), 9, 3600},
		{"dopo l'ora", time.Date(2026, 10, 18, 10, 0, 0, 0, rome), 9, 23 * 3600},
		{"esattamente all'ora", time.Date(2026, 10, 18, 9, 0, 0, 0, rome), 9, 24 * 3600},
		{"microsecondi", time.Date(2026, 10, 18, 8, 59, 59, 500_000_999, rome), 9, 0.5},
		{"mezzanotte", time.Date(2026, 10, 18, 23, 30, 0, 0, rome), 0, 1800},
		{"fine anno", time.Date(2026, 12, 31, 12, 0, 0, 0, rome), 11, 23 * 3600},
		// datetime naive: la differenza è da orologio da parete anche nei
		// giorni del cambio d'ora (Python dorme 8h reali → le 10:00)
		{"inizio ora legale", time.Date(2026, 3, 29, 1, 0, 0, 0, rome), 9, 8 * 3600},
		{"fine ora legale", time.Date(2026, 10, 25, 1, 0, 0, 0, rome), 9, 8 * 3600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CalculateSecondsUntilNextRun(tc.now, tc.hour)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("CalculateSecondsUntilNextRun = %v, atteso %v", got, tc.want)
			}
		})
	}
}

// TestCalculateSecondsUntilNextRunInvalidHour: now.replace(hour=24) solleva
// ValueError.
func TestCalculateSecondsUntilNextRunInvalidHour(t *testing.T) {
	for _, hour := range []int{-1, 24, 100} {
		_, err := CalculateSecondsUntilNextRun(time.Now(), hour)
		if !pycompat.IsInstance(err, "ValueError") || err.Error() != "hour must be in 0..23" {
			t.Errorf("hour=%d: errore = %v", hour, err)
		}
	}
}

// TestDailyTaskSchedule verifica i log e le attese del loop giornaliero:
// log iniziale, attesa fino all'ora, esecuzione, log "⏰ Prossimo ..." e
// nuova attesa ricalcolata.
func TestDailyTaskSchedule(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := time.Date(2026, 10, 18, 8, 30, 0, 0, time.Local)
	var sleeps []time.Duration
	var runs int
	s := newTestScheduler()
	s.Now = func() time.Time { return clock }
	s.Sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		if len(sleeps) == 3 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	s.runScraperFn = func(context.Context) {
		runs++
		clock = clock.Add(90 * time.Second) // durata dell'esecuzione
	}

	if err := s.ScraperDailyTask(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ScraperDailyTask = %v", err)
	}
	want := []time.Duration{30 * time.Minute, 24*time.Hour - 90*time.Second, 24*time.Hour - 90*time.Second}
	if fmt.Sprint(sleeps) != fmt.Sprint(want) {
		t.Errorf("attese = %v, attese %v", sleeps, want)
	}
	if runs != 2 {
		t.Errorf("esecuzioni = %d, attese 2", runs)
	}
	assertLogContains(t, logs, "INFO - 🕷️  Scraper schedulato per le 9:00 (tra 0.5 ore)")
	assertLogContains(t, logs, "INFO - ⏰ Prossimo scraper tra 24.0 ore (alle 9:00)")
}

// TestDailyTaskLogTexts verifica i testi dei log di checker e reminder.
func TestDailyTaskLogTexts(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestScheduler()
	s.Sleep = func(ctx context.Context, _ time.Duration) error { return nil }
	var n atomic.Int32
	stopAfterOne := func(context.Context) {
		if n.Add(1)%2 == 0 {
			cancel()
		}
	}
	s.runCheckerFn = stopAfterOne
	s.runReminderFn = stopAfterOne
	_ = s.CheckerDailyTask(ctx)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	_ = s.ReminderDailyTask(ctx)

	assertLogContains(t, logs, "🔍 Checker schedulato per le 10:00 (tra 2.0 ore)")
	assertLogContains(t, logs, "⏰ Prossimo checker tra 2.0 ore (alle 10:00)")
	assertLogContains(t, logs, "📅 Reminder schedulato per le 11:00 (tra 3.0 ore)")
	assertLogContains(t, logs, "⏰ Prossimo reminder tra 3.0 ore (alle 11:00)")
}

// TestDailyTaskInvalidHour: un'ora non valida fa terminare il task con
// ValueError prima di qualsiasi attesa.
func TestDailyTaskInvalidHour(t *testing.T) {
	s := newTestScheduler()
	s.ScraperHour = 25
	err := s.ScraperDailyTask(context.Background())
	if !pycompat.IsInstance(err, "ValueError") {
		t.Fatalf("errore = %v, atteso ValueError", err)
	}
}

// TestSleepCtx verifica l'attesa di default (interrompibile).
func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Errorf("sleepCtx = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("sleepCtx cancellato = %v", err)
	}
	if err := sleepCtx(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("sleepCtx(0) cancellato = %v", err)
	}
	s := &Scheduler{}
	if err := s.sleep(context.Background(), 0); err != nil {
		t.Errorf("Scheduler.sleep = %v", err)
	}
	if s.now().IsZero() {
		t.Error("Scheduler.now zero")
	}
}

// TestNewSchedulerWiring verifica che NewScheduler colleghi le operazioni
// reali: con database vuoto checker e reminder terminano senza errori e lo
// scraper rispetta il timeout.
func TestNewSchedulerWiring(t *testing.T) {
	logs := captureLogs(t)
	store := newTestStore(t)
	s := NewScheduler(9, 10, 11, 600, "123:abc", store)
	if s.ScraperTimeout != 600*time.Second || s.ScraperHour != 9 || s.CheckerHour != 10 || s.ReminderHour != 11 {
		t.Fatalf("scheduler = %+v", s)
	}
	if err := s.CheckAndNotify(context.Background()); err != nil {
		t.Errorf("CheckAndNotify = %v", err)
	}
	if err := s.SendReminders(context.Background()); err != nil {
		t.Errorf("SendReminders = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.FetchTariffe(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchTariffe cancellato = %v", err)
	}
	assertLogContains(t, logs, "Nessun utente registrato")
}
