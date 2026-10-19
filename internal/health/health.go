// Package health implementa l'endpoint /health per il monitoring di
// OctoTracker, che verifica database, freschezza delle tariffe e task
// dello scheduler.
//
// Risposta: JSON indentato di 2 spazi nella forma
//
//	{"status": "healthy"|"degraded"|"unhealthy", "timestamp": "...",
//	 "checks": {"database": {...}, "tariffe": {...}, "bot": {...}}}
//
// con status HTTP 200 (anche se degraded) oppure 503 se unhealthy. Solo
// GET è ammesso (altri metodi → 405).
//
// # Task dello scheduler
//
// Lo scheduler espone lo stato dei task tramite l'interfaccia
// TaskReporter; Registry ne è un'implementazione pronta:
//
//	reg := health.NewRegistry()
//	t := reg.Register(health.ScraperTask)  // all'avvio del task
//	defer t.MarkDone()                     // quando il task termina
//	mux.Handle(health.Path, health.NewHandler(store, reg))
//
// Un task non registrato vale come "missing", un task terminato come
// "stopped".
package health

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dstmrk/octotracker/internal/logging"
)

var logger = logging.Logger("health_handler")

// now è l'orologio (ora locale); sostituibile nei test.
var now = time.Now

// Stati complessivi della risposta.
const (
	statusHealthy   = "healthy"
	statusDegraded  = "degraded"
	statusUnhealthy = "unhealthy"
)

// Stati dei singoli controlli.
const (
	checkOK      = "ok"
	checkWarning = "warning"
	checkError   = "error"
)

// Path è il percorso dell'endpoint.
const Path = "/health"

// Nomi dei task controllati.
const (
	ScraperTask = "scraper_task"
	CheckerTask = "checker_task"
)

// MaxTariffeAgeDays è l'età massima delle tariffe prima del warning (> 3 giorni).
const MaxTariffeAgeDays = 3

// TaskReporter espone lo stato dei task dello scheduler.
type TaskReporter interface {
	// TaskDone restituisce exists=false se il task name non è registrato;
	// altrimenti done riporta se il task è terminato (completato,
	// cancellato o crashato).
	TaskDone(name string) (exists, done bool)
}

// Task è un task registrato in Registry.
type Task struct {
	done atomic.Bool
}

// MarkDone segna il task come terminato.
func (t *Task) MarkDone() { t.done.Store(true) }

// Done riporta se il task è terminato.
func (t *Task) Done() bool { return t.done.Load() }

// Registry è un TaskReporter sicuro per l'uso concorrente in cui lo
// scheduler registra i task.
type Registry struct {
	mu    sync.Mutex
	tasks map[string]*Task
}

// NewRegistry crea un registro vuoto.
func NewRegistry() *Registry {
	return &Registry{tasks: map[string]*Task{}}
}

// Register registra (o sostituisce) un task attivo e ne restituisce l'handle.
func (r *Registry) Register(name string) *Task {
	t := &Task{}
	r.mu.Lock()
	r.tasks[name] = t
	r.mu.Unlock()
	return t
}

// TaskDone implementa TaskReporter.
func (r *Registry) TaskDone(name string) (exists, done bool) {
	r.mu.Lock()
	t, ok := r.tasks[name]
	r.mu.Unlock()
	if !ok {
		return false, false
	}
	return true, t.Done()
}

// Store è la parte del database usata dal check (*database.Store la
// implementa): Path() è il percorso del file SQLite.
type Store interface {
	Path() string
	GetUserCount() int
	GetLatestRateDate() *string
}

// Response è la risposta di /health.
type Response struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Checks    Checks `json:"checks"`
}

// Checks sono i risultati dei singoli controlli.
type Checks struct {
	Database Check `json:"database"`
	Tariffe  Check `json:"tariffe"`
	Bot      Check `json:"bot"`
}

// Check è il risultato di un controllo: nel JSON compaiono solo i campi
// valorizzati.
type Check struct {
	Status         string          `json:"status"`
	UsersCount     *int            `json:"users_count,omitempty"`
	LastUpdate     string          `json:"last_update,omitempty"`
	DaysOld        *int            `json:"days_old,omitempty"`
	Message        string          `json:"message,omitempty"`
	ScheduledTasks *ScheduledTasks `json:"scheduled_tasks,omitempty"`
	Error          string          `json:"error,omitempty"`
}

// ScheduledTasks è lo stato dei task: "running", "stopped" o "missing".
type ScheduledTasks struct {
	Scraper string `json:"scraper"`
	Checker string `json:"checker"`
}

// Handler è l'handler di /health.
type Handler struct {
	store Store
	tasks TaskReporter
}

// NewHandler crea l'handler di /health. tasks può essere nil (nessun task
// registrato).
func NewHandler(store Store, tasks TaskReporter) *Handler {
	return &Handler{store: store, tasks: tasks}
}

