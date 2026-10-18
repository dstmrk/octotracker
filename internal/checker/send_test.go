package checker

// Test aggiuntivi (non presenti in tests/test_checker.py) per i dettagli del
// port Go: mappatura degli errori gotgbot sulle eccezioni PTB, tastiera,
// parallelismo, errori della FASE 1 e integrazione con il database reale.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// fixedClock rende deterministici i tempi nei log ("completato in 0.00s").
func fixedClock(t *testing.T) {
	t.Helper()
	prev := now
	base := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	now = func() time.Time { return base }
	t.Cleanup(func() { now = prev })
}

type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

func TestClassifySendError(t *testing.T) {
	migrated := &gotgbot.TelegramError{Code: 400, Description: "Bad Request: group chat was upgraded to a supergroup chat",
		ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -100123}}
	tests := []struct {
		name string
		err  error
		want sendErrorKind
	}{
		{"retry_after", testutil.RetryAfterError("sendMessage", 3), kindRetryAfter},
		{"migrate_to_chat_id → ChatMigrated", migrated, kindTelegram},
		{"403 Forbidden", testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"), kindTelegram},
		{"401 InvalidToken", testutil.TelegramError("sendMessage", 401, "Unauthorized"), kindTelegram},
		{"404 InvalidToken", testutil.TelegramError("sendMessage", 404, "Not Found"), kindTelegram},
		{"409 Conflict", testutil.TelegramError("sendMessage", 409, "Conflict"), kindTelegram},
		{"400 BadRequest (sottoclasse di NetworkError)", testutil.TelegramError("sendMessage", 400, "Bad Request: chat not found"), kindNetwork},
		{"429 senza retry_after", testutil.TelegramError("sendMessage", 429, "Too Many Requests"), kindNetwork},
		{"500", testutil.TelegramError("sendMessage", 500, "Internal Server Error"), kindNetwork},
		{"502", testutil.TelegramError("sendMessage", 502, "Bad Gateway"), kindNetwork},
		{"params vuoti", &gotgbot.TelegramError{Code: 403, Description: "Forbidden", ResponseParams: &gotgbot.ResponseParameters{}}, kindTelegram},
		{"deadline del context", timeoutError(), kindTimedOut},
		{"net.Error con Timeout()", &url.Error{Op: "Post", URL: "u", Err: timeoutNetErr{}}, kindTimedOut},
		{"errore di trasporto", networkError("dial tcp: connection refused"), kindNetwork},
		{"context cancellato", fmt.Errorf("failed: %w", context.Canceled), kindNetwork},
		{"risposta non JSON", errors.New("failed to decode POST request to sendMessage: invalid character '<'"), kindNetwork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifySendError(tt.err); got != tt.want {
				t.Errorf("classifySendError = %v, atteso %v", got, tt.want)
			}
		})
	}
}

// Un vero HTTP 400 "chat not found" in PTB è BadRequest ⊂ NetworkError:
// send_notification lo intercetta nel ramo NetworkError e NON rimuove
// l'utente (bug Python replicato di proposito).
func TestSendNotificationRealBadRequestChatNotFoundIsNetworkError(t *testing.T) {
	logs := captureLogs(t)

	result, store := sendWithError(t, testutil.TelegramError("sendMessage", 400, "Bad Request: chat not found"))

	if result {
		t.Error("atteso False")
	}
	if got := store.removedIDs(); len(got) != 0 {
		t.Errorf("l'utente non deve essere rimosso: %v", got)
	}
	assertContains(t, logs.String(), "ERROR - 🌐 Errore di rete invio messaggio a 123456: Chat not found\n")
}

func TestSendNotificationChatMigratedLogsTelegramError(t *testing.T) {
	logs := captureLogs(t)
	err := &gotgbot.TelegramError{Code: 400, Description: "Bad Request: group chat was upgraded",
		ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -100123}}

	result, store := sendWithError(t, err)

	if result || len(store.removedIDs()) != 0 {
		t.Errorf("result=%v removed=%v", result, store.removedIDs())
	}
	assertContains(t, logs.String(),
		"ERROR - ❌ Errore Telegram invio messaggio a 123456: Group migrated to supergroup. New chat id: -100123\n")
}

