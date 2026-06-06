// Package winelevate ensures the Windows desktop client runs with the administrator rights
// required to create the WinTun network adapter. At startup it checks whether the process is
// already elevated and, if not, relaunches itself through the operating system's elevation
// prompt, preserving the original arguments. On non-Windows platforms every entry point is a
// no-op, so headless and Linux GUI builds are unaffected.
package winelevate

import "strings"

// commandLine joins process arguments into a single command-line string, quoting any argument
// that contains whitespace or a double quote (escaping embedded quotes). It is the pure,
// platform-independent core used to carry the user's arguments across an elevated relaunch.
func commandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quoteArg(a)
	}
	return strings.Join(quoted, " ")
}

func quoteArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\"") {
		return arg
	}
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}
