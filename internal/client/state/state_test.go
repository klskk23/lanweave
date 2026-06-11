package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestSchemaV1LoadsWithDefaultedNewFields proves a legacy v1 record (written before the
// 018 schema bump, with none of the new keys) still loads, with the new fields defaulted to
// "unpinned" and "firewall off" (FR-020 / SC-005 / SC-006).
func TestSchemaV1LoadsWithDefaultedNewFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.json")
	v1 := `{"schema_version":1,"server_url":"https://vpn.example.com","node_name":"laptop","ip":"100.127.0.5","network":"100.127.0.0/16"}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := state.Load(path)
	if err != nil {
		t.Fatalf("v1 record should still load: %v", err)
	}
	if got.PinnedCertSHA256 != "" {
		t.Errorf("v1 record should load unpinned, got %q", got.PinnedCertSHA256)
	}
	if got.FirewallAllowVPN {
		t.Error("v1 record should load with the firewall preference off")
	}
}

// TestNewFieldsRoundTrip proves the two 018 fields survive a Save/Load and that Save writes
// the current schema version (2).
func TestNewFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lanweave", "state.json")
	rec := state.Record{
		ServerURL: "https://vpn.example.com", NodeName: "laptop", IP: "100.127.0.5",
		Network:          "100.127.0.0/16",
		PinnedCertSHA256: "ab12cd34",
		FirewallAllowVPN: true,
	}
	if err := state.Save(path, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := state.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.PinnedCertSHA256 != "ab12cd34" || !got.FirewallAllowVPN {
		t.Errorf("new fields not preserved: %+v", got)
	}
	if got.SchemaVersion != 3 {
		t.Errorf("expected schema version 3 after save, got %d", got.SchemaVersion)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"schema_version": 3`) {
		t.Errorf("state file should record schema_version 3: %s", raw)
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

// TestLoadV2RecordDefaultsNodeID proves a pre-031 (v2) record loads unchanged
// with the new NodeID defaulted to zero ("unknown").
func TestLoadV2RecordDefaultsNodeID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	v2 := `{"schema_version":2,"server_url":"https://vpn.example.com","node_name":"laptop","ip":"100.127.0.7","server_public_key":"pk","endpoint":"vpn.example.com:51820","network":"100.127.0.0/16","pinned_cert_sha256":"abc"}`
	if err := os.WriteFile(path, []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := state.Load(path)
	if err != nil {
		t.Fatalf("load v2: %v", err)
	}
	if r.NodeID != 0 {
		t.Errorf("NodeID = %d, want 0 for v2 record", r.NodeID)
	}
	if r.ServerURL != "https://vpn.example.com" || r.PinnedCertSHA256 != "abc" {
		t.Errorf("v2 fields lost: %+v", r)
	}
}

// TestSaveConcurrent proves the temp+rename atomic write keeps the record
// readable under concurrent writers (FR-014: CLI and daemon may both save).
func TestSaveConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	base := state.Record{ServerURL: "https://s", NodeName: "n", IP: "100.127.0.2"}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := base
			r.NodeID = int64(i + 1)
			if err := state.Save(path, r); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	r, err := state.Load(path)
	if err != nil {
		t.Fatalf("load after concurrent saves: %v", err)
	}
	if r.NodeID < 1 || r.NodeID > 16 {
		t.Errorf("record corrupted: %+v", r)
	}
}
