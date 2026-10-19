package broadcast

// Port di tests/test_broadcast.py. I mock di Python (Bot, confirm_send,
// input, asyncio.run) sono sostituiti da testutil.FakeClient, da
// stdin/stdout iniettati e da casi reali che producono gli stessi errori.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/api/apitest"
	"github.com/dstmrk/octotracker/internal/testutil"
)

// sequenceBot restituisce, chiamata dopo chiamata, gli errori della lista
// (side_effect=[...] di AsyncMock); nil = successo.
func sequenceBot(t *testing.T, errs ...error) (*gotgbot.Bot, *testutil.FakeClient) {
	b, fc := testutil.NewBot(t)
	var mu sync.Mutex
	i := 0
	fc.SetResponder(func(string, map[string]any) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(errs) {
			return nil, nil
		}
		err := errs[i]
		i++
		return nil, err
	})
	return b, fc
}

// alwaysFail fa fallire ogni invio con err.
func alwaysFail(t *testing.T, err error) (*gotgbot.Bot, *testutil.FakeClient) {
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", err)
	return b, fc
}

func retryAfter() error { return testutil.RetryAfterError("sendMessage", 30) }

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// tmpCwd crea una directory temporanea e la rende la directory corrente
// (tmp_path + monkeypatch.chdir).
func tmpCwd(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// wantInputError verifica che err sia un errore sui file di input con
// contains nel messaggio.
func wantInputError(t *testing.T, err error, contains string) {
	t.Helper()
	var ie inputError
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), contains) {
		t.Errorf("errore %v; atteso inputError con %q", err, contains)
	}
}

func onlyBroadcast(recs []apitest.Record) []apitest.Record {
	var out []apitest.Record
	for _, r := range recs {
		if r[0] == "broadcast" {
			out = append(out, r)
		}
	}
	return out
}

// ========== send_broadcast_message ==========

func TestSendBroadcastMessageSuccess(t *testing.T) {
	b, fc := testutil.NewBot(t)
	if !SendBroadcastMessage(context.Background(), b, "123456", "Test message") {
		t.Fatal("invio fallito")
	}
	calls := fc.CallsTo("sendMessage")
	if len(calls) != 1 || calls[0].ChatID() != "123456" || calls[0].Text() != "Test message" || calls[0].ParseMode() != "HTML" {
		t.Errorf("chiamate = %+v", calls)
	}
}

