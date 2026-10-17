package database

// Porting di tests/test_integration.py: flussi completi con SQLite reale.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func luceOnly(tipo, fascia string, energia, comm float64) User {
	return User{Luce: Utility{Tipo: tipo, Fascia: fascia, Energia: energia, Commercializzazione: comm}}
}

// TestUserRegistrationFlow: save → exists → load → count.
func TestUserRegistrationFlow(t *testing.T) {
	s := newTestStore(t)
	userID := "123456789"
	if !s.SaveUser(userID, luceOnly("fissa", "monoraria", 0.145, 72.0)) {
		t.Fatal("save_user dovrebbe restituire True per nuovo utente")
	}
	if !s.UserExists(userID) {
		t.Fatal("user_exists dovrebbe restituire True")
	}
	loaded := s.LoadUser(userID)
	if loaded == nil {
		t.Fatal("load_user dovrebbe restituire dati")
	}
	if loaded.ID != userID || loaded.Luce.Tipo != "fissa" || loaded.Luce.Fascia != "monoraria" ||
		loaded.Luce.Energia != 0.145 || loaded.Luce.Commercializzazione != 72.0 {
		t.Fatalf("dati luce errati: %+v", loaded.Luce)
	}
	if loaded.Gas != nil {
		t.Fatal("gas non dovrebbe essere presente quando è None")
	}
	if got := jsonString(t, loaded); got != `{"luce":{"tipo":"fissa","fascia":"monoraria","energia":0.145,"commercializzazione":72}}` {
		t.Fatalf("dict utente = %s", got)
	}
	if n := s.GetUserCount(); n != 1 {
		t.Fatalf("Dovrebbe esserci 1 utente, got %d", n)
	}
}

func TestUserRegistrationWithGasFlow(t *testing.T) {
	s := newTestStore(t)
	userID := "987654321"
	u := luceOnly("variabile", "trioraria", 0.012, 96.0)
	u.Gas = &Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.025, Commercializzazione: 120.0}
	if !s.SaveUser(userID, u) {
		t.Fatal("save_user fallito")
	}
	loaded := s.LoadUser(userID)
	if loaded.Luce.Tipo != "variabile" || loaded.Luce.Fascia != "trioraria" || loaded.Luce.Energia != 0.012 {
		t.Fatalf("luce errata: %+v", loaded.Luce)
	}
	if loaded.Gas == nil || loaded.Gas.Tipo != "variabile" || loaded.Gas.Energia != 0.025 || loaded.Gas.Commercializzazione != 120.0 {
		t.Fatalf("gas errato: %+v", loaded.Gas)
	}
}

func TestUserUpdateFlow(t *testing.T) {
	s := newTestStore(t)
	userID := "111222333"
	if !s.SaveUser(userID, luceOnly("fissa", "monoraria", 0.150, 60.0)) {
		t.Fatal("registrazione iniziale fallita")
	}
	updated := luceOnly("variabile", "trioraria", 0.010, 96.0)
	updated.Gas = &Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.350, Commercializzazione: 100.0}
	if !s.SaveUser(userID, updated) {
		t.Fatal("aggiornamento fallito")
	}
	loaded := s.LoadUser(userID)
	if loaded.Luce.Tipo != "variabile" || loaded.Luce.Energia != 0.010 || loaded.Gas == nil || loaded.Gas.Energia != 0.350 {
		t.Fatalf("dati non aggiornati: %+v %+v", loaded.Luce, loaded.Gas)
	}
	if n := s.GetUserCount(); n != 1 {
		t.Fatalf("utente duplicato: count %d", n)
	}
}

func TestUserRemovalFlow(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	userID := "444555666"
	if !s.SaveUser(userID, luceOnly("fissa", "monoraria", 0.140, 80.0)) || !s.UserExists(userID) {
		t.Fatal("setup fallito")
	}
	if !s.RemoveUser(userID) {
		t.Fatal("remove_user dovrebbe restituire True")
	}
	assertLogContains(t, logs, "- database - INFO - 🗑️  Utente 444555666 rimosso dal database")
	if s.UserExists(userID) {
		t.Fatal("Utente non dovrebbe più esistere")
	}
	if s.LoadUser(userID) != nil {
		t.Fatal("load_user dovrebbe restituire None")
	}
	if s.GetUserCount() != 0 {
		t.Fatal("Non dovrebbero esserci utenti")
	}
	if s.RemoveUser(userID) {
		t.Fatal("Rimozione utente inesistente dovrebbe restituire False")
	}
}

