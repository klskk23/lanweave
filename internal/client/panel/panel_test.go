package panel_test

import (
	"errors"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

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
	token        string
	refreshToken string
	loginErr     error
	meErr        error
	nodes        protocol.NodeListResponse
	zones        protocol.ZoneListResponse
	members      protocol.ZoneMembersResponse

	announcements map[string][]protocol.AnnouncementResponse
	announceErr   error

	joinedNodeID  int64
	kickedNodeID  int64
	createdNodeID int64
	deletedNodeID int64
	deleteNodeErr error
	// deleteNodeErrSeq is consumed one-per-DeleteNode-call (front to back); once exhausted,
	// DeleteNode falls back to deleteNodeErr. It drives the bounded-retry tests (return
	// apiclient.ErrUnreachable N times, then a chosen terminal result).
	deleteNodeErrSeq []error
	deleteNodeCalls  int

	listNodesErr error
	tokenSetTo   string
	rtSetTo      string
	logoutCalls  int
}

func (f *fakeAPI) Login(string, string) error {
	f.token = "fresh-token"
	f.refreshToken = "fresh-rt"
	return f.loginErr
}
func (f *fakeAPI) Token() string            { return f.token }
func (f *fakeAPI) SetToken(t string)        { f.token = t; f.tokenSetTo = t }
func (f *fakeAPI) RefreshToken() string     { return f.refreshToken }
func (f *fakeAPI) SetRefreshToken(t string) { f.refreshToken = t; f.rtSetTo = t }
func (f *fakeAPI) Me() (protocol.MeResponse, error) {
	return protocol.MeResponse{UserID: 1, Username: "alice"}, f.meErr
}
func (f *fakeAPI) ListNodes() (protocol.NodeListResponse, error)            { return f.nodes, f.listNodesErr }
func (f *fakeAPI) ListZones() (protocol.ZoneListResponse, error)            { return f.zones, nil }
func (f *fakeAPI) ZoneMembers(string) (protocol.ZoneMembersResponse, error) { return f.members, nil }

func (f *fakeAPI) ListAnnouncements(zone string) (protocol.AnnouncementListResponse, error) {
	if f.announceErr != nil {
		return protocol.AnnouncementListResponse{}, f.announceErr
	}
	return protocol.AnnouncementListResponse{Announcements: f.announcements[zone]}, nil
}
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
func (f *fakeAPI) DeleteNode(nodeID int64) error {
	f.deletedNodeID = nodeID
	f.deleteNodeCalls++
	if len(f.deleteNodeErrSeq) > 0 {
		err := f.deleteNodeErrSeq[0]
		f.deleteNodeErrSeq = f.deleteNodeErrSeq[1:]
		return err
	}
	return f.deleteNodeErr
}
func (f *fakeAPI) Logout() error { f.logoutCalls++; return nil }

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

	// Valid cached token (Me ok) → no sign in. The cached refresh token is restored into
	// the client too, so an expired access token can be silently renewed mid-session.
	_ = fk.Set(keyring.SessionTokenName, []byte("cached"))
	_ = fk.Set(keyring.RefreshTokenName, []byte("cached-rt"))
	if need, err := c.LoadSession(); err != nil || need {
		t.Errorf("valid token: need=%v err=%v, want need=false", need, err)
	}
	if f.rtSetTo != "cached-rt" {
		t.Errorf("LoadSession did not seed the cached refresh token: got %q", f.rtSetTo)
	}

	// Expired token (Me → ErrSessionExpired) → sign in needed.
	f.meErr = apiclient.ErrSessionExpired
	if need, err := c.LoadSession(); err != nil || !need {
		t.Errorf("expired token: need=%v err=%v, want need=true", need, err)
	}

	// SignIn caches both the new access token and the new refresh token.
	if err := c.SignIn("alice", "pw"); err != nil {
		t.Fatal(err)
	}
	if tok, err := fk.Get(keyring.SessionTokenName); err != nil || string(tok) != "fresh-token" {
		t.Errorf("token not cached after sign-in: %q %v", tok, err)
	}
	if rt, err := fk.Get(keyring.RefreshTokenName); err != nil || string(rt) != "fresh-rt" {
		t.Errorf("refresh token not cached after sign-in: %q %v", rt, err)
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

// logoutFixtureFW wires a controller with a cached session token, refresh token, device key, and
// a real state file so a Logout's local clears (or their absence) are observable, and exposes the
// fakeFirewall so tests can assert Clear() fired (done/force) or not (blocked/need-sign-in).
func logoutFixtureFW(t *testing.T, f *fakeAPI) (*panel.Controller, *keyring.Fake, string, *fakeFirewall) {
	t.Helper()
	fk := keyring.NewFake()
	_ = fk.Set(keyring.SessionTokenName, []byte("tok"))
	_ = fk.Set(keyring.RefreshTokenName, []byte("rt"))
	_ = fk.Set(keyring.DeviceKeyName, []byte("priv"))
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := state.Save(statePath, state.Record{ServerURL: "https://s", NodeName: "laptop", IP: "100.127.0.2"}); err != nil {
		t.Fatal(err)
	}
	rec := state.Record{NodeName: "laptop", IP: "100.127.0.2"}
	fw := &fakeFirewall{}
	return panel.New(f, rec, fk, statePath, false, fw), fk, statePath, fw
}

// logoutFixture is logoutFixtureFW without the firewall handle (for tests that don't inspect it).
func logoutFixture(t *testing.T, f *fakeAPI) (*panel.Controller, *keyring.Fake, string) {
	t.Helper()
	c, fk, sp, _ := logoutFixtureFW(t, f)
	return c, fk, sp
}

// assertLocalIntact is the INV-1 inverse of assertLocalCleared: every keyring entry and the state
// file survive unchanged (used on the blocked / need-sign-in paths, where nothing must mutate).
func assertLocalIntact(t *testing.T, fk *keyring.Fake, statePath string) {
	t.Helper()
	for _, name := range []string{keyring.SessionTokenName, keyring.RefreshTokenName, keyring.DeviceKeyName} {
		if _, err := fk.Get(name); err != nil {
			t.Errorf("keyring %q should be intact, got err %v", name, err)
		}
	}
	if !state.Exists(statePath) {
		t.Error("state file should be intact")
	}
}

func assertLocalCleared(t *testing.T, fk *keyring.Fake, statePath string) {
	t.Helper()
	if _, err := fk.Get(keyring.SessionTokenName); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("session token not cleared: %v", err)
	}
	if _, err := fk.Get(keyring.RefreshTokenName); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("refresh token not cleared: %v", err)
	}
	if _, err := fk.Get(keyring.DeviceKeyName); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("device key not cleared: %v", err)
	}
	if state.Exists(statePath) {
		t.Error("state file not cleared")
	}
}

