package pycompat

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// golden contiene gli output di CPython 3.13 (e Tornado 6.5) generati con
// .venv/bin/python da uno script fuori dal repository.
type golden struct {
	Dumps []struct {
		In      tagged `json:"in"`
		Dumps   string `json:"dumps"`
		Indent2 string `json:"indent2"`
	} `json:"dumps"`
	FloatRepr []struct {
		In   string `json:"in"`
		Repr string `json:"repr"`
	} `json:"float_repr"`
	Loads []struct {
		In   string  `json:"in"`
		OK   *tagged `json:"ok"`
		Str  string  `json:"str"`
		Repr string  `json:"repr"`
		Err  string  `json:"err"`
		Msg  string  `json:"msg"`
	} `json:"loads"`
	LoadsDepth []struct {
		Kind  string  `json:"kind"`
		Depth int     `json:"depth"`
		Err   *string `json:"err"`
		Msg   *string `json:"msg"`
	} `json:"loads_depth"`
	StrRepr []struct {
		In   string `json:"in"`
		Repr string `json:"repr"`
	} `json:"str_repr"`
	BytesRepr []struct {
		In   string `json:"in"`
		Repr string `json:"repr"`
	} `json:"bytes_repr"`
	Int []struct {
		In  string  `json:"in"`
		OK  *string `json:"ok"`
		Err string  `json:"err"`
	} `json:"int"`
	IntStrLimit struct {
		DigitsOK int    `json:"digits_ok"`
		Err4301  string `json:"err_4301"`
	} `json:"int_str_limit"`
	UTF8 []struct {
		In      string  `json:"in"`
		Replace string  `json:"replace"`
		Strict  *string `json:"strict"`
		Err     string  `json:"err"`
	} `json:"utf8"`
	Query []struct {
		In          string         `json:"in"`
		Unquote     string         `json:"unquote"`
		UnquotePlus string         `json:"unquote_plus"`
		QSLBlank    [][2]string    `json:"qsl_blank"`
		QSL         [][2]string    `json:"qsl"`
		QSBlank     []qsField      `json:"qs_blank"`
		RawHex      string         `json:"raw_hex"`
		QSBytes     []qsBytesField `json:"qs_bytes"`
	} `json:"query"`
	MaxFields       string `json:"max_fields"`
	MaxFieldsOK1000 int    `json:"max_fields_ok_1000"`
	Strptime        []struct {
		In  string  `json:"in"`
		OK  *string `json:"ok"`
		Err string  `json:"err"`
	} `json:"strptime"`
	ISOFormat []struct {
		In  [7]int `json:"in"`
		Out string `json:"out"`
	} `json:"isoformat"`
	TimedeltaDays []struct {
		A    [7]int `json:"a"`
		B    [3]int `json:"b"`
		Days int    `json:"days"`
	} `json:"timedelta_days"`
	IsSpace []int `json:"isspace"`
}

// qsField è una voce [nome, [valori]] di parse_qs.
type qsField struct {
	Name   string
	Values []string
}

func (f *qsField) UnmarshalJSON(b []byte) error {
	var raw [2]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[0], &f.Name); err != nil {
		return err
	}
	return json.Unmarshal(raw[1], &f.Values)
}

// qsBytesField è una voce [nome, [valori hex]] di parse_qs_bytes.
type qsBytesField = qsField

// tagged è un valore Python serializzato con il suo tipo.
type tagged struct {
	T string          `json:"t"`
	V json.RawMessage `json:"v"`
}

func parseFloatTag(s string) float64 {
	switch s {
	case "inf":
		return math.Inf(1)
	case "-inf":
		return math.Inf(-1)
	case "nan":
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(err)
	}
	return f
}

// value ricostruisce il valore Go corrispondente (come lo produrrebbe Loads).
func (t tagged) value() any {
	switch t.T {
	case "null":
		return nil
	case "bool":
		var b bool
		mustUnmarshal(t.V, &b)
		return b
	case "int":
		var s string
		mustUnmarshal(t.V, &s)
		n, _ := new(big.Int).SetString(s, 10)
		return n
	case "float":
		var s string
		mustUnmarshal(t.V, &s)
		return parseFloatTag(s)
	case "str":
		var s string
		mustUnmarshal(t.V, &s)
		return s
	case "list":
		var items []tagged
		mustUnmarshal(t.V, &items)
		out := []any{}
		for _, it := range items {
			out = append(out, it.value())
		}
		return out
	case "dict":
		var items [][2]json.RawMessage
		mustUnmarshal(t.V, &items)
		out := Object{}
		for _, it := range items {
			var k string
			var v tagged
			mustUnmarshal(it[0], &k)
			mustUnmarshal(it[1], &v)
			out = append(out, Member{k, v.value()})
		}
		return out
	}
	panic("tag sconosciuto " + t.T)
}

