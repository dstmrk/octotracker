package rateupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Test differenziali contro i file in testdata/, generati eseguendo il
// codice Python reale:
//   - texthtml_golden.json: Message.text_html di python-telegram-bot 22.6
//     (Message.de_json dello stesso JSON) su messaggi realistici e su un
//     corpus casuale di entità annidate, anche di tipo sconosciuto, come
//     può inviarle Telegram;
//   - handlers_golden.json: scenari a più passi degli handler di
//     handlers/rate_update.py con database reale (chiamate API in ordine,
//     stato, eccezioni, user_data, utente e pending dopo ogni passo, log).

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func TestGoldenTextHTML(t *testing.T) {
	var cases []struct {
		Text      *string         `json:"text"`
		Entities  json.RawMessage `json:"entities"`
		HTML      *string         `json:"html"`
		Exception string          `json:"exception"`
	}
	readJSON(t, "texthtml_golden.json", &cases)
	if len(cases) < 500 {
		t.Fatalf("corpus troppo piccolo: %d", len(cases))
	}
	for i, c := range cases {
		// Stesso JSON del messaggio passato a Message.de_json dal generatore
		raw := map[string]any{"message_id": 42, "date": 1700000000, "chat": map[string]any{"id": testutil.DefaultUserID, "type": "private"}}
		if c.Text != nil {
			raw["text"] = *c.Text
		}
		if len(c.Entities) > 0 {
			raw["entities"] = c.Entities
		}
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var msg gotgbot.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		got, err := textHTML(&msg)
		if c.Exception != "" {
			// Messaggio senza testo: errore (il testo non conta)
			if err == nil {
				t.Errorf("case %d %s: got %q, want errore (%s)", i, data, got, c.Exception)
			}
			continue
		}
		if err != nil || got != *c.HTML {
			t.Errorf("case %d %s:\n got: %q err=%v\nwant: %q", i, data, got, err, *c.HTML)
		}
	}
}

type goldenStep struct {
	Kind    string          `json:"kind"`
	Handler string          `json:"handler"`
	Data    string          `json:"data"`
	Text    *string         `json:"text"`
	Message json.RawMessage `json:"message"`
}

type goldenCall struct {
	Method      string `json:"method"`
	Text        string `json:"text"`
	ParseMode   any    `json:"parse_mode"`
	ReplyMarkup any    `json:"reply_markup"`
}

type goldenStepOut struct {
	Result        *string        `json:"result"`
	Exception     string         `json:"exception"`
	Calls         []goldenCall   `json:"calls"`
	UserDataAfter map[string]any `json:"user_data_after"`
	UserAfter     any            `json:"user_after"`
	PendingAfter  any            `json:"pending_after"`
	Logs          []string       `json:"logs"`
}

// goldenHandler associa il nome del passo all'handler.
var goldenHandler = map[string]func(*Handlers) handlers.Response{
	"yes": rateUpdateYes, "no": rateUpdateNo, "choice": scadenzaUpdateChoice,
	"date": updateScadenzaDate, "skip": skipScadenzaDate,
}

type goldenScenario struct {
	Name  string `json:"name"`
	Setup struct {
		User     map[string]any  `json:"user"`
		Pending  json.RawMessage `json:"pending"`
		UserData map[string]any  `json:"user_data"`
	} `json:"setup"`
	Steps []goldenStep    `json:"steps"`
	Out   []goldenStepOut `json:"out"`
}

var pyMethod = map[string]string{
	"sendMessage":         "reply_text",
	"editMessageText":     "edit_message_text",
	"answerCallbackQuery": "answer",
}

func TestGoldenHandlers(t *testing.T) {
	var scenarios []goldenScenario
	readJSON(t, "handlers_golden.json", &scenarios)
	if len(scenarios) < 25 {
		t.Fatalf("scenari: %d", len(scenarios))
	}
	logs := captureLogs(t)
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) { runScenario(t, sc, logs) })
	}
}

