package scraper

// Helper che replicano il comportamento di Python usato da data_reader.py e
// backfill_rate_history.py: str.strip(), str.isdigit(), float(), int(),
// repr() di str/float, json.dumps e le eccezioni non gestite.

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// PyException rappresenta un'eccezione Python che nel codice originale non
// viene gestita e risale al chiamante (es. AttributeError su NOME_OFFERTA
// mancante, ValueError da float()). Error() restituisce str(e), così il
// chiamante può loggarla come Python ("❌ Errore inatteso scraper: {e}").
type PyException struct {
	// Type è il nome della classe Python (es. "ValueError").
	Type string
	// Msg è str(e).
	Msg string
}

// Error implementa error restituendo str(e).
func (e *PyException) Error() string { return e.Msg }

func newPyException(typ, format string, args ...any) *PyException {
	return &PyException{Type: typ, Msg: fmt.Sprintf(format, args...)}
}

// ========== Classificazione caratteri (tabelle Unicode di Python) ==========

// pyIsSpace replica str.isspace() su un singolo carattere.
func pyIsSpace(r rune) bool {
	_, found := slices.BinarySearch(pySpaceRunes, r)
	return found
}

// pyDecimal replica unicodedata.decimal(ch, -1): il valore della cifra
// decimale, -1 se il carattere non è una cifra decimale.
func pyDecimal(r rune) int {
	i, found := slices.BinarySearch(pyDecimalRunStarts, r)
	if found {
		return 0
	}
	if i > 0 && r-pyDecimalRunStarts[i-1] < 10 {
		return int(r - pyDecimalRunStarts[i-1])
	}
	return -1
}

// pyIsDigit replica str.isdigit() su un singolo carattere.
func pyIsDigit(r rune) bool {
	if pyDecimal(r) >= 0 {
		return true
	}
	i, _ := slices.BinarySearchFunc(pyDigitOnlyRanges, r, func(rg [2]rune, t rune) int {
		switch {
		case rg[1] < t:
			return -1
		case rg[0] > t:
			return 1
		}
		return 0
	})
	return i < len(pyDigitOnlyRanges) && pyDigitOnlyRanges[i][0] <= r && r <= pyDigitOnlyRanges[i][1]
}

// pyStrip replica str.strip() senza argomenti.
func pyStrip(s string) string {
	return strings.TrimFunc(s, pyIsSpace)
}

// pyIsDigitString replica str.isdigit(): True se la stringa non è vuota e
// tutti i caratteri sono cifre.
func pyIsDigitString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !pyIsDigit(r) {
			return false
		}
	}
	return true
}

// transformDecimalAndSpaceToASCII replica _PyUnicode_TransformDecimalAndSpaceToASCII:
// spazi Unicode → ' ', cifre decimali Unicode → '0'-'9', altri caratteri non
// ASCII → '?' (così il parsing successivo fallisce).
func transformDecimalAndSpaceToASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < utf8.RuneSelf:
			b.WriteRune(r)
		case pyIsSpace(r):
			b.WriteByte(' ')
		case pyDecimal(r) >= 0:
			b.WriteByte(byte('0' + pyDecimal(r)))
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// ========== float() ==========

// ParseFloat replica float(s) di Python su una stringa: spazi Unicode attorno,
// cifre decimali Unicode, underscore tra cifre, "inf"/"infinity"/"nan"
// (maiuscole/minuscole, con segno); niente esadecimali. In caso di errore
// restituisce un *PyException ValueError con il messaggio di Python
// ("could not convert string to float: '...'").
func ParseFloat(s string) (float64, error) {
	fail := func() (float64, error) {
		return 0, newPyException("ValueError", "could not convert string to float: %s", pyStrRepr(s))
	}
	t := transformDecimalAndSpaceToASCII(s)
	if strings.Contains(t, "\x00") {
		return fail()
	}
	if strings.Contains(t, "_") {
		var ok bool
		if t, ok = removeNumericUnderscores(t); !ok {
			return fail()
		}
	}
	t = strings.Trim(t, " \t\n\v\f\r")
	if !isPyFloatLiteral(t) {
		return fail()
	}
	sign, mag := 1.0, t
	if mag[0] == '+' || mag[0] == '-' {
		if mag[0] == '-' {
			sign = -1
		}
		mag = mag[1:]
	}
	switch strings.ToLower(mag) {
	case "inf", "infinity":
		return math.Inf(int(sign)), nil
	case "nan":
		return math.Copysign(math.NaN(), sign), nil
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		// Overflow/underflow: Python restituisce ±inf o 0.0 senza errore.
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			return fail()
		}
	}
	return f, nil
}

