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
	upErr      error
	hs         bool
	rx, tx     int64
	upCalls    int
	closeCalls int
}

func (f *fakeEngine) up(_ string, _ netip.Addr, _ netip.Prefix) (string, error) {
	f.upCalls++
	if f.upErr != nil {
		return "", f.upErr
	}
	return "lwtest0", nil
}
func (f *fakeEngine) handshaked() (bool, error)       { return f.hs, nil }
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
