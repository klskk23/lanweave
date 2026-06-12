package tunnel

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// fakeEngine is an in-package stand-in for the wireguard-go engine (our own seam), so the
// state machine is testable without a real device or privileges.
type fakeEngine struct {
	upErr        error
	hs           bool
	hsUnix       int64  // last-handshake timestamp returned by lastHandshake() (drives Stale)
	onHandshaked func() // optional hook fired inside handshaked(), to inject the disconnect race
	rx, tx       int64
	upCalls      int
	closeCalls   int
	peerUpdates  []string // applyPeerUpdate payloads (033 consumer routes)
}

func (f *fakeEngine) applyPeerUpdate(uapi string) error {
	f.peerUpdates = append(f.peerUpdates, uapi)
	return nil
}

func (f *fakeEngine) up(_ string, _ netip.Addr, _ netip.Prefix) (string, error) {
	f.upCalls++
	if f.upErr != nil {
		return "", f.upErr
	}
	return "lwtest0", nil
}
func (f *fakeEngine) handshaked() (bool, error) {
	if f.onHandshaked != nil {
		f.onHandshaked()
	}
	return f.hs, nil
}
func (f *fakeEngine) lastHandshake() (int64, error)   { return f.hsUnix, nil }
func (f *fakeEngine) transfer() (int64, int64, error) { return f.rx, f.tx, nil }
func (f *fakeEngine) close() error                    { f.closeCalls++; return nil }

func newTestTunnel(t *testing.T, eng *fakeEngine) *Tunnel {
	t.Helper()
	rec, priv := validRecord(t)
	tn := New(rec, priv)
	tn.newEngine = func() engine { return eng }
	tn.connectTimeout = 400 * time.Millisecond
	tn.pollInterval = 50 * time.Millisecond
	return tn
}

func TestConnectSuccess(t *testing.T) {
	eng := &fakeEngine{hs: true}
	tn := newTestTunnel(t, eng)
	if tn.State() != Disconnected {
		t.Fatal("should start disconnected")
	}
	if err := tn.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if tn.State() != Connected {
		t.Errorf("state = %v, want Connected", tn.State())
	}
	// A second Connect while active is a no-op (no new engine).
	if err := tn.Connect(); err != nil {
		t.Fatalf("second connect: %v", err)
	}
	if eng.upCalls != 1 {
		t.Errorf("up called %d times, want 1 (second Connect must be a no-op)", eng.upCalls)
	}
}

func TestConnectTimeout(t *testing.T) {
	eng := &fakeEngine{hs: false} // never handshakes
	tn := newTestTunnel(t, eng)
	if err := tn.Connect(); !errors.Is(err, ErrServerUnreachable) {
		t.Fatalf("connect: got %v, want ErrServerUnreachable", err)
	}
	if tn.State() != Disconnected {
		t.Errorf("state = %v, want Disconnected after failure", tn.State())
	}
	if eng.closeCalls == 0 {
		t.Error("engine must be torn down on connect failure")
	}
}

func TestDisconnectIdempotent(t *testing.T) {
	eng := &fakeEngine{hs: true}
	tn := newTestTunnel(t, eng)
	if err := tn.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := tn.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if tn.State() != Disconnected {
		t.Errorf("state = %v, want Disconnected", tn.State())
	}
	// Idempotent: a second Disconnect is a no-op.
	if err := tn.Disconnect(); err != nil {
		t.Errorf("second disconnect should be a no-op, got %v", err)
	}
}

// parseHandshakeAge extracts last_handshake_time_sec (taking the most recent across peers),
// returning ok=false for a missing field, a zero value, or garbage (U1, FR-001/FR-002).
func TestParseHandshakeAge(t *testing.T) {
	cases := []struct {
		name   string
		uapi   string
		wantTS int64
		wantOK bool
	}{
		{"present", "public_key=aa\nlast_handshake_time_sec=1717000000\nrx_bytes=1\n", 1717000000, true},
		{"zero", "last_handshake_time_sec=0\n", 0, false},
		{"missing", "public_key=aa\nrx_bytes=1\n", 0, false},
		{"multi-peer takes max", "last_handshake_time_sec=100\npublic_key=bb\nlast_handshake_time_sec=500\n", 500, true},
		{"garbage value", "last_handshake_time_sec=notanumber\n", 0, false},
		{"empty", "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts, ok := parseHandshakeAge(c.uapi)
			if ts != c.wantTS || ok != c.wantOK {
				t.Errorf("parseHandshakeAge = (%d,%v), want (%d,%v)", ts, ok, c.wantTS, c.wantOK)
			}
		})
	}
}

// Stale fires only for a live tunnel whose handshake is older than the threshold; the boundary
// is exclusive (== threshold is fresh). now is injected — zero wall-clock sleeps (U2, FR-002/3).
func TestStaleThresholdBoundary(t *testing.T) {
	eng := &fakeEngine{hs: true}
	tn := newTestTunnel(t, eng)
	tn.staleThreshold = 240 * time.Second
	if err := tn.Connect(); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1_700_000_000, 0)
	eng.hsUnix = base.Unix()

	if tn.Stale(base.Add(239 * time.Second)) {
		t.Error("age 239s (< 240s threshold) must NOT be stale")
	}
	if tn.Stale(base.Add(240 * time.Second)) {
		t.Error("age exactly 240s must NOT be stale (boundary is exclusive)")
	}
	if !tn.Stale(base.Add(241 * time.Second)) {
		t.Error("age 241s (> 240s threshold) MUST be stale")
	}

	// Never-handshaked: not stale regardless of clock.
	eng.hsUnix = 0
	if tn.Stale(base.Add(10 * time.Hour)) {
		t.Error("a tunnel that never handshaked must not be stale")
	}
	// A down tunnel (no engine) is never stale.
	if err := tn.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if tn.Stale(base.Add(10 * time.Hour)) {
		t.Error("a disconnected tunnel must not be stale")
	}
}