func TestSendBroadcastMessageErrors(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	tests := []struct {
		name string
		err  error
		log  apitest.Record
	}{
		{"retry_after", retryAfter(), apitest.Record{"broadcast", "WARNING", "⏱️  Rate limit per utente 123456: riprova dopo 30s"}},
		{"timeout", fmt.Errorf("x: %w", context.DeadlineExceeded), apitest.Record{"broadcast", "ERROR", "❌ Errore di rete per utente 123456: x: context deadline exceeded"}},
		{"network_error", errors.New("Network error"), apitest.Record{"broadcast", "ERROR", "❌ Errore di rete per utente 123456: Network error"}},
		{"telegram_error", testutil.TelegramError("sendMessage", 409, "Generic error"), apitest.Record{"broadcast", "ERROR", "❌ Errore Telegram per utente 123456: Generic error"}},
		{"bad_request", testutil.TelegramError("sendMessage", 400, "Bad Request: chat not found"), apitest.Record{"broadcast", "ERROR", "❌ Errore Telegram per utente 123456: Bad Request: chat not found"}},
		{"forbidden", testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"), apitest.Record{"broadcast", "ERROR", "❌ Errore Telegram per utente 123456: Forbidden: bot was blocked by the user"}},
		{"server_error", testutil.TelegramError("sendMessage", 500, "Internal Server Error"), apitest.Record{"broadcast", "ERROR", "❌ Errore Telegram per utente 123456: Internal Server Error"}},
		{"migrated", &gotgbot.TelegramError{Code: 400, Description: "Bad Request: group chat was upgraded", ResponseParams: &gotgbot.ResponseParameters{MigrateToChatId: -100}},
			apitest.Record{"broadcast", "ERROR", "❌ Errore Telegram per utente 123456: Bad Request: group chat was upgraded"}},
	}
	for _, tt := range tests {
		b, fc := alwaysFail(t, tt.err)
		if SendBroadcastMessage(context.Background(), b, "123456", "Test message") {
			t.Errorf("%s: invio riuscito", tt.name)
		}
		if n := len(fc.CallsTo("sendMessage")); n != 1 {
			t.Errorf("%s: %d chiamate", tt.name, n)
		}
		if got := logs.Take(); len(got) != 1 || got[0] != tt.log {
			t.Errorf("%s: log %q", tt.name, got)
		}
	}
}

func TestSendBroadcastMessageNonNumericChatID(t *testing.T) {
	b, fc := testutil.NewBot(t)
	if !SendBroadcastMessage(context.Background(), b, "@canale", "x") {
		t.Fatal("invio fallito")
	}
	if c := fc.LastCall(t, "sendMessage"); c.Params["chat_id"] != "@canale" || c.ParseMode() != "HTML" {
		t.Errorf("parametri = %v", c.Params)
	}
	// errore con chat_id non numerico: stesso trattamento
	fc.FailMethod("sendMessage", testutil.TelegramError("sendMessage", 400, "Bad Request: chat not found"))
	if SendBroadcastMessage(context.Background(), b, "@canale", "x") {
		t.Error("invio riuscito")
	}
}

func TestSendBroadcastMessageCancelled(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, _ := alwaysFail(t, fmt.Errorf("x: %w", context.Canceled))
	if SendBroadcastMessage(ctx, b, "1", "x") {
		t.Error("invio riuscito")
	}
	if got := logs.Take(); len(got) != 0 {
		t.Errorf("log inattesi %q", got)
	}
}

// ========== send_broadcasts_parallel ==========

func TestSendBroadcastsParallelSuccess(t *testing.T) {
	b, fc := testutil.NewBot(t)
	ok, failed, err := SendBroadcastsParallel(context.Background(), b, []string{"user1", "user2", "user3"}, "Test broadcast", DefaultBatchSize)
	if err != nil || ok != 3 || failed != 0 || len(fc.CallsTo("sendMessage")) != 3 {
		t.Errorf("risultato %d/%d (%v), chiamate %d", ok, failed, err, len(fc.CallsTo("sendMessage")))
	}
}

func TestSendBroadcastsParallelPartialFailure(t *testing.T) {
	b, fc := sequenceBot(t, nil, retryAfter(), nil, nil, retryAfter())
	ok, failed, err := SendBroadcastsParallel(context.Background(), b, []string{"user1", "user2", "user3", "user4", "user5"}, "Test broadcast", DefaultBatchSize)
	if err != nil || ok != 3 || failed != 2 || len(fc.CallsTo("sendMessage")) != 5 {
		t.Errorf("risultato %d/%d (%v)", ok, failed, err)
	}
}

func TestSendBroadcastsParallelAllFailures(t *testing.T) {
	b, fc := alwaysFail(t, testutil.TelegramError("sendMessage", 409, "Error"))
	ok, failed, err := SendBroadcastsParallel(context.Background(), b, []string{"user1", "user2"}, "Test broadcast", DefaultBatchSize)
	if err != nil || ok != 0 || failed != 2 || len(fc.CallsTo("sendMessage")) != 2 {
		t.Errorf("risultato %d/%d (%v)", ok, failed, err)
	}
}

// concurrencyBot misura il numero massimo di invii simultanei.
func concurrencyBot(t *testing.T, delay time.Duration) (*gotgbot.Bot, *atomic.Int64) {
	b, fc := testutil.NewBot(t)
	var current, peak atomic.Int64
	fc.SetResponder(func(string, map[string]any) (json.RawMessage, error) {
		n := current.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(delay)
		current.Add(-1)
		return nil, nil
	})
	return b, &peak
}

func TestSendBroadcastsParallelRateLimiting(t *testing.T) {
	b, peak := concurrencyBot(t, 20*time.Millisecond)
	users := make([]string, 25)
	for i := range users {
		users[i] = fmt.Sprintf("user%d", i)
	}
	ok, failed, err := SendBroadcastsParallel(context.Background(), b, users, "Test broadcast", DefaultBatchSize)
	if err != nil || ok != 25 || failed != 0 || peak.Load() > 10 {
		t.Errorf("risultato %d/%d (%v), massimo simultanei %d", ok, failed, err, peak.Load())
	}
}

func TestSendBroadcastsParallelCustomBatchSize(t *testing.T) {
	b, peak := concurrencyBot(t, 10*time.Millisecond)
	users := make([]string, 15)
	for i := range users {
		users[i] = fmt.Sprintf("user%d", i)
	}
	ok, failed, err := SendBroadcastsParallel(context.Background(), b, users, "Test broadcast", 5)
	if err != nil || ok != 15 || failed != 0 || peak.Load() > 5 {
		t.Errorf("risultato %d/%d (%v), massimo simultanei %d", ok, failed, err, peak.Load())
	}
}

func TestSendBroadcastsParallelBatchSizeEdgeCases(t *testing.T) {
	b, fc := testutil.NewBot(t)
	for _, batch := range []int{-1, 0} {
		if _, _, err := SendBroadcastsParallel(context.Background(), b, []string{"1"}, "x", batch); err == nil ||
			err.Error() != fmt.Sprintf("batch size non valido: %d (minimo 1)", batch) {
			t.Errorf("batch %d: %v", batch, err)
		}
	}
	if n := len(fc.CallsTo("sendMessage")); n != 0 {
		t.Errorf("batch non valido: %d invii", n)
	}
	if ok, failed, err := SendBroadcastsParallel(context.Background(), b, nil, "x", 3); ok != 0 || failed != 0 || err != nil {
		t.Errorf("lista vuota: %d %d %v", ok, failed, err)
	}
}

func TestSendBroadcastsParallelCancellation(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	b, fc := testutil.NewBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 10)
	fc.SetResponder(func(string, map[string]any) (json.RawMessage, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, fmt.Errorf("x: %w", context.Canceled)
	})
	done := make(chan struct{})
	var ok, failed int
	var err error
	go func() {
		ok, failed, err = SendBroadcastsParallel(ctx, b, []string{"1", "2", "3"}, "x", 1)
		close(done)
	}()
	<-started
	cancel()
	<-done
	if !errors.Is(err, context.Canceled) || ok != 0 || failed != 3 || len(fc.CallsTo("sendMessage")) != 1 {
		t.Errorf("risultato %d/%d %v, chiamate %d", ok, failed, err, len(fc.CallsTo("sendMessage")))
	}
	for _, r := range logs.Take() {
		if strings.Contains(r[2], "destinatario") || strings.Contains(r[2], "Errore") {
			t.Errorf("log inatteso %q", r)
		}
	}
}

