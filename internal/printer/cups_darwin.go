//go:build darwin

package printer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// commandRunner runs an external command with optional stdin and returns its
// stdout. It is injectable so the driver's Print/Status flows are testable
// without shelling out or real hardware.
type commandRunner interface {
	run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error)
}

// execRunner runs real commands via os/exec.
type execRunner struct{}

func (execRunner) run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s: %w: %s", name, err, bytes.TrimSpace(errBuf.Bytes()))
	}
	return out.Bytes(), nil
}

// cupsDriver prints and reports status through the macOS CUPS system. Raster is
// sent verbatim to the printer with `lp -o raw` (Requirements.md §8: the payload
// is server-generated Brother raster, so the bridge does not transform it).
//
// Queue resolution matches on model because driverless USB (ippusb/AirPrint)
// URIs carry no serial. The bridge registers discovered printers so the driver
// can map a job's printer ID (its serial) to the model, and thence the queue.
type cupsDriver struct {
	runner commandRunner
	roll   RollKind // loaded roll kind, used only for raw queues

	mu        sync.RWMutex
	known     map[string]Printer // printer ID -> full descriptor
	endpoints map[string]string  // printer ID -> cached live IPP device endpoint
	cuts      map[string]bool    // queue name -> offers CutMedia=EndOfPage
}

var (
	_ Driver        = (*cupsDriver)(nil)
	_ Registrar     = (*cupsDriver)(nil)
	_ MediaReporter = (*cupsDriver)(nil)
)

// NewCUPSDriver returns a CUPS-backed Driver for macOS.
func NewCUPSDriver() Driver {
	return &cupsDriver{
		runner:    execRunner{},
		known:     map[string]Printer{},
		endpoints: map[string]string{},
		cuts:      map[string]bool{},
	}
}

// NewSystemDriver returns the platform's real print driver. On macOS this is the
// CUPS driver; other platforms return ErrDriverUnsupported until implemented.
func NewSystemDriver() (Driver, error) {
	return NewSystemDriverWith(DriverOptions{})
}

// NewSystemDriverWith is NewSystemDriver with options.
func NewSystemDriverWith(opts DriverOptions) (Driver, error) {
	d := NewCUPSDriver().(*cupsDriver)
	d.roll = opts.Roll
	return d, nil
}

// Register records a discovered printer so later Print/Status calls (which carry
// only an ID) can recover the model for queue resolution.
func (d *cupsDriver) Register(p Printer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.known[p.ID] = p
}

// lookup returns the registered printer for id, or a best-effort descriptor
// using the id as its own model/serial.
func (d *cupsDriver) lookup(id string) Printer {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if p, ok := d.known[id]; ok {
		return p
	}
	return Printer{ID: id, Model: id, SerialNumber: id}
}

// resolveQueue maps a printer to its CUPS queue (name + device URI) via
// `lpstat -v`. A driverless queue is preferred; a raw queue created by
// `bridge setup-usb` (macOS 10.13, see cups_raw.go) is the fallback.
func (d *cupsDriver) resolveQueue(ctx context.Context, p Printer) (cupsQueue, error) {
	out, err := d.runner.run(ctx, "lpstat", []string{"-v"}, nil)
	if err != nil {
		return cupsQueue{}, fmt.Errorf("list cups queues: %w", err)
	}
	var normal, raw []cupsQueue
	for _, q := range parseLpstatV(out) {
		if isRawQueue(q) {
			raw = append(raw, q)
		} else {
			normal = append(normal, q)
		}
	}
	if name, ok := matchQueue(normal, p); ok {
		q := cupsQueue{Name: name}
		for _, nq := range normal {
			if nq.Name == name {
				q = nq
			}
		}
		// A driverless queue that cannot cut per label (macOS 10.13's AirPrint
		// queue for the QL has no usable CutMedia option) prints uncut labels.
		// If `bridge setup-usb` made a raw queue, use it: its jobs carry the
		// printer's own cut commands.
		if rq, ok := matchRawQueue(raw, p); ok && !d.queueCuts(ctx, q.Name) {
			return rq, nil
		}
		return q, nil
	}
	if q, ok := matchRawQueue(raw, p); ok {
		return q, nil
	}
	return cupsQueue{}, fmt.Errorf("%w: printer %q (model %q)", ErrNoQueue, p.ID, p.Model)
}

