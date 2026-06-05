package wgkey_test

import (
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/client/wgkey"
)

func TestGenerateKeyPair(t *testing.T) {
	priv, pub, err := wgkey.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if priv == "" || pub == "" {
		t.Fatal("keys must not be empty")
	}
	k, err := wgtypes.ParseKey(priv)
	if err != nil {
		t.Fatalf("private key is not a valid WireGuard key: %v", err)
	}
	if k.PublicKey().String() != pub {
		t.Error("returned public key does not match the private key")
	}

	priv2, _, err := wgkey.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if priv2 == priv {
		t.Error("two generations must produce distinct keys")
	}
}
