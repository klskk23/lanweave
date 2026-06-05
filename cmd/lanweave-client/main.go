//go:build gui

// Command lanweave-client is the lanweave desktop client (Fyne). On first run it walks the
// user through setup; on later runs it goes straight to the home area.
package main

import (
	"flag"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"lanweave/internal/client/keyring"
	"lanweave/internal/client/onboard"
	"lanweave/internal/client/state"
	"lanweave/internal/client/ui"
)

var version = "dev"

func main() {
	// --insecure skips TLS certificate verification. It is intentionally available only on
	// the command line (for troubleshooting) and never surfaced in the UI.
	insecure := flag.Bool("insecure", false, "skip TLS certificate verification (advanced; not shown in the UI)")
	flag.Parse()

	a := app.NewWithID("com.lanweave.client")
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
		w.SetContent(ui.NewHome(*rec))
	default:
		ui.NewWizard(w, statePath, keys, *insecure).Start()
	}

	w.ShowAndRun()
}
