//go:build gui

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

func TestSwitchTogglesAndFires(t *testing.T) {
	var last bool
	calls := 0
	s := NewSwitch(func(on bool) { last = on; calls++ })
	w := test.NewWindow(s)
	defer w.Close()

	if s.On {
		t.Fatal("switch should start off")
	}
	test.Tap(s)
	if !s.On || !last || calls != 1 {
		t.Fatalf("after tap: On=%v cb=%v calls=%d, want On=true cb=true calls=1", s.On, last, calls)
	}
	test.Tap(s)
	if s.On || last || calls != 2 {
		t.Fatalf("after second tap: On=%v cb=%v calls=%d, want off", s.On, last, calls)
	}

	// SetOn seeds state without firing OnChange.
	s.SetOn(true)
	if !s.On {
		t.Error("SetOn(true) should set On")
	}
	if calls != 2 {
		t.Errorf("SetOn fired OnChange (calls=%d), want it silent", calls)
	}
}

func TestStatusWidgetsRender(t *testing.T) {
	si := statusIndicator("已连接", successColor)
	chip := makeChip("本机", brandCyanChipBg, brandCyanChipText)
	av := makeAvatar("Game-PC", true)
	avOffline := makeAvatar("hyperv", false)

	w := test.NewWindow(container.NewVBox(si, chip, av, avOffline))
	defer w.Close()

	for name, o := range map[string]fyne.CanvasObject{"status": si, "chip": chip, "avatar": av} {
		sz := o.MinSize()
		if sz.Width <= 0 || sz.Height <= 0 {
			t.Errorf("%s has empty min size %v", name, sz)
		}
	}
}
