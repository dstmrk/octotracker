package app

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

// SecretTokenHeader è l'header con cui Telegram invia il secret_token del
// webhook.
const SecretTokenHeader = "X-Telegram-Bot-Api-Secret-Token"

// MaxWebhookBodySize è il limite del corpo di una richiesta al webhook.
const MaxWebhookBodySize = 100 * 1024 * 1024

// AllowedUpdates sono tutti i tipi di update, passati a setWebhook come
// allowed_updates.
var AllowedUpdates = []string{
	"message", "edited_message", "channel_post", "edited_channel_post",
	"inline_query", "chosen_inline_result", "callback_query", "shipping_query",
	"pre_checkout_query", "poll", "poll_answer", "my_chat_member", "chat_member",
	"chat_join_request", "chat_boost", "removed_chat_boost", "message_reaction",
	"message_reaction_count", "business_connection", "business_message",
	"edited_business_message", "deleted_business_messages", "purchased_paid_media",
}

// WebhookHandler riceve gli update di Telegram su "/<url_path>" (anche con
// "/" finale) e li passa all'handler del gotgbot Updater:
//
//   - altri percorsi → 404; metodi diversi da POST → 405;
//   - secret token assente o diverso da quello atteso → 403;
//   - corpo che non è un update JSON → 400;
//   - altrimenti l'update viene accodato e la risposta è subito 200.
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
	if r.URL.Path != h.path && r.URL.Path != h.path+"/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	token := r.Header.Get(SecretTokenHeader)
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(h.secret)) != 1 {
		http.Error(w, "secret token mancante o errato", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxWebhookBodySize))
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	var update gotgbot.Update
	if err := json.Unmarshal(body, &update); err != nil {
		logger.Error(fmt.Sprintf("❌ Update non valido ricevuto dal webhook: %v", err))
		http.Error(w, "Update could not be processed", http.StatusBadRequest)
		return
	}

	fwd := r.Clone(r.Context())
	fwd.URL.Path = h.path
	fwd.URL.RawPath = ""
	fwd.Body = io.NopCloser(bytes.NewReader(body))
	h.next.ServeHTTP(w, fwd)
}

// SequentialDispatcher è l'UpdateDispatcher passato al gotgbot Updater:
// riceve gli update senza bloccare il webhook (coda illimitata) e li fa
// elaborare uno alla volta, nell'ordine di arrivo, dal Dispatcher (creato
// con MaxRoutines 1). Allo stop elabora gli update ancora in coda.
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
