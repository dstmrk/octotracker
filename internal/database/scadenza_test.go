package database

// Porting di tests/test_database_scadenza.py: persistenza della scadenza
// delle offerte fisse e reminder.

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// baseUser costruisce un utente con scadenza opzionale (_base_user).
func baseUser(scadenza, gasScadenza string, gas bool) User {
	u := User{Luce: Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72.0, Scadenza: scadenza}}
	if gas {
		u.Gas = &Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.456, Commercializzazione: 84.0, Scadenza: gasScadenza}
	}
	return u
}

func strPtrEq(p *string, want string) bool { return p != nil && *p == want }

// ========== PERSISTENZA SCADENZA ==========

func TestSaveAndLoadLuceScadenza(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	if got := s.LoadUser("1").Luce.Scadenza; got != "2026-03-15" {
		t.Fatalf("scadenza = %q", got)
	}
}

func TestSaveAndLoadGasScadenza(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "2026-05-20", true))
	loaded := s.LoadUser("1")
	if loaded.Luce.Scadenza != "2026-03-15" || loaded.Gas.Scadenza != "2026-05-20" {
		t.Fatalf("scadenze = %q %q", loaded.Luce.Scadenza, loaded.Gas.Scadenza)
	}
}

func TestScadenzaAbsentWhenNotProvided(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("", "", false))
	loaded := s.LoadUser("1")
	if loaded.Luce.Scadenza != "" {
		t.Fatalf("scadenza deve essere assente, got %q", loaded.Luce.Scadenza)
	}
	if got := jsonString(t, loaded.Luce); got != `{"tipo":"fissa","fascia":"monoraria","energia":0.145,"commercializzazione":72}` {
		t.Fatalf("chiave scadenza presente: %s", got)
	}
	if rawColumn(t, s, "1", "luce_scadenza") != nil {
		t.Fatal("luce_scadenza deve essere NULL")
	}
}

func TestUpdateOverwritesScadenza(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	s.SaveUser("1", baseUser("2027-01-01", "", false))
	if got := s.LoadUser("1").Luce.Scadenza; got != "2027-01-01" {
		t.Fatalf("scadenza = %q", got)
	}
}

func TestUpdateCanClearScadenza(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	// Aggiornamento senza scadenza (es. passaggio a variabile) → azzerata
	s.SaveUser("1", baseUser("", "", false))
	if got := s.LoadUser("1").Luce.Scadenza; got != "" {
		t.Fatalf("scadenza non azzerata: %q", got)
	}
}

// ========== REMINDER MAP / MARK ==========

func TestRemindedMapDefaultsToNone(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	r := s.GetScadenzaRemindedMap()
	if got, ok := r["1"]; !ok || got.Luce != nil || got.Gas != nil {
		t.Fatalf("atteso {luce: None, gas: None}, got %+v (presente=%v)", got, ok)
	}
	if got := jsonString(t, r["1"]); got != `{"luce":null,"gas":null}` {
		t.Fatalf("JSON = %s", got)
	}
}

func TestMarkScadenzaRemindedLuce(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	if !s.MarkScadenzaReminded("1", "luce", "2026-03-15") {
		t.Fatal("atteso true")
	}
	r := s.GetScadenzaRemindedMap()["1"]
	if !strPtrEq(r.Luce, "2026-03-15") || r.Gas != nil {
		t.Fatalf("got luce=%v gas=%v", deref(r.Luce), deref(r.Gas))
	}
	if !strPtrEq(r.Get("luce"), "2026-03-15") || r.Get("gas") != nil || r.Get("acqua") != nil {
		t.Fatal("ScadenzaReminded.Get")
	}
}

func TestMarkScadenzaRemindedGas(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "2026-05-20", true))
	s.MarkScadenzaReminded("1", "gas", "2026-05-20")
	if r := s.GetScadenzaRemindedMap()["1"]; !strPtrEq(r.Gas, "2026-05-20") {
		t.Fatalf("gas = %v", deref(r.Gas))
	}
}

