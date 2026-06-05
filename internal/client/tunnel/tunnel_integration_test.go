//go:build linux

package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/netip"
	"os/exec"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/client/state"
	"lanweave/internal/server/config"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

// realServerTunnel stands up a real kernel-WireGuard server, registers a client device as a
// peer, and returns a Tunnel configured to reach it. The whole thing runs in one network
// namespace under `unshare -rUn`.
func realServerTunnel(t *testing.T) *Tunnel {
	t.Helper()
	testutil.RequireNetAdmin(t)
	// A fresh network namespace (unshare -rUn) starts with loopback down, which breaks
	// reaching the server on 127.0.0.1; bring it up.
	if lo, err := netlink.LinkByName("lo"); err == nil {
		_ = netlink.LinkSetUp(lo)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	srvName := "wgs" + hex.EncodeToString(b)
	srvCfg := config.WireGuardConfig{Network: "100.127.0.0/16", ListenPort: 0, Interface: srvName, MTU: 1420, Endpoint: "vpn.example.com:51820"}
	srvKey, _ := wgtypes.GeneratePrivateKey()
	srv, err := wg.EnsureInterface(srvCfg, srvKey, log)
	if err != nil {
		t.Fatalf("server interface: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(srvName); e == nil {
			_ = netlink.LinkDel(l)
		}
	})

	// The client device + its server-side peer (allowed IP = 100.127.0.2).
	clientKey, _ := wgtypes.GeneratePrivateKey()
	clientIP := netip.MustParseAddr("100.127.0.2")
	if err := srv.AddPeer(clientKey.PublicKey().String(), clientIP); err != nil {
		t.Fatalf("add client peer: %v", err)
	}

	// Discover the server's real UDP listen port (config used 0 = ephemeral).
	wgc, err := wgctrl.New()
	if err != nil {
		t.Fatalf("wgctrl: %v", err)
	}
	t.Cleanup(func() { _ = wgc.Close() })
	dev, err := wgc.Device(srvName)
	if err != nil {
		t.Fatalf("read server device: %v", err)
	}

	rec := state.Record{
		ServerURL: "https://vpn.example.com", NodeName: "laptop", IP: clientIP.String(),
		ServerPublicKey: srv.PublicKey().String(),
		Endpoint:        netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(dev.ListenPort)).String(),
		Network:         "100.127.0.0/16",
	}
	tn := New(rec, clientKey.String())
	t.Cleanup(func() {
		_ = tn.Close()
		if l, e := netlink.LinkByName("lanweave0"); e == nil {
			_ = netlink.LinkDel(l)
		}
	})
	return tn
}

// TestIntegrationConnectReachesServer (privileged): a real user-space client tunnel
// connects to the real server and a handshake completes — the reliable proof of
// reachability (M1) — with an optional best-effort ping.
func TestIntegrationConnectReachesServer(t *testing.T) {
	tn := realServerTunnel(t)

	if err := tn.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if tn.State() != Connected {
		t.Fatalf("state = %v, want Connected (handshake completed)", tn.State())
	}
	if tn.InterfaceName() == "" {
		t.Error("expected an interface name while connected")
	}

	// Best-effort reachability confirmation: skip (don't fail) if ping is unavailable.
	if _, err := exec.LookPath("ping"); err == nil {
		if out, err := exec.Command("ping", "-c1", "-W2", "100.127.0.1").CombinedOutput(); err != nil {
			t.Logf("best-effort ping 100.127.0.1 did not succeed (handshake already proved reachability): %v\n%s", err, out)
		}
	}
}

// TestIntegrationDisconnectTeardown (privileged): after connecting, Disconnect removes the
// tun interface and returns to Disconnected; a second Disconnect is a no-op.
func TestIntegrationDisconnectTeardown(t *testing.T) {
	tn := realServerTunnel(t)
	if err := tn.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	ifName := tn.InterfaceName()
	if ifName == "" || !interfacePresent(ifName) {
		t.Fatalf("interface %q should exist while connected", ifName)
	}

	if err := tn.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if tn.State() != Disconnected {
		t.Errorf("state = %v, want Disconnected", tn.State())
	}
	// Give the kernel a moment to remove the closed tun interface.
	gone := false
	for range 20 {
		if !interfacePresent(ifName) {
			gone = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !gone {
		t.Errorf("interface %q still present after disconnect", ifName)
	}
	if err := tn.Disconnect(); err != nil {
		t.Errorf("second disconnect should be a no-op, got %v", err)
	}
}
