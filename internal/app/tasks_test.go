package app

// Test dei task in background (avvio, esito e server HTTP).

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/health"
)

// localListen apre un socket locale su una porta libera, qualunque sia
// l'indirizzo richiesto, e ricorda l'indirizzo richiesto → effettivo.
type localListen struct {
	mu    sync.Mutex
	addrs map[string]string
}

func (l *localListen) listen(network, address string) (net.Listener, error) {
	ln, err := net.Listen(network, "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.addrs == nil {
		l.addrs = map[string]string{}
	}
	l.addrs[address] = ln.Addr().String()
	return ln, nil
}

func (l *localListen) addr(requested string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.addrs[requested]
}

// waitFor attende che cond diventi vera (al massimo 5 secondi).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout in attesa di: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestStartBackgroundTasks: vengono creati i 4 task (scraper, checker,
// reminder, health_server), registrati per /health e con il log dell'esito
// alla fine.
func TestStartBackgroundTasks(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := newTestScheduler()
	sched.Sleep = sleepCtx // le attese restano in corso fino alla cancellazione
	ll := &localListen{}
	hs := &healthServer{port: 8444, handler: http.NotFoundHandler(), listen: ll.listen}
	reg := health.NewRegistry()

	bt := newBackgroundTasks(reg)
	startBackgroundTasks(ctx, bt, sched, hs)

	// Verifica che tutti i task siano stati creati e registrati
	for _, key := range []string{KeyScraperTask, KeyCheckerTask, KeyReminderTask, KeyHealthTask} {
		exists, done := reg.TaskDone(key)
		if !exists || done {
			t.Errorf("task %s: exists=%v done=%v", key, exists, done)
		}
	}
	assertLogContains(t, logs, "🕷️  Scraper schedulato per le 9:00 (tra 1.0 ore)")
	assertLogContains(t, logs, "🔍 Checker schedulato per le 10:00 (tra 2.0 ore)")
	assertLogContains(t, logs, "📅 Reminder schedulato per le 11:00 (tra 3.0 ore)")
	assertLogContains(t, logs, "🏥 Health check endpoint attivo su porta 8444")
	assertLogContains(t, logs, "📊 API Mini App attiva su porta 8444/api/")
	assertLogContains(t, logs, "🌐 WebApp Mini App attiva su porta 8444/app/")

	// Il server HTTP risponde
	resp, err := http.Get("http://" + ll.addr("0.0.0.0:8444") + "/x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", resp.StatusCode)
	}

	// Alla cancellazione ogni task logga il proprio esito
	cancel()
	bt.wait()
	for _, name := range []string{TaskScraperDaily, TaskCheckerDaily, TaskReminderDaily, TaskHealthServer} {
		assertLogContains(t, logs, "INFO - Task "+name+" cancellato")
	}
	for _, key := range []string{KeyScraperTask, KeyCheckerTask, KeyReminderTask, KeyHealthTask} {
		if _, done := reg.TaskDone(key); !done {
			t.Errorf("task %s non terminato", key)
		}
	}
	assertLogNotContains(t, logs, "CRITICAL")
}

// TestTaskDoneCallbackLogsException: gli errori dei task vengono loggati.
func TestTaskDoneCallbackLogsException(t *testing.T) {
	logs := captureLogs(t)
	taskDoneCallback("test_task", errors.New("task crashed"))
	assertLogContains(t, logs, "CRITICAL - 💀 Background task test_task terminato con errore: task crashed")
}

// TestTaskDoneCallbackHandlesCancellation: un task cancellato non è un errore.
func TestTaskDoneCallbackHandlesCancellation(t *testing.T) {
	logs := captureLogs(t)
	taskDoneCallback("cancelled_task", context.Canceled)
	assertLogContains(t, logs, "INFO - Task cancelled_task cancellato")
	assertLogNotContains(t, logs, "CRITICAL")
}

// TestTaskDoneCallbackNoException: nessun log se il task termina
// normalmente.
func TestTaskDoneCallbackNoException(t *testing.T) {
	logs := captureLogs(t)
	taskDoneCallback("normal_task", nil)
	if logs.String() != "" {
		t.Errorf("log inattesi: %s", logs.String())
	}
}

// TestHealthServerPortInUse: se la porta è occupata il task health_server
// termina con l'errore di bind (log CRITICAL); gli altri task continuano.
func TestHealthServerPortInUse(t *testing.T) {
	logs := captureLogs(t)
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched := newTestScheduler()
	sched.Sleep = sleepCtx
	hs := &healthServer{port: port, handler: http.NotFoundHandler(),
		listen: func(network, _ string) (net.Listener, error) {
			return net.Listen(network, busy.Addr().String())
		}}
	reg := health.NewRegistry()
	bt := newBackgroundTasks(reg)
	startBackgroundTasks(ctx, bt, sched, hs)

	waitFor(t, "health_task terminato", func() bool { _, done := reg.TaskDone(KeyHealthTask); return done })
	waitFor(t, "log critical", func() bool {
		return strings.Contains(logs.String(), "💀 Background task health_server terminato con errore: ") &&
			strings.Contains(logs.String(), "address already in use")
	})
	if _, done := reg.TaskDone(KeyScraperTask); done {
		t.Error("scraper_task terminato")
	}
	assertLogNotContains(t, logs, "🏥 Health check endpoint attivo")
	cancel()
	bt.wait()
}

// TestBackgroundTaskInvalidHour: un'ora non valida termina subito il solo
// task interessato (che per /health non è più attivo).
func TestBackgroundTaskInvalidHour(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched := newTestScheduler()
	sched.Sleep = sleepCtx
	sched.CheckerHour = 24
	ll := &localListen{}
	hs := &healthServer{port: 1, handler: http.NotFoundHandler(), listen: ll.listen}
	reg := health.NewRegistry()
	bt := newBackgroundTasks(reg)
	startBackgroundTasks(ctx, bt, sched, hs)

	waitFor(t, "checker_task terminato", func() bool { _, done := reg.TaskDone(KeyCheckerTask); return done })
	waitFor(t, "log critical", func() bool {
		return strings.Contains(logs.String(), "CRITICAL - 💀 Background task checker_daily terminato con errore: ora non valida: 24")
	})
	w := httptest.NewRecorder()
	health.NewHandler(newTestStore(t), reg).ServeHTTP(w, httptest.NewRequest(http.MethodGet, health.Path, nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "unhealthy") {
		t.Errorf("health = %d %s", w.Code, w.Body.String())
	}
	cancel()
	bt.wait()
}

// TestGoTaskRecoversPanic: un panic in un task viene recuperato e loggato
// dal done callback.
func TestGoTaskRecoversPanic(t *testing.T) {
	logs := captureLogs(t)
	bt := newBackgroundTasks(health.NewRegistry())
	bt.goTask("x_task", "x", func() error { panic("boom") })
	bt.wait()
	assertLogContains(t, logs, "CRITICAL - 💀 Background task x terminato con errore: boom")
	if _, done := bt.reg.TaskDone("x_task"); !done {
		t.Error("task non terminato")
	}
}

// TestServeUntilDone: Serve termina con ctx.Err() alla cancellazione e con
// l'errore se il listener fallisce.
func TestServeUntilDone(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	done := make(chan error, 1)
	go func() { done <- serveUntilDone(ctx, srv, ln) }()
	resp, err := http.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("serveUntilDone = %v", err)
	}

	closed, _ := net.Listen("tcp4", "127.0.0.1:0")
	_ = closed.Close()
	if err := serveUntilDone(context.Background(), &http.Server{}, closed); err == nil {
		t.Error("atteso errore con listener chiuso")
	}
}
