package reminder

// Port 1:1 di tests/test_reminder.py (stessi nomi in CamelCase, stessi
// scenari). Dove il test Python patcha reminder.send_notification qui si usa
// il FakeClient di testutil: un invio riuscito equivale a send_notification
// che restituisce True, un errore API a False.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/testutil"
)

func expiringEqual(t *testing.T, got, want []ExpiringService) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expiring = %+v, atteso %+v", got, want)
	}
}

func noReminded() database.ScadenzaReminded { return database.ScadenzaReminded{} }

// ========== _get_expiring_services ==========

// test_expiring_luce_within_window
func TestExpiringLuceWithinWindow(t *testing.T) {
	user := fissaUser("2025-01-20", nil) // 19 giorni
	result := getExpiringServices(&user, noReminded(), TODAY)
	expiringEqual(t, result, []ExpiringService{{"luce", "2025-01-20", 19}})
}

// test_expiring_luce_exactly_30_days
func TestExpiringLuceExactly30Days(t *testing.T) {
	user := fissaUser("2025-01-31", nil) // 30 giorni → incluso
	result := getExpiringServices(&user, noReminded(), TODAY)
	expiringEqual(t, result, []ExpiringService{{"luce", "2025-01-31", 30}})
}

// test_not_expiring_beyond_window
func TestNotExpiringBeyondWindow(t *testing.T) {
	user := fissaUser("2025-03-01", nil) // ~59 giorni
	expiringEqual(t, getExpiringServices(&user, noReminded(), TODAY), nil)
}

// test_not_expiring_already_passed
func TestNotExpiringAlreadyPassed(t *testing.T) {
	user := fissaUser("2024-12-01", nil) // già scaduta
	expiringEqual(t, getExpiringServices(&user, noReminded(), TODAY), nil)
}

// test_not_expiring_variabile
func TestNotExpiringVariabile(t *testing.T) {
	user := fissaUser("2025-01-20", nil)
	user.Luce.Tipo = "variabile"
	expiringEqual(t, getExpiringServices(&user, noReminded(), TODAY), nil)
}

// test_not_expiring_without_scadenza
func TestNotExpiringWithoutScadenza(t *testing.T) {
	user := fissaUser("", nil)
	expiringEqual(t, getExpiringServices(&user, noReminded(), TODAY), nil)
}

// test_not_expiring_with_malformed_scadenza
func TestNotExpiringWithMalformedScadenza(t *testing.T) {
	// Una scadenza non parsabile viene ignorata senza errori
	user := fissaUser("data-non-valida", nil)
	expiringEqual(t, getExpiringServices(&user, noReminded(), TODAY), nil)
}

// test_not_expiring_already_reminded
func TestNotExpiringAlreadyReminded(t *testing.T) {
	user := fissaUser("2025-01-20", nil)
	reminded := database.ScadenzaReminded{Luce: sp("2025-01-20")}
	expiringEqual(t, getExpiringServices(&user, reminded, TODAY), nil)
}

// test_reminded_different_scadenza_still_fires
func TestRemindedDifferentScadenzaStillFires(t *testing.T) {
	// Se la scadenza è cambiata rispetto a quella già notificata, si notifica di nuovo
	user := fissaUser("2025-01-20", nil)
	reminded := database.ScadenzaReminded{Luce: sp("2024-06-01")} // vecchia scadenza
	expiringEqual(t, getExpiringServices(&user, reminded, TODAY), []ExpiringService{{"luce", "2025-01-20", 19}})
}

// test_expiring_gas
func TestExpiringGas(t *testing.T) {
	user := fissaUser("", fissaGas("2025-01-15"))
	result := getExpiringServices(&user, noReminded(), TODAY)
	expiringEqual(t, result, []ExpiringService{{"gas", "2025-01-15", 14}})
}

// test_expiring_both_luce_and_gas
func TestExpiringBothLuceAndGas(t *testing.T) {
	user := fissaUser("2025-01-20", fissaGas("2025-01-15"))
	result := getExpiringServices(&user, noReminded(), TODAY)
	contains := func(e ExpiringService) bool {
		for _, r := range result {
			if r == e {
				return true
			}
		}
		return false
	}
	if !contains(ExpiringService{"luce", "2025-01-20", 19}) || !contains(ExpiringService{"gas", "2025-01-15", 14}) {
		t.Errorf("expiring = %+v", result)
	}
}

// test_gas_variabile_not_expiring
func TestGasVariabileNotExpiring(t *testing.T) {
	gas := &database.Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.08, Commercializzazione: 84.0, Scadenza: "2025-01-15"}
	user := fissaUser("", gas)
	expiringEqual(t, getExpiringServices(&user, noReminded(), TODAY), nil)
}

// ========== _format_days_left ==========

// test_format_days_left_today
func TestFormatDaysLeftToday(t *testing.T) {
	if got := formatDaysLeft(0); got != "oggi" {
		t.Errorf("got %q", got)
	}
}

// test_format_days_left_one
func TestFormatDaysLeftOne(t *testing.T) {
	if got := formatDaysLeft(1); got != "tra 1 giorno" {
		t.Errorf("got %q", got)
	}
}

// test_format_days_left_many
func TestFormatDaysLeftMany(t *testing.T) {
	if got := formatDaysLeft(15); got != "tra 15 giorni" {
		t.Errorf("got %q", got)
	}
}

// ========== format_reminder_message ==========

