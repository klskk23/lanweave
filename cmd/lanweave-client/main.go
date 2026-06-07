//go:build gui

// Command lanweave-client is the lanweave desktop client (Fyne). On first run it walks the
// user through setup; on later runs it goes straight to the home area.
package main

import (
	"flag"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/lang"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/firewall"
	"lanweave/internal/client/i18n"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/onboard"
	"lanweave/internal/client/panel"
	"lanweave/internal/client/state"
	"lanweave/internal/client/tunnel"
	"lanweave/internal/client/ui"
	"lanweave/internal/client/winelevate"
)

var version = "dev"

func main() {
	// On Windows the client must run as administrator to create the WinTun adapter. When
	// launched unelevated it relaunches itself through the UAC prompt and exits; on other
	// platforms this is a no-op. Done before flag parsing so the original arguments are passed
	// through to the elevated instance.
	winelevate.EnsureElevated()

	// --insecure skips TLS certificate verification. It is intentionally available only on
	// the command line (for troubleshooting) and never surfaced in the UI.
	insecure := flag.Bool("insecure", false, "skip TLS certificate verification (advanced; not shown in the UI)")
	flag.Parse()

	a := app.NewWithID("com.lanweave.client")
	a.Settings().SetTheme(ui.NewTheme()) // forced-dark brand theme (feature 022)
	// Pick the UI language before building any view: a saved preference (set via the in-app
	// language selector) wins; an empty preference follows the system locale. Must run before
	// the first NewWizard/NewPanel and is independent of the local state file.
	i18n.Init(a.Preferences().StringWithFallback("ui.language", ""), string(lang.SystemLocale()))
	a.SetIcon(ui.AppIcon())
	w := a.NewWindow("lanweave " + version)
	w.Resize(fyne.NewSize(440, 380))

	statePath, err := state.DefaultPath()
	if err != nil {
		log.Fatalf("locate state path: %v", err)
	}
	keys, err := keyring.Open()
	if err != nil {
		log.Fatalf("open secure store: %v", err)
	}

	// Startup orphan sweep: a previous run that crashed while connected may have left the host
	// inbound-allow rule installed. Remove any such rule before building the UI so the firewall
	// only ever opens under an active, opted-in session (best-effort; absent rule is fine).
	_ = firewall.Clear()

	switch target, rec := onboard.StartupTarget(statePath); target {
	case onboard.Home:
		priv, _ := keys.Get(keyring.DeviceKeyName)
		tn := tunnel.New(*rec, string(priv))
		defer tn.Close()       // tear the tunnel down cleanly on exit (no orphan adapter)
		defer firewall.Clear() // close the host rule on exit (no orphan firewall opening)
		var opts []apiclient.Option
		switch {
		case *insecure:
			opts = append(opts, apiclient.WithInsecure())
		case rec.PinnedCertSHA256 != "":
			opts = append(opts, apiclient.WithPinnedCert(rec.PinnedCertSHA256))
		}
		ctrl := panel.New(apiclient.New(rec.ServerURL, opts...), *rec, keys, statePath, *insecure, firewall.System())
		w.SetContent(ui.NewPanel(w, *rec, tn, ctrl, func() {
			ui.NewWizard(w, statePath, keys, *insecure).Start()
		}))
	default:
		ui.NewWizard(w, statePath, keys, *insecure).Start()
	}

	w.ShowAndRun()
}
