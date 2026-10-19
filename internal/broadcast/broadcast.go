// Package broadcast invia un messaggio a una lista di utenti letti da file,
// con anteprima e conferma da terminale.
//
// Uso (sottocomando "octotracker broadcast [message_file] [users_file]"):
//
//	code := broadcast.Main(ctx, broadcast.MainConfig{
//	    Args:      args,                   // argomenti dopo "broadcast"
//	    BotToken:  cfg.BotToken,           // TELEGRAM_BOT_TOKEN
//	    BatchSize: cfg.BroadcastBatchSize, // BROADCAST_BATCH_SIZE (default 10)
//	})
//	os.Exit(code)
//
// oppure, con un bot già creato, Run(ctx, b, opts). I file di default sono
// message.txt e users.txt; i percorsi devono restare dentro la directory
// di lavoro (protezione da path traversal). Il messaggio (HTML) viene
// inviato in parallelo con al massimo BatchSize invii simultanei.
package broadcast

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/tg"
)

var logger = logging.Logger("broadcast")

// DefaultBatchSize è il numero di invii simultanei di default (BROADCAST_BATCH_SIZE).
const DefaultBatchSize = 10

// File di default del sottocomando.
const (
	DefaultMessageFile = "message.txt"
	DefaultUsersFile   = "users.txt"
)

// separator è la riga "=" * 70 dell'anteprima e del riepilogo.
var separator = strings.Repeat("=", 70)

// acceptedAnswers sono le risposte che confermano l'invio (senza spazi ai
// lati, in maiuscolo).
var acceptedAnswers = []string{"S", "SI", "SÌ", "Y", "YES"}

// now è l'orologio per la durata dell'invio; sostituibile nei test.
var now = time.Now

// inputError è un errore sui file di input: il messaggio, già pensato per
// l'operatore, viene loggato così com'è.
type inputError string

func (e inputError) Error() string { return string(e) }

// resolveSafePath risolve filePath (relativo o assoluto) rispetto a
// baseDir (default: directory corrente) seguendo i symlink e restituisce
// un errore se il risultato è fuori da baseDir. Un file inesistente viene
// controllato sul percorso ripulito da "." e "..".
func resolveSafePath(filePath, baseDir string) (string, error) {
	if baseDir == "" {
		baseDir = "."
	}
	base, err := filepath.Abs(baseDir)
	if err == nil {
		base, err = filepath.EvalSymlinks(base)
	}
	if err != nil {
		return "", err
	}
	candidate := filePath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	switch {
	case err == nil:
		candidate = resolved
	case errors.Is(err, fs.ErrNotExist):
		candidate = filepath.Clean(candidate)
	default:
		return "", err
	}
	if rel, err := filepath.Rel(base, candidate); err != nil || !filepath.IsLocal(rel) {
		return "", inputError("Percorso non consentito, fuori dalla directory di lavoro: " + filePath)
	}
	return candidate, nil
}

// readSafeFile risolve e legge un file di testo, con i fine riga "\r\n" e
// "\r" convertiti in "\n"; notFound è il messaggio se il file non esiste.
func readSafeFile(file, baseDir, notFound string) (string, error) {
	path, err := resolveSafePath(file, baseDir)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", inputError(notFound)
	}
	if err != nil {
		return "", err
	}
	return strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(data)), nil
}

// LoadMessage carica il messaggio da file, senza spazi iniziali e finali.
// Errori: file inesistente, vuoto o fuori dalla directory consentita.
func LoadMessage(messageFile, baseDir string) (string, error) {
	text, err := readSafeFile(messageFile, baseDir, "File messaggio non trovato: "+messageFile)
	if err != nil {
		return "", err
	}
	message := strings.TrimSpace(text)
	if message == "" {
		return "", inputError("Il file messaggio è vuoto")
	}
	return message, nil
}

// LoadUsersFromFile carica gli user_id da file, uno per riga: righe vuote
// e commenti (#) vengono ignorati. Errori: file inesistente o fuori dalla
// directory consentita, nessun user_id.
func LoadUsersFromFile(usersFile, baseDir string) ([]string, error) {
	text, err := readSafeFile(usersFile, baseDir, "File utenti non trovato: "+usersFile)
	if err != nil {
		return nil, err
	}
	var userIDs []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			userIDs = append(userIDs, line)
		}
	}
	if len(userIDs) == 0 {
		return nil, inputError(fmt.Sprintf("Il file %s non contiene user_id validi", usersFile))
	}
	return userIDs, nil
}

// readInput scrive il prompt e legge una riga (senza il terminatore);
// io.EOF se lo stream è finito senza caratteri. La lettura si interrompe
// se ctx viene cancellato (Ctrl-C).
func readInput(ctx context.Context, in io.Reader, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err == io.EOF && line != "" {
			err = nil
		}
		ch <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return strings.TrimRight(r.line, "\r\n"), r.err
	}
}

