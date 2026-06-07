//go:build gui

package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2/test"

	"lanweave/internal/client/i18n"
	"lanweave/internal/client/tunnel"
)

// US1: the Hero shows a single primary button whose label tracks the tunnel state (FR-006).
func TestHeroSingleButtonByState(t *testing.T) {
	d := heroData{state: tunnel.Disconnected, deviceName: "GAME-PC", ip: "100.127.0.2", cidr: "100.127.0.0/16"}
	r := heroCard(d, func() {}, func(bool) {})
	bts := buttons(r.object)
	if len(bts) != 1 {
		t.Fatalf("disconnected Hero has %d buttons, want exactly 1 (no second action button)", len(bts))
	}
	if bts[0].Text != i18n.T("panel.connect") {
		t.Errorf("button label = %q, want %q", bts[0].Text, i18n.T("panel.connect"))
	}

	d.state = tunnel.Connected
	r2 := heroCard(d, func() {}, func(bool) {})
	b2 := buttons(r2.object)
	if len(b2) != 1 || b2[0].Text != i18n.T("panel.disconnect") {
		t.Errorf("connected Hero buttons = %d label = %q, want 1 / %q", len(b2), b2[0].Text, i18n.T("panel.disconnect"))
	}
}

// US1: the inbound Switch reflects FirewallAllowed and writes back through onToggle (FR-007).
func TestHeroSwitchReflectsAndWritesFirewall(t *testing.T) {
	var got *bool
	r := heroCard(heroData{state: tunnel.Disconnected, firewallOn: true}, func() {}, func(on bool) { got = &on })
	sw := switches(r.object)
	if len(sw) != 1 {
		t.Fatalf("Hero has %d switches, want 1", len(sw))
	}
	if !sw[0].On {
		t.Error("switch should reflect firewallOn=true")
	}
	w := test.NewWindow(r.object)
	defer w.Close()
	test.Tap(sw[0])
	if got == nil || *got != false {
		t.Errorf("tapping the on switch should call onToggle(false), got %v", got)
	}
}

func TestPrimaryActionAndStatusView(t *testing.T) {
	if primaryActionLabel(tunnel.Connected) != i18n.T("panel.disconnect") {
		t.Error("connected → Disconnect label")
	}
	if primaryActionLabel(tunnel.Disconnected) != i18n.T("panel.connect") {
		t.Error("disconnected → Connect label")
	}

	if c, s := statusView(tunnel.Connected, false); s != i18n.T("status.connected") || c != color.Color(successColor) {
		t.Errorf("connected status = (%v,%q)", c, s)
	}
	if c, s := statusView(tunnel.Connecting, false); s != i18n.T("status.connecting") || c != color.Color(warningColor) {
		t.Errorf("connecting status = (%v,%q)", c, s)
	}
	if c, s := statusView(tunnel.Disconnected, true); s != i18n.T("status.failed") || c != color.Color(dangerColor) {
		t.Errorf("failed status = (%v,%q), want failed/danger", c, s)
	}
	if c, s := statusView(tunnel.Disconnected, false); s != i18n.T("status.disconnected") || c != color.Color(textTertiary) {
		t.Errorf("disconnected status = (%v,%q)", c, s)
	}
}
