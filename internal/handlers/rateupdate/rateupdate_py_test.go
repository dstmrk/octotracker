package rateupdate

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Port di tests/test_rate_update.py: un test Go per ogni test Python, con
// lo stesso nome in CamelCase.
//
// Adattamenti dovuti al confine dei package:
//   - le funzioni database sono quelle di internal/database; "get_connection
//     che solleva sqlite3.Error" diventa uno store chiuso;
//   - _build_pending_rates è interna a internal/checker: i test la
//     esercitano tramite l'API pubblica checker.CheckAndNotifyUsers con uno
//     store finto, leggendo le pending salvate dopo la notifica (i flag
//     show_luce/show_gas sono quindi quelli calcolati dal checker);
//   - patch("handlers.rate_update.apply_pending_rates") diventa un fakeStore.

// ========== FIXTURES ==========

func sampleUserData() database.User {
	return database.User{Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0}}
}

func sampleUserDataWithGas() database.User {
	u := sampleUserData()
	u.Gas = &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.456, Commercializzazione: 84.0}
	return u
}

func samplePendingRates() database.PendingRates {
	return database.PendingRates{Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.130, Commercializzazione: 65.0}}
}

// notifica è il messaggio della fixture mock_callback_query: text_html =
// "⚡️ <b>Buone notizie!</b>\nTesto notifica...\n\n{PROMPT_TEXT}\n\n🔗 Maggiori info: https://octopusenergy.it".
func notifica() gotgbot.Message {
	text := "⚡️ Buone notizie!\nTesto notifica...\n\n" + PromptText + "\n\n🔗 Maggiori info: https://octopusenergy.it"
	return notification(text,
		gotgbot.MessageEntity{Type: "bold", Offset: 3, Length: 14},
		gotgbot.MessageEntity{Type: "url", Offset: int64(len(utf16Units(text))) - 23, Length: 23},
	)
}

func utf16Units(s string) []uint16 {
	var u []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			u = append(u, uint16(0xd800+(r>>10)), uint16(0xdc00+(r&0x3ff)))
		} else {
			u = append(u, uint16(r))
		}
	}
	return u
}

type env struct {
	t  *testing.T
	s  *database.Store
	h  *Handlers
	fc *testutil.FakeClient
}

func newEnv(t *testing.T) *env {
	s := newTestStore(t)
	return &env{t: t, s: s, h: &Handlers{Store: s, UserData: tg.NewUserData()}}
}

// click esegue l'handler su una callback con il messaggio msg.
func (e *env) click(handler func(*Handlers) handlers.Response, data string, msg gotgbot.Message) result {
	e.t.Helper()
	b, fc := testutil.NewBot(e.t)
	e.fc = fc
	return call(e.t, fc, e.h.UserData, handler(e.h), b, ext.NewContext(b, callbackUpdate(data, msg), nil))
}

// send esegue l'handler su un messaggio di testo.
func (e *env) send(handler func(*Handlers) handlers.Response, text string) result {
	e.t.Helper()
	b, fc := testutil.NewBot(e.t)
	e.fc = fc
	return call(e.t, fc, e.h.UserData, handler(e.h), b, testutil.MessageContext(b, testutil.DefaultUserID, text))
}

var (
	rateUpdateYes        = func(h *Handlers) handlers.Response { return h.RateUpdateYes }
	rateUpdateNo         = func(h *Handlers) handlers.Response { return h.RateUpdateNo }
	scadenzaUpdateChoice = func(h *Handlers) handlers.Response { return h.ScadenzaUpdateChoice }
	updateScadenzaDate   = func(h *Handlers) handlers.Response { return h.UpdateScadenzaDate }
	skipScadenzaDate     = func(h *Handlers) handlers.Response { return h.SkipScadenzaDate }
)

func (e *env) save(u database.User) {
	e.t.Helper()
	if !e.s.SaveUser(testUserID, u) {
		e.t.Fatal("SaveUser fallito")
	}
}

func callsTo(res result, method string) []testutil.Call {
	var out []testutil.Call
	for _, c := range res.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func onlyCall(t *testing.T, res result, method string) testutil.Call {
	t.Helper()
	c := callsTo(res, method)
	if len(c) != 1 {
		t.Fatalf("attesa una chiamata %s, chiamate: %+v (err=%v)", method, res.calls, res.err)
	}
	return c[0]
}

func assertContains(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("il testo non contiene %q:\n%s", w, text)
		}
	}
}

func assertNotContains(t *testing.T, text string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(text, w) {
			t.Errorf("il testo contiene %q:\n%s", w, text)
		}
	}
}

// ========== TEST DATABASE PENDING RATES ==========

