package app

// Test con i golden generati eseguendo la versione Python:
//
//   - error_handler_golden.json: per ogni errore (ricavato dalle stesse
//     risposte HTTP dell'API Telegram che qui riceve gotgbot) se l'error
//     handler invia l'alert all'admin, con quale modello di testo, e quanto
//     attende;
//   - scheduler_golden.json: secondi fino alla prossima esecuzione
//     con l'orologio fissato; i casi senza "seconds" sono ore non valide.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// goldenGoError descrive come ottenere in Go l'errore di un caso.
type goldenGoError struct {
	Kind    string `json:"kind"` // http, timeout, altrimenti testo
	Code    int    `json:"code"`
	Payload string `json:"payload"`
	Text    string `json:"text"`
}

// goldenHandlerRun è l'esito di error_handler in Python.
type goldenHandlerRun struct {
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
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() { close(s.stop) }) // eseguita prima di srv.Close
	return s
}

func (s *telegramStub) serve(w http.ResponseWriter, r *http.Request) {
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
}

func (s *telegramStub) sentMessages() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// bot restituisce un bot gotgbot reale che parla con lo stub.
func (s *telegramStub) bot() *gotgbot.Bot {
	opts := &gotgbot.RequestOpts{APIURL: s.srv.URL, Timeout: -1}
	if s.hang {
		opts.Timeout = 50 * time.Millisecond
	}
	return &gotgbot.Bot{
		Token:     testToken,
		BotClient: &gotgbot.BaseBotClient{Client: http.Client{}, DefaultRequestOpts: opts},
	}
}

// stubFor crea lo stub che risponde come indicato da g.
func stubFor(t *testing.T, g goldenGoError) *telegramStub {
	if g.Kind == "timeout" {
		return newTelegramStub(t, 0, "", true)
	}
	return newTelegramStub(t, g.Code, g.Payload, false)
}

// goError costruisce l'errore Go del caso: per "http" e "timeout" è
// l'errore reale restituito da gotgbot per quella risposta.
func goError(t *testing.T, g goldenGoError) error {
	t.Helper()
	if g.Kind != "http" && g.Kind != "timeout" {
		return errors.New(g.Text)
	}
	_, err := stubFor(t, g).bot().SendMessage(1, "x", nil)
	return err
}

// TestGoldenErrorHandler: alert all'admin e attese di ErrorHandler contro
// error_handler della versione Python.
func TestGoldenErrorHandler(t *testing.T) {
	var cases []struct {
		Name        string            `json:"name"`
		Go          goldenGoError     `json:"go"`
		GoSendError *goldenGoError    `json:"go_send_error"`
		Admin       goldenHandlerRun  `json:"admin"`
		NoAdmin     *goldenHandlerRun `json:"no_admin"`
	}
	loadGolden(t, "error_handler_golden.json", &cases)
	captureLogs(t)

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			err := goError(t, c.Go)
			if err == nil {
				t.Fatal("nessun errore")
			}
			checkErrorHandlerRun(t, err, "999", c.GoSendError, c.Admin)
			if c.NoAdmin != nil {
				checkErrorHandlerRun(t, err, "", nil, *c.NoAdmin)
			}
		})
	}
}

// checkErrorHandlerRun gestisce err con ADMIN_USER_ID admin e confronta
// alert e attese con want; sendError è la risposta all'invio dell'alert
// (nil = successo).
func checkErrorHandlerRun(t *testing.T, err error, admin string, sendError *goldenGoError, want goldenHandlerRun) {
	t.Helper()
	ok := goldenGoError{Kind: "http", Code: http.StatusOK,
		Payload: `{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":999,"type":"private"}}}`}
	if sendError != nil {
		ok = *sendError
	}
	alert := stubFor(t, ok)
	var sleeps []time.Duration
	h := &ErrorHandler{AdminUserID: admin, Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
	h.HandleError(alert.bot(), nil, err)

	var wantSleeps []time.Duration
	for _, s := range want.Sleeps {
		wantSleeps = append(wantSleeps, time.Duration(s)*time.Second)
	}
	if !slices.Equal(sleeps, wantSleeps) {
		t.Errorf("admin %q: attese = %v, attese %v", admin, sleeps, wantSleeps)
	}
	sent := alert.sentMessages()
	if len(sent) != len(want.Sent) {
		t.Fatalf("admin %q: alert inviati = %v, attesi %v", admin, sent, want.Sent)
	}
	for i, s := range want.Sent {
		// Il tipo e il testo dell'errore non fanno parte del confronto
		// (in Python sono la classe e il messaggio dell'eccezione).
		if sent[i]["chat_id"] != s.ChatID || sent[i]["parse_mode"] != s.ParseMode ||
			maskAlertError(sent[i]["text"]) != maskAlertError(s.Text) {
			t.Errorf("alert = %q, atteso %+v", sent[i], s)
		}
	}
}

// alertErrorValues sono i valori delle righe Tipo e Messaggio dell'alert.
var alertErrorValues = regexp.MustCompile(`(?s)(<b>Tipo:</b>) .*\n(<b>Messaggio:</b>) .*(\n<b>Update:</b> )`)

// maskAlertError nasconde tipo e testo dell'errore nell'alert all'admin.
func maskAlertError(text string) string {
	return alertErrorValues.ReplaceAllString(text, "$1 …\n$2 …$3")
}

// TestGoldenScheduler: secondi fino alla prossima esecuzione e attesa del
// loop giornaliero, con l'orologio fissato.
func TestGoldenScheduler(t *testing.T) {
	var cases []struct {
		Now     string   `json:"now"`
		Hour    int      `json:"hour"`
		Seconds *float64 `json:"seconds"`
	}
	loadGolden(t, "scheduler_golden.json", &cases)
	captureLogs(t)

	for _, c := range cases {
		now, err := time.Parse("2006-01-02T15:04:05.999999", c.Now)
		if err != nil {
			t.Fatal(err)
		}
		secs, err := CalculateSecondsUntilNextRun(now, c.Hour)
		if c.Seconds == nil {
			if err == nil {
				t.Errorf("%s ora %d: atteso errore, ottenuto %v", c.Now, c.Hour, secs)
			}
			continue
		}
		if err != nil || secs != *c.Seconds {
			t.Errorf("%s ora %d: %v %v, atteso %v", c.Now, c.Hour, secs, err, *c.Seconds)
		}
		checkLoopWait(t, now, c.Hour, *c.Seconds)
	}
}

// checkLoopWait verifica che il loop giornaliero, dopo un'esecuzione,
// attenda esattamente seconds.
func checkLoopWait(t *testing.T, now time.Time, hour int, seconds float64) {
	t.Helper()
	var waits []time.Duration
	s := &Scheduler{CheckerHour: hour, Now: func() time.Time { return now }}
	s.Sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		if len(waits) == 2 {
			return context.Canceled
		}
		return nil
	}
	s.CheckAndNotify = func(context.Context) error { return nil }
	if err := s.loop(context.Background(), s.dailyTasks()[checkerIdx], 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("loop = %v", err)
	}
	if waits[1] != secondsDuration(seconds) {
		t.Errorf("%v ora %d: attesa = %v, attesa %v", now, hour, waits[1], secondsDuration(seconds))
	}
}
