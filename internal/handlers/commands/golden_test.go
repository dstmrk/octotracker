package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// Test differenziali: testdata/commands_golden.json è generato eseguendo i
// veri handler Python di handlers/commands.py (python-telegram-bot 22.6) con
// update e context finti su un corpus di utenti e configurazioni. Per ogni
// caso si confrontano testo, parse_mode e reply_markup di ogni reply_text,
// lo stato restituito, l'eventuale eccezione (qui un panic del formatter,
// di cui non conta il testo) e user_data dopo la chiamata.

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

// goldenHandlers associa il nome del caso al comando e al testo inviato.
var goldenHandlers = map[string]struct {
	text    string
	handler func(*Handlers) handlers.Response
}{
	"status":  {"/status", status},
	"history": {"/history", historyCommand},
	"remove":  {"/remove", removeData},
	"help":    {"/help", helpCommand},
	"cancel":  {"/cancel", cancelConv},
	"unknown": {"/unknown", unknownCommand},
}

// runGolden esegue un caso: prepara DB e user_data come il generatore
// Python ({"luce_energia": 0.145, "x": "y"} salvo "cancel" con {}), esegue
// l'handler scelto e confronta l'esito con l'output Python.
func runGolden(t *testing.T, h *Handlers, s *database.Store, c goldenCase, name string, emptyUserData bool) {
	t.Helper()
	b, fc := testutil.NewBot(t)
	s.RemoveUser(testUserID)
	if c.User != nil && !s.SaveUser(testUserID, userFromGolden(c.User)) {
		t.Fatalf("SaveUser fallito per %v", c.User)
	}
	h.UserData.Clear(testutil.DefaultUserID)
	if !emptyUserData {
		h.UserData.Set(testutil.DefaultUserID, "luce_energia", 0.145)
		h.UserData.Set(testutil.DefaultUserID, "x", "y")
	}

	gh := goldenHandlers[name]
	res := call(t, fc, h.UserData, gh.handler(h), b, testutil.MessageContext(b, testutil.DefaultUserID, gh.text))
	checkGoldenOutcome(t, c.Out, res)
	checkGoldenCalls(t, c.Out.Calls, res.calls)

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

// checkGoldenOutcome confronta lo stato restituito; un'eccezione Python
// corrisponde a un panic del formatter (il testo non conta).
func checkGoldenOutcome(t *testing.T, want goldenOut, res result) {
	t.Helper()
	if want.Exception != "" {
		if res.panicked == "" {
			t.Errorf("atteso un panic (%s), got state=%q err=%v", want.Exception, res.state, res.err)
		}
		return
	}
	if res.err != nil || res.panicked != "" {
		t.Fatalf("errore inatteso: %v / %q", res.err, res.panicked)
	}
	wantState := ""
	if want.Result != nil {
		wantState = fmt.Sprint(want.Result)
	}
	if res.state != wantState {
		t.Errorf("stato: got %q, want %q", res.state, wantState)
	}
}

// checkGoldenCalls confronta le chiamate reply_text con quelle Python.
func checkGoldenCalls(t *testing.T, want []goldenReply, got []testutil.Call) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("chiamate: got %d %+v, want %d", len(got), got, len(want))
	}
	for i, w := range want {
		g := got[i]
		wantPM, _ := w.ParseMode.(string)
		if g.Method != "sendMessage" || g.Text() != w.Text || g.ParseMode() != wantPM || g.ChatID() != testUserID {
			t.Errorf("chiamata %d:\n got: %s %s %q %q\nwant: sendMessage %s %q %q", i, g.Method, g.ChatID(), g.ParseMode(), g.Text(), testUserID, wantPM, w.Text)
		}
		assertDeepEqual(t, "reply_markup", generic(t, g.JSON("reply_markup")), w.ReplyMarkup)
		if _, quoted := g.Params["reply_parameters"]; quoted {
			t.Errorf("reply_text non deve citare il messaggio")
		}
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
