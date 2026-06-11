package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/time/rate"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/router/natctl"
	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/status"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

// e2e is the three-namespace announcer topology: the full server stack lives in
// serverNS (its kernel does the zone forwarding, immune to other packages'
// tables in the shared root namespace), the router CLI/daemon/NAT live in
// routerNS, and a simulated zone member lives in memberNS.
type e2e struct {
	serverNS, memberNS netns.NsHandle
	url                string
	pin                string
	invite             func() string
	store              *store.Store
	mgr                *netfw.Manager
	srv                *wg.Server
	wgPort             int
	dataDir            string
	sink               *bytes.Buffer
}

const (
	e2eVPNPool   = "100.111.0.0/16" // private to this file (the 031 lesson)
	e2eSynthPool = "100.100.0.0/16"
	e2eTunIface  = "lwann0"
	e2eLANIface  = "lwlan0"
)

func buildAnnounceTopology(t *testing.T, annLimit int) *e2e {
	t.Helper()
	testutil.RequireNetAdmin(t)
	x := &e2e{
		serverNS: testutil.NewChildNS(t),
		memberNS: testutil.NewChildNS(t),
		dataDir:  filepath.Join(t.TempDir(), "lanweave"),
		sink:     &bytes.Buffer{},
	}
	// The ROUTER is the root test namespace: the CLI's HTTP client dials on
	// http.Transport's own goroutines, which a pinned-thread namespace cannot
	// cover — so everything the router does (HTTP, netlink, nftables, wg) must
	// live where ordinary goroutines live. Server and member get child
	// namespaces (the 030 pattern); all root-side resources use private names.
	rootNS, err := netns.Get()
	if err != nil {
		t.Fatalf("root ns: %v", err)
	}
	t.Cleanup(func() { _ = rootNS.Close() })
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	x.wgPort = 19000 + int(b[0])%1000
	sfx := hex.EncodeToString(b)
	// Unique names per test: the root-side peer outlives serverNS teardown
	// long enough to collide with the next test otherwise.
	rootVeth := "vr" + sfx
	t.Cleanup(func() {
		if l, err := netlink.LinkByName(rootVeth); err == nil {
			_ = netlink.LinkDel(l)
		}
	})
	testutil.ConnectNS(t, x.serverNS, rootNS, "vs"+sfx, "10.21.1.1/24", rootVeth, "10.21.1.2/24")
	testutil.ConnectNS(t, x.serverNS, x.memberNS, "vm"+sfx, "10.21.2.1/24", "vmp"+sfx, "10.21.2.2/24")

	// Full server stack inside serverNS. The wgctrl handle inside wg.Server and
	// the HTTPS listener bind serverNS at creation, so later use from any
	// goroutine still lands there. (The API handlers' per-call nftables conns do
	// NOT — the test re-syncs serverNS firewall state from the store below.)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if err := testutil.InNS(x.serverNS, func() error {
		st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = st.Close() })
		if err := st.Migrate(nil); err != nil {
			return err
		}
		hash, _ := auth.HashPassword("Admin-pass-123")
		admin, err := st.Users().CreateAdmin(context.Background(), "admin", hash)
		if err != nil {
			return err
		}
		x.invite = func() string {
			code, _, err := st.Invites().Create(context.Background(), admin.ID, 0)
			if err != nil {
				t.Fatalf("mint invite: %v", err)
			}
			return code
		}
		wgCfg := config.WireGuardConfig{
			Network: e2eVPNPool, ListenPort: x.wgPort, Interface: "wgsrv", MTU: 1420,
			Endpoint: fmt.Sprintf("10.21.1.1:%d", x.wgPort),
		}
		serverKey, _ := wgtypes.GeneratePrivateKey()
		srv, err := wg.EnsureInterface(wgCfg, serverKey, log)
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = srv.Close() })
		if err := netfw.EnableIPv4Forward(); err != nil {
			return err
		}
		if err := srv.EnsurePoolRoute(netip.MustParsePrefix(e2eSynthPool)); err != nil {
			return err
		}
		mgr := netfw.NewManager("lanweave_e2e")
		if err := mgr.Rebuild(nil, log); err != nil {
			return err
		}
		jwtMgr := auth.NewJWTManager("0123456789abcdef0123456789abcdef", time.Hour)
		router := api.NewRouter(api.Options{
			Version: "a32", Limiter: rate.NewLimiter(rate.Limit(10000), 10000), Logger: log,
			Store: st, JWT: jwtMgr, WG: srv, NetFW: mgr, WGConfig: wgCfg,
			Status:                     status.New(srv.Handshakes, status.DefaultInterval, log),
			AnnouncePool:               netip.MustParsePrefix(e2eSynthPool),
			MaxAnnouncedSubnetsPerUser: annLimit,
		})
		l, err := net.Listen("tcp", "10.21.1.1:0")
		if err != nil {
			return err
		}
		ts := httptest.NewUnstartedServer(router)
		ts.Listener = l
		ts.StartTLS()
		t.Cleanup(ts.Close)
		x.url = ts.URL
		x.pin = certFP(ts.Certificate())
		x.store, x.mgr, x.srv = st, mgr, srv
		return nil
	}); err != nil {
		t.Fatalf("server stack: %v", err)
	}

	// Router prep (root ns): forwarding + the simulated LAN (dummy; .50 is the
	// zero-config "NAS"). Private names; everything cleaned up.
	if err := netfw.EnableIPv4Forward(); err != nil {
		t.Fatalf("ip_forward: %v", err)
	}
	// API handler goroutines run on root-ns threads, so their incremental
	// firewall writes need a root-ns skeleton of the server table to land in
	// (it is discarded; serverNS holds the real one via syncServerNFT).
	if err := x.mgr.Rebuild(nil, log); err != nil {
		t.Fatalf("root skeleton table: %v", err)
	}
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: e2eLANIface}}); err != nil {
		t.Fatalf("lan dummy: %v", err)
	}
	t.Cleanup(func() {
		if l, err := netlink.LinkByName(e2eLANIface); err == nil {
			_ = netlink.LinkDel(l)
		}
		if l, err := netlink.LinkByName(e2eTunIface); err == nil {
			_ = netlink.LinkDel(l)
		}
		_ = natctl.Teardown(natctl.DefaultTable)
		// API handler goroutines run on root-ns threads; their incremental
		// nftables writes land in a root-ns copy of the server table.
		if conn, err := nftables.New(); err == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: "lanweave_e2e"})
			_ = conn.Flush()
		}
	})
	lan, err := netlink.LinkByName(e2eLANIface)
	if err != nil {
		t.Fatalf("lan link: %v", err)
	}
	for _, cidr := range []string{"192.168.50.1/24", "192.168.50.50/32"} {
		a, _ := netlink.ParseAddr(cidr)
		if err := netlink.AddrAdd(lan, a); err != nil {
			t.Fatalf("lan addr %s: %v", cidr, err)
		}
	}
	if err := netlink.LinkSetUp(lan); err != nil {
		t.Fatalf("lan up: %v", err)
	}
	return x
}

