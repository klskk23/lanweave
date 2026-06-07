// Package config loads and validates the single TOML configuration file consumed
// once at startup.
package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// DefaultConfigPath is used when neither --config nor LANWEAVE_CONFIG is set.
const DefaultConfigPath = "/etc/lanweave/config.toml"

// Secret is a string that never reveals itself through logging or fmt. Use
// Reveal() to obtain the underlying value where it is actually required.
type Secret string

func (s Secret) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }
func (s Secret) String() string       { return "[REDACTED]" }
func (s Secret) Reveal() string       { return string(s) }

// MarshalText redacts the secret for any text-based encoder (including the JSON
// log handler when an entire Config struct is logged). Decoding is unaffected:
// Secret intentionally does not implement TextUnmarshaler, so TOML still loads
// the real value via its underlying string kind.
func (s Secret) MarshalText() ([]byte, error) { return []byte("[REDACTED]"), nil }

type Config struct {
	Server    ServerConfig    `toml:"server"`
	Log       LogConfig       `toml:"log"`
	RateLimit RateLimitConfig `toml:"ratelimit"`
	WireGuard WireGuardConfig `toml:"wireguard"`
	NFTables  NFTablesConfig  `toml:"nftables"`
	Auth      AuthConfig      `toml:"auth"`
	Admin     AdminConfig     `toml:"admin"`
	Limits    LimitsConfig    `toml:"limits"`
}

// defaultPerUserLimit is the cap applied to a per-user limit whose key is absent
// from the config (unset → 10). An explicit 0 means unlimited and is preserved.
const defaultPerUserLimit = 10

// LimitsConfig holds the two server-wide per-user caps. Each field is a three-state
// pointer mirroring ServerConfig.TLS: nil (key absent) → default 10; an explicit 0 →
// unlimited; a negative value is rejected by Validate. The pointer keeps "unset"
// distinct from "explicit 0" so 0 can carry the "unlimited" meaning.
type LimitsConfig struct {
	MaxDevicesPerUser    *int `toml:"max_devices_per_user"`
	MaxOwnedZonesPerUser *int `toml:"max_owned_zones_per_user"`
}

type ServerConfig struct {
	Listen string `toml:"listen"`
	// TLS is a three-state pointer: nil (key absent) and true both mean HTTPS;
	// only an explicit `tls = false` selects plaintext HTTP. The pointer keeps
	// "unset" distinct from "explicit false" so an existing config that never
	// mentioned the key is never silently downgraded to plaintext.
	TLS     *bool  `toml:"tls"`
	TLSCert string `toml:"tls_cert"`
	TLSKey  string `toml:"tls_key"`
	DataDir string `toml:"data_dir"`
}

// TLSEnabled reports whether the control plane listens over HTTPS. It is
// nil-safe: an absent toggle reads as enabled, so the safe default holds even
// on a path that skipped defaulting.
func (s ServerConfig) TLSEnabled() bool { return s.TLS == nil || *s.TLS }

// WarnPlaintextExposure reports whether a startup warning is warranted: the
// control plane is plaintext AND bound to a non-loopback address (so it could
// be reachable beyond the local reverse proxy). It does not block startup.
func (s ServerConfig) WarnPlaintextExposure() bool {
	return !s.TLSEnabled() && !s.listenIsLoopback()
}

