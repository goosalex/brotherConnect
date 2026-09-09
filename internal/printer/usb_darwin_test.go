//go:build darwin

package printer

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// fixtureScan returns the captured system_profiler output, or an injected error.
func fixtureScan(t *testing.T) scanFunc {
	t.Helper()
	raw, err := os.ReadFile("testdata/system_profiler_usb.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return func(context.Context) ([]byte, error) { return raw, nil }
}

func TestDarwinDiscovererEmitsConnected(t *testing.T) {
	d := newDarwinUSBDiscoverer(fixtureScan(t), USBOptions{PollInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := d.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-events:
		if ev.Kind != EventConnected {
			t.Fatalf("first event kind = %v, want Connected", ev.Kind)
		}
		if ev.Printer.SerialNumber != "000D6G173970" {
			t.Fatalf("serial = %q, want 000D6G173970", ev.Printer.SerialNumber)
		}
	case <-time.After(time.Second):
		t.Fatal("no Connected event emitted")
	}

	if got := d.List(); len(got) != 1 {
		t.Fatalf("List() = %d printers, want 1", len(got))
	}
}

func TestDarwinDiscovererDetectsDisconnect(t *testing.T) {
	// First scan sees the printer; second scan sees nothing -> Disconnected.
	present, err := os.ReadFile("testdata/system_profiler_usb.json")
	if err != nil {
		t.Fatal(err)
	}
	empty := []byte(`{"SPUSBDataType":[]}`)

	calls := 0
	scan := func(context.Context) ([]byte, error) {
		calls++
		if calls == 1 {
			return present, nil
		}
		return empty, nil
	}

	d := newDarwinUSBDiscoverer(scan, USBOptions{PollInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Drive poll directly with a local channel so the two scans are exercised
	// deterministically (no ticker timing).
	events := make(chan Event, 16)
	d.poll(ctx, events) // scan 1: printer present -> Connected
	if ev := <-events; ev.Kind != EventConnected {
		t.Fatalf("scan 1 event = %v, want Connected", ev.Kind)
	}
	d.poll(ctx, events) // scan 2: printer gone -> Disconnected
	select {
	case ev := <-events:
		if ev.Kind != EventDisconnected {
			t.Fatalf("scan 2 event = %v, want Disconnected", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no Disconnected event emitted")
	}
	if len(d.List()) != 0 {
		t.Fatalf("List() should be empty after disconnect")
	}
}

func TestScanUSBUnsupportedPathIsDarwinOnly(t *testing.T) {
	// Sanity: on darwin the real scanner is wired; just ensure it does not panic
	// when system_profiler is unavailable by using an error-returning scan.
	d := newDarwinUSBDiscoverer(func(context.Context) ([]byte, error) {
		return nil, errors.New("boom")
	}, USBOptions{PollInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := d.Start(ctx); err != nil {
		t.Fatalf("Start should tolerate scan errors, got %v", err)
	}
	if len(d.List()) != 0 {
		t.Fatal("List should be empty when scan fails")
	}
}
