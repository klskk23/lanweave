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

func findPeer(peers []wgtypes.Peer, pub wgtypes.Key) *wgtypes.Peer {
	for i := range peers {
		if peers[i].PublicKey == pub {
			return &peers[i]
		}
	}
	return nil
}
