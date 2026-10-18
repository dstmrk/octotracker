// Package feedback è il port di handlers/feedback.py: il sistema di
// feedback di OctoTracker, con la conversazione guidata /feedback (voto da
// 1 a 5 stelle, commento facoltativo, annullamento) e il limite di un
// feedback ogni 24 ore.
//
// Testi, tastiere, callback_data, validazione e stati sono identici alla
// versione Python. Gli handler sono metodi di Handlers con la firma gotgbot
// e restituiscono, come in Python, lo stato successivo
// (handlers.NextConversationState) o handlers.EndConversation(); gli altri
// errori corrispondono alle eccezioni che in Python arrivano all'error
// handler. Handlers.Conversation costruisce la conversazione come bot.py.
//
// Il cooldown confronta users.last_feedback_at (CURRENT_TIMESTAMP di
// SQLite, quindi UTC) con l'ora locale datetime.now(), esattamente come
// Python: il cooldown effettivo è spostato del fuso orario (bug Python
// replicato di proposito).
package feedback

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/conversation"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
	"github.com/dstmrk/octotracker/internal/handlers/commands"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/tg"
)

// logger replica logging.getLogger(__name__) di handlers/feedback.py.
var logger = logging.Logger("handlers.feedback")

// parseModeHTML corrisponde a ParseMode.HTML di python-telegram-bot.
const parseModeHTML = "HTML"

// FeedbackCooldownHours è il rate limiting: max 1 feedback ogni 24 ore
// (FEEDBACK_COOLDOWN_HOURS).
const FeedbackCooldownHours = 24

// MaxCommentLength è la lunghezza massima del commento in caratteri (code
// point, come len() di Python) (MAX_COMMENT_LENGTH).
const MaxCommentLength = 1000

// Stati della conversazione feedback (FeedbackState): il valore è il nome
// del membro dell'enum Python.
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

// keyRating è la chiave di context.user_data con il voto scelto.
const keyRating = "rating"

// Testi (identici a handlers/feedback.py).
const (
	msgFeedbackStart = "💬 <b>Grazie per il tuo feedback!</b>\n\n" +
		"Come valuteresti OctoTracker?\n" +
		"Seleziona il numero di stelle:"
	msgFeedbackSaved = "✅ <b>Feedback ricevuto!</b>\n\n" +
		"Grazie per il tuo contributo. Lo useremo per migliorare OctoTracker!"
	msgFeedbackError     = "❌ Si è verificato un errore nel salvataggio del feedback.\nRiprova più tardi."
	msgFeedbackCancelled = "❌ Feedback annullato.\n\nPuoi riavviare in qualsiasi momento con /feedback"
)

// Store è il sottoinsieme di *database.Store usato dal feedback
// (get_last_feedback_time, save_feedback).
type Store interface {
	GetLastFeedbackTime(userID string) *string
	SaveFeedback(userID, feedbackType string, rating *int, comment *string) bool
}

var _ Store = (*database.Store)(nil)

// Handlers raccoglie le dipendenze degli handler di feedback.
type Handlers struct {
	// Store è il database (feedback e users.last_feedback_at).
	Store Store
	// UserData è l'equivalente di context.user_data, condiviso con le
	// altre conversazioni del bot.
	UserData *tg.UserData
}

// ========== HELPER ==========

// effectiveUserID replica str(update.effective_user.id).
func effectiveUserID(ctx *ext.Context) (string, error) {
	if ctx.EffectiveUser == nil {
		return "", attributeError("id")
	}
	return strconv.FormatInt(ctx.EffectiveUser.Id, 10), nil
}

// naiveMicros restituisce i microsecondi di un datetime naive dall'epoca
// (calendario proleptico; solo per differenze).
func naiveMicros(year, month, day, hour, minute, second, microsecond int) int64 {
	days := int64(ymdToOrd(year, month, day))
	secs := days*86400 + int64(hour)*3600 + int64(minute)*60 + int64(second)
	return secs*1_000_000 + int64(microsecond)
}

