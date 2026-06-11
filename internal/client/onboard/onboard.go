// Package onboard is the UI-free first-run onboarding controller: it composes the REST
// client, key generation, the secure store, and the local state record into the setup
// sequence. It has no Fyne dependency, so it is tested headlessly against a real server.
package onboard

import (
	"errors"
	"fmt"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/state"
	"lanweave/internal/client/wgkey"
	"lanweave/pkg/protocol"
)

// AuthMode selects between signing in to an existing account and creating a new one.
type AuthMode int

const (
	CreateAccount AuthMode = iota
	SignIn
)

// apiClient is the subset of the REST client onboarding needs. It is an interface so unit
// tests can supply a fake; *apiclient.Client satisfies it.
type apiClient interface {
	Register(invite, username, password string) error
	Login(username, password string) error
	RegisterNode(name, pubKey string) (protocol.NodeResponse, error)
	RegisterNodePlatform(name, pubKey, platform string) (protocol.NodeResponse, error)
	ListNodes() (protocol.NodeListResponse, error)
	ServerInfo() (protocol.ServerInfoResponse, error)
	// Token returns the session token set by Login; persisted after a successful
	// Provision so the panel reuses the session without a second sign-in.
	Token() string
	// RefreshToken returns the refresh token set by Login; persisted alongside the
	// session token so the panel can silently renew without re-prompting.
	RefreshToken() string
}

// The real REST client must satisfy the onboarding interface.
var _ apiClient = (*apiclient.Client)(nil)

// Credentials are the inputs to the authentication step.
type Credentials struct {
	Mode     AuthMode
	Invite   string // create-account only
	Username string
	Password string
}

// Provisioner runs the onboarding sequence against an already-built API client (the
// server URL has been applied when constructing the client).
type Provisioner struct {
	API       apiClient
	Keys      keyring.Store
	StatePath string
	ServerURL string // recorded in the state file
	KeyName   string // defaults to keyring.DeviceKeyName
	// PinnedCertSHA256 is the TOFU certificate fingerprint the user trusted during onboarding
	// (empty when the server verified against a system CA); it is recorded in the state file.
	PinnedCertSHA256 string
	// Platform is the self-reported client platform sent with node registration
	// (feature 031, e.g. "openwrt"). Empty keeps the pre-031 request shape.
	Platform string
}

func (p *Provisioner) keyName() string {
	if p.KeyName != "" {
		return p.KeyName
	}
	return keyring.DeviceKeyName
}

// Authenticate signs in, creating the account first when in create-account mode. It
// returns the apiclient's typed errors so the UI can show a specific message.
func (p *Provisioner) Authenticate(c Credentials) error {
	if c.Mode == CreateAccount {
		if err := p.API.Register(c.Invite, c.Username, c.Password); err != nil {
			return err
		}
	}
	return p.API.Login(c.Username, c.Password)
}

// Provision runs the full sequence: authenticate, generate the device key, store the
// private key (before registration, so it survives a retry), register the device (with
// idempotent recovery), fetch server info, and write the state record. On failure the
// caller may retry; on cancel call Cleanup.
func (p *Provisioner) Provision(c Credentials, nodeName string) (state.Record, error) {
	// Validate before authenticating (pre-031 behavior): an empty device name
	// must not trigger sign-in side effects.
	if nodeName == "" {
		return state.Record{}, errors.New("device name is required")
	}
	if err := p.Authenticate(c); err != nil {
		return state.Record{}, err
	}
	return p.ProvisionDevice(nodeName)
}

// ProvisionDevice is the post-authentication tail of Provision: generate and
// store the device key, register the device, fetch server info, persist the
// state record and cache the tokens. Split out for clients whose sign-in and
// device registration are separate commands (the router CLI, feature 031).
func (p *Provisioner) ProvisionDevice(nodeName string) (state.Record, error) {
	if nodeName == "" {
		return state.Record{}, errors.New("device name is required")
	}

	priv, pub, err := wgkey.GenerateKeyPair()
	if err != nil {
		return state.Record{}, err
	}
	// Durably store the private key before registering, so a failed local save can be
	// retried without regenerating the device identity.
	if err := p.Keys.Set(p.keyName(), []byte(priv)); err != nil {
		return state.Record{}, fmt.Errorf("store device key: %w", err)
	}

	ip, nodeID, err := p.registerDevice(nodeName, pub)
	if err != nil {
		return state.Record{}, err
	}

	info, err := p.API.ServerInfo()
	if err != nil {
		return state.Record{}, err
	}

	rec := state.Record{
		ServerURL:        p.ServerURL,
		NodeName:         nodeName,
		IP:               ip,
		ServerPublicKey:  info.PublicKey,
		Endpoint:         info.Endpoint,
		Network:          info.Network,
		PinnedCertSHA256: p.PinnedCertSHA256,
		NodeID:           nodeID,
	}
	if err := state.Save(p.StatePath, rec); err != nil {
		return state.Record{}, fmt.Errorf("save state: %w", err)
	}
	// Cache the session token last — only after every prior step has succeeded — so the
	// management panel reuses this sign-in instead of prompting again. Persisting it here
	// keeps the device key, state record, and session token consistently present (and
	// consistently absent after Cleanup).
	if err := p.Keys.Set(keyring.SessionTokenName, []byte(p.API.Token())); err != nil {
		return state.Record{}, fmt.Errorf("cache session: %w", err)
	}
	// Persist the refresh token next to the session token (same final, all-succeeded
	// point) so the panel can silently renew the access token on its next launch.
	if err := p.Keys.Set(keyring.RefreshTokenName, []byte(p.API.RefreshToken())); err != nil {
		return state.Record{}, fmt.Errorf("cache refresh token: %w", err)
	}
	return rec, nil
}

// registerDevice registers pub under nodeName and returns the assigned address. On
// pubkey_taken (this session's earlier attempt already registered the device) it recovers
// the address by matching the name in the device list — idempotent, no duplicate. A
// node_name_taken error is returned for the UI to ask for a different name.
func (p *Provisioner) registerDevice(nodeName, pub string) (string, int64, error) {
	node, err := p.API.RegisterNodePlatform(nodeName, pub, p.Platform)
	switch {
	case err == nil:
		return node.IP, node.ID, nil
	case errors.Is(err, apiclient.ErrPubKeyTaken):
		list, lerr := p.API.ListNodes()
		if lerr != nil {
			return "", 0, lerr
		}
		for _, n := range list.Nodes {
			if n.Name == nodeName {
				return n.IP, n.ID, nil
			}
		}
		return "", 0, errors.New("device was registered but its address could not be recovered")
	default:
		return "", 0, err
	}
}

// Cleanup deletes the stored device key, the cached session and refresh tokens, and clears
// any partial state record so a cancelled or failed setup leaves the machine fresh for the
// next launch.
func (p *Provisioner) Cleanup() error {
	return errors.Join(
		p.Keys.Delete(p.keyName()),
		p.Keys.Delete(keyring.SessionTokenName),
		p.Keys.Delete(keyring.RefreshTokenName),
		state.Clear(p.StatePath),
	)
}

// Target is where the app should go on launch.
type Target int

const (
	Wizard Target = iota
	Home
)

// StartupTarget decides, from the local state, whether to run the wizard or go straight
// to the home area. A present, valid record means Home; anything else means Wizard.
func StartupTarget(statePath string) (Target, *state.Record) {
	rec, err := state.Load(statePath)
	if err != nil {
		return Wizard, nil
	}
	return Home, &rec
}
