//go:build gui

package ui

import (
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/firewall"
	"lanweave/internal/client/i18n"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/onboard"
	"lanweave/internal/client/panel"
	"lanweave/internal/client/state"
	"lanweave/internal/client/tunnel"
)

// Wizard drives the first-run setup screens, binding the UI to the Fyne-free onboarding
// controller. Every step offers Back and Cancel, network actions show progress, errors are
// human-readable, and there is deliberately no control to weaken certificate verification.
type Wizard struct {
	win          fyne.Window
	statePath    string
	keys         keyring.Store
	insecure     bool // the --insecure CLI value; bypasses verification entirely
	origInsecure bool // the original --insecure CLI value; used to seed a post-logout restart

	// pinnedCert is the TOFU leaf-certificate fingerprint the user trusted for this server in
	// the current wizard run; threaded into the API client and persisted with the state record.
	pinnedCert string

	serverURL string
	mode      onboard.AuthMode
	invite    string
	username  string
	password  string
	nodeName  string
}

// NewWizard builds a wizard. The insecure flag comes only from the command line, never the
// UI; it is threaded into the API client's TLS settings.
func NewWizard(win fyne.Window, statePath string, keys keyring.Store, insecure bool) *Wizard {
	return &Wizard{win: win, statePath: statePath, keys: keys, insecure: insecure, origInsecure: insecure, mode: onboard.SignIn}
}

// Start shows the first step.
func (z *Wizard) Start() { z.stepServer() }

// render lays out a step with a title, body, a Back/Cancel pair, and a primary action,
// and wires Enter (confirm) / Escape (back or cancel).
func (z *Wizard) render(title string, body fyne.CanvasObject, onBack, onNext func(), nextLabel string, focus fyne.Focusable) {
	header := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	cancel := widget.NewButton(i18n.T("btn.cancel"), z.cancel)
	var left fyne.CanvasObject = cancel
	if onBack != nil {
		left = container.NewHBox(widget.NewButton(i18n.T("btn.back"), onBack), cancel)
	}
	next := widget.NewButton(nextLabel, onNext)
	next.Importance = widget.HighImportance
	bar := container.NewBorder(nil, nil, left, next)

	// Persistent trust indicator: a severe "not verified" banner under --insecure (no
	// verification at all), or a neutral "trusted on this device" note once a self-signed
	// certificate has been pinned via TOFU (FR-008/009).
	var topObj fyne.CanvasObject = header
	switch {
	case z.insecure:
		warn := widget.NewLabelWithStyle(i18n.T("trust.notVerified"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		topObj = container.NewVBox(warn, header)
	case z.pinnedCert != "":
		note := widget.NewLabelWithStyle(i18n.T("trust.selfSignedNote"), fyne.TextAlignLeading, fyne.TextStyle{})
		topObj = container.NewVBox(note, header)
	}
	// The language selector sits at the very top of every wizard step (FR-003). The step body is
	// wrapped in the same rounded dark card as the panel's Hero for visual consistency (feature
	// 022); the four-step flow and Back/Cancel/Next logic are unchanged.
	top := container.NewVBox(newLanguageSelect(z.win), topObj)
	z.win.SetContent(container.NewBorder(top, bar, nil, nil, card(container.NewVBox(body))))
	z.win.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		switch e.Name {
		case fyne.KeyEscape:
			if onBack != nil {
				onBack()
			} else {
				z.cancel()
			}
		case fyne.KeyReturn, fyne.KeyEnter:
			if onNext != nil {
				onNext()
			}
		}
	})
	if focus != nil {
		z.win.Canvas().Focus(focus)
	}
}

func (z *Wizard) stepServer() {
	url := widget.NewEntry()
	url.SetPlaceHolder("https://vpn.example.com")
	url.SetText(z.serverURL)
	errLbl := widget.NewLabel("")
	body := container.NewVBox(widget.NewLabel(i18n.T("wizard.serverPrompt")), url, errLbl)

	z.render(i18n.T("wizard.serverTitle"), body, nil, func() {
		if url.Text == "" {
			errLbl.SetText(i18n.T("wizard.serverRequired"))
			return
		}
		z.serverURL = url.Text
		z.stepAuth()
	}, i18n.T("btn.next"), url)
}

