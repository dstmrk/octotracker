package database

// Test differenziali dei helper Python: i valori attesi in
// testdata/pyjson_golden.json sono generati eseguendo CPython 3.13
// (json.dumps, repr, round(x, 6), sum/min/max, json.loads + ==).

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"os"
	"testing"
)

type pyjsonGolden struct {
	FloatRepr []struct {
		Bits  string `json:"bits"`
		Dumps string `json:"dumps"`
		Repr  string `json:"repr"`
	} `json:"float_repr"`
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
	Dumps   []string `json:"dumps"`
	LoadsEq []struct {
		A     string `json:"a"`
		B     string `json:"b"`
		Equal bool   `json:"equal"`
	} `json:"loads_eq"`
	Repr []struct {
		JSON string `json:"json"`
		Repr string `json:"repr"`
	} `json:"repr"`
}

func loadPyjsonGolden(t *testing.T) pyjsonGolden {
	t.Helper()
	b, err := os.ReadFile("testdata/pyjson_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g pyjsonGolden
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

func TestGoldenFloatRepr(t *testing.T) {
	g := loadPyjsonGolden(t)
	for _, c := range g.FloatRepr {
		x := fromBits(t, c.Bits)
		if got := pyFloatRepr(x); got != c.Dumps {
			t.Errorf("json.dumps(%v): got %q, want %q", x, got, c.Dumps)
		}
		if got := pyRepr(x); got != c.Repr {
			t.Errorf("repr(%v): got %q, want %q", x, got, c.Repr)
		}
		got, err := pyDumps(x)
		if err != nil || got != c.Dumps {
			t.Errorf("pyDumps(%v) = %q, %v; want %q", x, got, err, c.Dumps)
		}
	}
}

func TestGoldenRound6(t *testing.T) {
	g := loadPyjsonGolden(t)
	for _, c := range g.Round6 {
		x := fromBits(t, c.Bits)
		want := fromBits(t, c.ResultBits)
		if got := pyRound(x, 6); !sameFloat(got, want) {
			t.Errorf("round(%v, 6): got %v, want %v", x, got, want)
		}
	}
	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, math.Copysign(0, -1)} {
		if got := pyRound(x, 6); !sameFloat(got, x) {
			t.Errorf("round(%v, 6) = %v", x, got)
		}
	}
}

func TestGoldenStats(t *testing.T) {
	g := loadPyjsonGolden(t)
	for i, c := range g.Stats {
		data := make([]float64, len(c.Data))
		for j, h := range c.Data {
			data[j] = fromBits(t, h)
		}
		if got, want := pySum(data), fromBits(t, c.SumBits); !sameFloat(got, want) {
			t.Errorf("caso %d: sum = %v, want %v", i, got, want)
		}
		st := computeStats(data)
		if want := fromBits(t, c.MinBits); !sameFloat(*st.Min, want) {
			t.Errorf("caso %d: min = %v, want %v", i, *st.Min, want)
		}
		if want := fromBits(t, c.MaxBits); !sameFloat(*st.Max, want) {
			t.Errorf("caso %d: max = %v, want %v", i, *st.Max, want)
		}
		if want := fromBits(t, c.AvgBits); !sameFloat(*st.Avg, want) {
			t.Errorf("caso %d: avg = %v, want %v", i, *st.Avg, want)
		}
	}
}

func TestPySumEdgeCases(t *testing.T) {
	if pySum(nil) != 0 {
		t.Error("sum([]) deve essere 0")
	}
	// 0 + (-0.0) = 0.0 come in Python
	if got := pySum([]float64{math.Copysign(0, -1)}); math.Signbit(got) {
		t.Error("sum([-0.0]) deve essere 0.0")
	}
	// La compensazione non deve trasformare un overflow in NaN
	if got := pySum([]float64{math.MaxFloat64, math.MaxFloat64, -1}); !math.IsInf(got, 1) {
		t.Errorf("sum overflow = %v", got)
	}
	if got := pySum([]float64{1e100, 1, -1e100}); got != 1 {
		t.Errorf("Neumaier: got %v, want 1", got)
	}
}

func TestGoldenDumpsRoundTrip(t *testing.T) {
	g := loadPyjsonGolden(t)
	for _, text := range g.Dumps {
		v, err := pyLoads(text)
		if err != nil {
			t.Fatalf("pyLoads(%s): %v", text, err)
		}
		got, err := pyDumps(v)
		if err != nil {
			t.Fatalf("pyDumps: %v", err)
		}
		if got != text {
			t.Errorf("round-trip:\n got  %s\n want %s", got, text)
		}
	}
}

func TestGoldenLoadsEquality(t *testing.T) {
	g := loadPyjsonGolden(t)
	for _, c := range g.LoadsEq {
		a, err := pyLoads(c.A)
		if err != nil {
			t.Fatalf("pyLoads(%s): %v", c.A, err)
		}
		b, err := pyLoads(c.B)
		if err != nil {
			t.Fatalf("pyLoads(%s): %v", c.B, err)
		}
		if got := pyEqual(a, b); got != c.Equal {
			t.Errorf("%s == %s: got %v, want %v", c.A, c.B, got, c.Equal)
		}
		if got := pyEqual(b, a); got != c.Equal {
			t.Errorf("%s == %s (simmetrico): got %v, want %v", c.B, c.A, got, c.Equal)
		}
	}
}

func TestGoldenRepr(t *testing.T) {
	g := loadPyjsonGolden(t)
	for _, c := range g.Repr {
		v, err := pyLoads(c.JSON)
		if err != nil {
			t.Fatal(err)
		}
		if got := pyRepr(v); got != c.Repr {
			t.Errorf("repr(%s):\n got  %s\n want %s", c.JSON, got, c.Repr)
		}
	}
}

func TestPyLoadsErrors(t *testing.T) {
	for _, s := range []string{"", "{", `{"a": 1} x`, "NaN", "[1,]"} {
		if _, err := pyLoads(s); err == nil {
			t.Errorf("pyLoads(%q) doveva fallire", s)
		}
	}
	v, err := pyLoads("  [1, 2.0, 3e2, -0, 123456789012345678901234567890]  ")
	if err != nil {
		t.Fatal(err)
	}
	list := v.([]any)
	if _, ok := list[0].(int64); !ok {
		t.Errorf("1 deve essere int: %T", list[0])
	}
	if _, ok := list[1].(float64); !ok {
		t.Errorf("2.0 deve essere float: %T", list[1])
	}
	if _, ok := list[2].(float64); !ok {
		t.Errorf("3e2 deve essere float: %T", list[2])
	}
	if _, ok := list[4].(*big.Int); !ok {
		t.Errorf("intero grande deve essere *big.Int: %T", list[4])
	}
	if pyNumber("1-") != "1-" {
		t.Error("letterale non numerico deve restare stringa")
	}
}

func TestPyDumpsGoTypes(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "null"},
		{true, "true"},
		{false, "false"},
		{7, "7"},
		{int64(-3), "-3"},
		{int32(5), "5"},
		{uint8(9), "9"},
		{float32(0.5), "0.5"},
		{json.Number("72"), "72"},
		{json.Number("72.50"), "72.5"},
		{big.NewInt(12), "12"},
		{[]string{"luce", "gas"}, `["luce", "gas"]`},
		{[]float64{1, 0.1}, "[1.0, 0.1]"},
		{map[string]float64{"commercializzazione": 72, "energia": 0.1}, `{"energia": 0.1, "commercializzazione": 72.0}`},
		{LastNotifiedRates{"gas": NotifiedRate(0.39, 84), "luce": NotifiedRate(0.1, 72)},
			`{"luce": {"energia": 0.1, "commercializzazione": 72.0}, "gas": {"energia": 0.39, "commercializzazione": 84.0}}`},
		{map[string]any{"z": 1, "b": 2, "luce": 0}, `{"luce": 0, "b": 2, "z": 1}`},
		{(*float64)(nil), "null"},
		{fp(2), "2.0"},
		{[2]int{1, 2}, "[1, 2]"},
	}
	for _, c := range cases {
		got, err := pyDumps(c.v)
		if err != nil || got != c.want {
			t.Errorf("pyDumps(%#v) = %q, %v; want %q", c.v, got, err, c.want)
		}
	}
	for _, bad := range []any{struct{}{}, map[int]int{1: 1}, []any{make(chan int)}, odict{{"a", func() {}}}, map[string]any{"x": complex(1, 2)}} {
		if _, err := pyDumps(bad); err == nil {
			t.Errorf("pyDumps(%T) doveva fallire", bad)
		}
	}
}