// test_save_and_load_pending_rates
func TestSaveAndLoadPendingRates(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserData())
	if !s.SavePendingRates(testUserID, samplePendingRates()) {
		t.Fatal("SavePendingRates = false")
	}
	loaded := s.LoadPendingRates(testUserID)
	if loaded == nil || loaded.Luce.Energia != 0.130 || loaded.Luce.Commercializzazione != 65.0 {
		t.Fatalf("loaded = %+v", loaded)
	}
}

// test_load_pending_rates_empty
func TestLoadPendingRatesEmpty(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserData())
	if p := s.LoadPendingRates(testUserID); p != nil {
		t.Errorf("got %+v", p)
	}
}

// test_load_pending_rates_nonexistent_user
func TestLoadPendingRatesNonexistentUser(t *testing.T) {
	if p := newTestStore(t).LoadPendingRates("999999"); p != nil {
		t.Errorf("got %+v", p)
	}
}

// test_clear_pending_rates
func TestClearPendingRates(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserData())
	s.SavePendingRates(testUserID, samplePendingRates())
	if !s.ClearPendingRates(testUserID) {
		t.Fatal("ClearPendingRates = false")
	}
	if p := s.LoadPendingRates(testUserID); p != nil {
		t.Errorf("got %+v", p)
	}
}

// test_schema_has_pending_rates_column
func TestSchemaHasPendingRatesColumn(t *testing.T) {
	rows, err := rawDB(t, newTestStore(t)).Query("SELECT name FROM pragma_table_info('users')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		found = found || name == "pending_rates"
	}
	if !found {
		t.Error("colonna pending_rates mancante")
	}
}

// ========== TEST HANDLER RATE UPDATE YES ==========

// test_rate_update_yes_success
func TestRateUpdateYesSuccess(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	e.s.SavePendingRates(testUserID, samplePendingRates())

	res := e.click(rateUpdateYes, CallbackYes, notifica())

	onlyCall(t, res, "answerCallbackQuery")
	text := onlyCall(t, res, "editMessageText").Text()
	assertContains(t, text, ConfirmedText)
	assertNotContains(t, text, PromptText)

	updated := e.s.LoadUser(testUserID)
	if updated.Luce.Energia != 0.130 || updated.Luce.Commercializzazione != 65.0 {
		t.Errorf("tariffe non aggiornate: %+v", updated.Luce)
	}
	if e.s.LoadPendingRates(testUserID) != nil {
		t.Error("pending non rimosse")
	}
}

// test_rate_update_yes_with_gas
func TestRateUpdateYesWithGas(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserDataWithGas())
	pending := samplePendingRates()
	pending.Gas = &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.420, Commercializzazione: 80.0}
	e.s.SavePendingRates(testUserID, pending)

	e.click(rateUpdateYes, CallbackYes, notifica())

	updated := e.s.LoadUser(testUserID)
	if updated.Luce.Energia != 0.130 || updated.Gas.Energia != 0.420 || updated.Gas.Commercializzazione != 80.0 {
		t.Errorf("tariffe: %+v %+v", updated.Luce, updated.Gas)
	}
}

// test_rate_update_yes_no_pending
func TestRateUpdateYesNoPending(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	res := e.click(rateUpdateYes, CallbackYes, notifica())
	assertContains(t, onlyCall(t, res, "editMessageText").Text(), DeclinedText)
	if u := e.s.LoadUser(testUserID); u.Luce.Energia != 0.145 {
		t.Errorf("tariffe cambiate: %+v", u.Luce)
	}
}

// test_rate_update_yes_clears_last_notified_rates
func TestRateUpdateYesClearsLastNotifiedRates(t *testing.T) {
	e := newEnv(t)
	u := sampleUserData()
	u.LastNotifiedRates = database.LastNotifiedRates{"luce": database.NotifiedRate(0.130, 65.0)}
	e.save(u)
	e.s.SavePendingRates(testUserID, samplePendingRates())

	e.click(rateUpdateYes, CallbackYes, notifica())

	if got := e.s.LoadUser(testUserID).LastNotifiedRates; len(got) != 0 {
		t.Errorf("last_notified_rates non azzerato: %v", got)
	}
}

// test_rate_update_yes_preserves_consumption
func TestRateUpdateYesPreservesConsumption(t *testing.T) {
	e := newEnv(t)
	u := sampleUserData()
	u.Luce.ConsumoF1 = fp(2700.0)
	e.save(u)
	pending := samplePendingRates()
	pending.Luce.ConsumoF1 = fp(2700.0)
	e.s.SavePendingRates(testUserID, pending)

	e.click(rateUpdateYes, CallbackYes, notifica())

	updated := e.s.LoadUser(testUserID)
	if updated.Luce.Energia != 0.130 || updated.Luce.ConsumoF1 == nil || *updated.Luce.ConsumoF1 != 2700.0 {
		t.Errorf("luce: %+v", updated.Luce)
	}
}

