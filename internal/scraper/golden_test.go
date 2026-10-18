package scraper

// Test differenziali: i golden in testdata/ sono generati eseguendo
// data_reader.py/backfill_rate_history.py reali con .venv/bin/python.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

type pyExc struct {
	Type string `json:"type"`
	Msg  string `json:"msg"`
}

func assertPyException(t *testing.T, err error, want *pyExc) {
	t.Helper()
	var exc *PyException
	if !errors.As(err, &exc) {
		t.Fatalf("errore = %v (%T), atteso %s: %s", err, err, want.Type, want.Msg)
	}
	if exc.Type != want.Type || exc.Msg != want.Msg {
		t.Errorf("eccezione = %s: %q, attesa %s: %q", exc.Type, exc.Msg, want.Type, want.Msg)
	}
}

func TestGoldenParseAreraXML(t *testing.T) {
	var cases []struct {
		Name      string      `json:"name"`
		Service   string      `json:"service"`
		XML       string      `json:"xml"`
		Result    *string     `json:"result"`
		Flat      [][]*string `json:"flat"`
		Exception *pyExc      `json:"exception"`
		Logs      [][2]string `json:"logs"`
	}
	loadGolden(t, "parse_golden.json", &cases)
	if len(cases) < 50 {
		t.Fatalf("golden incompleti: %d casi", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			logs := captureLogs(t)
			res, err := parseAreraXML(c.XML, c.Service)
			if c.Exception != nil {
				assertPyException(t, err, c.Exception)
			} else {
				if err != nil {
					t.Fatalf("errore inatteso: %v", err)
				}
				if got := res.pyValue().pyJSON(); got != *c.Result {
					t.Errorf("risultato:\n got  %s\n want %s", got, *c.Result)
				}
				got := rowsJSON(t, flatRows(flattenRates(res.get("luce"), res.get("gas"))))
				if want := rowsJSON(t, c.Flat); got != want {
					t.Errorf("flatten:\n got  %s\n want %s", got, want)
				}
			}
			assertLogs(t, logs.all(), c.Logs)
		})
	}
}

// treeOf rende l'albero come tree() del generatore: [tag, text, [figli]].
func treeOf(e *element) []any {
	children := []any{}
	for _, c := range e.children {
		children = append(children, treeOf(c))
	}
	var text any
	if e.text != nil {
		text = *e.text
	}
	return []any{e.tag, text, children}
}

func TestGoldenWellFormedness(t *testing.T) {
	var cases []struct {
		Name    string `json:"name"`
		XML     string `json:"xml"`
		Verdict string `json:"verdict"`
		Tree    any    `json:"tree"`
		Type    string `json:"type"`
		Msg     string `json:"msg"`
	}
	loadGolden(t, "wellformed_golden.json", &cases)
	// Divergenze note (documentate in open_issues): encoding/xml converte un
	// riferimento a un surrogato (&#xD800;) in U+FFFD invece di rifiutarlo.
	knownDivergences := map[string]string{"charref-surrogate": "\ufffd"}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			root, err := parseElement(c.XML)
			if text, ok := knownDivergences[c.Name]; ok {
				if err != nil || root.text == nil || *root.text != text {
					t.Fatalf("divergenza nota cambiata: err=%v", err)
				}
				return
			}
			switch c.Verdict {
			case "ok":
				if err != nil {
					t.Fatalf("errore %v, Python accetta il documento", err)
				}
				if got, want := rowsJSON(t, treeOf(root)), rowsJSON(t, c.Tree); got != want {
					t.Errorf("albero:\n got  %s\n want %s", got, want)
				}
			case "parse_error":
				var se *xmlSyntaxError
				if !errors.As(err, &se) {
					t.Fatalf("errore = %v (%T), atteso ParseError (%s)", err, err, c.Msg)
				}
			case "exception":
				assertPyException(t, err, &pyExc{Type: c.Type, Msg: c.Msg})
			default:
				t.Fatalf("verdetto sconosciuto %q", c.Verdict)
			}
		})
	}
}

func TestGoldenPyFloat(t *testing.T) {
	var cases []struct {
		In    *string `json:"in"`
		Repr  string  `json:"repr"`
		JSON  string  `json:"json"`
		Error *pyExc  `json:"error"`
	}
	loadGolden(t, "pyfloat_golden.json", &cases)
	for _, c := range cases {
		v, err := pyFloatOf(c.In)
		name := "None"
		if c.In != nil {
			name = pyStrRepr(*c.In)
		}
		if c.Error != nil {
			var exc *PyException
			if !errors.As(err, &exc) || exc.Type != c.Error.Type || exc.Msg != c.Error.Msg {
				t.Errorf("float(%s): errore = %v, atteso %s: %s", name, err, c.Error.Type, c.Error.Msg)
			}
			continue
		}
		if err != nil {
			t.Errorf("float(%s): errore inatteso %v", name, err)
			continue
		}
		if got := pyFloatRepr(v); got != c.Repr {
			t.Errorf("repr(float(%s)) = %s, atteso %s", name, got, c.Repr)
		}
		if got := pyJSONFloat(v); got != c.JSON {
			t.Errorf("json.dumps(float(%s)) = %s, atteso %s", name, got, c.JSON)
		}
	}
}

