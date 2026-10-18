package pycompat

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// IsSpace replica str.isspace() per un singolo carattere: gli spazi
// Unicode più i separatori ASCII U+001C..U+001F, che Python considera spazi
// (unicode.IsSpace di Go no).
func IsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// Strip replica str.strip() senza argomenti.
func Strip(s string) string {
	return strings.TrimFunc(s, IsSpace)
}

// FloatRepr replica repr(float) / str(float): cifre minime per il
// round-trip, notazione esponenziale se l'esponente decimale è < -4 o
// >= 16, altrimenti notazione fissa con almeno una cifra decimale
// ("72.0"). NaN e infiniti diventano "nan", "inf", "-inf".
func FloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
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

// StrRepr replica repr() di una str: apici singoli, doppi se la stringa
// contiene apici singoli e nessun apice doppio; escape di backslash,
// apice scelto, \t \n \r, caratteri di controllo e non stampabili.
func StrRepr(s string) string {
	quote := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == quote || r == '\\':
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
	b.WriteRune(quote)
	return b.String()
}

// BytesRepr replica repr() di un oggetto bytes (b'...').
func BytesRepr(p []byte) string {
	quote := byte('\'')
	if strings.IndexByte(string(p), '\'') >= 0 && strings.IndexByte(string(p), '"') < 0 {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte('b')
	b.WriteByte(quote)
	for _, c := range p {
		switch {
		case c == quote || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20 || c >= 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// SetRepr formatta un set Python di stringhe, es. {'gas', 'luce'}. In
// Python l'ordine degli elementi dipende dall'hash randomizzato delle
// stringhe (PYTHONHASHSEED): qui è sempre alfabetico.
func SetRepr(items []string) string {
	if len(items) == 0 {
		return "set()"
	}
	sorted := append([]string(nil), items...)
	sort.Strings(sorted)
	parts := make([]string, len(sorted))
	for i, s := range sorted {
		parts[i] = StrRepr(s)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// Repr replica repr() dei valori prodotti da Loads e dei tipi Go di base:
// nil → None, bool → True/False, string → 'str', float64 → repr(float),
// interi (anche *big.Int) → cifre decimali, Object → {'k': v}, []any →
// [a, b], []byte → b'...'.
func Repr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return StrRepr(x)
	case float64:
		return FloatRepr(x)
	case *big.Int:
		return x.String()
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []byte:
		return BytesRepr(x)
	case Object:
		parts := make([]string, len(x))
		for i, m := range x {
			parts[i] = StrRepr(m.Key) + ": " + Repr(m.Value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = Repr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(x)
	}
}

// Str replica str(v): le stringhe restano invariate, il resto è Repr(v).
func Str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return Repr(v)
}
