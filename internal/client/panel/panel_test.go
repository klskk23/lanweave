package panel_test

import (
	"errors"
	"path/filepath"
	"testing"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/firewall"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/panel"
	"lanweave/internal/client/state"
	"lanweave/pkg/protocol"
)

// fakeFirewall is an inspectable stand-in for firewall.Control. open models the rule's presence
// (a bool, so two Allow()s can't create a duplicate "open" state — proving idempotency); the
// counters let tests assert exactly one of Allow/Clear fired per decision.
type fakeFirewall struct {
	open       bool
	allowCalls int
	clearCalls int
}

func (f *fakeFirewall) Allow() error { f.open = true; f.allowCalls++; return nil }
func (f *fakeFirewall) Clear() error { f.open = false; f.clearCalls++; return nil }

var _ firewall.Control = (*fakeFirewall)(nil)

// fakeAPI is a programmable stand-in for the REST client (our own seam).
type fakeAPI struct {
	token    string
	loginErr error
	meErr    error
	nodes    protocol.NodeListResponse
	zones    protocol.ZoneListResponse
	members  protocol.ZoneMembersResponse

	joinedNodeID  int64
	kickedNodeID  int64
	createdNodeID int64
	deletedNodeID int64
	deleteNodeErr error

	listNodesErr error
	tokenSetTo   string
}

func (f *fakeAPI) Login(string, string) error { f.token = "fresh-token"; return f.loginErr }
func (f *fakeAPI) Token() string              { return f.token }
func (f *fakeAPI) SetToken(t string)          { f.token = t; f.tokenSetTo = t }
func (f *fakeAPI) Me() (protocol.MeResponse, error) {
	return protocol.MeResponse{UserID: 1, Username: "alice"}, f.meErr
}
func (f *fakeAPI) ListNodes() (protocol.NodeListResponse, error)            { return f.nodes, f.listNodesErr }
func (f *fakeAPI) ListZones() (protocol.ZoneListResponse, error)            { return f.zones, nil }
func (f *fakeAPI) ZoneMembers(string) (protocol.ZoneMembersResponse, error) { return f.members, nil }
func (f *fakeAPI) CreateZone(_ string, nodeID int64, _ string) (protocol.ZoneResponse, error) {
	f.createdNodeID = nodeID
	return protocol.ZoneResponse{}, nil
}
func (f *fakeAPI) JoinZone(_ string, nodeID int64, _ string) error {
	f.joinedNodeID = nodeID
	return nil
}
func (f *fakeAPI) LeaveZone(_ string, nodeID int64) error  { f.joinedNodeID = nodeID; return nil }
func (f *fakeAPI) ChangeZonePassword(string, string) error { return nil }
func (f *fakeAPI) DeleteZone(string) error                 { return nil }
func (f *fakeAPI) KickMember(_ string, nodeID int64) error { f.kickedNodeID = nodeID; return nil }
func (f *fakeAPI) DeleteNode(nodeID int64) error           { f.deletedNodeID = nodeID; return f.deleteNodeErr }

func newController(t *testing.T, f *fakeAPI) (*panel.Controller, *keyring.Fake) {
	t.Helper()
	fk := keyring.NewFake()
	rec := state.Record{NodeName: "laptop", IP: "100.127.0.2"}
	statePath := filepath.Join(t.TempDir(), "state.json")
	return panel.New(f, rec, fk, statePath, false, &fakeFirewall{}), fk
}

func TestDevicesMarksThisMachine(t *testing.T) {
	f := &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
		{ID: 1, Name: "laptop", IP: "100.127.0.2", Online: true, LastHandshake: "2026-06-06T10:00:00Z"},
		{ID: 2, Name: "phone", IP: "100.127.0.3", Online: false},
	}}}
	c, _ := newController(t, f)
	devs, err := c.Devices()
	if err != nil {
		t.Fatal(err)
	}
	this, other := 0, 0
	for _, d := range devs {
		if d.IsThisMachine {
			this++
			if d.Name != "laptop" || !d.Online || d.LastSeen == "" {
				t.Errorf("this-machine view wrong: %+v", d)
			}
		} else {
			other++
		}
	}
	if this != 1 || other != 1 {
		t.Errorf("this-machine marking wrong: this=%d other=%d", this, other)
	}
}

