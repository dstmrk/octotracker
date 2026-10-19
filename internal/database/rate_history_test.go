package database

// Porting di tests/test_database_rate_history.py: funzioni storico tariffe.

import (
	"reflect"
	"testing"
)

func rec(servizio, tipo, fascia string, energia float64) RateRecord {
	return RateRecord{Servizio: servizio, Tipo: tipo, Fascia: fascia, Energia: energia}
}

func countRates(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM rate_history").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ========== TestSaveRate ==========

func TestSaveRate_Success(t *testing.T) {
	s := newTestStore(t)
	if !s.SaveRate("2025-01-15", "luce", "fissa", "monoraria", 0.1078, fp(72.0), sp("COD123")) {
		t.Fatal("atteso true")
	}
	var comm float64
	var cod string
	if err := s.db.QueryRow("SELECT commercializzazione, cod_offerta FROM rate_history").Scan(&comm, &cod); err != nil {
		t.Fatal(err)
	}
	if comm != 72.0 || cod != "COD123" {
		t.Fatalf("valori salvati errati: %v %v", comm, cod)
	}
}

func TestSaveRate_Minimal(t *testing.T) {
	s := newTestStore(t)
	if !s.SaveRate("2025-01-15", "gas", "variabile", "monoraria", 0.08, nil, nil) {
		t.Fatal("atteso true")
	}
	var comm, cod any
	if err := s.db.QueryRow("SELECT commercializzazione, cod_offerta FROM rate_history").Scan(&comm, &cod); err != nil {
		t.Fatal(err)
	}
	if comm != nil || cod != nil {
		t.Fatalf("attesi NULL, got %v %v", comm, cod)
	}
}

func TestSaveRate_DuplicateIgnored(t *testing.T) {
	s := newTestStore(t)
	s.SaveRate("2025-01-15", "luce", "fissa", "monoraria", 0.1078, nil, nil)
	// Duplicato - stessa chiave, deve essere ignorato senza errore
	if !s.SaveRate("2025-01-15", "luce", "fissa", "monoraria", 0.1078, nil, nil) {
		t.Fatal("atteso true anche per duplicato")
	}
	if n := countRates(t, s); n != 1 {
		t.Fatalf("attesa 1 riga, got %d", n)
	}
}

func TestSaveRate_DbError(t *testing.T) {
	logs := captureLogs(t)
	if brokenStore(t).SaveRate("2025-01-15", "luce", "fissa", "monoraria", 0.1078, nil, nil) {
		t.Fatal("atteso false")
	}
	assertLogContains(t, logs, "❌ Errore salvataggio tariffa storico:")
}

// ========== TestSaveRatesBatch ==========

func TestSaveRatesBatch_Success(t *testing.T) {
	s := newTestStore(t)
	rates := []RateRecord{
		{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.1078, Commercializzazione: fp(72.0)},
		{Servizio: "luce", Tipo: "variabile", Fascia: "monoraria", Energia: 0.0088, Commercializzazione: fp(72.0)},
		{Servizio: "gas", Tipo: "fissa", Fascia: "monoraria", Energia: 0.39, Commercializzazione: fp(84.0)},
	}
	if n := s.SaveRatesBatch("2025-01-15", rates); n != 3 {
		t.Fatalf("attese 3 inserite, got %d", n)
	}
}

func TestSaveRatesBatch_Empty(t *testing.T) {
	s := newTestStore(t)
	if n := s.SaveRatesBatch("2025-01-15", nil); n != 0 {
		t.Fatalf("atteso 0, got %d", n)
	}
}

func TestSaveRatesBatch_DuplicatesSkipped(t *testing.T) {
	s := newTestStore(t)
	rates := []RateRecord{rec("luce", "fissa", "monoraria", 0.1078)}
	if n := s.SaveRatesBatch("2025-01-15", rates); n != 1 {
		t.Fatalf("prima inserzione: atteso 1, got %d", n)
	}
	if n := s.SaveRatesBatch("2025-01-15", rates); n != 0 {
		t.Fatalf("duplicato: atteso 0, got %d", n)
	}
}

func TestSaveRatesBatch_PartialDuplicates(t *testing.T) {
	s := newTestStore(t)
	s.SaveRatesBatch("2025-01-15", []RateRecord{rec("luce", "fissa", "monoraria", 0.1078)})
	n := s.SaveRatesBatch("2025-01-15", []RateRecord{
		rec("luce", "fissa", "monoraria", 0.1078),
		rec("gas", "fissa", "monoraria", 0.39),
	})
	if n != 1 {
		t.Fatalf("atteso 1 (solo gas), got %d", n)
	}
}

func TestSaveRatesBatch_DbError(t *testing.T) {
	logs := captureLogs(t)
	n := brokenStore(t).SaveRatesBatch("2025-01-15", []RateRecord{rec("luce", "fissa", "monoraria", 0.10)})
	if n != -1 {
		t.Fatalf("atteso -1, got %d", n)
	}
	assertLogContains(t, logs, "❌ Errore salvataggio batch tariffe:")
}

// TestSaveRatesBatch_RollbackOnError: la seconda INSERT fallisce (trigger
// che solleva un errore SQLite), il batch restituisce -1 e la prima riga
// non resta nel DB (rollback eseguito, connessione riutilizzabile).
func TestSaveRatesBatch_RollbackOnError(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_gas BEFORE INSERT ON rate_history
		WHEN NEW.servizio = 'gas' BEGIN SELECT RAISE(ABORT, 'Insert failed'); END`); err != nil {
		t.Fatal(err)
	}
	n := s.SaveRatesBatch("2025-01-15", []RateRecord{
		rec("luce", "fissa", "monoraria", 0.10),
		rec("gas", "fissa", "monoraria", 0.39),
	})
	if n != -1 {
		t.Fatalf("atteso -1, got %d", n)
	}
	if c := countRates(t, s); c != 0 {
		t.Fatalf("rollback non eseguito: %d righe", c)
	}
	assertLogContains(t, logs, "Insert failed", "- database - DEBUG - Rollback transazione completato")

	// Lo store resta utilizzabile (nessuna transazione rimasta aperta)
	if n := s.SaveRatesBatch("2025-01-16", []RateRecord{rec("luce", "fissa", "monoraria", 0.10)}); n != 1 {
		t.Fatalf("batch successivo: atteso 1, got %d", n)
	}
}

// ========== TestGetCurrentRates ==========

func TestGetCurrentRates_Success(t *testing.T) {
	s := newTestStore(t)
	s.SaveRatesBatch("2025-01-15", []RateRecord{
		{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.1078, Commercializzazione: fp(72.0), CodOfferta: sp("COD123")},
		{Servizio: "luce", Tipo: "variabile", Fascia: "monoraria", Energia: 0.0088, Commercializzazione: fp(72.0)},
		{Servizio: "gas", Tipo: "fissa", Fascia: "monoraria", Energia: 0.39, Commercializzazione: fp(84.0)},
	})
	result := s.GetCurrentRates()
	if result == nil {
		t.Fatal("atteso risultato non nil")
	}
	if _, ok := result["luce"]; !ok {
		t.Fatal("manca luce")
	}
	if _, ok := result["gas"]; !ok {
		t.Fatal("manca gas")
	}
	lf := result["luce"]["fissa"]["monoraria"]
	if lf.Energia != 0.1078 || *lf.Commercializzazione != 72.0 || *lf.CodOfferta != "COD123" {
		t.Fatalf("luce fissa errata: %+v", lf)
	}
	if result["luce"]["variabile"]["monoraria"].Energia != 0.0088 {
		t.Fatal("luce variabile errata")
	}
	if result["gas"]["fissa"]["monoraria"].Energia != 0.39 {
		t.Fatal("gas fissa errata")
	}
	// Struttura completa come il dict Python (gas variabile vuoto ma presente)
	want := `{"gas":{"fissa":{"monoraria":{"energia":0.39,"commercializzazione":84}},"variabile":{}},` +
		`"luce":{"fissa":{"monoraria":{"energia":0.1078,"commercializzazione":72,"cod_offerta":"COD123"}},` +
		`"variabile":{"monoraria":{"energia":0.0088,"commercializzazione":72}}}}`
	if got := jsonString(t, result); got != want {
		t.Fatalf("JSON:\n got  %s\n want %s", got, want)
	}
	if r, ok := result.Get("luce", "fissa", "monoraria"); !ok || r.Energia != 0.1078 {
		t.Fatal("Get luce/fissa/monoraria")
	}
	if _, ok := result.Get("acqua", "fissa", "monoraria"); ok {
		t.Fatal("Get su servizio inesistente deve fallire")
	}
}

func TestGetCurrentRates_EmptyDb(t *testing.T) {
	if r := newTestStore(t).GetCurrentRates(); r != nil {
		t.Fatalf("atteso nil, got %v", r)
	}
}

func TestGetCurrentRates_ReturnsLatest(t *testing.T) {
	s := newTestStore(t)
	s.SaveRatesBatch("2025-01-10", []RateRecord{rec("luce", "fissa", "monoraria", 0.10)})
	s.SaveRatesBatch("2025-01-15", []RateRecord{rec("luce", "fissa", "monoraria", 0.12)})
	if e := s.GetCurrentRates()["luce"]["fissa"]["monoraria"].Energia; e != 0.12 {
		t.Fatalf("attesa 0.12 (più recente), got %v", e)
	}
}

func TestGetCurrentRates_WithoutOptionalFields(t *testing.T) {
	s := newTestStore(t)
	s.SaveRatesBatch("2025-01-15", []RateRecord{rec("luce", "fissa", "monoraria", 0.1078)})
	r := s.GetCurrentRates()["luce"]["fissa"]["monoraria"]
	if r.Energia != 0.1078 {
		t.Fatal("energia mancante")
	}
	if r.Commercializzazione != nil || r.CodOfferta != nil {
		t.Fatalf("campi opzionali devono essere assenti: %+v", r)
	}
	if got := jsonString(t, r); got != `{"energia":0.1078}` {
		t.Fatalf("JSON = %s", got)
	}
}

func TestGetCurrentRates_DbError(t *testing.T) {
	logs := captureLogs(t)
	if brokenStore(t).GetCurrentRates() != nil {
		t.Fatal("atteso nil")
	}
	assertLogContains(t, logs, "❌ Errore lettura tariffe correnti:")
}

// ========== TestGetLatestRateDate ==========

func TestGetLatestRateDate_Success(t *testing.T) {
	s := newTestStore(t)
	s.SaveRatesBatch("2025-01-10", []RateRecord{rec("luce", "fissa", "monoraria", 0.10)})
	s.SaveRatesBatch("2025-01-15", []RateRecord{rec("gas", "fissa", "monoraria", 0.39)})
	if d := s.GetLatestRateDate(); d == nil || *d != "2025-01-15" {
		t.Fatalf("atteso 2025-01-15, got %v", deref(d))
	}
}

func TestGetLatestRateDate_EmptyDb(t *testing.T) {
	if d := newTestStore(t).GetLatestRateDate(); d != nil {
		t.Fatalf("atteso nil, got %v", *d)
	}
}

func TestGetLatestRateDate_DbError(t *testing.T) {
	logs := captureLogs(t)
	if brokenStore(t).GetLatestRateDate() != nil {
		t.Fatal("atteso nil")
	}
	assertLogContains(t, logs, "❌ Errore lettura data tariffe:")
}

// ========== TestGetRateHistoryDates ==========

func TestGetRateHistoryDates_Success(t *testing.T) {
	s := newTestStore(t)
	s.SaveRatesBatch("2025-01-10", []RateRecord{rec("luce", "fissa", "monoraria", 0.10)})
	s.SaveRatesBatch("2025-01-15", []RateRecord{rec("luce", "fissa", "monoraria", 0.12)})
	s.SaveRatesBatch("2025-01-20", []RateRecord{rec("gas", "fissa", "monoraria", 0.39)})
	want := map[RateKey]struct{}{
		{"2025-01-10", "luce", "fissa", "monoraria"}: {},
		{"2025-01-15", "luce", "fissa", "monoraria"}: {},
		{"2025-01-20", "gas", "fissa", "monoraria"}:  {},
	}
	if got := s.GetRateHistoryDates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGetRateHistoryDates_EmptyDb(t *testing.T) {
	if got := newTestStore(t).GetRateHistoryDates(); len(got) != 0 {
		t.Fatalf("atteso insieme vuoto, got %v", got)
	}
}

func TestGetRateHistoryDates_DbError(t *testing.T) {
	logs := captureLogs(t)
	if got := brokenStore(t).GetRateHistoryDates(); got == nil || len(got) != 0 {
		t.Fatalf("atteso insieme vuoto, got %v", got)
	}
	assertLogContains(t, logs, "❌ Errore lettura date storico:")
}
