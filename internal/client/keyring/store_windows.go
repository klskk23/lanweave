//go:build windows

package keyring

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows backend: the secret is encrypted with the current user's credentials via DPAPI
// (CryptProtectData) and the ciphertext is stored under %LOCALAPPDATA%\lanweave\secrets.
// The private key therefore never sits in a plaintext file (FR-005); only the current
// Windows user can decrypt it.

var (
	crypt32            = windows.NewLazySystemDLL("crypt32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(d []byte) dataBlob {
	if len(d) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(d)), pbData: &d[0]}
}

func (b dataBlob) bytes() []byte {
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

func protect(plain []byte) ([]byte, error) {
	in := newBlob(plain)
	var out dataBlob
	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func unprotect(enc []byte) ([]byte, error) {
	in := newBlob(enc)
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

// Open returns the DPAPI-backed store under %LOCALAPPDATA%\lanweave\secrets.
func Open() (Store, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("locate config dir: %w", err)
		}
		base = dir
	}
	return &dpapiStore{dir: filepath.Join(base, "lanweave", "secrets")}, nil
}

type dpapiStore struct{ dir string }

func (s *dpapiStore) path(name string) string {
	return filepath.Join(s.dir, url.PathEscape(name)+".dpapi")
}

func (s *dpapiStore) Set(name string, secret []byte) error {
	enc, err := protect(secret)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	return os.WriteFile(s.path(name), enc, 0o600)
}

func (s *dpapiStore) Get(name string) ([]byte, error) {
	enc, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return unprotect(enc)
}

func (s *dpapiStore) Delete(name string) error {
	if err := os.Remove(s.path(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
