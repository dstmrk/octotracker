package registration

// Port 1:1 dei test Python di tests/test_registration.py e dei test di
// registrazione di tests/test_bot.py (che ne sono un sottoinsieme con gli
// stessi nomi e scenari): ogni caso Python è una funzione qui sotto,
// eseguita con il nome Python in TestRegistrationPy e TestBotPyRegistration.

import (
	"strings"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// pyEnv sostituisce le fixture di conftest.py: temp_database, mock_update,
// mock_callback_query e mock_context (user_data vuoto).
type pyEnv struct {
	t  *testing.T
	h  *Handlers
	b  *gotgbot.Bot
	fc *testutil.FakeClient
}

func newPyEnv(t *testing.T) *pyEnv {
	t.Helper()
	b, fc := testutil.NewBot(t)
	return &pyEnv{t: t, h: newHandlers(t), b: b, fc: fc}
}

// msg è mock_update con message.text = text (utente 123456789).
func (e *pyEnv) msg(text string) *ext.Context { return testutil.MessageContext(e.b, testUID, text) }

// msgAs è mock_update con effective_user.id = uid.
func (e *pyEnv) msgAs(uid int64, text string) *ext.Context {
	return testutil.MessageContext(e.b, uid, text)
}

// cb è mock_callback_query con callback_query.data = data.
func (e *pyEnv) cb(data string) *ext.Context { return testutil.CallbackContext(e.b, testUID, data) }

func (e *pyEnv) cbAs(uid int64, data string) *ext.Context {
	return testutil.CallbackContext(e.b, uid, data)
}

func (e *pyEnv) setUD(data map[string]any) { setUserData(e.h, testUID, data) }

func (e *pyEnv) ud(key string) (any, bool) { return e.h.UserData.Get(testUID, key) }

// replies sono le chiamate a message.reply_text.
func (e *pyEnv) replies() []testutil.Call { return e.fc.CallsTo("sendMessage") }

// edits sono le chiamate a query.edit_message_text.
func (e *pyEnv) edits() []testutil.Call { return e.fc.CallsTo("editMessageText") }

// answers sono le chiamate a query.answer.
func (e *pyEnv) answers() []testutil.Call { return e.fc.CallsTo("answerCallbackQuery") }

// onlyReply verifica reply_text.assert_called_once() e restituisce il testo.
func (e *pyEnv) onlyReply() testutil.Call {
	e.t.Helper()
	r := e.replies()
	if len(r) != 1 {
		e.t.Fatalf("reply_text chiamato %d volte, atteso 1", len(r))
	}
	return r[0]
}

// onlyEdit verifica edit_message_text.assert_called_once().
func (e *pyEnv) onlyEdit() testutil.Call {
	e.t.Helper()
	r := e.edits()
	if len(r) != 1 {
		e.t.Fatalf("edit_message_text chiamato %d volte, atteso 1", len(r))
	}
	return r[0]
}

func (e *pyEnv) lastReply() testutil.Call {
	e.t.Helper()
	return e.fc.LastCall(e.t, "sendMessage")
}

func (e *pyEnv) wantState(err error, want string) {
	e.t.Helper()
	if got := mustState(e.t, err); got != want {
		e.t.Fatalf("stato %q, atteso %q", got, want)
	}
}

func (e *pyEnv) wantUD(key string, want any) {
	e.t.Helper()
	got, ok := e.ud(key)
	if !ok || got != want {
		e.t.Fatalf("user_data[%q] = %#v (presente=%v), atteso %#v", key, got, ok, want)
	}
}

func (e *pyEnv) wantNoUD(key string) {
	e.t.Helper()
	if v, ok := e.ud(key); ok {
		e.t.Fatalf("user_data[%q] = %#v, atteso assente", key, v)
	}
}

func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q non contiene %q", s, sub)
	}
}

func notContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("%q contiene %q", s, sub)
	}
}

func ptr(f float64) *float64 { return &f }

// ========== TEST COMANDI BASE ==========

func testStartNewUser(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.Start(e.b, e.msg("/start")), StateTipoTariffa)

	call := e.onlyReply()
	// Verifica messaggio contiene "Benvenuto"
	contains(t, call.Text(), "Benvenuto")
	// Verifica keyboard con Fissa/Variabile
	kb := call.InlineKeyboard()
	if len(kb) != 1 || len(kb[0]) != 2 || kb[0][0].CallbackData != "tipo_fissa" || kb[0][1].CallbackData != "tipo_variabile" {
		t.Fatalf("tastiera inattesa: %+v", kb)
	}
}

func testUpdateCommand(t *testing.T) {
	e := newPyEnv(t)
	// Prepara dati esistenti nel database
	e.h.Store.SaveUser("123456789", database.User{Luce: database.Utility{
		Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0,
	}})

	// /update deve chiamare start() che riavvia registrazione
	e.wantState(e.h.Start(e.b, e.msg("/update")), StateTipoTariffa)
	call := e.onlyReply()
	contains(t, strings.ToLower(call.Text()), "tipo di tariffa")
	contains(t, call.Text(), "Aggiorniamo")
}

func testStartWithTelegramChannel(t *testing.T) {
	e := newPyEnv(t)
	// TELEGRAM_CHANNEL="@octotracker_updates" (già ripulito con strip())
	e.h.TelegramChannel = "@octotracker_updates"

	e.wantState(e.h.Start(e.b, e.msg("/start")), StateTipoTariffa)
	text := e.onlyReply().Text()
	contains(t, text, "Per avere aggiornamenti sulle nuove funzionalità")
	contains(t, text, "@octotracker_updates")
	contains(t, text, "https://t.me/octotracker_updates")
}