// Desired defaults to false on a fresh tunnel — a newly launched app does not auto-connect
// (U3, FR-007). The accessor round-trips.
func TestDesiredDefaultsFalse(t *testing.T) {
	tn := newTestTunnel(t, &fakeEngine{})
	if tn.Desired() {
		t.Error("a fresh Tunnel must report Desired()==false (no auto-connect at launch)")
	}
	tn.SetDesired(true)
	if !tn.Desired() {
		t.Error("SetDesired(true) must be observable")
	}
	tn.SetDesired(false)
	if tn.Desired() {
		t.Error("SetDesired(false) must be observable")
	}
}

// Single-flight reconcile: a manual Disconnect that lands while a Connect attempt is mid-flight
// wins — the connection must end Disconnected with the engine torn down, never "revived" to
// Connected (U4, FR-011/FR-012). The race is injected deterministically (no sleeps) via the
// engine's handshaked hook, which runs Disconnect just before Connect would commit Connected.
func TestSingleFlightDisconnectWins(t *testing.T) {
	eng := &fakeEngine{hs: true}
	tn := newTestTunnel(t, eng)
	eng.onHandshaked = func() { _ = tn.Disconnect() } // user disconnects mid-connect

	if err := tn.Connect(); err != nil {
		t.Fatalf("connect returned %v, want nil (abandoned cleanly)", err)
	}
	if tn.State() != Disconnected {
		t.Errorf("state = %v, want Disconnected — disconnect must win, no revive", tn.State())
	}
	if eng.closeCalls == 0 {
		t.Error("engine must be torn down (closed), not leaked")
	}
}

// A3 (US3): the self-heal boundary. A failed manual connect must leave intent unset, so nothing
// retries it in the background (SC-004); and a freshly-built Tunnel — the "app restart" case —
// starts with no intent, so launch never auto-connects (SC-007). Intent is set by the UI only on
// a *successful* manual connect (panel T016); the tunnel itself never sets it, so both cases hold
// at this layer.
func TestSelfHealBoundary(t *testing.T) {
	// Failed manual connect → still not desired → no background retry (SC-004).
	eng := &fakeEngine{hs: false} // never handshakes → ErrServerUnreachable
	tn := newTestTunnel(t, eng)
	if err := tn.Connect(); !errors.Is(err, ErrServerUnreachable) {
		t.Fatalf("connect: got %v, want ErrServerUnreachable", err)
	}
	if tn.Desired() {
		t.Error("a failed manual connect must leave Desired()==false (no auto-retry, SC-004)")
	}

	// "Restart" = a brand-new Tunnel: no intent, so no auto-connect at launch (SC-007).
	rec, priv := validRecord(t)
	fresh := New(rec, priv)
	if fresh.Desired() {
		t.Error("a freshly launched Tunnel must report Desired()==false (no auto-connect, SC-007)")
	}
	if fresh.State() != Disconnected {
		t.Errorf("a freshly launched Tunnel must be Disconnected, got %v", fresh.State())
	}
}

func TestConnectNoSetup(t *testing.T) {
	rec, _ := validRecord(t)
	tn := New(rec, "") // no private key
	if err := tn.Connect(); !errors.Is(err, ErrNoSetup) {
		t.Errorf("connect without key: got %v, want ErrNoSetup", err)
	}
}

// sumTransfer must add up the per-peer rx_bytes=/tx_bytes= counters across every peer line in
// the WireGuard UAPI text, ignoring all other fields (FR-012).
func TestSumTransferMultiPeer(t *testing.T) {
	uapi := strings.Join([]string{
		"private_key=00",
		"public_key=aa",
		"last_handshake_time_sec=123",
		"rx_bytes=1000",
		"tx_bytes=2000",
		"public_key=bb",
		"last_handshake_time_sec=456",
		"rx_bytes=40",
		"tx_bytes=7",
		"",
	}, "\n")
	rx, tx := sumTransfer(uapi)
	if rx != 1040 || tx != 2007 {
		t.Errorf("sumTransfer = (rx %d, tx %d), want (1040, 2007)", rx, tx)
	}
	if rx, tx := sumTransfer("errno=0\n\n"); rx != 0 || tx != 0 {
		t.Errorf("sumTransfer(no peers) = (%d, %d), want (0, 0)", rx, tx)
	}
}

// Transfer reports the engine's totals while connected, and (0,0,nil) — not an error — when
// there is no engine (disconnected), so the Hero can simply hide the counters.
func TestTransferConnectedAndDisconnected(t *testing.T) {
	eng := &fakeEngine{hs: true, rx: 1040, tx: 2007}
	tn := newTestTunnel(t, eng)
	if err := tn.Connect(); err != nil {
		t.Fatal(err)
	}
	rx, tx, err := tn.Transfer()
	if err != nil || rx != 1040 || tx != 2007 {
		t.Fatalf("Transfer connected = (%d, %d, %v), want (1040, 2007, nil)", rx, tx, err)
	}
	if err := tn.Disconnect(); err != nil {
		t.Fatal(err)
	}
	rx, tx, err = tn.Transfer()
	if err != nil || rx != 0 || tx != 0 {
		t.Errorf("Transfer disconnected = (%d, %d, %v), want (0, 0, nil)", rx, tx, err)
	}
}
