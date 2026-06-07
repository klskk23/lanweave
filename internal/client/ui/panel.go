//go:build gui

package ui

import (
	"errors"
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/firewall"
	"lanweave/internal/client/i18n"
	"lanweave/internal/client/panel"
	"lanweave/internal/client/state"
	"lanweave/internal/client/tunnel"
)

// Panel is the main management view (feature 022 redesign): an App Bar with an overflow menu, a
// Hero card for this device (status + single connect button + inbound Switch + live traffic),
// and Nodes/Zones tabs with a FAB. The controller and tunnel are unchanged; only where their
// methods are triggered and how their data is presented changed.
type Panel struct {
	win     fyne.Window
	rec     state.Record
	tn      *tunnel.Tunnel
	ctrl    *panel.Controller
	restart func() // returns to the wizard's server-URL step after a logout

	// Hero references kept for in-place updates on state/traffic change.
	statusDot   *canvas.Circle
	statusText  *canvas.Text
	trafficText *canvas.Text
	heroBtn     *widget.Button
	fwSwitch    *Switch

	connFailed  bool          // last Connect attempt failed → red "connection failed"
	trafficQuit chan struct{} // stops the 2s traffic poller; nil when not polling

	menuBtn  *widget.Button
	nodesBox *fyne.Container
	zonesBox *fyne.Container
	nodesTab *container.TabItem
	zonesTab *container.TabItem
	tabs     *container.AppTabs
}

// NewPanel builds the panel, validates the session (prompting sign-in when needed), then loads
// data and starts periodic refresh. restart navigates back to the wizard after a logout.
func NewPanel(win fyne.Window, rec state.Record, tn *tunnel.Tunnel, ctrl *panel.Controller, restart func()) fyne.CanvasObject {
	p := &Panel{win: win, rec: rec, tn: tn, ctrl: ctrl, restart: restart}
	content := p.build()
	go p.start()
	return content
}

func (p *Panel) build() fyne.CanvasObject {
	p.nodesBox = container.NewVBox()
	p.zonesBox = container.NewVBox()
	p.nodesTab = container.NewTabItemWithIcon(i18n.T("panel.tabNodes"), theme.ComputerIcon(), container.NewVScroll(p.nodesBox))
	p.zonesTab = container.NewTabItemWithIcon(i18n.T("panel.tabZones"), theme.FolderIcon(), container.NewVScroll(p.zonesBox))
	p.tabs = container.NewAppTabs(p.nodesTab, p.zonesTab)
	p.tabs.SetTabLocation(container.TabLocationTop)

	// The "+" FAB floats over the bottom-right of the tab content (zone create/join).
	body := container.NewStack(p.tabs, container.NewPadded(bottomRight(p.buildFAB())))

	top := container.NewVBox(p.buildAppBar(), container.NewPadded(p.buildHero()))
	return container.NewBorder(top, nil, nil, nil, body)
}

// buildAppBar renders the 48px top bar: left-aligned logo + "lanweave", a right-aligned ⋮
// overflow button, and a 0.5px bottom divider — never a centered title (FR-002).
func (p *Panel) buildAppBar() fyne.CanvasObject {
	logo := canvas.NewImageFromResource(AppIcon())
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(24, 24))
	name := canvas.NewText("lanweave", textPrimary)
	name.TextSize = 16
	left := container.NewHBox(
		container.NewCenter(container.NewGridWrap(fyne.NewSize(24, 24), logo)),
		container.NewCenter(name),
	)
	p.menuBtn = widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), p.showOverflow)
	p.menuBtn.Importance = widget.LowImportance
	bar := container.NewBorder(nil, nil, left, container.NewCenter(p.menuBtn))

	divider := canvas.NewRectangle(dividerColor)
	divider.SetMinSize(fyne.NewSize(0, 0.5))
	return container.NewVBox(container.NewPadded(bar), divider)
}