func TestZonesAndMembers(t *testing.T) {
	f := &fakeAPI{
		zones: protocol.ZoneListResponse{Zones: []protocol.ZoneResponse{
			{ID: 1, Name: "owned", IsOwner: true},
			{ID: 2, Name: "joined", IsOwner: false},
		}},
		members: protocol.ZoneMembersResponse{Members: []protocol.ZoneMemberResponse{
			{NodeID: 7, NodeName: "a", Owner: "alice", IP: "100.127.0.2"},
			{NodeID: 8, NodeName: "b", Owner: "bob", IP: "100.127.0.3"},
		}},
	}
	c, _ := newController(t, f)
	zones, _ := c.Zones()
	owned := map[string]bool{}
	for _, z := range zones {
		owned[z.Name] = z.IsOwner
	}
	if !owned["owned"] || owned["joined"] {
		t.Errorf("is_owner wrong: %+v", owned)
	}
	members, _ := c.Members("owned")
	if len(members) != 2 || members[1].NodeID != 8 || members[1].Owner != "bob" || members[1].IP != "100.127.0.3" {
		t.Errorf("member view wrong: %+v", members)
	}
}

func TestJoinAndKickUseNodeIDs(t *testing.T) {
	f := &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
		{ID: 42, Name: "laptop", IP: "100.127.0.2"},
	}}}
	c, _ := newController(t, f)
	if err := c.JoinZone("team", "pw"); err != nil {
		t.Fatal(err)
	}
	if f.joinedNodeID != 42 {
		t.Errorf("join used node id %d, want 42 (this machine)", f.joinedNodeID)
	}
	if err := c.KickMember("team", 8); err != nil {
		t.Fatal(err)
	}
	if f.kickedNodeID != 8 {
		t.Errorf("kick used node id %d, want 8 (the member)", f.kickedNodeID)
	}
}

func TestCreateZoneUsesThisMachineNodeID(t *testing.T) {
	f := &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
		{ID: 42, Name: "laptop", IP: "100.127.0.2"},
	}}}
	c, _ := newController(t, f)
	if err := c.CreateZone("team", "zone-strong-pw"); err != nil {
		t.Fatal(err)
	}
	if f.createdNodeID != 42 {
		t.Errorf("create used node id %d, want 42 (this machine)", f.createdNodeID)
	}
}

func TestLoadSessionAndSignIn(t *testing.T) {
	// No cached token → sign in needed.
	f := &fakeAPI{}
	c, fk := newController(t, f)
	if need, err := c.LoadSession(); err != nil || !need {
		t.Errorf("no token: need=%v err=%v, want need=true", need, err)
	}

	// Valid cached token (Me ok) → no sign in.
	_ = fk.Set(keyring.SessionTokenName, []byte("cached"))
	if need, err := c.LoadSession(); err != nil || need {
		t.Errorf("valid token: need=%v err=%v, want need=false", need, err)
	}

	// Expired token (Me → ErrSessionExpired) → sign in needed.
	f.meErr = apiclient.ErrSessionExpired
	if need, err := c.LoadSession(); err != nil || !need {
		t.Errorf("expired token: need=%v err=%v, want need=true", need, err)
	}

	// SignIn caches the new token.
	if err := c.SignIn("alice", "pw"); err != nil {
		t.Fatal(err)
	}
	if tok, err := fk.Get(keyring.SessionTokenName); err != nil || string(tok) != "fresh-token" {
		t.Errorf("token not cached after sign-in: %q %v", tok, err)
	}
}

func TestLoadSessionTransientError(t *testing.T) {
	f := &fakeAPI{meErr: errors.New("boom")} // not a session error
	c, fk := newController(t, f)
	_ = fk.Set(keyring.SessionTokenName, []byte("cached"))
	need, err := c.LoadSession()
	if need || err == nil {
		t.Errorf("transient error: need=%v err=%v, want need=false + err", need, err)
	}
}

// logoutFixture wires a controller with a cached session token, device key, and a real state
// file so a Logout's local clears are observable.
func logoutFixture(t *testing.T, f *fakeAPI) (*panel.Controller, *keyring.Fake, string) {
	t.Helper()
	fk := keyring.NewFake()
	_ = fk.Set(keyring.SessionTokenName, []byte("tok"))
	_ = fk.Set(keyring.DeviceKeyName, []byte("priv"))
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := state.Save(statePath, state.Record{ServerURL: "https://s", NodeName: "laptop", IP: "100.127.0.2"}); err != nil {
		t.Fatal(err)
	}
	rec := state.Record{NodeName: "laptop", IP: "100.127.0.2"}
	return panel.New(f, rec, fk, statePath, false, &fakeFirewall{}), fk, statePath
}

func assertLocalCleared(t *testing.T, fk *keyring.Fake, statePath string) {
	t.Helper()
	if _, err := fk.Get(keyring.SessionTokenName); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("session token not cleared: %v", err)
	}
	if _, err := fk.Get(keyring.DeviceKeyName); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("device key not cleared: %v", err)
	}
	if state.Exists(statePath) {
		t.Error("state file not cleared")
	}
}