func TestMultipleUsersFlow(t *testing.T) {
	s := newTestStore(t)
	user2 := luceOnly("variabile", "trioraria", 0.015, 90.0)
	user2.Gas = &Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.300, Commercializzazione: 110.0}
	users := []struct {
		id string
		u  User
	}{
		{"user1", luceOnly("fissa", "monoraria", 0.140, 70.0)},
		{"user2", user2},
		{"user3", luceOnly("fissa", "monoraria", 0.130, 65.0)},
	}
	for _, x := range users {
		if !s.SaveUser(x.id, x.u) {
			t.Fatalf("save %s fallito", x.id)
		}
	}
	if n := s.GetUserCount(); n != 3 {
		t.Fatalf("Dovrebbero esserci 3 utenti, got %d", n)
	}
	loaded := s.LoadUsers()
	if len(loaded) != 3 {
		t.Fatalf("load_users dovrebbe restituire 3 utenti, got %d", len(loaded))
	}
	byID := map[string]*User{}
	for i, u := range loaded {
		byID[u.ID] = u
		// Stesso ordine di iterazione del dict Python (ordine di inserimento)
		if u.ID != users[i].id {
			t.Fatalf("ordine load_users: posizione %d = %s, atteso %s", i, u.ID, users[i].id)
		}
	}
	if byID["user1"].Luce.Energia != 0.140 || byID["user2"].Gas.Energia != 0.300 || byID["user3"].Luce.Commercializzazione != 65.0 {
		t.Fatal("dati utenti errati")
	}
}

func TestValidationInvalidTipo(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	if s.SaveUser("999", luceOnly("invalido", "monoraria", 0.140, 70.0)) {
		t.Fatal("save_user dovrebbe fallire con tipo invalido")
	}
	if s.UserExists("999") {
		t.Fatal("utente non deve essere salvato")
	}
	assertLogContains(t, logs,
		"- database - ERROR - ❌ Validazione fallita per 999: luce.tipo non valido: 'invalido'. Valori ammessi: {'fissa', 'variabile'}",
		"- database - DEBUG -    user_data ricevuto: {'luce': {'tipo': 'invalido', 'fascia': 'monoraria', 'energia': 0.14, 'commercializzazione': 70.0}}")
}

func TestValidationInvalidFasciaLuce(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	if s.SaveUser("888", luceOnly("fissa", "quadrioraria", 0.140, 70.0)) {
		t.Fatal("save_user dovrebbe fallire con fascia invalida")
	}
	if s.UserExists("888") {
		t.Fatal("utente non deve essere salvato")
	}
	assertLogContains(t, logs, "luce.fascia non valida: 'quadrioraria'. Valori ammessi: {'bioraria', 'monoraria', 'trioraria'}")
}

func TestValidationInvalidFasciaGas(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	u := luceOnly("fissa", "monoraria", 0.140, 70.0)
	u.Gas = &Utility{Tipo: "fissa", Fascia: "bioraria", Energia: 0.300, Commercializzazione: 100.0}
	if s.SaveUser("777", u) {
		t.Fatal("save_user dovrebbe fallire con fascia gas invalida")
	}
	if s.UserExists("777") {
		t.Fatal("utente non deve essere salvato")
	}
	assertLogContains(t, logs, "gas.fascia non valida: 'bioraria'. Valori ammessi: {'monoraria'}")

	u.Gas = &Utility{Tipo: "boh", Fascia: "monoraria"}
	if s.SaveUser("777", u) {
		t.Fatal("save_user dovrebbe fallire con tipo gas invalido")
	}
	assertLogContains(t, logs, "gas.tipo non valido: 'boh'. Valori ammessi: {'fissa', 'variabile'}")
}

// TestValidationMissingField: in Python manca 'energia' (KeyError → False).
// Con il modello tipizzato un campo numerico non può mancare; il campo
// obbligatorio mancante rappresentabile è una stringa vuota (tipo), che
// fallisce la validazione. Il caso "chiave mancante" reale in Go è il blob
// pending_rates, verificato con ApplyPendingRates.
func TestValidationMissingField(t *testing.T) {
	s := newTestStore(t)
	if s.SaveUser("666", User{Luce: Utility{Fascia: "monoraria", Commercializzazione: 70.0}}) {
		t.Fatal("save_user dovrebbe fallire con campo mancante")
	}
	if s.UserExists("666") {
		t.Fatal("utente non deve essere salvato")
	}

	// Chiave 'energia' mancante nel JSON delle pending → validation_error
	logs := captureLogs(t)
	s.SaveUser("666", luceOnly("fissa", "monoraria", 0.140, 70.0))
	if _, err := s.db.Exec(`UPDATE users SET pending_rates = '{"luce": {"tipo": "fissa", "fascia": "monoraria", "commercializzazione": 70.0}}' WHERE user_id = '666'`); err != nil {
		t.Fatal(err)
	}
	if ok, reason := s.ApplyPendingRates("666"); ok || reason != ApplyValidationError {
		t.Fatalf("atteso validation_error, got %v %s", ok, reason)
	}
	assertLogContains(t, logs, "❌ Validazione fallita per 666: 'energia'")
	if u := s.LoadUser("666"); u.Luce.Energia != 0.140 {
		t.Fatal("utente modificato nonostante la validazione fallita")
	}
}

