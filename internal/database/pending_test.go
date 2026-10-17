package database

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSaveLoadClearPendingRates(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", luceOnly("fissa", "monoraria", 0.15, 70))
	if s.LoadPendingRates("1") != nil {
		t.Fatal("nessuna pending: atteso nil")
	}
	p := PendingRates{
		Luce:            Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.13, Commercializzazione: 65, ConsumoF1: fp(2700)},
		Gas:             &Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.05, Commercializzazione: 84, ConsumoAnnuo: fp(1000)},
		UpdatedServices: []string{"luce"},
	}
	if !s.SavePendingRates("1", p) {
		t.Fatal("SavePendingRates fallito")
	}
	// Stesso testo di json.dumps(_build_pending_rates(...))
	want := `{"luce": {"tipo": "fissa", "fascia": "monoraria", "consumo_f1": 2700.0, "energia": 0.13, "commercializzazione": 65.0}, ` +
		`"gas": {"tipo": "variabile", "fascia": "monoraria", "consumo_annuo": 1000.0, "energia": 0.05, "commercializzazione": 84.0}, ` +
		`"updated_services": ["luce"]}`
	if raw := rawColumn(t, s, "1", "pending_rates"); raw != want {
		t.Fatalf("blob:\n got  %v\n want %s", raw, want)
	}
	loaded := s.LoadPendingRates("1")
	if loaded == nil || jsonString(t, loaded) != jsonString(t, p) {
		t.Fatalf("round-trip: %+v", loaded)
	}
	if !s.ClearPendingRates("1") {
		t.Fatal("ClearPendingRates fallito")
	}
	if s.LoadPendingRates("1") != nil || rawColumn(t, s, "1", "pending_rates") != nil {
		t.Fatal("pending non rimosse")
	}
	// Utente inesistente: UPDATE senza righe → True come in Python
	if !s.SavePendingRates("ghost", p) || !s.ClearPendingRates("ghost") {
		t.Fatal("UPDATE senza righe deve restituire true")
	}
	if s.LoadPendingRates("ghost") != nil {
		t.Fatal("utente inesistente: atteso nil")
	}
}

func TestLoadPendingRatesMalformed(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	s.SaveUser("1", luceOnly("fissa", "monoraria", 0.15, 70))
	for _, raw := range []string{"{non json", "[1, 2]"} {
		if _, err := s.db.Exec("UPDATE users SET pending_rates = ? WHERE user_id = '1'", raw); err != nil {
			t.Fatal(err)
		}
		if p := s.LoadPendingRates("1"); p != nil {
			t.Fatalf("%s: atteso nil, got %+v", raw, p)
		}
	}
	assertLogContains(t, logs, "❌ Errore caricamento pending_rates per 1:")
	// Formato permissivo: valori mancanti → zero, updated_services non lista → []
	if _, err := s.db.Exec(`UPDATE users SET pending_rates = '{"luce": {"tipo": "fissa", "energia": 1, "consumo_f1": null}, "gas": null, "updated_services": "luce"}' WHERE user_id = '1'`); err != nil {
		t.Fatal(err)
	}
	p := s.LoadPendingRates("1")
	if p.Luce.Tipo != "fissa" || p.Luce.Fascia != "" || p.Luce.Energia != 1 || p.Luce.ConsumoF1 != nil || p.Gas != nil || p.UpdatedServices == nil || len(p.UpdatedServices) != 0 {
		t.Fatalf("decodifica permissiva: %+v", p)
	}
}

func TestPendingDbErrors(t *testing.T) {
	logs := captureLogs(t)
	b := brokenStore(t)
	if b.SavePendingRates("1", PendingRates{Luce: pendingLuce(1, 1)}) {
		t.Fatal("SavePendingRates: atteso false")
	}
	if b.LoadPendingRates("1") != nil {
		t.Fatal("LoadPendingRates: atteso nil")
	}
	if b.ClearPendingRates("1") {
		t.Fatal("ClearPendingRates: atteso false")
	}
	assertLogContains(t, logs, "❌ Errore salvataggio pending_rates per 1:", "❌ Errore caricamento pending_rates per 1:",
		"❌ Errore rimozione pending_rates per 1:")
}

