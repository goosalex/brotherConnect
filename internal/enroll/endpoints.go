package enroll

import (
	"fmt"
	"net/url"
	"strings"
)

// Default OAuth endpoint paths on the trencitos server. These are the bridge's
// working assumption; the concrete paths are a Phase 0 contract item to confirm
// with trencitos (PhaseMapping.md, docs/server-contract.md).
const (
	defaultDeviceAuthPath = "/oauth/device_authorization"
	defaultTokenPath      = "/oauth/token"
)

// ConfigFromServer derives an enrollment Config from the bridge's server URL and
// installation ID. The server URL is the wss:// WebSocket endpoint
// (e.g. wss://box.dev.trencitos.dev:8443/bridge); this converts it to the https
// origin and appends the OAuth endpoint paths, since device authorization is an
// HTTP flow that precedes the WebSocket connection.
func ConfigFromServer(serverURL, installationID string) (Config, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return Config{}, fmt.Errorf("parse server URL %q: %w", serverURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "wss", "https":
		u.Scheme = "https"
	case "ws", "http":
		u.Scheme = "http"
	default:
		return Config{}, fmt.Errorf("unsupported server URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return Config{}, fmt.Errorf("server URL %q has no host", serverURL)
	}
	// Endpoints live at the origin root, not under the /bridge WebSocket path.
	origin := &url.URL{Scheme: u.Scheme, Host: u.Host}
	return Config{
		DeviceAuthURL: origin.ResolveReference(&url.URL{Path: defaultDeviceAuthPath}).String(),
		TokenURL:      origin.ResolveReference(&url.URL{Path: defaultTokenPath}).String(),
		ClientID:      installationID,
	}, nil
}