func TestPyEqualGoTypes(t *testing.T) {
	cases := []struct {
		a, b any
		want bool
	}{
		{nil, nil, true},
		{nil, map[string]any{}, false},
		{map[string]any(nil), map[string]any{}, true},
		{LastNotifiedRates{"luce": NotifiedRate(0.1, 72)}, map[string]any{"luce": map[string]float64{"energia": 0.1, "commercializzazione": 72}}, true},
		{odict{{"a", 1}}, map[string]any{"a": 1.0}, true},
		{[]string{"a"}, []any{"a"}, true},
		{[]any{"a"}, "a", false},
		{map[string]any{"a": 1}, []any{"a"}, false},
		{int32(1), uint(1), true},
		{float32(0.5), 0.5, true},
		{json.Number("2"), 2.0, true},
		{1, "1", false},
		{"1", 1, false},
		{struct{ A int }{1}, struct{ A int }{1}, true},
		{map[string]any{"a": 1}, map[string]any{"b": 1}, false},
		{[]any{1}, []any{1, 2}, false},
		{math.NaN(), math.NaN(), false},
		{math.Inf(1), big.NewInt(1), false},
	}
	for _, c := range cases {
		if got := pyEqual(c.a, c.b); got != c.want {
			t.Errorf("pyEqual(%#v, %#v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestPyDictHelpers(t *testing.T) {
	d := map[string]any{"a": 1.0}
	if _, err := pyGetItem(d, "b"); err == nil || err.Error() != "'b'" {
		t.Errorf("KeyError atteso, got %v", err)
	}
	var te pyTypeError
	if _, err := pyGetItem([]any{}, "a"); !errors.As(err, &te) || err.Error() != "'list' object is not subscriptable" {
		t.Errorf("TypeError atteso, got %v", err)
	}
	if _, err := pyDictGet(nil, "a", nil); err == nil || err.Error() != "'NoneType' object has no attribute 'get'" {
		t.Errorf("AttributeError atteso, got %v", err)
	}
	if v, _ := pyDictGet(d, "x", "def"); v != "def" {
		t.Errorf("default non restituito: %v", v)
	}
	contains := []struct {
		c    any
		want bool
		err  bool
	}{
		{[]any{"gas", "luce"}, true, false},
		{[]any{"gas"}, false, false},
		{"luce,gas", true, false},
		{map[string]any{"luce": nil}, true, false},
		{nil, false, true},
		{int64(1), false, true},
	}
	for _, c := range contains {
		got, err := pyContains(c.c, "luce")
		if got != c.want || (err != nil) != c.err {
			t.Errorf("pyContains(%#v) = %v, %v", c.c, got, err)
		}
	}
	names := map[string]any{"NoneType": nil, "bool": true, "str": "", "float": 1.0, "int": int64(1), "list": []any{}, "dict": map[string]any{}, "uint8": uint8(1)}
	for want, v := range names {
		if got := pyTypeName(v); got != want {
			t.Errorf("pyTypeName(%#v) = %q, want %q", v, got, want)
		}
	}
}

func TestPyReprGoTypes(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "None"},
		{true, "True"},
		{false, "False"},
		{math.NaN(), "nan"},
		{math.Inf(1), "inf"},
		{math.Inf(-1), "-inf"},
		{3, "3"},
		{[]string{"luce", "it's"}, `['luce', "it's"]`},
		{LastNotifiedRates{"luce": 1.0}, "{'luce': 1.0}"},
		{map[string]any{"b": 1.0, "a": nil}, "{'a': None, 'b': 1.0}"},
		{"\u0085\u00a0\u200b\U000e0001", "'\\x85\\xa0\\u200b\\U000e0001'"},
		{struct{}{}, "{}"},
	}
	for _, c := range cases {
		if got := pyRepr(c.v); got != c.want {
			t.Errorf("pyRepr(%#v) = %s, want %s", c.v, got, c.want)
		}
	}
	if pyStr("abc") != "abc" || pyStr(nil) != "None" {
		t.Error("pyStr")
	}
	if got := pySetRepr(ValidFasceLuce); got != "{'bioraria', 'monoraria', 'trioraria'}" {
		t.Errorf("pySetRepr = %s", got)
	}
	if pyNumberRepr(big.NewInt(5)) != "5" || pyNumberRepr(int64(5)) != "5" || pyNumberRepr(0.5) != "0.5" {
		t.Error("pyNumberRepr")
	}
}

func TestPyMinMax(t *testing.T) {
	negZero := math.Copysign(0, -1)
	// min/max restituiscono il primo estremo come Python
	if got := pyMin([]float64{0, negZero}); math.Signbit(got) {
		t.Error("min([0.0, -0.0]) deve essere 0.0")
	}
	if got := pyMax([]float64{negZero, 0}); !math.Signbit(got) {
		t.Error("max([-0.0, 0.0]) deve essere -0.0")
	}
	if pyMin([]float64{3, 1, 2}) != 1 || pyMax([]float64{3, 1, 5}) != 5 {
		t.Error("min/max")
	}
}