// ServeHTTP risponde alle GET con 503 se unhealthy, 200 altrimenti (anche
// degraded).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	res := CheckSystemHealth(h.store, h.tasks)
	status := http.StatusOK
	if res.Status == statusUnhealthy {
		status = http.StatusServiceUnavailable
	}
	body, _ := json.MarshalIndent(res, "", "  ") // solo stringhe e interi: non fallisce
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// isoformat formatta t come "YYYY-MM-DDTHH:MM:SS", con ".ffffff" solo se i
// microsecondi non sono zero.
func isoformat(t time.Time) string {
	if t.Nanosecond()/1000 == 0 {
		return t.Format("2006-01-02T15:04:05")
	}
	return t.Format("2006-01-02T15:04:05.000000")
}

// CheckSystemHealth esegue i controlli: un errore del database, delle
// tariffe o dei task rende lo stato unhealthy, un warning delle tariffe
// degraded.
func CheckSystemHealth(store Store, tasks TaskReporter) Response {
	checks := Checks{
		Database: checkDatabase(store),
		Tariffe:  checkTariffe(store),
		Bot:      checkTasks(tasks),
	}
	status := statusHealthy
	switch {
	case checks.Database.Status == checkError, checks.Tariffe.Status == checkError, checks.Bot.Status == checkError:
		status = statusUnhealthy
	case checks.Tariffe.Status == checkWarning:
		status = statusDegraded
	}
	return Response{Status: status, Timestamp: isoformat(now()), Checks: checks}
}

// guarded esegue check trasformando un errore o un panic nel risultato
// {"status": "error", "error": messaggio}, dopo il log logMsg.
func guarded(logMsg string, check func() (Check, error)) (result Check) {
	fail := func(msg string) Check {
		logger.Error(logMsg + ": " + msg)
		return Check{Status: checkError, Error: msg}
	}
	defer func() {
		if r := recover(); r != nil {
			result = fail(fmt.Sprint(r))
		}
	}()
	res, err := check()
	if err != nil {
		return fail(err.Error())
	}
	return res
}

// checkDatabase richiede che il file del database esista e sia un file
// regolare, poi esegue una query di test (conteggio utenti).
func checkDatabase(store Store) Check {
	return guarded("❌ Errore check database", func() (Check, error) {
		info, err := os.Stat(store.Path())
		if err != nil || !info.Mode().IsRegular() {
			return Check{Status: checkError, Error: "Database file not found"}, nil
		}
		usersCount := store.GetUserCount()
		return Check{Status: checkOK, UsersCount: &usersCount}, nil
	})
}

// checkTariffe dà warning se non ci sono tariffe o se l'ultima data è più
// vecchia di MaxTariffeAgeDays giorni, error se la data non è valida.
func checkTariffe(store Store) Check {
	return guarded("❌ Errore check tariffe", func() (Check, error) {
		lastUpdate := store.GetLatestRateDate()
		if lastUpdate == nil || *lastUpdate == "" {
			return Check{Status: checkWarning, Message: "No rates in database (will be populated on first scrape)"}, nil
		}
		date, err := time.Parse(time.DateOnly, *lastUpdate)
		if err != nil {
			return Check{}, err
		}
		// giorni di calendario tra la data delle tariffe e oggi (ora locale)
		n := now()
		today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
		daysOld := int(today.Sub(date) / (24 * time.Hour))

		res := Check{Status: checkOK, LastUpdate: *lastUpdate, DaysOld: &daysOld}
		if daysOld > MaxTariffeAgeDays {
			res.Status = checkWarning
			res.Message = fmt.Sprintf("Tariffe outdated (%d days old)", daysOld)
		}
		return res, nil
	})
}

func taskState(done bool) string {
	if done {
		return "stopped"
	}
	return "running"
}

// checkTasks richiede che entrambi i task esistano e non siano terminati.
func checkTasks(tasks TaskReporter) Check {
	return guarded("❌ Errore check tasks", func() (Check, error) {
		var scraperExists, scraperDone, checkerExists, checkerDone bool
		if tasks != nil {
			scraperExists, scraperDone = tasks.TaskDone(ScraperTask)
			checkerExists, checkerDone = tasks.TaskDone(CheckerTask)
		}
		if !scraperExists || !checkerExists {
			return Check{
				Status:         checkError,
				ScheduledTasks: &ScheduledTasks{Scraper: "missing", Checker: "missing"},
				Error:          "Scheduler tasks not initialized",
			}, nil
		}
		scheduled := &ScheduledTasks{Scraper: taskState(scraperDone), Checker: taskState(checkerDone)}
		// un task terminato potrebbe essere crashato
		if scraperDone || checkerDone {
			return Check{Status: checkError, ScheduledTasks: scheduled, Error: "One or more scheduler tasks have stopped"}, nil
		}
		return Check{Status: checkOK, ScheduledTasks: scheduled}, nil
	})
}
