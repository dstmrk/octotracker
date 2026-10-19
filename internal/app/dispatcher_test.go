package app

// Test end-to-end del dispatcher: il dispatcher reale di NewDispatcher con
// il client Telegram finto di testutil e un database temporaneo. Ogni update
// passa da Dispatcher.ProcessUpdate, così si verifica l'instradamento come
// in python-telegram-bot (ordine degli handler, conversazioni, fallback).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/config"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/handlers/rateupdate"
	"github.com/dstmrk/octotracker/internal/testutil"
)

const (
	msgUnknownCommand = "Comando non riconosciuto 🤷‍♂️\nDai un'occhiata a /help per vedere cosa puoi fare con OctoTracker."
	msgCommPrompt     = "Perfetto! Ora indica il costo di commercializzazione luce, in euro/anno.\n\n💬 Esempio: 72 (se paghi 6 €/mese)"
	msgNotRegistered  = "ℹ️ Non hai ancora registrato le tue tariffe."
	msgCancelled      = "❌ <b>Registrazione annullata</b>"
)

// harness collega il dispatcher reale a un bot finto e a un database.
type harness struct {
	t     *testing.T
	b     *gotgbot.Bot
	fc    *testutil.FakeClient
	d     *ext.Dispatcher
	store *database.Store
}

func newHarness(t *testing.T, cfg config.Config) *harness {
	t.Helper()
	b, fc := testutil.NewBot(t)
	store := newTestStore(t)
	if cfg.CheckerHour == 0 {
		cfg.CheckerHour = 10
	}
	d := NewDispatcher(DispatcherConfig{Config: cfg, Store: store})
	return &harness{t: t, b: b, fc: fc, d: d, store: store}
}

// send elabora un update e restituisce le chiamate API che ha prodotto.
func (h *harness) send(u *gotgbot.Update) []testutil.Call {
	h.t.Helper()
	h.fc.Reset()
	if err := h.d.ProcessUpdate(h.b, u, nil); err != nil {
		h.t.Fatalf("ProcessUpdate: %v", err)
	}
	return h.fc.Calls()
}

func (h *harness) text(s string) []testutil.Call {
	return h.send(testutil.MessageUpdate(testutil.DefaultUserID, s))
}

func (h *harness) press(data string) []testutil.Call {
	return h.send(testutil.CallbackUpdate(testutil.DefaultUserID, data))
}

// lastText restituisce il testo dell'ultima sendMessage/editMessageText.
func lastText(t *testing.T, calls []testutil.Call) string {
	t.Helper()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Method == "sendMessage" || calls[i].Method == "editMessageText" {
			return calls[i].Text()
		}
	}
	t.Fatalf("nessun messaggio inviato (chiamate: %v)", calls)
	return ""
}

// buttons restituisce le callback_data della tastiera dell'ultima chiamata.
func buttons(calls []testutil.Call) []string {
	if len(calls) == 0 {
		return nil
	}
	var out []string
	for _, row := range calls[len(calls)-1].InlineKeyboard() {
		for _, b := range row {
			out = append(out, b.CallbackData)
		}
	}
	return out
}

func methods(calls []testutil.Call) string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Method)
	}
	return strings.Join(out, ",")
}

// startRegistration porta la registrazione allo stato LUCE_ENERGIA (tariffa fissa).
func (h *harness) startRegistration() {
	h.t.Helper()
	calls := h.text("/start")
	if got := strings.Join(buttons(calls), ","); got != "tipo_fissa,tipo_variabile" {
		h.t.Fatalf("/start: pulsanti = %q", got)
	}
	calls = h.press("tipo_fissa")
	if methods(calls) != "answerCallbackQuery,editMessageText" {
		h.t.Fatalf("tipo_fissa: chiamate = %s", methods(calls))
	}
}

// TestDispatcherConfiguration: update elaborati uno alla volta.
func TestDispatcherConfiguration(t *testing.T) {
	h := newHarness(t, config.Config{})
	if h.d.MaxUsage() != 1 {
		t.Errorf("MaxRoutines = %d, atteso 1", h.d.MaxUsage())
	}
}