// showOverflow pops the custom overflow menu anchored under the ⋮ button (language submenu,
// trust note, and a red Log out at the bottom) (FR-003/004).
func (p *Panel) showOverflow() {
	var pop *widget.PopUp
	dismiss := func() {
		if pop != nil {
			pop.Hide()
		}
	}
	box := overflowContent(p.ctrl.Insecure(), p.rec.PinnedCertSHA256, p.win, func() {
		dismiss()
		p.confirmLogout()
	})
	pop = widget.NewPopUp(box, p.win.Canvas())
	anchor := fyne.CurrentApp().Driver().AbsolutePositionForObject(p.menuBtn)
	x := anchor.X + p.menuBtn.Size().Width - box.MinSize().Width
	if x < 0 {
		x = 0
	}
	pop.ShowAtPosition(fyne.NewPos(x, anchor.Y+p.menuBtn.Size().Height))
}

// trustKind is the session's certificate-trust state, which decides the overflow trust item.
type trustKind int

const (
	trustNone trustKind = iota
	trustPinned
	trustInsecure
)

func trustState(insecure bool, pinned string) trustKind {
	switch {
	case insecure:
		return trustInsecure
	case pinned != "":
		return trustPinned
	default:
		return trustNone
	}
}

// overflowContent builds the overflow menu body: a language selector, an optional trust note
// (red "not verified" under --insecure, neutral "trusted on this device" when a self-signed
// cert is pinned, nothing under a system-CA cert), and a red Log out pinned at the bottom. Pure
// inputs + callbacks so it is unit-testable without a live controller (FR-003/004).
func overflowContent(insecure bool, pinned string, win fyne.Window, onLogout func()) *fyne.Container {
	rows := container.NewVBox(
		widget.NewLabel(i18n.T("lang.title")),
		newLanguageSelect(win),
	)
	switch trustState(insecure, pinned) {
	case trustInsecure:
		rows.Add(coloredText(i18n.T("trust.notVerified"), dangerColor))
	case trustPinned:
		rows.Add(coloredText(i18n.T("trust.selfSignedNote"), textSecondary))
	}
	rows.Add(widget.NewSeparator())
	rows.Add(newTapRow(container.NewPadded(coloredText(i18n.T("panel.logout"), dangerColor)), nil, onLogout))
	return rows
}

// --- Hero card ------------------------------------------------------------------------------

// heroData is the immutable input to heroCard; the Panel fills it from the tunnel/controller.
type heroData struct {
	state       tunnel.State
	failed      bool
	deviceName  string
	ip          string
	cidr        string
	firewallOn  bool
	showTraffic bool
	up, down    int64 // cumulative bytes: up = tx, down = rx
}

// heroRefs are the mutable widgets the Panel updates in place (status line, button, switch).
type heroRefs struct {
	object     fyne.CanvasObject
	button     *widget.Button
	toggle     *Switch
	statusDot  *canvas.Circle
	statusText *canvas.Text
	traffic    *canvas.Text
}

// primaryActionLabel is the single Hero button's label for a tunnel state: Disconnect when
// connected, otherwise Connect (FR-006). Pure function for unit testing.
func primaryActionLabel(st tunnel.State) string {
	if st == tunnel.Connected {
		return i18n.T("panel.disconnect")
	}
	return i18n.T("panel.connect")
}

// statusView maps a tunnel state (plus a transient failed flag) to the status dot/text color
// and label (FR-005). Pure function for unit testing.
func statusView(st tunnel.State, failed bool) (color.Color, string) {
	if failed && st == tunnel.Disconnected {
		return dangerColor, i18n.T("status.failed")
	}
	switch st {
	case tunnel.Connected:
		return successColor, i18n.T("status.connected")
	case tunnel.Connecting:
		return warningColor, i18n.T("status.connecting")
	default:
		return textTertiary, i18n.T("status.disconnected")
	}
}

func trafficString(up, down int64) string {
	return fmt.Sprintf("%s %s · %s %s",
		i18n.T("traffic.up"), formatBytes(up), i18n.T("traffic.down"), formatBytes(down))
}

