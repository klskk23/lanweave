package ui

import (
	"testing"
	"time"
)

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024*1024 + 512*1024, "1.5 MB"},
		{3 * 1024 * 1024 * 1024, "3.0 GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOfflineSince(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	// Empty / unparseable → plain "offline" (no relative time).
	if got := offlineSince("", now); got == "" {
		t.Error("offlineSince(empty) should return a non-empty offline label")
	}
	if got := offlineSince("not-a-time", now); got == "" {
		t.Error("offlineSince(garbage) should fall back to a non-empty offline label")
	}

	// A parseable timestamp yields a relative-minutes label distinct from the plain fallback.
	seen := now.Add(-3 * time.Minute).Format(time.RFC3339)
	rel := offlineSince(seen, now)
	if rel == offlineSince("", now) {
		t.Errorf("offlineSince(3 min ago) = %q, should differ from the plain offline label", rel)
	}

	// Sub-minute differences clamp to the same bucket as 1 minute (no "0 minutes ago").
	recent := now.Add(-10 * time.Second).Format(time.RFC3339)
	if offlineSince(recent, now) != offlineSince(now.Add(-1*time.Minute).Format(time.RFC3339), now) {
		t.Error("sub-minute offline should clamp to the 1-minute label")
	}
}
