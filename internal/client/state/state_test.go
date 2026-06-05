package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lanweave/internal/client/state"
)

func TestStateRoundTripAndClear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lanweave", "state.json")

	if state.Exists(path) {
		t.Fatal("state should not exist before save")
	}
	if _, err := state.Load(path); err == nil {
		t.Error("loading a missing record should error")
	}

	rec := state.Record{
		ServerURL: "https://vpn.example.com", NodeName: "laptop", IP: "100.127.0.5",
		ServerPublicKey: "srv-pub", Endpoint: "vpn.example.com:51820", Network: "100.127.0.0/16",
	}
	if err := state.Save(path, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !state.Exists(path) {
		t.Fatal("state should exist after save")
	}
	got, err := state.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SchemaVersion != state.SchemaVersion || got.IP != rec.IP || got.ServerPublicKey != "srv-pub" || got.Network != rec.Network {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Atomic overwrite with a new value.
	rec2 := rec
	rec2.IP = "100.127.0.9"
	if err := state.Save(path, rec2); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got2, _ := state.Load(path); got2.IP != "100.127.0.9" {
		t.Errorf("overwrite not applied: %s", got2.IP)
	}

	// No secret material in the file.
	raw, _ := os.ReadFile(path)
	if strings.Contains(strings.ToLower(string(raw)), "private") || strings.Contains(strings.ToLower(string(raw)), "token") {
		t.Errorf("state file must not contain secret material: %s", raw)
	}

	// Clear, then clearing a missing file is a no-op.
	if err := state.Clear(path); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if state.Exists(path) {
		t.Error("state should be gone after clear")
	}
	if err := state.Clear(path); err != nil {
		t.Errorf("clearing a missing file should be nil, got %v", err)
	}
}

func TestLoadRejectsIncomplete(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Load(bad); err == nil {
		t.Error("an incomplete record should be rejected")
	}
}

func TestDefaultPath(t *testing.T) {
	p, err := state.DefaultPath()
	if err != nil {
		t.Fatalf("default path: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(p), "lanweave/state.json") {
		t.Errorf("unexpected default path: %s", p)
	}
}