// test_message_contains_core_info
func TestMessageContainsCoreInfo(t *testing.T) {
	user := fissaUser("2025-01-20", nil)
	expiring := []ExpiringService{{"luce", "2025-01-20", 19}}
	msg := FormatReminderMessage(expiring, &user, database.CurrentRates{})

	for _, s := range []string{"sta per scadere", "20/01/2025", "tra 19 giorni", "La tua tariffa", "variabile del momento"} {
		assertContains(t, msg, s)
	}
}

// test_message_includes_octopus_comparison
func TestMessageIncludesOctopusComparison(t *testing.T) {
	user := fissaUser("2025-01-20", nil)
	expiring := []ExpiringService{{"luce", "2025-01-20", 19}}
	current := database.CurrentRates{"luce": {"fissa": {"monoraria": {
		Energia: 0.130, Commercializzazione: fp(60.0), CodOfferta: sp("OCTO123"),
	}}}}
	msg := FormatReminderMessage(expiring, &user, current)
	assertContains(t, msg, "Offerta fissa Octopus oggi")
	assertContains(t, msg, "OCTO123")
}

// test_message_without_octopus_rate
func TestMessageWithoutOctopusRate(t *testing.T) {
	user := fissaUser("2025-01-20", nil)
	expiring := []ExpiringService{{"luce", "2025-01-20", 19}}
	msg := FormatReminderMessage(expiring, &user, database.CurrentRates{})
	assertNotContains(t, msg, "Offerta fissa Octopus oggi")
}

// ========== _prepare_reminder ==========

// test_prepare_reminder_none_when_nothing_expiring
func TestPrepareReminderNoneWhenNothingExpiring(t *testing.T) {
	user := fissaUser("2025-06-01", nil) // troppo lontana
	if _, _, ok := prepareReminder("1", &user, nil, database.CurrentRates{}, TODAY); ok {
		t.Error("atteso None")
	}
}

// test_prepare_reminder_returns_message
func TestPrepareReminderReturnsMessage(t *testing.T) {
	user := fissaUser("2025-01-20", nil)
	message, expiring, ok := prepareReminder("1", &user, nil, database.CurrentRates{}, TODAY)
	if !ok {
		t.Fatal("atteso un reminder")
	}
	assertContains(t, message, "sta per scadere")
	expiringEqual(t, expiring, []ExpiringService{{"luce", "2025-01-20", 19}})
}

// ========== check_and_send_reminders (integrazione) ==========

// test_check_and_send_no_users
func TestCheckAndSendNoUsers(t *testing.T) {
	logs := captureLogs(t)
	db := openTestDB(t)
	b, _ := testutil.NewBot(t)

	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}
	assertContains(t, logs.String(), "WARNING - ⚠️  Nessun utente registrato per il controllo scadenze")
}

// test_check_and_send_marks_reminded
func TestCheckAndSendMarksReminded(t *testing.T) {
	db := openTestDB(t)
	db.SaveUser("1", fissaUser("2025-01-20", nil))
	b, fc := testutil.NewBot(t)

	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}

	// È stato inviato un messaggio
	if n := len(fc.CallsTo("sendMessage")); n != 1 {
		t.Fatalf("atteso 1 invio, ottenuti %d", n)
	}
	// Ed è stato marcato come notificato
	reminded := db.GetScadenzaRemindedMap()
	if r := reminded["1"].Luce; r == nil || *r != "2025-01-20" {
		t.Errorf("reminded luce = %v", r)
	}
}

// test_check_and_send_not_marked_on_failure
func TestCheckAndSendNotMarkedOnFailure(t *testing.T) {
	db := openTestDB(t)
	db.SaveUser("1", fissaUser("2025-01-20", nil))
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", testutil.TelegramError("sendMessage", 500, "Internal Server Error")) // invio fallito

	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}

	reminded := db.GetScadenzaRemindedMap()
	if r := reminded["1"].Luce; r != nil {
		t.Errorf("reminded luce = %q, atteso None", *r)
	}
}

// test_check_and_send_skips_when_not_expiring
func TestCheckAndSendSkipsWhenNotExpiring(t *testing.T) {
	db := openTestDB(t)
	db.SaveUser("1", fissaUser("2025-06-01", nil)) // troppo lontana
	b, fc := testutil.NewBot(t)

	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}
	if n := len(fc.CallsTo("sendMessage")); n != 0 {
		t.Errorf("nessun invio atteso, ottenuti %d", n)
	}
}

// test_check_and_send_not_resent_if_already_reminded
func TestCheckAndSendNotResentIfAlreadyReminded(t *testing.T) {
	db := openTestDB(t)
	db.SaveUser("1", fissaUser("2025-01-20", nil))
	b, fc := testutil.NewBot(t)

	// Primo giro: invia
	if err := CheckAndSendReminders(context.Background(), b, db, dp(TODAY)); err != nil {
		t.Fatal(err)
	}
	if n := len(fc.CallsTo("sendMessage")); n != 1 {
		t.Fatalf("atteso 1 invio, ottenuti %d", n)
	}

	// Secondo giro (giorno dopo): non deve reinviare
	fc.Reset()
	if err := CheckAndSendReminders(context.Background(), b, db, dp(dateutil.NewDate(2025, time.January, 2))); err != nil {
		t.Fatal(err)
	}
	if n := len(fc.CallsTo("sendMessage")); n != 0 {
		t.Errorf("nessun nuovo invio atteso, ottenuti %d", n)
	}
}
