package scraper

// Porting di _download_xml (urllib.request.urlopen + response.read().decode("utf-8")).
//
// Il corpo della risposta non viene tenuto in memoria: passa, attraverso un
// validatore UTF-8, direttamente al parser XML in streaming (consume) e poi
// viene scaricato fino in fondo, così gli esiti restano quelli di Python, dove
// download e decodifica terminano prima del parsing: un errore di rete o di
// decodifica UTF-8 in qualunque punto del file è un download fallito, anche
// se il parser si era già fermato prima.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// downloadError è un'eccezione sollevata da _download_xml. code è il codice
// HTTP (HTTPError ha l'attributo code); 0 per le eccezioni senza code
// (URLError, errori di lettura, UnicodeDecodeError).
type downloadError struct {
	code int
	msg  string // str(e)
}

func (e *downloadError) Error() string { return e.msg }

// downloaded descrive un download riuscito: chars è len(content) (numero di
// caratteri del testo decodificato), head i primi 500 caratteri.
type downloaded struct {
	chars int
	head  string
}

// downloadXML scarica url con gli header di urllib e passa il corpo a consume.
// Restituisce *downloadError se il download fallisce, oppure l'errore del
// contesto se ctx viene annullato.
func (c *Client) downloadXML(ctx context.Context, url string, consume func(io.Reader)) (downloaded, error) {
	logger.Debug("Downloading " + url)

	// Il timeout di urlopen è un timeout di socket (per singola operazione,
	// non sul download complessivo): il watchdog annulla la richiesta solo se
	// passano Timeout secondi senza ricevere dati.
	timeout := c.timeout()
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	watchdog := time.AfterFunc(timeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		// urllib solleva ValueError/InvalidURL (non URLError): nessun warning.
		return downloaded{}, &downloadError{msg: err.Error()}
	}
	for _, h := range HTTPHeaders {
		req.Header.Set(h[0], h[1])
	}
	// urllib non chiede compressione: senza questo header net/http
	// richiederebbe gzip.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return downloaded{}, ctxErr
		}
		return downloaded{}, c.urlError(url, urlErrorReason(err, timedOut.Load()))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.Warn(fmt.Sprintf("⚠️  HTTP %d durante download %s", resp.StatusCode, url))
		reason := strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
		return downloaded{}, &downloadError{code: resp.StatusCode, msg: fmt.Sprintf("HTTP Error %d: %s", resp.StatusCode, reason)}
	}

	body := &utf8Validator{r: &watchdogReader{r: resp.Body, reset: func() { watchdog.Reset(timeout) }}}
	consume(body)
	_, _ = io.Copy(io.Discard, body)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return downloaded{}, ctxErr
	}
	if body.readErr != nil {
		return downloaded{}, &downloadError{msg: readErrorMessage(body.readErr, timedOut.Load(), url, body.bytes, resp.ContentLength)}
	}
	if body.decodeErr != "" {
		return downloaded{}, &downloadError{msg: body.decodeErr}
	}
	logger.Debug(fmt.Sprintf("Downloaded %d bytes", body.chars))
	return downloaded{chars: body.chars, head: body.head.String()}, nil
}

// urlError registra il warning di URLError e restituisce l'eccezione
// (str(URLError) = "<urlopen error reason>").
func (c *Client) urlError(url, reason string) error {
	logger.Warn(fmt.Sprintf("⚠️  Errore rete durante download %s: %s", url, reason))
	return &downloadError{msg: "<urlopen error " + reason + ">"}
}

// urlErrorReason approssima str(e.reason) di URLError per gli errori più
// comuni (timeout, DNS, errno), altrimenti usa il messaggio Go.
func urlErrorReason(err error, timedOut bool) string {
	if timedOut {
		return "timed out"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return "[Errno -2] Name or service not known"
	}
	if s, ok := errnoMessage(err); ok {
		return s
	}
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) && ue.Unwrap() != nil {
		return ue.Unwrap().Error()
	}
	return err.Error()
}

// errnoMessage formatta un errno come OSError di Python ("[Errno 111]
// Connection refused").
func errnoMessage(err error) (string, bool) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "", false
	}
	msg := []rune(errno.Error())
	if len(msg) > 0 {
		msg[0] = unicode.ToUpper(msg[0])
	}
	return fmt.Sprintf("[Errno %d] %s", int(errno), string(msg)), true
}

// readErrorMessage approssima str(e) delle eccezioni sollevate da
// response.read() (TimeoutError, IncompleteRead, OSError).
func readErrorMessage(err error, timedOut bool, url string, read, contentLength int64) string {
	switch {
	case timedOut:
		if strings.HasPrefix(url, "https:") {
			return "The read operation timed out"
		}
		return "timed out"
	case errors.Is(err, io.ErrUnexpectedEOF) && contentLength >= 0:
		return fmt.Sprintf("IncompleteRead(%d bytes read, %d more expected)", read, contentLength-read)
	}
	if s, ok := errnoMessage(err); ok {
		return s
	}
	return err.Error()
}

