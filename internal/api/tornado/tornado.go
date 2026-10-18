// Package tornado replica la parte di tornado.web.RequestHandler (Tornado
// 6.5) che determina le risposte HTTP degli handler Python di OctoTracker
// (api/handlers.py e health_handler.py), così che i client vedano le
// stesse risposte:
//
//   - header iniziali di clear() (Content-Type "text/html; charset=UTF-8")
//     più set_default_headers() dell'handler;
//   - status, buffer di output e finish(): ETag automatico (SHA-1 del corpo)
//     per le risposte 200 a GET/HEAD con 304 se If-None-Match corrisponde,
//     header di rappresentazione rimossi per 204/304, Content-Length;
//   - send_error()/write_error(): la pagina HTML "<html><title>405: Method
//     Not Allowed</title>..." (con gli header di default dell'handler);
//   - _execute(): metodi non supportati o non implementati → 405, corpo
//     application/x-www-form-urlencoded analizzato e unito agli argomenti
//     (400 se non valido), eccezioni non gestite → 500;
//   - get_argument(): ultimo valore, decodifica UTF-8 stretta (HTTPError
//     400 "Invalid unicode in ..."), caratteri di controllo → spazio, strip;
//   - il log di accesso "tornado.access" ("200 GET /health (10.0.0.1) 1.23ms").
//
// Non sono replicati l'header "Server: TornadoServer/x.y" e il parsing dei
// corpi multipart/form-data.
package tornado

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/logging"
)

var (
	accessLog = logging.Logger("tornado.access")
	appLog    = logging.Logger("tornado.application")
	genLog    = logging.Logger("tornado.general")
)

// now è l'orologio usato per request_time() (sostituibile nei test).
var now = time.Now

// MaxArguments è il limite di campi per query string e corpo urlencoded
// (ParseURLEncodedConfig.max_arguments).
const MaxArguments = 1000

// SupportedMethods è RequestHandler.SUPPORTED_METHODS.
var SupportedMethods = []string{"GET", "HEAD", "POST", "DELETE", "PATCH", "PUT", "OPTIONS"}

// controlChars è RequestHandler._remove_control_chars_regex.
var controlChars = regexp.MustCompile(`[\x00-\x08\x0e-\x1f]`)

// HTTPError replica tornado.web.HTTPError.
type HTTPError struct {
	StatusCode int
	LogMessage string
	Reason     string
}

// Error restituisce str(e), es. "HTTP 400: Bad Request (Invalid unicode in
// servizio: b'\xff')".
func (e *HTTPError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = responses(e.StatusCode)
	}
	msg := fmt.Sprintf("HTTP %d: %s", e.StatusCode, reason)
	if e.LogMessage != "" {
		msg += " (" + e.LogMessage + ")"
	}
	return msg
}

// responses replica httputil.responses.get(code, "Unknown").
func responses(code int) string {
	if s := http.StatusText(code); s != "" {
		return s
	}
	return "Unknown"
}

// Method è l'implementazione di un metodo HTTP (get, options, ...). Un
// errore restituito equivale a un'eccezione non gestita: *HTTPError produce
// la pagina d'errore con il suo status, ogni altro errore un 500.
type Method func(h *RequestHandler) error

// Methods associa i metodi HTTP implementati dall'handler.
type Methods map[string]Method

// RequestHandler è lo stato di una richiesta (l'istanza di RequestHandler).
type RequestHandler struct {
	Request  *http.Request
	w        http.ResponseWriter
	defaults func(*RequestHandler)

	status   int
	reason   string
	headers  http.Header
	buf      bytes.Buffer
	finished bool
	start    time.Time

	arguments []pycompat.BytesQueryField
}

// Clear replica clear(): azzera header, corpo e status (200) e riapplica
// set_default_headers().
func (h *RequestHandler) Clear() {
	h.headers = http.Header{}
	h.headers.Set("Content-Type", "text/html; charset=UTF-8")
	if h.defaults != nil {
		h.defaults(h)
	}
	h.buf.Reset()
	h.status = http.StatusOK
	h.reason = responses(http.StatusOK)
}

// SetStatus replica set_status(code).
func (h *RequestHandler) SetStatus(code int) {
	h.status = code
	h.reason = responses(code)
}

// Status restituisce get_status().
func (h *RequestHandler) Status() int { return h.status }

// SetHeader replica set_header(name, value).
func (h *RequestHandler) SetHeader(name, value string) { h.headers.Set(name, value) }

// ClearHeader replica clear_header(name).
func (h *RequestHandler) ClearHeader(name string) { h.headers.Del(name) }

// Header restituisce gli header della risposta (sola lettura nei test).
func (h *RequestHandler) Header() http.Header { return h.headers }

// Write replica write(chunk) per una str (codificata UTF-8).
func (h *RequestHandler) Write(s string) { h.buf.WriteString(s) }

