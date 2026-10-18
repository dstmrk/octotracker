// Package health porta health_handler.py: l'endpoint /health per il
// monitoring di OctoTracker, che verifica database, freschezza delle
// tariffe e task dello scheduler.
//
// Risposta: JSON indentato di 2 spazi (json.dumps(indent=2), con escape
// \uXXXX dei caratteri non ASCII) nella forma
//
//	{"status": "healthy"|"degraded"|"unhealthy", "timestamp": "...",
//	 "checks": {"database": {...}, "tariffe": {...}, "bot": {...}}}
//
// con status HTTP 200 (anche se degraded) oppure 503 se unhealthy o in caso
// di eccezione.
//
// # Task dello scheduler
//
// In Python il check legge i task asyncio scraper_task e checker_task da
// bot_data. In Go lo scheduler espone lo stato tramite l'interfaccia
// TaskStatus; Registry ne è un'implementazione pronta:
//
//	reg := health.NewRegistry()
//	reg.Go(health.ScraperTask, func() { scraperLoop(ctx) })  // come asyncio.create_task
//	reg.Go(health.CheckerTask, func() { checkerLoop(ctx) })
//	mux.Handle("/health", health.NewHandler(store, reg))
//
// Un task non registrato vale come "missing", un task terminato (anche per
// panic) come "stopped".
package health

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/api/tornado"
	"github.com/dstmrk/octotracker/internal/logging"
)

var logger = logging.Logger("health_handler")

// now è l'orologio (datetime.now(), ora locale); sostituibile nei test.
var now = time.Now

// Path è il percorso dell'endpoint.
const Path = "/health"

// Nomi dei task controllati (le chiavi di bot_data).
const (
	ScraperTask = "scraper_task"
	CheckerTask = "checker_task"
)

// MaxTariffeAgeDays è l'età massima delle tariffe prima del warning (> 3 giorni).
const MaxTariffeAgeDays = 3

// TaskStatus espone lo stato dei task dello scheduler.
type TaskStatus interface {
	// TaskDone restituisce exists=false se il task name non è registrato
	// (bot_data.get(name) is None); altrimenti done riporta task.done(),
	// cioè se il task è terminato (completato, cancellato o crashato).
	TaskDone(name string) (exists, done bool)
}

// Task è un task registrato in Registry.
type Task struct {
	done atomic.Bool
}

// MarkDone segna il task come terminato (task.done() diventa True).
func (t *Task) MarkDone() { t.done.Store(true) }

// Done restituisce task.done().
func (t *Task) Done() bool { return t.done.Load() }

// Registry è un TaskStatus sicuro per l'uso concorrente: lo scheduler vi
// registra i task (come application.bot_data["scraper_task"] = task).
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

// Go registra il task name ed esegue fn in una goroutine; quando fn
// termina (anche per panic) il task risulta "stopped".
func (r *Registry) Go(name string, fn func()) *Task {
	t := r.Register(name)
	go func() {
		defer t.MarkDone()
		fn()
	}()
	return t
}

// TaskDone implementa TaskStatus.
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
// implementa). Path() sostituisce il percorso fisso "data/octotracker.db"
// di Python, che coincide con il default <DATA_DIR>/octotracker.db.
type Store interface {
	Path() string
	GetUserCount() int
	GetLatestRateDate() *string
}

// Handler è l'handler di /health (HealthHandler).
type Handler struct {
	store Store
	tasks TaskStatus
	check func() pycompat.Object
}

// NewHandler crea l'handler di /health. tasks può essere nil (nessun task
// registrato).
func NewHandler(store Store, tasks TaskStatus) *Handler {
	h := &Handler{store: store, tasks: tasks}
	h.check = func() pycompat.Object { return CheckSystemHealth(h.store, h.tasks) }
	return h
}

// ServeHTTP implementa http.Handler con la semantica di un RequestHandler
// Tornado che implementa solo get() (altri metodi → 405).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tornado.Serve(w, r, nil, tornado.Methods{http.MethodGet: h.get})
}

func panicError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}

// get replica HealthHandler.get: 503 se unhealthy, 200 altrimenti (anche
// degraded); in caso di eccezione 503 con {"status", "error", "timestamp"}.
func (h *Handler) get(rh *tornado.RequestHandler) error {
	body, status, err := func() (body string, status int, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = panicError(r)
			}
		}()
		healthData := h.check()
		status = http.StatusOK // OK (anche per degraded)
		if s, _ := healthData.Get("status"); s == "unhealthy" {
			status = http.StatusServiceUnavailable // Service Unavailable
		}
		body, err = pycompat.DumpsIndent(healthData, 2)
		return body, status, err
	}()
	rh.SetHeader("Content-Type", "application/json")
	if err == nil {
		rh.SetStatus(status)
		rh.Write(body)
		return nil
	}
	logger.Error(fmt.Sprintf("❌ Errore health check: %s", err.Error()))
	rh.SetStatus(http.StatusServiceUnavailable)
	rh.Write(pycompat.MustDumps(pycompat.Object{
		{Key: "status", Value: "unhealthy"},
		{Key: "error", Value: err.Error()},
		{Key: "timestamp", Value: pycompat.ISOFormat(now())},
	}))
	return nil
}

func statusOf(o pycompat.Object) any {
	s, _ := o.Get("status")
	return s
}

