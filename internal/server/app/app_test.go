package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	_ "modernc.org/sqlite"

	"lanweave/internal/server/app"
	"lanweave/internal/testutil"
	"lanweave/pkg/protocol"
)

// Full app.Run brings up the kernel data plane (WireGuard interface + nftables),
// so the booting tests below require CAP_NET_ADMIN. They run for real under
// `unshare -rUn` and skip in a bare unprivileged `go test`.

func uniqueNames(t *testing.T) (iface, table string) {
	t.Helper()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return "wgt" + h, "lwtest" + h
}

// ensureLoopbackUp brings `lo` up. A fresh network namespace (from `unshare -rUn`)
// starts with loopback down, which would break binding 127.0.0.1; on a real host
// lo is already up and this is a no-op.
func ensureLoopbackUp(t *testing.T) {
	t.Helper()
	if lo, err := netlink.LinkByName("lo"); err == nil {
		_ = netlink.LinkSetUp(lo)
	}
}

func cleanupDataPlane(iface, table string) {
	if l, err := netlink.LinkByName(iface); err == nil {
		_ = netlink.LinkDel(l)
	}
	if conn, err := nftables.New(); err == nil {
		conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
		_ = conn.Flush()
	}
}

func writeConfig(t *testing.T, dataDir, adminPassword, certPath, keyPath, iface, table string) string {
	return writeServerConfig(t, dataDir, adminPassword, certPath, keyPath, iface, table, "127.0.0.1:0", "")
}

// writeServerConfig is writeConfig with an explicit listen address and an extra
// [server] line (e.g. "tls = false"); pass "" for tlsLine to omit it.
func writeServerConfig(t *testing.T, dataDir, adminPassword, certPath, keyPath, iface, table, listen, tlsLine string) string {
	t.Helper()
	body := fmt.Sprintf(`
[server]
listen = %q
%s
tls_cert = %q
tls_key = %q
data_dir = %q

[log]
level = "error"

[wireguard]
network = "100.127.0.0/16"
listen_port = 0
interface = %q
mtu = 1420
endpoint = "vpn.example.com:51820"

[nftables]
table = %q

[auth]
jwt_secret = "0123456789abcdef0123456789abcdef"

[admin]
username = "admin"
password = %q
`, listen, tlsLine, certPath, keyPath, dataDir, iface, table, adminPassword)
	path := filepath.Join(dataDir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newCerts(t *testing.T, dir string) (cert, key string) {
	t.Helper()
	c, k, err := testutil.WriteSelfSignedCert(dir)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	return c, k
}

func TestRunServesAndShutsDown(t *testing.T) {
	testutil.RequireNetAdmin(t)
	ensureLoopbackUp(t)
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	iface, table := uniqueNames(t)
	t.Cleanup(func() { cleanupDataPlane(iface, table) })
	path := writeConfig(t, dir, "supersecret", cert, key, iface, table)

	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan string, 1)
	runErr := make(chan error, 1)
	start := time.Now()
	go func() {
		runErr <- app.Run(ctx, app.Options{
			ConfigPath: path,
			Version:    "acc-test",
			Ready:      func(addr string) { addrCh <- addr },
		})
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case err := <-runErr:
		t.Fatalf("server exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server not ready within 5s")
	}

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test client
	}}
	resp, err := client.Get("https://" + addr + "/api/v1/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("cold start to first 200 = %v, budget 3s", elapsed)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run returned error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("graceful shutdown exceeded 10s")
	}
}

