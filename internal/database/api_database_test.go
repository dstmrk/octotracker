package database

// Porting di tests/test_api_database.py: GetRateHistory per l'API Mini App.

import (
	"testing"
	"time"
)

// populatedStore genera dati per gli ultimi 30 giorni (fixture populated_db).
// Python usa datetime.now() (ora locale) mentre la query usa date('now')
// (UTC): con TZ a est di UTC, tra la mezzanotte locale e quella UTC il test
// Python con days=7 trova 9 date e fallisce. Qui le date sono generate in
// UTC per rendere il test deterministico.
func populatedStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	base := time.Now().UTC()
	for i := 0; i < 30; i++ {
		date := base.AddDate(0, 0, -i).Format("2006-01-02")
		fi := float64(i)
		rates := []RateRecord{
			// Luce fissa monoraria
			{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.115 + (fi * 0.001), Commercializzazione: fp(72.0)},
			// Luce variabile monoraria
			{Servizio: "luce", Tipo: "variabile", Fascia: "monoraria", Energia: 0.008 + (fi * 0.0005), Commercializzazione: fp(72.0)},
			// Luce fissa bioraria
			{Servizio: "luce", Tipo: "fissa", Fascia: "bioraria", Energia: 0.110 + (fi * 0.001), Commercializzazione: fp(72.0)},
			// Gas fissa
			{Servizio: "gas", Tipo: "fissa", Fascia: "monoraria", Energia: 0.39 + (fi * 0.005), Commercializzazione: fp(84.0)},
			// Gas variabile
			{Servizio: "gas", Tipo: "variabile", Fascia: "monoraria", Energia: 0.05 + (fi * 0.002), Commercializzazione: fp(84.0)},
		}
		if n := s.SaveRatesBatch(date, rates); n != 5 {
			t.Fatalf("batch %s: %d", date, n)
		}
	}
	return s
}

func assertEmptyHistory(t *testing.T, r RateHistory) {
	t.Helper()
	if r.Labels == nil || r.Data == nil || len(r.Labels) != 0 || len(r.Data) != 0 {
		t.Fatalf("atteso risultato vuoto, got %+v", r)
	}
}

// ========== TestGetRateHistory ==========

func TestGetHistoryBasic(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, false, false)
	if len(r.Labels) != 30 || len(r.Data) != 30 {
		t.Fatalf("attesi 30 punti, got %d/%d", len(r.Labels), len(r.Data))
	}
	// Chiavi del dict Python senza opzioni: labels, data, period
	g := toGeneric(t, r).(map[string]any)
	if len(g) != 3 || g["labels"] == nil || g["data"] == nil || g["period"] == nil {
		t.Fatalf("chiavi JSON errate: %v", g)
	}
}

func TestGetHistoryReturnsSortedByDate(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, false, false)
	for i := 0; i < len(r.Labels)-1; i++ {
		if r.Labels[i] >= r.Labels[i+1] {
			t.Fatalf("date non crescenti: %s >= %s", r.Labels[i], r.Labels[i+1])
		}
	}
}

func TestGetHistoryEnergiaValues(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, false, false)
	for _, v := range r.Data {
		if v <= 0 {
			t.Fatalf("valore non positivo: %v", v)
		}
	}
}

func TestGetHistoryIncludesCommercializzazione(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, true, false)
	if len(r.Commercializzazione) != 30 {
		t.Fatalf("attese 30 commercializzazioni, got %d", len(r.Commercializzazione))
	}
	for _, c := range r.Commercializzazione {
		if c == nil || *c != 72.0 {
			t.Fatalf("commercializzazione errata: %v", deref(c))
		}
	}
}

func TestGetHistoryFilterByDays7(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 7, false, false)
	// SQLite date('now', '-7 days') include oggi, quindi possono essere 7 o 8 giorni
	if n := len(r.Labels); n < 7 || n > 8 {
		t.Fatalf("attesi 7-8 giorni, got %d", n)
	}
	if len(r.Labels) != len(r.Data) {
		t.Fatal("labels e data di lunghezza diversa")
	}
}

func TestGetHistoryFilterByDays365(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 365, false, false)
	if len(r.Labels) != 30 {
		t.Fatalf("attesi tutti i 30 giorni, got %d", len(r.Labels))
	}
}

