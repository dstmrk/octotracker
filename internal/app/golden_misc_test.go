package app

// Test differenziali con i golden generati eseguendo il codice Python:
//
//   - parsedate_golden.json: email.utils.parsedate_to_datetime (CPython 3.13)
//   - error_handler_golden.json: bot.error_handler con le eccezioni reali di
//     python-telegram-bot 22.6, ricavate dalle stesse risposte HTTP
//     dell'API Telegram che qui riceve gotgbot
//   - scheduler_golden.json: calculate_seconds_until_next_run e i log dei
//     task giornalieri con datetime.now() fissato

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/checker"
)

// TestGoldenParsedate: parsedateToDatetime contro parsedate_to_datetime.
func TestGoldenParsedate(t *testing.T) {
	var cases []struct {
		Input string `json:"input"`
		OK    bool   `json:"ok"`
		Unix  int64  `json:"unix"`
	}
	loadHTTPGolden(t, "parsedate_golden.json", &cases)
	for _, c := range cases {
		got, ok := parsedateToDatetime(c.Input)
		if ok != c.OK {
			t.Errorf("%q: ok = %v, atteso %v (%v)", c.Input, ok, c.OK, got)
			continue
		}
		if ok && got.Unix() != c.Unix {
			t.Errorf("%q: %v (%d), atteso %d", c.Input, got, got.Unix(), c.Unix)
		}
	}
}

// goldenGoError descrive come ottenere in Go l'errore di un caso.
type goldenGoError struct {
	Kind    string `json:"kind"` // http, timeout, text, keyerror, pyerror
	Code    int    `json:"code"`
	Payload string `json:"payload"`
	Text    string `json:"text"`
	Class   string `json:"class"`
}

// goldenHandlerRun è l'esito di error_handler in Python.
type goldenHandlerRun struct {
	Logs []goldenLog `json:"logs"`
	Sent []struct {
		ChatID    string `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	} `json:"sent"`
	Sleeps []int64 `json:"sleeps"`
}

// telegramStub è un'API Telegram finta su HTTP: registra le sendMessage e
// risponde con code/payload (o mai, se hang è true).
type telegramStub struct {
	srv     *httptest.Server
	mu      sync.Mutex
	sent    []map[string]string
	code    int
	payload string
	hang    bool
	stop    chan struct{}
}

func newTelegramStub(t *testing.T, code int, payload string, hang bool) *telegramStub {
	t.Helper()
	s := &telegramStub{code: code, payload: payload, hang: hang, stop: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			fields := map[string]string{}
			for k, v := range r.MultipartForm.Value {
				fields[k] = v[0]
			}
			s.mu.Lock()
			s.sent = append(s.sent, fields)
			s.mu.Unlock()
		}
		if s.hang {
			// Il client rinuncia dopo il suo timeout; stop sblocca comunque
			// l'handler prima di srv.Close, che attende le richieste in corso
			select {
			case <-r.Context().Done():
			case <-s.stop:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.code)
		_, _ = w.Write([]byte(s.payload))
	}))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() { close(s.stop) }) // eseguita prima di srv.Close
	return s
}

// bot restituisce un bot gotgbot reale che parla con lo stub.
func (s *telegramStub) bot() *gotgbot.Bot {
	opts := &gotgbot.RequestOpts{APIURL: s.srv.URL, Timeout: -1}
	if s.hang {
		opts.Timeout = 50 * time.Millisecond
	}
	return &gotgbot.Bot{
		Token:     "123456:TEST-TOKEN",
		BotClient: &gotgbot.BaseBotClient{Client: http.Client{}, DefaultRequestOpts: opts},
	}
}

// goError costruisce l'errore Go del caso: per "http" e "timeout" è
// l'errore reale restituito da gotgbot per quella risposta.
func goError(t *testing.T, g goldenGoError) error {
	t.Helper()
	switch g.Kind {
	case "http":
		_, err := newTelegramStub(t, g.Code, g.Payload, false).bot().SendMessage(1, "x", nil)
		return err
	case "timeout":
		_, err := newTelegramStub(t, 0, "", true).bot().SendMessage(1, "x", nil)
		return err
	case "text":
		return errors.New(g.Text)
	case "keyerror":
		return checker.KeyError(g.Text)
	case "pyerror":
		return pycompat.NewError(g.Class, g.Text)
	}
	t.Fatalf("tipo di errore sconosciuto %q", g.Kind)
	return nil
}

// TestGoldenErrorHandler: log, alert all'admin e attese di ErrorHandler
// contro error_handler di bot.py.
func TestGoldenErrorHandler(t *testing.T) {
	var cases []struct {
		Name        string            `json:"name"`
		Go          goldenGoError     `json:"go"`
		GoSendError *goldenGoError    `json:"go_send_error"`
		PyClass     string            `json:"py_class"`
		PyStr       string            `json:"py_str"`
		Admin       goldenHandlerRun  `json:"admin"`
		NoAdmin     *goldenHandlerRun `json:"no_admin"`
	}
	loadHTTPGolden(t, "error_handler_golden.json", &cases)
	logs := captureRecords(t)

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			err := goError(t, c.Go)
			if err == nil {
				t.Fatal("nessun errore")
			}
			if e := describe(err); e.class != c.PyClass || e.msg != c.PyStr {
				t.Errorf("describe = %s %q, atteso %s %q", e.class, e.msg, c.PyClass, c.PyStr)
			}

			run := func(admin string, want goldenHandlerRun) {
				t.Helper()
				alert := newTelegramStub(t, http.StatusOK, `{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":999,"type":"private"}}}`, false)
				if c.GoSendError != nil {
					switch c.GoSendError.Kind {
					case "timeout":
						alert = newTelegramStub(t, 0, "", true)
					default:
						alert = newTelegramStub(t, c.GoSendError.Code, c.GoSendError.Payload, false)
					}
				}
				var sleeps []time.Duration
				h := &ErrorHandler{AdminUserID: admin, Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
				_ = logs.take()
				h.HandleError(alert.bot(), nil, err)

				if got := logs.take("__main__"); !slices.Equal(got, withName(want.Logs, "__main__")) {
					t.Errorf("log = %q\natteso %q", got, want.Logs)
				}
				var wantSleeps []time.Duration
				for _, s := range want.Sleeps {
					wantSleeps = append(wantSleeps, time.Duration(s)*time.Second)
				}
				if !slices.Equal(sleeps, wantSleeps) {
					t.Errorf("attese = %v, attese %v", sleeps, wantSleeps)
				}
				alert.mu.Lock()
				defer alert.mu.Unlock()
				if len(alert.sent) != len(want.Sent) {
					t.Fatalf("alert inviati = %v, attesi %v", alert.sent, want.Sent)
				}
				for i, s := range want.Sent {
					got := alert.sent[i]
					if got["chat_id"] != s.ChatID || got["text"] != s.Text || got["parse_mode"] != s.ParseMode {
						t.Errorf("alert = %q, atteso %+v", got, s)
					}
				}
			}
			run("999", c.Admin)
			if c.NoAdmin != nil {
				run("", *c.NoAdmin)
			}
		})
	}
}

