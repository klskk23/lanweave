//go:build gui

package ui

import (
	"errors"
	"fmt"
	"image/color"
	"strings"
	"sync"
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

	healthMu    sync.Mutex    // guards healthQuit (start/stop of the health loop)
	healthQuit  chan struct{} // stops the 15s health/auto-reconnect loop; nil when not running
	routesMu    sync.Mutex    // guards routesQuit + reachable (033 consumer routes)
	routesQuit  chan struct{} // stops the 60s consumer-route sync loop; nil when not running
	reachable   []panel.ReachableView
	reachErr    bool       // last sync failed (display "showing last known")
	reconnectMu sync.Mutex // single-flights healthTick so overlapping ticks never stack

	menuBtn   *widget.Button
	nodesBox  *fyne.Container
	zonesBox  *fyne.Container
	routesBox *fyne.Container
	nodesTab  *container.TabItem
	zonesTab  *container.TabItem
	tabs      *container.AppTabs
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
	p.routesBox = container.NewVBox()
	p.nodesTab = container.NewTabItemWithIcon(i18n.T("panel.tabNodes"), theme.ComputerIcon(), container.NewVScroll(p.nodesBox))
	p.zonesTab = container.NewTabItemWithIcon(i18n.T("panel.tabZones"), theme.FolderIcon(), container.NewVScroll(container.NewVBox(p.routesBox, p.zonesBox)))
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
	desired     bool
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

// primaryActionLabel is the single Hero button's label: Disconnect whenever the user wants the
// link up — connected, or desired during the auto-reconnect retry window — otherwise Connect, so
// the same button always aborts an in-progress self-heal (FR-006/FR-014). Pure for unit testing.
func primaryActionLabel(st tunnel.State, desired bool) string {
	if st == tunnel.Connected || desired {
		return i18n.T("panel.disconnect")
	}
	return i18n.T("panel.connect")
}

// statusView derives the status dot/text color and label from three inputs (state, desired,
// failed). Precedence (data-model "优先级"): a live link is green; a connecting link is yellow;
// while disconnected, desired (the auto-reconnect retry window) shows yellow "connecting" and
// wins over failed — red "failed" appears only when the user is NOT trying to connect and the
// last manual attempt failed; otherwise grey "disconnected" (FR-005/FR-009/FR-013/FR-014). Pure.
func statusView(st tunnel.State, desired, failed bool) (color.Color, string) {
	switch st {
	case tunnel.Connected:
		return successColor, i18n.T("status.connected")
	case tunnel.Connecting:
		return warningColor, i18n.T("status.connecting")
	default: // Disconnected
		if desired {
			return warningColor, i18n.T("status.connecting") // retry window — not "failed"
		}
		if failed {
			return dangerColor, i18n.T("status.failed")
		}
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
	dotColor, statusStr := statusView(d.state, d.desired, d.failed)
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

	btn := widget.NewButton(primaryActionLabel(d.state, d.desired), onPrimary)
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
		desired:     p.tn.Desired(),
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

// onPrimary is the single Hero button: disconnect when connected OR while the user wants the link
// up (the auto-reconnect retry window), otherwise connect — so the button also aborts a running
// self-heal (FR-006/FR-014).
func (p *Panel) onPrimary() {
	if p.tn.State() == tunnel.Connected || p.tn.Desired() {
		p.onDisconnect()
		return
	}
	p.onConnect()
}

func (p *Panel) onConnect() {
	p.connFailed = false
	p.heroBtn.Disable()
	p.applyStatus(tunnel.Connecting, false, false)
	go func() {
		err := p.tn.Connect()
		if err == nil {
			p.tn.SetDesired(true)              // record intent ONLY on success → enables self-heal (FR-006)
			_ = p.ctrl.ReconcileFirewall(true) // install the host rule iff opted in
			p.syncRoutesNow()                  // consumer routes right away — no waiting for the 60s tick (033)
		}
		fyne.Do(func() {
			if err != nil {
				p.connFailed = true // a failed manual connect leaves Desired()==false → no retry (FR-009)
				dialog.ShowError(errors.New(tunnelMessage(err)), p.win)
			}
			p.refreshConnection()
		})
	}()
}

func (p *Panel) onDisconnect() {
	p.tn.SetDesired(false) // user intent wins; stops any background auto-reconnect (FR-011)
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
	desired := p.tn.Desired()
	p.applyStatus(st, desired, p.connFailed)
	p.heroBtn.SetText(primaryActionLabel(st, desired))
	switch st {
	case tunnel.Connected:
		p.heroBtn.Enable()
		p.startTraffic()
	case tunnel.Connecting:
		p.heroBtn.Disable()
	default:
		p.heroBtn.Enable() // retry window stays tappable so the user can abort the self-heal
		p.stopTraffic()
	}
}

func (p *Panel) applyStatus(st tunnel.State, desired, failed bool) {
	c, s := statusView(st, desired, failed)
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

// confirmLogout names this device + server and the consequences, then on confirmation runs the
// remote-first hardened logout (slice 025).
func (p *Panel) confirmLogout() {
	msg := i18n.T("panel.logoutConfirm", p.rec.NodeName, p.rec.ServerURL)
	dialog.ShowConfirm(i18n.T("panel.logout"), msg, func(ok bool) {
		if ok {
			p.runLogout()
		}
	}, p.win)
}

// runLogout removes the remote node FIRST (the tunnel stays up — the control API is public HTTPS,
// independent of the tunnel), then branches on the outcome: LogoutBlocked shows the two-button
// blocked prompt with NO local change; LogoutNeedSignIn prompts a fresh sign-in then retries;
// LogoutDone disconnects the tunnel and returns to the wizard (warning if the node may linger).
func (p *Panel) runLogout() {
	prog := dialog.NewCustomWithoutButtons(i18n.T("panel.loggingOut"), widget.NewProgressBarInfinite(), p.win)
	prog.Show()
	go func() {
		outcome, lerr := p.ctrl.Logout()
		if outcome == panel.LogoutDone {
			p.stopHealth()
			p.stopRoutes()
			p.stopRoutes()
			p.tn.SetDesired(false) // logged out → no self-heal of a torn-down tunnel
			_ = p.tn.Disconnect()  // only tear the tunnel down once removal is safely done
		}
		fyne.Do(func() {
			prog.Hide()
			switch outcome {
			case panel.LogoutBlocked:
				p.showLogoutBlocked()
			case panel.LogoutNeedSignIn:
				info := dialog.NewInformation(i18n.T("panel.logout"), i18n.T("panel.logoutNeedSignIn"), p.win)
				info.SetOnClosed(func() { p.promptSignInThen(p.runLogout) })
				info.Show()
			default: // LogoutDone
				if errors.Is(lerr, panel.ErrRemoteMayLinger) {
					dialog.ShowInformation(i18n.T("panel.loggedOutTitle"), i18n.T("panel.logoutRemoteLinger"), p.win)
				} else if lerr != nil {
					dialog.ShowInformation(i18n.T("panel.loggedOutTitle"), i18n.T("panel.logoutPartialFail"), p.win)
				}
				p.restart()
			}
		})
	}()
}

// showLogoutBlocked presents the two-button blocked prompt: Cancel (default — stay signed in, no
// local change) and "Force log out anyway" (the escape hatch, accepting a server-side orphan).
func (p *Panel) showLogoutBlocked() {
	body := widget.NewLabel(i18n.T("panel.logoutBlockedBody"))
	body.Wrapping = fyne.TextWrapWord
	dialog.NewCustomConfirm(
		i18n.T("panel.logoutBlockedTitle"),
		i18n.T("panel.logoutForce"),  // confirm button
		i18n.T("panel.logoutCancel"), // dismiss button (default)
		body,
		func(force bool) {
			if force {
				p.runForceLogout()
			}
		},
		p.win,
	).Show()
}

// runForceLogout tears down the tunnel and does the unconditional full local teardown, returning
// to the wizard and accepting a server-side orphaned node.
func (p *Panel) runForceLogout() {
	prog := dialog.NewCustomWithoutButtons(i18n.T("panel.loggingOut"), widget.NewProgressBarInfinite(), p.win)
	prog.Show()
	go func() {
		p.stopHealth()
		p.tn.SetDesired(false)
		_ = p.tn.Disconnect()
		lerr := p.ctrl.ForceLogout()
		fyne.Do(func() {
			prog.Hide()
			if lerr != nil {
				dialog.ShowInformation(i18n.T("panel.loggedOutTitle"), i18n.T("panel.logoutPartialFail"), p.win)
			}
			p.restart()
		})
	}()
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
	p.startHealth()
	p.startRoutes()
}

func (p *Panel) pollLoop() {
	for range time.Tick(15 * time.Second) {
		p.refresh()
	}
}

// startHealth launches the auto-reconnect health loop if it is not already running. Idempotent
// (start() may run several times — e.g. a TOFU retry), so the guard keeps it a single goroutine.
func (p *Panel) startHealth() {
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	if p.healthQuit != nil {
		return
	}
	q := make(chan struct{})
	p.healthQuit = q
	go p.healthLoop(q)
}

// stopHealth tears the health loop down (logout paths), so a logged-out session never tries to
// auto-reconnect a torn-down tunnel.
func (p *Panel) stopHealth() {
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	if p.healthQuit != nil {
		close(p.healthQuit)
		p.healthQuit = nil
	}
}

// healthLoop is the auto-reconnect heartbeat: a dedicated 15s ticker, decoupled from pollLoop so
// self-heal never blocks the node-list refresh (FR-004/FR-005). It exits when quit is closed.
func (p *Panel) healthLoop(quit chan struct{}) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
			p.healthTick(time.Now())
		}
	}
}

// healthTick performs one self-heal pass. It acts only when the user wants the link up
// (Desired) and the tunnel is either silently dead (Stale) or already down — i.e. inside the
// retry window. It re-runs the same connect+firewall path as a manual connect but FULLY SILENT
// (no dialogs, FR-015), mirroring the firewall to the live state (closed during the retry window,
// FR-016). A fixed 15s cadence with no backoff (FR-010); overlapping ticks are single-flighted so
// a slow attempt never stacks. A manual Disconnect (Desired→false + teardown) makes the in-flight
// Connect abandon via its engine-identity guard, so the user always wins (FR-011/FR-012).
func (p *Panel) healthTick(now time.Time) {
	if !p.tn.Desired() {
		return
	}
	if p.tn.State() == tunnel.Connected && !p.tn.Stale(now) {
		return // healthy link — nothing to do (and no false reconnect, SC-002)
	}
	if !p.reconnectMu.TryLock() {
		return // a reconnect is already in flight
	}
	defer p.reconnectMu.Unlock()

	// A stale-but-"Connected" tunnel must be torn down first, else Connect() is a no-op ("one
	// tunnel only"). Closing it also drops the firewall for the retry window (FR-016).
	if p.tn.State() != tunnel.Disconnected {
		_ = p.tn.Disconnect()
		_ = p.ctrl.ReconcileFirewall(false)
	}
	fyne.Do(p.refreshConnection) // show yellow "connecting" promptly (FR-013), silently

	err := p.tn.Connect()
	if err == nil && p.tn.Desired() {
		_ = p.ctrl.ReconcileFirewall(true)
	} else {
		_ = p.ctrl.ReconcileFirewall(false) // failed, or the user disconnected mid-attempt
	}
	fyne.Do(p.refreshConnection)
}

// promptSignIn is the session-start re-auth: on success it refreshes the view and starts polling.
func (p *Panel) promptSignIn() {
	p.promptSignInThen(func() {
		p.refresh()
		go p.pollLoop()
		p.startHealth()
	})
}

// promptSignInThen shows the username/password form and runs onSuccess after a successful sign-in,
// re-prompting on a bad credential and aborting (no-op) on cancel. The continuation is
// parameterized so the same dialog serves both session-start (refresh + poll) and the logout
// retry (re-invoke runLogout) paths (slice 025 T018).
func (p *Panel) promptSignInThen(onSuccess func()) {
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
						p.promptSignInThen(onSuccess)
						return
					}
					onSuccess()
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
	case errors.Is(err, apiclient.ErrOwnedZoneLimitReached):
		return i18n.T("panel.errZoneLimit")
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

// startRoutes launches the consumer-route sync loop (feature 033): while
// connected, the reachable announced subnets of this account's zones are
// fetched and applied to the tunnel every minute (plus immediately after a
// successful connect). Idempotent like startHealth.
func (p *Panel) startRoutes() {
	p.routesMu.Lock()
	defer p.routesMu.Unlock()
	if p.routesQuit != nil {
		return
	}
	q := make(chan struct{})
	p.routesQuit = q
	go p.routesLoop(q)
}

func (p *Panel) stopRoutes() {
	p.routesMu.Lock()
	defer p.routesMu.Unlock()
	if p.routesQuit != nil {
		close(p.routesQuit)
		p.routesQuit = nil
	}
}

func (p *Panel) routesLoop(quit chan struct{}) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
			p.syncRoutesNow()
		}
	}
}

