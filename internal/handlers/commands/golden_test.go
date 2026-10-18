package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// Test differenziali: testdata/commands_golden.json è generato eseguendo i
// veri handler Python di handlers/commands.py (python-telegram-bot 22.6) con
// update e context finti su un corpus di utenti e configurazioni. Per ogni
// caso si confrontano testo, parse_mode e reply_markup di ogni reply_text,
// lo stato restituito, l'eventuale eccezione e user_data dopo la chiamata.

type goldenReply struct {
	Text        string `json:"text"`
	ParseMode   any    `json:"parse_mode"`
	ReplyMarkup any    `json:"reply_markup"`
}

type goldenOut struct {
	Result          any            `json:"result"`
	Exception       string         `json:"exception"`
	Calls           []goldenReply  `json:"calls"`
	UserDataAfter   map[string]any `json:"user_data_after"`
	UserExistsAfter *bool          `json:"user_exists_after"`
}

type goldenCase struct {
	User        map[string]any `json:"user"`
	WebAppURL   string         `json:"webapp_url"`
	CheckerHour int            `json:"checker_hour"`
	Channel     string         `json:"channel"`
	Out         goldenOut      `json:"out"`
}

type goldenFile struct {
	Status  []goldenCase `json:"status"`
	History []goldenCase `json:"history"`
	Remove  []goldenCase `json:"remove"`
	Help    []goldenCase `json:"help"`
	Cancel  []goldenCase `json:"cancel"`
	Unknown []goldenCase `json:"unknown"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/commands_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

// utilityFromGolden converte il dict Python di una utility nel modello Go.
func utilityFromGolden(m map[string]any) database.Utility {
	u := database.Utility{
		Tipo:                m["tipo"].(string),
		Fascia:              m["fascia"].(string),
		Energia:             m["energia"].(float64),
		Commercializzazione: m["commercializzazione"].(float64),
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

// userFromGolden converte il dict Python di un utente nel modello Go.
func userFromGolden(m map[string]any) database.User {
	u := database.User{Luce: utilityFromGolden(m["luce"].(map[string]any))}
	if g, ok := m["gas"].(map[string]any); ok {
		gas := utilityFromGolden(g)
		u.Gas = &gas
	}
	return u
}

// runGolden esegue un caso: prepara DB e user_data come il generatore
// Python ({"luce_energia": 0.145, "x": "y"} salvo "cancel" con {}), esegue
// l'handler scelto e confronta l'esito con l'output Python.
func runGolden(t *testing.T, h *Handlers, s *database.Store, c goldenCase, name string, emptyUserData bool) {
	t.Helper()
	b, fc := testutil.NewBot(t)
	s.RemoveUser(testUserID)
	if c.User != nil {
		if !s.SaveUser(testUserID, userFromGolden(c.User)) {
			t.Fatalf("SaveUser fallito per %v", c.User)
		}
	}
	h.UserData.Clear(testutil.DefaultUserID)
	if !emptyUserData {
		h.UserData.Set(testutil.DefaultUserID, "luce_energia", 0.145)
		h.UserData.Set(testutil.DefaultUserID, "x", "y")
	}

	handler := map[string]func() result{
		"status": func() result {
			return call(t, fc, h.UserData, h.Status, b, testutil.MessageContext(b, testutil.DefaultUserID, "/status"))
		},
		"history": func() result {
			return call(t, fc, h.UserData, h.HistoryCommand, b, testutil.MessageContext(b, testutil.DefaultUserID, "/history"))
		},
		"remove": func() result {
			return call(t, fc, h.UserData, h.RemoveData, b, testutil.MessageContext(b, testutil.DefaultUserID, "/remove"))
		},
		"help": func() result {
			return call(t, fc, h.UserData, h.HelpCommand, b, testutil.MessageContext(b, testutil.DefaultUserID, "/help"))
		},
		"cancel": func() result {
			return call(t, fc, h.UserData, h.CancelConversation, b, testutil.MessageContext(b, testutil.DefaultUserID, "/cancel"))
		},
		"unknown": func() result {
			return call(t, fc, h.UserData, h.UnknownCommand, b, testutil.MessageContext(b, testutil.DefaultUserID, "/unknown"))
		},
	}[name]
	res := handler()

	// Stato restituito / eccezione
	if c.Out.Exception != "" {
		if res.panicked != c.Out.Exception {
			t.Errorf("eccezione: got %q, want %q", res.panicked, c.Out.Exception)
		}
	} else {
		if res.err != nil || res.panicked != "" {
			t.Fatalf("errore inatteso: %v / %q", res.err, res.panicked)
		}
		wantState := ""
		if c.Out.Result != nil {
			wantState = fmt.Sprint(c.Out.Result)
		}
		if res.state != wantState {
			t.Errorf("stato: got %q, want %q", res.state, wantState)
		}
	}

	// Chiamate reply_text
	if len(res.calls) != len(c.Out.Calls) {
		t.Fatalf("chiamate: got %d %+v, want %d", len(res.calls), res.calls, len(c.Out.Calls))
	}
	for i, want := range c.Out.Calls {
		got := res.calls[i]
		if got.Method != "sendMessage" {
			t.Errorf("chiamata %d: metodo %s", i, got.Method)
		}
		if got.Text() != want.Text {
			t.Errorf("testo %d:\n got: %q\nwant: %q", i, got.Text(), want.Text)
		}
		wantPM := ""
		if want.ParseMode != nil {
			wantPM = want.ParseMode.(string)
		}
		if got.ParseMode() != wantPM {
			t.Errorf("parse_mode %d: got %q, want %q", i, got.ParseMode(), wantPM)
		}
		assertDeepEqual(t, "reply_markup", generic(t, got.JSON("reply_markup")), want.ReplyMarkup)
		if got.ChatID() != testUserID {
			t.Errorf("chat_id: %s", got.ChatID())
		}
		if _, quoted := got.Params["reply_parameters"]; quoted {
			t.Errorf("reply_text non deve citare il messaggio")
		}
	}

	// user_data dopo la chiamata
	wantUD := c.Out.UserDataAfter
	if wantUD == nil {
		wantUD = map[string]any{}
	}
	assertDeepEqual(t, "user_data", res.userAfter, wantUD)

	if c.Out.UserExistsAfter != nil && s.UserExists(testUserID) != *c.Out.UserExistsAfter {
		t.Errorf("user_exists dopo: got %v, want %v", s.UserExists(testUserID), *c.Out.UserExistsAfter)
	}
}

func TestGoldenStatus(t *testing.T) {
	g := loadGolden(t)
	h, s := newHandlers(t)
	if len(g.Status) < 300 {
		t.Fatalf("corpus status troppo piccolo: %d", len(g.Status))
	}
	for i, c := range g.Status {
		t.Run(fmt.Sprintf("case%03d", i), func(t *testing.T) { runGolden(t, h, s, c, "status", false) })
	}
}

func TestGoldenHistory(t *testing.T) {
	g := loadGolden(t)
	h, s := newHandlers(t)
	for i, c := range g.History {
		h.WebAppURL = c.WebAppURL
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) { runGolden(t, h, s, c, "history", false) })
	}
}

func TestGoldenRemove(t *testing.T) {
	g := loadGolden(t)
	h, s := newHandlers(t)
	for i, c := range g.Remove {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) { runGolden(t, h, s, c, "remove", false) })
	}
}

func TestGoldenHelp(t *testing.T) {
	g := loadGolden(t)
	h, s := newHandlers(t)
	for i, c := range g.Help {
		h.CheckerHour, h.TelegramChannel = c.CheckerHour, c.Channel
		t.Run(fmt.Sprintf("case%02d", i), func(t *testing.T) { runGolden(t, h, s, c, "help", false) })
	}
}

func TestGoldenCancelAndUnknown(t *testing.T) {
	g := loadGolden(t)
	h, s := newHandlers(t)
	if len(g.Cancel) != 2 || len(g.Unknown) != 1 {
		t.Fatalf("golden inattesi: %d %d", len(g.Cancel), len(g.Unknown))
	}
	runGolden(t, h, s, g.Cancel[0], "cancel", false)
	runGolden(t, h, s, g.Cancel[1], "cancel", true)
	runGolden(t, h, s, g.Unknown[0], "unknown", false)
}
