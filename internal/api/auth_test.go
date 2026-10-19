package api

// Port di tests/test_api_auth.py (20 casi) più casi limite aggiuntivi.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/dstmrk/octotracker/internal/api/apitest"
)

// testBotToken è il bot token di test (non reale).
const testBotToken = "1234567890:ABCdefGHIjklMNOpqrsTUVwxyz123456789"

type initDataOpts struct {
	userID    int64
	firstName string
	lastName  string
	username  string
	authDate  *int64
	botToken  string
}

// signParams firma le coppie come fa Telegram e restituisce la query string.
func signParams(pairs [][2]string, botToken string) string {
	keys := make([]string, 0, len(pairs))
	m := map[string]string{}
	for _, p := range pairs {
		keys = append(keys, p[0])
		m[p[0]] = p[1]
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + m[k]
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	parts := make([]string, 0, len(pairs)+1)
	for _, p := range pairs {
		parts = append(parts, url.QueryEscape(p[0])+"="+url.QueryEscape(p[1]))
	}
	parts = append(parts, "hash="+hex.EncodeToString(mac.Sum(nil)))
	return strings.Join(parts, "&")
}

// generateTestInitData genera initData valido (generate_test_init_data).
func generateTestInitData(o initDataOpts) string {
	if o.userID == 0 {
		o.userID = 123456789
	}
	if o.firstName == "" {
		o.firstName = "Mario"
	}
	if o.lastName == "" {
		o.lastName = "Rossi"
	}
	if o.username == "" {
		o.username = "mariorossi"
	}
	if o.botToken == "" {
		o.botToken = testBotToken
	}
	authDate := nowUnix()
	if o.authDate != nil {
		authDate = *o.authDate
	}
	user, _ := json.Marshal(struct {
		ID           int64  `json:"id"`
		FirstName    string `json:"first_name"`
		LastName     string `json:"last_name"`
		Username     string `json:"username"`
		LanguageCode string `json:"language_code"`
	}{o.userID, o.firstName, o.lastName, o.username, "it"})
	return signParams([][2]string{{"auth_date", strconv.FormatInt(authDate, 10)}, {"user", string(user)}}, o.botToken)
}

func userIDOf(t *testing.T, d InitData) any {
	t.Helper()
	u, ok := d.User()
	if !ok {
		t.Fatalf("user non è un dict: %#v", d["user"])
	}
	return u["id"]
}

// numEq verifica che v sia il numero JSON n.
func numEq(v any, n int64) bool {
	f, ok := v.(float64)
	return ok && f == float64(n)
}

// ========== Parsing di initData ==========

// parseInitData analizza initData come VerifyTelegramAuth, senza firma.
func parseInitData(initData string) (InitData, error) {
	d := firstValues(initData)
	return d, decodeUser(d)
}

func TestParseValidInitData(t *testing.T) {
	result, err := parseInitData(generateTestInitData(initDataOpts{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"auth_date", "user", "hash"} {
		if _, ok := result[k]; !ok {
			t.Errorf("manca %q", k)
		}
	}
}

func TestParseExtractsUserJSON(t *testing.T) {
	result, _ := parseInitData(generateTestInitData(initDataOpts{userID: 999, firstName: "Test", username: "testuser"}))
	user, ok := result.User()
	if !ok {
		t.Fatalf("user non è un dict: %#v", result["user"])
	}
	if !numEq(user["id"], 999) || user["first_name"] != "Test" || user["username"] != "testuser" {
		t.Errorf("user = %#v", user)
	}
}

func TestParseEmptyString(t *testing.T) {
	if result, err := parseInitData(""); err != nil || result == nil || len(result) != 0 {
		t.Errorf("parseInitData(\"\") = %#v, %v", result, err)
	}
}

func TestParseInvalidFormat(t *testing.T) {
	result, _ := parseInitData("not_valid_query_string")
	if v, ok := result["not_valid_query_string"]; !ok || v != "" {
		t.Errorf("result = %#v", result)
	}
}

func TestParsePreservesHash(t *testing.T) {
	result, _ := parseInitData(generateTestInitData(initDataOpts{}))
	if h, _ := result["hash"].(string); len(h) != 64 {
		t.Errorf("hash = %q", h)
	}
}

func TestParseInitDataEdgeCases(t *testing.T) {
	// JSON non valido: errore e resta la stringa
	r, err := parseInitData("user=%7Bx&auth_date=1")
	if err == nil || r["user"] != "{x" || r["auth_date"] != "1" {
		t.Errorf("JSON non valido: %#v, %v", r, err)
	}
	// user vuoto: nessun parsing; chiavi duplicate: vince il primo valore
	if r, err := parseInitData("user=&a=1&a=2"); err != nil || r["user"] != "" || r["a"] != "1" {
		t.Errorf("user vuoto/duplicati: %#v, %v", r, err)
	}
}

// ========== TestVerifyTelegramAuth ==========

func TestVerifyValidSignature(t *testing.T) {
	result := VerifyTelegramAuth(generateTestInitData(initDataOpts{}), testBotToken)
	if result == nil {
		t.Fatal("nil")
	}
	if !numEq(userIDOf(t, result), 123456789) {
		t.Errorf("id = %v", userIDOf(t, result))
	}
	if _, ok := result["hash"]; ok {
		t.Error("hash non rimosso")
	}
}

func TestVerifyInvalidSignature(t *testing.T) {
	if result := VerifyTelegramAuth(generateTestInitData(initDataOpts{botToken: testBotToken}), "wrong_bot_token"); result != nil {
		t.Errorf("result = %#v", result)
	}
}

func TestVerifyTamperedData(t *testing.T) {
	tampered := strings.Replace(generateTestInitData(initDataOpts{}), "auth_date=", "auth_date=999", 1)
	if result := VerifyTelegramAuth(tampered, testBotToken); result != nil {
		t.Errorf("result = %#v", result)
	}
}

func TestVerifyMissingHash(t *testing.T) {
	var parts []string
	for _, p := range strings.Split(generateTestInitData(initDataOpts{}), "&") {
		if !strings.HasPrefix(p, "hash=") {
			parts = append(parts, p)
		}
	}
	if result := VerifyTelegramAuth(strings.Join(parts, "&"), testBotToken); result != nil {
		t.Errorf("result = %#v", result)
	}
}

func TestVerifyEmptyInitData(t *testing.T) {
	if result := VerifyTelegramAuth("", testBotToken); result != nil {
		t.Errorf("result = %#v", result)
	}
	if result := VerifyTelegramAuth(generateTestInitData(initDataOpts{}), ""); result != nil {
		t.Errorf("token vuoto: %#v", result)
	}
}

// TestVerifyUnexpectedInputReturnsNone: in Python il caso usa un
// bot_token non stringa (impossibile con i tipi Go); qui un hash con
// caratteri non ASCII, che non può coincidere con quello atteso.
func TestVerifyUnexpectedInputReturnsNone(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	if result := VerifyTelegramAuth("auth_date=1&hash=%C3%A9", testBotToken); result != nil {
		t.Errorf("hash non ASCII: %#v", result)
	}
	if got := logs.Take(); len(got) != 1 || got[0] != (apitest.Record{"api.auth", "DEBUG", "Hash mismatch in initData verification"}) {
		t.Errorf("log = %q", got)
	}
}

func TestVerifyReturnsParsedUser(t *testing.T) {
	result := VerifyTelegramAuth(generateTestInitData(initDataOpts{userID: 42, firstName: "Alice", lastName: "Wonder"}), testBotToken)
	if result == nil {
		t.Fatal("nil")
	}
	user, _ := result.User()
	if !numEq(userIDOf(t, result), 42) || user["first_name"] != "Alice" || user["last_name"] != "Wonder" {
		t.Errorf("user = %#v", user)
	}
}

func TestVerifyEdgeCases(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	// firma valida ma JSON user non valido → None con warning
	bad := signParams([][2]string{{"auth_date", "1"}, {"user", "{"}}, testBotToken)
	if VerifyTelegramAuth(bad, testBotToken) != nil {
		t.Error("JSON non valido accettato")
	}
	// hash vuoto
	if VerifyTelegramAuth("auth_date=1&hash=", testBotToken) != nil {
		t.Error("hash vuoto accettato")
	}
	// campi aggiuntivi firmati e user vuoto (non decodificato)
	extra := signParams([][2]string{{"query_id", "AAE"}, {"auth_date", "1"}, {"user", ""}}, testBotToken)
	r := VerifyTelegramAuth(extra, testBotToken)
	if r == nil || r["query_id"] != "AAE" || r["user"] != "" {
		t.Errorf("campi extra: %#v", r)
	}
	got := logs.Take()
	if len(got) != 2 || got[0] != (apitest.Record{"api.auth", "WARNING", "Valid signature but invalid user JSON"}) ||
		got[1] != (apitest.Record{"api.auth", "DEBUG", "No hash in initData"}) {
		t.Errorf("log = %q", got)
	}
}

// ========== TestValidateInitData ==========

func TestValidateSuccess(t *testing.T) {
	result, err := ValidateInitData(generateTestInitData(initDataOpts{}), testBotToken, DefaultMaxAgeSeconds)
	if err != nil || result == nil {
		t.Fatalf("ValidateInitData: %v", err)
	}
	if _, ok := result["user"]; !ok {
		t.Error("manca user")
	}
}

func TestValidateExpiredAuth(t *testing.T) {
	old := nowUnix() - 7200
	_, err := ValidateInitData(generateTestInitData(initDataOpts{authDate: &old}), testBotToken, 3600)
	var authErr *TelegramAuthError
	if !errors.As(err, &authErr) || !strings.Contains(strings.ToLower(err.Error()), "expired") {
		t.Errorf("errore = %v", err)
	}
}

func TestValidateCustomMaxAge(t *testing.T) {
	recent := nowUnix() - 1800
	initData := generateTestInitData(initDataOpts{authDate: &recent})
	if result, err := ValidateInitData(initData, testBotToken, 3600); err != nil || result == nil {
		t.Errorf("max_age 3600: %v", err)
	}
	if _, err := ValidateInitData(initData, testBotToken, 600); err == nil {
		t.Error("max_age 600: nessun errore")
	}
}

func TestValidateInvalidSignatureRaises(t *testing.T) {
	_, err := ValidateInitData(generateTestInitData(initDataOpts{}), "wrong_token", DefaultMaxAgeSeconds)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "invalid") {
		t.Errorf("errore = %v", err)
	}
}

func TestValidateMissingUserRaises(t *testing.T) {
	initData := signParams([][2]string{{"auth_date", strconv.FormatInt(nowUnix(), 10)}}, testBotToken)
	_, err := ValidateInitData(initData, testBotToken, DefaultMaxAgeSeconds)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "user") {
		t.Errorf("errore = %v", err)
	}
}

func TestValidateExtractsUserID(t *testing.T) {
	result, err := ValidateInitData(generateTestInitData(initDataOpts{userID: 987654321}), testBotToken, DefaultMaxAgeSeconds)
	if err != nil {
		t.Fatal(err)
	}
	if !numEq(userIDOf(t, result), 987654321) || result.UserID() != "987654321" {
		t.Errorf("id = %v / %q", userIDOf(t, result), result.UserID())
	}
}

func TestValidateMessages(t *testing.T) {
	setNow(t, 1760000000)
	sign := func(authDate, user string) string {
		pairs := [][2]string{}
		if authDate != "\x00" {
			pairs = append(pairs, [2]string{"auth_date", authDate})
		}
		if user != "\x00" {
			pairs = append(pairs, [2]string{"user", user})
		}
		return signParams(pairs, testBotToken)
	}
	tests := []struct {
		name, authDate, user, want string
	}{
		{"expired", "1759913599", `{"id":1}`, "Authentication expired (auth_date: 1759913599, age: 86401s, max: 86400s)"},
		{"limite", "1759913600", `{"id":1}`, ""},
		{"futuro", "99999999999", `{"id":1}`, ""},
		{"auth_date assente", "\x00", `{"id":1}`, "Authentication expired (auth_date: 0, age: 1760000000s, max: 86400s)"},
		{"auth_date non numerico", "abc", `{"id":1}`, `Invalid auth_date format: strconv.ParseInt: parsing "abc": invalid syntax`},
		{"auth_date enorme", "-" + strings.Repeat("9", 30), `{"id":1}`,
			`Invalid auth_date format: strconv.ParseInt: parsing "-` + strings.Repeat("9", 30) + `": value out of range`},
		{"user assente", "1760000000", "\x00", "Missing user data in authentication"},
		{"user vuoto", "1760000000", "", "Missing user data in authentication"},
		{"user dict vuoto", "1760000000", "{}", "Missing user data in authentication"},
		{"user lista", "1760000000", "[1]", "Missing user data in authentication"},
		{"user stringa", "1760000000", `"x"`, "Missing user data in authentication"},
		{"user senza id", "1760000000", `{"a":1}`, "Missing user id in authentication"},
		{"id null", "1760000000", `{"id":null}`, ""},
	}
	for _, tt := range tests {
		_, err := ValidateInitData(sign(tt.authDate, tt.user), testBotToken, DefaultMaxAgeSeconds)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tt.want {
			t.Errorf("%s: errore %q; atteso %q", tt.name, got, tt.want)
		}
	}
}

// TestValidateMinAuthDate: auth_date minimo di int64 è scaduto (nessun
// overflow nel confronto con l'età massima).
func TestValidateMinAuthDate(t *testing.T) {
	setNow(t, 1760000000)
	initData := signParams([][2]string{{"auth_date", "-9223372036854775808"}, {"user", `{"id":1}`}}, testBotToken)
	_, err := ValidateInitData(initData, testBotToken, DefaultMaxAgeSeconds)
	if err == nil || !strings.HasPrefix(err.Error(), "Authentication expired (auth_date: -9223372036854775808,") {
		t.Errorf("errore = %v", err)
	}
}

func TestUserIDStr(t *testing.T) {
	tests := map[string]string{
		`{"id":123}`:              "123",
		`{"id":8123456789012345}`: "8123456789012345",
		`{"id":"abc"}`:            "abc",
		`{"id":1.5}`:              "1.5",
		`{"id":true}`:             "true",
	}
	for user, want := range tests {
		var v any
		if err := json.Unmarshal([]byte(user), &v); err != nil {
			t.Fatal(err)
		}
		if got := (InitData{"user": v}).UserID(); got != want {
			t.Errorf("UserID(%s) = %q; atteso %q", user, got, want)
		}
	}
}

// ========== TestTelegramAuthError ==========

func TestTelegramAuthErrorMessage(t *testing.T) {
	if err := (&TelegramAuthError{"Test error message"}); err.Error() != "Test error message" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestTelegramAuthErrorIsError(t *testing.T) {
	var err error = &TelegramAuthError{"Test"}
	var authErr *TelegramAuthError
	if !errors.As(err, &authErr) {
		t.Error("non è un TelegramAuthError")
	}
}
