package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeEngine is the daemon's own seam (the real kernel engine has privileged
// integration tests of its own).
type fakeEngine struct {
	mu        sync.Mutex
	upCalls   int
	downCalls int
	upErr     error
	handshake time.Time
	hsErr     error
}

func (f *fakeEngine) Up() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upCalls++
	return f.upErr
}

func (f *fakeEngine) Down() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downCalls++
	return nil
}

func (f *fakeEngine) LastHandshake() (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handshake, f.hsErr
}

func (f *fakeEngine) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upCalls, f.downCalls
}

func (f *fakeEngine) set(fn func(*fakeEngine)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func testDaemon(e Engine) *Daemon {
	return &Daemon{Engine: e, Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), Tick: 5 * time.Millisecond}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}

// TestStaleHandshakeTriggersRebuild: a connected tunnel whose handshake ages
// past the threshold is torn down and rebuilt exactly (028 semantics).
func TestStaleHandshakeTriggersRebuild(t *testing.T) {
	e := &fakeEngine{handshake: time.Now().Add(-staleAfter - time.Minute)}
	d := testDaemon(e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	waitFor(t, func() bool { up, down := e.counts(); return up >= 2 && down >= 1 })
	// Make the new session fresh — rebuilding must stop.
	e.set(func(f *fakeEngine) { f.handshake = time.Now() })
	up1, _ := e.counts()
	time.Sleep(50 * time.Millisecond)
	up2, _ := e.counts()
	if up2 > up1+1 {
		t.Errorf("rebuild kept firing on a fresh session: %d -> %d", up1, up2)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

// TestUpFailureRetriesForever: a failing Up is retried every tick without the
// loop exiting, and converges once Up starts succeeding.
func TestUpFailureRetriesForever(t *testing.T) {
	e := &fakeEngine{upErr: errors.New("no network")}
	d := testDaemon(e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	waitFor(t, func() bool { up, _ := e.counts(); return up >= 3 })
	e.set(func(f *fakeEngine) { f.upErr = nil; f.handshake = time.Now() })
	before, _ := e.counts()
	waitFor(t, func() bool { up, _ := e.counts(); return up >= before+1 })
	// Once converged, no further rebuilds.
	stable, _ := e.counts()
	time.Sleep(50 * time.Millisecond)
	after, _ := e.counts()
	if after != stable {
		t.Errorf("loop kept rebuilding after convergence: %d -> %d", stable, after)
	}
	cancel()
	<-done
}

// TestNeverConnectedIsNotStale: a session that has not handshaked yet must not
// be rebuilt (first handshake legitimately takes a moment).
func TestNeverConnectedIsNotStale(t *testing.T) {
	e := &fakeEngine{} // zero handshake
	d := testDaemon(e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	time.Sleep(60 * time.Millisecond)
	up, down := e.counts()
	if up != 1 || down != 0 {
		t.Errorf("never-connected session rebuilt: up=%d down=%d", up, down)
	}
	cancel()
	<-done
	if _, down := e.counts(); down != 1 {
		t.Errorf("teardown on cancel: down=%d, want 1", down)
	}
}

// TestCancelTearsDown: context cancel tears the tunnel down exactly once and
// Run returns nil.
func TestCancelTearsDown(t *testing.T) {
	e := &fakeEngine{handshake: time.Now()}
	d := testDaemon(e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, func() bool { up, _ := e.counts(); return up == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, down := e.counts(); down != 1 {
		t.Errorf("down calls = %d, want exactly 1", down)
	}
}
