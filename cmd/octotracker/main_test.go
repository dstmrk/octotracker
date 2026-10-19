package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
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

// TestParseBackfillArgs: opzioni, default ed errori di backfill.
func TestParseBackfillArgs(t *testing.T) {
	ok := []struct {
		args []string
		want backfillArgs
	}{
		{nil, backfillArgs{days: 365, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days", "5"}, backfillArgs{days: 5, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days=5"}, backfillArgs{days: 5, delay: 0.5, logLevel: "INFO"}},
		{[]string{"-days", "7"}, backfillArgs{days: 7, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--days", "-5"}, backfillArgs{days: -5, delay: 0.5, logLevel: "INFO"}},
		{[]string{"--dry-run", "--delay", "1.5", "--log-level=ERROR"}, backfillArgs{days: 365, dryRun: true, delay: 1.5, logLevel: "ERROR"}},
		{[]string{"--delay", "-.5", "--log-level", "DEBUG"}, backfillArgs{days: 365, delay: -0.5, logLevel: "DEBUG"}},
		{[]string{"--days", "1", "--days", "2"}, backfillArgs{days: 2, delay: 0.5, logLevel: "INFO"}},
	}
	for _, tc := range ok {
		got, err := parseBackfillArgs(tc.args, io.Discard)
		if err != nil || got != tc.want {
			t.Errorf("parseBackfillArgs(%v) = %+v %v", tc.args, got, err)
		}
	}

	errs := [][]string{
		{"--days", "x"},
		{"--days"},
		{"--delay", "x"},
		{"--log-level", "debug"},
		{"extra", "--days", "1"},
		{"--days", "5", "extra"},
		{"--bogus"},
	}
	for _, args := range errs {
		var stderr bytes.Buffer
		if _, err := parseBackfillArgs(args, &stderr); err == nil || errors.Is(err, flag.ErrHelp) {
			t.Errorf("parseBackfillArgs(%v): errore = %v", args, err)
		}
		if !strings.Contains(stderr.String(), "-days") {
			t.Errorf("parseBackfillArgs(%v): uso non stampato: %q", args, stderr.String())
		}
	}
	for _, args := range [][]string{{"-h"}, {"--help"}} {
		if _, err := parseBackfillArgs(args, io.Discard); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("%v: errore = %v", args, err)
		}
	}
}

// TestConfigError estrae l'errore di config.Load per una variabile.
func TestConfigError(t *testing.T) {
	joined := errors.Join(
		errors.New(`WEBHOOK_PORT non valido: "99999999999999999999"`),
		errors.New(`SCRAPER_HOUR non valido: "x"`),
		errors.New(`CHECKER_HOUR non valido: "y"`),
		errors.New(`BROADCAST_BATCH_SIZE non valido: "z"`),
	)
	// Vale l'ordine delle variabili richieste, non quello di config
	if err := configError(joined, "SCRAPER_HOUR", "WEBHOOK_PORT"); err == nil || err.Error() != `SCRAPER_HOUR non valido: "x"` {
		t.Errorf("configError = %v", err)
	}
	if err := configError(joined, botIntVars...); err == nil || err.Error() != `CHECKER_HOUR non valido: "y"` {
		t.Errorf("configError(botIntVars) = %v", err)
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

// TestRunServeConfigErrors: serve termina con l'errore di configurazione.
func TestRunServeConfigErrors(t *testing.T) {
	cleanEnv(t)
	code, _, errOut := runCmd(context.Background())
	if code != exitError || !strings.HasPrefix(errOut, "WEBHOOK_SECRET è obbligatorio") {
		t.Errorf("serve senza secret: %d %q", code, errOut)
	}
	if _, err := os.Stat("data/octotracker.log"); err != nil {
		t.Errorf("file di log non creato: %v", err)
	}

	t.Setenv("SCRAPER_HOUR", "nove")
	code, _, errOut = runCmd(context.Background(), "serve")
	if code != exitError || errOut != "SCRAPER_HOUR non valido: \"nove\"\n" {
		t.Errorf("serve con SCRAPER_HOUR non valido: %d %q", code, errOut)
	}

	// BROADCAST_BATCH_SIZE non è usato da serve
	t.Setenv("SCRAPER_HOUR", "")
	t.Setenv("BROADCAST_BATCH_SIZE", "x")
	t.Setenv("WEBHOOK_SECRET", "s")
	code, _, errOut = runCmd(context.Background(), "serve")
	if code != exitError || errOut != "TELEGRAM_BOT_TOKEN non impostato\n" {
		t.Errorf("serve senza token: %d %q", code, errOut)
	}

	t.Setenv("DATA_DIR", "/dev/null/impossibile")
	if code, _, _ := runCmd(context.Background(), "serve"); code != exitError {
		t.Errorf("serve con DATA_DIR non scrivibile: %d", code)
	}
}

// TestRunScripts: check e remind senza token terminano con errore; con il
// token e il database vuoto terminano con successo.
func TestRunScripts(t *testing.T) {
	cleanEnv(t)
	for _, cmd := range []string{"check", "remind"} {
		code, _, errOut := runCmd(context.Background(), cmd)
		if code != exitError || errOut != "TELEGRAM_BOT_TOKEN non impostato in .env\n" {
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
		if !strings.Contains(errOut, "database - INFO - ✅ Database inizializzato") {
			t.Errorf("%s: log = %q", cmd, errOut)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, _, errOut := runCmd(ctx, "check")
	if code != exitInterrupted || !strings.HasSuffix(errOut, "interrotto\n") {
		t.Errorf("check interrotto: %d %q", code, errOut)
	}

	t.Setenv("DATA_DIR", "/dev/null/impossibile")
	if code, _, _ := runCmd(context.Background(), "check"); code != exitError {
		t.Errorf("check con DATA_DIR non valido: %d", code)
	}
}

// TestRunScrapeAndBackfillInterrupted: scrape e backfill interrotti escono
// con il codice di interruzione.
func TestRunScrapeAndBackfillInterrupted(t *testing.T) {
	cleanEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{{"scrape"}, {"backfill", "--days", "2", "--delay", "0"}} {
		code, _, errOut := runCmd(ctx, args...)
		if code != exitInterrupted || !strings.HasSuffix(errOut, "interrotto\n") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	code, _, errOut := runCmd(context.Background(), "backfill", "--help")
	if code != exitOK || !strings.Contains(errOut, "-dry-run") {
		t.Errorf("backfill --help: %d %q", code, errOut)
	}
	code, _, errOut = runCmd(context.Background(), "backfill", "--bogus")
	if code != exitUsage || !strings.Contains(errOut, "bogus") {
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
// valido è un errore.
func TestRunBroadcast(t *testing.T) {
	cleanEnv(t)
	code, _, errOut := runCmd(context.Background(), "broadcast")
	if code != exitError || !strings.Contains(errOut, "broadcast - ERROR - ❌ TELEGRAM_BOT_TOKEN non configurato nel file .env") {
		t.Errorf("broadcast senza token: %d %q", code, errOut)
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("BROADCAST_BATCH_SIZE", "dieci")
	code, _, errOut = runCmd(context.Background(), "broadcast")
	if code != exitError || errOut != "BROADCAST_BATCH_SIZE non valido: \"dieci\"\n" {
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
	if code, _, errOut := runCmd(context.Background(), "healthcheck"); code != exitError || errOut != "HEALTH_PORT non valido: \"porta\"\n" {
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

// TestFail verifica la stampa degli errori fatali.
func TestFail(t *testing.T) {
	var stderr bytes.Buffer
	s := streams{stderr: &stderr}
	if code := fail(errors.New("date value out of range"), s); code != exitError ||
		stderr.String() != "date value out of range\n" {
		t.Errorf("fail = %d %q", code, stderr.String())
	}
	stderr.Reset()
	if code := fail(fmt.Errorf("x: %w", context.Canceled), s); code != exitInterrupted || stderr.String() != "interrotto\n" {
		t.Errorf("fail(cancel) = %d %q", code, stderr.String())
	}
}
