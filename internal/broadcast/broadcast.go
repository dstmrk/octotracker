// Package broadcast porta broadcast.py: invio di un messaggio a una lista
// di utenti letti da file, con anteprima e conferma da terminale.
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
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
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

// acceptedAnswers sono le risposte che confermano l'invio (dopo strip/upper).
var acceptedAnswers = []string{"S", "SI", "SÌ", "Y", "YES"}

// now è l'orologio per la durata dell'invio (time()); sostituibile nei test.
var now = time.Now

// resolveSafePath replica _resolve_safe_path: risolve filePath (relativo o
// assoluto) rispetto a baseDir (default: directory corrente) seguendo i
// symlink e restituisce un ValueError se il risultato è fuori da baseDir.
func resolveSafePath(filePath, baseDir string) (string, error) {
	if baseDir == "" {
		wd, err := pycompat.Getcwd()
		if err != nil {
			return "", err
		}
		baseDir = wd
	}
	base, err := pycompat.RealPath(baseDir)
	if err != nil {
		return "", err
	}
	joined := filePath
	if !strings.HasPrefix(filePath, "/") {
		joined = base + "/" + filePath // Path(base) / file_path
	}
	candidate, err := pycompat.RealPath(joined)
	if err != nil {
		return "", err
	}
	if candidate != base && base != "/" && !strings.HasPrefix(candidate, base+"/") {
		return "", pycompat.NewError("ValueError",
			"Percorso non consentito, fuori dalla directory di lavoro: "+filePath)
	}
	return candidate, nil
}

// readSafeFile risolve e legge un file di testo; notFound è il messaggio
// del FileNotFoundError se il file non esiste.
func readSafeFile(file, baseDir, notFound string) (string, error) {
	path, err := resolveSafePath(file, baseDir)
	if err != nil {
		return "", err
	}
	exists, err := pycompat.PathExists(path)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", pycompat.NewError("FileNotFoundError", notFound)
	}
	return pycompat.ReadText(path)
}

// LoadMessage carica il messaggio da file (load_message): spazi iniziali e
// finali rimossi. Errori: FileNotFoundError se il file non esiste,
// ValueError se è vuoto o fuori dalla directory consentita.
func LoadMessage(messageFile, baseDir string) (string, error) {
	text, err := readSafeFile(messageFile, baseDir, "File messaggio non trovato: "+messageFile)
	if err != nil {
		return "", err
	}
	message := pycompat.Strip(text)
	if message == "" {
		return "", pycompat.NewError("ValueError", "Il file messaggio è vuoto")
	}
	return message, nil
}

// LoadUsersFromFile carica gli user_id da file, uno per riga
// (load_users_from_file): righe vuote e commenti (#) vengono ignorati.
// Errori: FileNotFoundError, ValueError se non ci sono user_id validi.
func LoadUsersFromFile(usersFile, baseDir string) ([]string, error) {
	text, err := readSafeFile(usersFile, baseDir, "File utenti non trovato: "+usersFile)
	if err != nil {
		return nil, err
	}
	var userIDs []string
	for _, line := range strings.Split(pycompat.Strip(text), "\n") {
		line = pycompat.Strip(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			userIDs = append(userIDs, line)
		}
	}
	if len(userIDs) == 0 {
		return nil, pycompat.NewError("ValueError", fmt.Sprintf("Il file %s non contiene user_id validi", usersFile))
	}
	return userIDs, nil
}

// readInput replica input(prompt): scrive il prompt, legge una riga (senza
// il terminatore) e restituisce EOFError se lo stream è finito senza
// caratteri. La lettura si interrompe se ctx viene cancellato (Ctrl-C).
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
		if r.err == io.EOF {
			return "", pycompat.NewError("EOFError", "")
		}
		if r.err != nil {
			return "", r.err
		}
		line := strings.TrimSuffix(r.line, "\n")
		return strings.TrimSuffix(line, "\r"), nil
	}
}

// ConfirmSend mostra l'anteprima del messaggio e chiede conferma
// (confirm_send). Restituisce true se la risposta, senza spazi e in
// maiuscolo, è S, SI, SÌ, Y o YES.
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
	confirmation := strings.ToUpper(pycompat.Strip(answer))
	for _, a := range acceptedAnswers {
		if confirmation == a {
			return true, nil
		}
	}
	return false, nil
}

