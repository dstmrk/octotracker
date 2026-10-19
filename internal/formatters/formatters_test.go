package formatters

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// ========== Test portati da tests/test_checker.py ==========

func TestFormatNumberInteger(t *testing.T) {
	// format_number con numero intero
	if got := FormatNumber(72.0, 2); got != "72" {
		t.Fatalf("got %q, want %q", got, "72")
	}
}

func TestFormatNumberWithDecimals(t *testing.T) {
	// format_number con decimali
	if got := FormatNumber(0.1078, 4); got != "0,1078" {
		t.Fatalf("got %q, want %q", got, "0,1078")
	}
}

func TestFormatNumberTrailingZeros(t *testing.T) {
	// format_number rimuove zeri trailing oltre il secondo decimale
	if got := FormatNumber(0.1000, 4); got != "0,10" {
		t.Fatalf("got %q, want %q", got, "0,10")
	}
}

func TestFormatNumberTwoDecimalsMin(t *testing.T) {
	// format_number mantiene almeno 2 decimali
	if got := FormatNumber(0.5, 4); got != "0,50" {
		t.Fatalf("got %q, want %q", got, "0,50")
	}
}

// ========== Esempi della docstring ==========

func TestFormatNumberDocstringExamples(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{72.0, "72"},
		{72.5, "72,50"},
		{0.145, "0,145"},
		{0.140, "0,14"},
		{0.100, "0,10"},
	}
	for _, tt := range tests {
		if got := FormatNumber(tt.value, DefaultMaxDecimals); got != tt.want {
			t.Errorf("FormatNumber(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestFormatNumberEdgeCases(t *testing.T) {
	tests := []struct {
		value       float64
		maxDecimals int
		want        string
	}{
		{0, 2, "0"},
		{math.Copysign(0, -1), 2, "0"},
		{-1e-7, 4, "0"},
		{0.125, 2, "0,12"}, // pareggio esatto: half-even
		{0.375, 2, "0,38"},
		{2.5, 0, "2"},
		{3.5, 0, "4"},
		{2.675, 2, "2,67"}, // 2.675 in binario è leggermente sotto
		{0.1 + 0.2, 4, "0,30"},
		{-0.00005, 4, "-0,0001"},
		{1e20, 2, "100000000000000000000"},
		{123.456, 400, "123,4560000000000030695446184836328029632568359375"},
	}
	for _, tt := range tests {
		if got := FormatNumber(tt.value, tt.maxDecimals); got != tt.want {
			t.Errorf("FormatNumber(%v, %d) = %q, want %q", tt.value, tt.maxDecimals, got, tt.want)
		}
	}
}

func TestFormatNumberPanicsOnNaNAndInf(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{math.NaN(), "FormatNumber: valore NaN"},
		{math.Inf(1), "FormatNumber: valore infinito"},
		{math.Inf(-1), "FormatNumber: valore infinito"},
	}
	for _, tt := range tests {
		if got := panicMessage(func() { FormatNumber(tt.value, 2) }); got != tt.want {
			t.Errorf("FormatNumber(%v) panic = %q, want %q", tt.value, got, tt.want)
		}
	}
}

// ========== Costanti ==========

func TestConstants(t *testing.T) {
	if MaxDecimalsEnergy != 4 || MaxDecimalsCost != 2 || MaxNumericInputLength != 10 {
		t.Fatal("costanti numeriche diverse da constants.py")
	}
	want := fmt.Sprintf("❌ Il valore inserito è troppo lungo (max %d caratteri)", MaxNumericInputLength)
	if ErrorInputTooLong != want {
		t.Errorf("ErrorInputTooLong = %q, want %q", ErrorInputTooLong, want)
	}
	if ErrorValueNegative != "❌ Il valore deve essere maggiore o uguale a zero" {
		t.Errorf("ErrorValueNegative = %q", ErrorValueNegative)
	}
	if LabelFixedPrice != "Prezzo fisso" || LabelVariableElectricity != "Spread (PUN +)" || LabelVariableGas != "Spread (PSV +)" {
		t.Error("label diverse da constants.py")
	}
}

// ========== Esempi delle docstring delle altre funzioni ==========

func TestFormatUtilityTypeDisplay(t *testing.T) {
	if got := FormatUtilityTypeDisplay("fissa", "monoraria"); got != "Fissa Monoraria" {
		t.Errorf("got %q", got)
	}
	if got := FormatUtilityTypeDisplay("variabile", "trioraria"); got != "Variabile Trioraria" {
		t.Errorf("got %q", got)
	}
}

func TestGetUtilityLabelAndUnit(t *testing.T) {
	if got := GetUtilityLabel("fissa", "luce"); got != "Prezzo fisso" {
		t.Errorf("got %q", got)
	}
	if got := GetUtilityLabel("variabile", "luce"); got != "Spread (PUN +)" {
		t.Errorf("got %q", got)
	}
	if got := GetUtilityLabel("variabile", "gas"); got != "Spread (PSV +)" {
		t.Errorf("got %q", got)
	}
	if got := GetUtilityUnit("luce"); got != "€/kWh" {
		t.Errorf("got %q", got)
	}
	if got := GetUtilityUnit("gas"); got != "€/Smc" {
		t.Errorf("got %q", got)
	}
}

func TestFormatUtilityHeaderExample(t *testing.T) {
	tipo, label, unit := FormatUtilityHeader("luce", "fissa", "monoraria")
	if tipo != "Fissa Monoraria" || label != "Prezzo fisso" || unit != "€/kWh" {
		t.Errorf("got (%q, %q, %q)", tipo, label, unit)
	}
}

func TestFormatLuceConsumptionExamples(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		name       string
		fascia     string
		f1, f2, f3 *float64
		prefix     string
		want       string
	}{
		{"senza consumi", "monoraria", nil, nil, nil, DefaultConsumptionPrefix, ""},
		{"monoraria", "monoraria", f(2700), nil, nil, DefaultConsumptionPrefix, "- Consumo: <b>2700</b> kWh/anno\n"},
		{
			"bioraria", "bioraria", f(1500), f(1200), nil, "  - ",
			"  - Consumo: <b>2700</b> kWh/anno - F1: 1500 kWh | F23: 1200 kWh\n",
		},
		{
			"trioraria", "trioraria", f(1000), f(900), f(800), DefaultConsumptionPrefix,
			"- Consumo: <b>2700</b> kWh/anno - F1: 1000 kWh | F2: 900 kWh | F3: 800 kWh\n",
		},
		{"fascia sconosciuta", "altro", f(1000), f(1), f(1), DefaultConsumptionPrefix, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatLuceConsumption(tt.fascia, tt.f1, tt.f2, tt.f3, tt.prefix); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ========== Golden test (output generati dalla versione Python) ==========

type numberGolden struct {
	Cases    [][5]string       `json:"cases"`
	Others   [][3]any          `json:"others"`
	Defaults [][2]string       `json:"defaults"`
	Errors   []json.RawMessage `json:"errors"`
}

func loadJSON(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("lettura golden: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("parsing golden %s: %v", name, err)
	}
}

func parsePyFloat(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("ParseFloat(%q): %v", s, err)
	}
	return v
}

func TestFormatNumberGolden(t *testing.T) {
	var g numberGolden
	loadJSON(t, "format_number_golden.json", &g)
	if len(g.Cases) < 1000 || len(g.Others) < 1000 || len(g.Defaults) < 300 {
		t.Fatalf("golden troppo piccolo: %d/%d/%d", len(g.Cases), len(g.Others), len(g.Defaults))
	}

	decimals := []int{0, 2, 3, 4}
	for _, row := range g.Cases {
		for i, nd := range decimals {
			checkFormatNumber(t, row[0], nd, row[i+1])
		}
	}
	for _, row := range g.Others {
		checkFormatNumber(t, row[0].(string), int(row[1].(float64)), row[2].(string))
	}
	for _, row := range g.Defaults {
		checkFormatNumber(t, row[0], DefaultMaxDecimals, row[1])
	}

	for _, raw := range g.Errors {
		var e struct {
			Repr  string `json:"repr"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		// Come in Python va in panic (il testo dell'eccezione non conta)
		v := parsePyFloat(t, e.Repr)
		if got := panicMessage(func() { FormatNumber(v, 2) }); got == "" {
			t.Errorf("FormatNumber(%s): atteso panic (%s)", e.Repr, e.Error)
		}
	}
}

func checkFormatNumber(t *testing.T, repr string, nd int, want string) {
	t.Helper()
	if got := FormatNumber(parsePyFloat(t, repr), nd); got != want {
		t.Errorf("FormatNumber(%s, %d) = %q, want %q", repr, nd, got, want)
	}
}

type formattersGolden struct {
	TypeDisplay []struct {
		Tipo, Fascia, Result string
	} `json:"type_display"`
	Labels []struct {
		Tipo    string `json:"tipo"`
		Utility string `json:"utility"`
		Result  string `json:"result"`
	} `json:"labels"`
	Units []struct {
		Utility string `json:"utility"`
		Result  string `json:"result"`
	} `json:"units"`
	Headers []struct {
		Utility string    `json:"utility"`
		Tipo    string    `json:"tipo"`
		Fascia  string    `json:"fascia"`
		Result  [3]string `json:"result"`
	} `json:"headers"`
	Consumption        [][6]any `json:"consumption"`
	ConsumptionMissing []struct {
		Fascia string   `json:"fascia"`
		F2     *float64 `json:"f2"`
		F3     *float64 `json:"f3"`
		Result *string  `json:"result"`
		Error  *string  `json:"error"`
	} `json:"consumption_missing"`
}

func TestFormattersGolden(t *testing.T) {
	var g formattersGolden
	loadJSON(t, "formatters_golden.json", &g)

	for _, c := range g.TypeDisplay {
		if got := FormatUtilityTypeDisplay(c.Tipo, c.Fascia); got != c.Result {
			t.Errorf("FormatUtilityTypeDisplay(%q, %q) = %q, want %q", c.Tipo, c.Fascia, got, c.Result)
		}
	}
	for _, c := range g.Labels {
		if got := GetUtilityLabel(c.Tipo, c.Utility); got != c.Result {
			t.Errorf("GetUtilityLabel(%q, %q) = %q, want %q", c.Tipo, c.Utility, got, c.Result)
		}
	}
	for _, c := range g.Units {
		if got := GetUtilityUnit(c.Utility); got != c.Result {
			t.Errorf("GetUtilityUnit(%q) = %q, want %q", c.Utility, got, c.Result)
		}
	}
	for _, c := range g.Headers {
		a, b, u := FormatUtilityHeader(c.Utility, c.Tipo, c.Fascia)
		if [3]string{a, b, u} != c.Result {
			t.Errorf("FormatUtilityHeader(%q, %q, %q) = %q, want %q", c.Utility, c.Tipo, c.Fascia, [3]string{a, b, u}, c.Result)
		}
	}
}

func TestFormatLuceConsumptionGolden(t *testing.T) {
	var g formattersGolden
	loadJSON(t, "formatters_golden.json", &g)

	if len(g.Consumption) < 200 {
		t.Fatalf("golden consumi troppo piccolo: %d", len(g.Consumption))
	}
	for _, row := range g.Consumption {
		prefix := DefaultConsumptionPrefix
		if row[1] != nil {
			prefix = row[1].(string)
		}
		got := FormatLuceConsumption(row[0].(string), optFloat(row[2]), optFloat(row[3]), optFloat(row[4]), prefix)
		if want := row[5].(string); got != want {
			t.Errorf("FormatLuceConsumption(%v) = %q, want %q", row[:5], got, want)
		}
	}

	one := 1000.0
	for _, c := range g.ConsumptionMissing {
		var got string
		msg := panicMessage(func() {
			got = FormatLuceConsumption(c.Fascia, &one, c.F2, c.F3, DefaultConsumptionPrefix)
		})
		switch {
		case c.Error != nil:
			// Come in Python va in panic (il testo dell'eccezione non conta)
			if msg == "" {
				t.Errorf("%s f2=%v f3=%v: atteso panic (%s), ottenuto %q", c.Fascia, c.F2, c.F3, *c.Error, got)
			}
		case msg != "":
			t.Errorf("%s f2=%v f3=%v: panic inatteso %q", c.Fascia, c.F2, c.F3, msg)
		case got != *c.Result:
			t.Errorf("%s f2=%v f3=%v: got %q, want %q", c.Fascia, c.F2, c.F3, got, *c.Result)
		}
	}
}

func optFloat(v any) *float64 {
	if v == nil {
		return nil
	}
	f := v.(float64)
	return &f
}

func panicMessage(fn func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	fn()
	return ""
}

func TestCapitalize(t *testing.T) {
	tests := map[string]string{
		"":           "",
		"fissa":      "Fissa",
		"FISSA":      "Fissa",
		"fIsSa":      "Fissa",
		"àbc":        "Àbc",
		" fissa":     " fissa",
		"123ABC":     "123abc",
		"élan VITAL": "Élan vital",
	}
	for in, want := range tests {
		if got := Capitalize(in); got != want {
			t.Errorf("Capitalize(%q) = %q, want %q", in, got, want)
		}
	}
	if !strings.HasPrefix(Capitalize("ǆa"), "ǅ") {
		t.Error("Capitalize deve usare il title case per il primo carattere")
	}
}
