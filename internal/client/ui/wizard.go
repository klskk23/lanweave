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

	cancel := widget.NewButton("Cancel", z.cancel)
	var left fyne.CanvasObject = cancel
	if onBack != nil {
		left = container.NewHBox(widget.NewButton("Back", onBack), cancel)
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
		warn := widget.NewLabelWithStyle("⚠ certificate not verified", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		topObj = container.NewVBox(warn, header)
	case z.pinnedCert != "":
		note := widget.NewLabelWithStyle("self-signed (trusted on this device)", fyne.TextAlignLeading, fyne.TextStyle{})
		topObj = container.NewVBox(note, header)
	}
	z.win.SetContent(container.NewBorder(topObj, bar, nil, nil, container.NewVBox(body)))
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
	body := container.NewVBox(widget.NewLabel("Enter your lanweave server address."), url, errLbl)

	z.render("Server", body, nil, func() {
		if url.Text == "" {
			errLbl.SetText("Please enter the server address.")
			return
		}
		z.serverURL = url.Text
		z.stepAuth()
	}, "Next", url)
}

func (z *Wizard) stepAuth() {
	user := widget.NewEntry()
	user.SetPlaceHolder("username")
	user.SetText(z.username)
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder("password")

	invite := widget.NewEntry()
	invite.SetPlaceHolder("invite code")
	inviteRow := container.NewVBox(widget.NewLabel("Invite code"), invite)
	if z.mode == onboard.SignIn {
		inviteRow.Hide()
	}

	mode := widget.NewRadioGroup([]string{"Sign in", "Create account"}, func(s string) {
		if s == "Create account" {
			z.mode = onboard.CreateAccount
			inviteRow.Show()
		} else {
			z.mode = onboard.SignIn
			inviteRow.Hide()
		}
	})
	if z.mode == onboard.CreateAccount {
		mode.SetSelected("Create account")
	} else {
		mode.SetSelected("Sign in")
	}

	errLbl := widget.NewLabel("")
	body := container.NewVBox(mode, inviteRow, widget.NewLabel("Username"), user, widget.NewLabel("Password"), pass, errLbl)

	z.render("Account", body, z.stepServer, func() {
		if user.Text == "" || pass.Text == "" {
			errLbl.SetText("Username and password are required.")
			return
		}
		if z.mode == onboard.CreateAccount && invite.Text == "" {
			errLbl.SetText("An invite code is required to create an account.")
			return
		}
		z.username, z.password, z.invite = user.Text, pass.Text, invite.Text
		z.stepName()
	}, "Next", user)
}

func (z *Wizard) stepName() {
	name := widget.NewEntry()
	name.SetPlaceHolder("e.g. my-laptop")
	name.SetText(z.nodeName)
	errLbl := widget.NewLabel("")
	body := container.NewVBox(widget.NewLabel("Name this device."), name, errLbl)

	z.render("Device name", body, z.stepAuth, func() {
		if name.Text == "" {
			errLbl.SetText("Please name this device.")
			return
		}
		z.nodeName = name.Text
		z.runProvision()
	}, "Finish", name)
}

// runProvision performs the network setup with a progress indicator and routes back to the
// relevant step on a recoverable error.
func (z *Wizard) runProvision() {
	z.win.SetContent(container.NewVBox(
		widget.NewLabel("Setting up this device…"),
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
				case errors.Is(err, apiclient.ErrNodeNameTaken):
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
		dialog.ShowConfirm("⚠ Server certificate CHANGED",
			"The certificate presented by "+z.serverURL+" is DIFFERENT from the one you trusted "+
				"before. This can mean the server was reinstalled — or that someone is intercepting "+
				"your connection.\n\nNew fingerprint (SHA-256):\n"+fp+
				"\n\nTrust this new certificate and replace the saved one?",
			accept, z.win)
		return
	}
	dialog.ShowConfirm("Trust this server?",
		"The certificate for "+z.serverURL+" is self-signed and can't be checked against a public "+
			"authority. Verify the fingerprint with your administrator before trusting it.\n\n"+
			"Fingerprint (SHA-256):\n"+fp+
			"\n\nTrust this certificate on this device?",
		accept, z.win)
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
		return "Sign-in failed — check your username and password."
	case errors.Is(err, apiclient.ErrInviteInvalid):
		return "That invite code is invalid or has already been used."
	case errors.Is(err, apiclient.ErrUsernameTaken):
		return "That username is already taken."
	case errors.Is(err, apiclient.ErrNodeNameTaken):
		return "You already have a device with that name — please choose another."
	case errors.Is(err, apiclient.ErrUnreachable):
		return "Can't reach the server — check the address and your network connection."
	case errors.Is(err, apiclient.ErrUntrustedCert):
		return "The server's certificate isn't trusted. Ask your administrator to install the root certificate."
	case errors.Is(err, apiclient.ErrPoolExhausted):
		return "The server has no free addresses available right now."
	default:
		return "Something went wrong during setup. Please try again."
	}
}
