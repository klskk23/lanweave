package config_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lanweave/internal/server/config"
	"lanweave/internal/testutil"
)

func validConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cert, key, err := testutil.WriteSelfSignedCert(dir)
	if err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return &config.Config{
		Server:    config.ServerConfig{Listen: "127.0.0.1:0", TLSCert: cert, TLSKey: key, DataDir: dir},
		Log:       config.LogConfig{Level: "info"},
		RateLimit: config.RateLimitConfig{RPS: 100, Burst: 200},
		WireGuard: config.WireGuardConfig{Network: "100.127.0.0/16", ListenPort: 51820, Interface: "wg-lanweave", MTU: 1420},
		Auth:      config.AuthConfig{JWTSecret: config.Secret("0123456789abcdef0123456789abcdef"), JWTTTL: "2h"},
		Admin:     config.AdminConfig{Username: "admin", Password: config.Secret("supersecret")},
	}
}

func TestValidateOK(t *testing.T) {
	if err := validConfig(t).Validate(); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidateFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *config.Config)
		wantSub string
	}{
		{"missing listen", func(c *config.Config) { c.Server.Listen = "" }, "server.listen"},
		{"bad listen", func(c *config.Config) { c.Server.Listen = "not-a-hostport" }, "host:port"},
		{"missing cert", func(c *config.Config) { c.Server.TLSCert = "/no/such/cert.pem" }, "server.tls_cert"},
		{"missing data_dir", func(c *config.Config) { c.Server.DataDir = "" }, "server.data_dir"},
		{"bad log level", func(c *config.Config) { c.Log.Level = "verbose" }, "log.level"},
		{"bad cidr", func(c *config.Config) { c.WireGuard.Network = "100.127.0.0/x" }, "wireguard.network"},
		{"short jwt secret", func(c *config.Config) { c.Auth.JWTSecret = config.Secret("short") }, "auth.jwt_secret"},
		{"bad ttl", func(c *config.Config) { c.Auth.JWTTTL = "2weeks" }, "auth.jwt_ttl"},
		{"missing admin user", func(c *config.Config) { c.Admin.Username = "" }, "admin.username"},
		{"missing admin pw", func(c *config.Config) { c.Admin.Password = config.Secret("") }, "admin.password"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig(t)
			tc.mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestValidateCollectsAllErrors(t *testing.T) {
	c := validConfig(t)
	c.Server.Listen = ""
	c.Admin.Username = ""
	c.WireGuard.Network = ""
	err := c.Validate()
	if err == nil {
		t.Fatal("expected joined errors")
	}
	for _, sub := range []string{"server.listen", "admin.username", "wireguard.network"} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("joined error missing %q: %v", sub, err)
		}
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	cert, key, err := testutil.WriteSelfSignedCert(dir)
	if err != nil {
		t.Fatalf("write cert: %v", err)
	}
	toml := `
[server]
listen = "127.0.0.1:8443"
tls_cert = "` + cert + `"
tls_key = "` + key + `"
data_dir = "` + dir + `"

[wireguard]
network = "100.127.0.0/16"

[auth]
jwt_secret = "0123456789abcdef0123456789abcdef"

[admin]
username = "admin"
password = "supersecret"
`
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Log.Level != "info" {
		t.Errorf("log.level default = %q, want info", c.Log.Level)
	}
	if c.RateLimit.RPS != 100 || c.RateLimit.Burst != 200 {
		t.Errorf("ratelimit defaults = %v/%d, want 100/200", c.RateLimit.RPS, c.RateLimit.Burst)
	}
	if c.Auth.JWTTTL != "2h" {
		t.Errorf("jwt_ttl default = %q, want 2h", c.Auth.JWTTTL)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate after load: %v", err)
	}
}

// TestSecretRedaction proves secrets never appear in structured logs (FR-019).
func TestSecretRedaction(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	cfg := validConfig(t)
	logger.Info("config", "cfg", cfg, "jwt", cfg.Auth.JWTSecret, "pw", cfg.Admin.Password)

	out := buf.String()
	if strings.Contains(out, "0123456789abcdef0123456789abcdef") {
		t.Errorf("jwt secret leaked into logs: %s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Errorf("admin password leaked into logs: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker in logs, got: %s", out)
	}
}
