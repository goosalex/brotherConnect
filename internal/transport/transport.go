// Package transport manages the bridge's outbound WSS connection to trencitos:
// dialing, protocol handshake, heartbeats, and automatic reconnection with
// exponential backoff. See Requirements.md §5.
//
// The real connection uses wss:// (a gorilla/coder WebSocket Conn implementing
// the Conn interface here); that concrete dialer is added when the network
// dependency is vendored. This package is stdlib-only and testable via an
// in-memory fake Conn (see fake.go).
package transport

import (
	"context"
	"errors"

	"brotherConnect/internal/protocol"
)

// Conn is a single live bidirectional message connection. Implementations must
// make Send and Receive safe to call from separate goroutines. A returned error
// from Receive or Send means the connection is dead and should be redialed.
type Conn interface {
	Send(protocol.Envelope) error
	Receive(context.Context) (protocol.Envelope, error)
	Close() error
}

// Dialer establishes new connections. The real implementation dials wss:// with
// the device token; the fake returns an in-memory pipe.
type Dialer interface {
	Dial(ctx context.Context) (Conn, error)
}

// ErrClosed is returned once the transport has been shut down.
var ErrClosed = errors.New("transport closed")

// ErrRevoked signals the device token was revoked or expired and reconnection
// must stop (Requirements.md §5: "Detect revoked or expired credentials").
var ErrRevoked = errors.New("device token revoked")
