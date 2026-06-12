package engine_test

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/router/engine"
	"lanweave/internal/testutil"
)

// TestUpDownLifecycle (privileged) asserts the real kernel state the engine
// builds: interface + address + VPN route + keepalive'd server peer; refusal to
// overwrite an existing interface; idempotent teardown.
func TestUpDownLifecycle(t *testing.T) {
	testutil.RequireNetAdmin(t)

	devKey, _ := wgtypes.GeneratePrivateKey()
	srvKey, _ := wgtypes.GeneratePrivateKey()
	cfg := engine.Config{
		Iface:        "lwenginetest",
		PrivateKey:   devKey.String(),
		Address:      netip.MustParseAddr("100.127.0.9"),
		Network:      netip.MustParsePrefix("100.127.0.0/16"),
		ServerPubKey: srvKey.PublicKey().String(),
		Endpoint:     "127.0.0.1:51820",
		Keepalive:    25 * time.Second,
	}
	e := engine.New(cfg)
	t.Cleanup(func() { _ = e.Down() })

	if err := e.Up(); err != nil {
		t.Fatalf("up: %v", err)
	}

	link, err := netlink.LinkByName(cfg.Iface)
	if err != nil {
		t.Fatalf("interface missing: %v", err)
	}
	addrs, _ := netlink.AddrList(link, netlink.FAMILY_V4)
	if len(addrs) != 1 || addrs[0].IP.String() != "100.127.0.9" {
		t.Errorf("addresses = %v, want 100.127.0.9/32", addrs)
	}
	routes, _ := netlink.RouteList(link, netlink.FAMILY_V4)
	foundRoute := false
	for _, r := range routes {
		if r.Dst != nil && r.Dst.String() == "100.127.0.0/16" {
			foundRoute = true
		}
	}
	if !foundRoute {
		t.Errorf("VPN route missing: %v", routes)
	}

	wgc, err := wgctrl.New()
	if err != nil {
		t.Fatalf("wgctrl: %v", err)
	}
	defer wgc.Close()
	dev, err := wgc.Device(cfg.Iface)
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	if len(dev.Peers) != 1 {
		t.Fatalf("peers = %d, want 1", len(dev.Peers))
	}
	p := dev.Peers[0]
	if p.PublicKey.String() != cfg.ServerPubKey {
		t.Errorf("peer key mismatch")
	}
	if p.PersistentKeepaliveInterval != 25*time.Second {
		t.Errorf("keepalive = %v, want 25s", p.PersistentKeepaliveInterval)
	}
	if len(p.AllowedIPs) != 1 || p.AllowedIPs[0].String() != "100.127.0.0/16" {
		t.Errorf("allowed ips = %v, want VPN pool", p.AllowedIPs)
	}

	// Never-handshaked tunnel reports disconnected, zero handshake time.
	if hs, err := e.LastHandshake(); err != nil || !hs.IsZero() {
		t.Errorf("last handshake = (%v, %v), want zero", hs, err)
	}
	if e.Connected(3 * time.Minute) {
		t.Error("Connected() = true for a never-handshaked tunnel")
	}

	// Refuse to overwrite an existing interface.
	if err := engine.New(cfg).Up(); !errors.Is(err, engine.ErrIfaceExists) {
		t.Errorf("second Up err = %v, want ErrIfaceExists", err)
	}

	if err := e.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := netlink.LinkByName(cfg.Iface); err == nil {
		t.Error("interface still present after Down")
	}
	// Idempotent teardown.
	if err := e.Down(); err != nil {
		t.Errorf("second down: %v", err)
	}
}

