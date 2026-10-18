package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dstmrk/octotracker/internal/logging"
)

// loggerNameKey è l'attributo con cui internal/logging passa il nome del
// logger (%(name)s).
const loggerNameKey = "logger"

// ScriptFormat è il formato di logging.basicConfig di uno script standalone.
type ScriptFormat struct {
	// TimeLayout è datefmt in formato Go ("" = nessun %(asctime)s).
	TimeLayout string
	// Msecs aggiunge ",mmm" all'ora (datefmt di default di Python).
	Msecs bool
	// Name stampa %(name)s.
	Name bool
	// Level stampa %(levelname)s.
	Level bool
}

// Formati dei sottocomandi che sostituiscono gli script Python.
var (
	// FormatChecker è il formato di checker.py e reminder.py:
	// "%(asctime)s - %(levelname)s - %(message)s" con datefmt "%Y-%m-%d %H:%M:%S".
	FormatChecker = ScriptFormat{TimeLayout: "2006-01-02 15:04:05", Level: true}
	// FormatBroadcast è il formato di broadcast.py:
	// "%(asctime)s - %(name)s - %(levelname)s - %(message)s" con il datefmt
	// di default ("2026-10-18 12:00:00,123").
	FormatBroadcast = ScriptFormat{TimeLayout: "2006-01-02 15:04:05", Msecs: true, Name: true, Level: true}
)

// ScriptHandler è uno slog.Handler che scrive come lo StreamHandler di
// logging.basicConfig (una riga per record, campi separati da " - ", senza
// attributi extra).
type ScriptHandler struct {
	mu     *sync.Mutex
	w      io.Writer
	level  slog.Leveler
	format ScriptFormat
	name   string
}

// NewScriptHandler crea il handler (basicConfig scrive su sys.stderr).
func NewScriptHandler(w io.Writer, level slog.Leveler, format ScriptFormat) *ScriptHandler {
	return &ScriptHandler{mu: &sync.Mutex{}, w: w, level: level, format: format, name: "root"}
}

// Enabled implementa slog.Handler.
func (h *ScriptHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

// Handle implementa slog.Handler.
func (h *ScriptHandler) Handle(_ context.Context, r slog.Record) error {
	var parts []string
	if h.format.TimeLayout != "" {
		ts := r.Time
		if ts.IsZero() {
			ts = time.Now()
		}
		s := ts.Format(h.format.TimeLayout)
		if h.format.Msecs {
			s += fmt.Sprintf(",%03d", ts.Nanosecond()/int(time.Millisecond))
		}
		parts = append(parts, s)
	}
	if h.format.Name {
		parts = append(parts, h.name)
	}
	if h.format.Level {
		parts = append(parts, logging.LevelName(r.Level))
	}
	parts = append(parts, r.Message)
	line := strings.Join(parts, " - ") + "\n"

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, line)
	return err
}

// WithAttrs implementa slog.Handler: conserva solo il nome del logger.
func (h *ScriptHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	for _, a := range attrs {
		if a.Key == loggerNameKey {
			cp.name = a.Value.String()
		}
	}
	return &cp
}

// WithGroup implementa slog.Handler.
func (h *ScriptHandler) WithGroup(string) slog.Handler { return h }
