package printer

import (
	"context"
	"fmt"
	"image"
	"image/gif"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// VirtualBackend is a debug printer that presents a virtual Brother QL (the
// "BROTHER_62", 62mm continuous) without any hardware. On Print it renders the
// received document to a GIF on disk and writes a notification to a stream
// (stdout), so the full server -> bridge -> print path can be exercised and the
// rasterised label inspected. It implements Discoverer, Driver, MediaReporter
// and Registrar.
type VirtualBackend struct {
	outDir string
	notify io.Writer
	now    func() time.Time

	printer Printer

	mu     sync.Mutex
	seq    int
	events chan Event
}

var (
	_ Discoverer    = (*VirtualBackend)(nil)
	_ Driver        = (*VirtualBackend)(nil)
	_ MediaReporter = (*VirtualBackend)(nil)
	_ Registrar     = (*VirtualBackend)(nil)
)

// VirtualPrinterID is the stable identifier of the virtual printer.
const VirtualPrinterID = "BROTHER_62"

// NewVirtualBackend returns a virtual backend writing captured labels to outDir
// (default "labels") and notifications to notify (default os.Stdout).
func NewVirtualBackend(outDir string, notify io.Writer) *VirtualBackend {
	if outDir == "" {
		outDir = "labels"
	}
	if notify == nil {
		notify = os.Stdout
	}
	return &VirtualBackend{
		outDir: outDir,
		notify: notify,
		now:    time.Now,
		printer: Printer{
			ID:             VirtualPrinterID,
			Model:          "Brother QL-820NWB (virtual)",
			SerialNumber:   "VIRTUAL-BROTHER-62",
			Connection:     ConnectionUSB,
			Status:         StatusReady,
			LoadedWidthMM:  62, // 62mm continuous tape
			LoadedHeightMM: 0,
		},
	}
}

// Start emits a Connected event for the virtual printer, then blocks until ctx
// is done (Discoverer).
func (b *VirtualBackend) Start(ctx context.Context) (<-chan Event, error) {
	events := make(chan Event, 4)
	events <- Event{Kind: EventConnected, Printer: b.printer, At: b.now()}
	b.events = events
	go func() {
		<-ctx.Done()
		close(events)
	}()
	return events, nil
}

// List returns the single virtual printer (Discoverer).
func (b *VirtualBackend) List() []Printer { return []Printer{b.printer} }

// Status always reports ready (Driver).
func (b *VirtualBackend) Status(context.Context, string) (Status, error) {
	return StatusReady, nil
}

// LoadedMedia reports the virtual 62mm continuous tape (MediaReporter).
func (b *VirtualBackend) LoadedMedia(context.Context, string) (float64, float64, bool) {
	return b.printer.LoadedWidthMM, b.printer.LoadedHeightMM, true
}

// Register is a no-op; the virtual printer is fixed (Registrar).
func (b *VirtualBackend) Register(Printer) {}

// Print renders the document to a GIF in outDir and writes a notification. It
// returns nil (the job is "printed") even when the payload can't be rasterised —
// in that case it saves the raw bytes and says so — because the debug goal is to
// capture whatever the server sent.
func (b *VirtualBackend) Print(ctx context.Context, id string, doc []byte, opts PrintOptions) error {
	if len(doc) == 0 {
		return fmt.Errorf("empty document")
	}
	if err := os.MkdirAll(b.outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	b.mu.Lock()
	b.seq++
	seq := b.seq
	b.mu.Unlock()

	dims := fmt.Sprintf("%gx%gmm", opts.WidthMM, opts.HeightMM)
	img, format, derr := decodeToImage(ctx, doc)
	if derr != nil || img == nil {
		path := filepath.Join(b.outDir, fmt.Sprintf("label-%04d-%s.bin", seq, dims))
		if err := os.WriteFile(path, doc, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(b.notify,
			"🖨  virtual BROTHER_62 received a job it could not rasterise (%s, %d bytes)\n    saved raw payload: %s\n",
			format, len(doc), path)
		return nil
	}

	path := filepath.Join(b.outDir, fmt.Sprintf("label-%04d-%s.gif", seq, dims))
	if err := writeGIF(path, img); err != nil {
		return err
	}
	bnds := img.Bounds()
	fmt.Fprintf(b.notify,
		"🖨  virtual BROTHER_62 printed a label\n    size=%s bytes=%d format=%s\n    saved=%s (%dx%d px)\n",
		dims, len(doc), format, path, bnds.Dx(), bnds.Dy())
	return nil
}

// writeGIF encodes img to path as a GIF.
func writeGIF(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := gif.Encode(f, img, nil); err != nil {
		return fmt.Errorf("encode gif: %w", err)
	}
	return nil
}
