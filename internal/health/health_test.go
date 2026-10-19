package health

// Port di tests/test_health.py. I patch di Python su
// _check_database/_check_tariffe/_check_tasks e sul modulo database sono
// sostituiti da uno Store e da un TaskReporter finti.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/api/apitest"
)

// fakeStore implementa Store; i campi *Panic simulano eccezioni.
type fakeStore struct {
	path       string
	count      int
	countPanic error
	date       *string
	datePanic  error
}

func (f fakeStore) Path() string { return f.path }

func (f fakeStore) GetUserCount() int {
	if f.countPanic != nil {
		panic(f.countPanic)
	}
	return f.count
}

func (f fakeStore) GetLatestRateDate() *string {
	if f.datePanic != nil {
		panic(f.datePanic)
	}
	return f.date
}

// dbFile crea un file database finto e ne restituisce il percorso.
func dbFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "octotracker.db")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func dateAgo(days int) *string {
	s := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	return &s
}

func healthyStore(t *testing.T) fakeStore {
	return fakeStore{path: dbFile(t), count: 1, date: dateAgo(1)}
}

func runningTasks() fakeTasks { return fakeTasks{ScraperTask: false, CheckerTask: false} }

func intVal(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func hasRecord(recs []apitest.Record, want apitest.Record) bool {
	for _, r := range recs {
		if r == want {
			return true
		}
	}
	return false
}

// ========== TestHealthHandler ==========

func serve(t *testing.T, store Store, tasks TaskReporter) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewHandler(store, tasks).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))
	return rec
}

func TestGetHealthy(t *testing.T) {
	setNow(t, time.Date(2025, 11, 11, 10, 0, 0, 0, time.Local))
	date := "2025-11-10"
	rec := serve(t, fakeStore{path: dbFile(t), count: 2, date: &date}, runningTasks())
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, header %v", rec.Code, rec.Header())
	}
	want := `{
  "status": "healthy",
  "timestamp": "2025-11-11T10:00:00",
  "checks": {
    "database": {
      "status": "ok",
      "users_count": 2
    },
    "tariffe": {
      "status": "ok",
      "last_update": "2025-11-10",
      "days_old": 1
    },
    "bot": {
      "status": "ok",
      "scheduled_tasks": {
        "scraper": "running",
        "checker": "running"
      }
    }
  }
}`
	if rec.Body.String() != want {
		t.Errorf("corpo = %s", rec.Body.String())
	}
}

