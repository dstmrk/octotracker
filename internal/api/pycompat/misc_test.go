package pycompat

import (
	"math/big"
	"strings"
	"testing"
)

func TestObjectGetSet(t *testing.T) {
	o := Object{}
	o.Set("a", 1)
	o.Set("b", 2)
	o.Set("a", 3) // chiave esistente: resta in prima posizione
	if len(o) != 2 || o[0].Key != "a" || o[0].Value != 3 {
		t.Fatalf("Set: %#v", o)
	}
	if v, ok := o.Get("b"); !ok || v != 2 {
		t.Errorf("Get(b) = %v, %v", v, ok)
	}
	if _, ok := o.Get("z"); ok {
		t.Error("Get(z) trovato")
	}
}

func TestDumpsTypes(t *testing.T) {
	f := 0.5
	s := "x"
	var nilPtr *float64
	var nilBig *big.Int
	cases := []struct {
		in   any
		want string
	}{
		{&f, "0.5"},
		{&s, `"x"`},
		{nilPtr, "null"},
		{nilBig, "null"},
		{[]string{"a", "b"}, `["a", "b"]`},
		{[]*float64{&f, nil}, "[0.5, null]"},
		{[]float64(nil), "[]"},
		{[2]int{1, 2}, "[1, 2]"},
		{int8(-3), "-3"},
		{uint16(7), "7"},
		{float32(0.5), "0.5"},
		{int64(-9), "-9"},
		{Object{{"k", []any{}}}, `{"k": []}`},
	}
	for _, c := range cases {
		if got, err := Dumps(c.in); err != nil || got != c.want {
			t.Errorf("Dumps(%#v) = %q, %v; atteso %q", c.in, got, err, c.want)
		}
	}
	_, err := Dumps(map[string]any{"a": 1})
	if !IsInstance(err, "TypeError") || err.Error() != "Object of type map[string]interface {} is not JSON serializable" {
		t.Errorf("Dumps(map) errore %v", err)
	}
	if _, err := DumpsIndent([]any{struct{}{}}, 2); err == nil {
		t.Error("DumpsIndent(struct) senza errore")
	}
	if _, err := Dumps(Object{{"a", struct{}{}}}); err == nil {
		t.Error("Dumps(Object con struct) senza errore")
	}
	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(5000), nil)
	if _, err := Dumps(huge); err == nil {
		t.Error("Dumps(10**5000) senza errore")
	}
	nanInf, _ := Loads("[NaN, Infinity, -Infinity]")
	if got := MustDumps(nanInf); got != "[NaN, Infinity, -Infinity]" {
		t.Errorf("Dumps(nan/inf) = %q", got)
	}
	if _, err := Loads("[1,\n 2,\n x]"); err == nil || err.Error() != "Expecting value: line 3 column 2 (char 9)" {
		t.Errorf("errore multilinea: %v", err)
	}
	if MustDumps(Object{{"success", true}}) != `{"success": true}` {
		t.Error("MustDumps")
	}
	defer func() {
		if recover() == nil {
			t.Error("MustDumps non ha panicato")
		}
	}()
	MustDumps(struct{}{})
}

func TestReprMisc(t *testing.T) {
	if Repr(nil) != "None" || Repr(true) != "True" || Repr(false) != "False" || Repr(5) != "5" || Repr(int64(-5)) != "-5" {
		t.Error("Repr scalari")
	}
	if Repr(struct{ A int }{1}) != "{1}" {
		t.Errorf("Repr fallback = %q", Repr(struct{ A int }{1}))
	}
	if Str(1.0) != "1.0" {
		t.Errorf("Str(1.0) = %q", Str(1.0))
	}
	if SetRepr(nil) != "set()" || SetRepr([]string{"luce", "gas"}) != "{'gas', 'luce'}" {
		t.Error("SetRepr")
	}
	if Strip("\x1c  a b 　\t") != "a b" {
		t.Errorf("Strip = %q", Strip("\x1c  a b 　\t"))
	}
}

func TestLatin1RoundTrip(t *testing.T) {
	raw := string([]byte{'a', 0xc3, 0xa9, 0xff})
	l := Latin1(raw)
	if l != "aÃ©ÿ" {
		t.Errorf("Latin1 = %q", l)
	}
	if got := string(EncodeLatin1(l + "€")); got != raw {
		t.Errorf("EncodeLatin1 = %q", got)
	}
	if NewError("ValueError", "x").Error() != "x" {
		t.Error("NewError")
	}
	if !strings.Contains(Repr([]byte("a'")), `"a'"`) {
		t.Error("BytesRepr quote")
	}
}
