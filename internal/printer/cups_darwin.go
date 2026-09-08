//go:build darwin

package printer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
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

	mu    sync.RWMutex
	known map[string]Printer // printer ID -> full descriptor
}

var (
	_ Driver    = (*cupsDriver)(nil)
	_ Registrar = (*cupsDriver)(nil)
)

// NewCUPSDriver returns a CUPS-backed Driver for macOS.
func NewCUPSDriver() Driver {
	return &cupsDriver{runner: execRunner{}, known: map[string]Printer{}}
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

// resolveQueue maps a printer to its CUPS queue name via `lpstat -v`.
func (d *cupsDriver) resolveQueue(ctx context.Context, p Printer) (string, error) {
	out, err := d.runner.run(ctx, "lpstat", []string{"-v"}, nil)
	if err != nil {
		return "", fmt.Errorf("list cups queues: %w", err)
	}
	name, ok := matchQueue(parseLpstatV(out), p)
	if !ok {
		return "", fmt.Errorf("no CUPS queue matches printer %q (model %q)", p.ID, p.Model)
	}
	return name, nil
}

// Print sends the raster payload to the printer via `lp -d <queue> -o raw`.
func (d *cupsDriver) Print(ctx context.Context, id string, raster []byte) error {
	if len(raster) == 0 {
		return fmt.Errorf("empty raster payload")
	}
	p := d.lookup(id)
	queue, err := d.resolveQueue(ctx, p)
	if err != nil {
		return err
	}
	// -o raw bypasses CUPS filters so the server-generated Brother raster
	// reaches the device untransformed. -T titles the job for diagnostics.
	args := []string{"-d", queue, "-o", "raw", "-T", "brotherConnect"}
	if _, err := d.runner.run(ctx, "lp", args, raster); err != nil {
		return fmt.Errorf("submit print job to %q: %w", queue, err)
	}
	return nil
}

// Status queries the printer's CUPS state and maps it to a coarse status. This
// reflects queue state plus any printer-state-reasons CUPS exposes; richer
// device status (media size, exact fault) requires the device online.
func (d *cupsDriver) Status(ctx context.Context, id string) (Status, error) {
	p := d.lookup(id)
	queue, err := d.resolveQueue(ctx, p)
	if err != nil {
		return StatusOffline, err
	}
	out, err := d.runner.run(ctx, "lpstat", []string{"-l", "-p", queue}, nil)
	if err != nil {
		// A failed query most likely means the device/queue is unreachable.
		return StatusOffline, nil
	}
	return parsePrinterState(out), nil
}
