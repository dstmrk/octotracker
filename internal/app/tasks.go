package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/health"
	"github.com/dstmrk/octotracker/internal/logging"
)

// Nomi dei task in background (task.get_name() in Python).
const (
	TaskScraperDaily  = "scraper_daily"
	TaskCheckerDaily  = "checker_daily"
	TaskReminderDaily = "reminder_daily"
	TaskHealthServer  = "health_server"
)

// Chiavi dei task in bot_data (health.ScraperTask e health.CheckerTask
// sono quelle controllate da /health).
const (
	KeyScraperTask  = health.ScraperTask
	KeyCheckerTask  = health.CheckerTask
	KeyReminderTask = "reminder_task"
	KeyHealthTask   = "health_task"
)

// HTTPShutdownTimeout è il tempo concesso ai server HTTP per chiudere le
// connessioni in corso allo spegnimento.
const HTTPShutdownTimeout = 5 * time.Second

// ReadHeaderTimeout limita la lettura degli header delle richieste HTTP in
// arrivo (protezione da client lenti; Tornado non ha un limite equivalente
// così stretto, ma nessun client legittimo ci si avvicina).
const ReadHeaderTimeout = 60 * time.Second

// taskDoneCallback è il port di _task_done_callback: logga se un task in
// background termina per cancellazione o con un errore. err è nil per un
// task terminato normalmente.
func taskDoneCallback(name string, err error) {
	if errors.Is(err, context.Canceled) {
		logger.Info(fmt.Sprintf("Task %s cancellato", name))
		return
	}
	if err != nil {
		logging.Critical(logger, fmt.Sprintf("💀 Background task %s terminato con errore: %s", name, describe(err).msg))
	}
}

// backgroundTasks raccoglie i task avviati da post_init (asyncio.create_task
// + add_done_callback) e ne espone lo stato a /health tramite il registro.
type backgroundTasks struct {
	reg *health.Registry
	wg  sync.WaitGroup
}

func newBackgroundTasks(reg *health.Registry) *backgroundTasks {
	if reg == nil {
		reg = health.NewRegistry()
	}
	return &backgroundTasks{reg: reg}
}

// goTask registra il task con la chiave di bot_data key ed esegue fn in una
// goroutine. Alla fine (anche per panic) il task risulta terminato e
// taskDoneCallback ne logga l'esito.
func (bt *backgroundTasks) goTask(key, name string, fn func() error) {
	t := bt.reg.Register(key)
	bt.wg.Add(1)
	go func() {
		defer bt.wg.Done()
		err := runRecovering(fn)
		t.MarkDone()
		taskDoneCallback(name, err)
	}()
}

// wait attende la fine di tutti i task.
func (bt *backgroundTasks) wait() { bt.wg.Wait() }

// runRecovering esegue fn trasformando un panic in errore.
func runRecovering(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError(r)
		}
	}()
	return fn()
}

// healthServer è il server HTTP su HEALTH_PORT (run_health_server).
type healthServer struct {
	port    int
	handler http.Handler
	listen  func(network, address string) (net.Listener, error)
}

// open apre il socket come server.listen(HEALTH_PORT, address="0.0.0.0")
// (solo IPv4) e logga gli endpoint attivi. Un errore è l'OSError di bind.
func (h *healthServer) open() (net.Listener, error) {
	listen := h.listen
	if listen == nil {
		listen = net.Listen
	}
	ln, err := listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(h.port)))
	if err != nil {
		return nil, pycompat.OSError(err)
	}
	logger.Info(fmt.Sprintf("🏥 Health check endpoint attivo su porta %d", h.port))
	logger.Info(fmt.Sprintf("📊 API Mini App attiva su porta %d/api/", h.port))
	logger.Info(fmt.Sprintf("🌐 WebApp Mini App attiva su porta %d/app/", h.port))
	return ln, nil
}

// serve serve le richieste finché ctx non viene cancellato.
func (h *healthServer) serve(ctx context.Context, ln net.Listener) error {
	return serveUntilDone(ctx, &http.Server{Handler: h.handler, ReadHeaderTimeout: ReadHeaderTimeout}, ln)
}

// serveUntilDone esegue srv.Serve(ln) e lo chiude quando ctx viene
// cancellato, restituendo ctx.Err(); un errore di Serve viene restituito
// come OSError.
func serveUntilDone(ctx context.Context, srv *http.Server, ln net.Listener) error {
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), HTTPShutdownTimeout)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		case <-stopped:
		}
	}()
	err := srv.Serve(ln)
	close(stopped)
	if errors.Is(err, http.ErrServerClosed) {
		return ctx.Err()
	}
	return pycompat.OSError(err)
}

// startBackgroundTasks è il port di post_init: avvia i task giornalieri di
// scraper, checker e reminder e il server HTTP di health check, API e Mini
// App, registrandoli come application.bot_data["*_task"].
//
// Come in Python ogni task esegue subito la parte iniziale (log dell'orario
// schedulato, apertura del socket del server HTTP) nell'ordine di
// creazione; il resto prosegue in background fino alla cancellazione di
// ctx. Un errore iniziale (ora non valida, porta occupata) termina solo quel
// task, con il log CRITICAL di _task_done_callback.
func startBackgroundTasks(ctx context.Context, sched *Scheduler, hs *healthServer, reg *health.Registry) *backgroundTasks {
	bt := newBackgroundTasks(reg)

	for _, t := range []struct {
		key, name string
		spec      *dailyTaskSpec
	}{
		{KeyScraperTask, TaskScraperDaily, sched.scraperSpec()},
		{KeyCheckerTask, TaskCheckerDaily, sched.checkerSpec()},
		{KeyReminderTask, TaskReminderDaily, sched.reminderSpec()},
	} {
		spec := t.spec
		startErr := sched.start(spec)
		bt.goTask(t.key, t.name, func() error {
			if startErr != nil {
				return startErr
			}
			return sched.loop(ctx, spec)
		})
	}

	ln, listenErr := hs.open()
	bt.goTask(KeyHealthTask, TaskHealthServer, func() error {
		if listenErr != nil {
			return listenErr
		}
		return hs.serve(ctx, ln)
	})
	return bt
}