func mustUnmarshal(b []byte, v any) {
	if err := json.Unmarshal(b, v); err != nil {
		panic(err)
	}
}

// deepEqual confronta due valori come li produce Loads (NaN uguale a NaN,
// -0.0 distinto da 0.0 tramite il bit di segno).
func deepEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(x) {
			return math.IsNaN(y)
		}
		return x == y && math.Signbit(x) == math.Signbit(y)
	case *big.Int:
		y, ok := b.(*big.Int)
		return ok && x.Cmp(y) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !deepEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case Object:
		y, ok := b.(Object)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i].Key != y[i].Key || !deepEqual(x[i].Value, y[i].Value) {
				return false
			}
		}
		return true
	}
	return false
}

func loadGolden(t *testing.T) *golden {
	t.Helper()
	data, err := os.ReadFile("testdata/pycompat_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

func TestGoldenDumps(t *testing.T) {
	g := loadGolden(t)
	if len(g.Dumps) == 0 {
		t.Fatal("nessun caso")
	}
	for i, c := range g.Dumps {
		v := c.In.value()
		got, err := Dumps(v)
		if err != nil || got != c.Dumps {
			t.Errorf("#%d Dumps = %q, %v; atteso %q", i, got, err, c.Dumps)
		}
		got, err = DumpsIndent(v, 2)
		if err != nil || got != c.Indent2 {
			t.Errorf("#%d DumpsIndent = %q, %v; atteso %q", i, got, err, c.Indent2)
		}
	}
}

func TestGoldenFloatRepr(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.FloatRepr {
		if got := FloatRepr(parseFloatTag(c.In)); got != c.Repr {
			t.Errorf("FloatRepr(%s) = %q; atteso %q", c.In, got, c.Repr)
		}
	}
}

func TestGoldenLoads(t *testing.T) {
	g := loadGolden(t)
	for i, c := range g.Loads {
		v, err := Loads(c.In)
		if c.OK != nil {
			if err != nil {
				t.Errorf("#%d Loads(%q) errore %v", i, c.In, err)
				continue
			}
			if want := c.OK.value(); !deepEqual(v, want) {
				t.Errorf("#%d Loads(%q) = %#v; atteso %#v", i, c.In, v, want)
			}
			if got := Str(v); got != c.Str {
				t.Errorf("#%d str = %q; atteso %q", i, got, c.Str)
			}
			if got := Repr(v); got != c.Repr {
				t.Errorf("#%d repr = %q; atteso %q", i, got, c.Repr)
			}
			continue
		}
		if err == nil {
			t.Errorf("#%d Loads(%q) = %v; atteso %s", i, c.In, v, c.Err)
			continue
		}
		if !IsInstance(err, c.Err) || err.Error() != c.Msg {
			t.Errorf("#%d Loads(%q) errore %T %q; atteso %s %q", i, c.In, err, err.Error(), c.Err, c.Msg)
		}
	}
}

func TestGoldenLoadsDepth(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.LoadsDepth {
		var s string
		if c.Kind == "array" {
			s = strings.Repeat("[", c.Depth) + strings.Repeat("]", c.Depth)
		} else {
			s = strings.Repeat(`{"a":`, c.Depth) + "1" + strings.Repeat("}", c.Depth)
		}
		_, err := Loads(s)
		if c.Err == nil {
			if err != nil {
				t.Errorf("%s %d: errore %v", c.Kind, c.Depth, err)
			}
			continue
		}
		if !IsInstance(err, *c.Err) || err.Error() != *c.Msg {
			t.Errorf("%s %d: errore %v; atteso %s %q", c.Kind, c.Depth, err, *c.Err, *c.Msg)
		}
	}
}

func TestGoldenReprs(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.StrRepr {
		if got := StrRepr(c.In); got != c.Repr {
			t.Errorf("StrRepr(%q) = %q; atteso %q", c.In, got, c.Repr)
		}
		if got := Repr(c.In); got != c.Repr {
			t.Errorf("Repr(%q) = %q; atteso %q", c.In, got, c.Repr)
		}
		if got := Str(c.In); got != c.In {
			t.Errorf("Str(%q) = %q", c.In, got)
		}
	}
	for _, c := range g.BytesRepr {
		b, _ := hex.DecodeString(c.In)
		if got := BytesRepr(b); got != c.Repr {
			t.Errorf("BytesRepr(%x) = %q; atteso %q", b, got, c.Repr)
		}
		if got := Repr(b); got != c.Repr {
			t.Errorf("Repr(%x) = %q; atteso %q", b, got, c.Repr)
		}
	}
}

func TestGoldenInt(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Int {
		v, err := ParseInt(c.In)
		if c.OK != nil {
			if err != nil || v.String() != *c.OK {
				t.Errorf("ParseInt(%q) = %v, %v; atteso %s", c.In, v, err, *c.OK)
			}
			continue
		}
		if err == nil || !IsInstance(err, "ValueError") || err.Error() != c.Err {
			t.Errorf("ParseInt(%q) errore %v; atteso %q", c.In, err, c.Err)
		}
	}
	ok := new(big.Int).Exp(big.NewInt(10), big.NewInt(4299), nil)
	if s, err := FormatInt(ok); err != nil || len(s) != g.IntStrLimit.DigitsOK {
		t.Errorf("FormatInt(10**4299) = %d cifre, %v; atteso %d", len(s), err, g.IntStrLimit.DigitsOK)
	}
	tooBig := new(big.Int).Exp(big.NewInt(10), big.NewInt(4300), nil)
	if _, err := FormatInt(tooBig); err == nil || err.Error() != g.IntStrLimit.Err4301 {
		t.Errorf("FormatInt(10**4300) errore %v; atteso %q", err, g.IntStrLimit.Err4301)
	}
	if _, err := FormatInt(new(big.Int).Neg(tooBig)); err == nil {
		t.Error("FormatInt(-10**4300) senza errore")
	}
}

func TestGoldenUTF8(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.UTF8 {
		b, _ := hex.DecodeString(c.In)
		if got := DecodeUTF8Replace(b); got != c.Replace {
			t.Errorf("DecodeUTF8Replace(%x) = %q; atteso %q", b, got, c.Replace)
		}
		got, err := DecodeUTF8(b)
		if c.Strict != nil {
			if err != nil || got != *c.Strict {
				t.Errorf("DecodeUTF8(%x) = %q, %v", b, got, err)
			}
			continue
		}
		if err == nil || !IsInstance(err, "UnicodeDecodeError") || !IsInstance(err, "ValueError") || err.Error() != c.Err {
			t.Errorf("DecodeUTF8(%x) errore %v; atteso %q", b, err, c.Err)
		}
	}
}

func pairsOf(ps []QueryPair) [][2]string {
	out := [][2]string{}
	for _, p := range ps {
		out = append(out, [2]string{p.Name, p.Value})
	}
	return out
}

func equalPairs(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGoldenQuery(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Query {
		if got := Unquote(c.In); got != c.Unquote {
			t.Errorf("Unquote(%q) = %q; atteso %q", c.In, got, c.Unquote)
		}
		if got := UnquotePlus(c.In); got != c.UnquotePlus {
			t.Errorf("UnquotePlus(%q) = %q; atteso %q", c.In, got, c.UnquotePlus)
		}
		ps, err := ParseQSL(c.In, true, 0)
		if err != nil || !equalPairs(pairsOf(ps), c.QSLBlank) {
			t.Errorf("ParseQSL(%q, blank) = %v; atteso %v", c.In, pairsOf(ps), c.QSLBlank)
		}
		ps, err = ParseQSL(c.In, false, 0)
		if err != nil || !equalPairs(pairsOf(ps), c.QSL) {
			t.Errorf("ParseQSL(%q) = %v; atteso %v", c.In, pairsOf(ps), c.QSL)
		}
		qs, err := ParseQS(c.In, true, 0)
		if err != nil || len(qs) != len(c.QSBlank) {
			t.Errorf("ParseQS(%q) = %v; atteso %v", c.In, qs, c.QSBlank)
		} else {
			for i, f := range qs {
				if f.Name != c.QSBlank[i].Name || strings.Join(f.Values, "\x00") != strings.Join(c.QSBlank[i].Values, "\x00") {
					t.Errorf("ParseQS(%q)[%d] = %v; atteso %v", c.In, i, f, c.QSBlank[i])
				}
				if got := qs.Get(f.Name); len(got) != len(f.Values) {
					t.Errorf("Get(%q) = %v", f.Name, got)
				}
			}
		}
		raw, _ := hex.DecodeString(c.RawHex)
		bq, err := ParseQSBytes(string(raw), true, 0)
		if err != nil || len(bq) != len(c.QSBytes) {
			t.Errorf("ParseQSBytes(%q) = %v; atteso %v", raw, bq, c.QSBytes)
			continue
		}
		for i, f := range bq {
			want := c.QSBytes[i]
			if f.Name != want.Name || len(f.Values) != len(want.Values) {
				t.Errorf("ParseQSBytes(%q)[%d] = %q; atteso %v", raw, i, f.Name, want)
				continue
			}
			for j, v := range f.Values {
				if hex.EncodeToString(v) != want.Values[j] {
					t.Errorf("ParseQSBytes(%q)[%d][%d] = %x; atteso %s", raw, i, j, v, want.Values[j])
				}
			}
		}
	}
	if got := (QueryValues{}).Get("x"); got != nil {
		t.Errorf("Get su vuoto = %v", got)
	}
	many := strings.Repeat("a=1&", 1000) + "a=1"
	if _, err := ParseQSL(many, true, 1000); err == nil || err.Error() != g.MaxFields || !IsInstance(err, "ValueError") {
		t.Errorf("ParseQSL(1001 campi) errore %v; atteso %q", err, g.MaxFields)
	}
	if _, err := ParseQS(many, true, 1000); err == nil {
		t.Error("ParseQS(1001 campi) senza errore")
	}
	if _, err := ParseQSBytes(many, true, 1000); err == nil {
		t.Error("ParseQSBytes(1001 campi) senza errore")
	}
	ps, err := ParseQSL(strings.Repeat("a=1&", 999)+"a=1", true, 1000)
	if err != nil || len(ps) != g.MaxFieldsOK1000 {
		t.Errorf("ParseQSL(1000 campi) = %d, %v", len(ps), err)
	}
}

func TestGoldenStrptime(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Strptime {
		v, err := StrptimeYMD(c.In)
		if c.OK != nil {
			if err != nil || v.Format("2006-01-02") != *c.OK {
				t.Errorf("StrptimeYMD(%q) = %v, %v; atteso %s", c.In, v, err, *c.OK)
			}
			continue
		}
		if err == nil || !IsInstance(err, "ValueError") || err.Error() != c.Err {
			t.Errorf("StrptimeYMD(%q) errore %v; atteso %q", c.In, err, c.Err)
		}
	}
}

func TestGoldenISOFormatAndTimedelta(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.ISOFormat {
		in := c.In
		tm := time.Date(in[0], time.Month(in[1]), in[2], in[3], in[4], in[5], in[6]*1000, time.Local)
		if got := ISOFormat(tm); got != c.Out {
			t.Errorf("ISOFormat(%v) = %q; atteso %q", in, got, c.Out)
		}
	}
	for _, c := range g.TimedeltaDays {
		a := time.Date(c.A[0], time.Month(c.A[1]), c.A[2], c.A[3], c.A[4], c.A[5], c.A[6]*1000, time.Local)
		b := time.Date(c.B[0], time.Month(c.B[1]), c.B[2], 0, 0, 0, 0, time.UTC)
		if got := TimedeltaDays(a, b); got != c.Days {
			t.Errorf("TimedeltaDays(%v, %v) = %d; atteso %d", c.A, c.B, got, c.Days)
		}
	}
}

func TestGoldenIsSpace(t *testing.T) {
	g := loadGolden(t)
	want := map[rune]bool{}
	for _, c := range g.IsSpace {
		want[rune(c)] = true
	}
	for r := rune(0); r <= 0x10ffff; r++ {
		if IsSpace(r) != want[r] {
			t.Errorf("IsSpace(%U) = %v", r, IsSpace(r))
		}
	}
}
