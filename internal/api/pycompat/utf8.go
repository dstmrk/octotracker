package pycompat

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// utf8Error descrive una sequenza non valida come la riporta il decoder
// UTF-8 di CPython: byte [start, end) e motivo.
type utf8Error struct {
	start, end int
	reason     string
}

// secondByteRange restituisce l'intervallo ammesso per il secondo byte di una
// sequenza che inizia con lead, e la lunghezza totale della sequenza (0 se
// lead non è un byte iniziale valido).
func secondByteRange(lead byte) (lo, hi byte, size int) {
	switch {
	case lead >= 0xc2 && lead <= 0xdf:
		return 0x80, 0xbf, 2
	case lead == 0xe0:
		return 0xa0, 0xbf, 3
	case lead == 0xed:
		return 0x80, 0x9f, 3
	case lead >= 0xe1 && lead <= 0xef:
		return 0x80, 0xbf, 3
	case lead == 0xf0:
		return 0x90, 0xbf, 4
	case lead == 0xf4:
		return 0x80, 0x8f, 4
	case lead >= 0xf1 && lead <= 0xf3:
		return 0x80, 0xbf, 4
	}
	return 0, 0, 0
}

// nextUTF8 decodifica la sequenza che inizia in p[i]: restituisce la
// lunghezza della sequenza valida, oppure l'errore secondo la regola delle
// "maximal subparts" usata da CPython.
func nextUTF8(p []byte, i int) (int, *utf8Error) {
	lead := p[i]
	if lead < utf8.RuneSelf {
		return 1, nil
	}
	lo, hi, size := secondByteRange(lead)
	if size == 0 {
		return 0, &utf8Error{i, i + 1, "invalid start byte"}
	}
	for k := 1; k < size; k++ {
		if i+k >= len(p) {
			return 0, &utf8Error{i, len(p), "unexpected end of data"}
		}
		c := p[i+k]
		if k > 1 {
			lo, hi = 0x80, 0xbf
		}
		if c < lo || c > hi {
			return 0, &utf8Error{i, i + k, "invalid continuation byte"}
		}
	}
	return size, nil
}

// DecodeUTF8Replace replica bytes.decode("utf-8", errors="replace"): ogni
// "maximal subpart" non valida diventa un solo U+FFFD.
func DecodeUTF8Replace(p []byte) string {
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); {
		n, err := nextUTF8(p, i)
		if err != nil {
			b.WriteRune(utf8.RuneError)
			i = err.end
			continue
		}
		b.Write(p[i : i+n])
		i += n
	}
	return b.String()
}

// DecodeUTF8 replica bytes.decode("utf-8") stretto: in caso di sequenza non
// valida restituisce un UnicodeDecodeError con il messaggio di CPython, es.
// "'utf-8' codec can't decode byte 0xff in position 0: invalid start byte".
func DecodeUTF8(p []byte) (string, error) {
	for i := 0; i < len(p); {
		n, err := nextUTF8(p, i)
		if err != nil {
			var msg string
			if err.end-err.start == 1 {
				msg = fmt.Sprintf("'utf-8' codec can't decode byte 0x%02x in position %d: %s", p[err.start], err.start, err.reason)
			} else {
				msg = fmt.Sprintf("'utf-8' codec can't decode bytes in position %d-%d: %s", err.start, err.end-1, err.reason)
			}
			return "", NewError("UnicodeDecodeError", msg)
		}
		i += n
	}
	return string(p), nil
}

// Latin1 decodifica p come latin-1 (ogni byte diventa il carattere con lo
// stesso codice), come fa Tornado con le righe di header HTTP.
func Latin1(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		b.WriteRune(rune(p[i]))
	}
	return b.String()
}

// EncodeLatin1 è l'inverso di Latin1: ogni carattere (< U+0100) diventa un
// byte. I caratteri fuori range, che non possono comparire in una stringa
// prodotta da Latin1, vengono scartati.
func EncodeLatin1(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x100 {
			out = append(out, byte(r))
		}
	}
	return out
}