// TestE2ERegistrationFlowToDB: registrazione completa (luce fissa senza
// consumi né gas) fino al salvataggio nel database.
func TestE2ERegistrationFlowToDB(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.startRegistration()

	if got := lastText(t, h.text("0,145")); got != msgCommPrompt {
		t.Fatalf("dopo l'energia: %q", got)
	}
	calls := h.text("72")
	if got := strings.Join(buttons(calls), ","); got != "skip_scadenza_luce" || calls[len(calls)-1].ParseMode() != "HTML" {
		t.Fatalf("dopo la commercializzazione: pulsanti %q, chiamate %v", got, calls)
	}
	calls = h.press("skip_scadenza_luce")
	if got := strings.Join(buttons(calls), ","); got != "consumi_luce_si,consumi_luce_no" {
		t.Fatalf("dopo lo skip della scadenza: pulsanti %q", got)
	}
	calls = h.press("consumi_luce_no")
	if got := strings.Join(buttons(calls), ","); got != "gas_si,gas_no" {
		t.Fatalf("dopo consumi_luce_no: pulsanti %q", got)
	}
	calls = h.press("gas_no")
	if !strings.Contains(lastText(t, calls), "Tutto corretto?") {
		t.Fatalf("conferma = %q", lastText(t, calls))
	}

	u := h.store.LoadUser("123456789")
	if u == nil {
		t.Fatal("utente non salvato")
	}
	if u.Luce.Tipo != "fissa" || u.Luce.Fascia != "monoraria" || u.Luce.Energia != 0.145 || u.Luce.Commercializzazione != 72 || u.Gas != nil {
		t.Errorf("utente salvato = %+v", u)
	}

	// La conversazione è terminata: il testo libero viene ignorato
	if calls := h.text("0,2"); len(calls) != 0 {
		t.Errorf("testo dopo la fine della conversazione: %v", calls)
	}
}

// TestE2EStatusDuringRegistration: /status durante la registrazione viene
// gestito dal comando /status, mentre la conversazione resta nel suo stato.
func TestE2EStatusDuringRegistration(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.startRegistration()

	if got := lastText(t, h.text("/status")); !strings.HasPrefix(got, msgNotRegistered) {
		t.Fatalf("/status = %q", got)
	}
	if got := lastText(t, h.text("0,145")); got != msgCommPrompt {
		t.Errorf("dopo /status la conversazione deve restare in LUCE_ENERGIA: %q", got)
	}
}

// TestE2EStartDuringRegistrationIsUnknownCommand: senza allow_reentry,
// /start durante una registrazione attiva non riavvia la conversazione e
// arriva a unknown_command; lo stato resta invariato.
func TestE2EStartDuringRegistrationIsUnknownCommand(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.startRegistration()

	if got := lastText(t, h.text("/start")); got != msgUnknownCommand {
		t.Fatalf("/start durante la registrazione = %q", got)
	}
	if got := lastText(t, h.text("/update")); got != msgUnknownCommand {
		t.Fatalf("/update durante la registrazione = %q", got)
	}
	if got := lastText(t, h.text("0,145")); got != msgCommPrompt {
		t.Errorf("stato cambiato: %q", got)
	}
}

// TestE2ECancelEndsConversation: /cancel (fallback) termina la
// registrazione; il testo successivo viene ignorato. Fuori dalle
// conversazioni /cancel risponde comunque.
func TestE2ECancelEndsConversation(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.startRegistration()

	if got := lastText(t, h.text("/cancel")); !strings.HasPrefix(got, msgCancelled) {
		t.Fatalf("/cancel = %q", got)
	}
	if calls := h.text("0,145"); len(calls) != 0 {
		t.Errorf("testo dopo /cancel: %v", calls)
	}
	if got := lastText(t, h.text("/cancel")); !strings.HasPrefix(got, msgCancelled) {
		t.Errorf("/cancel fuori conversazione = %q", got)
	}
	// /start dopo /cancel riavvia la registrazione
	h.startRegistration()
}

// TestE2EHelpFallbackEndsRegistration: /help è un fallback della
// registrazione e la termina.
func TestE2EHelpFallbackEndsRegistration(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.startRegistration()
	if got := lastText(t, h.text("/help")); !strings.Contains(got, "10:00") {
		t.Fatalf("/help = %q", got)
	}
	if calls := h.text("0,145"); len(calls) != 0 {
		t.Errorf("testo dopo /help: %v", calls)
	}
}