// errorKind è la classe di eccezione PTB di un errore di invio.
type errorKind int

const (
	kindRetryAfter errorKind = iota // telegram.error.RetryAfter
	kindNetwork                     // TimedOut / NetworkError (incluso BadRequest)
	kindTelegram                    // ogni altro TelegramError
)

// classifyError mappa un errore gotgbot sulla classe PTB (come
// checker.classifySendError): retry_after → RetryAfter;
// migrate_to_chat_id, 401, 403, 404, 409 → TelegramError; 400 (BadRequest,
// sottoclasse di NetworkError in PTB) e ogni altro codice → NetworkError;
// errori di trasporto → TimedOut/NetworkError.
func classifyError(err error) errorKind {
	var tgErr *gotgbot.TelegramError
	if !errors.As(err, &tgErr) {
		return kindNetwork
	}
	if p := tgErr.ResponseParams; p != nil {
		if p.MigrateToChatId != 0 {
			return kindTelegram
		}
		if p.RetryAfter != 0 {
			return kindRetryAfter
		}
	}
	switch tgErr.Code {
	case http.StatusForbidden, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict:
		return kindTelegram
	}
	return kindNetwork
}

// errorString restituisce str(e) dell'eccezione PTB: "Timed out" per i
// timeout di trasporto, tg.ErrorString per il resto.
func errorString(err error) string {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "Timed out"
	}
	return tg.ErrorString(err)
}

// sendMessage invia il messaggio come bot.send_message(chat_id=user_id,
// text=message, parse_mode="HTML"); un user_id non numerico viene inviato
// come stringa, come fa PTB.
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

// SendBroadcastMessage invia il messaggio a un utente
// (send_broadcast_message) e restituisce true se l'invio riesce. Gli errori
// Telegram vengono loggati e restituiscono false; un invio interrotto dalla
// cancellazione di ctx (CancelledError in Python) restituisce false senza log.
func SendBroadcastMessage(ctx context.Context, b *gotgbot.Bot, userID, message string) bool {
	err := sendMessage(ctx, b, userID, message)
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return false
	}
	switch classifyError(err) {
	case kindRetryAfter:
		var tgErr *gotgbot.TelegramError
		errors.As(err, &tgErr)
		logger.Warn(fmt.Sprintf("⏱️  Rate limit per utente %s: riprova dopo %ds", userID, tgErr.ResponseParams.RetryAfter))
	case kindNetwork:
		logger.Error(fmt.Sprintf("❌ Errore di rete per utente %s: %s", userID, errorString(err)))
	default:
		logger.Error(fmt.Sprintf("❌ Errore Telegram per utente %s: %s", userID, errorString(err)))
	}
	return false
}

// sendRecovering esegue l'invio trattando un panic come un'eccezione non
// Telegram: in Python risale fino a gather(return_exceptions=True) e conta
// come fallimento senza alcun log.
func sendRecovering(send func() bool) (ok, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			ok, panicked = false, true
		}
	}()
	return send(), false
}

// SendBroadcastsParallel invia il messaggio a tutti gli utenti con al
// massimo batchSize invii simultanei, avviati nell'ordine della lista
// (asyncio.Semaphore + gather). Restituisce (inviati, falliti).
//
// Come asyncio.Semaphore: batchSize negativo è un ValueError e batchSize 0
// blocca ogni invio finché ctx non viene cancellato. Se ctx viene
// cancellato non partono nuovi invii e viene restituito ctx.Err().
func SendBroadcastsParallel(ctx context.Context, b *gotgbot.Bot, userIDs []string, message string, batchSize int) (successful, failed int, err error) {
	if batchSize < 0 {
		return 0, 0, pycompat.NewError("ValueError", "Semaphore initial value must be >= 0")
	}
	logger.Info(fmt.Sprintf("📨 Invio %d messaggi in parallelo (max %d simultanei)...", len(userIDs), batchSize))
	if len(userIDs) == 0 {
		return 0, 0, nil
	}
	if batchSize == 0 {
		<-ctx.Done()
		return 0, 0, ctx.Err()
	}

	sem := make(chan struct{}, batchSize)
	results := make([]bool, len(userIDs))
	var wg sync.WaitGroup
loop:
	for i, userID := range userIDs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		if ctx.Err() != nil {
			<-sem
			break
		}
		wg.Add(1)
		go func(position int, userID string) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, panicked := sendRecovering(func() bool { return SendBroadcastMessage(ctx, b, userID, message) })
			results[position-1] = ok
			switch {
			case panicked || ctx.Err() != nil && !ok:
				// eccezione non Telegram o cancellazione: nessun log
			case ok:
				logger.Info(fmt.Sprintf("  ✅ Messaggio inviato al destinatario #%d", position))
			default:
				logger.Warn(fmt.Sprintf("  ❌ Invio fallito per il destinatario #%d", position))
			}
		}(i+1, userID)
	}
	wg.Wait()

	for _, ok := range results {
		if ok {
			successful++
		}
	}
	if err := ctx.Err(); err != nil {
		return successful, len(results) - successful, err
	}
	return successful, len(results) - successful, nil
}

