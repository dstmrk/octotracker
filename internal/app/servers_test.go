package app

// Test dei timeout dei server HTTP e del limite del corpo del webhook.

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerTimeouts(t *testing.T) {
	if MaxWebhookBodySize != 1<<20 {
		t.Errorf("MaxWebhookBodySize = %d", MaxWebhookBodySize)
	}
	for name, srv := range map[string]*http.Server{
		"webhook": newWebhookServer(http.NotFoundHandler()),
		"health":  newHealthServer(http.NotFoundHandler()),
	} {
		if srv.ReadHeaderTimeout != 10*time.Second || srv.ReadTimeout != 10*time.Second ||
			srv.WriteTimeout != 10*time.Second || srv.IdleTimeout != 60*time.Second {
			t.Errorf("%s: timeout = %v/%v/%v/%v", name, srv.ReadHeaderTimeout,
				srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
		}
	}
}

// startShortServer avvia srv su una porta locale con timeout brevi, per
// verificare il comportamento senza attendere i valori di produzione.
func startShortServer(t *testing.T, h http.Handler, mutate func(*http.Server)) string {
	t.Helper()
	srv := newWebhookServer(h)
	mutate(srv)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// TestServerClosesStalledBody: un client che manda gli header e poi si
// ferma a metà corpo viene disconnesso dopo ReadTimeout.
func TestServerClosesStalledBody(t *testing.T) {
	addr := startShortServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}), func(s *http.Server) { s.ReadTimeout = 200 * time.Millisecond })
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nabc"))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(conn); err != nil {
		t.Fatalf("connessione non chiusa dal server: %v", err)
	}
}

// TestServerClosesIdleKeepAlive: una connessione keep-alive inattiva viene
// chiusa dopo IdleTimeout.
func TestServerClosesIdleKeepAlive(t *testing.T) {
	addr := startShortServer(t, http.NotFoundHandler(),
		func(s *http.Server) { s.IdleTimeout = 200 * time.Millisecond })
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("la connessione inattiva doveva chiudersi, err = %v", err)
	}
}

// TestWebhookBodyLimit: un corpo oltre il limite è rifiutato e nessun
// update viene accodato; uno di dimensioni normali è accettato.
func TestWebhookBodyLimit(t *testing.T) {
	f := newWebhookFixture(t, nil)
	srv := httptest.NewServer(f.handler)
	defer srv.Close()
	post := func(body string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/"+testToken, strings.NewReader(body))
		for k, v := range validHeaders {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if got := post(updateBodyText(1, strings.Repeat("a", MaxWebhookBodySize))); got != http.StatusBadRequest {
		t.Errorf("corpo oltre il limite: status %d", got)
	}
	if got := post(updateBody(2)); got != http.StatusOK {
		t.Errorf("corpo normale: status %d", got)
	}
	f.stop()
	if got := f.recorder.processed(); len(got) != 1 || got[0] != 2 {
		t.Errorf("update elaborati = %v, attesi solo [2]", got)
	}
}
