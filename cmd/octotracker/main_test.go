package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
)

// cleanEnv isola il test: directory di lavoro temporanea (senza .env) e
// variabili di OctoTracker vuote.
func cleanEnv(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	for _, name := range []string{
		"TELEGRAM_BOT_TOKEN", "WEBHOOK_URL", "WEBHOOK_PORT", "WEBHOOK_SECRET", "HEALTH_PORT",
		"SCRAPER_HOUR", "CHECKER_HOUR", "REMINDER_HOUR", "SCRAPER_TIMEOUT_SECONDS", "ADMIN_USER_ID",
		"WEBAPP_URL", "TELEGRAM_CHANNEL", "LOG_LEVEL", "BROADCAST_BATCH_SIZE", "DATA_DIR",
	} {
		t.Setenv(name, "")
	}
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// runCmd esegue run e restituisce codice di uscita, stdout e stderr.
func runCmd(ctx context.Context, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(ctx, args, streams{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr})
	return code, stdout.String(), stderr.String()
}

// TestParseCommand verifica la scelta del sottocomando.
func TestParseCommand(t *testing.T) {
	cases := []struct {
		args []string
		cmd  string
		rest []string
	}{
		{nil, "serve", nil},
		{[]string{"serve"}, "serve", []string{}},
		{[]string{"check"}, "check", []string{}},
		{[]string{"remind"}, "remind", []string{}},
		{[]string{"scrape"}, "scrape", []string{}},
		{[]string{"broadcast", "m.txt", "u.txt"}, "broadcast", []string{"m.txt", "u.txt"}},
		{[]string{"backfill", "--days", "7"}, "backfill", []string{"--days", "7"}},
		{[]string{"healthcheck"}, "healthcheck", []string{}},
		{[]string{"version"}, "version", []string{}},
		{[]string{"--help"}, "help", nil},
		{[]string{"-h"}, "help", nil},
		{[]string{"help"}, "help", nil},
	}
	for _, tc := range cases {
		cmd, rest, err := parseCommand(tc.args)
		if err != nil || cmd != tc.cmd || fmt.Sprint(rest) != fmt.Sprint(tc.rest) {
			t.Errorf("parseCommand(%v) = %q %v %v", tc.args, cmd, rest, err)
		}
	}
	if _, _, err := parseCommand([]string{"boh"}); err == nil {
		t.Error("comando sconosciuto accettato")
	}
}

// TestRunVersionHelpAndUsageErrors verifica version, help e gli errori di uso.
func TestRunVersionHelpAndUsageErrors(t *testing.T) {
	code, out, _ := runCmd(context.Background(), "version")
	if code != exitOK || out != version+"\n" {
		t.Errorf("version: %d %q", code, out)
	}
	code, out, _ = runCmd(context.Background(), "help")
	if code != exitOK || !strings.Contains(out, "healthcheck") {
		t.Errorf("help: %d %q", code, out)
	}
	code, _, errOut := runCmd(context.Background(), "boh")
	if code != exitUsage || !strings.Contains(errOut, `comando sconosciuto: "boh"`) || !strings.Contains(errOut, "uso: octotracker") {
		t.Errorf("comando sconosciuto: %d %q", code, errOut)
	}
	code, _, errOut = runCmd(context.Background(), "check", "extra")
	if code != exitUsage || !strings.Contains(errOut, "argomenti inattesi per check: extra") {
		t.Errorf("argomenti inattesi: %d %q", code, errOut)
	}
}

// TestParseBackfill confronta il parser con argparse di
// backfill_rate_history.py (messaggi generati con Python 3.13).
func TestParseBackfill(t *testing.T) {
	ok := []struct {
		args []string
		want backfillArgs
	}{
		{nil, backfillArgs{days: 365, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days", "5"}, backfillArgs{days: 5, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days=5"}, backfillArgs{days: 5, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--da", "7"}, backfillArgs{days: 7, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days", "-5"}, backfillArgs{days: -5, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days", "1_0"}, backfillArgs{days: 10, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days", "٣"}, backfillArgs{days: 3, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days", " 4 "}, backfillArgs{days: 4, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--dry-run", "--delay", "1.5", "--log-level=ERROR"}, backfillArgs{days: 365, dryRun: true, delay: 1.5, logLevel: "ERROR"}},
		{[]string{"--dr", "--de=0", "--l", "DEBUG"}, backfillArgs{days: 365, dryRun: true, delay: 0, logLevel: "DEBUG"}},
		{[]string{"--delay", "-.5"}, backfillArgs{days: 365, delay: -0.5, logLevel: "INFO"}},
		{[]string{"--days", "1", "--days", "2"}, backfillArgs{days: 2, delay: 0.5, logLevel: "INFO"}},
	}
	for _, tc := range ok {
		got, help, err := parseBackfill(tc.args)
		if err != nil || help || got != tc.want {
			t.Errorf("parseBackfill(%v) = %+v %v %v", tc.args, got, help, err)
		}
	}

	if got, _, err := parseBackfill([]string{"--delay", "nan"}); err != nil || !math.IsNaN(got.delay) {
		t.Errorf("--delay nan = %v %v", got.delay, err)
	}
	if got, _, _ := parseBackfill([]string{"--days", "99999999999999999999999"}); got.days != math.MaxInt {
		t.Errorf("--days enorme = %d", got.days)
	}
	if got, _, _ := parseBackfill([]string{"--days", "-99999999999999999999999"}); got.days != math.MinInt {
		t.Errorf("--days enorme negativo = %d", got.days)
	}

	errs := []struct {
		args []string
		msg  string
	}{
		{[]string{"--d", "3"}, "ambiguous option: --d could match --days, --dry-run, --delay"},
		{[]string{"--days", "x"}, "argument --days: invalid int value: 'x'"},
		{[]string{"--days"}, "argument --days: expected one argument"},
		{[]string{"--days", "--dry-run"}, "argument --days: expected one argument"},
		{[]string{"--dry-run=1"}, "argument --dry-run: ignored explicit argument '1'"},
		{[]string{"--delay", "x"}, "argument --delay: invalid float value: 'x'"},
		{[]string{"--log-level", "debug"}, "argument --log-level: invalid choice: 'debug' (choose from 'DEBUG', 'INFO', 'WARNING', 'ERROR')"},
		{[]string{"extra", "--bogus"}, "unrecognized arguments: extra --bogus"},
		{[]string{"--days", "5", "extra"}, "unrecognized arguments: extra"},
		{[]string{"-x"}, "unrecognized arguments: -x"},
		{[]string{"--", "--days", "1"}, "unrecognized arguments: -- --days 1"},
		{[]string{"extra", "--days", "x"}, "argument --days: invalid int value: 'x'"},
	}
	for _, tc := range errs {
		_, _, err := parseBackfill(tc.args)
		if err == nil || err.Error() != tc.msg {
			t.Errorf("parseBackfill(%v) errore = %v, atteso %q", tc.args, err, tc.msg)
		}
	}

	for _, args := range [][]string{{"-h"}, {"--help"}, {"--he"}, {"--days", "x", "-h"}} {
		_, help, err := parseBackfill(args)
		if args[0] == "--days" {
			if err == nil {
				t.Errorf("%v: atteso errore prima di -h", args)
			}
			continue
		}
		if !help || err != nil {
			t.Errorf("%v: help=%v err=%v", args, help, err)
		}
	}
}

// TestParseBackfillArgsOutput verifica testi e codici di uscita di argparse.
func TestParseBackfillArgsOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	if _, code, done := parseBackfillArgs([]string{"-h"}, &out, &errOut); !done || code != exitOK || out.String() != backfillHelp {
		t.Errorf("-h: %v %d %q", done, code, out.String())
	}
	out.Reset()
	_, code, done := parseBackfillArgs([]string{"--days", "x"}, &out, &errOut)
	want := "usage: octotracker backfill [-h] [--days DAYS] [--dry-run] [--delay DELAY]\n" +
		"                            [--log-level {DEBUG,INFO,WARNING,ERROR}]\n" +
		"octotracker backfill: error: argument --days: invalid int value: 'x'\n"
	if !done || code != exitUsage || errOut.String() != want {
		t.Errorf("--days x: %v %d %q", done, code, errOut.String())
	}
	if _, _, done := parseBackfillArgs(nil, &out, &errOut); done {
		t.Error("argomenti validi: done=true")
	}
}

