//go:build darwin

package printer

import (
	"bytes"
	"context"
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

	mu        sync.RWMutex
	known     map[string]Printer // printer ID -> full descriptor
	endpoints map[string]string  // printer ID -> cached live IPP device endpoint
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
	}
}

// NewSystemDriver returns the platform's real print driver. On macOS this is the
// CUPS driver; other platforms return ErrDriverUnsupported until implemented.
func NewSystemDriver() (Driver, error) {
	return NewCUPSDriver(), nil
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
// `lpstat -v`.
func (d *cupsDriver) resolveQueue(ctx context.Context, p Printer) (cupsQueue, error) {
	out, err := d.runner.run(ctx, "lpstat", []string{"-v"}, nil)
	if err != nil {
		return cupsQueue{}, fmt.Errorf("list cups queues: %w", err)
	}
	queues := parseLpstatV(out)
	name, ok := matchQueue(queues, p)
	if !ok {
		return cupsQueue{}, fmt.Errorf("no CUPS queue matches printer %q (model %q)", p.ID, p.Model)
	}
	for _, q := range queues {
		if q.Name == name {
			return q, nil
		}
	}
	return cupsQueue{Name: name}, nil
}

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
	args := []string{"-d", queue.Name, "-T", "brotherConnect"}
	if opts.WidthMM > 0 && opts.HeightMM > 0 {
		// Custom.WIDTHxHEIGHTmm is the CUPS page-size form this queue exposes;
		// without it CUPS uses the default media (e.g. 12x12mm) and shrinks the
		// output. fit-to-page then scales the document to that page.
		args = append(args,
			"-o", fmt.Sprintf("PageSize=Custom.%gx%gmm", opts.WidthMM, opts.HeightMM),
			"-o", "fit-to-page")
	}
	// doc is piped on stdin; CUPS auto-detects the format from its content.
	if _, err := d.runner.run(ctx, "lp", args, doc); err != nil {
		return fmt.Errorf("submit print job to %q: %w", queue.Name, err)
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
	return parseIPPMedia(out)
}

// deviceAttributes fetches the printer's live IPP attributes. It prefers the
// direct ipp-usb device endpoint (resolved by matching the CUPS queue's uuid),
// and falls back to the CUPS queue proxy. Returns false if neither is reachable.
func (d *cupsDriver) deviceAttributes(ctx context.Context, p Printer) ([]byte, bool) {
	queue, err := d.resolveQueue(ctx, p)
	if err != nil {
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