// ========== TEST HANDLER RATE UPDATE NO ==========

// test_rate_update_no
func TestRateUpdateNo(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	e.s.SavePendingRates(testUserID, samplePendingRates())

	res := e.click(rateUpdateNo, CallbackNo, notifica())

	onlyCall(t, res, "answerCallbackQuery")
	edit := onlyCall(t, res, "editMessageText")
	assertContains(t, edit.Text(), DeclinedText)
	assertNotContains(t, edit.Text(), PromptText)
	if edit.ParseMode() != "HTML" {
		t.Errorf("parse_mode = %q", edit.ParseMode())
	}
	if e.s.LoadPendingRates(testUserID) != nil {
		t.Error("pending non rimosse")
	}
	if u := e.s.LoadUser(testUserID); u.Luce.Energia != 0.145 {
		t.Errorf("tariffe cambiate: %+v", u.Luce)
	}
}

// ========== TEST CHECKER _build_pending_rates (via CheckAndNotifyUsers) ==========

// checkerStore è uno checker.Store in memoria che registra le pending salvate.
type checkerStore struct {
	mu      sync.Mutex
	users   []*database.User
	rates   database.CurrentRates
	pending map[string]database.PendingRates
}

func (c *checkerStore) RemoveUser(string) bool                 { return true }
func (c *checkerStore) LoadUsers() []*database.User            { return c.users }
func (c *checkerStore) GetCurrentRates() database.CurrentRates { return c.rates }
func (c *checkerStore) SaveUser(string, database.User) bool    { return true }
func (c *checkerStore) SavePendingRates(id string, p database.PendingRates) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[id] = p
	return true
}

// currentRates costruisce le tariffe correnti fissa/monoraria; nil = assente.
func currentRates(luce, gas *[2]float64) database.CurrentRates {
	r := database.CurrentRates{"luce": {}, "gas": {}}
	if luce != nil {
		r["luce"]["fissa"] = map[string]database.Rate{"monoraria": {Energia: luce[0], Commercializzazione: fp(luce[1])}}
	}
	if gas != nil {
		r["gas"]["fissa"] = map[string]database.Rate{"monoraria": {Energia: gas[0], Commercializzazione: fp(gas[1])}}
	}
	return r
}

// buildPendingRates esegue il checker per un utente e restituisce le
// pending salvate dopo la notifica (false se non è stata inviata).
func buildPendingRates(t *testing.T, user database.User, rates database.CurrentRates) (database.PendingRates, bool) {
	t.Helper()
	user.ID = testUserID
	st := &checkerStore{users: []*database.User{&user}, rates: rates, pending: map[string]database.PendingRates{}}
	b, _ := testutil.NewBot(t)
	if err := checker.CheckAndNotifyUsers(context.Background(), b, st); err != nil {
		t.Fatal(err)
	}
	p, ok := st.pending[testUserID]
	return p, ok
}

func mustBuildPending(t *testing.T, user database.User, rates database.CurrentRates) database.PendingRates {
	t.Helper()
	p, ok := buildPendingRates(t, user, rates)
	if !ok {
		t.Fatal("nessuna notifica / pending salvate")
	}
	return p
}

// test_build_pending_rates_luce_only
func TestBuildPendingRatesLuceOnly(t *testing.T) {
	p := mustBuildPending(t, sampleUserData(), currentRates(&[2]float64{0.130, 65.0}, nil))
	if p.Luce.Tipo != "fissa" || p.Luce.Fascia != "monoraria" || p.Luce.Energia != 0.130 || p.Luce.Commercializzazione != 65.0 {
		t.Errorf("luce: %+v", p.Luce)
	}
	if p.Gas != nil {
		t.Error("gas non atteso")
	}
}

// test_build_pending_rates_with_gas
func TestBuildPendingRatesWithGas(t *testing.T) {
	p := mustBuildPending(t, sampleUserDataWithGas(), currentRates(&[2]float64{0.130, 65.0}, &[2]float64{0.420, 80.0}))
	if p.Luce.Energia != 0.130 || p.Gas == nil || p.Gas.Energia != 0.420 || p.Gas.Commercializzazione != 80.0 {
		t.Errorf("pending: %+v %+v", p.Luce, p.Gas)
	}
}