func testStartWithoutTelegramChannel(t *testing.T) {
	e := newPyEnv(t)
	e.h.TelegramChannel = ""

	e.wantState(e.h.Start(e.b, e.msg("/start")), StateTipoTariffa)
	text := e.onlyReply().Text()
	notContains(t, text, "Per avere aggiornamenti sulle nuove funzionalità")
	if strings.Contains(text, "@") && !strings.Contains(text, "tipo di tariffa") {
		t.Fatalf("@ inatteso: %q", text)
	}
}

func testStartWithEmptyTelegramChannel(t *testing.T) {
	e := newPyEnv(t)
	// TELEGRAM_CHANNEL="   " → strip() → ""
	e.h.TelegramChannel = strings.TrimSpace("   ")

	e.wantState(e.h.Start(e.b, e.msg("/start")), StateTipoTariffa)
	notContains(t, e.onlyReply().Text(), "Per avere aggiornamenti sulle nuove funzionalità")
}

// ========== TEST FLUSSO CONVERSAZIONE ==========

func testTipoTariffaFissa(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.TipoTariffa(e.b, e.cb("tipo_fissa")), StateLuceEnergia)
	e.wantUD("is_variabile", false)
	e.wantUD("luce_tipo", "fissa")
	e.wantUD("luce_fascia", "monoraria")

	if len(e.answers()) != 1 {
		t.Fatalf("answer chiamato %d volte", len(e.answers()))
	}
	e.onlyEdit()
}

func testTipoTariffaVariabile(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.TipoTariffa(e.b, e.cb("tipo_variabile")), StateLuceTipoVariabile)
	e.wantUD("is_variabile", true)
	if len(e.answers()) != 1 {
		t.Fatalf("answer chiamato %d volte", len(e.answers()))
	}
}

func testLuceTipoVariabileMono(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.LuceTipoVariabile(e.b, e.cb("luce_mono")), StateLuceEnergia)
	e.wantUD("luce_tipo", "variabile")
	e.wantUD("luce_fascia", "monoraria")
	// gas_tipo non viene più impostato qui, verrà chiesto separatamente in GAS_TIPO
	e.wantNoUD("gas_tipo")
}

func testLuceTipoVariabileTri(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.LuceTipoVariabile(e.b, e.cb("luce_tri")), StateLuceEnergia)
	e.wantUD("luce_tipo", "variabile")
	e.wantUD("luce_fascia", "trioraria")
}

// ========== TEST INPUT VALIDI ==========

func testLuceEnergiaValidInput(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.LuceEnergia(e.b, e.msg("0,145")), StateLuceComm)
	e.wantUD("luce_energia", 0.145)
	e.onlyReply()
}

func testLuceEnergiaDotSeparator(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.LuceEnergia(e.b, e.msg("0.145")), StateLuceComm)
	e.wantUD("luce_energia", 0.145)
}

func testLuceCommValidInput(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.LuceComm(e.b, e.msg("72")), StateVuoiConsumiLuce)
	e.wantUD("luce_comm", 72.0)
	e.onlyReply()
}

// ========== TEST INPUT NON VALIDI ==========

func testLuceEnergiaInvalidString(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"is_variabile": false})
	// Deve tornare allo stesso stato
	e.wantState(e.h.LuceEnergia(e.b, e.msg("abc")), StateLuceEnergia)
	// Deve mostrare errore
	text := e.onlyReply().Text()
	contains(t, text, "❌")
	contains(t, strings.ToLower(text), "valido")
}

func testLuceEnergiaEmptyString(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"is_variabile": false})
	e.wantState(e.h.LuceEnergia(e.b, e.msg("")), StateLuceEnergia)
	e.onlyReply()
}

func testLuceEnergiaSpecialChars(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"is_variabile": false})
	// ValueError perché € non è valido
	e.wantState(e.h.LuceEnergia(e.b, e.msg("0,14€")), StateLuceEnergia)
	e.onlyReply()
}

func testLuceCommInvalidInput(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.LuceComm(e.b, e.msg("settantadue")), StateLuceComm)
	contains(t, e.onlyReply().Text(), "❌")
}

func testGasEnergiaInvalidInput(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"gas_tipo": "fissa"})
	e.wantState(e.h.GasEnergia(e.b, e.msg("invalid")), StateGasEnergia)
	e.onlyReply()
}

func testGasCommInvalidInput(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.GasComm(e.b, e.msg("xyz123")), StateGasComm)
	e.onlyReply()
}

// ========== TEST FLUSSI COMPLETI ==========

func testHasGasYes(t *testing.T) {
	e := newPyEnv(t)
	// Ora chiede tipo tariffa gas
	e.wantState(e.h.HaGas(e.b, e.cb("gas_si")), StateGasTipo)
	// Verifica che mostri la domanda sul tipo tariffa gas
	contains(t, e.onlyEdit().Text(), "tipo di tariffa")
}

func testHasGasNo(t *testing.T) {
	e := newPyEnv(t)
	// Setup dati luce
	e.setUD(map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": 0.145, "luce_comm": 72.0})

	// Deve salvare e terminare conversazione
	e.wantState(e.h.HaGas(e.b, e.cb("gas_no")), "END")

	// Verifica salvataggio nel database
	u := e.h.Store.LoadUser("123456789")
	if u == nil {
		t.Fatal("utente non salvato")
	}
	// Bot salva gas: None quando utente non ha gas
	if u.Gas != nil {
		t.Fatalf("gas = %+v, atteso None", u.Gas)
	}
}

func testGasTipoTariffaFissa(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.GasTipoTariffa(e.b, e.cb("gas_tipo_fissa")), StateGasEnergia)
	e.wantUD("gas_tipo", "fissa")
	e.wantUD("gas_fascia", "monoraria")
	// Verifica messaggio per gas fisso
	contains(t, e.onlyEdit().Text(), "Gas fisso")
}

