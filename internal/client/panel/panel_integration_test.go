//go:build linux

package panel_test

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.org/x/time/rate"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/panel"
	"lanweave/internal/client/state"
	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/status"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

// realServer stands up the real API (real SQLite + WireGuard + nftables) behind a TLS
// httptest server and returns its URL, certificate, and an invite minter.
func realServer(t *testing.T) (url string, cert *x509.Certificate, mintInvite func() string) {
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
	admin, err := st.Users().CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	wgName := "wgp" + hex.EncodeToString(b)
	wgCfg := config.WireGuardConfig{Network: "100.127.0.0/16", ListenPort: 0, Interface: wgName, MTU: 1420, Endpoint: "vpn.example.com:51820"}
	serverKey, _ := wgtypes.GeneratePrivateKey()
	srv, err := wg.EnsureInterface(wgCfg, serverKey, log)
	if err != nil {
		t.Fatalf("server interface: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(wgName); e == nil {
			_ = netlink.LinkDel(l)
		}
	})
	table := "lwp" + hex.EncodeToString(b)
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
		Version: "itest", Limiter: rate.NewLimiter(rate.Limit(10000), 10000), Logger: log,
		Store: st, JWT: jwtMgr, WG: srv, NetFW: mgr, WGConfig: wgCfg,
		Status: status.New(srv.Handshakes, status.DefaultInterval, log),
	})
	ts := httptest.NewTLSServer(router)
	t.Cleanup(ts.Close)
	return ts.URL, ts.Certificate(), func() string {
		code, err := st.Invites().Create(ctx, admin.ID)
		if err != nil {
			t.Fatalf("mint invite: %v", err)
		}
		return code
	}
}

func trustPool(cert *x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(cert)
	return p
}

// onboardUser creates an account + a device and returns a panel controller bound to it.
func onboardUser(t *testing.T, url string, pool *x509.CertPool, invite, username, devName string) (*panel.Controller, *apiclient.Client) {
	t.Helper()
	c := apiclient.New(url, apiclient.WithRootCAs(pool))
	if err := c.Register(invite, username, "password123"); err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	if err := c.Login(username, "password123"); err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	k, _ := wgtypes.GeneratePrivateKey()
	node, err := c.RegisterNode(devName, k.PublicKey().String())
	if err != nil {
		t.Fatalf("register device %s: %v", devName, err)
	}
	rec := state.Record{NodeName: devName, IP: node.IP, ServerURL: url, Network: "100.127.0.0/16"}
	return panel.New(c, rec, keyring.NewFake()), c
}