// syncRoutesNow performs one consumer-route reconcile pass. Disconnected →
// the display empties (routes died with the interface); an API failure keeps
// the last known list with a stale note (routes frozen, FR-003).
func (p *Panel) syncRoutesNow() {
	if p.tn.State() != tunnel.Connected {
		p.routesMu.Lock()
		changed := len(p.reachable) > 0 || p.reachErr
		p.reachable, p.reachErr = nil, false
		p.routesMu.Unlock()
		if changed {
			fyne.Do(p.refreshRoutes)
		}
		return
	}
	views, err := p.ctrl.SyncRoutes(p.tn)
	p.routesMu.Lock()
	if err != nil {
		p.reachErr = true
	} else {
		p.reachable, p.reachErr = views, false
	}
	p.routesMu.Unlock()
	fyne.Do(p.refreshRoutes)
}

// refreshRoutes renders the "reachable announced subnets" block at the top of
// the zones tab (hidden when empty).
func (p *Panel) refreshRoutes() {
	p.routesMu.Lock()
	views := append([]panel.ReachableView(nil), p.reachable...)
	stale := p.reachErr
	p.routesMu.Unlock()

	p.routesBox.RemoveAll()
	if len(views) > 0 || stale {
		p.routesBox.Add(widget.NewLabelWithStyle(i18n.T("routes.title"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		if stale {
			p.routesBox.Add(coloredText(i18n.T("routes.stale"), textSecondary))
		}
		for _, v := range views {
			line := fmt.Sprintf("%s  ←  %s   ·  %s  ·  %s", v.Synthetic, v.Subnet, strings.Join(v.Zones, ","), v.Announcer)
			if v.Conflict {
				p.routesBox.Add(coloredText(line+"   "+i18n.T("routes.conflict"), dangerColor))
			} else {
				p.routesBox.Add(widget.NewLabel(line))
			}
		}
	}
	p.routesBox.Refresh()
}
