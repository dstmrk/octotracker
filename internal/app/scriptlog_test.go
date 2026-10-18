package app

import (
	"bytes"
	"log/slog"
	"regexp"
	"testing"

	"github.com/dstmrk/octotracker/internal/logging"
)

// TestScriptHandlerFormats verifica i formati di logging.basicConfig degli
// script standalone.
func TestScriptHandlerFormats(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)

	slog.SetDefault(slog.New(NewScriptHandler(&buf, slog.LevelInfo, FormatChecker)))
	l := logging.Logger("checker")
	l.Debug("nascosto")
	l.Info("✅ Checker completato", "extra", 1)
	l.Warn("attenzione")
	logging.Critical(l, "critico")

	checkerRe := regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d - INFO - ✅ Checker completato\n` +
		`\d{4}-\d\d-\d\d \d\d:\d\d:\d\d - WARNING - attenzione\n` +
		`\d{4}-\d\d-\d\d \d\d:\d\d:\d\d - CRITICAL - critico\n$`)
	if !checkerRe.MatchString(buf.String()) {
		t.Errorf("formato checker:\n%s", buf.String())
	}

	buf.Reset()
	slog.SetDefault(slog.New(NewScriptHandler(&buf, slog.LevelInfo, FormatBroadcast)))
	logging.Logger("broadcast").WithGroup("g").Info("🚀 Inizio invio")
	slog.Info("senza nome")
	broadcastRe := regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d,\d{3} - broadcast - INFO - 🚀 Inizio invio\n` +
		`\d{4}-\d\d-\d\d \d\d:\d\d:\d\d,\d{3} - root - INFO - senza nome\n$`)
	if !broadcastRe.MatchString(buf.String()) {
		t.Errorf("formato broadcast:\n%s", buf.String())
	}

	buf.Reset()
	h := NewScriptHandler(&buf, slog.LevelDebug, ScriptFormat{})
	_ = h.Handle(t.Context(), slog.Record{Message: "solo messaggio"})
	if buf.String() != "solo messaggio\n" {
		t.Errorf("formato vuoto: %q", buf.String())
	}
}