// TestSetRoutesLifecycle (privileged) covers the 033 consumer side: extra
// synthetic blocks land in the server peer's AllowedIPs and the kernel route
// table, shrink cleanly, are idempotent, skip locally-conflicting prefixes,
// and vanish on Down.
func TestSetRoutesLifecycle(t *testing.T) {
	e, iface, srvPub := upTestEngine(t)

	s1 := netip.MustParsePrefix("100.105.1.0/24")
	s2 := netip.MustParsePrefix("100.105.2.0/24")

	applied, err := e.SetRoutes([]netip.Prefix{s1, s2})
	if err != nil || len(applied) != 2 {
		t.Fatalf("SetRoutes = %v (%v), want both", applied, err)
	}
	if !hasAllowedIP(t, iface, srvPub, s1) || !hasAllowedIP(t, iface, srvPub, s2) {
		t.Fatal("AllowedIPs missing synthetic blocks")
	}
	if !kernelRoutePresent(t, iface, s1) || !kernelRoutePresent(t, iface, s2) {
		t.Fatal("kernel routes missing")
	}

	// Shrink: s2 disappears from both.
	if _, err := e.SetRoutes([]netip.Prefix{s1}); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	if hasAllowedIP(t, iface, srvPub, s2) || kernelRoutePresent(t, iface, s2) {
		t.Fatal("s2 survived shrink")
	}
	if !hasAllowedIP(t, iface, srvPub, s1) || !kernelRoutePresent(t, iface, s1) {
		t.Fatal("s1 lost during shrink")
	}

	// Idempotent: same set, route table unchanged.
	before := kernelRouteCount(t, iface)
	if _, err := e.SetRoutes([]netip.Prefix{s1}); err != nil {
		t.Fatalf("idempotent: %v", err)
	}
	if kernelRouteCount(t, iface) != before {
		t.Fatal("idempotent SetRoutes changed routes")
	}

	// Conflict gate: a prefix overlapping a local network is skipped.
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "engclash0"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if l, e := netlink.LinkByName("engclash0"); e == nil {
			_ = netlink.LinkDel(l)
		}
	})
	cl, _ := netlink.LinkByName("engclash0")
	ca, _ := netlink.ParseAddr("100.105.9.1/24")
	_ = netlink.AddrAdd(cl, ca)
	_ = netlink.LinkSetUp(cl)
	clash := netip.MustParsePrefix("100.105.9.0/24")
	applied, err = e.SetRoutes([]netip.Prefix{s1, clash})
	if err != nil || len(applied) != 1 || applied[0] != s1 {
		t.Fatalf("conflict gate: applied = %v (%v), want only s1", applied, err)
	}

	// Down: zero residue.
	if err := e.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if len(e.Routes()) != 0 {
		t.Fatal("route memory survived Down")
	}
}

// upTestEngine brings a fresh engine up on a private iface/network and returns
// it with the interface name and server public key.
func upTestEngine(t *testing.T) (*engine.Engine, string, string) {
	t.Helper()
	testutil.RequireNetAdmin(t)
	devKey, _ := wgtypes.GeneratePrivateKey()
	srvKey, _ := wgtypes.GeneratePrivateKey()
	cfg := engine.Config{
		Iface:        "lwengroutes",
		PrivateKey:   devKey.String(),
		Address:      netip.MustParseAddr("100.105.0.9"),
		Network:      netip.MustParsePrefix("100.105.0.0/20"),
		ServerPubKey: srvKey.PublicKey().String(),
		Endpoint:     "127.0.0.1:51821",
		Keepalive:    25 * time.Second,
	}
	e := engine.New(cfg)
	t.Cleanup(func() { _ = e.Down() })
	if err := e.Up(); err != nil {
		t.Fatalf("up: %v", err)
	}
	return e, cfg.Iface, cfg.ServerPubKey
}

func hasAllowedIP(t *testing.T, iface, srvPub string, p netip.Prefix) bool {
	t.Helper()
	wgc, err := wgctrl.New()
	if err != nil {
		t.Fatal(err)
	}
	defer wgc.Close()
	dev, err := wgc.Device(iface)
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range dev.Peers {
		if peer.PublicKey.String() != srvPub {
			continue
		}
		for _, a := range peer.AllowedIPs {
			if a.String() == p.Masked().String() {
				return true
			}
		}
	}
	return false
}

func kernelRoutePresent(t *testing.T, iface string, p netip.Prefix) bool {
	t.Helper()
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return false
	}
	routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if r.Dst != nil && r.Dst.String() == p.Masked().String() {
			return true
		}
	}
	return false
}

func kernelRouteCount(t *testing.T, iface string) int {
	t.Helper()
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return 0
	}
	routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	return len(routes)
}
