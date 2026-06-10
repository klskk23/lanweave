package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/config"
	"lanweave/internal/server/ipam"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

// inNS runs fn on a locked OS thread inside the given network namespace and
// returns its error. Sockets and netlink handles created inside keep working
// from any goroutine afterwards (they are bound to the namespace at creation).
func inNS(ns netns.NsHandle, fn func() error) error {
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		orig, err := netns.Get()
		if err != nil {
			errCh <- fmt.Errorf("get current ns: %w", err)
			return
		}
		defer orig.Close()
		if err := netns.Set(ns); err != nil {
			errCh <- fmt.Errorf("enter ns: %w", err)
			return
		}
		defer func() { _ = netns.Set(orig) }()
		errCh <- fn()
	}()
	return <-errCh
}

// newChildNS creates a new (anonymous) network namespace and returns a handle,
// leaving the calling thread in its original namespace.
func newChildNS(t *testing.T) netns.NsHandle {
	t.Helper()
	var handle netns.NsHandle
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		orig, err := netns.Get()
		if err != nil {
			done <- err
			return
		}
		defer orig.Close()
		h, err := netns.New() // creates AND enters
		if err != nil {
			done <- err
			return
		}
		handle = h
		done <- netns.Set(orig)
	}()
	if err := <-done; err != nil {
		t.Fatalf("create child netns: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

// peerNS is one simulated client: a child namespace connected to the root ns by
// a veth pair, running a kernel WireGuard interface toward the test server.
type peerNS struct {
	ns     netns.NsHandle
	vpnIP  netip.Addr
	pubKey string
}

// addVeth wires serverNS<->peer child ns with a /24 transfer net and returns
// the server-side IP. Both namespaces are private to the test: package-parallel
// `go test ./...` runs other suites in the shared root namespace whose
// drop-policy forward chains would eat this test's relayed packets.
func addVeth(t *testing.T, serverNS, ns netns.NsHandle, idx int) (hostIP, peerIP string) {
	t.Helper()
	host := fmt.Sprintf("ve%dh", idx)
	peer := fmt.Sprintf("ve%dp", idx)
	hostIP = fmt.Sprintf("10.9.%d.1", idx)
	peerIP = fmt.Sprintf("10.9.%d.2", idx)
	if err := inNS(serverNS, func() error {
		veth := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: host}, PeerName: peer}
		if err := netlink.LinkAdd(veth); err != nil {
			return fmt.Errorf("veth add: %w", err)
		}
		peerLink, err := netlink.LinkByName(peer)
		if err != nil {
			return fmt.Errorf("peer link: %w", err)
		}
		if err := netlink.LinkSetNsFd(peerLink, int(ns)); err != nil {
			return fmt.Errorf("move peer: %w", err)
		}
		hostLink, err := netlink.LinkByName(host)
		if err != nil {
			return err
		}
		addr, _ := netlink.ParseAddr(hostIP + "/24")
		if err := netlink.AddrAdd(hostLink, addr); err != nil {
			return fmt.Errorf("host addr: %w", err)
		}
		return netlink.LinkSetUp(hostLink)
	}); err != nil {
		t.Fatalf("server-side veth %d: %v", idx, err)
	}
	if err := inNS(ns, func() error {
		lo, err := netlink.LinkByName("lo")
		if err != nil {
			return err
		}
		if err := netlink.LinkSetUp(lo); err != nil {
			return err
		}
		pl, err := netlink.LinkByName(peer)
		if err != nil {
			return err
		}
		a, _ := netlink.ParseAddr(peerIP + "/24")
		if err := netlink.AddrAdd(pl, a); err != nil {
			return err
		}
		return netlink.LinkSetUp(pl)
	}); err != nil {
		t.Fatalf("configure peer ns: %v", err)
	}
	return hostIP, peerIP
}

