package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"brotherConnect/internal/protocol"
)

// Default framing limits for the WSS connection.
const (
	// defaultReadLimit bounds a single inbound message. Job deliveries carry a
	// rendered document (PDF), so this is generous. Align with the Phase 0
	// max-payload agreement (Requirements.md §10).
	defaultReadLimit = 16 << 20 // 16 MiB
	// defaultWriteTimeout bounds a single outbound write.
	defaultWriteTimeout = 15 * time.Second
)

// WSDialer dials the trencitos wss:// endpoint and authenticates with a
// device-scoped token (Requirements.md §5). It implements Dialer.
type WSDialer struct {
	URL   string
	Token string
	// ReadLimit overrides defaultReadLimit when > 0.
	ReadLimit int64
	// WriteTimeout overrides defaultWriteTimeout when > 0.
	WriteTimeout time.Duration
	// HTTPClient lets callers customise TLS/proxy; nil uses the default.
	HTTPClient *http.Client
}

var _ Dialer = (*WSDialer)(nil)

// NewWSDialer returns a dialer for url authenticating with token.
func NewWSDialer(url, token string) *WSDialer {
	return &WSDialer{URL: url, Token: token}
}

// Dial opens a TLS WebSocket connection and returns it as a Conn. The device
// token is sent as a Bearer credential; no password is exposed.
func (d *WSDialer) Dial(ctx context.Context) (Conn, error) {
	opts := &websocket.DialOptions{
		HTTPClient: d.HTTPClient,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + d.Token}},
	}
	c, resp, err := websocket.Dial(ctx, d.URL, opts)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		// A 401/403 means the token was revoked or is invalid; surface it so the
		// reconnect loop can stop instead of hammering (Requirements.md §5).
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("%w: server returned %s", ErrRevoked, resp.Status)
		}
		return nil, fmt.Errorf("dial %s: %w", d.URL, err)
	}

	readLimit := d.ReadLimit
	if readLimit <= 0 {
		readLimit = defaultReadLimit
	}
	c.SetReadLimit(readLimit)

	writeTimeout := d.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = defaultWriteTimeout
	}
	return &wsConn{c: c, writeTimeout: writeTimeout}, nil
}

// wsConn adapts a websocket.Conn to the Conn interface, framing each protocol
// Envelope as one JSON text message. A websocket.Conn permits one concurrent
// reader and one concurrent writer, which matches the Client's separate read
// and write loops.
type wsConn struct {
	c            *websocket.Conn
	writeTimeout time.Duration
}

var _ Conn = (*wsConn)(nil)

// Send marshals the envelope to JSON and writes it as a text message.
func (w *wsConn) Send(e protocol.Envelope) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", e.Type, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), w.writeTimeout)
	defer cancel()
	if err := w.c.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("ws write: %w", err)
	}
	return nil
}

// Receive blocks for the next message and decodes it into an Envelope.
func (w *wsConn) Receive(ctx context.Context) (protocol.Envelope, error) {
	_, data, err := w.c.Read(ctx)
	if err != nil {
		return protocol.Envelope{}, fmt.Errorf("ws read: %w", err)
	}
	var e protocol.Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return protocol.Envelope{}, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return e, nil
}

// Close shuts the connection with a normal-closure status.
func (w *wsConn) Close() error {
	return w.c.Close(websocket.StatusNormalClosure, "bridge shutdown")
}
