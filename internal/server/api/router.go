// Package api builds the HTTP routing and middleware for the control plane.
package api

import (
	"log/slog"
	"net/http"

	"golang.org/x/time/rate"

	"lanweave/internal/server/auth"
	"lanweave/internal/server/store"
	"lanweave/pkg/protocol"
)

// Options configures the router.
type Options struct {
	Version string
	Limiter *rate.Limiter
	Logger  *slog.Logger
	Store   *store.Store
	JWT     *auth.JWTManager
}

// NewRouter returns the fully wrapped handler. Middleware order, outermost first:
// RequestLogger -> Recoverer -> RateLimit -> mux. Protected routes additionally
// opt into AuthRequired (and AdminRequired) wrappers.
func NewRouter(opts Options) http.Handler {
	h := &handlers{store: opts.Store, jwt: opts.JWT, log: opts.Logger, version: opts.Version}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/healthz", healthz(opts.Version))

	// Public (rate-limited) endpoints.
	mux.HandleFunc("POST /api/v1/login", h.login)
	mux.HandleFunc("POST /api/v1/register", h.register)

	// Authenticated endpoints.
	mux.Handle("GET /api/v1/me", AuthRequired(opts.JWT)(http.HandlerFunc(h.me)))

	// Admin-only endpoints.
	mux.Handle("POST /api/v1/admin/invites",
		AuthRequired(opts.JWT)(AdminRequired()(http.HandlerFunc(h.createInvite))))
	mux.Handle("GET /api/v1/admin/invites",
		AuthRequired(opts.JWT)(AdminRequired()(http.HandlerFunc(h.listInvites))))

	mux.HandleFunc("/", notFound)

	var handler http.Handler = mux
	handler = RateLimit(opts.Limiter)(handler)
	handler = Recoverer(opts.Logger)(handler)
	handler = RequestLogger(opts.Logger)(handler)
	return handler
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
}