// withName assegna il nome del logger ai record attesi (in Python il
// modulo importato nei test si chiama "bot", in produzione "__main__").
func withName(logs []goldenLog, name string) []goldenLog {
	out := make([]goldenLog, len(logs))
	for i, l := range logs {
		l.Name = name
		out[i] = l
	}
	return out
}

// TestGoldenScheduler: secondi fino alla prossima esecuzione e testi dei log
// dei task giornalieri, con datetime.now() fissato.
func TestGoldenScheduler(t *testing.T) {
	var cases []struct {
		Now        string  `json:"now"`
		Hour       int     `json:"hour"`
		Seconds    float64 `json:"seconds"`
		ScraperLog string  `json:"scraper_log"`
		NextLog    string  `json:"next_log"`
		Error      string  `json:"error"`
	}
	loadHTTPGolden(t, "scheduler_golden.json", &cases)
	logs := captureRecords(t)

	for _, c := range cases {
		now, err := time.Parse("2006-01-02T15:04:05.999999", c.Now)
		if err != nil {
			t.Fatal(err)
		}
		secs, err := CalculateSecondsUntilNextRun(now, c.Hour)
		if c.Error != "" {
			if ExceptionLine(err) != c.Error {
				t.Errorf("%s ora %d: errore %v, atteso %q", c.Now, c.Hour, err, c.Error)
			}
			continue
		}
		if err != nil || secs != c.Seconds {
			t.Errorf("%s ora %d: %v %v, atteso %v", c.Now, c.Hour, secs, err, c.Seconds)
		}

		// Log iniziale di scraper_daily_task
		s := &Scheduler{ScraperHour: c.Hour, CheckerHour: c.Hour, Now: func() time.Time { return now }}
		_ = logs.take()
		if err := s.start(s.scraperSpec()); err != nil {
			t.Fatal(err)
		}
		if got := logs.take("__main__"); len(got) != 1 || got[0].Msg != c.ScraperLog || got[0].Level != "INFO" {
			t.Errorf("log iniziale = %q, atteso %q", got, c.ScraperLog)
		}

		// Log "⏰ Prossimo checker" del loop, e attesa corrispondente
		var waits []time.Duration
		s.Sleep = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			if len(waits) == 2 {
				return context.Canceled
			}
			return nil
		}
		s.runCheckerFn = func(context.Context) {}
		spec := s.checkerSpec()
		if err := s.loop(context.Background(), spec); !errors.Is(err, context.Canceled) {
			t.Fatalf("loop = %v", err)
		}
		if got := logs.take("__main__"); len(got) != 1 || got[0].Msg != c.NextLog {
			t.Errorf("log del loop = %q, atteso %q", got, c.NextLog)
		}
		if waits[1] != secondsDuration(c.Seconds) {
			t.Errorf("attesa = %v, attesa %v", waits[1], secondsDuration(c.Seconds))
		}
	}
}
