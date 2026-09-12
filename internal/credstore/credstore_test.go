package credstore

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// sampleCreds returns a representative Credentials value.
func sampleCreds() Credentials {
	return Credentials{
		ServerURL:   "wss://box.dev.trencitos.dev:8443/bridge",
		DeviceToken: "dev-token-abc123",
		TokenType:   "Bearer",
		TenantID:    "tenant_42",
		TenantName:  "Alex's Collection",
		ObtainedAt:  time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		ExpiresAt:   time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC),
	}
}

// roundTrip exercises the Store contract against any implementation.
func roundTrip(t *testing.T, s Store) {
	t.Helper()

	if _, err := s.Load(); !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("Load on empty store = %v, want ErrNotEnrolled", err)
	}

	want := sampleCreds()
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}

	// Save replaces rather than appends.
	want.DeviceToken = "rotated-token"
	if err := s.Save(want); err != nil {
		t.Fatalf("Save (replace): %v", err)
	}
	if got, _ := s.Load(); got.DeviceToken != "rotated-token" {
		t.Fatalf("replace failed: token = %q", got.DeviceToken)
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("Load after Clear = %v, want ErrNotEnrolled", err)
	}
	// Clear is idempotent.
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear (idempotent): %v", err)
	}
}

func TestKeyringStore(t *testing.T) {
	keyring.MockInit() // in-memory provider; no real Keychain touched
	roundTrip(t, NewKeyring())
}

func TestFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	roundTrip(t, NewFile(path))
}

func TestOpenSelectsKeyringWhenUsable(t *testing.T) {
	keyring.MockInit()
	s := Open(t.TempDir())
	if s.Kind() != "os-credential-store" {
		t.Fatalf("Open Kind = %q, want os-credential-store when keyring is usable", s.Kind())
	}
}

func TestCredentialsExpired(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	if c := (Credentials{}); c.Expired(now) {
		t.Fatal("zero ExpiresAt should never be considered expired")
	}
	past := Credentials{ExpiresAt: now.Add(-time.Hour)}
	if !past.Expired(now) {
		t.Fatal("token past ExpiresAt should be expired")
	}
	future := Credentials{ExpiresAt: now.Add(time.Hour)}
	if future.Expired(now) {
		t.Fatal("token before ExpiresAt should not be expired")
	}
}
