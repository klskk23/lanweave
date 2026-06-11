package onboard_test

import (
	"errors"
	"path/filepath"
	"testing"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/onboard"
	"lanweave/internal/client/state"
	"lanweave/pkg/protocol"
)

// fakeAPI is a programmable stand-in for the REST client (our own seam). It returns the
// apiclient package's typed errors so the controller's recovery logic is exercised.
type fakeAPI struct {
	registerErr   error
	loginErr      error
	registerNode  func(name, pub string) (protocol.NodeResponse, error)
	listNodes     func() (protocol.NodeListResponse, error)
	serverInfo    protocol.ServerInfoResponse
	serverInfoErr error
	token         string // session token returned after a successful Login
	refreshToken  string // refresh token returned after a successful Login

	registerCalls     int
	registerNodeCalls int
	lastPlatform      string
}

func (f *fakeAPI) Register(_, _, _ string) error { f.registerCalls++; return f.registerErr }
func (f *fakeAPI) Login(_, _ string) error       { return f.loginErr }
func (f *fakeAPI) Token() string                 { return f.token }
func (f *fakeAPI) RefreshToken() string          { return f.refreshToken }
func (f *fakeAPI) RegisterNode(name, pub string) (protocol.NodeResponse, error) {
	return f.RegisterNodePlatform(name, pub, "")
}
func (f *fakeAPI) RegisterNodePlatform(name, pub, platform string) (protocol.NodeResponse, error) {
	f.registerNodeCalls++
	f.lastPlatform = platform
	if f.registerNode != nil {
		return f.registerNode(name, pub)
	}
	return protocol.NodeResponse{ID: 7, Name: name, IP: "100.127.0.2", Platform: platform}, nil
}
func (f *fakeAPI) ListNodes() (protocol.NodeListResponse, error) {
	if f.listNodes != nil {
		return f.listNodes()
	}
	return protocol.NodeListResponse{}, nil
}
func (f *fakeAPI) ServerInfo() (protocol.ServerInfoResponse, error) {
	return f.serverInfo, f.serverInfoErr
}

func newProvisioner(t *testing.T, api *fakeAPI) (*onboard.Provisioner, *keyring.Fake, string) {
	t.Helper()
	fk := keyring.NewFake()
	statePath := filepath.Join(t.TempDir(), "lanweave", "state.json")
	p := &onboard.Provisioner{
		API: api, Keys: fk, StatePath: statePath, ServerURL: "https://vpn.example.com",
	}
	return p, fk, statePath
}

func okServerInfo() protocol.ServerInfoResponse {
	return protocol.ServerInfoResponse{PublicKey: "srv-pub", Endpoint: "vpn:51820", Network: "100.127.0.0/16", MTU: 1420}
}

func TestProvisionCreateAccount(t *testing.T) {
	api := &fakeAPI{serverInfo: okServerInfo()}
	p, fk, statePath := newProvisioner(t, api)

	rec, err := p.Provision(onboard.Credentials{Mode: onboard.CreateAccount, Invite: "good", Username: "alice", Password: "pw"}, "laptop")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if api.registerCalls != 1 {
		t.Errorf("expected account creation; register calls = %d", api.registerCalls)
	}
	if rec.IP != "100.127.0.2" || rec.ServerPublicKey != "srv-pub" || rec.Network != "100.127.0.0/16" {
		t.Errorf("record missing server info: %+v", rec)
	}
	if key, err := fk.Get(keyring.DeviceKeyName); err != nil || len(key) == 0 {
		t.Errorf("private key not stored in vault: %v", err)
	}
	if got, err := state.Load(statePath); err != nil || got.IP != "100.127.0.2" {
		t.Errorf("state not written: %+v %v", got, err)
	}
}

func TestProvisionSignIn(t *testing.T) {
	api := &fakeAPI{serverInfo: okServerInfo()}
	p, _, statePath := newProvisioner(t, api)

	if _, err := p.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "alice", Password: "pw"}, "laptop"); err != nil {
		t.Fatalf("provision sign-in: %v", err)
	}
	if api.registerCalls != 0 {
		t.Errorf("sign-in must not create an account; register calls = %d", api.registerCalls)
	}
	if !state.Exists(statePath) {
		t.Error("state not written on sign-in path")
	}
}