// ========== load_message ==========

func TestLoadMessageSuccess(t *testing.T) {
	dir := tmpCwd(t)
	p := writeFile(t, dir, "test_message.txt", "Test message content")
	if got, err := LoadMessage(p, ""); err != nil || got != "Test message content" {
		t.Errorf("LoadMessage = %q, %v", got, err)
	}
}

func TestLoadMessageFileNotFound(t *testing.T) {
	tmpCwd(t)
	_, err := LoadMessage("nonexistent/file.txt", "")
	wantInputError(t, err, "File messaggio non trovato: nonexistent/file.txt")
}

func TestLoadMessagePathTraversal(t *testing.T) {
	tmpCwd(t)
	_, err := LoadMessage("/etc/passwd", "")
	wantInputError(t, err, "non consentito")
}

func TestLoadMessageEmptyFile(t *testing.T) {
	dir := tmpCwd(t)
	p := writeFile(t, dir, "empty.txt", "   \n  \t  ")
	_, err := LoadMessage(p, "")
	wantInputError(t, err, "Il file messaggio è vuoto")
}

func TestLoadMessageWithWhitespace(t *testing.T) {
	dir := tmpCwd(t)
	p := writeFile(t, dir, "whitespace.txt", "  \n  Test message  \n  ")
	if got, err := LoadMessage(p, ""); err != nil || got != "Test message" {
		t.Errorf("LoadMessage = %q, %v", got, err)
	}
}