// TestLogout covers the three outcomes from research D3: the local credentials + state are
// ALWAYS cleared (FR-005/008), while remoteRemoved reflects whether the server node is gone.
func TestLogout(t *testing.T) {
	// (a) node present + DeleteNode ok → remoteRemoved=true.
	present := func() *fakeAPI {
		return &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
			{ID: 42, Name: "laptop", IP: "100.127.0.2"},
		}}}
	}
	f := present()
	c, fk, sp := logoutFixture(t, f)
	removed, err := c.Logout()
	if err != nil {
		t.Fatalf("logout err: %v", err)
	}
	if !removed {
		t.Error("remoteRemoved = false, want true (delete ok)")
	}
	if f.deletedNodeID != 42 {
		t.Errorf("deleted node id = %d, want 42", f.deletedNodeID)
	}
	assertLocalCleared(t, fk, sp)

	// (b) DeleteNode fails → remoteRemoved=false, local still cleared.
	f = present()
	f.deleteNodeErr = errors.New("boom")
	c, fk, sp = logoutFixture(t, f)
	if removed, err := c.Logout(); err != nil || removed {
		t.Errorf("delete-fail: removed=%v err=%v, want removed=false err=nil", removed, err)
	}
	assertLocalCleared(t, fk, sp)

	// (c) server unreachable (ListNodes fails) → remoteRemoved=false, local cleared.
	f = &fakeAPI{listNodesErr: errors.New("network down")}
	c, fk, sp = logoutFixture(t, f)
	if removed, err := c.Logout(); err != nil || removed {
		t.Errorf("unreachable: removed=%v err=%v, want removed=false err=nil", removed, err)
	}
	assertLocalCleared(t, fk, sp)

	// (d) node already absent (not in list) → remoteRemoved=true, no DeleteNode call.
	f = &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
		{ID: 5, Name: "someone-else", IP: "100.127.9.9"},
	}}}
	c, fk, sp = logoutFixture(t, f)
	if removed, err := c.Logout(); err != nil || !removed {
		t.Errorf("already-absent: removed=%v err=%v, want removed=true err=nil", removed, err)
	}
	if f.deletedNodeID != 0 {
		t.Errorf("DeleteNode should not be called when node already absent; got id %d", f.deletedNodeID)
	}
	assertLocalCleared(t, fk, sp)
}

// TestUseClient covers the TOFU re-pin swap: calls route to the new client and the cached
// session token is re-applied, WITHOUT flipping the insecure indicator (a pinned cert is a
// verified connection, not an insecure one). Replaces 017's TestUseInsecureClient.
func TestUseClient(t *testing.T) {
	f1 := &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{{ID: 1, Name: "laptop", IP: "100.127.0.2"}}}}
	fk := keyring.NewFake()
	_ = fk.Set(keyring.SessionTokenName, []byte("cached-tok"))
	c := panel.New(f1, state.Record{NodeName: "laptop", IP: "100.127.0.2"}, fk, filepath.Join(t.TempDir(), "state.json"), false, &fakeFirewall{})
	if c.Insecure() {
		t.Error("controller should start secure")
	}

	f2 := &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{{ID: 2, Name: "laptop", IP: "100.127.0.2"}}}}
	c.UseClient(f2)
	if c.Insecure() {
		t.Error("UseClient must NOT set the insecure indicator (pinned cert is verified)")
	}
	if f2.tokenSetTo != "cached-tok" {
		t.Errorf("cached token not re-applied to swapped client: got %q", f2.tokenSetTo)
	}
	// Subsequent calls route to f2: JoinZone resolves this machine via f2's node list (id 2).
	if err := c.JoinZone("team", "pw"); err != nil {
		t.Fatal(err)
	}
	if f2.joinedNodeID != 2 || f1.joinedNodeID != 0 {
		t.Errorf("calls not routed to swapped client: f2.joined=%d f1.joined=%d", f2.joinedNodeID, f1.joinedNodeID)
	}
}

// TestSetPinnedCert covers persisting a TOFU pin to the local state file (FR-002/006).
func TestSetPinnedCert(t *testing.T) {
	fk := keyring.NewFake()
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := state.Save(statePath, state.Record{ServerURL: "https://s", NodeName: "laptop", IP: "100.127.0.2"}); err != nil {
		t.Fatal(err)
	}
	c := panel.New(&fakeAPI{}, state.Record{ServerURL: "https://s", NodeName: "laptop", IP: "100.127.0.2"}, fk, statePath, false, &fakeFirewall{})
	if err := c.SetPinnedCert("abc123"); err != nil {
		t.Fatalf("set pin: %v", err)
	}
	got, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.PinnedCertSHA256 != "abc123" {
		t.Errorf("persisted pin = %q, want abc123", got.PinnedCertSHA256)
	}
}