func TestProvisionPersistsSession(t *testing.T) {
	api := &fakeAPI{serverInfo: okServerInfo(), token: "tok-xyz"}
	p, fk, _ := newProvisioner(t, api)

	if _, err := p.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "alice", Password: "pw"}, "laptop"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	tok, err := fk.Get(keyring.SessionTokenName)
	if err != nil || string(tok) != "tok-xyz" {
		t.Errorf("session token not cached after provision: got %q err=%v, want %q", tok, err, "tok-xyz")
	}
}

// TestProvisionPersistsRefreshToken covers US1: the refresh token returned by Login is
// cached in the vault after a successful Provision, so the panel can silently renew later.
func TestProvisionPersistsRefreshToken(t *testing.T) {
	api := &fakeAPI{serverInfo: okServerInfo(), token: "tok-xyz", refreshToken: "rt-abc"}
	p, fk, _ := newProvisioner(t, api)

	if _, err := p.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "alice", Password: "pw"}, "laptop"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	rt, err := fk.Get(keyring.RefreshTokenName)
	if err != nil || string(rt) != "rt-abc" {
		t.Errorf("refresh token not cached after provision: got %q err=%v, want %q", rt, err, "rt-abc")
	}
}

// failOnSessionSet wraps a Store and fails only when caching the session token, proving
// Provision surfaces a final-step persistence failure instead of returning a bad success.
type failOnSessionSet struct {
	keyring.Store
}

func (f failOnSessionSet) Set(name string, secret []byte) error {
	if name == keyring.SessionTokenName {
		return errors.New("vault write failed")
	}
	return f.Store.Set(name, secret)
}

func TestProvisionSessionSaveFailure(t *testing.T) {
	api := &fakeAPI{serverInfo: okServerInfo(), token: "tok-xyz"}
	statePath := filepath.Join(t.TempDir(), "lanweave", "state.json")
	p := &onboard.Provisioner{
		API: api, Keys: failOnSessionSet{keyring.NewFake()}, StatePath: statePath, ServerURL: "https://vpn.example.com",
	}
	if _, err := p.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "a", Password: "p"}, "laptop"); err == nil {
		t.Fatal("expected provision to fail when the session token cannot be cached")
	}
}

func TestStartupTarget(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "lanweave", "state.json")
	// Absent → wizard.
	if target, rec := onboard.StartupTarget(statePath); target != onboard.Wizard || rec != nil {
		t.Errorf("absent state: target=%v rec=%v, want Wizard/nil", target, rec)
	}
	// Present+valid → home.
	_ = state.Save(statePath, state.Record{ServerURL: "https://s", NodeName: "n", IP: "100.127.0.2", ServerPublicKey: "k", Endpoint: "e", Network: "100.127.0.0/16"})
	if target, rec := onboard.StartupTarget(statePath); target != onboard.Home || rec == nil || rec.IP != "100.127.0.2" {
		t.Errorf("present state: target=%v rec=%+v, want Home + record", target, rec)
	}
}

func TestProvisionAuthAndNameErrors(t *testing.T) {
	// Sign-in failure surfaces ErrAuthFailed and writes nothing.
	api := &fakeAPI{loginErr: apiclient.ErrAuthFailed}
	p, fk, statePath := newProvisioner(t, api)
	if _, err := p.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "a", Password: "bad"}, "laptop"); !errors.Is(err, apiclient.ErrAuthFailed) {
		t.Errorf("auth fail: got %v, want ErrAuthFailed", err)
	}
	if state.Exists(statePath) {
		t.Error("state written despite auth failure")
	}
	if _, err := fk.Get(keyring.DeviceKeyName); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("key stored despite auth failure (auth happens before keygen)")
	}
	if _, err := fk.Get(keyring.SessionTokenName); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("session token stored despite auth failure (token persisted only after full success)")
	}

	// Duplicate device name surfaces ErrNodeNameTaken for the UI to recover.
	api2 := &fakeAPI{serverInfo: okServerInfo(), registerNode: func(_, _ string) (protocol.NodeResponse, error) {
		return protocol.NodeResponse{}, apiclient.ErrNodeNameTaken
	}}
	p2, _, _ := newProvisioner(t, api2)
	if _, err := p2.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "a", Password: "p"}, "taken"); !errors.Is(err, apiclient.ErrNodeNameTaken) {
		t.Errorf("dup name: got %v, want ErrNodeNameTaken", err)
	}
}

