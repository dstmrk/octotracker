package registration

// Edge case non coperti dai test Python: errori API su ogni handler,
// update senza le parti attese (AttributeError/TypeError in Python),
// destinazione delle modifiche, repr Python per i log e matching dei comandi.

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
)

var (
	errBlocked = testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user")
	errEdit    = testutil.TelegramError("editMessageText", 400, "Bad Request: message to edit not found")
	errAnswer  = testutil.TelegramError("answerCallbackQuery", 400, "Bad Request: message is not modified")
)

// fullUserData contiene tutti i dati di una registrazione luce + gas fissa.
func fullUserData() map[string]any {
	return map[string]any{
		"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": 0.145, "luce_comm": 72.0,
		"gas_tipo": "fissa", "gas_fascia": "monoraria", "gas_energia": 0.456, "gas_comm": 84.0,
	}
}

// TestCallbackHandlersPropagateErrors: un errore di answer (non "too old") o
// di edit interrompe l'handler senza cambiare stato, come l'eccezione Python.
func TestCallbackHandlersPropagateErrors(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*Handlers) handlers.Response
		data string
		ud   map[string]any
	}{
		{"tipo_tariffa_fissa", func(h *Handlers) handlers.Response { return h.TipoTariffa }, "tipo_fissa", nil},
		{"tipo_tariffa_variabile", func(h *Handlers) handlers.Response { return h.TipoTariffa }, "tipo_variabile", nil},
		{"luce_tipo_variabile", func(h *Handlers) handlers.Response { return h.LuceTipoVariabile }, "luce_mono", nil},
		{"skip_luce", func(h *Handlers) handlers.Response { return h.SkipLuceAttivazione }, SkipScadenzaLuce, nil},
		{"skip_gas", func(h *Handlers) handlers.Response { return h.SkipGasAttivazione }, SkipScadenzaGas, nil},
		{"vuoi_consumi_luce_si", func(h *Handlers) handlers.Response { return h.VuoiConsumiLuce }, "consumi_luce_si", nil},
		{"vuoi_consumi_luce_no", func(h *Handlers) handlers.Response { return h.VuoiConsumiLuce }, "consumi_luce_no", nil},
		{"ha_gas_si", func(h *Handlers) handlers.Response { return h.HaGas }, "gas_si", nil},
		{"ha_gas_no", func(h *Handlers) handlers.Response { return h.HaGas }, "gas_no", fullUserData()},
		{"ha_gas_no_missing", func(h *Handlers) handlers.Response { return h.HaGas }, "gas_no", nil},
		{"gas_tipo_tariffa", func(h *Handlers) handlers.Response { return h.GasTipoTariffa }, "gas_tipo_fissa", nil},
		{"vuoi_consumi_gas_si", func(h *Handlers) handlers.Response { return h.VuoiConsumiGas }, "consumi_gas_si", nil},
		{"vuoi_consumi_gas_no", func(h *Handlers) handlers.Response { return h.VuoiConsumiGas }, "consumi_gas_no", fullUserData()},
	}
	for _, c := range cases {
		for _, fail := range []struct {
			method string
			err    *gotgbot.TelegramError
		}{{"answerCallbackQuery", errAnswer}, {"editMessageText", errEdit}} {
			t.Run(c.name+"/"+fail.method, func(t *testing.T) {
				h := newHandlers(t)
				setUserData(h, testUID, c.ud)
				b, fc := testutil.NewBot(t)
				fc.FailMethod(fail.method, fail.err)
				state, err := stateOf(c.fn(h)(b, testutil.CallbackContext(b, testUID, c.data)))
				if err != fail.err || state != "" {
					t.Fatalf("want %v, got state %q err %v", fail.err, state, err)
				}
				if fail.method == "answerCallbackQuery" && len(fc.CallsTo("editMessageText")) != 0 {
					t.Fatal("edit dopo answer fallito")
				}
			})
		}
	}
}

