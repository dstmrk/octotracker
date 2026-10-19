package broadcast

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/testutil"
)

// broadcastGolden contiene gli output di broadcast.py (CPython 3.13, PTB
// 22.6) per gli stessi file, risposte e comportamenti del bot: percorsi
// risolti, contenuti letti, messaggi d'errore dell'app (msg), stdout,
// risultati e chiamate a Telegram.
type broadcastGolden struct {
	Files map[string]string `json:"files"`
	Paths []struct {
		In  string `json:"in"`
		OK  string `json:"ok"`
		Msg string `json:"msg"`
	} `json:"paths"`
	Loads []struct {
		Fn  string          `json:"fn"`
		In  string          `json:"in"`
		OK  json.RawMessage `json:"ok"`
		Msg string          `json:"msg"`
	} `json:"loads"`
	Confirm []struct {
		Answer string `json:"answer"`
		OK     bool   `json:"ok"`
		Stdout string `json:"stdout"`
	} `json:"confirm"`
	Runs []struct {
		Name      string            `json:"name"`
		UsersFile string            `json:"users_file"`
		Behaviour map[string]string `json:"behaviour"`
		Answer    string            `json:"answer"`
		Batch     int               `json:"batch"`
		Result    map[string]int    `json:"result"`
		Stdout    string            `json:"stdout"`
		Calls     [][3]string       `json:"calls"`
	} `json:"runs"`
	Main []struct {
		Name   string   `json:"name"`
		Args   []string `json:"args"`
		Token  string   `json:"token"`
		Batch  int      `json:"batch"`
		Code   int      `json:"code"`
		Stdout string   `json:"stdout"`
	} `json:"main"`
}