func TestGoldenNormalizedCode(t *testing.T) {
	var cases []struct {
		In    *string `json:"in"`
		Out   *string `json:"out"`
		Error *pyExc  `json:"error"`
	}
	loadGolden(t, "normalized_code_golden.json", &cases)
	for _, c := range cases {
		got, err := normalizedCode(c.In)
		name := pyOptStrRepr(c.In)
		if c.Error != nil {
			var exc *PyException
			if !errors.As(err, &exc) || exc.Type != c.Error.Type || exc.Msg != c.Error.Msg {
				t.Errorf("_normalized_code(%.40s): errore = %v, atteso %s: %.80s", name, err, c.Error.Type, c.Error.Msg)
			}
			continue
		}
		if err != nil {
			t.Errorf("_normalized_code(%.40s): errore inatteso %v", name, err)
			continue
		}
		if pyOptStrRepr(got) != pyOptStrRepr(c.Out) {
			t.Errorf("_normalized_code(%.40s) = %s, atteso %s", name, pyOptStrRepr(got), pyOptStrRepr(c.Out))
		}
	}
}

// chunkReader restituisce al massimo n byte per Read.
type chunkReader struct {
	r io.Reader
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

func TestGoldenUTF8Decode(t *testing.T) {
	var cases []struct {
		Hex   string  `json:"hex"`
		Len   int     `json:"len"`
		Head  string  `json:"head"`
		Error *string `json:"error"`
	}
	loadGolden(t, "utf8_golden.json", &cases)
	for _, c := range cases {
		data, err := hex.DecodeString(c.Hex)
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range []int{1, 2, 3, 5, 4096} {
			v := &utf8Validator{r: &chunkReader{r: bytes.NewReader(data), n: chunk}}
			if _, err := io.Copy(io.Discard, v); err != nil {
				t.Fatal(err)
			}
			if c.Error != nil {
				if v.decodeErr != *c.Error {
					t.Errorf("%s (chunk %d): errore %q, atteso %q", c.Hex, chunk, v.decodeErr, *c.Error)
				}
				continue
			}
			if v.decodeErr != "" {
				t.Errorf("%s (chunk %d): errore inatteso %q", c.Hex, chunk, v.decodeErr)
				continue
			}
			if v.chars != c.Len || v.head.String() != c.Head {
				t.Errorf("%s (chunk %d): len %d head %q, attesi %d %q", c.Hex, chunk, v.chars, v.head.String(), c.Len, c.Head)
			}
		}
	}
}

type rateSpec [4]*string // fascia, energia, commercializzazione, cod_offerta

type tariffeSpec struct {
	Luce struct {
		Fissa     []rateSpec `json:"fissa"`
		Variabile []rateSpec `json:"variabile"`
	} `json:"luce"`
	Gas struct {
		Fissa     []rateSpec `json:"fissa"`
		Variabile []rateSpec `json:"variabile"`
	} `json:"gas"`
	DataAggiornamento string  `json:"data_aggiornamento"`
	DataFonteXML      *string `json:"data_fonte_xml"`
}

func buildFasce(t *testing.T, specs []rateSpec) Fasce {
	t.Helper()
	var f Fasce
	for _, s := range specs {
		e, err := ParseFloat(*s[1])
		if err != nil {
			t.Fatal(err)
		}
		r := Rate{Energia: e, CodOfferta: s[3]}
		if s[2] != nil {
			c, err := ParseFloat(*s[2])
			if err != nil {
				t.Fatal(err)
			}
			r.Commercializzazione = &c
		}
		f.set(*s[0], r)
	}
	return f
}

func TestGoldenReprAndJSON(t *testing.T) {
	var cases []struct {
		Spec       tariffeSpec `json:"spec"`
		Repr       string      `json:"repr"`
		JSONIndent string      `json:"json_indent"`
		Flat       [][]*string `json:"flat"`
	}
	loadGolden(t, "repr_golden.json", &cases)
	for i, c := range cases {
		tr := Tariffe{
			Luce:              ServiceRates{Fissa: buildFasce(t, c.Spec.Luce.Fissa), Variabile: buildFasce(t, c.Spec.Luce.Variabile)},
			Gas:               ServiceRates{Fissa: buildFasce(t, c.Spec.Gas.Fissa), Variabile: buildFasce(t, c.Spec.Gas.Variabile)},
			DataAggiornamento: c.Spec.DataAggiornamento,
			DataFonteXML:      c.Spec.DataFonteXML,
		}
		if got := tr.String(); got != c.Repr {
			t.Errorf("caso %d repr:\n got  %s\n want %s", i, got, c.Repr)
		}
		if got := (&tr).String(); got != c.Repr {
			t.Errorf("caso %d repr via puntatore diverso", i)
		}
		if got := tr.JSONIndent(); got != c.JSONIndent {
			t.Errorf("caso %d json indent:\n got  %s\n want %s", i, got, c.JSONIndent)
		}
		if got, want := rowsJSON(t, flatRows(flattenRates(tr.Luce, tr.Gas))), rowsJSON(t, c.Flat); got != want {
			t.Errorf("caso %d flatten:\n got  %s\n want %s", i, got, want)
		}
	}
}

// TestJSONIndentIsValidJSON: senza NaN/Infinity l'output è JSON standard.
func TestJSONIndentIsValidJSON(t *testing.T) {
	tr := Tariffe{DataAggiornamento: "2025-11-13"}
	tr.Luce.Fissa.set("monoraria", Rate{Energia: 0.1078, Commercializzazione: ptr(72.0)})
	var v map[string]any
	if err := json.Unmarshal([]byte(tr.JSONIndent()), &v); err != nil {
		t.Fatalf("JSON non valido: %v", err)
	}
}
