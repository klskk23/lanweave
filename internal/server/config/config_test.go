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
		WireGuard: config.WireGuardConfig{Network: "100.127.0.0/16", ListenPort: 51820, Interface: "wg-lanweave", MTU: 1420, Endpoint: "vpn.example.com:51820"},
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
		{"bad invite_ttl", func(c *config.Config) { c.Auth.InviteTTL = "2weeks" }, "auth.invite_ttl"},
		{"negative invite_ttl", func(c *config.Config) { c.Auth.InviteTTL = "-1h" }, "auth.invite_ttl"},
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

// TestInviteTTLValidation — invite_ttl is optional (empty = never expire) and a
// normal duration validates; the rejection cases live in TestValidateFailures.
func TestInviteTTLValidation(t *testing.T) {
	c := validConfig(t)
	c.Auth.InviteTTL = "" // disabled
	if err := c.Validate(); err != nil {
		t.Errorf("empty invite_ttl must be valid (never expire), got %v", err)
	}
	c = validConfig(t)
	c.Auth.InviteTTL = "24h"
	if err := c.Validate(); err != nil {
		t.Errorf("invite_ttl=24h must be valid, got %v", err)
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
endpoint = "vpn.example.com:51820"

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

// writeTLSToggleConfig writes a minimal valid config, inserting tlsLine verbatim
// into [server] (pass "" to omit the tls key entirely). Returns the config path.
func writeTLSToggleConfig(t *testing.T, tlsLine string) string {
	t.Helper()
	dir := t.TempDir()
	cert, key, err := testutil.WriteSelfSignedCert(dir)
	if err != nil {
		t.Fatalf("write cert: %v", err)
	}
	toml := `
[server]
listen = "127.0.0.1:8443"
` + tlsLine + `
tls_cert = "` + cert + `"
tls_key = "` + key + `"
data_dir = "` + dir + `"

[wireguard]
network = "100.127.0.0/16"
endpoint = "vpn.example.com:51820"

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
	return path
}

// writeLimitsConfig writes a minimal valid config, inserting limitsBlock as the body
// of a [limits] section (pass "" to omit the section entirely). Returns the path.
func writeLimitsConfig(t *testing.T, limitsBlock string) string {
	t.Helper()
	dir := t.TempDir()
	cert, key, err := testutil.WriteSelfSignedCert(dir)
	if err != nil {
		t.Fatalf("write cert: %v", err)
	}
	limits := ""
	if limitsBlock != "" {
		limits = "\n[limits]\n" + limitsBlock + "\n"
	}
	toml := `
[server]
listen = "127.0.0.1:8443"
tls_cert = "` + cert + `"
tls_key = "` + key + `"
data_dir = "` + dir + `"

[wireguard]
network = "100.127.0.0/16"
endpoint = "vpn.example.com:51820"

[auth]
jwt_secret = "0123456789abcdef0123456789abcdef"

[admin]
username = "admin"
password = "supersecret"
` + limits
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLimitsConfigThreeState proves the per-user caps mirror the TLS three-state:
// absent → default 10, explicit 0 preserved (unlimited), negative rejected at startup
// (FR-002/007/008, US3).
func TestLimitsConfigThreeState(t *testing.T) {
	// (a) [limits] absent → both default to 10 after Load/applyDefaults.
	c, err := config.Load(writeLimitsConfig(t, ""))
	if err != nil {
		t.Fatalf("load absent: %v", err)
	}
	if got := c.Limits.MaxDevicesPerUser; got == nil || *got != 10 {
		t.Fatalf("absent device cap: got %v, want 10", got)
	}
	if got := c.Limits.MaxOwnedZonesPerUser; got == nil || *got != 10 {
		t.Fatalf("absent zone cap: got %v, want 10", got)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("default caps should validate: %v", err)
	}

	// (b) explicit 0 is preserved (unlimited), not re-defaulted to 10.
	c, err = config.Load(writeLimitsConfig(t, "max_devices_per_user = 0\nmax_owned_zones_per_user = 0"))
	if err != nil {
		t.Fatalf("load zero: %v", err)
	}
	if *c.Limits.MaxDevicesPerUser != 0 || *c.Limits.MaxOwnedZonesPerUser != 0 {
		t.Fatalf("explicit 0 not preserved: %d/%d", *c.Limits.MaxDevicesPerUser, *c.Limits.MaxOwnedZonesPerUser)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("zero caps should validate (0 = unlimited): %v", err)
	}

	// (c) negative → Validate error naming the field; the server refuses to start.
	c, err = config.Load(writeLimitsConfig(t, "max_devices_per_user = -1"))
	if err != nil {
		t.Fatalf("load negative: %v", err)
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "limits.max_devices_per_user") {
		t.Fatalf("negative cap: got %v, want error naming limits.max_devices_per_user", err)
	}

	// (d) a valid positive config validates clean and keeps its values.
	c, err = config.Load(writeLimitsConfig(t, "max_devices_per_user = 3\nmax_owned_zones_per_user = 5"))
	if err != nil {
		t.Fatalf("load positive: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("positive caps should validate: %v", err)
	}
	if *c.Limits.MaxDevicesPerUser != 3 || *c.Limits.MaxOwnedZonesPerUser != 5 {
		t.Fatalf("positive caps = %d/%d, want 3/5", *c.Limits.MaxDevicesPerUser, *c.Limits.MaxOwnedZonesPerUser)
	}
}

// TestTLSEnabledThreeState proves the tls toggle distinguishes "unset" (HTTPS,
// safe default) from explicit false (plaintext) — the FR-002 no-downgrade
// invariant. A bare bool would collapse unset and false; *bool keeps them apart.
func TestTLSEnabledThreeState(t *testing.T) {
	decoded := []struct {
		name    string
		tlsLine string
		want    bool
	}{
		{"unset key defaults to HTTPS", "", true},
		{"explicit true", "tls = true", true},
		{"explicit false is plaintext", "tls = false", false},
	}
	for _, tc := range decoded {
		t.Run(tc.name, func(t *testing.T) {
			c, err := config.Load(writeTLSToggleConfig(t, tc.tlsLine))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := c.Server.TLSEnabled(); got != tc.want {
				t.Errorf("TLSEnabled() = %v, want %v", got, tc.want)
			}
		})
	}

	// Direct struct three-state (nil pointer must read as HTTPS, never panic).
	tru, fls := true, false
	for _, tc := range []struct {
		name string
		ptr  *bool
		want bool
	}{
		{"nil pointer is HTTPS", nil, true},
		{"&true", &tru, true},
		{"&false", &fls, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := config.ServerConfig{TLS: tc.ptr}
			if got := s.TLSEnabled(); got != tc.want {
				t.Errorf("TLSEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateTLSConditional proves cert/key validation is gated on the
// transport mode: TLS (default/explicit true) still hard-fails on an unreadable
// cert (FR-004, no silent downgrade); plaintext ignores cert/key entirely
// (FR-005).
func TestValidateTLSConditional(t *testing.T) {
	tru, fls := true, false

	// Plaintext mode ignores cert/key — even a bogus path must validate clean.
	t.Run("plaintext ignores bad cert path", func(t *testing.T) {
		c := validConfig(t)
		c.Server.TLS = &fls
		c.Server.TLSCert = "/no/such/cert.pem"
		c.Server.TLSKey = "/no/such/key.pem"
		if err := c.Validate(); err != nil {
			t.Fatalf("plaintext mode should ignore cert/key, got: %v", err)
		}
	})
	t.Run("plaintext ignores empty cert path", func(t *testing.T) {
		c := validConfig(t)
		c.Server.TLS = &fls
		c.Server.TLSCert = ""
		c.Server.TLSKey = ""
		if err := c.Validate(); err != nil {
			t.Fatalf("plaintext mode should ignore empty cert/key, got: %v", err)
		}
	})

	// TLS mode (nil = default, and explicit true) hard-fails on a missing cert.
	for _, tc := range []struct {
		name string
		tls  *bool
	}{
		{"default (nil) requires cert", nil},
		{"explicit true requires cert", &tru},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig(t)
			c.Server.TLS = tc.tls
			c.Server.TLSCert = "/no/such/cert.pem"
			err := c.Validate()
			if err == nil {
				t.Fatal("TLS mode with missing cert must fail validation")
			}
			if !strings.Contains(err.Error(), "server.tls_cert") {
				t.Fatalf("error %q does not mention server.tls_cert", err.Error())
			}
		})
	}
}

// TestWarnPlaintextExposure covers the plaintext-on-non-loopback warning
// decision (FR-006/FR-007): warn only when plaintext AND the bind host is not
// loopback. TLS mode never warns regardless of address.
func TestWarnPlaintextExposure(t *testing.T) {
	const plaintext, httpsOn = false, true

	cases := []struct {
		name   string
		tlsOn  bool
		listen string
		want   bool
	}{
		{"plaintext loopback v4", plaintext, "127.0.0.1:8080", false},
		{"plaintext loopback v6", plaintext, "[::1]:8080", false},
		{"plaintext localhost", plaintext, "localhost:8080", false},
		{"plaintext all-interfaces v4", plaintext, "0.0.0.0:8080", true},
		{"plaintext all-interfaces v6", plaintext, "[::]:8080", true},
		{"plaintext empty host", plaintext, ":8080", true},
		{"plaintext real ip", plaintext, "192.168.1.10:8080", true},
		{"plaintext hostname", plaintext, "vpn.example.com:8080", true},
		{"tls non-loopback never warns", httpsOn, "0.0.0.0:8080", false},
		{"tls loopback never warns", httpsOn, "127.0.0.1:8080", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			on := tc.tlsOn
			s := config.ServerConfig{Listen: tc.listen, TLS: &on}
			if got := s.WarnPlaintextExposure(); got != tc.want {
				t.Errorf("WarnPlaintextExposure(listen=%q, tls=%v) = %v, want %v", tc.listen, tc.tlsOn, got, tc.want)
			}
		})
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

// TestAPIDocsEnabledThreeState proves the api_docs toggle distinguishes "unset"
// (docs exposed, the chosen default) from explicit false (hidden). A bare bool
// would collapse unset and false; *bool keeps them apart, mirroring TLS.
func TestAPIDocsEnabledThreeState(t *testing.T) {
	decoded := []struct {
		name     string
		docsLine string
		want     bool
	}{
		{"unset key defaults to enabled", "", true},
		{"explicit true", "api_docs = true", true},
		{"explicit false hides docs", "api_docs = false", false},
	}
	for _, tc := range decoded {
		t.Run(tc.name, func(t *testing.T) {
			c, err := config.Load(writeTLSToggleConfig(t, tc.docsLine))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := c.Server.APIDocsEnabled(); got != tc.want {
				t.Errorf("APIDocsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}

	// Direct struct three-state (nil pointer must read as enabled, never panic).
	tru, fls := true, false
	for _, tc := range []struct {
		name string
		ptr  *bool
		want bool
	}{
		{"nil pointer is enabled", nil, true},
		{"&true", &tru, true},
		{"&false", &fls, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := config.ServerConfig{APIDocs: tc.ptr}
			if got := s.APIDocsEnabled(); got != tc.want {
				t.Errorf("APIDocsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
