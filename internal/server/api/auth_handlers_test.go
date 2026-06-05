package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"lanweave/pkg/protocol"
)

// US1 — login + /me + token rejection + no enumeration.
func TestLoginAndMe(t *testing.T) {
	h := newHarness(t)

	token := h.loginToken("admin", h.adminPW)
	if token == "" {
		t.Fatal("empty token")
	}

	rec := h.do(http.MethodGet, "/api/v1/me", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/me status %d", rec.Code)
	}
	var me protocol.MeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Username != "admin" || !me.IsAdmin || me.UserID == 0 {
		t.Fatalf("unexpected /me: %+v", me)
	}
}

func TestMeRejectsBadTokens(t *testing.T) {
	h := newHarness(t)

	if rec := h.do(http.MethodGet, "/api/v1/me", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/api/v1/me", "not.a.jwt", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("garbage token: status %d, want 401", rec.Code)
	}
}

func TestLoginNoEnumeration(t *testing.T) {
	h := newHarness(t)

	wrongPw := h.do(http.MethodPost, "/api/v1/login", "", protocol.LoginRequest{Username: "admin", Password: "wrong"})
	unknown := h.do(http.MethodPost, "/api/v1/login", "", protocol.LoginRequest{Username: "ghost", Password: "whatever"})

	if wrongPw.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("expected both 401, got wrong=%d unknown=%d", wrongPw.Code, unknown.Code)
	}
	if wrongPw.Body.String() != unknown.Body.String() {
		t.Errorf("responses differ (enumeration leak):\n wrong=%s\n unknown=%s", wrongPw.Body, unknown.Body)
	}
	if decodeError(t, wrongPw).Error != "invalid_credentials" {
		t.Errorf("expected invalid_credentials")
	}
}

// US3 — invite-gated registration.
func TestRegisterFlow(t *testing.T) {
	h := newHarness(t)
	adminToken := h.loginToken("admin", h.adminPW)
	code := h.createInviteCode(adminToken)

	// Register with the code.
	rec := h.do(http.MethodPost, "/api/v1/register", "", protocol.RegisterRequest{
		InviteCode: code, Username: "bob", Password: "bobs-strong-pw",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status %d: %s", rec.Code, rec.Body.String())
	}
	var reg protocol.RegisterResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &reg)
	if reg.Username != "bob" || reg.IsAdmin {
		t.Fatalf("unexpected register response: %+v", reg)
	}

	// New user can log in and is not admin.
	bobToken := h.loginToken("bob", "bobs-strong-pw")
	meRec := h.do(http.MethodGet, "/api/v1/me", bobToken, nil)
	var me protocol.MeResponse
	_ = json.Unmarshal(meRec.Body.Bytes(), &me)
	if me.Username != "bob" || me.IsAdmin {
		t.Fatalf("bob should be non-admin: %+v", me)
	}
}

func TestRegisterRejections(t *testing.T) {
	h := newHarness(t)
	adminToken := h.loginToken("admin", h.adminPW)
	code := h.createInviteCode(adminToken)

	// Consume the code once.
	if rec := h.do(http.MethodPost, "/api/v1/register", "", protocol.RegisterRequest{
		InviteCode: code, Username: "bob", Password: "bobs-strong-pw",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("setup register failed: %d", rec.Code)
	}

	cases := []struct {
		name     string
		req      protocol.RegisterRequest
		wantCode int
		wantErr  string
	}{
		{"reused code", protocol.RegisterRequest{InviteCode: code, Username: "carol", Password: "carols-strong-pw"}, http.StatusUnprocessableEntity, "invite_invalid"},
		{"unknown code", protocol.RegisterRequest{InviteCode: "nope", Username: "dave", Password: "daves-strong-pw"}, http.StatusUnprocessableEntity, "invite_invalid"},
		{"missing code", protocol.RegisterRequest{Username: "erin", Password: "erins-strong-pw"}, http.StatusBadRequest, "validation_error"},
		{"short password", protocol.RegisterRequest{InviteCode: code, Username: "frank", Password: "short"}, http.StatusBadRequest, "validation_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(http.MethodPost, "/api/v1/register", "", tc.req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d, want %d (%s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if got := decodeError(t, rec).Error; got != tc.wantErr {
				t.Errorf("error %q, want %q", got, tc.wantErr)
			}
		})
	}

	// Taken username with a FRESH code → 409, fresh code stays unused.
	code2 := h.createInviteCode(adminToken)
	rec := h.do(http.MethodPost, "/api/v1/register", "", protocol.RegisterRequest{
		InviteCode: code2, Username: "bob", Password: "another-strong-pw",
	})
	if rec.Code != http.StatusConflict || decodeError(t, rec).Error != "username_taken" {
		t.Fatalf("taken username: status %d body %s", rec.Code, rec.Body.String())
	}
}

// US4 — no secret reaches the logs across a register+login cycle.
func TestNoSecretsInLogs(t *testing.T) {
	h := newHarness(t)
	adminToken := h.loginToken("admin", h.adminPW)
	code := h.createInviteCode(adminToken)
	const bobPw = "bobs-very-secret-pw"
	h.do(http.MethodPost, "/api/v1/register", "", protocol.RegisterRequest{
		InviteCode: code, Username: "bob", Password: bobPw,
	})
	bobToken := h.loginToken("bob", bobPw)

	logs := h.logBuf.String()
	for _, secret := range []string{bobPw, h.adminPW, code, bobToken, adminToken} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Errorf("secret leaked into logs: %q", secret)
		}
	}
}