// RemoteIP replica request.remote_ip (indirizzo del peer, senza porta).
func RemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RequestSummary replica _request_summary(): "GET /path?q (1.2.3.4)", con
// l'URI decodificato come latin-1 come fa Tornado.
func RequestSummary(r *http.Request) string {
	uri := r.RequestURI
	if uri == "" {
		uri = r.URL.RequestURI()
	}
	return fmt.Sprintf("%s %s (%s)", r.Method, pycompat.Latin1(uri), RemoteIP(r))
}

// LogRequest replica Application.log_request(): logga su "tornado.access"
// a livello INFO (status < 400), WARNING (< 500) o ERROR.
func LogRequest(status int, r *http.Request, start time.Time) {
	level := slog.LevelInfo
	switch {
	case status >= 500:
		level = slog.LevelError
	case status >= 400:
		level = slog.LevelWarn
	}
	ms := float64(now().Sub(start).Nanoseconds()) / 1e6
	accessLog.Log(r.Context(), level, fmt.Sprintf("%d %s %.2fms", status, RequestSummary(r), ms))
}

// computeEtag replica compute_etag(): SHA-1 esadecimale del corpo tra virgolette.
func (h *RequestHandler) computeEtag() string {
	sum := sha1.Sum(h.buf.Bytes())
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

var etagRe = regexp.MustCompile(`\*|(?:W/)?"[^"]*"`)

// checkEtagHeader replica check_etag_header(): confronto debole con i tag
// di If-None-Match ("*" corrisponde sempre).
func (h *RequestHandler) checkEtagHeader() bool {
	computed := h.headers.Get("Etag")
	inm := strings.Join(h.Request.Header.Values("If-None-Match"), ",")
	etags := etagRe.FindAllString(pycompat.Latin1(inm), -1)
	if computed == "" || len(etags) == 0 {
		return false
	}
	if etags[0] == "*" {
		return true
	}
	val := func(x string) string { return strings.TrimPrefix(x, "W/") }
	for _, e := range etags {
		if val(e) == val(computed) {
			return true
		}
	}
	return false
}

// Finish replica finish(): ETag/304 automatici, header di rappresentazione
// rimossi per 1xx/204/304, Content-Length, invio della risposta e log di
// accesso.
func (h *RequestHandler) Finish() {
	if h.finished {
		panic("finish() called twice")
	}
	method := h.Request.Method
	if h.status == http.StatusOK && (method == http.MethodGet || method == http.MethodHead) && h.headers.Get("Etag") == "" {
		h.headers.Set("Etag", h.computeEtag())
		if h.checkEtagHeader() {
			h.buf.Reset()
			h.SetStatus(http.StatusNotModified)
		}
	}
	if h.status == http.StatusNoContent || h.status == http.StatusNotModified || (h.status >= 100 && h.status < 200) {
		for _, name := range []string{"Content-Encoding", "Content-Language", "Content-Type"} {
			h.headers.Del(name)
		}
	} else if h.headers.Get("Content-Length") == "" {
		h.headers.Set("Content-Length", strconv.Itoa(h.buf.Len()))
	}

	out := h.w.Header()
	for name, values := range h.headers {
		out[name] = values
	}
	h.w.WriteHeader(h.status)
	if h.buf.Len() > 0 && method != http.MethodHead {
		_, _ = h.w.Write(h.buf.Bytes())
	}
	h.finished = true
	LogRequest(h.status, h.Request, h.start)
}

// SendError replica send_error(code): scarta l'output, riapplica gli header
// di default e scrive la pagina d'errore HTML di write_error().
func (h *RequestHandler) SendError(code int) {
	h.Clear()
	h.SetStatus(code)
	if code != http.StatusNotModified {
		reason := html.EscapeString(h.reason)
		h.Write(fmt.Sprintf("<html><title>%d: %s</title><body>%d: %s</body></html>", code, reason, code, reason))
	}
	if !h.finished {
		h.Finish()
	}
}

// handleRequestException replica _handle_request_exception + log_exception.
func (h *RequestHandler) handleRequestException(err error) {
	if httpErr, ok := err.(*HTTPError); ok {
		if httpErr.LogMessage != "" {
			genLog.Warn(fmt.Sprintf("%d %s: %s", httpErr.StatusCode, RequestSummary(h.Request), httpErr.LogMessage))
		}
		if h.finished {
			return
		}
		h.SendError(httpErr.StatusCode)
		return
	}
	// In Python segue il traceback; qui si accoda il messaggio dell'errore.
	appLog.Error(fmt.Sprintf("Uncaught exception %s\n%s\n%s", RequestSummary(h.Request), requestRepr(h.Request), err.Error()))
	if !h.finished {
		h.SendError(http.StatusInternalServerError)
	}
}

// requestRepr approssima repr(HTTPServerRequest).
func requestRepr(r *http.Request) string {
	return fmt.Sprintf("HTTPServerRequest(protocol='http', host=%s, method=%s, uri=%s, version=%s, remote_ip=%s)",
		pycompat.StrRepr(r.Host), pycompat.StrRepr(r.Method), pycompat.StrRepr(pycompat.Latin1(r.RequestURI)),
		pycompat.StrRepr(r.Proto), pycompat.StrRepr(RemoteIP(r)))
}

func isSupported(method string) bool {
	for _, m := range SupportedMethods {
		if m == method {
			return true
		}
	}
	return false
}

// parseBody replica HTTPServerRequest._parse_body() per i corpi
// application/x-www-form-urlencoded, unendo gli argomenti del corpo a
// quelli della query (arguments.setdefault(k, []).extend(v)).
func (h *RequestHandler) parseBody() error {
	contentType := strings.Join(h.Request.Header.Values("Content-Type"), ",")
	if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		return nil
	}
	if _, ok := h.Request.Header["Content-Encoding"]; ok {
		enc := strings.Join(h.Request.Header.Values("Content-Encoding"), ",")
		return &HTTPError{StatusCode: 400, LogMessage: "Invalid body: Unsupported Content-Encoding: " + enc}
	}
	var body []byte
	if h.Request.Body != nil {
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(h.Request.Body); err != nil {
			return err
		}
		body = buf.Bytes()
	}
	fields, err := pycompat.ParseQSBytes(string(body), true, MaxArguments)
	if err != nil {
		return &HTTPError{StatusCode: 400, LogMessage: "Invalid body: Invalid x-www-form-urlencoded body: " + err.Error()}
	}
	for _, f := range fields {
		h.addArguments(f.Name, f.Values)
	}
	return nil
}