// CheckSystemHealth replica check_system_health: database (error →
// unhealthy), tariffe (error → unhealthy, warning → degraded) e task dello
// scheduler (error → unhealthy).
func CheckSystemHealth(store Store, tasks TaskStatus) pycompat.Object {
	checks := pycompat.Object{}
	overallStatus := "healthy"

	// Check 1: Database
	dbCheck := checkDatabase(store)
	checks.Set("database", dbCheck)
	if statusOf(dbCheck) == "error" {
		overallStatus = "unhealthy"
	}

	// Check 2: Tariffe
	tariffeCheck := checkTariffe(store)
	checks.Set("tariffe", tariffeCheck)
	if statusOf(tariffeCheck) == "error" {
		overallStatus = "unhealthy"
	} else if statusOf(tariffeCheck) == "warning" && overallStatus == "healthy" {
		overallStatus = "degraded"
	}

	// Check 3: Bot tasks
	tasksCheck := checkTasks(tasks)
	checks.Set("bot", tasksCheck)
	if statusOf(tasksCheck) == "error" {
		overallStatus = "unhealthy"
	}

	return pycompat.Object{
		{Key: "status", Value: overallStatus},
		{Key: "timestamp", Value: pycompat.ISOFormat(now())},
		{Key: "checks", Value: checks},
	}
}

// guarded esegue check trasformando errori e panic nel dict
// {"status": "error", "error": str(e)} dopo il log logMsg (logger.exception).
func guarded(logMsg string, check func() (pycompat.Object, error)) (result pycompat.Object) {
	fail := func(err error) pycompat.Object {
		logger.Error(fmt.Sprintf("%s: %s", logMsg, err.Error()))
		return pycompat.Object{{Key: "status", Value: "error"}, {Key: "error", Value: err.Error()}}
	}
	defer func() {
		if r := recover(); r != nil {
			result = fail(panicError(r))
		}
	}()
	res, err := check()
	if err != nil {
		return fail(err)
	}
	return res
}

// checkDatabase replica _check_database: il file del database deve esistere
// ed essere un file regolare, poi una query di test (conteggio utenti).
func checkDatabase(store Store) pycompat.Object {
	return guarded("❌ Errore check database", func() (pycompat.Object, error) {
		dbPath := store.Path()
		notFound := pycompat.Object{{Key: "status", Value: "error"}, {Key: "error", Value: "Database file not found"}}
		exists, err := pycompat.PathExists(dbPath)
		if err != nil {
			return nil, err
		}
		if !exists {
			return notFound, nil
		}
		isFile, err := pycompat.PathIsFile(dbPath)
		if err != nil {
			return nil, err
		}
		if !isFile {
			return notFound, nil
		}

		// Verifica database funzionante (query di test)
		usersCount := store.GetUserCount()
		return pycompat.Object{{Key: "status", Value: "ok"}, {Key: "users_count", Value: usersCount}}, nil
	})
}

// checkTariffe replica _check_tariffe: warning se non ci sono tariffe o se
// l'ultima data è più vecchia di 3 giorni, error se la data non è valida.
func checkTariffe(store Store) pycompat.Object {
	return guarded("❌ Errore check tariffe", func() (pycompat.Object, error) {
		lastUpdateStr := store.GetLatestRateDate()
		if lastUpdateStr == nil || *lastUpdateStr == "" {
			return pycompat.Object{
				{Key: "status", Value: "warning"},
				{Key: "message", Value: "No rates in database (will be populated on first scrape)"},
			}, nil
		}

		lastUpdate, err := pycompat.StrptimeYMD(*lastUpdateStr)
		if err != nil {
			return nil, err
		}
		daysOld := pycompat.TimedeltaDays(now(), lastUpdate)

		if daysOld > MaxTariffeAgeDays {
			return pycompat.Object{
				{Key: "status", Value: "warning"},
				{Key: "last_update", Value: *lastUpdateStr},
				{Key: "days_old", Value: daysOld},
				{Key: "message", Value: fmt.Sprintf("Tariffe outdated (%d days old)", daysOld)},
			}, nil
		}
		return pycompat.Object{
			{Key: "status", Value: "ok"},
			{Key: "last_update", Value: *lastUpdateStr},
			{Key: "days_old", Value: daysOld},
		}, nil
	})
}

// checkTasks replica _check_tasks: entrambi i task devono esistere e non
// essere terminati.
func checkTasks(tasks TaskStatus) pycompat.Object {
	return guarded("❌ Errore check tasks", func() (pycompat.Object, error) {
		var scraperExists, scraperDone, checkerExists, checkerDone bool
		if tasks != nil {
			scraperExists, scraperDone = tasks.TaskDone(ScraperTask)
			checkerExists, checkerDone = tasks.TaskDone(CheckerTask)
		}

		// Verifica task esistono
		if !scraperExists || !checkerExists {
			return pycompat.Object{
				{Key: "status", Value: "error"},
				{Key: "scheduled_tasks", Value: pycompat.Object{
					{Key: "scraper", Value: "missing"},
					{Key: "checker", Value: "missing"},
				}},
				{Key: "error", Value: "Scheduler tasks not initialized"},
			}, nil
		}

		// Verifica task non sono cancellati o in errore
		statusName := func(done bool) string {
			if done {
				return "stopped"
			}
			return "running"
		}
		scheduled := pycompat.Object{
			{Key: "scraper", Value: statusName(scraperDone)},
			{Key: "checker", Value: statusName(checkerDone)},
		}

		// Se un task è done(), potrebbe essere crashato
		if scraperDone || checkerDone {
			return pycompat.Object{
				{Key: "status", Value: "error"},
				{Key: "scheduled_tasks", Value: scheduled},
				{Key: "error", Value: "One or more scheduler tasks have stopped"},
			}, nil
		}
		return pycompat.Object{{Key: "status", Value: "ok"}, {Key: "scheduled_tasks", Value: scheduled}}, nil
	})
}
