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
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	// TODO(phase1): replace stub with real USB discovery (libusb/gousb).
	backend := printer.NewStubBackend(nil)

	// TODO(phase1): replace fake dialer with a wss:// dialer that authenticates
	// using cfg.DevToken and negotiates the protocol version.
	dialer := transport.NewFakeDialer(transport.NewFakeConn())

	b := bridge.New(cfg, dialer, backend, backend, log)

	if err := b.Run(ctx); err != nil && err != context.Canceled {
		log.Error("bridge stopped", "err", err)
		os.Exit(1)
	}
	log.Info("bridge stopped cleanly")
}