// TestMessageHandlersPropagateErrors: un errore di sendMessage interrompe
// l'handler in ogni ramo (errore di validazione, conversione, successo).
func TestMessageHandlersPropagateErrors(t *testing.T) {
	type mh = func(*Handlers) func(*gotgbot.Bot, *ext.Context) error
	cases := []struct {
		name string
		fn   mh
		text string
		ud   map[string]any
	}{
		{"luce_energia_negative", hLuceEnergia, "-1", nil},
		{"luce_energia_invalid", hLuceEnergia, "abc", nil},
		{"luce_energia_ok", hLuceEnergia, "1", nil},
		{"luce_comm_fissa", hLuceComm, "1", map[string]any{"luce_tipo": "fissa"}},
		{"luce_comm_variabile", hLuceComm, "1", nil},
		{"luce_f1_mono", hLuceConsumoF1, "1", nil},
		{"luce_f1_tri", hLuceConsumoF1, "1", map[string]any{"luce_fascia": "trioraria"}},
		{"luce_f2", hLuceConsumoF2, "1", nil},
		{"luce_f3", hLuceConsumoF3, "1", nil},
		{"gas_energia", hGasEnergia, "1", nil},
		{"gas_comm_fissa", hGasComm, "1", map[string]any{"gas_tipo": "fissa"}},
		{"gas_comm_variabile", hGasComm, "1", nil},
		{"gas_consumo_ok", hGasConsumo, "1", fullUserData()},
		{"gas_consumo_missing", hGasConsumo, "1", nil},
		{"luce_attivazione_ok", func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.LuceAttivazione }, "15/03/2025", nil},
		{"luce_attivazione_bad", func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.LuceAttivazione }, "xx", nil},
		{"gas_attivazione_ok", func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.GasAttivazione }, "15/03/2025", nil},
		{"start", func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.Start }, "/start", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHandlers(t)
			setUserData(h, testUID, c.ud)
			b, fc := testutil.NewBot(t)
			fc.FailMethod("sendMessage", errBlocked)
			state, err := stateOf(c.fn(h)(b, testutil.MessageContext(b, testUID, c.text)))
			if err != errBlocked || state != "" {
				t.Fatalf("want %v, got state %q err %v", errBlocked, state, err)
			}
		})
	}

	// attivazione: fallisce il secondo invio (prompt dei consumi)
	t.Run("luce_attivazione_second_send", func(t *testing.T) {
		h := newHandlers(t)
		b, fc := testutil.NewBot(t)
		n := 0
		fc.SetResponder(func(method string, _ map[string]any) (json.RawMessage, error) {
			if method == "sendMessage" {
				if n++; n == 2 {
					return nil, errBlocked
				}
			}
			return nil, nil
		})
		if _, err := stateOf(h.LuceAttivazione(b, testutil.MessageContext(b, testUID, "15/03/2025"))); err != errBlocked {
			t.Fatalf("want %v, got %v", errBlocked, err)
		}
		// la scadenza è già stata salvata, come in Python
		if v, _ := h.UserData.Get(testUID, keyLuceScadenza); v != "2026-03-15" {
			t.Fatalf("luce_scadenza = %v", v)
		}
	})
}

// TestHandlersWithoutExpectedUpdateParts replica gli AttributeError di
// Python quando l'update non contiene la parte usata dall'handler.
func TestHandlersWithoutExpectedUpdateParts(t *testing.T) {
	h := newHandlers(t)
	b, fc := testutil.NewBot(t)
	msgCtx := testutil.MessageContext(b, testUID, "x")

	callbackHandlers := map[string]handlers.Response{
		"tipo_tariffa": h.TipoTariffa, "luce_tipo_variabile": h.LuceTipoVariabile,
		"skip_luce": h.SkipLuceAttivazione, "vuoi_consumi_luce": h.VuoiConsumiLuce, "ha_gas": h.HaGas,
		"gas_tipo_tariffa": h.GasTipoTariffa, "skip_gas": h.SkipGasAttivazione, "vuoi_consumi_gas": h.VuoiConsumiGas,
	}
	for name, fn := range callbackHandlers {
		_, err := stateOf(fn(b, msgCtx))
		if err == nil || err.Error() != "AttributeError: 'NoneType' object has no attribute 'answer'" {
			t.Errorf("%s: err %v", name, err)
		}
	}

	// update senza utente (es. post di un canale)
	noUser := newCtx(b, &gotgbot.Update{ChannelPost: &gotgbot.Message{Chat: gotgbot.Chat{Id: -1}, Text: "/start"}})
	if _, err := stateOf(h.Start(b, noUser)); err == nil || !strings.Contains(err.Error(), "'id'") {
		t.Errorf("start senza utente: %v", err)
	}
	if _, err := stateOf(h.SalvaEConferma(b, noUser, nil, true)); err == nil || !strings.Contains(err.Error(), "'id'") {
		t.Errorf("salva_e_conferma senza utente: %v", err)
	}
	if len(fc.Calls()) != 0 {
		t.Errorf("chiamate inattese: %v", fc.Calls())
	}

	if err := editMessageText(b, nil, "x", "", nil); err == nil || !strings.Contains(err.Error(), "edit_message_text") {
		t.Errorf("edit senza query: %v", err)
	}
}

