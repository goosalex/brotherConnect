//go:build darwin

package printer

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// DefaultUSBPollInterval is how often the macOS discoverer re-scans USB to
// detect connect/disconnect events (Requirements.md §6). system_profiler is
// relatively heavy, so this is deliberately not sub-second.
const DefaultUSBPollInterval = 3 * time.Second

// scanFunc returns the raw `system_profiler SPUSBDataType -json` output. It is a
// field so tests can inject a fixture instead of shelling out.
type scanFunc func(ctx context.Context) ([]byte, error)

// systemProfilerScan runs the real system_profiler command.
func systemProfilerScan(ctx context.Context) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "system_profiler", "SPUSBDataType", "-json").Output()
	if err != nil {
		return nil, fmt.Errorf("run system_profiler: %w", err)
	}
	return out, nil
}

// ScanUSB performs a single discovery scan and returns the connected Brother QL
// printers. Used by the one-shot `bridge list` path.
func ScanUSB(ctx context.Context) ([]Printer, error) {
	raw, err := systemProfilerScan(ctx)
	if err != nil {
		return nil, err
	}
	return parseBrotherPrinters(raw)
}

// darwinUSBDiscoverer implements Discoverer on macOS by polling system_profiler
// and diffing successive scans into connect/disconnect events.
type darwinUSBDiscoverer struct {
	scan     scanFunc
	interval time.Duration
	now      func() time.Time

	mu    sync.Mutex
	known map[string]Printer // by Printer.ID
}

var _ Discoverer = (*darwinUSBDiscoverer)(nil)

// USBOptions configures the USB discoverer.
type USBOptions struct {
	PollInterval time.Duration
	Now          func() time.Time
}

// NewUSBDiscoverer returns the platform USB discoverer. On macOS it polls
// system_profiler; other platforms return an unsupported error until their
// backend is implemented.
func NewUSBDiscoverer(opts USBOptions) (Discoverer, error) {
	return newDarwinUSBDiscoverer(systemProfilerScan, opts), nil
}

func newDarwinUSBDiscoverer(scan scanFunc, opts USBOptions) *darwinUSBDiscoverer {
	interval := opts.PollInterval
	if interval <= 0 {
		interval = DefaultUSBPollInterval
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &darwinUSBDiscoverer{
		scan:     scan,
		interval: interval,
		now:      now,
		known:    map[string]Printer{},
	}
}

// Start performs an initial scan (emitting a Connected event per present
// printer) and then polls until ctx is done.
func (d *darwinUSBDiscoverer) Start(ctx context.Context) (<-chan Event, error) {
	events := make(chan Event, 16)

	// Synchronous first scan so List() is populated before Start returns and
	// callers see current devices immediately.
	d.poll(ctx, events)

	go func() {
		defer close(events)
		t := time.NewTicker(d.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.poll(ctx, events)
			}
		}
	}()
	return events, nil
}

// List returns the currently known printers.
func (d *darwinUSBDiscoverer) List() []Printer {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Printer, 0, len(d.known))
	for _, p := range d.known {
		out = append(out, p)
	}
	return out
}

// poll scans once and emits events for the difference against the known set.
func (d *darwinUSBDiscoverer) poll(ctx context.Context, events chan<- Event) {
	raw, err := d.scan(ctx)
	if err != nil {
		return // transient; next tick retries
	}
	printers, err := parseBrotherPrinters(raw)
	if err != nil {
		return
	}

	current := make(map[string]Printer, len(printers))
	for _, p := range printers {
		current[p.ID] = p
	}

	d.mu.Lock()
	prev := d.known
	d.known = current
	d.mu.Unlock()

	// Connected or status-changed.
	for id, p := range current {
		old, existed := prev[id]
		switch {
		case !existed:
			emit(ctx, events, Event{Kind: EventConnected, Printer: p, At: d.now()})
		case old.Status != p.Status:
			emit(ctx, events, Event{Kind: EventStatusChanged, Printer: p, At: d.now()})
		}
	}
	// Disconnected.
	for id, p := range prev {
		if _, still := current[id]; !still {
			emit(ctx, events, Event{Kind: EventDisconnected, Printer: p, At: d.now()})
		}
	}
}
