// Comando octotracker: il bot Telegram OctoTracker e gli script di
// manutenzione, in un unico binario:
//
//	octotracker [serve]                       avvia il bot
//	octotracker check                         controlla le tariffe e notifica
//	octotracker remind                        invia i reminder delle scadenze
//	octotracker scrape                        legge e salva le tariffe ARERA
//	octotracker broadcast [msg_file] [users]  messaggio a una lista di utenti
//	octotracker backfill [--days N] ...       popola lo storico tariffe
//	octotracker healthcheck                   GET http://127.0.0.1:HEALTH_PORT/health
//	octotracker version                       versione del binario
//
// Ogni sottocomando carica prima il file .env della directory corrente (le
// variabili d'ambiente già impostate hanno la precedenza) e logga su stderr
// nel formato di internal/logging; solo serve logga su stdout e sul file
// <DATA_DIR>/octotracker.log.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	// Database dei fusi orari incorporato: TZ=Europe/Rome funziona anche
	// in un'immagine senza /usr/share/zoneinfo.
	_ "time/tzdata"

	"github.com/dstmrk/octotracker/internal/app"
	"github.com/dstmrk/octotracker/internal/broadcast"
	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/config"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/reminder"
	"github.com/dstmrk/octotracker/internal/scraper"
)

// version è la versione del binario, impostata al build con
// -ldflags "-X main.version=1.2.3".
var version = "dev"

// Codici di uscita.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2   // errore di argomenti
	exitInterrupted = 130 // interruzione (Ctrl-C/SIGTERM)
)

// HealthcheckTimeout è il limite della richiesta di healthcheck (il timeout
// dell'HEALTHCHECK Docker è 5 s).
const HealthcheckTimeout = 4 * time.Second

// commands elenca i sottocomandi validi.
var commands = []string{"serve", "check", "remind", "scrape", "broadcast", "backfill", "healthcheck", "version"}

const usageText = `uso: octotracker [comando] [argomenti]

comandi:
  serve                              avvia il bot (default)
  check                              controlla le tariffe e invia le notifiche
  remind                             invia i reminder delle scadenze
  scrape                             legge le tariffe ARERA e le salva nel database
  broadcast [message_file] [users]   invia un messaggio a una lista di utenti
  backfill [--days N] [--dry-run] [--delay S] [--log-level L]
                                     popola lo storico tariffe
  healthcheck                        verifica /health (uscita 0 solo con HTTP 200)
  version                            stampa la versione
`

// streams sono gli stream standard del processo (sostituibili nei test).
type streams struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], streams{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr})
	stop()
	os.Exit(code)
}

// parseCommand separa il sottocomando dai suoi argomenti: nessun argomento
// significa "serve".
func parseCommand(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "serve", nil, nil
	}
	cmd := args[0]
	if slices.Contains(commands, cmd) {
		return cmd, args[1:], nil
	}
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		return "help", nil, nil
	}
	return "", nil, fmt.Errorf("comando sconosciuto: %q", cmd)
}

// run esegue il sottocomando e restituisce il codice di uscita.
func run(ctx context.Context, args []string, s streams) int {
	cmd, rest, err := parseCommand(args)
	if err != nil {
		fmt.Fprintf(s.stderr, "octotracker: %v\n\n%s", err, usageText)
		return exitUsage
	}
	switch cmd {
	case "help":
		fmt.Fprint(s.stdout, usageText)
		return exitOK
	case "version":
		fmt.Fprintln(s.stdout, version)
		return exitOK
	}
	if cmd != "backfill" && cmd != "broadcast" && len(rest) > 0 {
		fmt.Fprintf(s.stderr, "octotracker: argomenti inattesi per %s: %s\n\n%s", cmd, strings.Join(rest, " "), usageText)
		return exitUsage
	}

	// .env: le variabili d'ambiente già impostate hanno la precedenza
	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprintln(s.stderr, err)
		return exitError
	}
	cfg, cfgErr := config.Load()

	switch cmd {
	case "serve":
		return runServe(ctx, cfg, cfgErr, s)
	case "check":
		return runScript(ctx, cfg, s, func(store *database.Store) error {
			return checker.Main(ctx, cfg.BotToken, store)
		})
	case "remind":
		return runScript(ctx, cfg, s, func(store *database.Store) error {
			return reminder.Main(ctx, cfg.BotToken, store)
		})
	case "scrape":
		return runScrape(ctx, cfg, s)
	case "broadcast":
		return runBroadcast(ctx, cfg, cfgErr, rest, s)
	case "backfill":
		return runBackfill(ctx, cfg, rest, s)
	default: // healthcheck
		return runHealthcheck(ctx, cfg, cfgErr, s)
	}
}