// ErrNoQueue means CUPS has no queue for a discovered printer. On macOS 10.13
// without Brother's driver, `sudo bridge setup-usb` creates one.
var ErrNoQueue = errors.New("no CUPS queue for this printer (on macOS 10.13 run 'sudo bridge setup-usb')")

// Print submits doc to the printer through the CUPS filter chain (NOT `-o raw`).
//
// This macOS printer is driverless (AirPrint/ippusb): it accepts only formats
// the OS renders to URF (image/PDF/URF), and native Brother raster cannot be
// delivered — raw CUPS queues are unsupported on macOS, direct libusb is denied
// by the OS, and `-o raw` over ippusb makes the device jam. So the payload is a
// printable document, and CUPS converts it. The label dimensions set a custom
// page size so the output fills the label instead of the small default media.
func (d *cupsDriver) Print(ctx context.Context, id string, doc []byte, opts PrintOptions) error {
	if len(doc) == 0 {
		return fmt.Errorf("empty document")
	}
	p := d.lookup(id)
	queue, err := d.resolveQueue(ctx, p)
	if err != nil {
		return err
	}
	if isRawQueue(queue) {
		return d.printRaw(ctx, p, queue, doc, opts)
	}
	args := []string{"-d", queue.Name, "-T", "brotherConnect"}
	if opts.WidthMM > 0 && opts.HeightMM > 0 {
		// Custom.WIDTHxHEIGHTmm is the CUPS page-size form this queue exposes;
		// without it CUPS uses the default media (e.g. 12x12mm) and shrinks the
		// output. fit-to-page then scales the document to that page.
		args = append(args,
			"-o", fmt.Sprintf("PageSize=Custom.%gx%gmm", opts.WidthMM, opts.HeightMM),
			"-o", "fit-to-page")
	}
	// Ask for a cut after every label explicitly. The AirPrint queue's PPD
	// defaults CutMedia to EndOfPage, but its IPP finishings-default is "none",
	// and that is what reaches the QL for a job that names neither: the label
	// prints uncut (seen on a shared QL-820NWB queue). Name both the PPD option
	// and its IPP equivalent, finishings 60 (trim-after-pages), which the
	// queue maps to each other via *cupsIPPFinishings.
	if d.queueCuts(ctx, queue.Name) {
		args = append(args, "-o", "CutMedia="+cutPerLabel, "-o", "finishings=60")
	}
	// doc is piped on stdin; CUPS auto-detects the format from its content.
	if _, err := d.runner.run(ctx, "lp", args, doc); err != nil {
		return fmt.Errorf("submit print job to %q: %w", queue.Name, err)
	}
	return nil
}

// queueCuts reports whether a driverless queue offers CutMedia=EndOfPage, from
// `lpoptions -p QUEUE -l`. Answers are cached per queue; a failed lookup is
// treated as "cannot cut" and retried next time.
func (d *cupsDriver) queueCuts(ctx context.Context, queue string) bool {
	d.mu.RLock()
	v, ok := d.cuts[queue]
	d.mu.RUnlock()
	if ok {
		return v
	}
	out, err := d.runner.run(ctx, "lpoptions", []string{"-p", queue, "-l"}, nil)
	if err != nil {
		return false
	}
	v = canCutPerLabel(out)
	d.mu.Lock()
	d.cuts[queue] = v
	d.mu.Unlock()
	return v
}

// DescribePath explains how the driver reaches a printer: which CUPS queue,
// which print path, and how labels are cut. Used by `bridge list`.
func (d *cupsDriver) DescribePath(ctx context.Context, id string) string {
	queue, err := d.resolveQueue(ctx, d.lookup(id))
	if err != nil {
		return err.Error()
	}
	if isRawQueue(queue) {
		return fmt.Sprintf("raw queue %s: bridge renders Brother raster for a %s roll and cuts each label", queue.Name, d.roll)
	}
	if d.queueCuts(ctx, queue.Name) {
		return fmt.Sprintf("driverless queue %s: CUPS renders the PDF, CutMedia=%s cuts each label", queue.Name, cutPerLabel)
	}
	return fmt.Sprintf("driverless queue %s: CUPS renders the PDF, but the queue offers no per-label cut; run 'sudo bridge setup-usb' to print through a raw queue that cuts", queue.Name)
}