func TestLoadMessageBaseDir(t *testing.T) {
	dir := tmpCwd(t)
	other, _ := filepath.EvalSymlinks(t.TempDir())
	writeFile(t, other, "m.txt", "altrove")
	if got, err := LoadMessage("m.txt", other); err != nil || got != "altrove" {
		t.Errorf("LoadMessage con BaseDir = %q, %v", got, err)
	}
	_, err := LoadMessage(filepath.Join(dir, "x"), other)
	wantInputError(t, err, "fuori dalla directory di lavoro")
	if got, err := resolveSafePath("etc/passwd", "/"); err != nil || got != "/etc/passwd" {
		t.Errorf("BaseDir /: %q %v", got, err)
	}
}

// ========== load_users_from_file ==========

func TestLoadUsersFromFileSuccess(t *testing.T) {
	dir := tmpCwd(t)
	p := writeFile(t, dir, "users.txt", "123456\n789012\n345678")
	if got, err := LoadUsersFromFile(p, ""); err != nil || strings.Join(got, ",") != "123456,789012,345678" {
		t.Errorf("LoadUsersFromFile = %v, %v", got, err)
	}
}

func TestLoadUsersFromFileWithComments(t *testing.T) {
	dir := tmpCwd(t)
	p := writeFile(t, dir, "users.txt", "# Utenti test\n123456\n# Altro commento\n789012")
	if got, err := LoadUsersFromFile(p, ""); err != nil || strings.Join(got, ",") != "123456,789012" {
		t.Errorf("LoadUsersFromFile = %v, %v", got, err)
	}
}

func TestLoadUsersFromFileWithEmptyLines(t *testing.T) {
	dir := tmpCwd(t)
	p := writeFile(t, dir, "users.txt", "123456\n\n  \n789012\n")
	if got, err := LoadUsersFromFile(p, ""); err != nil || strings.Join(got, ",") != "123456,789012" {
		t.Errorf("LoadUsersFromFile = %v, %v", got, err)
	}
}

func TestLoadUsersFromFileNotFound(t *testing.T) {
	tmpCwd(t)
	_, err := LoadUsersFromFile("nonexistent/users.txt", "")
	wantInputError(t, err, "File utenti non trovato")
}

func TestLoadUsersFromFileEmpty(t *testing.T) {
	dir := tmpCwd(t)
	p := writeFile(t, dir, "empty.txt", "   \n# solo commenti\n  ")
	_, err := LoadUsersFromFile(p, "")
	wantInputError(t, err, "non contiene user_id validi")
}

// ========== confirm_send ==========

