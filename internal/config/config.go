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

// ErrMissingWebhookSecret è l'errore di avvio del bot quando WEBHOOK_SECRET manca.
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
		if strings.TrimSpace(raw) == "" {
			return def
		}
		v, err := strconv.Atoi(strings.TrimSpace(raw))
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
		TelegramChannel:       strings.TrimSpace(os.Getenv("TELEGRAM_CHANNEL")),
		LogLevel:              strings.ToUpper(envOr("LOG_LEVEL", "INFO")),
		BroadcastBatchSize:    intVar("BROADCAST_BATCH_SIZE", 10),
		DataDir:               envOr("DATA_DIR", "data"),
	}
	return cfg, errors.Join(errs...)
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