// listenIsLoopback reports whether the listen host is a loopback address. An
// empty host (all interfaces), a bare hostname, or an unparseable value is
// conservatively treated as non-loopback so the exposure warning errs toward
// firing.
func (s ServerConfig) listenIsLoopback() bool {
	host, _, err := net.SplitHostPort(s.Listen)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

type LogConfig struct {
	Level string `toml:"level"`
}

type RateLimitConfig struct {
	RPS   float64 `toml:"rps"`
	Burst int     `toml:"burst"`
}

type WireGuardConfig struct {
	Network    string `toml:"network"`
	ListenPort int    `toml:"listen_port"`
	Interface  string `toml:"interface"`
	MTU        int    `toml:"mtu"`
	// Endpoint is the publicly reachable host:port clients dial over UDP. It may
	// differ from the API address and from listen_port (NAT). Returned by GET /server.
	Endpoint string `toml:"endpoint"`
}

// NFTablesConfig names the dedicated isolation table. The family is always `inet`,
// so this is the bare table name (e.g. "lanweave"), not "inet lanweave".
type NFTablesConfig struct {
	Table string `toml:"table"`
}

// AuthConfig is validated for presence here but consumed by feature 002.
type AuthConfig struct {
	JWTSecret Secret `toml:"jwt_secret"`
	JWTTTL    string `toml:"jwt_ttl"`
	// InviteTTL is how long a newly minted invite code stays redeemable, as a Go
	// duration string (e.g. "24h"). Unlike JWTTTL it has NO built-in default:
	// empty/absent means codes never expire (stamped with a NULL expires_at), which
	// keeps "0/empty = never" literally true and avoids silently expiring codes on
	// an upgrade that never set the key. The shipped example provides "24h".
	InviteTTL string `toml:"invite_ttl"`
}

type AdminConfig struct {
	Username string `toml:"username"`
	Password Secret `toml:"password"`
}

// DBPath returns the SQLite file path derived from the data directory.
func (c *Config) DBPath() string {
	return filepath.Join(c.Server.DataDir, "db.sqlite")
}

// Resolve picks the config path: --config flag, then LANWEAVE_CONFIG env, then default.
func Resolve(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	if env := os.Getenv("LANWEAVE_CONFIG"); env != "" {
		return env
	}
	return DefaultConfigPath
}

// Load reads and decodes the TOML file and applies defaults. It does not validate;
// call Validate separately so all problems can be reported together.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	c.applyDefaults()
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.RateLimit.RPS == 0 {
		c.RateLimit.RPS = 100
	}
	if c.RateLimit.Burst == 0 {
		c.RateLimit.Burst = 200
	}
	if c.Auth.JWTTTL == "" {
		c.Auth.JWTTTL = "2h"
	}
	if c.NFTables.Table == "" {
		c.NFTables.Table = "lanweave"
	}
	if c.Limits.MaxDevicesPerUser == nil {
		v := defaultPerUserLimit
		c.Limits.MaxDevicesPerUser = &v
	}
	if c.Limits.MaxOwnedZonesPerUser == nil {
		v := defaultPerUserLimit
		c.Limits.MaxOwnedZonesPerUser = &v
	}
}

