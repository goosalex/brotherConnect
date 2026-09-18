package printer

import (
	"context"
	"sync"
)

// MultiDiscoverer merges several discoverers (e.g. USB Brother and BLE NIIMBOT)
// into one event stream and printer list.
type MultiDiscoverer struct {
	discoverers []Discoverer
}

var _ Discoverer = (*MultiDiscoverer)(nil)

// NewMultiDiscoverer combines ds. Nil entries are ignored.
func NewMultiDiscoverer(ds ...Discoverer) *MultiDiscoverer {
	m := &MultiDiscoverer{}
	for _, d := range ds {
		if d != nil {
			m.discoverers = append(m.discoverers, d)
		}
	}
	return m
}

// Start starts every discoverer and fans their events into one channel, which
// closes once all of them have closed.
func (m *MultiDiscoverer) Start(ctx context.Context) (<-chan Event, error) {
	out := make(chan Event, 16)
	var wg sync.WaitGroup
	for _, d := range m.discoverers {
		ch, err := d.Start(ctx)
		if err != nil {
			return nil, err
		}
		wg.Add(1)
		go func(ch <-chan Event) {
			defer wg.Done()
			for ev := range ch {
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}(ch)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out, nil
}

// List concatenates the discoverers' printer lists.
func (m *MultiDiscoverer) List() []Printer {
	var out []Printer
	for _, d := range m.discoverers {
		out = append(out, d.List()...)
	}
	return out
}

// Owner is an optional Driver capability: it reports whether the driver is
// responsible for a printer ID. MultiDriver routes by it.
type Owner interface {
	Owns(id string) bool
}

// MultiDriver routes Print/Status calls to the driver that owns the printer ID
// (drivers implementing Owner); calls for unowned IDs go to the fallback (the
// first driver without Owner, or the first driver).
type MultiDriver struct {
	drivers []Driver
}

var (
	_ Driver        = (*MultiDriver)(nil)
	_ Registrar     = (*MultiDriver)(nil)
	_ MediaReporter = (*MultiDriver)(nil)
)

// NewMultiDriver combines ds. Nil entries are ignored.
func NewMultiDriver(ds ...Driver) *MultiDriver {
	m := &MultiDriver{}
	for _, d := range ds {
		if d != nil {
			m.drivers = append(m.drivers, d)
		}
	}
	return m
}

// resolve picks the driver for id.
func (m *MultiDriver) resolve(id string) Driver {
	var fallback Driver
	for _, d := range m.drivers {
		o, ok := d.(Owner)
		if ok && o.Owns(id) {
			return d
		}
		if !ok && fallback == nil {
			fallback = d
		}
	}
	if fallback == nil && len(m.drivers) > 0 {
		fallback = m.drivers[0]
	}
	return fallback
}

// Print routes to the owning driver.
func (m *MultiDriver) Print(ctx context.Context, id string, doc []byte, opts PrintOptions) error {
	d := m.resolve(id)
	if d == nil {
		return ErrUnknownPrinter{ID: id}
	}
	return d.Print(ctx, id, doc, opts)
}

// Status routes to the owning driver.
func (m *MultiDriver) Status(ctx context.Context, id string) (Status, error) {
	d := m.resolve(id)
	if d == nil {
		return StatusOffline, ErrUnknownPrinter{ID: id}
	}
	return d.Status(ctx, id)
}

// Register forwards to the owning driver if it is a Registrar.
func (m *MultiDriver) Register(p Printer) {
	if r, ok := m.resolve(p.ID).(Registrar); ok {
		r.Register(p)
	}
}

// LoadedMedia forwards to the owning driver if it is a MediaReporter.
func (m *MultiDriver) LoadedMedia(ctx context.Context, id string) (float64, float64, bool) {
	if r, ok := m.resolve(id).(MediaReporter); ok {
		return r.LoadedMedia(ctx, id)
	}
	return 0, 0, false
}
