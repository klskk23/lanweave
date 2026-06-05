package netfw_test

import (
	"os"
	"strings"
	"testing"

	"lanweave/internal/server/netfw"
	"lanweave/internal/testutil"
)

// TestEnableIPv4Forward is a privileged integration test. Under `unshare -rUn`
// the namespace has its own ip_forward; this writes and reads it for real.
func TestEnableIPv4Forward(t *testing.T) {
	testutil.RequireNetAdmin(t)

	if err := netfw.EnableIPv4Forward(); err != nil {
		t.Fatalf("enable: %v (rootless runs may need `unshare -rUn --mount-proc`)", err)
	}
	// Idempotent second call.
	if err := netfw.EnableIPv4Forward(); err != nil {
		t.Fatalf("enable (2nd): %v", err)
	}

	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		t.Fatalf("read ip_forward: %v", err)
	}
	if strings.TrimSpace(string(data)) != "1" {
		t.Errorf("ip_forward = %q, want 1", strings.TrimSpace(string(data)))
	}
}
