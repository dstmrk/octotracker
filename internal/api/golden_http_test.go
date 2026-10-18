package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dstmrk/octotracker/internal/api/apitest"
	"github.com/dstmrk/octotracker/internal/api/tornado"
	"github.com/dstmrk/octotracker/internal/database"
)

// httpGolden sono le risposte della vera app Tornado (api/handlers.py) alle
// stesse richieste HTTP grezze, con DB e orologio fissati.
type httpGolden struct {
	Token     string `json:"token"`
	Now       int64  `json:"now"`
	Scenarios []struct {
		Name  string         `json:"name"`
		SQL   []string       `json:"sql"`
		Cases []apitest.Case `json:"cases"`
	} `json:"scenarios"`
}

// setNow fissa int(time.time()) per la durata del test.
func setNow(t *testing.T, v int64) {
	t.Helper()
	prev := nowUnix
	nowUnix = func() int64 { return v }
	t.Cleanup(func() { nowUnix = prev })
}

// newTestStore apre un database vuoto in una directory temporanea e vi
// esegue le istruzioni SQL.
func newTestStore(t *testing.T, stmts []string) *database.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", database.DBFileName)
	store, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	apitest.ExecSQL(t, path, stmts)
	return store
}

func TestGoldenHTTP(t *testing.T) {
	data, err := os.ReadFile("testdata/http_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g httpGolden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	setNow(t, g.Now)
	total := 0
	for _, sc := range g.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			logs := apitest.CaptureLogs(t)
			store := newTestStore(t, sc.SQL)
			mux := http.NewServeMux()
			New(store, g.Token).Register(mux)
			mux.Handle("/", tornado.NotFoundHandler())
			srv := httptest.NewServer(mux)
			defer srv.Close()
			addr := strings.TrimPrefix(srv.URL, "http://")
			logs.Take() // log di apertura del DB
			for _, c := range sc.Cases {
				got := apitest.Do(t, addr, c.Raw())
				apitest.Check(t, c, got, logs.Take())
				total++
			}
		})
	}
	if total < 100 {
		t.Errorf("solo %d casi golden", total)
	}
}
