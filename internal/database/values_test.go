package database

// Test differenziali dei valori calcolati: testdata/python_values_golden.json
// è generato con CPython 3.13 (round(x, 6), min/max/sum delle statistiche
// dello storico e json.loads + == tra blob JSON).

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"testing"
)

type pythonValuesGolden struct {
	Round6 []struct {
		Bits       string `json:"bits"`
		ResultBits string `json:"result_bits"`
	} `json:"round6"`
	Stats []struct {
		Data    []string `json:"data"`
		SumBits string   `json:"sum_bits"`
		MinBits string   `json:"min_bits"`
		MaxBits string   `json:"max_bits"`
		AvgBits string   `json:"avg_bits"`
	} `json:"stats"`
	LoadsEq []struct {
		A     string `json:"a"`
		B     string `json:"b"`
		Equal bool   `json:"equal"`
	} `json:"loads_eq"`
}

func loadPythonValuesGolden(t *testing.T) pythonValuesGolden {
	t.Helper()
	b, err := os.ReadFile("testdata/python_values_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g pythonValuesGolden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func fromBits(t *testing.T, h string) float64 {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 8 {
		t.Fatalf("bits non validi %q", h)
	}
	return math.Float64frombits(binary.BigEndian.Uint64(b))
}

func sameFloat(a, b float64) bool {
	return math.Float64bits(a) == math.Float64bits(b) || (math.IsNaN(a) && math.IsNaN(b))
}

func TestGoldenRound6(t *testing.T) {
	for _, c := range loadPythonValuesGolden(t).Round6 {
		x := fromBits(t, c.Bits)
		if got, want := round6(x), fromBits(t, c.ResultBits); !sameFloat(got, want) {
			t.Errorf("round(%v, 6): got %v, want %v", x, got, want)
		}
	}
}

func TestGoldenStats(t *testing.T) {
	for i, c := range loadPythonValuesGolden(t).Stats {
		data := make([]float64, len(c.Data))
		for j, h := range c.Data {
			data[j] = fromBits(t, h)
		}
		st := computeStats(data)
		checks := []struct {
			name      string
			got, want float64
		}{
			{"sum", compensatedSum(data), fromBits(t, c.SumBits)},
			{"min", *st.Min, fromBits(t, c.MinBits)},
			{"max", *st.Max, fromBits(t, c.MaxBits)},
			{"avg", *st.Avg, fromBits(t, c.AvgBits)},
		}
		for _, ck := range checks {
			if !sameFloat(ck.got, ck.want) {
				t.Errorf("caso %d: %s = %v, want %v", i, ck.name, ck.got, ck.want)
			}
		}
	}
}

func TestCompensatedSum(t *testing.T) {
	if compensatedSum(nil) != 0 {
		t.Error("sum([]) deve essere 0")
	}
	if got := compensatedSum([]float64{1e100, 1, -1e100}); got != 1 {
		t.Errorf("Neumaier: got %v, want 1", got)
	}
	// La compensazione non deve trasformare un overflow in NaN
	if got := compensatedSum([]float64{math.MaxFloat64, math.MaxFloat64, -1}); !math.IsInf(got, 1) {
		t.Errorf("sum overflow = %v", got)
	}
}

// TestGoldenJSONEquality: l'uguaglianza tra blob decodificati coincide con
// json.loads(a) == json.loads(b) di Python.
func TestGoldenJSONEquality(t *testing.T) {
	for _, c := range loadPythonValuesGolden(t).LoadsEq {
		var a, b any
		if err := json.Unmarshal([]byte(c.A), &a); err != nil {
			t.Fatalf("%s: %v", c.A, err)
		}
		if err := json.Unmarshal([]byte(c.B), &b); err != nil {
			t.Fatalf("%s: %v", c.B, err)
		}
		if got := jsonEqual(a, b); got != c.Equal {
			t.Errorf("%s == %s: got %v, want %v", c.A, c.B, got, c.Equal)
		}
		if got := jsonEqual(b, a); got != c.Equal {
			t.Errorf("%s == %s (simmetrico): got %v, want %v", c.B, c.A, got, c.Equal)
		}
	}
}

func TestLastNotifiedRatesEqual(t *testing.T) {
	if !LastNotifiedRates(nil).Equal(LastNotifiedRates{}) {
		t.Fatal("nil deve essere uguale a {}")
	}
	a := LastNotifiedRates{"luce": map[string]any{"energia": 0.1}}
	if a.Equal(LastNotifiedRates{"luce": map[string]any{"energia": 0.1, "commercializzazione": nil}}) {
		t.Fatal("chiave mancante != null")
	}
	if !a.Equal(LastNotifiedRates{"luce": map[string]any{"energia": 0.1}}) {
		t.Fatal("mappe uguali")
	}
	if a.Equal(LastNotifiedRates{"luce": []any{0.1}}) || jsonEqual(1, 1) {
		t.Fatal("tipi diversi o non JSON non sono uguali")
	}
}