// TestLogout covers the reachable-server outcomes (slice 025 remote-first flow): a clean delete
// and a reachable-but-failed delete both reach LogoutDone with local material cleared, but the
// latter carries the ErrRemoteMayLinger advisory (FR-008/010). The unreachable (block) and
// already-absent cases have their own dedicated tests below.
func TestLogout(t *testing.T) {
	// (a) node present + DeleteNode ok → LogoutDone, no linger, RT revoked, local cleared.
	present := func() *fakeAPI {
		return &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
			{ID: 42, Name: "laptop", IP: "100.127.0.2"},
		}}}
	}
	f := present()
	c, fk, sp := logoutFixture(t, f)
	outcome, err := c.Logout()
	if err != nil {
		t.Fatalf("logout err: %v", err)
	}
	if outcome != panel.LogoutDone {
		t.Errorf("outcome = %v, want LogoutDone (delete ok)", outcome)
	}
	if f.deletedNodeID != 42 {
		t.Errorf("deleted node id = %d, want 42", f.deletedNodeID)
	}
	if f.logoutCalls != 1 {
		t.Errorf("api.Logout called %d times, want 1 (server-side RT revoke)", f.logoutCalls)
	}
	assertLocalCleared(t, fk, sp)

	// (b) DeleteNode fails with a reachable non-network error → LogoutDone + ErrRemoteMayLinger,
	// local still cleared (the user is signed out locally; the node may linger server-side).
	f = present()
	f.deleteNodeErr = errors.New("boom") // not ErrUnreachable / not auth
	c, fk, sp = logoutFixture(t, f)
	outcome, err = c.Logout()
	if outcome != panel.LogoutDone || !errors.Is(err, panel.ErrRemoteMayLinger) {
		t.Errorf("reachable delete-fail: outcome=%v err=%v, want LogoutDone + ErrRemoteMayLinger", outcome, err)
	}
	assertLocalCleared(t, fk, sp)

	// (c) ListNodes fails with a reachable non-network error → LogoutDone + ErrRemoteMayLinger.
	f = &fakeAPI{listNodesErr: errors.New("server boom")}
	c, fk, sp = logoutFixture(t, f)
	outcome, err = c.Logout()
	if outcome != panel.LogoutDone || !errors.Is(err, panel.ErrRemoteMayLinger) {
		t.Errorf("reachable list-fail: outcome=%v err=%v, want LogoutDone + ErrRemoteMayLinger", outcome, err)
	}
	assertLocalCleared(t, fk, sp)
}

// presentNode returns a fakeAPI whose node list contains exactly this machine (id 42), so the
// removal flow resolves an id and proceeds to DeleteNode.
func presentNode() *fakeAPI {
	return &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
		{ID: 42, Name: "laptop", IP: "100.127.0.2"},
	}}}
}

