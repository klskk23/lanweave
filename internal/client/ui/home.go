//go:build gui

package ui

import (
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lanweave/internal/client/state"
	"lanweave/internal/client/tunnel"
)

// NewHome returns the home area for a set-up device: device details, an always-visible
// connection status, and a Connect / Disconnect control bound to the tunnel.
func NewHome(win fyne.Window, rec state.Record, tn *tunnel.Tunnel) fyne.CanvasObject {
	status := widget.NewLabel("")
	var connectBtn, disconnectBtn *widget.Button

	refresh := func() {
		st := tn.State()
		status.SetText("Status: " + st.String())
		switch st {
		case tunnel.Connected:
			connectBtn.Disable()
			disconnectBtn.Enable()
		case tunnel.Connecting:
			connectBtn.Disable()
			disconnectBtn.Disable()
		default:
			connectBtn.Enable()
			disconnectBtn.Disable()
		}
	}

	connectBtn = widget.NewButton("Connect", func() {
		status.SetText("Status: connecting…")
		connectBtn.Disable()
		go func() {
			err := tn.Connect()
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(errors.New(tunnelMessage(err)), win)
				}
				refresh()
			})
		}()
	})
	connectBtn.Importance = widget.HighImportance

	disconnectBtn = widget.NewButton("Disconnect", func() {
		_ = tn.Disconnect()
		refresh()
	})

	refresh()
	// Keep the shown status honest with the real tunnel state.
	go func() {
		for range time.Tick(time.Second) {
			fyne.Do(refresh)
		}
	}()

	return container.NewVBox(
		widget.NewLabelWithStyle("lanweave", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("Device:  "+rec.NodeName),
		widget.NewLabel("Address: "+rec.IP),
		status,
		container.NewHBox(connectBtn, disconnectBtn),
	)
}

// tunnelMessage maps a tunnel error to a plain-language message.
func tunnelMessage(err error) string {
	switch {
	case errors.Is(err, tunnel.ErrServerUnreachable):
		return "Couldn't reach the server — check your connection and try again."
	case errors.Is(err, tunnel.ErrElevationDenied):
		return "lanweave needs administrator rights to create the network adapter."
	case errors.Is(err, tunnel.ErrAdapter):
		return "Couldn't set up the network adapter."
	case errors.Is(err, tunnel.ErrNoSetup):
		return "This device isn't set up yet."
	default:
		return "Couldn't connect. Please try again."
	}
}