func (h *RequestHandler) addArguments(name string, values [][]byte) {
	for i := range h.arguments {
		if h.arguments[i].Name == name {
			h.arguments[i].Values = append(h.arguments[i].Values, values...)
			return
		}
	}
	h.arguments = append(h.arguments, pycompat.BytesQueryField{Name: name, Values: values})
}

// GetArguments replica _get_arguments(name, request.arguments, strip): ogni
// valore viene decodificato in UTF-8 (HTTPError 400 se non valido), i
// caratteri di controllo diventano spazi e, se strip, vengono tolti gli
// spazi ai lati.
func (h *RequestHandler) GetArguments(name string, strip bool) ([]string, error) {
	var out []string
	for _, f := range h.arguments {
		if f.Name != name {
			continue
		}
		for _, raw := range f.Values {
			s, err := pycompat.DecodeUTF8(raw)
			if err != nil {
				shown := raw
				if len(shown) > 40 {
					shown = shown[:40]
				}
				return nil, &HTTPError{StatusCode: 400,
					LogMessage: fmt.Sprintf("Invalid unicode in %s: %s", name, pycompat.BytesRepr(shown))}
			}
			s = controlChars.ReplaceAllString(s, " ")
			if strip {
				s = pycompat.Strip(s)
			}
			out = append(out, s)
		}
	}
	return out, nil
}

// GetArgument replica get_argument(name, default): l'ultimo valore del
// parametro (con strip), oppure def (nil = None) se assente.
func (h *RequestHandler) GetArgument(name string, def *string) (*string, error) {
	values, err := h.GetArguments(name, true)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return def, nil
	}
	v := values[len(values)-1]
	return &v, nil
}

// Serve esegue una richiesta come Application + RequestHandler._execute:
// analisi della query (oltre MaxArguments campi Tornado chiude la
// connessione con un 400 senza corpo), clear() con setDefaultHeaders,
// controllo del metodo, analisi del corpo, chiamata del metodo e finish()
// automatico. setDefaultHeaders può essere nil.
func Serve(w http.ResponseWriter, r *http.Request, setDefaultHeaders func(*RequestHandler), methods Methods) {
	start := now()
	query, err := pycompat.ParseQSBytes(r.URL.RawQuery, true, MaxArguments)
	if err != nil {
		genLog.Info(fmt.Sprintf("Malformed HTTP message from %s: Invalid query string: %s", RemoteIP(r), err.Error()))
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	h := &RequestHandler{Request: r, w: w, defaults: setDefaultHeaders, start: start, arguments: query}
	h.Clear()
	h.execute(methods)
}

func (h *RequestHandler) execute(methods Methods) {
	defer func() {
		if rec := recover(); rec != nil {
			err, ok := rec.(error)
			if !ok {
				err = fmt.Errorf("%v", rec)
			}
			h.handleRequestException(err)
		}
	}()
	err := func() error {
		if !isSupported(h.Request.Method) {
			return &HTTPError{StatusCode: http.StatusMethodNotAllowed}
		}
		if err := h.parseBody(); err != nil {
			return err
		}
		method := methods[h.Request.Method]
		if method == nil {
			return &HTTPError{StatusCode: http.StatusMethodNotAllowed} // _unimplemented_method
		}
		return method(h)
	}()
	if err != nil {
		h.handleRequestException(err)
		return
	}
	if !h.finished {
		h.Finish()
	}
}

// NotFoundHandler replica il gestore di default di Tornado per i percorsi
// senza route (ErrorHandler con status 404): 405 per i metodi non
// supportati, altrimenti la pagina HTML "404: Not Found".
func NotFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notFound := func(*RequestHandler) error { return &HTTPError{StatusCode: http.StatusNotFound} }
		methods := Methods{}
		for _, m := range SupportedMethods {
			methods[m] = notFound
		}
		Serve(w, r, nil, methods)
	})
}
