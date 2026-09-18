//go:build !((darwin && cgo) || linux || windows)

package niimbot

import (
	"context"
	"log/slog"
	"time"
)

// BLEDevice is a printer seen in a scan.
type BLEDevice struct {
	Name    string
	Address string
	RSSI    int
	Model   Model
}

// BLETransport is unavailable in this build.
type BLETransport struct{}

// BLEAvailable reports whether BLE support is compiled in.
func BLEAvailable() bool { return false }

// ScanBLE is unavailable in this build (macOS requires cgo for CoreBluetooth).
func ScanBLE(context.Context, time.Duration, *slog.Logger) ([]BLEDevice, error) {
	return nil, ErrUnsupported
}

// ScanBLEUntil is unavailable in this build.
func ScanBLEUntil(context.Context, time.Duration, *slog.Logger, func(BLEDevice) bool) ([]BLEDevice, error) {
	return nil, ErrUnsupported
}

// ConnectBLE is unavailable in this build.
func ConnectBLE(context.Context, string, *slog.Logger) (*BLETransport, error) {
	return nil, ErrUnsupported
}

func (*BLETransport) Write(context.Context, []byte) error { return ErrUnsupported }
func (*BLETransport) Receive() <-chan []byte              { return nil }
func (*BLETransport) Close() error                        { return nil }
func (*BLETransport) Kind() string                        { return "ble" }