func TestGetHistoryFilterLuceVariabile(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "variabile", "monoraria", 30, false, false)
	if len(r.Data) != 30 {
		t.Fatalf("attesi 30, got %d", len(r.Data))
	}
	for _, v := range r.Data {
		if v >= 0.05 {
			t.Fatalf("spread variabile troppo alto: %v", v)
		}
	}
}

func TestGetHistoryFilterGas(t *testing.T) {
	r := populatedStore(t).GetRateHistory("gas", "fissa", "monoraria", 30, false, false)
	if len(r.Data) != 30 {
		t.Fatalf("attesi 30, got %d", len(r.Data))
	}
	for _, v := range r.Data {
		if v <= 0.3 {
			t.Fatalf("prezzo gas troppo basso: %v", v)
		}
	}
}

func TestGetHistoryFilterBioraria(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "bioraria", 30, false, false)
	if len(r.Data) != 30 {
		t.Fatalf("attesi 30, got %d", len(r.Data))
	}
}

func TestGetHistoryEmptyResult(t *testing.T) {
	r := newTestStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, false, false)
	assertEmptyHistory(t, r)
	if got := jsonString(t, r); got != `{"labels":[],"data":[],"period":{"from":null,"to":null}}` {
		t.Fatalf("JSON risultato vuoto = %s", got)
	}
	full := newTestStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, true, true)
	want := `{"labels":[],"data":[],"period":{"from":null,"to":null},"commercializzazione":[],"stats":{"min":null,"max":null,"avg":null}}`
	if got := jsonString(t, full); got != want {
		t.Fatalf("JSON risultato vuoto con opzioni:\n got  %s\n want %s", got, want)
	}
}

func TestGetHistoryNoMatchingData(t *testing.T) {
	// Fascia trioraria non esiste nei dati di test
	assertEmptyHistory(t, populatedStore(t).GetRateHistory("luce", "fissa", "trioraria", 30, false, false))
}

func TestGetHistoryInvalidServizio(t *testing.T) {
	assertEmptyHistory(t, populatedStore(t).GetRateHistory("acqua", "fissa", "monoraria", 30, false, false))
}

func TestGetHistoryDbError(t *testing.T) {
	logs := captureLogs(t)
	r := brokenStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, true, true)
	assertEmptyHistory(t, r)
	if r.Commercializzazione == nil || r.Stats == nil {
		t.Fatal("il risultato vuoto deve mantenere le chiavi richieste")
	}
	assertLogContains(t, logs, "❌ Errore lettura storico tariffe:")
}

func TestGetHistoryMetadata(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, false, false)
	if r.Period.From == nil || r.Period.To == nil {
		t.Fatalf("period incompleto: %+v", r.Period)
	}
	if *r.Period.From != r.Labels[0] || *r.Period.To != r.Labels[len(r.Labels)-1] {
		t.Fatal("period non coerente con labels")
	}
}

func TestGetHistoryStatistics(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 30, false, true)
	if r.Stats == nil || r.Stats.Min == nil || r.Stats.Max == nil || r.Stats.Avg == nil {
		t.Fatalf("stats mancanti: %+v", r.Stats)
	}
	if !(*r.Stats.Min <= *r.Stats.Avg && *r.Stats.Avg <= *r.Stats.Max) {
		t.Fatalf("stats incoerenti: %v %v %v", *r.Stats.Min, *r.Stats.Avg, *r.Stats.Max)
	}
	g := toGeneric(t, r).(map[string]any)
	if _, ok := g["commercializzazione"]; ok {
		t.Fatal("commercializzazione non richiesta ma presente")
	}
}

// ========== TestGetRateHistoryEdgeCases ==========

func TestDaysZero(t *testing.T) {
	assertEmptyHistory(t, populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 0, false, false))
}

func TestDaysNegative(t *testing.T) {
	assertEmptyHistory(t, populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", -5, false, false))
}

func TestVeryLargeDays(t *testing.T) {
	r := populatedStore(t).GetRateHistory("luce", "fissa", "monoraria", 10000, false, false)
	if len(r.Labels) > 30 || len(r.Labels) == 0 {
		t.Fatalf("attesi al più 30 giorni, got %d", len(r.Labels))
	}
}
