package app

import (
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/big"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dstmrk/octotracker/internal/api"
	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/api/tornado"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/health"
)

// AppPrefix è il prefisso della route della Mini App ("/app/(.*)").
const AppPrefix = "/app/"

// DefaultFilename è default_filename di StaticFileHandler.
const DefaultFilename = "index.html"

// StaticCacheMaxAge è StaticFileHandler.CACHE_MAX_AGE (10 anni), usato per
// le richieste con il parametro "v" (cache busting, es. style.css?v=3).
const StaticCacheMaxAge = 86400 * 365 * 10

// staticNow è l'orologio per l'header Expires (sostituibile nei test).
var staticNow = time.Now

// NewHTTPHandler crea l'applicazione HTTP di run_health_server, con le
// stesse route Tornado (confrontate con il percorso grezzo della richiesta,
// senza normalizzazioni né redirect):
//
//   - /health              → health.Handler
//   - /api/rates/history   → api.RatesHistoryHandler
//   - /api/rates/current   → api.RatesCurrentHandler
//   - /api/user/rates      → api.UserRatesHandler
//   - /app/(.*)            → file statici della Mini App (index.html di default)
//
// Ogni altro percorso (incluso "/app" senza barra finale) riceve la pagina
// 404 di Tornado. modTime è l'header Last-Modified dei file statici (i file
// incorporati non hanno una data di modifica: si usa l'avvio del server).
func NewHTTPHandler(store *database.Store, tasks health.TaskStatus, botToken string, webapp fs.FS, modTime time.Time) http.Handler {
	a := api.New(store, botToken)
	routes := map[string]http.Handler{
		health.Path:          health.NewHandler(store, tasks),
		api.PathRatesHistory: a.RatesHistoryHandler(),
		api.PathRatesCurrent: a.RatesCurrentHandler(),
		api.PathUserRates:    a.UserRatesHandler(),
	}
	static := &StaticHandler{FS: webapp, ModTime: modTime}
	notFound := tornado.NotFoundHandler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := rawRequestPath(r)
		if h, ok := routes[reqPath]; ok {
			h.ServeHTTP(w, r)
			return
		}
		if rest, ok := strings.CutPrefix(reqPath, AppPrefix); ok {
			static.Serve(w, r, rest)
			return
		}
		notFound.ServeHTTP(w, r)
	})
}

// StaticHandler replica tornado.web.StaticFileHandler (Tornado 6.5) con
// default_filename "index.html" su un fs.FS:
//
//   - "/app/" e "/app/index.html" restituiscono la pagina; una directory
//     senza "/" finale viene rediretta (301) alla stessa con "/";
//   - percorsi fuori dalla radice → 403, file inesistenti → 404;
//   - header Accept-Ranges, Etag (SHA-512 del contenuto), Last-Modified,
//     Content-Type da mimetypes di Python (senza charset) e, se la query ha
//     il parametro "v", Expires e Cache-Control: max-age di 10 anni;
//   - 304 con If-None-Match / If-Modified-Since, richieste Range (206/416);
//   - solo GET e HEAD (altri metodi → 405) e log di accesso "tornado.access".
type StaticHandler struct {
	FS      fs.FS
	ModTime time.Time
}

// Serve gestisce la richiesta per il percorso catturato rawArg (la parte
// dopo "/app/", non ancora decodificata).
func (s *StaticHandler) Serve(w http.ResponseWriter, r *http.Request, rawArg string) {
	// _execute decodifica i path_args prima di chiamare il metodo: un
	// percorso non UTF-8 dà 400 anche con un metodo non implementato.
	get := func(h *tornado.RequestHandler) error {
		pathArg, err := decodePathArg(rawArg)
		if err != nil {
			return err
		}
		return s.get(h, pathArg, h.Request.Method != http.MethodHead)
	}
	unimplemented := func(*tornado.RequestHandler) error {
		if _, err := decodePathArg(rawArg); err != nil {
			return err
		}
		return &tornado.HTTPError{StatusCode: http.StatusMethodNotAllowed}
	}
	methods := tornado.Methods{}
	for _, m := range tornado.SupportedMethods {
		methods[m] = unimplemented
	}
	methods[http.MethodGet] = get
	methods[http.MethodHead] = get
	tornado.Serve(w, r, nil, methods)
}

// decodePathArg replica decode_argument(url_unescape(arg, encoding=None,
// plus=False)): decodifica %XX e poi UTF-8 stretto (HTTPError 400).
func decodePathArg(raw string) (string, error) {
	b := unquoteToBytes(raw)
	s, err := pycompat.DecodeUTF8(b)
	if err != nil {
		shown := b
		if len(shown) > 40 {
			shown = shown[:40]
		}
		return "", &tornado.HTTPError{StatusCode: http.StatusBadRequest,
			LogMessage: fmt.Sprintf("Invalid unicode in url: %s", pycompat.BytesRepr(shown))}
	}
	return s, nil
}

