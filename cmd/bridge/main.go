// Command bridge is the local Go print bridge that connects locally discovered
// Brother label printers to the trencitos cloud application over an outbound WSS
// connection. See Requirements.md and PhaseMapping.md.
//
// Phase 1 status: this wires the stub printer backend and an in-memory transport
// so the full job path (deliver -> ack -> queue -> print -> status) runs
// end-to-end without external dependencies. The real wss:// dialer (gorilla/
// coder WebSocket) and the real USB backend (libusb/gousb) replace the stubs as
// they are implemented; both sit behind the transport.Dialer / printer.Discoverer
// interfaces so main.go is the only wiring that changes.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"brotherConnect/internal/bridge"
	"brotherConnect/internal/config"
	"brotherConnect/internal/printer"
	"brotherConnect/internal/transport"
)

// version is the bridge software version reported to the cloud. Overridable at
// build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// One-shot discovery mode: list connected printers and exit. Handled before
	// config so it needs no device token.
	if slices.Contains(os.Args[1:], "-list-printers") || slices.Contains(os.Args[1:], "--list-printers") {
		if err := listPrinters(); err != nil {
			log.Error("printer discovery failed", "err", err)
			os.Exit(1)
		}
		return
	}

	// One-shot print mode: send a raster file to the first discovered printer,
	// for validating the real driver against hardware. Usage: -print <file>.
	if path, ok := flagValue(os.Args[1:], "-print", "--print"); ok {
		if err := printFile(path); err != nil {
			log.Error("print failed", "err", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load(os.Args[1:], version)
	if err != nil {
		log.Error("configuration error", "err", err)
		os.Exit(2)
	}
	log.Info("starting bridge",
		"version", version,
		"installation_id", cfg.InstallationID,
		"server", cfg.ServerURL)

	// Ctrl-C / SIGTERM triggers graceful shutdown (Requirements.md §5).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var disc printer.Discoverer
	var driver printer.Driver
	if cfg.Virtual {
		// Debug mode: a virtual BROTHER_62 that captures jobs to GIF, no hardware.
		vb := printer.NewVirtualBackend(cfg.VirtualOut, os.Stdout)
		log.Warn("virtual mode: presenting virtual BROTHER_62; captured labels -> "+cfg.VirtualOut, "printer_id", printer.VirtualPrinterID)
		disc, driver = vb, vb
	} else {
		// Real USB discovery where a backend exists (macOS in Phase 1); otherwise
		// fall back to the stub so the bridge still runs.
		d, err := printer.NewUSBDiscoverer(printer.USBOptions{})
		if err != nil {
			log.Warn("USB discovery unavailable; using stub backend", "err", err)
			d = printer.NewStubBackend(nil)
		}
		disc = d

		// Real print driver (CUPS on macOS) sends the document to the device;
		// fall back to the stub recorder where unsupported.
		dr, err := printer.NewSystemDriver()
		if err != nil {
			log.Warn("system print driver unavailable; using stub driver", "err", err)
			dr = printer.NewStubBackend(nil)
		}
		driver = dr
	}

	// Real wss:// dialer authenticating with the device token. -offline swaps in
	// an in-memory transport so discovery/printing can be exercised without a
	// live trencitos server.
	var dialer transport.Dialer
	if cfg.Offline {
		log.Warn("offline mode: using in-memory transport (no server connection)")
		dialer = transport.NewFakeDialer(transport.NewFakeConn())
	} else {
		dialer = transport.NewWSDialer(cfg.ServerURL, cfg.DevToken)
	}

	b := bridge.New(cfg, dialer, disc, driver, log)

	if err := b.Run(ctx); err != nil && err != context.Canceled {
		log.Error("bridge stopped", "err", err)
		os.Exit(1)
	}
	log.Info("bridge stopped cleanly")
}

// listPrinters runs a single USB discovery scan and prints the connected
// printers to stdout, then returns. It is the demonstrable Phase 1 discovery
// path against real hardware.
func listPrinters() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	printers, err := printer.ScanUSB(ctx)
	if err != nil {
		return err
	}
	if len(printers) == 0 {
		fmt.Println("No supported Brother QL printers found on USB.")
		return nil
	}
	// Enrich the presence status from discovery with the live device status the
	// driver can read (Requirements.md §9a). Falls back to the discovery status
	// if no driver is available on this platform.
	driver, derr := printer.NewSystemDriver()

	fmt.Printf("Discovered %d printer(s):\n", len(printers))
	for _, p := range printers {
		status := p.Status
		if derr == nil {
			if reg, ok := driver.(printer.Registrar); ok {
				reg.Register(p)
			}
			if live, err := driver.Status(ctx, p.ID); err == nil {
				status = live
			}
		}
		media := ""
		if mr, ok := driver.(printer.MediaReporter); ok && derr == nil {
			if w, h, ok := mr.LoadedMedia(ctx, p.ID); ok {
				if h > 0 {
					media = fmt.Sprintf(" media=%gx%gmm", w, h)
				} else {
					media = fmt.Sprintf(" media=%gmm(continuous)", w)
				}
			}
		}
		fmt.Printf("  - %s\n      id=%s serial=%s connection=%s status=%s%s\n",
			p.Model, p.ID, p.SerialNumber, p.Connection, status, media)
	}
	return nil
}

// printFile sends the document in path to the first discovered printer via the
// real system driver. It is a manual validation harness for the print path
// against physical hardware. The payload should be a platform-printable document
// (image/PDF/URF on macOS); label size defaults to 62x45mm, override with
// -w/-h (mm). Usage: -print <file> [-w 62 -h 45].
func printFile(path string) error {
	doc, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read document: %w", err)
	}
	opts := printer.PrintOptions{
		WidthMM:  flagFloat(os.Args[1:], 62, "-w", "--width"),
		HeightMM: flagFloat(os.Args[1:], 45, "-h", "--height"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// -virtual: render to a GIF via the virtual printer instead of real hardware.
	if slices.Contains(os.Args[1:], "-virtual") || slices.Contains(os.Args[1:], "--virtual") {
		outDir, ok := flagValue(os.Args[1:], "-virtual-out", "--virtual-out")
		if !ok {
			outDir = "labels"
		}
		vb := printer.NewVirtualBackend(outDir, os.Stdout)
		return vb.Print(ctx, printer.VirtualPrinterID, doc, opts)
	}

	printers, err := printer.ScanUSB(ctx)
	if err != nil {
		return err
	}
	if len(printers) == 0 {
		return fmt.Errorf("no printer discovered; is it powered on and connected?")
	}
	p := printers[0]

	driver, err := printer.NewSystemDriver()
	if err != nil {
		return err
	}
	if reg, ok := driver.(printer.Registrar); ok {
		reg.Register(p)
	}
	fmt.Printf("Printing %d bytes to %s (%s) at %gx%gmm...\n",
		len(doc), p.Model, p.ID, opts.WidthMM, opts.HeightMM)
	if err := driver.Print(ctx, p.ID, doc, opts); err != nil {
		return err
	}
	fmt.Println("Submitted to the print queue.")
	return nil
}

// flagFloat returns the float value following the first matching flag, or def.
func flagFloat(args []string, def float64, names ...string) float64 {
	if v, ok := flagValue(args, names...); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// flagValue returns the value following the first matching flag name in args.
func flagValue(args []string, names ...string) (string, bool) {
	for i, a := range args {
		for _, n := range names {
			if a == n && i+1 < len(args) {
				return args[i+1], true
			}
			if v, ok := strings.CutPrefix(a, n+"="); ok {
				return v, true
			}
		}
	}
	return "", false
}
