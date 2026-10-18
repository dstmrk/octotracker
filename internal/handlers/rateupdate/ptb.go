package rateupdate

import (
	"errors"
	"fmt"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/tg"
)

// Equivalenti gotgbot dei metodi di python-telegram-bot (v22.6) usati da
// handlers/rate_update.py.

// errInaccessibleMessage replica il TypeError di CallbackQuery._get_message
// quando la callback non ha un messaggio.
var errInaccessibleMessage = errors.New("TypeError: Cannot edit an inaccessible message")

// attributeError replica l'AttributeError di Python su un attributo di None.
func attributeError(attr string) error {
	return fmt.Errorf("AttributeError: 'NoneType' object has no attribute '%s'", attr)
}

// callbackMessage restituisce callback_query.message come lo vede PTB, che
// lo decodifica sempre come Message (anche un InaccessibleMessage, con
// date 0 e senza testo); nil se la callback non ha messaggio (inline).
func callbackMessage(query *gotgbot.CallbackQuery) *gotgbot.Message {
	if query == nil {
		return nil
	}
	switch m := query.Message.(type) {
	case gotgbot.Message:
		return &m
	case *gotgbot.Message:
		return m
	case gotgbot.InaccessibleMessage:
		return m.ToMessage()
	case *gotgbot.InaccessibleMessage:
		return m.ToMessage()
	}
	return nil
}

// effectiveMessage replica update.effective_message: message,
// edited_message, poi il messaggio della callback, poi gli altri tipi.
func effectiveMessage(ctx *ext.Context) *gotgbot.Message {
	switch {
	case ctx.Message != nil:
		return ctx.Message
	case ctx.EditedMessage != nil:
		return ctx.EditedMessage
	case ctx.CallbackQuery != nil:
		return callbackMessage(ctx.CallbackQuery)
	}
	return ctx.EffectiveMessage
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
// sua business connection; una callback senza messaggio dà TypeError.
// gotgbot invia sempre reply_markup: senza tastiera viene inviato {}, che
// Telegram tratta come l'assenza del parametro (pulsanti rimossi).
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
		msg := callbackMessage(query)
		if msg == nil {
			return errInaccessibleMessage
		}
		opts.ChatId, opts.MessageId, opts.BusinessConnectionId = msg.Chat.Id, msg.MessageId, msg.BusinessConnectionId
	}
	_, _, err := b.EditMessageText(opts)
	return err
}

// answer replica await query.answer() (senza la protezione di
// safe_answer_callback): ogni errore viene propagato.
func answer(b *gotgbot.Bot, query *gotgbot.CallbackQuery) error {
	if query == nil {
		return attributeError("answer")
	}
	_, err := query.Answer(b, nil)
	return err
}

// safeAnswerCallback replica safe_answer_callback(query).
func safeAnswerCallback(b *gotgbot.Bot, query *gotgbot.CallbackQuery) error {
	if query == nil {
		return attributeError("answer")
	}
	return tg.SafeAnswerCallback(b, query)
}
