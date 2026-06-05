package auth_test

import (
	"strings"
	"testing"

	"lanweave/internal/server/auth"
)

func TestHashPasswordFormat(t *testing.T) {
	h, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("unexpected PHC format: %q", h)
	}
	if strings.Contains(h, "hunter2") {
		t.Fatalf("hash leaks plaintext: %q", h)
	}
}

func TestHashPasswordRandomSalt(t *testing.T) {
	h1, _ := auth.HashPassword("samepw")
	h2, _ := auth.HashPassword("samepw")
	if h1 == h2 {
		t.Fatal("two hashes of the same password are identical; salt is not random")
	}
}

func TestVerifyPassword(t *testing.T) {
	h, err := auth.HashPassword("correct horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := auth.VerifyPassword("correct horse", h)
	if err != nil || !ok {
		t.Fatalf("verify correct: ok=%v err=%v", ok, err)
	}
	bad, err := auth.VerifyPassword("wrong", h)
	if err != nil {
		t.Fatalf("verify wrong errored: %v", err)
	}
	if bad {
		t.Fatal("verify accepted a wrong password")
	}
}

func TestVerifyPasswordBadFormat(t *testing.T) {
	if _, err := auth.VerifyPassword("x", "not-a-phc-string"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
}
