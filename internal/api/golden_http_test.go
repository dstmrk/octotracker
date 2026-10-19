package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dstmrk/octotracker/internal/api/apitest"
	"github.com/dstmrk/octotracker/internal/database"
)

// httpGolden sono le risposte della versione Python (api/handlers.py) alle
// stesse richieste HTTP grezze, con DB e orologio fissati. Contiene solo il
// contratto: status, header CORS e Content-Type, corpo JSON.
type httpGolden struct {
	Token     string `json:"token"`
	Now       int64  `json:"now"`
	Scenarios []struct {
		Name  string         `json:"name"`
		SQL   []string       `json:"sql"`
		Cases []apitest.Case `json:"cases"`
	} `json:"scenarios"`
}

// setNow fissa l'ora corrente per la durata del test.
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
			mux := http.NewServeMux()
			New(newTestStore(t, sc.SQL), g.Token).Register(mux)
			for _, c := range sc.Cases {
				apitest.Check(t, mux, c)
				total++
			}
		})
	}
	if total < 80 {
		t.Errorf("solo %d casi golden", total)
	}
}