// TestLogoutBlockedOnUnreachable (US1, INV-1/INV-4): three ErrUnreachable removal attempts →
// LogoutBlocked with ZERO local mutation — keyring + state intact, firewall never cleared — and
// the injected sleep called exactly twice with 1s (no real sleeping).
func TestLogoutBlockedOnUnreachable(t *testing.T) {
	f := presentNode()
	f.deleteNodeErr = apiclient.ErrUnreachable // every attempt is network-unreachable
	c, fk, sp, fw := logoutFixtureFW(t, f)
	var sleeps []time.Duration
	c.SetSleep(func(d time.Duration) { sleeps = append(sleeps, d) })

	outcome, err := c.Logout()
	if outcome != panel.LogoutBlocked || err != nil {
		t.Fatalf("outcome=%v err=%v, want LogoutBlocked, nil", outcome, err)
	}
	if f.deleteNodeCalls != 3 {
		t.Errorf("DeleteNode called %d times, want 3 (bounded retry)", f.deleteNodeCalls)
	}
	if len(sleeps) != 2 || sleeps[0] != time.Second || sleeps[1] != time.Second {
		t.Errorf("sleeps = %v, want exactly [1s 1s]", sleeps)
	}
	if fw.clearCalls != 0 {
		t.Errorf("firewall Clear called %d times on a blocked logout, want 0", fw.clearCalls)
	}
	if f.logoutCalls != 0 {
		t.Errorf("api.Logout (RT revoke) called %d times on a blocked logout, want 0", f.logoutCalls)
	}
	assertLocalIntact(t, fk, sp)
}

// TestLogoutRetryBoundedThenSucceeds (US1): ErrUnreachable twice then a successful delete →
// LogoutDone, sleep called twice, local material cleared.
func TestLogoutRetryBoundedThenSucceeds(t *testing.T) {
	f := presentNode()
	f.deleteNodeErrSeq = []error{apiclient.ErrUnreachable, apiclient.ErrUnreachable} // then nil (success)
	c, fk, sp, fw := logoutFixtureFW(t, f)
	var sleeps []time.Duration
	c.SetSleep(func(d time.Duration) { sleeps = append(sleeps, d) })

	outcome, err := c.Logout()
	if outcome != panel.LogoutDone || err != nil {
		t.Fatalf("outcome=%v err=%v, want LogoutDone, nil", outcome, err)
	}
	if f.deleteNodeCalls != 3 {
		t.Errorf("DeleteNode called %d times, want 3 (2 fail + 1 success)", f.deleteNodeCalls)
	}
	if len(sleeps) != 2 {
		t.Errorf("sleeps = %v, want 2", sleeps)
	}
	if fw.clearCalls < 1 {
		t.Error("firewall not cleared on a successful logout")
	}
	assertLocalCleared(t, fk, sp)
}

// TestLogoutAlreadyAbsentIsDone (US2, INV-3): the node is missing from the list → LogoutDone with
// NO DeleteNode call (never blocked); local material cleared.
func TestLogoutAlreadyAbsentIsDone(t *testing.T) {
	f := &fakeAPI{nodes: protocol.NodeListResponse{Nodes: []protocol.NodeResponse{
		{ID: 5, Name: "someone-else", IP: "100.127.9.9"},
	}}}
	c, fk, sp := logoutFixture(t, f)
	outcome, err := c.Logout()
	if outcome != panel.LogoutDone || err != nil {
		t.Errorf("already-absent: outcome=%v err=%v, want LogoutDone, nil", outcome, err)
	}
	if f.deleteNodeCalls != 0 {
		t.Errorf("DeleteNode should not be called when node already absent; got %d calls", f.deleteNodeCalls)
	}
	assertLocalCleared(t, fk, sp)
}

// TestLogoutNeedSignInOnRefreshFail (US2 edge, research D7): the (post-lazy-refresh) delete still
// surfaces ErrSessionExpired → LogoutNeedSignIn with NO local mutation and no firewall clear.
func TestLogoutNeedSignInOnRefreshFail(t *testing.T) {
	f := presentNode()
	f.deleteNodeErr = apiclient.ErrSessionExpired
	c, fk, sp, fw := logoutFixtureFW(t, f)
	outcome, err := c.Logout()
	if outcome != panel.LogoutNeedSignIn || err != nil {
		t.Fatalf("outcome=%v err=%v, want LogoutNeedSignIn, nil", outcome, err)
	}
	if fw.clearCalls != 0 {
		t.Errorf("firewall Clear called %d times on need-sign-in, want 0", fw.clearCalls)
	}
	assertLocalIntact(t, fk, sp)
}

