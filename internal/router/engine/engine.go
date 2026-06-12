// Package engine owns the router client's kernel WireGuard tunnel: one
// interface toward the relay server, the node /32 address, the VPN-network
// route and a keepalive'd server peer. It is the productized form of the
// peer-namespace setup validated by the 030 dataplane e2e test
// (internal/server/app/announce_dataplane_test.go newPeerNS).
package engine

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"lanweave/internal/netutil"
)

// DefaultIface is the fixed tunnel interface name on the router.
const DefaultIface = "lanweave0"

// ErrIfaceExists reports that the tunnel interface already exists — the engine
// refuses to adopt or overwrite an interface it did not create.
var ErrIfaceExists = errors.New("tunnel interface already exists")

// Config describes the tunnel: everything comes from the onboard state record
// plus the device private key.
type Config struct {
	Iface        string // empty → DefaultIface
	PrivateKey   string // device private key (base64)
	Address      netip.Addr
	Network      netip.Prefix // VPN pool; also the route and the peer AllowedIPs
	ServerPubKey string
	Endpoint     string // host:port the server listens on (UDP)
	Keepalive    time.Duration
}

func (c Config) iface() string {
	if c.Iface != "" {
		return c.Iface
	}
	return DefaultIface
}

// Engine drives one kernel WireGuard interface.
type Engine struct {
	cfg Config

	routesMu      sync.Mutex // consumer-route memory (033); daemon and reconcile goroutines
	routesWanted  []netip.Prefix
	routesApplied []netip.Prefix
}

// New returns an engine for the given config (no kernel side effects yet).
func New(cfg Config) *Engine { return &Engine{cfg: cfg} }

// Up creates and configures the tunnel interface. It fails with ErrIfaceExists
// when the name is taken (never silently overwriting someone else's interface).
func (e *Engine) Up() error {
	name := e.cfg.iface()
	if _, err := netlink.LinkByName(name); err == nil {
		return fmt.Errorf("%w: %s", ErrIfaceExists, name)
	}

	key, err := wgtypes.ParseKey(e.cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("parse device key: %w", err)
	}
	serverKey, err := wgtypes.ParseKey(e.cfg.ServerPubKey)
	if err != nil {
		return fmt.Errorf("parse server key: %w", err)
	}
	host, port, err := splitEndpoint(e.cfg.Endpoint)
	if err != nil {
		return err
	}

	if err := netlink.LinkAdd(&netlink.Wireguard{LinkAttrs: netlink.LinkAttrs{Name: name}}); err != nil {
		return fmt.Errorf("create interface %s: %w", name, err)
	}
	// Any failure past this point tears the interface back down so a half-built
	// tunnel never lingers.
	fail := func(err error) error {
		_ = e.Down()
		return err
	}

	link, err := netlink.LinkByName(name)
	if err != nil {
		return fail(fmt.Errorf("re-fetch interface: %w", err))
	}
	addr, err := netlink.ParseAddr(e.cfg.Address.String() + "/32")
	if err != nil {
		return fail(fmt.Errorf("parse address: %w", err))
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return fail(fmt.Errorf("assign address: %w", err))
	}

	wgc, err := wgctrl.New()
	if err != nil {
		return fail(fmt.Errorf("open wgctrl: %w", err))
	}
	defer wgc.Close()
	keepalive := e.cfg.Keepalive
	netw := e.cfg.Network.Masked()
	allowed := net.IPNet{IP: netw.Addr().AsSlice(), Mask: net.CIDRMask(netw.Bits(), 32)}
	if err := wgc.ConfigureDevice(name, wgtypes.Config{
		PrivateKey: &key,
		Peers: []wgtypes.PeerConfig{{
			PublicKey:                   serverKey,
			Endpoint:                    &net.UDPAddr{IP: host, Port: port},
			AllowedIPs:                  []net.IPNet{allowed},
			PersistentKeepaliveInterval: &keepalive,
		}},
	}); err != nil {
		return fail(fmt.Errorf("configure device: %w", err))
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fail(fmt.Errorf("bring interface up: %w", err))
	}
	// Replace (not add): a stale route from a crashed previous run — or, in
	// tests, the server interface sharing the namespace — must not wedge the
	// tunnel. This mirrors wg-quick's route handling.
	if err := netlink.RouteReplace(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       &net.IPNet{IP: allowed.IP, Mask: allowed.Mask},
	}); err != nil {
		return fail(fmt.Errorf("set VPN route: %w", err))
	}
	return nil
}

// Down removes the tunnel interface (routes and addresses die with it). It is
// idempotent: a missing interface is success.
func (e *Engine) Down() error {
	e.routesMu.Lock()
	e.routesWanted, e.routesApplied = nil, nil
	e.routesMu.Unlock()
	link, err := netlink.LinkByName(e.cfg.iface())
	if err != nil {
		return nil
	}
	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("delete interface: %w", err)
	}
	return nil
}

