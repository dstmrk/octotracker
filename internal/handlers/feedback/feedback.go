// Package feedback contiene il sistema di feedback di OctoTracker: la
// conversazione guidata /feedback (voto da 1 a 5 stelle, commento
// facoltativo, annullamento) con il limite di un feedback ogni 24 ore.
//
// Gli handler sono metodi di Handlers con la firma gotgbot e restituiscono
// lo stato successivo (handlers.NextConversationState) o
// handlers.EndConversation(); gli altri errori arrivano all'error handler.
// Handlers.Conversation costruisce la conversazione.
//
// Il cooldown legge users.last_feedback_at (CURRENT_TIMESTAMP di SQLite,
// quindi UTC) come ora locale, come la versione Python: il cooldown
// effettivo è spostato del fuso orario (bug Python mantenuto di proposito).
package feedback

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/conversation"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/tg"
)

var logger = logging.Logger("handlers.feedback")

const parseModeHTML = "HTML"

// FeedbackCooldownHours è il rate limiting: max 1 feedback ogni 24 ore.
const FeedbackCooldownHours = 24

// MaxCommentLength è la lunghezza massima del commento in caratteri.
const MaxCommentLength = 1000

// Stati della conversazione feedback: il valore è il nome dello stato nella
// versione Python.
const (
	StateRating  = "RATING"  // 0
	StateComment = "COMMENT" // 1
)

// Callback data dei pulsanti.
const (
	// CallbackSkipComment è la callback_data del pulsante «Salta».
	CallbackSkipComment = "skip_comment"
	// callbackRatingPrefix precede il numero di stelle (rating_N).
	callbackRatingPrefix = "rating_"
)

// keyRating è la chiave di user_data con il voto scelto.
const keyRating = "rating"

// sqliteTimestamp è il formato di CURRENT_TIMESTAMP di SQLite (eventuali
// frazioni di secondo sono accettate da time.Parse).
const sqliteTimestamp = "2006-01-02 15:04:05"

// Testi.
const (
	msgFeedbackStart = "💬 <b>Grazie per il tuo feedback!</b>\n\n" +
		"Come valuteresti OctoTracker?\n" +
		"Seleziona il numero di stelle:"
	msgFeedbackSaved = "✅ <b>Feedback ricevuto!</b>\n\n" +
		"Grazie per il tuo contributo. Lo useremo per migliorare OctoTracker!"
	msgFeedbackError     = "❌ Si è verificato un errore nel salvataggio del feedback.\nRiprova più tardi."
	msgFeedbackCancelled = "❌ Feedback annullato.\n\nPuoi riavviare in qualsiasi momento con /feedback"
)

// Store è il sottoinsieme di *database.Store usato dal feedback.
type Store interface {
	GetLastFeedbackTime(userID string) *string
	SaveFeedback(userID, feedbackType string, rating *int, comment *string) bool
}

var _ Store = (*database.Store)(nil)

// Handlers raccoglie le dipendenze degli handler di feedback.
type Handlers struct {
	// Store è il database (feedback e users.last_feedback_at).
	Store Store
	// UserData sono i dati di conversazione, condivisi con le altre
	// conversazioni del bot.
	UserData *tg.UserData
}

// ========== HELPER ==========

// canGiveFeedback controlla se l'utente può dare feedback (rate limiting).
// Restituisce (puòProcedere, messaggioErrore): il messaggio è vuoto se
// l'utente può procedere. Un timestamp illeggibile non blocca l'utente.
func (h *Handlers) canGiveFeedback(userID string) (bool, string) {
	lastFeedback := h.Store.GetLastFeedbackTime(userID)
	if lastFeedback == nil {
		return true, ""
	}

	lastTime, err := time.Parse(sqliteTimestamp, *lastFeedback)
	if err != nil {
		logger.Warn(fmt.Sprintf("Errore parsing timestamp feedback per %s: %v", userID, err))
		// In caso di errore, permetti comunque (safe default)
		return true, ""
	}

	// Come in Python, il timestamp UTC di SQLite è confrontato con l'ora
	// locale "da muro" di datetime.now(): la differenza è tra date senza
	// fuso, quindi un cambio d'ora non sposta la finestra di 24h.
	// Microsecondi trascorsi, senza la saturazione di time.Duration (±292 anni)
	now := wallClock(dateutil.Now())
	elapsed := (now.Unix()-lastTime.Unix())*1_000_000 + int64(now.Nanosecond()/1000-lastTime.Nanosecond()/1000)
	if elapsed >= FeedbackCooldownHours*3600*1_000_000 {
		return true, ""
	}

	hoursRemaining := float64(FeedbackCooldownHours) - float64(elapsed)/1e6/3600
	return false, "⏰ Hai già inviato un feedback di recente!\n\n" +
		"Per evitare spam, puoi dare feedback una volta ogni " + strconv.Itoa(FeedbackCooldownHours) + "h.\n" +
		"Riprova tra circa " + strconv.FormatInt(int64(hoursRemaining), 10) + "h."
}

