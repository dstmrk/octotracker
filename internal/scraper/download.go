package scraper

// Download dei file XML ARERA. Il corpo della risposta non viene tenuto in
// memoria: passa, attraverso un controllo UTF-8, direttamente al parser in
// streaming e poi viene letto fino in fondo, così un errore di rete o un
// byte non UTF-8 in qualunque punto del file rende fallito il download (e si
// prova il giorno precedente), anche se il parser si era già fermato.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// headBytes è la lunghezza dell'inizio del file riportato nei log di debug
// quando l'XML è malformato.
const headBytes = 500

var errInvalidUTF8 = errors.New("il file non è UTF-8 valido")

// httpStatusError è una risposta HTTP non 2xx.
type httpStatusError struct {
	code int
}

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

// downloadXML scarica url e passa il corpo a consume. Restituisce i primi
// headBytes byte del file, l'errore del download (*httpStatusError per le
// risposte non 2xx) oppure l'errore di ctx se viene annullato.
func (c *Client) downloadXML(ctx context.Context, url string, consume func(io.Reader)) (string, error) {
	logger.Debug("Downloading " + url)

	// Il timeout vale per singola lettura, non per l'intero download: il
	// watchdog annulla la richiesta solo se per Timeout non arrivano dati.
	timeout := c.timeout()
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	watchdog := time.AfterFunc(timeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer watchdog.Stop()
	withTimeout := func(err error) error {
		if timedOut.Load() {
			return fmt.Errorf("nessun dato ricevuto per %v: %w", timeout, err)
		}
		return err
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	for _, h := range HTTPHeaders {
		req.Header.Set(h[0], h[1])
	}
	// Niente compressione: senza questo header net/http chiederebbe gzip.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		err = withTimeout(err)
		logger.Warn(fmt.Sprintf("⚠️  Errore rete durante download %s: %v", url, err))
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.Warn(fmt.Sprintf("⚠️  HTTP %d durante download %s", resp.StatusCode, url))
		return "", &httpStatusError{code: resp.StatusCode}
	}

	body := &bodyReader{r: resp.Body, onData: func() { watchdog.Reset(timeout) }}
	consume(body)
	_, _ = io.Copy(io.Discard, body)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if body.err != nil {
		return "", withTimeout(body.err)
	}
	logger.Debug(fmt.Sprintf("Downloaded %d bytes", body.n))
	return string(body.head), nil
}

// bodyReader passa il corpo della risposta al parser: rimanda il timeout a
// ogni lettura, conta i byte, conserva i primi headBytes e verifica che il
// contenuto sia UTF-8 valido. err è il primo errore di lettura (EOF escluso)
// o errInvalidUTF8.
type bodyReader struct {
	r       io.Reader
	onData  func()
	n       int64
	head    []byte
	pending []byte // sequenza UTF-8 incompleta a fine buffer
	err     error
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if n > 0 {
		b.onData()
		b.n += int64(n)
		if room := headBytes - len(b.head); room > 0 {
			b.head = append(b.head, p[:min(n, room)]...)
		}
	}
	eof := errors.Is(err, io.EOF)
	if b.err == nil {
		b.checkUTF8(p[:n], eof)
	}
	if err != nil && !eof && b.err == nil {
		b.err = err
	}
	return n, err
}

// checkUTF8 verifica un blocco di dati; una sequenza multibyte spezzata tra
// due letture viene completata con il blocco successivo.
func (b *bodyReader) checkUTF8(data []byte, eof bool) {
	if len(b.pending) > 0 {
		data = append(b.pending, data...)
	}
	cut := len(data)
	for i := len(data) - 1; i >= 0 && i > len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				cut = i
			}
			break
		}
	}
	if !utf8.Valid(data[:cut]) || (eof && cut < len(data)) {
		b.err = errInvalidUTF8
		return
	}
	b.pending = append([]byte(nil), data[cut:]...)
}