// fail stampa l'errore fatale e restituisce il codice di uscita: 130 per
// un'interruzione (Ctrl-C/SIGTERM).
func fail(err error, s streams) int {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(s.stderr, "interrotto")
		return exitInterrupted
	}
	fmt.Fprintln(s.stderr, err)
	return exitError
}

// configError restituisce il primo errore di config.Load che riguarda una
// delle variabili names (nell'ordine di names), o nil: ogni sottocomando si
// ferma solo per un valore non valido delle variabili che usa.
func configError(err error, names ...string) error {
	if err == nil {
		return nil
	}
	errs := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	}
	for _, name := range names {
		for _, e := range errs {
			if strings.HasPrefix(e.Error(), name+" non valido") {
				return e
			}
		}
	}
	return nil
}

// botIntVars sono le variabili intere usate da serve.
var botIntVars = []string{"CHECKER_HOUR", "SCRAPER_HOUR", "REMINDER_HOUR", "SCRAPER_TIMEOUT_SECONDS", "WEBHOOK_PORT", "HEALTH_PORT"}

// runServe avvia il bot: logging su console e su file con rotazione
// giornaliera in DATA_DIR, poi app.Serve fino al segnale di stop.
func runServe(ctx context.Context, cfg config.Config, cfgErr error, s streams) int {
	if err := configError(cfgErr, botIntVars...); err != nil {
		return fail(err, s)
	}
	closeLog, err := logging.Setup(cfg.LogLevel, cfg.DataDir)
	if err != nil {
		return fail(err, s)
	}
	defer func() { _ = closeLog() }()

	if err := app.Serve(ctx, cfg, app.ServeOptions{}); err != nil {
		return fail(err, s)
	}
	return exitOK
}

// setScriptLog installa il logger dei sottocomandi: stderr, al livello
// indicato.
func setScriptLog(s streams, level slog.Level) {
	slog.SetDefault(slog.New(logging.NewTextHandler(s.stderr, level)))
}

// openStore apre il database in <DATA_DIR>/octotracker.db.
func openStore(cfg config.Config) (*database.Store, error) {
	return database.Open(filepath.Join(cfg.DataDir, database.DBFileName))
}

// withStore apre il database, esegue fn e lo chiude; un errore o
// un'interruzione diventano il codice di uscita.
func withStore(ctx context.Context, cfg config.Config, s streams, fn func(*database.Store) error) int {
	store, err := openStore(cfg)
	if err != nil {
		return fail(err, s)
	}
	defer func() { _ = store.Close() }()
	if err := fn(store); err != nil {
		return fail(err, s)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err(), s)
	}
	return exitOK
}

// runScript esegue check o remind al livello LOG_LEVEL; il token viene
// controllato prima di qualunque accesso al database.
func runScript(ctx context.Context, cfg config.Config, s streams, main func(*database.Store) error) int {
	setScriptLog(s, logging.ParseLevel(cfg.LogLevel))
	if cfg.BotToken == "" {
		return fail(checker.ErrMissingToken, s)
	}
	return withStore(ctx, cfg, s, main)
}

// runScrape legge le tariffe ARERA, le salva e stampa il risultato.
func runScrape(ctx context.Context, cfg config.Config, s streams) int {
	setScriptLog(s, logging.ParseLevel(cfg.LogLevel))
	return withStore(ctx, cfg, s, func(store *database.Store) error {
		return scraper.RunScrape(ctx, store)
	})
}

