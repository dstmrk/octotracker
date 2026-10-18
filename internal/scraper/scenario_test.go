package scraper

// Scenari end-to-end confrontati con fetch_octopus_tariffe e backfill()
// eseguiti in Python con urlopen, orologio e database simulati.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
)

type savedGolden struct {
	DataFonte string
	Rows      [][]*string
}

// UnmarshalJSON legge [data_fonte, righe].
func (s *savedGolden) UnmarshalJSON(b []byte) error {
	var raw [2]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[0], &s.DataFonte); err != nil {
		return err
	}
	return json.Unmarshal(raw[1], &s.Rows)
}

func existingKeys(keys [][4]string) map[database.RateKey]struct{} {
	out := map[database.RateKey]struct{}{}
	for _, k := range keys {
		out[database.RateKey{DataFonte: k[0], Servizio: k[1], Tipo: k[2], Fascia: k[3]}] = struct{}{}
	}
	return out
}

func assertSaved(t *testing.T, calls []saveCall, want []savedGolden) {
	t.Helper()
	if len(calls) != len(want) {
		t.Fatalf("SaveRatesBatch chiamato %d volte, attese %d", len(calls), len(want))
	}
	for i, c := range calls {
		if c.dataFonte != want[i].DataFonte {
			t.Errorf("salvataggio %d: data_fonte %q, attesa %q", i, c.dataFonte, want[i].DataFonte)
		}
		if got, w := rowsJSON(t, flatRows(c.rates)), rowsJSON(t, want[i].Rows); got != w {
			t.Errorf("salvataggio %d righe:\n got  %s\n want %s", i, got, w)
		}
	}
}

func TestGoldenFetchOctopusTariffe(t *testing.T) {
	var cases []struct {
		Name        string           `json:"name"`
		Routes      map[string]route `json:"routes"`
		MaxDaysBack int              `json:"max_days_back"`
		Now         []int            `json:"now"`
		SaveReturns *int             `json:"save_returns"`
		Repr        *string          `json:"repr"`
		JSONIndent  *string          `json:"json_indent"`
		Exception   *pyExc           `json:"exception"`
		Saved       []savedGolden    `json:"saved"`
		Logs        [][2]string      `json:"logs"`
	}
	loadGolden(t, "fetch_golden.json", &cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			client, _ := fakeClient(c.Routes, nowFrom(c.Now))
			store := &fakeStore{t: t}
			if c.SaveReturns != nil {
				store.returns = []any{*c.SaveReturns}
			}
			logs := captureLogs(t)
			res, err := client.FetchOctopusTariffe(context.Background(), store, c.MaxDaysBack)
			if c.Exception != nil {
				assertPyException(t, err, c.Exception)
				if res != nil {
					t.Error("risultato non nil con eccezione")
				}
			} else {
				if err != nil {
					t.Fatalf("errore inatteso: %v", err)
				}
				if got := res.String(); got != *c.Repr {
					t.Errorf("repr:\n got  %s\n want %s", got, *c.Repr)
				}
				if got := res.JSONIndent(); got != *c.JSONIndent {
					t.Errorf("json:\n got  %s\n want %s", got, *c.JSONIndent)
				}
			}
			assertSaved(t, store.calls, c.Saved)
			assertLogs(t, logs.all(), c.Logs)
		})
	}
}

func TestGoldenBackfill(t *testing.T) {
	var cases []struct {
		Name        string           `json:"name"`
		Days        int              `json:"days"`
		DryRun      bool             `json:"dry_run"`
		Delay       string           `json:"delay"`
		Routes      map[string]route `json:"routes"`
		Existing    [][4]string      `json:"existing"`
		SaveReturns []any            `json:"save_returns"`
		Now         []int            `json:"now"`
		Exception   *pyExc           `json:"exception"`
		Saved       []savedGolden    `json:"saved"`
		Sleeps      []string         `json:"sleeps"`
		Logs        [][2]string      `json:"logs"`
	}
	loadGolden(t, "backfill_golden.json", &cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			client, _ := fakeClient(c.Routes, nowFrom(c.Now))
			store := &fakeStore{t: t, returns: c.SaveReturns, existing: existingKeys(c.Existing)}
			delay, err := ParseFloat(c.Delay)
			if err != nil {
				t.Fatal(err)
			}
			var sleeps []time.Duration
			opts := BackfillOptions{Days: c.Days, DryRun: c.DryRun, Delay: delay, Client: client,
				Sleep: func(_ context.Context, d time.Duration) error {
					sleeps = append(sleeps, d)
					return nil
				}}
			logs := captureLogs(t)
			err = Backfill(context.Background(), store, opts)
			if c.Exception != nil {
				assertPyException(t, err, c.Exception)
			} else if err != nil {
				t.Fatalf("errore inatteso: %v", err)
			}
			assertSaved(t, store.calls, c.Saved)
			// Python registra la chiamata a time.sleep anche quando solleva;
			// in Go il delay viene validato prima di chiamare Sleep.
			var wantSleeps []time.Duration
			for _, s := range c.Sleeps {
				v, _ := ParseFloat(s)
				if d, err := pySleepDuration(v); err == nil {
					wantSleeps = append(wantSleeps, d)
				}
			}
			if rowsJSON(t, sleeps) != rowsJSON(t, wantSleeps) {
				t.Errorf("sleep = %v, attesi %v", sleeps, wantSleeps)
			}
			assertLogs(t, logs.all(), c.Logs)
		})
	}
}