// test_build_pending_rates_preserves_consumption
func TestBuildPendingRatesPreservesConsumption(t *testing.T) {
	u := sampleUserDataWithGas()
	u.Luce.ConsumoF1, u.Luce.ConsumoF2 = fp(2700.0), fp(500.0)
	u.Gas.ConsumoAnnuo = fp(1200.0)
	p := mustBuildPending(t, u, currentRates(&[2]float64{0.130, 65.0}, &[2]float64{0.420, 80.0}))
	if *p.Luce.ConsumoF1 != 2700.0 || *p.Luce.ConsumoF2 != 500.0 || *p.Gas.ConsumoAnnuo != 1200.0 {
		t.Errorf("consumi: %+v %+v", p.Luce, p.Gas)
	}
}

// test_build_pending_rates_missing_current_rates: senza tariffe correnti
// per il servizio dell'utente le tariffe attuali restano invariate. Dal
// checker il caso si osserva come nessuna notifica e nessuna pending (il
// ramo "show e tariffa assente" di _build_utility_pending non è
// raggiungibile: show richiede un risparmio, che richiede la tariffa).
func TestBuildPendingRatesMissingCurrentRates(t *testing.T) {
	if p, ok := buildPendingRates(t, sampleUserData(), currentRates(nil, nil)); ok {
		t.Errorf("pending inattese: %+v", p)
	}
	// Tariffa luce assente ma gas conveniente: la luce mantiene le tariffe utente
	p := mustBuildPending(t, sampleUserDataWithGas(), currentRates(nil, &[2]float64{0.420, 80.0}))
	if p.Luce.Energia != 0.145 || p.Luce.Commercializzazione != 72.0 {
		t.Errorf("luce: %+v", p.Luce)
	}
}

// ========== TEST CHECKER build_rate_update_keyboard ==========

// test_build_rate_update_keyboard
func TestBuildRateUpdateKeyboard(t *testing.T) {
	kb := checker.BuildRateUpdateKeyboard().InlineKeyboard
	if len(kb) != 1 || len(kb[0]) != 2 {
		t.Fatalf("tastiera: %+v", kb)
	}
	if kb[0][0].CallbackData != "rate_update_yes" || kb[0][1].CallbackData != "rate_update_no" {
		t.Errorf("callback_data: %+v", kb[0])
	}
	// Le costanti del package coincidono con quelle usate dal checker
	if kb[0][0].CallbackData != CallbackYes || kb[0][1].CallbackData != CallbackNo {
		t.Error("callback_data diverse da CallbackYes/CallbackNo")
	}
}

// ========== TEST EDGE CASES HANDLER ==========

// test_rate_update_yes_user_not_in_db: utente assente → no_pending.
func TestRateUpdateYesUserNotInDB(t *testing.T) {
	e := newEnv(t)
	res := e.click(rateUpdateYes, CallbackYes, notifica())
	assertContains(t, onlyCall(t, res, "editMessageText").Text(), DeclinedText)
}

// test_rate_update_yes_save_failure: apply_pending_rates → (False, "db_error").
func TestRateUpdateYesSaveFailure(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	e.s.SavePendingRates(testUserID, samplePendingRates())
	e.h.Store = &fakeStore{Store: e.s, applyReason: database.ApplyDBError}
	res := e.click(rateUpdateYes, CallbackYes, notifica())
	assertContains(t, onlyCall(t, res, "editMessageText").Text(), "Errore nell'aggiornamento")
}

// ========== TEST DATABASE PENDING RATES ERROR HANDLING ==========

// test_save_pending_rates_db_error
func TestSavePendingRatesDBError(t *testing.T) {
	if brokenStore(t).SavePendingRates("123", database.PendingRates{}) {
		t.Error("atteso false")
	}
}

// test_load_pending_rates_db_error
func TestLoadPendingRatesDBError(t *testing.T) {
	if p := brokenStore(t).LoadPendingRates("123"); p != nil {
		t.Errorf("got %+v", p)
	}
}

// test_clear_pending_rates_db_error
func TestClearPendingRatesDBError(t *testing.T) {
	if brokenStore(t).ClearPendingRates("123") {
		t.Error("atteso false")
	}
}

// ========== TEST MIXED TARIFF SCENARIOS ==========

// test_build_pending_rates_gas_better_luce_worse: aggiorna solo il gas.
func TestBuildPendingRatesGasBetterLuceWorse(t *testing.T) {
	u := database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.100, Commercializzazione: 60.0},
		Gas:  &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.500, Commercializzazione: 90.0},
	}
	p := mustBuildPending(t, u, currentRates(&[2]float64{0.130, 72.0}, &[2]float64{0.420, 80.0}))
	if p.Gas.Energia != 0.420 || p.Gas.Commercializzazione != 80.0 {
		t.Errorf("gas: %+v", p.Gas)
	}
	if p.Luce.Energia != 0.100 || p.Luce.Commercializzazione != 60.0 {
		t.Errorf("luce aggiornata: %+v", p.Luce)
	}
}