// watchdogReader rimanda il timeout ogni volta che arrivano dati.
type watchdogReader struct {
	r     io.Reader
	reset func()
}

func (w *watchdogReader) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if n > 0 {
		w.reset()
	}
	return n, err
}

// utf8Validator lascia passare i byte invariati e verifica, come
// bytes.decode("utf-8"), che siano UTF-8 valido. Conta i caratteri
// (len(content)) e conserva i primi 500 (xml_content[:500]). Il primo errore
// di decodifica viene registrato col messaggio di UnicodeDecodeError; gli
// errori del reader sottostante sono registrati in readErr e restituiti.
type utf8Validator struct {
	r io.Reader

	bytes     int64
	chars     int
	head      strings.Builder
	headChars int
	pending   []byte // sequenza multibyte incompleta a fine buffer
	pendStart int64  // posizione del primo byte di pending
	decodeErr string
	readErr   error
}

const headChars = 500

func (v *utf8Validator) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	if n > 0 {
		v.feed(p[:n])
		v.bytes += int64(n)
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			v.finish()
		} else if v.readErr == nil {
			v.readErr = err
		}
	}
	return n, err
}

func (v *utf8Validator) addRune(r rune) {
	v.chars++
	if v.headChars < headChars {
		v.head.WriteRune(r)
		v.headChars++
	}
}

func (v *utf8Validator) feed(b []byte) {
	if v.decodeErr != "" {
		return
	}
	pos := v.bytes
	if len(v.pending) > 0 {
		// Completa la sequenza rimasta in sospeso dal buffer precedente.
		need := utf8SeqLen(v.pending[0]) - len(v.pending)
		take := min(need, len(b))
		buf := append(append([]byte{}, v.pending...), b[:take]...)
		consumed, ok := v.decodeSeq(buf, v.pendStart, false)
		if !ok {
			return
		}
		if consumed == 0 { // ancora incompleta
			v.pending = buf
			return
		}
		v.pending = nil
		b = b[take:]
		pos += int64(take)
	}
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf {
			v.addRune(rune(b[i]))
			i++
			continue
		}
		consumed, ok := v.decodeSeq(b[i:], pos+int64(i), false)
		if !ok {
			return
		}
		if consumed == 0 {
			v.pending = append([]byte{}, b[i:]...)
			v.pendStart = pos + int64(i)
			return
		}
		i += consumed
	}
}

func (v *utf8Validator) finish() {
	if v.decodeErr == "" && len(v.pending) > 0 {
		v.decodeSeq(v.pending, v.pendStart, true)
		v.pending = nil
	}
}

// utf8SeqLen è la lunghezza della sequenza annunciata dal primo byte.
func utf8SeqLen(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 1
}

// decodeSeq decodifica una sequenza multibyte che inizia in b[0] (posizione
// assoluta pos). Restituisce i byte consumati (0 se la sequenza è troncata e
// atEOF è falso) e false se ha registrato un errore di decodifica, con le
// stesse regole del decoder UTF-8 di CPython ("maximal subpart").
func (v *utf8Validator) decodeSeq(b []byte, pos int64, atEOF bool) (int, bool) {
	lead := b[0]
	n := utf8SeqLen(lead)
	if n == 1 {
		v.setDecodeErr(lead, pos, 1, "invalid start byte")
		return 0, false
	}
	lo, hi := byte(0x80), byte(0xBF)
	switch lead {
	case 0xE0:
		lo = 0xA0
	case 0xED:
		hi = 0x9F
	case 0xF0:
		lo = 0x90
	case 0xF4:
		hi = 0x8F
	}
	for k := 1; k < n; k++ {
		if k >= len(b) {
			if atEOF {
				v.setDecodeErr(lead, pos, k, "unexpected end of data")
				return 0, false
			}
			return 0, true
		}
		c := b[k]
		if k > 1 {
			lo, hi = 0x80, 0xBF
		}
		if c < lo || c > hi {
			v.setDecodeErr(lead, pos, k, "invalid continuation byte")
			return 0, false
		}
	}
	r, _ := utf8.DecodeRune(b[:n])
	v.addRune(r)
	return n, true
}

// setDecodeErr compone str(UnicodeDecodeError).
func (v *utf8Validator) setDecodeErr(lead byte, start int64, length int, reason string) {
	if length == 1 {
		v.decodeErr = fmt.Sprintf("'utf-8' codec can't decode byte 0x%02x in position %d: %s", lead, start, reason)
	} else {
		v.decodeErr = fmt.Sprintf("'utf-8' codec can't decode bytes in position %d-%d: %s", start, start+int64(length)-1, reason)
	}
}