// Options configura Run.
type Options struct {
	MessageFile string    // file del messaggio (DefaultMessageFile nel sottocomando)
	UsersFile   string    // file degli user_id (DefaultUsersFile nel sottocomando)
	BatchSize   int       // invii simultanei, usato così com'è (DefaultBatchSize è il default Python)
	BaseDir     string    // directory entro cui devono stare i file (default: directory corrente)
	Stdin       io.Reader // risposta alla conferma (default os.Stdin)
	Stdout      io.Writer // anteprima e riepilogo (default os.Stdout)
}

// DefaultOptions restituisce le opzioni di default del sottocomando.
func DefaultOptions() Options {
	return Options{MessageFile: DefaultMessageFile, UsersFile: DefaultUsersFile, BatchSize: DefaultBatchSize}
}

// Result sono le statistiche di invio ({"successful", "failed", "total"}).
type Result struct {
	Successful int
	Failed     int
	Total      int
}

// Run carica messaggio e utenti, chiede conferma e invia il broadcast
// (broadcast_to_users). Se l'utente non conferma restituisce un Result
// vuoto. Errori: FileNotFoundError / ValueError (pycompat.Error) per i file,
// ctx.Err() se interrotto, altri errori per I/O e input.
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

	// Carica il messaggio
	message, err := LoadMessage(opts.MessageFile, opts.BaseDir)
	if err != nil {
		return Result{}, err
	}

	// Carica gli utenti dal file
	userIDs, err := LoadUsersFromFile(opts.UsersFile, opts.BaseDir)
	if err != nil {
		return Result{}, err
	}
	userCount := len(userIDs)

	// Mostra preview e chiedi conferma
	confirmed, err := ConfirmSend(ctx, message, userCount, opts.BatchSize, in, out)
	if err != nil {
		return Result{}, err
	}
	if !confirmed {
		logger.Info("❌ Invio annullato dall'utente")
		return Result{}, nil
	}

	// Inizia l'invio
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

	// Riepilogo
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

// MainConfig configura Main (il main() di broadcast.py).
type MainConfig struct {
	Args      []string // argomenti dopo il sottocomando: [message_file] [users_file]
	BotToken  string   // TELEGRAM_BOT_TOKEN
	BatchSize int      // BROADCAST_BATCH_SIZE (config.Load usa già il default 10)
	// NewBot crea il client Telegram dopo la conferma (default
	// checker.NewBot: nessuna chiamata di rete, come Bot(token) di PTB).
	NewBot  func(token string) (*gotgbot.Bot, error)
	BaseDir string    // default: directory corrente
	Stdin   io.Reader // default os.Stdin
	Stdout  io.Writer // default os.Stdout
}

// Main replica main(): verifica il token, sceglie i file (default
// message.txt e users.txt) ed esegue il broadcast. Restituisce il codice di
// uscita: 0 se completato (anche se annullato dall'utente), 1 in caso di
// errore o interruzione (ctx cancellato, es. Ctrl-C).
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
	switch {
	case err == nil:
		return 0
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		logger.Info("\n❌ Invio interrotto dall'utente")
	case pycompat.IsInstance(err, "FileNotFoundError"), pycompat.IsInstance(err, "ValueError"):
		logger.Error(fmt.Sprintf("❌ %s", err.Error()))
	default:
		logger.Error(fmt.Sprintf("❌ Errore durante l'invio broadcast: %s", err.Error()))
	}
	return 1
}