func confirm(t *testing.T, answer string) bool {
	t.Helper()
	ok, err := ConfirmSend(context.Background(), "Test message", 5, 10, strings.NewReader(answer+"\n"), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestConfirmSendYes(t *testing.T) {
	if !confirm(t, "S") {
		t.Error("S rifiutato")
	}
}

func TestConfirmSendSi(t *testing.T) {
	if !confirm(t, "SI") {
		t.Error("SI rifiutato")
	}
}

func TestConfirmSendYesEnglish(t *testing.T) {
	if !confirm(t, "Y") {
		t.Error("Y rifiutato")
	}
}

func TestConfirmSendNo(t *testing.T) {
	if confirm(t, "N") {
		t.Error("N accettato")
	}
}

func TestConfirmSendInvalid(t *testing.T) {
	if confirm(t, "MAYBE") {
		t.Error("MAYBE accettato")
	}
}

func TestConfirmSendLowercase(t *testing.T) {
	if !confirm(t, "s") {
		t.Error("s rifiutato")
	}
}

func TestConfirmSendInputEdgeCases(t *testing.T) {
	// riga senza newline finale prima di EOF: input() la restituisce
	if ok, err := ConfirmSend(context.Background(), "m", 1, 1, strings.NewReader("yes"), &bytes.Buffer{}); err != nil || !ok {
		t.Errorf("senza newline: %v %v", ok, err)
	}
	if ok, err := ConfirmSend(context.Background(), "m", 1, 1, strings.NewReader("Y\r\n"), &bytes.Buffer{}); err != nil || !ok {
		t.Errorf("CRLF: %v %v", ok, err)
	}
	// EOF senza caratteri
	if _, err := ConfirmSend(context.Background(), "m", 1, 1, strings.NewReader(""), &bytes.Buffer{}); !errors.Is(err, io.EOF) {
		t.Errorf("EOF: %v", err)
	}
	// errore di lettura
	if _, err := ConfirmSend(context.Background(), "m", 1, 1, errReader{}, &bytes.Buffer{}); err == nil || err.Error() != "read error" {
		t.Errorf("errore di lettura: %v", err)
	}
	// Ctrl-C durante l'attesa della risposta
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ConfirmSend(ctx, "m", 1, 1, blockingReader{}, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancellazione: %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error") }

type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

// ========== broadcast_to_users ==========

// run esegue il broadcast con il bot b.
func run(ctx context.Context, b *gotgbot.Bot, opts Options) (Result, error) {
	return broadcastToUsers(ctx, func() (*gotgbot.Bot, error) { return b, nil }, opts)
}

func runOpts(message, users, answer string, batch int) Options {
	return Options{MessageFile: message, UsersFile: users, BatchSize: batch, Stdin: strings.NewReader(answer + "\n"), Stdout: &bytes.Buffer{}}
}

func TestBroadcastToUsersSuccess(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "message.txt", "Test broadcast message")
	u := writeFile(t, dir, "users.txt", "user1\nuser2")
	b, fc := testutil.NewBot(t)
	res, err := run(context.Background(), b, runOpts(m, u, "S", DefaultBatchSize))
	if err != nil || res != (Result{2, 0, 2}) || len(fc.CallsTo("sendMessage")) != 2 {
		t.Errorf("risultato %+v, %v", res, err)
	}
}

func TestBroadcastToUsersNoUsers(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "message.txt", "Test broadcast message")
	u := writeFile(t, dir, "users.txt", "# solo commenti")
	b, _ := testutil.NewBot(t)
	_, err := run(context.Background(), b, runOpts(m, u, "S", DefaultBatchSize))
	wantInputError(t, err, "non contiene user_id validi")
}

func TestBroadcastToUsersCancelled(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	dir := tmpCwd(t)
	m := writeFile(t, dir, "message.txt", "Test broadcast message")
	u := writeFile(t, dir, "users.txt", "user1")
	b, fc := testutil.NewBot(t)
	res, err := run(context.Background(), b, runOpts(m, u, "N", DefaultBatchSize))
	if err != nil || res != (Result{}) || len(fc.Calls()) != 0 {
		t.Errorf("risultato %+v, %v", res, err)
	}
	if got := onlyBroadcast(logs.Take()); len(got) != 1 || got[0][2] != "❌ Invio annullato dall'utente" {
		t.Errorf("log %q", got)
	}
}

func TestBroadcastToUsersPartialFailure(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "message.txt", "Test broadcast message")
	u := writeFile(t, dir, "users.txt", "user1\nuser2\nuser3")
	b, _ := sequenceBot(t, nil, retryAfter(), nil)
	res, err := run(context.Background(), b, runOpts(m, u, "S", DefaultBatchSize))
	if err != nil || res != (Result{2, 1, 3}) {
		t.Errorf("risultato %+v, %v", res, err)
	}
}

func TestBroadcastToUsersFileNotFound(t *testing.T) {
	tmpCwd(t)
	b, _ := testutil.NewBot(t)
	_, err := run(context.Background(), b, runOpts("nonexistent.txt", "users.txt", "S", DefaultBatchSize))
	wantInputError(t, err, "nonexistent.txt")
}

func TestBroadcastToUsersPathTraversal(t *testing.T) {
	tmpCwd(t)
	b, _ := testutil.NewBot(t)
	_, err := run(context.Background(), b, runOpts("/etc/passwd", "users.txt", "S", DefaultBatchSize))
	wantInputError(t, err, "non consentito")
}

func TestBroadcastToUsersUsersFileNotFound(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "message.txt", "Test message")
	b, _ := testutil.NewBot(t)
	_, err := run(context.Background(), b, runOpts(m, "nonexistent.txt", "S", DefaultBatchSize))
	wantInputError(t, err, "File utenti non trovato")
}

