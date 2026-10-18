package app

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

// syncBuffer è un buffer sicuro per scritture e letture concorrenti (i log
// arrivano anche dalle goroutine dei task).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs installa un logger di default che scrive in un buffer (come
// caplog di pytest) e lo ripristina alla fine del test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logging.NewTextHandler(buf, slog.LevelDebug)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// assertLogContains verifica che il log contenga want.
func assertLogContains(t *testing.T, logs *syncBuffer, want string) {
	t.Helper()
	if !strings.Contains(logs.String(), want) {
		t.Errorf("log senza %q:\n%s", want, logs.String())
	}
}

// assertLogNotContains verifica che il log non contenga unwanted.
func assertLogNotContains(t *testing.T, logs *syncBuffer, unwanted string) {
	t.Helper()
	if strings.Contains(logs.String(), unwanted) {
		t.Errorf("log con %q inatteso:\n%s", unwanted, logs.String())
	}
}

// newTestStore apre un database temporaneo.
func newTestStore(t *testing.T) *database.Store {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), database.DBFileName))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func fp(v float64) *float64 { return &v }

func sp(v string) *string { return &v }
