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
	ListNodes() (protocol.NodeListResponse, error)
	ServerInfo() (protocol.ServerInfoResponse, error)
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
	if nodeName == "" {
		return state.Record{}, errors.New("device name is required")
	}
	if err := p.Authenticate(c); err != nil {
		return state.Record{}, err
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

	ip, err := p.registerDevice(nodeName, pub)
	if err != nil {
		return state.Record{}, err
	}

	info, err := p.API.ServerInfo()
	if err != nil {
		return state.Record{}, err
	}

	rec := state.Record{
		ServerURL:       p.ServerURL,
		NodeName:        nodeName,
		IP:              ip,
		ServerPublicKey: info.PublicKey,
		Endpoint:        info.Endpoint,
		Network:         info.Network,
	}
	if err := state.Save(p.StatePath, rec); err != nil {
		return state.Record{}, fmt.Errorf("save state: %w", err)
	}
	return rec, nil
}

// registerDevice registers pub under nodeName and returns the assigned address. On
// pubkey_taken (this session's earlier attempt already registered the device) it recovers
// the address by matching the name in the device list — idempotent, no duplicate. A
// node_name_taken error is returned for the UI to ask for a different name.
func (p *Provisioner) registerDevice(nodeName, pub string) (string, error) {
	node, err := p.API.RegisterNode(nodeName, pub)
	switch {
	case err == nil:
		return node.IP, nil
	case errors.Is(err, apiclient.ErrPubKeyTaken):
		list, lerr := p.API.ListNodes()
		if lerr != nil {
			return "", lerr
		}
		for _, n := range list.Nodes {
			if n.Name == nodeName {
				return n.IP, nil
			}
		}
		return "", errors.New("device was registered but its address could not be recovered")
	default:
		return "", err
	}
}

// Cleanup deletes the stored device key and clears any partial state record so a
// cancelled or failed setup leaves the machine fresh for the next launch.
func (p *Provisioner) Cleanup() error {
	return errors.Join(
		p.Keys.Delete(p.keyName()),
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
