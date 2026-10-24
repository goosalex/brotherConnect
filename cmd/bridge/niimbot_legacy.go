//go:build legacy

// Stand-ins for the legacy Go 1.20 macOS build (`-tags legacy`, see
// docs/macos-legacy.md), which cannot include NIIMBOT support: its Bluetooth
// and serial libraries need newer Go. See niimbot.go for the real versions.

package main

import (
	"errors"

	"brotherConnect/internal/compat/slog"
	"brotherConnect/internal/config"
	"brotherConnect/internal/printer"
)

var errNiimbotLegacy = errors.New("NIIMBOT printers are not supported in the legacy macOS 10.13 build; use the standard build on macOS 12 or later")

// withNiimbot returns the Brother backends unchanged.
func withNiimbot(cfg config.Config, disc printer.Discoverer, driver printer.Driver, log *slog.Logger) (printer.Discoverer, printer.Driver) {
	if len(cfg.NiimbotSerial) > 0 {
		log.Warn("ignoring -niimbot-serial", "err", errNiimbotLegacy)
	}
	return disc, driver
}

// runNiimbot reports that the NIIMBOT commands are unavailable.
func runNiimbot([]string, *slog.Logger) error { return errNiimbotLegacy }
