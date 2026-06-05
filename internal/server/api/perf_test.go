package api_test

import (
	"net/http"
	"testing"
	"time"
)

// TestLatencyBudgets is a coarse guard for constitution §IV: /me must answer from
// token claims with no DB read (≤100 ms), and login (one argon2id verify) must
// stay within the write budget (≤300 ms). Measured in-process, so these are
// generous ceilings that catch gross regressions (e.g., an accidental DB read in /me).
func TestLatencyBudgets(t *testing.T) {
	h := newHarness(t)
	token := h.loginToken("admin", h.adminPW)

	// /me: median of several calls well under 100 ms.
	const n = 20
	var maxMe time.Duration
	for range n {
		start := time.Now()
		rec := h.do(http.MethodGet, "/api/v1/me", token, nil)
		d := time.Since(start)
		if rec.Code != http.StatusOK {
			t.Fatalf("/me status %d", rec.Code)
		}
		if d > maxMe {
			maxMe = d
		}
	}
	if maxMe > 100*time.Millisecond {
		t.Errorf("/me worst-case %v exceeds 100ms budget", maxMe)
	}

	// login: single argon2id verify under the 300 ms write budget.
	start := time.Now()
	_ = h.loginToken("admin", h.adminPW)
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("login %v exceeds 300ms budget", d)
	}
}