// TestEditMessageTextTargets verifica la destinazione della modifica per
// messaggi normali (valore e puntatore), inaccessibili, inline e assenti.
func TestEditMessageTextTargets(t *testing.T) {
	b, fc := testutil.NewBot(t)
	chat := gotgbot.Chat{Id: 77, Type: "private"}
	cases := []struct {
		name string
		cq   *gotgbot.CallbackQuery
		want map[string]any
	}{
		{"message", &gotgbot.CallbackQuery{Message: gotgbot.Message{Chat: chat, MessageId: 5, BusinessConnectionId: "bc"}},
			map[string]any{"chat_id": int64(77), "message_id": int64(5), "business_connection_id": "bc"}},
		{"pointer", &gotgbot.CallbackQuery{Message: &gotgbot.Message{Chat: chat, MessageId: 6}},
			map[string]any{"chat_id": int64(77), "message_id": int64(6)}},
		{"inaccessible", &gotgbot.CallbackQuery{Message: gotgbot.InaccessibleMessage{Chat: chat, MessageId: 7}},
			map[string]any{"chat_id": int64(77), "message_id": int64(7)}},
		{"inline", &gotgbot.CallbackQuery{InlineMessageId: "inl"},
			map[string]any{"inline_message_id": "inl"}},
	}
	for _, c := range cases {
		fc.Reset()
		if err := editMessageText(b, c.cq, "testo", parseModeHTML, nil); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		call := fc.LastCall(t, "editMessageText")
		for k, v := range c.want {
			if call.Params[k] != v {
				t.Errorf("%s: %s = %#v, want %#v", c.name, k, call.Params[k], v)
			}
		}
		for _, k := range []string{"chat_id", "message_id", "inline_message_id", "business_connection_id"} {
			if _, ok := c.want[k]; !ok {
				if _, present := call.Params[k]; present {
					t.Errorf("%s: parametro %s inatteso", c.name, k)
				}
			}
		}
		if call.Text() != "testo" || call.ParseMode() != "HTML" {
			t.Errorf("%s: text/parse_mode %q %q", c.name, call.Text(), call.ParseMode())
		}
	}

	fc.Reset()
	if err := editMessageText(b, &gotgbot.CallbackQuery{}, "x", "", nil); err != errInaccessibleMessage {
		t.Errorf("callback senza messaggio: %v", err)
	}
	if len(fc.Calls()) != 0 {
		t.Error("nessuna chiamata attesa senza messaggio")
	}
}

// TestReplyTextOptions verifica topic, direct messages topic e business
// connection come Message.reply_text di PTB.
func TestReplyTextOptions(t *testing.T) {
	b, fc := testutil.NewBot(t)
	msg := &gotgbot.Message{
		Chat: gotgbot.Chat{Id: 9}, IsTopicMessage: true, MessageThreadId: 3,
		BusinessConnectionId: "bc", DirectMessagesTopic: &gotgbot.DirectMessagesTopic{TopicId: 4},
	}
	if err := replyText(b, msg, "ciao", "", nil); err != nil {
		t.Fatal(err)
	}
	call := fc.LastCall(t, "sendMessage")
	want := map[string]any{"chat_id": int64(9), "text": "ciao", "message_thread_id": int64(3),
		"business_connection_id": "bc", "direct_messages_topic_id": int64(4)}
	if len(call.Params) != len(want) {
		t.Fatalf("params %v, want %v", call.Params, want)
	}
	for k, v := range want {
		if call.Params[k] != v {
			t.Errorf("%s = %#v, want %#v", k, call.Params[k], v)
		}
	}

	// message_thread_id solo per i messaggi in un topic
	fc.Reset()
	_ = replyText(b, &gotgbot.Message{Chat: gotgbot.Chat{Id: 9}, MessageThreadId: 3}, "x", "", nil)
	if _, ok := fc.LastCall(t, "sendMessage").Params["message_thread_id"]; ok {
		t.Error("message_thread_id inviato per un messaggio fuori da un topic")
	}

	if err := replyText(b, nil, "x", "", nil); err == nil || err.Error() != "AttributeError: 'NoneType' object has no attribute 'reply_text'" {
		t.Errorf("reply senza messaggio: %v", err)
	}
}