func TestSendNotificationInvalidTokenMatchesPatterns(t *testing.T) {
	// 404/401 → InvalidToken (TelegramError): si applicano i pattern di rimozione.
	result, store := sendWithError(t, testutil.TelegramError("sendMessage", 404, "Not Found: chat not found"))
	if result {
		t.Error("atteso False")
	}
	assertRemoved(t, store)
}

func TestSendNotificationTimeoutLogsError(t *testing.T) {
	logs := captureLogs(t)

	sendWithError(t, timeoutError())

	// logger.exception: messaggio Python identico, errore in coda (traceback).
	assertContains(t, logs.String(), "ERROR - ⏱️  Timeout invio messaggio a 123456 error=failed to execute POST request to sendMessage")
}

func TestSendNotificationWithKeyboard(t *testing.T) {
	b, fc := testutil.NewBot(t)

	if !SendNotification(context.Background(), b, &fakeStore{}, "42", "<b>ciao</b>", BuildRateUpdateKeyboard()) {
		t.Fatal("atteso True")
	}
	c := fc.LastCall(t, "sendMessage")
	if c.ChatID() != "42" || c.Text() != "<b>ciao</b>" || c.ParseMode() != "HTML" {
		t.Errorf("parametri = %v", c.Params)
	}
	kb := c.InlineKeyboard()
	if len(kb) != 1 || len(kb[0]) != 2 {
		t.Fatalf("tastiera = %+v", kb)
	}
	if kb[0][0].Text != "✅ Aggiorna tariffe" || kb[0][0].CallbackData != "rate_update_yes" ||
		kb[0][1].Text != "❌ No grazie" || kb[0][1].CallbackData != "rate_update_no" {
		t.Errorf("tastiera = %+v", kb)
	}
}

