package app

// Test dello scheduler: esecuzioni di scraper, checker e reminder, calcolo
// dell'attesa e loop giornaliero.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// ========== RunScraper, RunChecker, RunReminder ==========

var (
	errConnection = &url.Error{Op: "Get", URL: "https://www.arera.it", Err: errors.New("connection refused")}
	errIO         = &fs.PathError{Op: "write", Path: "octotracker.db", Err: errors.New("disk full")}
)

// TestRunScraperErrors: ogni categoria di errore ha il suo log.
func TestRunScraperErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errConnection, `ERROR - 🌐 Errore di connessione scraper: Get "https://www.arera.it": connection refused`},
		{errIO, "ERROR - 💾 Errore I/O scraper: write octotracker.db: disk full"},
		{timeoutErr{}, "ERROR - ⏱️  Scraper interrotto: nessuna risposta entro 600s"},
		{errors.New("boom"), "ERROR - ❌ Errore inatteso scraper: boom"},
	}
	for _, tc := range cases {
		logs := captureLogs(t)
		s := newTestScheduler()
		s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) { return nil, tc.err }
		s.RunScraper(context.Background())
		assertLogContains(t, logs, tc.want)
	}
}

// TestRunScraperTimeout: RunScraper logga un errore chiaro se lo scraper
// non risponde entro il timeout.
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

// TestRunCheckerAndReminderErrors: ogni categoria di errore ha il suo log
// (un 400 dell'API è un errore di rete, un rate limit un errore Telegram).
func TestRunCheckerAndReminderErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"), "ERROR - ❌ Errore Telegram %s: Forbidden: bot was blocked by the user"},
		{testutil.RetryAfterError("sendMessage", 5), "ERROR - ❌ Errore Telegram %s: Too Many Requests: retry after 5"},
		{testutil.TelegramError("sendMessage", 400, "Bad Request: chat not found"), "ERROR - 🌐 Errore di rete %s: Bad Request: chat not found"},
		{errConnection, `ERROR - 🌐 Errore di rete %s: Get "https://www.arera.it": connection refused`},
		{errIO, "ERROR - 💾 Errore I/O %s: write octotracker.db: disk full"},
		{errors.New("boom"), "ERROR - ❌ Errore inatteso %s: boom"},
	}
	for _, tc := range cases {
		logs := captureLogs(t)
		s := newTestScheduler()
		s.CheckAndNotify = func(context.Context) error { return tc.err }
		s.SendReminders = func(context.Context) error { return tc.err }
		s.RunChecker(context.Background())
		s.RunReminder(context.Background())
		assertLogContains(t, logs, fmt.Sprintf(tc.want, "checker"))
		assertLogContains(t, logs, fmt.Sprintf(tc.want, "reminder"))
		assertLogNotContains(t, logs, "completato")
	}
}

// ========== *_daily_task ==========

// panicThenCancel restituisce una funzione run che alla prima chiamata va
// in panic e alla seconda cancella ctx.
func panicThenCancel(cancel context.CancelFunc, calls *atomic.Int32) func(context.Context) {
	return func(context.Context) {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		cancel()
	}
}

// TestDailyTasksLogPanics: un panic nell'esecuzione viene loggato e il task
// giornaliero continua fino alla cancellazione.
func TestDailyTasksLogPanics(t *testing.T) {
	for _, tc := range []struct {
		label string
		set   func(s *Scheduler, run func(context.Context))
		task  func(s *Scheduler, ctx context.Context) error
	}{
		{"scraper", func(s *Scheduler, run func(context.Context)) { s.runScraperFn = run }, (*Scheduler).ScraperDailyTask},
		{"checker", func(s *Scheduler, run func(context.Context)) { s.runCheckerFn = run }, (*Scheduler).CheckerDailyTask},
		{"reminder", func(s *Scheduler, run func(context.Context)) { s.runReminderFn = run }, (*Scheduler).ReminderDailyTask},
	} {
		logs := captureLogs(t)
		ctx, cancel := context.WithCancel(context.Background())
		var calls atomic.Int32
		s := newTestScheduler()
		tc.set(s, panicThenCancel(cancel, &calls))
		if err := tc.task(s, ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: task = %v, attesa cancellazione", tc.label, err)
		}
		cancel()
		assertLogContains(t, logs, "ERROR - ❌ Errore non gestito nel task "+tc.label+": boom")
		if calls.Load() != 2 {
			t.Errorf("%s: eseguito %d volte, atteso 2", tc.label, calls.Load())
		}
	}
}

// TestRunReminderSuccess: run_reminder logga il completamento in caso di successo.
func TestRunReminderSuccess(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	s.RunReminder(context.Background())
	assertLogContains(t, logs, "INFO - 📅 Avvio reminder scadenze...")
	assertLogContains(t, logs, "INFO - ✅ Reminder scadenze completato")
}

// TestRunScraperSuccessAndCancellation: il risultato viene loggato; la
// cancellazione non logga errori.
func TestRunScraperSuccessAndCancellation(t *testing.T) {
	logs := captureLogs(t)
	s := newTestScheduler()
	tariffe := &scraper.Tariffe{DataAggiornamento: "18/10/2026"}
	s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) { return tariffe, nil }
	s.RunScraper(context.Background())
	assertLogContains(t, logs, "INFO - 🕷️  Avvio scraper...")
	assertLogContains(t, logs, fmt.Sprintf("INFO - ✅ Scraper completato: %v", tariffe))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.FetchTariffe = func(ctx context.Context) (*scraper.Tariffe, error) { return nil, ctx.Err() }
	before := logs.String()
	s.RunScraper(ctx)
	if after := strings.TrimPrefix(logs.String(), before); strings.Contains(after, "ERROR") {
		t.Errorf("cancellazione loggata come errore: %s", after)
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
		// giorni del cambio d'ora (l'attesa reale è di 8h → le 10:00)
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

// TestCalculateSecondsUntilNextRunInvalidHour: un'ora fuori da 0..23 è un
// errore.
func TestCalculateSecondsUntilNextRunInvalidHour(t *testing.T) {
	for _, hour := range []int{-1, 24, 100} {
		if _, err := CalculateSecondsUntilNextRun(time.Now(), hour); err == nil {
			t.Errorf("hour=%d: nessun errore", hour)
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

// TestDailyTaskInvalidHour: un'ora non valida fa terminare il task con un
// errore prima di qualsiasi attesa.
func TestDailyTaskInvalidHour(t *testing.T) {
	s := newTestScheduler()
	s.ScraperHour = 25
	s.Sleep = func(context.Context, time.Duration) error { t.Error("attesa inattesa"); return nil }
	if err := s.ScraperDailyTask(context.Background()); err == nil || !strings.Contains(err.Error(), "25") {
		t.Fatalf("errore = %v", err)
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