// test_build_pending_rates_luce_better_gas_worse: aggiorna solo la luce.
func TestBuildPendingRatesLuceBetterGasWorse(t *testing.T) {
	u := database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.150, Commercializzazione: 80.0},
		Gas:  &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.400, Commercializzazione: 75.0},
	}
	p := mustBuildPending(t, u, currentRates(&[2]float64{0.120, 65.0}, &[2]float64{0.450, 85.0}))
	if p.Luce.Energia != 0.120 || p.Luce.Commercializzazione != 65.0 {
		t.Errorf("luce: %+v", p.Luce)
	}
	if p.Gas.Energia != 0.400 || p.Gas.Commercializzazione != 75.0 {
		t.Errorf("gas aggiornato: %+v", p.Gas)
	}
}

// test_build_pending_rates_both_better
func TestBuildPendingRatesBothBetter(t *testing.T) {
	u := database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.150, Commercializzazione: 80.0},
		Gas:  &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.500, Commercializzazione: 90.0},
	}
	p := mustBuildPending(t, u, currentRates(&[2]float64{0.120, 65.0}, &[2]float64{0.420, 80.0}))
	if p.Luce.Energia != 0.120 || p.Luce.Commercializzazione != 65.0 || p.Gas.Energia != 0.420 || p.Gas.Commercializzazione != 80.0 {
		t.Errorf("pending: %+v %+v", p.Luce, p.Gas)
	}
}

// test_build_pending_rates_preserves_consumption_with_mixed
func TestBuildPendingRatesPreservesConsumptionWithMixed(t *testing.T) {
	u := database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.100, Commercializzazione: 60.0, ConsumoF1: fp(2700.0)},
		Gas:  &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.500, Commercializzazione: 90.0, ConsumoAnnuo: fp(1200.0)},
	}
	p := mustBuildPending(t, u, currentRates(&[2]float64{0.130, 72.0}, &[2]float64{0.420, 80.0}))
	if *p.Luce.ConsumoF1 != 2700.0 || *p.Gas.ConsumoAnnuo != 1200.0 {
		t.Errorf("consumi: %+v %+v", p.Luce, p.Gas)
	}
	if p.Luce.Energia != 0.100 || p.Gas.Energia != 0.420 {
		t.Errorf("tariffe: %+v %+v", p.Luce, p.Gas)
	}
}

// ========== TEST APPLY_PENDING_RATES (ATOMIC) ==========

// test_apply_pending_rates_success
func TestApplyPendingRatesSuccess(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserData())
	s.SavePendingRates(testUserID, samplePendingRates())
	if ok, reason := s.ApplyPendingRates(testUserID); !ok || reason != "ok" {
		t.Fatalf("got %v %q", ok, reason)
	}
	if u := s.LoadUser(testUserID); u.Luce.Energia != 0.130 || u.Luce.Commercializzazione != 65.0 {
		t.Errorf("luce: %+v", u.Luce)
	}
	if s.LoadPendingRates(testUserID) != nil {
		t.Error("pending non rimosse")
	}
}

// test_apply_pending_rates_no_pending
func TestApplyPendingRatesNoPending(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserData())
	if ok, reason := s.ApplyPendingRates(testUserID); ok || reason != "no_pending" {
		t.Fatalf("got %v %q", ok, reason)
	}
	if u := s.LoadUser(testUserID); u.Luce.Energia != 0.145 {
		t.Errorf("luce: %+v", u.Luce)
	}
}

// test_apply_pending_rates_no_user: utente inesistente → no_pending.
func TestApplyPendingRatesNoUser(t *testing.T) {
	if ok, reason := newTestStore(t).ApplyPendingRates("999999"); ok || reason != "no_pending" {
		t.Fatalf("got %v %q", ok, reason)
	}
}

// test_apply_pending_rates_clears_last_notified_rates
func TestApplyPendingRatesClearsLastNotifiedRates(t *testing.T) {
	s := newTestStore(t)
	u := sampleUserData()
	u.LastNotifiedRates = database.LastNotifiedRates{"luce": map[string]any{"energia": 0.130}}
	s.SaveUser(testUserID, u)
	s.SavePendingRates(testUserID, samplePendingRates())
	if ok, _ := s.ApplyPendingRates(testUserID); !ok {
		t.Fatal("apply fallito")
	}
	if got := s.LoadUser(testUserID).LastNotifiedRates; len(got) != 0 {
		t.Errorf("last_notified_rates: %v", got)
	}
}

// test_apply_pending_rates_db_error: la connessione fallisce → db_error.
func TestApplyPendingRatesDBError(t *testing.T) {
	if ok, reason := brokenStore(t).ApplyPendingRates(testUserID); ok || reason != "db_error" {
		t.Fatalf("got %v %q", ok, reason)
	}
}

