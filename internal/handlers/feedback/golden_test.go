package feedback

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Test differenziali contro i file in testdata/, generati eseguendo il
// codice Python reale:
//   - fromisoformat_golden.json: datetime.fromisoformat di CPython 3.13 su
//     un corpus di stringhe valide, quasi valide e casuali;
//   - cooldown_golden.json: _can_give_feedback con datetime.now() fissato e
//     get_last_feedback_time sostituito (esito, messaggio e log);
//   - handlers_golden.json: gli handler di handlers/feedback.py con update
//     finti e database reale (chiamate API, stato, user_data, feedback salvati).

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

func TestGoldenFromISOFormat(t *testing.T) {
	var cases []struct {
		S     string `json:"s"`
		R     []int  `json:"r"`
		Aware bool   `json:"aware"`
		E     string `json:"e"`
	}
	readJSON(t, "fromisoformat_golden.json", &cases)
	if len(cases) < 5000 {
		t.Fatalf("corpus troppo piccolo: %d", len(cases))
	}
	ok := 0
	for _, c := range cases {
		dt, err := fromISOFormat(c.S)
		if c.E != "" {
			if err == nil {
				t.Errorf("%q: atteso ValueError %q, got %+v", c.S, c.E, dt)
			} else if err.Error() != c.E {
				t.Errorf("%q: errore\n got: %s\nwant: %s", c.S, err, c.E)
			} else if pe, isPy := err.(*pyError); !isPy || pe.typ != "ValueError" {
				t.Errorf("%q: tipo errore %T", c.S, err)
			}
			continue
		}
		ok++
		if err != nil {
			t.Errorf("%q: errore inatteso %v (want %v)", c.S, err, c.R)
			continue
		}
		got := []int{dt.year, dt.month, dt.day, dt.hour, dt.minute, dt.second, dt.microsecond}
		if fmt.Sprint(got) != fmt.Sprint(c.R) || dt.aware != c.Aware {
			t.Errorf("%q: got %v aware=%v, want %v aware=%v", c.S, got, dt.aware, c.R, c.Aware)
		}
	}
	if ok < 1000 {
		t.Errorf("pochi casi validi nel corpus: %d", ok)
	}
}

type goldenNow []int

func (n goldenNow) time() time.Time {
	return time.Date(n[0], time.Month(n[1]), n[2], n[3], n[4], n[5], n[6]*1000, time.Local)
}

func TestGoldenCooldown(t *testing.T) {
	var g struct {
		Now   goldenNow `json:"now"`
		Cases []struct {
			Last *string  `json:"last"`
			Can  bool     `json:"can"`
			Msg  *string  `json:"msg"`
			Logs []string `json:"logs"`
		} `json:"cases"`
	}
	readJSON(t, "cooldown_golden.json", &g)
	setNow(t, g.Now.time())
	logs := captureLogs(t)
	for _, c := range g.Cases {
		logs.Reset()
		h := &Handlers{Store: &fakeStore{last: c.Last}, UserData: tg.NewUserData()}
		can, msg := h.canGiveFeedback(testUserID)
		wantMsg := ""
		if c.Msg != nil {
			wantMsg = *c.Msg
		}
		if can != c.Can || msg != wantMsg {
			t.Errorf("last=%v: got (%v, %q), want (%v, %q)", deref(c.Last), can, msg, c.Can, wantMsg)
		}
		got := logs.String()
		if len(c.Logs) == 0 && got != "" {
			t.Errorf("last=%v: log inattesi %q", deref(c.Last), got)
		}
		for _, l := range c.Logs {
			// "WARNING handlers.feedback <msg>" → "- handlers.feedback - WARNING - <msg>"
			level, rest, _ := strings.Cut(l, " ")
			name, msg, _ := strings.Cut(rest, " ")
			want := " - " + name + " - " + level + " - " + msg + "\n"
			if !strings.Contains(got, want) {
				t.Errorf("last=%v: log mancante %q in %q", deref(c.Last), want, got)
			}
		}
	}
}

type goldenCall struct {
	Method      string `json:"method"`
	Text        string `json:"text"`
	ParseMode   any    `json:"parse_mode"`
	ReplyMarkup any    `json:"reply_markup"`
}

type goldenHandlerCase struct {
	Handler      string         `json:"handler"`
	Data         string         `json:"data"`
	Text         string         `json:"text"`
	WithUser     *bool          `json:"with_user"`
	LastFeedback *string        `json:"last_feedback"`
	UserData     map[string]any `json:"user_data"`
	Out          struct {
		Result        *string        `json:"result"`
		Exception     string         `json:"exception"`
		Calls         []goldenCall   `json:"calls"`
		UserDataAfter map[string]any `json:"user_data_after"`
		Feedbacks     []struct {
			Rating       *int    `json:"rating"`
			Comment      *string `json:"comment"`
			FeedbackType string  `json:"feedback_type"`
		} `json:"feedbacks"`
	} `json:"out"`
}

// pyMethod converte un metodo dell'API Bot nel metodo PTB registrato dal generatore.
var pyMethod = map[string]string{
	"sendMessage":         "reply_text",
	"editMessageText":     "edit_message_text",
	"answerCallbackQuery": "answer",
}