func testGasTipoTariffaVariabile(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.GasTipoTariffa(e.b, e.cb("gas_tipo_variabile")), StateGasEnergia)
	e.wantUD("gas_tipo", "variabile")
	e.wantUD("gas_fascia", "monoraria")
	// Verifica messaggio per gas variabile
	msg := e.onlyEdit().Text()
	contains(t, msg, "Gas variabile")
	contains(t, msg, "PSV")
}

func testGasEnergiaValidInput(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.GasEnergia(e.b, e.msg("0,456")), StateGasComm)
	e.wantUD("gas_energia", 0.456)
}

// completeGasFlow esegue gas_comm → (skip attivazione) → consumi gas "No".
func completeGasFlow(e *pyEnv, wantAfterComm string) {
	e.t.Helper()
	// Simula step gas_comm
	e.wantState(e.h.GasComm(e.b, e.msgAs(testUID, "84")), wantAfterComm)
	if wantAfterComm == StateGasAttivazione {
		// Simula "Salta" alla domanda data attivazione gas
		e.wantState(e.h.SkipGasAttivazione(e.b, e.cbAs(testUID, "skip_scadenza_gas")), StateVuoiConsumiGas)
	}
	// Simula risposta "No" alla domanda consumo gas
	e.wantState(e.h.VuoiConsumiGas(e.b, e.cbAs(testUID, "consumi_gas_no")), "END")
}

func testCompleteFlowFissaWithGas(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{
		"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": 0.145, "luce_comm": 72.0,
		"gas_tipo": "fissa", "gas_fascia": "monoraria", "gas_energia": 0.456, "gas_comm": 84.0,
	})
	// Gas fissa → chiede la data di attivazione (reminder scadenza)
	completeGasFlow(e, StateGasAttivazione)

	// Verifica salvataggio completo nel database
	u := e.h.Store.LoadUser("123456789")
	if u == nil || u.Luce.Energia != 0.145 || u.Gas == nil || u.Gas.Energia != 0.456 {
		t.Fatalf("utente salvato inatteso: %+v", u)
	}
}

func testCompleteFlowMixedLuceFissaGasVariabile(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{
		"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": 0.145, "luce_comm": 72.0,
		"gas_tipo": "variabile", "gas_fascia": "monoraria", "gas_energia": 0.08, "gas_comm": 84.0,
	})
	completeGasFlow(e, StateVuoiConsumiGas)

	u := e.h.Store.LoadUser("123456789")
	if u == nil {
		t.Fatal("utente non salvato")
	}
	// Luce fissa
	if u.Luce.Tipo != "fissa" || u.Luce.Energia != 0.145 {
		t.Fatalf("luce inattesa: %+v", u.Luce)
	}
	// Gas variabile (combinazione mista!)
	if u.Gas == nil || u.Gas.Tipo != "variabile" || u.Gas.Energia != 0.08 {
		t.Fatalf("gas inatteso: %+v", u.Gas)
	}
}

func testCompleteFlowMixedLuceVariabileGasFissa(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{
		"luce_tipo": "variabile", "luce_fascia": "trioraria", "luce_energia": 0.025, "luce_comm": 72.0,
		"gas_tipo": "fissa", "gas_fascia": "monoraria", "gas_energia": 0.456, "gas_comm": 84.0,
	})
	completeGasFlow(e, StateGasAttivazione)

	u := e.h.Store.LoadUser("123456789")
	if u == nil {
		t.Fatal("utente non salvato")
	}
	// Luce variabile
	if u.Luce.Tipo != "variabile" || u.Luce.Fascia != "trioraria" || u.Luce.Energia != 0.025 {
		t.Fatalf("luce inattesa: %+v", u.Luce)
	}
	// Gas fisso (combinazione mista!)
	if u.Gas == nil || u.Gas.Tipo != "fissa" || u.Gas.Energia != 0.456 {
		t.Fatalf("gas inatteso: %+v", u.Gas)
	}
}

// ========== TEST EDGE CASES ==========

func testNegativeValuesRejected(t *testing.T) {
	e := newPyEnv(t)
	// Con validazione, il bot ora rifiuta negativi
	e.wantState(e.h.LuceEnergia(e.b, e.msg("-0.5")), StateLuceEnergia)
	e.wantNoUD("luce_energia")
	contains(t, e.onlyReply().Text(), "maggiore o uguale a zero")
}

func testVeryLargeNumbers(t *testing.T) {
	e := newPyEnv(t)
	// Il bot accetta qualunque numero valido
	e.wantState(e.h.LuceEnergia(e.b, e.msg("999999.99")), StateLuceComm)
	e.wantUD("luce_energia", 999999.99)
}

func testZeroValues(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.LuceEnergia(e.b, e.msg("0")), StateLuceComm)
	e.wantUD("luce_energia", 0.0)
}

// ========== TEST CONFIRMATION MESSAGE WITH CONSUMPTION ==========

func testFormatConfirmationMessageWithLuceMonorariaConsumption(t *testing.T) {
	msg := formatConfirmationMessage(database.User{Luce: database.Utility{
		Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0, ConsumoF1: ptr(2700.0),
	}})
	contains(t, msg, "Consumo: <b>2700</b> kWh/anno")
	contains(t, msg, "Abbiamo finito!")
	contains(t, msg, "Luce (Fissa Monoraria)")
}