// rcli runs the CLI entry point as the router (root ns, private interface).
func (x *e2e) rcli(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	full := append([]string{"--data-dir", x.dataDir, "--iface", e2eTunIface}, args...)
	code := run(full, strings.NewReader(stdin), &out, &errb)
	x.sink.WriteString(out.String())
	x.sink.WriteString(errb.String())
	return code, out.String(), errb.String()
}

func (x *e2e) mustRCLI(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	code, out, errb := x.rcli(t, stdin, args...)
	if code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, errb)
	}
	return out
}

// inRouterAPI runs direct apiclient calls from the router side (root ns).
func (x *e2e) inRouterAPI(t *testing.T, fn func(c *apiclient.Client) error) {
	t.Helper()
	if err := fn(apiclient.New(x.url, apiclient.WithPinnedCert(x.pin))); err != nil {
		t.Fatalf("router api: %v", err)
	}
}

// syncServerNFT rebuilds serverNS's zone firewall from the store — the test
// equivalent of the app's startup rebuild (API handler goroutines run on
// arbitrary threads, so their incremental nftables writes land in the root
// namespace; serverNS forwarding state is reconstructed from the source of
// truth instead, exercising the same derived-state machinery).
func (x *e2e) syncServerNFT(t *testing.T) {
	t.Helper()
	if err := testutil.InNS(x.serverNS, func() error {
		ctx := context.Background()
		states, err := x.store.Zones().AllForRebuild(ctx)
		if err != nil {
			return err
		}
		routes, err := x.store.Announcements().RoutesByZone(ctx)
		if err != nil {
			return err
		}
		zones := make([]netfw.ZoneState, 0, len(states))
		for _, s := range states {
			zones = append(zones, netfw.ZoneState{ID: s.ID, MemberIPs: s.MemberIPs, RouteCIDRs: routes[s.ID]})
		}
		return x.mgr.Rebuild(zones, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}); err != nil {
		t.Fatalf("sync server nft: %v", err)
	}
}

// onboardRouterNS walks the CLI onboarding inside routerNS and joins a zone.
func (x *e2e) onboardRouterNS(t *testing.T, zone string) {
	t.Helper()
	x.mustRCLI(t, "", "setup", "--server", x.url, "--pin", x.pin)
	x.mustRCLI(t, "Password123", "register-account", "--username", "alice", "--invite", x.invite())
	x.mustRCLI(t, "", "register", "--name", "gw")
	x.mustRCLI(t, "zonepass-1", "zone", "create", zone)
}

// stopDaemon returns an idempotent stopper (safe as both defer and inline —
// the buffered done value is only harvested once).
func stopDaemon(cancel context.CancelFunc, done chan int) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// startRouterDaemon runs cmdRunCtx as the router with a fast reconcile.
func (x *e2e) startRouterDaemon(t *testing.T) (context.CancelFunc, chan int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		e := &env{
			dataDir: x.dataDir, iface: e2eTunIface,
			stdin: strings.NewReader(""), stdout: x.sink, stderr: x.sink,
			reconcileEvery: 300 * time.Millisecond,
		}
		done <- cmdRunCtx(e, ctx)
	}()
	return cancel, done
}

