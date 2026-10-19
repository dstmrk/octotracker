package feedback

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Test differenziali contro i file in testdata/, generati eseguendo il
// codice Python reale:
//   - cooldown_golden.json: _can_give_feedback con datetime.now() fissato e
//     get_last_feedback_time sostituito (esito e messaggio), sui timestamp
//     nel formato di SQLite;
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

type goldenNow []int

func (n goldenNow) time() time.Time {
	return time.Date(n[0], time.Month(n[1]), n[2], n[3], n[4], n[5], n[6]*1000, time.Local)
}

func TestGoldenCooldown(t *testing.T) {
	var g struct {
		Now   goldenNow `json:"now"`
		Cases []struct {
			Last *string `json:"last"`
			Can  bool    `json:"can"`
			Msg  *string `json:"msg"`
		} `json:"cases"`
	}
	readJSON(t, "cooldown_golden.json", &g)
	setNow(t, g.Now.time())
	for _, c := range g.Cases {
		h := &Handlers{Store: &fakeStore{last: c.Last}, UserData: tg.NewUserData()}
		can, msg := h.canGiveFeedback(testUserID)
		wantMsg := ""
		if c.Msg != nil {
			wantMsg = *c.Msg
		}
		if can != c.Can || msg != wantMsg {
			t.Errorf("last=%v: got (%v, %q), want (%v, %q)", deref(c.Last), can, msg, c.Can, wantMsg)
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

// goldenContext restituisce handler e update del caso.
func goldenContext(h *Handlers, b *gotgbot.Bot, c goldenHandlerCase) (handlers.Response, *ext.Context) {
	switch c.Handler {
	case "command":
		return h.FeedbackCommand, testutil.MessageContext(b, testutil.DefaultUserID, "/feedback")
	case "rating":
		return h.FeedbackRating, callbackContext(b, c.Data)
	case "comment":
		return h.FeedbackComment, testutil.MessageContext(b, testutil.DefaultUserID, c.Text)
	case "skip":
		return h.FeedbackSkipComment, callbackContext(b, "skip_comment")
	case "cancel_message":
		return h.FeedbackCancel, testutil.MessageContext(b, testutil.DefaultUserID, "/cancel")
	case "cancel_callback":
		return h.FeedbackCancel, callbackContext(b, "x")
	case "cancel_none":
		return h.FeedbackCancel, editedMessageContext(b, "/cancel")
	}
	return nil, nil
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
			v = int(f) // i voti sono int
		}
		h.UserData.Set(testutil.DefaultUserID, k, v)
	}
	b, fc := testutil.NewBot(t)
	handler, ctx := goldenContext(h, b, c)
	if handler == nil {
		t.Fatalf("handler sconosciuto %q", c.Handler)
	}
	res := call(t, fc, h.UserData, handler, b, ctx)

	checkGoldenOutcome(t, c, res)
	checkGoldenCalls(t, c.Out.Calls, res.calls)

	gotUD := map[string]any{}
	for k, v := range res.userAfter {
		if r, isInt := v.(int); isInt {
			v = float64(r)
		}
		gotUD[k] = v
	}
	assertDeepEqual(t, "user_data", gotUD, c.Out.UserDataAfter)
	checkGoldenFeedbacks(t, s, c)
}

// checkGoldenOutcome confronta lo stato restituito; un'eccezione Python
// corrisponde a un errore qualsiasi (il testo non conta).
func checkGoldenOutcome(t *testing.T, c goldenHandlerCase, res result) {
	t.Helper()
	if c.Out.Exception != "" {
		if res.err == nil {
			t.Errorf("atteso un errore (%s), got stato %q", c.Out.Exception, res.state)
		}
		return
	}
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

// checkGoldenCalls confronta metodo, testo, parse_mode e tastiera delle
// chiamate API.
func checkGoldenCalls(t *testing.T, want []goldenCall, got []testutil.Call) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("chiamate: got %+v, want %+v", got, want)
	}
	for i, w := range want {
		g := got[i]
		if pyMethod[g.Method] != w.Method {
			t.Errorf("chiamata %d: %s, want %s", i, g.Method, w.Method)
			continue
		}
		if w.Method == "answer" {
			continue
		}
		wantPM, _ := w.ParseMode.(string)
		if g.Text() != w.Text || g.ParseMode() != wantPM {
			t.Errorf("chiamata %d:\n got: %q %q\nwant: %q %q", i, g.ParseMode(), g.Text(), wantPM, w.Text)
		}
		gotMarkup := generic(t, g.JSON("reply_markup"))
		if m, isMap := gotMarkup.(map[string]any); isMap && len(m) == 0 && w.Method == "edit_message_text" {
			gotMarkup = nil // gotgbot invia sempre reply_markup: {} equivale all'assenza
		}
		assertDeepEqual(t, "reply_markup", gotMarkup, w.ReplyMarkup)
	}
}

// checkGoldenFeedbacks confronta i feedback salvati nel database.
func checkGoldenFeedbacks(t *testing.T, s *database.Store, c goldenHandlerCase) {
	t.Helper()
	saved := s.GetRecentFeedbacks(100)
	if len(saved) != len(c.Out.Feedbacks) {
		t.Fatalf("feedback salvati: got %d, want %d", len(saved), len(c.Out.Feedbacks))
	}
	for i, want := range c.Out.Feedbacks {
		got := saved[len(saved)-1-i]
		g := fmt.Sprintf("%v|%q|%s", deref(got.Rating), deref(got.Comment), got.FeedbackType)
		if w := fmt.Sprintf("%v|%q|%s", deref(want.Rating), deref(want.Comment), want.FeedbackType); g != w {
			t.Errorf("feedback %d: got %s, want %s", i, g, w)
		}
	}
}
