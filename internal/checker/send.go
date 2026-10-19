package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Callback data dei pulsanti della notifica (gestiti da handlers/rate_update:
// CALLBACK_YES e CALLBACK_NO).
const (
	CallbackRateUpdateYes = "rate_update_yes"
	CallbackRateUpdateNo  = "rate_update_no"
)

// MaxConcurrentSends è il numero massimo di invii simultanei
// (asyncio.Semaphore(10)), per rispettare i rate limit Telegram.
const MaxConcurrentSends = 10

// ErrMissingToken è l'errore sollevato da main() se TELEGRAM_BOT_TOKEN non è
// impostato (ValueError in Python).
var ErrMissingToken = errors.New("TELEGRAM_BOT_TOKEN non impostato in .env")

// now è l'orologio usato per misurare la durata (time.time()); sostituibile nei test.
var now = time.Now

// BuildRateUpdateKeyboard costruisce la tastiera inline per l'aggiornamento
// tariffe (build_rate_update_keyboard).
func BuildRateUpdateKeyboard() gotgbot.InlineKeyboardMarkup {
	return gotgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
			{
				{Text: "✅ Aggiorna tariffe", CallbackData: CallbackRateUpdateYes},
				{Text: "❌ No grazie", CallbackData: CallbackRateUpdateNo},
			},
		},
	}
}

// NewBot crea il client Telegram per il token indicato, senza chiamate di
// rete né validazione del token (un token errato emerge al primo invio come
// errore Telegram). Le richieste usano il timeout di default di gotgbot (5 s).
func NewBot(token string) *gotgbot.Bot {
	return &gotgbot.Bot{
		Token:     token,
		BotClient: &gotgbot.BaseBotClient{Client: http.Client{}},
	}
}

// sendErrorKind è il tipo di errore di invio, nell'ordine in cui
// SendNotification li gestisce.
type sendErrorKind int

const (
	kindRetryAfter sendErrorKind = iota // rate limit con retry_after
	kindTimedOut                        // timeout di trasporto
	kindNetwork                         // errore di rete, risposta non JSON, 400 o 5xx
	kindTelegram                        // altri errori Telegram: 401, 403, 404, 409, chat migrata
)

// classifySendError classifica un errore di invio come la versione Python:
//
//   - retry_after → kindRetryAfter; migrate_to_chat_id → kindTelegram;
//   - 401, 403, 404 e 409 → kindTelegram (unici candidati alla rimozione
//     dell'utente);
//   - 400 → kindNetwork: un "Bad Request: chat not found" reale viene
//     loggato come errore di rete e l'utente NON viene rimosso (bug Python
//     replicato di proposito);
//   - ogni altro codice (5xx, 429 senza retry_after, ...) → kindNetwork;
//   - deadline del context o timeout net.Error → kindTimedOut; altri errori
//     di trasporto e risposte non JSON → kindNetwork.
func classifySendError(err error) sendErrorKind {
	var tgErr *gotgbot.TelegramError
	if errors.As(err, &tgErr) {
		if p := tgErr.ResponseParams; p != nil {
			if p.MigrateToChatId != 0 {
				return kindTelegram
			}
			if p.RetryAfter != 0 {
				return kindRetryAfter
			}
		}
		switch tgErr.Code {
		case http.StatusForbidden, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict:
			return kindTelegram
		default:
			return kindNetwork
		}
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return kindTimedOut
	}
	return kindNetwork
}

// retryAfterSeconds restituisce e.retry_after (secondi) di un errore RetryAfter.
func retryAfterSeconds(err error) int64 {
	var tgErr *gotgbot.TelegramError
	if errors.As(err, &tgErr) && tgErr.ResponseParams != nil {
		return tgErr.ResponseParams.RetryAfter
	}
	return 0
}

// sendMessage invia il messaggio HTML (sendMessage con parse_mode HTML e
// reply_markup opzionale). Un user_id non numerico viene inviato come
// stringa, come faceva la versione Python: Telegram risponde con un errore.
func sendMessage(ctx context.Context, b *gotgbot.Bot, userID, text string, replyMarkup gotgbot.ReplyMarkup) error {
	if chatID, err := strconv.ParseInt(userID, 10, 64); err == nil {
		_, err = b.SendMessageWithContext(ctx, chatID, text, &gotgbot.SendMessageOpts{
			ParseMode:   "HTML",
			ReplyMarkup: replyMarkup,
		})
		return err
	}
	params := map[string]any{"chat_id": userID, "text": text, "parse_mode": "HTML"}
	if replyMarkup != nil {
		params["reply_markup"] = replyMarkup
	}
	_, err := b.RequestWithContext(ctx, "sendMessage", params, nil)
	return err
}

