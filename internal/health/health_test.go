package health

// Port di tests/test_health.py (23 casi). I patch di Python su
// _check_database/_check_tariffe/_check_tasks e sul modulo database sono
// sostituiti da uno Store e da un TaskStatus finti.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/api/apitest"
	"github.com/dstmrk/octotracker/internal/api/pycompat"
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

func get(o pycompat.Object, keys ...string) any {
	var v any = o
	for _, k := range keys {
		obj, ok := v.(pycompat.Object)
		if !ok {
			return nil
		}
		v, _ = obj.Get(k)
	}
	return v
}

func healthyStore(t *testing.T) fakeStore {
	return fakeStore{path: dbFile(t), count: 1, date: dateAgo(1)}
}

func runningTasks() fakeTasks { return fakeTasks{ScraperTask: false, CheckerTask: false} }

// ========== TestHealthHandler ==========

func serveWith(t *testing.T, check func() pycompat.Object) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(fakeStore{}, nil)
	h.check = check
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))
	return rec
}

func fixed(status string) func() pycompat.Object {
	return func() pycompat.Object {
		return pycompat.Object{{Key: "status", Value: status}, {Key: "timestamp", Value: "2025-11-11T10:00:00"}, {Key: "checks", Value: pycompat.Object{}}}
	}
}

func TestGetHealthy(t *testing.T) {
	rec := serveWith(t, fixed("healthy"))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, header %v", rec.Code, rec.Header())
	}
	want := "{\n  \"status\": \"healthy\",\n  \"timestamp\": \"2025-11-11T10:00:00\",\n  \"checks\": {}\n}"
	if rec.Body.String() != want {
		t.Errorf("corpo = %q", rec.Body.String())
	}
}

