package wg_test

import (
	"net/netip"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

func TestPeerLifecycle(t *testing.T) {
	testutil.RequireNetAdmin(t)
	name := uniqueIfaceName(t)
	serverKey, _ := wgtypes.GeneratePrivateKey()
	srv, err := wg.EnsureInterface(testWGConfig(name), serverKey, quietLog())
	if err != nil {
		t.Fatalf("ensure interface: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(name); e == nil {
			_ = netlink.LinkDel(l)
		}
	})

	wgc, err := wgctrl.New()
	if err != nil {
		t.Fatalf("wgctrl: %v", err)
	}
	defer wgc.Close()

	peerKey, _ := wgtypes.GeneratePrivateKey()
	peerPub := peerKey.PublicKey()
	ip := netip.MustParseAddr("100.127.0.2")

	// Add a peer.
	if err := srv.AddPeer(peerPub.String(), ip); err != nil {
		t.Fatalf("add peer: %v", err)
	}
	dev, _ := wgc.Device(name)
	p := findPeer(dev.Peers, peerPub)
	if p == nil {
		t.Fatal("peer not present after AddPeer")
	}
	if len(p.AllowedIPs) != 1 || p.AllowedIPs[0].String() != "100.127.0.2/32" {
		t.Errorf("allowed ips = %v, want [100.127.0.2/32]", p.AllowedIPs)
	}

	// Remove it.
	if err := srv.RemovePeer(peerPub.String()); err != nil {
		t.Fatalf("remove peer: %v", err)
	}
	dev, _ = wgc.Device(name)
	if findPeer(dev.Peers, peerPub) != nil {
		t.Error("peer still present after RemovePeer")
	}

	// ReplacePeers sets exactly the given set.
	k1, _ := wgtypes.GeneratePrivateKey()
	k2, _ := wgtypes.GeneratePrivateKey()
	if err := srv.ReplacePeers([]wg.PeerConfig{
		{PublicKey: k1.PublicKey().String(), IP: netip.MustParseAddr("100.127.0.5")},
		{PublicKey: k2.PublicKey().String(), IP: netip.MustParseAddr("100.127.0.6")},
	}); err != nil {
		t.Fatalf("replace peers: %v", err)
	}
	dev, _ = wgc.Device(name)
	if len(dev.Peers) != 2 {
		t.Fatalf("expected 2 peers after ReplacePeers, got %d", len(dev.Peers))
	}
}

// TestHandshakesReadsRealPeers (privileged) asserts Handshakes() reads the live
// kernel device: a freshly added peer that has never completed a handshake appears
// with the zero time (so the status tracker would treat it offline), and a key that
// is not a peer is absent from the map. A literal online=true needs a real
// handshaking client and is covered by the manual quickstart.
func TestHandshakesReadsRealPeers(t *testing.T) {
	testutil.RequireNetAdmin(t)
	name := uniqueIfaceName(t)
	serverKey, _ := wgtypes.GeneratePrivateKey()
	srv, err := wg.EnsureInterface(testWGConfig(name), serverKey, quietLog())
	if err != nil {
		t.Fatalf("ensure interface: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(name); e == nil {
			_ = netlink.LinkDel(l)
		}
	})

	// No peers yet → empty map (and no error reading the device).
	hs, err := srv.Handshakes()
	if err != nil {
		t.Fatalf("handshakes (empty): %v", err)
	}
	if len(hs) != 0 {
		t.Fatalf("expected 0 handshakes before any peer, got %d", len(hs))
	}

	peerKey, _ := wgtypes.GeneratePrivateKey()
	peerPub := peerKey.PublicKey().String()
	if err := srv.AddPeer(peerPub, netip.MustParseAddr("100.127.0.2")); err != nil {
		t.Fatalf("add peer: %v", err)
	}

	hs, err = srv.Handshakes()
	if err != nil {
		t.Fatalf("handshakes: %v", err)
	}
	ts, ok := hs[peerPub]
	if !ok {
		t.Fatal("added peer missing from Handshakes()")
	}
	if !ts.IsZero() {
		t.Errorf("never-handshaked peer should report zero time, got %v", ts)
	}
	stranger, _ := wgtypes.GeneratePrivateKey()
	if _, present := hs[stranger.PublicKey().String()]; present {
		t.Error("a non-peer key should not appear in Handshakes()")
	}
}

func findPeer(peers []wgtypes.Peer, pub wgtypes.Key) *wgtypes.Peer {
	for i := range peers {
		if peers[i].PublicKey == pub {
			return &peers[i]
		}
	}
	return nil
}
