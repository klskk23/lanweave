//go:build windows

package winelevate

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnsureElevated makes sure the client runs with administrator rights, which Windows requires
// to create the WinTun adapter. If the process is already elevated it returns immediately — that
// guard is also what prevents an infinite relaunch loop. Otherwise it relaunches the same
// executable through the OS elevation prompt (the "runas" verb), preserving the original
// arguments, and exits the unelevated process so only the elevated instance continues. If the
// user declines the prompt (or the relaunch fails) it shows one message and exits without
// opening the UI, so the app never presents a misleading state.
func EnsureElevated() {
	if windows.GetCurrentProcessToken().IsElevated() {
		return // already elevated: do not prompt or relaunch (loop break)
	}

	exe, err := os.Executable()
	if err != nil {
		failElevation("lanweave could not determine its program path and needs administrator rights to run.")
		return
	}

	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	var args *uint16
	if line := commandLine(os.Args[1:]); line != "" {
		args, _ = windows.UTF16PtrFromString(line)
	}

	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		failElevation("lanweave needs administrator rights to create the network adapter and will now close.")
		return
	}
	os.Exit(0) // the elevated instance takes over
}

// failElevation shows a single native message and exits non-zero. It is used when elevation is
// declined or cannot be requested, so the user gets an understandable outcome and the normal
// (unprivileged, non-functional) UI is never shown.
func failElevation(msg string) {
	text, _ := windows.UTF16PtrFromString(msg)
	caption, _ := windows.UTF16PtrFromString("lanweave")
	_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
	os.Exit(1)
}
