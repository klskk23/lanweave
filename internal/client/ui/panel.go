//go:build gui

package ui

import (
	"errors"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/firewall"
	"lanweave/internal/client/panel"
	"lanweave/internal/client/state"
	"lanweave/internal/client/tunnel"
)

// Panel is the main management view: top status with the connection switch, a "My nodes"
// tab and a "My zones" tab, create/join controls, and owner-only zone controls.
type Panel struct {
	win  fyne.Window
	rec  state.Record
	tn   *tunnel.Tunnel
	ctrl *panel.Controller

	// restart returns to the first setup step (server-URL entry) after a logout.
	restart func()

	status      *widget.Label
	connBtn     *widget.Button
	discBtn     *widget.Button
	insecureLbl *widget.Label // red banner: --insecure CLI flag (no verification)
	pinnedLbl   *widget.Label // neutral note: a self-signed cert is trusted (TOFU pin)
	nodesBox    *fyne.Container
	zonesBox    *fyne.Container
}

// NewPanel builds the panel. It validates the session (prompting a sign-in when needed),
// then loads the data and starts a periodic refresh. restart navigates back to the wizard's
// server-URL step after a logout.
func NewPanel(win fyne.Window, rec state.Record, tn *tunnel.Tunnel, ctrl *panel.Controller, restart func()) fyne.CanvasObject {
	p := &Panel{
		win: win, rec: rec, tn: tn, ctrl: ctrl, restart: restart,
		status:      widget.NewLabel("Status: disconnected"),
		insecureLbl: widget.NewLabelWithStyle("⚠ certificate not verified", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		pinnedLbl:   widget.NewLabelWithStyle("self-signed (trusted on this device)", fyne.TextAlignLeading, fyne.TextStyle{}),
		nodesBox:    container.NewVBox(),
		zonesBox:    container.NewVBox(),
	}
	content := p.build()
	go p.start()
	return content
}

func (p *Panel) build() fyne.CanvasObject {
	p.connBtn = widget.NewButton("Connect", p.onConnect)
	p.connBtn.Importance = widget.HighImportance
	p.discBtn = widget.NewButton("Disconnect", p.onDisconnect)

	top := container.NewVBox(
		widget.NewLabelWithStyle("lanweave", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("This device: "+p.rec.NodeName+"  ("+p.rec.IP+")"),
		container.NewHBox(p.status, p.connBtn, p.discBtn),
		widget.NewSeparator(),
	)

	createBtn := widget.NewButton("Create zone", p.onCreateZone)
	joinBtn := widget.NewButton("Join zone", p.onJoinZone)
	zonesTab := container.NewVBox(container.NewHBox(createBtn, joinBtn), widget.NewSeparator(), p.zonesBox)

	tabs := container.NewAppTabs(
		container.NewTabItem("My nodes", container.NewVScroll(p.nodesBox)),
		container.NewTabItem("My zones", container.NewVScroll(zonesTab)),
	)

	// Firewall toggle (feature 018): opting in installs a host inbound-allow rule while connected,
	// letting same-subnet VPN peers reach this device. It is OFF by default and persisted (no confirm
	// dialog — enabling it is reversible and user-initiated). The handler persists the preference and
	// reconciles the rule against the current connection state (FR-012/014).
	fwCheck := widget.NewCheck("Allow inbound from VPN peers ("+firewall.VPNSubnet+")", nil)
	fwCheck.SetChecked(p.ctrl.FirewallAllowed())
	fwCheck.OnChanged = func(on bool) {
		if err := p.ctrl.SetFirewallAllowed(on, p.tn.State() == tunnel.Connected); err != nil {
			dialog.ShowError(errors.New(panelMessage(err)), p.win)
		}
	}

	// Log out sits in a footer, deliberately away from the Connect/zone primary controls so
	// it isn't triggered by accident (FR-001). The footer also hosts the persistent trust
	// indicator: a red "certificate not verified" banner under --insecure, or a neutral
	// "trusted on this device" note when a self-signed certificate is pinned (FR-008/009).
	logoutBtn := widget.NewButton("Log out", p.confirmLogout)
	logoutBtn.Importance = widget.LowImportance
	bottom := container.NewVBox(
		widget.NewSeparator(),
		fwCheck,
		container.NewBorder(nil, nil, container.NewHBox(p.insecureLbl, p.pinnedLbl), logoutBtn),
	)
	p.refreshTrust()
	return container.NewBorder(top, bottom, nil, nil, tabs)
}

// refreshTrust shows the persistent trust indicator: the red "certificate not verified" banner
// while the session bypasses verification (--insecure), otherwise a neutral "trusted on this
// device" note when a self-signed certificate has been pinned (TOFU). Only one is ever visible.
func (p *Panel) refreshTrust() {
	switch {
	case p.ctrl.Insecure():
		p.insecureLbl.Show()
		p.pinnedLbl.Hide()
	case p.rec.PinnedCertSHA256 != "":
		p.insecureLbl.Hide()
		p.pinnedLbl.Show()
	default:
		p.insecureLbl.Hide()
		p.pinnedLbl.Hide()
	}
}

// confirmLogout names this device + server and the consequences (disconnect, remove this
// device's node, re-enter the server address), then on confirmation tears down the tunnel,
// runs the logout, warns if the remote node may linger, and returns to the setup wizard.
func (p *Panel) confirmLogout() {
	msg := fmt.Sprintf("Log out “%s” from %s?\n\nThis disconnects, removes this device's "+
		"registration on that server, and returns you to the server-address step where you can "+
		"sign in again.", p.rec.NodeName, p.rec.ServerURL)
	dialog.ShowConfirm("Log out", msg, func(ok bool) {
		if !ok {
			return
		}
		prog := dialog.NewCustomWithoutButtons("Logging out…", widget.NewProgressBarInfinite(), p.win)
		prog.Show()
		go func() {
			_ = p.tn.Disconnect()
			remoteRemoved, lerr := p.ctrl.Logout()
			fyne.Do(func() {
				prog.Hide()
				if lerr != nil {
					dialog.ShowInformation("Logged out", "You've been logged out, but clearing some local "+
						"data failed. You can finish setup again from the start.", p.win)
				} else if !remoteRemoved {
					dialog.ShowInformation("Logged out", "You've been logged out on this device. The server "+
						"couldn't be reached, so this device's registration may still exist there until it's "+
						"cleaned up.", p.win)
				}
				p.restart()
			})
		}()
	}, p.win)
}

// offerTrust handles a TOFU verification failure surfaced during a running session. A first-trust
// prompt names the server and shows the leaf fingerprint; a changed certificate gets a visibly
// heavier warning. On accept it persists the pin, rebuilds the controller's API client to trust it
// (re-applying the cached session token), updates the trust indicator, and runs onAccept (typically
// a retry of the failed operation). (FR-002/004/005)
func (p *Panel) offerTrust(ce *apiclient.CertError, onAccept func()) {
	fp := fingerprintDisplay(ce.Fingerprint)
	accept := func(ok bool) {
		if !ok {
			return
		}
		if err := p.ctrl.SetPinnedCert(ce.Fingerprint); err != nil {
			dialog.ShowError(errors.New(panelMessage(err)), p.win)
			return
		}
		p.rec.PinnedCertSHA256 = ce.Fingerprint
		p.ctrl.UseClient(apiclient.New(p.rec.ServerURL, apiclient.WithPinnedCert(ce.Fingerprint)))
		p.refreshTrust()
		onAccept()
	}
	if ce.Changed {
		dialog.ShowConfirm("⚠ Server certificate CHANGED",
			"The certificate presented by "+p.rec.ServerURL+" is DIFFERENT from the one you trusted "+
				"before. This can mean the server was reinstalled — or that someone is intercepting "+
				"your connection.\n\nNew fingerprint (SHA-256):\n"+fp+
				"\n\nTrust this new certificate and replace the saved one?",
			accept, p.win)
		return
	}
	dialog.ShowConfirm("Trust this server?",
		"The certificate for "+p.rec.ServerURL+" is self-signed and can't be checked against a public "+
			"authority. Verify the fingerprint with your administrator before trusting it.\n\n"+
			"Fingerprint (SHA-256):\n"+fp+
			"\n\nTrust this certificate on this device?",
		accept, p.win)
}

// start validates the session (prompting sign-in if needed), then refreshes and polls.
func (p *Panel) start() {
	need, err := p.ctrl.LoadSession()
	if err != nil {
		var ce *apiclient.CertError
		if errors.As(err, &ce) {
			fyne.Do(func() { p.offerTrust(ce, func() { go p.start() }) })
			return
		}
		fyne.Do(func() { dialog.ShowError(errors.New(panelMessage(err)), p.win) })
	}
	if need {
		fyne.Do(p.promptSignIn)
		return
	}
	p.refresh()
	go func() {
		for range time.Tick(15 * time.Second) {
			p.refreshConnection()
			p.refresh()
		}
	}()
}

func (p *Panel) promptSignIn() {
	user := widget.NewEntry()
	pass := widget.NewPasswordEntry()
	form := dialog.NewForm("Sign in", "Sign in", "Cancel",
		[]*widget.FormItem{{Text: "Username", Widget: user}, {Text: "Password", Widget: pass}},
		func(ok bool) {
			if !ok {
				return
			}
			go func() {
				err := p.ctrl.SignIn(user.Text, pass.Text)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(errors.New(panelMessage(err)), p.win)
						p.promptSignIn()
						return
					}
					p.refresh()
					go func() {
						for range time.Tick(15 * time.Second) {
							p.refreshConnection()
							p.refresh()
						}
					}()
				})
			}()
		}, p.win)
	form.Show()
}

