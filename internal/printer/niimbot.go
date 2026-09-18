package printer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"brotherConnect/internal/niimbot"
)

// ConnectionBluetooth is the connection type of printers reached over
// Bluetooth (LE or Classic SPP), as reported to the cloud.
const ConnectionBluetooth Connection = "bluetooth"

// NiimbotIDPrefix prefixes the stable printer ID of NIIMBOT printers; the rest
// is the device serial (Requirements.md §6 "Printer identity").
const NiimbotIDPrefix = "niimbot-"

// NiimbotConnector abstracts the transports so the backend is testable without
// Bluetooth hardware.
type NiimbotConnector interface {
	Scan(ctx context.Context, d time.Duration) ([]niimbot.BLEDevice, error)
	ConnectBLE(ctx context.Context, address string) (niimbot.Transport, error)
	OpenSerial(path string) (niimbot.Transport, error)
}

// realConnector uses the niimbot package's BLE and serial transports.
type realConnector struct{ log *slog.Logger }

func (r realConnector) Scan(ctx context.Context, d time.Duration) ([]niimbot.BLEDevice, error) {
	return niimbot.ScanBLE(ctx, d, r.log)
}
func (r realConnector) ConnectBLE(ctx context.Context, address string) (niimbot.Transport, error) {
	return niimbot.ConnectBLE(ctx, address, r.log)
}
func (r realConnector) OpenSerial(path string) (niimbot.Transport, error) {
	return niimbot.OpenSerial(path, r.log)
}

// NiimbotOptions configures the NIIMBOT backend.
type NiimbotOptions struct {
	// ScanInterval is the pause between BLE scans (default 20s).
	ScanInterval time.Duration
	// ScanDuration is how long each BLE scan listens (default 5s).
	ScanDuration time.Duration
	// StatusInterval is how often a known printer is contacted for a fresh
	// heartbeat when idle (default 2m). 0 disables periodic refresh.
	StatusInterval time.Duration
	// MissedScans is the number of consecutive scans a printer may be absent
	// from before it is reported disconnected (default 3).
	MissedScans int
	// SerialPorts lists serial/SPP ports to probe instead of, or in addition
	// to, BLE (e.g. /dev/cu.B1-I711131967).
	SerialPorts []string
	// DisableBLE turns BLE scanning off (serial ports only).
	DisableBLE bool
	Log        *slog.Logger
	Connector  NiimbotConnector
	Now        func() time.Time
}

// niimbotDevice is a printer the backend has seen.
type niimbotDevice struct {
	printer   Printer
	address   string // BLE address or "serial:<path>"
	serialTr  string // serial path when address is a serial port
	model     niimbot.Model
	missed    int
	lastCheck time.Time
}

// NiimbotBackend discovers NIIMBOT printers (B1/B21 family) over Bluetooth LE
// or serial ports and prints to them by rasterising the job payload on the
// bridge (the printers only accept 1-bit rows; see docs/niimbot.md). It
// implements Discoverer, Driver and Owner. Printers are contacted on demand
// (identify, status refresh, print) and disconnected in between so they stay
// visible to other apps and do not hold a link open.
type NiimbotBackend struct {
	opts NiimbotOptions
	conn NiimbotConnector
	log  *slog.Logger
	now  func() time.Time

	mu      sync.Mutex                // guards devices
	devices map[string]*niimbotDevice // by printer ID

	linkMu sync.Mutex // serialises Bluetooth use (scan vs. print)
}

var (
	_ Discoverer = (*NiimbotBackend)(nil)
	_ Driver     = (*NiimbotBackend)(nil)
	_ Owner      = (*NiimbotBackend)(nil)
)

