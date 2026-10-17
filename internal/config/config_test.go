package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dstmrk/octotracker/internal/logging"
)

var configVars = []string{
	"TELEGRAM_BOT_TOKEN", "WEBHOOK_URL", "WEBHOOK_PORT", "WEBHOOK_SECRET", "HEALTH_PORT",
	"SCRAPER_HOUR", "CHECKER_HOUR", "REMINDER_HOUR", "SCRAPER_TIMEOUT_SECONDS", "ADMIN_USER_ID",
	"WEBAPP_URL", "TELEGRAM_CHANNEL", "LOG_LEVEL", "BROADCAST_BATCH_SIZE", "DATA_DIR",
}

// clearConfigEnv rimuove le variabili di configurazione per la durata del test.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range configVars {
		unsetEnv(t, name)
	}
}

// unsetEnv rimuove una variabile ripristinandola a fine test.
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "") // registra il ripristino
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		WebhookPort:           8443,
		HealthPort:            8444,
		ScraperHour:           9,
		CheckerHour:           10,
		ReminderHour:          11,
		ScraperTimeoutSeconds: 600,
		LogLevel:              "INFO",
		BroadcastBatchSize:    10,
		DataDir:               "data",
	}
	if cfg != want {
		t.Fatalf("Load() = %+v\nwant %+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearConfigEnv(t)
	env := map[string]string{
		"TELEGRAM_BOT_TOKEN":      "123:abc",
		"WEBHOOK_URL":             "https://example.org",
		"WEBHOOK_PORT":            "9443",
		"WEBHOOK_SECRET":          "s3cret",
		"HEALTH_PORT":             " 9444 ",
		"SCRAPER_HOUR":            "+7",
		"CHECKER_HOUR":            "1_2",
		"REMINDER_HOUR":           "١٣", // cifre arabe: int() di Python le accetta
		"SCRAPER_TIMEOUT_SECONDS": "60",
		"ADMIN_USER_ID":           "42",
		"WEBAPP_URL":              "https://example.org/app/",
		"TELEGRAM_CHANNEL":        "  @canale  ",
		"LOG_LEVEL":               "debug",
		"BROADCAST_BATCH_SIZE":    "25",
		"DATA_DIR":                "/tmp/x",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		BotToken:              "123:abc",
		WebhookURL:            "https://example.org",
		WebhookPort:           9443,
		WebhookSecret:         "s3cret",
		HealthPort:            9444,
		ScraperHour:           7,
		CheckerHour:           12,
		ReminderHour:          13,
		ScraperTimeoutSeconds: 60,
		AdminUserID:           "42",
		WebappURL:             "https://example.org/app/",
		TelegramChannel:       "@canale",
		LogLevel:              "DEBUG",
		BroadcastBatchSize:    25,
		DataDir:               "/tmp/x",
	}
	if cfg != want {
		t.Fatalf("Load() = %+v\nwant %+v", cfg, want)
	}
}

func TestLoadInvalidInts(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("WEBHOOK_PORT", "abc")
	t.Setenv("CHECKER_HOUR", "1__0")
	t.Setenv("HEALTH_PORT", "   ") // vuoto: default
	cfg, err := Load()
	if err == nil {
		t.Fatal("Load deve fallire con interi non validi")
	}
	msg := err.Error()
	if !strings.Contains(msg, `WEBHOOK_PORT non valido: "abc"`) || !strings.Contains(msg, `CHECKER_HOUR non valido: "1__0"`) {
		t.Errorf("errore = %q", msg)
	}
	if cfg.WebhookPort != 8443 || cfg.CheckerHour != 10 || cfg.HealthPort != 8444 {
		t.Errorf("i valori non validi devono ricadere sul default: %+v", cfg)
	}
}

func TestPyAtoi(t *testing.T) {
	// Risultati verificati con int() di Python 3.13
	valid := map[string]int{
		" 8443 ": 8443, "+5": 5, "-3": -3, "1_000": 1000, "٣": 3, "010": 10,
		" 5　": 5, "1_2_3": 123, "１２": 12, "\t7\n": 7,
	}
	for in, want := range valid {
		got, err := pyAtoi(in)
		if err != nil || got != want {
			t.Errorf("pyAtoi(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	invalid := []string{
		"1__0", "_1", "1_", "", " ", "8443a", "0x10", "1 2", "\x1c7\x1f", "+", "-_1", "+-1",
		"99999999999999999999999",
	}
	for _, in := range invalid {
		if got, err := pyAtoi(in); err == nil {
			t.Errorf("pyAtoi(%q) = %d, want errore", in, got)
		} else if !strings.HasPrefix(err.Error(), "invalid literal for int() with base 10") {
			t.Errorf("pyAtoi(%q) errore = %v", in, err)
		}
	}
}

func TestValidateBot(t *testing.T) {
	ok := Config{BotToken: "t", WebhookSecret: "s", WebhookURL: "https://x"}
	if err := ok.ValidateBot(); err != nil {
		t.Fatalf("config valida: %v", err)
	}

	noSecret := ok
	noSecret.WebhookSecret = ""
	if err := noSecret.ValidateBot(); !errors.Is(err, ErrMissingWebhookSecret) {
		t.Errorf("senza secret: %v", err)
	}

	// Come in Python il secret viene controllato prima del token
	nothing := Config{}
	if err := nothing.ValidateBot(); !errors.Is(err, ErrMissingWebhookSecret) {
		t.Errorf("config vuota: %v", err)
	}

	noToken := ok
	noToken.BotToken = ""
	if err := noToken.ValidateBot(); err == nil || err.Error() != "TELEGRAM_BOT_TOKEN non impostato" {
		t.Errorf("senza token: %v", err)
	}

	noURL := ok
	noURL.WebhookURL = ""
	if err := noURL.ValidateBot(); err == nil || err.Error() != "WEBHOOK_URL richiesto" {
		t.Errorf("senza URL: %v", err)
	}

	if !strings.HasPrefix(ErrMissingWebhookSecret.Error(), "WEBHOOK_SECRET è obbligatorio per sicurezza del webhook.\n") {
		t.Errorf("messaggio secret = %q", ErrMissingWebhookSecret.Error())
	}
}

// ========== LoadDotEnv ==========

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(logging.NewTextHandler(&buf, slog.LevelDebug)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return &buf
}

func environMap() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

func TestLoadDotEnvBasics(t *testing.T) {
	unsetEnv(t, "PYTHON_DOTENV_DISABLED")
	unsetEnv(t, "OTDT_QUOTED")
	unsetEnv(t, "OTDT_EXPORT")
	unsetEnv(t, "OTDT_COMMENT")
	t.Setenv("OTDT_EXISTING", "dall_ambiente")

	path := writeEnvFile(t, "# commento\n"+
		"export OTDT_EXPORT=1\n"+
		"OTDT_QUOTED=\"a # b\"\n"+
		"OTDT_COMMENT=valore # commento\n"+
		"OTDT_EXISTING=dal_file\n")
	if err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"OTDT_EXPORT":   "1",
		"OTDT_QUOTED":   "a # b",
		"OTDT_COMMENT":  "valore",
		"OTDT_EXISTING": "dall_ambiente", // nessun override
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestLoadDotEnvMissingFileAndDirectory(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "manca.env")); err != nil {
		t.Errorf("file assente: %v", err)
	}
	if err := LoadDotEnv(t.TempDir()); err != nil {
		t.Errorf("directory: %v", err)
	}
}

func TestLoadDotEnvInvalidUTF8(t *testing.T) {
	unsetEnv(t, "PYTHON_DOTENV_DISABLED")
	path := writeEnvFile(t, "OTDT_X=\xff\xfe\n")
	if err := LoadDotEnv(path); err == nil {
		t.Fatal("un file non UTF-8 deve produrre errore")
	}
}

func TestLoadDotEnvUnreadable(t *testing.T) {
	unsetEnv(t, "PYTHON_DOTENV_DISABLED")
	if os.Geteuid() == 0 {
		t.Skip("root può leggere qualsiasi file")
	}
	path := writeEnvFile(t, "OTDT_X=1\n")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(path); err == nil {
		t.Fatal("un file illeggibile deve produrre errore")
	}
}

func TestLoadDotEnvInvalidKey(t *testing.T) {
	unsetEnv(t, "PYTHON_DOTENV_DISABLED")
	// Come os.environ["A=B"] in Python, una chiave con "=" non è impostabile
	path := writeEnvFile(t, "'OTDT_A=B'=1\n")
	if err := LoadDotEnv(path); err == nil {
		t.Fatal("una chiave con '=' deve produrre errore")
	}
}

func TestLoadDotEnvDisabled(t *testing.T) {
	path := writeEnvFile(t, "OTDT_DISABLED=1\n")
	for _, v := range []string{"1", "true", "TRUE", "t", "Yes", "y"} {
		unsetEnv(t, "OTDT_DISABLED")
		t.Setenv("PYTHON_DOTENV_DISABLED", v)
		if err := LoadDotEnv(path); err != nil {
			t.Fatal(err)
		}
		if _, ok := os.LookupEnv("OTDT_DISABLED"); ok {
			t.Errorf("PYTHON_DOTENV_DISABLED=%q: il file non doveva essere caricato", v)
		}
	}
	for _, v := range []string{"0", "false", "no", ""} {
		unsetEnv(t, "OTDT_DISABLED")
		t.Setenv("PYTHON_DOTENV_DISABLED", v)
		if err := LoadDotEnv(path); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv("OTDT_DISABLED"); got != "1" {
			t.Errorf("PYTHON_DOTENV_DISABLED=%q: OTDT_DISABLED = %q", v, got)
		}
	}
}

type dotenvCase struct {
	Name     string            `json:"name"`
	Preset   map[string]string `json:"preset"`
	Content  string            `json:"content"`
	Expected map[string]string `json:"expected"`
	Warnings []string          `json:"warnings"`
}

// TestLoadDotEnvGolden confronta LoadDotEnv con load_dotenv() di
// python-dotenv su file .env generati ad hoc (testdata/dotenv_golden.json).
func TestLoadDotEnvGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/dotenv_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []dotenvCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 25 {
		t.Fatalf("golden troppo piccolo: %d casi", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			unsetEnv(t, "PYTHON_DOTENV_DISABLED")
			for k, v := range c.Preset {
				t.Setenv(k, v)
			}
			before := environMap()
			logs := captureLogs(t)
			path := writeEnvFile(t, c.Content)

			err := LoadDotEnv(path)

			after := environMap()
			got := map[string]string{}
			for k, v := range after {
				if _, ok := before[k]; !ok {
					got[k] = v
					_ = os.Unsetenv(k)
				}
			}
			if err != nil {
				t.Fatalf("LoadDotEnv: %v", err)
			}
			if len(got) != len(c.Expected) {
				t.Errorf("variabili impostate = %q, want %q", got, c.Expected)
			}
			for k, want := range c.Expected {
				if g, ok := got[k]; !ok || g != want {
					t.Errorf("%q = %q (presente=%v), want %q", k, g, ok, want)
				}
			}
			var warnings []string
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				if _, msg, ok := strings.Cut(line, " - dotenv.main - WARNING - "); ok {
					warnings = append(warnings, msg)
				}
			}
			if strings.Join(warnings, "|") != strings.Join(c.Warnings, "|") {
				t.Errorf("warning = %q, want %q", warnings, c.Warnings)
			}
		})
	}
}

func TestDigitValue(t *testing.T) {
	for r, want := range map[rune]int{'0': 0, '9': 9, '٣': 3, '١': 1, '０': 0, '９': 9, '७': 7} {
		if got := digitValue(r); got != want {
			t.Errorf("digitValue(%q) = %d, want %d", r, got, want)
		}
	}
}