// newPeerNS builds a complete simulated client: child ns + veth + kernel wg0
// pointed at the server, with routes for the VPN and synthetic pools.
func newPeerNS(t *testing.T, serverNS netns.NsHandle, idx int, vpnIP netip.Addr, serverPub string, serverPort int, extraLoAddrs []string) *peerNS {
	t.Helper()
	ns := newChildNS(t)
	hostIP, _ := addVeth(t, serverNS, ns, idx)

	key, _ := wgtypes.GeneratePrivateKey()
	pub := key.PublicKey()
	if err := inNS(ns, func() error {
		wgLink := &netlink.Wireguard{LinkAttrs: netlink.LinkAttrs{Name: "wg0"}}
		if err := netlink.LinkAdd(wgLink); err != nil {
			return fmt.Errorf("wg add: %w", err)
		}
		l, err := netlink.LinkByName("wg0")
		if err != nil {
			return err
		}
		a, _ := netlink.ParseAddr(vpnIP.String() + "/32")
		if err := netlink.AddrAdd(l, a); err != nil {
			return fmt.Errorf("wg addr: %w", err)
		}
		wgc, err := wgctrl.New()
		if err != nil {
			return err
		}
		defer wgc.Close()
		srvKey, err := wgtypes.ParseKey(serverPub)
		if err != nil {
			return err
		}
		keepalive := time.Second
		_, vpnNet, _ := net.ParseCIDR("100.127.0.0/16")
		_, synthNet, _ := net.ParseCIDR("100.100.0.0/16")
		if err := wgc.ConfigureDevice("wg0", wgtypes.Config{
			PrivateKey: &key,
			Peers: []wgtypes.PeerConfig{{
				PublicKey:                   srvKey,
				Endpoint:                    &net.UDPAddr{IP: net.ParseIP(hostIP), Port: serverPort},
				AllowedIPs:                  []net.IPNet{*vpnNet, *synthNet},
				PersistentKeepaliveInterval: &keepalive,
			}},
		}); err != nil {
			return fmt.Errorf("wg configure: %w", err)
		}
		if err := netlink.LinkSetUp(l); err != nil {
			return err
		}
		for _, cidr := range []string{"100.127.0.0/16", "100.100.0.0/16"} {
			_, dst, _ := net.ParseCIDR(cidr)
			if err := netlink.RouteAdd(&netlink.Route{LinkIndex: l.Attrs().Index, Dst: dst}); err != nil {
				return fmt.Errorf("route %s: %w", cidr, err)
			}
		}
		// Extra loopback addresses simulate the post-NETMAP local target: the
		// announcer answers on its synthetic addresses (the 032 OpenWrt client
		// will achieve the same with a real netmap rule).
		lo, _ := netlink.LinkByName("lo")
		for _, la := range extraLoAddrs {
			a, _ := netlink.ParseAddr(la)
			if err := netlink.AddrAdd(lo, a); err != nil {
				return fmt.Errorf("lo addr %s: %w", la, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("peer ns %d: %v", idx, err)
	}
	return &peerNS{ns: ns, vpnIP: vpnIP, pubKey: pub.String()}
}

// udpListen creates a UDP echo server socket inside the namespace.
func udpListen(t *testing.T, ns netns.NsHandle, addr string) *net.UDPConn {
	t.Helper()
	var conn *net.UDPConn
	if err := inNS(ns, func() error {
		ua, err := net.ResolveUDPAddr("udp4", addr)
		if err != nil {
			return err
		}
		conn, err = net.ListenUDP("udp4", ua)
		return err
	}); err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			n, raddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], raddr)
		}
	}()
	return conn
}

// udpEcho dials from inside the namespace (optionally from a specific local
// address) and reports whether an echo came back within the deadline. Several
// attempts cover the WireGuard handshake warm-up.
func udpEcho(t *testing.T, ns netns.NsHandle, local, remote string, attempts int) bool {
	t.Helper()
	var conn *net.UDPConn
	if err := inNS(ns, func() error {
		var laddr *net.UDPAddr
		if local != "" {
			var err error
			laddr, err = net.ResolveUDPAddr("udp4", local)
			if err != nil {
				return err
			}
		}
		raddr, err := net.ResolveUDPAddr("udp4", remote)
		if err != nil {
			return err
		}
		conn, err = net.DialUDP("udp4", laddr, raddr)
		return err
	}); err != nil {
		t.Fatalf("dial %s: %v", remote, err)
	}
	defer conn.Close()
	buf := make([]byte, 16)
	for i := 0; i < attempts; i++ {
		_, _ = conn.Write([]byte("ping"))
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if n, err := conn.Read(buf); err == nil && n > 0 {
			return true
		}
	}
	return false
}