func testFormatConfirmationMessageWithLuceTriorariaConsumption(t *testing.T) {
	msg := formatConfirmationMessage(database.User{Luce: database.Utility{
		Tipo: "variabile", Fascia: "trioraria", Energia: 0.025, Commercializzazione: 72.0,
		ConsumoF1: ptr(900.0), ConsumoF2: ptr(850.0), ConsumoF3: ptr(950.0),
	}})
	contains(t, msg, "Consumo: <b>2700</b> kWh/anno")
	contains(t, msg, "F1: 900 kWh")
	contains(t, msg, "F2: 850 kWh")
	contains(t, msg, "F3: 950 kWh")
	contains(t, msg, "Luce (Variabile Trioraria)")
}

func testFormatConfirmationMessageWithGasConsumption(t *testing.T) {
	msg := formatConfirmationMessage(database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0},
		Gas: &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.456, Commercializzazione: 84.0,
			ConsumoAnnuo: ptr(1200.0)},
	})
	contains(t, msg, "Consumo: <b>1200</b> Smc/anno")
	contains(t, msg, "Gas (Fissa Monoraria)")
}

func testFormatConfirmationMessageWithoutConsumption(t *testing.T) {
	msg := formatConfirmationMessage(database.User{Luce: database.Utility{
		Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0,
	}})
	notContains(t, msg, "Consumo")
	contains(t, msg, "Abbiamo finito!")
}

// ========== TEST CONSUMPTION COLLECTION FLOW ==========

func testVuoiConsumiLuceYesMonoraria(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"luce_fascia": "monoraria"})
	e.wantState(e.h.VuoiConsumiLuce(e.b, e.cb("consumi_luce_si")), StateLuceConsumoF1)
	contains(t, e.onlyEdit().Text(), "consumo annuo totale di energia elettrica")
}

func testLuceConsumoF1MonorariaValid(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"luce_fascia": "monoraria"})
	err := e.h.LuceConsumoF1(e.b, e.msg("2700"))
	e.wantUD("luce_consumo_f1", 2700.0)
	// Per monoraria dovrebbe andare a HA_GAS
	e.wantState(err, StateHaGas)
}

func testVuoiConsumiGasYes(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.VuoiConsumiGas(e.b, e.cbAs(testUID, "consumi_gas_si")), StateGasConsumo)
	contains(t, e.onlyEdit().Text(), "consumo annuo di gas in Smc")
}

func testGasConsumoValid(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{
		"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": 0.145, "luce_comm": 72.0,
		"gas_tipo": "fissa", "gas_fascia": "monoraria", "gas_energia": 0.456, "gas_comm": 84.0,
	})
	err := e.h.GasConsumo(e.b, e.msgAs(testUID, "1200"))
	e.wantUD("gas_consumo_annuo", 1200.0)
	e.wantState(err, "END") // Fine conversazione

	// Verifica salvataggio nel database
	u := e.h.Store.LoadUser("123456789")
	if u == nil || u.Gas == nil || u.Gas.ConsumoAnnuo == nil || *u.Gas.ConsumoAnnuo != 1200.0 {
		t.Fatalf("utente salvato inatteso: %+v", u)
	}
}

// ========== TEST ERROR HANDLING AND EDGE CASES ==========

func testVuoiConsumiLuceNo(t *testing.T) {
	e := newPyEnv(t)
	e.wantState(e.h.VuoiConsumiLuce(e.b, e.cb("consumi_luce_no")), StateHaGas)
	if len(e.answers()) != 1 {
		t.Fatalf("answer chiamato %d volte", len(e.answers()))
	}
	e.onlyEdit()
}

func testVuoiConsumiLuceYesTrioraria(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"luce_fascia": "trioraria"})
	e.wantState(e.h.VuoiConsumiLuce(e.b, e.cb("consumi_luce_si")), StateLuceConsumoF1)
	if len(e.answers()) != 1 {
		t.Fatalf("answer chiamato %d volte", len(e.answers()))
	}
	// Verifica che il messaggio menzioni F1
	contains(t, e.onlyEdit().Text(), "F1")
}

// numericCase esegue un handler numerico e verifica stato e risposta.
func numericCase(t *testing.T, ud map[string]any, handler func(*Handlers) func(*gotgbot.Bot, *ext.Context) error,
	text, wantState, wantSub string, lower bool) *pyEnv {
	t.Helper()
	e := newPyEnv(t)
	e.setUD(ud)
	e.wantState(handler(e.h)(e.b, e.msg(text)), wantState)
	reply := e.onlyReply().Text()
	if lower {
		reply = strings.ToLower(reply)
	}
	contains(t, reply, wantSub)
	return e
}

var (
	hLuceEnergia   = func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.LuceEnergia }
	hLuceComm      = func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.LuceComm }
	hLuceConsumoF1 = func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.LuceConsumoF1 }
	hLuceConsumoF2 = func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.LuceConsumoF2 }
	hLuceConsumoF3 = func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.LuceConsumoF3 }
	hGasEnergia    = func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.GasEnergia }
	hGasComm       = func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.GasComm }
	hGasConsumo    = func(h *Handlers) func(*gotgbot.Bot, *ext.Context) error { return h.GasConsumo }
)

func testLuceConsumoF1Negative(t *testing.T) {
	numericCase(t, map[string]any{"luce_fascia": "monoraria"}, hLuceConsumoF1, "-100", StateLuceConsumoF1, "maggiore o uguale a zero", true)
}

func testLuceConsumoF1ValueError(t *testing.T) {
	numericCase(t, map[string]any{"luce_fascia": "monoraria"}, hLuceConsumoF1, "abc", StateLuceConsumoF1, "numero valido", false)
}

func testLuceConsumoF1TriorariaValid(t *testing.T) {
	e := numericCase(t, map[string]any{"luce_fascia": "trioraria"}, hLuceConsumoF1, "900", StateLuceConsumoF2, "F2", false)
	e.wantUD("luce_consumo_f1", 900.0)
}

