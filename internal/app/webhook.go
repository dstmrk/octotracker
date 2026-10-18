package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/logging"
)

// updaterLogger replica il logger "telegram.ext.Updater" del webhook server
// di PTB (telegram/ext/_utils/webhookhandler.py).
var updaterLogger = logging.Logger("telegram.ext.Updater")

// SecretTokenHeader è l'header con cui Telegram invia il secret_token del
// webhook.
const SecretTokenHeader = "X-Telegram-Bot-Api-Secret-Token"

// MaxWebhookBodySize è il limite del corpo di una richiesta al webhook
// (max_body_size di default di Tornado, 100 MB).
const MaxWebhookBodySize = 100 * 1024 * 1024

// webhookContentType è l'header impostato da TelegramHandler.set_default_headers.
const webhookContentType = `application/json; charset="utf-8"`

// tornadoHTMLContentType è il Content-Type di default di Tornado (clear()).
const tornadoHTMLContentType = "text/html; charset=UTF-8"

// AllowedUpdates è Update.ALL_TYPES di python-telegram-bot 22.6, passato a
// setWebhook come allowed_updates.
var AllowedUpdates = []string{
	"message", "edited_message", "channel_post", "edited_channel_post",
	"inline_query", "chosen_inline_result", "callback_query", "shipping_query",
	"pre_checkout_query", "poll", "poll_answer", "my_chat_member", "chat_member",
	"chat_join_request", "chat_boost", "removed_chat_boost", "message_reaction",
	"message_reaction_count", "business_connection", "business_message",
	"edited_business_message", "deleted_business_messages", "purchased_paid_media",
}

// WebhookHandler è il webhook server di PTB (WebhookAppClass +
// TelegramHandler) davanti all'handler del gotgbot Updater. Replica:
//
//   - route "/<url_path>/?" (con o senza "/" finale); altri percorsi →
//     pagina 404 di Tornado (405 per i metodi sconosciuti);
//   - solo POST (altri metodi → 405);
//   - Content-Type esattamente "application/json", altrimenti 403;
//   - secret token assente o vuoto → 403 "Request did not include the secret
//     token"; diverso → 403 "Request had the wrong secret token";
//   - corpo non UTF-8 o JSON non valido → 500; JSON che non è un update
//     (Update.de_json fallisce) → log CRITICAL e 400 "Update could not be
//     processed";
//   - altrimenti l'update viene accodato e la risposta è subito 200 (corpo
//     vuoto, Content-Type application/json; charset="utf-8").
//
// Come in PTB non viene scritto alcun log di accesso.
type WebhookHandler struct {
	path   string
	secret string
	next   http.Handler
}

// NewWebhookHandler crea il webhook per url_path (il token del bot) e il
// secret token; next riceve gli update validi (con il percorso
// normalizzato a "/<url_path>"), tipicamente Updater.GetHandlerFunc("/").
func NewWebhookHandler(urlPath, secretToken string, next http.Handler) *WebhookHandler {
	if !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	return &WebhookHandler{path: urlPath, secret: secretToken, next: next}
}

// ServeHTTP implementa http.Handler.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqPath := rawRequestPath(r)
	if reqPath != h.path && reqPath != h.path+"/" {
		// Nessuna route: ErrorHandler di Tornado (404, o 405 per un metodo
		// che Tornado non supporta)
		code := http.StatusNotFound
		if !isTornadoMethod(r.Method) {
			code = http.StatusMethodNotAllowed
		}
		writeTornadoError(w, code, "", tornadoHTMLContentType)
		return
	}

	if r.Method != http.MethodPost {
		// TelegramHandler.SUPPORTED_METHODS = ("POST",)
		writeTornadoError(w, http.StatusMethodNotAllowed, "", webhookContentType)
		return
	}

	// _validate_post: solo richieste con content type JSON
	if strings.Join(r.Header.Values("Content-Type"), ",") != "application/json" {
		writeTornadoError(w, http.StatusForbidden, "", webhookContentType)
		return
	}
	// verifica del secret token
	token := strings.Join(r.Header.Values(SecretTokenHeader), ",")
	if token == "" {
		writeTornadoError(w, http.StatusForbidden, "Request did not include the secret token", webhookContentType)
		return
	}
	if token != h.secret {
		writeTornadoError(w, http.StatusForbidden, "Request had the wrong secret token", webhookContentType)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxWebhookBodySize))
	if err != nil {
		writeTornadoError(w, http.StatusBadRequest, "", webhookContentType)
		return
	}

	// json_string = self.request.body.decode(); data = json.loads(json_string)
	jsonString, err := pycompat.DecodeUTF8(body)
	if err != nil {
		writeTornadoError(w, http.StatusInternalServerError, "", webhookContentType)
		return
	}
	data, err := pycompat.Loads(jsonString)
	if err != nil {
		writeTornadoError(w, http.StatusInternalServerError, "", webhookContentType)
		return
	}

	// update = Update.de_json(data, self.bot)
	if !isUpdateData(data, body) {
		logging.Critical(updaterLogger, fmt.Sprintf(
			"Something went wrong processing the data received from Telegram. "+
				"Received data was *not* processed! Received data was: %s", pycompat.Repr(data)))
		writeTornadoError(w, http.StatusBadRequest, "Update could not be processed", webhookContentType)
		return
	}

	// await self.update_queue.put(update)
	fwd := r.Clone(r.Context())
	fwd.URL.Path = h.path
	fwd.URL.RawPath = ""
	fwd.Body = io.NopCloser(bytes.NewReader(body))
	w.Header().Set("Content-Type", webhookContentType)
	h.next.ServeHTTP(w, fwd)
}

