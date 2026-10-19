package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// setDefault installa un TextHandler su buffer come logger di default.
func setDefault(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(NewTextHandler(&buf, level)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return &buf
}

var lineRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} - [^ ]+ - (DEBUG|INFO|WARNING|ERROR|CRITICAL) - .*$`)

func TestTextHandlerFormat(t *testing.T) {
	var buf bytes.Buffer
	h := NewTextHandler(&buf, slog.LevelDebug)
	ts := time.Date(2026, 10, 16, 9, 5, 7, 123, time.Local)
	r := slog.NewRecord(ts, slog.LevelInfo, "✅ Checker completato", 0)
	if err := h.WithAttrs([]slog.Attr{slog.String(nameKey, "checker")}).Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	want := "2026-10-16 09:05:07 - checker - INFO - ✅ Checker completato\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestTextHandlerRootNameZeroTimeAndAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewTextHandler(&buf, slog.LevelDebug)
	r := slog.NewRecord(time.Time{}, slog.LevelWarn, "msg", 0)
	r.AddAttrs(slog.Int("n", 3), slog.String("s", "x"))
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSuffix(buf.String(), "\n")
	if !lineRe.MatchString(line) {
		t.Fatalf("formato non valido: %q", line)
	}
	if !strings.HasSuffix(line, " - root - WARNING - msg n=3 s=x") {
		t.Fatalf("got %q", line)
	}
}

func TestTextHandlerGroups(t *testing.T) {
	var buf bytes.Buffer
	var h slog.Handler = NewTextHandler(&buf, slog.LevelDebug)
	h = h.WithAttrs([]slog.Attr{slog.String("a", "1")})
	h = h.WithGroup("g")
	h = h.WithAttrs([]slog.Attr{slog.String("b", "2"), slog.String(nameKey, "nome")})
	h = h.WithGroup("h")
	r := slog.NewRecord(time.Now(), slog.LevelError, "boom", 0)
	r.AddAttrs(slog.String("c", "3"))
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(buf.String(), " - nome - ERROR - boom a=1 g.b=2 g.h.c=3\n") {
		t.Fatalf("got %q", buf.String())
	}
}

func TestTextHandlerEnabled(t *testing.T) {
	h := NewTextHandler(&bytes.Buffer{}, slog.LevelWarn)
	ctx := context.Background()
	if h.Enabled(ctx, slog.LevelInfo) || !h.Enabled(ctx, slog.LevelWarn) || !h.Enabled(ctx, LevelCritical) {
		t.Fatal("Enabled non rispetta il livello")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disco pieno") }

func TestTextHandlerWriteError(t *testing.T) {
	h := NewTextHandler(failingWriter{}, slog.LevelDebug)
	if err := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "x", 0)); err == nil {
		t.Fatal("errore di scrittura atteso")
	}
}

func TestLevelName(t *testing.T) {
	tests := map[slog.Level]string{
		slog.Level(-8):  "DEBUG",
		slog.LevelDebug: "DEBUG",
		slog.Level(-1):  "DEBUG",
		slog.LevelInfo:  "INFO",
		slog.Level(2):   "INFO",
		slog.LevelWarn:  "WARNING",
		slog.LevelError: "ERROR",
		LevelCritical:   "CRITICAL",
		slog.Level(20):  "CRITICAL",
	}
	for l, want := range tests {
		if got := LevelName(l); got != want {
			t.Errorf("LevelName(%d) = %q, want %q", l, got, want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"DEBUG":    slog.LevelDebug,
		"debug":    slog.LevelDebug,
		"NOTSET":   slog.LevelDebug,
		"INFO":     slog.LevelInfo,
		"WARNING":  slog.LevelWarn,
		"warn":     slog.LevelWarn,
		"ERROR":    slog.LevelError,
		"CRITICAL": LevelCritical,
		"FATAL":    LevelCritical,
		"":         slog.LevelInfo,
		"verbose":  slog.LevelInfo,
		" DEBUG ":  slog.LevelInfo, // getattr(logging, " DEBUG ") non esiste
	}
	for in, want := range tests {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLazyLoggerResolvesDefaultLater(t *testing.T) {
	// Il logger viene creato prima di configurare il default (come una
	// variabile di package) e deve usare il default impostato dopo.
	logger := Logger("checker")
	buf := setDefault(t, slog.LevelInfo)

	logger.Info("✅ Notifica inviata a 123")
	logger.Debug("non visibile")
	if got := buf.String(); !strings.HasSuffix(got, " - checker - INFO - ✅ Notifica inviata a 123\n") || strings.Count(got, "\n") != 1 {
		t.Fatalf("got %q", got)
	}

	// Cambiando di nuovo il default il logger segue il nuovo handler
	buf2 := setDefault(t, slog.LevelDebug)
	logger.Debug("ora visibile")
	if !strings.HasSuffix(buf2.String(), " - checker - DEBUG - ora visibile\n") {
		t.Fatalf("got %q", buf2.String())
	}
}

func TestLazyLoggerAttrsGroupsAndCritical(t *testing.T) {
	buf := setDefault(t, slog.LevelDebug)
	logger := Logger("database").With("user", "42").WithGroup("g").With("k", "v")
	logger.Warn("attenzione", "x", 1)
	if !strings.HasSuffix(buf.String(), " - database - WARNING - attenzione user=42 g.k=v g.x=1\n") {
		t.Fatalf("got %q", buf.String())
	}

	buf.Reset()
	same := Logger("bot")
	for _, h := range []slog.Handler{same.Handler().WithAttrs(nil), same.Handler().WithGroup("")} {
		if lh, ok := h.(lazyHandler); !ok || len(lh.steps) != 0 || lh.name != "bot" {
			t.Errorf("WithAttrs(nil)/WithGroup(\"\") non devono aggiungere passi: %#v", h)
		}
	}
	Critical(same, "💥 errore critico")
	if !strings.HasSuffix(buf.String(), " - bot - CRITICAL - 💥 errore critico\n") {
		t.Fatalf("got %q", buf.String())
	}
}

func TestLazyLoggerEnabled(t *testing.T) {
	setDefault(t, slog.LevelError)
	logger := Logger("x")
	ctx := context.Background()
	if logger.Enabled(ctx, slog.LevelWarn) || !logger.Enabled(ctx, slog.LevelError) {
		t.Fatal("Enabled deve seguire il livello del default")
	}
}

// ========== DailyRotatingWriter ==========

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func write(t *testing.T, w *DailyRotatingWriter, s string) {
	t.Helper()
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestDailyRotatingWriterRotationAndCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "octotracker.log")
	clock := &fakeClock{t: time.Date(2026, 10, 10, 8, 0, 0, 0, time.Local)}
	w, err := NewDailyRotatingWriter(path, 2, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	write(t, w, "giorno 10 a\n")
	clock.set(time.Date(2026, 10, 10, 23, 59, 59, 0, time.Local))
	write(t, w, "giorno 10 b\n")
	if got := listDir(t, dir); len(got) != 1 {
		t.Fatalf("nessuna rotazione attesa nello stesso giorno: %v", got)
	}

	clock.set(time.Date(2026, 10, 11, 0, 0, 1, 0, time.Local))
	write(t, w, "giorno 11\n")
	if got := readFile(t, path+".2026-10-10"); got != "giorno 10 a\ngiorno 10 b\n" {
		t.Fatalf("file ruotato = %q", got)
	}
	if got := readFile(t, path); got != "giorno 11\n" {
		t.Fatalf("file corrente = %q", got)
	}

	for _, day := range []int{12, 13, 15} {
		clock.set(time.Date(2026, 10, day, 9, 0, 0, 0, time.Local))
		write(t, w, "giorno\n")
	}
	want := []string{"octotracker.log", "octotracker.log.2026-10-12", "octotracker.log.2026-10-13"}
	if got := listDir(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("file = %v, want %v (oggi + 2 backup)", got, want)
	}
}

func TestDailyRotatingWriterExistingOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "octotracker.log")
	if err := os.WriteFile(path, []byte("vecchio\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	// Un backup con lo stesso nome viene sostituito
	if err := os.WriteFile(path+".2026-10-01", []byte("da sostituire"), 0o644); err != nil {
		t.Fatal(err)
	}

	clock := &fakeClock{t: time.Date(2026, 10, 16, 10, 0, 0, 0, time.Local)}
	w, err := NewDailyRotatingWriter(path, 2, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	write(t, w, "nuovo\n")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path+".2026-10-01"); got != "vecchio\n" {
		t.Fatalf("backup = %q", got)
	}
	if got := readFile(t, path); got != "nuovo\n" {
		t.Fatalf("corrente = %q", got)
	}

	// Close è idempotente e Write dopo Close fallisce senza panic
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("Write dopo Close deve fallire")
	}
}

func TestDailyRotatingWriterOpenError(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewDailyRotatingWriter(dir, 2, time.Now); err == nil {
		t.Fatal("aprire una directory come log deve fallire")
	}
}

func TestDailyRotatingWriterRotateErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "octotracker.log")
	clock := &fakeClock{t: time.Date(2026, 10, 10, 8, 0, 0, 0, time.Local)}
	w, err := NewDailyRotatingWriter(path, 2, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	write(t, w, "a\n")

	// La destinazione della rotazione è una directory non vuota: la
	// rinomina fallisce ma il file viene riaperto e la scrittura prosegue.
	if err := os.MkdirAll(filepath.Join(path+".2026-10-10", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	clock.set(time.Date(2026, 10, 11, 8, 0, 0, 0, time.Local))
	write(t, w, "b\n")
	if got := readFile(t, path); got != "a\nb\n" {
		t.Fatalf("corrente = %q", got)
	}

	// La directory dei log sparisce: il file non è più creabile e la
	// scrittura fallisce...
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	clock.set(time.Date(2026, 10, 12, 8, 0, 0, 0, time.Local))
	if _, err := w.Write([]byte("c\n")); err == nil {
		t.Fatal("Write deve fallire se il file non può essere riaperto")
	}

	// ...e riprende appena la directory torna disponibile.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, w, "d\n")
	if got := readFile(t, path); got != "d\n" {
		t.Fatalf("corrente = %q", got)
	}
}

// ========== Setup ==========

// redirectStdout sostituisce os.Stdout con un file temporaneo.
func redirectStdout(t *testing.T) func() string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = orig; f.Close() })
	return func() string { return readFile(t, f.Name()) }
}

func TestSetupWithFile(t *testing.T) {
	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })
	stdout := redirectStdout(t)
	dir := filepath.Join(t.TempDir(), "data")

	closeFn, err := Setup("warning", dir)
	if err != nil {
		t.Fatal(err)
	}
	logger := Logger("bot")
	logger.Info("non visibile")
	logger.Warn("⚠️ visibile")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}

	fileContent := readFile(t, filepath.Join(dir, "octotracker.log"))
	for name, out := range map[string]string{"file": fileContent, "stdout": stdout()} {
		if strings.Contains(out, "non visibile") || !strings.HasSuffix(out, " - bot - WARNING - ⚠️ visibile\n") {
			t.Errorf("%s = %q", name, out)
		}
		if !lineRe.MatchString(strings.TrimSuffix(out, "\n")) {
			t.Errorf("%s: formato non valido %q", name, out)
		}
	}
}

func TestSetupStdoutOnly(t *testing.T) {
	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })
	stdout := redirectStdout(t)
	closeFn, err := Setup("DEBUG", "")
	if err != nil {
		t.Fatal(err)
	}
	Logger("scraper").Debug("dettaglio")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stdout(), " - scraper - DEBUG - dettaglio\n") {
		t.Fatalf("stdout = %q", stdout())
	}
}

func TestSetupErrors(t *testing.T) {
	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })

	// dataDir è un file: MkdirAll fallisce
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup("INFO", file); err == nil {
		t.Error("Setup deve fallire se dataDir è un file")
	}

	// octotracker.log è una directory: l'apertura fallisce
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "octotracker.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup("INFO", dir); err == nil {
		t.Error("Setup deve fallire se il file di log non è apribile")
	}
}

func TestDailyRotatingWriterConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "octotracker.log")
	clock := &fakeClock{t: time.Date(2026, 10, 10, 8, 0, 0, 0, time.Local)}
	w, err := NewDailyRotatingWriter(path, 2, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if j == 25 && i == 0 {
					clock.set(time.Date(2026, 10, 11, 8, 0, 0, 0, time.Local))
				}
				_, _ = w.Write([]byte("riga\n"))
			}
		}(i)
	}
	wg.Wait()
	total := strings.Count(readFile(t, path), "\n") + strings.Count(readFile(t, path+".2026-10-10"), "\n")
	if total != 400 {
		t.Fatalf("righe scritte = %d, want 400", total)
	}
}
