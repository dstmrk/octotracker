package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

// newHandlers crea gli handler con uno store temporaneo e la
// configurazione di default (CHECKER_HOUR=10, nessuna Mini App né canale).
func newHandlers(t *testing.T) (*Handlers, *database.Store) {
	t.Helper()
	s := newTestStore(t)
	return &Handlers{Store: s, UserData: tg.NewUserData(), CheckerHour: 10}, s
}

func fp(f float64) *float64 { return &f }

// luceFissaMono è l'utente di esempio dei test Python (sample user_data).
func luceFissaMono(energia, comm float64) database.User {
	return database.User{Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: energia, Commercializzazione: comm}}
}

// result è l'esito di un handler: stato restituito, eccezione e chiamate API.
type result struct {
	state     string // "END", "" (None) o il nome dello stato
	err       error  // errore non di stato (eccezione Python)
	panicked  string // valore del panic (eccezione Python da formatter)
	calls     []testutil.Call
	userAfter map[string]any
}

// call esegue l'handler h su ctx recuperando un eventuale panic.
func call(t *testing.T, fc *testutil.FakeClient, ud *tg.UserData, h handlers.Response, b *gotgbot.Bot, ctx *ext.Context) (res result) {
	t.Helper()
	fc.Reset()
	defer func() {
		res.calls = fc.Calls()
		if ctx.EffectiveUser != nil {
			res.userAfter = ud.Snapshot(ctx.EffectiveUser.Id)
		}
		if r := recover(); r != nil {
			res.panicked = fmt.Sprint(r)
		}
	}()
	err := h(b, ctx)
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

// assertEnd verifica che l'handler abbia restituito ConversationHandler.END.
func assertEnd(t *testing.T, res result) {
	t.Helper()
	if res.err != nil || res.panicked != "" || res.state != "END" {
		t.Fatalf("atteso END, got state=%q err=%v panic=%q", res.state, res.err, res.panicked)
	}
}

// onlySend restituisce l'unica chiamata sendMessage (reply_text.assert_called_once).
func onlySend(t *testing.T, res result) testutil.Call {
	t.Helper()
	var sends []testutil.Call
	for _, c := range res.calls {
		if c.Method == "sendMessage" {
			sends = append(sends, c)
		}
	}
	if len(sends) != 1 || len(res.calls) != 1 {
		t.Fatalf("attesa una sola sendMessage, chiamate: %+v", res.calls)
	}
	return sends[0]
}

// generic decodifica JSON in valori generici per confronti strutturali.
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