// TestAnnounceEndToEnd is the SC-001 acceptance: real kernel WireGuard tunnels in
// child namespaces, real nftables forwarding on the "server" (the test's root
// namespace). A member reaches the announced synthetic address through the
// relay; an outsider (not in the zone) is dropped; a NEW flow initiated from the
// announced side toward a member is dropped (FR-013 one-way semantics).
func TestAnnounceEndToEnd(t *testing.T) {
	testutil.RequireNetAdmin(t)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	b := make([]byte, 3)
	_, _ = rand.Read(b)
	suffix := hex.EncodeToString(b)
	const serverPort = 51899

	// The whole relay lives in its own namespace: the shared root ns hosts other
	// packages' drop-policy tables during parallel `go test ./...`, which would
	// silently drop this test's forwarded packets.
	serverNS := newChildNS(t)
	serverInNS := func(name string, fn func() error) {
		t.Helper()
		if err := inNS(serverNS, fn); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	serverKey, _ := wgtypes.GeneratePrivateKey()
	wgCfg := config.WireGuardConfig{
		Network: "100.127.0.0/16", ListenPort: serverPort, Interface: "wga" + suffix,
		MTU: 1420, Endpoint: "irrelevant:1",
	}
	var srv *wg.Server
	synthPool := netip.MustParsePrefix("100.100.0.0/16")
	serverInNS("server wg+forward+route", func() error {
		lo, err := netlink.LinkByName("lo")
		if err != nil {
			return err
		}
		if err := netlink.LinkSetUp(lo); err != nil {
			return err
		}
		// Netlink/wgctrl sockets bind their namespace at creation, so srv keeps
		// operating on serverNS from any goroutine afterwards.
		srv, err = wg.EnsureInterface(wgCfg, serverKey, log)
		if err != nil {
			return fmt.Errorf("server wg: %w", err)
		}
		if err := netfw.EnableIPv4Forward(); err != nil {
			return fmt.Errorf("forward: %w", err)
		}
		return srv.EnsurePoolRoute(synthPool)
	})
	t.Cleanup(func() { _ = srv.Close() })

	// Three simulated clients. The announcer answers on the synthetic address
	// 100.100.0.50 (loopback-assigned = equivalent translation, see 032).
	synthetic := netip.MustParsePrefix("100.100.0.0/24")
	member := newPeerNS(t, serverNS, 1, netip.MustParseAddr("100.127.0.2"), srv.PublicKey().String(), serverPort, nil)
	announcer := newPeerNS(t, serverNS, 2, netip.MustParseAddr("100.127.0.3"), srv.PublicKey().String(), serverPort,
		[]string{"100.100.0.50/32"})
	outsider := newPeerNS(t, serverNS, 3, netip.MustParseAddr("100.127.0.4"), srv.PublicKey().String(), serverPort, nil)

	// Server peers: member and outsider carry their /32; the announcer also
	// carries the synthetic block (what SetPeerRoutes does in production). The
	// wgctrl handle inside srv is bound to serverNS, so no namespace dance here.
	if err := srv.ReplacePeers([]wg.PeerConfig{
		{PublicKey: member.pubKey, IP: member.vpnIP},
		{PublicKey: announcer.pubKey, IP: announcer.vpnIP, Routes: []netip.Prefix{synthetic}},
		{PublicKey: outsider.pubKey, IP: outsider.vpnIP},
	}); err != nil {
		t.Fatalf("server peers: %v", err)
	}

	// Isolation: zone 1 = {member, announcer} with the synthetic block routed;
	// the outsider is in no zone.
	table := "lwann" + suffix
	mgr := netfw.NewManager(table)
	serverInNS("nft rebuild", func() error {
		return mgr.Rebuild([]netfw.ZoneState{{
			ID:         1,
			MemberIPs:  []netip.Addr{member.vpnIP, announcer.vpnIP},
			RouteCIDRs: []netip.Prefix{synthetic},
		}}, log)
	})

	// The announcer answers on its synthetic address.
	udpListen(t, announcer.ns, "100.100.0.50:9100")
	// A member service, target of the reverse-initiation negative test.
	udpListen(t, member.ns, member.vpnIP.String()+":9200")

	// US1 / SC-001 positive: member ↔ announced subnet round trip via the relay.
	if !udpEcho(t, member.ns, "", "100.100.0.50:9100", 8) {
		t.Fatal("member could not reach the announced synthetic address through the relay")
	}
	// Isolation: an outsider (different/no zone) must be dropped.
	if udpEcho(t, outsider.ns, "", "100.100.0.50:9100", 4) {
		t.Fatal("outsider reached the announced subnet — zone isolation broken")
	}
	// FR-013 one-way: a NEW flow from the announced side toward a member has no
	// conntrack entry and must fall through to the drop policy.
	if udpEcho(t, announcer.ns, "100.100.0.50:9300", member.vpnIP.String()+":9200", 4) {
		t.Fatal("LAN-side initiated flow reached a member — one-way semantics broken")
	}
}

// TestAnnounceRestartRebuild is the SC-004 acceptance at the dataplane level:
// announcements seeded in the database are fully reconstructed by the startup
// rebuild — peer AllowedIPs carry the synthetic blocks and the zone routes sets
// match the attachments.
func TestAnnounceRestartRebuild(t *testing.T) {
	testutil.RequireNetAdmin(t)
	ctx := context.Background()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Seed: one user, an openwrt node in two zones, announcing one subnet into
	// both (shared synthetic block) and a second subnet into zone1 only.
	owner, _ := st.Users().CreateAdmin(ctx, "alice", "hash")
	first, last, _ := ipam.PoolRange("100.127.0.0/16")
	k, _ := wgtypes.GeneratePrivateKey()
	node, err := st.Nodes().Create(ctx, owner.ID, "router", k.PublicKey().String(), "openwrt", first, last, 0)
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	z1, _ := st.Zones().Create(ctx, owner.ID, "z1", "hash", 0)
	z2, _ := st.Zones().Create(ctx, owner.ID, "z2", "hash", 0)
	_ = st.Zones().Join(ctx, z1.ID, node.ID)
	_ = st.Zones().Join(ctx, z2.ID, node.ID)
	pool := netip.MustParsePrefix("100.100.0.0/16")
	a1, _, err := st.Announcements().Create(ctx, owner.ID, node.ID, ipam.BlockFromPrefix(netip.MustParsePrefix("192.168.1.0/24")), z1.ID, 0, pool)
	if err != nil {
		t.Fatalf("announce 1: %v", err)
	}
	if _, _, err := st.Announcements().Create(ctx, owner.ID, node.ID, ipam.BlockFromPrefix(netip.MustParsePrefix("192.168.1.0/24")), z2.ID, 0, pool); err != nil {
		t.Fatalf("announce 1 → z2: %v", err)
	}
	a2, _, err := st.Announcements().Create(ctx, owner.ID, node.ID, ipam.BlockFromPrefix(netip.MustParsePrefix("10.5.0.0/24")), z1.ID, 0, pool)
	if err != nil {
		t.Fatalf("announce 2: %v", err)
	}

	// Fresh dataplane (as a restarted server would build it), rebuilt from DB.
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	suffix := hex.EncodeToString(b)
	serverKey, _ := wgtypes.GeneratePrivateKey()
	wgCfg := config.WireGuardConfig{
		Network: "100.127.0.0/16", ListenPort: 0, Interface: "wgr" + suffix,
		MTU: 1420, Endpoint: "irrelevant:1",
	}
	srv, err := wg.EnsureInterface(wgCfg, serverKey, log)
	if err != nil {
		t.Fatalf("server wg: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(wgCfg.Interface); e == nil {
			_ = netlink.LinkDel(l)
		}
	})
	table := "lwrr" + suffix
	mgr := netfw.NewManager(table)
	t.Cleanup(func() {
		if conn, e := nftables.New(); e == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
			_ = conn.Flush()
		}
	})

	if err := rebuildNodePeers(ctx, st.Nodes(), st.Announcements(), srv, log); err != nil {
		t.Fatalf("rebuild peers: %v", err)
	}
	if err := rebuildZoneRules(ctx, st.Zones(), st.Announcements(), mgr, log); err != nil {
		t.Fatalf("rebuild rules: %v", err)
	}

	// Peer AllowedIPs: /32 + both synthetic blocks.
	wgc, err := wgctrl.New()
	if err != nil {
		t.Fatalf("wgctrl: %v", err)
	}
	defer wgc.Close()
	dev, err := wgc.Device(wgCfg.Interface)
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	if len(dev.Peers) != 1 {
		t.Fatalf("peers = %d, want 1", len(dev.Peers))
	}
	allowed := map[string]bool{}
	for _, a := range dev.Peers[0].AllowedIPs {
		allowed[a.String()] = true
	}
	for _, want := range []string{node.IP.String() + "/32", a1.Synthetic.Prefix().String(), a2.Synthetic.Prefix().String()} {
		if !allowed[want] {
			t.Errorf("rebuilt allowed ips %v missing %s", allowed, want)
		}
	}

	// Routes sets: z1 carries both blocks (4 interval elements), z2 one (2).
	conn, _ := nftables.New()
	count := func(zoneID int64) int {
		s, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: table}, fmt.Sprintf("zone_%d_routes", zoneID))
		if err != nil {
			t.Fatalf("routes set zone %d: %v", zoneID, err)
		}
		elems, _ := conn.GetSetElements(s)
		return len(elems)
	}
	if got := count(z1.ID); got != 4 {
		t.Errorf("z1 routes elements = %d, want 4", got)
	}
	if got := count(z2.ID); got != 2 {
		t.Errorf("z2 routes elements = %d, want 2", got)
	}
}
