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
//     corpus casuale di entità (annidate, sovrapposte, duplicate, di tipo
//     sconosciuto, fuori dal testo, a metà di una coppia surrogata);
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
	if len(cases) < 2000 {
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
			if err == nil || err.Error() != c.Exception {
				t.Errorf("case %d %s:\n got: %q err=%v\nwant: %s", i, data, got, err, c.Exception)
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
	if sc.Setup.User != nil {
		if !s.SaveUser(testUserID, userFromDict(sc.Setup.User)) {
			t.Fatal("SaveUser fallito")
		}
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
			services := make([]string, len(list))
			for i, x := range list {
				services[i] = x.(string)
			}
			h.UserData.Set(testutil.DefaultUserID, k, services)
			continue
		}
		h.UserData.Set(testutil.DefaultUserID, k, v)
	}

	for i, step := range sc.Steps {
		want := sc.Out[i]
		b, fc := testutil.NewBot(t)
		var ctx *ext.Context
		if step.Kind == "callback" {
			var msg gotgbot.Message
			if err := json.Unmarshal(step.Message, &msg); err != nil {
				t.Fatal(err)
			}
			ctx = ext.NewContext(b, callbackUpdate(step.Data, msg), nil)
		} else {
			text := ""
			if step.Text != nil {
				text = *step.Text
			}
			ctx = testutil.MessageContext(b, testutil.DefaultUserID, text)
		}
		handler := map[string]handlers.Response{
			"yes": h.RateUpdateYes, "no": h.RateUpdateNo, "choice": h.ScadenzaUpdateChoice,
			"date": h.UpdateScadenzaDate, "skip": h.SkipScadenzaDate,
		}[step.Handler]

		logs.Reset()
		res := call(t, fc, h.UserData, handler, b, ctx)
		prefix := fmt.Sprintf("passo %d (%s)", i, step.Handler)

		if want.Exception != "" {
			if res.err == nil || res.err.Error() != want.Exception {
				t.Errorf("%s: eccezione got %v, want %q", prefix, res.err, want.Exception)
			}
		} else {
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

		if len(res.calls) != len(want.Calls) {
			t.Fatalf("%s: chiamate got %+v, want %+v", prefix, res.calls, want.Calls)
		}
		for j, wc := range want.Calls {
			got := res.calls[j]
			if pyMethod[got.Method] != wc.Method {
				t.Errorf("%s: chiamata %d %s, want %s", prefix, j, got.Method, wc.Method)
				continue
			}
			if wc.Method == "answer" {
				continue
			}
			if got.Text() != wc.Text {
				t.Errorf("%s: testo %d\n got: %q\nwant: %q", prefix, j, got.Text(), wc.Text)
			}
			wantPM := ""
			if wc.ParseMode != nil {
				wantPM = wc.ParseMode.(string)
			}
			if got.ParseMode() != wantPM {
				t.Errorf("%s: parse_mode %d got %q, want %q", prefix, j, got.ParseMode(), wantPM)
			}
			gotMarkup := generic(t, got.JSON("reply_markup"))
			if m, isMap := gotMarkup.(map[string]any); isMap && len(m) == 0 && wc.Method == "edit_message_text" {
				gotMarkup = nil // gotgbot invia sempre reply_markup: {} equivale all'assenza
			}
			assertDeepEqual(t, prefix+" reply_markup", gotMarkup, wc.ReplyMarkup)
			if got.ChatID() != testUserID {
				t.Errorf("%s: chat_id %s", prefix, got.ChatID())
			}
			if wc.Method == "edit_message_text" && got.String("message_id") != "42" {
				t.Errorf("%s: message_id %s", prefix, got.String("message_id"))
			}
		}

		gotUD := toGeneric(t, res.userAfter)
		assertDeepEqual(t, prefix+" user_data", gotUD, toGeneric(t, want.UserDataAfter))
		assertDeepEqual(t, prefix+" utente", userDict(t, s.LoadUser(testUserID)), want.UserAfter)
		var gotPending any
		if p := s.LoadPendingRates(testUserID); p != nil {
			gotPending = toGeneric(t, p)
		}
		assertDeepEqual(t, prefix+" pending", gotPending, want.PendingAfter)

		out := logs.String()
		for _, l := range want.Logs {
			level, rest, _ := strings.Cut(l, " ")
			name, msg, _ := strings.Cut(rest, " ")
			if line := " - " + name + " - " + level + " - " + msg + "\n"; !strings.Contains(out, line) {
				t.Errorf("%s: log mancante %q in:\n%s", prefix, line, out)
			}
		}
		if n := strings.Count(out, " - handlers.rate_update - "); n != len(want.Logs) {
			t.Errorf("%s: %d log di handlers.rate_update, want %d:\n%s", prefix, n, len(want.Logs), out)
		}
	}
}
