package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/logging"
)

var authLogger = logging.Logger("api.auth")

// DefaultMaxAgeSeconds è l'età massima di default di auth_date (24 ore),
// il default di max_age_seconds in validate_init_data.
const DefaultMaxAgeSeconds = 86400

// nowUnix restituisce int(time.time()); sostituibile nei test.
var nowUnix = func() int64 { return time.Now().Unix() }

// TelegramAuthError è l'eccezione per gli errori di autenticazione Telegram.
type TelegramAuthError struct {
	Message string
}

// Error restituisce str(e).
func (e *TelegramAuthError) Error() string { return e.Message }

// InitData è il dict ottenuto da initData: chiave → valore (str). La
// chiave "user", se presente e non vuota, contiene il JSON decodificato con
// la semantica di json.loads (pycompat.Object per un oggetto JSON); se il
// JSON non è valido ParseInitData lascia la stringa originale.
type InitData map[string]any

// User restituisce il dict "user" (ok=false se manca o non è un oggetto JSON).
func (d InitData) User() (pycompat.Object, bool) {
	u, ok := d["user"].(pycompat.Object)
	return u, ok
}

// UserID replica get_user_id: str(auth_data["user"]["id"]). Va chiamato su
// dati validati da ValidateInitData (user è un dict con "id").
func (d InitData) UserID() string {
	u, _ := d.User()
	id, _ := u.Get("id")
	return pycompat.Str(id)
}

// firstValues replica {k: v[0] if v else "" for k, v in parse_qs(...).items()}.
func firstValues(initData string) InitData {
	fields, _ := pycompat.ParseQS(initData, true, 0) // senza max_num_fields non fallisce
	out := InitData{}
	for _, f := range fields {
		out[f.Name] = f.Values[0]
	}
	return out
}

// ParseInitData replica parse_init_data: analizza la query string di
// initData in un dict (primo valore di ogni chiave) e decodifica "user"
// come JSON. Stringa vuota → dict vuoto.
func ParseInitData(initData string) InitData {
	if initData == "" {
		return InitData{}
	}
	result := firstValues(initData)
	if raw, ok := result["user"].(string); ok && raw != "" {
		v, err := pycompat.Loads(raw)
		switch {
		case err == nil:
			result["user"] = v
		case pycompat.IsInstance(err, "JSONDecodeError"):
			authLogger.Warn("Failed to parse user JSON in initData")
		default:
			// RecursionError o ValueError (limite di cifre): l'except esterno
			authLogger.Warn(fmt.Sprintf("Failed to parse initData: %s", err.Error()))
			return InitData{}
		}
	}
	return result
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
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

// VerifyTelegramAuth replica verify_telegram_auth: verifica la firma
// HMAC-SHA256 di initData (algoritmo ufficiale Telegram, confronto a tempo
// costante) e restituisce i dati con "user" decodificato, oppure nil (None)
// se la firma manca o non è valida, o se "user" non è JSON valido.
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

	// hmac.compare_digest su str rifiuta i caratteri non ASCII (TypeError)
	if !isASCII(receivedHash) {
		authLogger.Error("Error verifying Telegram auth: comparing strings with non-ASCII characters is not supported")
		return nil
	}
	if !hmac.Equal([]byte(receivedHash), []byte(expectedHash)) {
		authLogger.Debug("Hash mismatch in initData verification")
		return nil
	}

	if raw, ok := params["user"].(string); ok && raw != "" {
		v, err := pycompat.Loads(raw)
		if err != nil {
			if pycompat.IsInstance(err, "JSONDecodeError") {
				authLogger.Warn("Valid signature but invalid user JSON")
			} else {
				authLogger.Error(fmt.Sprintf("Error verifying Telegram auth: %s", err.Error()))
			}
			return nil
		}
		params["user"] = v
	}
	return params
}

// ValidateInitData replica validate_init_data: firma valida, auth_date non
// più vecchio di maxAgeSeconds (DefaultMaxAgeSeconds in Python) e user con
// id. In caso di errore restituisce *TelegramAuthError con lo stesso
// messaggio di Python.
func ValidateInitData(initData, botToken string, maxAgeSeconds int) (InitData, error) {
	authData := VerifyTelegramAuth(initData, botToken)
	if authData == nil {
		return nil, &TelegramAuthError{"Invalid Telegram authentication signature"}
	}

	if err := checkAuthDate(authData, maxAgeSeconds); err != nil {
		return nil, err
	}

	user, isDict := authData.User()
	if !isDict || len(user) == 0 {
		return nil, &TelegramAuthError{"Missing user data in authentication"}
	}
	id, ok := user.Get("id")
	if !ok {
		return nil, &TelegramAuthError{"Missing user id in authentication"}
	}

	authLogger.Debug(fmt.Sprintf("Telegram auth validated for user %s", pycompat.Str(id)))
	return authData, nil
}

// checkAuthDate replica il blocco try di validate_init_data su auth_date:
// int(auth_data.get("auth_date", 0)) con interi Python di precisione
// arbitraria; ValueError (anche dalla formattazione di interi oltre 4300
// cifre) → "Invalid auth_date format: ...".
func checkAuthDate(authData InitData, maxAgeSeconds int) error {
	invalid := func(err error) error {
		return &TelegramAuthError{"Invalid auth_date format: " + err.Error()}
	}
	authDate := new(big.Int)
	if raw, ok := authData["auth_date"].(string); ok {
		v, err := pycompat.ParseInt(raw)
		if err != nil {
			return invalid(err)
		}
		authDate = v
	}
	currentTime := big.NewInt(nowUnix())
	age := new(big.Int).Sub(currentTime, authDate)
	if age.Cmp(big.NewInt(int64(maxAgeSeconds))) <= 0 {
		return nil
	}
	authDateStr, err := pycompat.FormatInt(authDate)
	if err != nil {
		return invalid(err)
	}
	ageStr, err := pycompat.FormatInt(age)
	if err != nil {
		return invalid(err)
	}
	return &TelegramAuthError{fmt.Sprintf("Authentication expired (auth_date: %s, age: %ss, max: %ds)",
		authDateStr, ageStr, maxAgeSeconds)}
}
