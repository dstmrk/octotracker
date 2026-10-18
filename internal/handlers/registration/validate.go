package registration

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dstmrk/octotracker/internal/formatters"
)

// ========== INPUT VALIDATION ==========

// ValidateNumericInput valida un input numerico da parte dell'utente
// (validate_numeric_input).
//
// Validazioni eseguite, nello stesso ordine di Python:
//  1. lunghezza massima in caratteri (protezione da input molto lunghi);
//  2. conversione con float() dopo aver sostituito la virgola con il punto;
//  3. valore non negativo.
//
// Restituisce la tupla Python (valore, messaggio_errore) come
// (value, ok, errMsg):
//   - ok true: valore valido, errMsg vuoto;
//   - ok false ed errMsg vuoto: errore di conversione (None, None), gestito
//     dal chiamante;
//   - ok false ed errMsg non vuoto: input troppo lungo o negativo.
//
// Come in Python, float() accetta anche "nan", "inf" e "infinity" (con
// segno e in qualunque combinazione di maiuscole): nan e +inf superano la
// validazione, -inf è rifiutato come negativo.
func ValidateNumericInput(text string) (value float64, ok bool, errMsg string) {
	// 1. Verifica lunghezza (protezione attacchi): len() conta i caratteri
	if utf8.RuneCountInString(text) > formatters.MaxNumericInputLength {
		return 0, false, formatters.ErrorInputTooLong
	}

	// 2. Prova a convertire in float
	value, ok = pyFloat(strings.ReplaceAll(text, ",", "."))
	if !ok {
		return 0, false, "" // Errore di conversione, gestito dal chiamante
	}

	// 3. Verifica che non sia negativo
	if value < 0 {
		return 0, false, formatters.ErrorValueNegative
	}

	return value, true, ""
}

// pyFloat replica float(s) di CPython (PyFloat_FromString) per una stringa:
// false equivale al ValueError.
//
// Passi, come in CPython:
//  1. _PyUnicode_TransformDecimalAndSpaceToASCII: gli spazi Unicode
//     diventano ' ', le cifre decimali Unicode (categoria Nd) le cifre
//     ASCII corrispondenti, il primo altro carattere non ASCII tronca la
//     stringa con '?' (che rende l'input non valido);
//  2. _Py_string_to_number_with_underscores: '_' ammesso solo tra due cifre;
//  3. float_from_string_inner: strip degli spazi ASCII e parsing con
//     PyOS_string_to_double, che deve consumare tutta la stringa.
func pyFloat(s string) (float64, bool) {
	ascii, ok := transformDecimalAndSpaceToASCII(s)
	if !ok {
		return 0, false
	}
	// Nessun NUL ammesso (strtod si fermerebbe prima della fine).
	if strings.IndexByte(ascii, 0) >= 0 {
		return 0, false
	}
	if strings.IndexByte(ascii, '_') >= 0 {
		if ascii, ok = removeUnderscores(ascii); !ok {
			return 0, false
		}
	}
	return floatFromStringInner(ascii)
}

// transformDecimalAndSpaceToASCII replica l'omonima funzione di CPython.
// Restituisce false se la stringa contiene un carattere non ASCII che non è
// né uno spazio né una cifra decimale (CPython lo sostituisce con '?',
// rendendo comunque fallita la conversione).
func transformDecimalAndSpaceToASCII(s string) (string, bool) {
	isASCII := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			isASCII = false
			break
		}
	}
	if isASCII {
		return s, true
	}

	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 127:
			sb.WriteByte(byte(r))
		case pyIsSpace(r):
			sb.WriteByte(' ')
		default:
			d := pyDecimal(r)
			if d < 0 {
				return "", false
			}
			sb.WriteByte(byte('0' + d))
		}
	}
	return sb.String(), true
}

