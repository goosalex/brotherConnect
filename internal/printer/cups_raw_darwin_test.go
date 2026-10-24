//go:build darwin

package printer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

const rawLpstatV = "device for brotherConnect_QL_820NWB_000D6G173970: usb://Brother/QL-820NWB?serial=000D6G173970\n"

var rawPrinter = Printer{ID: "000D6G173970", Model: "Brother QL-820NWB", SerialNumber: "000D6G173970"}

// On macOS 10.13 the only queue is the raw one from `bridge setup-usb`; the
// driver must render Brother raster and send it with -o raw.
func TestCUPSPrintRawQueueSendsBrotherRaster(t *testing.T) {
	r := &fakeRunner{lpstatV: []byte(rawLpstatV)}
	d := newCUPS(r)
	d.Register(rawPrinter)

	doc := pngDoc(t, testLabel())
	if err := d.Print(context.Background(), rawPrinter.ID, doc, PrintOptions{WidthMM: 62, HeightMM: 29}); err != nil {
		t.Fatal(err)
	}
	var lp *recordedCall
	for i := range r.calls {
		if r.calls[i].name == "lp" {
			lp = &r.calls[i]
		}
	}
	if lp == nil {
		t.Fatal("lp was not called")
	}
	if !contains(lp.args, "raw") || !contains(lp.args, "brotherConnect_QL_820NWB_000D6G173970") {
		t.Errorf("lp args = %v, want -o raw to the bridge queue", lp.args)
	}
	if contains(lp.args, "fit-to-page") {
		t.Errorf("raw jobs must not carry filter options: %v", lp.args)
	}
	if !looksLikeBrotherRaster(r.lpStdin) || bytes.Equal(r.lpStdin, doc) {
		t.Fatal("stdin must be the rendered Brother raster, not the PNG")
	}
	if img, ok := decodeBrotherRaster(r.lpStdin); !ok || img.Bounds().Dy() == 0 {
		t.Fatal("rendered raster does not decode")
	}
	// The default roll is continuous, even for a die-cut-sized label.
	if typ := r.lpStdin[400+10]; typ != 0x0A {
		t.Errorf("media type = %#x, want 0x0a (continuous)", typ)
	}

	// A driver configured for a die-cut roll lays the same job out as die-cut.
	dc, _ := NewSystemDriverWith(DriverOptions{Roll: RollDieCut})
	dcd := dc.(*cupsDriver)
	r2 := &fakeRunner{lpstatV: []byte(rawLpstatV)}
	dcd.runner = r2
	dcd.Register(rawPrinter)
	if err := dcd.Print(context.Background(), rawPrinter.ID, doc, PrintOptions{WidthMM: 62, HeightMM: 29}); err != nil {
		t.Fatal(err)
	}
	if typ := r2.lpStdin[400+10]; typ != 0x0B {
		t.Errorf("die-cut driver media type = %#x, want 0x0b", typ)
	}
}

// A driverless queue still wins when both exist (modern macOS with a raw
// queue left over), keeping the existing PDF path.
// lpoptionsQL is `lpoptions -p Brother_QL_820NWB -l` from the AirPrint queue
// of a QL-820NWB (captured 2026-10-23).
const lpoptionsQL = `ColorModel/Color Mode: *Gray
cupsPrintQuality/Quality: *Normal High
CutMedia/CutMedia: None *EndOfPage EndOfJob
PageSize/Media Size: *12x12mm 17x54mm 17x87mm 23x23mm 24x24mm 29x42mm 29x52mm 29x54mm 29x62mm 29x90mm 38x90mm 39x48mm 58x58mm 60x86mm 62x100mm Custom.WIDTHxHEIGHT
MediaType/MediaType: labels roll *any
`

const driverlessLpstatV = "device for Brother_QL_820NWB: ippusb://Brother%20QL-820NWB._ipp._tcp.local./?uuid=x\n"

func lpCall(t *testing.T, r *fakeRunner) recordedCall {
	t.Helper()
	for _, c := range r.calls {
		if c.name == "lp" {
			return c
		}
	}
	t.Fatal("lp was not called")
	return recordedCall{}
}

// A driverless queue that can cut wins over a leftover raw queue, and the job
// asks for a cut after each label explicitly: the queue's IPP
// finishings-default is "none", so relying on the default printed uncut labels.
func TestCUPSDriverlessJobRequestsCut(t *testing.T) {
	r := &fakeRunner{lpstatV: []byte(rawLpstatV + driverlessLpstatV), lpoptsOut: []byte(lpoptionsQL)}
	d := newCUPS(r)
	d.Register(rawPrinter)
	doc := []byte("%PDF-1.4 fake")
	if err := d.Print(context.Background(), rawPrinter.ID, doc, PrintOptions{WidthMM: 62, HeightMM: 29}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r.lpStdin, doc) {
		t.Error("driverless queue must receive the document unchanged")
	}
	lp := lpCall(t, r)
	for _, want := range []string{"Brother_QL_820NWB", "CutMedia=EndOfPage", "finishings=60"} {
		if !contains(lp.args, want) {
			t.Errorf("lp args = %v, missing %s", lp.args, want)
		}
	}
	if got := d.DescribePath(context.Background(), rawPrinter.ID); !strings.Contains(got, "CutMedia=EndOfPage") {
		t.Errorf("DescribePath = %q", got)
	}
}