// wallClock restituisce l'ora "da muro" di t come se fosse UTC.
func wallClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// ratingKeyboard è la tastiera con i pulsanti da 1 a 5 stelle.
func ratingKeyboard() gotgbot.InlineKeyboardMarkup {
	button := func(n int) gotgbot.InlineKeyboardButton {
		return gotgbot.InlineKeyboardButton{Text: strings.Repeat("⭐", n), CallbackData: callbackRatingPrefix + strconv.Itoa(n)}
	}
	return gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
		{button(1), button(2), button(3)},
		{button(4), button(5)},
	}}
}

// ========== HANDLERS ==========

// FeedbackCommand è l'handler del comando /feedback: avvia la
// conversazione. Restituisce lo stato RATING se l'utente può procedere,
// handlers.EndConversation() se è in cooldown.
func (h *Handlers) FeedbackCommand(b *gotgbot.Bot, ctx *ext.Context) error {
	userID, err := tg.UserID(ctx)
	if err != nil {
		return err
	}

	// Controlla rate limiting
	canProceed, errorMsg := h.canGiveFeedback(userID)
	if !canProceed {
		if err := tg.Reply(b, ctx.Message, errorMsg, "", nil); err != nil {
			return err
		}
		return handlers.EndConversation()
	}

	// Reset context
	h.UserData.Clear(ctx.EffectiveUser.Id)

	// Mostra pulsanti rating
	replyMarkup := ratingKeyboard()
	if err := tg.Reply(b, ctx.Message, msgFeedbackStart, parseModeHTML, &replyMarkup); err != nil {
		return err
	}
	return handlers.NextConversationState(StateRating)
}

// FeedbackRating gestisce la selezione del voto: salva il numero di stelle
// in user_data e chiede il commento. Restituisce lo stato COMMENT.
func (h *Handlers) FeedbackRating(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}

	// Estrai rating dal callback_data (formato: "rating_N")
	parts := strings.Split(query.Data, "_")
	if len(parts) < 2 {
		return fmt.Errorf("callback_data del voto non valida: %q", query.Data)
	}
	rating, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return err
	}
	h.UserData.Set(query.From.Id, keyRating, rating)

	// Mostra pulsanti per commento
	replyMarkup := gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
		{{Text: "⏭️ Salta", CallbackData: CallbackSkipComment}},
	}}

	stars := ""
	if rating > 0 {
		stars = strings.Repeat("⭐", rating)
	}
	message := "Grazie! Hai dato " + stars + "\n\n" +
		"Vuoi aggiungere un commento per aiutarci a migliorare?\n\n" +
		"💬 <i>Scrivi il tuo messaggio (max " + strconv.Itoa(MaxCommentLength) + " caratteri) oppure premi Salta.</i>"

	if err := tg.EditText(b, query, message, parseModeHTML, &replyMarkup); err != nil {
		return err
	}
	return handlers.NextConversationState(StateComment)
}

// rating restituisce il voto in user_data (nil se assente).
func (h *Handlers) rating(userID int64) *int {
	if v, ok := h.UserData.Get(userID, keyRating); ok {
		if r, ok := v.(int); ok {
			return &r
		}
	}
	return nil
}

