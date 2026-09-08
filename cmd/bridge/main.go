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

	// Real USB discovery where a backend exists (macOS in Phase 1); otherwise
	// fall back to the stub so the bridge still runs.
	disc, err := printer.NewUSBDiscoverer(printer.USBOptions{})
	if err != nil {
		log.Warn("USB discovery unavailable; using stub backend", "err", err)
		disc = printer.NewStubBackend(nil)
	}

	// TODO(phase1): the raster Driver (actual printing to the device) is not yet
	// implemented; the stub Driver records jobs. Discovery above is real.
	driver := printer.NewStubBackend(nil)

	// TODO(phase1): replace fake dialer with a wss:// dialer that authenticates
	// using cfg.DevToken and negotiates the protocol version.
	dialer := transport.NewFakeDialer(transport.NewFakeConn())

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
	fmt.Printf("Discovered %d printer(s):\n", len(printers))
	for _, p := range printers {
		fmt.Printf("  - %s\n      id=%s serial=%s connection=%s status=%s\n",
			p.Model, p.ID, p.SerialNumber, p.Connection, p.Status)
	}
	return nil
}
