// Package packaging holds the deployment artifacts (the Debian `.deb` definition, the
// systemd unit, the maintainer scripts, and the Windows installer script) plus a
// host-gated test that builds and inspects the real server `.deb`. There is no production
// Go code here; this file exists only so `go build ./...` treats the directory as a
// package.
package packaging
