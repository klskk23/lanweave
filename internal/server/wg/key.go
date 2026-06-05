package wg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// LoadOrGenerateKey returns the server's WireGuard private key. On first use it
// generates a key and persists it with owner-only (0600) permissions; on every
// later use it loads the existing key and never regenerates. A present but
// corrupt/unreadable key file is a hard error (it is NEVER replaced — silent
// regeneration would rotate the server identity and orphan every client). The
// returned bool reports whether the key was generated this call.
func LoadOrGenerateKey(path string) (wgtypes.Key, bool, error) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		// Tighten broad permissions before reading (FR-002).
		if info.Mode().Perm()&0o077 != 0 {
			if cherr := os.Chmod(path, 0o600); cherr != nil {
				return wgtypes.Key{}, false, fmt.Errorf("key file %q has broad permissions and could not be tightened: %w", path, cherr)
			}
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return wgtypes.Key{}, false, fmt.Errorf("read key file %q: %w", path, rerr)
		}
		key, perr := wgtypes.ParseKey(strings.TrimSpace(string(data)))
		if perr != nil {
			return wgtypes.Key{}, false, fmt.Errorf("server key file %q is corrupt (refusing to regenerate): %w", path, perr)
		}
		return key, false, nil

	case errors.Is(err, fs.ErrNotExist):
		key, gerr := wgtypes.GeneratePrivateKey()
		if gerr != nil {
			return wgtypes.Key{}, false, fmt.Errorf("generate server key: %w", gerr)
		}
		if werr := os.WriteFile(path, []byte(key.String()+"\n"), 0o600); werr != nil {
			return wgtypes.Key{}, false, fmt.Errorf("write key file %q: %w", path, werr)
		}
		return key, true, nil

	default:
		return wgtypes.Key{}, false, fmt.Errorf("stat key file %q: %w", path, err)
	}
}