func TestMarkScadenzaRemindedInvalidService(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	if s.MarkScadenzaReminded("1", "acqua", "2026-03-15") {
		t.Fatal("atteso false")
	}
	assertLogContains(t, logs, "- database - ERROR - ❌ Servizio non valido per mark_scadenza_reminded: acqua")
}

// TestRemindedPreservedAcrossUpdate: il flag reminded non deve essere
// azzerato da un normale save_user.
func TestRemindedPreservedAcrossUpdate(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	s.MarkScadenzaReminded("1", "luce", "2026-03-15")
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	if r := s.GetScadenzaRemindedMap()["1"]; !strPtrEq(r.Luce, "2026-03-15") {
		t.Fatalf("reminded azzerato: %v", deref(r.Luce))
	}
}

// ========== MIGRAZIONE SCHEMA ==========

const legacyUsersTable = `
        CREATE TABLE users (
            user_id TEXT PRIMARY KEY,
            luce_tipo TEXT NOT NULL,
            luce_fascia TEXT NOT NULL,
            luce_energia REAL NOT NULL,
            luce_commercializzazione REAL NOT NULL,
            gas_tipo TEXT,
            gas_fascia TEXT,
            gas_energia REAL,
            gas_commercializzazione REAL,
            luce_consumo_f1 REAL,
            luce_consumo_f2 REAL,
            luce_consumo_f3 REAL,
            gas_consumo_annuo REAL,
            last_notified_rates TEXT,
            pending_rates TEXT,
            last_feedback_at TIMESTAMP,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
        `

