package database

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dstmrk/octotracker/internal/logging"
)

// newTestStore apre un database temporaneo isolato (fixture temp_db / temp_database).
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test_octotracker.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// brokenStore restituisce uno store il cui database è chiuso: ogni operazione
// fallisce con un errore database (equivalente Go di get_connection che
// solleva sqlite3.Error nei test Python).
func brokenStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return s
}

func fp(f float64) *float64 { return &f }
func sp(s string) *string   { return &s }
func ip(i int) *int         { return &i }

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// logCapture cattura i log del package (formato Python) durante un test.
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

// captureLogs installa un logger di default a livello DEBUG che scrive su un
// buffer e lo ripristina a fine test.
func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logging.NewTextHandler(c, slog.LevelDebug)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

func assertLogContains(t *testing.T, logs *logCapture, want ...string) {
	t.Helper()
	out := logs.String()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("log non contiene %q; log:\n%s", w, out)
		}
	}
}

// toGeneric serializza v con encoding/json e lo decodifica in valori
// generici, per confrontare strutture Go con i dict Python in JSON.
func toGeneric(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", v, err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return out
}

func jsonString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

// rawColumn legge il valore grezzo di una colonna users (nil se NULL).
func rawColumn(t *testing.T, s *Store, userID, column string) any {
	t.Helper()
	var v any
	if err := s.db.QueryRow("SELECT CAST("+column+" AS TEXT) FROM users WHERE user_id = ?", userID).Scan(&v); err != nil {
		t.Fatalf("rawColumn %s: %v", column, err)
	}
	return v
}

func userColumns(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	cols, err := s.userColumns(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return cols
}

// assertSameBlob confronta un blob JSON letto dal DB con quello atteso per
// valore (stesse chiavi e valori, formattazione libera). want nil indica
// una colonna NULL.
func assertSameBlob(t *testing.T, what string, got, want any) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Errorf("%s: got %v, want %v", what, got, want)
		}
		return
	}
	var g, w any
	if err := json.Unmarshal([]byte(got.(string)), &g); err != nil {
		t.Fatalf("%s: blob non valido %v: %v", what, got, err)
	}
	if err := json.Unmarshal([]byte(want.(string)), &w); err != nil {
		t.Fatalf("%s: blob atteso non valido %v: %v", what, want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s:\n got  %v\n want %v", what, got, want)
	}
}
