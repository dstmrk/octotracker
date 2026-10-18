package health

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/api/apitest"
	"github.com/dstmrk/octotracker/internal/database"
)

// healthGolden sono le risposte della vera app Tornado (health_handler.py)
// con DB, orologio e task fissati per scenario.
type healthGolden struct {
	Scenarios []struct {
		Name       string         `json:"name"`
		SQL        []string       `json:"sql"`
		Now        [7]int         `json:"now"`
		Tasks      [2]any         `json:"tasks"` // false/true = running/stopped, null = assente, "raise" = done() solleva
		HealthPath string         `json:"health_path"`
		Cases      []apitest.Case `json:"cases"`
	} `json:"scenarios"`
}

// fakeTasks è un TaskStatus configurabile; "raise" fa panicare TaskDone
// come task.done() che solleva Exception("Task error").
type fakeTasks map[string]any

func (f fakeTasks) TaskDone(name string) (bool, bool) {
	v, ok := f[name]
	if !ok || v == nil {
		return false, false
	}
	if v == "raise" {
		panic(errors.New("Task error"))
	}
	return true, v.(bool)
}

// pathStore sostituisce Path() di uno Store (DB assente o directory).
type pathStore struct {
	Store
	path string
}

func (p pathStore) Path() string { return p.path }

func setNow(t *testing.T, tm time.Time) {
	t.Helper()
	prev := now
	now = func() time.Time { return tm }
	t.Cleanup(func() { now = prev })
}

func TestGoldenHTTP(t *testing.T) {
	data, err := os.ReadFile("testdata/http_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g healthGolden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, sc := range g.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			logs := apitest.CaptureLogs(t)
			n := sc.Now
			setNow(t, time.Date(n[0], time.Month(n[1]), n[2], n[3], n[4], n[5], n[6]*1000, time.Local))

			dir := t.TempDir()
			dbPath := filepath.Join(dir, "data", database.DBFileName)
			db, err := database.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			apitest.ExecSQL(t, dbPath, sc.SQL)

			var store Store = db
			switch sc.HealthPath {
			case "missing":
				store = pathStore{db, filepath.Join(dir, "missing", database.DBFileName)}
			case "dir":
				d := filepath.Join(dir, "isdir", database.DBFileName)
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
				store = pathStore{db, d}
			}
			tasks := fakeTasks{ScraperTask: sc.Tasks[0], CheckerTask: sc.Tasks[1]}

			srv := httptest.NewServer(NewHandler(store, tasks))
			defer srv.Close()
			addr := strings.TrimPrefix(srv.URL, "http://")
			logs.Take()
			for _, c := range sc.Cases {
				got := apitest.Do(t, addr, c.Raw())
				apitest.Check(t, c, got, logs.Take())
				total++
			}
		})
	}
	if total < 20 {
		t.Errorf("solo %d casi golden", total)
	}
}
