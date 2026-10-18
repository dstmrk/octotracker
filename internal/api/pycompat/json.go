package pycompat

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Object è un dict Python: coppie chiave/valore nell'ordine di inserimento.
// È il tipo con cui Dumps serializza gli oggetti JSON (nell'ordine dato) e
// con cui Loads restituisce gli oggetti decodificati.
type Object []Member

// Member è una coppia chiave/valore di Object.
type Member struct {
	Key   string
	Value any
}

// Get restituisce il valore della chiave (d.get(key)) e se è presente.
func (o Object) Get(key string) (any, bool) {
	for _, m := range o {
		if m.Key == key {
			return m.Value, true
		}
	}
	return nil, false
}

// Set imposta d[key] = value: una chiave già presente mantiene la sua
// posizione, una nuova viene aggiunta in fondo.
func (o *Object) Set(key string, value any) {
	for i := range *o {
		if (*o)[i].Key == key {
			(*o)[i].Value = value
			return
		}
	}
	*o = append(*o, Member{key, value})
}

// ========== json.dumps ==========

// Dumps replica json.dumps(v) con le opzioni di default (separatori ", " e
// ": ", ensure_ascii=True, allow_nan=True). Tipi supportati: nil, bool,
// string, float64 (e float32), interi Go e *big.Int, Object, []any e ogni
// slice/array o puntatore a questi tipi (puntatore nil → null). Ogni altro
// tipo restituisce un TypeError come Python.
func Dumps(v any) (string, error) {
	var b strings.Builder
	if err := writeJSON(&b, v, "", 0, false); err != nil {
		return "", err
	}
	return b.String(), nil
}

// DumpsIndent replica json.dumps(v, indent=n): elementi su righe separate
// rientrate di n spazi, separatori "," e ": ".
func DumpsIndent(v any, indent int) (string, error) {
	var b strings.Builder
	if err := writeJSON(&b, v, strings.Repeat(" ", indent), 0, true); err != nil {
		return "", err
	}
	return b.String(), nil
}

// MustDumps è Dumps per valori che sicuramente si possono serializzare:
// panica in caso di errore.
func MustDumps(v any) string {
	s, err := Dumps(v)
	if err != nil {
		panic(err)
	}
	return s
}

func writeContainerStart(b *strings.Builder, open byte, indent string, level int, indented bool) string {
	b.WriteByte(open)
	if !indented {
		return ", "
	}
	nl := "\n" + strings.Repeat(indent, level+1)
	b.WriteString(nl)
	return "," + nl
}

func writeContainerEnd(b *strings.Builder, closeCh byte, indent string, level int, indented bool) {
	if indented {
		b.WriteString("\n" + strings.Repeat(indent, level))
	}
	b.WriteByte(closeCh)
}

func writeJSON(b *strings.Builder, v any, indent string, level int, indented bool) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeJSONString(b, x)
	case float64:
		b.WriteString(jsonFloat(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case *big.Int:
		if x == nil {
			b.WriteString("null")
			return nil
		}
		s, err := FormatInt(x)
		if err != nil {
			return err
		}
		b.WriteString(s)
	case Object:
		if len(x) == 0 {
			b.WriteString("{}")
			return nil
		}
		sep := writeContainerStart(b, '{', indent, level, indented)
		for i, m := range x {
			if i > 0 {
				b.WriteString(sep)
			}
			writeJSONString(b, m.Key)
			b.WriteString(": ")
			if err := writeJSON(b, m.Value, indent, level+1, indented); err != nil {
				return err
			}
		}
		writeContainerEnd(b, '}', indent, level, indented)
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return nil
		}
		sep := writeContainerStart(b, '[', indent, level, indented)
		for i, e := range x {
			if i > 0 {
				b.WriteString(sep)
			}
			if err := writeJSON(b, e, indent, level+1, indented); err != nil {
				return err
			}
		}
		writeContainerEnd(b, ']', indent, level, indented)
	default:
		return writeJSONReflect(b, v, indent, level, indented)
	}
	return nil
}

