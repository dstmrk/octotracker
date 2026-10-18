package registration

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/tg"
)

// errInaccessibleMessage replica il TypeError di CallbackQuery._get_message
// di python-telegram-bot quando la callback non ha un messaggio.
var errInaccessibleMessage = errors.New("TypeError: Cannot edit an inaccessible message")

// replyText replica Message.reply_text di python-telegram-bot (v22): invia
// text nella chat del messaggio, senza citarlo, nello stesso topic (se il
// messaggio è in un topic) e con la stessa business connection.
// parseMode vuoto e markup nil corrispondono ai parametri omessi.
func replyText(b *gotgbot.Bot, msg *gotgbot.Message, text, parseMode string, markup *gotgbot.InlineKeyboardMarkup) error {
	if msg == nil {
		return attributeError("reply_text")
	}
	opts := &gotgbot.SendMessageOpts{
		ParseMode:            parseMode,
		BusinessConnectionId: msg.BusinessConnectionId,
	}
	if msg.IsTopicMessage {
		opts.MessageThreadId = msg.MessageThreadId
	}
	if msg.DirectMessagesTopic != nil {
		opts.DirectMessagesTopicId = msg.DirectMessagesTopic.TopicId
	}
	if markup != nil {
		opts.ReplyMarkup = *markup
	}
	_, err := b.SendMessage(msg.Chat.Id, text, opts)
	return err
}

// editMessageText replica CallbackQuery.edit_message_text di
// python-telegram-bot (v22): modifica il messaggio inline
// (inline_message_id) o il messaggio a cui è associato il pulsante, con la
// sua business connection. PTB 22 decodifica il messaggio della callback
// sempre come Message, anche quando Telegram lo invia come inaccessibile
// (date 0), quindi anche in quel caso la modifica viene tentata; solo una
// callback senza messaggio restituisce errInaccessibleMessage.
//
// gotgbot invia sempre reply_markup: senza tastiera viene inviato {}, che
// Telegram tratta come l'assenza del parametro (tastiera rimossa).
func editMessageText(b *gotgbot.Bot, query *gotgbot.CallbackQuery, text, parseMode string, markup *gotgbot.InlineKeyboardMarkup) error {
	if query == nil {
		return attributeError("edit_message_text")
	}
	opts := &gotgbot.EditMessageTextOpts{Text: text, ParseMode: parseMode}
	if markup != nil {
		opts.ReplyMarkup = *markup
	}
	if query.InlineMessageId != "" {
		opts.InlineMessageId = query.InlineMessageId
	} else {
		switch m := query.Message.(type) {
		case gotgbot.Message:
			opts.ChatId, opts.MessageId, opts.BusinessConnectionId = m.Chat.Id, m.MessageId, m.BusinessConnectionId
		case *gotgbot.Message:
			opts.ChatId, opts.MessageId, opts.BusinessConnectionId = m.Chat.Id, m.MessageId, m.BusinessConnectionId
		case nil:
			return errInaccessibleMessage
		default: // InaccessibleMessage
			opts.ChatId, opts.MessageId = m.GetChat().Id, m.GetMessageId()
		}
	}
	_, _, err := b.EditMessageText(opts)
	return err
}

// safeAnswerCallback replica safe_answer_callback(query); con query nil
// replica l'AttributeError di Python.
func safeAnswerCallback(b *gotgbot.Bot, query *gotgbot.CallbackQuery) error {
	if query == nil {
		return attributeError("answer")
	}
	return tg.SafeAnswerCallback(b, query)
}

// ========== Repr Python (per i log) ==========

// pyStrRepr replica repr(str) di Python.
func pyStrRepr(s string) string {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var sb strings.Builder
	sb.WriteByte(quote)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == rune(quote) || r == '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '\t':
			sb.WriteString(`\t`)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case unicode.IsPrint(r): // stessi caratteri di str.isprintable()
			sb.WriteRune(r)
		case r < 0x100:
			sb.WriteString(`\x` + pad(strconv.FormatInt(int64(r), 16), 2))
		case r < 0x10000:
			sb.WriteString(`\u` + pad(strconv.FormatInt(int64(r), 16), 4))
		default:
			sb.WriteString(`\U` + pad(strconv.FormatInt(int64(r), 16), 8))
		}
	}
	sb.WriteByte(quote)
	return sb.String()
}

func pad(s string, n int) string {
	return strings.Repeat("0", n-len(s)) + s
}

// pyListRepr replica repr(list[str]) di Python, es. ['a', 'b'].
func pyListRepr(items []string) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = pyStrRepr(it)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
