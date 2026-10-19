package app

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// Timeout e limiti del client HTTP del bot.
const (
	// ConnectTimeout limita connessione TCP e handshake TLS.
	ConnectTimeout = 10 * time.Second
	// ReadTimeout è l'attesa massima per ogni lettura dal socket (anche
	// l'attesa della risposta dopo l'invio della richiesta).
	ReadTimeout = 30 * time.Second
	// WriteTimeout è l'attesa massima per ogni scrittura.
	WriteTimeout = 15 * time.Second
	// MaxConnections è la dimensione del pool di connessioni.
	MaxConnections = 256
	// KeepAliveExpiry è la durata delle connessioni inattive.
	KeepAliveExpiry = 5 * time.Second
)

// NewBot crea il bot del webhook con i timeout per operazione sul socket
// (connect 10 s, read 30 s, write 15 s) invece che sull'intera richiesta.
// Non fa chiamate di rete né valida il formato del token: l'utente del bot
// (per i comandi /cmd@bot) viene letto con getMe all'avvio (initializeBot).
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

var defaultBotTimeouts = botTimeouts{connect: ConnectTimeout, read: ReadTimeout, write: WriteTimeout}

// newBotTransport crea il trasporto HTTP/1.1 con i timeout per operazione e
// i proxy dalle variabili d'ambiente.
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

// deadlineConn applica a ogni Read e Write una scadenza relativa. Una
// scrittura (l'invio di una richiesta) fa ripartire anche il timeout di
// lettura, così l'attesa della risposta su una connessione riusata ha
// sempre ReadTimeout a disposizione.
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
