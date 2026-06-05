package app_test

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"lanweave/internal/server/app"
	"lanweave/internal/testutil"
)

func writeConfig(t *testing.T, dataDir, adminPassword, certPath, keyPath string) string {
	t.Helper()
	body := fmt.Sprintf(`
[server]
listen = "127.0.0.1:0"
tls_cert = %q
tls_key = %q
data_dir = %q

[log]
level = "error"

[wireguard]
network = "100.127.0.0/16"

[auth]
jwt_secret = "0123456789abcdef0123456789abcdef"

[admin]
username = "admin"
password = %q
`, certPath, keyPath, dataDir, adminPassword)
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
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	path := writeConfig(t, dir, "supersecret", cert, key)

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

func TestRunBootstrapsAdminIdempotently(t *testing.T) {
	dir := t.TempDir()
	cert, key := newCerts(t, dir)

	// First boot with one password.
	path1 := writeConfig(t, dir, "first-password", cert, key)
	bootAndStop(t, path1)
	hash1 := readAdminHash(t, filepath.Join(dir, "db.sqlite"))
	if hash1 == "" {
		t.Fatal("admin not created on first boot")
	}
	if hash1 == "first-password" {
		t.Fatal("password stored as plaintext")
	}

	// Second boot with a CHANGED password — stored hash must not change.
	path2 := writeConfig(t, dir, "second-password", cert, key)
	bootAndStop(t, path2)
	hash2 := readAdminHash(t, filepath.Join(dir, "db.sqlite"))
	if hash1 != hash2 {
		t.Fatalf("admin hash changed across restart: %q != %q", hash1, hash2)
	}
}

func TestRunRejectsMissingAdminPassword(t *testing.T) {
	dir := t.TempDir()
	cert, key := newCerts(t, dir)
	path := writeConfig(t, dir, "", cert, key)

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
