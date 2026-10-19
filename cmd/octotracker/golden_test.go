package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
)

// TestGoldenBackfillArgs confronta parseBackfillArgs con l'argparse di
// backfill_rate_history.py (Python 3.13, prog "octotracker backfill",
// COLUMNS=80): stdout, stderr, codice di uscita e valori letti.
func TestGoldenBackfillArgs(t *testing.T) {
	raw, err := os.ReadFile("testdata/backfill_args_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Args   []string `json:"args"`
		Stdout string   `json:"stdout"`
		Stderr string   `json:"stderr"`
		Exit   *int     `json:"exit"`
		Parsed *struct {
			Days     string `json:"days"`
			DryRun   bool   `json:"dry_run"`
			Delay    string `json:"delay"`
			LogLevel string `json:"log_level"`
		} `json:"parsed"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		var stdout, stderr bytes.Buffer
		parsed, code, done := parseBackfillArgs(c.Args, &stdout, &stderr)
		if stdout.String() != c.Stdout || stderr.String() != c.Stderr {
			t.Errorf("%q: stdout %q stderr %q\natteso stdout %q stderr %q", c.Args, stdout.String(), stderr.String(), c.Stdout, c.Stderr)
		}
		if c.Exit != nil {
			if !done || code != *c.Exit {
				t.Errorf("%q: uscita (%v, %d), attesa %d", c.Args, done, code, *c.Exit)
			}
			continue
		}
		if done {
			t.Errorf("%q: uscita %d inattesa", c.Args, code)
			continue
		}
		days := strconv.Itoa(parsed.days)
		if n, err := pycompat.ParseInt(c.Parsed.Days); err == nil && !n.IsInt64() {
			// Gli int di Python non hanno limiti: oltre int64 il valore viene
			// saturato (il backfill fallisce comunque con OverflowError)
			if parsed.days != math.MaxInt {
				t.Errorf("%q: days = %d, atteso MaxInt", c.Args, parsed.days)
			}
			days = c.Parsed.Days
		}
		if days != c.Parsed.Days || parsed.dryRun != c.Parsed.DryRun ||
			pycompat.Repr(parsed.delay) != c.Parsed.Delay || parsed.logLevel != c.Parsed.LogLevel {
			t.Errorf("%q: %+v (delay %s), atteso %+v", c.Args, parsed, pycompat.Repr(parsed.delay), *c.Parsed)
		}
	}
}
