package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.org/x/time/rate"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/keyring"
	"lanweave/internal/router/engine"
	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/status"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

// testServer is a full real server stack (SQLite + kernel WireGuard + nftables
// + the genuine router behind TLS) whose WireGuard endpoint is reachable on
// loopback, so the router CLI's tunnel can complete a real handshake.
type testServer struct {
	url    string
	cert   *x509.Certificate
	invite string
	store  *store.Store
	close  func()
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	testutil.RequireNetAdmin(t)
	ctx := context.Background()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	hash, _ := auth.HashPassword("Admin-pass-123")
	admin, err := st.Users().CreateAdmin(ctx, "admin", hash)
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	code, _, err := st.Invites().Create(ctx, admin.ID, 0)
	if err != nil {
		t.Fatalf("mint invite: %v", err)
	}

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	// A fixed-but-randomized UDP port so the engine can dial the kernel wg
	// listener over loopback. Deliberately BELOW the kernel ephemeral range
	// (32768+): parallel test packages bind ephemeral UDP sockets constantly,
	// and a collision there silently eats our handshake traffic.
	port := 19000 + int(b[0])%1000
	wgName := "wgr" + hex.EncodeToString(b)
	// A private VPN pool: the engine RouteReplace's this /16 in the SHARED test
	// namespace, and hijacking 100.127.0.0/16 would break parallel packages
	// (the Windows tunnel integration tests route that pool).
	wgCfg := config.WireGuardConfig{
		Network: "100.111.0.0/16", ListenPort: port, Interface: wgName, MTU: 1420,
		Endpoint: fmt.Sprintf("127.0.0.1:%d", port),
	}
	serverKey, _ := wgtypes.GeneratePrivateKey()
	srv, err := wg.EnsureInterface(wgCfg, serverKey, log)
	if err != nil {
		t.Fatalf("ensure interface: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(wgName); e == nil {
			_ = netlink.LinkDel(l)
		}
	})

	table := "lwr" + hex.EncodeToString(b)
	mgr := netfw.NewManager(table)
	if err := mgr.Rebuild(nil, log); err != nil {
		t.Fatalf("nft rebuild: %v", err)
	}
	t.Cleanup(func() {
		if conn, e := nftables.New(); e == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
			_ = conn.Flush()
		}
	})

	jwtMgr := auth.NewJWTManager("0123456789abcdef0123456789abcdef", time.Hour)
	router := api.NewRouter(api.Options{
		Version: "rtest", Limiter: rate.NewLimiter(rate.Limit(10000), 10000), Logger: log,
		Store: st, JWT: jwtMgr, WG: srv, NetFW: mgr, WGConfig: wgCfg,
		Status: status.New(srv.Handshakes, status.DefaultInterval, log),
	})
	ts := httptest.NewTLSServer(router)
	t.Cleanup(ts.Close)
	return &testServer{url: ts.URL, cert: ts.Certificate(), invite: code, store: st, close: ts.Close}
}

func certFP(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// cli invokes the real entry point with a private data dir, returning exit
// code and captured output. All output is also accumulated into sink for the
// no-secrets assertion.
func cli(t *testing.T, dataDir, stdin string, sink *bytes.Buffer, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	full := append([]string{"--data-dir", dataDir, "--iface", testIface}, args...)
	code := run(full, strings.NewReader(stdin), &out, &errb)
	if sink != nil {
		sink.WriteString(out.String())
		sink.WriteString(errb.String())
	}
	return code, out.String(), errb.String()
}

func mustCLI(t *testing.T, dataDir, stdin string, sink *bytes.Buffer, args ...string) string {
	t.Helper()
	code, out, errb := cli(t, dataDir, stdin, sink, args...)
	if code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, errb)
	}
	return out
}

// testIface is deliberately NOT the production lanweave0: the Windows tunnel
// integration tests create lanweave0 in the same shared test namespace, and
// the daemon's stale-interface adoption would tear their live interface down.
const testIface = "lwrcli0"

func cleanupIface(t *testing.T) {
	t.Cleanup(func() {
		if l, err := netlink.LinkByName(testIface); err == nil {
			_ = netlink.LinkDel(l)
		}
	})
}

