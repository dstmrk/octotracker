// Package formatters è il port di constants.py e formatters.py: costanti
// condivise e funzioni di formattazione usate nei messaggi utente.
//
// L'output deve essere identico byte per byte a quello della versione Python,
// in particolare per FormatNumber (verificato dai test golden in testdata/).
package formatters

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultMaxDecimals è il valore di default di max_decimals in format_number.
const DefaultMaxDecimals = 3

// DefaultConsumptionPrefix è il prefisso di default di format_luce_consumption.
const DefaultConsumptionPrefix = "- "

// FormatNumber formatta un numero con logica intelligente per i decimali:
//   - se intero (es. 72.0) → "72" (nessun decimale)
//   - se ha decimali → mostra almeno 2 decimali, rimuove gli zeri trailing
//     oltre il secondo
//
// Usa la virgola come separatore decimale (stile italiano).
//
// Esempi: 72.0 → "72", 72.5 → "72,50", 0.145 → "0,145", 0.140 → "0,14",
// 0.100 → "0,10". maxDecimals deve essere >= 0.
//
// Come la versione Python, va in panic se value è NaN o infinito.
func FormatNumber(value float64, maxDecimals int) string {
	if math.IsNaN(value) {
		panic("ValueError: cannot convert float NaN to integer")
	}
	if math.IsInf(value, 0) {
		panic("OverflowError: cannot convert float infinity to integer")
	}

	// Arrotonda al massimo di decimali (come round(value, maxDecimals):
	// FormatFloat arrotonda correttamente il valore binario esatto,
	// half-even sui pareggi)
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(value, 'f', maxDecimals, 64), 64)

	// Se è un numero intero niente decimali (str(int(x)): -0 diventa "0")
	if rounded == 0 {
		return "0"
	}
	if rounded == math.Trunc(rounded) {
		return strconv.FormatFloat(rounded, 'f', 0, 64)
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

// FormatUtilityTypeDisplay formatta tipo e fascia per visualizzazione.
//
// Esempi: ("fissa", "monoraria") → "Fissa Monoraria",
// ("variabile", "trioraria") → "Variabile Trioraria".
func FormatUtilityTypeDisplay(tipo, fascia string) string {
	return fmt.Sprintf("%s %s", Capitalize(tipo), Capitalize(fascia))
}

// Capitalize restituisce s con il primo carattere in title case e gli altri
// in minuscolo, come str.capitalize() di Python (es. "luce" → "Luce").
func Capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return string(unicode.ToTitle(r)) + strings.ToLower(s[size:])
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

// requireConsumo restituisce il consumo; va in panic se manca, come la
// versione Python (None + float).
func requireConsumo(v *float64) float64 {
	if v == nil {
		panic("TypeError: unsupported operand type(s) for +: 'float' and 'NoneType'")
	}
	return *v
}
