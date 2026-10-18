package scraper

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Layout dell'orario per NewBasicHandler, come i format di logging.basicConfig
// degli script standalone.
const (
	// ScrapeLogTimeLayout: data_reader.py usa format="%(message)s" (niente orario).
	ScrapeLogTimeLayout = ""
	// BackfillLogTimeLayout: backfill_rate_history.py usa
	// format="%(asctime)s %(message)s" con datefmt="%H:%M:%S".
	BackfillLogTimeLayout = "15:04:05"
)

// BasicHandler è uno slog.Handler che scrive come il StreamHandler installato
// da logging.basicConfig negli script standalone: "%(message)s" oppure
// "%(asctime)s %(message)s" (ora locale nel layout indicato). Gli attributi
// slog non vengono stampati.
type BasicHandler struct {
	mu         *sync.Mutex
	w          io.Writer
	level      slog.Leveler
	timeLayout string
}

// NewBasicHandler crea il handler: w è lo stream (basicConfig usa
// sys.stderr), level il livello minimo (es. logging.ParseLevel(LOG_LEVEL)),
// timeLayout ScrapeLogTimeLayout o BackfillLogTimeLayout.
func NewBasicHandler(w io.Writer, level slog.Leveler, timeLayout string) *BasicHandler {
	return &BasicHandler{mu: &sync.Mutex{}, w: w, level: level, timeLayout: timeLayout}
}

// Enabled implementa slog.Handler.
func (h *BasicHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

// Handle implementa slog.Handler.
func (h *BasicHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if h.timeLayout != "" {
		ts := r.Time
		if ts.IsZero() {
			ts = time.Now()
		}
		b.WriteString(ts.Format(h.timeLayout))
		b.WriteByte(' ')
	}
	b.WriteString(r.Message)
	b.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

// WithAttrs implementa slog.Handler (gli attributi, compreso il nome del
// logger, non compaiono nel formato).
func (h *BasicHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implementa slog.Handler.
func (h *BasicHandler) WithGroup(string) slog.Handler { return h }
