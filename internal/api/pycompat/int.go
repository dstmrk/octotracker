package pycompat

import (
	"fmt"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxStrDigits è il limite di cifre per le conversioni str ↔ int di Python
// (sys.get_int_max_str_digits(), default 4300).
const MaxStrDigits = 4300

// maxReprInMessage è la precisione di %.200R nel messaggio di int().
const maxReprInMessage = 200

// isASCIISpace replica Py_ISSPACE per i caratteri ASCII.
func isASCIISpace(c byte) bool {
	return c == ' ' || (c >= '\t' && c <= '\r')
}

// digitValue restituisce il valore di una cifra decimale Unicode (categoria
// Nd): le cifre sono assegnate in blocchi contigui da 0 a 9.
func digitValue(r rune) int {
	if r >= '0' && r <= '9' {
		return int(r - '0')
	}
	n := 0
	for unicode.IsDigit(r - rune(n) - 1) {
		n++
	}
	return n % 10
}

// toASCIIDecimal replica _PyUnicode_TransformDecimalAndSpaceToASCII: cifre
// Unicode → cifre ASCII, spazi Unicode → ' ', altri caratteri non ASCII → '?'.
func toASCIIDecimal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < utf8.RuneSelf:
			b.WriteByte(byte(r))
		case IsSpace(r):
			b.WriteByte(' ')
		case unicode.IsDigit(r):
			b.WriteByte(byte('0' + digitValue(r)))
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// truncatedRepr replica %.200R: repr() troncato a 200 caratteri.
func truncatedRepr(s string) string {
	r := []rune(StrRepr(s))
	if len(r) > maxReprInMessage {
		r = r[:maxReprInMessage]
	}
	return string(r)
}

// ParseInt replica int(s) di Python in base 10 su una str: spazi ai lati,
// segno opzionale, cifre decimali Unicode separate al più da singoli
// underscore. Restituisce un ValueError con lo stesso messaggio di Python
// ("invalid literal for int() with base 10: 'abc'", oppure il messaggio
// del limite di 4300 cifre).
func ParseInt(s string) (*big.Int, error) {
	invalid := func() error {
		return NewError("ValueError", "invalid literal for int() with base 10: "+truncatedRepr(s))
	}
	t := toASCIIDecimal(s)
	i := 0
	for i < len(t) && isASCIISpace(t[i]) {
		i++
	}
	var digits strings.Builder
	if i < len(t) && (t[i] == '+' || t[i] == '-') {
		if t[i] == '-' {
			digits.WriteByte('-')
		}
		i++
	}
	n := 0
	prevDigit := false
	for i < len(t) {
		c := t[i]
		if c >= '0' && c <= '9' {
			digits.WriteByte(c)
			n++
			prevDigit = true
		} else if c == '_' && prevDigit && i+1 < len(t) && t[i+1] >= '0' && t[i+1] <= '9' {
			prevDigit = false
		} else {
			break
		}
		i++
	}
	if n == 0 {
		return nil, invalid()
	}
	for i < len(t) && isASCIISpace(t[i]) {
		i++
	}
	if i != len(t) {
		return nil, invalid()
	}
	if n > MaxStrDigits {
		return nil, NewError("ValueError", fmt.Sprintf(
			"Exceeds the limit (%d digits) for integer string conversion: value has %d digits; "+
				"use sys.set_int_max_str_digits() to increase the limit", MaxStrDigits, n))
	}
	v, _ := new(big.Int).SetString(digits.String(), 10)
	return v, nil
}

// FormatInt replica str(n) per un int, compreso il ValueError oltre le
// 4300 cifre.
func FormatInt(n *big.Int) (string, error) {
	s := n.String()
	digits := len(strings.TrimPrefix(s, "-"))
	if digits > MaxStrDigits {
		return "", NewError("ValueError", fmt.Sprintf(
			"Exceeds the limit (%d digits) for integer string conversion; "+
				"use sys.set_int_max_str_digits() to increase the limit", MaxStrDigits))
	}
	return s, nil
}