// isUpdateData indica se Update.de_json accetterebbe i dati: deve essere un
// oggetto JSON con la chiave update_id, decodificabile come gotgbot.Update.
func isUpdateData(data any, body []byte) bool {
	obj, ok := data.(pycompat.Object)
	if !ok {
		return false
	}
	if _, ok := obj.Get("update_id"); !ok {
		return false
	}
	var upd gotgbot.Update
	return json.Unmarshal(body, &upd) == nil
}

// rawRequestPath restituisce request.path di Tornado: la parte dell'URI
// della richiesta prima di "?", senza decodifica.
func rawRequestPath(r *http.Request) string {
	uri := r.RequestURI
	if uri == "" {
		uri = r.URL.RequestURI()
	}
	p, _, _ := strings.Cut(uri, "?")
	return p
}

// isTornadoMethod indica se il metodo è in RequestHandler.SUPPORTED_METHODS.
func isTornadoMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodDelete,
		http.MethodPatch, http.MethodPut, http.MethodOptions:
		return true
	}
	return false
}

// writeTornadoError scrive la pagina d'errore di RequestHandler.write_error
// ("<html><title>403: Forbidden</title>..."), con il reason indicato (vuoto
// = frase standard dello status) e il Content-Type degli header di default
// dell'handler.
func writeTornadoError(w http.ResponseWriter, code int, reason, contentType string) {
	if reason == "" {
		reason = http.StatusText(code)
	}
	reason = html.EscapeString(reason)
	body := fmt.Sprintf("<html><title>%d: %s</title><body>%d: %s</body></html>", code, reason, code, reason)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

// SequentialDispatcher è l'UpdateDispatcher passato al gotgbot Updater: come
// la update_queue di PTB riceve gli update senza bloccare il webhook (coda
// illimitata) e li fa elaborare uno alla volta, nell'ordine di arrivo, dal
// Dispatcher (creato con MaxRoutines 1). Allo stop elabora gli update
// ancora in coda, come Application.stop().
type SequentialDispatcher struct {
	inner *ext.Dispatcher

	mu      sync.Mutex
	running int
	closed  bool
	done    chan struct{}
}

var _ ext.UpdateDispatcher = (*SequentialDispatcher)(nil)

// NewSequentialDispatcher avvolge d.
func NewSequentialDispatcher(d *ext.Dispatcher) *SequentialDispatcher {
	return &SequentialDispatcher{inner: d, done: make(chan struct{})}
}

// Start implementa ext.UpdateDispatcher: termina quando updates viene
// chiuso e tutti gli update in coda sono stati consegnati al Dispatcher.
func (s *SequentialDispatcher) Start(b *gotgbot.Bot, updates <-chan json.RawMessage) {
	s.mu.Lock()
	s.running++
	s.mu.Unlock()

	out := make(chan json.RawMessage)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		s.inner.Start(b, out)
	}()
	pumpUpdates(updates, out)
	<-finished

	s.mu.Lock()
	s.running--
	if s.running == 0 && !s.closed {
		s.closed = true
		close(s.done)
	}
	s.mu.Unlock()
}

// Stop implementa ext.UpdateDispatcher: attende che Start abbia consegnato
// tutti gli update e che il Dispatcher li abbia elaborati.
func (s *SequentialDispatcher) Stop() {
	<-s.done
	s.inner.Stop()
}

// pumpUpdates inoltra gli update da in a out tramite una coda illimitata:
// in non resta mai bloccato in attesa del Dispatcher. Quando in viene
// chiuso consegna quelli rimasti in coda e chiude out.
func pumpUpdates(in <-chan json.RawMessage, out chan<- json.RawMessage) {
	var queue []json.RawMessage
	for in != nil || len(queue) > 0 {
		var send chan<- json.RawMessage
		var next json.RawMessage
		if len(queue) > 0 {
			send = out
			next = queue[0]
		}
		select {
		case upd, ok := <-in:
			if !ok {
				in = nil
				continue
			}
			queue = append(queue, upd)
		case send <- next:
			queue[0] = nil
			queue = queue[1:]
		}
	}
	close(out)
}