// TestE2EFeedbackFlow: /feedback → voto → commento salvato nel database.
func TestE2EFeedbackFlow(t *testing.T) {
	h := newHarness(t, config.Config{})
	if !h.store.SaveUser("123456789", database.User{Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.1, Commercializzazione: 60}}) {
		t.Fatal("SaveUser")
	}

	calls := h.text("/feedback")
	if got := strings.Join(buttons(calls), ","); got != "rating_1,rating_2,rating_3,rating_4,rating_5" {
		t.Fatalf("/feedback: pulsanti %q", got)
	}
	calls = h.press("rating_5")
	if got := strings.Join(buttons(calls), ","); got != "skip_comment" {
		t.Fatalf("rating_5: pulsanti %q", got)
	}
	h.text("Ottimo bot")
	if n := h.store.GetFeedbackCount(); n != 1 {
		t.Fatalf("feedback salvati = %d", n)
	}
	fbs := h.store.GetRecentFeedbacks(10)
	if len(fbs) != 1 || fbs[0].Rating == nil || *fbs[0].Rating != 5 || fbs[0].Comment == nil || *fbs[0].Comment != "Ottimo bot" {
		t.Errorf("feedback = %+v", fbs)
	}
	if calls := h.text("altro testo"); len(calls) != 0 {
		t.Errorf("testo dopo il feedback: %v", calls)
	}
}

// seedNotification registra un utente con tariffa fissa, salva tariffe
// correnti migliori ed esegue il checker, che invia la notifica con i
// pulsanti e salva le tariffe pendenti.
func (h *harness) seedNotification() {
	h.t.Helper()
	luce := database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.145, Commercializzazione: 72}
	if !h.store.SaveUser("123456789", database.User{Luce: luce}) {
		h.t.Fatal("SaveUser")
	}
	n := h.store.SaveRatesBatch("2026-10-17", []database.RateRecord{
		{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.10, Commercializzazione: fp(60), CodOfferta: sp("COD")},
	})
	if n != 1 {
		h.t.Fatalf("SaveRatesBatch = %d", n)
	}
	h.fc.Reset()
	if err := checker.CheckAndNotifyUsers(context.Background(), h.b, h.store); err != nil {
		h.t.Fatal(err)
	}
	calls := h.fc.CallsTo("sendMessage")
	if len(calls) != 1 || strings.Join(buttons(calls), ",") != "rate_update_yes,rate_update_no" {
		h.t.Fatalf("notifica = %v", calls)
	}
	if h.store.LoadPendingRates("123456789") == nil {
		h.t.Fatal("tariffe pendenti non salvate")
	}
}

// notificationCallback simula il tocco di un pulsante della notifica.
func notificationCallback(data string) *gotgbot.Update {
	u := testutil.CallbackUpdate(testutil.DefaultUserID, data)
	u.CallbackQuery.Message.(*gotgbot.Message).Text = "Notifica\n\n" + rateupdate.PromptText
	return u
}

// TestE2ERateUpdateYesAppliesPending: dopo una notifica del checker il
// pulsante "Aggiorna tariffe" applica le tariffe pendenti; per un'offerta
// fissa la conversazione chiede la nuova scadenza.
func TestE2ERateUpdateYesAppliesPending(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.seedNotification()

	calls := h.send(notificationCallback(checker.CallbackRateUpdateYes))
	if !strings.HasPrefix(methods(calls), "answerCallbackQuery,editMessageText,sendMessage") {
		t.Fatalf("chiamate = %s", methods(calls))
	}
	if calls[1].Text() != "Notifica\n\n"+rateupdate.ConfirmedText {
		t.Errorf("messaggio modificato = %q", calls[1].Text())
	}
	if calls[2].Text() != rateupdate.ScadenzaAskText || strings.Join(buttons(calls), ",") != "scadenza_update_yes,scadenza_update_no" {
		t.Errorf("richiesta scadenza = %q %v", calls[2].Text(), buttons(calls))
	}

	u := h.store.LoadUser("123456789")
	if u.Luce.Energia != 0.10 || u.Luce.Commercializzazione != 60 {
		t.Errorf("tariffe non applicate: %+v", u.Luce)
	}
	if h.store.LoadPendingRates("123456789") != nil {
		t.Error("tariffe pendenti non rimosse")
	}

	// Stato ASK_SCADENZA: "No" termina la conversazione
	calls = h.press(rateupdate.CallbackScadenzaNo)
	if !strings.Contains(lastText(t, calls), rateupdate.ScadenzaDeclinedText) {
		t.Errorf("scadenza_update_no = %q", lastText(t, calls))
	}
	if calls := h.text("01/01/2026"); len(calls) != 0 {
		t.Errorf("testo dopo la fine: %v", calls)
	}
}

