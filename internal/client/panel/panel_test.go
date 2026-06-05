package panel_test

import (
	"errors"
	"testing"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/panel"
	"lanweave/internal/client/state"
	"lanweave/pkg/protocol"
)

// fakeAPI is a programmable stand-in for the REST client (our own seam).
type fakeAPI struct {
	token    string
	loginErr error
	meErr    error
	nodes    protocol.NodeListResponse
	zones    protocol.ZoneListResponse
	members  protocol.ZoneMembersResponse

	joinedNodeID int64
	kickedNodeID int64
}

func (f *fakeAPI) Login(string, string) error { f.token = "fresh-token"; return f.loginErr }
func (f *fakeAPI) Token() string              { return f.token }
func (f *fakeAPI) SetToken(t string)          { f.token = t }
func (f *fakeAPI) Me() (protocol.MeResponse, error) {
	return protocol.MeResponse{UserID: 1, Username: "alice"}, f.meErr
}
func (f *fakeAPI) ListNodes() (protocol.NodeListResponse, error)            { return f.nodes, nil }
func (f *fakeAPI) ListZones() (protocol.ZoneListResponse, error)            { return f.zones, nil }
func (f *fakeAPI) ZoneMembers(string) (protocol.ZoneMembersResponse, error) { return f.members, nil }
func (f *fakeAPI) CreateZone(string, string) (protocol.ZoneResponse, error) {
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

func newController(t *testing.T, f *fakeAPI) (*panel.Controller, *keyring.Fake) {
	t.Helper()
	fk := keyring.NewFake()
	rec := state.Record{NodeName: "laptop", IP: "100.127.0.2"}
	return panel.New(f, rec, fk), fk
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
