//go:build !windows

package winelevate

// EnsureElevated is a no-op on non-Windows platforms; only the Windows client needs the
// administrator rights required to create the privileged network adapter.
func EnsureElevated() {}
