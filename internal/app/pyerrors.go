package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/scraper"
	"github.com/dstmrk/octotracker/internal/tg"
)

// pyError è la vista Python di un errore Go: la classe dell'eccezione che
// python-telegram-bot o il codice Python avrebbero sollevato (type(e).__name__)
// e str(e). Serve per scegliere il ramo except giusto e per i testi dei log.
type pyError struct {
	class string
	msg   string
}

// pyTyped è implementata dagli errori che conoscono la propria classe Python.
type pyTyped interface {
	PyType() string
}

// pyParents è la gerarchia (classe → superclasse) delle eccezioni che
// compaiono nei rami except di bot.py e di python-telegram-bot (v22.6).
// Una classe non elencata deriva direttamente da Exception.
var pyParents = map[string]string{
	// telegram.error
	"TimedOut":         "NetworkError",
	"BadRequest":       "NetworkError",
	"NetworkError":     "TelegramError",
	"Forbidden":        "TelegramError",
	"InvalidToken":     "TelegramError",
	"Conflict":         "TelegramError",
	"ChatMigrated":     "TelegramError",
	"RetryAfter":       "TelegramError",
	"EndPointNotFound": "TelegramError",
	"TelegramError":    "Exception",
	// builtins
	"ConnectionError":        "OSError",
	"BrokenPipeError":        "ConnectionError",
	"ConnectionAbortedError": "ConnectionError",
	"ConnectionRefusedError": "ConnectionError",
	"ConnectionResetError":   "ConnectionError",
	"TimeoutError":           "OSError",
	"FileNotFoundError":      "OSError",
	"FileExistsError":        "OSError",
	"IsADirectoryError":      "OSError",
	"NotADirectoryError":     "OSError",
	"PermissionError":        "OSError",
	"InterruptedError":       "OSError",
	"BlockingIOError":        "OSError",
	"OSError":                "Exception",
	"KeyError":               "LookupError",
	"IndexError":             "LookupError",
	"LookupError":            "Exception",
	"JSONDecodeError":        "ValueError",
	"UnicodeDecodeError":     "UnicodeError",
	"UnicodeEncodeError":     "UnicodeError",
	"UnicodeError":           "ValueError",
	"ValueError":             "Exception",
	"OverflowError":          "ArithmeticError",
	"ZeroDivisionError":      "ArithmeticError",
	"ArithmeticError":        "Exception",
	"RecursionError":         "RuntimeError",
	"NotImplementedError":    "RuntimeError",
	"RuntimeError":           "Exception",
}

// isInstance replica isinstance(e, base) sulla classe class.
func isInstance(class, base string) bool {
	if base == "Exception" {
		return true
	}
	for c := class; c != ""; c = pyParents[c] {
		if c == base {
			return true
		}
	}
	return false
}

// ptbTelegramClass restituisce la classe di eccezione che
// python-telegram-bot (request/_baserequest.py) solleva per una risposta
// di errore dell'API: i parametri migrate_to_chat_id e retry_after hanno la
// precedenza, poi lo status HTTP.
func ptbTelegramClass(e *gotgbot.TelegramError) string {
	if p := e.ResponseParams; p != nil {
		if p.MigrateToChatId != 0 {
			return "ChatMigrated"
		}
		if p.RetryAfter != 0 {
			return "RetryAfter"
		}
	}
	switch e.Code {
	case http.StatusForbidden:
		return "Forbidden"
	case http.StatusNotFound, http.StatusUnauthorized:
		return "InvalidToken"
	case http.StatusBadRequest:
		return "BadRequest"
	case http.StatusConflict:
		return "Conflict"
	default:
		return "NetworkError"
	}
}

// describe restituisce classe Python e str(e) di un errore:
//
//   - errori dell'API Telegram → classe PTB (Forbidden, BadRequest, ...) e
//     il messaggio come lo mostra PTB (tg.ErrorString);
//   - eccezioni Python ricostruite dagli altri package (pycompat.Error,
//     scraper.PyException, checker.KeyError, errori con PyType()) → la loro
//     classe;
//   - timeout di trasporto → TimedOut ("Timed out"), altri errori di rete
//     di gotgbot → NetworkError;
//   - errori nella forma "NomeError: messaggio" (convenzione dei package
//     handlers) → NomeError e messaggio;
//   - qualunque altro errore → Exception con err.Error().
func describe(err error) pyError {
	if err == nil {
		return pyError{}
	}
	var tgErr *gotgbot.TelegramError
	if errors.As(err, &tgErr) {
		return pyError{class: ptbTelegramClass(tgErr), msg: tg.ErrorString(err)}
	}
	var pe *pycompat.Error
	if errors.As(err, &pe) {
		return pyError{class: pe.Class, msg: pe.Message}
	}
	var se *scraper.PyException
	if errors.As(err, &se) {
		return pyError{class: se.Type, msg: se.Msg}
	}
	var ke checker.KeyError
	if errors.As(err, &ke) {
		return pyError{class: "KeyError", msg: ke.Error()}
	}
	var typed pyTyped
	if errors.As(err, &typed) {
		return pyError{class: typed.PyType(), msg: err.Error()}
	}
	if isTransportTimeout(err) {
		return pyError{class: "TimedOut", msg: "Timed out"}
	}
	if isTransportError(err) {
		return pyError{class: "NetworkError", msg: err.Error()}
	}
	if name, msg, ok := strings.Cut(err.Error(), ": "); ok && isPyExceptionName(name) {
		return pyError{class: name, msg: msg}
	}
	return pyError{class: "Exception", msg: err.Error()}
}

// isTransportTimeout indica un timeout della richiesta HTTP verso Telegram
// (httpx.TimeoutException → telegram.error.TimedOut).
func isTransportTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// isTransportError indica un errore di rete o di decodifica della risposta
// di gotgbot (httpx.HTTPError → telegram.error.NetworkError).
func isTransportError(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "failed to execute POST request to ") ||
		strings.HasPrefix(msg, "failed to decode POST request to ")
}

// isPyExceptionName indica se s è il nome di una classe d'eccezione Python
// (identificatore ASCII che termina con "Error" o "Exception").
func isPyExceptionName(s string) bool {
	if !strings.HasSuffix(s, "Error") && !strings.HasSuffix(s, "Exception") {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// retryAfterSeconds restituisce error.retry_after (secondi) di un RetryAfter.
func retryAfterSeconds(err error) int64 {
	var tgErr *gotgbot.TelegramError
	if errors.As(err, &tgErr) && tgErr.ResponseParams != nil {
		return tgErr.ResponseParams.RetryAfter
	}
	return 0
}

// panicError converte il valore di un panic nell'errore corrispondente
// all'eccezione Python: un error resta tale, una stringa (anche nella forma
// "NomeError: messaggio") diventa un errore con lo stesso testo.
func panicError(r any) error {
	switch v := r.(type) {
	case error:
		return v
	case string:
		return errors.New(v)
	default:
		return fmt.Errorf("%v", r)
	}
}

// valueError crea un ValueError Python (le eccezioni sollevate da bot.py
// all'avvio).
func valueError(msg string) error {
	return pycompat.NewError("ValueError", msg)
}

// ExceptionLine restituisce l'ultima riga del traceback che Python
// stamperebbe per l'eccezione non gestita err ("ValueError: messaggio"),
// usata da cmd/octotracker per gli errori fatali.
func ExceptionLine(err error) string {
	e := describe(err)
	if e.msg == "" {
		return e.class
	}
	return e.class + ": " + e.msg
}
