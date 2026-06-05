// Package netfw manages host IP forwarding and the nftables isolation table.
package netfw

import (
	"fmt"
	"os"
)

const ipForwardPath = "/proc/sys/net/ipv4/ip_forward"

// EnableIPv4Forward turns on host IPv4 packet forwarding so the relay can forward
// traffic between clients. It is idempotent and is deliberately never reverted on
// shutdown (avoids disrupting other host services).
func EnableIPv4Forward() error {
	if err := os.WriteFile(ipForwardPath, []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("enable ipv4 forwarding: %w", err)
	}
	return nil
}
