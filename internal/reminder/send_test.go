package reminder

// Test aggiuntivi (non presenti in tests/test_reminder.py): testo completo,
// log, entrambi i servizi, utenti bloccati, default di today, errori della
// FASE 1, parallelismo sul DB reale e Main.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/testutil"
)

func TestFormatReminderMessageFullText(t *testing.T) {
	user := fissaUser("2025-01-20", fissaGas("2025-01-01"))
	expiring := []ExpiringService{{"luce", "2025-01-20", 19}, {"gas", "2025-01-01", 0}}
	current := database.CurrentRates{
		"luce": {"fissa": {"monoraria": {Energia: 0.13, Commercializzazione: fp(60.0), CodOfferta: sp("OCTO123")}}},
		"gas":  {"fissa": {"monoraria": {Energia: 0.4}}}, // commercializzazione NULL
	}

	got := FormatReminderMessage(expiring, &user, current)

	want := "⏰ <b>La tua offerta a prezzo fisso sta per scadere</b>\n\n" +
		"💡 <b>Luce</b>\n" +
		"La tua offerta a prezzo fisso scade il <b>20/01/2025</b> (tra 19 giorni).\n" +
		"La tua tariffa: Prezzo fisso 0,145 €/kWh, Comm. 72 €/anno\n" +
		"Offerta fissa Octopus oggi: Prezzo fisso 0,13 €/kWh, Comm. 60 €/anno\n" +
		"📋 Codice offerta: <code>OCTO123</code>\n\n" +
		"🔥 <b>Gas</b>\n" +
		"La tua offerta a prezzo fisso scade il <b>01/01/2025</b> (oggi).\n" +
		"La tua tariffa: Prezzo fisso 0,456 €/Smc, Comm. 84 €/anno\n" +
		"Offerta fissa Octopus oggi: Prezzo fisso 0,40 €/Smc\n\n" +
		"ℹ️ Alla scadenza Octopus Energy applica in automatico la tariffa variabile del momento. " +
		"Se preferisci un'altra offerta, valuta le opzioni per tempo.\n\n" +
		"🔎 Con /history vedi lo storico delle tariffe, con /update aggiorni i tuoi dati.\n\n" +
		"🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe\n\n" +
		"⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy."
	assertSameText(t, "messaggio", got, want)
}

func TestCheckAndSendRemindersLogsAndMarksBothServices(t *testing.T) {
	fixedClock(t)
	logs := captureLogs(t)
	db := openTestDB(t)
	db.SaveUser("1", fissaUser("2025-01-20", fissaGas("2025-01-15")))
	db.SaveUser("2", fissaUser("2025-06-01", nil)) // fuori finestra
	b, fc := testutil.NewBot(t)

	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}

	calls := fc.CallsTo("sendMessage")
	if len(calls) != 1 {
		t.Fatalf("atteso 1 invio, ottenuti %d", len(calls))
	}
	c := calls[0]
	if c.ChatID() != "1" || c.ParseMode() != "HTML" {
		t.Errorf("parametri = %v", c.Params)
	}
	if _, ok := c.Params["reply_markup"]; ok {
		t.Error("il reminder non ha tastiera (reply_markup=None)")
	}
	r := db.GetScadenzaRemindedMap()["1"]
	if r.Luce == nil || *r.Luce != "2025-01-20" || r.Gas == nil || *r.Gas != "2025-01-15" {
		t.Errorf("reminded = %+v", r)
	}
	if r2 := db.GetScadenzaRemindedMap()["2"]; r2.Luce != nil {
		t.Errorf("utente 2 non doveva essere marcato: %v", *r2.Luce)
	}
	want := "INFO - 📅 Inizio controllo scadenze offerte fisse...\n" +
		"INFO - 📅 Utente 1: reminder scadenza accodato (luce, gas)\n" +
		"INFO - 📨 Invio 1 reminder scadenza in parallelo...\n" +
		"INFO - ✅ Reminder scadenza inviato a 1\n" +
		"INFO - ✅ Reminder scadenze completato in 0.00s - Inviati: 1/2\n"
	if got := stripLogPrefix(logs.String(), "reminder"); got != want {
		t.Errorf("log =\n%s\natteso\n%s", got, want)
	}
}

func TestCheckAndSendRemindersFailureLogs(t *testing.T) {
	fixedClock(t)
	logs := captureLogs(t)
	db := openTestDB(t)
	db.SaveUser("1", fissaUser("2025-01-20", nil))
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", testutil.RetryAfterError("sendMessage", 7))

	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}

	got := stripLogPrefix(logs.String(), "reminder")
	assertContains(t, got, "WARNING - ❌ Reminder scadenza fallito per 1\n")
	assertContains(t, got, "INFO - ✅ Reminder scadenze completato in 0.00s - Inviati: 0/1\n")
	assertContains(t, stripLogPrefix(logs.String(), "checker"), "WARNING - ⏱️  Rate limit per utente 1: riprova tra 7s\n")
}

// send_notification rimuove l'utente che ha bloccato il bot anche per i reminder.
func TestCheckAndSendRemindersRemovesBlockedUser(t *testing.T) {
	db := openTestDB(t)
	db.SaveUser("1", fissaUser("2025-01-20", nil))
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"))

	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}
	if db.UserExists("1") {
		t.Error("l'utente doveva essere rimosso")
	}
}