// TestE2ERateUpdateNo: il pulsante "No grazie" scarta le tariffe pendenti
// senza modificare quelle dell'utente.
func TestE2ERateUpdateNo(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.seedNotification()

	calls := h.send(notificationCallback(checker.CallbackRateUpdateNo))
	if methods(calls) != "answerCallbackQuery,editMessageText" {
		t.Fatalf("chiamate = %s", methods(calls))
	}
	if calls[1].Text() != "Notifica\n\n"+rateupdate.DeclinedText {
		t.Errorf("messaggio modificato = %q", calls[1].Text())
	}
	if h.store.LoadPendingRates("123456789") != nil {
		t.Error("tariffe pendenti non rimosse")
	}
	if u := h.store.LoadUser("123456789"); u.Luce.Energia != 0.145 {
		t.Errorf("tariffe modificate: %+v", u.Luce)
	}
}

// TestE2EUnknownCommandsAndIgnoredUpdates: i comandi sconosciuti ricevono la
// risposta di unknown_command; testo libero e pulsanti senza conversazione
// vengono ignorati.
func TestE2EUnknownCommandsAndIgnoredUpdates(t *testing.T) {
	h := newHarness(t, config.Config{})
	for _, cmd := range []string{"/foo", "/foo@" + testutil.BotUsername, "/status@altro_bot", "/start@altro_bot"} {
		if got := lastText(t, h.text(cmd)); got != msgUnknownCommand {
			t.Errorf("%s = %q", cmd, got)
		}
	}
	if got := lastText(t, h.text("/status@"+testutil.BotUsername)); !strings.HasPrefix(got, msgNotRegistered) {
		t.Errorf("/status@bot = %q", got)
	}
	for _, text := range []string{"ciao", "0,145"} {
		if calls := h.text(text); len(calls) != 0 {
			t.Errorf("%q: chiamate inattese %v", text, calls)
		}
	}
	for _, data := range []string{"tipo_fissa", "gas_no", "rating_5", "scadenza_update_yes"} {
		if calls := h.press(data); len(calls) != 0 {
			t.Errorf("pulsante %s: chiamate inattese %v", data, calls)
		}
	}
}

// TestE2EEditedCommandAlertsAdmin: come MessageHandler(filters.COMMAND) di
// PTB, un comando modificato arriva a unknown_command, che solleva
// AttributeError (update.message è None): l'error handler avvisa l'admin.
func TestE2EEditedCommandAlertsAdmin(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t, config.Config{AdminUserID: "999"})
	u := testutil.MessageUpdate(testutil.DefaultUserID, "/foo")
	u.EditedMessage, u.Message = u.Message, nil

	calls := h.send(u)
	if len(calls) != 1 || calls[0].Params["chat_id"] != "999" {
		t.Fatalf("chiamate = %v", calls)
	}
	if !strings.Contains(calls[0].Text(), "<b>Tipo:</b> AttributeError\n<b>Messaggio:</b> 'NoneType' object has no attribute 'reply_text'") {
		t.Errorf("alert = %q", calls[0].Text())
	}
	assertLogContains(t, logs, "⚠️  Errore non gestito: AttributeError")
}

// TestE2ETelegramErrorReachesErrorHandler: un errore dell'API in un handler
// arriva all'error handler (qui Forbidden, anche per l'alert all'admin).
func TestE2ETelegramErrorReachesErrorHandler(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t, config.Config{AdminUserID: "999"})
	h.fc.FailMethod("sendMessage", testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"))
	h.text("/status")
	assertLogContains(t, logs, "ERROR - ❌ Errore: Forbidden: bot was blocked by the user")
	assertLogContains(t, logs, "⚠️  Errore non gestito: Forbidden")
	assertLogContains(t, logs, "❌ Errore invio alert admin: Forbidden: bot was blocked by the user")

	// Un BadRequest è un NetworkError: solo warning
	h.fc.FailMethod("sendMessage", testutil.TelegramError("sendMessage", 400, "Bad Request: chat not found"))
	h.text("/help")
	assertLogContains(t, logs, "⏱️  Timeout/errore di rete")
}

// TestRegisterHandlersSharesUserData: RegisterHandlers usa (o crea) un solo
// user_data per tutte le conversazioni.
func TestRegisterHandlersSharesUserData(t *testing.T) {
	store := newTestStore(t)
	d := ext.NewDispatcher(nil)
	if ud := RegisterHandlers(d, DispatcherConfig{Store: store}); ud == nil {
		t.Fatal("user_data nil")
	}
}

