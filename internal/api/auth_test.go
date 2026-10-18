package api

// Port di tests/test_api_auth.py (20 casi) più casi limite aggiuntivi.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/dstmrk/octotracker/internal/api/apitest"
	"github.com/dstmrk/octotracker/internal/api/pycompat"
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
	sortStrings(keys)
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

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
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
	user := pycompat.MustDumps(pycompat.Object{
		{Key: "id", Value: o.userID}, {Key: "first_name", Value: o.firstName}, {Key: "last_name", Value: o.lastName},
		{Key: "username", Value: o.username}, {Key: "language_code", Value: "it"},
	})
	user = strings.ReplaceAll(strings.ReplaceAll(user, ", ", ","), ": ", ":") // separators=(",", ":")
	return signParams([][2]string{{"auth_date", strconv.FormatInt(authDate, 10)}, {"user", user}}, o.botToken)
}

func userIDOf(t *testing.T, d InitData) any {
	t.Helper()
	u, ok := d.User()
	if !ok {
		t.Fatalf("user non è un dict: %#v", d["user"])
	}
	id, _ := u.Get("id")
	return id
}

func bigEq(v any, n int64) bool {
	b, ok := v.(*big.Int)
	return ok && b.Cmp(big.NewInt(n)) == 0
}

// ========== TestParseInitData ==========

func TestParseValidInitData(t *testing.T) {
	result := ParseInitData(generateTestInitData(initDataOpts{}))
	for _, k := range []string{"auth_date", "user", "hash"} {
		if _, ok := result[k]; !ok {
			t.Errorf("manca %q", k)
		}
	}
}

func TestParseExtractsUserJSON(t *testing.T) {
	result := ParseInitData(generateTestInitData(initDataOpts{userID: 999, firstName: "Test", username: "testuser"}))
	user, ok := result.User()
	if !ok {
		t.Fatalf("user non è un dict: %#v", result["user"])
	}
	id, _ := user.Get("id")
	fn, _ := user.Get("first_name")
	un, _ := user.Get("username")
	if !bigEq(id, 999) || fn != "Test" || un != "testuser" {
		t.Errorf("user = %#v", user)
	}
}

func TestParseEmptyString(t *testing.T) {
	if result := ParseInitData(""); result == nil || len(result) != 0 {
		t.Errorf("ParseInitData(\"\") = %#v", result)
	}
}

func TestParseInvalidFormat(t *testing.T) {
	result := ParseInitData("not_valid_query_string")
	if result == nil {
		t.Fatal("nil")
	}
	if v, ok := result["not_valid_query_string"]; !ok || v != "" {
		t.Errorf("result = %#v", result)
	}
}

func TestParsePreservesHash(t *testing.T) {
	result := ParseInitData(generateTestInitData(initDataOpts{}))
	if h, _ := result["hash"].(string); len(h) != 64 {
		t.Errorf("hash = %q", h)
	}
}

func TestParseInitDataEdgeCases(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	// JSON non valido: resta la stringa, con warning
	r := ParseInitData("user=%7Bx&auth_date=1")
	if r["user"] != "{x" || r["auth_date"] != "1" {
		t.Errorf("JSON non valido: %#v", r)
	}
	// RecursionError: l'except esterno restituisce {}
	deep := "user=" + url.QueryEscape(strings.Repeat("[", 10000)+strings.Repeat("]", 10000))
	if r := ParseInitData(deep); len(r) != 0 {
		t.Errorf("JSON troppo annidato: %d chiavi", len(r))
	}
	// user vuoto: nessun parsing; chiavi duplicate: vince il primo valore
	if r := ParseInitData("user=&a=1&a=2"); r["user"] != "" || r["a"] != "1" {
		t.Errorf("user vuoto/duplicati: %#v", r)
	}
	got := logs.Take()
	if len(got) != 2 || got[0][2] != "Failed to parse user JSON in initData" ||
		got[1][2] != "Failed to parse initData: maximum recursion depth exceeded while decoding a JSON array from a unicode string" {
		t.Errorf("log = %q", got)
	}
}

// ========== TestVerifyTelegramAuth ==========

func TestVerifyValidSignature(t *testing.T) {
	result := VerifyTelegramAuth(generateTestInitData(initDataOpts{}), testBotToken)
	if result == nil {
		t.Fatal("nil")
	}
	if !bigEq(userIDOf(t, result), 123456789) {
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

// TestVerifyUnexpectedExceptionReturnsNone: in Python il caso usa un
// bot_token non stringa (impossibile con i tipi Go); qui si esercita il
// ramo "except Exception" raggiungibile: hash con caratteri non ASCII
// (TypeError di hmac.compare_digest) e un JSON user troppo annidato
// (RecursionError) con firma valida.
func TestVerifyUnexpectedExceptionReturnsNone(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	if result := VerifyTelegramAuth("auth_date=1&hash=%C3%A9", testBotToken); result != nil {
		t.Errorf("hash non ASCII: %#v", result)
	}
	deep := signParams([][2]string{{"auth_date", "1"}, {"user", strings.Repeat("[", 10000) + strings.Repeat("]", 10000)}}, testBotToken)
	if result := VerifyTelegramAuth(deep, testBotToken); result != nil {
		t.Errorf("JSON annidato: %#v", result)
	}
	got := logs.Take()
	want := []apitest.Record{
		{"api.auth", "ERROR", "Error verifying Telegram auth: comparing strings with non-ASCII characters is not supported"},
		{"api.auth", "ERROR", "Error verifying Telegram auth: maximum recursion depth exceeded while decoding a JSON array from a unicode string"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("log = %q", got)
	}
}

func TestVerifyReturnsParsedUser(t *testing.T) {
	result := VerifyTelegramAuth(generateTestInitData(initDataOpts{userID: 42, firstName: "Alice", lastName: "Wonder"}), testBotToken)
	if result == nil {
		t.Fatal("nil")
	}
	user, _ := result.User()
	fn, _ := user.Get("first_name")
	ln, _ := user.Get("last_name")
	if !bigEq(userIDOf(t, result), 42) || fn != "Alice" || ln != "Wonder" {
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
	if !bigEq(userIDOf(t, result), 987654321) || result.UserID() != "987654321" {
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
		{"futuro", "99999999999999999999999", `{"id":1}`, ""},
		{"auth_date assente", "\x00", `{"id":1}`, "Authentication expired (auth_date: 0, age: 1760000000s, max: 86400s)"},
		{"auth_date non numerico", "abc", `{"id":1}`, "Invalid auth_date format: invalid literal for int() with base 10: 'abc'"},
		{"auth_date unicode", "١٧٦٠٠٠٠٠٠٠", `{"id":1}`, ""},
		{"auth_date enorme", "-" + strings.Repeat("9", 4300), `{"id":1}`,
			"Invalid auth_date format: Exceeds the limit (4300 digits) for integer string conversion; use sys.set_int_max_str_digits() to increase the limit"},
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

func TestUserIDStr(t *testing.T) {
	tests := map[string]string{
		`{"id":123}`:           "123",
		`{"id":"abc"}`:         "abc",
		`{"id":1.0}`:           "1.0",
		`{"id":1e16}`:          "1e+16",
		`{"id":true}`:          "True",
		`{"id":null}`:          "None",
		`{"id":[1,"a"]}`:       "[1, 'a']",
		`{"id":{"b":2,"a":1}}`: "{'b': 2, 'a': 1}",
	}
	for user, want := range tests {
		v, err := pycompat.Loads(user)
		if err != nil {
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