func testLuceConsumoF2Negative(t *testing.T) {
	numericCase(t, nil, hLuceConsumoF2, "-100", StateLuceConsumoF2, "maggiore o uguale a zero", true)
}

func testLuceConsumoF2ValueError(t *testing.T) {
	numericCase(t, nil, hLuceConsumoF2, "xyz", StateLuceConsumoF2, "numero valido", false)
}

func testLuceConsumoF2Valid(t *testing.T) {
	e := numericCase(t, nil, hLuceConsumoF2, "850", StateLuceConsumoF3, "F3", false)
	e.wantUD("luce_consumo_f2", 850.0)
}

func testLuceConsumoF3Negative(t *testing.T) {
	numericCase(t, nil, hLuceConsumoF3, "-100", StateLuceConsumoF3, "maggiore o uguale a zero", true)
}

func testLuceConsumoF3ValueError(t *testing.T) {
	numericCase(t, nil, hLuceConsumoF3, "invalid", StateLuceConsumoF3, "numero valido", false)
}

func testLuceConsumoF3Valid(t *testing.T) {
	e := numericCase(t, nil, hLuceConsumoF3, "950", StateHaGas, "", false)
	e.wantUD("luce_consumo_f3", 950.0)
}

func testGasEnergiaNegative(t *testing.T) {
	numericCase(t, nil, hGasEnergia, "-0.5", StateGasEnergia, "maggiore o uguale a zero", true)
}

func testGasEnergiaValueErrorVariabile(t *testing.T) {
	e := numericCase(t, map[string]any{"gas_tipo": "variabile"}, hGasEnergia, "invalid", StateGasEnergia, "numero valido", false)
	contains(t, e.lastReply().Text(), "0,08") // Esempio per variabile
}

func testGasEnergiaValueErrorFissa(t *testing.T) {
	e := numericCase(t, map[string]any{"gas_tipo": "fissa"}, hGasEnergia, "invalid", StateGasEnergia, "numero valido", false)
	contains(t, e.lastReply().Text(), "0,456") // Esempio per fissa
}

func testGasCommNegative(t *testing.T) {
	numericCase(t, nil, hGasComm, "-50", StateGasComm, "maggiore o uguale a zero", true)
}

// ========== TEST VALIDAZIONE INPUT NUMERICI (PROTEZIONE ATTACCHI) ==========

// wantValid verifica (valore, None).
func wantValid(t *testing.T, text string, want float64) {
	t.Helper()
	value, ok, errMsg := ValidateNumericInput(text)
	if !ok || value != want || errMsg != "" {
		t.Fatalf("validate(%q) = (%v, %v, %q), atteso (%v, None)", text, value, ok, errMsg, want)
	}
}

// wantInvalid verifica (None, errore) dove errSub "" significa errore None.
func wantInvalid(t *testing.T, text, errSub string) string {
	t.Helper()
	_, ok, errMsg := ValidateNumericInput(text)
	if ok {
		t.Fatalf("validate(%q): atteso None", text)
	}
	if errSub == "" {
		if errMsg != "" {
			t.Fatalf("validate(%q): errore %q, atteso None", text, errMsg)
		}
		return errMsg
	}
	if errMsg == "" {
		t.Fatalf("validate(%q): atteso un errore", text)
	}
	contains(t, strings.ToLower(errMsg), errSub)
	return errMsg
}

func testValidateNumericInputValid(t *testing.T)          { wantValid(t, "123.45", 123.45) }
func testValidateNumericInputValidWithComma(t *testing.T) { wantValid(t, "123,45", 123.45) }
func testValidateNumericInputZero(t *testing.T)           { wantValid(t, "0", 0.0) }
func testValidateNumericInputSmallDecimal(t *testing.T)   { wantValid(t, "0.0088", 0.0088) }

func testValidateNumericInputNegative(t *testing.T) {
	wantInvalid(t, "-10", "maggiore o uguale a zero")
}

func testValidateNumericInputTooLong(t *testing.T) {
	// Input con 11 caratteri (max 10)
	errMsg := wantInvalid(t, "12345678901", "troppo lungo")
	contains(t, errMsg, "10") // Verifica menzione del limite
}

func testValidateNumericInputVeryLong(t *testing.T) {
	// Input con 100 caratteri
	wantInvalid(t, strings.Repeat("1", 100), "troppo lungo")
}

func testValidateNumericInputInvalidString(t *testing.T) {
	// None significa errore di conversione, gestito dal chiamante
	wantInvalid(t, "abc", "")
}

func testValidateNumericInputEmpty(t *testing.T)        { wantInvalid(t, "", "") }
func testValidateNumericInputSpecialChars(t *testing.T) { wantInvalid(t, "12.34€", "") }

func testValidateNumericInputAtMaxLength(t *testing.T) {
	// 10 caratteri esatti - dovrebbe essere valido
	wantValid(t, "1234567.89", 1234567.89)
}

func testLuceEnergiaTooLongInput(t *testing.T) {
	// Input con 50 cifre
	e := numericCase(t, map[string]any{"is_variabile": false}, hLuceEnergia, strings.Repeat("1", 50), StateLuceEnergia, "troppo lungo", true)
	contains(t, e.lastReply().Text(), "10")
}

func testLuceCommTooLongInput(t *testing.T) {
	numericCase(t, nil, hLuceComm, strings.Repeat("9", 20), StateLuceComm, "troppo lungo", true)
}

func testLuceConsumoF1TooLongInput(t *testing.T) {
	numericCase(t, nil, hLuceConsumoF1, strings.Repeat("8", 15), StateLuceConsumoF1, "troppo lungo", true)
}

