// Comando octotracker: il bot Telegram OctoTracker e gli script di
// manutenzione, in un unico binario. Sostituisce gli entry point Python:
//
//	octotracker [serve]                       python bot.py
//	octotracker check                         python checker.py
//	octotracker remind                        python reminder.py
//	octotracker scrape                        python data_reader.py
//	octotracker broadcast [msg_file] [users]  python broadcast.py [msg_file] [users]
//	octotracker backfill [--days N] ...       python backfill_rate_history.py ...
//	octotracker healthcheck                   GET http://127.0.0.1:HEALTH_PORT/health
//	octotracker version                       versione del binario
//
// Ogni sottocomando carica prima il file .env della directory corrente (come
// load_dotenv) e configura il logging con il formato dello script Python
// corrispondente; solo serve scrive anche il file <DATA_DIR>/octotracker.log.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	// Database dei fusi orari incorporato: TZ=Europe/Rome funziona anche
	// in un'immagine senza /usr/share/zoneinfo.
	_ "time/tzdata"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
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
	exitError       = 1   // eccezione non gestita in Python
	exitUsage       = 2   // errore di argomenti (argparse)
	exitInterrupted = 130 // KeyboardInterrupt
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
	for _, c := range commands {
		if c == cmd {
			return cmd, args[1:], nil
		}
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

	// load_dotenv(): le variabili d'ambiente già impostate hanno la precedenza
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

// fail stampa l'eccezione non gestita (l'ultima riga del traceback Python) e
// restituisce il codice di uscita: 130 per un'interruzione (Ctrl-C/SIGTERM).
func fail(err error, s streams) int {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(s.stderr, "KeyboardInterrupt")
		return exitInterrupted
	}
	fmt.Fprintln(s.stderr, app.ExceptionLine(err))
	return exitError
}

// configError restituisce l'errore di config.Load della prima variabile di
// names (nell'ordine in cui Python le converte) con un valore non valido, o
// nil. In Python ogni script converte solo le variabili che usa, quindi un
// valore non valido ferma solo quegli script. Il messaggio è quello di
// int() ("invalid literal for int() with base 10: 'x'"); per i valori che
// int() accetterebbe ma config rifiuta (oltre int64) resta quello di config.
func configError(err error, names ...string) error {
	if err == nil {
		return nil
	}
	var errs []error
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	} else {
		errs = []error{err}
	}
	for _, name := range names {
		for _, e := range errs {
			if strings.HasPrefix(e.Error(), name+" non valido") {
				if _, intErr := pycompat.ParseInt(os.Getenv(name)); intErr != nil {
					return intErr
				}
				return e
			}
		}
	}
	return nil
}

// botIntVars sono le variabili intere convertite all'avvio di bot.py,
// nell'ordine di esecuzione: CHECKER_HOUR di handlers/commands.py (importato
// in cima a bot.py), poi quelle di bot.py.
var botIntVars = []string{"CHECKER_HOUR", "SCRAPER_HOUR", "REMINDER_HOUR", "SCRAPER_TIMEOUT_SECONDS", "WEBHOOK_PORT", "HEALTH_PORT"}

// runServe avvia il bot (python bot.py): logging su console e su file con
// rotazione giornaliera in DATA_DIR, poi app.Serve fino al segnale di stop.
func runServe(ctx context.Context, cfg config.Config, cfgErr error, s streams) int {
	if err := configError(cfgErr, botIntVars...); err != nil {
		fmt.Fprintf(s.stderr, "ValueError: %v\n", err)
		return exitError
	}
	closeLog, err := logging.Setup(cfg.LogLevel, cfg.DataDir)
	if err != nil {
		fmt.Fprintln(s.stderr, err)
		return exitError
	}
	defer func() { _ = closeLog() }()

	if err := app.Serve(ctx, cfg, app.ServeOptions{}); err != nil {
		return fail(err, s)
	}
	return exitOK
}

// openStore apre il database in <DATA_DIR>/octotracker.db.
func openStore(cfg config.Config) (*database.Store, error) {
	return database.Open(filepath.Join(cfg.DataDir, database.DBFileName))
}

