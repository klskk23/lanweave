package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/pkg/passwordpolicy"
	"lanweave/pkg/protocol"
)

// passwordPolicyMessage maps a passwordpolicy.Reason to the English, API-facing
// rejection message. Related reasons are deliberately collapsed into a single
// phrase here; precise per-rule guidance is the client's job (it renders the
// typed reason via i18n). See specs/027-password-complexity/contracts/register.md.
func passwordPolicyMessage(reason passwordpolicy.Reason) string {
	switch reason {
	case passwordpolicy.ReasonCharset:
		return "Password may only contain ASCII letters, digits, and symbols (no spaces)."
	case passwordpolicy.ReasonTooShort, passwordpolicy.ReasonTooLong:
		return "Password must be 8-64 characters."
	default: // ReasonNoUpper, ReasonNoLower, ReasonNoDigit
		return "Password must include an uppercase letter, a lowercase letter, and a digit."
	}
}

// handlers holds the dependencies shared by the control-plane endpoints.
type handlers struct {
	store    *store.Store
	jwt      *auth.JWTManager
	log      *slog.Logger
	version  string
	wg       *wg.Server
	netfw    *netfw.Manager
	wgConfig config.WireGuardConfig
	status   statusProvider
	// Resolved per-user caps (0 = unlimited). registerNode/createZone pass these to
	// the store, substituting 0 for an admin caller so admin reuses the unlimited path.
	maxDevicesPerUser    int
	maxOwnedZonesPerUser int
	// announcePool / maxAnnouncedSubnetsPerUser configure subnet announcements
	// (feature 030); an invalid pool disables the feature.
	announcePool               netip.Prefix
	maxAnnouncedSubnetsPerUser int
	// inviteTTL is the window stamped onto codes minted via createInvite
	// (0 = never expire).
	inviteTTL time.Duration
}

func (h *handlers) serverError(w http.ResponseWriter, err error) {
	h.log.Error("handler error", "error", err.Error())
	protocol.WriteJSONError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
}

// login verifies credentials and issues a session token. Wrong password and
// unknown username return an identical 401 (no user enumeration); the unknown
// path still runs a dummy verify to equalize timing.
func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	var req protocol.LoginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	if req.Username == "" || req.Password == "" {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Username and password are required.")
		return
	}

	u, err := h.store.Users().GetByUsername(r.Context(), req.Username)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if u == nil {
		auth.DummyVerify(req.Password)
		protocol.WriteJSONError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password.")
		return
	}
	ok, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password.")
		return
	}

	token, err := h.jwt.Issue(auth.Claims{UserID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin})
	if err != nil {
		h.serverError(w, err)
		return
	}
	refreshToken, err := h.store.RefreshTokens().Issue(r.Context(), u.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.LoginResponse{Token: token, RefreshToken: refreshToken})
}

// refresh exchanges a valid refresh token for a fresh access token, sliding the
// refresh token's expiry. It is a public route authenticated by the RT in the body
// (the access JWT is, by definition, expired at refresh time), never by the JWT.
// The plaintext RT is never logged.
func (h *handlers) refresh(w http.ResponseWriter, r *http.Request) {
	var req protocol.RefreshRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	if req.RefreshToken == "" {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "A refresh token is required.")
		return
	}

	userID, err := h.store.RefreshTokens().Validate(r.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, store.ErrRefreshInvalid) {
			protocol.WriteJSONError(w, http.StatusUnauthorized, "invalid_refresh_token", "Refresh token is invalid or expired.")
			return
		}
		h.serverError(w, err)
		return
	}

	u, err := h.store.Users().GetByID(r.Context(), userID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if u == nil {
		// The owning user vanished between validate and lookup (e.g. deleted). Treat
		// as an invalid refresh rather than minting a token for a ghost account.
		protocol.WriteJSONError(w, http.StatusUnauthorized, "invalid_refresh_token", "Refresh token is invalid or expired.")
		return
	}

	token, err := h.jwt.Issue(auth.Claims{UserID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin})
	if err != nil {
		h.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.RefreshResponse{Token: token})
}

// logout revokes a refresh token server-side. It is a public route (the access JWT may
// already be expired) authenticated by the RT in the body. It always returns 204 for a
// well-formed request — revoking an unknown or already-revoked token is a no-op — so it
// is never an oracle for token state. A malformed/oversized body or missing field is 400.
func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	var req protocol.LogoutRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	if req.RefreshToken == "" {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "A refresh token is required.")
		return
	}
	if err := h.store.RefreshTokens().Revoke(r.Context(), req.RefreshToken); err != nil {
		h.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// me returns the authenticated identity straight from the verified token claims.
func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	writeJSON(w, http.StatusOK, protocol.MeResponse{
		UserID:   id.UserID,
		Username: id.Username,
		IsAdmin:  id.IsAdmin,
	})
}

// register creates a non-admin account by consuming a one-time invite code.
func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	var req protocol.RegisterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	username := strings.TrimSpace(req.Username)
	switch {
	case username == "" || len(username) > 64:
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Username must be 1-64 characters.")
		return
	}
	if reason, ok := passwordpolicy.Validate(req.Password); !ok {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", passwordPolicyMessage(reason))
		return
	}
	if req.InviteCode == "" {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "An invite code is required.")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.serverError(w, err)
		return
	}
	u, err := h.store.Register(r.Context(), username, hash, req.InviteCode)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInviteInvalid):
			protocol.WriteJSONError(w, http.StatusUnprocessableEntity, "invite_invalid", "Invite code is invalid or already used.")
		case errors.Is(err, store.ErrUserExists):
			protocol.WriteJSONError(w, http.StatusConflict, "username_taken", "That username is already taken.")
		default:
			h.serverError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, protocol.RegisterResponse{Username: u.Username, IsAdmin: u.IsAdmin})
}
