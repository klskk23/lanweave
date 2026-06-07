package api_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"lanweave/pkg/protocol"
)

// login returns both the access token and the refresh token from a successful login.
func (h *harness) login(username, password string) protocol.LoginResponse {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/v1/login", "", protocol.LoginRequest{Username: username, Password: password})
	if rec.Code != http.StatusOK {
		h.t.Fatalf("login(%s) status %d: %s", username, rec.Code, rec.Body.String())
	}
	var resp protocol.LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		h.t.Fatalf("login decode: %v", err)
	}
	return resp
}

// accessTokenLifetimeSec returns exp−iat (seconds) of an access JWT, parsed without
// verification — enough to assert the TTL is unchanged by the refresh feature.
func accessTokenLifetimeSec(t *testing.T, token string) float64 {
	t.Helper()
	var claims jwt.RegisteredClaims
	if _, _, err := jwt.NewParser().ParseUnverified(token, &claims); err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		t.Fatalf("access token missing iat/exp")
	}
	return claims.ExpiresAt.Sub(claims.IssuedAt.Time).Seconds()
}

// TestLoginReturnsRefreshToken covers US1: /login returns a non-empty refresh token
// alongside the access token.
func TestLoginReturnsRefreshToken(t *testing.T) {
	h := newHarness(t)
	resp := h.login("admin", h.adminPW)
	if resp.Token == "" {
		t.Error("login returned an empty access token")
	}
	if resp.RefreshToken == "" {
		t.Error("login returned an empty refresh token")
	}
}