// unquoteToBytes replica urllib.parse.unquote_to_bytes: le sequenze %XX
// valide diventano byte, le altre restano invariate.
func unquoteToBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if hi, ok1 := unhex(s[i+1]); ok1 {
				if lo, ok2 := unhex(s[i+2]); ok2 {
					out = append(out, hi<<4|lo)
					i += 2
					continue
				}
			}
		}
		out = append(out, s[i])
	}
	return out
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// errRedirected segnala che validate_absolute_path ha già impostato il
// redirect (return None).
var errRedirected = errors.New("redirect")

// resolve replica get_absolute_path + validate_absolute_path: restituisce
// il nome del file nel FS oppure l'HTTPError (403/404) o errRedirected.
func (s *StaticHandler) resolve(h *tornado.RequestHandler, pathArg string) (string, error) {
	// os.path.abspath(os.path.join(root, path)) deve restare dentro root
	if strings.HasPrefix(pathArg, "/") {
		return "", &tornado.HTTPError{StatusCode: http.StatusForbidden,
			LogMessage: fmt.Sprintf("%s is not in root static directory", pathArg)}
	}
	name := path.Clean(pathArg)
	if name == ".." || strings.HasPrefix(name, "../") {
		return "", &tornado.HTTPError{StatusCode: http.StatusForbidden,
			LogMessage: fmt.Sprintf("%s is not in root static directory", pathArg)}
	}
	// _resolve_symlink_target: os.path.realpath fa lstat sul percorso, che
	// con un byte NUL solleva ValueError (500 "Uncaught exception")
	if strings.ContainsRune(pathArg, 0) {
		return "", pycompat.NewError("ValueError", "lstat: embedded null character in path")
	}

	if info, err := fs.Stat(s.FS, name); err == nil && info.IsDir() {
		// serve guardare request.path: path può essere vuoto
		reqPath := rawRequestPath(h.Request)
		if !strings.HasSuffix(reqPath, "/") {
			if strings.HasPrefix(reqPath, "//") {
				// un redirect con due barre iniziali sarebbe un URL "protocol-relative"
				return "", &tornado.HTTPError{StatusCode: http.StatusForbidden,
					LogMessage: "cannot redirect path with two initial slashes"}
			}
			h.SetStatus(http.StatusMovedPermanently)
			h.SetHeader("Location", reqPath+"/")
			return "", errRedirected
		}
		name = path.Join(name, DefaultFilename)
	}

	info, err := fs.Stat(s.FS, name)
	if err != nil {
		return "", &tornado.HTTPError{StatusCode: http.StatusNotFound}
	}
	if !info.Mode().IsRegular() {
		return "", &tornado.HTTPError{StatusCode: http.StatusForbidden,
			LogMessage: fmt.Sprintf("%s is not a file", pathArg)}
	}
	return name, nil
}