// pyIsSpace replica Py_UNICODE_ISSPACE (str.isspace) per i caratteri non
// ASCII: categoria Zs o classe bidirezionale WS, B, S.
func pyIsSpace(r rune) bool {
	switch r {
	case 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// pyDecimal replica Py_UNICODE_TODECIMAL: il valore della cifra decimale
// Unicode (Numeric_Type=Decimal), -1 per gli altri caratteri non ASCII.
//
// Le cifre decimali sono codificate in blocchi contigui da 0 a 9: la tabella
// decimalBlockStarts elenca l'inizio di ogni blocco secondo il database
// Unicode 15.1 di Python 3.13. Non si usa unicode.IsDigit perché le tabelle
// di Go includono già i blocchi di Unicode 16 (es. U+10D40 Garay), che per
// Python non sono cifre.
func pyDecimal(r rune) int {
	i := sort.Search(len(decimalBlockStarts), func(i int) bool { return decimalBlockStarts[i] > r }) - 1
	if i < 0 || r-decimalBlockStarts[i] >= 10 {
		return -1
	}
	return int(r - decimalBlockStarts[i])
}

// decimalBlockStarts: primo code point (cifra 0) di ogni blocco di cifre
// decimali non ASCII in Unicode 15.1 (unicodedata.decimal di Python 3.13).
var decimalBlockStarts = []rune{
	0x0660, 0x06F0, 0x07C0, 0x0966, 0x09E6, 0x0A66, 0x0AE6, 0x0B66,
	0x0BE6, 0x0C66, 0x0CE6, 0x0D66, 0x0DE6, 0x0E50, 0x0ED0, 0x0F20,
	0x1040, 0x1090, 0x17E0, 0x1810, 0x1946, 0x19D0, 0x1A80, 0x1A90,
	0x1B50, 0x1BB0, 0x1C40, 0x1C50, 0xA620, 0xA8D0, 0xA900, 0xA9D0,
	0xA9F0, 0xAA50, 0xABF0, 0xFF10, 0x104A0, 0x10D30, 0x11066, 0x110F0,
	0x11136, 0x111D0, 0x112F0, 0x11450, 0x114D0, 0x11650, 0x116C0, 0x11730,
	0x118E0, 0x11950, 0x11C50, 0x11D50, 0x11DA0, 0x11F50, 0x16A60, 0x16AC0,
	0x16B50, 0x1D7CE, 0x1D7D8, 0x1D7E2, 0x1D7EC, 0x1D7F6, 0x1E140, 0x1E2F0,
	0x1E4F0, 0x1E950, 0x1FBF0,
}

// removeUnderscores replica _Py_string_to_number_with_underscores: gli
// underscore sono ammessi solo tra due cifre e vengono rimossi.
func removeUnderscores(s string) (string, bool) {
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	var sb strings.Builder
	sb.Grow(len(s))
	var prev byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			// Underscores are only allowed after digits.
			if !isDigit(prev) {
				return "", false
			}
		} else {
			sb.WriteByte(c)
			// Underscores are only allowed before digits.
			if prev == '_' && !isDigit(c) {
				return "", false
			}
		}
		prev = c
	}
	// Underscores are not allowed at the end.
	if prev == '_' {
		return "", false
	}
	return sb.String(), true
}

var (
	// decimalFloatRe è la sintassi accettata da _Py_dg_strtod.
	decimalFloatRe = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	// infNanRe è la sintassi accettata da _Py_parse_inf_or_nan.
	infNanRe = regexp.MustCompile(`^(?i:[+-]?(?:inf|infinity|nan))$`)
)

// floatFromStringInner replica float_from_string_inner di CPython: strip
// degli spazi ASCII (Py_ISSPACE) e conversione dell'intera stringa.
func floatFromStringInner(s string) (float64, bool) {
	s = strings.Trim(s, " \t\n\v\f\r")
	if s == "" {
		return 0, false
	}

	if decimalFloatRe.MatchString(s) {
		// ParseFloat arrotonda correttamente come dtoa; in overflow
		// restituisce ±Inf (come Python) e in underflow 0.
		f, _ := strconv.ParseFloat(s, 64)
		return f, true
	}

	if infNanRe.MatchString(s) {
		lower := strings.ToLower(s)
		neg := strings.HasPrefix(lower, "-")
		if strings.HasSuffix(lower, "nan") {
			return math.NaN(), true
		}
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	}

	return 0, false
}