// printRaw renders doc as Brother raster for the label size and sends it
// through a raw queue unchanged (`lp -o raw`). This is the macOS 10.13 path,
// where no driver or driverless USB mode can render the document.
func (d *cupsDriver) printRaw(ctx context.Context, p Printer, queue cupsQueue, doc []byte, opts PrintOptions) error {
	model := p.Model
	if dev, ok := parseUSBURI(queue.URI); ok && dev.Model != "" {
		model = dev.Model
	}
	data, err := BrotherRaster(ctx, doc, model, opts, d.roll)
	if err != nil {
		return err
	}
	args := []string{"-d", queue.Name, "-T", "brotherConnect", "-o", "raw"}
	if _, err := d.runner.run(ctx, "lp", args, data); err != nil {
		return fmt.Errorf("submit raster job to %q: %w", queue.Name, err)
	}
	return nil
}

// Status reports the printer's live status from the device's IPP attributes
// (printer-state + printer-state-reasons), so real faults such as cover-open and
// out-of-media are caught (Requirements.md §9a). It queries the direct ipp-usb
// device endpoint, which reflects live device state even at idle; the CUPS queue
// proxy caches idle state and misses faults until a job is attempted.
func (d *cupsDriver) Status(ctx context.Context, id string) (Status, error) {
	p := d.lookup(id)
	if queue, err := d.resolveQueue(ctx, p); err == nil && isRawQueue(queue) {
		// A raw USB queue has no IPP device to ask; the CUPS queue state is
		// all there is (the bridge cannot read the printer's status back).
		out, err := d.runner.run(ctx, "lpstat", []string{"-l", "-p", queue.Name}, nil)
		if err != nil {
			return StatusOffline, nil
		}
		return parsePrinterState(out), nil
	}
	if out, ok := d.deviceAttributes(ctx, p); ok {
		return parseIPPState(out), nil
	}
	// Last resort when IPP is unavailable: coarse CUPS queue state.
	queue, err := d.resolveQueue(ctx, p)
	if err != nil {
		return StatusOffline, nil
	}
	if out, err := d.runner.run(ctx, "lpstat", []string{"-l", "-p", queue.Name}, nil); err == nil {
		return parsePrinterState(out), nil
	}
	return StatusOffline, nil
}

// LoadedMedia returns the media size (mm) currently loaded in the printer, read
// from the device's IPP media-ready/media-default attribute. Implements
// MediaReporter (Requirements.md §8).
func (d *cupsDriver) LoadedMedia(ctx context.Context, id string) (width, height float64, ok bool) {
	out, found := d.deviceAttributes(ctx, d.lookup(id))
	if !found {
		return 0, 0, false
	}
	return parseLoadedMedia(out)
}

// deviceAttributes fetches the printer's live IPP attributes. It prefers the
// direct ipp-usb device endpoint (resolved by matching the CUPS queue's uuid),
// and falls back to the CUPS queue proxy. Returns false if neither is reachable.
func (d *cupsDriver) deviceAttributes(ctx context.Context, p Printer) ([]byte, bool) {
	queue, err := d.resolveQueue(ctx, p)
	if err != nil || isRawQueue(queue) {
		// A raw queue's CUPS attributes describe no real media; reporting them
		// would claim a bogus loaded size, so treat media as unknown.
		return nil, false
	}
	if ep := d.deviceEndpoint(ctx, p, queue); ep != "" {
		if out, err := d.getPrinterAttributes(ctx, ep); err == nil {
			return out, true
		}
		// Stale cached endpoint (e.g. ipp-usb restarted); clear and retry once.
		d.forgetEndpoint(p.ID)
		if ep := d.deviceEndpoint(ctx, p, queue); ep != "" {
			if out, err := d.getPrinterAttributes(ctx, ep); err == nil {
				return out, true
			}
		}
	}
	// Fallback: the CUPS queue proxy (may report cached idle state).
	proxy := "ipp://localhost/printers/" + queue.Name
	if out, err := d.getPrinterAttributes(ctx, proxy); err == nil {
		return out, true
	}
	return nil, false
}

