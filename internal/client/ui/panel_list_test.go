//go:build gui

package ui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"lanweave/internal/client/i18n"
	"lanweave/internal/client/panel"
)

// US3: the this-machine node row carries the "this machine" chip, a highlight background, and is
// inert (nodes have no detail) (FR-009).
func TestNodeRowThisMachine(t *testing.T) {
	row := buildNodeRow(panel.DeviceView{Name: "GAME-PC", IP: "100.127.0.2", Online: true, IsThisMachine: true}, time.Now())
	if !contains(texts(row), i18n.T("panel.thisMachineTag")) {
		t.Error("this-machine row should include the chip")
	}
	tr, ok := row.(*tapRow)
	if !ok {
		t.Fatalf("node row is %T, want *tapRow", row)
	}
	if tr.bg == nil {
		t.Error("this-machine row should be highlighted")
	}
	if tr.onTap != nil {
		t.Error("node rows must be inert (no detail)")
	}
}

// US3: an offline node appends the relative offline time and dims to textTertiary (FR-009).
func TestNodeRowOfflineDimAndRelativeTime(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seen := now.Add(-3 * time.Minute).Format(time.RFC3339)
	row := buildNodeRow(panel.DeviceView{Name: "hyperv", IP: "100.127.0.3", Online: false, LastSeen: seen}, now)

	want := offlineSince(seen, now)
	joined := strings.Join(texts(row), "|")
	if !strings.Contains(joined, want) {
		t.Errorf("offline row %q should contain relative time %q", joined, want)
	}

	var dimmed bool
	walk(row, func(c fyne.CanvasObject) {
		if tx, ok := c.(*canvas.Text); ok && strings.Contains(tx.Text, want) {
			if tx.Color == color.Color(textTertiary) {
				dimmed = true
			}
		}
	})
	if !dimmed {
		t.Error("offline subtitle should use textTertiary")
	}
}

// US3: a zone row taps through to the detail callback and shows the owner chip when owned (FR-010).
func TestZoneRowTapsAndOwnerChip(t *testing.T) {
	tapped := false
	row := buildZoneRow(panel.ZoneView{Name: "home", IsOwner: true}, func() { tapped = true })
	if !contains(texts(row), i18n.T("panel.zoneOwnerTag")) {
		t.Error("owner zone row should show the owner chip")
	}
	tr, ok := row.(*tapRow)
	if !ok {
		t.Fatalf("zone row is %T, want *tapRow", row)
	}
	if tr.onTap == nil {
		t.Fatal("zone row must be tappable")
	}
	tr.onTap()
	if !tapped {
		t.Error("tapping a zone row should open its detail")
	}

	plain := buildZoneRow(panel.ZoneView{Name: "guest", IsOwner: false}, func() {})
	if contains(texts(plain), i18n.T("panel.zoneOwnerTag")) {
		t.Error("non-owner zone row should not show the owner chip")
	}
}
