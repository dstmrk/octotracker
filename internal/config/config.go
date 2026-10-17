// Package config carica la configurazione di OctoTracker dalle variabili d'ambiente.
//
// I nomi delle variabili e i valori di default sono identici a quelli della
// versione Python, così il file .env e il docker-compose.yml esistenti
// continuano a funzionare senza modifiche.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// Config contiene tutti i parametri di runtime.
type Config struct {
	BotToken              string // TELEGRAM_BOT_TOKEN
	WebhookURL            string // WEBHOOK_URL
	WebhookPort           int    // WEBHOOK_PORT (default 8443)
	WebhookSecret         string // WEBHOOK_SECRET (obbligatorio per il bot)
	HealthPort            int    // HEALTH_PORT (default 8444)
	ScraperHour           int    // SCRAPER_HOUR (default 9)
	CheckerHour           int    // CHECKER_HOUR (default 10)
	ReminderHour          int    // REMINDER_HOUR (default 11)
	ScraperTimeoutSeconds int    // SCRAPER_TIMEOUT_SECONDS (default 600)
	AdminUserID           string // ADMIN_USER_ID (opzionale)
	WebappURL             string // WEBAPP_URL (opzionale)
	TelegramChannel       string // TELEGRAM_CHANNEL (opzionale, già trimmato)
	LogLevel              string // LOG_LEVEL (default INFO, maiuscolo)
	BroadcastBatchSize    int    // BROADCAST_BATCH_SIZE (default 10)
	DataDir               string // DATA_DIR (default "data")
}

// ErrMissingWebhookSecret viene restituito da ValidateBot se WEBHOOK_SECRET manca.
var ErrMissingWebhookSecret = errors.New(
	"WEBHOOK_SECRET è obbligatorio per sicurezza del webhook.\n" +
		"Genera un token sicuro con:\n" +
		"  openssl rand -base64 32\n" +
		"Poi aggiungilo al file .env:\n" +
		"  WEBHOOK_SECRET=<token_generato>",
)

// Load legge la configurazione dall'ambiente. Non valida i campi obbligatori:
// ogni sottocomando chiama la validazione che gli serve.
func Load() (Config, error) {
	var errs []error
	intVar := func(name string, def int) int {
		raw := os.Getenv(name)
		if strings.TrimFunc(raw, pyIntSpace) == "" {
			// Python solleverebbe ValueError su int(""): qui si usa il default
			return def
		}
		v, err := pyAtoi(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s non valido: %q", name, raw))
			return def
		}
		return v
	}

	cfg := Config{
		BotToken:              os.Getenv("TELEGRAM_BOT_TOKEN"),
		WebhookURL:            os.Getenv("WEBHOOK_URL"),
		WebhookPort:           intVar("WEBHOOK_PORT", 8443),
		WebhookSecret:         os.Getenv("WEBHOOK_SECRET"),
		HealthPort:            intVar("HEALTH_PORT", 8444),
		ScraperHour:           intVar("SCRAPER_HOUR", 9),
		CheckerHour:           intVar("CHECKER_HOUR", 10),
		ReminderHour:          intVar("REMINDER_HOUR", 11),
		ScraperTimeoutSeconds: intVar("SCRAPER_TIMEOUT_SECONDS", 600),
		AdminUserID:           os.Getenv("ADMIN_USER_ID"),
		WebappURL:             os.Getenv("WEBAPP_URL"),
		TelegramChannel:       strings.TrimFunc(os.Getenv("TELEGRAM_CHANNEL"), pyIsSpace),
		LogLevel:              strings.ToUpper(envOr("LOG_LEVEL", "INFO")),
		BroadcastBatchSize:    intVar("BROADCAST_BATCH_SIZE", 10),
		DataDir:               envOr("DATA_DIR", "data"),
	}
	return cfg, errors.Join(errs...)
}

// ValidateBot verifica i campi obbligatori per avviare il bot in modalità
// webhook, nello stesso ordine della versione Python (WEBHOOK_SECRET è
// controllato all'import di bot.py, prima del token).
func (c Config) ValidateBot() error {
	if c.WebhookSecret == "" {
		return ErrMissingWebhookSecret
	}
	if c.BotToken == "" {
		return errors.New("TELEGRAM_BOT_TOKEN non impostato")
	}
	if c.WebhookURL == "" {
		return errors.New("WEBHOOK_URL richiesto")
	}
	return nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// pyIsSpace replica str.isspace() di Python (usato da strip() e int()).
func pyIsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// pyIntSpace indica i caratteri ignorati ai lati da int() di Python: gli
// spazi ASCII " \t\n\v\f\r" e gli spazi Unicode non ASCII (U+001C..U+001F
// invece non sono ammessi, a differenza di str.strip()).
func pyIntSpace(r rune) bool {
	return r == ' ' || (r >= '\t' && r <= '\r') || (r >= 0x80 && pyIsSpace(r))
}

// pyAtoi replica int(s) di Python in base 10: spazi ai lati, segno
// opzionale, cifre decimali Unicode separate al più da singoli underscore
// (es. " 8_443 ").
func pyAtoi(s string) (int, error) {
	t := strings.TrimFunc(s, pyIntSpace)
	invalid := fmt.Errorf("invalid literal for int() with base 10: %q", s)
	var b strings.Builder
	if t != "" && (t[0] == '+' || t[0] == '-') {
		b.WriteByte(t[0])
		t = t[1:]
	}
	prevDigit := false
	for _, r := range t {
		switch {
		case r == '_' && prevDigit:
			prevDigit = false
		case unicode.IsDigit(r):
			b.WriteByte(byte('0' + digitValue(r)))
			prevDigit = true
		default:
			return 0, invalid
		}
	}
	if !prevDigit {
		return 0, invalid // vuoto o underscore finale
	}
	v, err := strconv.Atoi(b.String())
	if err != nil {
		return 0, invalid
	}
	return v, nil
}

// digitValue restituisce il valore di una cifra Unicode (categoria Nd): le
// cifre sono assegnate in blocchi contigui da 0 a 9.
func digitValue(r rune) int {
	if r >= '0' && r <= '9' {
		return int(r - '0')
	}
	n := 0
	for unicode.IsDigit(r - rune(n) - 1) {
		n++
	}
	return n % 10
}