// TestRunReportsNodeOfflineUntilConnected is the quickstart Scenario A acceptance
// test against the real booted binary: a registered node that never connects is
// reported online:false with no last_handshake. (A literal online=true needs a real
// handshaking client and is the manual quickstart scenario.)
func TestRunReportsNodeOfflineUntilConnected(t *testing.T) {
	testutil.RequireNetAdmin(t)
	ensureLoopbackUp(t)
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	iface, table := uniqueNames(t)
	t.Cleanup(func() { cleanupDataPlane(iface, table) })
	const adminPW = "supersecret-pw"
	path := writeConfig(t, dir, adminPW, cert, key, iface, table)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(ctx, app.Options{ConfigPath: path, Version: "acc", Ready: func(a string) { addrCh <- a }})
	}()
	var addr string
	select {
	case addr = <-addrCh:
	case err := <-runErr:
		t.Fatalf("server exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server not ready within 5s")
	}

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test client
	}}
	base := "https://" + addr

	// Log in as admin.
	var login protocol.LoginResponse
	doJSON(t, client, http.MethodPost, base+"/api/v1/login", "",
		protocol.LoginRequest{Username: "admin", Password: adminPW}, http.StatusOK, &login)

	// Register a node (never connects).
	nodeKey, _ := wgtypes.GeneratePrivateKey()
	var node protocol.NodeResponse
	doJSON(t, client, http.MethodPost, base+"/api/v1/nodes", login.Token,
		protocol.RegisterNodeRequest{Name: "laptop", WGPubKey: nodeKey.PublicKey().String()},
		http.StatusCreated, &node)

	// List nodes → the node is present but offline, with no last handshake.
	var list protocol.NodeListResponse
	doJSON(t, client, http.MethodGet, base+"/api/v1/nodes", login.Token, nil, http.StatusOK, &list)
	if len(list.Nodes) != 1 {
		t.Fatalf("node list = %d, want 1", len(list.Nodes))
	}
	if list.Nodes[0].Online {
		t.Error("never-connected node reported online")
	}
	if list.Nodes[0].LastHandshake != "" {
		t.Errorf("never-connected node has last_handshake = %q, want empty", list.Nodes[0].LastHandshake)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run returned error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("graceful shutdown exceeded 10s")
	}
}

// doJSON sends an optional JSON body with an optional bearer token, asserts the
// status code, and decodes the response into out (when non-nil).
func doJSON(t *testing.T, client *http.Client, method, url, token string, body any, wantStatus int, out any) {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		b, _ := json.Marshal(body)
		req, err = http.NewRequest(method, url, bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, url, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d", method, url, resp.StatusCode, wantStatus)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
}

func TestRunBootstrapsAdminIdempotently(t *testing.T) {
	testutil.RequireNetAdmin(t)
	ensureLoopbackUp(t)
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	iface, table := uniqueNames(t)
	t.Cleanup(func() { cleanupDataPlane(iface, table) })

	// First boot with one password.
	path1 := writeConfig(t, dir, "first-password", cert, key, iface, table)
	bootAndStop(t, path1)
	hash1 := readAdminHash(t, filepath.Join(dir, "db.sqlite"))
	if hash1 == "" {
		t.Fatal("admin not created on first boot")
	}
	if hash1 == "first-password" {
		t.Fatal("password stored as plaintext")
	}

	// Second boot with a CHANGED password — stored hash must not change.
	path2 := writeConfig(t, dir, "second-password", cert, key, iface, table)
	bootAndStop(t, path2)
	hash2 := readAdminHash(t, filepath.Join(dir, "db.sqlite"))
	if hash1 != hash2 {
		t.Fatalf("admin hash changed across restart: %q != %q", hash1, hash2)
	}
}

func TestRunRejectsMissingAdminPassword(t *testing.T) {
	// Aborts during config validation, before any data-plane setup, so this needs
	// no privilege.
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	path := writeConfig(t, dir, "", cert, key, "wgt-unused", "lw-unused")

	err := app.Run(context.Background(), app.Options{ConfigPath: path, Version: "x"})
	if err == nil {
		t.Fatal("expected startup error for empty admin password")
	}
}

// bootAndStop runs the server until ready, then cancels and waits for clean exit.
func bootAndStop(t *testing.T, configPath string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan string, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(ctx, app.Options{
			ConfigPath: configPath,
			Version:    "boot",
			Ready:      func(addr string) { addrCh <- addr },
		})
	}()
	select {
	case <-addrCh:
	case err := <-runErr:
		cancel()
		t.Fatalf("boot failed: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("boot not ready in 5s")
	}
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}

// TestRunServesPlaintextHTTP is the US1 acceptance test: with tls = false the
// server listens plaintext HTTP and a bare (non-TLS) client completes an
// authenticated, protected call. Certs are present in the config but ignored.
func TestRunServesPlaintextHTTP(t *testing.T) {
	testutil.RequireNetAdmin(t)
	ensureLoopbackUp(t)
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	iface, table := uniqueNames(t)
	t.Cleanup(func() { cleanupDataPlane(iface, table) })
	const adminPW = "supersecret-pw"
	path := writeServerConfig(t, dir, adminPW, cert, key, iface, table, "127.0.0.1:0", "tls = false")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(ctx, app.Options{ConfigPath: path, Version: "acc", Ready: func(a string) { addrCh <- a }})
	}()
	var addr string
	select {
	case addr = <-addrCh:
	case err := <-runErr:
		t.Fatalf("server exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server not ready within 5s")
	}

	// Plain HTTP client — no TLS at all.
	client := &http.Client{}
	base := "http://" + addr

	var login protocol.LoginResponse
	doJSON(t, client, http.MethodPost, base+"/api/v1/login", "",
		protocol.LoginRequest{Username: "admin", Password: adminPW}, http.StatusOK, &login)

	// A protected call must succeed over plaintext with the bearer token.
	var list protocol.NodeListResponse
	doJSON(t, client, http.MethodGet, base+"/api/v1/nodes", login.Token, nil, http.StatusOK, &list)

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run returned error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("graceful shutdown exceeded 10s")
	}
}

