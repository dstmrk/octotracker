package database

// Helper che replicano il comportamento di Python usato da database.py:
// json.dumps/json.loads (formato dei blob JSON nel DB), repr()/str() dei
// valori (messaggi di log), uguaglianza tra dict, round(x, n), sum(), min()
// e max() su liste di float.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

var errNotObject = errors.New("il JSON non è un oggetto")

// odict è un dict Python con ordine di inserimento delle chiavi.
type odict []okv

type okv struct {
	k string
	v any
}

// keyPriority fissa l'ordine delle chiavi note quando si serializza una map
// Go (non ordinata): è l'ordine di inserimento usato dal codice Python
// (checker._build_current_octopus_rates e _build_pending_rates). Le chiavi
// sconosciute seguono in ordine alfabetico.
var keyPriority = map[string]int{
	"luce":                0,
	"gas":                 1,
	"tipo":                2,
	"fascia":              3,
	"consumo_f1":          4,
	"consumo_f2":          5,
	"consumo_f3":          6,
	"consumo_annuo":       7,
	"energia":             8,
	"commercializzazione": 9,
	"updated_services":    10,
}

func orderedKeys(keys []string) []string {
	sort.Slice(keys, func(i, j int) bool {
		pi, iok := keyPriority[keys[i]]
		pj, jok := keyPriority[keys[j]]
		switch {
		case iok && jok:
			return pi < pj
		case iok != jok:
			return iok
		default:
			return keys[i] < keys[j]
		}
	})
	return keys
}

// ========== json.dumps ==========

