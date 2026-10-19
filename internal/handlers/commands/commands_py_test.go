package commands

import (
	"strings"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/handlers/registration"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// Port dei test dei comandi di tests/test_bot.py (help, unknown, history_*,
// status_*, remove, cancel_*, help come fallback e update command). I test
// della conversazione di registrazione dello stesso file sono nel package
// registration. Ogni funzione ha il nome del test Python.

// env raccoglie bot finto, handler e store di un test.
type env struct {
	t  *testing.T
	h  *Handlers
	s  *database.Store
	fc *testutil.FakeClient
}

func newEnv(t *testing.T) *env {
	h, s := newHandlers(t)
	_, fc := testutil.NewBot(t)
	return &env{t: t, h: h, s: s, fc: fc}
}

// command esegue l'handler su un messaggio "text" dell'utente di default.
func (e *env) command(handler func(*Handlers) handlers.Response, text string) result {
	e.t.Helper()
	b, fc := testutil.NewBot(e.t)
	e.fc = fc
	return call(e.t, fc, e.h.UserData, handler(e.h), b, testutil.MessageContext(b, testutil.DefaultUserID, text))
}

func (e *env) save(u database.User) {
	e.t.Helper()
	if !e.s.SaveUser(testUserID, u) {
		e.t.Fatal("SaveUser fallito")
	}
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

var (
	status         = func(h *Handlers) handlers.Response { return h.Status }
	historyCommand = func(h *Handlers) handlers.Response { return h.HistoryCommand }
	removeData     = func(h *Handlers) handlers.Response { return h.RemoveData }
	helpCommand    = func(h *Handlers) handlers.Response { return h.HelpCommand }
	cancelConv     = func(h *Handlers) handlers.Response { return h.CancelConversation }
	unknownCommand = func(h *Handlers) handlers.Response { return h.UnknownCommand }
)

// test_unknown_command: comando non riconosciuto mostra messaggio di aiuto.
func TestUnknownCommand(t *testing.T) {
	e := newEnv(t)
	res := e.command(unknownCommand, "/unknown")
	if res.err != nil || res.state != "" {
		t.Fatalf("got state=%q err=%v", res.state, res.err)
	}
	text := onlySend(t, res).Text()
	assertContains(t, strings.ToLower(text), "non riconosciuto")
	assertContains(t, text, "/help")
}

// test_update_command: /update (alias di /start) per un utente esistente
// riavvia la registrazione chiedendo il tipo di tariffa.
func TestUpdateCommand(t *testing.T) {
	e := newEnv(t)
	e.save(luceFissaMono(0.145, 72.0))
	reg := &registration.Handlers{Store: e.s, UserData: e.h.UserData}
	b, fc := testutil.NewBot(t)
	res := call(t, fc, e.h.UserData, reg.Start, b, testutil.MessageContext(b, testutil.DefaultUserID, "/update"))
	if res.state != registration.StateTipoTariffa {
		t.Fatalf("stato: got %q, want %q", res.state, registration.StateTipoTariffa)
	}
	assertContains(t, strings.ToLower(onlySend(t, res).Text()), "tipo di tariffa")
}

// test_help_command: /help mostra i comandi disponibili.
func TestHelpCommand(t *testing.T) {
	e := newEnv(t)
	res := e.command(helpCommand, "/help")
	assertContains(t, onlySend(t, res).Text(), "/start", "/status", "/history", "/remove")
}

// test_history_command_user_not_registered
func TestHistoryCommandUserNotRegistered(t *testing.T) {
	e := newEnv(t)
	e.h.WebAppURL = "https://example.com/app/"
	res := e.command(historyCommand, "/history")
	assertContains(t, onlySend(t, res).Text(), "Non hai ancora registrato", "/start")
	assertEnd(t, res)
}

// test_history_command_no_webapp_url
func TestHistoryCommandNoWebappURL(t *testing.T) {
	e := newEnv(t)
	e.h.WebAppURL = ""
	e.save(luceFissaMono(0.1, 72))
	res := e.command(historyCommand, "/history")
	assertContains(t, onlySend(t, res).Text(), "non è ancora configurata")
	assertEnd(t, res)
}

// test_history_command_with_webapp_url
func TestHistoryCommandWithWebappURL(t *testing.T) {
	e := newEnv(t)
	e.h.WebAppURL = "https://example.com/app/"
	e.save(luceFissaMono(0.1, 72))
	res := e.command(historyCommand, "/history")
	c := onlySend(t, res)
	assertContains(t, c.Text(), "Storico Tariffe", "grafici interattivi")

	kb := c.InlineKeyboard()
	if len(kb) != 1 || len(kb[0]) != 1 {
		t.Fatalf("tastiera inattesa: %+v", kb)
	}
	button := kb[0][0]
	if button.Text != "📊 Apri Grafici Tariffe" {
		t.Errorf("testo pulsante: %q", button.Text)
	}
	if button.WebApp == nil || button.WebApp.Url != "https://example.com/app/" {
		t.Errorf("web_app: %+v", button.WebApp)
	}
	assertEnd(t, res)
}

// test_history_command_clears_context
func TestHistoryCommandClearsContext(t *testing.T) {
	e := newEnv(t)
	e.h.WebAppURL = "https://example.com/app/"
	e.save(luceFissaMono(0.1, 72))
	e.h.UserData.Set(testutil.DefaultUserID, "old_data", "some_value")
	res := e.command(historyCommand, "/history")
	if len(res.userAfter) != 0 {
		t.Errorf("user_data non pulito: %v", res.userAfter)
	}
}

// test_status_no_data
func TestStatusNoData(t *testing.T) {
	e := newEnv(t)
	res := e.command(status, "/status")
	assertContains(t, onlySend(t, res).Text(), "Non hai ancora registrato")
}

// test_status_with_data
func TestStatusWithData(t *testing.T) {
	e := newEnv(t)
	e.save(luceFissaMono(0.145, 72.0))
	text := onlySend(t, e.command(status, "/status")).Text()
	assertContains(t, text, "Luce", "0,145", "72")
}

// test_status_shows_luce_scadenza
func TestStatusShowsLuceScadenza(t *testing.T) {
	e := newEnv(t)
	u := luceFissaMono(0.145, 72.0)
	u.Luce.Scadenza = "2026-03-15"
	e.save(u)
	text := onlySend(t, e.command(status, "/status")).Text()
	assertContains(t, text, "Scadenza offerta", "15/03/2026")
}

// test_status_shows_gas_scadenza
func TestStatusShowsGasScadenza(t *testing.T) {
	e := newEnv(t)
	e.save(database.User{
		Luce: database.Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.02, Commercializzazione: 72.0},
		Gas:  &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.456, Commercializzazione: 84.0, Scadenza: "2026-05-20"},
	})
	assertContains(t, onlySend(t, e.command(status, "/status")).Text(), "20/05/2026")
}

