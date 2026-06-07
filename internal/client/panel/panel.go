// Package panel is the UI-free management controller for the desktop client. It assembles
// the view data (devices, zones, members) and performs every operation against an
// authenticated API client, reusing a cached session. It has no Fyne dependency, so it is
// tested headlessly against a real server.
package panel

import (
	"errors"
	"time"

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
	RefreshToken() string
	SetRefreshToken(token string)
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
	Logout() error
}

var _ api = (*apiclient.Client)(nil)

// errNotRegistered means this machine's device is not in the caller's node list (so logout
// can distinguish "already gone" from a list/network failure).
var errNotRegistered = errors.New("this device is not registered with the server")

// ErrRemoteMayLinger accompanies a LogoutDone when the remote node removal hit a *reachable*
// non-network, non-auth failure (e.g. a 5xx or a changed certificate). Local teardown still
// completes (the user is signed out locally), but the server-side node may still be registered,
// so the GUI surfaces the "remote may still be registered" advisory. It is NOT a local error.
var ErrRemoteMayLinger = errors.New("remote node may still be registered")

// LogoutOutcome classifies an attempted logout (data-model.md outcome table).
type LogoutOutcome int

const (
	// LogoutDone: the remote node was confirmed removed (or already absent); the refresh token
	// was revoked best-effort and all local material (keyring + state) was cleared.
	LogoutDone LogoutOutcome = iota
	// LogoutBlocked: every remote-removal attempt failed with network-unreachability. NOTHING
	// changed — tunnel, firewall, keyring, and state are all intact; the GUI shows the two-button
	// blocked prompt (Cancel / Force).
	LogoutBlocked
	// LogoutNeedSignIn: the session expired and the lazy refresh also failed. Nothing changed; the
	// GUI prompts a fresh sign-in and, on success, retries Logout().
	LogoutNeedSignIn
)

// removeResult is the internal classification of a remote-node removal attempt.
type removeResult int

const (
	removeDone       removeResult = iota // removed, or already absent (404 / not in list)
	removeBlocked                        // network-unreachable (retryable / blocking)
	removeNeedSignIn                     // session expired and refresh failed
	removeWarn                           // reachable but failed (5xx / cert) — proceed, warn linger
)

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
	// sleep is the delay between bounded remote-removal retries (research D3). Defaults to
	// time.Sleep; tests replace it (via export_test.go) with a no-op/recorder so the suite is
	// instant and deterministic (Constitution II — no wall-clock sleeps in tests).
	sleep func(time.Duration)
}

// New builds a controller bound to an API client, the setup record (to identify this
// machine), and the secure store (for the cached session token). statePath lets Logout
// clear the local state file; insecure seeds the "certificate not verified" indicator for
// the --insecure CLI-flag case; fw installs/removes the optional host inbound-allow rule.
func New(a api, record state.Record, keys keyring.Store, statePath string, insecure bool, fw firewall.Control) *Controller {
	return &Controller{api: a, record: record, keys: keys, statePath: statePath, insecure: insecure, fw: fw, sleep: time.Sleep}
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
	// Restore the refresh token too, so an expired access token is silently renewed
	// (apiclient.do) during this session instead of forcing a sign-in.
	if rt, rerr := c.keys.Get(keyring.RefreshTokenName); rerr == nil && len(rt) > 0 {
		c.api.SetRefreshToken(string(rt))
	}
	if _, merr := c.api.Me(); merr != nil {
		if errors.Is(merr, apiclient.ErrSessionExpired) || errors.Is(merr, apiclient.ErrAuthFailed) {
			return true, nil
		}
		return false, merr
	}
	return false, nil
}