// TestLastNotifiedRatesPersistence: persistenza di last_notified_rates (usato dal checker).
func TestLastNotifiedRatesPersistence(t *testing.T) {
	s := newTestStore(t)
	userID := "555444333"
	u := luceOnly("variabile", "monoraria", 0.010, 96.0)
	u.LastNotifiedRates = LastNotifiedRates{
		"luce_energia": 0.120,
		"luce_comm":    72.0,
		"timestamp":    "2024-01-15T10:00:00",
	}
	if !s.SaveUser(userID, u) {
		t.Fatal("save_user fallito")
	}
	loaded := s.LoadUser(userID)
	if loaded.LastNotifiedRates == nil {
		t.Fatal("last_notified_rates assente")
	}
	if loaded.LastNotifiedRates["luce_energia"] != 0.120 || loaded.LastNotifiedRates["timestamp"] != "2024-01-15T10:00:00" {
		t.Fatalf("valori errati: %v", loaded.LastNotifiedRates)
	}
	if !loaded.LastNotifiedRates.Equal(u.LastNotifiedRates) {
		t.Fatal("Equal dopo round-trip")
	}
	// Stesso testo che produrrebbe json.dumps di Python (chiavi non note in ordine alfabetico)
	if raw := rawColumn(t, s, userID, "last_notified_rates"); raw != `{"luce_comm": 72.0, "luce_energia": 0.12, "timestamp": "2024-01-15T10:00:00"}` {
		t.Fatalf("blob = %v", raw)
	}
}

// TestDatabaseIsolation: il database temporaneo è isolato da quello di produzione.
func TestDatabaseIsolation(t *testing.T) {
	s := newTestStore(t)
	if s.GetUserCount() != 0 {
		t.Fatal("Database temporaneo dovrebbe iniziare vuoto")
	}
	s.SaveUser("test_isolation", luceOnly("fissa", "monoraria", 0.150, 80.0))
	if s.GetUserCount() != 1 {
		t.Fatal("utente non salvato")
	}
	if !strings.HasPrefix(s.Path(), os.TempDir()) {
		t.Fatalf("il DB non è nella directory temporanea: %s", s.Path())
	}
}

// TestDatabaseConnectionErrorHandling: path non valido / connessione non
// disponibile → load_users restituisce {}.
func TestDatabaseConnectionErrorHandling(t *testing.T) {
	logs := captureLogs(t)
	// La directory padre è un file: il database non può essere aperto.
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(filepath.Join(notDir, "invalid", "db.sqlite")); err == nil {
		_ = s.Close()
		t.Fatal("Open doveva fallire")
	}
	if _, err := Open("data/x?.db"); err == nil {
		t.Fatal("Open doveva rifiutare un path con '?'")
	}

	users := brokenStore(t).LoadUsers()
	if users == nil || len(users) != 0 {
		t.Fatalf("load_users dovrebbe restituire {} in caso di errore, got %v", users)
	}
	assertLogContains(t, logs, "❌ Errore caricamento utenti:")
}

func TestUserConsumptionMonoraria(t *testing.T) {
	s := newTestStore(t)
	u := luceOnly("fissa", "monoraria", 0.145, 72.0)
	u.Luce.ConsumoF1 = fp(2700.0)
	if !s.SaveUser("consumption_mono_001", u) {
		t.Fatal("save fallito")
	}
	loaded := s.LoadUser("consumption_mono_001")
	if loaded.Luce.Tipo != "fissa" || loaded.Luce.Fascia != "monoraria" {
		t.Fatal("tariffe errate")
	}
	if loaded.Luce.ConsumoF1 == nil || *loaded.Luce.ConsumoF1 != 2700.0 {
		t.Fatalf("consumo_f1 = %v", deref(loaded.Luce.ConsumoF1))
	}
	if loaded.Luce.ConsumoF2 != nil || loaded.Luce.ConsumoF3 != nil {
		t.Fatal("consumo_f2/f3 non dovrebbero essere presenti")
	}
}