func TestPendingRatesJSONMethods(t *testing.T) {
	p := PendingRates{Luce: Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.1, Commercializzazione: 72, Scadenza: "2030-01-01"}, UpdatedServices: []string{}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"luce":{"tipo":"fissa","fascia":"monoraria","energia":0.1,"commercializzazione":72.0,"scadenza":"2030-01-01"},"updated_services":[]}` {
		t.Fatalf("Marshal = %s", b)
	}
	var back PendingRates
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Luce != p.Luce || back.UpdatedServices == nil {
		t.Fatalf("Unmarshal = %+v", back)
	}
	if err := json.Unmarshal([]byte(`[1]`), &back); err == nil {
		t.Fatal("array: atteso errore")
	}
	if err := back.UnmarshalJSON([]byte(`{`)); err == nil {
		t.Fatal("JSON non valido: atteso errore")
	}
}

func TestLastNotifiedRatesJSONMethods(t *testing.T) {
	var l LastNotifiedRates
	if err := json.Unmarshal([]byte(`{"gas": {"commercializzazione": 84, "energia": 0.39}, "luce": {"energia": 0.1, "commercializzazione": 72.0}}`), &l); err != nil {
		t.Fatal(err)
	}
	b, err := l.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"luce": {"energia": 0.1, "commercializzazione": 72.0}, "gas": {"energia": 0.39, "commercializzazione": 84}}` {
		t.Fatalf("MarshalJSON = %s", b)
	}
	if err := l.UnmarshalJSON([]byte("null")); err != nil || l != nil {
		t.Fatalf("null: %v %v", l, err)
	}
	if err := l.UnmarshalJSON([]byte("[1]")); err == nil {
		t.Fatal("array: atteso errore")
	}
	if err := l.UnmarshalJSON([]byte("{")); err == nil {
		t.Fatal("JSON non valido: atteso errore")
	}
	// Uguaglianza Python: nil == {}, chiave mancante != None
	if !LastNotifiedRates(nil).Equal(LastNotifiedRates{}) {
		t.Fatal("nil deve essere uguale a {}")
	}
	a := LastNotifiedRates{"luce": map[string]any{"energia": 0.1}}
	if a.Equal(LastNotifiedRates{"luce": map[string]any{"energia": 0.1, "commercializzazione": nil}}) {
		t.Fatal("chiave mancante != None")
	}
}

func TestLoadUserMalformedLastNotified(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	s.SaveUser("1", luceOnly("fissa", "monoraria", 0.15, 70))
	for _, raw := range []string{"{rotto", "[1]"} {
		if _, err := s.db.Exec("UPDATE users SET last_notified_rates = ? WHERE user_id = '1'", raw); err != nil {
			t.Fatal(err)
		}
		if u := s.LoadUser("1"); u != nil {
			t.Fatalf("%s: atteso nil", raw)
		}
		if users := s.LoadUsers(); len(users) != 0 {
			t.Fatalf("%s: atteso [], got %d utenti", raw, len(users))
		}
	}
	assertLogContains(t, logs, "❌ Errore caricamento utente 1:", "❌ Errore caricamento utenti:")
}

func TestSaveUserUnserializableLastNotified(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	u := luceOnly("fissa", "monoraria", 0.15, 70)
	u.LastNotifiedRates = LastNotifiedRates{"x": make(chan int)}
	if s.SaveUser("1", u) {
		t.Fatal("atteso false")
	}
	if s.UserExists("1") {
		t.Fatal("utente non deve essere salvato")
	}
	assertLogContains(t, logs, "❌ Validazione fallita per 1: Object of type chan int is not JSON serializable")
}

func TestSaveUserEmptyLastNotifiedIsNull(t *testing.T) {
	s := newTestStore(t)
	u := luceOnly("fissa", "monoraria", 0.15, 70)
	u.LastNotifiedRates = LastNotifiedRates{"luce": NotifiedRate(0.1, 72)}
	s.SaveUser("1", u)
	// {} è falsy in Python: last_notified_rates torna NULL
	u.LastNotifiedRates = LastNotifiedRates{}
	s.SaveUser("1", u)
	if rawColumn(t, s, "1", "last_notified_rates") != nil {
		t.Fatal("last_notified_rates {} deve essere salvato come NULL")
	}
	if s.LoadUser("1").LastNotifiedRates != nil {
		t.Fatal("chiave last_notified_rates deve essere assente")
	}
}

