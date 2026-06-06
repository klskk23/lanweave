package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"lanweave/internal/server/auth"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestIssueVerifyRoundTrip(t *testing.T) {
	m := auth.NewJWTManager(testSecret, time.Hour)
	tok, err := m.Issue(auth.Claims{UserID: 7, Username: "alice", IsAdmin: true})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	got, err := m.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.UserID != 7 || got.Username != "alice" || !got.IsAdmin {
		t.Fatalf("claims mismatch: %+v", got)
	}
}

func TestVerifyRejectsTampered(t *testing.T) {
	m := auth.NewJWTManager(testSecret, time.Hour)
	tok, _ := m.Issue(auth.Claims{UserID: 1, Username: "bob"})
	// Flip the FIRST character of the signature. (The final base64url char of an
	// HMAC-SHA256 signature carries unused low bits, so flipping the last char can
	// leave the decoded signature bytes unchanged — a flaky no-op tamper.)
	i := strings.LastIndexByte(tok, '.')
	tampered := tok[:i+1] + flip(tok[i+1:i+2]) + tok[i+2:]
	if _, err := m.Verify(tampered); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	m := auth.NewJWTManager(testSecret, -time.Minute) // already expired
	tok, _ := m.Issue(auth.Claims{UserID: 1, Username: "bob"})
	if _, err := m.Verify(tok); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestVerifyRejectsWrongAlg(t *testing.T) {
	// A token with alg=none must be rejected by the HS256 pin.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{Subject: "1"})
	raw, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build none token: %v", err)
	}
	m := auth.NewJWTManager(testSecret, time.Hour)
	if _, err := m.Verify(raw); err == nil {
		t.Fatal("expected alg=none token to be rejected")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	signer := auth.NewJWTManager(testSecret, time.Hour)
	tok, _ := signer.Issue(auth.Claims{UserID: 1, Username: "bob"})
	other := auth.NewJWTManager("ffffffffffffffffffffffffffffffff", time.Hour)
	if _, err := other.Verify(tok); err == nil {
		t.Fatal("expected token signed with a different secret to be rejected (rotation invalidates)")
	}
}

func flip(s string) string {
	if s == "x" {
		return "y"
	}
	return "x"
}