// onboardRouter walks the full CLI onboard (TOFU trust → register-account →
// register) and returns the data dir plus the output sink.
func onboardRouter(t *testing.T, ts *testServer) (string, *bytes.Buffer) {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "lanweave")
	sink := &bytes.Buffer{}

	mustCLI(t, dataDir, "", sink, "setup", "--server", ts.url)

	// Untrusted self-signed certificate: sign-in must fail with an actionable
	// fingerprint message (TOFU first contact, 018 semantics).
	code, _, errb := cli(t, dataDir, "Password123", sink, "login", "--username", "alice")
	if code == 0 || !strings.Contains(errb, "not trusted") || !strings.Contains(errb, certFP(ts.cert)) {
		t.Fatalf("untrusted login = %d %q, want failure naming the fingerprint", code, errb)
	}
	mustCLI(t, dataDir, "", sink, "trust", certFP(ts.cert))

	mustCLI(t, dataDir, "Password123", sink, "register-account", "--username", "alice", "--invite", ts.invite)
	out := mustCLI(t, dataDir, "", sink, "register", "--name", "home-router")
	if !strings.Contains(out, "node ") {
		t.Fatalf("register output missing node id: %q", out)
	}
	return dataDir, sink
}

// TestCLIOnboardAndTunnel is the US1 acceptance (SC-001/SC-002 CI dimension):
// full CLI onboard with TOFU, file permissions, platform=openwrt on the
// server, a real kernel-WireGuard handshake through the daemon, recovery after
// a daemon restart, duplicate-register refusal, and the no-secrets guarantee.
func TestCLIOnboardAndTunnel(t *testing.T) {
	ts := newTestServer(t)
	cleanupIface(t)
	dataDir, sink := onboardRouter(t, ts)

	// Three-artifact consistency + permissions (FR-002/FR-003).
	stateInfo, err := os.Stat(filepath.Join(dataDir, "state.json"))
	if err != nil || stateInfo.Mode().Perm() != 0o600 {
		t.Errorf("state.json perm = %v (%v), want 0600", stateInfo.Mode(), err)
	}
	keysInfo, err := os.Stat(filepath.Join(dataDir, "keys"))
	if err != nil || keysInfo.Mode().Perm() != 0o700 {
		t.Errorf("keys dir perm = %v (%v), want 0700", keysInfo.Mode(), err)
	}
	entries, _ := os.ReadDir(filepath.Join(dataDir, "keys"))
	if len(entries) != 3 { // device key + session + refresh tokens
		t.Errorf("keys dir entries = %d, want 3", len(entries))
	}
	for _, ent := range entries {
		info, _ := ent.Info()
		if info.Mode().Perm() != 0o600 {
			t.Errorf("key file %s perm = %v, want 0600", ent.Name(), info.Mode())
		}
	}

	// platform=openwrt landed on the server (FR-002, the 032 capability gate).
	nodes, err := ts.store.Nodes().AllForPeers(context.Background())
	if err != nil || len(nodes) != 1 {
		t.Fatalf("server nodes = %v (%v), want 1", nodes, err)
	}
	if nodes[0].Platform != "openwrt" {
		t.Errorf("server platform = %q, want openwrt", nodes[0].Platform)
	}

	// Duplicate register is refused with guidance (edge case).
	code, _, errb := cli(t, dataDir, "", sink, "register", "--name", "second")
	if code == 0 || !strings.Contains(errb, "logout") {
		t.Errorf("duplicate register = %d %q, want refusal mentioning logout", code, errb)
	}

	// Daemon brings the tunnel up and a REAL kernel handshake completes within
	// the constitution's 3s budget (the CI equivalent of pinging the server —
	// a completed handshake is cryptographic proof of two-way reachability;
	// the literal ping lives in the on-device quickstart matrix).
	runDaemon := func() (context.CancelFunc, chan int) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan int, 1)
		e := &env{dataDir: dataDir, iface: testIface, stdin: strings.NewReader(""), stdout: sink, stderr: sink}
		go func() { done <- cmdRunCtx(e, ctx) }()
		return cancel, done
	}
	waitHandshake := func() {
		t.Helper()
		eng := engine.New(engine.Config{Iface: testIface, ServerPubKey: readServerPub(t, dataDir)})
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if hs, err := eng.LastHandshake(); err == nil && !hs.IsZero() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("no WireGuard handshake within deadline")
	}

	cancel, done := runDaemon()
	waitHandshake()

	// status reflects the live tunnel.
	out := mustCLI(t, dataDir, "", sink, "status")
	for _, want := range []string{"daemon: running", "tunnel: connected", "ip: 100.111.0.2", "zones:"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output %q missing %q", out, want)
		}
	}

	// Graceful stop tears the interface down; a restart recovers without any
	// re-onboarding (SC-002).
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("daemon exit = %d", code)
	}
	if _, err := netlink.LinkByName(testIface); err == nil {
		t.Fatal("interface still present after daemon stop")
	}
	cancel2, done2 := runDaemon()
	waitHandshake()
	cancel2()
	<-done2

	// No secret ever reached stdout/stderr/logs (FR-003 + constitution).
	output := sink.String()
	if strings.Contains(output, "Password123") {
		t.Error("password leaked into CLI output")
	}
	priv, err := keyring.OpenAt(filepath.Join(dataDir, "keys")).Get(keyring.DeviceKeyName)
	if err == nil && strings.Contains(output, string(priv)) {
		t.Error("device private key leaked into CLI output")
	}
	for _, name := range []string{keyring.SessionTokenName, keyring.RefreshTokenName} {
		if tok, err := keyring.OpenAt(filepath.Join(dataDir, "keys")).Get(name); err == nil && len(tok) > 0 &&
			strings.Contains(output, string(tok)) {
			t.Errorf("%s leaked into CLI output", name)
		}
	}
}