// test_status_with_consumption_monoraria
func TestStatusWithConsumptionMonoraria(t *testing.T) {
	e := newEnv(t)
	u := luceFissaMono(0.145, 72.0)
	u.Luce.ConsumoF1 = fp(2700.0)
	e.save(u)
	text := onlySend(t, e.command(status, "/status")).Text()
	assertContains(t, text, "Consumo:", "2700", "kWh/anno")
	// Non deve mostrare breakdown fasce per monoraria
	assertNotContains(t, text, "F1:")
}

// test_status_with_consumption_bioraria
func TestStatusWithConsumptionBioraria(t *testing.T) {
	e := newEnv(t)
	e.save(database.User{Luce: database.Utility{
		Tipo: "variabile", Fascia: "bioraria", Energia: 0.015, Commercializzazione: 80.0,
		ConsumoF1: fp(1200.0), ConsumoF2: fp(1500.0),
	}})
	text := onlySend(t, e.command(status, "/status")).Text()
	assertContains(t, text, "Consumo:", "2700", "kWh/anno", "F1: 1200 kWh", "F23: 1500 kWh")
}

// test_status_with_consumption_trioraria
func TestStatusWithConsumptionTrioraria(t *testing.T) {
	e := newEnv(t)
	e.save(database.User{Luce: database.Utility{
		Tipo: "variabile", Fascia: "trioraria", Energia: 0.012, Commercializzazione: 96.0,
		ConsumoF1: fp(900.0), ConsumoF2: fp(900.0), ConsumoF3: fp(900.0),
	}})
	text := onlySend(t, e.command(status, "/status")).Text()
	assertContains(t, text, "Consumo:", "2700", "kWh/anno", "F1: 900 kWh", "F2: 900 kWh", "F3: 900 kWh")
}

// test_status_with_consumption_gas
func TestStatusWithConsumptionGas(t *testing.T) {
	e := newEnv(t)
	e.save(database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.140, Commercializzazione: 70.0, ConsumoF1: fp(2500.0)},
		Gas:  &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.350, Commercializzazione: 120.0, ConsumoAnnuo: fp(1200.0)},
	})
	text := onlySend(t, e.command(status, "/status")).Text()
	assertContains(t, text, "2500", "kWh/anno", "1200", "Smc/anno")
}

// test_status_backward_compat_no_consumption
func TestStatusBackwardCompatNoConsumption(t *testing.T) {
	e := newEnv(t)
	e.save(database.User{
		Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0},
		Gas:  &database.Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.025, Commercializzazione: 100.0},
	})
	text := onlySend(t, e.command(status, "/status")).Text()
	assertContains(t, text, "Luce", "0,145", "Gas")
	assertNotContains(t, text, "Consumo:")
}

// test_remove_command
func TestRemoveCommand(t *testing.T) {
	e := newEnv(t)
	e.save(luceFissaMono(0.145, 72.0))
	res := e.command(removeData, "/remove")
	if e.s.LoadUser(testUserID) != nil {
		t.Error("utente non rimosso")
	}
	onlySend(t, res)
	assertEnd(t, res)
}

// test_cancel_command
func TestCancelCommand(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, "luce_energia", 0.145)
	e.h.UserData.Set(testutil.DefaultUserID, "is_variabile", false)
	res := e.command(cancelConv, "/cancel")
	if len(res.userAfter) != 0 {
		t.Errorf("user_data non pulito: %v", res.userAfter)
	}
	assertEnd(t, res)
	assertContains(t, strings.ToLower(onlySend(t, res).Text()), "annullat")
}

// test_cancel_command_no_conversation
func TestCancelCommandNoConversation(t *testing.T) {
	e := newEnv(t)
	if len(e.h.UserData.Snapshot(testutil.DefaultUserID)) != 0 {
		t.Fatal("user_data non vuoto")
	}
	res := e.command(cancelConv, "/cancel")
	assertEnd(t, res)
	onlySend(t, res)
}

// test_help_command_clears_conversation_context
func TestHelpCommandClearsConversationContext(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, "luce_energia", 0.145)
	e.h.UserData.Set(testutil.DefaultUserID, "is_variabile", false)
	res := e.command(helpCommand, "/help")
	if len(res.userAfter) != 0 {
		t.Errorf("user_data non pulito: %v", res.userAfter)
	}
	assertEnd(t, res)
	assertContains(t, onlySend(t, res).Text(), "/cancel")
}
