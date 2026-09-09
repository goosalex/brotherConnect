package transport

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"brotherConnect/internal/protocol"
)

// Handler processes an inbound message. It runs on the transport's read loop, so
// it must not block for long; hand work off to the bridge/queue.
type Handler func(protocol.Envelope)

// Config configures a Client.
type Config struct {
	Hello             protocol.Hello
	HeartbeatInterval time.Duration // 0 disables heartbeats
	Backoff           *Backoff      // nil uses DefaultBackoff
	Logger            *slog.Logger  // nil discards
	// OutboundBuffer sizes the send queue that absorbs messages produced while
	// disconnected. 0 uses 128.
	OutboundBuffer int
}

// Client owns the outbound WSS connection lifecycle: dial, handshake, heartbeat,
// pump messages, and reconnect with backoff (Requirements.md §5). It presents a
// stable Send API to the rest of the bridge regardless of connection state.
type Client struct {
	dialer  Dialer
	cfg     Config
	backoff *Backoff
	log     *slog.Logger
	handler Handler

	out chan protocol.Envelope
}

// NewClient constructs a Client. handler receives every non-handshake inbound
// message.
func NewClient(dialer Dialer, cfg Config, handler Handler) *Client {
	if cfg.Backoff == nil {
		cfg.Backoff = DefaultBackoff()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.OutboundBuffer <= 0 {
		cfg.OutboundBuffer = 128
	}
	if handler == nil {
		handler = func(protocol.Envelope) {}
	}
	return &Client{
		dialer:  dialer,
		cfg:     cfg,
		backoff: cfg.Backoff,
		log:     cfg.Logger,
		handler: handler,
		out:     make(chan protocol.Envelope, cfg.OutboundBuffer),
	}
}

// Send queues a message for delivery. It never blocks the caller indefinitely:
// if the outbound buffer is full (prolonged disconnection) the message is
// dropped and an error returned so the caller can decide what to do.
func (c *Client) Send(e protocol.Envelope) error {
	select {
	case c.out <- e:
		return nil
	default:
		return errors.New("outbound buffer full")
	}
}

// Run connects and services the connection until ctx is cancelled or the token
// is revoked. It reconnects automatically on connection loss. Returns
// ErrRevoked if the server revoked the device token, or ctx.Err() on shutdown.
func (c *Client) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.connectAndServe(ctx)
		switch {
		case err == nil:
			// clean disconnect; reconnect immediately
			c.backoff.Reset()
		case errors.Is(err, ErrRevoked):
			c.log.Warn("device token revoked; stopping")
			return ErrRevoked
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return err
		default:
			delay := c.backoff.Next()
			c.log.Warn("connection lost; backing off", "err", err, "retry_in", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// connectAndServe performs one dial + handshake + service cycle. It returns when
// the connection dies, ctx is cancelled, or the token is revoked.
func (c *Client) connectAndServe(ctx context.Context) error {
	conn, err := c.dialer.Dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := c.handshake(ctx, conn); err != nil {
		return err
	}
	c.backoff.Reset()
	c.log.Info("connected", "installation_id", c.cfg.Hello.InstallationID)

	// Reader loop feeds inbound messages to the handler; writer loop drains the
	// outbound queue and emits heartbeats. The first to error tears down the
	// connection.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errc := make(chan error, 2)
	go func() { errc <- c.readLoop(ctx, conn) }()
	go func() { errc <- c.writeLoop(ctx, conn) }()

	err = <-errc
	cancel()
	return err
}

// handshake sends Hello and waits for Welcome, checking protocol compatibility.
func (c *Client) handshake(ctx context.Context, conn Conn) error {
	hello, err := protocol.Encode(protocol.TypeHello, c.cfg.Hello)
	if err != nil {
		return err
	}
	if err := conn.Send(hello); err != nil {
		return err
	}
	env, err := conn.Receive(ctx)
	if err != nil {
		return err
	}
	if env.Type != protocol.TypeWelcome {
		return errors.New("expected welcome, got " + string(env.Type))
	}
	var w protocol.Welcome
	if err := protocol.Decode(env, &w); err != nil {
		return err
	}
	if w.ProtocolVersion != protocol.Version {
		c.log.Warn("protocol version mismatch",
			"bridge", protocol.Version, "server", w.ProtocolVersion)
		// Phase 0 defines the compatibility policy; for now proceed and log.
	}
	return nil
}

func (c *Client) readLoop(ctx context.Context, conn Conn) error {
	for {
		env, err := conn.Receive(ctx)
		if err != nil {
			return err
		}
		if env.Type == protocol.TypeTokenRevoked {
			return ErrRevoked
		}
		c.handler(env)
	}
}

func (c *Client) writeLoop(ctx context.Context, conn Conn) error {
	var tick <-chan time.Time
	if c.cfg.HeartbeatInterval > 0 {
		t := time.NewTicker(c.cfg.HeartbeatInterval)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-c.out:
			if err := conn.Send(e); err != nil {
				return err
			}
		case <-tick:
			hb, err := protocol.Encode(protocol.TypeHeartbeat,
				protocol.Heartbeat{InstallationID: c.cfg.Hello.InstallationID})
			if err != nil {
				return err
			}
			if err := conn.Send(hb); err != nil {
				return err
			}
		}
	}
}
