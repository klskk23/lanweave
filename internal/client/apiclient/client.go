// Package apiclient is the client-side REST client for the lanweave server. It speaks
// HTTPS + JSON, reuses the shared pkg/protocol DTOs, and maps server responses to typed
// errors so the UI can show specific, human-readable messages.
package apiclient

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lanweave/pkg/protocol"
)

// Typed errors surfaced to the onboarding flow / UI.
var (
	ErrUnreachable        = errors.New("server unreachable")
	ErrUntrustedCert      = errors.New("server certificate not trusted")
	ErrCertChanged        = errors.New("server certificate changed")
	ErrAuthFailed         = errors.New("sign-in failed")
	ErrInviteInvalid      = errors.New("invite code invalid or already used")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrNodeNameTaken      = errors.New("device name already taken")
	ErrPubKeyTaken        = errors.New("device key already registered")
	ErrPoolExhausted      = errors.New("no addresses available")
	ErrDeviceLimitReached = errors.New("device limit reached")
	ErrServer             = errors.New("server error")

	// ErrRefreshFailed reports that a silent token refresh did not succeed (the
	// refresh token was rejected or the server was unreachable), so the caller must
	// fall back to password sign-in.
	ErrRefreshFailed = errors.New("refresh failed")

	// Zone/session errors (feature 011).
	ErrSessionExpired        = errors.New("session expired")
	ErrZoneNameTaken         = errors.New("zone name already taken")
	ErrOwnedZoneLimitReached = errors.New("owned-zone limit reached")
	ErrZoneOrPassword        = errors.New("invalid zone or password")
	ErrNotOwner              = errors.New("only the zone owner can do that")
	ErrNotMember             = errors.New("not a member of that zone")
	ErrZoneNotFound          = errors.New("zone not found")
)

// CertError reports a TLS certificate that neither matched the configured pin nor verified
// against the configured roots. It carries the presented leaf certificate's fingerprint (for
// a trust prompt) and whether a pin was configured (Changed=true means a previously trusted
// certificate was replaced by an unrecognized one, vs a first-time untrusted certificate).
type CertError struct {
	Fingerprint string // lowercase-hex SHA-256 of the presented leaf certificate
	Changed     bool
}

func (e *CertError) Error() string {
	if e.Changed {
		return "server certificate changed (fingerprint " + e.Fingerprint + ")"
	}
	return "server certificate not trusted (fingerprint " + e.Fingerprint + ")"
}

// Is lets callers match a *CertError with errors.Is against the sentinel that fits its kind:
// a changed certificate matches ErrCertChanged, a first-time untrusted one ErrUntrustedCert.
func (e *CertError) Is(target error) bool {
	if e.Changed {
		return target == ErrCertChanged
	}
	return target == ErrUntrustedCert
}

// Option configures a Client.
type Option func(*Client)

// WithInsecure disables TLS certificate verification (advanced/troubleshooting only;
// never exposed in the UI). Mutually exclusive with WithRootCAs / WithPinnedCert.
func WithInsecure() Option { return func(c *Client) { c.insecure = true } }

// WithRootCAs trusts a specific certificate pool in addition to nothing else, so the
// verify-on path can be exercised against a known server certificate (e.g. tests) without
// disabling verification. Mutually exclusive with WithInsecure.
func WithRootCAs(pool *x509.CertPool) Option { return func(c *Client) { c.rootCAs = pool } }

// WithPinnedCert trusts the server certificate whose leaf SHA-256 fingerprint (lowercase hex)
// equals fp, in addition to the system roots (TOFU). Verification passes if the presented
// leaf matches the pin OR chains to a trusted root. Mutually exclusive with WithInsecure.
func WithPinnedCert(fp string) Option { return func(c *Client) { c.pinnedCert = fp } }

// certFingerprint is the lowercase-hex SHA-256 of a certificate's DER bytes.
func certFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// Client talks to one lanweave server.
type Client struct {
	baseURL      string
	http         *http.Client
	token        string
	refreshToken string
	insecure     bool
	rootCAs      *x509.CertPool
	pinnedCert   string
}

