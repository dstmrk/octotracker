package feedback

import (
	"errors"
	"fmt"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/tg"
)

// Equivalenti gotgbot dei metodi di python-telegram-bot (v22.6) usati da
// handlers/feedback.py.

// errInaccessibleMessage replica il TypeError di CallbackQuery._get_message
// quando la callback non ha un messaggio.
var errInaccessibleMessage = errors.New("TypeError: Cannot edit an inaccessible message")

// attributeError replica l'AttributeError di Python su un attributo di
// None (es. update.message.text con update.message None).
func attributeError(attr string) error {
	return fmt.Errorf("AttributeError: 'NoneType' object has no attribute '%s'", attr)
}

// replyText replica Message.reply_text: invia text nella chat del
// messaggio, senza citarlo, nello stesso topic (se il messaggio è in un
// topic) e con la stessa business connection. parseMode vuoto e markup nil
// corrispondono ai parametri omessi; msg nil replica l'AttributeError.
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

// editMessageText replica CallbackQuery.edit_message_text: modifica il
// messaggio inline (inline_message_id) o il messaggio del pulsante, con la
// sua business connection. PTB decodifica il messaggio della callback
// sempre come Message (anche se inaccessibile, date 0), quindi la modifica
// viene tentata anche in quel caso; solo una callback senza messaggio dà
// TypeError. gotgbot invia sempre reply_markup: senza tastiera viene
// inviato {}, che Telegram tratta come l'assenza del parametro.
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

// safeAnswerCallback replica safe_answer_callback(query); con query None
// replica l'AttributeError di Python.
func safeAnswerCallback(b *gotgbot.Bot, query *gotgbot.CallbackQuery) error {
	if query == nil {
		return attributeError("answer")
	}
	return tg.SafeAnswerCallback(b, query)
}