// writeJSONReflect gestisce puntatori, slice/array tipizzati e gli altri
// tipi numerici.
func writeJSONReflect(b *strings.Builder, v any, indent string, level int, indented bool) error {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			b.WriteString("null")
			return nil
		}
		return writeJSON(b, rv.Elem().Interface(), indent, level, indented)
	case reflect.Int8, reflect.Int16, reflect.Int32:
		b.WriteString(strconv.FormatInt(rv.Int(), 10))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		b.WriteString(strconv.FormatUint(rv.Uint(), 10))
		return nil
	case reflect.Float32:
		b.WriteString(jsonFloat(rv.Float()))
		return nil
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			// una slice nil di Go vale come lista vuota
			return writeJSON(b, []any{}, indent, level, indented)
		}
		items := make([]any, rv.Len())
		for i := range items {
			items[i] = rv.Index(i).Interface()
		}
		return writeJSON(b, items, indent, level, indented)
	}
	return NewError("TypeError", fmt.Sprintf("Object of type %T is not JSON serializable", v))
}

// jsonFloat replica float.__repr__ come lo usa json.dumps (NaN e infiniti
// come "NaN", "Infinity", "-Infinity").
func jsonFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return FloatRepr(f)
}

// writeJSONString replica l'escape di json.dumps con ensure_ascii=True:
// tutto ciò che non è ASCII stampabile diventa \uXXXX (minuscolo, coppie
// surrogate oltre il BMP).
func writeJSONString(b *strings.Builder, s string) {
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
			case r >= 0x20 && r <= 0x7e:
				b.WriteRune(r)
			case r < 0x10000:
				fmt.Fprintf(b, `\u%04x`, r)
			default:
				r1, r2 := utf16.EncodeRune(r)
				fmt.Fprintf(b, `\u%04x\u%04x`, r1, r2)
			}
		}
	}
	b.WriteByte('"')
}

// ========== json.loads ==========

// MaxJSONDepth è la profondità massima di annidamento accettata da
// json.loads di CPython 3.13 prima del RecursionError (misurata: 9998).
const MaxJSONDepth = 9998

type jsonDecoder struct {
	s     []rune
	depth int
}

// stopIteration segnala che in quella posizione non inizia alcun valore
// (StopIteration dello scanner C).
type stopIteration struct{ pos int }

func (e *stopIteration) Error() string { return "StopIteration" }

func (d *jsonDecoder) errAt(msg string, pos int) error {
	doc := d.s[:min(pos, len(d.s))]
	lineno := 1
	last := -1
	for i, r := range doc {
		if r == '\n' {
			lineno++
			last = i
		}
	}
	colno := pos - last
	return NewError("JSONDecodeError", fmt.Sprintf("%s: line %d column %d (char %d)", msg, lineno, colno, pos))
}

func isJSONSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func (d *jsonDecoder) skipSpace(i int) int {
	for i < len(d.s) && isJSONSpace(d.s[i]) {
		i++
	}
	return i
}

func (d *jsonDecoder) hasWord(i int, word string) bool {
	w := []rune(word)
	if i+len(w) > len(d.s) {
		return false
	}
	for k, r := range w {
		if d.s[i+k] != r {
			return false
		}
	}
	return true
}

// Loads replica json.loads(s): oggetti → Object (chiavi duplicate: vince
// l'ultimo valore, nella posizione della prima), array → []any, interi →
// *big.Int, numeri con parte decimale o esponente → float64, NaN /
// Infinity / -Infinity accettati. Gli errori sono JSONDecodeError (un
// ValueError), RecursionError oltre MaxJSONDepth livelli e ValueError per
// interi con più di 4300 cifre.
//
// Differenza nota: un surrogato UTF-16 isolato (\ud800) non è
// rappresentabile in una stringa Go e diventa U+FFFD.
func Loads(s string) (any, error) {
	if strings.HasPrefix(s, "\ufeff") {
		return nil, NewError("JSONDecodeError", "Unexpected UTF-8 BOM (decode using utf-8-sig): line 1 column 1 (char 0)")
	}
	d := &jsonDecoder{s: []rune(s)}
	idx := d.skipSpace(0)
	v, end, err := d.scanOnce(idx)
	if err != nil {
		if si, ok := err.(*stopIteration); ok {
			return nil, d.errAt("Expecting value", si.pos)
		}
		return nil, err
	}
	end = d.skipSpace(end)
	if end != len(d.s) {
		return nil, d.errAt("Extra data", end)
	}
	return v, nil
}