// get replica StaticFileHandler.get(path, include_body).
func (s *StaticHandler) get(h *tornado.RequestHandler, pathArg string, includeBody bool) error {
	name, err := s.resolve(h, pathArg)
	if errors.Is(err, errRedirected) {
		return nil
	}
	if err != nil {
		return err
	}
	content, err := fs.ReadFile(s.FS, name)
	if err != nil {
		return err
	}

	// set_headers()
	h.SetHeader("Accept-Ranges", "bytes")
	sum := sha512.Sum512(content)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	h.SetHeader("Etag", etag)
	modified := s.ModTime.UTC().Truncate(time.Second)
	if !s.ModTime.IsZero() {
		h.SetHeader("Last-Modified", modified.Format(http.TimeFormat))
	}
	h.SetHeader("Content-Type", staticContentType(name))
	if s.hasVersionArgument(h) {
		expires := staticNow().UTC().Add(StaticCacheMaxAge * time.Second)
		h.SetHeader("Expires", expires.Format(http.TimeFormat))
		h.SetHeader("Cache-Control", "max-age="+strconv.Itoa(StaticCacheMaxAge))
	}

	if shouldReturn304(h.Request, etag, modified, !s.ModTime.IsZero()) {
		h.SetStatus(http.StatusNotModified)
		return nil
	}

	size := len(content)
	start, end := 0, size
	// headers.get("Range"): più header vengono uniti con ","
	if rangeHeader := pycompat.Latin1(strings.Join(h.Request.Header.Values("Range"), ",")); rangeHeader != "" {
		// RFC 2616 14.16: un header Range non valido viene ignorato
		if rs, re, ok := parseRequestRange(rangeHeader); ok {
			if rs != nil && *rs < 0 {
				*rs += size
				if *rs < 0 {
					*rs = 0
				}
			}
			if (rs != nil && (*rs >= size || (re != nil && *rs >= *re))) || (re != nil && *re == 0) {
				h.SetStatus(http.StatusRequestedRangeNotSatisfiable)
				h.SetHeader("Content-Type", "text/plain")
				h.SetHeader("Content-Range", fmt.Sprintf("bytes */%d", size))
				return nil
			}
			if re != nil && *re > size {
				*re = size
			}
			if rs != nil {
				start = *rs
			}
			if re != nil {
				end = *re
			}
			// 206 solo se è richiesto meno dell'intero contenuto
			if size != end-start {
				h.SetStatus(http.StatusPartialContent)
				h.SetHeader("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end-1, size))
			}
		}
	}

	h.SetHeader("Content-Length", strconv.Itoa(end-start))
	if includeBody {
		h.Write(string(content[start:end]))
	}
	return nil
}

// hasVersionArgument replica "v" in self.request.arguments (get_cache_time).
func (s *StaticHandler) hasVersionArgument(h *tornado.RequestHandler) bool {
	values, err := h.GetArguments("v", false)
	return err != nil || len(values) > 0
}

var staticEtagRe = regexp.MustCompile(`\*|(?:W/)?"[^"]*"`)

// shouldReturn304 replica StaticFileHandler.should_return_304: If-None-Match
// ha la precedenza su If-Modified-Since.
func shouldReturn304(r *http.Request, etag string, modified time.Time, hasModified bool) bool {
	if inm := strings.Join(r.Header.Values("If-None-Match"), ","); inm != "" {
		etags := staticEtagRe.FindAllString(pycompat.Latin1(inm), -1)
		if len(etags) == 0 {
			return false
		}
		if etags[0] == "*" {
			return true
		}
		for _, e := range etags {
			if strings.TrimPrefix(e, "W/") == strings.TrimPrefix(etag, "W/") {
				return true
			}
		}
		return false
	}
	// headers.get("If-Modified-Since") is not None: anche un header vuoto
	// conta (e poi non è una data valida)
	values := r.Header.Values("If-Modified-Since")
	if values == nil || !hasModified {
		return false
	}
	ifSince, ok := parsedateToDatetime(pycompat.Latin1(strings.Join(values, ",")))
	if !ok {
		return false
	}
	return !ifSince.Before(modified)
}

// parseRequestRange replica httputil._parse_request_range: un solo range
// "bytes=start-end" (end inclusivo, restituito esclusivo); un suffisso
// "bytes=-N" diventa start=-N. ok=false se l'header non è valido.
func parseRequestRange(header string) (start, end *int, ok bool) {
	unit, value, _ := strings.Cut(header, "=")
	if pycompat.Strip(unit) != "bytes" {
		return nil, nil, false
	}
	startB, endB, _ := strings.Cut(pycompat.Strip(value), "-")
	start, okS := intOrNone(startB)
	end, okE := intOrNone(endB)
	if !okS || !okE {
		return nil, nil, false
	}
	if end != nil {
		if start == nil {
			if *end != 0 {
				s := -*end
				start, end = &s, nil
			}
		} else {
			e := *end + 1
			end = &e
		}
	}
	return start, end, true
}

// maxRangeBound limita i valori di un range: gli int di Python non hanno
// limiti, ma oltre questa soglia (molto più grande di qualunque file) i
// confronti con la dimensione del file danno lo stesso esito.
const maxRangeBound = math.MaxInt64 / 4

// intOrNone replica _int_or_none: stringa vuota → None, altrimenti int()
// di Python (spazi Unicode, segno, underscore, cifre Unicode).
func intOrNone(s string) (*int, bool) {
	s = pycompat.Strip(s)
	if s == "" {
		return nil, true
	}
	v, err := pycompat.ParseInt(s)
	if err != nil {
		return nil, false
	}
	var n int
	switch {
	case v.Cmp(big.NewInt(maxRangeBound)) > 0:
		n = maxRangeBound
	case v.Cmp(big.NewInt(-maxRangeBound)) < 0:
		n = -maxRangeBound
	default:
		n = int(v.Int64())
	}
	return &n, true
}

// staticContentTypes è la parte di mimetypes.guess_type di Python usata per
// i file statici (tipi di default, senza charset).
var staticContentTypes = map[string]string{
	".html":  "text/html",
	".htm":   "text/html",
	".css":   "text/css",
	".js":    "text/javascript",
	".mjs":   "text/javascript",
	".json":  "application/json",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".svg":   "image/svg+xml",
	".ico":   "image/vnd.microsoft.icon",
	".txt":   "text/plain",
	".webp":  "image/webp",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// staticContentType replica StaticFileHandler.get_content_type.
func staticContentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ext == ".gz" {
		return "application/gzip"
	}
	if ct, ok := staticContentTypes[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}