func TestBuildRateUpdateKeyboard(t *testing.T) {
	kb := BuildRateUpdateKeyboard()
	raw, err := json.Marshal(kb)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"inline_keyboard":[[{"text":"✅ Aggiorna tariffe","callback_data":"rate_update_yes"},{"text":"❌ No grazie","callback_data":"rate_update_no"}]]}`
	if string(raw) != want {
		t.Errorf("tastiera = %s\natteso     %s", raw, want)
	}
	if CallbackRateUpdateYes != "rate_update_yes" || CallbackRateUpdateNo != "rate_update_no" {
		t.Error("callback data diversi da handlers/rate_update.py")
	}
}

func TestSendNotificationNonNumericChatID(t *testing.T) {
	// PTB accetta chat_id stringa: un id non numerico viene inviato così com'è.
	b, fc := testutil.NewBot(t)

	if !SendNotification(context.Background(), b, &fakeStore{}, "@canale", "msg", BuildRateUpdateKeyboard()) {
		t.Fatal("atteso True")
	}
	c := fc.LastCall(t, "sendMessage")
	if v, ok := c.Params["chat_id"].(string); !ok || v != "@canale" {
		t.Errorf("chat_id = %#v", c.Params["chat_id"])
	}
	if c.ParseMode() != "HTML" || c.Text() != "msg" || len(c.InlineKeyboard()) != 1 {
		t.Errorf("parametri = %v", c.Params)
	}

	// Senza tastiera reply_markup non viene inviato.
	fc.Reset()
	SendNotification(context.Background(), b, &fakeStore{}, "@canale", "msg", nil)
	if _, ok := fc.LastCall(t, "sendMessage").Params["reply_markup"]; ok {
		t.Error("reply_markup non atteso")
	}
}

func TestParallelSendCountsAndLimitsConcurrency(t *testing.T) {
	var active, maxActive atomic.Int64
	var mu sync.Mutex
	seen := map[int]bool{}

	sent := ParallelSend(context.Background(), 37, func(i int) bool {
		cur := active.Add(1)
		for {
			m := maxActive.Load()
			if cur <= m || maxActive.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		active.Add(-1)
		mu.Lock()
		seen[i] = true
		mu.Unlock()
		return i%3 != 0
	})

	if sent != 24 { // 37 invii, falliscono gli indici multipli di 3 (13)
		t.Errorf("sent = %d, atteso 24", sent)
	}
	if len(seen) != 37 {
		t.Errorf("eseguiti %d invii, attesi 37", len(seen))
	}
	if m := maxActive.Load(); m > MaxConcurrentSends || m < 2 {
		t.Errorf("concorrenza massima = %d (limite %d)", m, MaxConcurrentSends)
	}
}

func TestParallelSendPanicCountsAsFailure(t *testing.T) {
	sent := ParallelSend(context.Background(), 5, func(i int) bool {
		if i == 2 {
			panic("boom")
		}
		return true
	})
	if sent != 4 {
		t.Errorf("sent = %d, atteso 4", sent)
	}
}

func TestParallelSendStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int64
	sent := ParallelSend(ctx, 20, func(int) bool { calls.Add(1); return true })
	if sent != 0 || calls.Load() != 0 {
		t.Errorf("sent=%d calls=%d, attesi 0", sent, calls.Load())
	}

	// Cancellazione durante l'attesa del semaforo: nessun nuovo invio parte.
	ctx, cancel = context.WithCancel(context.Background())
	release := make(chan struct{})
	var started atomic.Int64
	done := make(chan int)
	go func() {
		done <- ParallelSend(ctx, 25, func(int) bool {
			started.Add(1)
			<-release
			return true
		})
	}()
	for started.Load() < MaxConcurrentSends {
		time.Sleep(time.Millisecond)
	}
	cancel()
	time.Sleep(5 * time.Millisecond)
	close(release)
	if got := <-done; got != MaxConcurrentSends || started.Load() != MaxConcurrentSends {
		t.Errorf("sent=%d started=%d, attesi %d", got, started.Load(), MaxConcurrentSends)
	}
}

func TestPanicError(t *testing.T) {
	ke := KeyError("commercializzazione")
	if err := PanicError(ke); !errors.Is(err, ke) || err.Error() != "'commercializzazione'" {
		t.Errorf("KeyError: %v", err)
	}
	if err := PanicError("OverflowError: cannot convert float infinity to integer"); err.Error() != "cannot convert float infinity to integer" {
		t.Errorf("OverflowError: %q", err.Error())
	}
	if err := PanicError("ValueError: cannot convert float NaN to integer"); err.Error() != "cannot convert float NaN to integer" {
		t.Errorf("ValueError: %q", err.Error())
	}
	if err := PanicError("not python: x"); err.Error() != "not python: x" {
		t.Errorf("stringa generica: %q", err.Error())
	}
	if err := PanicError("Bad Name Error: x"); err.Error() != "Bad Name Error: x" {
		t.Errorf("nome non valido: %q", err.Error())
	}
	if err := PanicError(42); err.Error() != "42" {
		t.Errorf("valore generico: %q", err.Error())
	}
	var pe *pyException
	if err := PanicError("TypeError: boom"); !errors.As(err, &pe) || pe.name != "TypeError" {
		t.Errorf("pyException attesa: %#v", err)
	}
}

func TestKeyErrorString(t *testing.T) {
	if got := KeyError("commercializzazione").Error(); got != "'commercializzazione'" {
		t.Errorf("str(KeyError) = %q", got)
	}
}

// Una tariffa corrente senza commercializzazione (NULL nel DB) fa sollevare
// KeyError in _build_current_octopus_rates: l'eccezione esce da
// check_and_notify_users e nessuna notifica viene inviata, nemmeno agli
// utenti preparati prima.
func TestCheckAndNotifyUsersKeyErrorAbortsRun(t *testing.T) {
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	ok := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	broken := userWith("2", utility("variabile", "monoraria", 0.02, 72.0), nil)
	store := &fakeStore{
		users: []*database.User{ok, broken},
		rates: database.CurrentRates{
			"luce": {
				"fissa":     {"monoraria": rate(0.130, 72.0)},
				"variabile": {"monoraria": {Energia: 0.01}}, // commercializzazione NULL
			},
			"gas": {"fissa": {}, "variabile": {}},
		},
	}

	err := CheckAndNotifyUsers(context.Background(), b, store)

	var ke KeyError
	if !errors.As(err, &ke) || err.Error() != "'commercializzazione'" {
		t.Fatalf("atteso KeyError 'commercializzazione', ottenuto %v", err)
	}
	if n := len(fc.CallsTo("sendMessage")); n != 0 {
		t.Errorf("nessun invio atteso, ottenuti %d", n)
	}
	if len(store.saved) != 0 || len(store.pending) != 0 {
		t.Error("nessun salvataggio atteso")
	}
	out := logs.String()
	assertContains(t, out, "📤 Utente 1: notifica accodata (luce)")
	assertNotContains(t, out, "Checker completato")
}

// Una tariffa utente infinita (registrazione che accetta "inf", bug Python)
// fa sollevare OverflowError a format_number: tutta l'esecuzione si ferma.
func TestCheckAndNotifyUsersInfiniteRateAbortsRun(t *testing.T) {
	b, fc := testutil.NewBot(t)
	store := &fakeStore{
		users: []*database.User{userWith("1", utility("fissa", "monoraria", math.Inf(1), 72.0), nil)},
		rates: database.CurrentRates{"luce": {"fissa": {"monoraria": rate(0.130, 72.0)}, "variabile": {}}},
	}

	err := CheckAndNotifyUsers(context.Background(), b, store)

	if err == nil || err.Error() != "cannot convert float infinity to integer" {
		t.Fatalf("atteso OverflowError, ottenuto %v", err)
	}
	if n := len(fc.CallsTo("sendMessage")); n != 0 {
		t.Errorf("nessun invio atteso, ottenuti %d", n)
	}
}

func TestValidateCheckerDataLogs(t *testing.T) {
	fixedClock(t)
	logs := captureLogs(t)
	b, _ := testutil.NewBot(t)

	if err := CheckAndNotifyUsers(context.Background(), b, &fakeStore{rates: database.CurrentRates{"luce": {}}}); err != nil {
		t.Fatal(err)
	}
	user := userWith("1", utility("fissa", "monoraria", 0.1, 1), nil)
	if err := CheckAndNotifyUsers(context.Background(), b, &fakeStore{users: []*database.User{user}}); err != nil {
		t.Fatal(err)
	}
	if err := CheckAndNotifyUsers(context.Background(), b, &fakeStore{users: []*database.User{user}, rates: database.CurrentRates{}}); err != nil {
		t.Fatal(err)
	}

	want := "INFO - 🔍 Inizio controllo tariffe...\n" +
		"WARNING - ⚠️  Nessun utente registrato (completato in 0.00s)\n" +
		"INFO - 🔍 Inizio controllo tariffe...\n" +
		"ERROR - ❌ Nessuna tariffa disponibile dopo 0.00s. Esegui prima scraper.py\n" +
		"INFO - 🔍 Inizio controllo tariffe...\n" +
		"ERROR - ❌ Nessuna tariffa disponibile dopo 0.00s. Esegui prima scraper.py\n"
	if got := stripLogPrefix(logs.String()); got != want {
		t.Errorf("log =\n%s\natteso\n%s", got, want)
	}
}

// stripLogPrefix rimuove "data - nome - " da ogni riga di log del checker.
func stripLogPrefix(s string) string {
	var out strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if i := strings.Index(line, " - checker - "); i >= 0 {
			line = line[i+len(" - checker - "):]
		}
		out.WriteString(line)
	}
	return out.String()
}

func TestCheckAndNotifyUsersFailedSendDoesNotSave(t *testing.T) {
	fixedClock(t)
	logs := captureLogs(t)
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", testutil.TelegramError("sendMessage", 500, "Internal Server Error"))
	store := &fakeStore{
		users: []*database.User{userWith("7", utility("fissa", "monoraria", 0.145, 72.0), nil)},
		rates: database.CurrentRates{"luce": {"fissa": {"monoraria": rate(0.130, 72.0)}}},
	}

	if err := CheckAndNotifyUsers(context.Background(), b, store); err != nil {
		t.Fatal(err)
	}

	if len(store.saved) != 0 || len(store.pending) != 0 {
		t.Error("nessun salvataggio atteso dopo un invio fallito")
	}
	got := stripLogPrefix(logs.String())
	for _, line := range []string{
		"INFO - 📤 Utente 7: notifica accodata (luce)\n",
		"INFO - 📨 Invio 1 notifiche in parallelo (max 10 simultanee)...\n",
		"ERROR - 🌐 Errore di rete invio messaggio a 7: Internal Server Error\n",
		"WARNING - ❌ Notifica fallita per 7\n",
		"INFO - ✅ Checker completato in 0.00s - Notifiche: 0/1\n",
	} {
		assertContains(t, got, line)
	}
}

func TestShouldShowUtilityUnknownType(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.1, 1), nil)
	if show, est := shouldShowUtility("acqua", Savings{}, user, nil); show || est != nil {
		t.Errorf("(%v, %v), atteso (False, None)", show, est)
	}
	if est := calculateUtilitySavings("acqua", user, nil); est != nil {
		t.Errorf("atteso None, ottenuto %v", *est)
	}
	// Gas senza fornitura gas
	if est := calculateUtilitySavings("gas", user, nil); est != nil {
		t.Errorf("atteso None, ottenuto %v", *est)
	}
	// Consumi presenti ma tariffa corrente assente
	luce := utility("fissa", "monoraria", 0.1, 1)
	luce.ConsumoF1 = fp(100)
	if est := calculateUtilitySavings("luce", userWith("1", luce, nil), database.CurrentRates{}); est != nil {
		t.Errorf("atteso None, ottenuto %v", *est)
	}
}

func TestFormatSavingsEstimatesIsEmpty(t *testing.T) {
	if got := formatSavingsEstimates(); got != "" {
		t.Errorf("atteso \"\", ottenuto %q", got)
	}
}

func TestFormatMixedConsumptionMessagePartial(t *testing.T) {
	got := formatMixedConsumptionMessage([]mixedUtility{{"luce", fp(10)}, {"gas", nil}})
	if got != "📊 Per una stima ancora più precisa, puoi indicare tutti i tuoi consumi con /update.\n\n" {
		t.Errorf("got %q", got)
	}
	if got := formatMixedConsumptionMessage(nil); got != "" {
		t.Errorf("lista vuota: %q", got)
	}
}

func TestMainRequiresToken(t *testing.T) {
	if err := Main(context.Background(), "", &fakeStore{}); !errors.Is(err, ErrMissingToken) {
		t.Errorf("atteso ErrMissingToken, ottenuto %v", err)
	}
	if ErrMissingToken.Error() != "TELEGRAM_BOT_TOKEN non impostato in .env" {
		t.Errorf("messaggio = %q", ErrMissingToken.Error())
	}
	// Con token e senza utenti termina senza chiamate di rete.
	if err := Main(context.Background(), "123:abc", &fakeStore{}); err != nil {
		t.Errorf("errore inatteso: %v", err)
	}
}

func TestNewBot(t *testing.T) {
	b := NewBot("123:abc")
	if b.Token != "123:abc" {
		t.Errorf("token = %q", b.Token)
	}
	bc, ok := b.BotClient.(*gotgbot.BaseBotClient)
	if !ok {
		t.Fatalf("BotClient = %T", b.BotClient)
	}
	if bc.DefaultRequestOpts != nil || bc.Client.Timeout != 0 {
		t.Error("atteso il timeout di default di gotgbot (5 s)")
	}
	if gotgbot.DefaultTimeout != 5*time.Second {
		t.Errorf("DefaultTimeout gotgbot = %v, atteso 5s come PTB", gotgbot.DefaultTimeout)
	}
}

// Integrazione con il database reale: notifica inviata, last_notified_rates
// e pending_rates salvati nel formato Python, nessuna seconda notifica.
func TestCheckAndNotifyUsersIntegrationWithDatabase(t *testing.T) {
	fixedClock(t)
	logs := captureLogs(t)
	db := openTestDB(t)
	b, fc := testutil.NewBot(t)

	luce := utility("fissa", "monoraria", 0.145, 72.0)
	luce.ConsumoF1 = fp(2700.0)
	luce.Scadenza = "2026-03-01"
	gas := utility("variabile", "monoraria", 0.10, 84.0)
	gas.ConsumoAnnuo = fp(1200.0)
	if !db.SaveUser("123", database.User{Luce: luce, Gas: &gas}) {
		t.Fatal("SaveUser")
	}
	// Utente senza risparmi: non notificato
	if !db.SaveUser("456", database.User{Luce: utility("fissa", "monoraria", 0.100, 50.0)}) {
		t.Fatal("SaveUser")
	}
	n := db.SaveRatesBatch("2026-10-17", []database.RateRecord{
		{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.130, Commercializzazione: fp(85.0), CodOfferta: sp("COD-LUCE")},
		{Servizio: "gas", Tipo: "variabile", Fascia: "monoraria", Energia: 0.11, Commercializzazione: fp(80.0)},
	})
	if n != 2 {
		t.Fatalf("SaveRatesBatch = %d", n)
	}

	if err := CheckAndNotifyUsers(context.Background(), b, db); err != nil {
		t.Fatal(err)
	}

	calls := fc.CallsTo("sendMessage")
	if len(calls) != 1 {
		t.Fatalf("attesa 1 notifica, ottenute %d", len(calls))
	}
	want := "⚖️ <b>Aggiornamento tariffe Octopus Energy</b>\n" +
		"OctoTracker ha rilevato una variazione nelle tariffe, ma non è detto che sia automaticamente più conveniente: una delle due componenti è migliorata, l'altra è aumentata.\n\n" +
		"💡 <b>Luce (Fissa Monoraria):</b>\n" +
		"Tua tariffa: Prezzo fisso 0,145 €/kWh, Comm. 72 €/anno\n" +
		"Nuova tariffa: Prezzo fisso <b>0,13 €/kWh</b>, Comm. <u>85 €/anno</u>\n" +
		"📋 Codice offerta: <code>COD-LUCE</code>\n" +
		"💰 In base ai tuoi consumi di luce, stimiamo un risparmio di circa 27,50 €/anno.\n\n" +
		"👇 Vuoi aggiornare le tariffe memorizzate su OctoTracker con quelle nuove?\n\n" +
		"🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe\n\n" +
		"☕️ Se pensi che questo bot ti sia utile, puoi offrirmi un caffè su ko-fi.com/dstmrk — grazie di cuore! 💙"
	assertSameText(t, "messaggio", calls[0].Text(), want)
	if calls[0].ChatID() != "123" || calls[0].ParseMode() != "HTML" || len(calls[0].InlineKeyboard()) != 1 {
		t.Errorf("parametri = %v", calls[0].Params)
	}

	u := db.LoadUser("123")
	raw, err := u.LastNotifiedRates.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != `{"luce": {"energia": 0.13, "commercializzazione": 85.0}, "gas": {"energia": 0.11, "commercializzazione": 80.0}}` {
		t.Errorf("last_notified_rates = %s", got)
	}
	if u.Luce.Scadenza != "2026-03-01" || u.Luce.ConsumoF1 == nil || *u.Luce.ConsumoF1 != 2700.0 {
		t.Errorf("dati utente alterati: %+v", u.Luce)
	}
	p := db.LoadPendingRates("123")
	if p == nil {
		t.Fatal("pending_rates attese")
	}
	praw, _ := p.MarshalJSON()
	wantPending := `{"luce": {"tipo": "fissa", "fascia": "monoraria", "consumo_f1": 2700.0, "energia": 0.13, "commercializzazione": 85.0}, ` +
		`"gas": {"tipo": "variabile", "fascia": "monoraria", "consumo_annuo": 1200.0, "energia": 0.1, "commercializzazione": 84.0}, ` +
		`"updated_services": ["luce"]}`
	if string(praw) != wantPending {
		t.Errorf("pending_rates = %s\natteso         %s", praw, wantPending)
	}
	if db.LoadPendingRates("456") != nil {
		t.Error("nessuna pending per l'utente 456")
	}
	assertContains(t, stripLogPrefix(logs.String()), "INFO - ✅ Notifica inviata a 123\n")
	assertContains(t, stripLogPrefix(logs.String()), "INFO - ✅ Checker completato in 0.00s - Notifiche: 1/2\n")

	// Secondo giro con le stesse tariffe: già notificato, nessun invio.
	fc.Reset()
	if err := CheckAndNotifyUsers(context.Background(), b, db); err != nil {
		t.Fatal(err)
	}
	if n := len(fc.CallsTo("sendMessage")); n != 0 {
		t.Errorf("nessuna nuova notifica attesa, ottenute %d", n)
	}
}

// Un utente che ha bloccato il bot viene rimosso dal database reale.
func TestCheckAndNotifyUsersRemovesBlockedUserFromDatabase(t *testing.T) {
	db := openTestDB(t)
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", testutil.TelegramError("sendMessage", http.StatusForbidden, "Forbidden: bot was blocked by the user"))
	db.SaveUser("123", database.User{Luce: utility("fissa", "monoraria", 0.145, 72.0)})
	db.SaveRatesBatch("2026-10-17", []database.RateRecord{
		{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.130, Commercializzazione: fp(72.0)},
	})

	if err := CheckAndNotifyUsers(context.Background(), b, db); err != nil {
		t.Fatal(err)
	}
	if db.UserExists("123") {
		t.Error("l'utente doveva essere rimosso")
	}
}

// Molti utenti in parallelo sul database reale (eseguito con -race).
func TestCheckAndNotifyUsersManyUsersParallel(t *testing.T) {
	db := openTestDB(t)
	b, fc := testutil.NewBot(t)
	const n = 35
	for i := range n {
		db.SaveUser(fmt.Sprint(1000+i), database.User{Luce: utility("fissa", "monoraria", 0.145+float64(i)/1000, 72.0)})
	}
	db.SaveRatesBatch("2026-10-17", []database.RateRecord{
		{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.130, Commercializzazione: fp(72.0)},
	})
	// Un utente su 5 fallisce l'invio (rate limit).
	fc.SetResponder(func(method string, params map[string]any) (json.RawMessage, error) {
		if id, _ := params["chat_id"].(int64); method == "sendMessage" && id%5 == 0 {
			return nil, testutil.RetryAfterError(method, 1)
		}
		return nil, nil
	})

	if err := CheckAndNotifyUsers(context.Background(), b, db); err != nil {
		t.Fatal(err)
	}

	if got := len(fc.CallsTo("sendMessage")); got != n {
		t.Errorf("invii = %d, attesi %d", got, n)
	}
	notified := 0
	for _, u := range db.LoadUsers() {
		var id int64
		fmt.Sscan(u.ID, &id)
		has := len(u.LastNotifiedRates) > 0
		if has {
			notified++
		}
		if has == (id%5 == 0) {
			t.Errorf("utente %s: last_notified_rates presente=%v", u.ID, has)
		}
	}
	if notified != n-7 {
		t.Errorf("notificati = %d, attesi %d", notified, n-7)
	}
}
