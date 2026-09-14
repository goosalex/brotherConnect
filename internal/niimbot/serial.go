package niimbot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.bug.st/serial"
)

// SerialTransport talks to a printer over a serial port: a USB CDC port
// (Linux /dev/ttyACM*, Windows COMn) or a Bluetooth Classic SPP port (macOS
// /dev/cu.B1-<serial>, Linux rfcomm, Windows COMn). The printer speaks the
// same packet protocol on all of them.
type SerialTransport struct {
	port serial.Port
	rx   chan []byte
	once sync.Once
	path string
	log  *slog.Logger
}

// SerialBaud is the printer's serial speed (ignored by SPP ports).
const SerialBaud = 115200

// OpenSerial opens path and starts the reader goroutine.
func OpenSerial(path string, log *slog.Logger) (*SerialTransport, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	port, err := serial.Open(path, &serial.Mode{BaudRate: SerialBaud})
	if err != nil {
		return nil, fmt.Errorf("niimbot: open serial %s: %w", path, err)
	}
	_ = port.SetReadTimeout(200 * time.Millisecond)
	_ = port.SetDTR(true)
	_ = port.SetRTS(true)
	t := &SerialTransport{port: port, rx: make(chan []byte, 64), path: path, log: log}
	go t.readLoop()
	return t, nil
}

func (t *SerialTransport) readLoop() {
	defer t.once.Do(func() { close(t.rx) })
	buf := make([]byte, 512)
	for {
		n, err := t.port.Read(buf)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, buf[:n])
			t.rx <- cp
		}
		if err != nil {
			t.log.Debug("niimbot serial: read ended", "path", t.path, "err", err)
			return
		}
	}
}

// Write sends p, completing partial writes.
func (t *SerialTransport) Write(ctx context.Context, p []byte) error {
	for len(p) > 0 {
		n, err := t.port.Write(p)
		if err != nil {
			return fmt.Errorf("niimbot: serial write: %w", err)
		}
		p = p[n:]
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// Receive implements Transport.
func (t *SerialTransport) Receive() <-chan []byte { return t.rx }

// Close closes the port; the reader goroutine ends and Receive closes.
func (t *SerialTransport) Close() error { return t.port.Close() }

// Kind implements Transport.
func (t *SerialTransport) Kind() string { return "serial" }