// New builds a Client for the given base URL (e.g. "https://vpn.example.com").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{baseURL: strings.TrimRight(baseURL, "/")}
	for _, o := range opts {
		o(c)
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.insecure {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // advanced --insecure flag only
	} else {
		// Take over verification so a pinned certificate (TOFU) is accepted alongside the
		// system/configured roots, and so the leaf fingerprint is captured on failure for the
		// trust prompt. InsecureSkipVerify only disables Go's *default* check; VerifyConnection
		// below is the sole authority.
		host := serverName(c.baseURL)
		pin, roots := c.pinnedCert, c.rootCAs
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // replaced by VerifyConnection
		tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
			leaf := cs.PeerCertificates[0]
			fp := certFingerprint(leaf)
			if pin != "" && fp == pin {
				return nil
			}
			inter := x509.NewCertPool()
			for _, ic := range cs.PeerCertificates[1:] {
				inter.AddCert(ic)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: inter}); err == nil {
				return nil
			}
			return &CertError{Fingerprint: fp, Changed: pin != ""}
		}
	}
	c.http = &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	return c
}

// serverName extracts the host (no port) from a base URL for certificate hostname
// verification, so an IP-literal or named server is checked against the right SANs.
func serverName(baseURL string) string {
	if u, err := url.Parse(baseURL); err == nil {
		return u.Hostname()
	}
	return ""
}

// Token returns the current session token (set by Login).
func (c *Client) Token() string { return c.token }

// Register creates a new account with an invite code, then leaves the client
// unauthenticated (call Login next to obtain a token).
func (c *Client) Register(invite, username, password string) error {
	body := protocol.RegisterRequest{InviteCode: invite, Username: username, Password: password}
	_, err := c.do(http.MethodPost, "/api/v1/register", false, body, nil)
	return err
}

// Login authenticates and stores the session token and refresh token on the client.
func (c *Client) Login(username, password string) error {
	var resp protocol.LoginResponse
	if _, err := c.do(http.MethodPost, "/api/v1/login", false, protocol.LoginRequest{Username: username, Password: password}, &resp); err != nil {
		return err
	}
	c.token = resp.Token
	c.refreshToken = resp.RefreshToken
	return nil
}

// Refresh exchanges the held refresh token for a fresh access token, storing it on
// the client. It returns ErrRefreshFailed if no refresh token is held or the server
// rejects it — the caller then falls back to password sign-in.
func (c *Client) Refresh() error {
	if c.refreshToken == "" {
		return ErrRefreshFailed
	}
	var resp protocol.RefreshResponse
	if _, err := c.do(http.MethodPost, "/api/v1/refresh", false, protocol.RefreshRequest{RefreshToken: c.refreshToken}, &resp); err != nil {
		return fmt.Errorf("%w: %v", ErrRefreshFailed, err)
	}
	c.token = resp.Token
	return nil
}

// Logout revokes the held refresh token server-side. It is best-effort: with no
// refresh token there is nothing to revoke, and network/server errors are returned
// so the caller can decide, but local sign-out should proceed regardless.
func (c *Client) Logout() error {
	if c.refreshToken == "" {
		return nil
	}
	_, err := c.do(http.MethodPost, "/api/v1/logout", false, protocol.LogoutRequest{RefreshToken: c.refreshToken}, nil)
	return err
}

// RegisterNode registers this device's public key under a name and returns the node.
func (c *Client) RegisterNode(name, pubKey string) (protocol.NodeResponse, error) {
	return c.RegisterNodePlatform(name, pubKey, "")
}

// RegisterNodePlatform is RegisterNode with an explicit self-reported platform
// (e.g. "openwrt", the announce-capability gate of feature 030). Empty platform
// keeps the pre-030 request shape, so existing clients are unchanged.
func (c *Client) RegisterNodePlatform(name, pubKey, platform string) (protocol.NodeResponse, error) {
	var resp protocol.NodeResponse
	_, err := c.do(http.MethodPost, "/api/v1/nodes", true,
		protocol.RegisterNodeRequest{Name: name, WGPubKey: pubKey, Platform: platform}, &resp)
	return resp, err
}