// refresh reloads devices + zones from the server.
func (p *Panel) refresh() {
	devs, derr := p.ctrl.Devices()
	zones, zerr := p.ctrl.Zones()
	fyne.Do(func() {
		p.refreshConnection()
		if derr != nil {
			return
		}
		p.nodesBox.RemoveAll()
		for _, d := range devs {
			label := fmt.Sprintf("%s  %s  [%s]", d.Name, d.IP, onlineText(d.Online))
			if d.IsThisMachine {
				label = "★ " + label + "  (this machine)"
			}
			p.nodesBox.Add(widget.NewLabel(label))
		}
		p.nodesBox.Refresh()

		if zerr != nil {
			return
		}
		p.zonesBox.RemoveAll()
		for _, z := range zones {
			p.zonesBox.Add(p.zoneRow(z))
		}
		p.zonesBox.Refresh()
	})
}

func (p *Panel) zoneRow(z panel.ZoneView) fyne.CanvasObject {
	title := z.Name
	if z.IsOwner {
		title += "  (owner)"
	}
	buttons := container.NewHBox(
		widget.NewButton("Members", func() { p.showMembers(z) }),
		widget.NewButton("Leave", func() { p.confirmLeave(z) }),
	)
	if z.IsOwner {
		buttons.Add(widget.NewButton("Change password", func() { p.changePassword(z) }))
		buttons.Add(widget.NewButton("Delete", func() { p.confirmDelete(z) }))
	}
	return container.NewVBox(widget.NewLabel(title), buttons, widget.NewSeparator())
}

