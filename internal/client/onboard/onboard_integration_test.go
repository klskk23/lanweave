package onboard_test

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
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
	"lanweave/internal/client/onboard"
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

// realServer stands up the genuine API (real SQLite + WireGuard + nftables) behind a TLS
// httptest server, returning its URL, certificate, and a freshly minted invite code.
func realServer(t *testing.T) (url string, cert *x509.Certificate, invite string) {
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
	code, err := st.Invites().Create(ctx, admin.ID)
	if err != nil {
		t.Fatalf("mint invite: %v", err)
	}

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	wgName := "wgt" + hex.EncodeToString(b)
	wgCfg := config.WireGuardConfig{Network: "100.127.0.0/16", ListenPort: 0, Interface: wgName, MTU: 1420, Endpoint: "vpn.example.com:51820"}
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

	table := "lwc" + hex.EncodeToString(b)
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
	return ts.URL, ts.Certificate(), code
}

func trustPool(cert *x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool
}

// TestOnboardIntegrationCreateAccount drives the real apiclient + onboard against the real
// server over TLS (verify-on via the test cert as trust root — not --insecure), creating
// an account and registering a device.
func TestOnboardIntegrationCreateAccount(t *testing.T) {
	url, cert, invite := realServer(t)

	client := apiclient.New(url, apiclient.WithRootCAs(trustPool(cert)))
	fk := keyring.NewFake()
	statePath := filepath.Join(t.TempDir(), "lanweave", "state.json")
	p := &onboard.Provisioner{API: client, Keys: fk, StatePath: statePath, ServerURL: url}

	rec, err := p.Provision(onboard.Credentials{Mode: onboard.CreateAccount, Invite: invite, Username: "alice", Password: "password123"}, "laptop")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if rec.IP != "100.127.0.2" {
		t.Errorf("assigned address = %s, want 100.127.0.2", rec.IP)
	}
	if rec.ServerPublicKey == "" || rec.Endpoint != "vpn.example.com:51820" || rec.Network != "100.127.0.0/16" {
		t.Errorf("server info not recorded: %+v", rec)
	}
	if key, err := fk.Get(keyring.DeviceKeyName); err != nil || len(key) == 0 {
		t.Errorf("device key not in vault: %v", err)
	}
	if got, err := state.Load(statePath); err != nil || got.IP != "100.127.0.2" {
		t.Errorf("state not persisted: %+v %v", got, err)
	}
}

// TestOnboardIntegrationSignIn reuses the account-creation path, then signs in as the same
// user on a fresh machine and registers a second device.
func TestOnboardIntegrationSignIn(t *testing.T) {
	url, cert, invite := realServer(t)
	pool := trustPool(cert)

	// First, create the account + a device.
	first := &onboard.Provisioner{API: apiclient.New(url, apiclient.WithRootCAs(pool)), Keys: keyring.NewFake(), StatePath: filepath.Join(t.TempDir(), "s1.json"), ServerURL: url}
	if _, err := first.Provision(onboard.Credentials{Mode: onboard.CreateAccount, Invite: invite, Username: "bob", Password: "password123"}, "laptop"); err != nil {
		t.Fatalf("setup create-account: %v", err)
	}

	// Now sign in (no invite) and register a second device.
	second := &onboard.Provisioner{API: apiclient.New(url, apiclient.WithRootCAs(pool)), Keys: keyring.NewFake(), StatePath: filepath.Join(t.TempDir(), "s2.json"), ServerURL: url}
	rec, err := second.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "bob", Password: "password123"}, "desktop")
	if err != nil {
		t.Fatalf("sign-in provision: %v", err)
	}
	if rec.IP == "" || rec.NodeName != "desktop" {
		t.Errorf("second device not registered: %+v", rec)
	}
}
