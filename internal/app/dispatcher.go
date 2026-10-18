package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/config"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/handlers/commands"
	"github.com/dstmrk/octotracker/internal/handlers/feedback"
	"github.com/dstmrk/octotracker/internal/handlers/rateupdate"
	"github.com/dstmrk/octotracker/internal/handlers/registration"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/tg"
)

// DispatcherConfig contiene ciò che serve per costruire il dispatcher.
type DispatcherConfig struct {
	// Config fornisce CHECKER_HOUR, WEBAPP_URL, TELEGRAM_CHANNEL e
	// ADMIN_USER_ID.
	Config config.Config
	// Store è il database.
	Store *database.Store
	// UserData è context.user_data, condiviso da tutte le conversazioni;
	// nil crea uno store nuovo.
	UserData *tg.UserData
	// Sleep è l'attesa usata dall'error handler per il rate limit
	// (nil = time.Sleep).
	Sleep func(time.Duration)
}

// NewDispatcher costruisce il dispatcher del bot come main() di bot.py:
// handler registrati in RegisterHandlers, error handler ErrorHandler e
// update elaborati uno alla volta (MaxRoutines 1), come fa
// python-telegram-bot con concurrent_updates disabilitato (default).
func NewDispatcher(cfg DispatcherConfig) *ext.Dispatcher {
	eh := &ErrorHandler{AdminUserID: cfg.Config.AdminUserID, Sleep: cfg.Sleep}
	d := ext.NewDispatcher(&ext.DispatcherOpts{
		Error:            eh.HandleError,
		Panic:            eh.HandlePanic,
		UnhandledErrFunc: logUnhandledError,
		Logger:           ptbLogger("telegram.ext.Application"),
		MaxRoutines:      1,
	})
	RegisterHandlers(d, cfg)
	return d
}

// RegisterHandlers aggiunge al gruppo 0 gli handler di bot.py, nello stesso
// ordine:
//
//  1. conversazione di registrazione (/start, /update) con fallback
//     /cancel e /help;
//  2. conversazione feedback (/feedback);
//  3. conversazione di aggiornamento tariffe (pulsante rate_update_yes)
//     con fallback /cancel;
//  4. pulsante rate_update_no;
//  5. /cancel, /status, /history, /remove, /help;
//  6. MessageHandler(filters.COMMAND, unknown_command) per i comandi non
//     riconosciuti.
//
// Restituisce lo store user_data usato.
func RegisterHandlers(d *ext.Dispatcher, cfg DispatcherConfig) *tg.UserData {
	userData := cfg.UserData
	if userData == nil {
		userData = tg.NewUserData()
	}

	cmd := &commands.Handlers{
		Store:           cfg.Store,
		UserData:        userData,
		CheckerHour:     cfg.Config.CheckerHour,
		WebAppURL:       cfg.Config.WebappURL,
		TelegramChannel: cfg.Config.TelegramChannel,
	}
	reg := &registration.Handlers{
		Store:           cfg.Store,
		UserData:        userData,
		TelegramChannel: cfg.Config.TelegramChannel,
	}
	fb := &feedback.Handlers{Store: cfg.Store, UserData: userData}
	ru := &rateupdate.Handlers{Store: cfg.Store, UserData: userData}

	// Handler conversazione registrazione
	d.AddHandler(reg.Conversation([]ext.Handler{
		commands.NewCommandHandler("cancel", cmd.CancelConversation),
		commands.NewCommandHandler("help", cmd.HelpCommand),
	}))

	// Handler conversazione feedback
	d.AddHandler(fb.Conversation())

	// Handler per pulsanti aggiornamento tariffe nelle notifiche.
	// È una conversazione: dopo aver adottato un'offerta fissa, chiede la nuova scadenza.
	d.AddHandler(ru.Conversation([]ext.Handler{
		commands.NewCommandHandler("cancel", cmd.CancelConversation),
	}))
	d.AddHandler(ru.RateUpdateNoHandler())

	// Comandi fuori dalle conversazioni: PTB ignora il valore restituito
	// (ConversationHandler.END), da cui commands.Standalone.
	d.AddHandler(commands.NewCommandHandler("cancel", commands.Standalone(cmd.CancelConversation)))
	d.AddHandler(commands.NewCommandHandler("status", commands.Standalone(cmd.Status)))
	d.AddHandler(commands.NewCommandHandler("history", commands.Standalone(cmd.HistoryCommand)))
	d.AddHandler(commands.NewCommandHandler("remove", commands.Standalone(cmd.RemoveData)))
	d.AddHandler(commands.NewCommandHandler("help", commands.Standalone(cmd.HelpCommand)))

	// Handler per comandi non riconosciuti (deve essere dopo tutti gli altri
	// CommandHandler). Come MessageHandler di PTB considera anche i messaggi
	// modificati, i post dei canali, i messaggi business e i messaggi
	// inviati da bot.
	d.AddHandler(handlers.NewMessage(tg.AnyCommand, cmd.UnknownCommand).
		SetAllowEdited(true).
		SetAllowChannel(true).
		SetAllowBusiness(true).
		SetAllowBot(true))

	return userData
}

// logUnhandledError logga gli errori che il dispatcher non può passare
// all'error handler (es. update non decodificabile).
func logUnhandledError(err error) {
	logging.Critical(logging.Logger("telegram.ext.Application"), fmt.Sprintf("Errore non gestito nell'elaborazione dell'update: %v", err))
}

// ptbLogger restituisce il logger per i messaggi interni di gotgbot con il
// nome del logger PTB equivalente. bot.py imposta il logger "telegram" a
// WARNING: i messaggi DEBUG e INFO della libreria non vengono mai mostrati.
func ptbLogger(name string) *slog.Logger {
	return slog.New(minLevelHandler{inner: logging.Logger(name).Handler(), min: slog.LevelWarn})
}

// minLevelHandler scarta i record sotto min (logger.setLevel di Python).
type minLevelHandler struct {
	inner slog.Handler
	min   slog.Level
}

func (h minLevelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.inner.Enabled(ctx, l)
}

func (h minLevelHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.min {
		return nil
	}
	return h.inner.Handle(ctx, r)
}

func (h minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return minLevelHandler{inner: h.inner.WithAttrs(attrs), min: h.min}
}

func (h minLevelHandler) WithGroup(name string) slog.Handler {
	return minLevelHandler{inner: h.inner.WithGroup(name), min: h.min}
}