// NewNiimbotBackend constructs the backend. It does not touch Bluetooth until
// Start or Print.
func NewNiimbotBackend(opts NiimbotOptions) *NiimbotBackend {
	if opts.ScanInterval <= 0 {
		opts.ScanInterval = 20 * time.Second
	}
	if opts.ScanDuration <= 0 {
		opts.ScanDuration = 5 * time.Second
	}
	if opts.StatusInterval == 0 {
		opts.StatusInterval = 2 * time.Minute
	}
	if opts.MissedScans <= 0 {
		opts.MissedScans = 3
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Connector == nil {
		opts.Connector = realConnector{log: opts.Log}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &NiimbotBackend{opts: opts, conn: opts.Connector, log: opts.Log, now: opts.Now, devices: map[string]*niimbotDevice{}}
}

// Owns reports whether id belongs to this backend (Owner).
func (b *NiimbotBackend) Owns(id string) bool { return strings.HasPrefix(id, NiimbotIDPrefix) }

// Start runs an initial discovery pass synchronously, then polls until ctx is
// done (Discoverer).
func (b *NiimbotBackend) Start(ctx context.Context) (<-chan Event, error) {
	if b.opts.DisableBLE && len(b.opts.SerialPorts) == 0 {
		return nil, errors.New("niimbot: BLE disabled and no serial ports configured")
	}
	events := make(chan Event, 16)
	b.poll(ctx, events)
	go func() {
		defer close(events)
		t := time.NewTicker(b.opts.ScanInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				b.poll(ctx, events)
			}
		}
	}()
	return events, nil
}

// List returns the known printers (Discoverer).
func (b *NiimbotBackend) List() []Printer {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Printer, 0, len(b.devices))
	for _, d := range b.devices {
		out = append(out, d.printer)
	}
	return out
}

// poll runs one discovery cycle: probe serial ports, scan BLE, identify new
// devices, refresh stale statuses and expire missing devices.
func (b *NiimbotBackend) poll(ctx context.Context, events chan<- Event) {
	// Skip the cycle if a print holds the link; the next tick retries.
	if !b.linkMu.TryLock() {
		return
	}
	defer b.linkMu.Unlock()

	seen := map[string]bool{} // by address
	for _, path := range b.opts.SerialPorts {
		addr := "serial:" + path
		if b.knownByAddress(addr) != "" {
			seen[addr] = true
			continue
		}
		tr, err := b.conn.OpenSerial(path)
		if err != nil {
			b.log.Debug("niimbot: serial port unavailable", "path", path, "err", err)
			continue
		}
		if dev, err := b.identify(ctx, tr, addr, ""); err == nil {
			seen[addr] = true
			b.add(dev, events)
		} else {
			b.log.Debug("niimbot: identify over serial failed", "path", path, "err", err)
		}
	}

	if !b.opts.DisableBLE {
		found, err := b.conn.Scan(ctx, b.opts.ScanDuration)
		if err != nil {
			b.log.Warn("niimbot: BLE scan failed", "err", err)
		}
		for _, f := range found {
			seen[f.Address] = true
			if b.knownByAddress(f.Address) != "" {
				continue
			}
			tr, err := b.conn.ConnectBLE(ctx, f.Address)
			if err != nil {
				b.log.Warn("niimbot: connect failed", "name", f.Name, "err", err)
				continue
			}
			dev, err := b.identify(ctx, tr, f.Address, f.Name)
			if err != nil {
				b.log.Warn("niimbot: identify failed", "name", f.Name, "err", err)
				continue
			}
			b.add(dev, events)
		}
	}

	// Expire devices that stopped advertising (powered off / out of range) and
	// refresh statuses that are due.
	b.mu.Lock()
	var refresh []*niimbotDevice
	for id, d := range b.devices {
		if seen[d.address] {
			d.missed = 0
		} else if d.serialTr == "" {
			// A connected BLE peripheral does not advertise; only count misses
			// while it is idle (we hold linkMu here, so it is idle).
			d.missed++
		}
		if d.missed >= b.opts.MissedScans {
			delete(b.devices, id)
			emit(ctx, events, Event{Kind: EventDisconnected, Printer: d.printer, At: b.now()})
			continue
		}
		if b.opts.StatusInterval > 0 && b.now().Sub(d.lastCheck) >= b.opts.StatusInterval {
			refresh = append(refresh, d)
		}
	}
	b.mu.Unlock()

	for _, d := range refresh {
		st, err := b.probeStatus(ctx, d)
		if err != nil {
			b.log.Debug("niimbot: status refresh failed", "printer_id", d.printer.ID, "err", err)
			continue
		}
		b.setStatus(d.printer.ID, st, events, ctx)
	}
}

