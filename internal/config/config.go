// Package config loads bridge configuration from flags and environment.
//
// The device token comes from -token / BRIDGE_DEV_TOKEN when set (the Phase 1
// path); otherwise Load reads it from the OS credential store populated by
// `bridge enroll` (the Phase 2 device-authorization flow, see internal/enroll and
// internal/credstore). Offline mode needs no token.
package config

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"brotherConnect/internal/credstore"
)

// Config holds runtime settings for the bridge.
type Config struct {
	// ServerURL is the trencitos WSS endpoint, e.g. wss://box.dev.trencitos.dev:8443/bridge.
	ServerURL string
	// DevToken is the resolved device token used to authenticate the WSS
	// connection. It comes from -token / BRIDGE_DEV_TOKEN when set, otherwise
	// from the credential store populated by `bridge enroll` (Phase 2).
	DevToken string
	// TenantName is the tenant the stored token is scoped to, for display in the
	// status UI/CLI (Requirements.md §12). Empty when unknown.
	TenantName string
	// InstallationID uniquely identifies this bridge installation
	// (Requirements.md §5). Persisted across restarts.
	InstallationID string
	// BridgeVersion is reported to the cloud for diagnostics.
	BridgeVersion string
	// HeartbeatInterval controls liveness pings; 0 disables.
	HeartbeatInterval time.Duration
	// Offline uses an in-memory transport instead of dialing the server, for
	// exercising discovery/printing without a live trencitos endpoint.
	Offline bool
	// Virtual replaces hardware discovery/printing with a virtual BROTHER_62
	// printer that captures print jobs to GIF files (debug mode).
	Virtual bool
	// VirtualOut is the directory the virtual printer writes captured labels to.
	VirtualOut string
	// UIAddr is the loopback address for the local status UI. Empty disables it.
	UIAddr string
	// Niimbot enables discovery of NIIMBOT printers over Bluetooth LE
	// (docs/niimbot.md). Needs a cgo build on macOS.
	Niimbot bool
	// NiimbotSerial lists serial/SPP ports to probe for NIIMBOT printers
	// (e.g. /dev/cu.B1-XXXX), usable without BLE support.
	NiimbotSerial []string
}

// Default dev endpoint from Requirements.md §1.
const defaultServerURL = "wss://box.dev.trencitos.dev:8443/bridge"

// registerFlags binds the daemon's option flags to c on fs. It is shared by
// Load (which parses them) and FlagUsage (which prints them), so the option help
// can never drift from what Load actually accepts.
func registerFlags(fs *flag.FlagSet, c *Config) {
	fs.StringVar(&c.ServerURL, "server", env("BRIDGE_SERVER_URL", defaultServerURL), "trencitos WSS endpoint")
	fs.StringVar(&c.DevToken, "token", env("BRIDGE_DEV_TOKEN", ""), "device token (normally supplied by 'bridge enroll'; overrides stored credentials)")
	fs.StringVar(&c.InstallationID, "installation-id", env("BRIDGE_INSTALLATION_ID", ""), "installation ID (auto-generated if empty)")
	fs.DurationVar(&c.HeartbeatInterval, "heartbeat", envDuration("BRIDGE_HEARTBEAT", 30*time.Second), "heartbeat interval (0 disables)")
	fs.BoolVar(&c.Offline, "offline", false, "use an in-memory transport instead of dialing the server")
	fs.BoolVar(&c.Virtual, "virtual", false, "debug: present a virtual BROTHER_62 printer that captures jobs to GIF")
	fs.StringVar(&c.VirtualOut, "virtual-out", "labels", "directory the virtual printer writes captured labels to")
	fs.Bool("debug", false, "verbose (debug-level) logging")
	fs.StringVar(&c.UIAddr, "ui-addr", env("BRIDGE_UI_ADDR", "127.0.0.1:17600"), "local status UI address (empty disables)")
	fs.BoolVar(&c.Niimbot, "niimbot", envBool("BRIDGE_NIIMBOT", true), "discover NIIMBOT label printers over Bluetooth LE")
	fs.Func("niimbot-serial", "comma-separated serial/SPP ports of NIIMBOT printers (e.g. /dev/cu.B1-XXXX)", func(v string) error {
		c.NiimbotSerial = splitList(v)
		return nil
	})
	c.NiimbotSerial = splitList(env("BRIDGE_NIIMBOT_SERIAL", ""))
}

// splitList splits a comma-separated list, dropping empty items.
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// envBool reads a boolean environment variable ("1", "true", "yes" are true).
func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// FlagUsage writes the daemon option flags and their defaults to w. The bridge
// CLI prefixes this with a command overview to form the full help output.
func FlagUsage(w io.Writer) {
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	fs.SetOutput(w)
	registerFlags(fs, &Config{})
	fs.PrintDefaults()
}

// Load parses configuration from the given args and the environment. Flags take
// precedence over environment variables. A -h/--help request returns
// flag.ErrHelp so the caller can print the combined command/option usage.
func Load(args []string, version string) (Config, error) {
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the CLI prints its own combined usage on error/help
	var c Config
	registerFlags(fs, &c)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	c.BridgeVersion = version

	// Was -server given explicitly? If not, stored credentials may supply it.
	serverExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "server" {
			serverExplicit = true
		}
	})

	if c.InstallationID == "" {
		id, err := loadOrCreateInstallationID()
		if err != nil {
			return Config{}, err
		}
		c.InstallationID = id
	}

	// No token from flag/env and we need one: fall back to enrolled credentials
	// from the OS credential store (Phase 2, Requirements.md §5 step 7). Offline
	// mode needs no token, so skip the credential-store probe there.
	if c.DevToken == "" && !c.Offline {
		c.loadStoredCredentials(serverExplicit)
	}

	return c, c.validate()
}

// loadStoredCredentials populates the token (and, when the server was not given
// explicitly, the server URL and tenant) from the credential store if the bridge
// has been enrolled. A missing enrollment is not an error here — validate()
// produces the "not enrolled" guidance.
func (c *Config) loadStoredCredentials(serverExplicit bool) {
	dir, err := appDir()
	if err != nil {
		return
	}
	creds, err := credstore.Open(dir).Load()
	if err != nil {
		if !errors.Is(err, credstore.ErrNotEnrolled) {
			slog.Warn("could not read credential store", "err", err)
		}
		return
	}
	c.DevToken = creds.DeviceToken
	c.TenantName = creds.TenantName
	if !serverExplicit && creds.ServerURL != "" {
		c.ServerURL = creds.ServerURL
	}
}

func (c Config) validate() error {
	if c.Offline {
		return nil // no server connection, so no URL/token needed
	}
	if c.ServerURL == "" {
		return errors.New("server URL is required")
	}
	if c.DevToken == "" {
		return errors.New("no device token: run 'bridge enroll' to authorize this bridge, or set -token / BRIDGE_DEV_TOKEN")
	}
	return nil
}

// AppDir returns the per-user config directory for the bridge, creating it. It
// is exported for the CLI enroll/sign-out flows, which store credentials there.
func AppDir() (string, error) { return appDir() }

// InstallationID returns the persisted installation ID, generating one on first
// use (Requirements.md §5 step 1). Exported so enrollment can use it as the
// OAuth client_id.
func InstallationID() (string, error) { return loadOrCreateInstallationID() }

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
