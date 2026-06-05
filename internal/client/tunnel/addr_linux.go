//go:build linux

package tunnel

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
)

// configureAdapter assigns the device's VPN address to the tunnel interface, brings it up,
// and routes the VPN range through it (split tunnel). Used by the real engine on Linux —
// the same approach the server uses for its WireGuard interface.
func configureAdapter(ifName string, ip netip.Addr, network netip.Prefix) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("find adapter %s: %w", ifName, err)
	}
	addr := &netlink.Addr{IPNet: &net.IPNet{IP: ip.AsSlice(), Mask: net.CIDRMask(network.Bits(), 32)}}
	if err := netlink.AddrReplace(link, addr); err != nil {
		return fmt.Errorf("assign %s to %s: %w", ip, ifName, err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring %s up: %w", ifName, err)
	}
	dst := &net.IPNet{IP: network.Masked().Addr().AsSlice(), Mask: net.CIDRMask(network.Bits(), 32)}
	route := &netlink.Route{LinkIndex: link.Attrs().Index, Dst: dst}
	if err := netlink.RouteReplace(route); err != nil {
		return fmt.Errorf("route %s via %s: %w", network, ifName, err)
	}
	return nil
}

// teardownAdapter brings the interface down (best-effort). Closing the user-space device
// removes the tun interface entirely, so a missing interface is not an error.
func teardownAdapter(ifName string) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return nil // already gone (device closed)
	}
	_ = netlink.LinkSetDown(link)
	return nil
}

// interfacePresent reports whether the named interface still exists (used by teardown
// assertions in tests).
func interfacePresent(ifName string) bool {
	_, err := netlink.LinkByName(ifName)
	return err == nil
}
