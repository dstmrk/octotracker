package reminder

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/logging"
)

// TODAY replica TODAY = date(2025, 1, 1) dei test Python.
var TODAY = dateutil.NewDate(2025, time.January, 1)

func fp(v float64) *float64 { return &v }
func sp(v string) *string   { return &v }
func dp(t time.Time) *time.Time {
	return &t
}

// fissaUser replica _fissa_user(scadenza=None, gas=None).
func fissaUser(scadenza string, gas *database.Utility) database.User {
	return database.User{
		Luce: database.Utility{
			Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0,
			Scadenza: scadenza,
		},
		Gas: gas,
	}
}

func fissaGas(scadenza string) *database.Utility {
	return &database.Utility{
		Tipo: "fissa", Fascia: "monoraria", Energia: 0.456, Commercializzazione: 84.0, Scadenza: scadenza,
	}
}

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%q non contiene %q", s, sub)
	}
}

func assertNotContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("%q non dovrebbe contenere %q", s, sub)
	}
}

// ========== Cattura log (caplog) ==========

type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logging.NewTextHandler(c, slog.LevelDebug)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

// stripLogPrefix rimuove "data - nome - " dalle righe del logger indicato.
func stripLogPrefix(s, name string) string {
	var out strings.Builder
	sep := " - " + name + " - "
	for _, line := range strings.SplitAfter(s, "\n") {
		if i := strings.Index(line, sep); i >= 0 {
			out.WriteString(line[i+len(sep):])
		}
	}
	return out.String()
}

// fixedClock rende deterministici i tempi nei log ("completato in 0.00s").
func fixedClock(t *testing.T) {
	t.Helper()
	prev := now
	base := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	now = func() time.Time { return base }
	t.Cleanup(func() { now = prev })
}

// openTestDB apre un database nuovo per test (fixture temp_database di conftest.py).
func openTestDB(t *testing.T) *database.Store {
	t.Helper()
	s, err := database.Open(filepath.Join(t.TempDir(), "test_octotracker.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fakeStore è uno Store in memoria per i casi non rappresentabili nel DB.
type fakeStore struct {
	users    []*database.User
	rates    database.CurrentRates
	reminded map[string]database.ScadenzaReminded

	mu      sync.Mutex
	marked  []string
	removed []string
}

func (f *fakeStore) LoadUsers() []*database.User            { return f.users }
func (f *fakeStore) GetCurrentRates() database.CurrentRates { return f.rates }
func (f *fakeStore) GetScadenzaRemindedMap() map[string]database.ScadenzaReminded {
	if f.reminded == nil {
		return map[string]database.ScadenzaReminded{}
	}
	return f.reminded
}

func (f *fakeStore) MarkScadenzaReminded(id, servizio, scadenza string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marked = append(f.marked, id+"/"+servizio+"/"+scadenza)
	return true
}

func (f *fakeStore) RemoveUser(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return true
}