func TestBroadcastToUsersEmptyMessage(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "empty.txt", "   ")
	b, _ := testutil.NewBot(t)
	_, err := run(context.Background(), b, runOpts(m, "users.txt", "S", DefaultBatchSize))
	wantInputError(t, err, "Il file messaggio è vuoto")
}

func TestBroadcastToUsersCustomBatchSize(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "message.txt", "Test message")
	u := writeFile(t, dir, "users.txt", "user1\nuser2")
	b, _ := testutil.NewBot(t)
	opts := runOpts(m, u, "S", 5)
	res, err := run(context.Background(), b, opts)
	if err != nil || res.Successful != 2 || res.Total != 2 {
		t.Errorf("risultato %+v, %v", res, err)
	}
	if out := opts.Stdout.(*bytes.Buffer).String(); !strings.Contains(out, "   • Batch size: 5 messaggi simultanei\n") {
		t.Errorf("stdout = %q", out)
	}
}

func TestBroadcastToUsersErrors(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "message.txt", "Test message")
	u := writeFile(t, dir, "users.txt", "user1")
	// EOF alla conferma
	b, _ := testutil.NewBot(t)
	opts := runOpts(m, u, "", DefaultBatchSize)
	opts.Stdin = strings.NewReader("")
	if _, err := run(context.Background(), b, opts); !errors.Is(err, io.EOF) {
		t.Errorf("EOF: %v", err)
	}
	// batch negativo dopo la conferma
	if _, err := run(context.Background(), b, runOpts(m, u, "S", -1)); err == nil {
		t.Error("batch negativo accettato")
	}
	// errore nella creazione del bot (dopo la conferma)
	_, err := broadcastToUsers(context.Background(), func() (*gotgbot.Bot, error) { return nil, errors.New("no bot") }, runOpts(m, u, "S", 1))
	if err == nil || err.Error() != "no bot" {
		t.Errorf("newBot: %v", err)
	}
	// stdin/stdout di default (os.Stdin/os.Stdout) senza conferma possibile
	_, err = broadcastToUsers(context.Background(), nil, Options{MessageFile: "nonexistent.txt"})
	wantInputError(t, err, "File messaggio non trovato")
}

// ========== main ==========