func TestGetDegraded(t *testing.T) {
	if rec := serveWith(t, fixed("degraded")); rec.Code != 200 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestGetUnhealthy(t *testing.T) {
	if rec := serveWith(t, fixed("unhealthy")); rec.Code != 503 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestGetException(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	setNow(t, time.Date(2025, 11, 11, 10, 0, 0, 0, time.Local))
	rec := serveWith(t, func() pycompat.Object { panic(errors.New("Test error")) })
	if rec.Code != 503 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d", rec.Code)
	}
	if want := `{"status": "unhealthy", "error": "Test error", "timestamp": "2025-11-11T10:00:00"}`; rec.Body.String() != want {
		t.Errorf("corpo = %s", rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp["status"] != "unhealthy" || resp["error"] == nil {
		t.Errorf("risposta = %v (%v)", resp, err)
	}
	if !hasRecord(logs.Take(), apitest.Record{"health_handler", "ERROR", "❌ Errore health check: Test error"}) {
		t.Error("log mancante")
	}
	// un valore non serializzabile fa fallire json.dumps: stesso ramo except
	rec = serveWith(t, func() pycompat.Object { return pycompat.Object{{Key: "status", Value: struct{}{}}} })
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "is not JSON serializable") {
		t.Errorf("dumps fallito: %d %s", rec.Code, rec.Body.String())
	}
}

func hasRecord(recs []apitest.Record, want apitest.Record) bool {
	for _, r := range recs {
		if r == want {
			return true
		}
	}
	return false
}

// ========== TestCheckSystemHealth ==========

func TestAllHealthy(t *testing.T) {
	result := CheckSystemHealth(healthyStore(t), runningTasks())
	if get(result, "status") != "healthy" || get(result, "timestamp") == nil || get(result, "checks") == nil {
		t.Fatalf("result = %v", result)
	}
	for _, k := range []string{"database", "tariffe", "bot"} {
		if get(result, "checks", k, "status") != "ok" {
			t.Errorf("%s = %v", k, get(result, "checks", k))
		}
	}
}

func TestDegradedTariffeWarning(t *testing.T) {
	store := healthyStore(t)
	store.date = dateAgo(10)
	if s := get(CheckSystemHealth(store, runningTasks()), "status"); s != "degraded" {
		t.Errorf("status = %v", s)
	}
}

func TestUnhealthyDatabaseError(t *testing.T) {
	store := healthyStore(t)
	store.path = filepath.Join(t.TempDir(), "missing.db")
	if s := get(CheckSystemHealth(store, runningTasks()), "status"); s != "unhealthy" {
		t.Errorf("status = %v", s)
	}
}

func TestUnhealthyTariffeError(t *testing.T) {
	store := healthyStore(t)
	bad := "non-una-data"
	store.date = &bad
	if s := get(CheckSystemHealth(store, runningTasks()), "status"); s != "unhealthy" {
		t.Errorf("status = %v", s)
	}
}

func TestUnhealthyTasksError(t *testing.T) {
	if s := get(CheckSystemHealth(healthyStore(t), fakeTasks{}), "status"); s != "unhealthy" {
		t.Errorf("status = %v", s)
	}
	// warning delle tariffe non abbassa unhealthy a degraded
	store := healthyStore(t)
	store.path = filepath.Join(t.TempDir(), "missing.db")
	store.date = nil
	if s := get(CheckSystemHealth(store, runningTasks()), "status"); s != "unhealthy" {
		t.Errorf("db error + warning: %v", s)
	}
}

// ========== TestCheckDatabase ==========

func TestDatabaseOK(t *testing.T) {
	result := checkDatabase(fakeStore{path: dbFile(t), count: 42})
	if get(result, "status") != "ok" || get(result, "users_count") != 42 {
		t.Errorf("result = %v", result)
	}
}

func TestDatabaseNotFound(t *testing.T) {
	result := checkDatabase(fakeStore{path: filepath.Join(t.TempDir(), "missing.db")})
	errText, _ := get(result, "error").(string)
	if get(result, "status") != "error" || !strings.Contains(strings.ToLower(errText), "not found") {
		t.Errorf("result = %v", result)
	}
}

func TestDatabaseNotFile(t *testing.T) {
	result := checkDatabase(fakeStore{path: t.TempDir()})
	if get(result, "status") != "error" || get(result, "error") != "Database file not found" {
		t.Errorf("result = %v", result)
	}
}

func TestDatabaseException(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	result := checkDatabase(fakeStore{path: dbFile(t), countPanic: errors.New("Database locked")})
	errText, _ := get(result, "error").(string)
	if get(result, "status") != "error" || !strings.Contains(strings.ToLower(errText), "locked") {
		t.Errorf("result = %v", result)
	}
	if !hasRecord(logs.Take(), apitest.Record{"health_handler", "ERROR", "❌ Errore check database: Database locked"}) {
		t.Error("log mancante")
	}
	// errore di stat non ignorato (ENAMETOOLONG) → stesso ramo except
	long := filepath.Join(t.TempDir(), strings.Repeat("x", 300))
	result = checkDatabase(fakeStore{path: long})
	if errText, _ := get(result, "error").(string); !strings.HasPrefix(errText, "[Errno 36] File name too long: ") {
		t.Errorf("result = %v", result)
	}
}

// ========== TestCheckTariffe ==========

func TestTariffeOK(t *testing.T) {
	yesterday := dateAgo(1)
	result := checkTariffe(fakeStore{date: yesterday})
	if get(result, "status") != "ok" || get(result, "last_update") != *yesterday || get(result, "days_old") != 1 {
		t.Errorf("result = %v", result)
	}
}

func TestTariffeNoData(t *testing.T) {
	result := checkTariffe(fakeStore{})
	msg, _ := get(result, "message").(string)
	if get(result, "status") != "warning" || !strings.Contains(strings.ToLower(msg), "no rates") {
		t.Errorf("result = %v", result)
	}
	empty := ""
	if get(checkTariffe(fakeStore{date: &empty}), "status") != "warning" {
		t.Error("stringa vuota non trattata come assente")
	}
}

func TestTariffeOutdated(t *testing.T) {
	result := checkTariffe(fakeStore{date: dateAgo(5)})
	msg, _ := get(result, "message").(string)
	if get(result, "status") != "warning" || get(result, "days_old") != 5 || !strings.Contains(strings.ToLower(msg), "outdated") {
		t.Errorf("result = %v", result)
	}
	if msg != "Tariffe outdated (5 days old)" {
		t.Errorf("message = %q", msg)
	}
}

func TestTariffeException(t *testing.T) {
	result := checkTariffe(fakeStore{datePanic: errors.New("DB error")})
	errText, _ := get(result, "error").(string)
	if get(result, "status") != "error" || !strings.Contains(strings.ToLower(errText), "db error") {
		t.Errorf("result = %v", result)
	}
	// senza eccezione e senza data si torna al warning "No rates"
	result = checkTariffe(fakeStore{})
	if get(result, "status") != "warning" {
		t.Errorf("result = %v", result)
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
		if get(result, "status") != c.status || get(result, "days_old") != c.days {
			t.Errorf("%v: %v", c.now, result)
		}
	}
}

// ========== TestCheckTasks ==========

func TestTasksOK(t *testing.T) {
	result := checkTasks(runningTasks())
	if get(result, "status") != "ok" || get(result, "scheduled_tasks", "scraper") != "running" || get(result, "scheduled_tasks", "checker") != "running" {
		t.Errorf("result = %v", result)
	}
}

func TestTasksMissing(t *testing.T) {
	for _, tasks := range []TaskStatus{fakeTasks{}, nil, fakeTasks{ScraperTask: false}} {
		result := checkTasks(tasks)
		errText, _ := get(result, "error").(string)
		if get(result, "status") != "error" || get(result, "scheduled_tasks", "scraper") != "missing" ||
			get(result, "scheduled_tasks", "checker") != "missing" || !strings.Contains(strings.ToLower(errText), "not initialized") {
			t.Errorf("result = %v", result)
		}
	}
}

func TestTasksStoppedScraper(t *testing.T) {
	result := checkTasks(fakeTasks{ScraperTask: true, CheckerTask: false})
	errText, _ := get(result, "error").(string)
	if get(result, "status") != "error" || get(result, "scheduled_tasks", "scraper") != "stopped" ||
		get(result, "scheduled_tasks", "checker") != "running" || !strings.Contains(strings.ToLower(errText), "stopped") {
		t.Errorf("result = %v", result)
	}
}

func TestTasksStoppedChecker(t *testing.T) {
	result := checkTasks(fakeTasks{ScraperTask: false, CheckerTask: true})
	if get(result, "status") != "error" || get(result, "scheduled_tasks", "scraper") != "running" || get(result, "scheduled_tasks", "checker") != "stopped" {
		t.Errorf("result = %v", result)
	}
}

func TestTasksBothStopped(t *testing.T) {
	result := checkTasks(fakeTasks{ScraperTask: true, CheckerTask: true})
	if get(result, "status") != "error" || get(result, "scheduled_tasks", "scraper") != "stopped" || get(result, "scheduled_tasks", "checker") != "stopped" {
		t.Errorf("result = %v", result)
	}
}

func TestTasksException(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	result := checkTasks(fakeTasks{ScraperTask: "raise", CheckerTask: false})
	if get(result, "status") != "error" || get(result, "error") != "Task error" {
		t.Errorf("result = %v", result)
	}
	if !hasRecord(logs.Take(), apitest.Record{"health_handler", "ERROR", "❌ Errore check tasks: Task error"}) {
		t.Error("log mancante")
	}
	result = checkTasks(panicTasks{})
	if get(result, "error") != "boom" {
		t.Errorf("panic non error: %v", result)
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

	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	ct := reg.Go(CheckerTask, func() {
		defer wg.Done()
		<-release
	})
	if _, done := reg.TaskDone(CheckerTask); done {
		t.Error("task in esecuzione risulta terminato")
	}
	close(release)
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for !ct.Done() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, done := reg.TaskDone(CheckerTask); !done {
		t.Error("task terminato risulta attivo")
	}
	// Registry come TaskStatus di CheckSystemHealth
	if get(CheckSystemHealth(healthyStore(t), reg), "checks", "bot", "status") != "error" {
		t.Error("task fermi non segnalati")
	}
	reg.Register(ScraperTask)
	reg.Register(CheckerTask)
	if get(CheckSystemHealth(healthyStore(t), reg), "status") != "healthy" {
		t.Error("task riregistrati non attivi")
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