func testLuceConsumoF2TooLongInput(t *testing.T) {
	numericCase(t, nil, hLuceConsumoF2, strings.Repeat("7", 12), StateLuceConsumoF2, "troppo lungo", true)
}

func testLuceConsumoF3TooLongInput(t *testing.T) {
	numericCase(t, nil, hLuceConsumoF3, strings.Repeat("6", 11), StateLuceConsumoF3, "troppo lungo", true)
}

func testGasEnergiaTooLongInput(t *testing.T) {
	numericCase(t, map[string]any{"gas_tipo": "fissa"}, hGasEnergia, strings.Repeat("5", 30), StateGasEnergia, "troppo lungo", true)
}

func testGasCommTooLongInput(t *testing.T) {
	numericCase(t, nil, hGasComm, strings.Repeat("4", 25), StateGasComm, "troppo lungo", true)
}

func testGasConsumoTooLongInput(t *testing.T) {
	numericCase(t, nil, hGasConsumo, strings.Repeat("3", 40), StateGasConsumo, "troppo lungo", true)
}

func testSalvaEConfermaMissingDataReturnsEnd(t *testing.T) {
	e := newPyEnv(t)
	// context.user_data è vuoto: _build_user_data solleva KeyError
	e.wantState(e.h.SalvaEConferma(e.b, e.msg(""), nil, true), "END")
	contains(t, strings.ToLower(e.onlyReply().Text()), "errore")
}

// ========== TEST STEP DATA ATTIVAZIONE (REMINDER SCADENZA) ==========

func testLuceCommFissaAsksActivation(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria"})
	e.wantState(e.h.LuceComm(e.b, e.msg("72")), StateLuceAttivazione)
	contains(t, e.lastReply().Text(), "data di attivazione")
}

func testLuceCommVariabileSkipsActivation(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"luce_tipo": "variabile", "luce_fascia": "monoraria"})
	e.wantState(e.h.LuceComm(e.b, e.msg("72")), StateVuoiConsumiLuce)
	e.wantNoUD("luce_scadenza")
}

func testLuceAttivazioneValidDate(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria"})
	e.wantState(e.h.LuceAttivazione(e.b, e.msg("15/03/2025")), StateVuoiConsumiLuce)
	e.wantUD("luce_scadenza", "2026-03-15")
}

func testLuceAttivazioneInvalidDateReasks(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria"})
	e.wantState(e.h.LuceAttivazione(e.b, e.msg("non è una data")), StateLuceAttivazione)
	e.wantNoUD("luce_scadenza")
	if got := e.lastReply().Text(); got != MsgDateInvalid {
		t.Fatalf("risposta %q, attesa %q", got, MsgDateInvalid)
	}
}

func testSkipLuceAttivazione(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria"})
	e.wantState(e.h.SkipLuceAttivazione(e.b, e.cb("skip_scadenza_luce")), StateVuoiConsumiLuce)
	e.wantNoUD("luce_scadenza")
}

func testGasCommFissaAsksActivation(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{
		"luce_tipo": "variabile", "luce_fascia": "monoraria", "luce_energia": 0.02, "luce_comm": 72.0,
		"gas_tipo": "fissa", "gas_fascia": "monoraria", "gas_energia": 0.456,
	})
	e.wantState(e.h.GasComm(e.b, e.msg("84")), StateGasAttivazione)
}

func testGasAttivazioneValidDate(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"gas_tipo": "fissa", "gas_fascia": "monoraria"})
	e.wantState(e.h.GasAttivazione(e.b, e.msg("01/06/2025")), StateVuoiConsumiGas)
	e.wantUD("gas_scadenza", "2026-06-01")
}

func testGasAttivazioneInvalidDateReasks(t *testing.T) {
	e := newPyEnv(t)
	e.setUD(map[string]any{"gas_tipo": "fissa", "gas_fascia": "monoraria"})
	e.wantState(e.h.GasAttivazione(e.b, e.msg("99/99/9999")), StateGasAttivazione)
	e.wantNoUD("gas_scadenza")
}

func testConfirmationMessageShowsScadenza(t *testing.T) {
	msg := formatConfirmationMessage(database.User{Luce: database.Utility{
		Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0, Scadenza: "2026-03-15",
	}})
	contains(t, msg, "Scadenza offerta")
	contains(t, msg, "15/03/2026")
}

func testFullLuceFissaFlowWithActivation(t *testing.T) {
	e := newPyEnv(t)
	const uid int64 = 555
	setUserData(e.h, uid, map[string]any{"luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": 0.145})

	// commercializzazione → chiede attivazione
	e.wantState(e.h.LuceComm(e.b, e.msgAs(uid, "72")), StateLuceAttivazione)
	// data attivazione → scadenza salvata, va ai consumi
	e.wantState(e.h.LuceAttivazione(e.b, e.msgAs(uid, "15/03/2025")), StateVuoiConsumiLuce)
	// risposta "No" ai consumi luce → chiede gas
	e.wantState(e.h.VuoiConsumiLuce(e.b, e.cbAs(uid, "consumi_luce_no")), StateHaGas)
	// "No" al gas → salvataggio
	e.wantState(e.h.HaGas(e.b, e.cbAs(uid, "gas_no")), "END")

	// Verifica scadenza persistita
	saved := e.h.Store.LoadUser("555")
	if saved == nil || saved.Luce.Scadenza != "2026-03-15" {
		t.Fatalf("scadenza non persistita: %+v", saved)
	}
}

// ========== TABELLE DEI TEST PYTHON ==========

type pyTest struct {
	name string
	fn   func(*testing.T)
}

