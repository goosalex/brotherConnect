package transport

import (
	"context"
	"errors"
	"sync"

	"brotherConnect/internal/protocol"
)

// FakeConn is an in-memory Conn for tests. Messages sent by the bridge land in
// Sent; messages queued via Push are delivered to Receive. Closing or a queued
// error terminates the connection to exercise reconnect logic.
type FakeConn struct {
	mu     sync.Mutex
	inbox  chan protocol.Envelope
	sent   []protocol.Envelope
	closed chan struct{}
	once   sync.Once
}

var _ Conn = (*FakeConn)(nil)

// NewFakeConn returns an open FakeConn.
func NewFakeConn() *FakeConn {
	return &FakeConn{
		inbox:  make(chan protocol.Envelope, 32),
		closed: make(chan struct{}),
	}
}

// Push queues a message to be returned by Receive.
func (c *FakeConn) Push(e protocol.Envelope) { c.inbox <- e }

// Send records an outbound message.
func (c *FakeConn) Send(e protocol.Envelope) error {
	select {
	case <-c.closed:
		return errors.New("send on closed conn")
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, e)
	return nil
}

// Receive returns the next queued message, blocking until one is available, the
// connection closes, or ctx is done.
func (c *FakeConn) Receive(ctx context.Context) (protocol.Envelope, error) {
	select {
	case e := <-c.inbox:
		return e, nil
	case <-c.closed:
		return protocol.Envelope{}, errors.New("conn closed")
	case <-ctx.Done():
		return protocol.Envelope{}, ctx.Err()
	}
}

// Close terminates the connection.
func (c *FakeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// Sent returns a copy of all messages the bridge sent on this conn.
func (c *FakeConn) Sent() []protocol.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]protocol.Envelope, len(c.sent))
	copy(out, c.sent)
	return out
}

// FakeDialer hands out preconfigured FakeConns in order, simulating a sequence
// of connections/reconnections. When conns are exhausted it returns the last
// one again, or an error if configured.
type FakeDialer struct {
	mu    sync.Mutex
	conns []*FakeConn
	i     int
	// DialErr, if set, is returned for every Dial (simulates an unreachable
	// server).
	DialErr error
}

var _ Dialer = (*FakeDialer)(nil)

// NewFakeDialer returns a dialer that yields the given conns in order.
func NewFakeDialer(conns ...*FakeConn) *FakeDialer {
	return &FakeDialer{conns: conns}
}

// Dial returns the next FakeConn.
func (d *FakeDialer) Dial(ctx context.Context) (Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.DialErr != nil {
		return nil, d.DialErr
	}
	if len(d.conns) == 0 {
		return nil, errors.New("no conns configured")
	}
	c := d.conns[d.i]
	if d.i < len(d.conns)-1 {
		d.i++
	}
	return c, nil
}
