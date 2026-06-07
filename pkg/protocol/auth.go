package protocol

// LoginRequest is the body of POST /api/v1/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse carries the issued session token plus the long-lived refresh
// token used to silently renew the session without re-entering the password.
type LoginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
}

// RefreshRequest is the body of POST /api/v1/refresh. It carries the opaque
// refresh token; the (expired) access token does not gate this endpoint.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshResponse carries a freshly minted access token.
type RefreshResponse struct {
	Token string `json:"token"`
}

// LogoutRequest is the body of POST /api/v1/logout. It carries the refresh
// token to revoke; revocation is idempotent.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RegisterRequest is the body of POST /api/v1/register.
type RegisterRequest struct {
	InviteCode string `json:"invite_code"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

// RegisterResponse describes the created account.
type RegisterResponse struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

// MeResponse is the body of GET /api/v1/me.
type MeResponse struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

// CreateInviteResponse carries a freshly minted invite code.
type CreateInviteResponse struct {
	Code string `json:"code"`
}

// InviteListItem is one row of GET /api/v1/admin/invites.
type InviteListItem struct {
	Code      string  `json:"code"`
	Status    string  `json:"status"` // "unused" | "used"
	CreatedBy *string `json:"created_by"`
	CreatedAt string  `json:"created_at"`
	UsedBy    *string `json:"used_by"`
	UsedAt    *string `json:"used_at"`
}

// InviteListResponse wraps the invite list.
type InviteListResponse struct {
	Invites []InviteListItem `json:"invites"`
}