func mainConfig(t *testing.T, args []string, answer string) (MainConfig, *testutil.FakeClient, *bytes.Buffer, *string) {
	t.Helper()
	b, fc := testutil.NewBot(t)
	out := &bytes.Buffer{}
	gotToken := new(string)
	return MainConfig{
		Args:      args,
		BotToken:  "fake_token",
		BatchSize: DefaultBatchSize,
		NewBot: func(token string) (*gotgbot.Bot, error) {
			*gotToken = token
			return b, nil
		},
		Stdin:  strings.NewReader(answer),
		Stdout: out,
	}, fc, out, gotToken
}

func TestMainSuccess(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "message.txt", "Test message")
	u := writeFile(t, dir, "users.txt", "user1")
	cfg, fc, out, token := mainConfig(t, []string{m, u}, "S\n")
	if code := Main(context.Background(), cfg); code != 0 {
		t.Errorf("codice %d", code)
	}
	if len(fc.CallsTo("sendMessage")) != 1 || *token != "fake_token" || !strings.Contains(out.String(), "📊 RIEPILOGO INVIO") {
		t.Errorf("chiamate %d, token %q, stdout %q", len(fc.CallsTo("sendMessage")), *token, out.String())
	}
}

func TestMainNoToken(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	cfg, _, _, _ := mainConfig(t, nil, "")
	cfg.BotToken = ""
	if code := Main(context.Background(), cfg); code != 1 {
		t.Errorf("codice %d", code)
	}
	if got := onlyBroadcast(logs.Take()); len(got) != 1 || got[0] != (apitest.Record{"broadcast", "ERROR", "❌ TELEGRAM_BOT_TOKEN non configurato nel file .env"}) {
		t.Errorf("log %q", got)
	}
}

func TestMainFileNotFound(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	tmpCwd(t)
	cfg, _, _, _ := mainConfig(t, []string{"nonexistent.txt"}, "")
	if code := Main(context.Background(), cfg); code != 1 {
		t.Errorf("codice %d", code)
	}
	if got := onlyBroadcast(logs.Take()); len(got) != 1 || got[0][2] != "❌ File messaggio non trovato: nonexistent.txt" {
		t.Errorf("log %q", got)
	}
}

func TestMainValueError(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	dir := tmpCwd(t)
	writeFile(t, dir, "message.txt", "  ")
	cfg, _, _, _ := mainConfig(t, nil, "")
	if code := Main(context.Background(), cfg); code != 1 {
		t.Errorf("codice %d", code)
	}
	if got := onlyBroadcast(logs.Take()); len(got) != 1 || got[0] != (apitest.Record{"broadcast", "ERROR", "❌ Il file messaggio è vuoto"}) {
		t.Errorf("log %q", got)
	}
}