// TestMinLevelHandler: i log della libreria sotto WARNING vengono scartati.
func TestMinLevelHandler(t *testing.T) {
	logs := captureLogs(t)
	l := ptbLogger("telegram.ext.Application").With("k", "v").WithGroup("g")
	l.Debug("debug nascosto")
	l.Info("info nascosto")
	l.Warn("warning visibile")
	assertLogNotContains(t, logs, "nascosto")
	assertLogContains(t, logs, "telegram.ext.Application - WARNING - warning visibile")
	logUnhandledError(context.Canceled)
	assertLogContains(t, logs, "CRITICAL - Errore non gestito nell'elaborazione dell'update: context canceled")
}

// TestE2ERateUpdateScadenzaDate: dopo "Aggiorna tariffe" per un'offerta
// fissa, "Sì" chiede la data di attivazione; una data non valida lascia la
// conversazione in UPDATE_DATE, una valida salva la nuova scadenza.
func TestE2ERateUpdateScadenzaDate(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.seedNotification()

	h.send(notificationCallback(checker.CallbackRateUpdateYes))
	calls := h.press(rateupdate.CallbackScadenzaYes)
	if got := strings.Join(buttons(calls), ","); got != rateupdate.CallbackScadenzaSkip {
		t.Fatalf("richiesta della data: pulsanti %q (%v)", got, calls)
	}
	if got := lastText(t, h.text("31/02/2026")); got != rateupdate.MsgDateInvalid {
		t.Fatalf("data non valida = %q", got)
	}
	if got := lastText(t, h.text("15/03/2026")); got != rateupdate.ScadenzaDoneText {
		t.Fatalf("data valida = %q", got)
	}
	want := dateutil.FormatISODate(dateutil.ComputeExpiryDate(dateutil.NewDate(2026, time.March, 15)))
	if u := h.store.LoadUser("123456789"); u.Luce.Scadenza != want {
		t.Errorf("scadenza = %q, attesa %q", u.Luce.Scadenza, want)
	}
	if calls := h.text("01/01/2026"); len(calls) != 0 {
		t.Errorf("testo dopo la fine: %v", calls)
	}
}

// TestE2ECancelEndsFeedbackAndRateUpdate: /cancel è il fallback anche delle
// conversazioni feedback (feedback_cancel) e rate_update
// (cancel_conversation).
func TestE2ECancelEndsFeedbackAndRateUpdate(t *testing.T) {
	h := newHarness(t, config.Config{})
	h.seedNotification()

	h.text("/feedback")
	if got := lastText(t, h.text("/cancel")); !strings.HasPrefix(got, "❌ Feedback annullato.") {
		t.Fatalf("/cancel nel feedback = %q", got)
	}
	if calls := h.press("rating_5"); len(calls) != 0 {
		t.Errorf("voto dopo /cancel: %v", calls)
	}

	h.send(notificationCallback(checker.CallbackRateUpdateYes))
	if got := lastText(t, h.text("/cancel")); !strings.HasPrefix(got, msgCancelled) {
		t.Fatalf("/cancel nell'aggiornamento tariffe = %q", got)
	}
	if calls := h.press(rateupdate.CallbackScadenzaYes); len(calls) != 0 {
		t.Errorf("pulsante dopo /cancel: %v", calls)
	}
}

// TestE2ETwoActiveConversations: come in PTB due conversazioni possono
// essere attive insieme (/feedback durante la registrazione non è gestito
// dalla registrazione e avvia il feedback); /cancel va al primo handler che
// lo accetta, cioè la registrazione, mentre il feedback resta attivo.
func TestE2ETwoActiveConversations(t *testing.T) {
	h := newHarness(t, config.Config{})
	if !h.store.SaveUser("123456789", database.User{Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.1, Commercializzazione: 60}}) {
		t.Fatal("SaveUser")
	}
	h.startRegistration()

	if got := strings.Join(buttons(h.text("/feedback")), ","); got != "rating_1,rating_2,rating_3,rating_4,rating_5" {
		t.Fatalf("/feedback durante la registrazione: pulsanti %q", got)
	}
	if got := lastText(t, h.text("/cancel")); !strings.HasPrefix(got, msgCancelled) {
		t.Fatalf("primo /cancel = %q", got)
	}
	if got := strings.Join(buttons(h.press("rating_4")), ","); got != "skip_comment" {
		t.Fatalf("il feedback deve restare attivo: pulsanti %q", got)
	}
	if got := lastText(t, h.text("/cancel")); !strings.HasPrefix(got, "❌ Feedback annullato.") {
		t.Fatalf("secondo /cancel = %q", got)
	}
}
