package database

// Test differenziale di ApplyPendingRates su blob pending_rates anomali:
// testdata/apply_edge_golden.json contiene, per ogni blob, l'esito di
// apply_pending_rates in Python (o l'eccezione non gestita sollevata) e la
// riga users risultante.

import (
	"encoding/json"
	"os"
	"testing"
)

type applyEdgeCase struct {
	Raw       string `json:"raw"`
	Result    []any  `json:"result"`
	Exception string `json:"exception"`
	Row       []any  `json:"row"`
	RowTypes  []any  `json:"row_types"`
}

const applyEdgeRowSQL = `SELECT luce_tipo, luce_fascia, luce_energia, luce_commercializzazione, gas_tipo, gas_fascia,
	gas_energia, gas_commercializzazione, luce_consumo_f1, luce_consumo_f2, luce_consumo_f3,
	gas_consumo_annuo, luce_scadenza, gas_scadenza, luce_scadenza_reminded, gas_scadenza_reminded,
	last_notified_rates, pending_rates FROM users WHERE user_id = 'u'`

// setupApplyEdgeUser crea lo stesso utente del generatore Python.
func setupApplyEdgeUser(t *testing.T, s *Store) {
	t.Helper()
	u := User{
		Luce: Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.15, Commercializzazione: 70.0,
			ConsumoF1: fp(2000.0), Scadenza: "2026-12-01"},
		Gas: &Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.45, Commercializzazione: 84.0,
			ConsumoAnnuo: fp(1000.0), Scadenza: "2027-01-01"},
		LastNotifiedRates: LastNotifiedRates{"luce": NotifiedRate(0.1, 60.0)},
	}
	if !s.SaveUser("u", u) || !s.MarkScadenzaReminded("u", "luce", "2026-12-01") || !s.MarkScadenzaReminded("u", "gas", "2027-01-01") {
		t.Fatal("setup utente fallito")
	}
}

// goResultForPythonException: dove Python solleva un'eccezione non gestita
// (che in Go non può propagarsi), Go restituisce un codice di errore e
// lascia la riga invariata (rollback), come la chiusura della connessione
// Python senza commit.
var goResultForPythonException = map[string]string{
	"TypeError":     ApplyValidationError,
	"OverflowError": ApplyDBError,
}

func TestApplyPendingRatesEdgeCasesGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/apply_edge_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []applyEdgeCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			s := newTestStore(t)
			setupApplyEdgeUser(t, s)
			if _, err := s.db.Exec("UPDATE users SET pending_rates = ? WHERE user_id = 'u'", c.Raw); err != nil {
				t.Fatal(err)
			}
			ok, reason := s.ApplyPendingRates("u")

			wantOK, wantReason := false, goResultForPythonException[c.Exception]
			if c.Exception == "" {
				wantOK, wantReason = c.Result[0].(bool), c.Result[1].(string)
			}
			if ok != wantOK || reason != wantReason {
				t.Fatalf("pending %q: got (%v, %s), want (%v, %s) [python: %v %s]", c.Raw, ok, reason, wantOK, wantReason, c.Result, c.Exception)
			}

			maps, err := queryMaps(t.Context(), s.db, applyEdgeRowSQL)
			if err != nil || len(maps) != 1 {
				t.Fatalf("lettura riga: %v", err)
			}
			cols := []string{"luce_tipo", "luce_fascia", "luce_energia", "luce_commercializzazione", "gas_tipo", "gas_fascia",
				"gas_energia", "gas_commercializzazione", "luce_consumo_f1", "luce_consumo_f2", "luce_consumo_f3",
				"gas_consumo_annuo", "luce_scadenza", "gas_scadenza", "luce_scadenza_reminded", "gas_scadenza_reminded",
				"last_notified_rates", "pending_rates"}
			row := make([]any, len(cols))
			for i, col := range cols {
				row[i] = maps[0][col]
			}
			assertSameJSON(t, "riga dopo apply di "+c.Raw, row, c.Row)

			var types []any
			var t1, t2 string
			if err := s.db.QueryRow("SELECT typeof(luce_energia), typeof(luce_commercializzazione) FROM users WHERE user_id = 'u'").Scan(&t1, &t2); err != nil {
				t.Fatal(err)
			}
			types = append(types, t1, t2)
			assertSameJSON(t, "typeof dopo apply di "+c.Raw, types, c.RowTypes)
		})
	}
}

func TestApplyPendingRatesLogs(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	setupApplyEdgeUser(t, s)
	if _, err := s.db.Exec(`UPDATE users SET pending_rates = '{"luce": {"tipo": "mista", "fascia": "monoraria"}}' WHERE user_id = 'u'`); err != nil {
		t.Fatal(err)
	}
	s.ApplyPendingRates("u")
	assertLogContains(t, logs, "- database - ERROR - ❌ Validazione fallita per u: luce.tipo non valido: 'mista'. Valori ammessi: {'fissa', 'variabile'}")

	if _, err := s.db.Exec(`UPDATE users SET pending_rates = '{"luce": {"tipo": "fissa", "fascia": "monoraria", "energia": null, "commercializzazione": 1}}' WHERE user_id = 'u'`); err != nil {
		t.Fatal(err)
	}
	if ok, reason := s.ApplyPendingRates("u"); ok || reason != ApplyDBError {
		t.Fatalf("atteso db_error, got %v %s", ok, reason)
	}
	assertLogContains(t, logs, "- database - ERROR - ❌ Errore database apply_pending_rates per u:", "NOT NULL")
}

func TestApplyPendingRatesNoPendingAndErrors(t *testing.T) {
	s := newTestStore(t)
	// Utente inesistente → no_pending (la prima SELECT non trova righe)
	if ok, reason := s.ApplyPendingRates("ghost"); ok || reason != ApplyNoPending {
		t.Fatalf("got %v %s", ok, reason)
	}
	setupApplyEdgeUser(t, s)
	if ok, reason := s.ApplyPendingRates("u"); ok || reason != ApplyNoPending {
		t.Fatalf("pending NULL: got %v %s", ok, reason)
	}
	// La connessione non resta in transazione dopo no_pending
	if !s.SaveUser("v", luceOnly("fissa", "monoraria", 0.1, 1)) {
		t.Fatal("store non riutilizzabile dopo no_pending")
	}

	logs := captureLogs(t)
	if ok, reason := brokenStore(t).ApplyPendingRates("u"); ok || reason != ApplyDBError {
		t.Fatalf("db chiuso: got %v %s", ok, reason)
	}
	assertLogContains(t, logs, "❌ Errore database apply_pending_rates per u:")
}

// TestApplyPendingRatesBusy: con un'altra connessione che detiene il lock
// di scrittura, BEGIN IMMEDIATE fallisce (dopo busy_timeout) → db_error.
func TestApplyPendingRatesBusy(t *testing.T) {
	s := newTestStore(t)
	setupApplyEdgeUser(t, s)
	s.SavePendingRates("u", PendingRates{Luce: pendingLuce(0.1, 1)})
	if _, err := s.db.Exec("PRAGMA busy_timeout = 50"); err != nil {
		t.Fatal(err)
	}
	other, err := Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx := t.Context()
	conn, err := other.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if ok, reason := s.ApplyPendingRates("u"); ok || reason != ApplyDBError {
		t.Fatalf("lock occupato: got %v %s", ok, reason)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if ok, reason := s.ApplyPendingRates("u"); !ok || reason != ApplyOK {
		t.Fatalf("dopo rilascio lock: got %v %s", ok, reason)
	}
}