// pyDumps replica json.dumps(v) con le opzioni di default di Python
// (separatori ", " e ": ", ensure_ascii=True, allow_nan=True).
func pyDumps(v any) (string, error) {
	var b strings.Builder
	if err := writePyJSON(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writePyJSON(b *strings.Builder, v any) error {
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
		writePyJSONString(b, x)
	case float64:
		b.WriteString(pyFloatRepr(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case *big.Int:
		b.WriteString(x.String())
	case json.Number:
		b.WriteString(pyNumberRepr(pyNumber(string(x))))
	case odict:
		b.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyJSONString(b, kv.k)
			b.WriteString(": ")
			if err := writePyJSON(b, kv.v); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case LastNotifiedRates:
		return writePyJSON(b, map[string]any(x))
	case map[string]any:
		return writePyJSON(b, mapToODict(x))
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := writePyJSON(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		return writePyJSONReflect(b, v)
	}
	return nil
}

// writePyJSONReflect gestisce gli altri tipi Go (slice e map tipizzate,
// puntatori, interi/float di altre dimensioni).
func writePyJSONReflect(b *strings.Builder, v any) error {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			b.WriteString("null")
			return nil
		}
		return writePyJSON(b, rv.Elem().Interface())
	case reflect.Int8, reflect.Int16, reflect.Int32:
		b.WriteString(strconv.FormatInt(rv.Int(), 10))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		b.WriteString(strconv.FormatUint(rv.Uint(), 10))
		return nil
	case reflect.Float32:
		b.WriteString(pyFloatRepr(rv.Float()))
		return nil
	case reflect.Slice, reflect.Array:
		items := make([]any, rv.Len())
		for i := range items {
			items[i] = rv.Index(i).Interface()
		}
		return writePyJSON(b, items)
	case reflect.Map:
		if rv.Type().Key().Kind() == reflect.String {
			m := make(map[string]any, rv.Len())
			iter := rv.MapRange()
			for iter.Next() {
				m[iter.Key().String()] = iter.Value().Interface()
			}
			return writePyJSON(b, m)
		}
	}
	//lint:ignore ST1005 stesso messaggio del TypeError di json.dumps in Python
	return fmt.Errorf("Object of type %T is not JSON serializable", v)
}

func mapToODict(m map[string]any) odict {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	d := make(odict, 0, len(m))
	for _, k := range orderedKeys(keys) {
		d = append(d, okv{k, m[k]})
	}
	return d
}

// writePyJSONString replica l'escape di json.dumps con ensure_ascii=True.
func writePyJSONString(b *strings.Builder, s string) {
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

// pyFloatRepr replica float.__repr__ come usato da json.dumps (NaN e
// infiniti come "NaN", "Infinity", "-Infinity").
func pyFloatRepr(f float64) string {
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

func pyNumberRepr(n any) string {
	switch x := n.(type) {
	case float64:
		return pyFloatRepr(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return fmt.Sprint(x)
	}
}

// ========== json.loads ==========

// pyLoads replica json.loads: oggetti → map[string]any, array → []any,
// numeri interi → int64 (*big.Int se fuori range), numeri con parte
// decimale o esponente → float64.
func pyLoads(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		//lint:ignore ST1005 stesso messaggio del JSONDecodeError di json.loads in Python
		return nil, errors.New("Extra data")
	}
	return convertJSONNumbers(v), nil
}

func convertJSONNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		return pyNumber(string(x))
	case map[string]any:
		for k, e := range x {
			x[k] = convertJSONNumbers(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = convertJSONNumbers(e)
		}
		return x
	}
	return v
}

// pyNumber converte un letterale numerico JSON come il decoder Python:
// int se non ha parte frazionaria né esponente, float altrimenti.
func pyNumber(lit string) any {
	if strings.ContainsAny(lit, ".eE") {
		f, _ := strconv.ParseFloat(lit, 64) // overflow → ±Inf come float() di Python
		return f
	}
	if i, err := strconv.ParseInt(lit, 10, 64); err == nil {
		return i
	}
	n, ok := new(big.Int).SetString(lit, 10)
	if !ok {
		return lit
	}
	return n
}

// ========== Uguaglianza tra valori Python ==========

// pyNum è un numero Python: intero (anche bool) o float.
type pyNum struct {
	isFloat bool
	f       float64
	i       *big.Int
}

func toPyNum(v any) (pyNum, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return pyNum{i: big.NewInt(1)}, true
		}
		return pyNum{i: big.NewInt(0)}, true
	case float64:
		return pyNum{isFloat: true, f: x}, true
	case float32:
		return pyNum{isFloat: true, f: float64(x)}, true
	case int:
		return pyNum{i: big.NewInt(int64(x))}, true
	case int64:
		return pyNum{i: big.NewInt(x)}, true
	case *big.Int:
		return pyNum{i: x}, true
	case json.Number:
		return toPyNum(pyNumber(string(x)))
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32:
		return pyNum{i: big.NewInt(rv.Int())}, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return pyNum{i: new(big.Int).SetUint64(rv.Uint())}, true
	}
	return pyNum{}, false
}

func pyNumEqual(a, b pyNum) bool {
	switch {
	case a.isFloat && b.isFloat:
		return a.f == b.f
	case !a.isFloat && !b.isFloat:
		return a.i.Cmp(b.i) == 0
	case a.isFloat:
		return floatEqualsInt(a.f, b.i)
	default:
		return floatEqualsInt(b.f, a.i)
	}
}

// floatEqualsInt confronta in modo esatto come Python (int == float).
func floatEqualsInt(f float64, i *big.Int) bool {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return false
	}
	fi, _ := new(big.Float).SetFloat64(f).Int(nil)
	return fi.Cmp(i) == 0
}

func asStringMap(v any) (map[string]any, bool) {
	switch x := v.(type) {
	case map[string]any:
		return x, true
	case LastNotifiedRates:
		return x, true
	case odict:
		m := make(map[string]any, len(x))
		for _, kv := range x {
			m[kv.k] = kv.v
		}
		return m, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		m := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			m[iter.Key().String()] = iter.Value().Interface()
		}
		return m, true
	}
	return nil, false
}

func asList(v any) ([]any, bool) {
	if x, ok := v.([]any); ok {
		return x, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = rv.Index(i).Interface()
		}
		return out, true
	}
	return nil, false
}

