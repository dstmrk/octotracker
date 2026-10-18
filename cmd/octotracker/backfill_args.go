package main

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/scraper"
)

// backfillProg è il nome del programma nei messaggi di argparse.
const backfillProg = "octotracker backfill"

// backfillUsage è la riga di uso generata da argparse per backfill.
const backfillUsage = "usage: octotracker backfill [-h] [--days DAYS] [--dry-run] [--delay DELAY]\n" +
	"                            [--log-level {DEBUG,INFO,WARNING,ERROR}]\n"

// backfillHelp è il testo di -h/--help.
const backfillHelp = backfillUsage + `
Backfill storico tariffe Octopus Energy da ARERA

options:
  -h, --help            show this help message and exit
  --days DAYS           Numero di giorni indietro da scaricare (default: 365)
  --dry-run             Simula senza scrivere nel database
  --delay DELAY         Secondi di attesa tra richieste HTTP (default: 0.5)
  --log-level {DEBUG,INFO,WARNING,ERROR}
                        Livello di logging (default: INFO)
`

// backfillLogLevels sono le choices di --log-level.
var backfillLogLevels = []string{"DEBUG", "INFO", "WARNING", "ERROR"}

// backfillOptions sono le opzioni lunghe del parser (per le abbreviazioni).
var backfillOptions = []string{"--help", "--days", "--dry-run", "--delay", "--log-level"}

// negativeNumber è _negative_number_matcher di argparse: un valore che
// sembra un numero negativo non è un'opzione.
var negativeNumber = regexp.MustCompile(`^-\d+$|^-\d*\.\d+$`)

// backfillArgs sono gli argomenti di backfill_rate_history.py.
type backfillArgs struct {
	days     int
	dryRun   bool
	delay    float64
	logLevel string
}

// argError è un errore di argparse (parser.error).
type argError string

func (e argError) Error() string { return string(e) }

// parseBackfillArgs replica parser.parse_args() di backfill_rate_history.py:
// --days (int), --dry-run, --delay (float), --log-level (choices), con le
// abbreviazioni univoche delle opzioni lunghe. Se done è true il comando
// deve terminare con code (help stampato su stdout con 0, oppure uso ed
// errore su stderr con 2, come argparse).
func parseBackfillArgs(args []string, stdout, stderr io.Writer) (parsed backfillArgs, code int, done bool) {
	parsed, help, err := parseBackfill(args)
	switch {
	case help:
		fmt.Fprint(stdout, backfillHelp)
		return parsed, exitOK, true
	case err != nil:
		fmt.Fprintf(stderr, "%s%s: error: %s\n", backfillUsage, backfillProg, err)
		return parsed, exitUsage, true
	}
	return parsed, exitOK, false
}

// parseBackfill analizza args; help è true per -h/--help.
func parseBackfill(args []string) (parsed backfillArgs, help bool, err error) {
	parsed = backfillArgs{days: scraper.DefaultBackfillDays, delay: scraper.DefaultBackfillDelay, logLevel: "INFO"}
	var extras []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			// tutto ciò che segue è posizionale; argparse (3.13) riporta anche "--"
			extras = append(extras, args[i:]...)
			break
		}
		if arg == "-h" {
			return parsed, true, nil
		}
		if !strings.HasPrefix(arg, "--") {
			extras = append(extras, arg)
			continue
		}

		name, explicit, hasExplicit := strings.Cut(arg, "=")
		option, err := matchOption(name)
		if err != nil {
			return parsed, false, err
		}
		if option == "" {
			extras = append(extras, arg)
			continue
		}

		switch option {
		case "--help":
			return parsed, true, nil
		case "--dry-run":
			if hasExplicit {
				return parsed, false, argError(fmt.Sprintf("argument --dry-run: ignored explicit argument %s", pycompat.StrRepr(explicit)))
			}
			parsed.dryRun = true
			continue
		}

		// opzioni con un valore
		value := explicit
		if !hasExplicit {
			if i+1 >= len(args) || isOptionLike(args[i+1]) {
				return parsed, false, argError(fmt.Sprintf("argument %s: expected one argument", option))
			}
			i++
			value = args[i]
		}
		if err := parsed.set(option, value); err != nil {
			return parsed, false, err
		}
	}
	if len(extras) > 0 {
		return parsed, false, argError("unrecognized arguments: " + strings.Join(extras, " "))
	}
	return parsed, false, nil
}

// matchOption risolve un'opzione lunga, anche abbreviata: "" se non
// corrisponde a nessuna opzione, errore se l'abbreviazione è ambigua.
func matchOption(name string) (string, error) {
	var matches []string
	for _, o := range backfillOptions {
		if o == name {
			return o, nil
		}
		if strings.HasPrefix(o, name) {
			matches = append(matches, o)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", argError(fmt.Sprintf("ambiguous option: %s could match %s", name, strings.Join(matches, ", ")))
	}
}

// isOptionLike indica se arg verrebbe interpretato da argparse come
// un'opzione (e quindi non come valore dell'opzione precedente).
func isOptionLike(arg string) bool {
	return strings.HasPrefix(arg, "-") && arg != "-" && !negativeNumber.MatchString(arg)
}

// set converte e assegna il valore di un'opzione (type=int, type=float,
// choices).
func (a *backfillArgs) set(option, value string) error {
	switch option {
	case "--days":
		n, err := pycompat.ParseInt(value)
		if err != nil {
			return argError(fmt.Sprintf("argument --days: invalid int value: %s", pycompat.StrRepr(value)))
		}
		// Python ha interi illimitati: oltre int64 il backfill solleverebbe
		// comunque OverflowError sulle date
		switch {
		case n.IsInt64():
			a.days = int(n.Int64())
		case n.Sign() > 0:
			a.days = math.MaxInt
		default:
			a.days = math.MinInt
		}
	case "--delay":
		f, err := scraper.ParseFloat(value)
		if err != nil {
			return argError(fmt.Sprintf("argument --delay: invalid float value: %s", pycompat.StrRepr(value)))
		}
		a.delay = f
	case "--log-level":
		for _, l := range backfillLogLevels {
			if l == value {
				a.logLevel = value
				return nil
			}
		}
		quoted := make([]string, len(backfillLogLevels))
		for i, l := range backfillLogLevels {
			quoted[i] = pycompat.StrRepr(l)
		}
		return argError(fmt.Sprintf("argument --log-level: invalid choice: %s (choose from %s)",
			pycompat.StrRepr(value), strings.Join(quoted, ", ")))
	}
	return nil
}
