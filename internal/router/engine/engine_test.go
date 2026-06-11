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
