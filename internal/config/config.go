// Package config loads bridge configuration from flags and environment.
//
// Phase 1 uses a statically provisioned development token (Requirements.md §5
// note, PhaseMapping.md). The browser-based device-authorization flow and secure
// credential-store persistence are Phase 2.
package config

import (
	"errors"
	"flag"
	"os"
	"time"
)

// Config holds runtime settings for the bridge.
type Config struct {
	// ServerURL is the trencitos WSS endpoint, e.g. wss://box.dev.trencitos.dev:8443/bridge.
	ServerURL string
	// DevToken is the Phase 1 statically provisioned device token.
	DevToken string
	// InstallationID uniquely identifies this bridge installation
	// (Requirements.md §5). Persisted across restarts.
	InstallationID string
	// BridgeVersion is reported to the cloud for diagnostics.
	BridgeVersion string
	// HeartbeatInterval controls liveness pings; 0 disables.
	HeartbeatInterval time.Duration
}

// Default dev endpoint from Requirements.md §1.
const defaultServerURL = "wss://box.dev.trencitos.dev:8443/bridge"

// Load parses configuration from the given args and the environment. Flags take
// precedence over environment variables.
func Load(args []string, version string) (Config, error) {
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	var c Config
	fs.StringVar(&c.ServerURL, "server", env("BRIDGE_SERVER_URL", defaultServerURL), "trencitos WSS endpoint")
	fs.StringVar(&c.DevToken, "token", env("BRIDGE_DEV_TOKEN", ""), "Phase 1 development device token")
	fs.StringVar(&c.InstallationID, "installation-id", env("BRIDGE_INSTALLATION_ID", ""), "installation ID (auto-generated if empty)")
	hb := fs.Duration("heartbeat", envDuration("BRIDGE_HEARTBEAT", 30*time.Second), "heartbeat interval (0 disables)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	c.HeartbeatInterval = *hb
	c.BridgeVersion = version

	if c.InstallationID == "" {
		id, err := loadOrCreateInstallationID()
		if err != nil {
			return Config{}, err
		}
		c.InstallationID = id
	}
	return c, c.validate()
}

func (c Config) validate() error {
	if c.ServerURL == "" {
		return errors.New("server URL is required")
	}
	if c.DevToken == "" {
		return errors.New("dev token is required in Phase 1 (set -token or BRIDGE_DEV_TOKEN)")
	}
	return nil
}

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