// SendNotification invia una notifica Telegram in HTML con tastiera inline
// opzionale (send_notification). replyMarkup nil equivale a None.
//
// Restituisce true se il messaggio è stato inviato. Gli errori non vengono
// propagati ma loggati (vedi classifySendError):
//
//   - rate limit: warning, nessun nuovo tentativo;
//   - timeout ed errori di rete (incluso il 400): errore loggato;
//   - altri errori Telegram: se il messaggio (in minuscolo) contiene uno dei
//     TelegramErrorsToDelete l'utente viene rimosso dal database, altrimenti
//     l'errore viene loggato.
func SendNotification(ctx context.Context, b *gotgbot.Bot, store UserRemover, userID, message string, replyMarkup gotgbot.ReplyMarkup) bool {
	err := sendMessage(ctx, b, userID, message, replyMarkup)
	if err == nil {
		return true
	}

	switch classifySendError(err) {
	case kindRetryAfter:
		logger.Warn(fmt.Sprintf("⏱️  Rate limit per utente %s: riprova tra %ds", userID, retryAfterSeconds(err)))
	case kindTimedOut:
		logger.Error(fmt.Sprintf("⏱️  Timeout invio messaggio a %s", userID), "error", tg.ErrorString(err))
	case kindNetwork:
		logger.Error(fmt.Sprintf("🌐 Errore di rete invio messaggio a %s: %s", userID, tg.ErrorString(err)))
	default:
		errText := tg.ErrorString(err)
		errorMsg := strings.ToLower(errText) // Case-insensitive per robustezza

		// Controlla se l'errore indica che dobbiamo rimuovere l'utente
		for _, pattern := range TelegramErrorsToDelete {
			if strings.Contains(errorMsg, pattern) {
				logger.Warn(fmt.Sprintf("🚫 Utente %s non raggiungibile ('%s') - rimozione dal database", userID, errText))
				store.RemoveUser(userID)
				return false
			}
		}

		logger.Error(fmt.Sprintf("❌ Errore Telegram invio messaggio a %s: %s", userID, errText))
	}
	return false
}

// validateCheckerData verifica che ci siano utenti e tariffe disponibili
// (_validate_checker_data).
func validateCheckerData(users []*database.User, currentRates database.CurrentRates, startTime time.Time) bool {
	if len(users) == 0 {
		logger.Warn(fmt.Sprintf("⚠️  Nessun utente registrato (completato in %.2fs)", now().Sub(startTime).Seconds()))
		return false
	}

	if len(currentRates) == 0 {
		logger.Error(fmt.Sprintf("❌ Nessuna tariffa disponibile dopo %.2fs. Esegui prima scraper.py", now().Sub(startTime).Seconds()))
		return false
	}

	return true
}

// ParallelSend esegue send(i) per i in [0, n) con al massimo
// MaxConcurrentSends invii simultanei e restituisce quanti hanno restituito
// true. Gli invii partono nell'ordine dato e un panic in un invio conta come
// fallimento senza interrompere gli altri. Se ctx viene cancellato non
// vengono avviati nuovi invii.
func ParallelSend(ctx context.Context, n int, send func(i int) bool) int {
	sem := make(chan struct{}, MaxConcurrentSends)
	var wg sync.WaitGroup
	var sent atomic.Int64

	for i := range n {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if callRecovering(send, i) {
				sent.Add(1)
			}
		}()
	}

	wg.Wait()
	return int(sent.Load())
}

// callRecovering chiama send(i) trattando un panic come fallimento.
func callRecovering(send func(int) bool, i int) (ok bool) {
	defer func() { _ = recover() }()
	return send(i)
}

// queuedNotification è una notifica pronta per l'invio (la tupla
// (user_id, user_rates, current_octopus, message, pending_rates)).
type queuedNotification struct {
	userID string
	user   *database.User
	*preparedNotification
}

