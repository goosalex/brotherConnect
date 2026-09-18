//go:build (darwin && cgo) || linux || windows

package niimbot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// GATT identifiers observed on the B1 (see docs/niimbot.md §3). The printer
// exposes a Microchip transparent-UART service and a NIIMBOT service; the
// NIIMBOT characteristic accepts writes and notifies replies. niimbluelib uses
// the same pair.
var (
	bleServiceUUID = mustUUID("e7810a71-73ae-499d-8c15-faa9aef0c3f2")
	bleCharUUID    = mustUUID("bef8d6c9-9c21-4c9e-b632-bd58c1009f9f")
)

func mustUUID(s string) bluetooth.UUID {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		panic(err)
	}
	return u
}

var (
	adapterOnce sync.Once
	adapterErr  error
	// bleMu serialises adapter operations (scan/connect) which tinygo does not
	// allow to overlap.
	bleMu sync.Mutex
)

// adapter enables the default Bluetooth adapter once.
func adapter() (*bluetooth.Adapter, error) {
	adapterOnce.Do(func() {
		adapterErr = bluetooth.DefaultAdapter.Enable()
	})
	if adapterErr != nil {
		return nil, fmt.Errorf("niimbot: enable bluetooth: %w", adapterErr)
	}
	return bluetooth.DefaultAdapter, nil
}

// BLEDevice is a printer seen in a scan.
type BLEDevice struct {
	Name    string
	Address string // platform address: MAC on Linux/Windows, CoreBluetooth UUID on macOS
	RSSI    int
	Model   Model
}

// BLEAvailable reports whether BLE support is compiled in.
func BLEAvailable() bool { return true }

// ScanBLE scans for NIIMBOT printers for the duration of ctx (or until
// timeout). A device is reported once. Printers are recognised by their
// advertised name (e.g. "B1-I711131967") or by advertising the NIIMBOT
// service UUID.
func ScanBLE(ctx context.Context, timeout time.Duration, log *slog.Logger) ([]BLEDevice, error) {
	return ScanBLEUntil(ctx, timeout, log, nil)
}

// ScanBLEUntil is ScanBLE that stops early once stop returns true for a
// discovered printer (e.g. the one being looked for).
func ScanBLEUntil(ctx context.Context, timeout time.Duration, log *slog.Logger, stop func(BLEDevice) bool) ([]BLEDevice, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	a, err := adapter()
	if err != nil {
		return nil, err
	}
	bleMu.Lock()
	defer bleMu.Unlock()

	var (
		mu    sync.Mutex
		found []BLEDevice
		seen  = map[string]bool{}
	)
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	go func() {
		<-sctx.Done()
		_ = a.StopScan()
	}()
	err = a.Scan(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
		name := r.LocalName()
		addr := r.Address.String()
		if seen[addr] {
			return
		}
		m, ok := ModelFromAdvertisedName(name)
		if !ok && !r.HasServiceUUID(bleServiceUUID) {
			return
		}
		seen[addr] = true
		log.Debug("niimbot ble: found", "name", name, "address", addr, "rssi", r.RSSI)
		dev := BLEDevice{Name: name, Address: addr, RSSI: int(r.RSSI), Model: m}
		mu.Lock()
		found = append(found, dev)
		mu.Unlock()
		if stop != nil && stop(dev) {
			cancel()
		}
	})
	if err != nil && sctx.Err() == nil {
		return nil, fmt.Errorf("niimbot: ble scan: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return found, nil
}

// BLETransport is a connected GATT link to a printer.
type BLETransport struct {
	dev  bluetooth.Device
	char bluetooth.DeviceCharacteristic
	rx   chan []byte
	once sync.Once
	mtu  int
	log  *slog.Logger
}

// ConnectBLE connects to the printer at address (from ScanBLE), discovers the
// NIIMBOT characteristic and subscribes to its notifications.
func ConnectBLE(ctx context.Context, address string, log *slog.Logger) (*BLETransport, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	a, err := adapter()
	if err != nil {
		return nil, err
	}
	var addr bluetooth.Address
	addr.Set(address)

	bleMu.Lock()
	dev, err := a.Connect(addr, bluetooth.ConnectionParams{})
	bleMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("niimbot: ble connect %s: %w", address, err)
	}
	t := &BLETransport{dev: dev, rx: make(chan []byte, 64), log: log}

	svcs, err := dev.DiscoverServices([]bluetooth.UUID{bleServiceUUID})
	if err != nil {
		_ = dev.Disconnect()
		return nil, fmt.Errorf("niimbot: discover NIIMBOT service: %w", err)
	}
	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{bleCharUUID})
	if err != nil {
		_ = dev.Disconnect()
		return nil, fmt.Errorf("niimbot: discover NIIMBOT characteristic: %w", err)
	}
	t.char = chars[0]
	if mtu, err := t.char.GetMTU(); err == nil {
		t.mtu = int(mtu)
	}
	if err := t.char.EnableNotifications(func(buf []byte) {
		cp := make([]byte, len(buf))
		copy(cp, buf)
		select {
		case t.rx <- cp:
		default:
			log.Warn("niimbot ble: rx buffer full, dropping notification")
		}
	}); err != nil {
		_ = dev.Disconnect()
		return nil, fmt.Errorf("niimbot: enable notifications: %w", err)
	}
	// Release the receive channel when the peripheral disconnects.
	a.SetConnectHandler(func(d bluetooth.Device, connected bool) {
		if !connected && d.Address.String() == dev.Address.String() {
			t.closeRx()
		}
	})
	log.Debug("niimbot ble: connected", "address", address, "mtu", t.mtu)
	return t, nil
}

func (t *BLETransport) closeRx() { t.once.Do(func() { close(t.rx) }) }

// Write sends one packet as a single write-without-response. Packets are at
// most 262 bytes; the observed MTU is 237 on macOS, so long packets are split
// across writes, which the printer's stream parser tolerates.
func (t *BLETransport) Write(ctx context.Context, p []byte) error {
	max := t.mtu
	if max <= 0 {
		max = 20
	}
	for len(p) > 0 {
		n := min(len(p), max)
		if _, err := t.char.WriteWithoutResponse(p[:n]); err != nil {
			return fmt.Errorf("niimbot: ble write: %w", err)
		}
		p = p[n:]
	}
	return ctx.Err()
}

// Receive implements Transport.
func (t *BLETransport) Receive() <-chan []byte { return t.rx }

// Close disconnects.
func (t *BLETransport) Close() error {
	err := t.dev.Disconnect()
	t.closeRx()
	return err
}

// Kind implements Transport.
func (t *BLETransport) Kind() string { return "ble" }
