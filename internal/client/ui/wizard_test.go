//go:build gui

package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"

	"lanweave/internal/client/i18n"
	"lanweave/internal/client/keyring"
)

// US5: each wizard step renders under the brand theme without panicking, and the localized
// Back/Cancel/Next labels are present — proof the four-step flow is intact after re-skinning.
func TestWizardRendersSteps(t *testing.T) {
	w := test.NewWindow(nil)
	defer w.Close()

	statePath := filepath.Join(t.TempDir(), "state.json")
	wz := NewWizard(w, statePath, keyring.NewFake(), false)
	wz.Start()
	if w.Content() == nil {
		t.Fatal("wizard step 1 did not render")
	}

	// Walk forward to the account step (Back appears once past step 1).
	wz.stepAuth()
	labels := texts(w.Content())
	for _, want := range []string{i18n.T("btn.back"), i18n.T("btn.cancel"), i18n.T("btn.next")} {
		if !contains(labels, want) {
			t.Errorf("account step missing control %q (have %v)", want, labels)
		}
	}

	// Cancel resets to step 1 without a Back control.
	wz.cancel()
	if l := texts(w.Content()); contains(l, i18n.T("btn.back")) {
		t.Errorf("step 1 should have no Back control, got %v", l)
	}
}