func (p *Panel) showMembers(z panel.ZoneView) {
	go func() {
		members, err := p.ctrl.Members(z.Name)
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(errors.New(panelMessage(err)), p.win)
				return
			}
			box := container.NewVBox()
			for _, m := range members {
				row := widget.NewLabel(fmt.Sprintf("%s  %s  (owner: %s)", m.NodeName, m.IP, m.Owner))
				if z.IsOwner && m.Owner != "" {
					id := m.NodeID
					name := m.NodeName
					kick := widget.NewButton("Kick", func() { p.confirmKick(z, id, name) })
					box.Add(container.NewBorder(nil, nil, row, kick))
				} else {
					box.Add(row)
				}
			}
			dialog.ShowCustom("Members of "+z.Name, "Close", container.NewVScroll(box), p.win)
		})
	}()
}

func (p *Panel) onCreateZone() {
	name := widget.NewEntry()
	pass := widget.NewPasswordEntry()
	dialog.ShowForm("Create zone", "Create", "Cancel",
		[]*widget.FormItem{{Text: "Name", Widget: name}, {Text: "Password", Widget: pass}},
		func(ok bool) {
			if ok {
				p.run("Creating zone…", func() error { return p.ctrl.CreateZone(name.Text, pass.Text) })
			}
		}, p.win)
}

func (p *Panel) onJoinZone() {
	name := widget.NewEntry()
	pass := widget.NewPasswordEntry()
	dialog.ShowForm("Join zone", "Join", "Cancel",
		[]*widget.FormItem{{Text: "Name", Widget: name}, {Text: "Password", Widget: pass}},
		func(ok bool) {
			if ok {
				p.run("Joining zone…", func() error { return p.ctrl.JoinZone(name.Text, pass.Text) })
			}
		}, p.win)
}

func (p *Panel) changePassword(z panel.ZoneView) {
	pass := widget.NewPasswordEntry()
	dialog.ShowForm("Change password for "+z.Name, "Change", "Cancel",
		[]*widget.FormItem{{Text: "New password", Widget: pass}},
		func(ok bool) {
			if ok {
				p.run("Changing password…", func() error { return p.ctrl.ChangePassword(z.Name, pass.Text) })
			}
		}, p.win)
}

