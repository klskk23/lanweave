//go:build !windows

package keyring

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// Open returns the non-Windows dev backend: secrets are written under the user config
// directory with user-only permissions. This exists so the client can be developed on
// non-Windows hosts; production secret storage is the Windows DPAPI backend. Tests use
// the in-memory Fake, not this backend.
func Open() (Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate config dir: %w", err)
	}
	return &fileStore{dir: filepath.Join(dir, "lanweave", "secrets")}, nil
}

// OpenAt returns a file-backed store rooted at the given directory (created
// 0700 on first Set, files 0600). The OpenWrt router client uses this with its
// data dir — routers have no OS keyring, so a root-only file is the accepted
// storage (DESIGN §11).
func OpenAt(dir string) Store {
	return &fileStore{dir: dir}
}

type fileStore struct{ dir string }

func (s *fileStore) path(name string) string {
	return filepath.Join(s.dir, url.PathEscape(name))
}

func (s *fileStore) Set(name string, secret []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	return os.WriteFile(s.path(name), secret, 0o600)
}

func (s *fileStore) Get(name string) ([]byte, error) {
	b, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *fileStore) Delete(name string) error {
	if err := os.Remove(s.path(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
