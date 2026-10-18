package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/logging"
)

// BootstrapRetries è bootstrap_retries=3 di app.run_webhook: ogni chiamata
// di avvio (getMe, deleteWebhook, setWebhook) viene ritentata fino a 3
// volte dopo il primo tentativo.
const BootstrapRetries = 3

// BootstrapInterval è l'intervallo iniziale (secondi) tra i tentativi.
const BootstrapInterval = 1.0

// WebhookMaxConnections è max_connections di default di Updater.start_webhook.
const WebhookMaxConnections = 40

// networkLoopLogger replica il logger "telegram.ext" di network_retry_loop.
var networkLoopLogger = logging.Logger("telegram.ext")

// sleepFunc attende d o la cancellazione di ctx (asyncio.sleep).
type sleepFunc func(ctx context.Context, d time.Duration) error

// sleepCtx è l'attesa di default: restituisce ctx.Err() se ctx viene
// cancellato prima della scadenza.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// secondsDuration converte secondi (float, come asyncio.sleep) in Duration.
func secondsDuration(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

// formatSeconds replica la stampa di un numero di secondi Python (int o
// float): 600 → "600", 0.5 → "0.5".
func formatSeconds(s float64) string {
	return strconv.FormatFloat(s, 'f', -1, 64)
}

// networkRetryLoop replica network_retry_loop di PTB
// (telegram/ext/_utils/networkloop.py) con repeat_on_success=False: esegue
// action finché non riesce, ritentando dopo gli errori Telegram fino a
// maxRetries volte (< 0 = all'infinito).
//
//   - RetryAfter: attesa retry_after + 0,5 secondi;
//   - TimedOut: nuovo tentativo immediato;
//   - InvalidToken: nessun nuovo tentativo;
//   - altri TelegramError (inclusi gli errori di rete): attesa crescente
//     (×1,5 fino a 30 secondi);
//   - errori non Telegram: restituiti subito.
//
// Come in bot.py il logger "telegram" è a livello WARNING: i messaggi DEBUG
// di PTB non vengono riprodotti, solo quelli di errore all'interruzione.
func networkRetryLoop(ctx context.Context, description string, interval float64, maxRetries int, sleep sleepFunc, action func(ctx context.Context) error) error {
	logPrefix := fmt.Sprintf("Network Retry Loop (%s):", description)

	// checkMaxRetriesAndLog restituisce true se si deve interrompere.
	checkMaxRetriesAndLog := func(currentRetries int, exceptionInfo string) bool {
		prefixWithInfo := logPrefix
		if exceptionInfo != "" {
			prefixWithInfo = logPrefix + " " + exceptionInfo
		}
		if maxRetries < 0 || currentRetries < maxRetries {
			return false
		}
		networkLoopLogger.Error(fmt.Sprintf("%s Failed run number %d of %d. Aborting.", prefixWithInfo, currentRetries, maxRetries))
		return true
	}

	curInterval := interval
	for retries := 0; ; retries++ {
		err := action(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		e := describe(err)
		switch {
		case e.class == "RetryAfter":
			const slackTime = 0.5
			curInterval = slackTime + float64(retryAfterSeconds(err))
			exceptionInfo := fmt.Sprintf("%s. Adding %s seconds to the specified time.", e.msg, formatSeconds(slackTime))
			if checkMaxRetriesAndLog(retries, exceptionInfo) {
				return err
			}
		case e.class == "TimedOut":
			// Se il fallimento è un timeout, si riprova subito
			curInterval = 0
			if checkMaxRetriesAndLog(retries, fmt.Sprintf("Timed out: %s.", e.msg)) {
				return err
			}
		case e.class == "InvalidToken":
			networkLoopLogger.Error(fmt.Sprintf("%s Invalid token. Aborting retry loop.", logPrefix))
			return err
		case isInstance(e.class, "TelegramError"):
			if checkMaxRetriesAndLog(retries, "") {
				return err
			}
			// attese crescenti sugli errori successivi, fino a 30 secondi
			if curInterval == 0 {
				curInterval = 1
			} else {
				curInterval = min(30, 1.5*curInterval)
			}
		default:
			return err
		}

		if curInterval > 0 {
			if err := sleep(ctx, secondsDuration(curInterval)); err != nil {
				return err
			}
		}
	}
}

// initializeBot replica Bot.initialize() di PTB: getMe per verificare il
// token e conoscere lo username del bot (usato dai comandi /cmd@bot). Un
// token rifiutato diventa InvalidToken (il messaggio di PTB conterrebbe il
// token, qui omesso).
func initializeBot(ctx context.Context, b *gotgbot.Bot) error {
	user, err := b.GetMeWithContext(ctx, nil)
	if err != nil {
		if describe(err).class == "InvalidToken" {
			return pycompat.NewError("InvalidToken", "The token was rejected by the server.")
		}
		return err
	}
	b.User = *user
	return nil
}

// bootstrapWebhook replica Updater._bootstrap con drop_pending_updates=True:
// deleteWebhook(drop_pending_updates=True) e poi setWebhook con url,
// allowed_updates, max_connections=40, drop_pending_updates e secret_token,
// ciascuno con i retry di network_retry_loop.
func bootstrapWebhook(ctx context.Context, b *gotgbot.Bot, webhookURL, secretToken string, sleep sleepFunc) error {
	err := networkRetryLoop(ctx, "Bootstrap delete Webhook", BootstrapInterval, BootstrapRetries, sleep,
		func(ctx context.Context) error {
			_, err := b.DeleteWebhookWithContext(ctx, &gotgbot.DeleteWebhookOpts{DropPendingUpdates: true})
			return err
		})
	if err != nil {
		return err
	}
	return networkRetryLoop(ctx, "Bootstrap Set Webhook", BootstrapInterval, BootstrapRetries, sleep,
		func(ctx context.Context) error {
			_, err := b.SetWebhookWithContext(ctx, webhookURL, &gotgbot.SetWebhookOpts{
				MaxConnections:     WebhookMaxConnections,
				AllowedUpdates:     AllowedUpdates,
				DropPendingUpdates: true,
				SecretToken:        secretToken,
			})
			return err
		})
}
