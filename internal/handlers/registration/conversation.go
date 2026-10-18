package registration

import (
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/conversation"

	"github.com/dstmrk/octotracker/internal/tg"
)

// Conversation costruisce il ConversationHandler di registrazione come in
// bot.py: entry point /start e /update (entrambi → Start), gli handler per
// stato con gli stessi filtri e fallbacks forniti dall'applicazione
// (/cancel e /help del package dei comandi). Lo stato è in memoria con la
// chiave di default (utente + chat), come per_user/per_chat di PTB.
//
// Per fedeltà a python-telegram-bot:
//   - gli entry point usano commandHandler, che replica CommandHandler
//     (anche sui messaggi modificati, filters.UpdateType.MESSAGES);
//   - i MessageHandler(filters.TEXT & ~filters.COMMAND) accettano anche
//     messaggi modificati e business, che gli handler ignorano restando
//     nello stesso stato (update.message è None);
//   - i CallbackQueryHandler senza pattern accettano qualsiasi callback;
//     quelli di SKIP_SCADENZA_* usano il pattern ^...$ e sono valutati
//     prima del MessageHandler dello stesso stato.
func (h *Handlers) Conversation(fallbacks []ext.Handler) handlers.Conversation {
	text := func(r handlers.Response) ext.Handler {
		return handlers.NewMessage(tg.TextNotCommand, r).SetAllowEdited(true).SetAllowBusiness(true)
	}
	callback := func(r handlers.Response) ext.Handler {
		return handlers.NewCallback(nil, r)
	}
	skip := func(data string, r handlers.Response) ext.Handler {
		return handlers.NewCallback(tg.CallbackPattern("^"+data+"$"), r)
	}

	return handlers.NewConversation(
		[]ext.Handler{
			newCommandHandler("start", h.Start),
			newCommandHandler("update", h.Start),
		},
		map[string][]ext.Handler{
			StateTipoTariffa:       {callback(h.TipoTariffa)},
			StateLuceTipoVariabile: {callback(h.LuceTipoVariabile)},
			StateLuceEnergia:       {text(h.LuceEnergia)},
			StateLuceComm:          {text(h.LuceComm)},
			StateVuoiConsumiLuce:   {callback(h.VuoiConsumiLuce)},
			StateLuceConsumoF1:     {text(h.LuceConsumoF1)},
			StateLuceConsumoF2:     {text(h.LuceConsumoF2)},
			StateLuceConsumoF3:     {text(h.LuceConsumoF3)},
			StateLuceAttivazione: {
				skip(SkipScadenzaLuce, h.SkipLuceAttivazione),
				text(h.LuceAttivazione),
			},
			StateHaGas:      {callback(h.HaGas)},
			StateGasTipo:    {callback(h.GasTipoTariffa)},
			StateGasEnergia: {text(h.GasEnergia)},
			StateGasComm:    {text(h.GasComm)},
			StateGasAttivazione: {
				skip(SkipScadenzaGas, h.SkipGasAttivazione),
				text(h.GasAttivazione),
			},
			StateVuoiConsumiGas: {callback(h.VuoiConsumiGas)},
			StateGasConsumo:     {text(h.GasConsumo)},
		},
		&handlers.ConversationOpts{
			Fallbacks:    fallbacks,
			StateStorage: conversation.NewInMemoryStorage(conversation.KeyStrategySenderAndChat),
		},
	)
}

// commandHandler replica telegram.ext.CommandHandler (python-telegram-bot
// v22) con il filtro di default filters.UpdateType.MESSAGES:
//   - considera update.message e update.edited_message;
//   - richiede testo (non caption) la cui prima entità sia un bot_command
//     con offset 0;
//   - il comando è text[1:length dell'entità], quindi "/start!" o
//     "/start-x" corrispondono a /start come in PTB;
//   - l'eventuale @username deve essere quello del bot (senza distinzione
//     tra maiuscole e minuscole).
//
// Diversamente da handlers.Command di gotgbot non accetta le caption e non
// richiede che il comando sia seguito da uno spazio.
type commandHandler struct {
	command  string
	response handlers.Response
}

var _ ext.Handler = commandHandler{}

// newCommandHandler crea un commandHandler per il comando (case-insensitive).
func newCommandHandler(command string, r handlers.Response) commandHandler {
	return commandHandler{command: strings.ToLower(command), response: r}
}

// CheckUpdate implementa ext.Handler.
func (c commandHandler) CheckUpdate(b *gotgbot.Bot, ctx *ext.Context) bool {
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
	// Confronto con str.lower() come PTB (non con il case folding Unicode).
	target, username := strings.ToLower(commandParts[1]), strings.ToLower(b.Username)
	return strings.ToLower(commandParts[0]) == c.command && target == username
}

// HandleUpdate implementa ext.Handler.
func (c commandHandler) HandleUpdate(b *gotgbot.Bot, ctx *ext.Context) error {
	return c.response(b, ctx)
}

// Name implementa ext.Handler.
func (c commandHandler) Name() string {
	return "registration_command_" + c.command
}
