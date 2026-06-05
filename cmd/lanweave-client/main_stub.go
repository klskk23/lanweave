//go:build !gui

// Command lanweave-client is the lanweave desktop client. The GUI is built with the "gui"
// build tag and a desktop/GL toolchain. This untagged stub keeps the command buildable on
// headless hosts (where only the Fyne-free client core is compiled and tested) and prints
// how to build the real app.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "lanweave-client: build the desktop UI with -tags gui (requires the Fyne/GL toolchain).")
	os.Exit(1)
}