// TestConfigError estrae l'errore di config.Load per una variabile.
func TestConfigError(t *testing.T) {
	joined := errors.Join(
		errors.New(`SCRAPER_HOUR non valido: "x"`),
		errors.New(`BROADCAST_BATCH_SIZE non valido: "y"`),
	)
	if err := configError(joined, "WEBHOOK_PORT", "SCRAPER_HOUR"); err == nil || !strings.Contains(err.Error(), "SCRAPER_HOUR") {
		t.Errorf("configError = %v", err)
	}
	if err := configError(joined, "HEALTH_PORT"); err != nil {
		t.Errorf("configError = %v", err)
	}
	if err := configError(nil, "HEALTH_PORT"); err != nil {
		t.Errorf("configError(nil) = %v", err)
	}
	if err := configError(errors.New(`HEALTH_PORT non valido: "z"`), "HEALTH_PORT"); err == nil {
		t.Error("errore singolo non riconosciuto")
	}
}

// TestRunServeConfigErrors: serve termina con il ValueError di bot.py.
func TestRunServeConfigErrors(t *testing.T) {
	cleanEnv(t)
	code, _, errOut := runCmd(context.Background())
	if code != exitError || !strings.HasPrefix(errOut, "ValueError: WEBHOOK_SECRET è obbligatorio") {
		t.Errorf("serve senza secret: %d %q", code, errOut)
	}
	if _, err := os.Stat("data/octotracker.log"); err != nil {
		t.Errorf("file di log non creato: %v", err)
	}

	t.Setenv("SCRAPER_HOUR", "nove")
	code, _, errOut = runCmd(context.Background(), "serve")
	if code != exitError || !strings.Contains(errOut, `ValueError: SCRAPER_HOUR non valido: "nove"`) {
		t.Errorf("serve con SCRAPER_HOUR non valido: %d %q", code, errOut)
	}

	// BROADCAST_BATCH_SIZE non è letto da bot.py
	t.Setenv("SCRAPER_HOUR", "")
	t.Setenv("BROADCAST_BATCH_SIZE", "x")
	t.Setenv("WEBHOOK_SECRET", "s")
	code, _, errOut = runCmd(context.Background(), "serve")
	if code != exitError || errOut != "ValueError: TELEGRAM_BOT_TOKEN non impostato\n" {
		t.Errorf("serve senza token: %d %q", code, errOut)
	}

	t.Setenv("DATA_DIR", "/dev/null/impossibile")
	if code, _, _ := runCmd(context.Background(), "serve"); code != exitError {
		t.Errorf("serve con DATA_DIR non scrivibile: %d", code)
	}
}

