package database

import (
	"fmt"
	"sync"
	"testing"
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
	// Stesso contenuto di json.dumps(_build_pending_rates(...))
	want := `{"luce": {"tipo": "fissa", "fascia": "monoraria", "consumo_f1": 2700.0, "energia": 0.13, "commercializzazione": 65.0}, ` +
		`"gas": {"tipo": "variabile", "fascia": "monoraria", "consumo_annuo": 1000.0, "energia": 0.05, "commercializzazione": 84.0}, ` +
		`"updated_services": ["luce"]}`
	assertSameBlob(t, "blob", rawColumn(t, s, "1", "pending_rates"), want)
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
	// Valori mancanti → zero (load_pending_rates restituisce il dict così com'è)
	if _, err := s.db.Exec(`UPDATE users SET pending_rates = '{"luce": {"tipo": "fissa", "energia": 1, "consumo_f1": null}, "gas": null, "updated_services": []}' WHERE user_id = '1'`); err != nil {
		t.Fatal(err)
	}
	p := s.LoadPendingRates("1")
	if p.Luce.Tipo != "fissa" || p.Luce.Fascia != "" || p.Luce.Energia != 1 || p.Luce.ConsumoF1 != nil || p.Gas != nil || p.UpdatedServices == nil || len(p.UpdatedServices) != 0 {
		t.Fatalf("decodifica: %+v", p)
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

func TestPendingRatesJSON(t *testing.T) {
	p := PendingRates{Luce: Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.1, Commercializzazione: 72}, UpdatedServices: []string{}}
	if got := jsonString(t, p); got != `{"luce":{"tipo":"fissa","fascia":"monoraria","energia":0.1,"commercializzazione":72},"updated_services":[]}` {
		t.Fatalf("Marshal = %s", got)
	}
	// updated_services nil (righe precedenti) → chiave assente
	if got := jsonString(t, PendingRates{Luce: p.Luce}); got != `{"luce":{"tipo":"fissa","fascia":"monoraria","energia":0.1,"commercializzazione":72}}` {
		t.Fatalf("Marshal senza updated_services = %s", got)
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
		if s.LoadUser("1") != nil {
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
	assertLogContains(t, logs, "❌ Validazione fallita per 1: json: unsupported type: chan int")
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
