package rateupdate

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

const testUserID = "123456789"

func newTestStore(t *testing.T) *database.Store {
	t.Helper()
	s, err := database.Open(filepath.Join(t.TempDir(), "test_octotracker.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// brokenStore restituisce uno store il cui database è chiuso (ogni
// operazione fallisce come con get_connection che solleva sqlite3.Error).
func brokenStore(t *testing.T) *database.Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return s
}

// rawDB apre una seconda connessione allo stesso file (database.get_connection
// nei test Python).
func rawDB(t *testing.T, s *database.Store) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", s.Path()+"?_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func fp(f float64) *float64 { return &f }

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

// callbackUpdate costruisce una callback come la decodifica gotgbot da
// Telegram: il messaggio della notifica è un gotgbot.Message (valore) con
// testo ed entità.
func callbackUpdate(data string, msg gotgbot.Message) *gotgbot.Update {
	u := testutil.CallbackUpdate(testutil.DefaultUserID, data)
	u.CallbackQuery.Message = msg
	return u
}

// notification è un messaggio di notifica con la domanda finale.
func notification(text string, entities ...gotgbot.MessageEntity) gotgbot.Message {
	return gotgbot.Message{
		MessageId: 42, Date: 1700000000,
		Chat:     gotgbot.Chat{Id: testutil.DefaultUserID, Type: "private"},
		Text:     text,
		Entities: entities,
	}
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

func toGeneric(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return generic(t, string(b))
}

func assertDeepEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Errorf("%s:\n got: %s\nwant: %s", what, gj, wj)
	}
}

// utilityDict replica il dict di una utility prodotto da _row_to_dict.
func utilityDict(u database.Utility) map[string]any {
	d := map[string]any{"tipo": u.Tipo, "fascia": u.Fascia, "energia": u.Energia, "commercializzazione": u.Commercializzazione}
	for k, v := range map[string]*float64{"consumo_f1": u.ConsumoF1, "consumo_f2": u.ConsumoF2, "consumo_f3": u.ConsumoF3, "consumo_annuo": u.ConsumoAnnuo} {
		if v != nil {
			d[k] = *v
		}
	}
	if u.Scadenza != "" {
		d["scadenza"] = u.Scadenza
	}
	return d
}

// userDict replica load_user: il dict dell'utente (nil se assente).
func userDict(t *testing.T, u *database.User) any {
	t.Helper()
	if u == nil {
		return nil
	}
	d := map[string]any{"luce": utilityDict(u.Luce)}
	if u.Gas != nil {
		d["gas"] = utilityDict(*u.Gas)
	}
	if len(u.LastNotifiedRates) > 0 {
		d["last_notified_rates"] = toGeneric(t, u.LastNotifiedRates)
	}
	return toGeneric(t, d)
}

// utilityFromDict converte il dict Python di una utility nel modello Go.
func utilityFromDict(m map[string]any) database.Utility {
	u := database.Utility{
		Tipo: m["tipo"].(string), Fascia: m["fascia"].(string),
		Energia: m["energia"].(float64), Commercializzazione: m["commercializzazione"].(float64),
	}
	ptr := func(k string) *float64 {
		if v, ok := m[k].(float64); ok {
			return &v
		}
		return nil
	}
	u.ConsumoF1, u.ConsumoF2, u.ConsumoF3, u.ConsumoAnnuo = ptr("consumo_f1"), ptr("consumo_f2"), ptr("consumo_f3"), ptr("consumo_annuo")
	if s, ok := m["scadenza"].(string); ok {
		u.Scadenza = s
	}
	return u
}

// userFromDict converte il dict Python di un utente nel modello Go.
func userFromDict(m map[string]any) database.User {
	u := database.User{Luce: utilityFromDict(m["luce"].(map[string]any))}
	if g, ok := m["gas"].(map[string]any); ok {
		gas := utilityFromDict(g)
		u.Gas = &gas
	}
	if l, ok := m["last_notified_rates"].(map[string]any); ok {
		u.LastNotifiedRates = l
	}
	return u
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

// fakeStore è uno Store programmabile (patch di apply_pending_rates nei
// test Python); le altre operazioni sono delegate allo store reale.
type fakeStore struct {
	*database.Store
	applyOK     bool
	applyReason string
}

func (f *fakeStore) ApplyPendingRates(string) (bool, string) { return f.applyOK, f.applyReason }