// TestRunDefaultConfigIsHTTPS is the US2 acceptance test that a config which
// never mentions the tls toggle still serves HTTPS (no silent downgrade): a TLS
// client succeeds and a bare HTTP client against the same port does not get 200.
func TestRunDefaultConfigIsHTTPS(t *testing.T) {
	testutil.RequireNetAdmin(t)
	ensureLoopbackUp(t)
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	iface, table := uniqueNames(t)
	t.Cleanup(func() { cleanupDataPlane(iface, table) })
	path := writeConfig(t, dir, "supersecret", cert, key, iface, table) // no tls key

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(ctx, app.Options{ConfigPath: path, Version: "acc", Ready: func(a string) { addrCh <- a }})
	}()
	var addr string
	select {
	case addr = <-addrCh:
	case err := <-runErr:
		t.Fatalf("server exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server not ready within 5s")
	}

	tlsClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test client
	}}
	resp, err := tlsClient.Get("https://" + addr + "/api/v1/healthz")
	if err != nil {
		t.Fatalf("https healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("https healthz status = %d, want 200", resp.StatusCode)
	}

	// A bare HTTP request to the TLS port must not be served as 200 (proves the
	// default really is HTTPS, not plaintext).
	plain := &http.Client{Timeout: 3 * time.Second}
	if r, err := plain.Get("http://" + addr + "/api/v1/healthz"); err == nil {
		_ = r.Body.Close()
		if r.StatusCode == http.StatusOK {
			t.Fatalf("plain HTTP got 200 against a TLS listener; default downgraded to plaintext")
		}
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run returned error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("graceful shutdown exceeded 10s")
	}
}

// TestRunTLSMissingCertHardFails is the US2 acceptance test that TLS mode with
// an unreadable cert aborts startup (never falls back to plaintext). It fails in
// config validation, before any data-plane setup, so it needs no privilege.
func TestRunTLSMissingCertHardFails(t *testing.T) {
	dir := t.TempDir()
	// tls = true with cert/key paths that do not exist.
	path := writeServerConfig(t, dir, "supersecret",
		filepath.Join(dir, "missing-cert.pem"), filepath.Join(dir, "missing-key.pem"),
		"wgt-unused", "lw-unused", "127.0.0.1:0", "tls = true")

	err := app.Run(context.Background(), app.Options{ConfigPath: path, Version: "x"})
	if err == nil {
		t.Fatal("expected startup error for TLS mode with missing cert")
	}
	if !strings.Contains(err.Error(), "tls_cert") {
		t.Fatalf("error %q does not mention tls_cert", err.Error())
	}
}

// TestRunPlaintextNonLoopbackStarts is the US3 acceptance test that a plaintext
// listener on a non-loopback address (0.0.0.0) is warned about but NOT blocked:
// the server still reaches Ready and serves.
func TestRunPlaintextNonLoopbackStarts(t *testing.T) {
	testutil.RequireNetAdmin(t)
	ensureLoopbackUp(t)
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	iface, table := uniqueNames(t)
	t.Cleanup(func() { cleanupDataPlane(iface, table) })
	path := writeServerConfig(t, dir, "supersecret", cert, key, iface, table, "0.0.0.0:0", "tls = false")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(ctx, app.Options{ConfigPath: path, Version: "acc", Ready: func(a string) { addrCh <- a }})
	}()
	select {
	case <-addrCh: // reached Ready → non-loopback plaintext bind did not block
	case err := <-runErr:
		t.Fatalf("server exited before ready (non-loopback plaintext blocked startup?): %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server not ready within 5s")
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run returned error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("graceful shutdown exceeded 10s")
	}
}

func readAdminHash(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var hash string
	err = db.QueryRow(`SELECT password_hash FROM users WHERE username = 'admin'`).Scan(&hash)
	if err != nil {
		t.Fatalf("read admin hash: %v", err)
	}
	return hash
}