// addMember registers a second account's node and joins it to the zone, then
// builds its kernel WireGuard peer in memberNS (the 030 simulated-member
// pattern: AllowedIPs/routes include the synthetic pool — what 033 will do for
// real clients).
func (x *e2e) addMember(t *testing.T, zone string) netip.Addr {
	t.Helper()
	key, _ := wgtypes.GeneratePrivateKey()
	var memberIP netip.Addr
	var serverPub string
	x.inRouterAPI(t, func(c *apiclient.Client) error {
		if err := c.Register(x.invite(), "bob", "Password456"); err != nil {
			return err
		}
		if err := c.Login("bob", "Password456"); err != nil {
			return err
		}
		node, err := c.RegisterNode("laptop", key.PublicKey().String())
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(node.IP)
		if err != nil {
			return err
		}
		memberIP = ip
		info, err := c.ServerInfo()
		if err != nil {
			return err
		}
		serverPub = info.PublicKey
		return c.JoinZone(zone, node.ID, "zonepass-1")
	})

	if err := testutil.InNS(x.memberNS, func() error {
		if err := netlink.LinkAdd(&netlink.Wireguard{LinkAttrs: netlink.LinkAttrs{Name: "wg0"}}); err != nil {
			return err
		}
		l, err := netlink.LinkByName("wg0")
		if err != nil {
			return err
		}
		a, _ := netlink.ParseAddr(memberIP.String() + "/32")
		if err := netlink.AddrAdd(l, a); err != nil {
			return err
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
		_, vpnNet, _ := net.ParseCIDR(e2eVPNPool)
		_, synthNet, _ := net.ParseCIDR(e2eSynthPool)
		if err := wgc.ConfigureDevice("wg0", wgtypes.Config{
			PrivateKey: &key,
			Peers: []wgtypes.PeerConfig{{
				PublicKey:                   srvKey,
				Endpoint:                    &net.UDPAddr{IP: net.ParseIP("10.21.2.1"), Port: x.wgPort},
				AllowedIPs:                  []net.IPNet{*vpnNet, *synthNet},
				PersistentKeepaliveInterval: &keepalive,
			}},
		}); err != nil {
			return err
		}
		if err := netlink.LinkSetUp(l); err != nil {
			return err
		}
		for _, cidr := range []string{e2eVPNPool, e2eSynthPool} {
			_, dst, _ := net.ParseCIDR(cidr)
			if err := netlink.RouteAdd(&netlink.Route{LinkIndex: l.Attrs().Index, Dst: dst}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("member peer: %v", err)
	}
	return memberIP
}

// nsUDPListen / nsUDPEcho mirror the 030 helpers but namespace-parameterized.
func nsUDPListen(t *testing.T, ns netns.NsHandle, addr string) *net.UDPConn {
	t.Helper()
	var conn *net.UDPConn
	if err := testutil.InNS(ns, func() error {
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

func nsUDPEcho(t *testing.T, ns netns.NsHandle, local, remote string, attempts int) bool {
	t.Helper()
	var conn *net.UDPConn
	if err := testutil.InNS(ns, func() error {
		var laddr *net.UDPAddr
		if local != "" {
			var err error
			if laddr, err = net.ResolveUDPAddr("udp4", local); err != nil {
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

// rootUDPListen/rootUDPEcho are the root-ns (router-side) twins of the ns helpers.
func rootUDPListen(t *testing.T, addr string) {
	t.Helper()
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp4", ua)
	if err != nil {
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
}

func rootUDPEcho(t *testing.T, local, remote string, attempts int) bool {
	t.Helper()
	laddr, err := net.ResolveUDPAddr("udp4", local)
	if err != nil {
		t.Fatal(err)
	}
	raddr, err := net.ResolveUDPAddr("udp4", remote)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp4", laddr, raddr)
	if err != nil {
		return false
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

func (x *e2e) routerRules(t *testing.T) []natctl.Rule {
	t.Helper()
	rules, err := natctl.Current(natctl.DefaultTable)
	if err != nil {
		t.Fatalf("router rules: %v", err)
	}
	return rules
}

func (x *e2e) announcementCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := x.store.DB().QueryRow(`SELECT COUNT(*) FROM announcements`).Scan(&n); err != nil {
		t.Fatalf("count announcements: %v", err)
	}
	return n
}

// TestAnnounceE2E is the US1 acceptance (SC-001/SC-002): announce → member
// reaches the LAN host via the synthetic address through real kernel WG +
// forwarding + prefix-DNAT + masquerade; list shows the mapping; a second zone
// reuses the block; the LAN side cannot initiate (FR-009); a daemon restart
// reconciles the rules back.
func TestAnnounceE2E(t *testing.T) {
	x := buildAnnounceTopology(t, 0)
	x.onboardRouterNS(t, "homelab")
	cancel, done := x.startRouterDaemon(t)
	stop1 := stopDaemon(cancel, done)
	defer stop1()

	memberIP := x.addMember(t, "homelab")
	rootUDPListen(t, "192.168.50.50:9500")
	memberSvc := nsUDPListen(t, x.memberNS, memberIP.String()+":9600")
	_ = memberSvc

	out := x.mustRCLI(t, "", "announce", "add", "192.168.50.0/24", "--zone", "homelab")
	if !strings.Contains(out, "192.168.50.0/24 -> 100.100.") {
		t.Fatalf("announce output = %q, want mapping", out)
	}
	synthetic := strings.Fields(strings.SplitAfter(out, "-> ")[1])[0]
	syntheticHost := strings.TrimSuffix(synthetic, ".0/24") + ".50"
	x.syncServerNFT(t)

	// SC-001: the member reaches the zero-config LAN host via the synthetic
	// address, through the relay and the router's translation.
	if !nsUDPEcho(t, x.memberNS, "", syntheticHost+":9500", 10) {
		t.Fatal("member could not reach the announced LAN host via the synthetic address")
	}

	// list: mapping visible, rules in sync.
	out = x.mustRCLI(t, "", "announce", "list")
	if !strings.Contains(out, "192.168.50.0/24") || !strings.Contains(out, synthetic) || !strings.Contains(out, "ok") {
		t.Errorf("announce list = %q", out)
	}

	// Second zone reuses the synthetic block (030 semantics).
	x.mustRCLI(t, "zonepass-1", "zone", "create", "office")
	out = x.mustRCLI(t, "", "announce", "add", "192.168.50.0/24", "--zone", "office")
	if !strings.Contains(out, synthetic) {
		t.Errorf("second zone output = %q, want reused %s", out, synthetic)
	}
	if rules := x.routerRules(t); len(rules) != 1 {
		t.Errorf("router rules = %d, want 1 (reused block)", len(rules))
	}

	// FR-009: the LAN side cannot initiate toward a member.
	if rootUDPEcho(t, "192.168.50.50:9700", memberIP.String()+":9600", 3) {
		t.Fatal("LAN-side initiated flow reached a member — one-way semantics broken")
	}

	// SC-002 (restart): wipe the local table, restart the daemon, reconcile
	// rebuilds it and reachability returns.
	stop1()
	if err := natctl.Teardown(natctl.DefaultTable); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	cancel2, done2 := x.startRouterDaemon(t)
	defer stopDaemon(cancel2, done2)()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(x.routerRules(t)) == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(x.routerRules(t)) != 1 {
		t.Fatal("reconcile did not rebuild the translation rules after restart")
	}
	if !nsUDPEcho(t, x.memberNS, "", syntheticHost+":9500", 10) {
		t.Fatal("reachability did not recover after daemon restart")
	}
}

// TestAnnounceFailuresAndCompensation is the SC-003/SC-004 + FR-008 matrix:
// server rejections leave zero local residue; a synthetic/local clash and an
// injected local failure both compensate the remote attachment away.
func TestAnnounceFailuresAndCompensation(t *testing.T) {
	x := buildAnnounceTopology(t, 1) // announcement quota 1
	x.onboardRouterNS(t, "homelab")

	for _, tc := range []struct {
		name, subnet, zone, want string
	}{
		{"public range", "8.8.8.0/24", "homelab", "private"},
		{"unknown zone", "192.168.60.0/24", "ghost", "not"},
	} {
		code, _, errb := x.rcli(t, "", "announce", "add", tc.subnet, "--zone", tc.zone)
		if code == 0 || !strings.Contains(strings.ToLower(errb), tc.want) {
			t.Errorf("%s = %d %q", tc.name, code, errb)
		}
	}
	if n := x.announcementCount(t); n != 0 {
		t.Fatalf("announcements after rejections = %d, want 0", n)
	}
	if rules := x.routerRules(t); len(rules) != 0 {
		t.Fatalf("local rules after rejections = %v, want none", rules)
	}

	// FR-008: a pre-existing local address inside the synthetic pool clashes
	// with the allocated block (first-fit → 100.100.0.0/24); the command fails
	// and compensates the remote attachment.
	lan, err := netlink.LinkByName(e2eLANIface)
	if err != nil {
		t.Fatal(err)
	}
	clashAddr, _ := netlink.ParseAddr("100.100.0.77/32")
	if err := netlink.AddrAdd(lan, clashAddr); err != nil {
		t.Fatal(err)
	}
	code, _, errb := x.rcli(t, "", "announce", "add", "192.168.50.0/24", "--zone", "homelab")
	if code == 0 || !strings.Contains(errb, "overlaps local network") {
		t.Fatalf("clash = %d %q, want local-overlap failure", code, errb)
	}
	if n := x.announcementCount(t); n != 0 {
		t.Fatalf("announcement survived compensation: %d", n)
	}
	// Remove the clashing address for the rest of the test.
	if err := netlink.AddrDel(lan, clashAddr); err != nil {
		t.Fatal(err)
	}

	// Self-overlap + quota via the real server.
	x.mustRCLI(t, "", "announce", "add", "192.168.50.0/24", "--zone", "homelab")
	if code, _, errb := x.rcli(t, "", "announce", "add", "192.168.50.128/25", "--zone", "homelab"); code == 0 || !strings.Contains(errb, "overlap") {
		t.Errorf("self overlap = %d %q", code, errb)
	}
	if code, _, errb := x.rcli(t, "", "announce", "add", "10.9.0.0/24", "--zone", "homelab"); code == 0 || !strings.Contains(errb, "limit") {
		t.Errorf("quota = %d %q", code, errb)
	}

	// SC-004: injected local failure → remote attachment compensated away.
	x.mustRCLI(t, "", "announce", "remove", "192.168.50.0/24", "--zone", "homelab")
	eFail := &env{dataDir: x.dataDir, iface: e2eTunIface, nat: failingNAT{},
		stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	if code := cmdAnnounce(eFail, []string{"add", "192.168.50.0/24", "--zone", "homelab"}); code == 0 {
		t.Fatal("announce with failing NAT succeeded")
	}
	if n := x.announcementCount(t); n != 0 {
		t.Fatalf("announcement survived NAT-failure compensation: %d", n)
	}
}

// TestAnnounceLifecycle is the US2 acceptance: withdraw semantics, partial
// withdrawal, third-party server-side removal converging within a reconcile
// period, and logout tearing the table down.
func TestAnnounceLifecycle(t *testing.T) {
	x := buildAnnounceTopology(t, 0)
	x.onboardRouterNS(t, "homelab")
	x.mustRCLI(t, "zonepass-1", "zone", "create", "office")
	cancel, done := x.startRouterDaemon(t)
	stop := stopDaemon(cancel, done)
	defer stop()

	x.mustRCLI(t, "", "announce", "add", "192.168.50.0/24", "--zone", "homelab")
	x.mustRCLI(t, "", "announce", "add", "192.168.50.0/24", "--zone", "office")

	// Partial withdrawal keeps the rule (block still attached to homelab).
	x.mustRCLI(t, "", "announce", "remove", "192.168.50.0/24", "--zone", "office")
	if rules := x.routerRules(t); len(rules) != 1 {
		t.Fatalf("rules after partial withdrawal = %v, want 1", rules)
	}
	// Removing a non-announced subnet fails cleanly.
	if code, _, _ := x.rcli(t, "", "announce", "remove", "10.99.0.0/24", "--zone", "homelab"); code == 0 {
		t.Error("removing a non-announced subnet succeeded")
	}

	// Third-party removal (server-side, outside this CLI) converges via the
	// reconcile loop within its period.
	var annID int64
	x.inRouterAPI(t, func(c *apiclient.Client) error {
		if err := c.Login("alice", "Password123"); err != nil {
			return err
		}
		list, err := c.ListAnnouncements("homelab")
		if err != nil {
			return err
		}
		if len(list.Announcements) != 1 {
			return fmt.Errorf("server list = %d", len(list.Announcements))
		}
		annID = list.Announcements[0].ID
		return c.DeleteAnnouncement("homelab", annID)
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(x.routerRules(t)) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if rules := x.routerRules(t); len(rules) != 0 {
		t.Fatalf("rules did not converge after third-party removal: %v", rules)
	}

	// logout tears the translation table down entirely.
	stop()
	x.mustRCLI(t, "", "logout")
	conn, err := nftables.New()
	if err != nil {
		t.Fatal(err)
	}
	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if tb.Name == natctl.DefaultTable {
			t.Fatal("translation table survived logout")
		}
	}
}

// failingNAT drives the FR-005 compensation path.
type failingNAT struct{}

func (failingNAT) Rebuild(netip.Prefix, []natctl.Rule) error { return errors.New("nft exploded") }
func (failingNAT) Teardown() error                           { return nil }
func (failingNAT) Current() ([]natctl.Rule, error)           { return nil, nil }
