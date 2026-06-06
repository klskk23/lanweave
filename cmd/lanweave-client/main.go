//go:build gui

// Command lanweave-client is the lanweave desktop client (Fyne). On first run it walks the
// user through setup; on later runs it goes straight to the home area.
package main

import (
	"flag"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"lanweave/internal/client/apiclient"
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

	switch target, rec := onboard.StartupTarget(statePath); target {
	case onboard.Home:
		priv, _ := keys.Get(keyring.DeviceKeyName)
		tn := tunnel.New(*rec, string(priv))
		defer tn.Close() // tear the tunnel down cleanly on exit (no orphan adapter)
		var opts []apiclient.Option
		if *insecure {
			opts = append(opts, apiclient.WithInsecure())
		}
		ctrl := panel.New(apiclient.New(rec.ServerURL, opts...), *rec, keys, statePath, *insecure)
		w.SetContent(ui.NewPanel(w, *rec, tn, ctrl, func() {
			ui.NewWizard(w, statePath, keys, *insecure).Start()
		}))
	default:
		ui.NewWizard(w, statePath, keys, *insecure).Start()
	}

	w.ShowAndRun()
}