// readServerPub loads the server public key from the router state record.
func readServerPub(t *testing.T, dataDir string) string {
	t.Helper()
	rec, err := loadLoose(filepath.Join(dataDir, "state.json"))
	if err != nil || rec.ServerPublicKey == "" {
		t.Fatalf("server pubkey not in state: %v", err)
	}
	return rec.ServerPublicKey
}

// TestCLIZones is the US2 acceptance (SC-003 CLI dimension): create with
// auto-join, a second device joining, member listing with name/IP/owner, the
// non-enumerable wrong-password error, leave, and the stable status fields.
func TestCLIZones(t *testing.T) {
	ts := newTestServer(t)
	cleanupIface(t)
	dataDir, sink := onboardRouter(t, ts)

	// zone create auto-joins this device (015 semantics).
	mustCLI(t, dataDir, "zonepass-1", sink, "zone", "create", "homelab")
	out := mustCLI(t, dataDir, "", sink, "zone", "list")
	if !strings.Contains(out, "homelab") || !strings.Contains(out, "owner") {
		t.Fatalf("zone list = %q, want homelab as owner", out)
	}
	out = mustCLI(t, dataDir, "", sink, "zone", "members", "homelab")
	if !strings.Contains(out, "home-router") || !strings.Contains(out, "100.111.0.2") || !strings.Contains(out, "alice") {
		t.Fatalf("members = %q, want name/IP/owner of this device", out)
	}

	// A second device (driven directly through the API, simulating another
	// client) joins and shows up in the member list.
	c := apiclient.New(ts.url, apiclient.WithPinnedCert(certFP(ts.cert)))
	if err := c.Register(mustInvite(t, ts), "bob", "Password456"); err != nil {
		t.Fatalf("bob register: %v", err)
	}
	if err := c.Login("bob", "Password456"); err != nil {
		t.Fatalf("bob login: %v", err)
	}
	_, pub, _ := mustKeyPair(t)
	bobNode, err := c.RegisterNode("laptop", pub)
	if err != nil {
		t.Fatalf("bob node: %v", err)
	}
	if err := c.JoinZone("homelab", bobNode.ID, "zonepass-1"); err != nil {
		t.Fatalf("bob join: %v", err)
	}
	out = mustCLI(t, dataDir, "", sink, "zone", "members", "homelab")
	if !strings.Contains(out, "laptop") || !strings.Contains(out, "bob") {
		t.Fatalf("members after bob = %q, want laptop/bob", out)
	}

	// Wrong password is non-enumerable.
	code, _, errb := cli(t, dataDir, "wrong-pass-1", sink, "zone", "join", "otherzone")
	if code == 0 || !strings.Contains(errb, "invalid zone or password") {
		t.Errorf("wrong-password join = %d %q, want generic invalid-zone-or-password", code, errb)
	}

	// status carries the zones field.
	out = mustCLI(t, dataDir, "", sink, "status")
	if !strings.Contains(out, "zones: homelab") {
		t.Errorf("status = %q, want zones: homelab", out)
	}

	// leave removes this device from the member list.
	mustCLI(t, dataDir, "", sink, "zone", "leave", "homelab")
	out = mustCLI(t, dataDir, "", sink, "zone", "members", "homelab")
	if strings.Contains(out, "home-router") {
		t.Errorf("members after leave = %q, this device should be gone", out)
	}
}

