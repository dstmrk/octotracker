// Package formatters è il port di constants.py e formatters.py: costanti
// condivise e funzioni di formattazione usate nei messaggi utente.
//
// L'output deve essere identico byte per byte a quello della versione Python,
// in particolare per FormatNumber, che replica round(), il formato f-string
// "{x:.Nf}" e la manipolazione delle stringhe dell'originale.
package formatters

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultMaxDecimals è il valore di default di max_decimals in format_number.
const DefaultMaxDecimals = 3

// DefaultConsumptionPrefix è il prefisso di default di format_luce_consumption.
const DefaultConsumptionPrefix = "- "

// Limiti di ndigits oltre i quali round() di CPython non arrotonda
// (NDIGITS_MAX / NDIGITS_MIN in Objects/floatobject.c).
const (
	pyRoundNDigitsMax = 323
	pyRoundNDigitsMin = -308
)

// FormatNumber formatta un numero con logica intelligente per i decimali:
//   - se intero (es. 72.0) → "72" (nessun decimale)
//   - se ha decimali → mostra almeno 2 decimali, rimuove gli zeri trailing
//     oltre il secondo
//
// Usa la virgola come separatore decimale (stile italiano).
//
// Esempi: 72.0 → "72", 72.5 → "72,50", 0.145 → "0,145", 0.140 → "0,14",
// 0.100 → "0,10".
//
// Come la versione Python (dove int(nan) / int(inf) sollevano eccezione),
// va in panic se value è NaN o infinito.
func FormatNumber(value float64, maxDecimals int) string {
	// Arrotonda al massimo di decimali
	rounded := pyRound(value, maxDecimals)

	// Controlla se è un numero intero (int(rounded) solleva per NaN/inf)
	if math.IsNaN(rounded) {
		panic("ValueError: cannot convert float NaN to integer")
	}
	if math.IsInf(rounded, 0) {
		panic("OverflowError: cannot convert float infinity to integer")
	}
	if rounded == math.Trunc(rounded) {
		return pyIntString(rounded)
	}

	// Ha decimali: formatta con max decimali e poi sistema
	formatted := strconv.FormatFloat(rounded, 'f', maxDecimals, 64)

	// Rimuovi zeri trailing
	formatted = strings.TrimRight(formatted, "0")

	// Assicurati di avere almeno 2 decimali se ci sono decimali
	parts := strings.Split(formatted, ".")
	if len(parts) > 1 && len(parts[1]) < 2 {
		parts[1] += strings.Repeat("0", 2-len(parts[1]))
		formatted = strings.Join(parts, ".")
	}

	// Sostituisci punto con virgola (stile italiano)
	return strings.ReplaceAll(formatted, ".", ",")
}

// pyRound replica round(x, ndigits) di CPython per i float: il valore binario
// esatto viene arrotondato a ndigits cifre decimali (half-even sui pareggi
// esatti) e il decimale risultante viene riconvertito nel double più vicino.
func pyRound(x float64, ndigits int) float64 {
	// NaN e infiniti restano invariati
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	if ndigits > pyRoundNDigitsMax {
		return x
	}
	if ndigits < pyRoundNDigitsMin {
		return math.Copysign(0, x)
	}

	exact := new(big.Rat).SetFloat64(x)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(ndigits))), nil)
	scaleRat := new(big.Rat).SetInt(scale)

	// q = x * 10^ndigits (oppure x / 10^-ndigits)
	q := new(big.Rat)
	if ndigits >= 0 {
		q.Mul(exact, scaleRat)
	} else {
		q.Quo(exact, scaleRat)
	}

	n := roundHalfEven(q)

	// Riporta alla scala originale e converti nel double più vicino
	res := new(big.Rat).SetInt(n)
	if ndigits >= 0 {
		res.Quo(res, scaleRat)
	} else {
		res.Mul(res, scaleRat)
	}
	f, _ := res.Float64()
	if f == 0 {
		// Il segno dello zero viene preservato come in CPython ("-0e..")
		return math.Copysign(0, x)
	}
	return f
}

// roundHalfEven arrotonda un razionale all'intero più vicino (pareggi al pari).
func roundHalfEven(q *big.Rat) *big.Int {
	num := new(big.Int).Set(q.Num())
	den := q.Denom()
	neg := num.Sign() < 0
	if neg {
		num.Neg(num)
	}
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	// confronta 2*rem con den
	cmp := new(big.Int).Lsh(rem, 1).Cmp(den)
	if cmp > 0 || (cmp == 0 && quo.Bit(0) == 1) {
		quo.Add(quo, big.NewInt(1))
	}
	if neg {
		quo.Neg(quo)
	}
	return quo
}

