package tunnel

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"lanweave/internal/client/state"
)

// State is the tunnel's connection state.
type State int

const (
	Disconnected State = iota
	Connecting
	Connected
)

func (s State) String() string {
	switch s {
	case Connecting:
		return "connecting"
	case Connected:
		return "connected"
	default:
		return "disconnected"
	}
}

// Typed errors surfaced to the UI.
var (
	ErrServerUnreachable = errors.New("server unreachable")
	ErrNoSetup           = errors.New("device is not set up")
	ErrAdapter           = errors.New("could not set up the network adapter")
	ErrElevationDenied   = errors.New("administrator rights are required")
)

// engine is the seam over the user-space WireGuard device: the real implementation is
// wireguard-go (`wgEngine`); unit tests supply a fake. up creates the device + addresses
// the adapter; close removes both.
type engine interface {
	up(uapiConfig string, ip netip.Addr, network netip.Prefix) (ifName string, err error)
	handshaked() (bool, error)
	transfer() (rx, tx int64, err error)
	close() error
}

// Tunnel is the client's single VPN tunnel. One machine = one device = one tunnel.
type Tunnel struct {
	rec     state.Record
	privKey string

	mu     sync.Mutex
	state  State
	eng    engine
	ifName string

	newEngine      func() engine // injectable for tests; default = wireguard-go
	connectTimeout time.Duration
	pollInterval   time.Duration
}

// New builds a Tunnel from the setup record and the device private key (base64).
func New(rec state.Record, privKey string) *Tunnel {
	return &Tunnel{
		rec: rec, privKey: privKey,
		newEngine:      func() engine { return &wgEngine{} },
		connectTimeout: 8 * time.Second,
		pollInterval:   200 * time.Millisecond,
	}
}

// State returns the current connection state.
func (t *Tunnel) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// InterfaceName returns the OS interface name while connected (empty otherwise).
func (t *Tunnel) InterfaceName() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ifName
}

// Transfer returns this tunnel's cumulative received/sent bytes for the current connection
// (summed across peers), driving the Hero card's ↑/↓ display. The values are cumulative, not a
// rate: they only increase within a connection and reset to 0 on the next one (a fresh device).
// When not connected (no engine) it returns (0, 0, nil) — absence of traffic, not an error.
func (t *Tunnel) Transfer() (rx, tx int64, err error) {
	t.mu.Lock()
	eng := t.eng
	t.mu.Unlock()
	if eng == nil {
		return 0, 0, nil
	}
	return eng.transfer()
}

// Connect brings the tunnel up: build the profile, create the device + adapter, probe the
// server to trigger a handshake, and reach Connected once the handshake completes. A
// second Connect while active is a no-op. On any failure it tears down and returns a typed
// error, leaving the tunnel Disconnected.
func (t *Tunnel) Connect() error {
	t.mu.Lock()
	if t.state != Disconnected {
		t.mu.Unlock()
		return nil // one tunnel only
	}
	if t.privKey == "" || t.rec.IP == "" {
		t.mu.Unlock()
		return ErrNoSetup
	}
	t.state = Connecting
	eng := t.newEngine()
	t.eng = eng
	t.mu.Unlock()

	cfg, err := BuildUAPIConfig(t.rec, t.privKey)
	if err != nil {
		t.teardown()
		return ErrNoSetup
	}
	ip, err := netip.ParseAddr(t.rec.IP)
	if err != nil {
		t.teardown()
		return ErrNoSetup
	}
	network, err := netip.ParsePrefix(t.rec.Network)
	if err != nil {
		t.teardown()
		return ErrNoSetup
	}

	ifName, err := eng.up(cfg, ip, network)
	if err != nil {
		t.teardown()
		return fmt.Errorf("%w: %v", ErrAdapter, err)
	}
	t.mu.Lock()
	t.ifName = ifName
	t.mu.Unlock()

	// Probe the server's VPN address to trigger the first handshake, then wait for it.
	t.probeServer()
	deadline := time.Now().Add(t.connectTimeout)
	for time.Now().Before(deadline) {
		if ok, _ := eng.handshaked(); ok {
			t.mu.Lock()
			t.state = Connected
			t.mu.Unlock()
			return nil
		}
		time.Sleep(t.pollInterval)
		t.probeServer()
	}
	t.teardown()
	return ErrServerUnreachable
}

