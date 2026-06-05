// Package status tracks per-node online state derived from the live WireGuard
// device. It is a read-through cache of the kernel's per-peer last-handshake
// times, refreshed on a fixed interval; it persists nothing and is rebuilt from
// the device on every poll, so it survives a restart without carrying stale state.
package status

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Threshold is the maximum age of a peer's last handshake for it to count as
// online (DESIGN §6.5).
const Threshold = 3 * time.Minute

// DefaultInterval is how often the tracker re-reads handshake times. It equals
// the constitution's "online-status update lag ≤ 30 s" budget (Principle IV).
const DefaultInterval = 30 * time.Second

// Tracker holds the most recent per-peer handshake snapshot and refreshes it from
// a source (the WireGuard device) on a fixed interval.
type Tracker struct {
	mu        sync.RWMutex
	snapshot  map[string]time.Time
	source    func() (map[string]time.Time, error)
	interval  time.Duration
	threshold time.Duration
	now       func() time.Time
	log       *slog.Logger
}

// New returns a tracker that reads handshake times from source every interval.
// The online threshold defaults to Threshold and the clock to time.Now.
func New(source func() (map[string]time.Time, error), interval time.Duration, log *slog.Logger) *Tracker {
	return &Tracker{
		snapshot:  map[string]time.Time{},
		source:    source,
		interval:  interval,
		threshold: Threshold,
		now:       time.Now,
		log:       log,
	}
}

// refresh reads the source once and replaces the snapshot. On error the previous
// snapshot is left intact (the caller decides whether to log).
func (t *Tracker) refresh() error {
	snap, err := t.source()
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.snapshot = snap
	t.mu.Unlock()
	return nil
}

// Run polls the source immediately, then every interval, until ctx is cancelled.
// A failed poll keeps the previous snapshot and is logged at WARN; Run never
// panics and never blocks the server, so an unreadable tunnel degrades to all
// nodes reporting offline rather than failing requests.
func (t *Tracker) Run(ctx context.Context) {
	if err := t.refresh(); err != nil {
		t.log.Warn("online-status refresh failed", "error", err.Error())
	}
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.refresh(); err != nil {
				t.log.Warn("online-status refresh failed", "error", err.Error())
			}
		}
	}
}

// Online reports whether the node with the given public key handshaked within the
// threshold. An absent key or a zero (never-handshaked) time is offline.
func (t *Tracker) Online(pubKey string) bool {
	t.mu.RLock()
	ts, ok := t.snapshot[pubKey]
	t.mu.RUnlock()
	if !ok || ts.IsZero() {
		return false
	}
	return t.now().Sub(ts) < t.threshold
}

// LastHandshake returns the node's most recent handshake time. ok is false when
// the node is absent from the snapshot or has never handshaked (zero time).
func (t *Tracker) LastHandshake(pubKey string) (time.Time, bool) {
	t.mu.RLock()
	ts, ok := t.snapshot[pubKey]
	t.mu.RUnlock()
	if !ok || ts.IsZero() {
		return time.Time{}, false
	}
	return ts, true
}