func (d *jsonDecoder) scanOnce(idx int) (any, int, error) {
	if idx >= len(d.s) {
		return nil, 0, &stopIteration{idx}
	}
	switch c := d.s[idx]; {
	case c == '"':
		return d.scanString(idx + 1)
	case c == '{':
		return d.enter(idx, "object", d.parseObject)
	case c == '[':
		return d.enter(idx, "array", d.parseArray)
	case c == 'n' && d.hasWord(idx, "null"):
		return nil, idx + 4, nil
	case c == 't' && d.hasWord(idx, "true"):
		return true, idx + 4, nil
	case c == 'f' && d.hasWord(idx, "false"):
		return false, idx + 5, nil
	case c == 'N' && d.hasWord(idx, "NaN"):
		return math.NaN(), idx + 3, nil
	case c == 'I' && d.hasWord(idx, "Infinity"):
		return math.Inf(1), idx + 8, nil
	case c == '-' && d.hasWord(idx, "-Infinity"):
		return math.Inf(-1), idx + 9, nil
	}
	return d.matchNumber(idx)
}

func (d *jsonDecoder) enter(idx int, kind string, parse func(int) (any, int, error)) (any, int, error) {
	d.depth++
	defer func() { d.depth-- }()
	if d.depth > MaxJSONDepth {
		return nil, 0, NewError("RecursionError",
			"maximum recursion depth exceeded while decoding a JSON "+kind+" from a unicode string")
	}
	return parse(idx + 1)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// matchNumber replica _match_number_unicode dello scanner C.
func (d *jsonDecoder) matchNumber(start int) (any, int, error) {
	s, n := d.s, len(d.s)
	idx := start
	if s[idx] == '-' {
		idx++
		if idx >= n {
			return nil, 0, &stopIteration{start}
		}
	}
	switch {
	case s[idx] >= '1' && s[idx] <= '9':
		idx++
		for idx < n && isDigit(s[idx]) {
			idx++
		}
	case s[idx] == '0':
		idx++
	default:
		return nil, 0, &stopIteration{start}
	}
	isFloat := false
	if idx+1 < n && s[idx] == '.' && isDigit(s[idx+1]) {
		isFloat = true
		idx += 2
		for idx < n && isDigit(s[idx]) {
			idx++
		}
	}
	if idx+1 < n && (s[idx] == 'e' || s[idx] == 'E') {
		eStart := idx
		idx++
		if idx+1 < n && (s[idx] == '-' || s[idx] == '+') {
			idx++
		}
		for idx < n && isDigit(s[idx]) {
			idx++
		}
		if isDigit(s[idx-1]) {
			isFloat = true
		} else {
			idx = eStart
		}
	}
	lit := string(s[start:idx])
	if isFloat {
		f, _ := strconv.ParseFloat(lit, 64) // overflow → ±Inf come float()
		return f, idx, nil
	}
	v, err := ParseInt(lit)
	if err != nil {
		return nil, 0, err
	}
	return v, idx, nil
}

func (d *jsonDecoder) parseObject(idx int) (any, int, error) {
	s, n := d.s, len(d.s)
	obj := Object{}
	idx = d.skipSpace(idx)
	if idx < n && s[idx] == '}' {
		return obj, idx + 1, nil
	}
	for {
		if idx >= n || s[idx] != '"' {
			return nil, 0, d.errAt("Expecting property name enclosed in double quotes", idx)
		}
		key, next, err := d.scanString(idx + 1)
		if err != nil {
			return nil, 0, err
		}
		idx = d.skipSpace(next)
		if idx >= n || s[idx] != ':' {
			return nil, 0, d.errAt("Expecting ':' delimiter", idx)
		}
		idx = d.skipSpace(idx + 1)
		val, next, err := d.scanOnce(idx)
		if err != nil {
			if si, ok := err.(*stopIteration); ok {
				return nil, 0, d.errAt("Expecting value", si.pos)
			}
			return nil, 0, err
		}
		obj.Set(key.(string), val)
		idx = d.skipSpace(next)
		if idx < n && s[idx] == '}' {
			return obj, idx + 1, nil
		}
		if idx >= n || s[idx] != ',' {
			return nil, 0, d.errAt("Expecting ',' delimiter", idx)
		}
		comma := idx
		idx = d.skipSpace(idx + 1)
		if idx < n && s[idx] == '}' {
			return nil, 0, d.errAt("Illegal trailing comma before end of object", comma)
		}
	}
}

func (d *jsonDecoder) parseArray(idx int) (any, int, error) {
	s, n := d.s, len(d.s)
	arr := []any{}
	idx = d.skipSpace(idx)
	if idx < n && s[idx] == ']' {
		return arr, idx + 1, nil
	}
	for {
		val, next, err := d.scanOnce(idx)
		if err != nil {
			if si, ok := err.(*stopIteration); ok {
				return nil, 0, d.errAt("Expecting value", si.pos)
			}
			return nil, 0, err
		}
		arr = append(arr, val)
		idx = d.skipSpace(next)
		if idx < n && s[idx] == ']' {
			return arr, idx + 1, nil
		}
		if idx >= n || s[idx] != ',' {
			return nil, 0, d.errAt("Expecting ',' delimiter", idx)
		}
		comma := idx
		idx = d.skipSpace(idx + 1)
		if idx < n && s[idx] == ']' {
			return nil, 0, d.errAt("Illegal trailing comma before end of array", comma)
		}
	}
}

func hexValue(r rune) (rune, bool) {
	switch {
	case r >= '0' && r <= '9':
		return r - '0', true
	case r >= 'a' && r <= 'f':
		return r - 'a' + 10, true
	case r >= 'A' && r <= 'F':
		return r - 'A' + 10, true
	}
	return 0, false
}

// scanString replica scanstring_unicode (strict=True) a partire dal
// carattere dopo l'apice d'apertura.
func (d *jsonDecoder) scanString(end int) (any, int, error) {
	s, n := d.s, len(d.s)
	begin := end - 1
	var b strings.Builder
	for {
		next := end
		var c rune
		for ; next < n; next++ {
			c = s[next]
			if c == '"' || c == '\\' {
				break
			}
			if c <= 0x1f {
				return nil, 0, d.errAt("Invalid control character at", next)
			}
		}
		if next >= n {
			return nil, 0, d.errAt("Unterminated string starting at", begin)
		}
		b.WriteString(string(s[end:next]))
		next++
		if c == '"' {
			return b.String(), next, nil
		}
		if next >= n {
			return nil, 0, d.errAt("Unterminated string starting at", begin)
		}
		c = s[next]
		if c != 'u' {
			end = next + 1
			switch c {
			case '"', '\\', '/':
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			default:
				return nil, 0, d.errAt("Invalid \\escape", end-2)
			}
			b.WriteRune(c)
			continue
		}
		next++
		end = next + 4
		if end >= n {
			return nil, 0, d.errAt("Invalid \\uXXXX escape", next-1)
		}
		c = 0
		for ; next < end; next++ {
			h, ok := hexValue(s[next])
			if !ok {
				return nil, 0, d.errAt("Invalid \\uXXXX escape", end-5)
			}
			c = c<<4 | h
		}
		if c >= 0xd800 && c <= 0xdbff && end+6 < n && s[next] == '\\' && s[next+1] == 'u' {
			next += 2
			end += 6
			var c2 rune
			for ; next < end; next++ {
				h, ok := hexValue(s[next])
				if !ok {
					return nil, 0, d.errAt("Invalid \\uXXXX escape", end-5)
				}
				c2 = c2<<4 | h
			}
			if c2 >= 0xdc00 && c2 <= 0xdfff {
				c = utf16.DecodeRune(c, c2)
			} else {
				end -= 6
			}
		}
		b.WriteRune(c) // un surrogato isolato diventa U+FFFD
	}
}