// Validate collects every configuration problem and returns them joined, so the
// operator can fix the file in a single pass. A nil return means the config is usable.
func (c *Config) Validate() error {
	var errs []error

	if c.Server.Listen == "" {
		errs = append(errs, errors.New("server.listen is required"))
	} else if _, _, err := net.SplitHostPort(c.Server.Listen); err != nil {
		errs = append(errs, fmt.Errorf("server.listen %q is not host:port: %w", c.Server.Listen, err))
	}

	// Plaintext mode (tls = false, behind a TLS-terminating reverse proxy) needs
	// no certificate, so cert/key are ignored. TLS mode still hard-fails on a
	// missing or invalid cert: an absent toggle defaults to TLS, so existing
	// configs are never silently downgraded.
	if c.Server.TLSEnabled() {
		certOK := requireReadable(&errs, "server.tls_cert", c.Server.TLSCert)
		keyOK := requireReadable(&errs, "server.tls_key", c.Server.TLSKey)
		if certOK && keyOK {
			if _, err := tls.LoadX509KeyPair(c.Server.TLSCert, c.Server.TLSKey); err != nil {
				errs = append(errs, fmt.Errorf("server tls cert/key invalid: %w", err))
			}
		}
	}

	if c.Server.DataDir == "" {
		errs = append(errs, errors.New("server.data_dir is required"))
	} else if info, err := os.Stat(c.Server.DataDir); err != nil {
		errs = append(errs, fmt.Errorf("server.data_dir %q: %w", c.Server.DataDir, err))
	} else if !info.IsDir() {
		errs = append(errs, fmt.Errorf("server.data_dir %q is not a directory", c.Server.DataDir))
	} else if err := checkWritable(c.Server.DataDir); err != nil {
		errs = append(errs, fmt.Errorf("server.data_dir %q not writable: %w", c.Server.DataDir, err))
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level %q must be one of debug|info|warn|error", c.Log.Level))
	}

	if c.RateLimit.RPS <= 0 {
		errs = append(errs, errors.New("ratelimit.rps must be > 0"))
	}
	if c.RateLimit.Burst < int(c.RateLimit.RPS) {
		errs = append(errs, errors.New("ratelimit.burst must be >= ratelimit.rps"))
	}

	if c.WireGuard.Network == "" {
		errs = append(errs, errors.New("wireguard.network is required"))
	} else if _, _, err := net.ParseCIDR(c.WireGuard.Network); err != nil {
		errs = append(errs, fmt.Errorf("wireguard.network %q is not a valid CIDR: %w", c.WireGuard.Network, err))
	}

	if c.WireGuard.Endpoint == "" {
		errs = append(errs, errors.New("wireguard.endpoint is required (public host:port clients dial)"))
	} else if _, _, err := net.SplitHostPort(c.WireGuard.Endpoint); err != nil {
		errs = append(errs, fmt.Errorf("wireguard.endpoint %q is not host:port: %w", c.WireGuard.Endpoint, err))
	}

	if strings.ContainsAny(c.NFTables.Table, " \t") {
		errs = append(errs, errors.New("nftables.table must be a bare name without whitespace (family is always inet)"))
	}

	if len(c.Auth.JWTSecret.Reveal()) < 32 {
		errs = append(errs, errors.New("auth.jwt_secret must be at least 32 bytes"))
	}
	if _, err := time.ParseDuration(c.Auth.JWTTTL); err != nil {
		errs = append(errs, fmt.Errorf("auth.jwt_ttl %q is not a valid duration: %w", c.Auth.JWTTTL, err))
	}
	// invite_ttl has no default: empty means "never expire". A non-empty value must
	// parse as a duration and must not be negative (a negative window is meaningless
	// and is rejected rather than silently treated as never-expire).
	if c.Auth.InviteTTL != "" {
		if d, err := time.ParseDuration(c.Auth.InviteTTL); err != nil {
			errs = append(errs, fmt.Errorf("auth.invite_ttl %q is not a valid duration: %w", c.Auth.InviteTTL, err))
		} else if d < 0 {
			errs = append(errs, fmt.Errorf("auth.invite_ttl %q must not be negative (0/empty = never expire)", c.Auth.InviteTTL))
		}
	}

	if c.Admin.Username == "" {
		errs = append(errs, errors.New("admin.username is required"))
	} else if len(c.Admin.Username) > 64 {
		errs = append(errs, errors.New("admin.username must be <= 64 characters"))
	}
	if c.Admin.Password.Reveal() == "" {
		errs = append(errs, errors.New("admin.password is required"))
	}

	// Per-user caps: a set value must be >= 0 (0 = unlimited). nil means "unset" and
	// is valid — applyDefaults resolves it to 10 — so the check is nil-safe, mirroring
	// the TLS three-state pointer.
	if v := c.Limits.MaxDevicesPerUser; v != nil && *v < 0 {
		errs = append(errs, errors.New("limits.max_devices_per_user must be >= 0 (0 = unlimited)"))
	}
	if v := c.Limits.MaxOwnedZonesPerUser; v != nil && *v < 0 {
		errs = append(errs, errors.New("limits.max_owned_zones_per_user must be >= 0 (0 = unlimited)"))
	}

	return errors.Join(errs...)
}

// requireReadable records an error if the path is empty or not readable. It
// returns true only when the file exists and could be opened.
func requireReadable(errs *[]error, field, path string) bool {
	if path == "" {
		*errs = append(*errs, fmt.Errorf("%s is required", field))
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s %q: %w", field, path, err))
		return false
	}
	_ = f.Close()
	return true
}

func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}
