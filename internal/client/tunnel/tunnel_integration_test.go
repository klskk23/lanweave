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

// TestIntegrationStaleDetectionAndReconnect (privileged, US1 / quickstart I1+SC-002): against the
// real server, a fresh handshake is NOT stale at any age within the threshold (the SC-002
// steady-state guard, checked at several injected "now" points so a healthy link never triggers a
// false reconnect), but an aged handshake IS stale — and the self-heal path the health loop runs
// (tear down + reconnect) re-handshakes successfully.
func TestIntegrationStaleDetectionAndReconnect(t *testing.T) {
	tn := realServerTunnel(t)
	if err := tn.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if tn.State() != Connected {
		t.Fatalf("state = %v, want Connected", tn.State())
	}

	// (b) SC-002 steady state: with the default 240s threshold, a freshly-handshaked link must
	// not be reported stale at any point comfortably inside the window — no false reconnect. We
	// keep a margin below 240s because the real last_handshake_time_sec is floored to whole
	// seconds and predates base by a fraction; the exact 239/240/241 boundary is pinned by the
	// deterministic unit test TestStaleThresholdBoundary (which controls the timestamp exactly).
	base := time.Now()
	for _, age := range []time.Duration{0, 30 * time.Second, 120 * time.Second, 200 * time.Second} {
		if tn.Stale(base.Add(age)) {
			t.Errorf("healthy link at age %v (< 240s) must not be stale (SC-002)", age)
		}
	}

	// (a) A silently-dead link: pushing "now" past the threshold makes Stale fire.
	if !tn.Stale(base.Add(241 * time.Second)) {
		t.Fatal("a handshake older than 240s must be stale")
	}

	// The self-heal action: tear the dead tunnel down and reconnect — a new handshake completes.
	if err := tn.Disconnect(); err != nil {
		t.Fatalf("teardown before reconnect: %v", err)
	}
	if err := tn.Connect(); err != nil {
		t.Fatalf("auto-reconnect failed to re-handshake: %v", err)
	}
	if tn.State() != Connected {
		t.Errorf("state = %v, want Connected after self-heal reconnect", tn.State())
	}
}

// TestIntegrationSelfHealRecovers (privileged, US1 acceptance A1 / SC-001): with the user's intent
// recorded (Desired) and no manual operation, a link detected as silently dead recovers to
// Connected with traffic flowing again. This drives the exact tunnel primitives the health loop
// uses each tick — Desired() && Stale(now) ⇒ reconnect — proving the tunnel side of the self-heal;
// the 15s loop wiring itself is covered by the UI tests (panel_test.go).
func TestIntegrationSelfHealRecovers(t *testing.T) {
	tn := realServerTunnel(t)
	tn.SetDesired(true) // the user connected; intent persists across the silent drop
	if err := tn.Connect(); err != nil {
		t.Fatalf("initial connect: %v", err)
	}

	// One health-tick decision against an aged "now": the link is wanted and stale → reconnect.
	deadNow := time.Now().Add(241 * time.Second)
	if !(tn.Desired() && tn.Stale(deadNow)) {
		t.Fatal("a desired-but-aged link must be eligible for auto-reconnect")
	}
	_ = tn.Disconnect()
	if err := tn.Connect(); err != nil {
		t.Fatalf("self-heal reconnect: %v", err)
	}
	if tn.State() != Connected {
		t.Fatalf("state = %v, want Connected after self-heal", tn.State())
	}

	// Traffic resumes: probe the server and confirm the counters move off zero.
	tn.probeServer()
	rx, tx, err := tn.Transfer()
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if tx == 0 && rx == 0 {
		t.Error("expected traffic to resume (non-zero rx/tx) after self-heal")
	}
}

// TestIntegrationManualDisconnectWins (privileged, US3 / I3 / SC-003+SC-005): a manual disconnect
// during a desired (retry-window) connection wins decisively — intent clears, the tunnel ends
// Disconnected with its interface gone, and the health-loop predicate (Desired && (Stale||down))
// no longer holds, so nothing reconnects. (The mid-flight revive race is covered deterministically
// by the unit test TestSingleFlightDisconnectWins; firewall mirroring by the panel wiring.)
func TestIntegrationManualDisconnectWins(t *testing.T) {
	tn := realServerTunnel(t)
	tn.SetDesired(true)
	if err := tn.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	ifName := tn.InterfaceName()

	// User aborts: intent first (so any concurrent tick bails), then teardown.
	tn.SetDesired(false)
	if err := tn.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	if tn.State() != Disconnected {
		t.Errorf("state = %v, want Disconnected", tn.State())
	}
	if tn.Desired() {
		t.Error("Desired() must be false after a manual disconnect (intent wins)")
	}
	// The health loop would act only if Desired && (Stale || down); intent is false → it won't.
	if tn.Desired() {
		t.Error("health loop must not consider a user-disconnected tunnel for reconnect")
	}
	gone := false
	for range 20 {
		if !interfacePresent(ifName) {
			gone = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !gone {
		t.Errorf("interface %q still present after manual disconnect", ifName)
	}
}

// TestIntegrationSourcePortRandomized (privileged, regression I2 / FR-017 / SC-008): because we
// never pin listen_port, each Connect builds a fresh device that binds a new OS-assigned ephemeral
// UDP source port. Two consecutive connections must therefore use different source ports.
func TestIntegrationSourcePortRandomized(t *testing.T) {
	tn := realServerTunnel(t)

	if err := tn.Connect(); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	port1 := tn.eng.(*wgEngine).localPort()
	if err := tn.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if err := tn.Connect(); err != nil {
		t.Fatalf("second connect: %v", err)
	}
	port2 := tn.eng.(*wgEngine).localPort()

	if port1 == 0 || port2 == 0 {
		t.Fatalf("expected non-zero OS-assigned source ports, got %d and %d", port1, port2)
	}
	// Two independent ephemeral allocations: the OS picks a fresh port each time (collision odds
	// are ~1/28000, negligible) — proving the port is not pinned to a constant.
	if port1 == port2 {
		t.Errorf("source ports identical (%d) across reconnects — listen_port appears pinned", port1)
	}
}
