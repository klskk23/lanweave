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
	"lanweave/internal/client/i18n"
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
		status:      widget.NewLabel(i18n.T("panel.status", i18n.T("status.disconnected"))),
		insecureLbl: widget.NewLabelWithStyle(i18n.T("trust.notVerified"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		pinnedLbl:   widget.NewLabelWithStyle(i18n.T("trust.selfSignedNote"), fyne.TextAlignLeading, fyne.TextStyle{}),
		nodesBox:    container.NewVBox(),
		zonesBox:    container.NewVBox(),
	}
	content := p.build()
	go p.start()
	return content
}

func (p *Panel) build() fyne.CanvasObject {
	p.connBtn = widget.NewButton(i18n.T("panel.connect"), p.onConnect)
	p.connBtn.Importance = widget.HighImportance
	p.discBtn = widget.NewButton(i18n.T("panel.disconnect"), p.onDisconnect)

	top := container.NewVBox(
		widget.NewLabelWithStyle("lanweave", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel(i18n.T("panel.thisDevice", p.rec.NodeName, p.rec.IP)),
		container.NewHBox(p.status, p.connBtn, p.discBtn),
		widget.NewSeparator(),
	)

	createBtn := widget.NewButton(i18n.T("panel.createZone"), p.onCreateZone)
	joinBtn := widget.NewButton(i18n.T("panel.joinZone"), p.onJoinZone)
	zonesTab := container.NewVBox(container.NewHBox(createBtn, joinBtn), widget.NewSeparator(), p.zonesBox)

	tabs := container.NewAppTabs(
		container.NewTabItem(i18n.T("panel.tabNodes"), container.NewVScroll(p.nodesBox)),
		container.NewTabItem(i18n.T("panel.tabZones"), container.NewVScroll(zonesTab)),
	)

	// Firewall toggle (feature 018): opting in installs a host inbound-allow rule while connected,
	// letting same-subnet VPN peers reach this device. It is OFF by default and persisted (no confirm
	// dialog — enabling it is reversible and user-initiated). The handler persists the preference and
	// reconciles the rule against the current connection state (FR-012/014).
	fwCheck := widget.NewCheck(i18n.T("panel.firewallToggle", firewall.VPNSubnet), nil)
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
	logoutBtn := widget.NewButton(i18n.T("panel.logout"), p.confirmLogout)
	logoutBtn.Importance = widget.LowImportance
	// The language selector sits in the footer alongside the trust indicator and Log out (FR-003).
	bottom := container.NewVBox(
		widget.NewSeparator(),
		fwCheck,
		container.NewBorder(nil, nil, container.NewHBox(p.insecureLbl, p.pinnedLbl), logoutBtn),
		container.NewBorder(nil, nil, newLanguageSelect(p.win), nil),
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
	msg := i18n.T("panel.logoutConfirm", p.rec.NodeName, p.rec.ServerURL)
	dialog.ShowConfirm(i18n.T("panel.logout"), msg, func(ok bool) {
		if !ok {
			return
		}
		prog := dialog.NewCustomWithoutButtons(i18n.T("panel.loggingOut"), widget.NewProgressBarInfinite(), p.win)
		prog.Show()
		go func() {
			_ = p.tn.Disconnect()
			remoteRemoved, lerr := p.ctrl.Logout()
			fyne.Do(func() {
				prog.Hide()
				if lerr != nil {
					dialog.ShowInformation(i18n.T("panel.loggedOutTitle"), i18n.T("panel.logoutPartialFail"), p.win)
				} else if !remoteRemoved {
					dialog.ShowInformation(i18n.T("panel.loggedOutTitle"), i18n.T("panel.logoutRemoteLinger"), p.win)
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
		dialog.ShowConfirm(i18n.T("trust.changedTitle"),
			i18n.T("trust.changedBody", p.rec.ServerURL, fp), accept, p.win)
		return
	}
	dialog.ShowConfirm(i18n.T("trust.firstTitle"),
		i18n.T("trust.firstBody", p.rec.ServerURL, fp), accept, p.win)
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
	form := dialog.NewForm(i18n.T("panel.signIn"), i18n.T("panel.signIn"), i18n.T("btn.cancel"),
		[]*widget.FormItem{{Text: i18n.T("wizard.usernameLabel"), Widget: user}, {Text: i18n.T("wizard.passwordLabel"), Widget: pass}},
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
				label = "★ " + label + "  " + i18n.T("panel.thisMachineTag")
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
		title += "  " + i18n.T("panel.zoneOwnerTag")
	}
	buttons := container.NewHBox(
		widget.NewButton(i18n.T("panel.members"), func() { p.showMembers(z) }),
		widget.NewButton(i18n.T("panel.leave"), func() { p.confirmLeave(z) }),
	)
	if z.IsOwner {
		buttons.Add(widget.NewButton(i18n.T("panel.changePassword"), func() { p.changePassword(z) }))
		buttons.Add(widget.NewButton(i18n.T("panel.delete"), func() { p.confirmDelete(z) }))
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
				row := widget.NewLabel(i18n.T("panel.memberRow", m.NodeName, m.IP, m.Owner))
				if z.IsOwner && m.Owner != "" {
					id := m.NodeID
					name := m.NodeName
					kick := widget.NewButton(i18n.T("panel.kick"), func() { p.confirmKick(z, id, name) })
					box.Add(container.NewBorder(nil, nil, row, kick))
				} else {
					box.Add(row)
				}
			}
			dialog.ShowCustom(i18n.T("panel.membersTitle", z.Name), i18n.T("btn.close"), container.NewVScroll(box), p.win)
		})
	}()
}

func (p *Panel) onCreateZone() {
	name := widget.NewEntry()
	pass := widget.NewPasswordEntry()
	dialog.ShowForm(i18n.T("panel.createZone"), i18n.T("panel.create"), i18n.T("btn.cancel"),
		[]*widget.FormItem{{Text: i18n.T("panel.nameLabel"), Widget: name}, {Text: i18n.T("wizard.passwordLabel"), Widget: pass}},
		func(ok bool) {
			if ok {
				p.run(i18n.T("panel.creatingZone"), func() error { return p.ctrl.CreateZone(name.Text, pass.Text) })
			}
		}, p.win)
}

func (p *Panel) onJoinZone() {
	name := widget.NewEntry()
	pass := widget.NewPasswordEntry()
	dialog.ShowForm(i18n.T("panel.joinZone"), i18n.T("panel.join"), i18n.T("btn.cancel"),
		[]*widget.FormItem{{Text: i18n.T("panel.nameLabel"), Widget: name}, {Text: i18n.T("wizard.passwordLabel"), Widget: pass}},
		func(ok bool) {
			if ok {
				p.run(i18n.T("panel.joiningZone"), func() error { return p.ctrl.JoinZone(name.Text, pass.Text) })
			}
		}, p.win)
}

func (p *Panel) changePassword(z panel.ZoneView) {
	pass := widget.NewPasswordEntry()
	dialog.ShowForm(i18n.T("panel.changePwTitle", z.Name), i18n.T("panel.change"), i18n.T("btn.cancel"),
		[]*widget.FormItem{{Text: i18n.T("panel.newPasswordLabel"), Widget: pass}},
		func(ok bool) {
			if ok {
				p.run(i18n.T("panel.changingPw"), func() error { return p.ctrl.ChangePassword(z.Name, pass.Text) })
			}
		}, p.win)
}

func (p *Panel) confirmLeave(z panel.ZoneView) {
	dialog.ShowConfirm(i18n.T("panel.leaveTitle"), i18n.T("panel.leaveConfirm", z.Name), func(ok bool) {
		if ok {
			p.run(i18n.T("panel.leaving"), func() error { return p.ctrl.LeaveZone(z.Name) })
		}
	}, p.win)
}

func (p *Panel) confirmDelete(z panel.ZoneView) {
	dialog.ShowConfirm(i18n.T("panel.deleteTitle"), i18n.T("panel.deleteConfirm", z.Name), func(ok bool) {
		if ok {
			p.run(i18n.T("panel.deleting"), func() error { return p.ctrl.DeleteZone(z.Name) })
		}
	}, p.win)
}

func (p *Panel) confirmKick(z panel.ZoneView, nodeID int64, nodeName string) {
	dialog.ShowConfirm(i18n.T("panel.kickTitle"), i18n.T("panel.kickConfirm", nodeName, z.Name), func(ok bool) {
		if ok {
			p.run(i18n.T("panel.removing"), func() error { return p.ctrl.KickMember(z.Name, nodeID) })
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
	p.status.SetText(i18n.T("panel.status", i18n.T("status.connecting")))
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
	p.status.SetText(i18n.T("panel.status", i18n.T("status."+st.String())))
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
		return i18n.T("online.yes")
	}
	return i18n.T("online.no")
}

// panelMessage maps a management error to a plain-language message.
func panelMessage(err error) string {
	switch {
	case errors.Is(err, apiclient.ErrSessionExpired):
		return i18n.T("panel.errSessionExpired")
	case errors.Is(err, apiclient.ErrZoneNameTaken):
		return i18n.T("panel.errZoneNameTaken")
	case errors.Is(err, apiclient.ErrZoneOrPassword):
		return i18n.T("panel.errZoneOrPassword")
	case errors.Is(err, apiclient.ErrNotOwner):
		return i18n.T("panel.errNotOwner")
	case errors.Is(err, apiclient.ErrNotMember):
		return i18n.T("panel.errNotMember")
	case errors.Is(err, apiclient.ErrAuthFailed):
		return i18n.T("panel.errAuthFailed")
	case errors.Is(err, apiclient.ErrUnreachable):
		return i18n.T("panel.errUnreachable")
	case errors.Is(err, apiclient.ErrUntrustedCert):
		return i18n.T("panel.errUntrustedCert")
	default:
		return i18n.T("panel.errGeneric")
	}
}

// tunnelMessage maps a tunnel error to a plain-language message.
func tunnelMessage(err error) string {
	switch {
	case errors.Is(err, tunnel.ErrServerUnreachable):
		return i18n.T("tunnel.errUnreachable")
	case errors.Is(err, tunnel.ErrElevationDenied):
		return i18n.T("tunnel.errElevationDenied")
	case errors.Is(err, tunnel.ErrAdapter):
		return i18n.T("tunnel.errAdapter")
	case errors.Is(err, tunnel.ErrNoSetup):
		return i18n.T("tunnel.errNoSetup")
	default:
		return i18n.T("tunnel.errGeneric")
	}
}