// registrationPyTests: tutti gli 82 test di tests/test_registration.py.
var registrationPyTests = []pyTest{
	{"test_start_new_user", testStartNewUser},
	{"test_update_command", testUpdateCommand},
	{"test_start_with_telegram_channel", testStartWithTelegramChannel},
	{"test_start_without_telegram_channel", testStartWithoutTelegramChannel},
	{"test_start_with_empty_telegram_channel", testStartWithEmptyTelegramChannel},
	{"test_tipo_tariffa_fissa", testTipoTariffaFissa},
	{"test_tipo_tariffa_variabile", testTipoTariffaVariabile},
	{"test_luce_tipo_variabile_mono", testLuceTipoVariabileMono},
	{"test_luce_tipo_variabile_tri", testLuceTipoVariabileTri},
	{"test_luce_energia_valid_input", testLuceEnergiaValidInput},
	{"test_luce_energia_dot_separator", testLuceEnergiaDotSeparator},
	{"test_luce_comm_valid_input", testLuceCommValidInput},
	{"test_luce_energia_invalid_string", testLuceEnergiaInvalidString},
	{"test_luce_energia_empty_string", testLuceEnergiaEmptyString},
	{"test_luce_energia_special_chars", testLuceEnergiaSpecialChars},
	{"test_luce_comm_invalid_input", testLuceCommInvalidInput},
	{"test_gas_energia_invalid_input", testGasEnergiaInvalidInput},
	{"test_gas_comm_invalid_input", testGasCommInvalidInput},
	{"test_has_gas_yes", testHasGasYes},
	{"test_has_gas_no", testHasGasNo},
	{"test_gas_tipo_tariffa_fissa", testGasTipoTariffaFissa},
	{"test_gas_tipo_tariffa_variabile", testGasTipoTariffaVariabile},
	{"test_gas_energia_valid_input", testGasEnergiaValidInput},
	{"test_complete_flow_fissa_with_gas", testCompleteFlowFissaWithGas},
	{"test_complete_flow_mixed_luce_fissa_gas_variabile", testCompleteFlowMixedLuceFissaGasVariabile},
	{"test_complete_flow_mixed_luce_variabile_gas_fissa", testCompleteFlowMixedLuceVariabileGasFissa},
	{"test_negative_values_rejected", testNegativeValuesRejected},
	{"test_very_large_numbers", testVeryLargeNumbers},
	{"test_zero_values", testZeroValues},
	{"test_format_confirmation_message_with_luce_monoraria_consumption", testFormatConfirmationMessageWithLuceMonorariaConsumption},
	{"test_format_confirmation_message_with_luce_trioraria_consumption", testFormatConfirmationMessageWithLuceTriorariaConsumption},
	{"test_format_confirmation_message_with_gas_consumption", testFormatConfirmationMessageWithGasConsumption},
	{"test_format_confirmation_message_without_consumption", testFormatConfirmationMessageWithoutConsumption},
	{"test_vuoi_consumi_luce_yes_monoraria", testVuoiConsumiLuceYesMonoraria},
	{"test_luce_consumo_f1_monoraria_valid", testLuceConsumoF1MonorariaValid},
	{"test_vuoi_consumi_gas_yes", testVuoiConsumiGasYes},
	{"test_gas_consumo_valid", testGasConsumoValid},
	{"test_vuoi_consumi_luce_no", testVuoiConsumiLuceNo},
	{"test_vuoi_consumi_luce_yes_trioraria", testVuoiConsumiLuceYesTrioraria},
	{"test_luce_consumo_f1_negative", testLuceConsumoF1Negative},
	{"test_luce_consumo_f1_value_error", testLuceConsumoF1ValueError},
	{"test_luce_consumo_f1_trioraria_valid", testLuceConsumoF1TriorariaValid},
	{"test_luce_consumo_f2_negative", testLuceConsumoF2Negative},
	{"test_luce_consumo_f2_value_error", testLuceConsumoF2ValueError},
	{"test_luce_consumo_f2_valid", testLuceConsumoF2Valid},
	{"test_luce_consumo_f3_negative", testLuceConsumoF3Negative},
	{"test_luce_consumo_f3_value_error", testLuceConsumoF3ValueError},
	{"test_luce_consumo_f3_valid", testLuceConsumoF3Valid},
	{"test_gas_energia_negative", testGasEnergiaNegative},
	{"test_gas_energia_value_error_variabile", testGasEnergiaValueErrorVariabile},
	{"test_gas_energia_value_error_fissa", testGasEnergiaValueErrorFissa},
	{"test_gas_comm_negative", testGasCommNegative},
	{"test_validate_numeric_input_valid", testValidateNumericInputValid},
	{"test_validate_numeric_input_valid_with_comma", testValidateNumericInputValidWithComma},
	{"test_validate_numeric_input_zero", testValidateNumericInputZero},
	{"test_validate_numeric_input_small_decimal", testValidateNumericInputSmallDecimal},
	{"test_validate_numeric_input_negative", testValidateNumericInputNegative},
	{"test_validate_numeric_input_too_long", testValidateNumericInputTooLong},
	{"test_validate_numeric_input_very_long", testValidateNumericInputVeryLong},
	{"test_validate_numeric_input_invalid_string", testValidateNumericInputInvalidString},
	{"test_validate_numeric_input_empty", testValidateNumericInputEmpty},
	{"test_validate_numeric_input_special_chars", testValidateNumericInputSpecialChars},
	{"test_validate_numeric_input_at_max_length", testValidateNumericInputAtMaxLength},
	{"test_luce_energia_too_long_input", testLuceEnergiaTooLongInput},
	{"test_luce_comm_too_long_input", testLuceCommTooLongInput},
	{"test_luce_consumo_f1_too_long_input", testLuceConsumoF1TooLongInput},
	{"test_luce_consumo_f2_too_long_input", testLuceConsumoF2TooLongInput},
	{"test_luce_consumo_f3_too_long_input", testLuceConsumoF3TooLongInput},
	{"test_gas_energia_too_long_input", testGasEnergiaTooLongInput},
	{"test_gas_comm_too_long_input", testGasCommTooLongInput},
	{"test_gas_consumo_too_long_input", testGasConsumoTooLongInput},
	{"test_salva_e_conferma_missing_data_returns_end", testSalvaEConfermaMissingDataReturnsEnd},
	{"test_luce_comm_fissa_asks_activation", testLuceCommFissaAsksActivation},
	{"test_luce_comm_variabile_skips_activation", testLuceCommVariabileSkipsActivation},
	{"test_luce_attivazione_valid_date", testLuceAttivazioneValidDate},
	{"test_luce_attivazione_invalid_date_reasks", testLuceAttivazioneInvalidDateReasks},
	{"test_skip_luce_attivazione", testSkipLuceAttivazione},
	{"test_gas_comm_fissa_asks_activation", testGasCommFissaAsksActivation},
	{"test_gas_attivazione_valid_date", testGasAttivazioneValidDate},
	{"test_gas_attivazione_invalid_date_reasks", testGasAttivazioneInvalidDateReasks},
	{"test_confirmation_message_shows_scadenza", testConfirmationMessageShowsScadenza},
	{"test_full_luce_fissa_flow_with_activation", testFullLuceFissaFlowWithActivation},
}

