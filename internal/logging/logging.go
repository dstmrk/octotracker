// Package logging configura il logging di OctoTracker.
//
// Replica il formato della versione Python:
//
//	2026-10-16 12:00:00 - checker - INFO - ✅ Checker completato
//
// e scrive sia su stdout (per i log Docker) sia su data/octotracker.log con
// rotazione giornaliera (file corrente + 2 giorni precedenti).
//
// Uso nei package:
//
//	var logger = logging.Logger("checker")
//	logger.Info(fmt.Sprintf("✅ Notifica inviata a %s", userID))
//
// Logger restituisce un logger "pigro": il handler effettivo viene risolto a
// ogni chiamata, quindi i logger dichiarati come variabili di package
// funzionano anche se Setup viene chiamato dopo la loro inizializzazione.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LevelCritical corrisponde a logging.CRITICAL di Python.
const LevelCritical = slog.Level(12)

// nameKey è l'attributo che porta il nome del logger (%(name)s in Python).
const nameKey = "logger"

// Logger restituisce un logger con il nome indicato (es. "checker", "database").
func Logger(name string) *slog.Logger {
	return slog.New(lazyHandler{name: name})
}

// Critical logga a livello CRITICAL.
func Critical(l *slog.Logger, msg string, args ...any) {
	l.Log(context.Background(), LevelCritical, msg, args...)
}

// ParseLevel converte LOG_LEVEL ("DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL")
// nel livello slog. Valori sconosciuti diventano INFO, come getattr(logging, x, INFO).
func ParseLevel(s string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	case "CRITICAL", "FATAL":
		return LevelCritical
	default:
		return slog.LevelInfo
	}
}

// Setup installa il logger di default: stdout più, se dataDir non è vuoto,
// file con rotazione giornaliera in dataDir/octotracker.log.
// Restituisce una funzione da chiamare in chiusura per rilasciare il file.
func Setup(level string, dataDir string) (func() error, error) {
	writers := []io.Writer{os.Stdout}
	closeFn := func() error { return nil }

	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return closeFn, fmt.Errorf("creazione directory log: %w", err)
		}
		rw, err := NewDailyRotatingWriter(filepath.Join(dataDir, "octotracker.log"), 2, time.Now)
		if err != nil {
			return closeFn, err
		}
		writers = append(writers, rw)
		closeFn = rw.Close
	}

	h := NewTextHandler(io.MultiWriter(writers...), ParseLevel(level))
	slog.SetDefault(slog.New(h))
	return closeFn, nil
}

// ========== Handler con formato Python ==========

// TextHandler scrive record nel formato "data - nome - LIVELLO - messaggio".
// Gli attributi slog extra vengono aggiunti in coda come chiave=valore.
type TextHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Leveler
	attrs []slog.Attr
	group string
}

// NewTextHandler crea un handler con il formato Python.
func NewTextHandler(w io.Writer, level slog.Leveler) *TextHandler {
	return &TextHandler{mu: &sync.Mutex{}, w: w, level: level}
}

// Enabled implementa slog.Handler.
func (h *TextHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

// Handle implementa slog.Handler.
func (h *TextHandler) Handle(_ context.Context, r slog.Record) error {
	name := "root"
	var extra []string
	collect := func(a slog.Attr) {
		if a.Key == nameKey {
			name = a.Value.String()
			return
		}
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		extra = append(extra, fmt.Sprintf("%s=%v", key, a.Value.Any()))
	}
	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		collect(a)
		return true
	})

	ts := r.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	var b strings.Builder
	b.WriteString(ts.Format("2006-01-02 15:04:05"))
	b.WriteString(" - ")
	b.WriteString(name)
	b.WriteString(" - ")
	b.WriteString(LevelName(r.Level))
	b.WriteString(" - ")
	b.WriteString(r.Message)
	if len(extra) > 0 {
		b.WriteString(" ")
		b.WriteString(strings.Join(extra, " "))
	}
	b.WriteString("\n")

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

// WithAttrs implementa slog.Handler.
func (h *TextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &cp
}

// WithGroup implementa slog.Handler.
func (h *TextHandler) WithGroup(name string) slog.Handler {
	cp := *h
	if cp.group == "" {
		cp.group = name
	} else {
		cp.group += "." + name
	}
	return &cp
}

// LevelName restituisce il nome del livello come in Python.
func LevelName(l slog.Level) string {
	switch {
	case l >= LevelCritical:
		return "CRITICAL"
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// lazyHandler risolve slog.Default() a ogni chiamata e aggiunge il nome.
type lazyHandler struct {
	name  string
	attrs []slog.Attr
}

func (h lazyHandler) target() slog.Handler {
	t := slog.Default().Handler()
	attrs := append([]slog.Attr{slog.String(nameKey, h.name)}, h.attrs...)
	return t.WithAttrs(attrs)
}

func (h lazyHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return slog.Default().Handler().Enabled(ctx, l)
}

func (h lazyHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.target().Handle(ctx, r)
}

func (h lazyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return lazyHandler{name: h.name, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h lazyHandler) WithGroup(string) slog.Handler { return h }

// ========== File con rotazione giornaliera ==========

// DailyRotatingWriter scrive su un file e lo ruota al cambio di giorno,
// rinominandolo in <file>.YYYY-MM-DD (come TimedRotatingFileHandler di Python)
// e mantenendo al massimo backupCount file ruotati.
type DailyRotatingWriter struct {
	mu          sync.Mutex
	path        string
	backupCount int
	now         func() time.Time
	file        *os.File
	day         string
}

// NewDailyRotatingWriter apre (o crea) il file di log in append.
func NewDailyRotatingWriter(path string, backupCount int, now func() time.Time) (*DailyRotatingWriter, error) {
	w := &DailyRotatingWriter{path: path, backupCount: backupCount, now: now}
	if err := w.open(); err != nil {
		return nil, err
	}
	// Se il file esistente è di un giorno precedente, va ruotato subito.
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		w.day = info.ModTime().Format("2006-01-02")
	}
	return w, nil
}

func (w *DailyRotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("apertura file di log: %w", err)
	}
	w.file = f
	w.day = w.now().Format("2006-01-02")
	return nil
}

// Write implementa io.Writer.
func (w *DailyRotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if today := w.now().Format("2006-01-02"); today != w.day {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *DailyRotatingWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
	}
	rotated := w.path + "." + w.day
	if _, err := os.Stat(rotated); err == nil {
		_ = os.Remove(rotated)
	}
	if err := os.Rename(w.path, rotated); err != nil && !os.IsNotExist(err) {
		return err
	}
	w.cleanup()
	return w.open()
}

func (w *DailyRotatingWriter) cleanup() {
	matches, err := filepath.Glob(w.path + ".????-??-??")
	if err != nil || len(matches) <= w.backupCount {
		return
	}
	sort.Strings(matches)
	for _, m := range matches[:len(matches)-w.backupCount] {
		_ = os.Remove(m)
	}
}

// Close chiude il file corrente.
func (w *DailyRotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
