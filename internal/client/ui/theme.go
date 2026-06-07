//go:build gui

package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Design tokens from docs/UI-DESIGN.md §2/§3/§6. They are package-level so the self-drawn
// controls (switch.go, widgets.go) and the panel/wizard layouts share one source of truth
// instead of redefining colors per file.
var (
	brandIndigo       = color.NRGBA{R: 0x1A, G: 0x1B, B: 0x3A, A: 0xFF} // button/track fill
	brandCyan         = color.NRGBA{R: 0x06, G: 0xD9, B: 0xD5, A: 0xFF} // primary action, tab indicator
	brandCyanFaded    = color.NRGBA{R: 0x06, G: 0xD9, B: 0xD5, A: 0x10} // ~6% — selected/this-machine row bg
	brandCyanChipBg   = color.NRGBA{R: 0x06, G: 0xD9, B: 0xD5, A: 0x1F} // ~12% — chip background
	brandCyanChipText = color.NRGBA{R: 0x0F, G: 0x6E, B: 0x56, A: 0xFF} // chip text

	surfaceBase = color.NRGBA{R: 0x12, G: 0x13, B: 0x1A, A: 0xFF} // app background
	surfaceA    = color.NRGBA{R: 0x1C, G: 0x1D, B: 0x28, A: 0xFF} // primary card
	surfaceB    = color.NRGBA{R: 0x22, G: 0x23, B: 0x2E, A: 0xFF} // Hero/secondary card
	avatarBg    = color.NRGBA{R: 0x2E, G: 0x2F, B: 0x3C, A: 0xFF} // avatar circle fill

	textPrimary   = color.NRGBA{R: 0xE6, G: 0xE7, B: 0xEA, A: 0xFF}
	textSecondary = color.NRGBA{R: 0x9C, G: 0xA0, B: 0xAB, A: 0xFF}
	textTertiary  = color.NRGBA{R: 0x6A, G: 0x6E, B: 0x7A, A: 0xFF} // offline / hints

	successColor = color.NRGBA{R: 0x4A, G: 0xDE, B: 0x80, A: 0xFF} // online / connected
	warningColor = color.NRGBA{R: 0xFA, G: 0xC7, B: 0x75, A: 0xFF} // connecting
	dangerColor  = color.NRGBA{R: 0xE2, G: 0x4B, B: 0x4A, A: 0xFF} // failed / not-verified
	dividerColor = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0x14} // ~8% white, 0.5px divider
)

// lanweaveTheme is the forced-dark, flat brand theme. It embeds the Fyne default theme and
// overrides only the brand-relevant colors and sizes; any name it does not override falls
// back to the default theme's *dark* variant, ignoring the system light/dark setting so the
// app is always dark (FR-001).
type lanweaveTheme struct{ fyne.Theme }

// NewTheme returns the lanweave desktop theme to install via app.Settings().SetTheme.
func NewTheme() fyne.Theme { return &lanweaveTheme{Theme: theme.DefaultTheme()} }

// Color overrides the brand palette and forces VariantDark for everything else, so the passed
// variant is intentionally ignored (the app never follows the system light theme).
func (t *lanweaveTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary:
		return brandCyan
	case theme.ColorNameBackground:
		return surfaceBase
	case theme.ColorNameInputBackground:
		return surfaceB
	case theme.ColorNameForeground:
		return textPrimary
	case theme.ColorNamePlaceHolder:
		return textTertiary
	case theme.ColorNameSuccess:
		return successColor
	case theme.ColorNameError:
		return dangerColor
	case theme.ColorNameSeparator:
		return dividerColor
	}
	return t.Theme.Color(name, theme.VariantDark)
}

// Size tightens padding and sets the brand type scale (UI-DESIGN §3/§6).
func (t *lanweaveTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 12
	case theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameInputBorder:
		return 0.5
	case theme.SizeNameInputRadius:
		return 8
	case theme.SizeNameSelectionRadius:
		return 8
	case theme.SizeNameText:
		return 14
	case theme.SizeNameCaptionText:
		return 12
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameHeadingText:
		return 18
	}
	return t.Theme.Size(name)
}

// statusColor maps online state to the avatar status-dot / list color (UI-DESIGN §4).
func statusColor(online bool) color.Color {
	if online {
		return successColor
	}
	return textTertiary
}
