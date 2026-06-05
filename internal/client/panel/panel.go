// Package panel is the UI-free management controller for the desktop client. It assembles
// the view data (devices, zones, members) and performs every operation against an
// authenticated API client, reusing a cached session. It has no Fyne dependency, so it is
// tested headlessly against a real server.
package panel

import (
	"errors"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/state"
	"lanweave/pkg/protocol"
)

// api is the subset of the REST client the panel needs. It is an interface so unit tests
// can supply a fake; *apiclient.Client satisfies it.
type api interface {
	Login(username, password string) error
	Token() string
	SetToken(token string)
	Me() (protocol.MeResponse, error)
	ListNodes() (protocol.NodeListResponse, error)
	ListZones() (protocol.ZoneListResponse, error)
	ZoneMembers(name string) (protocol.ZoneMembersResponse, error)
	CreateZone(name, password string) (protocol.ZoneResponse, error)
	JoinZone(name string, nodeID int64, password string) error
	LeaveZone(name string, nodeID int64) error
	ChangeZonePassword(name, password string) error
	DeleteZone(name string) error
	KickMember(name string, nodeID int64) error
}

var _ api = (*apiclient.Client)(nil)

// View models rendered by the Fyne panel.
type (
	DeviceView struct {
		Name          string
		IP            string
		LastSeen      string // RFC 3339, empty when never seen
		Online        bool
		IsThisMachine bool
	}
	ZoneView struct {
		Name    string
		IsOwner bool
	}
	MemberView struct {
		NodeID   int64
		NodeName string
		Owner    string
		IP       string
	}
)

// Controller drives the management panel.
type Controller struct {
	api    api
	record state.Record
	keys   keyring.Store
}

// New builds a controller bound to an API client, the setup record (to identify this
// machine), and the secure store (for the cached session token).
func New(a api, record state.Record, keys keyring.Store) *Controller {
	return &Controller{api: a, record: record, keys: keys}
}

// LoadSession restores a cached session and validates it. It returns needSignIn=true when
// there is no cached token or the token has expired; a non-nil err is a transient problem
// (e.g. the server is unreachable) with the cached token left in place.
func (c *Controller) LoadSession() (needSignIn bool, err error) {
	tok, gerr := c.keys.Get(keyring.SessionTokenName)
	if gerr != nil || len(tok) == 0 {
		return true, nil
	}
	c.api.SetToken(string(tok))
	if _, merr := c.api.Me(); merr != nil {
		if errors.Is(merr, apiclient.ErrSessionExpired) || errors.Is(merr, apiclient.ErrAuthFailed) {
			return true, nil
		}
		return false, merr
	}
	return false, nil
}

// SignIn authenticates and caches the new session token in the secure store.
func (c *Controller) SignIn(username, password string) error {
	if err := c.api.Login(username, password); err != nil {
		return err
	}
	return c.keys.Set(keyring.SessionTokenName, []byte(c.api.Token()))
}

// Devices lists the user's devices, marking exactly the one matching the setup record as
// this machine, with address + online + last-seen.
func (c *Controller) Devices() ([]DeviceView, error) {
	list, err := c.api.ListNodes()
	if err != nil {
		return nil, err
	}
	out := make([]DeviceView, 0, len(list.Nodes))
	for _, n := range list.Nodes {
		out = append(out, DeviceView{
			Name: n.Name, IP: n.IP, LastSeen: n.LastHandshake, Online: n.Online,
			IsThisMachine: c.isThisMachine(n.Name, n.IP),
		})
	}
	return out, nil
}

// Zones lists the zones the device participates in, carrying is_owner.
func (c *Controller) Zones() ([]ZoneView, error) {
	list, err := c.api.ListZones()
	if err != nil {
		return nil, err
	}
	out := make([]ZoneView, 0, len(list.Zones))
	for _, z := range list.Zones {
		out = append(out, ZoneView{Name: z.Name, IsOwner: z.IsOwner})
	}
	return out, nil
}

// Members lists a zone's members (node id, name, owner, address).
func (c *Controller) Members(zoneName string) ([]MemberView, error) {
	resp, err := c.api.ZoneMembers(zoneName)
	if err != nil {
		return nil, err
	}
	out := make([]MemberView, 0, len(resp.Members))
	for _, m := range resp.Members {
		out = append(out, MemberView{NodeID: m.NodeID, NodeName: m.NodeName, Owner: m.Owner, IP: m.IP})
	}
	return out, nil
}

// CreateZone creates a zone owned by the caller.
func (c *Controller) CreateZone(name, password string) error {
	_, err := c.api.CreateZone(name, password)
	return err
}

// JoinZone admits this machine's device to a zone.
func (c *Controller) JoinZone(name, password string) error {
	id, err := c.thisMachineNodeID()
	if err != nil {
		return err
	}
	return c.api.JoinZone(name, id, password)
}

// LeaveZone removes this machine's device from a zone.
func (c *Controller) LeaveZone(name string) error {
	id, err := c.thisMachineNodeID()
	if err != nil {
		return err
	}
	return c.api.LeaveZone(name, id)
}

// ChangePassword rotates an owned zone's password.
func (c *Controller) ChangePassword(name, password string) error {
	return c.api.ChangeZonePassword(name, password)
}

// KickMember removes a member (by node id, from a MemberView) from an owned zone.
func (c *Controller) KickMember(name string, nodeID int64) error {
	return c.api.KickMember(name, nodeID)
}

// DeleteZone deletes an owned zone.
func (c *Controller) DeleteZone(name string) error {
	return c.api.DeleteZone(name)
}

func (c *Controller) isThisMachine(name, ip string) bool {
	if c.record.NodeName != "" && name == c.record.NodeName {
		return true
	}
	return c.record.IP != "" && ip == c.record.IP
}

// thisMachineNodeID resolves this machine's node id from the device list (the setup record
// holds the name/address but not the id).
func (c *Controller) thisMachineNodeID() (int64, error) {
	list, err := c.api.ListNodes()
	if err != nil {
		return 0, err
	}
	for _, n := range list.Nodes {
		if c.isThisMachine(n.Name, n.IP) {
			return n.ID, nil
		}
	}
	return 0, errors.New("this device is not registered with the server")
}