// A driverless queue without a per-label cut loses to the raw queue, whose
// Brother raster carries the cut commands.
func TestCUPSRawQueueWinsWhenDriverlessCannotCut(t *testing.T) {
	r := &fakeRunner{lpstatV: []byte(rawLpstatV + driverlessLpstatV),
		lpoptsOut: []byte("ColorModel/Color Mode: *Gray\nPageSize/Media Size: *12x12mm\n")}
	d := newCUPS(r)
	d.Register(rawPrinter)
	if err := d.Print(context.Background(), rawPrinter.ID, pngDoc(t, testLabel()), PrintOptions{WidthMM: 62, HeightMM: 29}); err != nil {
		t.Fatal(err)
	}
	lp := lpCall(t, r)
	if !contains(lp.args, "brotherConnect_QL_820NWB_000D6G173970") || !contains(lp.args, "raw") {
		t.Errorf("lp args = %v, want the raw queue", lp.args)
	}
	if !strings.Contains(d.DescribePath(context.Background(), rawPrinter.ID), "raw queue") {
		t.Error("DescribePath should report the raw queue")
	}
}

// Without a raw queue, a driverless queue that cannot cut still prints, and
// DescribePath says how to get cutting.
func TestCUPSDriverlessWithoutCutStillPrints(t *testing.T) {
	r := &fakeRunner{lpstatV: []byte(driverlessLpstatV), lpoptsOut: []byte("PageSize/Media Size: *12x12mm\n")}
	d := newCUPS(r)
	d.Register(rawPrinter)
	if err := d.Print(context.Background(), rawPrinter.ID, []byte("%PDF-1.4 fake"), PrintOptions{WidthMM: 62, HeightMM: 29}); err != nil {
		t.Fatal(err)
	}
	if contains(lpCall(t, r).args, "finishings=60") {
		t.Error("must not request a cut the queue does not offer")
	}
	if got := d.DescribePath(context.Background(), rawPrinter.ID); !strings.Contains(got, "setup-usb") {
		t.Errorf("DescribePath = %q, want a setup-usb hint", got)
	}
}

func TestCUPSRawQueueStatusAndMedia(t *testing.T) {
	r := &fakeRunner{
		lpstatV: []byte(rawLpstatV),
		lpstatP: []byte("printer brotherConnect_QL_820NWB_000D6G173970 is idle.  enabled since Thu Oct 23 2026\n"),
		ippOut:  []byte("media-default (keyword) = na_letter_8.5x11in\n"),
	}
	d := newCUPS(r)
	d.Register(rawPrinter)
	st, err := d.Status(context.Background(), rawPrinter.ID)
	if err != nil || st != StatusReady {
		t.Errorf("status = %v, %v; want ready from the queue state", st, err)
	}
	for _, c := range r.calls {
		if c.name == "ippfind" || c.name == "ipptool" {
			t.Errorf("raw queue status must not query IPP (%s %v)", c.name, c.args)
		}
	}
	if w, h, ok := d.LoadedMedia(context.Background(), rawPrinter.ID); ok {
		t.Errorf("raw queue media = %gx%g, want unknown", w, h)
	}
}

func TestCUPSNoQueueError(t *testing.T) {
	d := newCUPS(&fakeRunner{lpstatV: []byte("")})
	d.Register(rawPrinter)
	err := d.Print(context.Background(), rawPrinter.ID, []byte("%PDF"), PrintOptions{WidthMM: 62, HeightMM: 29})
	if !errors.Is(err, ErrNoQueue) {
		t.Errorf("err = %v, want ErrNoQueue", err)
	}
}

func TestSetupRawQueues(t *testing.T) {
	r := &fakeRunner{
		lpinfoOut: []byte("direct usb://Brother/QL-820NWB?serial=000D6G173970\ndirect usb://Brother/QL-700?serial=K7Z123\nnetwork ipp\n"),
		lpstatV:   []byte(rawLpstatV), // the QL-820NWB is already set up
	}
	res, err := setupRawQueues(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Created || !res[1].Created || res[1].Queue != "brotherConnect_QL_700_K7Z123" {
		t.Fatalf("results = %+v", res)
	}
	var admin []recordedCall
	for _, c := range r.calls {
		if c.name == "lpadmin" {
			admin = append(admin, c)
		}
	}
	if len(admin) != 1 {
		t.Fatalf("lpadmin called %d times, want once (only the new printer)", len(admin))
	}
	want := []string{"-p", "brotherConnect_QL_700_K7Z123", "-E", "-v", "usb://Brother/QL-700?serial=K7Z123", "-m", "raw"}
	for i, a := range want {
		if admin[0].args[i] != a {
			t.Fatalf("lpadmin args = %v, want prefix %v", admin[0].args, want)
		}
	}
}

func TestSetupRawQueuesErrors(t *testing.T) {
	if _, err := setupRawQueues(context.Background(), &fakeRunner{lpinfoOut: []byte("network ipp\n")}); err == nil {
		t.Error("want an error when no QL printer is attached")
	}
	r := &fakeRunner{lpinfoOut: []byte("direct usb://Brother/QL-700\n"), lpadminErr: errors.New("lpadmin: Forbidden")}
	if _, err := setupRawQueues(context.Background(), r); err == nil {
		t.Error("want lpadmin's error to surface")
	}
}