// TestPanelIntegrationViewCreateJoin covers US1 (view + this-machine) and US2 (create/join/
// leave + transparency) against a real server.
func TestPanelIntegrationViewCreateJoin(t *testing.T) {
	url, cert, mint := realServer(t)
	pool := trustPool(cert)

	alice, aClient := onboardUser(t, url, pool, mint(), "alice", "laptop")
	// Alice registers a second device → her device list has two entries.
	k, _ := wgtypes.GeneratePrivateKey()
	if _, err := aClient.RegisterNode("phone", k.PublicKey().String()); err != nil {
		t.Fatalf("register alice phone: %v", err)
	}
	bob, _ := onboardUser(t, url, pool, mint(), "bob", "bob-pc")

	// US1: Devices lists both, with this machine (laptop) marked.
	devs, err := alice.Devices()
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("alice devices = %d, want 2", len(devs))
	}
	marked := 0
	for _, d := range devs {
		if d.IsThisMachine {
			marked++
			if d.Name != "laptop" {
				t.Errorf("wrong this-machine: %s", d.Name)
			}
		}
	}
	if marked != 1 {
		t.Errorf("this-machine marked %d times, want 1", marked)
	}

	// US2: Alice creates a zone (owner), Bob joins, Alice sees Bob in members (transparency).
	if err := alice.CreateZone("team", "zone-strong-pw"); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	zones, _ := alice.Zones()
	if len(zones) != 1 || zones[0].Name != "team" || !zones[0].IsOwner {
		t.Fatalf("alice zones wrong: %+v", zones)
	}
	if err := bob.JoinZone("team", "zone-strong-pw"); err != nil {
		t.Fatalf("bob join: %v", err)
	}
	members, _ := alice.Members("team")
	var sawBob bool
	for _, m := range members {
		if m.Owner == "bob" && m.NodeName == "bob-pc" && m.NodeID != 0 {
			sawBob = true
		}
	}
	if !sawBob {
		t.Errorf("alice should see bob's device with a node id in members: %+v", members)
	}

	// Wrong password / duplicate name → typed errors.
	if err := bob.JoinZone("team", "wrong-pw-here"); !errors.Is(err, apiclient.ErrZoneOrPassword) {
		t.Errorf("wrong pw: got %v, want ErrZoneOrPassword", err)
	}
	if err := bob.CreateZone("team", "another-pw"); !errors.Is(err, apiclient.ErrZoneNameTaken) {
		t.Errorf("dup name: got %v, want ErrZoneNameTaken", err)
	}

	// US2: Bob leaves → no longer a member.
	if err := bob.LeaveZone("team"); err != nil {
		t.Fatalf("bob leave: %v", err)
	}
	if members, _ := alice.Members("team"); len(members) != 0 {
		t.Errorf("after bob leaves, members = %d, want 0", len(members))
	}
}

// TestPanelIntegrationOwnerOps covers US3: owner change-password / kick / delete, and that a
// non-owner is refused.
func TestPanelIntegrationOwnerOps(t *testing.T) {
	url, cert, mint := realServer(t)
	pool := trustPool(cert)
	alice, _ := onboardUser(t, url, pool, mint(), "alice", "laptop")
	bob, _ := onboardUser(t, url, pool, mint(), "bob", "bob-pc")

	if err := alice.CreateZone("team", "old-strong-pw"); err != nil {
		t.Fatal(err)
	}
	if err := bob.JoinZone("team", "old-strong-pw"); err != nil {
		t.Fatal(err)
	}

	// Non-owner owner-op → ErrNotOwner.
	if err := bob.ChangePassword("team", "hacker-pw-123"); !errors.Is(err, apiclient.ErrNotOwner) {
		t.Errorf("non-owner change: got %v, want ErrNotOwner", err)
	}

	// Owner changes password: a new device can no longer join with the old password.
	if err := alice.ChangePassword("team", "new-strong-pw"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	carol, _ := onboardUser(t, url, pool, mint(), "carol", "carol-pc")
	if err := carol.JoinZone("team", "old-strong-pw"); !errors.Is(err, apiclient.ErrZoneOrPassword) {
		t.Errorf("join with old pw: got %v, want ErrZoneOrPassword", err)
	}
	if err := carol.JoinZone("team", "new-strong-pw"); err != nil {
		t.Errorf("join with new pw should succeed: %v", err)
	}

	// Owner kicks bob by his node id (from the members view).
	members, _ := alice.Members("team")
	var bobID int64
	for _, m := range members {
		if m.Owner == "bob" {
			bobID = m.NodeID
		}
	}
	if bobID == 0 {
		t.Fatal("could not find bob's node id in members")
	}
	if err := alice.KickMember("team", bobID); err != nil {
		t.Fatalf("kick: %v", err)
	}
	members, _ = alice.Members("team")
	for _, m := range members {
		if m.Owner == "bob" {
			t.Error("bob still a member after kick")
		}
	}

	// Owner deletes the zone → it disappears from her zones.
	if err := alice.DeleteZone("team"); err != nil {
		t.Fatalf("delete zone: %v", err)
	}
	if zones, _ := alice.Zones(); len(zones) != 0 {
		t.Errorf("after delete, alice zones = %d, want 0", len(zones))
	}
}
