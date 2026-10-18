package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/tg"
)

// Questo file contiene gli equivalenti gotgbot dei pezzi di
// python-telegram-bot (v22.6) usati da handlers/commands.py e da bot.py:
// Message.reply_text, CommandHandler e la semantica "valore di ritorno
// ignorato" degli handler registrati fuori da una conversazione.

// attributeError replica l'AttributeError di Python quando un attributo
// dell'update è None (es. update.message.reply_text su un messaggio
// modificato): in Python l'eccezione arriva all'error handler
// dell'applicazione. Stesso formato del package registration.
func attributeError(attr string) error {
	return fmt.Errorf("AttributeError: 'NoneType' object has no attribute '%s'", attr)
}

// replyText replica Message.reply_text di python-telegram-bot: invia text
// nella chat del messaggio, nello stesso topic (se il messaggio è in un
// topic), con la stessa business connection e lo stesso topic dei messaggi
// diretti. Come da convenzione del porting il messaggio originale non viene
// citato (PTB lo cita solo nelle chat non private). parseMode vuoto e
// markup nil corrispondono ai parametri omessi; msg nil replica
// l'AttributeError di update.message.reply_text con update.message None.
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

// Standalone adatta un handler per l'uso fuori da una conversazione: PTB
// ignora il valore restituito dagli handler non di conversazione, quindi un
// cambio di stato (ConversationHandler.END in Python, cioè
// handlers.EndConversation()) viene scartato invece di arrivare all'error
// handler del dispatcher. Gli altri errori passano invariati.
//
// Esempio (bot.py: app.add_handler(CommandHandler("status", status))):
//
//	dispatcher.AddHandler(commands.NewCommandHandler("status", commands.Standalone(h.Status)))
func Standalone(r handlers.Response) handlers.Response {
	return func(b *gotgbot.Bot, ctx *ext.Context) error {
		err := r(b, ctx)
		var stateChange *handlers.ConversationStateChange
		if errors.As(err, &stateChange) {
			return nil
		}
		return err
	}
}

// CommandHandler replica telegram.ext.CommandHandler di python-telegram-bot
// (v22.6) con il filtro di default filters.UpdateType.MESSAGES:
//   - considera update.message e update.edited_message (non i post dei
//     canali, le callback o i messaggi business);
//   - richiede un testo (non una caption) la cui prima entità sia un
//     bot_command con offset 0;
//   - il comando è text[1:length dell'entità] (slicing per code point come
//     in Python), quindi "/status!" o "/status-x" corrispondono a /status;
//   - l'eventuale @username deve essere quello del bot; il confronto usa
//     str.lower() di Python.
//
// Diversamente da handlers.Command di gotgbot non accetta le caption e non
// richiede che il comando sia seguito da uno spazio.
type CommandHandler struct {
	command  string
	response handlers.Response
}

var _ ext.Handler = CommandHandler{}

// NewCommandHandler crea un CommandHandler per command (CommandHandler(command,
// callback) di PTB); il comando è confrontato senza distinzione tra
// maiuscole e minuscole.
func NewCommandHandler(command string, r handlers.Response) CommandHandler {
	return CommandHandler{command: pyLower(command), response: r}
}

// CheckUpdate implementa ext.Handler (CommandHandler.check_update).
func (c CommandHandler) CheckUpdate(b *gotgbot.Bot, ctx *ext.Context) bool {
	msg := ctx.Message
	if msg == nil {
		msg = ctx.EditedMessage
	}
	if msg == nil || msg.Text == "" || !tg.IsCommand(msg) {
		return false
	}

	// command = message.text[1 : message.entities[0].length]
	runes := []rune(msg.Text)
	end := int(msg.Entities[0].Length)
	if end > len(runes) {
		end = len(runes)
	}
	command := ""
	if end > 1 {
		command = string(runes[1:end])
	}

	commandParts := strings.Split(command, "@")
	commandParts = append(commandParts, b.Username)
	return pyLower(commandParts[0]) == c.command && pyLower(commandParts[1]) == pyLower(b.Username)
}

// HandleUpdate implementa ext.Handler.
func (c CommandHandler) HandleUpdate(b *gotgbot.Bot, ctx *ext.Context) error {
	return c.response(b, ctx)
}

// Name implementa ext.Handler.
func (c CommandHandler) Name() string {
	return fmt.Sprintf("ptb_command_%s_%p", c.command, c.response)
}

// pyLower replica str.lower() di Python. Rispetto a strings.ToLower
// differisce solo per U+0130 (İ), che Python converte in "i + U+0307" (mappatura
// completa di SpecialCasing) e Go nella sola "i": senza questa eccezione
// "/hİstory" corrisponderebbe a /history, mentre in Python no.
func pyLower(s string) string {
	if !strings.ContainsRune(s, 'İ') {
		return strings.ToLower(s)
	}
	var sb strings.Builder
	for _, part := range strings.SplitAfter(s, "İ") {
		if p, ok := strings.CutSuffix(part, "İ"); ok {
			sb.WriteString(strings.ToLower(p))
			sb.WriteString("i\u0307")
			continue
		}
		sb.WriteString(strings.ToLower(part))
	}
	return sb.String()
}
