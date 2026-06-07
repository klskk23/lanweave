//go:build gui

package ui

import (
	"image/color"
	"testing"

	"lanweave/internal/client/i18n"
	"lanweave/internal/client/tunnel"
)

// A2 (US2): the status indicator is derived from THREE inputs (state, desired, connFailed) and
// MUST obey the data-model precedence — the auto-reconnect retry window (Disconnected && desired)
// shows yellow "connecting", never the red "failed" of a genuinely failed manual attempt; red
// appears only when the user is not trying to connect (!desired) and the last attempt failed;
// otherwise grey. Yellow desired-priority ignores connFailed entirely (FR-009/FR-013/FR-014).
func TestStatusViewThreeInputPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		state     tunnel.State
		desired   bool
		failed    bool
		wantColor color.Color
		wantText  string
		wantBtn   string
	}{
		{"retry window is yellow, not failed", tunnel.Disconnected, true, false,
			warningColor, i18n.T("status.connecting"), i18n.T("panel.disconnect")},
		{"retry window stays yellow even after a prior failure", tunnel.Disconnected, true, true,
			warningColor, i18n.T("status.connecting"), i18n.T("panel.disconnect")},
		{"failed manual connect is red", tunnel.Disconnected, false, true,
			dangerColor, i18n.T("status.failed"), i18n.T("panel.connect")},
		{"plain disconnected is grey", tunnel.Disconnected, false, false,
			textTertiary, i18n.T("status.disconnected"), i18n.T("panel.connect")},
		{"connected is green", tunnel.Connected, true, false,
			successColor, i18n.T("status.connected"), i18n.T("panel.disconnect")},
		{"connecting is yellow", tunnel.Connecting, false, false,
			warningColor, i18n.T("status.connecting"), i18n.T("panel.connect")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotColor, gotText := statusView(c.state, c.desired, c.failed)
			if gotColor != c.wantColor || gotText != c.wantText {
				t.Errorf("statusView(%v,%v,%v) = (%v,%q), want (%v,%q)",
					c.state, c.desired, c.failed, gotColor, gotText, c.wantColor, c.wantText)
			}
			if got := primaryActionLabel(c.state, c.desired); got != c.wantBtn {
				t.Errorf("primaryActionLabel(%v,%v) = %q, want %q", c.state, c.desired, got, c.wantBtn)
			}
		})
	}
}
