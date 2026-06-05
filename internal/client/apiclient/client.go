// Package apiclient is the client-side REST client for the lanweave server. It speaks
// HTTPS + JSON, reuses the shared pkg/protocol DTOs, and maps server responses to typed
// errors so the UI can show specific, human-readable messages.
package apiclient

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
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
	ErrUnreachable   = errors.New("server unreachable")
	ErrUntrustedCert = errors.New("server certificate not trusted")
	ErrAuthFailed    = errors.New("sign-in failed")
	ErrInviteInvalid = errors.New("invite code invalid or already used")
	ErrUsernameTaken = errors.New("username already taken")
	ErrNodeNameTaken = errors.New("device name already taken")
	ErrPubKeyTaken   = errors.New("device key already registered")
	ErrPoolExhausted = errors.New("no addresses available")
	ErrServer        = errors.New("server error")
)

// Option configures a Client.
type Option func(*Client)

// WithInsecure disables TLS certificate verification (advanced/troubleshooting only;
// never exposed in the UI). Mutually exclusive with WithRootCAs.
func WithInsecure() Option { return func(c *Client) { c.insecure = true } }

// WithRootCAs trusts a specific certificate pool in addition to nothing else, so the
// verify-on path can be exercised against a known server certificate (e.g. tests) without
// disabling verification. Mutually exclusive with WithInsecure.
func WithRootCAs(pool *x509.CertPool) Option { return func(c *Client) { c.rootCAs = pool } }

// Client talks to one lanweave server.
type Client struct {
	baseURL  string
	http     *http.Client
	token    string
	insecure bool
	rootCAs  *x509.CertPool
}

// New builds a Client for the given base URL (e.g. "https://vpn.example.com").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{baseURL: strings.TrimRight(baseURL, "/")}
	for _, o := range opts {
		o(c)
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case c.insecure:
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // advanced --insecure flag only
	case c.rootCAs != nil:
		tlsCfg.RootCAs = c.rootCAs
	}
	c.http = &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	return c
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

// Login authenticates and stores the session token on the client.
func (c *Client) Login(username, password string) error {
	var resp protocol.LoginResponse
	if _, err := c.do(http.MethodPost, "/api/v1/login", false, protocol.LoginRequest{Username: username, Password: password}, &resp); err != nil {
		return err
	}
	c.token = resp.Token
	return nil
}

// RegisterNode registers this device's public key under a name and returns the node.
func (c *Client) RegisterNode(name, pubKey string) (protocol.NodeResponse, error) {
	var resp protocol.NodeResponse
	_, err := c.do(http.MethodPost, "/api/v1/nodes", true, protocol.RegisterNodeRequest{Name: name, WGPubKey: pubKey}, &resp)
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

// do performs a request, decoding into out (when non-nil) and mapping failures to typed
// errors based on the HTTP status and the error envelope's code.
func (c *Client) do(method, path string, auth bool, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		var certErr x509.UnknownAuthorityError
		var hostErr x509.HostnameError
		if errors.As(err, &certErr) || errors.As(err, &hostErr) {
			return 0, ErrUntrustedCert
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			if inner := urlErr.Unwrap(); inner != nil {
				var ce x509.UnknownAuthorityError
				var he x509.HostnameError
				if errors.As(inner, &ce) || errors.As(inner, &he) {
					return 0, ErrUntrustedCert
				}
			}
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
	return resp.StatusCode, c.mapError(path, resp)
}

// mapError turns a non-2xx response into a typed error using the status and the error
// envelope's code.
func (c *Client) mapError(path string, resp *http.Response) error {
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
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrAuthFailed
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
