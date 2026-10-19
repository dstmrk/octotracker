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

	"github.com/dstmrk/octotracker/internal/health"
	"github.com/dstmrk/octotracker/internal/logging"
)

// Nomi dei task in background, usati nei log.
const (
	TaskScraperDaily  = "scraper_daily"
	TaskCheckerDaily  = "checker_daily"
	TaskReminderDaily = "reminder_daily"
	TaskHealthServer  = "health_server"
)

// Chiavi dei task nel registro di /health (health.ScraperTask e
// health.CheckerTask sono quelle controllate da /health).
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
// arrivo (protezione da client lenti).
const ReadHeaderTimeout = 60 * time.Second

// taskDoneCallback logga se un task in background termina per
// cancellazione o con un errore. err è nil per un task terminato
// normalmente.
func taskDoneCallback(name string, err error) {
	if errors.Is(err, context.Canceled) {
		logger.Info(fmt.Sprintf("Task %s cancellato", name))
		return
	}
	if err != nil {
		logging.Critical(logger, fmt.Sprintf("💀 Background task %s terminato con errore: %v", name, err))
	}
}

// backgroundTasks raccoglie i task in background e ne espone lo stato a
// /health tramite il registro.
type backgroundTasks struct {
	reg *health.Registry
	wg  sync.WaitGroup
}

func newBackgroundTasks(reg *health.Registry) *backgroundTasks {
	return &backgroundTasks{reg: reg}
}

// goTask registra il task con la chiave key ed esegue fn in una goroutine.
// Alla fine (anche per panic) il task risulta terminato e
// taskDoneCallback ne logga l'esito.
func (bt *backgroundTasks) goTask(key, name string, fn func() error) {
	t := bt.reg.Register(key)
	bt.wg.Go(func() {
		err := runRecovering(fn)
		t.MarkDone()
		taskDoneCallback(name, err)
	})
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

// healthServer è il server HTTP su HEALTH_PORT (/health, API e Mini App).
type healthServer struct {
	port    int
	handler http.Handler
	listen  func(network, address string) (net.Listener, error)
}

// open apre il socket su 0.0.0.0 (solo IPv4) e logga gli endpoint attivi.
func (h *healthServer) open() (net.Listener, error) {
	listen := h.listen
	if listen == nil {
		listen = net.Listen
	}
	ln, err := listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(h.port)))
	if err != nil {
		return nil, err
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
// così com'è.
func serveUntilDone(ctx context.Context, srv *http.Server, ln net.Listener) error {
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), HTTPShutdownTimeout)
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
	return err
}

// startBackgroundTasks avvia in bt i task giornalieri di scraper, checker
// e reminder e il server HTTP di health check, API e Mini App.
//
// Ogni task esegue subito la parte iniziale (log dell'orario schedulato,
// apertura del socket del server HTTP) nell'ordine di creazione; il resto
// prosegue in background fino alla cancellazione di ctx. Un errore iniziale
// (ora non valida, porta occupata) termina solo quel task, con il log
// CRITICAL di taskDoneCallback.
func startBackgroundTasks(ctx context.Context, bt *backgroundTasks, sched *Scheduler, hs *healthServer) {
	for _, task := range sched.dailyTasks() {
		wait, startErr := sched.start(task)
		bt.goTask(task.key, task.name, func() error {
			if startErr != nil {
				return startErr
			}
			return sched.loop(ctx, task, wait)
		})
	}

	ln, listenErr := hs.open()
	bt.goTask(KeyHealthTask, TaskHealthServer, func() error {
		if listenErr != nil {
			return listenErr
		}
		return hs.serve(ctx, ln)
	})
}
