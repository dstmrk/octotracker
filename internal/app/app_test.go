package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/config"
	"github.com/dstmrk/octotracker/internal/scraper"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// serveConfig è una configurazione completa per Serve.
func serveConfig(t *testing.T) config.Config {
	return config.Config{
		BotToken:              testToken,
		WebhookURL:            "https://octo.example.com",
		WebhookPort:           8443,
		WebhookSecret:         testSecret,
		HealthPort:            8444,
		ScraperHour:           9,
		CheckerHour:           10,
		ReminderHour:          11,
		ScraperTimeoutSeconds: 600,
		LogLevel:              "INFO",
		DataDir:               filepath.Join(t.TempDir(), "data"),
	}
}

// fakeOperations sostituisce le operazioni dello scheduler.
func fakeOperations(s *Scheduler) {
	s.FetchTariffe = func(context.Context) (*scraper.Tariffe, error) { return &scraper.Tariffe{}, nil }
	s.CheckAndNotify = func(context.Context) error { return nil }
	s.SendReminders = func(context.Context) error { return nil }
}

// TestServeConfigErrors: errori di configurazione nell'ordine dei controlli.
func TestServeConfigErrors(t *testing.T) {
	logs := captureLogs(t)

	cfg := serveConfig(t)
	cfg.WebhookSecret = ""
	err := Serve(context.Background(), cfg, ServeOptions{})
	if !errors.Is(err, config.ErrMissingWebhookSecret) {
		t.Errorf("senza WEBHOOK_SECRET: %v", err)
	}

	cfg = serveConfig(t)
	cfg.BotToken = ""
	if err := Serve(context.Background(), cfg, ServeOptions{}); err == nil || err.Error() != "TELEGRAM_BOT_TOKEN non impostato" {
		t.Errorf("senza token: %v", err)
	}
	if _, err := os.Stat(cfg.DataDir); !os.IsNotExist(err) {
		t.Error("database creato prima dei controlli")
	}

	b, fc := testutil.NewBot(t)
	cfg = serveConfig(t)
	cfg.WebhookURL = ""
	if err := Serve(context.Background(), cfg, ServeOptions{Bot: b}); err == nil || err.Error() != "WEBHOOK_URL richiesto" {
		t.Errorf("senza WEBHOOK_URL: %v", err)
	}
	if len(fc.Calls()) != 0 {
		t.Errorf("chiamate API prima della verifica di WEBHOOK_URL: %v", fc.Calls())
	}
	want := []string{
		"database - INFO - ✅ Database inizializzato",
		"__main__ - INFO - 💾 Database inizializzato",
		"__main__ - INFO - 🤖 Avvio OctoTracker...",
		"__main__ - INFO - 📡 Modalità: WEBHOOK",
		"__main__ - INFO - ⏰ Scraper schedulato: 9:00",
		"__main__ - INFO - ⏰ Checker schedulato: 10:00",
		"__main__ - INFO - ⏰ Reminder schedulato: 11:00",
		"__main__ - INFO - 🌐 Webhook URL: ",
		"__main__ - INFO - 🔌 Porta webhook: 8443",
		"__main__ - INFO - 🏥 Porta health check: 8444",
		"__main__ - INFO - ✅ Bot configurato!",
	}
	assertLogOrder(t, logs.String(), want)
	assertLogNotContains(t, logs, "🚀 Avvio webhook")

	// Database non apribile
	cfg = serveConfig(t)
	cfg.DataDir = filepath.Join(t.TempDir(), "x?y")
	if err := Serve(context.Background(), cfg, ServeOptions{Bot: b}); err == nil {
		t.Error("atteso errore di apertura del database")
	}
}

// assertLogOrder verifica che le righe compaiano nel log in quest'ordine.
func assertLogOrder(t *testing.T, logs string, lines []string) {
	t.Helper()
	pos := 0
	for _, line := range lines {
		i := strings.Index(logs[pos:], line)
		if i < 0 {
			t.Fatalf("log senza %q (dopo la posizione %d):\n%s", line, pos, logs)
		}
		pos += i + len(line)
	}
}

