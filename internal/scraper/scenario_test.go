package scraper

// Scenari end-to-end confrontati con fetch_octopus_tariffe e backfill()
// eseguiti in Python con urlopen, orologio e database simulati.

import (
	"context"
	"encoding/json"
	"strconv"
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
		assertRows(t, c.rates, want[i].Rows)
	}
}

// seconds legge un numero di secondi del golden (es. "0.5").
func seconds(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGoldenFetchOctopusTariffe(t *testing.T) {
	var cases []struct {
		Name        string           `json:"name"`
		Routes      map[string]route `json:"routes"`
		MaxDaysBack int              `json:"max_days_back"`
		Now         []int            `json:"now"`
		SaveReturns *int             `json:"save_returns"`
		JSONIndent  *string          `json:"json_indent"`
		Exception   *pyExc           `json:"exception"`
		Saved       []savedGolden    `json:"saved"`
	}
	loadGolden(t, "fetch_golden.json", &cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			client, _ := fakeClient(c.Routes, nowFrom(c.Now))
			store := &fakeStore{t: t}
			if c.SaveReturns != nil {
				store.returns = []int{*c.SaveReturns}
			}
			res, err := client.FetchOctopusTariffe(context.Background(), store, c.MaxDaysBack)
			switch {
			case c.Exception != nil:
				if err == nil || res != nil {
					t.Errorf("res = %v, err = %v, Python solleva %s", res, err, c.Exception.Type)
				}
			case err != nil:
				t.Fatalf("errore inatteso: %v", err)
			default:
				assertSameJSON(t, res.JSONIndent(), *c.JSONIndent)
			}
			assertSaved(t, store.calls, c.Saved)
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
		SaveReturns []int            `json:"save_returns"`
		Now         []int            `json:"now"`
		Saved       []savedGolden    `json:"saved"`
		Sleeps      []string         `json:"sleeps"`
		Logs        [][2]string      `json:"logs"`
	}
	loadGolden(t, "backfill_golden.json", &cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			client, _ := fakeClient(c.Routes, nowFrom(c.Now))
			store := &fakeStore{t: t, returns: c.SaveReturns, existing: existingKeys(c.Existing)}
			var sleeps []time.Duration
			opts := BackfillOptions{Days: c.Days, DryRun: c.DryRun, Delay: seconds(t, c.Delay), Client: client,
				Sleep: func(_ context.Context, d time.Duration) error {
					sleeps = append(sleeps, d)
					return nil
				}}
			logs := captureLogs(t)
			if err := Backfill(context.Background(), store, opts); err != nil {
				t.Fatalf("errore inatteso: %v", err)
			}
			assertSaved(t, store.calls, c.Saved)
			var wantSleeps []time.Duration
			for _, s := range c.Sleeps {
				wantSleeps = append(wantSleeps, time.Duration(seconds(t, s)*float64(time.Second)))
			}
			if rowsJSON(t, sleeps) != rowsJSON(t, wantSleeps) {
				t.Errorf("sleep = %v, attesi %v", sleeps, wantSleeps)
			}
			// I log del backfill (contatori e riepilogo) sono l'output del comando.
			assertLogs(t, logs.all(), "backfill_rate_history", c.Logs)
		})
	}
}
