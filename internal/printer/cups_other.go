//go:build !darwin

package printer

import (
	"context"
	"errors"
)

// ErrDriverUnsupported is returned by NewSystemDriver on platforms whose print
// backend is not yet implemented. macOS (CUPS) is the Phase 1 target; Windows
// and Linux drivers land later (PhaseMapping.md).
var ErrDriverUnsupported = errors.New("print driver not implemented on this platform")

// NewSystemDriver returns ErrDriverUnsupported on non-macOS platforms.
func NewSystemDriver() (Driver, error) {
	return nil, ErrDriverUnsupported
}

// NewSystemDriverWith returns ErrDriverUnsupported on non-macOS platforms.
func NewSystemDriverWith(DriverOptions) (Driver, error) {
	return nil, ErrDriverUnsupported
}

// RawQueueSetup mirrors the darwin type; raw queue setup is macOS-only.
type RawQueueSetup struct {
	Device  string
	Serial  string
	Queue   string
	Created bool
}

// SetupRawQueues returns ErrDriverUnsupported on non-macOS platforms.
func SetupRawQueues(context.Context) ([]RawQueueSetup, error) {
	return nil, ErrDriverUnsupported
}
