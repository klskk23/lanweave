package wg_test

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/config"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

func quietLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// uniqueIfaceName returns a fresh interface name (<=15 chars) so concurrent/repeat
// runs don't collide.
func uniqueIfaceName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "wgt" + hex.EncodeToString(b) // "wgt" + 8 hex = 11 chars
}

func testWGConfig(name string) config.WireGuardConfig {
	return config.WireGuardConfig{
		Network:    "100.127.0.0/16",
		ListenPort: 0, // 0 = let the kernel pick, avoids port collisions in tests
		Interface:  name,
		MTU:        1420,
	}
}

// TestEnsureInterfaceUnprivilegedFails asserts FR-015: without CAP_NET_ADMIN the
// setup returns a clear error (no panic, no partial state). Runs only when the
// process is unprivileged (the inverse of the integration tests below).
func TestEnsureInterfaceUnprivilegedFails(t *testing.T) {
	if testutil.HasNetAdmin() {
		t.Skip("has CAP_NET_ADMIN; this test asserts the unprivileged failure path")
	}
	key, _ := wgtypes.GeneratePrivateKey()
	if _, err := wg.EnsureInterface(testWGConfig("wgtest-noperm"), key, quietLog()); err == nil {
		t.Fatal("expected a privilege error when unprivileged")
	}
}

// TestEnsureInterfaceCreatesAndAdopts is the privileged integration test (US1/US3):
// create the interface for real, verify its attributes and zero peers, then a
// second call adopts it (same ifindex, same pubkey) rather than recreating.
func TestEnsureInterfaceCreatesAndAdopts(t *testing.T) {
	testutil.RequireNetAdmin(t)
	name := uniqueIfaceName(t)
	cfg := testWGConfig(name)
	key, _ := wgtypes.GeneratePrivateKey()

	srv, err := wg.EnsureInterface(cfg, key, quietLog())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(name); e == nil {
			_ = netlink.LinkDel(l)
		}
	})

	// Interface exists, is wireguard, is up, has the first pool address.
	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("link not found after create: %v", err)
	}
	if link.Type() != "wireguard" {
		t.Fatalf("type = %q, want wireguard", link.Type())
	}
	idx1 := link.Attrs().Index
	addrs, _ := netlink.AddrList(link, netlink.FAMILY_V4)
	foundAddr := false
	for _, a := range addrs {
		if a.IP.String() == "100.127.0.1" {
			foundAddr = true
		}
	}
	if !foundAddr {
		t.Errorf("interface missing 100.127.0.1; addrs=%v", addrs)
	}

	// Device has the server key and ZERO peers (FR-007).
	wgc, err := wgctrl.New()
	if err != nil {
		t.Fatalf("wgctrl: %v", err)
	}
	defer wgc.Close()
	dev, err := wgc.Device(name)
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	if dev.PublicKey != key.PublicKey() {
		t.Error("device public key does not match the configured key")
	}
	if len(dev.Peers) != 0 {
		t.Errorf("expected zero peers, got %d", len(dev.Peers))
	}
	if srv.PublicKey() != key.PublicKey() {
		t.Error("Server.PublicKey mismatch")
	}

	// Adopt: a second EnsureInterface keeps the SAME ifindex (FR-008/FR-016/SC-006).
	srv2, err := wg.EnsureInterface(cfg, key, quietLog())
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	defer srv2.Close()
	link2, _ := netlink.LinkByName(name)
	if link2.Attrs().Index != idx1 {
		t.Errorf("ifindex changed on restart (%d -> %d): interface was recreated, not adopted",
			idx1, link2.Attrs().Index)
	}
}

// TestEnsureInterfaceWrongTypeConflict asserts the conflict path: a non-wireguard
// device holding the name is a clear error.
func TestEnsureInterfaceWrongTypeConflict(t *testing.T) {
	testutil.RequireNetAdmin(t)
	name := uniqueIfaceName(t)

	// Create a dummy (non-wireguard) link with that name.
	la := netlink.NewLinkAttrs()
	la.Name = name
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: la}); err != nil {
		t.Skipf("cannot create dummy link in this environment: %v", err)
	}
	t.Cleanup(func() {
		if l, e := netlink.LinkByName(name); e == nil {
			_ = netlink.LinkDel(l)
		}
	})

	key, _ := wgtypes.GeneratePrivateKey()
	if _, err := wg.EnsureInterface(testWGConfig(name), key, quietLog()); err == nil {
		t.Fatal("expected conflict error for non-wireguard interface of the same name")
	}
}