func TestMainKeyboardInterrupt(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	dir := tmpCwd(t)
	writeFile(t, dir, "message.txt", "Test message")
	writeFile(t, dir, "users.txt", "user1")
	cfg, _, _, _ := mainConfig(t, nil, "")
	cfg.Stdin = blockingReader{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := Main(ctx, cfg); code != 1 {
		t.Errorf("codice %d", code)
	}
	if got := onlyBroadcast(logs.Take()); len(got) != 1 || got[0] != (apitest.Record{"broadcast", "INFO", "\n❌ Invio interrotto dall'utente"}) {
		t.Errorf("log %q", got)
	}
}

func TestMainGenericException(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	dir := tmpCwd(t)
	if err := os.Mkdir(filepath.Join(dir, "message.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _, _, _ := mainConfig(t, nil, "")
	if code := Main(context.Background(), cfg); code != 1 {
		t.Errorf("codice %d", code)
	}
	want := "❌ Errore durante l'invio broadcast: read " + filepath.Join(dir, "message.txt") + ": is a directory"
	if got := onlyBroadcast(logs.Take()); len(got) != 1 || got[0][2] != want {
		t.Errorf("log %q", got)
	}
}

func TestMainWithCustomFiles(t *testing.T) {
	dir := tmpCwd(t)
	m := writeFile(t, dir, "custom_message.txt", "Custom message")
	u := writeFile(t, dir, "custom_users.txt", "user1")
	cfg, _, out, _ := mainConfig(t, []string{m, u}, "N\n")
	if code := Main(context.Background(), cfg); code != 0 {
		t.Errorf("codice %d", code)
	}
	if !strings.Contains(out.String(), "\nCustom message\n") || !strings.Contains(out.String(), "Destinatari: 1 utenti") {
		t.Errorf("stdout %q", out.String())
	}
}

func TestMainDefaultFiles(t *testing.T) {
	dir := tmpCwd(t)
	writeFile(t, dir, DefaultMessageFile, "Messaggio di default")
	writeFile(t, dir, DefaultUsersFile, "1\n2")
	cfg, _, out, _ := mainConfig(t, nil, "N\n")
	if code := Main(context.Background(), cfg); code != 0 {
		t.Errorf("codice %d", code)
	}
	if !strings.Contains(out.String(), "\nMessaggio di default\n") || !strings.Contains(out.String(), "Destinatari: 2 utenti") {
		t.Errorf("stdout %q", out.String())
	}
}

func TestMainWithBatchSizeFromEnv(t *testing.T) {
	dir := tmpCwd(t)
	writeFile(t, dir, DefaultMessageFile, "m")
	writeFile(t, dir, DefaultUsersFile, "1")
	cfg, _, out, _ := mainConfig(t, nil, "N\n")
	cfg.BatchSize = 5
	if code := Main(context.Background(), cfg); code != 0 {
		t.Errorf("codice %d", code)
	}
	if !strings.Contains(out.String(), "Batch size: 5 messaggi simultanei") {
		t.Errorf("stdout %q", out.String())
	}
}

func TestMainDefaultNewBot(t *testing.T) {
	// senza NewBot viene usato checker.NewBot (nessuna chiamata di rete alla creazione)
	dir := tmpCwd(t)
	writeFile(t, dir, DefaultMessageFile, "m")
	writeFile(t, dir, DefaultUsersFile, "1")
	cfg, _, _, _ := mainConfig(t, nil, "N\n")
	cfg.NewBot = nil
	if code := Main(context.Background(), cfg); code != 0 {
		t.Errorf("codice %d", code)
	}
}

func TestResolveSafePathEdgeCases(t *testing.T) {
	dir := tmpCwd(t)
	writeFile(t, dir, "file.txt", "x")
	// directory base inesistente
	if _, err := resolveSafePath("x", filepath.Join(dir, "manca")); err == nil {
		t.Error("base inesistente accettata")
	}
	// componente intermedio che non è una directory: errore di sistema
	if _, err := resolveSafePath("file.txt/x", ""); err == nil || errors.As(err, new(inputError)) {
		t.Errorf("file.txt/x: %v", err)
	}
	// symlink che punta fuori a un file inesistente: controllato sul
	// percorso del link, poi la lettura non trova il file
	if err := os.Symlink(filepath.Join(t.TempDir(), "manca.txt"), filepath.Join(dir, "rotto.txt")); err != nil {
		t.Fatal(err)
	}
	_, err := LoadMessage("rotto.txt", "")
	wantInputError(t, err, "File messaggio non trovato: rotto.txt")
	// percorso inesistente fuori dalla base: non consentito
	_, err = LoadMessage("../manca/x.txt", "")
	wantInputError(t, err, "Percorso non consentito")
}

func TestSendBroadcastsParallelAlreadyCancelled(t *testing.T) {
	b, fc := testutil.NewBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok, failed, err := SendBroadcastsParallel(ctx, b, []string{"1", "2"}, "x", 2)
	if !errors.Is(err, context.Canceled) || ok != 0 || failed != 2 || len(fc.Calls()) != 0 {
		t.Errorf("risultato %d/%d %v, chiamate %d", ok, failed, err, len(fc.Calls()))
	}
}