func (p *Panel) confirmLeave(z panel.ZoneView) {
	dialog.ShowConfirm("Leave zone", "Leave zone “"+z.Name+"”?", func(ok bool) {
		if ok {
			p.run("Leaving…", func() error { return p.ctrl.LeaveZone(z.Name) })
		}
	}, p.win)
}

func (p *Panel) confirmDelete(z panel.ZoneView) {
	dialog.ShowConfirm("Delete zone", "Delete zone “"+z.Name+"” for everyone? This cannot be undone.", func(ok bool) {
		if ok {
			p.run("Deleting…", func() error { return p.ctrl.DeleteZone(z.Name) })
		}
	}, p.win)
}

func (p *Panel) confirmKick(z panel.ZoneView, nodeID int64, nodeName string) {
	dialog.ShowConfirm("Remove member", "Remove “"+nodeName+"” from zone “"+z.Name+"”?", func(ok bool) {
		if ok {
			p.run("Removing…", func() error { return p.ctrl.KickMember(z.Name, nodeID) })
		}
	}, p.win)
}

// run executes an operation with a progress dialog, then refreshes; errors are shown.
func (p *Panel) run(msg string, op func() error) {
	prog := dialog.NewCustomWithoutButtons(msg, widget.NewProgressBarInfinite(), p.win)
	prog.Show()
	go func() {
		err := op()
		fyne.Do(func() {
			prog.Hide()
			if err != nil {
				var ce *apiclient.CertError
				if errors.As(err, &ce) {
					p.offerTrust(ce, func() { p.run(msg, op) })
					return
				}
				dialog.ShowError(errors.New(panelMessage(err)), p.win)
				return
			}
			p.refresh()
		})
	}()
}

func (p *Panel) onConnect() {
	p.connBtn.Disable()
	p.status.SetText("Status: connecting…")
	go func() {
		err := p.tn.Connect()
		if err == nil {
			// Now connected: install the host rule iff the user has opted in (best-effort).
			_ = p.ctrl.ReconcileFirewall(true)
		}
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(errors.New(tunnelMessage(err)), p.win)
			}
			p.refreshConnection()
		})
	}()
}

func (p *Panel) onDisconnect() {
	_ = p.tn.Disconnect()
	_ = p.ctrl.ReconcileFirewall(false) // no tunnel → never leave the host rule open
	p.refreshConnection()
}

func (p *Panel) refreshConnection() {
	st := p.tn.State()
	p.status.SetText("Status: " + st.String())
	switch st {
	case tunnel.Connected:
		p.connBtn.Disable()
		p.discBtn.Enable()
	case tunnel.Connecting:
		p.connBtn.Disable()
		p.discBtn.Disable()
	default:
		p.connBtn.Enable()
		p.discBtn.Disable()
	}
}

func onlineText(online bool) string {
	if online {
		return "online"
	}
	return "offline"
}

// panelMessage maps a management error to a plain-language message.
func panelMessage(err error) string {
	switch {
	case errors.Is(err, apiclient.ErrSessionExpired):
		return "Your session expired — please sign in again."
	case errors.Is(err, apiclient.ErrZoneNameTaken):
		return "A zone with that name already exists."
	case errors.Is(err, apiclient.ErrZoneOrPassword):
		return "Wrong zone name or password."
	case errors.Is(err, apiclient.ErrNotOwner):
		return "Only the zone owner can do that."
	case errors.Is(err, apiclient.ErrNotMember):
		return "This device isn't a member of that zone."
	case errors.Is(err, apiclient.ErrAuthFailed):
		return "Sign-in failed — check your username and password."
	case errors.Is(err, apiclient.ErrUnreachable):
		return "Couldn't reach the server — check your connection."
	case errors.Is(err, apiclient.ErrUntrustedCert):
		return "The server's certificate isn't trusted."
	default:
		return "Something went wrong. Please try again."
	}
}

// tunnelMessage maps a tunnel error to a plain-language message.
func tunnelMessage(err error) string {
	switch {
	case errors.Is(err, tunnel.ErrServerUnreachable):
		return "Couldn't reach the server — check your connection and try again."
	case errors.Is(err, tunnel.ErrElevationDenied):
		return "lanweave needs administrator rights to create the network adapter."
	case errors.Is(err, tunnel.ErrAdapter):
		return "Couldn't set up the network adapter."
	case errors.Is(err, tunnel.ErrNoSetup):
		return "This device isn't set up yet."
	default:
		return "Couldn't connect. Please try again."
	}
}