// LastHandshake returns the server peer's last handshake time (zero when the
// tunnel has never completed one).
func (e *Engine) LastHandshake() (time.Time, error) {
	wgc, err := wgctrl.New()
	if err != nil {
		return time.Time{}, fmt.Errorf("open wgctrl: %w", err)
	}
	defer wgc.Close()
	dev, err := wgc.Device(e.cfg.iface())
	if err != nil {
		return time.Time{}, fmt.Errorf("read device: %w", err)
	}
	for _, p := range dev.Peers {
		if p.PublicKey.String() == e.cfg.ServerPubKey {
			return p.LastHandshakeTime, nil
		}
	}
	return time.Time{}, errors.New("server peer not present on interface")
}

// Connected reports whether the last handshake is younger than the threshold.
func (e *Engine) Connected(threshold time.Duration) bool {
	hs, err := e.LastHandshake()
	if err != nil || hs.IsZero() {
		return false
	}
	return time.Since(hs) < threshold
}

func splitEndpoint(endpoint string) (net.IP, int, error) {
	hostStr, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, 0, fmt.Errorf("parse endpoint %q: %w", endpoint, err)
	}
	port, err := net.LookupPort("udp", portStr)
	if err != nil {
		return nil, 0, fmt.Errorf("parse endpoint port %q: %w", portStr, err)
	}
	ips, err := net.LookupIP(hostStr)
	if err != nil || len(ips) == 0 {
		return nil, 0, fmt.Errorf("resolve endpoint host %q: %w", hostStr, err)
	}
	// Prefer IPv4 (the VPN pool and relay listener are v4).
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4, port, nil
		}
	}
	return ips[0], port, nil
}

// SetRoutes brings the consumer routes (feature 033: zone-mates' synthetic
// blocks) to the desired set: the server peer's AllowedIPs are replaced in
// place (UpdateOnly — endpoint/keepalive/handshake untouched) and kernel
// routes are diffed per prefix. A prefix overlapping a local network is
// skipped with the rest unaffected (FR-005) and retried next call. Returns
// the prefixes actually applied. Down clears the memory.
func (e *Engine) SetRoutes(extra []netip.Prefix) ([]netip.Prefix, error) {
	e.routesMu.Lock()
	defer e.routesMu.Unlock()

	desired := netutil.CanonicalPrefixes(extra)
	if slices.Equal(desired, e.routesWanted) && len(e.routesApplied) == len(e.routesWanted) {
		return append([]netip.Prefix(nil), e.routesApplied...), nil
	}

	iface := e.cfg.iface()
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return nil, fmt.Errorf("tunnel interface %s: %w", iface, err)
	}

	// Conflict gate per prefix (FR-005).
	applied := make([]netip.Prefix, 0, len(desired))
	for _, p := range desired {
		if _, clash := netutil.LocalOverlap(p, iface); clash {
			continue
		}
		applied = append(applied, p)
	}

	// Kernel route diff.
	cur := map[netip.Prefix]bool{}
	for _, p := range e.routesApplied {
		cur[p] = true
	}
	want := map[netip.Prefix]bool{}
	routed := make([]netip.Prefix, 0, len(applied))
	for _, p := range applied {
		want[p] = true
		if !cur[p] {
			if err := netlink.RouteReplace(&netlink.Route{
				LinkIndex: link.Attrs().Index,
				Dst:       &net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), 32)},
			}); err != nil {
				continue // per-prefix tolerance
			}
		}
		routed = append(routed, p)
	}
	for _, p := range e.routesApplied {
		if !want[p] {
			_ = netlink.RouteDel(&netlink.Route{
				LinkIndex: link.Attrs().Index,
				Dst:       &net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), 32)},
			})
		}
	}

	// Replace the server peer's AllowedIPs in place: VPN network ∪ routed.
	srvKey, err := wgtypes.ParseKey(e.cfg.ServerPubKey)
	if err != nil {
		return routed, fmt.Errorf("server key: %w", err)
	}
	allowed := make([]net.IPNet, 0, len(routed)+1)
	netMasked := e.cfg.Network.Masked()
	allowed = append(allowed, net.IPNet{IP: netMasked.Addr().AsSlice(), Mask: net.CIDRMask(netMasked.Bits(), 32)})
	for _, p := range routed {
		allowed = append(allowed, net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), 32)})
	}
	wgc, err := wgctrl.New()
	if err != nil {
		return routed, fmt.Errorf("wgctrl: %w", err)
	}
	defer wgc.Close()
	if err := wgc.ConfigureDevice(iface, wgtypes.Config{
		Peers: []wgtypes.PeerConfig{{
			PublicKey:         srvKey,
			UpdateOnly:        true,
			ReplaceAllowedIPs: true,
			AllowedIPs:        allowed,
		}},
	}); err != nil {
		return routed, fmt.Errorf("update allowed ips: %w", err)
	}

	e.routesWanted = desired
	e.routesApplied = routed
	return append([]netip.Prefix(nil), routed...), nil
}

// Routes returns the consumer routes currently applied.
func (e *Engine) Routes() []netip.Prefix {
	e.routesMu.Lock()
	defer e.routesMu.Unlock()
	return append([]netip.Prefix(nil), e.routesApplied...)
}

// localOverlapNot reports whether the prefix overlaps a local interface