// setupTree ricrea i file del generatore Python in una directory di base
// (che diventa la directory corrente) e una directory esterna.
func setupTree(t *testing.T, g *broadcastGolden) (base, outside string) {
	t.Helper()
	var err error
	base, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(base, "sub"))
	mustWrite(t, filepath.Join(outside, "secret.txt"), []byte("segreto"))
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(base, "link_out.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "sub"), filepath.Join(base, "link_in")); err != nil {
		t.Fatal(err)
	}
	for name, h := range g.Files {
		b, _ := hex.DecodeString(h)
		mustWrite(t, filepath.Join(base, name), b)
	}
	mustMkdir(t, filepath.Join(base, "dir.txt"))
	t.Chdir(base)
	return base, outside
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadBroadcastGolden(t *testing.T) *broadcastGolden {
	t.Helper()
	data, err := os.ReadFile("testdata/broadcast_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g broadcastGolden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

// behaviourError produce l'errore gotgbot equivalente all'eccezione PTB del
// generatore; "valueerror" (eccezione non Telegram) diventa un errore
// generico, che conta come invio fallito.
func behaviourError(b string) error {
	switch b {
	case "retry":
		return testutil.RetryAfterError("sendMessage", 30)
	case "timeout":
		return fmt.Errorf("failed to execute POST request to sendMessage: %w", context.DeadlineExceeded)
	case "network":
		return errors.New("Network error")
	case "badrequest":
		return testutil.TelegramError("sendMessage", 400, "Bad Request: chat not found")
	case "forbidden":
		return testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user")
	case "telegram":
		return testutil.TelegramError("sendMessage", 409, "Generic error")
	case "valueerror":
		return errors.New("boom")
	}
	return nil
}

func fakeBot(t *testing.T, behaviour map[string]string) (*gotgbot.Bot, *testutil.FakeClient) {
	b, fc := testutil.NewBot(t)
	fc.SetResponder(func(method string, params map[string]any) (json.RawMessage, error) {
		return nil, behaviourError(behaviour[fmt.Sprint(params["chat_id"])])
	})
	return b, fc
}

// fixedClock fa restituire a now() 100.0 e poi 101.5 secondi (durata 1.50s).
func fixedClock(t *testing.T) {
	t.Helper()
	prev := now
	var mu sync.Mutex
	vals := []time.Time{time.Unix(100, 0), time.Unix(101, 5e8)}
	now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		v := vals[0]
		if len(vals) > 1 {
			vals = vals[1:]
		}
		return v
	}
	t.Cleanup(func() { now = prev })
}

func TestGoldenBroadcast(t *testing.T) {
	g := loadBroadcastGolden(t)
	base, outside := setupTree(t, g)
	expand := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "@BASE@", base), "@OUTSIDE@", outside)
	}
	for _, c := range g.Paths {
		got, err := resolveSafePath(expand(c.In), "")
		if c.Msg == "" {
			if err != nil || got != expand(c.OK) {
				t.Errorf("resolveSafePath(%q) = %q, %v; atteso %q", c.In, got, err, c.OK)
			}
		} else if err == nil || err.Error() != expand(c.Msg) {
			t.Errorf("resolveSafePath(%q) errore %v; atteso %q", c.In, err, c.Msg)
		}
	}

	for _, c := range g.Loads {
		var got any
		var err error
		if c.Fn == "message" {
			got, err = LoadMessage(c.In, "")
		} else {
			got, err = LoadUsersFromFile(c.In, "")
		}
		if c.Msg != "" {
			if err == nil || err.Error() != expand(c.Msg) {
				t.Errorf("%s(%q) errore %v; atteso %q", c.Fn, c.In, err, c.Msg)
			}
			continue
		}
		gotJSON, _ := json.Marshal(got)
		var want, have any
		_ = json.Unmarshal(c.OK, &want)
		_ = json.Unmarshal(gotJSON, &have)
		if err != nil || fmt.Sprint(want) != fmt.Sprint(have) {
			t.Errorf("%s(%q) = %q, %v; atteso %s", c.Fn, c.In, got, err, c.OK)
		}
	}

	for _, c := range g.Confirm {
		var out bytes.Buffer
		ok, err := ConfirmSend(context.Background(), "<b>Test</b> message 🎉", 5, 10, strings.NewReader(c.Answer+"\n"), &out)
		if err != nil || ok != c.OK || out.String() != c.Stdout {
			t.Errorf("ConfirmSend(%q) = %v, %v; atteso %v\nstdout %q\natteso %q", c.Answer, ok, err, c.OK, out.String(), c.Stdout)
		}
	}

	for _, c := range g.Runs {
		fixedClock(t)
		b, fc := fakeBot(t, c.Behaviour)
		var out bytes.Buffer
		res, err := run(context.Background(), b, Options{
			MessageFile: "message.txt", UsersFile: c.UsersFile, BatchSize: c.Batch,
			Stdin: strings.NewReader(c.Answer + "\n"), Stdout: &out,
		})
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		want := Result{Successful: c.Result["successful"], Failed: c.Result["failed"], Total: c.Result["total"]}
		if res != want {
			t.Errorf("%s: risultato %+v; atteso %+v", c.Name, res, want)
		}
		if out.String() != c.Stdout {
			t.Errorf("%s: stdout\n%q\natteso\n%q", c.Name, out.String(), c.Stdout)
		}
		calls := fc.CallsTo("sendMessage")
		if len(calls) != len(c.Calls) {
			t.Errorf("%s: %d chiamate; attese %d", c.Name, len(calls), len(c.Calls))
		}
		for i := range min(len(calls), len(c.Calls)) {
			got := [3]string{calls[i].ChatID(), calls[i].Text(), calls[i].ParseMode()}
			if got != c.Calls[i] {
				t.Errorf("%s: chiamata %d = %q; attesa %q", c.Name, i, got, c.Calls[i])
			}
		}
	}

	for _, c := range g.Main {
		answer := "N\n"
		switch c.Name {
		case "negative_batch":
			answer = "S\n"
		case "eof":
			answer = ""
		}
		var out bytes.Buffer
		b, _ := fakeBot(t, nil)
		code := Main(context.Background(), MainConfig{
			Args: c.Args, BotToken: c.Token, BatchSize: c.Batch,
			NewBot: func(string) (*gotgbot.Bot, error) { return b, nil },
			Stdin:  strings.NewReader(answer), Stdout: &out,
		})
		if code != c.Code {
			t.Errorf("main %s: codice %d; atteso %d", c.Name, code, c.Code)
		}
		if out.String() != c.Stdout {
			t.Errorf("main %s: stdout\n%q\natteso\n%q", c.Name, out.String(), c.Stdout)
		}
	}
}
