package wg

import (
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/config"
)

// Server holds the live handle to the configured WireGuard device. The interface
// itself outlives the process (it is not torn down on Close).
type Server struct {
	name string
	pub  wgtypes.Key
	wgc  *wgctrl.Client
}

// EnsureInterface creates or adopts the configured WireGuard interface, addresses
// it with the first pool IP, brings it up, and configures the device with the
// server key, listen port, and zero peers. An existing interface is adopted and
// reconciled (its device key is overwritten to match the key file) rather than
// torn down, so a routine restart does not drop live client tunnels.
func EnsureInterface(cfg config.WireGuardConfig, key wgtypes.Key, log *slog.Logger) (*Server, error) {
	addr, bits, err := FirstUsableAddress(cfg.Network)
	if err != nil {
		return nil, err
	}

	link, created, err := ensureLink(cfg.Interface)
	if err != nil {
		return nil, err
	}

	if cfg.MTU > 0 {
		if merr := netlink.LinkSetMTU(link, cfg.MTU); merr != nil {
			return nil, fmt.Errorf("set MTU on %s: %w", cfg.Interface, merr)
		}
	}

	ipnet := &net.IPNet{IP: addr.AsSlice(), Mask: net.CIDRMask(bits, 32)}
	if aerr := netlink.AddrReplace(link, &netlink.Addr{IPNet: ipnet}); aerr != nil {
		return nil, fmt.Errorf("assign %s/%d to %s: %w", addr, bits, cfg.Interface, aerr)
	}
	if uerr := netlink.LinkSetUp(link); uerr != nil {
		return nil, fmt.Errorf("bring %s up: %w", cfg.Interface, uerr)
	}

	wgc, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("open wgctrl: %w", err)
	}
	port := cfg.ListenPort
	if cerr := wgc.ConfigureDevice(cfg.Interface, wgtypes.Config{
		PrivateKey:   &key,
		ListenPort:   &port,
		ReplacePeers: true, // this feature has zero client peers (FR-007)
		Peers:        nil,
	}); cerr != nil {
		_ = wgc.Close()
		return nil, fmt.Errorf("configure wireguard device %s (port %d in use?): %w", cfg.Interface, port, cerr)
	}

	log.Info("wireguard interface ready",
		"interface", cfg.Interface,
		"address", fmt.Sprintf("%s/%d", addr, bits),
		"listen_port", port,
		"public_key", key.PublicKey().String(), // public key is not a secret
		"created", created,
	)
	return &Server{name: cfg.Interface, pub: key.PublicKey(), wgc: wgc}, nil
}

// ensureLink returns the interface, creating it if absent. It errors if a
// non-WireGuard device already holds the name (conflict).
func ensureLink(name string) (netlink.Link, bool, error) {
	link, err := netlink.LinkByName(name)
	if err == nil {
		if link.Type() != "wireguard" {
			return nil, false, fmt.Errorf("interface %q exists but is type %q, not wireguard", name, link.Type())
		}
		return link, false, nil
	}
	var notFound netlink.LinkNotFoundError
	if !errors.As(err, &notFound) {
		return nil, false, fmt.Errorf("look up interface %q: %w", name, err)
	}

	la := netlink.NewLinkAttrs()
	la.Name = name
	if aerr := netlink.LinkAdd(&netlink.Wireguard{LinkAttrs: la}); aerr != nil {
		return nil, false, fmt.Errorf("create wireguard interface %q (need CAP_NET_ADMIN): %w", name, aerr)
	}
	link, err = netlink.LinkByName(name)
	if err != nil {
		return nil, false, fmt.Errorf("re-fetch interface %q after create: %w", name, err)
	}
	return link, true, nil
}

// PublicKey returns the server's WireGuard public key.
func (s *Server) PublicKey() wgtypes.Key { return s.pub }

// Close releases the wgctrl handle. It deliberately does NOT remove the
// interface, so existing client tunnels survive a process restart.
func (s *Server) Close() error {
	if s.wgc != nil {
		return s.wgc.Close()
	}
	return nil
}