func TestSalvaEConfermaFormatErrors(t *testing.T) {
	cases := []struct {
		name  string
		ud    map[string]any
		query bool
		want  string
	}{
		{"nan_query", map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": math.NaN(), "luce_comm": 1.0},
			true, "ValueError: cannot convert float NaN to integer"},
		{"inf_update", map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": 1.0, "luce_comm": math.Inf(1)},
			false, "OverflowError: cannot convert float infinity to integer"},
		{"trioraria_incomplete", map[string]any{"luce_tipo": "fissa", "luce_fascia": "trioraria", "luce_energia": 1.0,
			"luce_comm": 1.0, "luce_consumo_f1": 1.0}, false,
			"TypeError: unsupported operand type(s) for +: 'float' and 'NoneType'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHandlers(t)
			setUserData(h, testUID, c.ud)
			b, fc := testutil.NewBot(t)
			var ctx *ext.Context
			var query *gotgbot.CallbackQuery
			if c.query {
				ctx = testutil.CallbackContext(b, testUID, "gas_no")
				query = ctx.CallbackQuery
			} else {
				ctx = testutil.MessageContext(b, testUID, "1")
			}
			_, err := stateOf(h.SalvaEConferma(b, ctx, query, true))
			if err == nil || err.Error() != c.want {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if len(fc.Calls()) != 0 {
				t.Errorf("nessun messaggio atteso, chiamate %v", fc.Calls())
			}
		})
	}
}

// TestSalvaEConfermaSaveFailureStillConfirms: in Python l'esito di
// save_user è ignorato e la conferma viene inviata comunque.
func TestSalvaEConfermaSaveFailureStillConfirms(t *testing.T) {
	h := newHandlers(t)
	setUserData(h, testUID, map[string]any{"luce_tipo": "mista", "luce_fascia": "monoraria", "luce_energia": 0.1, "luce_comm": 1.0})
	b, fc := testutil.NewBot(t)
	if s := mustState(t, h.SalvaEConferma(b, testutil.MessageContext(b, testUID, "1"), nil, true)); s != "END" {
		t.Fatalf("stato %q", s)
	}
	if h.Store.LoadUser(userIDString(testUID)) != nil {
		t.Fatal("utente non valido salvato")
	}
	contains(t, fc.LastCall(t, "sendMessage").Text(), "Luce (Mista Monoraria)")
}

func TestBuildUserDataKeepsOptionalFields(t *testing.T) {
	h := newHandlers(t)
	ud := fullUserData()
	ud["luce_scadenza"] = "2026-03-15"
	ud["luce_consumo_f1"] = 1.0
	ud["luce_consumo_f2"] = 2.0
	ud["luce_consumo_f3"] = 3.0
	ud["gas_scadenza"] = "2026-06-01"
	ud["gas_consumo_annuo"] = 4.0
	setUserData(h, testUID, ud)

	u, err := h.buildUserData(testUID, false)
	if err != nil {
		t.Fatal(err)
	}
	want := database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0,
			ConsumoF1: ptr(1), ConsumoF2: ptr(2), ConsumoF3: ptr(3), Scadenza: "2026-03-15"},
		Gas: &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.456, Commercializzazione: 84.0,
			ConsumoAnnuo: ptr(4), Scadenza: "2026-06-01"},
	}
	if d := diffNorm(t, "user", userMap(&want), userMap(&u)); len(d) > 0 {
		t.Fatal(d)
	}

	// solo luce: i dati gas in user_data vengono ignorati (gas = None)
	u, err = h.buildUserData(testUID, true)
	if err != nil || u.Gas != nil {
		t.Fatalf("solo luce: %+v %v", u.Gas, err)
	}
}

