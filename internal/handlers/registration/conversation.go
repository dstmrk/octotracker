package registration

import (
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/conversation"

	"github.com/dstmrk/octotracker/internal/tg"
)

// Conversation costruisce la conversazione di registrazione: entry point
// /start e /update (entrambi → Start), gli handler per stato e i fallbacks
// forniti dall'applicazione (/cancel e /help del package dei comandi). Lo
// stato è in memoria per utente e chat.
//
//   - gli handler di testo accettano anche messaggi modificati e business,
//     che gli handler ignorano restando nello stesso stato;
//   - le callback senza pattern accettano qualsiasi pulsante; quelle di
//     SkipScadenza* sono valutate prima del testo dello stesso stato.
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
			tg.NewCommand("start", h.Start),
			tg.NewCommand("update", h.Start),
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
