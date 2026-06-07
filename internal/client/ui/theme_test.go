//go:build gui

package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2/theme"
)

// The theme must render dark regardless of the system light/dark setting (FR-001): overridden
// colors ignore the requested variant, and non-overridden names fall back to the default dark.
func TestThemeForcesDark(t *testing.T) {
	th := NewTheme()

	if got := th.Color(theme.ColorNameBackground, theme.VariantLight); got != color.Color(surfaceBase) {
		t.Errorf("Background under light variant = %v, want forced-dark surfaceBase %v", got, surfaceBase)
	}
	if got := th.Color(theme.ColorNamePrimary, theme.VariantLight); got != color.Color(brandCyan) {
		t.Errorf("Primary = %v, want brandCyan %v", got, brandCyan)
	}

	// A name we do not override must resolve identically for both variants — proof the theme
	// ignores the system variant and always uses the dark fallback.
	light := th.Color(theme.ColorNameScrollBar, theme.VariantLight)
	dark := th.Color(theme.ColorNameScrollBar, theme.VariantDark)
	if light != dark {
		t.Errorf("non-overridden color differs by variant (light=%v dark=%v); theme must force dark", light, dark)
	}
}
