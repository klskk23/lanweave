// Package wgkey generates the device's WireGuard key pair on the client. The private
// key is stored in the OS secure store; only the public key is sent to the server.
package wgkey

import (
	"fmt"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// GenerateKeyPair returns a new key pair as base64 strings: the private key (to be kept
// in the OS secure store) and the corresponding public key (to be uploaded to the server).
func GenerateKeyPair() (privateKey, publicKey string, err error) {
	priv, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return "", "", fmt.Errorf("generate device key: %w", err)
	}
	return priv.String(), priv.PublicKey().String(), nil
}