// pyFloatOf replica float(elem.text): text nil (None) solleva TypeError.
func pyFloatOf(text *string) (float64, error) {
	if text == nil {
		return 0, newPyException("TypeError", "float() argument must be a string or a real number, not 'NoneType'")
	}
	return ParseFloat(*text)
}

// removeNumericUnderscores replica _Py_string_to_number_with_underscores:
// ogni underscore deve stare tra due cifre ASCII.
func removeNumericUnderscores(s string) (string, bool) {
	var b strings.Builder
	var prev byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			if prev < '0' || prev > '9' {
				return "", false
			}
		} else {
			if prev == '_' && (c < '0' || c > '9') {
				return "", false
			}
			b.WriteByte(c)
		}
		prev = c
	}
	if prev == '_' {
		return "", false
	}
	return b.String(), true
}

// isPyFloatLiteral verifica la grammatica accettata da PyOS_string_to_double:
// [+-]? (inf | infinity | nan | cifre[.cifre?] | .cifre) ([eE][+-]?cifre)?
func isPyFloatLiteral(s string) bool {
	if s != "" && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	switch strings.ToLower(s) {
	case "inf", "infinity", "nan":
		return true
	}
	digits := func(i int) int {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		return j
	}
	i := digits(0)
	intDigits := i
	fracDigits := 0
	if i < len(s) && s[i] == '.' {
		j := digits(i + 1)
		fracDigits = j - i - 1
		i = j
	}
	if intDigits == 0 && fracDigits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		j := digits(i)
		if j == i {
			return false
		}
		i = j
	}
	return i == len(s)
}

// ========== int() ==========

// pyMaxStrDigits è sys.get_int_max_str_digits() di default.
const pyMaxStrDigits = 4300

// pyIntString replica str(int(s)) per una stringa che soddisfa str.isdigit():
// le cifre decimali Unicode diventano ASCII e gli zeri iniziali spariscono;
// le cifre non decimali (es. "²") sollevano ValueError come int().
func pyIntString(s string) (string, error) {
	var b strings.Builder
	n := 0
	for _, r := range s {
		d := pyDecimal(r)
		if d < 0 {
			return "", newPyException("ValueError", "invalid literal for int() with base 10: %s", truncateRunes(pyStrRepr(s), 200))
		}
		b.WriteByte(byte('0' + d))
		n++
	}
	if n > pyMaxStrDigits {
		return "", newPyException("ValueError",
			"Exceeds the limit (%d digits) for integer string conversion: value has %d digits; use sys.set_int_max_str_digits() to increase the limit",
			pyMaxStrDigits, n)
	}
	out := strings.TrimLeft(b.String(), "0")
	if out == "" {
		out = "0"
	}
	return out, nil
}

// truncateRunes replica la precisione di %.200R (troncamento a n caratteri).
func truncateRunes(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// ========== repr() ==========

// pyFloatRepr replica repr(float) (e quindi str(float) e f"{x}").
func pyFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	return pyFloatReprFinite(f)
}

// pyFloatReprFinite: cifre minime per il round-trip; notazione esponenziale
// se l'esponente decimale è < -4 o >= 16 (regola di repr in CPython),
// altrimenti notazione fissa con almeno una cifra decimale (".0").
func pyFloatReprFinite(f float64) string {
	e := strconv.FormatFloat(f, 'e', -1, 64)
	exp, _ := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
	if exp < -4 || exp >= 16 {
		return e
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}

// pyOptFloatStr replica f"{x}" per un float opzionale (None se nil).
func pyOptFloatStr(f *float64) string {
	if f == nil {
		return "None"
	}
	return pyFloatRepr(*f)
}

// pyOptStr replica f"{x}" per una str opzionale (None se nil).
func pyOptStr(s *string) string {
	if s == nil {
		return "None"
	}
	return *s
}

// pyStrRepr replica repr() di una str (apici singoli, doppi se la stringa
// contiene apici singoli e nessun apice doppio).
func pyStrRepr(s string) string {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f || unicode.IsPrint(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// pyOptStrRepr replica repr() di una str opzionale (None se nil).
func pyOptStrRepr(s *string) string {
	if s == nil {
		return "None"
	}
	return pyStrRepr(*s)
}

// ========== json.dumps ==========

// pyJSONFloat replica la serializzazione di un float in json.dumps
// (allow_nan=True: NaN, Infinity, -Infinity).
func pyJSONFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return pyFloatReprFinite(f)
}

// pyJSONString replica l'escape di json.dumps con ensure_ascii=True.
func pyJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			switch {
			case r >= 0x20 && r < 0x7f:
				b.WriteRune(r)
			case r > 0xffff:
				r1, r2 := utf16.EncodeRune(r)
				fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
			default:
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
