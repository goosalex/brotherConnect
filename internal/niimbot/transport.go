package niimbot

import (
	"context"
	"errors"
)

// Transport moves raw bytes between the host and the printer. Inbound bytes
// arrive on Receive in transport-defined chunks (BLE notifications, serial
// reads); the Client reassembles packets with a Parser.
type Transport interface {
	// Write sends one encoded packet. Implementations must deliver the whole
	// buffer (BLE: within a single notification-sized write).
	Write(ctx context.Context, p []byte) error
	// Receive delivers inbound byte chunks. The channel is closed when the
	// link drops or Close is called.
	Receive() <-chan []byte
	// Close tears the link down.
	Close() error
	// Kind names the transport for logs and printer descriptors ("ble", "serial").
	Kind() string
}

// ErrUnsupported is returned by transports not available on this platform or
// build (e.g. BLE on macOS without cgo).
var ErrUnsupported = errors.New("niimbot: transport unsupported on this platform/build")

// ErrLinkClosed is returned when the transport's receive side has closed.
var ErrLinkClosed = errors.New("niimbot: link closed")