func TestUserConsumptionBioraria(t *testing.T) {
	s := newTestStore(t)
	u := luceOnly("variabile", "bioraria", 0.015, 80.0)
	u.Luce.ConsumoF1, u.Luce.ConsumoF2 = fp(1200.0), fp(1500.0)
	if !s.SaveUser("consumption_bi_002", u) {
		t.Fatal("save fallito")
	}
	loaded := s.LoadUser("consumption_bi_002")
	if loaded.Luce.Tipo != "variabile" || loaded.Luce.Fascia != "bioraria" {
		t.Fatal("tariffe errate")
	}
	if *loaded.Luce.ConsumoF1 != 1200.0 || *loaded.Luce.ConsumoF2 != 1500.0 || loaded.Luce.ConsumoF3 != nil {
		t.Fatalf("consumi errati: %+v", loaded.Luce)
	}
}

func TestUserConsumptionTrioraria(t *testing.T) {
	s := newTestStore(t)
	u := luceOnly("variabile", "trioraria", 0.012, 96.0)
	u.Luce.ConsumoF1, u.Luce.ConsumoF2, u.Luce.ConsumoF3 = fp(900.0), fp(900.0), fp(900.0)
	if !s.SaveUser("consumption_tri_003", u) {
		t.Fatal("save fallito")
	}
	loaded := s.LoadUser("consumption_tri_003")
	if loaded.Luce.Tipo != "variabile" || loaded.Luce.Fascia != "trioraria" {
		t.Fatal("tariffe errate")
	}
	if *loaded.Luce.ConsumoF1 != 900.0 || *loaded.Luce.ConsumoF2 != 900.0 || *loaded.Luce.ConsumoF3 != 900.0 {
		t.Fatalf("consumi errati: %+v", loaded.Luce)
	}
	if got := jsonString(t, loaded.Luce); got != `{"tipo":"variabile","fascia":"trioraria","energia":0.012,"commercializzazione":96,"consumo_f1":900,"consumo_f2":900,"consumo_f3":900}` {
		t.Fatalf("dict luce = %s", got)
	}
}

func TestUserConsumptionGas(t *testing.T) {
	s := newTestStore(t)
	u := luceOnly("fissa", "monoraria", 0.140, 70.0)
	u.Luce.ConsumoF1 = fp(2500.0)
	u.Gas = &Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.350, Commercializzazione: 120.0, ConsumoAnnuo: fp(1200.0)}
	if !s.SaveUser("consumption_gas_004", u) {
		t.Fatal("save fallito")
	}
	loaded := s.LoadUser("consumption_gas_004")
	if *loaded.Luce.ConsumoF1 != 2500.0 {
		t.Fatal("consumo luce errato")
	}
	if loaded.Gas == nil || loaded.Gas.ConsumoAnnuo == nil || *loaded.Gas.ConsumoAnnuo != 1200.0 {
		t.Fatalf("consumo gas errato: %+v", loaded.Gas)
	}
}

// TestBackwardCompatibilityNoConsumption: utenti senza consumi (NULL nel DB).
func TestBackwardCompatibilityNoConsumption(t *testing.T) {
	s := newTestStore(t)
	u := luceOnly("fissa", "monoraria", 0.145, 72.0)
	u.Gas = &Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.025, Commercializzazione: 100.0}
	if !s.SaveUser("old_user_005", u) {
		t.Fatal("save fallito")
	}
	loaded := s.LoadUser("old_user_005")
	if loaded.Luce.Tipo != "fissa" || loaded.Gas.Tipo != "variabile" {
		t.Fatal("tariffe errate")
	}
	if loaded.Luce.ConsumoF1 != nil || loaded.Luce.ConsumoF2 != nil || loaded.Luce.ConsumoF3 != nil || loaded.Gas.ConsumoAnnuo != nil {
		t.Fatal("i campi consumo non devono essere presenti")
	}
}

// TestUpdateAddConsumption: utente vecchio che aggiorna i dati aggiungendo i consumi.
func TestUpdateAddConsumption(t *testing.T) {
	s := newTestStore(t)
	userID := "update_consumption_006"
	if !s.SaveUser(userID, luceOnly("fissa", "monoraria", 0.150, 60.0)) {
		t.Fatal("registrazione iniziale fallita")
	}
	if s.LoadUser(userID).Luce.ConsumoF1 != nil {
		t.Fatal("non dovrebbero esserci consumi")
	}
	u := luceOnly("fissa", "monoraria", 0.150, 60.0)
	u.Luce.ConsumoF1 = fp(3000.0)
	u.Gas = &Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.380, Commercializzazione: 110.0, ConsumoAnnuo: fp(1400.0)}
	if !s.SaveUser(userID, u) {
		t.Fatal("aggiornamento fallito")
	}
	loaded := s.LoadUser(userID)
	if *loaded.Luce.ConsumoF1 != 3000.0 || *loaded.Gas.ConsumoAnnuo != 1400.0 {
		t.Fatal("consumi non aggiunti")
	}
	if s.GetUserCount() != 1 {
		t.Fatal("update, non insert")
	}
}
