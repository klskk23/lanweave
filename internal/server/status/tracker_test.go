package status

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func quietLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// TestOnlineComputation pins the derivation rule against an injected clock and a
// fixed source: a recent handshake is online, an older-than-threshold handshake is
// offline (but still reports a last-seen time), a never-handshaked peer and an
// absent peer are both offline with no last-seen.
func TestOnlineComputation(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	const (
		recentKey = "recent"
		staleKey  = "stale"
		neverKey  = "never"
		absentKey = "absent"
	)
	src := func() (map[string]time.Time, error) {
		return map[string]time.Time{
			recentKey: now.Add(-1 * time.Minute), // within 3m → online
			staleKey:  now.Add(-5 * time.Minute), // older than 3m → offline, but seen
			neverKey:  {},                        // zero → never connected
		}, nil
	}
	tr := New(src, time.Minute, quietLog())
	tr.now = func() time.Time { return now }
	if err := tr.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	tests := []struct {
		key        string
		wantOnline bool
		wantSeen   bool
	}{
		{recentKey, true, true},
		{staleKey, false, true},
		{neverKey, false, false},
		{absentKey, false, false},
	}
	for _, tc := range tests {
		if got := tr.Online(tc.key); got != tc.wantOnline {
			t.Errorf("Online(%q) = %v, want %v", tc.key, got, tc.wantOnline)
		}
		ts, ok := tr.LastHandshake(tc.key)
		if ok != tc.wantSeen {
			t.Errorf("LastHandshake(%q) ok = %v, want %v", tc.key, ok, tc.wantSeen)
		}
		if ok && ts.IsZero() {
			t.Errorf("LastHandshake(%q) returned ok with zero time", tc.key)
		}
	}
}

// TestOnlineThresholdBoundary checks the strict-less-than cutoff: exactly at the
// threshold is offline, just under is online.
func TestOnlineThresholdBoundary(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	src := func() (map[string]time.Time, error) {
		return map[string]time.Time{
			"at":    now.Add(-Threshold),               // exactly 3m → offline
			"under": now.Add(-Threshold + time.Second), // 2m59s → online
		}, nil
	}
	tr := New(src, time.Minute, quietLog())
	tr.now = func() time.Time { return now }
	if err := tr.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tr.Online("at") {
		t.Error("handshake exactly at threshold should be offline")
	}
	if !tr.Online("under") {
		t.Error("handshake just under threshold should be online")
	}
}
