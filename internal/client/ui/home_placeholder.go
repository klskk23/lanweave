//go:build gui

package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"lanweave/internal/client/state"
)

// NewHome returns the placeholder home area shown once the device is set up. The full
// management panel (devices, zones, the connection toggle) arrives in later features.
func NewHome(rec state.Record) fyne.CanvasObject {
	return container.NewVBox(
		widget.NewLabelWithStyle("lanweave", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel(fmt.Sprintf("Device:  %s", rec.NodeName)),
		widget.NewLabel(fmt.Sprintf("Address: %s", rec.IP)),
		widget.NewLabel(fmt.Sprintf("Server:  %s", rec.ServerURL)),
		widget.NewSeparator(),
		widget.NewLabel("Setup complete. The connection panel arrives in a later update."),
	)
}
