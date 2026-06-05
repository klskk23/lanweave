package auth_test

import (
	"testing"

	"lanweave/internal/server/auth"
)

// DummyVerify must run without panicking and without revealing anything; it is
// used purely to equalize timing on the unknown-user login path.
func TestDummyVerifyDoesNotPanic(t *testing.T) {
	auth.DummyVerify("anything")
	auth.DummyVerify("")
}