func TestPyTruthy(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{nil, false}, {false, false}, {true, true}, {"", false}, {"x", true}, {0.0, false}, {0.5, true},
		{0, false}, {2, true}, {int64(0), false}, {int64(3), true}, {[]string{}, true},
	}
	for _, c := range cases {
		if got := pyTruthy(c.v); got != c.want {
			t.Errorf("pyTruthy(%#v) = %v", c.v, got)
		}
	}
}

func TestPyStrRepr(t *testing.T) {
	cases := map[string]string{
		"luce_tipo":     `'luce_tipo'`,
		"it's":          `"it's"`,
		`a'b"c`:         `'a\'b"c'`,
		`back\slash`:    `'back\\slash'`,
		"tab\tnl\ncr\r": `'tab\tnl\ncr\r'`,
		"\x00\x7f":      `'\x00\x7f'`,
		"\u00a0":        `'\xa0'`,
		"\u200b":        `'\u200b'`,
		"\U000E0001":    `'\U000e0001'`,
		"è€🐙":           `'è€🐙'`,
		"":              `''`,
	}
	for in, want := range cases {
		if got := pyStrRepr(in); got != want {
			t.Errorf("repr(%q) = %s, want %s", in, got, want)
		}
	}
	if got := pyListRepr([]string{"a", "b"}); got != "['a', 'b']" {
		t.Errorf("list repr %s", got)
	}
	if got := pyListRepr(nil); got != "[]" {
		t.Errorf("empty list repr %s", got)
	}
	if got := (&keyError{"Dati mancanti per luce: luce_tipo"}).Error(); got != "'Dati mancanti per luce: luce_tipo'" {
		t.Errorf("str(KeyError) = %s", got)
	}
}

