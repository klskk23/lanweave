// Package keyring stores the device private key in the operating system's secure
// credential store. The private key never lives in a plaintext file. Backends: Windows
// Credential Manager / DPAPI (production), a non-Windows dev backend, and an in-memory
// fake for tests.
package keyring

import "errors"

// ErrNotFound is returned by Get when no secret is stored under the name.
var ErrNotFound = errors.New("secret not found")

// Store is a secure secret store keyed by name.
type Store interface {
	Set(name string, secret []byte) error
	Get(name string) ([]byte, error)
	Delete(name string) error
}

// DeviceKeyName is the fixed name under which the device private key is stored (one
// machine = one device).
const DeviceKeyName = "lanweave-device-private-key"

// SessionTokenName is the fixed name under which the user's session token is cached, so
// the management panel can reuse it across launches without re-prompting (DESIGN §8).
const SessionTokenName = "lanweave-session-token"

// RefreshTokenName is the fixed name under which the long-lived refresh token is cached
// (same DPAPI-backed store as the session token). It lets the client silently renew an
// expired access token without re-prompting for the password (slice 024).
const RefreshTokenName = "lanweave-refresh-token"
