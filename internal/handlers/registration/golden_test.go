package registration

// Test differenziali: i JSON in testdata/ sono generati eseguendo il codice
// Python (handlers/registration.py con python-telegram-bot 22.6 reale e un
// livello HTTP che registra i parametri esatti inviati all'API) e il Go
// deve produrre esattamente lo stesso risultato.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/conversation"

	"github.com/dstmrk/octotracker/internal/testutil"
)

// ---------------------------------------------------------------- float()

func TestPyFloatGolden(t *testing.T) {
	var golden struct {
		Cases [][2]any `json:"cases"`
	}
	loadGolden(t, "float_golden.json", &golden)
	if len(golden.Cases) < 1000 {
		t.Fatalf("corpus troppo piccolo: %d", len(golden.Cases))
	}
	for _, c := range golden.Cases {
		in := c[0].(string)
		got, ok := pyFloat(in)
		if c[1] == nil {
			if ok {
				t.Errorf("float(%q): want ValueError, got %v", in, got)
			}
			continue
		}
		want, _ := pyFloatRepr(t, c[1])
		if !ok || !sameFloat(want, got) {
			t.Errorf("float(%q): want %v, got %v (ok=%v)", in, want, got, ok)
		}
	}
}

// TestUnicodeTablesGolden confronta, per tutti i code point non ASCII, gli
// spazi (str.isspace) e le cifre decimali (unicodedata.decimal) di Python
// con pyIsSpace / pyDecimal.
func TestUnicodeTablesGolden(t *testing.T) {
	var golden struct {
		Unicode struct {
			Version  string   `json:"unidata_version"`
			Spaces   []int32  `json:"spaces"`
			Decimals [][2]int `json:"decimals"`
		} `json:"unicode"`
	}
	loadGolden(t, "float_golden.json", &golden)
	spaces := map[rune]bool{}
	for _, cp := range golden.Unicode.Spaces {
		spaces[cp] = true
	}
	decimals := map[rune]int{}
	for _, d := range golden.Unicode.Decimals {
		decimals[rune(d[0])] = d[1]
	}
	var spaceDiffs, digitDiffs int
	for r := rune(128); r <= 0x10FFFF; r++ {
		if pyIsSpace(r) != spaces[r] {
			spaceDiffs++
			t.Errorf("isspace(U+%04X): python %v", r, spaces[r])
		}
		want, ok := decimals[r]
		if !ok {
			want = -1
		}
		if got := pyDecimal(r); got != want {
			digitDiffs++
			if digitDiffs < 20 {
				t.Errorf("decimal(U+%04X): python %d, go %d", r, want, got)
			}
		}
	}
	if spaceDiffs+digitDiffs > 0 {
		t.Errorf("differenze con Unicode %s di Python: spazi %d, cifre %d", golden.Unicode.Version, spaceDiffs, digitDiffs)
	}
}

func TestValidateNumericInputGolden(t *testing.T) {
	var cases [][3]any
	loadGolden(t, "validate_golden.json", &cases)
	for _, c := range cases {
		in := c[0].(string)
		value, ok, errMsg := ValidateNumericInput(in)
		wantErr, _ := c[2].(string)
		if errMsg != wantErr {
			t.Errorf("validate(%q): error want %q, got %q", in, wantErr, errMsg)
		}
		if c[1] == nil {
			if ok {
				t.Errorf("validate(%q): want None, got %v", in, value)
			}
			continue
		}
		want, _ := pyFloatRepr(t, c[1])
		if !ok || !sameFloat(want, value) {
			t.Errorf("validate(%q): want %v, got %v (ok=%v)", in, want, value, ok)
		}
	}
}

// ---------------------------------------------------------------- testi