// nowMicros restituisce datetime.now() (ora locale naive, risoluzione al
// microsecondo per difetto) in microsecondi, con l'orologio di dateutil.
func nowMicros() int64 {
	now := dateutil.Now()
	y, m, d := now.Date()
	return naiveMicros(y, int(m), d, now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1000)
}

// totalSeconds replica timedelta.total_seconds(): microsecondi / 10**6 con
// divisione intera "vera" di Python (correttamente arrotondata).
func totalSeconds(micros int64) float64 {
	const exact = 1 << 53
	if micros > -exact && micros < exact {
		return float64(micros) / 1e6
	}
	f, _ := new(big.Rat).SetFrac64(micros, 1_000_000).Float64()
	return f
}

// canGiveFeedback controlla se l'utente può dare feedback (rate limiting,
// _can_give_feedback). Restituisce (puòProcedere, messaggioErrore): il
// messaggio è vuoto se l'utente può procedere.
func (h *Handlers) canGiveFeedback(userID string) (bool, string) {
	lastFeedback := h.Store.GetLastFeedbackTime(userID)
	if lastFeedback == nil {
		return true, ""
	}

	// Parse timestamp (formato SQLite: "YYYY-MM-DD HH:MM:SS")
	lastTime, err := fromISOFormat(*lastFeedback)
	if err == nil && lastTime.aware {
		// now (naive) - last_time (aware) solleva TypeError
		err = &pyError{typ: "TypeError", msg: "can't subtract offset-naive and offset-aware datetimes"}
	}
	if err != nil {
		logger.Warn(fmt.Sprintf("Errore parsing timestamp feedback per %s: %s", userID, err))
		// In caso di errore, permetti comunque (safe default)
		return true, ""
	}

	timeDiff := nowMicros() - naiveMicros(lastTime.year, lastTime.month, lastTime.day,
		lastTime.hour, lastTime.minute, lastTime.second, lastTime.microsecond)

	if timeDiff < FeedbackCooldownHours*3600*1_000_000 {
		hoursRemaining := float64(FeedbackCooldownHours) - totalSeconds(timeDiff)/3600
		errorMsg := "⏰ Hai già inviato un feedback di recente!\n\n" +
			"Per evitare spam, puoi dare feedback una volta ogni " + strconv.Itoa(FeedbackCooldownHours) + "h.\n" +
			"Riprova tra circa " + strconv.FormatInt(int64(math.Trunc(hoursRemaining)), 10) + "h."
		return false, errorMsg
	}

	return true, ""
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

// FeedbackCommand è l'handler del comando /feedback (feedback_command): avvia
// la conversazione. Restituisce lo stato RATING se l'utente può procedere,
// handlers.EndConversation() se è in cooldown.
func (h *Handlers) FeedbackCommand(b *gotgbot.Bot, ctx *ext.Context) error {
	userID, err := effectiveUserID(ctx)
	if err != nil {
		return err
	}

	// Controlla rate limiting
	canProceed, errorMsg := h.canGiveFeedback(userID)
	if !canProceed {
		if err := replyText(b, ctx.Message, errorMsg, "", nil); err != nil {
			return err
		}
		return handlers.EndConversation()
	}

	// Reset context
	h.UserData.Clear(ctx.EffectiveUser.Id)

	// Mostra pulsanti rating
	replyMarkup := ratingKeyboard()
	if err := replyText(b, ctx.Message, msgFeedbackStart, parseModeHTML, &replyMarkup); err != nil {
		return err
	}
	return handlers.NextConversationState(StateRating)
}

// FeedbackRating gestisce la selezione del voto (feedback_rating): salva il
// numero di stelle in user_data e chiede il commento. Restituisce lo stato
// COMMENT.
func (h *Handlers) FeedbackRating(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := safeAnswerCallback(b, query); err != nil {
		return err
	}

	// Estrai rating dal callback_data (formato: "rating_N")
	parts := strings.Split(query.Data, "_")
	if len(parts) < 2 {
		return &pyError{typ: "IndexError", msg: "list index out of range"}
	}
	rating, err := pyInt(parts[1])
	if err != nil {
		return err
	}
	if ctx.EffectiveUser == nil {
		return attributeError("__setitem__")
	}
	h.UserData.Set(ctx.EffectiveUser.Id, keyRating, rating)

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

	if err := editMessageText(b, query, message, parseModeHTML, &replyMarkup); err != nil {
		return err
	}
	return handlers.NextConversationState(StateComment)
}

// rating restituisce context.user_data.get("rating") (nil se assente).
func (h *Handlers) rating(userID int64) *int {
	if v, ok := h.UserData.Get(userID, keyRating); ok {
		if r, ok := v.(int); ok {
			return &r
		}
	}
	return nil
}

// FeedbackComment gestisce il commento testuale (feedback_comment): salva
// il feedback e termina la conversazione, oppure resta nello stato COMMENT
// se il commento supera MaxCommentLength.
func (h *Handlers) FeedbackComment(b *gotgbot.Bot, ctx *ext.Context) error {
	userID, err := effectiveUserID(ctx)
	if err != nil {
		return err
	}
	rating := h.rating(ctx.EffectiveUser.Id)
	if ctx.Message == nil {
		return attributeError("text")
	}
	comment := pyStrip(ctx.Message.Text)

	// Validazione lunghezza commento
	if length := utf8.RuneCountInString(comment); length > MaxCommentLength {
		errorMessage := "❌ <b>Commento troppo lungo!</b>\n\n" +
			"Il tuo messaggio ha " + strconv.Itoa(length) + " caratteri, " +
			"ma il limite è " + strconv.Itoa(MaxCommentLength) + ".\n\n" +
			"💬 <i>Riprova con un messaggio più breve oppure premi /cancel per annullare.</i>"
		if err := replyText(b, ctx.Message, errorMessage, parseModeHTML, nil); err != nil {
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
	if err := replyText(b, ctx.Message, message, parseModeHTML, nil); err != nil {
		return err
	}
	return handlers.EndConversation()
}

// FeedbackSkipComment gestisce il pulsante «Salta» (feedback_skip_comment):
// salva il feedback senza commento e termina la conversazione.
func (h *Handlers) FeedbackSkipComment(b *gotgbot.Bot, ctx *ext.Context) error {
	query := ctx.CallbackQuery
	if err := safeAnswerCallback(b, query); err != nil {
		return err
	}

	userID, err := effectiveUserID(ctx)
	if err != nil {
		return err
	}
	rating := h.rating(ctx.EffectiveUser.Id)

	// Salva feedback senza commento
	success := h.Store.SaveFeedback(userID, "command", rating, nil)

	message := msgFeedbackError
	if success {
		message = msgFeedbackSaved
	}
	if err := editMessageText(b, query, message, parseModeHTML, nil); err != nil {
		return err
	}
	return handlers.EndConversation()
}

// FeedbackCancel annulla la conversazione feedback (feedback_cancel):
// risponde al messaggio o, per una callback, modifica il messaggio del
// pulsante. Restituisce sempre handlers.EndConversation().
func (h *Handlers) FeedbackCancel(b *gotgbot.Bot, ctx *ext.Context) error {
	var err error
	if ctx.Message != nil {
		err = replyText(b, ctx.Message, msgFeedbackCancelled, "", nil)
	} else if ctx.CallbackQuery != nil {
		err = editMessageText(b, ctx.CallbackQuery, msgFeedbackCancelled, "", nil)
	}
	if err != nil {
		return err
	}
	return handlers.EndConversation()
}

// Conversation costruisce il ConversationHandler del feedback come in
// bot.py:
//
//	entry_points=[CommandHandler("feedback", feedback_command)]
//	RATING:  [CallbackQueryHandler(feedback_rating, pattern=r"^rating_\d$")]
//	COMMENT: [MessageHandler(filters.TEXT & ~filters.COMMAND, feedback_comment),
//	          CallbackQueryHandler(feedback_skip_comment, pattern=r"^skip_comment$")]
//	fallbacks=[CommandHandler("cancel", feedback_cancel)]
//
// I pattern replicano re.match di Python: \d è una cifra Unicode (Nd) e $
// accetta anche un "\n" finale. Il MessageHandler accetta anche messaggi
// modificati e business (filtri PTB), che FeedbackComment tratta come
// update.message None (eccezione, stato invariato). Lo stato è in memoria
// per utente e chat (per_user/per_chat di PTB).
func (h *Handlers) Conversation() handlers.Conversation {
	return handlers.NewConversation(
		[]ext.Handler{commands.NewCommandHandler("feedback", h.FeedbackCommand)},
		map[string][]ext.Handler{
			StateRating: {
				handlers.NewCallback(tg.CallbackPattern(`^rating_\p{Nd}\n?$`), h.FeedbackRating),
			},
			StateComment: {
				handlers.NewMessage(tg.TextNotCommand, h.FeedbackComment).SetAllowEdited(true).SetAllowBusiness(true),
				handlers.NewCallback(tg.CallbackPattern(`^skip_comment\n?$`), h.FeedbackSkipComment),
			},
		},
		&handlers.ConversationOpts{
			Fallbacks:    []ext.Handler{commands.NewCommandHandler("cancel", h.FeedbackCancel)},
			StateStorage: conversation.NewInMemoryStorage(conversation.KeyStrategySenderAndChat),
		},
	)
}

// ========== Equivalenti di builtin Python ==========

// pyIsSpace replica str.isspace() per un carattere: rispetto a
// unicode.IsSpace include anche i separatori U+001C..U+001F.
func pyIsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// pyStrip replica str.strip() senza argomenti.
func pyStrip(s string) string {
	return strings.TrimFunc(s, pyIsSpace)
}

// pyInt replica int(s) di Python per stringhe in base 10: spazi iniziali e
// finali, segno, cifre Unicode (Nd) e underscore singoli tra le cifre.
func pyInt(s string) (int, error) {
	invalid := func() (int, error) {
		return 0, valueError("invalid literal for int() with base 10: " + pyStrRepr(s))
	}
	t := pyStrip(s)
	neg := false
	if t != "" && (t[0] == '+' || t[0] == '-') {
		neg = t[0] == '-'
		t = t[1:]
	}
	if t == "" {
		return invalid()
	}
	n := 0
	prevUnderscore := true // vieta "_" iniziale
	for _, r := range t {
		if r == '_' {
			if prevUnderscore {
				return invalid()
			}
			prevUnderscore = true
			continue
		}
		d := digitValue(r)
		if d < 0 {
			return invalid()
		}
		if n > (math.MaxInt32-d)/10 {
			// Python costruirebbe "⭐" * rating fino a esaurire la memoria;
			// con il pattern ^rating_\d$ il voto ha comunque una sola cifra.
			return 0, &pyError{typ: "MemoryError", msg: ""}
		}
		n = n*10 + d
		prevUnderscore = false
	}
	if prevUnderscore {
		return invalid()
	}
	if neg {
		n = -n
	}
	return n, nil
}

// digitValue restituisce il valore di una cifra decimale Unicode (Nd), -1
// se r non è una cifra. Unicode assegna le cifre Nd in blocchi contigui da
// 0 a 9, quindi il valore è la posizione nel blocco modulo 10.
func digitValue(r rune) int {
	if !unicode.IsDigit(r) {
		return -1
	}
	n := 0
	for unicode.IsDigit(r - rune(n) - 1) {
		n++
	}
	return n % 10
}

// Verifica statica: la conversazione è un ext.Handler.
var _ ext.Handler = handlers.Conversation{}
