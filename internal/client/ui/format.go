package ui

import (
	"fmt"
	"time"

	"lanweave/internal/client/i18n"
)

// offlineSince renders an offline node's subtitle suffix. With a parseable RFC 3339 LastSeen it
// returns the localized "N minutes ago offline" (clamped to at least 1 minute); with an empty
// or unparseable value it falls back to a plain "offline" (FR-009). It is a pure function (now
// is passed in) so the relative-time formatting is unit-testable.
func offlineSince(lastSeen string, now time.Time) string {
	if lastSeen == "" {
		return i18n.T("online.no")
	}
	ts, err := time.Parse(time.RFC3339, lastSeen)
	if err != nil {
		return i18n.T("online.no")
	}
	mins := max(int(now.Sub(ts).Minutes()), 1)
	return i18n.T("panel.offlineSince", mins)
}

// formatBytes renders a cumulative byte count as a compact human-readable string (B/KB/MB/GB…),
// using 1024-based units, for the Hero card's ↑/↓ traffic display (FR-012). Pure function.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}
