package wg_test

import (
	"os"
	"path/filepath"
	"testing"

	"lanweave/internal/server/wg"
)

func TestLoadOrGenerateKeyGeneratesThenLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wg_private")

	k1, generated, err := wg.LoadOrGenerateKey(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !generated {
		t.Error("first call should report generated=true")
	}

	// File must be owner-only.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file perm = %o, want 600", perm)
	}

	// Second call loads the SAME key, never regenerating.
	k2, generated2, err := wg.LoadOrGenerateKey(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if generated2 {
		t.Error("second call should report generated=false")
	}
	if k1 != k2 {
		t.Error("key changed across loads; identity must be stable")
	}
}

func TestLoadOrGenerateKeyCorruptFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wg_private")
	if err := os.WriteFile(path, []byte("this is not a valid key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	_, _, err := wg.LoadOrGenerateKey(path)
	if err == nil {
		t.Fatal("expected error for corrupt key file")
	}

	// MUST NOT regenerate: the file is unchanged.
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("corrupt key file must not be overwritten/regenerated")
	}
}

func TestLoadOrGenerateKeyTightensBroadPerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wg_private")
	// Generate a valid key, then loosen its perms.
	if _, _, err := wg.LoadOrGenerateKey(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := wg.LoadOrGenerateKey(path); err != nil {
		t.Fatalf("load with broad perms: %v", err)
	}
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm after load = %o, want 600 (should be tightened)", perm)
	}
}
