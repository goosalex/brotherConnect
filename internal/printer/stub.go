package printer

import (
	"context"
	"sync"
	"time"
)

// StubBackend is an in-memory Discoverer + Driver used for the Phase 1 skeleton
// and tests, standing in for the real USB (libusb/gousb) backend until it lands.
// It advertises a single fake Brother QL printer and records what it "printed".
//
// It implements both Discoverer and Driver.
type StubBackend struct {
	now func() time.Time

	mu       sync.Mutex
	printers map[string]Printer
	printed  map[string][][]byte // printer id -> raster payloads received
	events   chan Event
}

// compile-time interface checks
var (
	_ Discoverer = (*StubBackend)(nil)
	_ Driver     = (*StubBackend)(nil)
)

// NewStubBackend returns a StubBackend advertising one ready Brother QL-820NWB.
// If now is nil, time.Now is used.
func NewStubBackend(now func() time.Time) *StubBackend {
	if now == nil {
		now = time.Now
	}
	p := Printer{
		ID:             "SN-STUB-0001",
		Model:          "Brother QL-820NWB (stub)",
		SerialNumber:   "SN-STUB-0001",
		Connection:     ConnectionUSB,
		Status:         StatusReady,
		LoadedWidthMM:  62, // DK-N standard continuous roll width
		LoadedHeightMM: 0,  // continuous roll: height unbounded
	}
	return &StubBackend{
		now:      now,
		printers: map[string]Printer{p.ID: p},
		printed:  map[string][][]byte{},
	}
}

// Start emits a Connected event for each known printer, then streams future
// events until ctx is done.
func (b *StubBackend) Start(ctx context.Context) (<-chan Event, error) {
	b.mu.Lock()
	b.events = make(chan Event, 8)
	for _, p := range b.printers {
		b.events <- Event{Kind: EventConnected, Printer: p, At: b.now()}
	}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		close(b.events)
		b.events = nil
		b.mu.Unlock()
	}()
	return b.events, nil
}

// List returns the currently known printers.
func (b *StubBackend) List() []Printer {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Printer, 0, len(b.printers))
	for _, p := range b.printers {
		out = append(out, p)
	}
	return out
}

// Print records the document against the target printer.
func (b *StubBackend) Print(ctx context.Context, id string, doc []byte, opts PrintOptions) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.printers[id]
	if !ok {
		return ErrUnknownPrinter{ID: id}
	}
	if !p.Status.Printable() {
		return ErrNotPrintable{ID: id, Status: p.Status}
	}
	cp := make([]byte, len(doc))
	copy(cp, doc)
	b.printed[id] = append(b.printed[id], cp)
	return nil
}

// Status returns the stub printer's status.
func (b *StubBackend) Status(ctx context.Context, id string) (Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.printers[id]
	if !ok {
		return StatusOffline, ErrUnknownPrinter{ID: id}
	}
	return p.Status, nil
}

// SetStatus updates a printer's status (test helper).
func (b *StubBackend) SetStatus(id string, s Status) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.printers[id]; ok {
		p.Status = s
		b.printers[id] = p
	}
}

// PrintedCount returns how many payloads were printed to id (test helper).
func (b *StubBackend) PrintedCount(id string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.printed[id])
}