// TestServeLifecycle avvia il bot completo su socket locali: bootstrap
// (getMe, deleteWebhook, setWebhook), update dal webhook elaborato,
// /health e Mini App sul server HTTP, spegnimento ordinato alla
// cancellazione del contesto.
func TestServeLifecycle(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	cfg := serveConfig(t)
	cfg.BotToken = b.Token
	ll := &localListen{}
	ready := make(chan net.Addr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, cfg, ServeOptions{
			Bot:                b,
			Listen:             ll.listen,
			Sleep:              func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
			ConfigureScheduler: fakeOperations,
			Ready:              func(addr net.Addr) { ready <- addr },
		})
	}()

	var webhookAddr net.Addr
	select {
	case webhookAddr = <-ready:
	case err := <-done:
		t.Fatalf("Serve terminato: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout in attesa dell'avvio")
	}

	// Bootstrap
	if got := methods(fc.Calls()); got != "getMe,deleteWebhook,setWebhook" {
		t.Fatalf("chiamate di avvio = %s", got)
	}
	if url := fc.LastCall(t, "setWebhook").String("url"); url != "https://octo.example.com/"+b.Token {
		t.Errorf("webhook_url = %q", url)
	}
	if ll.addr("0.0.0.0:8443") == "" || ll.addr("0.0.0.0:8444") == "" {
		t.Fatalf("socket aperti = %v", ll.addrs)
	}

	// Update dal webhook: /status viene elaborato dal dispatcher
	req, _ := http.NewRequest(http.MethodPost, "http://"+webhookAddr.String()+"/"+b.Token, strings.NewReader(updateBodyText(10, "/status")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SecretTokenHeader, testSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook: status %d", resp.StatusCode)
	}
	waitFor(t, "risposta del bot a /status", func() bool { return len(fc.CallsTo("sendMessage")) == 1 })

	// Secret errato → 403
	req, _ = http.NewRequest(http.MethodPost, "http://"+webhookAddr.String()+"/"+b.Token, strings.NewReader(updateBody(11)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SecretTokenHeader, "sbagliato")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("secret errato: status %d", resp.StatusCode)
	}

	// /health: task attivi, nessuna tariffa → degraded (200)
	healthURL := "http://" + ll.addr("0.0.0.0:8444")
	resp, err = http.Get(healthURL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status": "degraded"`) ||
		!strings.Contains(string(body), `"scraper": "running"`) {
		t.Errorf("/health: %d %s", resp.StatusCode, body)
	}
	resp, err = http.Get(healthURL + "/app/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("/app/: %d %v", resp.StatusCode, resp.Header)
	}

	// Spegnimento
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout in attesa dello spegnimento")
	}

	calls := fc.CallsTo("sendMessage")
	if len(calls) != 1 || !strings.HasPrefix(calls[0].Text(), msgNotRegistered) {
		t.Errorf("risposta a /status = %v", calls)
	}
	assertLogOrder(t, logs.String(), []string{
		"🚀 Avvio webhook su https://octo.example.com...",
		"🕷️  Scraper schedulato per le 9:00",
		"🔍 Checker schedulato per le 10:00",
		"📅 Reminder schedulato per le 11:00",
		"🏥 Health check endpoint attivo su porta 8444",
		"📊 API Mini App attiva su porta 8444/api/",
		"🌐 WebApp Mini App attiva su porta 8444/app/",
	})
	for _, name := range []string{TaskScraperDaily, TaskCheckerDaily, TaskReminderDaily, TaskHealthServer} {
		assertLogContains(t, logs, "Task "+name+" cancellato")
	}
	if _, err := net.Dial("tcp", webhookAddr.String()); err == nil {
		t.Error("il webhook accetta ancora connessioni")
	}
}

