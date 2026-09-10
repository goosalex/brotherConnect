package printer

import (
	"bytes"
	"context"
	"image"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVirtualDiscovererReportsBrother62(t *testing.T) {
	vb := NewVirtualBackend(t.TempDir(), &bytes.Buffer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := vb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ev := <-events
	if ev.Kind != EventConnected || ev.Printer.ID != VirtualPrinterID {
		t.Fatalf("event = %+v, want Connected BROTHER_62", ev)
	}
	if w, h, ok := vb.LoadedMedia(ctx, VirtualPrinterID); !ok || w != 62 || h != 0 {
		t.Fatalf("LoadedMedia = %v x %v (ok=%v), want 62 x 0", w, h, ok)
	}
}

// makePNG returns a small PNG payload.
func makePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestVirtualPrintWritesGifAndNotifies(t *testing.T) {
	dir := t.TempDir()
	var notify bytes.Buffer
	vb := NewVirtualBackend(dir, &notify)

	if err := vb.Print(context.Background(), VirtualPrinterID, makePNG(t), PrintOptions{WidthMM: 62, HeightMM: 45}); err != nil {
		t.Fatalf("Print: %v", err)
	}

	// A GIF must have been written and be decodable.
	gifs := globExt(t, dir, ".gif")
	if len(gifs) != 1 {
		t.Fatalf("found %d gifs, want 1", len(gifs))
	}
	f, err := os.Open(gifs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := gif.Decode(f); err != nil {
		t.Fatalf("output is not a valid GIF: %v", err)
	}

	// The notification must be printed and name the saved file.
	msg := notify.String()
	if !strings.Contains(msg, "printed a label") || !strings.Contains(msg, ".gif") {
		t.Fatalf("notification missing expected content: %q", msg)
	}
}

func TestVirtualPrintRawFallback(t *testing.T) {
	dir := t.TempDir()
	var notify bytes.Buffer
	vb := NewVirtualBackend(dir, &notify)

	// Bytes that are neither an image, raster, nor PDF.
	if err := vb.Print(context.Background(), VirtualPrinterID, []byte("not a printable payload"), PrintOptions{WidthMM: 62}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if len(globExt(t, dir, ".bin")) != 1 {
		t.Fatal("expected a raw .bin fallback file")
	}
	if !strings.Contains(notify.String(), "could not rasterise") {
		t.Fatalf("expected a could-not-rasterise notice, got %q", notify.String())
	}
}

func TestVirtualPrintRejectsEmpty(t *testing.T) {
	vb := NewVirtualBackend(t.TempDir(), &bytes.Buffer{})
	if err := vb.Print(context.Background(), VirtualPrinterID, nil, PrintOptions{}); err == nil {
		t.Fatal("expected error for empty document")
	}
}

func globExt(t *testing.T, dir, ext string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*"+ext))
	if err != nil {
		t.Fatal(err)
	}
	return m
}
