package status

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func eventually(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// TestRunRefreshesAndStops verifies the poll loop picks up new handshake data on a
// tick (freshness, US2) and that cancelling the context stops Run promptly (FR-009).
func TestRunRefreshesAndStops(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	src := func() (map[string]time.Time, error) {
		mu.Lock()
		calls++
		c := calls
		mu.Unlock()
		if c == 1 {
			return map[string]time.Time{}, nil // first poll: node not yet connected
		}
		return map[string]time.Time{"n": time.Now()}, nil // later polls: connected now
	}
	tr := New(src, 5*time.Millisecond, quietLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tr.Run(ctx); close(done) }()

	if tr.Online("n") {
		t.Fatal("node should be offline before the first refresh observed a handshake")
	}
	if !eventually(2*time.Second, func() bool { return tr.Online("n") }) {
		t.Fatal("node never became online across refresh ticks")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// TestSourceErrorKeepsSnapshot verifies a fresh tracker reports offline before any
// poll (restart baseline), a failed poll retains the previous snapshot without
// panicking (FR-008), and a subsequent good poll updates it.
func TestSourceErrorKeepsSnapshot(t *testing.T) {
	var mu sync.Mutex
	good := func() (map[string]time.Time, error) {
		return map[string]time.Time{"n": time.Now()}, nil
	}
	current := good
	src := func() (map[string]time.Time, error) {
		mu.Lock()
		f := current
		mu.Unlock()
		return f()
	}
	tr := New(src, time.Hour, quietLog()) // long interval; refresh driven manually

	// Restart baseline: nothing polled yet → everything offline, never seen.
	if tr.Online("n") {
		t.Error("fresh tracker should report offline before first poll")
	}
	if _, ok := tr.LastHandshake("n"); ok {
		t.Error("fresh tracker should report no last handshake before first poll")
	}

	// First successful poll → online.
	if err := tr.refresh(); err != nil {
		t.Fatalf("refresh good: %v", err)
	}
	if !tr.Online("n") {
		t.Fatal("node should be online after a successful poll")
	}

	// Source fails → refresh errors but the prior snapshot is retained, no panic.
	mu.Lock()
	current = func() (map[string]time.Time, error) { return nil, errors.New("device unreadable") }
	mu.Unlock()
	if err := tr.refresh(); err == nil {
		t.Error("refresh should surface the source error")
	}
	if !tr.Online("n") {
		t.Error("a failed poll must keep the previous snapshot (node still online)")
	}

	// Recovery: a good poll with different data updates the snapshot.
	mu.Lock()
	current = func() (map[string]time.Time, error) { return map[string]time.Time{"m": time.Now()}, nil }
	mu.Unlock()
	if err := tr.refresh(); err != nil {
		t.Fatalf("refresh recovery: %v", err)
	}
	if tr.Online("n") {
		t.Error("node 'n' should be offline after it left the snapshot")
	}
	if !tr.Online("m") {
		t.Error("node 'm' should be online after the recovery poll")
	}
}