// sendNotificationsParallel invia le notifiche in parallelo con rate
// limiting (_send_notifications_parallel). Restituisce il numero di
// notifiche inviate con successo.
func sendNotificationsParallel(ctx context.Context, b *gotgbot.Bot, store Store, notificationsToSend []queuedNotification) int {
	if len(notificationsToSend) == 0 {
		return 0
	}

	logger.Info(fmt.Sprintf("📨 Invio %d notifiche in parallelo (max 10 simultanee)...", len(notificationsToSend)))

	// Invia notifica con rate limiting
	sendWithLimit := func(i int) bool {
		n := notificationsToSend[i]
		replyMarkup := BuildRateUpdateKeyboard()
		success := SendNotification(ctx, b, store, n.userID, n.message, replyMarkup)
		if success {
			// Aggiorna last_notified_rates per questo utente
			userRates := *n.user
			userRates.LastNotifiedRates = n.currentOctopus
			store.SaveUser(n.userID, userRates)
			// Salva tariffe pendenti per aggiornamento rapido via bottone
			store.SavePendingRates(n.userID, n.pendingRates)
			logger.Info(fmt.Sprintf("✅ Notifica inviata a %s", n.userID))
			return true
		}
		logger.Warn(fmt.Sprintf("❌ Notifica fallita per %s", n.userID))
		return false
	}

	// Esegui tutte le notifiche in parallelo (max 10 simultanee) e conta i successi
	return ParallelSend(ctx, len(notificationsToSend), sendWithLimit)
}

// PanicError converte il valore di un panic della FASE 1 (KeyError per una
// tariffa senza commercializzazione, panic di formatters.FormatNumber per
// un valore non finito) nell'errore restituito al chiamante.
func PanicError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}

// prepareNotifications è la FASE 1 di check_and_notify_users: prepara le
// notifiche per tutti gli utenti. Un panic (es. KeyError) interrompe la
// preparazione e viene restituito come errore: nessuna notifica viene
// inviata, come in Python dove l'eccezione esce da check_and_notify_users
// prima della FASE 2 (bug Python replicato di proposito).
func prepareNotifications(users []*database.User, currentRates database.CurrentRates) (notifications []queuedNotification, err error) {
	defer func() {
		if r := recover(); r != nil {
			notifications, err = nil, PanicError(r)
		}
	}()

	for _, user := range users {
		if result := prepareUserNotification(user.ID, user, currentRates); result != nil {
			notifications = append(notifications, queuedNotification{userID: user.ID, user: user, preparedNotification: result})
		}
	}
	return notifications, nil
}

// CheckAndNotifyUsers controlla le tariffe e invia le notifiche in parallelo
// (check_and_notify_users, chiamata dallo scheduler del bot).
//
// Il chiamante può passare NewBot(token).
//
// Restituisce un errore solo dove Python solleverebbe un'eccezione verso
// run_checker (KeyError 'commercializzazione' per una tariffa corrente senza
// commercializzazione, oppure una tariffa utente infinita che FormatNumber
// non sa formattare): in quel caso nessuna notifica viene inviata e il
// chiamante logga "❌ Errore inatteso checker: <err>". Gli errori di invio
// sono gestiti per utente da SendNotification.
func CheckAndNotifyUsers(ctx context.Context, b *gotgbot.Bot, store Store) error {
	startTime := now()
	logger.Info("🔍 Inizio controllo tariffe...")

	// Carica e valida dati
	users := store.LoadUsers()
	currentRates := store.GetCurrentRates()

	if !validateCheckerData(users, currentRates, startTime) {
		return nil
	}

	// ========== FASE 1: Prepara tutte le notifiche ==========
	notificationsToSend, err := prepareNotifications(users, currentRates)
	if err != nil {
		return err
	}

	// ========== FASE 2: Invia notifiche in parallelo ==========
	notificationsSent := sendNotificationsParallel(ctx, b, store, notificationsToSend)

	// Calcola metriche
	duration := now().Sub(startTime).Seconds()
	logger.Info(fmt.Sprintf("✅ Checker completato in %.2fs - Notifiche: %d/%d", duration, notificationsSent, len(users)))
	return nil
}

// Main esegue il checker standalone (sottocomando check): token è il valore
// di TELEGRAM_BOT_TOKEN. Restituisce ErrMissingToken se il
// token è vuoto. Caricamento di .env, configurazione del logging e apertura
// del database sono a carico del chiamante.
func Main(ctx context.Context, token string, store Store) error {
	if token == "" {
		return ErrMissingToken
	}
	return CheckAndNotifyUsers(ctx, NewBot(token), store)
}