// TestMigrationAddsColumnsToLegacyDb: init_db aggiunge le colonne scadenza a
// un DB creato senza di esse.
func TestMigrationAddsColumnsToLegacyDb(t *testing.T) {
	logs := captureLogs(t)
	legacy := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(legacyUsersTable); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO users (user_id, luce_tipo, luce_fascia, luce_energia, luce_commercializzazione)
		VALUES ('old', 'fissa', 'monoraria', 0.1, 70.0)`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	s, err := Open(legacy)
	if err != nil {
		t.Fatalf("init_db deve applicare la migrazione senza errori: %v", err)
	}
	defer s.Close()
	cols := userColumns(t, s)
	for _, c := range []string{"luce_scadenza", "gas_scadenza", "luce_scadenza_reminded", "gas_scadenza_reminded"} {
		if !cols[c] {
			t.Errorf("colonna %s mancante", c)
		}
	}
	assertLogContains(t, logs,
		"- database - INFO - 🔧 Migrazione DB: aggiunta colonna users.luce_scadenza",
		"- database - INFO - 🔧 Migrazione DB: aggiunta colonna users.gas_scadenza_reminded",
		"- database - INFO - ✅ Database inizializzato")
	// L'utente pre-esistente resta leggibile
	if u := s.LoadUser("old"); u == nil || u.Luce.Energia != 0.1 || u.Luce.Scadenza != "" {
		t.Fatalf("utente legacy: %+v", u)
	}
}

// TestMigrationIsIdempotent: chiamare init_db più volte non causa errori.
func TestMigrationIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.InitDB(); err != nil {
		t.Fatalf("seconda init_db: %v", err)
	}
	if !userColumns(t, s)["luce_scadenza"] {
		t.Fatal("luce_scadenza mancante")
	}
}

func TestRemindedMapDbError(t *testing.T) {
	logs := captureLogs(t)
	if got := brokenStore(t).GetScadenzaRemindedMap(); got == nil || len(got) != 0 {
		t.Fatalf("atteso dict vuoto, got %v", got)
	}
	assertLogContains(t, logs, "❌ Errore lettura reminder scadenza:")
}

func TestMarkRemindedDbError(t *testing.T) {
	logs := captureLogs(t)
	if brokenStore(t).MarkScadenzaReminded("1", "luce", "2026-03-15") {
		t.Fatal("atteso false")
	}
	assertLogContains(t, logs, "❌ Errore aggiornamento reminder scadenza per 1:")
}

// ========== update_user_scadenza ==========

func TestUpdateUserScadenzaSetsValueAndClearsReminded(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	s.MarkScadenzaReminded("1", "luce", "2026-03-15")
	if !s.UpdateUserScadenza("1", "luce", "2027-01-01") {
		t.Fatal("atteso true")
	}
	if got := s.LoadUser("1").Luce.Scadenza; got != "2027-01-01" {
		t.Fatalf("scadenza = %q", got)
	}
	// Il marker reminded deve essere azzerato per permettere un nuovo avviso
	if r := s.GetScadenzaRemindedMap()["1"]; r.Luce != nil {
		t.Fatalf("reminded non azzerato: %v", *r.Luce)
	}
}

func TestUpdateUserScadenzaInvalidService(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	s.SaveUser("1", baseUser("", "", false))
	if s.UpdateUserScadenza("1", "acqua", "2027-01-01") {
		t.Fatal("atteso false")
	}
	assertLogContains(t, logs, "❌ Servizio non valido per update_user_scadenza: acqua")
}

func TestUpdateUserScadenzaDbError(t *testing.T) {
	logs := captureLogs(t)
	if brokenStore(t).UpdateUserScadenza("1", "luce", "2027-01-01") {
		t.Fatal("atteso false")
	}
	assertLogContains(t, logs, "❌ Errore aggiornamento scadenza per 1:")
}

// ========== apply_pending_rates: reset scadenza servizi aggiornati ==========

func pendingLuce(energia, comm float64) Utility {
	return Utility{Tipo: "fissa", Fascia: "monoraria", Energia: energia, Commercializzazione: comm}
}

// TestApplyResetsScadenzaOnlyForUpdatedService: la scadenza del servizio
// aggiornato viene azzerata, l'altra preservata.
func TestApplyResetsScadenzaOnlyForUpdatedService(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "2026-05-20", true))
	gas := Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.456, Commercializzazione: 84.0}
	s.SavePendingRates("1", PendingRates{Luce: pendingLuce(0.130, 65.0), Gas: &gas, UpdatedServices: []string{"luce"}})

	if ok, _ := s.ApplyPendingRates("1"); !ok {
		t.Fatal("atteso success")
	}
	loaded := s.LoadUser("1")
	if loaded.Luce.Scadenza != "" {
		t.Fatalf("scadenza luce non azzerata: %q", loaded.Luce.Scadenza)
	}
	if loaded.Gas.Scadenza != "2026-05-20" {
		t.Fatalf("scadenza gas non preservata: %q", loaded.Gas.Scadenza)
	}
}

func TestApplyResetsRemindedForUpdatedService(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	s.MarkScadenzaReminded("1", "luce", "2026-03-15")
	s.SavePendingRates("1", PendingRates{Luce: pendingLuce(0.130, 65.0), UpdatedServices: []string{"luce"}})
	s.ApplyPendingRates("1")
	if r := s.GetScadenzaRemindedMap()["1"]; r.Luce != nil {
		t.Fatalf("reminded non azzerato: %v", *r.Luce)
	}
}

// TestApplyPreservesScadenzaWhenNoUpdatedServices: senza updated_services
// (backward compat) le scadenze restano intatte.
func TestApplyPreservesScadenzaWhenNoUpdatedServices(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", baseUser("2026-03-15", "", false))
	s.SavePendingRates("1", PendingRates{Luce: pendingLuce(0.130, 65.0)})
	if raw := rawColumn(t, s, "1", "pending_rates"); raw != `{"luce": {"tipo": "fissa", "fascia": "monoraria", "energia": 0.13, "commercializzazione": 65.0}}` {
		t.Fatalf("pending_rates senza updated_services: %v", raw)
	}
	s.ApplyPendingRates("1")
	if got := s.LoadUser("1").Luce.Scadenza; got != "2026-03-15" {
		t.Fatalf("scadenza non preservata: %q", got)
	}
}
