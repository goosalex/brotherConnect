// Package credstore persists the bridge's device credentials in the operating
// system credential store (macOS Keychain, Windows Credential Manager, Linux
// Secret Service via D-Bus) with a 0600 JSON file fallback for headless hosts
// that have no secret service. This satisfies Requirements.md §5 step 7 ("store
// credentials securely in the operating system's credential store") and §11
// ("revocable device tokens"), and follows docs/ui-design.md §5.4, which selects
// zalando/go-keyring precisely because it covers all three platforms without
// cgo, keeping the single-binary/cross-compile model intact.
//
// Enrollment (internal/enroll) writes here on success; `bridge sign-out` calls
// Clear; and config.Load reads here so an enrolled daemon needs no -token flag.
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// service is the credential-store service namespace for the bridge, and account
// is the item within it that holds the credentials blob.
const (
	service = "brotherconnect"
	account = "device-credentials"
)

// Credentials is the persisted result of a successful enrollment. A device token
// is bound to the (server, tenant) it was issued for — see docs/ui-design.md §1
// ("a device token is bound to (installation, tenant)") — so the tenant is
// stored alongside the token for display and the server for reconnecting to the
// right endpoint.
type Credentials struct {
	// ServerURL is the trencitos endpoint the token authenticates against.
	ServerURL string `json:"server_url"`
	// DeviceToken is the device-scoped bearer token (Requirements.md §5 step 6).
	DeviceToken string `json:"device_token"`
	// TokenType is the OAuth token type (typically "Bearer"); optional.
	TokenType string `json:"token_type,omitempty"`
	// TenantID and TenantName identify the tenant the token is scoped to
	// (Requirements.md §12), surfaced in the status UI/CLI.
	TenantID   string `json:"tenant_id,omitempty"`
	TenantName string `json:"tenant_name,omitempty"`
	// ObtainedAt and ExpiresAt record token lifetime where the server reports it.
	// A zero ExpiresAt means "no known expiry".
	ObtainedAt time.Time `json:"obtained_at,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

// Expired reports whether the token has a known expiry that is in the past.
func (c Credentials) Expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && now.After(c.ExpiresAt)
}

// ErrNotEnrolled is returned by Load when no credentials are stored.
var ErrNotEnrolled = errors.New("bridge is not enrolled (no stored credentials)")

// Store persists and retrieves the bridge's device credentials.
type Store interface {
	// Load returns the stored credentials, or ErrNotEnrolled if none exist.
	Load() (Credentials, error)
	// Save persists creds, replacing any existing credentials.
	Save(creds Credentials) error
	// Clear removes stored credentials (sign-out). Clearing when none exist is
	// not an error.
	Clear() error
	// Kind returns a short human label for the backing store, for logging (e.g.
	// "os-credential-store" or a file path).
	Kind() string
}

// encode/decode marshal Credentials to and from the single string the credential
// store (and the fallback file) holds.
func encode(c Credentials) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal credentials: %w", err)
	}
	return string(b), nil
}

func decode(s string) (Credentials, error) {
	var c Credentials
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return Credentials{}, fmt.Errorf("unmarshal credentials: %w", err)
	}
	return c, nil
}

// keyringStore stores the credentials blob in the OS credential store.
type keyringStore struct{}

var _ Store = keyringStore{}

func (keyringStore) Load() (Credentials, error) {
	s, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return Credentials{}, ErrNotEnrolled
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("read credential store: %w", err)
	}
	return decode(s)
}

func (keyringStore) Save(c Credentials) error {
	blob, err := encode(c)
	if err != nil {
		return err
	}
	if err := keyring.Set(service, account, blob); err != nil {
		return fmt.Errorf("write credential store: %w", err)
	}
	return nil
}

func (keyringStore) Clear() error {
	if err := keyring.Delete(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("clear credential store: %w", err)
	}
	return nil
}

func (keyringStore) Kind() string { return "os-credential-store" }

// NewKeyring returns a Store backed by the OS credential store.
func NewKeyring() Store { return keyringStore{} }