// test_apply_pending_rates_validation_error
func TestApplyPendingRatesValidationError(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserData())
	invalid := samplePendingRates()
	invalid.Luce.Tipo = "INVALID_TYPE"
	s.SavePendingRates(testUserID, invalid)
	if ok, reason := s.ApplyPendingRates(testUserID); ok || reason != "validation_error" {
		t.Fatalf("got %v %q", ok, reason)
	}
	if u := s.LoadUser(testUserID); u.Luce.Energia != 0.145 {
		t.Errorf("luce: %+v", u.Luce)
	}
}

// test_apply_pending_rates_db_error_during_transaction: in Python DB_FILE
// punta a un percorso inesistente dopo aver preparato i dati; qui il
// database dello store preparato viene chiuso.
func TestApplyPendingRatesDBErrorDuringTransaction(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserData())
	s.SavePendingRates(testUserID, samplePendingRates())
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if ok, reason := s.ApplyPendingRates(testUserID); ok || reason != "db_error" {
		t.Fatalf("got %v %q", ok, reason)
	}
}

// test_apply_pending_rates_with_gas
func TestApplyPendingRatesWithGas(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserDataWithGas())
	pending := samplePendingRates()
	pending.Gas = &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.420, Commercializzazione: 80.0}
	s.SavePendingRates(testUserID, pending)
	if ok, reason := s.ApplyPendingRates(testUserID); !ok || reason != "ok" {
		t.Fatalf("got %v %q", ok, reason)
	}
	u := s.LoadUser(testUserID)
	if u.Luce.Energia != 0.130 || u.Gas.Energia != 0.420 || u.Gas.Commercializzazione != 80.0 {
		t.Errorf("tariffe: %+v %+v", u.Luce, u.Gas)
	}
	if s.LoadPendingRates(testUserID) != nil {
		t.Error("pending non rimosse")
	}
}

// test_rate_update_yes_no_user: apply_pending_rates → (False, "no_user").
func TestRateUpdateYesNoUser(t *testing.T) {
	e := newEnv(t)
	e.h.Store = &fakeStore{Store: e.s, applyReason: database.ApplyNoUser}
	logs := captureLogs(t)
	res := e.click(rateUpdateYes, CallbackYes, notifica())
	assertContains(t, onlyCall(t, res, "editMessageText").Text(), DeclinedText)
	assertContains(t, logs.String(), "- handlers.rate_update - ERROR - ❌ Utente 123456789: non trovato nel database")
}

// test_apply_pending_rates_no_user_race_condition: in Python un proxy della
// connessione cancella l'utente tra le due SELECT. In Go le due letture
// avvengono nella stessa transazione BEGIN IMMEDIATE e la connessione non è
// intercettabile da fuori internal/database: si verifica che una
// cancellazione concorrente da un'altra connessione non possa
// interfoliarsi, cioè l'esito è "ok" (apply prima) o "no_pending"
// (cancellazione prima) e mai uno stato incoerente.
func TestApplyPendingRatesNoUserRaceCondition(t *testing.T) {
	for i := 0; i < 20; i++ {
		s := newTestStore(t)
		s.SaveUser(testUserID, sampleUserData())
		s.SavePendingRates(testUserID, samplePendingRates())
		db := rawDB(t, s)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.Exec("DELETE FROM users WHERE user_id = ?", testUserID); err != nil {
				t.Error(err)
			}
		}()
		ok, reason := s.ApplyPendingRates(testUserID)
		wg.Wait()

		switch {
		case ok && reason == "ok", !ok && reason == "no_pending":
		default:
			t.Fatalf("esito inatteso: %v %q", ok, reason)
		}
		if s.UserExists(testUserID) {
			t.Fatal("l'utente doveva essere cancellato")
		}
	}
}