// firewallController wires a controller with a real state file (so the persisted preference is
// observable) and an inspectable fake firewall, seeded with the given FirewallAllowVPN preference.
func firewallController(t *testing.T, pref bool) (*panel.Controller, *fakeFirewall, string) {
	t.Helper()
	fk := keyring.NewFake()
	statePath := filepath.Join(t.TempDir(), "state.json")
	rec := state.Record{ServerURL: "https://s", NodeName: "laptop", IP: "100.127.0.2", FirewallAllowVPN: pref}
	if err := state.Save(statePath, rec); err != nil {
		t.Fatal(err)
	}
	fw := &fakeFirewall{}
	return panel.New(&fakeAPI{}, rec, fk, statePath, false, fw), fw, statePath
}

// TestFirewallReconcileTruthTable asserts the core rule: the host rule exists iff
// (preference ON ∧ tunnel connected); every other cell removes it (FR-013/015/016).
func TestFirewallReconcileTruthTable(t *testing.T) {
	cases := []struct {
		pref, connected, wantOpen bool
	}{
		{false, false, false},
		{false, true, false},
		{true, false, false},
		{true, true, true},
	}
	for _, tc := range cases {
		c, fw, _ := firewallController(t, tc.pref)
		if err := c.ReconcileFirewall(tc.connected); err != nil {
			t.Fatalf("pref=%v connected=%v: %v", tc.pref, tc.connected, err)
		}
		if fw.open != tc.wantOpen {
			t.Errorf("pref=%v connected=%v: open=%v want %v", tc.pref, tc.connected, fw.open, tc.wantOpen)
		}
		if tc.wantOpen {
			if fw.allowCalls != 1 || fw.clearCalls != 0 {
				t.Errorf("pref=%v connected=%v: want exactly one Allow, got allow=%d clear=%d", tc.pref, tc.connected, fw.allowCalls, fw.clearCalls)
			}
		} else {
			if fw.clearCalls != 1 || fw.allowCalls != 0 {
				t.Errorf("pref=%v connected=%v: want exactly one Clear, got allow=%d clear=%d", tc.pref, tc.connected, fw.allowCalls, fw.clearCalls)
			}
		}
	}
}

// TestSetFirewallAllowedPersistsAndApplies covers the toggle handler: the preference is persisted
// regardless of connection state, but the rule is installed only when also connected (FR-012/014).
func TestSetFirewallAllowedPersistsAndApplies(t *testing.T) {
	// ON while connected → persisted true + Allow.
	c, fw, sp := firewallController(t, false)
	if err := c.SetFirewallAllowed(true, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := state.Load(sp); !got.FirewallAllowVPN {
		t.Error("FirewallAllowVPN not persisted true")
	}
	if !c.FirewallAllowed() {
		t.Error("FirewallAllowed() should report true")
	}
	if !fw.open || fw.allowCalls != 1 {
		t.Errorf("on ∧ connected: want Allow, got open=%v allow=%d", fw.open, fw.allowCalls)
	}

	// ON while disconnected → preference persists true, but rule stays closed (only when connected).
	c, fw, sp = firewallController(t, false)
	if err := c.SetFirewallAllowed(true, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := state.Load(sp); !got.FirewallAllowVPN {
		t.Error("preference should persist true even while disconnected")
	}
	if fw.open || fw.clearCalls != 1 {
		t.Errorf("on ∧ disconnected: want Clear, got open=%v clear=%d", fw.open, fw.clearCalls)
	}

	// OFF while connected → persisted false + Clear.
	c, fw, sp = firewallController(t, true)
	if err := c.SetFirewallAllowed(false, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := state.Load(sp); got.FirewallAllowVPN {
		t.Error("FirewallAllowVPN not persisted false")
	}
	if fw.open || fw.clearCalls != 1 {
		t.Errorf("toggled off: want Clear, got open=%v clear=%d", fw.open, fw.clearCalls)
	}
}

// TestLogoutClearsFirewall ensures logout never strands an open rule (FR-016).
func TestLogoutClearsFirewall(t *testing.T) {
	c, fw, _ := firewallController(t, true)
	if _, err := c.Logout(); err != nil {
		t.Fatal(err)
	}
	if fw.clearCalls < 1 {
		t.Error("Logout should clear the firewall rule")
	}
}

// TestFirewallAllowIdempotent shows repeated reconciles never produce a duplicate open state — the
// real netsh path is delete-then-add, modelled here by the fake's boolean open (FR-017).
func TestFirewallAllowIdempotent(t *testing.T) {
	c, fw, _ := firewallController(t, true)
	if err := c.ReconcileFirewall(true); err != nil {
		t.Fatal(err)
	}
	if err := c.ReconcileFirewall(true); err != nil {
		t.Fatal(err)
	}
	if !fw.open || fw.allowCalls != 2 {
		t.Errorf("two reconciles (on ∧ connected): open=%v allow=%d, want open + 2 Allow (no duplicate open)", fw.open, fw.allowCalls)
	}
}