func TestGoldenHandlers(t *testing.T) {
	var g struct {
		Now   goldenNow           `json:"now"`
		Cases []goldenHandlerCase `json:"cases"`
	}
	readJSON(t, "handlers_golden.json", &g)
	setNow(t, g.Now.time())
	for i, c := range g.Cases {
		t.Run(fmt.Sprintf("%02d_%s", i, c.Handler), func(t *testing.T) { runGoldenHandler(t, c) })
	}
}

func runGoldenHandler(t *testing.T, c goldenHandlerCase) {
	s := newTestStore(t)
	if c.WithUser == nil || *c.WithUser {
		saveSampleUser(t, s, testUserID)
		if c.LastFeedback != nil {
			setLastFeedback(t, s, testUserID, *c.LastFeedback)
		}
	}
	h := &Handlers{Store: s, UserData: tg.NewUserData()}
	for k, v := range c.UserData {
		if f, isNum := v.(float64); isNum && k == keyRating {
			h.UserData.Set(testutil.DefaultUserID, k, int(f)) // i voti sono int in Python
			continue
		}
		h.UserData.Set(testutil.DefaultUserID, k, v)
	}
	b, fc := testutil.NewBot(t)

	var handler handlers.Response
	ctx := testutil.MessageContext(b, testutil.DefaultUserID, c.Text)
	switch c.Handler {
	case "command":
		handler, ctx = h.FeedbackCommand, testutil.MessageContext(b, testutil.DefaultUserID, "/feedback")
	case "rating":
		handler, ctx = h.FeedbackRating, callbackContext(b, c.Data)
	case "comment":
		handler = h.FeedbackComment
	case "skip":
		handler, ctx = h.FeedbackSkipComment, callbackContext(b, "skip_comment")
	case "cancel_message":
		handler, ctx = h.FeedbackCancel, testutil.MessageContext(b, testutil.DefaultUserID, "/cancel")
	case "cancel_callback":
		handler, ctx = h.FeedbackCancel, callbackContext(b, "x")
	case "cancel_none":
		handler, ctx = h.FeedbackCancel, editedMessageContext(b, "/cancel")
	default:
		t.Fatalf("handler sconosciuto %q", c.Handler)
	}
	res := call(t, fc, h.UserData, handler, b, ctx)

	if c.Out.Exception != "" {
		if res.err == nil || pyErrString(res.err) != c.Out.Exception {
			t.Errorf("eccezione: got %v, want %q", res.err, c.Out.Exception)
		}
	} else {
		if res.err != nil {
			t.Fatalf("errore inatteso: %v", res.err)
		}
		want := ""
		if c.Out.Result != nil {
			want = *c.Out.Result
		}
		if res.state != want {
			t.Errorf("stato: got %q, want %q", res.state, want)
		}
	}

	if len(res.calls) != len(c.Out.Calls) {
		t.Fatalf("chiamate: got %+v, want %+v", res.calls, c.Out.Calls)
	}
	for i, want := range c.Out.Calls {
		got := res.calls[i]
		if pyMethod[got.Method] != want.Method {
			t.Errorf("chiamata %d: %s, want %s", i, got.Method, want.Method)
			continue
		}
		if want.Method == "answer" {
			continue
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
		gotMarkup := generic(t, got.JSON("reply_markup"))
		if m, isMap := gotMarkup.(map[string]any); isMap && len(m) == 0 && want.Method == "edit_message_text" {
			gotMarkup = nil // gotgbot invia sempre reply_markup: {} equivale all'assenza
		}
		assertDeepEqual(t, "reply_markup", gotMarkup, want.ReplyMarkup)
	}

	wantUD := c.Out.UserDataAfter
	gotUD := map[string]any{}
	for k, v := range res.userAfter {
		if r, isInt := v.(int); isInt {
			v = float64(r)
		}
		gotUD[k] = v
	}
	assertDeepEqual(t, "user_data", gotUD, wantUD)

	saved := s.GetRecentFeedbacks(100)
	if len(saved) != len(c.Out.Feedbacks) {
		t.Fatalf("feedback salvati: got %d, want %d", len(saved), len(c.Out.Feedbacks))
	}
	for i, want := range c.Out.Feedbacks {
		got := saved[len(saved)-1-i]
		if fmt.Sprint(deref(got.Rating)) != fmt.Sprint(deref(want.Rating)) ||
			fmt.Sprint(deref(got.Comment)) != fmt.Sprint(deref(want.Comment)) || got.FeedbackType != want.FeedbackType {
			t.Errorf("feedback %d: got %v/%v/%s, want %v/%v/%s", i, deref(got.Rating), deref(got.Comment), got.FeedbackType,
				deref(want.Rating), deref(want.Comment), want.FeedbackType)
		}
	}
}

// pyErrString restituisce "Tipo: messaggio" come f"{type(e).__name__}: {e}".
func pyErrString(err error) string {
	if pe, ok := err.(*pyError); ok {
		return pe.typ + ": " + pe.msg
	}
	return err.Error()
}
