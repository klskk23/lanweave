// Package daemon owns the router tunnel's lifecycle: bring it up, watch the
// handshake, rebuild a stale session, retry failures forever, and tear down
// cleanly on shutdown. The thresholds deliberately mirror feature 028 (the
// Windows client's self-heal in internal/client/tunnel): a connected tunnel
// whose last handshake is older than staleAfter is rebuilt; checks run every
// checkEvery; retries never back off and never exit the process.
package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"lanweave/internal/router/engine"
)

const (
	// checkEvery is the health-check cadence (028: 15s).
	checkEvery = 15 * time.Second
	// staleAfter marks a connected tunnel as stale (028: 240s — well past the
	// 25s keepalive plus WireGuard's internal rekey budget).
	staleAfter = 240 * time.Second
)

// Engine is the tunnel seam: the production implementation is
// internal/router/engine (kernel WireGuard); tests use a fake. This is our own
// boundary, not a mocked system — the real engine has its own privileged
// integration tests.
type Engine interface {
	Up() error
	Down() error
	LastHandshake() (time.Time, error)
}

// Daemon runs the watch loop. Tick is overridable for tests (defaults to
// checkEvery).
type Daemon struct {
	Engine Engine
	Log    *slog.Logger
	Tick   time.Duration
	// OnUp, if set, fires after every successful tunnel bring-up (initial or
	// self-heal rebuild) — feature 033 hooks the route/NAT reconcile here so a
	// rebuilt tunnel regains its consumer routes immediately instead of
	// waiting out the reconcile period.
	OnUp func()
}

func (d *Daemon) tick() time.Duration {
	if d.Tick > 0 {
		return d.Tick
	}
	return checkEvery
}

// Run brings the tunnel up and watches it until ctx is cancelled, then tears it
// down. Failures (initial or rebuild) are retried every tick, forever — the
// router must converge without human help once connectivity returns.
func (d *Daemon) Run(ctx context.Context) error {
	up := d.tryUp()
	ticker := time.NewTicker(d.tick())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := d.Engine.Down(); err != nil {
				d.Log.Error("teardown failed", "error", err.Error())
				return err
			}
			d.Log.Info("tunnel torn down")
			return nil
		case <-ticker.C:
			if !up {
				up = d.tryUp()
				continue
			}
			if d.stale() {
				d.Log.Warn("handshake stale; rebuilding tunnel")
				if err := d.Engine.Down(); err != nil {
					d.Log.Error("rebuild teardown failed", "error", err.Error())
					continue // retry whole rebuild next tick
				}
				up = d.tryUp()
			}
		}
	}
}

func (d *Daemon) tryUp() bool {
	err := d.Engine.Up()
	if errors.Is(err, engine.ErrIfaceExists) {
		// A leftover interface from an unclean death (OOM kill, power loss
		// before procd's stop hook). The daemon is the interface's only owner,
		// so adopt-by-replace: tear the stale one down and bring up fresh.
		d.Log.Warn("stale tunnel interface found; replacing it")
		if derr := d.Engine.Down(); derr != nil {
			d.Log.Error("stale interface teardown failed; will retry", "error", derr.Error())
			return false
		}
		err = d.Engine.Up()
	}
	if err != nil {
		d.Log.Error("tunnel up failed; will retry", "error", err.Error())
		return false
	}
	d.Log.Info("tunnel up")
	if d.OnUp != nil {
		d.OnUp()
	}
	return true
}

// stale reports whether the current session needs a rebuild: it has handshaked
// at least once and the last handshake is older than staleAfter.
func (d *Daemon) stale() bool {
	hs, err := d.Engine.LastHandshake()
	if err != nil {
		d.Log.Error("handshake check failed", "error", err.Error())
		return false
	}
	if hs.IsZero() {
		return false // never connected this session; keep waiting
	}
	return time.Since(hs) > staleAfter
}