func (b *NiimbotBackend) knownByAddress(addr string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, d := range b.devices {
		if d.address == addr {
			return id
		}
	}
	return ""
}

// add records a newly identified device and emits Connected.
func (b *NiimbotBackend) add(dev *niimbotDevice, events chan<- Event) {
	b.mu.Lock()
	b.devices[dev.printer.ID] = dev
	b.mu.Unlock()
	emit(context.Background(), events, Event{Kind: EventConnected, Printer: dev.printer, At: b.now()})
}

// setStatus updates a device's cached status, emitting StatusChanged on change.
func (b *NiimbotBackend) setStatus(id string, st Status, events chan<- Event, ctx context.Context) {
	b.mu.Lock()
	d, ok := b.devices[id]
	if !ok {
		b.mu.Unlock()
		return
	}
	d.lastCheck = b.now()
	changed := d.printer.Status != st
	d.printer.Status = st
	p := d.printer
	b.mu.Unlock()
	if changed && events != nil {
		emit(ctx, events, Event{Kind: EventStatusChanged, Printer: p, At: b.now()})
	}
}

// identify opens a client on tr, reads identity and status, and closes the
// link. name is the advertised name (BLE) or "" (serial).
func (b *NiimbotBackend) identify(ctx context.Context, tr niimbot.Transport, address, name string) (*niimbotDevice, error) {
	c := niimbot.NewClient(tr, b.log)
	defer c.Close()
	ictx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	hb, err := c.Heartbeat(ictx)
	if err != nil {
		return nil, err
	}
	model, err := c.Identify(ictx)
	if err != nil {
		return nil, err
	}
	serial, err := c.Serial(ictx)
	if err != nil || serial == "" {
		// Fall back to the advertised name's suffix ("B1-I711131967").
		if i := strings.IndexAny(name, "-_"); i >= 0 && i+1 < len(name) {
			serial = name[i+1:]
		} else {
			serial = strings.TrimPrefix(address, "serial:")
		}
	}
	dev := &niimbotDevice{
		address:   address,
		model:     model,
		lastCheck: b.now(),
		printer: Printer{
			ID:           NiimbotIDPrefix + serial,
			Model:        "NIIMBOT " + model.Name,
			SerialNumber: serial,
			Connection:   ConnectionBluetooth,
			Status:       statusFromHeartbeat(hb),
			DPI:          model.DPI,
		},
	}
	if strings.HasPrefix(address, "serial:") {
		dev.serialTr = strings.TrimPrefix(address, "serial:")
	}
	if rf, err := c.RFID(ictx); err == nil && rf.Present {
		b.log.Info("niimbot: label roll", "printer_id", dev.printer.ID, "barcode", rf.Barcode, "used", rf.UsedPaper, "total", rf.TotalPaper)
	}
	return dev, nil
}

// statusFromHeartbeat maps the printer's heartbeat to the bridge status
// vocabulary (Requirements.md §9a).
func statusFromHeartbeat(hb niimbot.Heartbeat) Status {
	if hb.LidClosed != nil && !*hb.LidClosed {
		return StatusCoverOpen
	}
	if hb.PaperInserted != nil && !*hb.PaperInserted {
		return StatusOutOfMedia
	}
	return StatusReady
}

// open connects to a known device and returns a ready client.
func (b *NiimbotBackend) open(ctx context.Context, d *niimbotDevice) (*niimbot.Client, error) {
	var (
		tr  niimbot.Transport
		err error
	)
	for attempt := 0; attempt < 2; attempt++ {
		if d.serialTr != "" {
			tr, err = b.conn.OpenSerial(d.serialTr)
		} else {
			tr, err = b.conn.ConnectBLE(ctx, d.address)
		}
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", d.printer.ID, err)
	}
	c := niimbot.NewClient(tr, b.log)
	c.SetModel(d.model)
	return c, nil
}

