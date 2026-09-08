package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// installIDFile is the filename holding the persisted installation ID.
const installIDFile = "installation_id"

// appDir returns the per-user config directory for the bridge, creating it.
func appDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "brotherconnect")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// loadOrCreateInstallationID returns the persisted installation ID, generating
// and storing one on first run (Requirements.md §5: "Generate a unique
// installation ID"). The ID is securely random and stable across restarts.
func loadOrCreateInstallationID() (string, error) {
	dir, err := appDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, installIDFile)

	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	}

	id, err := newID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("persist installation id: %w", err)
	}
	return id, nil
}

// newID returns a securely generated 128-bit identifier as hex.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "bri_" + hex.EncodeToString(b[:]), nil
}
