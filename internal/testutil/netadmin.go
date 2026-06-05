package testutil

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// capNetAdmin is the capability bit for CAP_NET_ADMIN.
const capNetAdmin = 12

// HasNetAdmin reports whether the current process holds CAP_NET_ADMIN in its
// effective capability set (true under root or `unshare -rUn`). It is used to
// decide whether kernel-touching integration tests can run for real.
func HasNetAdmin() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "CapEff:")
		if !ok {
			continue
		}
		eff, err := strconv.ParseUint(strings.TrimSpace(rest), 16, 64)
		if err != nil {
			return false
		}
		return eff&(1<<capNetAdmin) != 0
	}
	return false
}

// RequireNetAdmin skips the test when the process cannot manage network state.
// These integration tests are real (not mocked); run them as root or via
// `unshare -rUn go test ...`.
func RequireNetAdmin(t *testing.T) {
	t.Helper()
	if !HasNetAdmin() {
		t.Skip("requires CAP_NET_ADMIN; run as root or `unshare -rUn`")
	}
}