// ConfirmSend mostra l'anteprima del messaggio e chiede conferma.
// Restituisce true se la risposta, senza spazi e in maiuscolo, è S, SI,
// SÌ, Y o YES.
func ConfirmSend(ctx context.Context, message string, userCount, batchSize int, in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprintln(out, "\n"+separator)
	fmt.Fprintln(out, "📢 BROADCAST MESSAGE PREVIEW")
	fmt.Fprintln(out, separator)
	fmt.Fprintf(out, "\n%s\n\n", message)
	fmt.Fprintln(out, separator)
	fmt.Fprintln(out, "\n📊 Configurazione invio:")
	fmt.Fprintf(out, "   • Destinatari: %d utenti\n", userCount)
	fmt.Fprintf(out, "   • Batch size: %d messaggi simultanei\n", batchSize)
	fmt.Fprintf(out, "\n⚠️  Stai per inviare questo messaggio a %d utenti.\n", userCount)

	answer, err := readInput(ctx, in, out, "\nSei sicuro? (S/N): ")
	if err != nil {
		return false, err
	}
	return slices.Contains(acceptedAnswers, strings.ToUpper(strings.TrimSpace(answer))), nil
}

// sendMessage invia il messaggio in HTML; uno user_id non numerico (es.
// "@canale") viene inviato come stringa.
func sendMessage(ctx context.Context, b *gotgbot.Bot, userID, message string) error {
	if chatID, err := strconv.ParseInt(userID, 10, 64); err == nil {
		_, err = b.SendMessageWithContext(ctx, chatID, message, &gotgbot.SendMessageOpts{ParseMode: "HTML"})
		return err
	}
	_, err := b.RequestWithContext(ctx, "sendMessage", map[string]any{
		"chat_id": userID, "text": message, "parse_mode": "HTML",
	}, nil)
	return err
}

// SendBroadcastMessage invia il messaggio a un utente e restituisce true
// se l'invio riesce. Gli errori vengono loggati (rate limit, errore
// Telegram o errore di rete) e restituiscono false; un invio interrotto
// dalla cancellazione di ctx restituisce false senza log.
func SendBroadcastMessage(ctx context.Context, b *gotgbot.Bot, userID, message string) bool {
	err := sendMessage(ctx, b, userID, message)
	if err == nil {
		return true
	}
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return false
	}
	var tgErr *gotgbot.TelegramError
	if !errors.As(err, &tgErr) {
		logger.Error(fmt.Sprintf("❌ Errore di rete per utente %s: %s", userID, err))
		return false
	}
	if p := tgErr.ResponseParams; p != nil && p.RetryAfter > 0 {
		logger.Warn(fmt.Sprintf("⏱️  Rate limit per utente %s: riprova dopo %ds", userID, p.RetryAfter))
		return false
	}
	logger.Error(fmt.Sprintf("❌ Errore Telegram per utente %s: %s", userID, tg.ErrorString(err)))
	return false
}

// sendOne invia al destinatario in posizione position e ne logga l'esito;
// un invio interrotto dalla cancellazione di ctx non viene loggato.
func sendOne(ctx context.Context, b *gotgbot.Bot, position int, userID, message string) bool {
	ok := SendBroadcastMessage(ctx, b, userID, message)
	switch {
	case ok:
		logger.Info(fmt.Sprintf("  ✅ Messaggio inviato al destinatario #%d", position))
	case ctx.Err() == nil:
		logger.Warn(fmt.Sprintf("  ❌ Invio fallito per il destinatario #%d", position))
	}
	return ok
}

// acquire occupa un posto del semaforo; false (senza occuparlo) se ctx è
// stato cancellato.
func acquire(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	if ctx.Err() != nil {
		<-sem
		return false
	}
	return true
}

// SendBroadcastsParallel invia il messaggio a tutti gli utenti con al
// massimo batchSize (≥ 1) invii simultanei, avviati nell'ordine della
// lista. Restituisce (inviati, falliti). Se ctx viene cancellato non
// partono nuovi invii, quelli non partiti contano come falliti e viene
// restituito ctx.Err().
func SendBroadcastsParallel(ctx context.Context, b *gotgbot.Bot, userIDs []string, message string, batchSize int) (successful, failed int, err error) {
	if batchSize < 1 {
		return 0, 0, fmt.Errorf("batch size non valido: %d (minimo 1)", batchSize)
	}
	logger.Info(fmt.Sprintf("📨 Invio %d messaggi in parallelo (max %d simultanei)...", len(userIDs), batchSize))

	sem := make(chan struct{}, batchSize)
	results := make([]bool, len(userIDs))
	var wg sync.WaitGroup
	for i, userID := range userIDs {
		if !acquire(ctx, sem) {
			break
		}
		wg.Go(func() {
			defer func() { <-sem }()
			results[i] = sendOne(ctx, b, i+1, userID, message)
		})
	}
	wg.Wait()

	for _, ok := range results {
		if ok {
			successful++
		}
	}
	return successful, len(results) - successful, ctx.Err()
}