// TestAvailableKeysOrder: le chiavi delle altre conversazioni vengono prima,
// poi quelle della registrazione nell'ordine del flusso.
func TestAvailableKeysOrder(t *testing.T) {
	h := newHandlers(t)
	setUserData(h, testUID, map[string]any{
		"gas_consumo_annuo": 1.0, "luce_comm": 1.0, "rating": 3, "is_variabile": true, "aaa": 1, "gas_tipo": "fissa",
	})
	got := strings.Join(h.availableKeys(testUID), ",")
	if want := "aaa,rating,is_variabile,luce_comm,gas_tipo,gas_consumo_annuo"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCommandHandler(t *testing.T) {
	b, _ := testutil.NewBot(t)
	called := false
	c := newCommandHandler("Start", func(*gotgbot.Bot, *ext.Context) error { called = true; return nil })
	if c.Name() != "registration_command_start" {
		t.Errorf("name %q", c.Name())
	}
	entity := func(length int64) []gotgbot.MessageEntity {
		return []gotgbot.MessageEntity{{Type: "bot_command", Offset: 0, Length: length}}
	}
	msg := func(text string, ents []gotgbot.MessageEntity) *gotgbot.Message {
		u := testutil.User(testUID)
		return &gotgbot.Message{Chat: gotgbot.Chat{Id: testUID}, From: &u, Text: text, Entities: ents}
	}
	cases := []struct {
		name string
		upd  *gotgbot.Update
		want bool
	}{
		{"plain", &gotgbot.Update{Message: msg("/start", entity(6))}, true},
		{"edited", &gotgbot.Update{EditedMessage: msg("/start", entity(6))}, true},
		{"uppercase", &gotgbot.Update{Message: msg("/START", entity(6))}, true},
		{"punctuation", &gotgbot.Update{Message: msg("/start!", entity(6))}, true},
		{"bot username", &gotgbot.Update{Message: msg("/start@OctoTracker_Test_Bot", entity(27))}, true},
		{"other bot", &gotgbot.Update{Message: msg("/start@other_bot", entity(16))}, false},
		{"double at", &gotgbot.Update{Message: msg("/start@octotracker_test_bot@x", entity(29))}, true},
		{"entity longer than text", &gotgbot.Update{Message: msg("/start", entity(99))}, true},
		{"entity of one char", &gotgbot.Update{Message: msg("/start", entity(1))}, false},
		{"different command", &gotgbot.Update{Message: msg("/startx", entity(7))}, false},
		{"no entity", &gotgbot.Update{Message: msg("/start", nil)}, false},
		{"channel post", &gotgbot.Update{ChannelPost: msg("/start", entity(6))}, false},
		{"callback", testutil.CallbackUpdate(testUID, "/start"), false},
		{"caption", &gotgbot.Update{Message: &gotgbot.Message{Chat: gotgbot.Chat{Id: 1}, Caption: "/start",
			CaptionEntities: entity(6)}}, false},
		{"emoji command", &gotgbot.Update{Message: msg("/🐙start", entity(8))}, false},
	}
	for _, tc := range cases {
		if got := c.CheckUpdate(b, newCtx(b, tc.upd)); got != tc.want {
			t.Errorf("%s: CheckUpdate = %v, want %v", tc.name, got, tc.want)
		}
	}
	if err := c.HandleUpdate(b, newCtx(b, &gotgbot.Update{Message: msg("/start", entity(6))})); err != nil || !called {
		t.Errorf("HandleUpdate: %v called=%v", err, called)
	}
}

// TestConversationStructure verifica gli stati e gli handler registrati.
func TestConversationStructure(t *testing.T) {
	h := newHandlers(t)
	conv := h.Conversation(nil)
	if len(conv.EntryPoints) != 2 || len(conv.States) != 16 || conv.AllowReEntry || conv.StateStorage == nil {
		t.Fatalf("conversazione inattesa: %d entry, %d stati", len(conv.EntryPoints), len(conv.States))
	}
	for _, s := range []string{StateLuceAttivazione, StateGasAttivazione} {
		hs := conv.States[s]
		if len(hs) != 2 {
			t.Fatalf("%s: %d handler", s, len(hs))
		}
		if _, ok := hs[0].(handlers.CallbackQuery); !ok {
			t.Errorf("%s: il primo handler deve essere la callback di skip", s)
		}
		if m, ok := hs[1].(handlers.Message); !ok || !m.AllowEdited || !m.AllowBusiness {
			t.Errorf("%s: il secondo handler deve essere il MessageHandler", s)
		}
	}
	fb := []ext.Handler{newCommandHandler("cancel", nil)}
	if got := h.Conversation(fb).Fallbacks; len(got) != 1 {
		t.Errorf("fallbacks %v", got)
	}
}

// TestConversationEndToEnd percorre la conversazione tramite il dispatcher
// gotgbot: entry point, stati, skip con pattern e salvataggio finale.
func TestConversationEndToEnd(t *testing.T) {
	h := newHandlers(t)
	b, fc := testutil.NewBot(t)
	d := ext.NewDispatcher(&ext.DispatcherOpts{
		Error: func(_ *gotgbot.Bot, _ *ext.Context, err error) ext.DispatcherAction {
			t.Errorf("errore inatteso: %v", err)
			return ext.DispatcherActionNoop
		},
	})
	d.AddHandler(h.Conversation(nil))

	steps := []*gotgbot.Update{
		testutil.MessageUpdate(testUID, "/start"),
		testutil.CallbackUpdate(testUID, "tipo_fissa"),
		testutil.MessageUpdate(testUID, "0,145"),
		testutil.MessageUpdate(testUID, "72"),
		testutil.CallbackUpdate(testUID, "skip_scadenza_luce_x"), // non corrisponde al pattern ^...$
		testutil.CallbackUpdate(testUID, SkipScadenzaLuce),
		testutil.CallbackUpdate(testUID, "consumi_luce_no"),
		testutil.CallbackUpdate(testUID, "gas_no"),
	}
	for i, u := range steps {
		if err := d.ProcessUpdate(b, u, nil); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	u := h.Store.LoadUser(userIDString(testUID))
	if u == nil || u.Luce.Energia != 0.145 || u.Luce.Commercializzazione != 72 || u.Gas != nil || u.Luce.Scadenza != "" {
		t.Fatalf("utente salvato inatteso: %+v", u)
	}
	contains(t, fc.LastCall(t, "editMessageText").Text(), "Abbiamo finito!")
	// la callback con data diversa dallo skip non è gestita: 4 answer
	if n := len(fc.CallsTo("answerCallbackQuery")); n != 4 {
		t.Errorf("answerCallbackQuery %d volte, attese 4", n)
	}
}
