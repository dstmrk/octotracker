package pycompat

import (
	"strings"
	"unicode/utf8"
)

// QueryField è un parametro di parse_qs: il nome e i suoi valori
// nell'ordine in cui compaiono.
type QueryField struct {
	Name   string
	Values []string
}

// QueryValues è il dict restituito da parse_qs (ordine di prima comparsa
// dei nomi).
type QueryValues []QueryField

// Get restituisce i valori del parametro name (nil se assente).
func (q QueryValues) Get(name string) []string {
	for _, f := range q {
		if f.Name == name {
			return f.Values
		}
	}
	return nil
}

// QueryPair è una coppia (nome, valore) di parse_qsl.
type QueryPair struct {
	Name, Value string
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// unquoteImpl replica urllib.parse._unquote_impl su una sequenza di byte:
// le sequenze %XX (esadecimali, maiuscole o minuscole) diventano il byte
// corrispondente, ogni altro '%' resta invariato.
func unquoteImpl(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				out = append(out, hi<<4|lo)
				i += 2
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// Unquote replica urllib.parse.unquote(s) (encoding="utf-8",
// errors="replace") su una str: i tratti ASCII vengono decodificati da
// %XX e poi da UTF-8 con sostituzione, i caratteri non ASCII restano
// invariati.
func Unquote(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	var b strings.Builder
	start := 0
	flush := func(end int) {
		if end > start {
			b.WriteString(DecodeUTF8Replace(unquoteImpl(s[start:end])))
		}
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r < utf8.RuneSelf {
			i += size
			continue
		}
		flush(i)
		b.WriteString(s[i : i+size])
		i += size
		start = i
	}
	flush(len(s))
	return b.String()
}

// UnquotePlus replica urllib.parse.unquote_plus: '+' diventa spazio, poi Unquote.
func UnquotePlus(s string) string {
	return Unquote(strings.ReplaceAll(s, "+", " "))
}

// UnquotePlusBytes replica unquote_plus con encoding="latin1" su una str
// ottenuta decodificando byte grezzi come latin-1 (parse_qs_bytes di
// Tornado): restituisce i byte decodificati da %XX, con '+' → spazio.
func UnquotePlusBytes(raw string) []byte {
	return unquoteImpl(strings.ReplaceAll(raw, "+", " "))
}

// errMaxFields è il ValueError di parse_qsl con max_num_fields superato.
func errMaxFields() error {
	return NewError("ValueError", "Max number of fields exceeded")
}

// splitQSL replica il ciclo di parse_qsl (separatore '&', strict_parsing
// falso): restituisce le coppie grezze (non ancora decodificate).
func splitQSL(qs string, keepBlankValues bool, maxNumFields int) ([]QueryPair, error) {
	if qs == "" {
		return nil, nil
	}
	if maxNumFields > 0 && maxNumFields < 1+strings.Count(qs, "&") {
		return nil, errMaxFields()
	}
	var out []QueryPair
	for _, nv := range strings.Split(qs, "&") {
		if nv == "" {
			continue
		}
		name, value, _ := strings.Cut(nv, "=")
		if value != "" || keepBlankValues {
			out = append(out, QueryPair{name, value})
		}
	}
	return out, nil
}

// ParseQSL replica urllib.parse.parse_qsl(qs, keep_blank_values) su una str
// con encoding="utf-8" ed errors="replace". maxNumFields <= 0 significa
// nessun limite (None).
func ParseQSL(qs string, keepBlankValues bool, maxNumFields int) ([]QueryPair, error) {
	pairs, err := splitQSL(qs, keepBlankValues, maxNumFields)
	if err != nil {
		return nil, err
	}
	for i := range pairs {
		pairs[i] = QueryPair{UnquotePlus(pairs[i].Name), UnquotePlus(pairs[i].Value)}
	}
	return pairs, nil
}

// ParseQS replica urllib.parse.parse_qs(qs, keep_blank_values): come
// ParseQSL, raggruppando i valori per nome.
func ParseQS(qs string, keepBlankValues bool, maxNumFields int) (QueryValues, error) {
	pairs, err := ParseQSL(qs, keepBlankValues, maxNumFields)
	if err != nil {
		return nil, err
	}
	return groupPairs(pairs), nil
}

func groupPairs(pairs []QueryPair) QueryValues {
	var out QueryValues
	index := map[string]int{}
	for _, p := range pairs {
		if i, ok := index[p.Name]; ok {
			out[i].Values = append(out[i].Values, p.Value)
			continue
		}
		index[p.Name] = len(out)
		out = append(out, QueryField{Name: p.Name, Values: []string{p.Value}})
	}
	return out
}

// BytesQueryField è un parametro di parse_qs_bytes: nome (str latin-1) e
// valori come byte.
type BytesQueryField struct {
	Name   string
	Values [][]byte
}

// ParseQSBytes replica tornado.escape.parse_qs_bytes(qs, keep_blank_values,
// max_num_fields): qs sono byte grezzi (decodificati come latin-1), i nomi
// restano str latin-1 e i valori diventano byte.
func ParseQSBytes(qs string, keepBlankValues bool, maxNumFields int) ([]BytesQueryField, error) {
	pairs, err := splitQSL(qs, keepBlankValues, maxNumFields)
	if err != nil {
		return nil, err
	}
	var out []BytesQueryField
	index := map[string]int{}
	for _, p := range pairs {
		name := Latin1(string(UnquotePlusBytes(p.Name)))
		value := UnquotePlusBytes(p.Value)
		if i, ok := index[name]; ok {
			out[i].Values = append(out[i].Values, value)
			continue
		}
		index[name] = len(out)
		out = append(out, BytesQueryField{Name: name, Values: [][]byte{value}})
	}
	return out, nil
}
