package checker

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

// ========== Costruttori per i test ==========

func fp(v float64) *float64 { return &v }
func sp(v string) *string   { return &v }

// utility costruisce una fornitura senza consumi (dict con tipo, fascia,
// energia, commercializzazione).
func utility(tipo, fascia string, energia, comm float64) database.Utility {
	return database.Utility{Tipo: tipo, Fascia: fascia, Energia: energia, Commercializzazione: comm}
}

func gasPtr(u database.Utility) *database.Utility { return &u }

// rate costruisce una tariffa corrente {"energia": e, "commercializzazione": c}.
func rate(e, c float64) database.Rate {
	return database.Rate{Energia: e, Commercializzazione: fp(c)}
}

// rateCod costruisce una tariffa con codice offerta.
func rateCod(e, c float64, cod string) database.Rate {
	r := rate(e, c)
	r.CodOfferta = sp(cod)
	return r
}

// tariff costruisce {servizio: {tipo: {fascia: r}}}.
func tariff(servizio, tipo, fascia string, r database.Rate) database.CurrentRates {
	return database.CurrentRates{servizio: {tipo: {fascia: r}}}
}

// merge unisce più CurrentRates (come un dict letterale con più chiavi).
func merge(parts ...database.CurrentRates) database.CurrentRates {
	out := database.CurrentRates{}
	for _, p := range parts {
		for s, byTipo := range p {
			if out[s] == nil {
				out[s] = map[string]map[string]database.Rate{}
			}
			for tipo, byFascia := range byTipo {
				if out[s][tipo] == nil {
					out[s][tipo] = map[string]database.Rate{}
				}
				for f, r := range byFascia {
					out[s][tipo][f] = r
				}
			}
		}
	}
	return out
}

// ========== Cattura log ==========

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

// captureLogs installa un logger di default che scrive in memoria (come
// caplog di pytest) e lo ripristina a fine test.
func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logging.NewTextHandler(c, slog.LevelDebug)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
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

// ========== Store finto ==========

type savedUser struct {
	id   string
	user database.User
}

type savedPending struct {
	id      string
	pending database.PendingRates
}

// fakeStore sostituisce le funzioni database patchate nei test Python
// (checker.load_users, checker.get_current_rates, checker.save_user,
// checker.save_pending_rates, database.remove_user) e registra le chiamate.
// È sicuro per l'uso concorrente come *database.Store.
type fakeStore struct {
	users []*database.User
	rates database.CurrentRates

	mu      sync.Mutex
	saved   []savedUser
	pending []savedPending
	removed []string
}

func (f *fakeStore) LoadUsers() []*database.User            { return f.users }
func (f *fakeStore) GetCurrentRates() database.CurrentRates { return f.rates }

func (f *fakeStore) SaveUser(id string, u database.User) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, savedUser{id, u})
	return true
}

func (f *fakeStore) SavePendingRates(id string, p database.PendingRates) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = append(f.pending, savedPending{id, p})
	return true
}

func (f *fakeStore) RemoveUser(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return true
}

func (f *fakeStore) removedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
}

// userWith crea un utente con id.
func userWith(id string, luce database.Utility, gas *database.Utility) *database.User {
	return &database.User{ID: id, Luce: luce, Gas: gas}
}

// openTestDB apre un database SQLite nuovo in una directory temporanea.
func openTestDB(t *testing.T) *database.Store {
	t.Helper()
	s, err := database.Open(filepath.Join(t.TempDir(), "test_octotracker.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