// deviceEndpoint returns the direct ipp-usb device endpoint for p, resolving it
// by matching the CUPS queue's uuid (or model) against the endpoints that
// `ippfind` advertises. Results are cached per printer.
func (d *cupsDriver) deviceEndpoint(ctx context.Context, p Printer, queue cupsQueue) string {
	d.mu.RLock()
	cached := d.endpoints[p.ID]
	d.mu.RUnlock()
	if cached != "" {
		return cached
	}

	// Bound ippfind so a browse can never hang the caller.
	findCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := d.runner.run(findCtx, "ippfind", nil, nil)
	if err != nil {
		return ""
	}

	wantUUID := uuidFromURI(queue.URI)
	wantModel := normalizeModel(p.Model)
	for _, ep := range parseIPPFind(out) {
		attrs, err := d.getPrinterAttributes(ctx, ep)
		if err != nil {
			continue
		}
		gotUUID := normalizeUUID(firstAttr(attrs, "printer-uuid"))
		gotModel := normalizeModel(firstAttr(attrs, "printer-make-and-model"))
		if (wantUUID != "" && gotUUID == wantUUID) ||
			(wantModel != "" && strings.Contains(gotModel, wantModel)) {
			d.mu.Lock()
			d.endpoints[p.ID] = ep
			d.mu.Unlock()
			return ep
		}
	}
	return ""
}

func (d *cupsDriver) forgetEndpoint(id string) {
	d.mu.Lock()
	delete(d.endpoints, id)
	d.mu.Unlock()
}

// getPrinterAttributes runs ipptool's standard get-printer-attributes against an
// IPP URI and returns its output.
func (d *cupsDriver) getPrinterAttributes(ctx context.Context, uri string) ([]byte, error) {
	return d.runner.run(ctx, "ipptool", []string{"-tv", uri, "get-printer-attributes.test"}, nil)
}

// RawQueueSetup reports what SetupRawQueues did for one printer.
type RawQueueSetup struct {
	Device  string // e.g. "Brother QL-820NWB"
	Serial  string
	Queue   string
	Created bool // false when the queue already existed
}

// SetupRawQueues creates a raw CUPS queue for every USB Brother QL printer that
// CUPS can see and that has no bridge queue yet. This is the one-time setup on
// macOS 10.13 without Brother's driver (docs/macos-legacy.md); it needs
// administrator rights, so run it with sudo. Existing queues are left alone.
func SetupRawQueues(ctx context.Context) ([]RawQueueSetup, error) {
	return setupRawQueues(ctx, execRunner{})
}

func setupRawQueues(ctx context.Context, r commandRunner) ([]RawQueueSetup, error) {
	// --include-schemes keeps lpinfo from waiting on network discovery.
	out, err := r.run(ctx, "lpinfo", []string{"--include-schemes", "usb", "-v"}, nil)
	if err != nil {
		return nil, fmt.Errorf("list USB printers (run with sudo): %w", err)
	}
	devs := parseLpinfoUSB(out)
	if len(devs) == 0 {
		return nil, errors.New("CUPS sees no USB Brother QL printer: check it is plugged in and switched on")
	}

	// lpstat fails when no queue exists at all; that just means none to reuse.
	existing := map[string]string{} // device URI -> raw queue name
	if out, err := r.run(ctx, "lpstat", []string{"-v"}, nil); err == nil {
		for _, q := range parseLpstatV(out) {
			if isRawQueue(q) {
				existing[q.URI] = q.Name
			}
		}
	}

	var res []RawQueueSetup
	for _, dev := range devs {
		s := RawQueueSetup{Device: strings.TrimSpace(dev.Make + " " + dev.Model), Serial: dev.Serial}
		if name, ok := existing[dev.URI]; ok {
			s.Queue = name
			res = append(res, s)
			continue
		}
		s.Queue = rawQueueName(dev)
		args := []string{"-p", s.Queue, "-E", "-v", dev.URI, "-m", "raw",
			"-D", s.Device + " (brotherConnect)", "-o", "printer-is-shared=false"}
		if _, err := r.run(ctx, "lpadmin", args, nil); err != nil {
			return res, fmt.Errorf("create queue %s (run with sudo): %w", s.Queue, err)
		}
		s.Created = true
		res = append(res, s)
	}
	return res, nil
}
