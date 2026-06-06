// Package panel is the UI-free management controller for the desktop client. It assembles
// the view data (devices, zones, members) and performs every operation against an
// authenticated API client, reusing a cached session. It has no Fyne dependency, so it is
// tested headlessly against a real server.
package panel

import (
	"errors"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/firewall"
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
	CreateZone(name string, nodeID int64, password string) (protocol.ZoneResponse, error)
	JoinZone(name string, nodeID int64, password string) error
	LeaveZone(name string, nodeID int64) error
	ChangeZonePassword(name, password string) error
	DeleteZone(name string) error
	KickMember(name string, nodeID int64) error
	DeleteNode(nodeID int64) error
}

var _ api = (*apiclient.Client)(nil)

// errNotRegistered means this machine's device is not in the caller's node list (so logout
// can distinguish "already gone" from a list/network failure).
var errNotRegistered = errors.New("this device is not registered with the server")

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
	api       api
	record    state.Record
	keys      keyring.Store
	statePath string
	insecure  bool
	fw        firewall.Control
}

// New builds a controller bound to an API client, the setup record (to identify this
// machine), and the secure store (for the cached session token). statePath lets Logout
// clear the local state file; insecure seeds the "certificate not verified" indicator for
// the --insecure CLI-flag case; fw installs/removes the optional host inbound-allow rule.
func New(a api, record state.Record, keys keyring.Store, statePath string, insecure bool, fw firewall.Control) *Controller {
	return &Controller{api: a, record: record, keys: keys, statePath: statePath, insecure: insecure, fw: fw}
}

// Insecure reports whether this session is bypassing TLS certificate verification (set from
// the --insecure CLI flag at construction, or flipped by UseInsecureClient after the user
// accepts the reactive opt-in). Drives the persistent "certificate not verified" indicator.
func (c *Controller) Insecure() bool { return c.insecure }

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

// CreateZone creates a zone owned by the caller and auto-joins this machine's device, so
// the creator is a member immediately without a separate join step.
func (c *Controller) CreateZone(name, password string) error {
	id, err := c.thisMachineNodeID()
	if err != nil {
		return err
	}
	_, err = c.api.CreateZone(name, id, password)
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

// Logout deregisters this device on the server (best-effort) and ALWAYS clears the local
// session token, device private key, and state record — so an offline user can still leave.
// remoteRemoved reports whether the server-side node is gone (confirmed removed or already
// absent); err is non-nil only when a local clear failed.
func (c *Controller) Logout() (remoteRemoved bool, err error) {
	remoteRemoved = c.removeRemoteNode()
	// Close the host rule before clearing state so logout never strands an open firewall.
	_ = c.fw.Clear()
	localErr := errors.Join(
		c.keys.Delete(keyring.SessionTokenName),
		c.keys.Delete(keyring.DeviceKeyName),
		state.Clear(c.statePath),
	)
	return remoteRemoved, localErr
}

// removeRemoteNode best-effort deletes this device's own node. It returns true when the
// server confirmed removal or the node was already absent, and false when the server was
// unreachable or the delete failed (so the UI can warn the node may still be registered).
func (c *Controller) removeRemoteNode() bool {
	id, err := c.thisMachineNodeID()
	if err != nil {
		// Not in the list → already gone (removed); a list (network) failure → not removed.
		return errors.Is(err, errNotRegistered)
	}
	if derr := c.api.DeleteNode(id); derr != nil {
		// A 404 (raced, already gone) counts as removed; network/5xx does not.
		return errors.Is(derr, apiclient.ErrZoneNotFound)
	}
	return true
}

// UseClient swaps in a freshly built API client (e.g. rebuilt with apiclient.WithPinnedCert
// after the user trusts or re-trusts a certificate) and re-applies the cached session token
// so the signed-in session survives the swap. It does NOT change the insecure indicator —
// trusting a pinned certificate is a verified connection, not an insecure one.
func (c *Controller) UseClient(a api) {
	if tok, gerr := c.keys.Get(keyring.SessionTokenName); gerr == nil && len(tok) > 0 {
		a.SetToken(string(tok))
	}
	c.api = a
}

// SetPinnedCert records a trusted server leaf-certificate fingerprint (TOFU) in the local
// state file, so a certificate trusted or re-trusted during a running session is remembered
// for future connections. Overwrites any previous pin for this server.
func (c *Controller) SetPinnedCert(fp string) error {
	c.record.PinnedCertSHA256 = fp
	return state.Save(c.statePath, c.record)
}

// FirewallAllowed reports the persisted user preference for the host inbound-allow rule. It is the
// preference alone — the rule is present only when this is true AND the tunnel is connected.
func (c *Controller) FirewallAllowed() bool { return c.record.FirewallAllowVPN }

// SetFirewallAllowed records the user's toggle choice and reconciles the host rule. The preference
// is persisted regardless of connection state (so it survives restarts), but the rule itself is
// installed only when the toggle is on AND the tunnel is connected; otherwise it is removed.
func (c *Controller) SetFirewallAllowed(on, connected bool) error {
	c.record.FirewallAllowVPN = on
	if err := state.Save(c.statePath, c.record); err != nil {
		return err
	}
	return c.applyFirewall(on && connected)
}

// ReconcileFirewall installs or removes the host rule to match (preference ON ∧ connected),
// called on connect/disconnect so the rule tracks the tunnel without changing the preference.
func (c *Controller) ReconcileFirewall(connected bool) error {
	return c.applyFirewall(c.record.FirewallAllowVPN && connected)
}

// applyFirewall opens or closes the host rule. Allow is idempotent (delete-then-add), so calling
// it while already open is safe.
func (c *Controller) applyFirewall(open bool) error {
	if open {
		return c.fw.Allow()
	}
	return c.fw.Clear()
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
	return 0, errNotRegistered
}