// SignIn authenticates and caches the new session token and refresh token in the
// secure store, so a later launch can restore the session and silently renew it.
func (c *Controller) SignIn(username, password string) error {
	if err := c.api.Login(username, password); err != nil {
		return err
	}
	if err := c.keys.Set(keyring.SessionTokenName, []byte(c.api.Token())); err != nil {
		return err
	}
	return c.keys.Set(keyring.RefreshTokenName, []byte(c.api.RefreshToken()))
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

// Logout runs the remote-first hardened sign-out (slice 025). It removes this device's own
// node FIRST (the control API is public HTTPS, independent of the tunnel), retrying ONLY on
// network-unreachability up to 3 times at 1 s. The outcome drives the GUI:
//
//   - LogoutBlocked: every attempt hit network-unreachability → NOTHING is touched (no firewall
//     clear, no keyring/state delete) so the user can retry or force; err is nil.
//   - LogoutNeedSignIn: the session expired and the lazy refresh also failed → nothing touched.
//   - LogoutDone: the node is gone (or already absent), OR a reachable non-network failure
//     occurred (in which case err is ErrRemoteMayLinger). Either way the refresh token is revoked
//     best-effort, the host firewall rule is cleared, and the keyring + state are cleared. A
//     non-nil err is ErrRemoteMayLinger (benign linger advisory) and/or a local-teardown failure.
func (c *Controller) Logout() (LogoutOutcome, error) {
	switch c.removeRemoteNode() {
	case removeBlocked:
		// Nothing changed: the node is the orphan-causing residue and it is still there.
		return LogoutBlocked, nil
	case removeNeedSignIn:
		return LogoutNeedSignIn, nil
	case removeWarn:
		return LogoutDone, errors.Join(ErrRemoteMayLinger, c.tearDownLocal())
	default: // removeDone
		return LogoutDone, c.tearDownLocal()
	}
}

// ForceLogout is the escape hatch from the blocked prompt: an unconditional full local teardown
// that accepts a server-side orphaned node. It best-effort revokes the refresh token, clears the
// host firewall rule, and deletes the keyring + state — the old always-clear behavior, now
// reachable only via the force button (research D5, INV-5).
func (c *Controller) ForceLogout() error {
	return c.tearDownLocal()
}

// tearDownLocal revokes this device's refresh token (best-effort) then clears all local material:
// the host firewall rule, the three keyring entries, and the state file. The returned error is
// non-nil only when a local delete failed (the RT revoke is best-effort and never re-blocks).
func (c *Controller) tearDownLocal() error {
	// Revoke the refresh token server-side so a cached RT can never silently renew after logout.
	// Best-effort (research D6): the orphan-causing residue, the node, is already gone.
	_ = c.api.Logout()
	// Close the host rule before clearing state so logout never strands an open firewall.
	_ = c.fw.Clear()
	return errors.Join(
		c.keys.Delete(keyring.SessionTokenName),
		c.keys.Delete(keyring.RefreshTokenName),
		c.keys.Delete(keyring.DeviceKeyName),
		state.Clear(c.statePath),
	)
}

// removeRemoteNode deletes this device's own node with a bounded retry: at most 3 attempts, a
// c.sleep(1s) between attempts, retrying ONLY on network-unreachability. It classifies the
// terminal result into removeDone (removed / already absent), removeBlocked (unreachable after
// all attempts), removeNeedSignIn (session expired + refresh failed), or removeWarn (reachable
// but failed — 5xx / cert change).
func (c *Controller) removeRemoteNode() removeResult {
	const maxAttempts = 3
	res := removeBlocked
	for attempt := range maxAttempts {
		if attempt > 0 {
			c.sleep(1 * time.Second)
		}
		if res = c.tryRemoveRemoteNode(); res != removeBlocked {
			return res
		}
	}
	return res // removeBlocked after maxAttempts
}

// tryRemoveRemoteNode is a single removal attempt: resolve this machine's node id then delete it.
// A 404 (ErrZoneNotFound) or a not-in-list (errNotRegistered) is removeDone (already absent).
func (c *Controller) tryRemoveRemoteNode() removeResult {
	id, err := c.thisMachineNodeID()
	if err != nil {
		if errors.Is(err, errNotRegistered) {
			return removeDone // already absent
		}
		return classifyRemoveErr(err)
	}
	if derr := c.api.DeleteNode(id); derr != nil {
		if errors.Is(derr, apiclient.ErrZoneNotFound) {
			return removeDone // raced, already gone
		}
		return classifyRemoveErr(derr)
	}
	return removeDone
}

// classifyRemoveErr maps a removal error to a removeResult. Only ErrUnreachable blocks (research
// D1); ErrSessionExpired/ErrRefreshFailed need a fresh sign-in; everything else (5xx, cert) is a
// reachable failure that proceeds with a linger warning.
func classifyRemoveErr(err error) removeResult {
	switch {
	case errors.Is(err, apiclient.ErrUnreachable):
		return removeBlocked
	case errors.Is(err, apiclient.ErrSessionExpired), errors.Is(err, apiclient.ErrRefreshFailed):
		return removeNeedSignIn
	default:
		return removeWarn
	}
}

// UseClient swaps in a freshly built API client (e.g. rebuilt with apiclient.WithPinnedCert
// after the user trusts or re-trusts a certificate) and re-applies the cached session token
// so the signed-in session survives the swap. It does NOT change the insecure indicator —
// trusting a pinned certificate is a verified connection, not an insecure one.
func (c *Controller) UseClient(a api) {
	if tok, gerr := c.keys.Get(keyring.SessionTokenName); gerr == nil && len(tok) > 0 {
		a.SetToken(string(tok))
	}
	if rt, gerr := c.keys.Get(keyring.RefreshTokenName); gerr == nil && len(rt) > 0 {
		a.SetRefreshToken(string(rt))
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
