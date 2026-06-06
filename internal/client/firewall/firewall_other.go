//go:build !windows

package firewall

// system returns a no-op Control on non-Windows platforms: the inbound-allow rule is a
// Windows-only enforcement, but the preference is still persisted and the UI is identical, so the
// decision logic and tests run unchanged off-Windows.
func system() Control { return noop{} }

type noop struct{}

func (noop) Allow() error { return nil }
func (noop) Clear() error { return nil }
