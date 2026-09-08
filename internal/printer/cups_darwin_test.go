//go:build darwin

package printer

import (
	"context"
	"strings"
	"testing"
)

// fakeRunner records invocations and returns canned output per command.
type fakeRunner struct {
	calls      []recordedCall
	lpstatV    []byte // `lpstat -v` output
	lpstatP    []byte // `lpstat -l -p` output
	ippfindOut []byte // `ippfind` output (endpoint URIs)
	ippOut     []byte // `ipptool` output
	ippErr     error  // if set, ipptool fails
	lpStdin    []byte // captured stdin of the last `lp` call
	lpErr      error
	statErr    error
}

type recordedCall struct {
	name string
	args []string
}

func (f *fakeRunner) run(_ context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	f.calls = append(f.calls, recordedCall{name: name, args: args})
	switch {
	case name == "ippfind":
		return f.ippfindOut, nil
	case name == "ipptool":
		return f.ippOut, f.ippErr
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
	return &cupsDriver{runner: r, known: map[string]Printer{}, endpoints: map[string]string{}}
}

func TestCUPSPrintResolvesQueueAndSubmitsDocument(t *testing.T) {
	fr := &fakeRunner{lpstatV: []byte(lpstatVFixture)}
	d := newCUPS(fr)
	// The bridge registers the discovered printer so the driver knows its model.
	d.Register(Printer{ID: "000D6G173970", Model: "Brother QL-820NWB", SerialNumber: "000D6G173970"})

	doc := []byte{0x89, 'P', 'N', 'G'} // pretend PNG
	err := d.Print(context.Background(), "000D6G173970", doc, PrintOptions{WidthMM: 62, HeightMM: 45})
	if err != nil {
		t.Fatalf("Print: %v", err)
	}

	// The document must be sent verbatim on lp's stdin.
	if string(fr.lpStdin) != string(doc) {
		t.Fatalf("lp stdin = %v, want doc %v", fr.lpStdin, doc)
	}
	var lpArgs []string
	for _, c := range fr.calls {
		if c.name == "lp" {
			lpArgs = c.args
		}
	}
	// Must target the queue, size the page, and NOT use -o raw (which fails on
	// macOS driverless printers).
	if !contains(lpArgs, "Brother_QL_820NWB") {
		t.Fatalf("lp args = %v, want target queue", lpArgs)
	}
	if contains(lpArgs, "raw") {
		t.Fatalf("lp args = %v, must not contain -o raw", lpArgs)
	}
	if !contains(lpArgs, "PageSize=Custom.62x45mm") || !contains(lpArgs, "fit-to-page") {
		t.Fatalf("lp args = %v, want custom page size + fit-to-page", lpArgs)
	}
}

func TestCUPSPrintNoDimensionsOmitsPageSize(t *testing.T) {
	fr := &fakeRunner{lpstatV: []byte(lpstatVFixture)}
	d := newCUPS(fr)
	d.Register(Printer{ID: "000D6G173970", Model: "Brother QL-820NWB"})
	if err := d.Print(context.Background(), "000D6G173970", []byte{0x01}, PrintOptions{}); err != nil {
		t.Fatal(err)
	}
	var lpArgs []string
	for _, c := range fr.calls {
		if c.name == "lp" {
			lpArgs = c.args
		}
	}
	for _, a := range lpArgs {
		if strings.HasPrefix(a, "PageSize=") {
			t.Fatalf("did not expect a PageSize with zero dimensions, got %v", lpArgs)
		}
	}
}

func TestCUPSPrintRejectsEmptyDocument(t *testing.T) {
	d := newCUPS(&fakeRunner{lpstatV: []byte(lpstatVFixture)})
	if err := d.Print(context.Background(), "x", nil, PrintOptions{}); err == nil {
		t.Fatal("expected error for empty document")
	}
}

func TestCUPSPrintUnknownPrinter(t *testing.T) {
	d := newCUPS(&fakeRunner{lpstatV: []byte(lpstatVFixture)})
	// Unregistered id that doesn't match any queue model.
	err := d.Print(context.Background(), "nonexistent-serial", []byte{0x01}, PrintOptions{})
	if err == nil || !strings.Contains(err.Error(), "no CUPS queue") {
		t.Fatalf("err = %v, want no-queue-match error", err)
	}
}

func TestCUPSStatusUsesLiveIPP(t *testing.T) {
	// IPP reports out-of-media even though the CUPS queue reads "idle" -- the
	// live device state must win.
	fr := &fakeRunner{
		lpstatV: []byte(lpstatVFixture),
		lpstatP: []byte("printer Brother_QL_820NWB is idle. enabled since ..."),
		ippOut:  []byte("        printer-state (enum) = stopped\n        printer-state-reasons (keyword) = media-empty-error"),
	}
	d := newCUPS(fr)
	d.Register(Printer{ID: "000D6G173970", Model: "Brother QL-820NWB"})

	st, err := d.Status(context.Background(), "000D6G173970")
	if err != nil {
		t.Fatal(err)
	}
	if st != StatusOutOfMedia {
		t.Fatalf("status = %q, want out_of_media (from live IPP)", st)
	}
}

// TestCUPSStatusFromDirectEndpoint verifies that Status resolves the live
// ipp-usb device endpoint by matching the queue's uuid and reads the real fault
// (cover-open) from it -- the exact case the CUPS queue proxy misses at idle.
func TestCUPSStatusFromDirectEndpoint(t *testing.T) {
	fr := &fakeRunner{
		lpstatV:    []byte(lpstatVFixture), // QL queue URI carries uuid ...94ddf8ac746c
		ippfindOut: []byte("ipp://localhost:56863/ipp/print\n"),
		ippOut: []byte("printer-make-and-model (textWithoutLanguage) = Brother QL-820NWB\n" +
			"printer-uuid (uri) = urn:uuid:e3248000-80ce-11db-8000-94ddf8ac746c\n" +
			"printer-state (enum) = stopped\n" +
			"printer-state-reasons (keyword) = cover-open"),
	}
	d := newCUPS(fr)
	d.Register(Printer{ID: "000D6G173970", Model: "Brother QL-820NWB"})

	st, err := d.Status(context.Background(), "000D6G173970")
	if err != nil {
		t.Fatal(err)
	}
	if st != StatusCoverOpen {
		t.Fatalf("status = %q, want cover_open (from direct device endpoint)", st)
	}
	// The endpoint must be cached for reuse.
	if got := fr; !usedIPPFind(got) {
		t.Fatal("expected ippfind to have been used to resolve the endpoint")
	}
}

func usedIPPFind(f *fakeRunner) bool {
	for _, c := range f.calls {
		if c.name == "ippfind" {
			return true
		}
	}
	return false
}

func TestCUPSStatusFallsBackToLpstat(t *testing.T) {
	// When ipptool is unavailable, Status falls back to CUPS queue state.
	fr := &fakeRunner{
		lpstatV: []byte(lpstatVFixture),
		lpstatP: []byte("printer Brother_QL_820NWB is idle. enabled since ..."),
		ippErr:  context.DeadlineExceeded,
	}
	d := newCUPS(fr)
	d.Register(Printer{ID: "000D6G173970", Model: "Brother QL-820NWB"})

	st, err := d.Status(context.Background(), "000D6G173970")
	if err != nil {
		t.Fatal(err)
	}
	if st != StatusReady {
		t.Fatalf("status = %q, want ready (lpstat fallback)", st)
	}
}

func TestCUPSLoadedMedia(t *testing.T) {
	fr := &fakeRunner{
		lpstatV: []byte(lpstatVFixture),
		ippOut:  []byte("        media-default (keyword) = custom_12x12mm_12x12mm"),
	}
	d := newCUPS(fr)
	d.Register(Printer{ID: "000D6G173970", Model: "Brother QL-820NWB"})

	w, h, ok := d.LoadedMedia(context.Background(), "000D6G173970")
	if !ok || w != 12 || h != 12 {
		t.Fatalf("LoadedMedia = %v x %v (ok=%v), want 12 x 12", w, h, ok)
	}
}