// ListNodes returns the caller's devices (used to recover an address after an idempotent
// retry — the public key is never returned, so the name is the match key).
func (c *Client) ListNodes() (protocol.NodeListResponse, error) {
	var resp protocol.NodeListResponse
	_, err := c.do(http.MethodGet, "/api/v1/nodes", true, nil, &resp)
	return resp, err
}

// ServerInfo returns the server connection details for building the tunnel later.
func (c *Client) ServerInfo() (protocol.ServerInfoResponse, error) {
	var resp protocol.ServerInfoResponse
	_, err := c.do(http.MethodGet, "/api/v1/server", true, nil, &resp)
	return resp, err
}

// SetToken sets the bearer token (e.g. a session restored from the secure store).
func (c *Client) SetToken(token string) { c.token = token }

// SetRefreshToken sets the refresh token (e.g. restored from the secure store on launch).
func (c *Client) SetRefreshToken(rt string) { c.refreshToken = rt }

// RefreshToken returns the currently held refresh token (cached after Login/SetRefreshToken).
func (c *Client) RefreshToken() string { return c.refreshToken }

// Me returns the signed-in user; used to validate a cached session.
func (c *Client) Me() (protocol.MeResponse, error) {
	var resp protocol.MeResponse
	_, err := c.do(http.MethodGet, "/api/v1/me", true, nil, &resp)
	return resp, err
}

// CreateZone creates a password-protected zone owned by the caller. When nodeID is
// non-zero, that device is auto-joined to the new zone in the same request.
func (c *Client) CreateZone(name string, nodeID int64, password string) (protocol.ZoneResponse, error) {
	var resp protocol.ZoneResponse
	_, err := c.do(http.MethodPost, "/api/v1/zones", true, protocol.CreateZoneRequest{Name: name, Password: password, NodeID: nodeID}, &resp)
	return resp, err
}

// ListZones returns the zones the caller participates in (with is_owner).
func (c *Client) ListZones() (protocol.ZoneListResponse, error) {
	var resp protocol.ZoneListResponse
	_, err := c.do(http.MethodGet, "/api/v1/zones", true, nil, &resp)
	return resp, err
}

// JoinZone admits one of the caller's devices to a zone by name + password.
func (c *Client) JoinZone(name string, nodeID int64, password string) error {
	_, err := c.do(http.MethodPost, "/api/v1/zones/"+url.PathEscape(name)+"/join", true, protocol.JoinZoneRequest{NodeID: nodeID, Password: password}, nil)
	return err
}

// LeaveZone removes one of the caller's devices from a zone.
func (c *Client) LeaveZone(name string, nodeID int64) error {
	_, err := c.do(http.MethodPost, "/api/v1/zones/"+url.PathEscape(name)+"/leave", true, protocol.LeaveZoneRequest{NodeID: nodeID}, nil)
	return err
}

// ZoneMembers lists a zone's members (name, owner, address, and node id).
func (c *Client) ZoneMembers(name string) (protocol.ZoneMembersResponse, error) {
	var resp protocol.ZoneMembersResponse
	_, err := c.do(http.MethodGet, "/api/v1/zones/"+url.PathEscape(name)+"/members", true, nil, &resp)
	return resp, err
}

// ChangeZonePassword rotates a zone's password (owner only).
func (c *Client) ChangeZonePassword(name, password string) error {
	_, err := c.do(http.MethodPatch, "/api/v1/zones/"+url.PathEscape(name), true, protocol.ChangeZonePasswordRequest{Password: password}, nil)
	return err
}

// DeleteZone deletes a zone (owner only).
func (c *Client) DeleteZone(name string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/zones/"+url.PathEscape(name), true, nil, nil)
	return err
}

// KickMember removes a member device from a zone by node id (owner only).
func (c *Client) KickMember(name string, nodeID int64) error {
	_, err := c.do(http.MethodDelete, fmt.Sprintf("/api/v1/zones/%s/members/%d", url.PathEscape(name), nodeID), true, nil, nil)
	return err
}

// DeleteNode removes one of the caller's own devices (used by logout). The server enforces
// ownership, so a foreign or unknown id is a 404 (mapped to ErrZoneNotFound), never another
// user's node. Expects 204 No Content.
func (c *Client) DeleteNode(nodeID int64) error {
	_, err := c.do(http.MethodDelete, fmt.Sprintf("/api/v1/nodes/%d", nodeID), true, nil, nil)
	return err
}

