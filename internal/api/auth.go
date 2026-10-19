package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dstmrk/octotracker/internal/logging"
)

var authLogger = logging.Logger("api.auth")

// DefaultMaxAgeSeconds è l'età massima di default di auth_date (24 ore).
const DefaultMaxAgeSeconds = 86400

// nowUnix restituisce l'ora corrente in secondi Unix; sostituibile nei test.
var nowUnix = func() int64 { return time.Now().Unix() }

// TelegramAuthError è un errore di autenticazione: il messaggio diventa il
// campo "error" della risposta 401.
type TelegramAuthError struct {
	Message string
}

// Error restituisce il messaggio.
func (e *TelegramAuthError) Error() string { return e.Message }

// InitData sono i campi di initData (il primo valore di ogni chiave). Il
// campo "user", se non vuoto e JSON valido, contiene il JSON decodificato
// con encoding/json (un oggetto è map[string]any).
type InitData map[string]any

// User restituisce l'oggetto "user" (ok=false se manca o non è un oggetto JSON).
func (d InitData) User() (map[string]any, bool) {
	u, ok := d["user"].(map[string]any)
	return u, ok
}

// UserID restituisce user.id come stringa: le stringhe così come sono, i
// numeri in forma decimale (gli ID Telegram sono interi). Va chiamato su
// dati validati da ValidateInitData.
func (d InitData) UserID() string {
	u, _ := d.User()
	switch id := u["id"].(type) {
	case string:
		return id
	case float64:
		return strconv.FormatFloat(id, 'f', -1, 64)
	default:
		return fmt.Sprint(id)
	}
}

// firstValues analizza la query string tenendo il primo valore di ogni
// chiave; le coppie malformate vengono scartate.
func firstValues(initData string) InitData {
	q, _ := url.ParseQuery(initData)
	out := make(InitData, len(q))
	for k, v := range q {
		out[k] = v[0]
	}
	return out
}

// decodeUser sostituisce il campo "user", se non vuoto, con il suo JSON
// decodificato; se il JSON non è valido d resta invariato.
func decodeUser(d InitData) error {
	raw, _ := d["user"].(string)
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return err
	}
	d["user"] = v
	return nil
}

// ParseInitData analizza initData senza verificarne la firma: primo valore
// di ogni chiave e "user" decodificato (resta la stringa se non è JSON
// valido).
func ParseInitData(initData string) InitData {
	d := firstValues(initData)
	if err := decodeUser(d); err != nil {
		authLogger.Warn("Failed to parse user JSON in initData")
	}
	return d
}

// computeHash calcola l'hash atteso: HMAC-SHA256(data_check_string) con la
// chiave segreta HMAC-SHA256("WebAppData", bot_token), in esadecimale.
func computeHash(dataCheckString, botToken string) string {
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(dataCheckString))
	return hex.EncodeToString(mac.Sum(nil))
}

// dataCheckString ricostruisce la stringa firmata: "chiave=valore" per
// tutte le chiavi tranne hash, ordinate, separate da "\n".
func dataCheckString(params InitData) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + params[k].(string)
	}
	return strings.Join(lines, "\n")
}

// VerifyTelegramAuth verifica la firma HMAC-SHA256 di initData (algoritmo
// ufficiale Telegram, confronto a tempo costante) e restituisce i campi
// senza hash e con "user" decodificato, oppure nil se la firma manca o non
// è valida o se "user" non è JSON valido.
func VerifyTelegramAuth(initData, botToken string) InitData {
	if initData == "" || botToken == "" {
		return nil
	}
	params := firstValues(initData)

	receivedHash, _ := params["hash"].(string)
	delete(params, "hash")
	if receivedHash == "" {
		authLogger.Debug("No hash in initData")
		return nil
	}

	expectedHash := computeHash(dataCheckString(params), botToken)
	if !hmac.Equal([]byte(receivedHash), []byte(expectedHash)) {
		authLogger.Debug("Hash mismatch in initData verification")
		return nil
	}

	if err := decodeUser(params); err != nil {
		authLogger.Warn("Valid signature but invalid user JSON")
		return nil
	}
	return params
}

// ValidateInitData richiede firma valida, auth_date non più vecchio di
// maxAgeSeconds (auth_date assente vale 0) e un oggetto user con id. In
// caso di errore restituisce *TelegramAuthError.
func ValidateInitData(initData, botToken string, maxAgeSeconds int) (InitData, error) {
	authData := VerifyTelegramAuth(initData, botToken)
	if authData == nil {
		return nil, &TelegramAuthError{"Invalid Telegram authentication signature"}
	}

	var authDate int64
	if raw, ok := authData["auth_date"].(string); ok {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, &TelegramAuthError{"Invalid auth_date format: " + err.Error()}
		}
		authDate = v
	}
	// confronto senza sottrarre auth_date: nessun overflow con valori estremi
	if now := nowUnix(); authDate < now-int64(maxAgeSeconds) {
		return nil, &TelegramAuthError{fmt.Sprintf("Authentication expired (auth_date: %d, age: %ds, max: %ds)",
			authDate, now-authDate, maxAgeSeconds)}
	}

	user, ok := authData.User()
	if !ok || len(user) == 0 {
		return nil, &TelegramAuthError{"Missing user data in authentication"}
	}
	if _, ok := user["id"]; !ok {
		return nil, &TelegramAuthError{"Missing user id in authentication"}
	}

	authLogger.Debug("Telegram auth validated for user " + authData.UserID())
	return authData, nil
}