// heroCard builds the Hero card from heroData, wiring the single primary button to onPrimary
// and the inbound Switch to onToggle. Returns the widgets the Panel updates live.
func heroCard(d heroData, onPrimary func(), onToggle func(bool)) heroRefs {
	dotColor, statusStr := statusView(d.state, d.failed)
	dot := canvas.NewCircle(dotColor)
	statusText := coloredText(statusStr, dotColor)

	traffic := canvas.NewText("", textSecondary)
	traffic.TextSize = 12
	if d.showTraffic {
		traffic.Text = trafficString(d.up, d.down)
	}
	statusRow := container.NewBorder(nil, nil,
		container.NewHBox(
			container.NewCenter(container.NewGridWrap(fyne.NewSize(9, 9), dot)),
			container.NewCenter(statusText),
		),
		container.NewCenter(traffic),
	)

	name := canvas.NewText(d.deviceName, textPrimary)
	name.TextSize = 18
	ip := canvas.NewText(d.ip, textSecondary)
	ip.TextSize = 13
	ip.TextStyle = fyne.TextStyle{Monospace: true}

	btn := widget.NewButton(primaryActionLabel(d.state), onPrimary)
	btn.Importance = widget.HighImportance
	if d.state == tunnel.Connecting {
		btn.Disable()
	}

	sw := NewSwitch(onToggle)
	sw.SetOn(d.firewallOn)
	inboundTitle := canvas.NewText(i18n.T("panel.allowInbound"), textPrimary)
	inboundTitle.TextSize = 14
	cidr := canvas.NewText(d.cidr, textSecondary)
	cidr.TextSize = 13
	cidr.TextStyle = fyne.TextStyle{Monospace: true}
	inboundRow := container.NewBorder(nil, nil, nil, container.NewCenter(sw),
		container.NewVBox(inboundTitle, cidr))

	inner := container.NewVBox(statusRow, name, ip, btn, widget.NewSeparator(), inboundRow)
	bg := canvas.NewRectangle(surfaceB)
	bg.CornerRadius = 12
	return heroRefs{
		object:     container.NewStack(bg, container.NewPadded(inner)),
		button:     btn,
		toggle:     sw,
		statusDot:  dot,
		statusText: statusText,
		traffic:    traffic,
	}
}

func (p *Panel) buildHero() fyne.CanvasObject {
	st := p.tn.State()
	d := heroData{
		state:       st,
		failed:      p.connFailed,
		deviceName:  p.rec.NodeName,
		ip:          p.rec.IP,
		cidr:        firewall.VPNSubnet,
		firewallOn:  p.ctrl.FirewallAllowed(),
		showTraffic: st == tunnel.Connected,
	}
	if d.showTraffic {
		rx, tx, _ := p.tn.Transfer()
		d.up, d.down = tx, rx
	}
	refs := heroCard(d, p.onPrimary, p.onFirewallToggle)
	p.heroBtn = refs.button
	p.fwSwitch = refs.toggle
	p.statusDot = refs.statusDot
	p.statusText = refs.statusText
	p.trafficText = refs.traffic
	return refs.object
}

// onPrimary is the single Hero button: disconnect when connected, otherwise connect (FR-006).
func (p *Panel) onPrimary() {
	if p.tn.State() == tunnel.Connected {
		p.onDisconnect()
		return
	}
	p.onConnect()
}

func (p *Panel) onConnect() {
	p.connFailed = false
	p.heroBtn.Disable()
	p.applyStatus(tunnel.Connecting, false)
	go func() {
		err := p.tn.Connect()
		if err == nil {
			_ = p.ctrl.ReconcileFirewall(true) // install the host rule iff opted in
		}
		fyne.Do(func() {
			if err != nil {
				p.connFailed = true
				dialog.ShowError(errors.New(tunnelMessage(err)), p.win)
			}
			p.refreshConnection()
		})
	}()
}

func (p *Panel) onDisconnect() {
	_ = p.tn.Disconnect()
	_ = p.ctrl.ReconcileFirewall(false) // no tunnel → never leave the host rule open
	p.connFailed = false
	p.refreshConnection()
}

// onFirewallToggle persists the inbound preference and reconciles the host rule against the
// current connection state; on failure it reverts the Switch and reports the error (FR-007).
func (p *Panel) onFirewallToggle(on bool) {
	if err := p.ctrl.SetFirewallAllowed(on, p.tn.State() == tunnel.Connected); err != nil {
		dialog.ShowError(errors.New(panelMessage(err)), p.win)
		p.fwSwitch.SetOn(!on)
	}
}