// Disconnect tears the tunnel down (remove adapter + close device) and returns to
// Disconnected. Idempotent: a no-op when already disconnected.
func (t *Tunnel) Disconnect() error {
	return t.teardown()
}

// Close is an alias for Disconnect, suitable for deferred app-exit teardown.
func (t *Tunnel) Close() error { return t.teardown() }

func (t *Tunnel) teardown() error {
	t.mu.Lock()
	eng := t.eng
	t.eng = nil
	t.ifName = ""
	t.state = Disconnected
	t.mu.Unlock()
	if eng != nil {
		return eng.close()
	}
	return nil
}

// probeServer sends a best-effort datagram to the server's VPN address to trigger the
// WireGuard handshake (errors ignored — the handshake, not this packet, is the signal).
func (t *Tunnel) probeServer() {
	gw, ok := serverVPNIP(t.rec.Network)
	if !ok {
		return
	}
	c, err := net.DialTimeout("udp", net.JoinHostPort(gw.String(), "9"), 500*time.Millisecond)
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte{0})
}

// wgEngine is the real engine backed by wireguard-go + WinTun/Linux tun.
type wgEngine struct {
	dev  *device.Device
	name string
}

func (e *wgEngine) up(cfg string, ip netip.Addr, network netip.Prefix) (string, error) {
	td, err := tun.CreateTUN("lanweave0", 1420)
	if err != nil {
		return "", fmt.Errorf("create adapter: %w", err)
	}
	name, err := td.Name()
	if err != nil {
		_ = td.Close()
		return "", fmt.Errorf("adapter name: %w", err)
	}
	dev := device.NewDevice(td, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "lanweave: "))
	if err := dev.IpcSet(cfg); err != nil {
		dev.Close()
		return "", fmt.Errorf("apply config: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return "", fmt.Errorf("bring device up: %w", err)
	}
	if err := configureAdapter(name, ip, network); err != nil {
		dev.Close()
		return "", err
	}
	e.dev = dev
	e.name = name
	return name, nil
}

func (e *wgEngine) handshaked() (bool, error) {
	if e.dev == nil {
		return false, nil
	}
	s, err := e.dev.IpcGet()
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(s, "\n") {
		if v, ok := strings.CutPrefix(line, "last_handshake_time_sec="); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if n > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

// transfer reports cumulative received/sent bytes by parsing the same dev.IpcGet() UAPI text
// that handshaked() reads: every peer line carries rx_bytes=/tx_bytes= (cumulative per-peer
// counters); summing them yields the device totals. No new syscall surface beyond IpcGet().
func (e *wgEngine) transfer() (rx, tx int64, err error) {
	if e.dev == nil {
		return 0, 0, nil
	}
	s, err := e.dev.IpcGet()
	if err != nil {
		return 0, 0, err
	}
	rx, tx = sumTransfer(s)
	return rx, tx, nil
}

// sumTransfer sums the per-peer rx_bytes=/tx_bytes= counters in WireGuard UAPI text. Split out
// as a pure function so the parsing/aggregation is unit-testable with fixed multi-peer text.
func sumTransfer(uapi string) (rx, tx int64) {
	for line := range strings.SplitSeq(uapi, "\n") {
		if v, ok := strings.CutPrefix(line, "rx_bytes="); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			rx += n
		} else if v, ok := strings.CutPrefix(line, "tx_bytes="); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			tx += n
		}
	}
	return rx, tx
}

func (e *wgEngine) close() error {
	if e.name != "" {
		_ = teardownAdapter(e.name)
	}
	if e.dev != nil {
		e.dev.Close() // closes the tun and removes the interface
	}
	e.dev = nil
	e.name = ""
	return nil
}
