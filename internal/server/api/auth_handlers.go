package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/pkg/protocol"
)

const minPasswordLen = 8

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
	writeJSON(w, http.StatusOK, protocol.LoginResponse{Token: token})
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
	case len(req.Password) < minPasswordLen:
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Password must be at least 8 characters.")
		return
	case req.InviteCode == "":
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
