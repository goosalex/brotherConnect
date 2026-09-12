package credstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// credFile is the fallback filename holding the credentials blob.
const credFile = "credentials.json"

// fileStore persists the credentials blob to a 0600 JSON file. It is the
// fallback for headless hosts with no OS secret service (docs/ui-design.md §5.4:
// "Headless Linux may have no secret service → fall back to an encrypted file").
//
// This fallback relies on filesystem permissions (0600, in the per-user config
// dir), not encryption at rest: a local encryption key would have to live on the
// same disk, so it adds obfuscation rather than real protection without an OS
// keystore to anchor it. Encrypting the fallback is tracked as a Phase 2 harden
// item (Requirements.md §11) alongside encrypting persisted job data.
type fileStore struct{ path string }

var _ Store = fileStore{}

func (f fileStore) Load() (Credentials, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, ErrNotEnrolled
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("read credentials file: %w", err)
	}
	return decode(string(b))
}

func (f fileStore) Save(c Credentials) error {
	blob, err := encode(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	// Write via a temp file + rename so a crash mid-write cannot leave a
	// truncated credentials file.
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(blob), 0o600); err != nil {
		return fmt.Errorf("write credentials file: %w", err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace credentials file: %w", err)
	}
	return nil
}

func (f fileStore) Clear() error {
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear credentials file: %w", err)
	}
	return nil
}

func (f fileStore) Kind() string { return "file:" + f.path }

// NewFile returns a Store backed by a 0600 JSON file at path.
func NewFile(path string) Store { return fileStore{path: path} }

// Open selects the best available credential store for dir: the OS credential
// store when it is usable, otherwise a 0600 file fallback (credentials.json)
// under dir. It probes the keyring once with a throwaway item so a headless host
// with no secret service falls back cleanly instead of failing on first use.
func Open(dir string) Store {
	if keyringUsable() {
		return keyringStore{}
	}
	return fileStore{path: filepath.Join(dir, credFile)}
}

// keyringUsable reports whether the OS credential store can be written and
// deleted, using a throwaway probe item so a missing/unavailable secret service
// is detected before real credentials are stored.
func keyringUsable() bool {
	const probe = "__probe__"
	if err := keyring.Set(service, probe, "1"); err != nil {
		return false
	}
	_ = keyring.Delete(service, probe)
	return true
}