func (z *Wizard) stepAuth() {
	user := widget.NewEntry()
	user.SetPlaceHolder(i18n.T("wizard.usernamePlaceholder"))
	user.SetText(z.username)
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder(i18n.T("wizard.passwordPlaceholder"))

	invite := widget.NewEntry()
	invite.SetPlaceHolder(i18n.T("wizard.invitePlaceholder"))
	inviteRow := container.NewVBox(widget.NewLabel(i18n.T("wizard.inviteLabel")), invite)
	if z.mode == onboard.SignIn {
		inviteRow.Hide()
	}

	// The radio options are localized for display; selection is compared against the same
	// localized strings, so the labels never have to match an English literal.
	signInLabel, createLabel := i18n.T("wizard.signIn"), i18n.T("wizard.createAccount")
	mode := widget.NewRadioGroup([]string{signInLabel, createLabel}, func(s string) {
		if s == createLabel {
			z.mode = onboard.CreateAccount
			inviteRow.Show()
		} else {
			z.mode = onboard.SignIn
			inviteRow.Hide()
		}
	})
	if z.mode == onboard.CreateAccount {
		mode.SetSelected(createLabel)
	} else {
		mode.SetSelected(signInLabel)
	}

	errLbl := widget.NewLabel("")
	body := container.NewVBox(mode, inviteRow, widget.NewLabel(i18n.T("wizard.usernameLabel")), user, widget.NewLabel(i18n.T("wizard.passwordLabel")), pass, errLbl)

	z.render(i18n.T("wizard.accountTitle"), body, z.stepServer, func() {
		if user.Text == "" || pass.Text == "" {
			errLbl.SetText(i18n.T("wizard.credsRequired"))
			return
		}
		if z.mode == onboard.CreateAccount && invite.Text == "" {
			errLbl.SetText(i18n.T("wizard.inviteRequired"))
			return
		}
		z.username, z.password, z.invite = user.Text, pass.Text, invite.Text
		z.stepName()
	}, i18n.T("btn.next"), user)
}

func (z *Wizard) stepName() {
	name := widget.NewEntry()
	name.SetPlaceHolder(i18n.T("wizard.devicePlaceholder"))
	name.SetText(z.nodeName)
	errLbl := widget.NewLabel("")
	body := container.NewVBox(widget.NewLabel(i18n.T("wizard.devicePrompt")), name, errLbl)

	z.render(i18n.T("wizard.deviceTitle"), body, z.stepAuth, func() {
		if name.Text == "" {
			errLbl.SetText(i18n.T("wizard.deviceRequired"))
			return
		}
		z.nodeName = name.Text
		z.runProvision()
	}, i18n.T("btn.finish"), name)
}

// runProvision performs the network setup with a progress indicator and routes back to the
// relevant step on a recoverable error.
func (z *Wizard) runProvision() {
	z.win.SetContent(container.NewVBox(
		widget.NewLabel(i18n.T("wizard.settingUp")),
		widget.NewProgressBarInfinite(),
	))

	var opts []apiclient.Option
	switch {
	case z.insecure:
		opts = append(opts, apiclient.WithInsecure())
	case z.pinnedCert != "":
		opts = append(opts, apiclient.WithPinnedCert(z.pinnedCert))
	}
	p := &onboard.Provisioner{
		API: apiclient.New(z.serverURL, opts...), Keys: z.keys, StatePath: z.statePath, ServerURL: z.serverURL,
		PinnedCertSHA256: z.pinnedCert,
	}
	creds := onboard.Credentials{Mode: z.mode, Invite: z.invite, Username: z.username, Password: z.password}

	go func() {
		rec, err := p.Provision(creds, z.nodeName)
		fyne.Do(func() {
			if err != nil {
				// Trust-on-first-use: a verification failure surfaces a *CertError carrying the
				// leaf fingerprint. Prompt once (first-trust) or with a heavier warning (changed
				// cert); accepting persists the pin and re-runs. Not reached under --insecure
				// (verification is bypassed entirely). (FR-001..006)
				var ce *apiclient.CertError
				if errors.As(err, &ce) {
					z.offerTrust(ce)
					return
				}
				dialog.ShowError(errors.New(friendly(err)), z.win)
				switch {
				case errors.Is(err, apiclient.ErrNodeNameTaken), errors.Is(err, apiclient.ErrDeviceLimitReached):
					z.stepName()
				case errors.Is(err, apiclient.ErrAuthFailed), errors.Is(err, apiclient.ErrInviteInvalid), errors.Is(err, apiclient.ErrUsernameTaken):
					z.stepAuth()
				default:
					z.stepServer()
				}
				return
			}
			z.showHome(rec)
		})
	}()
}

