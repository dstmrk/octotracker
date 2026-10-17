package tg

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// captureLogs installa un logger di default in memoria per la durata del test.
func captureLogs(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(logging.NewTextHandler(&buf, level)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return &buf
}

func callbackQuery() *gotgbot.CallbackQuery {
	return testutil.CallbackUpdate(testutil.DefaultUserID, "data").CallbackQuery
}

// ========== Test portati da tests/test_handlers_init.py ==========

func TestSafeAnswerCallbackSuccess(t *testing.T) {
	// query.answer() va a buon fine
	b, fc := testutil.NewBot(t)
	if err := SafeAnswerCallback(b, callbackQuery()); err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	calls := fc.CallsTo("answerCallbackQuery")
	if len(calls) != 1 {
		t.Fatalf("answerCallbackQuery chiamato %d volte, want 1", len(calls))
	}
	if got := calls[0].String("callback_query_id"); got != "cbq-1" {
		t.Errorf("callback_query_id = %q", got)
	}
}

func TestSafeAnswerCallbackQueryTooOld(t *testing.T) {
	// query.answer() fallisce con 'Query is too old' - errore ignorato
	b, fc := testutil.NewBot(t)
	logs := captureLogs(t, slog.LevelDebug)
	fc.FailMethod("answerCallbackQuery", testutil.TelegramError("answerCallbackQuery", 400,
		"Bad Request: query is too old and response timeout expired or query id is invalid"))

	if err := SafeAnswerCallback(b, callbackQuery()); err != nil {
		t.Fatalf("l'errore doveva essere ignorato: %v", err)
	}
	if n := len(fc.CallsTo("answerCallbackQuery")); n != 1 {
		t.Fatalf("answerCallbackQuery chiamato %d volte, want 1", n)
	}
	want := " - handlers - DEBUG - Callback query scaduta (ignorata): " +
		"Query is too old and response timeout expired or query id is invalid\n"
	if !strings.HasSuffix(logs.String(), want) {
		t.Errorf("log = %q, want suffisso %q", logs.String(), want)
	}
}

func TestSafeAnswerCallbackQueryIDInvalid(t *testing.T) {
	// query.answer() fallisce con 'query id is invalid' - errore ignorato
	b, fc := testutil.NewBot(t)
	fc.FailMethod("answerCallbackQuery", testutil.TelegramError("answerCallbackQuery", 400, "Query id is invalid"))
	if err := SafeAnswerCallback(b, callbackQuery()); err != nil {
		t.Fatalf("l'errore doveva essere ignorato: %v", err)
	}
	if n := len(fc.CallsTo("answerCallbackQuery")); n != 1 {
		t.Fatalf("answerCallbackQuery chiamato %d volte, want 1", n)
	}
}

func TestSafeAnswerCallbackOtherBadRequestReraises(t *testing.T) {
	// query.answer() fallisce con altro BadRequest - errore rilanciato
	b, fc := testutil.NewBot(t)
	fc.FailMethod("answerCallbackQuery", testutil.TelegramError("answerCallbackQuery", 400, "Some other error"))
	err := SafeAnswerCallback(b, callbackQuery())
	if err == nil || !strings.Contains(err.Error(), "Some other error") {
		t.Fatalf("err = %v, want errore con 'Some other error'", err)
	}
	if !IsBadRequest(err) {
		t.Error("l'errore rilanciato deve essere un BadRequest")
	}
}

// ========== Test aggiuntivi ==========

func TestSafeAnswerCallbackNilQuery(t *testing.T) {
	b, fc := testutil.NewBot(t)
	if err := SafeAnswerCallback(b, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(fc.Calls()); n != 0 {
		t.Fatalf("nessuna chiamata attesa, trovate %d", n)
	}
}

func TestSafeAnswerCallbackNonBadRequestReraises(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		// Solo i BadRequest vengono ignorati, anche se il testo combacia
		{"forbidden", testutil.TelegramError("answerCallbackQuery", 403, "Forbidden: query is too old")},
		{"retry after", &gotgbot.TelegramError{
			Method: "answerCallbackQuery", Code: 400, Description: "query is too old",
			ResponseParams: &gotgbot.ResponseParameters{RetryAfter: 3},
		}},
		{"rete", errors.New("connection reset: query is too old")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, fc := testutil.NewBot(t)
			fc.FailMethod("answerCallbackQuery", tt.err)
			if err := SafeAnswerCallback(b, callbackQuery()); !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestIsBadRequest(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"400", testutil.TelegramError("m", 400, "Bad Request: chat not found"), true},
		{"400 wrapped", fmt.Errorf("wrap: %w", testutil.TelegramError("m", 400, "x")), true},
		{"400 params vuoti", &gotgbot.TelegramError{Code: 400, ResponseParams: &gotgbot.ResponseParameters{}}, true},
		{"403", testutil.TelegramError("m", 403, "Forbidden: bot was blocked by the user"), false},
		{"429", testutil.RetryAfterError("m", 5), false},
		{"migrated", &gotgbot.TelegramError{Code: 400, ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -100}}, false},
		{"altro", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		if got := IsBadRequest(tt.err); got != tt.want {
			t.Errorf("%s: IsBadRequest = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestErrorString(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"non telegram", errors.New("dial tcp: timeout"), "dial tcp: timeout"},
		{"bad request", testutil.TelegramError("m", 400, "Bad Request: chat not found"), "Chat not found"},
		{"bad request maiuscole", testutil.TelegramError("m", 400, "Bad Request: Message Is Not Modified"), "Message is not modified"},
		{"forbidden", testutil.TelegramError("m", 403, "Forbidden: bot was blocked by the user"), "Forbidden: bot was blocked by the user"},
		{"error prefix", testutil.TelegramError("m", 500, "Error: Internal"), "Internal"},
		{"error brackets", testutil.TelegramError("m", 500, "[Error]: Bad Request: X Y"), "X y"},
		{"senza prefisso", testutil.TelegramError("m", 400, "Query id is invalid"), "Query id is invalid"},
		{"vuoto dopo prefisso", testutil.TelegramError("m", 400, "Bad Request: "), ""},
		{"retry after", testutil.RetryAfterError("m", 5), "Flood control exceeded. Retry in 5 seconds"},
		{"migrated", &gotgbot.TelegramError{Code: 400, Description: "Bad Request: group chat was upgraded",
			ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -1001234}},
			"Group migrated to supergroup. New chat id: -1001234"},
		{"senza descrizione", &gotgbot.TelegramError{Code: 502}, "Bad Gateway (502)"},
		{"codice ignoto", &gotgbot.TelegramError{Code: 599}, "Unknown HTTPError (599)"},
	}
	for _, tt := range tests {
		if got := ErrorString(tt.err); got != tt.want {
			t.Errorf("%s: ErrorString = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestIsCommandAndTextFilters(t *testing.T) {
	cmd := testutil.MessageUpdate(1, "/start").Message
	cmdArgs := testutil.MessageUpdate(1, "/status arg").Message
	text := testutil.MessageUpdate(1, "ciao").Message
	notAtStart := &gotgbot.Message{Text: "ciao /start", Entities: []gotgbot.MessageEntity{{Type: "bot_command", Offset: 5, Length: 6}}}
	otherEntity := &gotgbot.Message{Text: "/x", Entities: []gotgbot.MessageEntity{{Type: "bold", Offset: 0, Length: 2}}}
	secondEntity := &gotgbot.Message{Text: "*a* /start", Entities: []gotgbot.MessageEntity{
		{Type: "bold", Offset: 0, Length: 3}, {Type: "bot_command", Offset: 0, Length: 6},
	}}
	empty := &gotgbot.Message{}

	tests := []struct {
		name            string
		msg             *gotgbot.Message
		isCmd, textOnly bool
	}{
		{"comando", cmd, true, false},
		{"comando con argomenti", cmdArgs, true, false},
		{"testo", text, false, true},
		{"comando non in testa", notAtStart, false, true},
		{"entità non comando", otherEntity, false, true},
		{"solo prima entità conta", secondEntity, false, true},
		{"messaggio senza testo", empty, false, false},
		{"nil", nil, false, false},
	}
	for _, tt := range tests {
		if got := IsCommand(tt.msg); got != tt.isCmd {
			t.Errorf("%s: IsCommand = %v, want %v", tt.name, got, tt.isCmd)
		}
		if got := AnyCommand(tt.msg); got != tt.isCmd {
			t.Errorf("%s: AnyCommand = %v, want %v", tt.name, got, tt.isCmd)
		}
		if got := TextNotCommand(tt.msg); got != tt.textOnly {
			t.Errorf("%s: TextNotCommand = %v, want %v", tt.name, got, tt.textOnly)
		}
	}
}

func TestCallbackPattern(t *testing.T) {
	tests := []struct {
		pattern, data string
		want          bool
	}{
		{"^tipo_", "tipo_fissa", true},
		{"tipo_", "tipo_fissa", true},  // re.match: ancorato all'inizio
		{"fissa", "tipo_fissa", false}, // non cerca nel mezzo
		{"^rate_update_yes$", "rate_update_yes", true},
		{"^rate_update_yes$", "rate_update_yes_2", false},
		{"rate_update_yes", "rate_update_yes_2", true}, // non ancorato alla fine
		{"a|b", "b", true},                             // alternativa raggruppata
		{"a|b", "cb", false},
		{"^(luce|gas)$", "gas", true},
	}
	for _, tt := range tests {
		f := CallbackPattern(tt.pattern)
		if got := f(&gotgbot.CallbackQuery{Data: tt.data}); got != tt.want {
			t.Errorf("CallbackPattern(%q)(%q) = %v, want %v", tt.pattern, tt.data, got, tt.want)
		}
	}

	// Callback senza data: come PTB v22, corrisponde solo se non è un gioco
	f := CallbackPattern("^x$")
	if !f(&gotgbot.CallbackQuery{}) {
		t.Error("callback senza data né gioco deve corrispondere")
	}
	if f(&gotgbot.CallbackQuery{GameShortName: "game"}) {
		t.Error("callback di un gioco non deve corrispondere a un pattern sui dati")
	}
}

func TestCallbackPatternInvalidRegexPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("pattern non valido deve andare in panic")
		}
	}()
	CallbackPattern("(")
}

func TestUserData(t *testing.T) {
	u := NewUserData()
	const user, other int64 = 1, 2

	if _, ok := u.Get(user, "k"); ok {
		t.Fatal("chiave inattesa")
	}
	u.Set(user, "energia", 0.123)
	u.Set(user, "tipo", "fissa")
	u.Set(other, "tipo", "variabile")

	if v, ok := u.Float(user, "energia"); !ok || v != 0.123 {
		t.Errorf("Float = %v, %v", v, ok)
	}
	if _, ok := u.Float(user, "tipo"); ok {
		t.Error("Float su stringa deve fallire")
	}
	if _, ok := u.Float(user, "manca"); ok {
		t.Error("Float su chiave assente deve fallire")
	}
	if v, ok := u.String(user, "tipo"); !ok || v != "fissa" {
		t.Errorf("String = %v, %v", v, ok)
	}
	if _, ok := u.String(user, "energia"); ok {
		t.Error("String su float deve fallire")
	}
	if _, ok := u.String(user, "manca"); ok {
		t.Error("String su chiave assente deve fallire")
	}

	snap := u.Snapshot(user)
	if len(snap) != 2 || snap["tipo"] != "fissa" {
		t.Errorf("Snapshot = %v", snap)
	}
	snap["tipo"] = "modificato"
	if v, _ := u.String(user, "tipo"); v != "fissa" {
		t.Error("Snapshot deve essere una copia")
	}

	u.Delete(user, "tipo")
	u.Delete(user, "manca")      // pop(key, None) su chiave assente
	u.Delete(12345, "qualsiasi") // utente sconosciuto
	if _, ok := u.Get(user, "tipo"); ok {
		t.Error("Delete non ha rimosso la chiave")
	}

	u.Clear(user)
	if len(u.Snapshot(user)) != 0 {
		t.Error("Clear non ha svuotato i dati")
	}
	if v, ok := u.String(other, "tipo"); !ok || v != "variabile" {
		t.Error("Clear non deve toccare gli altri utenti")
	}
}

func TestUserDataConcurrent(t *testing.T) {
	u := NewUserData()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				u.Set(id%4, "k", j)
				u.Get(id%4, "k")
				u.Snapshot(id % 4)
				if j%10 == 0 {
					u.Clear(id % 4)
				}
			}
		}(int64(i))
	}
	wg.Wait()
}

func TestPyCapitalize(t *testing.T) {
	for in, want := range map[string]string{"": "", "query": "Query", "QUERY ID": "Query id", "ǆa": "ǅa"} {
		if got := pyCapitalize(in); got != want {
			t.Errorf("pyCapitalize(%q) = %q, want %q", in, got, want)
		}
	}
}
