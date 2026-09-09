//go:build !darwin

package printer

import (
	"context"
	"errors"
	"runtime"
	"time"
)

// ErrUSBUnsupported is returned by USB discovery on platforms whose backend is
// not yet implemented. macOS is the Phase 1 target (Requirements.md §2,
// PhaseMapping.md); Windows and Linux backends land later.
var ErrUSBUnsupported = errors.New("usb discovery not implemented on " + runtime.GOOS)

// NewUSBDiscoverer returns ErrUSBUnsupported on non-macOS platforms.
func NewUSBDiscoverer(USBOptions) (Discoverer, error) {
	return nil, ErrUSBUnsupported
}

// ScanUSB returns ErrUSBUnsupported on non-macOS platforms.
func ScanUSB(context.Context) ([]Printer, error) {
	return nil, ErrUSBUnsupported
}

// USBOptions configures the USB discoverer (fields mirror the darwin build).
type USBOptions struct {
	PollInterval time.Duration
	Now          func() time.Time
}