func TestCancelCleanup(t *testing.T) {
	api := &fakeAPI{serverInfo: okServerInfo(), token: "tok-xyz"}
	p, fk, statePath := newProvisioner(t, api)
	if _, err := p.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "a", Password: "p"}, "laptop"); err != nil {
		t.Fatal(err)
	}
	// The vault key, the session token, and the state record all exist after a successful provision.
	if _, err := fk.Get(keyring.DeviceKeyName); err != nil {
		t.Fatal("precondition: key should exist")
	}
	if tok, err := fk.Get(keyring.SessionTokenName); err != nil || len(tok) == 0 {
		t.Fatal("precondition: session token should exist")
	}
	if !state.Exists(statePath) {
		t.Fatal("precondition: state should exist")
	}
	// Cleanup removes all three, leaving a fresh machine.
	if err := p.Cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := fk.Get(keyring.DeviceKeyName); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("vault key remains after cleanup")
	}
	if _, err := fk.Get(keyring.SessionTokenName); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("session token remains after cleanup")
	}
	if state.Exists(statePath) {
		t.Error("state record remains after cleanup")
	}
}

// TestPartialFailureRecovery: a prior attempt registered the device (server returns
// pubkey_taken), so the controller recovers the address from the device list and
// completes without creating a duplicate.
func TestPartialFailureRecovery(t *testing.T) {
	api := &fakeAPI{
		serverInfo: okServerInfo(),
		registerNode: func(_, _ string) (protocol.NodeResponse, error) {
			return protocol.NodeResponse{}, apiclient.ErrPubKeyTaken
		},
		listNodes: func() (protocol.NodeListResponse, error) {
			return protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
				{ID: 7, Name: "other", IP: "100.127.0.3"},
				{ID: 8, Name: "laptop", IP: "100.127.0.5"},
			}}, nil
		},
	}
	p, _, statePath := newProvisioner(t, api)

	rec, err := p.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "a", Password: "p"}, "laptop")
	if err != nil {
		t.Fatalf("recovery provision: %v", err)
	}
	if rec.IP != "100.127.0.5" {
		t.Errorf("recovered address = %s, want 100.127.0.5 (matched by name)", rec.IP)
	}
	if api.registerNodeCalls != 1 {
		t.Errorf("RegisterNode called %d times; recovery must not re-create (no duplicate)", api.registerNodeCalls)
	}
	if !state.Exists(statePath) {
		t.Error("state not written after recovery")
	}
}

// TestProvisionPlatformAndNodeID covers the 031 additions: the self-reported
// platform reaches registration and the assigned node id lands in the state
// record (zero only for pre-v3 records).
func TestProvisionPlatformAndNodeID(t *testing.T) {
	api := &fakeAPI{}
	dir := t.TempDir()
	p := &onboard.Provisioner{
		API:       api,
		Keys:      keyring.NewFake(),
		StatePath: filepath.Join(dir, "state.json"),
		ServerURL: "https://vpn.example.com",
		Platform:  "openwrt",
	}
	rec, err := p.Provision(onboard.Credentials{Mode: onboard.SignIn, Username: "alice", Password: "pw"}, "router")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if api.lastPlatform != "openwrt" {
		t.Errorf("platform sent = %q, want openwrt", api.lastPlatform)
	}
	if rec.NodeID != 7 {
		t.Errorf("state NodeID = %d, want 7", rec.NodeID)
	}
	loaded, err := state.Load(p.StatePath)
	if err != nil || loaded.NodeID != 7 {
		t.Errorf("persisted NodeID = %d (%v), want 7", loaded.NodeID, err)
	}
}