func TestGetDegraded(t *testing.T) {
	store := healthyStore(t)
	store.date = dateAgo(10)
	if rec := serve(t, store, runningTasks()); rec.Code != 200 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestGetUnhealthy(t *testing.T) {
	rec := serve(t, healthyStore(t), nil)
	if rec.Code != 503 {
		t.Errorf("status %d", rec.Code)
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Status != "unhealthy" || resp.Checks.Bot.Error == "" {
		t.Errorf("risposta = %+v (%v)", resp, err)
	}
}

// ========== TestCheckSystemHealth ==========

func TestAllHealthy(t *testing.T) {
	result := CheckSystemHealth(healthyStore(t), runningTasks())
	if result.Status != "healthy" || result.Timestamp == "" {
		t.Fatalf("result = %+v", result)
	}
	for name, c := range map[string]Check{"database": result.Checks.Database, "tariffe": result.Checks.Tariffe, "bot": result.Checks.Bot} {
		if c.Status != "ok" {
			t.Errorf("%s = %+v", name, c)
		}
	}
}

func TestDegradedTariffeWarning(t *testing.T) {
	store := healthyStore(t)
	store.date = dateAgo(10)
	if s := CheckSystemHealth(store, runningTasks()).Status; s != "degraded" {
		t.Errorf("status = %v", s)
	}
}

func TestUnhealthyDatabaseError(t *testing.T) {
	store := healthyStore(t)
	store.path = filepath.Join(t.TempDir(), "missing.db")
	if s := CheckSystemHealth(store, runningTasks()).Status; s != "unhealthy" {
		t.Errorf("status = %v", s)
	}
}

func TestUnhealthyTariffeError(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	store := healthyStore(t)
	bad := "2099-13-01"
	store.date = &bad
	result := CheckSystemHealth(store, runningTasks())
	if result.Status != "unhealthy" || result.Checks.Tariffe.Status != "error" || result.Checks.Tariffe.Error == "" {
		t.Errorf("result = %+v", result)
	}
	if got := logs.Take(); len(got) != 1 || !strings.HasPrefix(got[0][2], "❌ Errore check tariffe: ") {
		t.Errorf("log = %q", got)
	}
	if rec := serve(t, store, runningTasks()); rec.Code != 503 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestUnhealthyTasksError(t *testing.T) {
	if s := CheckSystemHealth(healthyStore(t), fakeTasks{}).Status; s != "unhealthy" {
		t.Errorf("status = %v", s)
	}
	// warning delle tariffe non abbassa unhealthy a degraded
	store := healthyStore(t)
	store.path = filepath.Join(t.TempDir(), "missing.db")
	store.date = nil
	if s := CheckSystemHealth(store, runningTasks()).Status; s != "unhealthy" {
		t.Errorf("db error + warning: %v", s)
	}
}

// ========== TestCheckDatabase ==========

func TestDatabaseOK(t *testing.T) {
	result := checkDatabase(fakeStore{path: dbFile(t), count: 42})
	if result.Status != "ok" || intVal(result.UsersCount) != 42 {
		t.Errorf("result = %+v", result)
	}
}

func TestDatabaseNotFound(t *testing.T) {
	result := checkDatabase(fakeStore{path: filepath.Join(t.TempDir(), "missing.db")})
	if result.Status != "error" || !strings.Contains(strings.ToLower(result.Error), "not found") {
		t.Errorf("result = %+v", result)
	}
}

func TestDatabaseNotFile(t *testing.T) {
	result := checkDatabase(fakeStore{path: t.TempDir()})
	if result.Status != "error" || result.Error != "Database file not found" {
		t.Errorf("result = %+v", result)
	}
}

func TestDatabaseException(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	result := checkDatabase(fakeStore{path: dbFile(t), countPanic: errors.New("Database locked")})
	if result.Status != "error" || !strings.Contains(strings.ToLower(result.Error), "locked") || result.UsersCount != nil {
		t.Errorf("result = %+v", result)
	}
	if !hasRecord(logs.Take(), apitest.Record{"health_handler", "ERROR", "❌ Errore check database: Database locked"}) {
		t.Error("log mancante")
	}
}

// ========== TestCheckTariffe ==========

func TestTariffeOK(t *testing.T) {
	yesterday := dateAgo(1)
	result := checkTariffe(fakeStore{date: yesterday})
	if result.Status != "ok" || result.LastUpdate != *yesterday || intVal(result.DaysOld) != 1 || result.Message != "" {
		t.Errorf("result = %+v", result)
	}
}

func TestTariffeNoData(t *testing.T) {
	result := checkTariffe(fakeStore{})
	if result.Status != "warning" || !strings.Contains(strings.ToLower(result.Message), "no rates") || result.DaysOld != nil {
		t.Errorf("result = %+v", result)
	}
	empty := ""
	if checkTariffe(fakeStore{date: &empty}).Status != "warning" {
		t.Error("stringa vuota non trattata come assente")
	}
}

func TestTariffeOutdated(t *testing.T) {
	result := checkTariffe(fakeStore{date: dateAgo(5)})
	if result.Status != "warning" || intVal(result.DaysOld) != 5 || result.Message != "Tariffe outdated (5 days old)" {
		t.Errorf("result = %+v", result)
	}
}

func TestTariffeException(t *testing.T) {
	result := checkTariffe(fakeStore{datePanic: errors.New("DB error")})
	if result.Status != "error" || !strings.Contains(strings.ToLower(result.Error), "db error") {
		t.Errorf("result = %+v", result)
	}
	// senza eccezione e senza data si torna al warning "No rates"
	if result = checkTariffe(fakeStore{}); result.Status != "warning" {
		t.Errorf("result = %+v", result)
	}
}

func TestTariffeBoundaries(t *testing.T) {
	d := "2099-01-05"
	cases := []struct {
		now    time.Time
		status string
		days   int
	}{
		{time.Date(2099, 1, 8, 23, 59, 59, 0, time.Local), "ok", 3},
		{time.Date(2099, 1, 9, 0, 0, 0, 0, time.Local), "warning", 4},
		{time.Date(2099, 1, 4, 12, 0, 0, 0, time.Local), "ok", -1},
	}
	for _, c := range cases {
		setNow(t, c.now)
		result := checkTariffe(fakeStore{date: &d})
		if result.Status != c.status || intVal(result.DaysOld) != c.days {
			t.Errorf("%v: %+v", c.now, result)
		}
	}
}

func TestIsoformat(t *testing.T) {
	if got := isoformat(time.Date(2099, 1, 6, 10, 0, 0, 123456789, time.UTC)); got != "2099-01-06T10:00:00.123456" {
		t.Errorf("con microsecondi: %q", got)
	}
	if got := isoformat(time.Date(2099, 1, 6, 10, 0, 0, 999, time.UTC)); got != "2099-01-06T10:00:00" {
		t.Errorf("senza microsecondi: %q", got)
	}
}

// ========== TestCheckTasks ==========

func scheduled(c Check) ScheduledTasks {
	if c.ScheduledTasks == nil {
		return ScheduledTasks{}
	}
	return *c.ScheduledTasks
}

func TestTasksOK(t *testing.T) {
	result := checkTasks(runningTasks())
	if result.Status != "ok" || scheduled(result) != (ScheduledTasks{"running", "running"}) || result.Error != "" {
		t.Errorf("result = %+v", result)
	}
}

func TestTasksMissing(t *testing.T) {
	for _, tasks := range []TaskReporter{fakeTasks{}, nil, fakeTasks{ScraperTask: false}} {
		result := checkTasks(tasks)
		if result.Status != "error" || scheduled(result) != (ScheduledTasks{"missing", "missing"}) ||
			!strings.Contains(strings.ToLower(result.Error), "not initialized") {
			t.Errorf("result = %+v", result)
		}
	}
}

func TestTasksStoppedScraper(t *testing.T) {
	result := checkTasks(fakeTasks{ScraperTask: true, CheckerTask: false})
	if result.Status != "error" || scheduled(result) != (ScheduledTasks{"stopped", "running"}) ||
		!strings.Contains(strings.ToLower(result.Error), "stopped") {
		t.Errorf("result = %+v", result)
	}
}

func TestTasksStoppedChecker(t *testing.T) {
	result := checkTasks(fakeTasks{ScraperTask: false, CheckerTask: true})
	if result.Status != "error" || scheduled(result) != (ScheduledTasks{"running", "stopped"}) {
		t.Errorf("result = %+v", result)
	}
}

func TestTasksBothStopped(t *testing.T) {
	result := checkTasks(fakeTasks{ScraperTask: true, CheckerTask: true})
	if result.Status != "error" || scheduled(result) != (ScheduledTasks{"stopped", "stopped"}) {
		t.Errorf("result = %+v", result)
	}
}

func TestTasksException(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	result := checkTasks(fakeTasks{ScraperTask: "raise", CheckerTask: false})
	if result.Status != "error" || result.Error != "Task error" || result.ScheduledTasks != nil {
		t.Errorf("result = %+v", result)
	}
	if !hasRecord(logs.Take(), apitest.Record{"health_handler", "ERROR", "❌ Errore check tasks: Task error"}) {
		t.Error("log mancante")
	}
	if result = checkTasks(panicTasks{}); result.Error != "boom" {
		t.Errorf("panic non error: %+v", result)
	}
}

type panicTasks struct{}

func (panicTasks) TaskDone(string) (bool, bool) { panic("boom") }

// ========== Registry ==========

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	if exists, _ := reg.TaskDone(ScraperTask); exists {
		t.Error("task non registrato esiste")
	}
	task := reg.Register(ScraperTask)
	if exists, done := reg.TaskDone(ScraperTask); !exists || done {
		t.Errorf("registrato: %v %v", exists, done)
	}
	task.MarkDone()
	if _, done := reg.TaskDone(ScraperTask); !done || !task.Done() {
		t.Error("MarkDone non visibile")
	}
	// Registry come TaskReporter di CheckSystemHealth
	reg.Register(CheckerTask)
	if CheckSystemHealth(healthyStore(t), reg).Checks.Bot.Status != "error" {
		t.Error("task fermo non segnalato")
	}
	reg.Register(ScraperTask)
	if s := CheckSystemHealth(healthyStore(t), reg).Status; s != "healthy" {
		t.Errorf("task riregistrati: %s", s)
	}
}

func TestServeHTTPMethods(t *testing.T) {
	h := NewHandler(healthyStore(t), runningTasks())
	for method, want := range map[string]int{"GET": 200, "HEAD": 405, "OPTIONS": 405, "POST": 405} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, Path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d", method, rec.Code)
		}
	}
}