// test_apply_pending_rates_rollback_failure: in Python l'UPDATE solleva
// sqlite3.Error (e anche il rollback fallisce) → db_error. Qui l'UPDATE
// fallisce per un trigger che solleva un errore: esito db_error, dati
// invariati e store ancora utilizzabile (transazione chiusa).
func TestApplyPendingRatesRollbackFailure(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser(testUserID, sampleUserData())
	s.SavePendingRates(testUserID, samplePendingRates())
	db := rawDB(t, s)
	if _, err := db.Exec(`CREATE TRIGGER fail_update BEFORE UPDATE OF luce_energia ON users
		BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`); err != nil {
		t.Fatal(err)
	}
	if ok, reason := s.ApplyPendingRates(testUserID); ok || reason != "db_error" {
		t.Fatalf("got %v %q", ok, reason)
	}
	if u := s.LoadUser(testUserID); u.Luce.Energia != 0.145 {
		t.Errorf("luce modificata: %+v", u.Luce)
	}
	if s.LoadPendingRates(testUserID) == nil {
		t.Error("pending rimosse nonostante il rollback")
	}
	if _, err := db.Exec("DROP TRIGGER fail_update"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.ApplyPendingRates(testUserID); !ok {
		t.Error("store non riutilizzabile dopo il rollback")
	}
}

// test_build_pending_rates_backward_compatible: senza parametri show_* il
// default aggiorna tutto.
func TestBuildPendingRatesBackwardCompatible(t *testing.T) {
	p := mustBuildPending(t, sampleUserData(), currentRates(&[2]float64{0.130, 65.0}, nil))
	if p.Luce.Energia != 0.130 || p.Luce.Commercializzazione != 65.0 {
		t.Errorf("luce: %+v", p.Luce)
	}
}

// ========== TEST updated_services in _build_pending_rates ==========

// test_build_pending_rates_marks_updated_luce
func TestBuildPendingRatesMarksUpdatedLuce(t *testing.T) {
	p := mustBuildPending(t, sampleUserData(), currentRates(&[2]float64{0.130, 65.0}, nil))
	if strings.Join(p.UpdatedServices, ",") != "luce" {
		t.Errorf("updated_services = %v", p.UpdatedServices)
	}
}

// test_build_pending_rates_marks_only_shown_service: solo la luce è
// mostrata come conveniente. Python passa show_gas=False con un gas
// migliore; dal checker show_gas è calcolato, quindi qui il gas corrente è
// peggiore di quello dell'utente.
func TestBuildPendingRatesMarksOnlyShownService(t *testing.T) {
	p := mustBuildPending(t, sampleUserDataWithGas(), currentRates(&[2]float64{0.130, 65.0}, &[2]float64{0.500, 90.0}))
	if strings.Join(p.UpdatedServices, ",") != "luce" {
		t.Errorf("updated_services = %v", p.UpdatedServices)
	}
}

// test_build_pending_rates_no_updated_when_rate_missing: senza tariffa
// corrente nessun servizio è marcato come aggiornato. Dal checker: il gas
// è conveniente ma la luce non ha tariffa → updated_services = ["gas"],
// senza la luce.
func TestBuildPendingRatesNoUpdatedWhenRateMissing(t *testing.T) {
	p := mustBuildPending(t, sampleUserDataWithGas(), currentRates(nil, &[2]float64{0.420, 80.0}))
	if strings.Join(p.UpdatedServices, ",") != "gas" {
		t.Errorf("updated_services = %v", p.UpdatedServices)
	}
	if p.UpdatedServices == nil {
		t.Error("updated_services deve essere presente ([] e non assente)")
	}
}

// ========== TEST _updated_fissa_services ==========

// test_updated_fissa_services_none
func TestUpdatedFissaServicesNone(t *testing.T) {
	if got := updatedFissaServices(nil); len(got) != 0 {
		t.Errorf("got %#v", got)
	}
}

// test_updated_fissa_services_filters_variabile
func TestUpdatedFissaServicesFiltersVariabile(t *testing.T) {
	p := &database.PendingRates{
		Luce:            database.Utility{Tipo: "variabile"},
		Gas:             &database.Utility{Tipo: "fissa"},
		UpdatedServices: []string{"luce", "gas"},
	}
	if got := strings.Join(updatedFissaServices(p), ","); got != "gas" {
		t.Errorf("got %q", got)
	}
}

// test_updated_fissa_services_respects_updated_list
func TestUpdatedFissaServicesRespectsUpdatedList(t *testing.T) {
	p := &database.PendingRates{
		Luce:            database.Utility{Tipo: "fissa"},
		Gas:             &database.Utility{Tipo: "fissa"},
		UpdatedServices: []string{"luce"}, // gas non aggiornato
	}
	if got := strings.Join(updatedFissaServices(p), ","); got != "luce" {
		t.Errorf("got %q", got)
	}
}

// ========== TEST CONVERSAZIONE SCADENZA POST-AGGIORNAMENTO ==========

func xPromptY() gotgbot.Message { return notification("x\n\n" + PromptText + "\n\ny") }

// test_rate_update_yes_fissa_asks_scadenza
func TestRateUpdateYesFissaAsksScadenza(t *testing.T) {
	e := newEnv(t)
	e.save(sampleUserData())
	pending := samplePendingRates()
	pending.UpdatedServices = []string{"luce"}
	e.s.SavePendingRates(testUserID, pending)

	res := e.click(rateUpdateYes, CallbackYes, xPromptY())
	if res.state != StateAskScadenza {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, onlyCall(t, res, "sendMessage").Text(), ScadenzaAskText)
	if got := res.userAfter[servicesKey]; strings.Join(got.([]string), ",") != "luce" {
		t.Errorf("user_data: %v", got)
	}
}

// test_rate_update_yes_variabile_no_scadenza
func TestRateUpdateYesVariabileNoScadenza(t *testing.T) {
	e := newEnv(t)
	e.save(database.User{Luce: database.Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.02, Commercializzazione: 72.0}})
	e.s.SavePendingRates(testUserID, database.PendingRates{
		Luce:            database.Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.01, Commercializzazione: 65.0},
		UpdatedServices: []string{"luce"},
	})
	res := e.click(rateUpdateYes, CallbackYes, xPromptY())
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	if n := len(callsTo(res, "sendMessage")); n != 0 {
		t.Errorf("reply_text non atteso (%d)", n)
	}
}