// probeStatus connects, reads a heartbeat and disconnects.
func (b *NiimbotBackend) probeStatus(ctx context.Context, d *niimbotDevice) (Status, error) {
	c, err := b.open(ctx, d)
	if err != nil {
		return StatusOffline, err
	}
	defer c.Close()
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	hb, err := c.Heartbeat(hctx)
	if err != nil {
		return StatusOffline, err
	}
	return statusFromHeartbeat(hb), nil
}

// Status returns the cached status of a known printer (Driver). The cache is
// refreshed on discovery, periodically, and around each print.
func (b *NiimbotBackend) Status(ctx context.Context, id string) (Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.devices[id]
	if !ok {
		return StatusOffline, ErrUnknownPrinter{ID: id}
	}
	return d.printer.Status, nil
}

// Print rasterises doc to the label size in opts and prints one copy (the
// queue repeats for copies). Cover-open / out-of-paper conditions reported by
// the printer become ErrNotPrintable so the queue classifies them.
func (b *NiimbotBackend) Print(ctx context.Context, id string, doc []byte, opts PrintOptions) error {
	b.mu.Lock()
	d, ok := b.devices[id]
	b.mu.Unlock()
	if !ok {
		return ErrUnknownPrinter{ID: id}
	}
	if opts.WidthMM <= 0 || opts.HeightMM <= 0 {
		return fmt.Errorf("niimbot: label dimensions required to print (got %gx%g mm)", opts.WidthMM, opts.HeightMM)
	}

	// Rasterise before touching the link so a bad payload never leaves the
	// printer mid-job.
	img, format, err := DecodeDocument(ctx, doc)
	if err != nil {
		return fmt.Errorf("niimbot: decode payload: %w", err)
	}
	fitted, err := niimbot.Fit(img, opts.WidthMM, opts.HeightMM, d.model.DPI, d.model.HeadPixels)
	if err != nil {
		return err
	}
	enc := niimbot.Encode(fitted, niimbot.EncodeOptions{})

	b.linkMu.Lock()
	defer b.linkMu.Unlock()
	c, err := b.open(ctx, d)
	if err != nil {
		b.setStatus(id, StatusOffline, nil, ctx)
		return err
	}
	defer c.Close()

	hb, err := c.Heartbeat(ctx)
	if err != nil {
		b.setStatus(id, StatusOffline, nil, ctx)
		return fmt.Errorf("niimbot: printer not responding: %w", err)
	}
	if st := statusFromHeartbeat(hb); !st.Printable() {
		b.setStatus(id, st, nil, ctx)
		return ErrNotPrintable{ID: id, Status: st}
	}

	b.log.Info("niimbot: printing", "printer_id", id, "format", format,
		"label_mm", fmt.Sprintf("%gx%g", opts.WidthMM, opts.HeightMM), "px", fmt.Sprintf("%dx%d", enc.Cols, enc.Rows))
	b.setStatus(id, StatusBusy, nil, ctx)
	err = c.Print(ctx, enc, niimbot.PrintOptions{Copies: 1})
	var perr *niimbot.PrintError
	switch {
	case err == nil:
		b.setStatus(id, StatusReady, nil, ctx)
		return nil
	case errors.As(err, &perr):
		st := StatusError
		switch perr.Code {
		case niimbot.ErrCoverOpen:
			st = StatusCoverOpen
		case niimbot.ErrNoPaper, niimbot.ErrPaperOutException:
			st = StatusOutOfMedia
		case niimbot.ErrWrongPaper:
			st = StatusWrongMedia
		}
		b.setStatus(id, st, nil, ctx)
		if st != StatusError {
			return ErrNotPrintable{ID: id, Status: st}
		}
		return fmt.Errorf("niimbot: %w", err)
	default:
		b.setStatus(id, StatusError, nil, ctx)
		return fmt.Errorf("niimbot: %w", err)
	}
}