// refreshConnection re-renders the status line and the single button from the current state,
// and starts/stops the traffic poller.
func (p *Panel) refreshConnection() {
	st := p.tn.State()
	p.applyStatus(st, p.connFailed)
	p.heroBtn.SetText(primaryActionLabel(st))
	switch st {
	case tunnel.Connected:
		p.heroBtn.Enable()
		p.startTraffic()
	case tunnel.Connecting:
		p.heroBtn.Disable()
	default:
		p.heroBtn.Enable()
		p.stopTraffic()
	}
}

func (p *Panel) applyStatus(st tunnel.State, failed bool) {
	c, s := statusView(st, failed)
	p.statusDot.FillColor = c
	p.statusDot.Refresh()
	p.statusText.Color = c
	p.statusText.Text = s
	p.statusText.Refresh()
}

// startTraffic begins polling Transfer() every ~2s while connected and updating the Hero ↑/↓
// line; a no-op if already polling (FR-012).
func (p *Panel) startTraffic() {
	if p.trafficQuit != nil {
		return
	}
	q := make(chan struct{})
	p.trafficQuit = q
	p.updateTraffic()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-q:
				return
			case <-t.C:
				fyne.Do(p.updateTraffic)
			}
		}
	}()
}

// stopTraffic halts polling and clears the ↑/↓ line (cumulative counters reset on next connect).
func (p *Panel) stopTraffic() {
	if p.trafficQuit != nil {
		close(p.trafficQuit)
		p.trafficQuit = nil
	}
	p.trafficText.Text = ""
	p.trafficText.Refresh()
}

func (p *Panel) updateTraffic() {
	if p.tn.State() != tunnel.Connected {
		return
	}
	rx, tx, _ := p.tn.Transfer()
	p.trafficText.Text = trafficString(tx, rx)
	p.trafficText.Refresh()
}

// --- Lists ----------------------------------------------------------------------------------

// buildNodeRow renders one node row: avatar + status dot, name, mono IP. Offline rows dim to
// textTertiary and append "N min ago offline"; the this-machine row adds a "this machine" chip
// and a highlight background and is inert (nodes have no detail) (FR-009). Pure for testing.
func buildNodeRow(d panel.DeviceView, now time.Time) fyne.CanvasObject {
	titleColor, subColor := textPrimary, textSecondary
	sub := d.IP
	if !d.Online {
		titleColor, subColor = textTertiary, textTertiary
		sub = d.IP + " · " + offlineSince(d.LastSeen, now)
	}
	title := canvas.NewText(d.Name, titleColor)
	title.TextSize = 14
	subtitle := canvas.NewText(sub, subColor)
	subtitle.TextSize = 12
	subtitle.TextStyle = fyne.TextStyle{Monospace: true}

	var trailing fyne.CanvasObject
	if d.IsThisMachine {
		trailing = container.NewCenter(makeChip(i18n.T("panel.thisMachineTag"), brandCyanChipBg, brandCyanChipText))
	}
	row := container.NewBorder(nil, nil, container.NewCenter(makeAvatar(d.Name, d.Online)), trailing,
		container.NewVBox(title, subtitle))

	var bg color.Color
	if d.IsThisMachine {
		bg = brandCyanFaded
	}
	return newTapRow(container.NewPadded(row), bg, nil) // nodes are never tappable
}

// buildZoneRow renders one zone row (plain avatar + name + optional owner chip); the whole row
// taps through to onTap (the zone detail sheet) (FR-010). Pure for testing.
func buildZoneRow(z panel.ZoneView, onTap func()) fyne.CanvasObject {
	title := canvas.NewText(z.Name, textPrimary)
	title.TextSize = 14
	var trailing fyne.CanvasObject
	if z.IsOwner {
		trailing = container.NewCenter(makeChip(i18n.T("panel.zoneOwnerTag"), brandCyanChipBg, brandCyanChipText))
	}
	row := container.NewBorder(nil, nil, container.NewCenter(makeAvatarPlain(z.Name)), trailing,
		container.NewCenter(title))
	return newTapRow(container.NewPadded(row), nil, onTap)
}

func (p *Panel) buildFAB() fyne.CanvasObject {
	fab := widget.NewButtonWithIcon("", theme.ContentAddIcon(), p.showAddZone)
	fab.Importance = widget.HighImportance
	return fab
}

// bottomRight pins o to the bottom-right corner of whatever area it overlays.
func bottomRight(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), container.NewHBox(layout.NewSpacer(), o))
}