// TestRefreshEndpoint covers US1: a valid RT exchanges for a fresh access token that
// authorizes /me; unknown/malformed RTs are rejected.
func TestRefreshEndpoint(t *testing.T) {
	h := newHarness(t)
	resp := h.login("admin", h.adminPW)

	// Valid RT → 200 {token}, and that token authorizes /me.
	rec := h.do(http.MethodPost, "/api/v1/refresh", "", protocol.RefreshRequest{RefreshToken: resp.RefreshToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh valid: status %d (%s)", rec.Code, rec.Body.String())
	}
	var rr protocol.RefreshResponse
	decodeJSONBody(t, rec.Body.Bytes(), &rr)
	if rr.Token == "" {
		t.Fatal("refresh returned an empty access token")
	}
	if me := h.do(http.MethodGet, "/api/v1/me", rr.Token, nil); me.Code != http.StatusOK {
		t.Errorf("refreshed token does not authorize /me: status %d", me.Code)
	}

	// Unknown RT → 401.
	if rec := h.do(http.MethodPost, "/api/v1/refresh", "", protocol.RefreshRequest{RefreshToken: "never-issued"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("refresh unknown: status %d, want 401", rec.Code)
	}
	// Missing/empty field → 400.
	if rec := h.do(http.MethodPost, "/api/v1/refresh", "", protocol.RefreshRequest{RefreshToken: ""}); rec.Code != http.StatusBadRequest {
		t.Errorf("refresh empty: status %d, want 400", rec.Code)
	}
}

// TestRefreshKeepsAccessTTL covers M1 / FR-003: the refresh feature must not alter the
// access JWT lifetime. The token minted by /refresh has the same exp−iat as /login's.
func TestRefreshKeepsAccessTTL(t *testing.T) {
	h := newHarness(t)
	resp := h.login("admin", h.adminPW)

	rec := h.do(http.MethodPost, "/api/v1/refresh", "", protocol.RefreshRequest{RefreshToken: resp.RefreshToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: status %d (%s)", rec.Code, rec.Body.String())
	}
	var rr protocol.RefreshResponse
	decodeJSONBody(t, rec.Body.Bytes(), &rr)

	loginTTL := accessTokenLifetimeSec(t, resp.Token)
	refreshTTL := accessTokenLifetimeSec(t, rr.Token)
	if math.Abs(loginTTL-refreshTTL) > 2 {
		t.Errorf("access TTL changed across refresh: login=%.0fs refresh=%.0fs", loginTTL, refreshTTL)
	}
}

// TestRefreshNoSecretInLogs covers the security gate: no plaintext refresh token
// reaches the server logs across a login+refresh cycle.
func TestRefreshNoSecretInLogs(t *testing.T) {
	h := newHarness(t)
	resp := h.login("admin", h.adminPW)
	h.do(http.MethodPost, "/api/v1/refresh", "", protocol.RefreshRequest{RefreshToken: resp.RefreshToken})

	if logs := h.logBuf.String(); strings.Contains(logs, resp.RefreshToken) {
		t.Error("plaintext refresh token leaked into server logs")
	}
}

// TestLogoutRevokesRefreshToken covers US2: /logout with a valid RT returns 204 and the
// RT can no longer be refreshed; /logout is idempotent for unknown tokens and rejects a
// missing field. It is never an oracle for token state (unknown still returns 204).
func TestLogoutRevokesRefreshToken(t *testing.T) {
	h := newHarness(t)
	resp := h.login("admin", h.adminPW)

	// Valid RT → 204.
	if rec := h.do(http.MethodPost, "/api/v1/logout", "", protocol.LogoutRequest{RefreshToken: resp.RefreshToken}); rec.Code != http.StatusNoContent {
		t.Fatalf("logout valid: status %d (%s)", rec.Code, rec.Body.String())
	}
	// The revoked RT can no longer refresh.
	if rec := h.do(http.MethodPost, "/api/v1/refresh", "", protocol.RefreshRequest{RefreshToken: resp.RefreshToken}); rec.Code != http.StatusUnauthorized {
		t.Errorf("refresh after logout: status %d, want 401", rec.Code)
	}
	// Idempotent: an unknown RT still returns 204 (never an oracle).
	if rec := h.do(http.MethodPost, "/api/v1/logout", "", protocol.LogoutRequest{RefreshToken: "never-issued"}); rec.Code != http.StatusNoContent {
		t.Errorf("logout unknown: status %d, want 204", rec.Code)
	}
	// Missing field → 400.
	if rec := h.do(http.MethodPost, "/api/v1/logout", "", protocol.LogoutRequest{RefreshToken: ""}); rec.Code != http.StatusBadRequest {
		t.Errorf("logout empty: status %d, want 400", rec.Code)
	}
}

// TestDeleteUserRevokesRefreshTokens covers US2 / FR-009: deleting a user cascades away
// their refresh tokens, so a deleted user's RT can no longer mint access tokens.
func TestDeleteUserRevokesRefreshTokens(t *testing.T) {
	h := newHarness(t)
	adminToken := h.loginToken("admin", h.adminPW)
	code := h.createInviteCode(adminToken)
	const bobPw = "bobs-strong-pw"
	if rec := h.do(http.MethodPost, "/api/v1/register", "", protocol.RegisterRequest{
		InviteCode: code, Username: "bob", Password: bobPw,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("register bob: %d", rec.Code)
	}
	bob := h.login("bob", bobPw)

	// Bob learns his own id via /me, then the admin deletes him.
	meRec := h.do(http.MethodGet, "/api/v1/me", bob.Token, nil)
	var me protocol.MeResponse
	decodeJSONBody(t, meRec.Body.Bytes(), &me)
	if del := h.do(http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatInt(me.UserID, 10), adminToken, nil); del.Code != http.StatusOK && del.Code != http.StatusNoContent {
		t.Fatalf("delete bob: status %d (%s)", del.Code, del.Body.String())
	}

	// Bob's RT no longer refreshes (rows cascaded away).
	if rec := h.do(http.MethodPost, "/api/v1/refresh", "", protocol.RefreshRequest{RefreshToken: bob.RefreshToken}); rec.Code != http.StatusUnauthorized {
		t.Errorf("refresh after user delete: status %d, want 401", rec.Code)
	}
}

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

// TestRegisterExpiredCodeIndistinguishable — an expired invite code is rejected at
// registration with a response byte-identical to the unknown-code rejection: the
// registrant cannot tell expiry apart from any other invalid code (FR-003 / SC-005).
func TestRegisterExpiredCodeIndistinguishable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	admin, err := h.store.Users().GetByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("get admin: %v", err)
	}
	// Seed an unused code already past its expiry (past-dated row, no sleep).
	now := time.Now().UTC().Format(time.RFC3339)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := h.store.DB().ExecContext(ctx,
		`INSERT INTO invites (code, created_by_user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		"expired-code", admin.ID, now, past); err != nil {
		t.Fatalf("seed expired invite: %v", err)
	}

	expired := h.do(http.MethodPost, "/api/v1/register", "", protocol.RegisterRequest{
		InviteCode: "expired-code", Username: "bob", Password: "bobs-strong-pw",
	})
	unknown := h.do(http.MethodPost, "/api/v1/register", "", protocol.RegisterRequest{
		InviteCode: "no-such-code", Username: "carol", Password: "carols-strong-pw",
	})

	if expired.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expired status %d, want 422 (%s)", expired.Code, expired.Body.String())
	}
	if expired.Code != unknown.Code {
		t.Errorf("expired status %d != unknown status %d", expired.Code, unknown.Code)
	}
	if expired.Body.String() != unknown.Body.String() {
		t.Errorf("expired body %q must equal unknown body %q", expired.Body.String(), unknown.Body.String())
	}
	if got := decodeError(t, expired).Error; got != "invite_invalid" {
		t.Errorf("expired error %q, want invite_invalid", got)
	}
	if u, _ := h.store.Users().GetByUsername(ctx, "bob"); u != nil {
		t.Error("expired code must not create an account")
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
