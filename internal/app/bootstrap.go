package app

import (
	"context"
	"fmt"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/tg"
)

// BootstrapRetries è il numero di nuovi tentativi di ogni chiamata di avvio
// (getMe, deleteWebhook, setWebhook) dopo il primo fallito.
const BootstrapRetries = 3

// BootstrapInterval è l'attesa iniziale tra i tentativi di avvio.
const BootstrapInterval = time.Second

// Limiti delle attese tra i tentativi di avvio.
const (
	// maxRetryInterval è l'attesa massima dopo un errore ripetuto.
	maxRetryInterval = 30 * time.Second
	// retryAfterSlack si aggiunge al retry_after di un rate limit.
	retryAfterSlack = 500 * time.Millisecond
)

// WebhookMaxConnections è il max_connections passato a setWebhook.
const WebhookMaxConnections = 40

// sleepFunc attende d o la cancellazione di ctx.
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

// networkRetryLoop esegue action finché non riesce, con al massimo
// maxRetries nuovi tentativi (< 0 = senza limite). L'attesa prima di un
// nuovo tentativo dipende dall'errore (nextRetryWait); un errore che non va
// ritentato viene restituito subito.
func networkRetryLoop(ctx context.Context, description string, interval time.Duration, maxRetries int, sleep sleepFunc, action func(ctx context.Context) error) error {
	wait := interval
	for retries := 0; ; retries++ {
		err := action(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		next, retry := nextRetryWait(err, wait)
		if !retry || retries == maxRetries {
			logGiveUp(description, err, retry, retries+1)
			return err
		}
		wait = next
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// logGiveUp logga perché networkRetryLoop rinuncia dopo attempts tentativi:
// token rifiutato, oppure tentativi esauriti per un errore ritentabile.
func logGiveUp(description string, err error, retryable bool, attempts int) {
	switch {
	case isInvalidToken(err):
		logger.Error(fmt.Sprintf("❌ %s: token del bot rifiutato da Telegram", description))
	case retryable:
		logger.Error(fmt.Sprintf("❌ %s: fallito dopo %d tentativi: %s", description, attempts, tg.ErrorString(err)))
	}
}

// nextRetryWait restituisce l'attesa prima di ritentare dopo err (wait è
// l'attesa precedente) e false se err non va ritentato:
//
//   - rate limit: retry_after più mezzo secondo;
//   - timeout: nuovo tentativo immediato;
//   - token rifiutato ed errori che non vengono da Telegram: nessun nuovo
//     tentativo;
//   - altri errori dell'API o di rete: attesa crescente (×1,5 fino a 30 s,
//     1 s dopo un timeout).
func nextRetryWait(err error, wait time.Duration) (time.Duration, bool) {
	switch {
	case retryAfter(err) > 0:
		return time.Duration(retryAfter(err))*time.Second + retryAfterSlack, true
	case isTimeout(err):
		return 0, true
	case isInvalidToken(err):
		return 0, false
	case apiError(err) != nil || isNetworkError(err):
		if wait == 0 {
			return time.Second, true
		}
		return min(maxRetryInterval, wait*3/2), true
	}
	return 0, false
}

// initializeBot verifica il token con getMe e memorizza l'utente del bot
// (lo username serve per i comandi /cmd@bot).
func initializeBot(ctx context.Context, b *gotgbot.Bot) error {
	user, err := b.GetMeWithContext(ctx, nil)
	if err != nil {
		return err
	}
	b.User = *user
	return nil
}

// bootstrapWebhook esegue deleteWebhook(drop_pending_updates) e poi
// setWebhook con url, allowed_updates, max_connections, drop_pending_updates
// e secret_token, ciascuno con i tentativi di networkRetryLoop.
func bootstrapWebhook(ctx context.Context, b *gotgbot.Bot, webhookURL, secretToken string, sleep sleepFunc) error {
	err := networkRetryLoop(ctx, "deleteWebhook", BootstrapInterval, BootstrapRetries, sleep,
		func(ctx context.Context) error {
			_, err := b.DeleteWebhookWithContext(ctx, &gotgbot.DeleteWebhookOpts{DropPendingUpdates: true})
			return err
		})
	if err != nil {
		return err
	}
	return networkRetryLoop(ctx, "setWebhook", BootstrapInterval, BootstrapRetries, sleep,
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