// today nil equivale a date.today() (ora locale).
func TestCheckAndSendRemindersDefaultsToToday(t *testing.T) {
	prev := dateutil.Now
	dateutil.Now = func() time.Time { return time.Date(2025, 1, 1, 10, 0, 0, 0, time.Local) }
	t.Cleanup(func() { dateutil.Now = prev })

	db := openTestDB(t)
	db.SaveUser("1", fissaUser("2025-01-31", nil)) // 30 giorni dal 2025-01-01
	b, fc := testutil.NewBot(t)

	if err := CheckAndSendReminders(context.Background(), b, db, nil); err != nil {
		t.Fatal(err)
	}
	calls := fc.CallsTo("sendMessage")
	if len(calls) != 1 {
		t.Fatalf("atteso 1 invio, ottenuti %d", len(calls))
	}
	assertContains(t, calls[0].Text(), "(tra 30 giorni)")
}

// Senza tariffe correnti (get_current_rates() → None) il reminder parte
// comunque, senza il confronto con l'offerta Octopus.
func TestCheckAndSendRemindersWithoutRates(t *testing.T) {
	store := &fakeStore{users: []*database.User{{ID: "9", Luce: fissaUser("2025-01-02", nil).Luce}}}
	b, fc := testutil.NewBot(t)

	if err := CheckAndSendReminders(context.Background(), b, store, dp(TODAY)); err != nil {
		t.Fatal(err)
	}
	c := fc.LastCall(t, "sendMessage")
	assertContains(t, c.Text(), "(tra 1 giorno)")
	assertNotContains(t, c.Text(), "Offerta fissa Octopus oggi")
	if len(store.marked) != 1 || store.marked[0] != "9/luce/2025-01-02" {
		t.Errorf("marked = %v", store.marked)
	}
}

// Un'eccezione nella FASE 1 (es. OverflowError da format_number per una
// tariffa infinita) esce da check_and_send_reminders: nessun invio.
func TestCheckAndSendRemindersPhaseOneError(t *testing.T) {
	luce := fissaUser("2025-01-20", nil).Luce
	luce.Energia = math.Inf(1)
	store := &fakeStore{users: []*database.User{
		{ID: "1", Luce: fissaUser("2025-01-20", nil).Luce},
		{ID: "2", Luce: luce},
	}}
	b, fc := testutil.NewBot(t)

	err := CheckAndSendReminders(context.Background(), b, store, dp(TODAY))

	if err == nil || err.Error() != "cannot convert float infinity to integer" {
		t.Fatalf("atteso OverflowError, ottenuto %v", err)
	}
	if n := len(fc.CallsTo("sendMessage")); n != 0 {
		t.Errorf("nessun invio atteso, ottenuti %d", n)
	}
}

func TestPrepareRemindersRecoversNonStringPanic(t *testing.T) {
	// Un servizio sconosciuto in formatServiceSection (KeyError in Python)
	// va in panic; prepareReminders lo converte in errore.
	reminders, err := func() ([]queuedReminder, error) {
		defer func() { _ = recover() }()
		return prepareReminders([]*database.User{nil}, nil, nil, TODAY)
	}()
	if err == nil || reminders != nil {
		t.Errorf("atteso errore, ottenuti %v, %v", reminders, err)
	}
}

func TestUserUtilityUnknownService(t *testing.T) {
	user := fissaUser("2025-01-20", nil)
	if userUtility(&user, "acqua") != nil {
		t.Error("servizio sconosciuto: atteso nil")
	}
	if userUtility(&user, "gas") != nil || userUtility(&user, "luce") == nil {
		t.Error("userUtility errato")
	}
}

func TestMainRequiresToken(t *testing.T) {
	if err := Main(context.Background(), "", &fakeStore{}); !errors.Is(err, checker.ErrMissingToken) {
		t.Errorf("atteso ErrMissingToken, ottenuto %v", err)
	}
	// Con token e senza utenti termina senza chiamate di rete.
	if err := Main(context.Background(), "123:abc", &fakeStore{}); err != nil {
		t.Errorf("errore inatteso: %v", err)
	}
}

// Molti reminder in parallelo sul database reale (eseguito con -race).
func TestCheckAndSendRemindersManyUsersParallel(t *testing.T) {
	db := openTestDB(t)
	const n = 33
	for i := range n {
		db.SaveUser(fmt.Sprint(2000+i), fissaUser("2025-01-10", fissaGas("2025-01-20")))
	}
	b, fc := testutil.NewBot(t)
	fc.SetResponder(func(method string, params map[string]any) (json.RawMessage, error) {
		if id, _ := params["chat_id"].(int64); method == "sendMessage" && id%4 == 0 {
			return nil, testutil.TelegramError(method, 500, "Internal Server Error")
		}
		return nil, nil
	})

	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}

	if got := len(fc.CallsTo("sendMessage")); got != n {
		t.Errorf("invii = %d, attesi %d", got, n)
	}
	for id, r := range db.GetScadenzaRemindedMap() {
		var v int
		fmt.Sscan(id, &v)
		marked := r.Luce != nil && r.Gas != nil
		if marked == (v%4 == 0) {
			t.Errorf("utente %s: marcato=%v", id, marked)
		}
	}
}
