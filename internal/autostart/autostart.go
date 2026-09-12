// Package autostart registers the bridge to start automatically at login as a
// per-user login agent (Requirements.md §5 "Support automatic startup",
// docs/ui-design.md §5.3). The bridge is tied to a logged-in user's printers and
// needs a session, so it targets a user-scoped agent — launchd LaunchAgent on
// macOS, and (later) a systemd user unit on Linux and a Scheduled Task on
// Windows — never a system-wide daemon.
//
// Only the macOS (launchd) backend is implemented so far; New returns
// ErrUnsupported elsewhere.
package autostart

import "errors"

// DefaultLabel is the reverse-DNS identifier for the bridge's login agent.
const DefaultLabel = "dev.trencitos.bridge"

// ErrUnsupported is returned by New on platforms without a backend yet.
var ErrUnsupported = errors.New("autostart is not implemented on this platform yet")

// Config describes the login agent to register.
type Config struct {
	// Label is the reverse-DNS service identifier (use DefaultLabel).
	Label string
	// Executable is the absolute path to the bridge binary to launch. Not needed
	// for Uninstall.
	Executable string
	// Args are additional daemon arguments. Empty is normal: the daemon reads its
	// token and server from the credential store.
	Args []string
	// LogDir overrides where the agent's stdout/stderr are written. Empty lets the
	// backend pick a per-user default.
	LogDir string
}

// Service installs and removes the bridge's OS autostart registration.
type Service interface {
	// Install registers and starts the login agent, replacing any existing
	// registration with the same label.
	Install() error
	// Uninstall stops and removes the login agent. Removing an absent agent is
	// not an error.
	Uninstall() error
	// Describe returns a human-readable location for the registration (e.g. the
	// plist path), for logging and CLI output.
	Describe() string
}
