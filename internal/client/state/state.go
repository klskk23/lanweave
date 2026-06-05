// Package state persists the client's non-secret "already set up" record (state.json).
// Its presence means the first-run wizard can be skipped. It never stores a secret — the
// device private key lives only in the OS secure store.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// SchemaVersion is the current state-record schema version.
const SchemaVersion = 1

// Record is the persisted, non-secret onboarding record.
type Record struct {
	SchemaVersion   int    `json:"schema_version"`
	ServerURL       string `json:"server_url"`
	NodeName        string `json:"node_name"`
	IP              string `json:"ip"`
	ServerPublicKey string `json:"server_public_key"`
	Endpoint        string `json:"endpoint"`
	Network         string `json:"network"`
}

// DefaultPath returns the per-user state file path: %LOCALAPPDATA%\lanweave\state.json on
// Windows, otherwise <UserConfigDir>/lanweave/state.json (dev).
func DefaultPath() (string, error) {
	var base string
	if runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
	}
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate config dir: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "lanweave", "state.json"), nil
}

// Exists reports whether a state file is present at path.
func Exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Load reads and validates the record. A missing file returns an error wrapping
// os.ErrNotExist; an incomplete record is rejected.
func Load(path string) (Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, fmt.Errorf("parse state: %w", err)
	}
	if r.SchemaVersion == 0 || r.ServerURL == "" || r.NodeName == "" || r.IP == "" {
		return Record{}, errors.New("state record is incomplete")
	}
	return r, nil
}

// Save writes the record atomically (temp file + rename) into a user-only directory. It
// defaults the schema version and never writes a secret.
func Save(path string, r Record) error {
	if r.SchemaVersion == 0 {
		r.SchemaVersion = SchemaVersion
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}

// Clear removes the state record; a missing file is not an error.
func Clear(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear state: %w", err)
	}
	return nil
}