// TestForceLogoutClearsLocal (US3, INV-5): with the server unreachable, ForceLogout still clears
// all three keyring entries + state, revokes the RT best-effort, clears the firewall, returns nil.
func TestForceLogoutClearsLocal(t *testing.T) {
	f := presentNode()
	f.deleteNodeErr = apiclient.ErrUnreachable // server unreachable — irrelevant to force teardown
	c, fk, sp, fw := logoutFixtureFW(t, f)
	if err := c.ForceLogout(); err != nil {
		t.Fatalf("ForceLogout err: %v", err)
	}
	if fw.clearCalls < 1 {
		t.Error("ForceLogout should clear the firewall rule")
	}
	if f.logoutCalls != 1 {
		t.Errorf("api.Logout (RT revoke) called %d times, want 1 (best-effort)", f.logoutCalls)
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

var errAPI = errors.New("api down")

// fakeRouteSetter records SetExtraRoutes calls; conflictOn prefixes are
// excluded from the returned applied set (simulating local-overlap skips).
type fakeRouteSetter struct {
	calls      [][]netip.Prefix
	conflictOn map[netip.Prefix]bool
}

func (f *fakeRouteSetter) SetExtraRoutes(extra []netip.Prefix) ([]netip.Prefix, error) {
	f.calls = append(f.calls, extra)
	var applied []netip.Prefix
	for _, p := range extra {
		if !f.conflictOn[p] {
			applied = append(applied, p)
		}
	}
	return applied, nil
}

// TestSyncRoutes drives the 033 consumer-route sync at the controller layer:
// aggregation feeds the tunnel, views carry announcer/zone data, conflicts are
// marked, an API failure freezes (no tunnel call), and a shrunken second
// fetch propagates the shrunken set (SC-001 withdrawal, panel dimension).
func TestSyncRoutes(t *testing.T) {
	f := &fakeAPI{
		zones: protocol.ZoneListResponse{Zones: []protocol.ZoneResponse{{ID: 1, Name: "home"}}},
		members: protocol.ZoneMembersResponse{Members: []protocol.ZoneMemberResponse{
			{NodeID: 1, NodeName: "laptop", IP: "100.127.0.2"},
			{NodeID: 9, NodeName: "bob-router", IP: "100.127.0.9"},
		}},
		announcements: map[string][]protocol.AnnouncementResponse{
			"home": {
				{ID: 1, NodeID: 9, NodeName: "bob-router", Subnet: "192.168.50.0/24", Synthetic: "100.100.1.0/24"},
				{ID: 2, NodeID: 9, NodeName: "bob-router", Subnet: "10.8.0.0/24", Synthetic: "100.100.2.0/24"},
			},
		},
	}
	c, _ := newController(t, f)
	rt := &fakeRouteSetter{conflictOn: map[netip.Prefix]bool{netip.MustParsePrefix("100.100.2.0/24"): true}}

	views, err := c.SyncRoutes(rt)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(rt.calls) != 1 || len(rt.calls[0]) != 2 {
		t.Fatalf("tunnel got %v, want both synthetic prefixes", rt.calls)
	}
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2", len(views))
	}
	if views[0].Conflict || views[0].Announcer != "bob-router" || views[0].Synthetic != "100.100.1.0/24" {
		t.Errorf("view 0 = %+v", views[0])
	}
	if !views[1].Conflict {
		t.Errorf("conflicting entry not marked: %+v", views[1])
	}

	// API failure freezes: error out, tunnel untouched.
	f.announceErr = errAPI
	if _, err := c.SyncRoutes(rt); err == nil {
		t.Fatal("api failure not surfaced")
	}
	if len(rt.calls) != 1 {
		t.Fatal("tunnel touched despite api failure (routes must freeze)")
	}
	f.announceErr = nil

	// Withdrawal converges: second cycle returns a shrunken list → the tunnel
	// receives the shrunken set.
	f.announcements["home"] = f.announcements["home"][:1]
	if _, err := c.SyncRoutes(rt); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	last := rt.calls[len(rt.calls)-1]
	if len(last) != 1 || last[0] != netip.MustParsePrefix("100.100.1.0/24") {
		t.Errorf("shrunken set not propagated: %v", last)
	}

	// Per-node membership: a zone where only a SIBLING device is a member must
	// not contribute routes (its blocks aren't in this peer's AllowedIPs).
	f.members = protocol.ZoneMembersResponse{Members: []protocol.ZoneMemberResponse{
		{NodeID: 9, NodeName: "bob-router", IP: "100.127.0.9"},
	}}
	views, err = c.SyncRoutes(rt)
	if err != nil || len(views) != 0 {
		t.Fatalf("sibling-only zone leaked routes: %v (%v)", views, err)
	}
	if last := rt.calls[len(rt.calls)-1]; len(last) != 0 {
		t.Errorf("tunnel received routes for a non-member zone: %v", last)
	}
}