// FeedbackComment gestisce il commento testuale: salva il feedback e
// termina la conversazione, oppure resta nello stato COMMENT se il commento
// supera MaxCommentLength.
func (h *Handlers) FeedbackComment(b *gotgbot.Bot, ctx *ext.Context) error {
	userID, err := tg.UserID(ctx)
	if err != nil {
		return err
	}
	rating := h.rating(ctx.EffectiveUser.Id)
	if ctx.Message == nil {
		return tg.ErrNoMessage
	}
	comment := strings.TrimSpace(ctx.Message.Text)

	// Validazione lunghezza commento
	if length := utf8.RuneCountInString(comment); length > MaxCommentLength {
		errorMessage := "❌ <b>Commento troppo lungo!</b>\n\n" +
			"Il tuo messaggio ha " + strconv.Itoa(length) + " caratteri, " +
			"ma il limite è " + strconv.Itoa(MaxCommentLength) + ".\n\n" +
			"💬 <i>Riprova con un messaggio più breve oppure premi /cancel per annullare.</i>"
		if err := tg.Reply(b, ctx.Message, errorMessage, parseModeHTML, nil); err != nil {
			return err
		}
		// Rimani nello stato COMMENT per permettere un nuovo tentativo
		return handlers.NextConversationState(StateComment)
	}

	// Salva feedback nel database
	success := h.Store.SaveFeedback(userID, "command", rating, &comment)

	message := msgFeedbackError
	if success {
		message = msgFeedbackSaved
	}
	if err := tg.Reply(b, ctx.Message, message, parseModeHTML, nil); err != nil {
		return err
	}
	return handlers.EndConversation()
}

// FeedbackSkipComment gestisce il pulsante «Salta»: salva il feedback
// senza commento e termina la conversazione.
func (h *Handlers) FeedbackSkipComment(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := tg.SafeAnswerCallback(b, query); err != nil {
		return err
	}

	userID := strconv.FormatInt(query.From.Id, 10)
	rating := h.rating(query.From.Id)

	// Salva feedback senza commento
	success := h.Store.SaveFeedback(userID, "command", rating, nil)

	message := msgFeedbackError
	if success {
		message = msgFeedbackSaved
	}
	if err := tg.EditText(b, query, message, parseModeHTML, nil); err != nil {
		return err
	}
	return handlers.EndConversation()
}

// FeedbackCancel annulla la conversazione feedback: risponde al messaggio
// o, per una callback, modifica il messaggio del pulsante.
func (h *Handlers) FeedbackCancel(b *gotgbot.Bot, ctx *ext.Context) error {
	var err error
	if ctx.Message != nil {
		err = tg.Reply(b, ctx.Message, msgFeedbackCancelled, "", nil)
	} else if ctx.CallbackQuery != nil {
		err = tg.EditText(b, ctx.CallbackQuery, msgFeedbackCancelled, "", nil)
	}
	if err != nil {
		return err
	}
	return handlers.EndConversation()
}

// Conversation costruisce la conversazione del feedback: /feedback avvia,
// RATING accetta i pulsanti rating_N, COMMENT il testo (anche modificato o
// business, che FeedbackComment rifiuta con un errore lasciando lo stato
// invariato) o «Salta»; /cancel annulla. Lo stato è in memoria per utente e
// chat.
func (h *Handlers) Conversation() handlers.Conversation {
	return handlers.NewConversation(
		[]ext.Handler{tg.NewCommand("feedback", h.FeedbackCommand)},
		map[string][]ext.Handler{
			StateRating: {
				handlers.NewCallback(tg.CallbackPattern(`^rating_\d$`), h.FeedbackRating),
			},
			StateComment: {
				handlers.NewMessage(tg.TextNotCommand, h.FeedbackComment).SetAllowEdited(true).SetAllowBusiness(true),
				handlers.NewCallback(tg.CallbackPattern(`^skip_comment$`), h.FeedbackSkipComment),
			},
		},
		&handlers.ConversationOpts{
			Fallbacks:    []ext.Handler{tg.NewCommand("cancel", h.FeedbackCancel)},
			StateStorage: conversation.NewInMemoryStorage(conversation.KeyStrategySenderAndChat),
		},
	)
}
