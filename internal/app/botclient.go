package app

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// Timeout del client HTTP del bot, come Application.builder() in bot.py.
const (
	// ConnectTimeout è connect_timeout(10.0): connessione TCP e handshake TLS.
	ConnectTimeout = 10 * time.Second
	// ReadTimeout è read_timeout(30.0): attesa massima per ogni lettura dal
	// socket (anche l'attesa della risposta dopo l'invio della richiesta).
	ReadTimeout = 30 * time.Second
	// WriteTimeout è write_timeout(15.0): attesa massima per ogni scrittura.
	WriteTimeout = 15 * time.Second
	// PoolTimeout è pool_timeout(10.0). Con un pool di MaxConnections
	// connessioni e update elaborati uno alla volta il pool non si esaurisce
	// mai: net/http non ha un equivalente e il valore è solo documentato.
	PoolTimeout = 10 * time.Second
	// MaxConnections è connection_pool_size di default di PTB (256).
	MaxConnections = 256
	// KeepAliveExpiry è keepalive_expiry di default di httpx (5 secondi).
	KeepAliveExpiry = 5 * time.Second
)

// NewBot crea il bot del webhook con i timeout di bot.py (connect 10 s,
// read 30 s, write 15 s), applicati come in httpx a ogni operazione sul
// socket invece che all'intera richiesta. Come Bot(token) di PTB non fa
// chiamate di rete né valida il formato del token: l'utente del bot (per
// i comandi /cmd@bot) viene letto con getMe all'avvio (initializeBot).
func NewBot(token string) *gotgbot.Bot {
	return &gotgbot.Bot{
		Token: token,
		BotClient: &gotgbot.BaseBotClient{
			Client: http.Client{Transport: newBotTransport(defaultBotTimeouts)},
			// Nessun timeout complessivo (il default di gotgbot è 5 s): valgono
			// i timeout per operazione del trasporto.
			DefaultRequestOpts: &gotgbot.RequestOpts{Timeout: -1},
		},
	}
}

// botTimeouts sono i timeout per operazione del client HTTP del bot.
type botTimeouts struct {
	connect, read, write time.Duration
}

// defaultBotTimeouts sono i timeout di bot.py.
var defaultBotTimeouts = botTimeouts{connect: ConnectTimeout, read: ReadTimeout, write: WriteTimeout}

// newBotTransport crea il trasporto HTTP/1.1 (come httpx in PTB, senza
// HTTP/2) con i timeout per operazione e i proxy dalle variabili d'ambiente
// (trust_env di httpx).
func newBotTransport(t botTimeouts) *http.Transport {
	dialer := &net.Dialer{Timeout: t.connect}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &deadlineConn{Conn: conn, read: t.read, write: t.write}, nil
		},
		TLSHandshakeTimeout: t.connect,
		MaxConnsPerHost:     MaxConnections,
		MaxIdleConns:        MaxConnections,
		MaxIdleConnsPerHost: MaxConnections,
		IdleConnTimeout:     KeepAliveExpiry,
		ForceAttemptHTTP2:   false,
	}
}

// deadlineConn applica a ogni Read e Write una scadenza relativa, come i
// timeout read/write di httpcore. Una scrittura (l'invio di una richiesta)
// fa ripartire anche il timeout di lettura, così l'attesa della risposta
// su una connessione riusata ha sempre ReadTimeout a disposizione.
type deadlineConn struct {
	net.Conn
	read, write time.Duration
}

func (c *deadlineConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.read)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c *deadlineConn) Write(p []byte) (int, error) {
	now := time.Now()
	if err := c.Conn.SetWriteDeadline(now.Add(c.write)); err != nil {
		return 0, err
	}
	if err := c.Conn.SetReadDeadline(now.Add(c.read)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}