// pyEqual replica l'operatore == di Python tra valori JSON (dict, list,
// str, numeri, bool, None): 72 == 72.0, True == 1, chiave assente ≠ None.
func pyEqual(a, b any) bool {
	if na, ok := toPyNum(a); ok {
		nb, ok := toPyNum(b)
		return ok && pyNumEqual(na, nb)
	}
	if _, ok := toPyNum(b); ok {
		return false
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if sa, ok := a.(string); ok {
		sb, ok := b.(string)
		return ok && sa == sb
	}
	if ma, ok := asStringMap(a); ok {
		mb, ok := asStringMap(b)
		if !ok || len(ma) != len(mb) {
			return false
		}
		for k, va := range ma {
			vb, ok := mb[k]
			if !ok || !pyEqual(va, vb) {
				return false
			}
		}
		return true
	}
	if la, ok := asList(a); ok {
		lb, ok := asList(b)
		if !ok || len(la) != len(lb) {
			return false
		}
		for i := range la {
			if !pyEqual(la[i], lb[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

// ========== Accesso a dict Python (per i blob JSON) ==========

// pyKeyError replica KeyError: str(e) è il repr della chiave.
type pyKeyError string

func (e pyKeyError) Error() string { return pyRepr(string(e)) }

// pyTypeError rappresenta un TypeError/AttributeError Python (accesso a un
// valore che non è un dict, `in` su un valore non iterabile, ...).
type pyTypeError string

func (e pyTypeError) Error() string { return string(e) }

func pyTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case float64:
		return "float"
	case int64, *big.Int:
		return "int"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	}
	return fmt.Sprintf("%T", v)
}

// pyGetItem replica d[key] su un dict.
func pyGetItem(d any, key string) (any, error) {
	m, ok := d.(map[string]any)
	if !ok {
		return nil, pyTypeError(fmt.Sprintf("'%s' object is not subscriptable", pyTypeName(d)))
	}
	v, ok := m[key]
	if !ok {
		return nil, pyKeyError(key)
	}
	return v, nil
}

// pyDictGet replica d.get(key, def) su un dict.
func pyDictGet(d any, key string, def any) (any, error) {
	m, ok := d.(map[string]any)
	if !ok {
		return nil, pyTypeError(fmt.Sprintf("'%s' object has no attribute 'get'", pyTypeName(d)))
	}
	if v, ok := m[key]; ok {
		return v, nil
	}
	return def, nil
}

// pyContains replica `item in container` per una stringa item.
func pyContains(container any, item string) (bool, error) {
	switch c := container.(type) {
	case []any:
		for _, e := range c {
			if pyEqual(e, item) {
				return true, nil
			}
		}
		return false, nil
	case string:
		return strings.Contains(c, item), nil
	case map[string]any:
		_, ok := c[item]
		return ok, nil
	}
	return false, pyTypeError(fmt.Sprintf("argument of type '%s' is not iterable", pyTypeName(container)))
}

// ========== repr() / str() per i messaggi di log ==========

// pyRepr replica repr() di Python per i valori usati nei log.
func pyRepr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return pyStrRepr(x)
	case float64:
		switch {
		case math.IsNaN(x):
			return "nan"
		case math.IsInf(x, 1):
			return "inf"
		case math.IsInf(x, -1):
			return "-inf"
		}
		return pyFloatReprFinite(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case *big.Int:
		return x.String()
	case odict:
		parts := make([]string, len(x))
		for i, kv := range x {
			parts[i] = pyStrRepr(kv.k) + ": " + pyRepr(kv.v)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]any:
		return pyRepr(mapToODict(x))
	case LastNotifiedRates:
		return pyRepr(mapToODict(x))
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = pyRepr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = pyStrRepr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(v)
}

// pyStr replica str(): le stringhe restano invariate, il resto è repr().
func pyStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return pyRepr(v)
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

// pySetRepr formatta un set Python di stringhe (in ordine alfabetico: in
// Python l'ordine dipende dall'hash randomizzato).
func pySetRepr(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = pyStrRepr(k)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// ========== Aritmetica float ==========

// pyRound replica round(x, ndigits) di Python per ndigits >= 0:
// arrotondamento corretto del valore binario esatto, half-even sui pareggi.
func pyRound(x float64, ndigits int) float64 {
	if x == 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', ndigits, 64), 64)
	return r
}

// pySum replica sum() di Python 3.12+ su una lista non vuota di float:
// 0 + xs[0], poi somma compensata di Neumaier sui restanti elementi.
func pySum(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	f := 0 + xs[0]
	c := 0.0
	for _, x := range xs[1:] {
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	if c != 0 && !math.IsInf(c, 0) && !math.IsNaN(c) {
		f += c
	}
	return f
}

// pyMin replica min() di Python su una lista non vuota (primo minimo).
func pyMin(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

// pyMax replica max() di Python su una lista non vuota (primo massimo).
func pyMax(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}
