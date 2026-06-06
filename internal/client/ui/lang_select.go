//go:build gui

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lanweave/internal/client/i18n"
)

// langPrefKey is where the UI-language preference lives in Fyne Preferences. It is read at
// startup by main before any view is built; storing it here (not in the state record) keeps it
// available during the very first wizard run, before any state file exists.
const langPrefKey = "ui.language"

// newLanguageSelect builds the shared three-option language selector ("Follow system /
// English / 中文") shown in both the wizard and the panel. Choosing a concrete language stores
// the preference (so it overrides the system locale next launch); choosing "Follow system"
// clears it. The change applies on the next launch, so this shows a restart notice rather than
// re-rendering the live view (FR-006); selecting the already-active option is a Fyne no-op.
func newLanguageSelect(win fyne.Window) *widget.Select {
	prefs := fyne.CurrentApp().Preferences()
	keys := i18n.LabelKeys()
	opts := make([]string, len(keys))
	for i, k := range keys {
		opts[i] = i18n.T(k)
	}
	sel := widget.NewSelect(opts, nil)
	// Reflect the stored preference before wiring OnChanged so this initial set is silent.
	sel.SetSelectedIndex(i18n.IndexForPref(prefs.StringWithFallback(langPrefKey, "")))
	sel.OnChanged = func(string) {
		if v := i18n.PrefForIndex(sel.SelectedIndex()); v == "" {
			prefs.RemoveValue(langPrefKey)
		} else {
			prefs.SetString(langPrefKey, v)
		}
		dialog.ShowInformation(i18n.T("lang.title"), i18n.T("lang.restartNotice"), win)
	}
	return sel
}
