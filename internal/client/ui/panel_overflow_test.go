//go:build gui

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"lanweave/internal/client/i18n"
)

// US2: the overflow trust item tracks the session's certificate state, and the menu always has a
// language selector + a Log out at the bottom (FR-003/004).
func TestOverflowTrustItems(t *testing.T) {
	w := test.NewWindow(widget.NewLabel(""))
	defer w.Close()

	notVerified := i18n.T("trust.notVerified")
	selfSigned := i18n.T("trust.selfSignedNote")
	logout := i18n.T("panel.logout")

	insecure := texts(overflowContent(true, "", w, func() {}))
	if !contains(insecure, notVerified) {
		t.Errorf("insecure overflow missing red %q", notVerified)
	}
	if contains(insecure, selfSigned) {
		t.Errorf("insecure overflow should not show the pinned note")
	}
	if !contains(insecure, logout) {
		t.Errorf("overflow must contain Log out")
	}

	pinned := texts(overflowContent(false, "ABC123", w, func() {}))
	if !contains(pinned, selfSigned) {
		t.Errorf("pinned overflow missing neutral %q", selfSigned)
	}
	if contains(pinned, notVerified) {
		t.Errorf("pinned overflow should not show the red not-verified note")
	}

	systemCA := texts(overflowContent(false, "", w, func() {}))
	if contains(systemCA, selfSigned) || contains(systemCA, notVerified) {
		t.Errorf("system-CA overflow should show no trust item, got %v", systemCA)
	}
	if !contains(systemCA, logout) {
		t.Errorf("system-CA overflow must still contain Log out")
	}
}

func TestOverflowLogoutFires(t *testing.T) {
	w := test.NewWindow(widget.NewLabel(""))
	defer w.Close()
	fired := false
	box := overflowContent(false, "", w, func() { fired = true })

	var row *tapRow
	walk(box, func(c fyne.CanvasObject) {
		if r, ok := c.(*tapRow); ok {
			row = r
		}
	})
	if row == nil {
		t.Fatal("overflow has no tappable logout row")
	}
	row.onTap()
	if !fired {
		t.Error("tapping logout should invoke the callback")
	}
}

func TestTrustStateMapping(t *testing.T) {
	if trustState(true, "") != trustInsecure {
		t.Error("insecure wins")
	}
	if trustState(false, "fp") != trustPinned {
		t.Error("pinned when a cert is stored")
	}
	if trustState(false, "") != trustNone {
		t.Error("system CA → no item")
	}
}