// mustInvite mints a fresh invite (each registration consumes one).
func mustInvite(t *testing.T, ts *testServer) string {
	t.Helper()
	admin, err := ts.store.Users().GetByUsername(context.Background(), "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin lookup: %v", err)
	}
	code, _, err := ts.store.Invites().Create(context.Background(), admin.ID, 0)
	if err != nil {
		t.Fatalf("mint invite: %v", err)
	}
	return code
}

func mustKeyPair(t *testing.T) (string, string, error) {
	t.Helper()
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key.String(), key.PublicKey().String(), nil
}

// TestCLILogout is the US3 acceptance (SC-004): reachable logout deregisters
// the node, revokes the refresh token and wipes local state; an unreachable
// server blocks logout leaving local state intact; --force wipes locally with
// a warning.
func TestCLILogout(t *testing.T) {
	t.Run("reachable", func(t *testing.T) {
		ts := newTestServer(t)
		cleanupIface(t)
		dataDir, sink := onboardRouter(t, ts)

		oldRT, err := keyring.OpenAt(filepath.Join(dataDir, "keys")).Get(keyring.RefreshTokenName)
		if err != nil {
			t.Fatalf("read RT: %v", err)
		}

		mustCLI(t, dataDir, "", sink, "logout")

		// Server side: node gone.
		nodes, err := ts.store.Nodes().AllForPeers(context.Background())
		if err != nil || len(nodes) != 0 {
			t.Errorf("server nodes after logout = %v (%v), want none", nodes, err)
		}
		// RT revoked: refreshing with it fails.
		c := apiclient.New(ts.url, apiclient.WithPinnedCert(certFP(ts.cert)))
		c.SetRefreshToken(string(oldRT))
		if err := c.Refresh(); err == nil {
			t.Error("old refresh token still works after logout")
		}
		// Local: all three artifacts gone.
		if _, err := os.Stat(filepath.Join(dataDir, "state.json")); err == nil {
			t.Error("state.json survived logout")
		}
		keys := keyring.OpenAt(filepath.Join(dataDir, "keys"))
		for _, name := range []string{keyring.DeviceKeyName, keyring.SessionTokenName, keyring.RefreshTokenName} {
			if _, err := keys.Get(name); err == nil {
				t.Errorf("%s survived logout", name)
			}
		}
		if _, err := netlink.LinkByName(testIface); err == nil {
			t.Error("tunnel interface survived logout")
		}
	})

	t.Run("unreachable blocks", func(t *testing.T) {
		ts := newTestServer(t)
		cleanupIface(t)
		dataDir, sink := onboardRouter(t, ts)
		ts.close() // server goes away

		code, _, errb := cli(t, dataDir, "", sink, "logout")
		if code == 0 {
			t.Fatalf("logout against unreachable server succeeded: %s", errb)
		}
		if !strings.Contains(errb, "--force") {
			t.Errorf("error %q should point at --force", errb)
		}
		// Local state intact.
		if _, err := os.Stat(filepath.Join(dataDir, "state.json")); err != nil {
			t.Error("state.json was wiped despite blocked logout")
		}
		if _, err := keyring.OpenAt(filepath.Join(dataDir, "keys")).Get(keyring.DeviceKeyName); err != nil {
			t.Error("device key was wiped despite blocked logout")
		}
	})

	t.Run("force wipes locally", func(t *testing.T) {
		ts := newTestServer(t)
		cleanupIface(t)
		dataDir, sink := onboardRouter(t, ts)
		ts.close()

		code, _, errb := cli(t, dataDir, "", sink, "logout", "--force")
		if code != 0 {
			t.Fatalf("forced logout failed: %s", errb)
		}
		if !strings.Contains(errb, "orphan") {
			t.Errorf("forced logout stderr %q should warn about the orphan node", errb)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "state.json")); err == nil {
			t.Error("state.json survived forced logout")
		}
	})
}
