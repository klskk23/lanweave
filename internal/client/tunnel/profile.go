// Package tunnel brings the client VPN tunnel up and down using an embedded user-space
// WireGuard engine. The profile builder here is host-agnostic and pure; the engine and
// OS-specific adapter addressing live in sibling files.
package tunnel

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"

	"lanweave/internal/client/state"
)

// KeepaliveSeconds is the persistent-keepalive interval (DESIGN §3.4; also hardcoded
// by the router client in cmd/lanweave-routerd, which must not import this
// wireguard-go-laden package): keeps an idle
// connection alive and visible as online.
const KeepaliveSeconds = 25

// BuildUAPIConfig assembles the wireguard-go UAPI configuration from the setup record and
// the device private key (base64). It is split-tunnel: a single allowed_ip equal to the
// recorded network range. WireGuard keys are converted from base64 to the hex the UAPI
// expects. The private key appears only in the returned string (held in memory by the
// caller), never logged or persisted.
func BuildUAPIConfig(rec state.Record, privKeyBase64 string) (string, error) {
	privHex, err := keyB64ToHex(privKeyBase64)
	if err != nil {
		return "", fmt.Errorf("device key: %w", err)
	}
	pubHex, err := keyB64ToHex(rec.ServerPublicKey)
	if err != nil {
		return "", fmt.Errorf("server key: %w", err)
	}
	if rec.Endpoint == "" {
		return "", fmt.Errorf("server endpoint is missing")
	}
	network, err := netip.ParsePrefix(rec.Network)
	if err != nil {
		return "", fmt.Errorf("network %q: %w", rec.Network, err)
	}
	if _, err := netip.ParseAddr(rec.IP); err != nil {
		return "", fmt.Errorf("device address %q: %w", rec.IP, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", privHex)
	fmt.Fprintf(&b, "public_key=%s\n", pubHex) // begins the single peer (the server)
	fmt.Fprintf(&b, "endpoint=%s\n", rec.Endpoint)
	fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", KeepaliveSeconds)
	fmt.Fprintf(&b, "allowed_ip=%s\n", network.Masked().String()) // split tunnel: VPN range only
	return b.String(), nil
}

// BuildPeerUpdate assembles an incremental UAPI update for the (single) server
// peer: replace its allowed_ips with the VPN network plus the given synthetic
// blocks (feature 033 consumer routes). Carrying public_key without
// private_key updates the existing peer in place — endpoint, keepalive and the
// handshake state are untouched, so applying this never drops the connection.
func BuildPeerUpdate(serverPubB64 string, network netip.Prefix, extras []netip.Prefix) (string, error) {
	pubHex, err := keyB64ToHex(serverPubB64)
	if err != nil {
		return "", fmt.Errorf("server key: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "public_key=%s\n", pubHex)
	fmt.Fprintf(&b, "replace_allowed_ips=true\n")
	fmt.Fprintf(&b, "allowed_ip=%s\n", network.Masked().String())
	for _, p := range extras {
		fmt.Fprintf(&b, "allowed_ip=%s\n", p.Masked().String())
	}
	return b.String(), nil
}

// routeDiff computes which prefixes must be added and removed to move the
// applied route set from current to desired. Pure for testing.
func routeDiff(current, desired []netip.Prefix) (add, del []netip.Prefix) {
	cur := map[netip.Prefix]bool{}
	for _, p := range current {
		cur[p] = true
	}
	want := map[netip.Prefix]bool{}
	for _, p := range desired {
		want[p] = true
		if !cur[p] {
			add = append(add, p)
		}
	}
	for _, p := range current {
		if !want[p] {
			del = append(del, p)
		}
	}
	return add, del
}

// serverVPNIP is the server's address inside the VPN — the first usable host in the
// network (e.g. 100.127.0.1 for 100.127.0.0/16). Used to probe reachability on connect.
func serverVPNIP(network string) (netip.Addr, bool) {
	p, err := netip.ParsePrefix(network)
	if err != nil {
		return netip.Addr{}, false
	}
	return p.Masked().Addr().Next(), true
}

func keyB64ToHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("decode key: %w", err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(raw))
	}
	return hex.EncodeToString(raw), nil
}