// TestRowToUserLegacyRow: riga senza le colonne di migrazione (_row_has_column).
func TestRowToUserLegacyRow(t *testing.T) {
	m := rowMap{
		"user_id": "x", "luce_tipo": "fissa", "luce_fascia": "monoraria", "luce_energia": int64(1),
		"luce_commercializzazione": "70.5", "luce_consumo_f1": nil, "luce_consumo_f2": []byte("2"),
		"luce_consumo_f3": nil, "gas_tipo": []byte("fissa"), "gas_fascia": []byte("monoraria"),
		"gas_energia": 0.4, "gas_commercializzazione": true, "gas_consumo_annuo": nil, "last_notified_rates": nil,
	}
	u, err := rowToUser(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"luce":{"tipo":"fissa","fascia":"monoraria","energia":1,"commercializzazione":70.5,"consumo_f2":2},` +
		`"gas":{"tipo":"fissa","fascia":"monoraria","energia":0.4,"commercializzazione":1}}`
	if got := jsonString(t, u); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestValueHelpers(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	strs := []struct {
		v    any
		want string
	}{{nil, ""}, {"a", "a"}, {[]byte("b"), "b"}, {int64(7), "7"}, {72.0, "72.0"}, {ts, "2026-01-02 03:04:05"}, {true, "true"}}
	for _, c := range strs {
		if got := valueString(c.v); got != c.want {
			t.Errorf("valueString(%#v) = %q", c.v, got)
		}
	}
	floats := []struct {
		v  any
		f  float64
		ok bool
	}{{1.5, 1.5, true}, {int64(2), 2, true}, {true, 1, true}, {false, 0, true}, {" 3.5 ", 3.5, true}, {"x", 0, false}, {[]byte("4"), 4, true}, {nil, 0, false}}
	for _, c := range floats {
		if f, ok := valueFloat(c.v); f != c.f || ok != c.ok {
			t.Errorf("valueFloat(%#v) = %v %v", c.v, f, ok)
		}
	}
	truthy := []struct {
		v    any
		want bool
	}{{nil, false}, {"", false}, {"a", true}, {[]byte{}, false}, {[]byte("a"), true}, {int64(0), false}, {int64(2), true}, {0.0, false}, {0.5, true}, {false, false}, {true, true}, {ts, true}}
	for _, c := range truthy {
		if got := pyTruthy(c.v); got != c.want {
			t.Errorf("pyTruthy(%#v) = %v", c.v, got)
		}
	}
	if sqlParam(true) != int64(1) || sqlParam(false) != int64(0) || sqlParam("x") != "x" {
		t.Error("sqlParam")
	}
	if _, err := pendingFromValue("x"); err == nil {
		t.Error("pendingFromValue su stringa: atteso errore")
	}
}

// TestConcurrentAccess: lo Store è usabile da più goroutine (connessione
// unica serializzata, nessun deadlock con la transazione di ApplyPendingRates).
func TestConcurrentAccess(t *testing.T) {
	s := newTestStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			for j := 0; j < 10; j++ {
				if !s.SaveUser(id, luceOnly("fissa", "monoraria", 0.1+float64(j)/100, 70)) {
					t.Errorf("SaveUser %s", id)
				}
				s.SavePendingRates(id, PendingRates{Luce: pendingLuce(0.2, 60), UpdatedServices: []string{"luce"}})
				if ok, reason := s.ApplyPendingRates(id); !ok {
					t.Errorf("ApplyPendingRates %s: %s", id, reason)
				}
				s.LoadUsers()
				s.GetRateHistory("luce", "fissa", "monoraria", 30, true, true)
			}
		}(i)
	}
	wg.Wait()
	if n := s.GetUserCount(); n != 8 {
		t.Fatalf("utenti = %d", n)
	}
}
