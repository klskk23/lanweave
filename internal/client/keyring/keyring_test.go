package keyring_test

import (
	"errors"
	"runtime"
	"testing"

	"lanweave/internal/client/keyring"
)

func TestFake(t *testing.T) {
	f := keyring.NewFake()
	if _, err := f.Get("k"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("missing key: got %v, want ErrNotFound", err)
	}
	if err := f.Set("k", []byte("secret")); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, err := f.Get("k")
	if err != nil || string(v) != "secret" {
		t.Fatalf("get: %q %v", v, err)
	}
	// Get must return an independent copy.
	v[0] = 'X'
	if v2, _ := f.Get("k"); string(v2) != "secret" {
		t.Error("Get must return a copy, not the stored slice")
	}
	if err := f.Delete("k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.Get("k"); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("key should be gone after delete")
	}
}

// TestDevBackend exercises the non-Windows file backend in an isolated config dir.
func TestDevBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dev file backend is non-Windows; DPAPI backend is validated on Windows")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := keyring.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set(keyring.DeviceKeyName, []byte("priv")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if v, err := s.Get(keyring.DeviceKeyName); err != nil || string(v) != "priv" {
		t.Fatalf("get: %q %v", v, err)
	}
	if _, err := s.Get("absent"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("absent: got %v, want ErrNotFound", err)
	}
	if err := s.Delete(keyring.DeviceKeyName); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(keyring.DeviceKeyName); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("key should be gone after delete")
	}
}