// runScript esegue checker.py o reminder.py standalone: logging.basicConfig
// con "%(asctime)s - %(levelname)s - %(message)s" su stderr al livello
// LOG_LEVEL.
func runScript(ctx context.Context, cfg config.Config, s streams, main func(*database.Store) error) int {
	slog.SetDefault(slog.New(app.NewScriptHandler(s.stderr, logging.ParseLevel(cfg.LogLevel), app.FormatChecker)))
	// main() controlla il token prima di qualunque accesso al database
	if cfg.BotToken == "" {
		fmt.Fprintf(s.stderr, "ValueError: %v\n", checker.ErrMissingToken)
		return exitError
	}
	store, err := openStore(cfg)
	if err != nil {
		return fail(err, s)
	}
	defer func() { _ = store.Close() }()
	if err := main(store); err != nil {
		return fail(err, s)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err(), s)
	}
	return exitOK
}

// runScrape esegue data_reader.py standalone: logging "%(message)s" su
// stderr, init_db, lettura delle tariffe e stampa del risultato.
func runScrape(ctx context.Context, cfg config.Config, s streams) int {
	slog.SetDefault(slog.New(scraper.NewBasicHandler(s.stderr, logging.ParseLevel(cfg.LogLevel), scraper.ScrapeLogTimeLayout)))
	store, err := openStore(cfg)
	if err != nil {
		return fail(err, s)
	}
	defer func() { _ = store.Close() }()
	if err := scraper.RunScrape(ctx, store); err != nil {
		return fail(err, s)
	}
	return exitOK
}

// runBroadcast esegue broadcast.py: logging INFO con
// "%(asctime)s - %(name)s - %(levelname)s - %(message)s" su stderr.
func runBroadcast(ctx context.Context, cfg config.Config, cfgErr error, args []string, s streams) int {
	slog.SetDefault(slog.New(app.NewScriptHandler(s.stderr, slog.LevelInfo, app.FormatBroadcast)))
	// int(os.getenv("BROADCAST_BATCH_SIZE")) avviene dopo il controllo del token
	if cfg.BotToken != "" {
		if err := configError(cfgErr, "BROADCAST_BATCH_SIZE"); err != nil {
			fmt.Fprintf(s.stderr, "ValueError: %v\n", err)
			return exitError
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

// runBackfill esegue backfill_rate_history.py: argomenti come argparse,
// logging "%(asctime)s %(message)s" (datefmt %H:%M:%S) su stderr al livello
// --log-level.
func runBackfill(ctx context.Context, cfg config.Config, args []string, s streams) int {
	parsed, code, done := parseBackfillArgs(args, s.stdout, s.stderr)
	if done {
		return code
	}
	slog.SetDefault(slog.New(scraper.NewBasicHandler(s.stderr, logging.ParseLevel(parsed.logLevel), scraper.BackfillLogTimeLayout)))
	store, err := openStore(cfg)
	if err != nil {
		return fail(err, s)
	}
	defer func() { _ = store.Close() }()
	opts := scraper.DefaultBackfillOptions()
	opts.Days = parsed.days
	opts.DryRun = parsed.dryRun
	opts.Delay = parsed.delay
	if err := scraper.Backfill(ctx, store, opts); err != nil {
		return fail(err, s)
	}
	return exitOK
}

// runHealthcheck interroga /health del server locale: esce con 0 solo se
// la risposta è HTTP 200 (healthy o degraded), così l'HEALTHCHECK Docker
// funziona anche in un'immagine senza shell né curl. Il corpo della
// risposta viene stampato su stdout.
func runHealthcheck(ctx context.Context, cfg config.Config, cfgErr error, s streams) int {
	if err := configError(cfgErr, "HEALTH_PORT"); err != nil {
		fmt.Fprintf(s.stderr, "ValueError: %v\n", err)
		return exitError
	}
	ctx, cancel := context.WithTimeout(ctx, HealthcheckTimeout)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/health", cfg.HealthPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(s.stderr, err)
		return exitError
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(s.stderr, err)
		return exitError
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(s.stdout, resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(s.stderr, "\nhealthcheck fallito: HTTP %d\n", resp.StatusCode)
		return exitError
	}
	return exitOK
}