func (p *Panel) showAddZone() {
	create := widget.NewButton(i18n.T("panel.createZone"), nil)
	join := widget.NewButton(i18n.T("panel.joinZone"), nil)
	d := dialog.NewCustom(i18n.T("panel.addZone"), i18n.T("btn.cancel"), container.NewVBox(create, join), p.win)
	create.OnTapped = func() { d.Hide(); p.onCreateZone() }
	join.OnTapped = func() { d.Hide(); p.onJoinZone() }
	d.Show()
}

// --- Session lifecycle (unchanged behavior) -------------------------------------------------

// offerTrust handles a TOFU verification failure during a running session: a first-trust prompt
// names the server and shows the leaf fingerprint; a changed certificate gets a heavier warning.
// On accept it pins the cert, rebuilds the API client to trust it (re-applying the cached token),
// and runs onAccept (typically a retry). The overflow menu reads the live pin, so there is no
// persistent indicator to refresh (FR-004).
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

// confirmLogout names this device + server and the consequences, then on confirmation tears down
// the tunnel, runs the logout, warns if the remote node may linger, and returns to the wizard.
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
	go p.pollLoop()
}

func (p *Panel) pollLoop() {
	for range time.Tick(15 * time.Second) {
		p.refresh()
	}
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
					go p.pollLoop()
				})
			}()
		}, p.win)
	form.Show()
}

// refresh reloads devices + zones, re-renders the lists, and updates the tab counts.
func (p *Panel) refresh() {
	devs, derr := p.ctrl.Devices()
	zones, zerr := p.ctrl.Zones()
	now := time.Now()
	fyne.Do(func() {
		p.refreshConnection()
		if derr == nil {
			p.nodesBox.RemoveAll()
			for _, d := range devs {
				p.nodesBox.Add(buildNodeRow(d, now))
			}
			p.nodesBox.Refresh()
			p.nodesTab.Text = fmt.Sprintf("%s  %d", i18n.T("panel.tabNodes"), len(devs))
		}
		if zerr == nil {
			p.zonesBox.RemoveAll()
			for _, z := range zones {
				p.zonesBox.Add(buildZoneRow(z, p.zoneTapper(z)))
			}
			p.zonesBox.Refresh()
			p.zonesTab.Text = fmt.Sprintf("%s  %d", i18n.T("panel.tabZones"), len(zones))
		}
		p.tabs.Refresh()
	})
}

func (p *Panel) zoneTapper(z panel.ZoneView) func() {
	return func() { p.showZoneDetail(z) }
}

// showZoneDetail opens the zone sheet: leave (everyone), change-password/delete (owner), and the
// member list with kick (owner). All actions reuse the existing controller methods — only the
// trigger location moved here from the old inline buttons (FR-010).
func (p *Panel) showZoneDetail(z panel.ZoneView) {
	go func() {
		members, err := p.ctrl.Members(z.Name)
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(errors.New(panelMessage(err)), p.win)
				return
			}
			actions := container.NewHBox(widget.NewButton(i18n.T("panel.leave"), func() { p.confirmLeave(z) }))
			if z.IsOwner {
				actions.Add(widget.NewButton(i18n.T("panel.changePassword"), func() { p.changePassword(z) }))
				del := widget.NewButton(i18n.T("panel.delete"), func() { p.confirmDelete(z) })
				del.Importance = widget.DangerImportance
				actions.Add(del)
			}
			box := container.NewVBox()
			for _, m := range members {
				row := widget.NewLabel(i18n.T("panel.memberRow", m.NodeName, m.IP, m.Owner))
				if z.IsOwner && m.Owner != "" {
					id, name := m.NodeID, m.NodeName
					kick := widget.NewButton(i18n.T("panel.kick"), func() { p.confirmKick(z, id, name) })
					box.Add(container.NewBorder(nil, nil, row, kick))
				} else {
					box.Add(row)
				}
			}
			content := container.NewBorder(actions, nil, nil, nil, container.NewVScroll(box))
			dialog.ShowCustom(i18n.T("panel.membersTitle", z.Name), i18n.T("btn.close"), content, p.win)
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

// run executes an operation with a progress dialog, then refreshes; errors are shown, and a
// surfaced TOFU CertError offers trust then retries.
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