// Options configura Run.
type Options struct {
	MessageFile string    // file del messaggio (DefaultMessageFile nel sottocomando)
	UsersFile   string    // file degli user_id (DefaultUsersFile nel sottocomando)
	BatchSize   int       // invii simultanei (DefaultBatchSize è il default del sottocomando)
	BaseDir     string    // directory entro cui devono stare i file (default: directory corrente)
	Stdin       io.Reader // risposta alla conferma (default os.Stdin)
	Stdout      io.Writer // anteprima e riepilogo (default os.Stdout)
}

// Result sono le statistiche di invio.
type Result struct {
	Successful int
	Failed     int
	Total      int
}

// Run carica messaggio e utenti, chiede conferma e invia il broadcast. Se
// l'utente non conferma restituisce un Result vuoto. Errori: file non
// validi, ctx.Err() se interrotto, errori di I/O e di input.
func Run(ctx context.Context, b *gotgbot.Bot, opts Options) (Result, error) {
	return broadcastToUsers(ctx, func() (*gotgbot.Bot, error) { return b, nil }, opts)
}

func broadcastToUsers(ctx context.Context, newBot func() (*gotgbot.Bot, error), opts Options) (Result, error) {
	in, out := opts.Stdin, opts.Stdout
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}

	message, err := LoadMessage(opts.MessageFile, opts.BaseDir)
	if err != nil {
		return Result{}, err
	}
	userIDs, err := LoadUsersFromFile(opts.UsersFile, opts.BaseDir)
	if err != nil {
		return Result{}, err
	}
	userCount := len(userIDs)

	confirmed, err := ConfirmSend(ctx, message, userCount, opts.BatchSize, in, out)
	if err != nil {
		return Result{}, err
	}
	if !confirmed {
		logger.Info("❌ Invio annullato dall'utente")
		return Result{}, nil
	}

	logger.Info(fmt.Sprintf("\n🚀 Inizio invio broadcast a %d utenti...", userCount))
	startTime := now()

	b, err := newBot()
	if err != nil {
		return Result{}, err
	}
	successful, failed, err := SendBroadcastsParallel(ctx, b, userIDs, message, opts.BatchSize)
	if err != nil {
		return Result{}, err
	}

	duration := now().Sub(startTime).Seconds()

	fmt.Fprintln(out, "\n"+separator)
	fmt.Fprintln(out, "📊 RIEPILOGO INVIO")
	fmt.Fprintln(out, separator)
	fmt.Fprintf(out, "  ✅ Inviati con successo: %d/%d\n", successful, userCount)
	fmt.Fprintf(out, "  ❌ Falliti: %d/%d\n", failed, userCount)
	fmt.Fprintf(out, "  ⏱️  Tempo totale: %.2fs\n", duration)
	fmt.Fprintln(out, separator+"\n")

	logger.Info(fmt.Sprintf("✅ Broadcast completato in %.2fs - Successi: %d/%d, Falliti: %d/%d",
		duration, successful, userCount, failed, userCount))

	return Result{Successful: successful, Failed: failed, Total: userCount}, nil
}

// MainConfig configura Main.
type MainConfig struct {
	Args      []string // argomenti dopo il sottocomando: [message_file] [users_file]
	BotToken  string   // TELEGRAM_BOT_TOKEN
	BatchSize int      // BROADCAST_BATCH_SIZE (config.Load usa già il default 10)
	// NewBot crea il client Telegram dopo la conferma (default
	// checker.NewBot, che non fa chiamate di rete).
	NewBot  func(token string) (*gotgbot.Bot, error)
	BaseDir string    // default: directory corrente
	Stdin   io.Reader // default os.Stdin
	Stdout  io.Writer // default os.Stdout
}

// Main verifica il token, sceglie i file (default message.txt e
// users.txt) ed esegue il broadcast. Restituisce il codice di uscita: 0 se
// completato (anche se annullato dall'utente), 1 in caso di errore o
// interruzione (ctx cancellato, es. Ctrl-C).
func Main(ctx context.Context, cfg MainConfig) int {
	if cfg.BotToken == "" {
		logger.Error("❌ TELEGRAM_BOT_TOKEN non configurato nel file .env")
		return 1
	}
	opts := Options{
		MessageFile: DefaultMessageFile,
		UsersFile:   DefaultUsersFile,
		BatchSize:   cfg.BatchSize,
		BaseDir:     cfg.BaseDir,
		Stdin:       cfg.Stdin,
		Stdout:      cfg.Stdout,
	}
	if len(cfg.Args) > 0 {
		opts.MessageFile = cfg.Args[0]
	}
	if len(cfg.Args) > 1 {
		opts.UsersFile = cfg.Args[1]
	}
	newBot := cfg.NewBot
	if newBot == nil {
		newBot = func(token string) (*gotgbot.Bot, error) { return checker.NewBot(token), nil }
	}

	_, err := broadcastToUsers(ctx, func() (*gotgbot.Bot, error) { return newBot(cfg.BotToken) }, opts)
	var inErr inputError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		logger.Info("\n❌ Invio interrotto dall'utente")
	case errors.As(err, &inErr):
		logger.Error("❌ " + err.Error())
	default:
		logger.Error("❌ Errore durante l'invio broadcast: " + err.Error())
	}
	return 1
}
