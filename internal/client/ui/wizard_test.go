//go:build gui

package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"lanweave/internal/client/i18n"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/onboard"
)

// entryByPlaceholder finds the (first) text entry whose placeholder matches, so a test can
// fill a specific wizard field without depending on layout order.
func entryByPlaceholder(o fyne.CanvasObject, ph string) *widget.Entry {
	var found *widget.Entry
	walk(o, func(c fyne.CanvasObject) {
		if e, ok := c.(*widget.Entry); ok && e.PlaceHolder == ph && found == nil {
			found = e
		}
	})
	return found
}

// tapButton invokes the tap handler of the (first) button with the given label.
func tapButton(o fyne.CanvasObject, label string) bool {
	for _, b := range buttons(o) {
		if b.Text == label && b.OnTapped != nil {
			b.OnTapped()
			return true
		}
	}
	return false
}

// visibleLabel returns the (first) currently-visible label with the given text, or nil.
func visibleLabel(o fyne.CanvasObject, text string) *widget.Label {
	var found *widget.Label
	walk(o, func(c fyne.CanvasObject) {
		if l, ok := c.(*widget.Label); ok && l.Text == text && l.Visible() && found == nil {
			found = l
		}
	})
	return found
}

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

// US2 (T008): in create-account mode the wizard blocks a non-compliant password with the
// reason-specific localized message and does not advance; a compliant password advances; and
// sign-in mode accepts any password unchanged (the policy applies only at account creation).
func TestWizardCreateAccountEnforcesPolicy(t *testing.T) {
	w := test.NewWindow(nil)
	defer w.Close()
	wz := NewWizard(w, filepath.Join(t.TempDir(), "state.json"), keyring.NewFake(), false)

	wz.mode = onboard.CreateAccount
	wz.stepAuth()
	c := w.Content()
	entryByPlaceholder(c, i18n.T("wizard.usernamePlaceholder")).SetText("alice")
	entryByPlaceholder(c, i18n.T("wizard.invitePlaceholder")).SetText("invite-code")
	pass := entryByPlaceholder(c, i18n.T("wizard.passwordPlaceholder"))

	// Non-compliant (no uppercase) → blocked with the no_upper message, no advance.
	pass.SetText("aa345678")
	if !tapButton(c, i18n.T("btn.next")) {
		t.Fatal("Next button not found")
	}
	if !contains(texts(w.Content()), i18n.T("wizard.pwRule.no_upper")) {
		t.Errorf("expected no_upper message, have %v", texts(w.Content()))
	}
	if wz.password != "" {
		t.Errorf("wizard advanced despite a non-compliant password (password=%q)", wz.password)
	}

	// Compliant → advances (stepName captures the password).
	pass.SetText("Aa345678")
	tapButton(w.Content(), i18n.T("btn.next"))
	if wz.password != "Aa345678" {
		t.Errorf("compliant password did not advance: password=%q", wz.password)
	}

	// Sign-in mode is unaffected: a weak password still advances.
	wz2 := NewWizard(w, filepath.Join(t.TempDir(), "state2.json"), keyring.NewFake(), false)
	wz2.mode = onboard.SignIn
	wz2.stepAuth()
	c2 := w.Content()
	entryByPlaceholder(c2, i18n.T("wizard.usernamePlaceholder")).SetText("bob")
	entryByPlaceholder(c2, i18n.T("wizard.passwordPlaceholder")).SetText("weak")
	tapButton(c2, i18n.T("btn.next"))
	if wz2.password != "weak" {
		t.Errorf("sign-in must not enforce the policy: password=%q", wz2.password)
	}
}

// US3 (T011): a persistent rule hint is visible under the password field in create-account
// mode before any input, and it is hidden in sign-in mode.
func TestWizardShowsRuleHint(t *testing.T) {
	w := test.NewWindow(nil)
	defer w.Close()
	wz := NewWizard(w, filepath.Join(t.TempDir(), "state.json"), keyring.NewFake(), false)

	wz.mode = onboard.CreateAccount
	wz.stepAuth()
	if visibleLabel(w.Content(), i18n.T("wizard.pwRule.hint")) == nil {
		t.Errorf("rule hint not visible in create-account step; texts=%v", texts(w.Content()))
	}

	wz.mode = onboard.SignIn
	wz.stepAuth()
	if visibleLabel(w.Content(), i18n.T("wizard.pwRule.hint")) != nil {
		t.Error("rule hint should be hidden in sign-in mode")
	}
}