// runBroadcast invia un messaggio a una lista di utenti (log a livello INFO).
func runBroadcast(ctx context.Context, cfg config.Config, cfgErr error, args []string, s streams) int {
	setScriptLog(s, slog.LevelInfo)
	// BROADCAST_BATCH_SIZE conta solo dopo il controllo del token
	if cfg.BotToken != "" {
		if err := configError(cfgErr, "BROADCAST_BATCH_SIZE"); err != nil {
			return fail(err, s)
		}
	}
	return broadcast.Main(ctx, broadcast.MainConfig{
		Args:      args,
		BotToken:  cfg.BotToken,
		BatchSize: cfg.BroadcastBatchSize,
		Stdin:     s.stdin,
		Stdout:    s.stdout,
	})
}

// backfillArgs sono le opzioni di backfill.
type backfillArgs struct {
	days     int
	dryRun   bool
	delay    float64
	logLevel string
}

// backfillLogLevels sono i valori ammessi per --log-level.
var backfillLogLevels = []string{"DEBUG", "INFO", "WARNING", "ERROR"}

// parseBackfillArgs legge --days (default 365), --dry-run, --delay
// (default 0.5 secondi) e --log-level (default INFO). Gli errori e l'aiuto
// (-h) vengono stampati su stderr.
func parseBackfillArgs(args []string, stderr io.Writer) (backfillArgs, error) {
	a := backfillArgs{logLevel: "INFO"}
	fs := flag.NewFlagSet("octotracker backfill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&a.days, "days", scraper.DefaultBackfillDays, "numero di giorni indietro da scaricare")
	fs.BoolVar(&a.dryRun, "dry-run", false, "simula senza scrivere nel database")
	fs.Float64Var(&a.delay, "delay", scraper.DefaultBackfillDelay, "secondi di attesa tra le richieste HTTP")
	fs.Func("log-level", "livello di logging: "+strings.Join(backfillLogLevels, ", ")+" (default INFO)", func(v string) error {
		if !slices.Contains(backfillLogLevels, v) {
			return fmt.Errorf("valore non ammesso (scegliere tra %s)", strings.Join(backfillLogLevels, ", "))
		}
		a.logLevel = v
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		err := fmt.Errorf("argomenti non riconosciuti: %s", strings.Join(fs.Args(), " "))
		fmt.Fprintln(stderr, err)
		fs.Usage()
		return a, err
	}
	return a, nil
}

// runBackfill popola lo storico tariffe con i dati ARERA dei giorni passati.
func runBackfill(ctx context.Context, cfg config.Config, args []string, s streams) int {
	parsed, err := parseBackfillArgs(args, s.stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case err != nil:
		return exitUsage
	}
	setScriptLog(s, logging.ParseLevel(parsed.logLevel))
	return withStore(ctx, cfg, s, func(store *database.Store) error {
		opts := scraper.DefaultBackfillOptions()
		opts.Days = parsed.days
		opts.DryRun = parsed.dryRun
		opts.Delay = parsed.delay
		return scraper.Backfill(ctx, store, opts)
	})
}

// runHealthcheck interroga /health del server locale: esce con 0 solo se
// la risposta è HTTP 200 (healthy o degraded), così l'HEALTHCHECK Docker
// funziona anche in un'immagine senza shell né curl. Il corpo della
// risposta viene stampato su stdout.
func runHealthcheck(ctx context.Context, cfg config.Config, cfgErr error, s streams) int {
	if err := configError(cfgErr, "HEALTH_PORT"); err != nil {
		return fail(err, s)
	}
	ctx, cancel := context.WithTimeout(ctx, HealthcheckTimeout)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/health", cfg.HealthPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fail(err, s)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fail(err, s)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(s.stdout, resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(s.stderr, "\nhealthcheck fallito: HTTP %d\n", resp.StatusCode)
		return exitError
	}
	return exitOK
}
