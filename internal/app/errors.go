package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// Classificazione degli errori usata dall'error handler, dai tentativi di
// avvio e dai log dello scheduler.

// apiError restituisce l'errore dell'API Telegram contenuto in err, o nil.
func apiError(err error) *gotgbot.TelegramError {
	var e *gotgbot.TelegramError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// retryAfter restituisce i secondi di attesa chiesti da un errore di rate
// limit dell'API (0 se err non lo è). Una migrazione del gruppo ha la
// precedenza sul rate limit.
func retryAfter(err error) int64 {
	if e := apiError(err); e != nil && e.ResponseParams != nil && e.ResponseParams.MigrateToChatId == 0 {
		return e.ResponseParams.RetryAfter
	}
	return 0
}

// isTimeout indica un timeout della richiesta.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout()
}

// isInvalidToken indica che Telegram ha rifiutato il token del bot.
func isInvalidToken(err error) bool {
	e := apiError(err)
	return e != nil && (e.Code == http.StatusUnauthorized || e.Code == http.StatusNotFound)
}

// isNetworkError indica un errore transitorio verso Telegram, per cui basta
// riprovare: timeout, errore di rete o risposta illeggibile, oppure una
// risposta dell'API che non è un rate limit, una migrazione, un token
// rifiutato, un divieto o un conflitto (es. 400 e 5xx).
func isNetworkError(err error) bool {
	if e := apiError(err); e != nil {
		if p := e.ResponseParams; p != nil && (p.RetryAfter != 0 || p.MigrateToChatId != 0) {
			return false
		}
		switch e.Code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict:
			return false
		}
		return true
	}
	return isTimeout(err) || isConnectionError(err) ||
		strings.HasPrefix(err.Error(), "failed to decode POST request to ")
}

// isConnectionError indica un errore di connessione (rete o richiesta HTTP).
func isConnectionError(err error) bool {
	var opErr *net.OpError
	var urlErr *url.Error
	return errors.As(err, &opErr) || errors.As(err, &urlErr)
}

// isIOError indica un errore del file system.
func isIOError(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr)
}

// errorType è il tipo dell'errore mostrato nei log e nell'alert all'admin:
// "TelegramError <codice>" per gli errori dell'API, altrimenti il tipo Go
// dell'errore più interno.
func errorType(err error) string {
	if e := apiError(err); e != nil {
		return fmt.Sprintf("TelegramError %d", e.Code)
	}
	for u := errors.Unwrap(err); u != nil; u = errors.Unwrap(err) {
		err = u
	}
	return fmt.Sprintf("%T", err)
}

// panicError converte il valore di un panic in errore.
func panicError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}
