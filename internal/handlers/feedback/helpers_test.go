package feedback

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

const testUserID = "123456789"

// newTestStore apre un database temporaneo isolato (fixture temp_database).
func newTestStore(t *testing.T) *database.Store {
	t.Helper()
	s, err := database.Open(filepath.Join(t.TempDir(), "test_octotracker.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// rawDB apre una seconda connessione allo stesso file (database.get_connection
// nei test Python) per leggere o forzare valori grezzi.
func rawDB(t *testing.T, s *database.Store) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", s.Path()+"?_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func setLastFeedback(t *testing.T, s *database.Store, userID string, value any) {
	t.Helper()
	if _, err := rawDB(t, s).Exec("UPDATE users SET last_feedback_at = ? WHERE user_id = ?", value, userID); err != nil {
		t.Fatal(err)
	}
}

// sampleUser è l'utente creato dai test Python prima di salvare feedback.
func sampleUser() database.User {
	return database.User{Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0}}
}

func saveSampleUser(t *testing.T, s *database.Store, userID string) {
	t.Helper()
	if !s.SaveUser(userID, sampleUser()) {
		t.Fatal("SaveUser fallito")
	}
}

func ip(i int) *int       { return &i }
func sp(s string) *string { return &s }

// setNow fissa l'orologio (datetime.now()) per il test.
func setNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := dateutil.Now
	dateutil.Now = func() time.Time { return now }
	t.Cleanup(func() { dateutil.Now = prev })
}

// result è l'esito di un handler.
type result struct {
	state     string // "END", "" (None) o il nome dello stato
	err       error
	calls     []testutil.Call
	userAfter map[string]any
}

func call(t *testing.T, fc *testutil.FakeClient, ud *tg.UserData, h handlers.Response, b *gotgbot.Bot, ctx *ext.Context) result {
	t.Helper()
	fc.Reset()
	err := h(b, ctx)
	res := result{calls: fc.Calls()}
	if ctx.EffectiveUser != nil {
		res.userAfter = ud.Snapshot(ctx.EffectiveUser.Id)
	}
	var sc *handlers.ConversationStateChange
	switch {
	case err == nil:
	case errors.As(err, &sc) && sc.End:
		res.state = "END"
	case errors.As(err, &sc) && sc.NextState != nil:
		res.state = *sc.NextState
	default:
		res.err = err
	}
	return res
}

// callbackContext costruisce un update callback_query come lo decodifica
// gotgbot da Telegram (CallbackQuery.Message di tipo valore gotgbot.Message).
func callbackContext(b *gotgbot.Bot, data string) *ext.Context {
	u := testutil.CallbackUpdate(testutil.DefaultUserID, data)
	u.CallbackQuery.Message = *u.CallbackQuery.Message.(*gotgbot.Message)
	return ext.NewContext(b, u, nil)
}

func generic(t *testing.T, raw string) any {
	t.Helper()
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("json %q: %v", raw, err)
	}
	return v
}

func assertDeepEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Errorf("%s:\n got: %s\nwant: %s", what, gj, wj)
	}
}

// logCapture cattura i log (formato Python) durante un test.
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

func (c *logCapture) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Reset()
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logging.NewTextHandler(c, slog.LevelDebug)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

// fakeStore è uno Store programmabile (monkeypatch di get_last_feedback_time
// e save_feedback nei test Python).
type fakeStore struct {
	last  *string
	save  bool
	saved []savedFeedback
}

type savedFeedback struct {
	userID, feedbackType string
	rating               *int
	comment              *string
}

func (f *fakeStore) GetLastFeedbackTime(string) *string { return f.last }

func (f *fakeStore) SaveFeedback(userID, feedbackType string, rating *int, comment *string) bool {
	f.saved = append(f.saved, savedFeedback{userID, feedbackType, rating, comment})
	return f.save
}

func (s savedFeedback) String() string {
	return fmt.Sprintf("%s/%s/%v/%v", s.userID, s.feedbackType, deref(s.rating), deref(s.comment))
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// editedMessageContext costruisce un update edited_message (update.message None).
func editedMessageContext(b *gotgbot.Bot, text string) *ext.Context {
	u := testutil.MessageUpdate(testutil.DefaultUserID, text)
	u.EditedMessage, u.Message = u.Message, nil
	return ext.NewContext(b, u, nil)
}
