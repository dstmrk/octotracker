package registration

// Edge case non coperti dai test Python: errori API su ogni handler,
// update senza le parti attese ed errori dei formatter.

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
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
}

// TestAttivazioneSecondSendFails: se fallisce il secondo invio (prompt dei
// consumi) la scadenza resta salvata in user_data.
func TestAttivazioneSecondSendFails(t *testing.T) {
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
	if v, _ := h.UserData.Get(testUID, keyLuceScadenza); v != "2026-03-15" {
		t.Fatalf("luce_scadenza = %v", v)
	}
}

// TestHandlersWithoutExpectedUpdateParts: senza la parte dell'update usata
// dall'handler (callback, utente) l'handler restituisce un errore senza
// chiamare l'API.
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
		if _, err := stateOf(fn(b, msgCtx)); !errors.Is(err, tg.ErrNoCallback) {
			t.Errorf("%s: err %v", name, err)
		}
	}

	// update senza utente (es. post di un canale)
	noUser := newCtx(b, &gotgbot.Update{ChannelPost: &gotgbot.Message{Chat: gotgbot.Chat{Id: -1}, Text: "/start"}})
	if _, err := stateOf(h.Start(b, noUser)); !errors.Is(err, tg.ErrNoUser) {
		t.Errorf("start senza utente: %v", err)
	}
	if _, err := stateOf(h.SalvaEConferma(b, noUser, nil, true)); !errors.Is(err, tg.ErrNoUser) {
		t.Errorf("salva_e_conferma senza utente: %v", err)
	}
	if len(fc.Calls()) != 0 {
		t.Errorf("chiamate inattese: %v", fc.Calls())
	}
}

func TestSalvaEConfermaFormatErrors(t *testing.T) {
	cases := []struct {
		name  string
		ud    map[string]any
		query bool
	}{
		{"nan_query", map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": math.NaN(), "luce_comm": 1.0}, true},
		{"inf_update", map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": 1.0, "luce_comm": math.Inf(1)}, false},
		{"trioraria_incomplete", map[string]any{"luce_tipo": "fissa", "luce_fascia": "trioraria", "luce_energia": 1.0,
			"luce_comm": 1.0, "luce_consumo_f1": 1.0}, false},
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
			if _, err := stateOf(h.SalvaEConferma(b, ctx, query, true)); err == nil {
				t.Fatal("atteso l'errore del formatter")
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

// TestBuildUserDataMissingKeysLogs: le chiavi mancanti finiscono nel log
// e nell'errore, la conversazione termina con il messaggio di errore.
func TestBuildUserDataMissingKeysLogs(t *testing.T) {
	logs := captureLogs(t)
	h := newHandlers(t)
	setUserData(h, testUID, map[string]any{"luce_tipo": "fissa", "rating": 3})
	b, fc := testutil.NewBot(t)
	if s := mustState(t, h.SalvaEConferma(b, testutil.MessageContext(b, testUID, "1"), nil, true)); s != "END" {
		t.Fatalf("stato %q", s)
	}
	contains(t, fc.LastCall(t, "sendMessage").Text(), "errore durante il salvataggio")
	want := [][]string{
		{"handlers.registration", "ERROR", "Dati mancanti in context.user_data. Richiesti: [luce_tipo luce_fascia luce_energia luce_comm], " +
			"Mancanti: [luce_fascia luce_energia luce_comm], Disponibili: [luce_tipo rating]"},
		{"handlers.registration", "ERROR", "User 123456789: Errore costruzione dati utente - dati mancanti per luce: luce_fascia, luce_energia, luce_comm"},
	}
	compareLogs(t, "missing", want, logs.handlersRecords())
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
	fb := []ext.Handler{tg.NewCommand("cancel", nil)}
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

func TestValidateNumericInputRejectsNonFinite(t *testing.T) {
	for _, in := range []string{"nan", "NaN", "+nan", "-nan", "inf", "+inf", "Infinity", "1e400", "1,5e400"} {
		if v, ok, msg := ValidateNumericInput(in); ok || msg != "" {
			t.Errorf("%q: want rifiutato come non numerico, got (%v, %v, %q)", in, v, ok, msg)
		}
	}
	for _, in := range []string{"-inf", "-1e400"} {
		if _, ok, msg := ValidateNumericInput(in); ok || msg == "" {
			t.Errorf("%q: want rifiutato come negativo, got ok=%v msg=%q", in, ok, msg)
		}
	}
	for _, in := range []string{"1e300", "1e-400"} {
		if _, ok, _ := ValidateNumericInput(in); !ok {
			t.Errorf("%q: want accettato", in)
		}
	}
}