func TestConfirmationMessageGolden(t *testing.T) {
	var cases []struct {
		User    any    `json:"user"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	loadGolden(t, "confirmation_golden.json", &cases)
	var errorsSeen int
	for i, c := range cases {
		u := userFromNorm(t, c.User)
		got, err := confirmationMessage(u)
		if c.Error != "" {
			errorsSeen++
			if err == nil || err.Error() != c.Error {
				t.Errorf("case %d: want error %q, got %v", i, c.Error, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d: unexpected error %v", i, err)
			continue
		}
		if got != c.Message {
			t.Errorf("case %d: message mismatch\nwant %q\n got %q", i, c.Message, got)
		}
	}
	if errorsSeen == 0 {
		t.Error("il corpus deve contenere casi di errore")
	}
}

func TestPromptsGolden(t *testing.T) {
	var golden map[string]json.RawMessage
	loadGolden(t, "prompts_golden.json", &golden)

	type prompt struct {
		Text        string          `json:"text"`
		ReplyMarkup json.RawMessage `json:"reply_markup"`
	}
	check := func(name, text string, kb gotgbot.InlineKeyboardMarkup) {
		var p prompt
		if err := json.Unmarshal(golden[name], &p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if text != p.Text {
			t.Errorf("%s text:\nwant %q\n got %q", name, p.Text, text)
		}
		gotKB, _ := json.Marshal(kb)
		var want, got any
		_ = json.Unmarshal(p.ReplyMarkup, &want)
		_ = json.Unmarshal(gotKB, &got)
		if d := diffNorm(t, name, want, got); len(d) > 0 {
			t.Errorf("%s keyboard: %v", name, d)
		}
	}
	text, kb := consumiLucePrompt()
	check("consumi_luce", text, kb)
	text, kb = consumiGasPrompt()
	check("consumi_gas", text, kb)
	text, kb = attivazionePrompt("luce", SkipScadenzaLuce)
	check("attivazione_luce", text, kb)
	text, kb = attivazionePrompt("gas", SkipScadenzaGas)
	check("attivazione_gas", text, kb)

	var constants map[string]string
	_ = json.Unmarshal(golden["constants"], &constants)
	for name, got := range map[string]string{
		"MSG_HAS_GAS": MsgHasGas, "MSG_GAS_CONSUMO": MsgGasConsumo, "MSG_DATE_INVALID": MsgDateInvalid,
		"SKIP_SCADENZA_LUCE": SkipScadenzaLuce, "SKIP_SCADENZA_GAS": SkipScadenzaGas,
	} {
		if constants[name] != got {
			t.Errorf("%s: want %q, got %q", name, constants[name], got)
		}
	}

	var states map[string]int
	_ = json.Unmarshal(golden["states"], &states)
	goStates := []string{
		StateTipoTariffa, StateLuceTipoVariabile, StateLuceEnergia, StateLuceComm, StateVuoiConsumiLuce,
		StateLuceConsumoF1, StateLuceConsumoF2, StateLuceConsumoF3, StateLuceAttivazione, StateHaGas,
		StateGasTipo, StateGasEnergia, StateGasComm, StateGasAttivazione, StateVuoiConsumiGas, StateGasConsumo,
	}
	if len(states) != len(goStates) {
		t.Errorf("stati Python %d, Go %d", len(states), len(goStates))
	}
	for _, s := range goStates {
		if _, ok := states[s]; !ok {
			t.Errorf("stato %s non esiste in Python", s)
		}
	}
}

// ---------------------------------------------------------------- handler

type goldenExpected struct {
	Error    *goldenError `json:"error"`
	State    *string      `json:"state"`
	Calls    any          `json:"calls"`
	UserData any          `json:"user_data"`
	DBUser   any          `json:"db_user"`
	Logs     [][]string   `json:"logs"`
}

type goldenScenario struct {
	Name     string            `json:"name"`
	Handler  string            `json:"handler"`
	Update   json.RawMessage   `json:"update"`
	UserData map[string]any    `json:"user_data"`
	DBUser   any               `json:"db_user"`
	Channel  *string           `json:"channel"`
	Fail     map[string][2]any `json:"fail"`
	Expected goldenExpected    `json:"expected"`
	Steps    []goldenFlowStep  `json:"steps"`
}

type goldenFlowStep struct {
	Update   json.RawMessage   `json:"update"`
	Fail     map[string][2]any `json:"fail"`
	Expected struct {
		Calls    any         `json:"calls"`
		Events   []flowEvent `json:"events"`
		State    *string     `json:"state"`
		UserData any         `json:"user_data"`
		DBUser   any         `json:"db_user"`
		Logs     [][]string  `json:"logs"`
	} `json:"expected"`
}

type flowEvent struct {
	Error     *goldenError `json:"error,omitempty"`
	Unhandled bool         `json:"unhandled,omitempty"`
}

// failMethods configura gli errori API del FakeClient come nel generatore.
func failMethods(fc *testutil.FakeClient, fail map[string][2]any) {
	for m := range map[string]bool{"sendMessage": true, "editMessageText": true, "answerCallbackQuery": true} {
		fc.FailMethod(m, nil)
	}
	for m, f := range fail {
		fc.FailMethod(m, testutil.TelegramError(m, int(f[0].(float64)), f[1].(string)))
	}
}

func channelOf(c *string) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(*c)
}

func compareLogs(t *testing.T, name string, want, got [][]string) {
	t.Helper()
	if want == nil {
		want = [][]string{}
	}
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Errorf("%s logs:\nwant %q\n got %q", name, want, got)
	}
}

func TestHandlersGolden(t *testing.T) {
	var scenarios []goldenScenario
	loadGolden(t, "handlers_golden.json", &scenarios)
	if len(scenarios) < 100 {
		t.Fatalf("troppi pochi scenari: %d", len(scenarios))
	}
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			logs := captureLogs(t)
			h := newHandlers(t)
			h.TelegramChannel = channelOf(sc.Channel)
			if sc.DBUser != nil {
				if !h.Store.SaveUser(userIDString(testUID), userFromNorm(t, sc.DBUser)) {
					t.Fatal("SaveUser pre-condizione fallito")
				}
			}
			setUserData(h, testUID, denorm(t, sc.UserData).(map[string]any))
			b, fc := testutil.NewBot(t)
			failMethods(fc, sc.Fail)
			logs.reset()

			fn := handlerFuncs(h)[sc.Handler]
			if fn == nil {
				t.Fatalf("handler %q sconosciuto", sc.Handler)
			}
			state, err := stateOf(fn(b, newCtx(b, decodeUpdate(t, sc.Update))))

			exp := sc.Expected
			switch {
			case exp.Error != nil:
				if !exp.Error.matches(err) {
					t.Errorf("error: want %s: %s, got %v", exp.Error.Type, exp.Error.Message, err)
				}
			case err != nil:
				t.Errorf("unexpected error: %v", err)
			case exp.State == nil || *exp.State != state:
				t.Errorf("state: want %v, got %q", exp.State, state)
			}
			if d := diffNorm(t, "calls", exp.Calls, normalizeCalls(t, fc.Calls())); len(d) > 0 {
				t.Errorf("calls differ:\n%s", strings.Join(d, "\n"))
			}
			if d := diffNorm(t, "user_data", exp.UserData, h.UserData.Snapshot(testUID)); len(d) > 0 {
				t.Errorf("user_data differ:\n%s", strings.Join(d, "\n"))
			}
			if d := diffNorm(t, "db_user", exp.DBUser, userMap(h.Store.LoadUser(userIDString(testUID)))); len(d) > 0 {
				t.Errorf("db differ:\n%s", strings.Join(d, "\n"))
			}
			compareLogs(t, sc.Name, exp.Logs, logs.handlersRecords())
		})
	}
}

// ---------------------------------------------------------------- conversazione

// catchAll replica TypeHandler(Update, ...) dopo il ConversationHandler.
type catchAll struct{ hit func() }

func (c catchAll) CheckUpdate(*gotgbot.Bot, *ext.Context) bool { return true }
func (c catchAll) HandleUpdate(*gotgbot.Bot, *ext.Context) error {
	c.hit()
	return nil
}
func (c catchAll) Name() string { return "catch_all" }

// stubFallbacks replica stub_cancel / stub_help del generatore.
func stubFallbacks(h *Handlers) []ext.Handler {
	cancel := func(b *gotgbot.Bot, ctx *ext.Context) error {
		if err := replyText(b, ctx.Message, "STUB cancel", "", nil); err != nil {
			return err
		}
		h.UserData.Clear(ctx.EffectiveUser.Id)
		return handlers.EndConversation()
	}
	help := func(b *gotgbot.Bot, ctx *ext.Context) error {
		h.UserData.Clear(ctx.EffectiveUser.Id)
		if err := replyText(b, ctx.Message, "STUB help", "", nil); err != nil {
			return err
		}
		return handlers.EndConversation()
	}
	return []ext.Handler{newCommandHandler("cancel", cancel), newCommandHandler("help", help)}
}

// conversationState legge lo stato della conversazione dell'utente di test.
func conversationState(t *testing.T, b *gotgbot.Bot, conv handlers.Conversation) *string {
	t.Helper()
	st, err := conv.StateStorage.Get(newCtx(b, testutil.MessageUpdate(testUID, "x")))
	if errors.Is(err, conversation.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("StateStorage.Get: %v", err)
	}
	return &st.Key
}

func TestConversationFlowsGolden(t *testing.T) {
	var flows []goldenScenario
	loadGolden(t, "flows_golden.json", &flows)
	for _, flow := range flows {
		t.Run(flow.Name, func(t *testing.T) {
			logs := captureLogs(t)
			h := newHandlers(t)
			h.TelegramChannel = channelOf(flow.Channel)
			if flow.DBUser != nil {
				h.Store.SaveUser(userIDString(testUID), userFromNorm(t, flow.DBUser))
			}
			b, fc := testutil.NewBot(t)

			var events []flowEvent
			var gotErrors []error
			d := ext.NewDispatcher(&ext.DispatcherOpts{
				Error: func(_ *gotgbot.Bot, _ *ext.Context, err error) ext.DispatcherAction {
					events = append(events, flowEvent{Error: &goldenError{}})
					gotErrors = append(gotErrors, err)
					return ext.DispatcherActionNoop
				},
				Panic: func(_ *gotgbot.Bot, _ *ext.Context, r any) {
					t.Errorf("panic non convertito in errore: %v", r)
				},
			})
			conv := h.Conversation(stubFallbacks(h))
			d.AddHandler(conv)
			d.AddHandler(catchAll{hit: func() { events = append(events, flowEvent{Unhandled: true}) }})

			for i, step := range flow.Steps {
				fc.Reset()
				failMethods(fc, step.Fail)
				events, gotErrors = nil, nil
				logs.reset()
				if err := d.ProcessUpdate(b, decodeUpdate(t, step.Update), nil); err != nil {
					t.Fatalf("step %d: ProcessUpdate: %v", i, err)
				}
				exp := step.Expected
				name := fmt.Sprintf("step %d (%s)", i, step.Update)

				if len(events) != len(exp.Events) {
					t.Errorf("%s: events want %+v, got %+v (errors %v)", name, exp.Events, events, gotErrors)
				} else {
					ei := 0
					for j, ev := range exp.Events {
						switch {
						case ev.Unhandled != events[j].Unhandled:
							t.Errorf("%s: event %d want %+v, got %+v", name, j, ev, events[j])
						case ev.Error != nil:
							if !ev.Error.matches(gotErrors[ei]) {
								t.Errorf("%s: error want %s: %s, got %v", name, ev.Error.Type, ev.Error.Message, gotErrors[ei])
							}
							ei++
						}
					}
				}
				gotState := conversationState(t, b, conv)
				if (gotState == nil) != (exp.State == nil) || (gotState != nil && *gotState != *exp.State) {
					t.Errorf("%s: state want %v, got %v", name, deref(exp.State), deref(gotState))
				}
				if dd := diffNorm(t, "calls", exp.Calls, normalizeCalls(t, fc.Calls())); len(dd) > 0 {
					t.Errorf("%s: calls differ:\n%s", name, strings.Join(dd, "\n"))
				}
				if dd := diffNorm(t, "user_data", exp.UserData, h.UserData.Snapshot(testUID)); len(dd) > 0 {
					t.Errorf("%s: user_data differ:\n%s", name, strings.Join(dd, "\n"))
				}
				if dd := diffNorm(t, "db_user", exp.DBUser, userMap(h.Store.LoadUser(userIDString(testUID)))); len(dd) > 0 {
					t.Errorf("%s: db differ:\n%s", name, strings.Join(dd, "\n"))
				}
				compareLogs(t, name, exp.Logs, logs.handlersRecords())
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
