//go:build !darwin

package printer

import "errors"

// ErrDriverUnsupported is returned by NewSystemDriver on platforms whose print
// backend is not yet implemented. macOS (CUPS) is the Phase 1 target; Windows
// and Linux drivers land later (PhaseMapping.md).
var ErrDriverUnsupported = errors.New("print driver not implemented on this platform")

// NewSystemDriver returns ErrDriverUnsupported on non-macOS platforms.
func NewSystemDriver() (Driver, error) {
	return nil, ErrDriverUnsupported
}