// offerTrust handles a TOFU verification failure during onboarding. A first-trust prompt names
// the server and shows the leaf fingerprint; a changed certificate gets a visibly heavier warning.
// Accepting pins the presented fingerprint and re-runs provisioning; declining returns to the
// server step. (FR-002/004/005)
func (z *Wizard) offerTrust(ce *apiclient.CertError) {
	fp := fingerprintDisplay(ce.Fingerprint)
	accept := func(ok bool) {
		if !ok {
			z.stepServer()
			return
		}
		z.pinnedCert = ce.Fingerprint
		z.runProvision()
	}
	if ce.Changed {
		dialog.ShowConfirm(i18n.T("trust.changedTitle"),
			i18n.T("trust.changedBody", z.serverURL, fp), accept, z.win)
		return
	}
	dialog.ShowConfirm(i18n.T("trust.firstTitle"),
		i18n.T("trust.firstBody", z.serverURL, fp), accept, z.win)
}

// fingerprintDisplay groups a lowercase hex fingerprint into colon-separated byte pairs so it can
// be read aloud and compared against the server's certificate.
func fingerprintDisplay(fp string) string {
	var b strings.Builder
	for i := 0; i < len(fp); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		end := i + 2
		if end > len(fp) {
			end = len(fp)
		}
		b.WriteString(fp[i:end])
	}
	return b.String()
}

// showHome builds the tunnel + management controller from the freshly stored key + record
// and shows the main panel.
func (z *Wizard) showHome(rec state.Record) {
	var tn *tunnel.Tunnel
	if priv, err := z.keys.Get(keyring.DeviceKeyName); err == nil {
		tn = tunnel.New(rec, string(priv))
	} else {
		tn = tunnel.New(rec, "") // panel still renders; Connect will report ErrNoSetup
	}
	var opts []apiclient.Option
	switch {
	case z.insecure:
		opts = append(opts, apiclient.WithInsecure())
	case rec.PinnedCertSHA256 != "":
		opts = append(opts, apiclient.WithPinnedCert(rec.PinnedCertSHA256))
	}
	ctrl := panel.New(apiclient.New(rec.ServerURL, opts...), rec, z.keys, z.statePath, z.insecure, firewall.System())
	z.win.SetContent(NewPanel(z.win, rec, tn, ctrl, func() {
		NewWizard(z.win, z.statePath, z.keys, z.origInsecure).Start()
	}))
}

// cancel discards any partial setup (vault key + state record) and returns to the start.
func (z *Wizard) cancel() {
	_ = (&onboard.Provisioner{Keys: z.keys, StatePath: z.statePath}).Cleanup()
	z.serverURL, z.username, z.password, z.invite, z.nodeName = "", "", "", "", ""
	z.mode = onboard.SignIn
	z.stepServer()
}

// friendly maps a typed error to a plain-language, actionable message (FR-012).
func friendly(err error) string {
	switch {
	case errors.Is(err, apiclient.ErrAuthFailed):
		return i18n.T("wizard.errAuthFailed")
	case errors.Is(err, apiclient.ErrInviteInvalid):
		return i18n.T("wizard.errInviteInvalid")
	case errors.Is(err, apiclient.ErrUsernameTaken):
		return i18n.T("wizard.errUsernameTaken")
	case errors.Is(err, apiclient.ErrNodeNameTaken):
		return i18n.T("wizard.errNodeNameTaken")
	case errors.Is(err, apiclient.ErrDeviceLimitReached):
		return i18n.T("wizard.errDeviceLimit")
	case errors.Is(err, apiclient.ErrUnreachable):
		return i18n.T("wizard.errUnreachable")
	case errors.Is(err, apiclient.ErrUntrustedCert):
		return i18n.T("wizard.errUntrustedCert")
	case errors.Is(err, apiclient.ErrPoolExhausted):
		return i18n.T("wizard.errPoolExhausted")
	default:
		return i18n.T("wizard.errGeneric")
	}
}