// TestServeBootstrapFailures: getMe con token rifiutato e setWebhook che
// fallisce sempre fermano l'avvio con l'errore di Telegram.
func TestServeBootstrapFailures(t *testing.T) {
	logs := captureLogs(t)
	noSleep := func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

	// Token rifiutato: nessun task avviato
	b, fc := testutil.NewBot(t)
	fc.FailMethod("getMe", testutil.TelegramError("getMe", 401, "Unauthorized"))
	cfg := serveConfig(t)
	err := Serve(context.Background(), cfg, ServeOptions{Bot: b, Sleep: noSleep, Listen: (&localListen{}).listen})
	if !isInvalidToken(err) {
		t.Errorf("errore = %v", err)
	}
	assertLogNotContains(t, logs, "Scraper schedulato per le")

	// setWebhook che fallisce: dopo 4 tentativi Serve termina e i task
	// avviati vengono cancellati
	b, fc = testutil.NewBot(t)
	fc.FailMethod("setWebhook", testutil.TelegramError("setWebhook", 400, "Bad Request: bad webhook: HTTPS url must be provided for webhook"))
	cfg = serveConfig(t)
	err = Serve(context.Background(), cfg, ServeOptions{Bot: b, Sleep: noSleep, Listen: (&localListen{}).listen, ConfigureScheduler: fakeOperations})
	if tgErr := apiError(err); tgErr == nil || tgErr.Code != 400 {
		t.Errorf("errore = %v", err)
	}
	if n := len(fc.CallsTo("setWebhook")); n != 4 {
		t.Errorf("tentativi setWebhook = %d", n)
	}
	assertLogContains(t, logs, "❌ setWebhook: fallito dopo 4 tentativi")
	assertLogContains(t, logs, "Task scraper_daily cancellato")
}

// TestServeWebhookPortInUse: un errore di bind sulla porta del webhook
// termina Serve con l'errore.
func TestServeWebhookPortInUse(t *testing.T) {
	captureLogs(t)
	b, _ := testutil.NewBot(t)
	cfg := serveConfig(t)
	cfg.BotToken = b.Token
	ll := &localListen{}
	listen := func(network, address string) (net.Listener, error) {
		if address == "0.0.0.0:8443" {
			return nil, &net.OpError{Op: "listen", Net: network, Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}
		}
		return ll.listen(network, address)
	}
	err := Serve(context.Background(), cfg, ServeOptions{Bot: b, Listen: listen, ConfigureScheduler: fakeOperations})
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Errorf("errore = %v", err)
	}
}

// TestServeWebhookServerError: se il server del webhook si ferma da solo
// Serve termina con l'errore.
func TestServeWebhookServerError(t *testing.T) {
	captureLogs(t)
	b, _ := testutil.NewBot(t)
	cfg := serveConfig(t)
	cfg.BotToken = b.Token
	ll := &localListen{}
	listen := func(network, address string) (net.Listener, error) {
		ln, err := ll.listen(network, address)
		if err == nil && address == "0.0.0.0:8443" {
			_ = ln.Close() // Serve fallirà subito
		}
		return ln, err
	}
	err := Serve(context.Background(), cfg, ServeOptions{Bot: b, Listen: listen, ConfigureScheduler: fakeOperations})
	if err == nil {
		t.Error("atteso errore del server webhook")
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("errore = %v", err)
	}
}

// TestServeStopDuringBootstrap: un segnale di stop durante i tentativi di
// avvio termina Serve senza errore (spegnimento ordinato).
func TestServeStopDuringBootstrap(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	fc.FailMethod("setWebhook", testutil.TelegramError("setWebhook", 502, "Bad Gateway"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleep := func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	err := Serve(ctx, serveConfig(t), ServeOptions{Bot: b, Sleep: sleep, Listen: (&localListen{}).listen, ConfigureScheduler: fakeOperations})
	if err != nil {
		t.Errorf("Serve = %v, atteso nil", err)
	}
	if n := len(fc.CallsTo("setWebhook")); n != 1 {
		t.Errorf("tentativi setWebhook = %d", n)
	}
	assertLogContains(t, logs, "Task checker_daily cancellato")

	// Stop durante getMe
	b, fc = testutil.NewBot(t)
	fc.FailMethod("getMe", context.DeadlineExceeded)
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := Serve(ctx, serveConfig(t), ServeOptions{Bot: b, Sleep: sleep}); err != nil {
		t.Errorf("Serve con ctx cancellato = %v", err)
	}
}