// test_scadenza_choice_no_ends
func TestScadenzaChoiceNoEnds(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
	res := e.click(scadenzaUpdateChoice, CallbackScadenzaNo, notification("testo"))
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	if _, ok := res.userAfter[servicesKey]; ok {
		t.Error("coda servizi non rimossa")
	}
	assertContains(t, onlyCall(t, res, "editMessageText").Text(), ScadenzaDeclinedText)
}

// test_scadenza_choice_yes_asks_date
func TestScadenzaChoiceYesAsksDate(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
	res := e.click(scadenzaUpdateChoice, CallbackScadenzaYes, notification("testo"))
	if res.state != StateUpdateDate {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, onlyCall(t, res, "editMessageText").Text(), "data di attivazione")
}

// test_update_scadenza_date_valid_single_service
func TestUpdateScadenzaDateValidSingleService(t *testing.T) {
	e := newEnv(t)
	e.save(database.User{Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.13, Commercializzazione: 65.0}})
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
	res := e.send(updateScadenzaDate, "15/03/2025")
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	if got := e.s.LoadUser(testUserID).Luce.Scadenza; got != "2026-03-15" {
		t.Errorf("scadenza = %q", got)
	}
	if _, ok := res.userAfter[servicesKey]; ok {
		t.Error("coda servizi non rimossa")
	}
}

// test_update_scadenza_date_invalid_reasks
func TestUpdateScadenzaDateInvalidReasks(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
	res := e.send(updateScadenzaDate, "data-sbagliata")
	if res.state != StateUpdateDate {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	if got := onlyCall(t, res, "sendMessage").Text(); got != MsgDateInvalid {
		t.Errorf("testo = %q", got)
	}
}

// test_update_scadenza_date_two_services
func TestUpdateScadenzaDateTwoServices(t *testing.T) {
	e := newEnv(t)
	e.save(database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.13, Commercializzazione: 65.0},
		Gas:  &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.42, Commercializzazione: 80.0},
	})
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce", "gas"})

	// Prima data (luce) → resta in UPDATE_DATE e chiede la data gas
	res := e.send(updateScadenzaDate, "15/03/2025")
	if res.state != StateUpdateDate {
		t.Fatalf("primo: stato %q err=%v", res.state, res.err)
	}
	if got := e.s.LoadUser(testUserID).Luce.Scadenza; got != "2026-03-15" {
		t.Errorf("luce scadenza = %q", got)
	}
	if got := res.userAfter[servicesKey]; strings.Join(got.([]string), ",") != "gas" {
		t.Errorf("coda: %v", got)
	}

	// Seconda data (gas) → termina
	res = e.send(updateScadenzaDate, "01/06/2025")
	if res.state != "END" {
		t.Fatalf("secondo: stato %q err=%v", res.state, res.err)
	}
	if got := e.s.LoadUser(testUserID).Gas.Scadenza; got != "2026-06-01" {
		t.Errorf("gas scadenza = %q", got)
	}
	if _, ok := res.userAfter[servicesKey]; ok {
		t.Error("coda servizi non rimossa")
	}
}

// test_skip_scadenza_date_single
func TestSkipScadenzaDateSingle(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce"})
	res := e.click(skipScadenzaDate, CallbackScadenzaSkip, notification("testo"))
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	if _, ok := res.userAfter[servicesKey]; ok {
		t.Error("coda servizi non rimossa")
	}
}

// test_skip_scadenza_date_moves_to_next
func TestSkipScadenzaDateMovesToNext(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, servicesKey, []string{"luce", "gas"})
	res := e.click(skipScadenzaDate, CallbackScadenzaSkip, notification("testo"))
	if res.state != StateUpdateDate {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	if got := res.userAfter[servicesKey]; strings.Join(got.([]string), ",") != "gas" {
		t.Errorf("coda: %v", got)
	}
	if len(callsTo(res, "sendMessage")) == 0 {
		t.Error("effective_message.reply_text non chiamato")
	}
}