func runScenario(t *testing.T, sc goldenScenario, logs *logCapture) {
	s := newTestStore(t)
	if sc.Setup.User != nil && !s.SaveUser(testUserID, userFromDict(sc.Setup.User)) {
		t.Fatal("SaveUser fallito")
	}
	if len(sc.Setup.Pending) > 0 && string(sc.Setup.Pending) != "null" {
		var p database.PendingRates
		if err := json.Unmarshal(sc.Setup.Pending, &p); err != nil {
			t.Fatal(err)
		}
		s.SavePendingRates(testUserID, p)
	}
	h := &Handlers{Store: s, UserData: tg.NewUserData()}
	for k, v := range sc.Setup.UserData {
		if list, isList := v.([]any); isList && k == servicesKey {
			v = toStrings(list)
		}
		h.UserData.Set(testutil.DefaultUserID, k, v)
	}

	for i, step := range sc.Steps {
		b, fc := testutil.NewBot(t)
		logs.Reset()
		res := call(t, fc, h.UserData, goldenHandler[step.Handler](h), b, stepContext(t, b, step))
		prefix := fmt.Sprintf("passo %d (%s)", i, step.Handler)
		want := sc.Out[i]
		checkStepOutcome(t, prefix, want, res)
		checkStepCalls(t, prefix, want.Calls, res.calls)
		assertDeepEqual(t, prefix+" user_data", toGeneric(t, res.userAfter), toGeneric(t, want.UserDataAfter))
		assertDeepEqual(t, prefix+" utente", userDict(t, s.LoadUser(testUserID)), want.UserAfter)
		var gotPending any
		if p := s.LoadPendingRates(testUserID); p != nil {
			gotPending = toGeneric(t, p)
		}
		assertDeepEqual(t, prefix+" pending", gotPending, want.PendingAfter)
		checkStepLogs(t, prefix, want.Logs, logs.String())
	}
}

func toStrings(list []any) []string {
	out := make([]string, len(list))
	for i, x := range list {
		out[i] = x.(string)
	}
	return out
}

// stepContext costruisce l'update di un passo: callback sul messaggio della
// notifica o messaggio di testo.
func stepContext(t *testing.T, b *gotgbot.Bot, step goldenStep) *ext.Context {
	t.Helper()
	if step.Kind == "callback" {
		var msg gotgbot.Message
		if err := json.Unmarshal(step.Message, &msg); err != nil {
			t.Fatal(err)
		}
		return ext.NewContext(b, callbackUpdate(step.Data, msg), nil)
	}
	text := ""
	if step.Text != nil {
		text = *step.Text
	}
	return testutil.MessageContext(b, testutil.DefaultUserID, text)
}

// checkStepOutcome confronta lo stato restituito; un'eccezione Python
// corrisponde a un errore qualsiasi (il testo non conta).
func checkStepOutcome(t *testing.T, prefix string, want goldenStepOut, res result) {
	t.Helper()
	if want.Exception != "" {
		if res.err == nil {
			t.Errorf("%s: atteso un errore (%s), got stato %q", prefix, want.Exception, res.state)
		}
		return
	}
	if res.err != nil {
		t.Fatalf("%s: errore inatteso %v", prefix, res.err)
	}
	wantState := ""
	if want.Result != nil {
		wantState = *want.Result
	}
	if res.state != wantState {
		t.Errorf("%s: stato got %q, want %q", prefix, res.state, wantState)
	}
}

// checkStepCalls confronta le chiamate API con quelle Python.
func checkStepCalls(t *testing.T, prefix string, want []goldenCall, got []testutil.Call) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: chiamate got %+v, want %+v", prefix, got, want)
	}
	for j, wc := range want {
		g := got[j]
		if pyMethod[g.Method] != wc.Method {
			t.Errorf("%s: chiamata %d %s, want %s", prefix, j, g.Method, wc.Method)
			continue
		}
		if wc.Method == "answer" {
			continue
		}
		wantPM, _ := wc.ParseMode.(string)
		if g.Text() != wc.Text || g.ParseMode() != wantPM || g.ChatID() != testUserID {
			t.Errorf("%s: chiamata %d\n got: %s %q %q\nwant: %s %q %q", prefix, j, g.ChatID(), g.ParseMode(), g.Text(), testUserID, wantPM, wc.Text)
		}
		gotMarkup := generic(t, g.JSON("reply_markup"))
		if m, isMap := gotMarkup.(map[string]any); isMap && len(m) == 0 && wc.Method == "edit_message_text" {
			gotMarkup = nil // gotgbot invia sempre reply_markup: {} equivale all'assenza
		}
		assertDeepEqual(t, prefix+" reply_markup", gotMarkup, wc.ReplyMarkup)
		if wc.Method == "edit_message_text" && g.String("message_id") != "42" {
			t.Errorf("%s: message_id %s", prefix, g.String("message_id"))
		}
	}
}

// checkStepLogs verifica i log dell'handler ("LIVELLO logger messaggio").
func checkStepLogs(t *testing.T, prefix string, want []string, out string) {
	t.Helper()
	for _, l := range want {
		level, rest, _ := strings.Cut(l, " ")
		name, msg, _ := strings.Cut(rest, " ")
		if line := " - " + name + " - " + level + " - " + msg + "\n"; !strings.Contains(out, line) {
			t.Errorf("%s: log mancante %q in:\n%s", prefix, line, out)
		}
	}
	if n := strings.Count(out, " - handlers.rate_update - "); n != len(want) {
		t.Errorf("%s: %d log di handlers.rate_update, want %d:\n%s", prefix, n, len(want), out)
	}
}
