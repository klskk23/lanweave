package api

import (
	"context"
	"net/http"
	"strings"

	"lanweave/internal/server/auth"
	"lanweave/pkg/protocol"
)

type ctxKey struct{}

var identityKey ctxKey

// IdentityFrom returns the authenticated identity placed in the context by
// AuthRequired, or false if none is present.
func IdentityFrom(ctx context.Context) (*auth.Claims, bool) {
	c, ok := ctx.Value(identityKey).(*auth.Claims)
	return c, ok
}

// AuthRequired verifies the Bearer token and stores the identity in the request
// context. Missing, malformed, expired, or invalid tokens get 401.
func AuthRequired(jwt *auth.JWTManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			const prefix = "Bearer "
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, prefix) {
				protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
				return
			}
			claims, err := jwt.Verify(strings.TrimSpace(authz[len(prefix):]))
			if err != nil {
				protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
				return
			}
			ctx := context.WithValue(r.Context(), identityKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminRequired allows only callers whose verified identity is an administrator.
// It must be applied inside AuthRequired (which populates the identity).
func AdminRequired() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := IdentityFrom(r.Context())
			if !ok {
				protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
				return
			}
			if !claims.IsAdmin {
				protocol.WriteJSONError(w, http.StatusForbidden, "forbidden", "Administrator privileges required.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