// botPyRegistrationTests: i 66 test di registrazione di tests/test_bot.py
// (test_start_new_user, test_update_command e la sezione da "TEST FLUSSO
// CONVERSAZIONE" in poi). Hanno gli stessi nomi e scenari dei test di
// test_registration.py; dove test_bot.py ha asserzioni più deboli
// (test_has_gas_yes, test_gas_tipo_tariffa_*) la funzione Go condivisa
// verifica anche quelle più forti.
var botPyRegistrationTests = []string{
	"test_start_new_user", "test_update_command",
	"test_tipo_tariffa_fissa", "test_tipo_tariffa_variabile", "test_luce_tipo_variabile_mono",
	"test_luce_tipo_variabile_tri", "test_luce_energia_valid_input", "test_luce_energia_dot_separator",
	"test_luce_comm_valid_input", "test_luce_energia_invalid_string", "test_luce_energia_empty_string",
	"test_luce_energia_special_chars", "test_luce_comm_invalid_input", "test_gas_energia_invalid_input",
	"test_gas_comm_invalid_input", "test_has_gas_yes", "test_gas_tipo_tariffa_fissa",
	"test_gas_tipo_tariffa_variabile", "test_has_gas_no", "test_gas_energia_valid_input",
	"test_complete_flow_fissa_with_gas", "test_negative_values_rejected", "test_very_large_numbers",
	"test_zero_values", "test_format_confirmation_message_with_luce_monoraria_consumption",
	"test_format_confirmation_message_with_luce_trioraria_consumption",
	"test_format_confirmation_message_with_gas_consumption",
	"test_format_confirmation_message_without_consumption", "test_vuoi_consumi_luce_yes_monoraria",
	"test_luce_consumo_f1_monoraria_valid", "test_vuoi_consumi_gas_yes", "test_gas_consumo_valid",
	"test_vuoi_consumi_luce_no", "test_vuoi_consumi_luce_yes_trioraria", "test_luce_consumo_f1_negative",
	"test_luce_consumo_f1_value_error", "test_luce_consumo_f1_trioraria_valid",
	"test_luce_consumo_f2_negative", "test_luce_consumo_f2_value_error", "test_luce_consumo_f2_valid",
	"test_luce_consumo_f3_negative", "test_luce_consumo_f3_value_error", "test_luce_consumo_f3_valid",
	"test_gas_energia_negative", "test_gas_energia_value_error_variabile",
	"test_gas_energia_value_error_fissa", "test_gas_comm_negative", "test_validate_numeric_input_valid",
	"test_validate_numeric_input_valid_with_comma", "test_validate_numeric_input_zero",
	"test_validate_numeric_input_small_decimal", "test_validate_numeric_input_negative",
	"test_validate_numeric_input_too_long", "test_validate_numeric_input_very_long",
	"test_validate_numeric_input_invalid_string", "test_validate_numeric_input_empty",
	"test_validate_numeric_input_special_chars", "test_validate_numeric_input_at_max_length",
	"test_luce_energia_too_long_input", "test_luce_comm_too_long_input",
	"test_luce_consumo_f1_too_long_input", "test_luce_consumo_f2_too_long_input",
	"test_luce_consumo_f3_too_long_input", "test_gas_energia_too_long_input",
	"test_gas_comm_too_long_input", "test_gas_consumo_too_long_input",
}

func TestRegistrationPy(t *testing.T) {
	if len(registrationPyTests) != 82 {
		t.Fatalf("test_registration.py ha 82 test, portati %d", len(registrationPyTests))
	}
	for _, tc := range registrationPyTests {
		t.Run(tc.name, tc.fn)
	}
}

func TestBotPyRegistration(t *testing.T) {
	if len(botPyRegistrationTests) != 66 {
		t.Fatalf("test_bot.py ha 66 test di registrazione, portati %d", len(botPyRegistrationTests))
	}
	byName := map[string]func(*testing.T){}
	for _, tc := range registrationPyTests {
		byName[tc.name] = tc.fn
	}
	for _, name := range botPyRegistrationTests {
		fn := byName[name]
		if fn == nil {
			t.Fatalf("%s: nessun port", name)
		}
		t.Run(name, fn)
	}
}
