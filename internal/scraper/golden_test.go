package scraper

// Test differenziali: i golden in testdata/ sono stati generati eseguendo
// data_reader.py e backfill_rate_history.py (commit 606e4c5). Si confrontano
// le tariffe lette e le righe salvate; i log e i messaggi delle eccezioni
// Python non fanno parte del contratto.

import (
	"testing"
)

// pyExc è l'eccezione Python registrata nel golden: conta solo che ci sia.
type pyExc struct {
	Type string `json:"type"`
}

func TestGoldenParseAreraXML(t *testing.T) {
	var cases []struct {
		Name      string      `json:"name"`
		Service   string      `json:"service"`
		XML       string      `json:"xml"`
		Result    *string     `json:"result"`
		Flat      [][]*string `json:"flat"`
		Exception *pyExc      `json:"exception"`
	}
	loadGolden(t, "parse_golden.json", &cases)
	if len(cases) < 50 {
		t.Fatalf("golden incompleti: %d casi", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			res, err := parseAreraXML(c.XML, c.Service)
			if c.Exception != nil {
				if err == nil {
					t.Fatalf("nessun errore, Python solleva %s", c.Exception.Type)
				}
				return
			}
			if err != nil {
				t.Fatalf("errore inatteso: %v", err)
			}
			assertSameJSON(t, areraJSON(t, res), *c.Result)
			assertRows(t, flattenRates(res.get("luce"), res.get("gas")), c.Flat)
		})
	}
}

func TestGoldenNormalizedCode(t *testing.T) {
	var cases []struct {
		In  *string `json:"in"`
		Out *string `json:"out"`
	}
	loadGolden(t, "normalized_code_golden.json", &cases)
	for _, c := range cases {
		got, ok := normalizedCode(c.In)
		switch {
		case c.Out == nil && ok:
			t.Errorf("normalizedCode(%.40q) = %q, atteso nessun codice", *c.In, got)
		case c.Out != nil && (!ok || got != *c.Out):
			t.Errorf("normalizedCode(%.40q) = %q %v, atteso %q", *c.In, got, ok, *c.Out)
		}
	}
}
