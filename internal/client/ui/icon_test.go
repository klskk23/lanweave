package ui_test

import (
	"bytes"
	"testing"

	"lanweave/internal/client/ui"
)

// pngSignature is the 8-byte header every PNG file begins with.
var pngSignature = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

func TestAppIcon(t *testing.T) {
	res := ui.AppIcon()
	if res == nil {
		t.Fatal("AppIcon() returned nil")
	}
	if got, want := res.Name(), "lanweave-icon"; got != want {
		t.Errorf("AppIcon().Name() = %q, want %q", got, want)
	}
	content := res.Content()
	if len(content) == 0 {
		t.Fatal("AppIcon().Content() is empty (icon.png not embedded?)")
	}
	if !bytes.HasPrefix(content, pngSignature) {
		n := min(8, len(content))
		t.Errorf("AppIcon().Content() is not a PNG; first bytes = % x", content[:n])
	}
}