// Insecure reports whether this client was built with TLS certificate verification disabled
// (via WithInsecure / the --insecure CLI flag). Used to drive the persistent "certificate
// not verified" indicator.
func (c *Client) Insecure() bool { return c.insecure }

// do performs a request, decoding into out (when non-nil) and mapping failures to typed
// errors based on the HTTP status and the error envelope's code. On an authenticated
// call that returns 401 (ErrSessionExpired) while a refresh token is held, it silently
// refreshes the access token once and retries the original request exactly once — the
// single chokepoint every authenticated call flows through, so lazy refresh is inherited
// everywhere without per-call changes.
func (c *Client) do(method, path string, auth bool, body, out any) (int, error) {
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode request: %w", err)
		}
		bodyBytes = b
	}

	status, err := c.attempt(method, path, auth, bodyBytes, out)
	if auth && errors.Is(err, ErrSessionExpired) && c.refreshToken != "" {
		if rerr := c.Refresh(); rerr != nil {
			return status, err // refresh failed → surface the original session-expired error
		}
		return c.attempt(method, path, auth, bodyBytes, out)
	}
	return status, err
}

// attempt performs a single request without any refresh/retry logic.
func (c *Client) attempt(method, path string, auth bool, bodyBytes []byte, out any) (int, error) {
	var reader io.Reader
	if bodyBytes != nil {
		reader = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	if bodyBytes != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Our VerifyConnection surfaces a *CertError (first-trust or changed) carrying the
		// leaf fingerprint; return it so the UI can prompt and pin.
		var ce *CertError
		if errors.As(err, &ce) {
			return 0, ce
		}
		// Fallback for any standard verification error not routed through VerifyConnection.
		var certErr x509.UnknownAuthorityError
		var hostErr x509.HostnameError
		if errors.As(err, &certErr) || errors.As(err, &hostErr) {
			return 0, ErrUntrustedCert
		}
		return 0, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return resp.StatusCode, fmt.Errorf("decode response: %w", err)
			}
		}
		return resp.StatusCode, nil
	}
	return resp.StatusCode, c.mapError(path, auth, resp)
}

// mapError turns a non-2xx response into a typed error using the status and the error
// envelope's code. `auth` distinguishes a 401 on an authenticated call (session expired)
// from a 401 on sign-in (bad credentials).
func (c *Client) mapError(path string, auth bool, resp *http.Response) error {
	var env protocol.ErrorResponse
	if b, _ := io.ReadAll(resp.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &env)
	}
	switch env.Error {
	case "node_name_taken":
		return ErrNodeNameTaken
	case "pubkey_taken":
		return ErrPubKeyTaken
	case "pool_exhausted":
		return ErrPoolExhausted
	case "device_limit_reached":
		return ErrDeviceLimitReached
	case "zone_name_taken":
		return ErrZoneNameTaken
	case "zone_limit_reached":
		return ErrOwnedZoneLimitReached
	case "invalid_zone_or_password":
		return ErrZoneOrPassword
	case "forbidden":
		return ErrNotOwner
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		if auth {
			return ErrSessionExpired
		}
		return ErrAuthFailed
	case http.StatusNotFound:
		if strings.Contains(path, "/leave") || strings.Contains(path, "/members/") {
			return ErrNotMember
		}
		return ErrZoneNotFound
	case http.StatusConflict:
		// Account creation conflicts: a taken username vs an already-used invite.
		if path == "/api/v1/register" {
			if strings.Contains(strings.ToLower(env.Error+env.Message), "user") {
				return ErrUsernameTaken
			}
			return ErrInviteInvalid
		}
		return fmt.Errorf("%w: %s", ErrServer, env.Error)
	case http.StatusBadRequest:
		if path == "/api/v1/register" {
			return ErrInviteInvalid
		}
		return fmt.Errorf("%w: %s", ErrServer, env.Error)
	}
	if resp.StatusCode >= 500 {
		return ErrServer
	}
	return fmt.Errorf("%w: unexpected status %d (%s)", ErrServer, resp.StatusCode, env.Error)
}