// TestRunScripts: check e remind senza token sollevano il ValueError di
// main(); con il token e il database vuoto terminano con successo.
func TestRunScripts(t *testing.T) {
	cleanEnv(t)
	for _, cmd := range []string{"check", "remind"} {
		code, _, errOut := runCmd(context.Background(), cmd)
		if code != exitError || errOut != "ValueError: TELEGRAM_BOT_TOKEN non impostato in .env\n" {
			t.Errorf("%s senza token: %d %q", cmd, code, errOut)
		}
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Error("database aperto prima del controllo del token")
	}

	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	for _, cmd := range []string{"check", "remind"} {
		code, _, errOut := runCmd(context.Background(), cmd)
		if code != exitOK {
			t.Errorf("%s: %d %q", cmd, code, errOut)
		}
		if !strings.Contains(errOut, " - INFO - ✅ Database inizializzato") {
			t.Errorf("%s: log = %q", cmd, errOut)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, _, errOut := runCmd(ctx, "check")
	if code != exitInterrupted || !strings.HasSuffix(errOut, "KeyboardInterrupt\n") {
		t.Errorf("check interrotto: %d %q", code, errOut)
	}

	t.Setenv("DATA_DIR", "/dev/null/impossibile")
	if code, _, _ := runCmd(context.Background(), "check"); code != exitError {
		t.Errorf("check con DATA_DIR non valido: %d", code)
	}
}

// TestRunScrapeAndBackfillInterrupted: scrape e backfill interrotti escono
// con KeyboardInterrupt.
func TestRunScrapeAndBackfillInterrupted(t *testing.T) {
	cleanEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{{"scrape"}, {"backfill", "--days", "2", "--delay", "0"}} {
		code, _, errOut := runCmd(ctx, args...)
		if code != exitInterrupted || !strings.HasSuffix(errOut, "KeyboardInterrupt\n") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	code, out, _ := runCmd(context.Background(), "backfill", "--help")
	if code != exitOK || out != backfillHelp {
		t.Errorf("backfill --help: %d", code)
	}
	code, _, errOut := runCmd(context.Background(), "backfill", "--bogus")
	if code != exitUsage || !strings.HasSuffix(errOut, "error: unrecognized arguments: --bogus\n") {
		t.Errorf("backfill --bogus: %d %q", code, errOut)
	}
	// Nessun giorno da scaricare: solo il riepilogo
	code, _, errOut = runCmd(context.Background(), "backfill", "--days", "0", "--dry-run")
	if code != exitOK {
		t.Errorf("backfill --days 0: %d %q", code, errOut)
	}

	t.Setenv("DATA_DIR", "/dev/null/impossibile")
	for _, cmd := range []string{"scrape", "backfill"} {
		if code, _, _ := runCmd(context.Background(), cmd); code != exitError {
			t.Errorf("%s con DATA_DIR non valido: %d", cmd, code)
		}
	}
}

// TestRunBroadcast: senza token esce con 1; un BROADCAST_BATCH_SIZE non
// valido è un ValueError.
func TestRunBroadcast(t *testing.T) {
	cleanEnv(t)
	code, _, errOut := runCmd(context.Background(), "broadcast")
	if code != exitError || !strings.Contains(errOut, "broadcast - ERROR - ❌ TELEGRAM_BOT_TOKEN non configurato nel file .env") {
		t.Errorf("broadcast senza token: %d %q", code, errOut)
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("BROADCAST_BATCH_SIZE", "dieci")
	code, _, errOut = runCmd(context.Background(), "broadcast")
	if code != exitError || !strings.HasPrefix(errOut, `ValueError: BROADCAST_BATCH_SIZE non valido: "dieci"`) {
		t.Errorf("broadcast con batch non valido: %d %q", code, errOut)
	}
	t.Setenv("BROADCAST_BATCH_SIZE", "")
	code, _, errOut = runCmd(context.Background(), "broadcast", "manca.txt")
	if code != exitError || !strings.Contains(errOut, "manca.txt") {
		t.Errorf("broadcast con file mancante: %d %q", code, errOut)
	}
}

// TestRunHealthcheck: 0 solo con HTTP 200 da /health.
func TestRunHealthcheck(t *testing.T) {
	cleanEnv(t)
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status": "healthy"}`))
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("HEALTH_PORT", port)

	code, out, _ := runCmd(context.Background(), "healthcheck")
	if code != exitOK || out != `{"status": "healthy"}` {
		t.Errorf("healthy: %d %q", code, out)
	}
	status = http.StatusServiceUnavailable
	code, _, errOut := runCmd(context.Background(), "healthcheck")
	if code != exitError || !strings.Contains(errOut, "HTTP 503") {
		t.Errorf("unhealthy: %d %q", code, errOut)
	}

	srv.Close()
	if code, _, _ := runCmd(context.Background(), "healthcheck"); code != exitError {
		t.Errorf("server spento: %d", code)
	}
	t.Setenv("HEALTH_PORT", "porta")
	if code, _, errOut := runCmd(context.Background(), "healthcheck"); code != exitError || !strings.Contains(errOut, "HEALTH_PORT") {
		t.Errorf("HEALTH_PORT non valido: %d %q", code, errOut)
	}
	t.Setenv("HEALTH_PORT", strconv.Itoa(-1))
	if code, _, _ := runCmd(context.Background(), "healthcheck"); code != exitError {
		t.Errorf("HEALTH_PORT negativo: %d", code)
	}
}

// TestRunDotEnvError: un .env non valido (non UTF-8) ferma il comando.
func TestRunDotEnvError(t *testing.T) {
	cleanEnv(t)
	if err := os.WriteFile(".env", []byte("A=\xff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(context.Background(), "check"); code != exitError || errOut == "" {
		t.Errorf("check con .env non valido: %d %q", code, errOut)
	}
}

// TestFail verifica la stampa delle eccezioni fatali.
func TestFail(t *testing.T) {
	var stderr bytes.Buffer
	s := streams{stderr: &stderr}
	if code := fail(pycompat.NewError("OverflowError", "date value out of range"), s); code != exitError ||
		stderr.String() != "OverflowError: date value out of range\n" {
		t.Errorf("fail = %d %q", code, stderr.String())
	}
	stderr.Reset()
	if code := fail(fmt.Errorf("x: %w", context.Canceled), s); code != exitInterrupted || stderr.String() != "KeyboardInterrupt\n" {
		t.Errorf("fail(cancel) = %d %q", code, stderr.String())
	}
}