// pyIntString replica str(int(x)) per un float finito con valore intero.
func pyIntString(x float64) string {
	i, _ := new(big.Float).SetFloat64(x).Int(nil)
	return i.String()
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// FormatUtilityTypeDisplay formatta tipo e fascia per visualizzazione.
//
// Esempi: ("fissa", "monoraria") → "Fissa Monoraria",
// ("variabile", "trioraria") → "Variabile Trioraria".
func FormatUtilityTypeDisplay(tipo, fascia string) string {
	return fmt.Sprintf("%s %s", pyCapitalize(tipo), pyCapitalize(fascia))
}

// pyCapitalize replica str.capitalize() di Python: primo carattere in
// title case, tutti gli altri in minuscolo. Usa le mappature semplici di
// Unicode (Python usa quelle complete, che differiscono solo per pochi
// caratteri speciali come "ß" o "İ", mai presenti nei dati del bot).
func pyCapitalize(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	var b strings.Builder
	b.Grow(len(s))
	b.WriteRune(unicode.ToTitle(r))
	for _, c := range s[size:] {
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}

// GetUtilityLabel restituisce la label per tipo tariffa e utility.
//
// Esempi: ("fissa", "luce") → "Prezzo fisso", ("variabile", "luce") →
// "Spread (PUN +)", ("variabile", "gas") → "Spread (PSV +)".
func GetUtilityLabel(tipo, utilityName string) string {
	if tipo == "fissa" {
		return LabelFixedPrice
	}

	// Variabile
	if utilityName == "luce" {
		return LabelVariableElectricity
	}
	return LabelVariableGas // gas
}

// GetUtilityUnit restituisce l'unità di misura per utility
// (€/kWh per luce, €/Smc per gas e qualsiasi altro valore).
func GetUtilityUnit(utilityName string) string {
	if utilityName == "luce" {
		return "€/kWh"
	}
	return "€/Smc"
}

// FormatUtilityHeader formatta l'header per la sezione utility (luce o gas).
//
// In Python riceve il dict utente; qui riceve direttamente tipo e fascia.
// Restituisce (tipoDisplay, label, unit), es. per ("luce", "fissa",
// "monoraria") → ("Fissa Monoraria", "Prezzo fisso", "€/kWh").
func FormatUtilityHeader(utilityName, tipo, fascia string) (tipoDisplay, label, unit string) {
	tipoDisplay = FormatUtilityTypeDisplay(tipo, fascia)
	label = GetUtilityLabel(tipo, utilityName)
	unit = GetUtilityUnit(utilityName)
	return tipoDisplay, label, unit
}

// FormatLuceConsumption formatta la riga dei consumi luce in base alla fascia.
//
// consumoF1/F2/F3 sono nil quando il campo è assente (None in Python).
// prefix è il prefisso della riga (in Python default "- ", vedi
// DefaultConsumptionPrefix). Restituisce la stringa vuota se consumoF1 è nil
// o la fascia non è riconosciuta.
//
// Come in Python (dove None + float solleva TypeError), va in panic se la
// fascia è bioraria/trioraria e manca uno dei consumi necessari.
func FormatLuceConsumption(fascia string, consumoF1, consumoF2, consumoF3 *float64, prefix string) string {
	if consumoF1 == nil {
		return ""
	}
	f1 := *consumoF1

	switch fascia {
	case "monoraria":
		return fmt.Sprintf("%sConsumo: <b>%s</b> kWh/anno\n", prefix, FormatNumber(f1, 0))

	case "bioraria":
		f2 := requireConsumo(consumoF2)
		totale := f1 + f2
		return fmt.Sprintf(
			"%sConsumo: <b>%s</b> kWh/anno - F1: %s kWh | F23: %s kWh\n",
			prefix, FormatNumber(totale, 0), FormatNumber(f1, 0), FormatNumber(f2, 0),
		)

	case "trioraria":
		f2 := requireConsumo(consumoF2)
		f3 := requireConsumo(consumoF3)
		totale := f1 + f2 + f3
		return fmt.Sprintf(
			"%sConsumo: <b>%s</b> kWh/anno - F1: %s kWh | F2: %s kWh | F3: %s kWh\n",
			prefix, FormatNumber(totale, 0), FormatNumber(f1, 0), FormatNumber(f2, 0),
			FormatNumber(f3, 0),
		)
	}

	return ""
}

// requireConsumo replica il TypeError di Python quando si somma None.
func requireConsumo(v *float64) float64 {
	if v == nil {
		panic("TypeError: unsupported operand type(s) for +: 'float' and 'NoneType'")
	}
	return *v
}
