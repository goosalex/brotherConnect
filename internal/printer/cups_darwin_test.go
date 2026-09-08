//go:build darwin

package printer

import (
	"context"
	"strings"
	"testing"
)

// fakeRunner records invocations and returns canned output per command.
type fakeRunner struct {
	calls    []recordedCall
	lpstatV  []byte // `lpstat -v` output
	lpstatP  []byte // `lpstat -l -p` output
	lpStdin  []byte // captured stdin of the last `lp` call
	lpErr    error
	statErr  error
}

type recordedCall struct {
	name string
	args []string
}

func (f *fakeRunner) run(_ context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	f.calls = append(f.calls, recordedCall{name: name, args: args})
	switch {
	case name == "lpstat" && contains(args, "-v"):
		return f.lpstatV, f.statErr
	case name == "lpstat" && contains(args, "-p"):
		return f.lpstatP, nil
	case name == "lp":
		f.lpStdin = stdin
		return nil, f.lpErr
	}
	return nil, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func newCUPS(r commandRunner) *cupsDriver {
	return &cupsDriver{runner: r, known: map[string]Printer{}}
}

func TestCUPSPrintResolvesQueueAndSendsRaw(t *testing.T) {
	fr := &fakeRunner{lpstatV: []byte(lpstatVFixture)}
	d := newCUPS(fr)
	// The bridge registers the discovered printer so the driver knows its model.
	d.Register(Printer{ID: "000D6G173970", Model: "Brother QL-820NWB", SerialNumber: "000D6G173970"})

	raster := []byte{0x1B, 0x40, 0xDE, 0xAD}
	if err := d.Print(context.Background(), "000D6G173970", raster); err != nil {
		t.Fatalf("Print: %v", err)
	}

	// The raster must be sent verbatim on lp's stdin.
	if string(fr.lpStdin) != string(raster) {
		t.Fatalf("lp stdin = %v, want raster %v", fr.lpStdin, raster)
	}
	// The lp call must target the resolved queue with -o raw.
	var lpArgs []string
	for _, c := range fr.calls {
		if c.name == "lp" {
			lpArgs = c.args
		}
	}
	if !contains(lpArgs, "Brother_QL_820NWB") || !contains(lpArgs, "raw") {
		t.Fatalf("lp args = %v, want queue + raw", lpArgs)
	}
}

func TestCUPSPrintRejectsEmptyRaster(t *testing.T) {
	d := newCUPS(&fakeRunner{lpstatV: []byte(lpstatVFixture)})
	if err := d.Print(context.Background(), "x", nil); err == nil {
		t.Fatal("expected error for empty raster")
	}
}

func TestCUPSPrintUnknownPrinter(t *testing.T) {
	d := newCUPS(&fakeRunner{lpstatV: []byte(lpstatVFixture)})
	// Unregistered id that doesn't match any queue model.
	err := d.Print(context.Background(), "nonexistent-serial", []byte{0x01})
	if err == nil || !strings.Contains(err.Error(), "no CUPS queue") {
		t.Fatalf("err = %v, want no-queue-match error", err)
	}
}

func TestCUPSStatusMapsReasons(t *testing.T) {
	fr := &fakeRunner{
		lpstatV: []byte(lpstatVFixture),
		lpstatP: []byte("printer Brother_QL_820NWB disabled since ...\n\treasons: media-empty-error"),
	}
	d := newCUPS(fr)
	d.Register(Printer{ID: "000D6G173970", Model: "Brother QL-820NWB"})

	st, err := d.Status(context.Background(), "000D6G173970")
	if err != nil {
		t.Fatal(err)
	}
	if st != StatusOutOfMedia {
		t.Fatalf("status = %q, want out_of_media", st)
	}
}
