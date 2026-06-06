package ui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

// icon.png is the 256x256 brand mark generated from packaging/icon.svg by `make icons`.
// This file is intentionally untagged (no "gui" build tag): it imports only the cgo-free
// fyne.io/fyne/v2 root package, so it — and its test — build on headless hosts.
//
//go:embed icon.png
var iconPNG []byte

// AppIcon returns the lanweave brand icon for use as the Fyne application/window icon.
func AppIcon() fyne.Resource {
	return fyne.NewStaticResource("lanweave-icon", iconPNG)
}
